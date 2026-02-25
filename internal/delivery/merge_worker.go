package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type MergeWorkerOptions struct {
	Pool     *pgxpool.Pool
	Git      GitService
	Events   eventbus.EventBus
	Enqueuer jobEnqueuer
	Clock    clock.Clock
	Logger   *slog.Logger
}

type MergeWorker struct {
	pool     *pgxpool.Pool
	git      GitService
	events   eventbus.EventBus
	enqueuer jobEnqueuer
	clock    clock.Clock
	logger   *slog.Logger

	queue    *repo.MergeQueueEntryRepo
	projects *repo.ProjectRepo
	remotes  *repo.ProjectRemoteRepo
	users    *repo.HumanUserRepo
	inbox    *repo.InboxItemRepo
}

func NewMergeWorker(opts MergeWorkerOptions) (*MergeWorker, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("merge worker requires a database pool")
	}
	if opts.Git == nil {
		opts.Git = UnavailableGitService{}
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Enqueuer == nil {
		opts.Enqueuer = jobqueue.New(opts.Pool, opts.Logger, jobqueue.Config{})
	}

	return &MergeWorker{
		pool:     opts.Pool,
		git:      opts.Git,
		events:   opts.Events,
		enqueuer: opts.Enqueuer,
		clock:    opts.Clock,
		logger:   opts.Logger,
		queue:    repo.NewMergeQueueEntryRepo(opts.Pool),
		projects: repo.NewProjectRepo(opts.Pool),
		remotes:  repo.NewProjectRemoteRepo(opts.Pool),
		users:    repo.NewHumanUserRepo(opts.Pool),
		inbox:    repo.NewInboxItemRepo(opts.Pool),
	}, nil
}

func (w *MergeWorker) Execute(ctx context.Context, job jobqueue.Job) error {
	if w == nil {
		return fmt.Errorf("merge worker is not configured")
	}

	var payload mergeExecutePayload
	if err := decodePayload(job.Payload, &payload); err != nil {
		return err
	}
	if payload.MergeQueueEntryID == uuid.Nil {
		return fmt.Errorf("merge_queue_entry_id is required")
	}
	if payload.ProjectID == uuid.Nil {
		return fmt.Errorf("project_id is required")
	}

	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin merge tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var hasLock bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1)::bigint)`, advisoryLockKey(payload.ProjectID)).Scan(&hasLock); err != nil {
		return fmt.Errorf("acquire project advisory lock: %w", err)
	}
	if !hasLock {
		runAfter := w.clock.Now().UTC().Add(mergeRetryDelay)
		_, enqueueErr := w.enqueuer.Enqueue(ctx, nil, MergeExecuteJobType, deliveryDefaultPriority, payload, &runAfter)
		return enqueueErr
	}

	entry, err := w.loadMergeQueueEntryForUpdate(ctx, tx, payload.MergeQueueEntryID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if payload.ProjectID != uuid.Nil && entry.ProjectID != payload.ProjectID {
		return fmt.Errorf("merge queue entry project mismatch")
	}
	if entry.ArchivedAt != nil || entry.Status == "merged" {
		w.logger.Info("merge entry already handled", "merge_queue_entry_id", entry.ID, "status", entry.Status)
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return nil
	}
	if entry.Status != "queued" {
		w.logger.Info("merge entry not in queued state", "merge_queue_entry_id", entry.ID, "status", entry.Status)
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return nil
	}

	projectRecord, err := w.projects.GetByID(ctx, entry.ProjectID)
	if err != nil {
		return err
	}
	if err := w.git.Merge(ctx, entry.ProjectID, entry.BranchName, entry.TargetBranch); err != nil {
		if errors.Is(err, ErrMergeConflict) {
			if err := w.markMergeFailed(ctx, tx, entry.ID, "merge conflict"); err != nil {
				return err
			}
			if err := w.createMergeConflictInbox(ctx, projectRecord, entry, err); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			return nil
		}
		return err
	}

	now := w.clock.Now().UTC()
	if err := w.markMerged(ctx, tx, entry.ID, now); err != nil {
		return err
	}

	remoteRecord, err := w.remotes.GetDefault(ctx, entry.ProjectID)
	if err != nil {
		return err
	}
	pushPayload := pushJobPayload{
		ProjectID:         entry.ProjectID,
		RemoteID:          remoteRecord.ID,
		CommitSHA:         stringValue(entry.CommitSHA),
		Attempt:           1,
		TriggeredByTaskID: entry.TaskID,
		DeliveryMode:      projectRecord.DeliveryMode,
		MergeQueueEntryID: entry.ID,
	}
	if _, err := w.enqueuer.Enqueue(ctx, tx, pushExecuteJobType, deliveryDefaultPriority, pushPayload, nil); err != nil {
		return err
	}

	if w.events != nil {
		eventPayload, err := json.Marshal(map[string]any{
			"merge_queue_entry_id": entry.ID,
			"project_id":           entry.ProjectID,
			"branch_name":          entry.BranchName,
		})
		if err != nil {
			return err
		}
		if err := w.events.Publish(ctx, tx, eventbus.DomainEvent{
			OrganizationID: projectRecord.OrganizationID,
			EventType:      "task.merged",
			ActorType:      deliverySystemActorType,
			Payload:        eventPayload,
		}); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func (w *MergeWorker) loadMergeQueueEntryForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (repo.MergeQueueEntry, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
		FROM merge_queue_entry
		WHERE id = $1
		FOR UPDATE
	`, id)

	var entry repo.MergeQueueEntry
	if err := row.Scan(
		&entry.ID,
		&entry.ProjectID,
		&entry.TaskID,
		&entry.BranchName,
		&entry.TargetBranch,
		&entry.CommitSHA,
		&entry.Position,
		&entry.Status,
		&entry.EnqueuedAt,
		&entry.MergedAt,
		&entry.ArchivedAt,
		&entry.FailureReason,
		&entry.Metadata,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repo.MergeQueueEntry{}, repo.ErrNotFound
		}
		return repo.MergeQueueEntry{}, err
	}
	return entry, nil
}

func (w *MergeWorker) markMerged(ctx context.Context, tx pgx.Tx, entryID uuid.UUID, mergedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE merge_queue_entry
		SET
			status = 'merged',
			merged_at = $2,
			failure_reason = NULL
		WHERE id = $1
	`, entryID, mergedAt)
	return err
}

func (w *MergeWorker) markMergeFailed(ctx context.Context, tx pgx.Tx, entryID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE merge_queue_entry
		SET
			status = 'failed',
			failure_reason = $2
		WHERE id = $1
	`, entryID, strings.TrimSpace(reason))
	return err
}

func (w *MergeWorker) createMergeConflictInbox(ctx context.Context, projectRecord repo.Project, entry repo.MergeQueueEntry, cause error) error {
	targetUserID, err := resolveAdminRecipient(ctx, w.users, projectRecord.OrganizationID)
	if err != nil {
		return err
	}

	reason := "merge conflict"
	if cause != nil {
		reason = strings.TrimSpace(cause.Error())
	}
	payload, err := json.Marshal(map[string]any{
		"requested_type":       "merge_conflict",
		"merge_queue_entry_id": entry.ID,
		"project_id":           projectRecord.ID,
		"task_id":              entry.TaskID,
		"branch_name":          entry.BranchName,
		"reason":               reason,
	})
	if err != nil {
		return err
	}
	body := "Merge failed due to conflicts and needs PM review."
	item := repo.InboxItem{
		OrganizationID:  projectRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "merge_conflict",
		SourceProjectID: &projectRecord.ID,
		SourceTaskID:    &entry.TaskID,
		CreatedByType:   deliverySystemActorType,
		Title:           "Merge conflict detected",
		Body:            &body,
		ActionPayload:   payload,
	}
	if _, err := w.inbox.Create(ctx, item); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			item.ItemType = deliverySystemAlertType
			_, err = w.inbox.Create(ctx, item)
		}
		return err
	}
	return nil
}

func resolveAdminRecipient(ctx context.Context, users *repo.HumanUserRepo, organizationID uuid.UUID) (*uuid.UUID, error) {
	if users == nil {
		return nil, nil
	}
	items, err := users.List(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if !item.IsActive {
			continue
		}
		if item.Role == "owner" || item.Role == "admin" {
			id := item.ID
			return &id, nil
		}
	}
	return nil, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
