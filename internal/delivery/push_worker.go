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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type PushWorkerOptions struct {
	Pool     *pgxpool.Pool
	Git      GitService
	Events   eventbus.EventBus
	Enqueuer jobEnqueuer
	Clock    clock.Clock
	Logger   *slog.Logger
}

type PushWorker struct {
	pool     *pgxpool.Pool
	git      GitService
	events   eventbus.EventBus
	enqueuer jobEnqueuer
	clock    clock.Clock
	logger   *slog.Logger

	projects     *repo.ProjectRepo
	remotes      *repo.ProjectRemoteRepo
	environments *repo.ProjectEnvironmentRepo
	taskEvents   *repo.ProjectTaskEventRepo
	inbox        *repo.InboxItemRepo
	users        *repo.HumanUserRepo

	jitter func(time.Duration) time.Duration
}

func NewPushWorker(opts PushWorkerOptions) (*PushWorker, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("push worker requires a database pool")
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
	return &PushWorker{
		pool:         opts.Pool,
		git:          opts.Git,
		events:       opts.Events,
		enqueuer:     opts.Enqueuer,
		clock:        opts.Clock,
		logger:       opts.Logger,
		projects:     repo.NewProjectRepo(opts.Pool),
		remotes:      repo.NewProjectRemoteRepo(opts.Pool),
		environments: repo.NewProjectEnvironmentRepo(opts.Pool),
		taskEvents:   repo.NewProjectTaskEventRepo(opts.Pool),
		inbox:        repo.NewInboxItemRepo(opts.Pool),
		users:        repo.NewHumanUserRepo(opts.Pool),
		jitter:       addJitter,
	}, nil
}

func (w *PushWorker) Execute(ctx context.Context, job jobqueue.Job) error {
	if w == nil {
		return fmt.Errorf("push worker is not configured")
	}

	var payload pushJobPayload
	if err := decodePayload(job.Payload, &payload); err != nil {
		return err
	}
	if payload.ProjectID == uuid.Nil {
		return fmt.Errorf("project_id is required")
	}
	if payload.Attempt <= 0 {
		payload.Attempt = 1
	}

	projectRecord, err := w.projects.GetByID(ctx, payload.ProjectID)
	if err != nil {
		return err
	}
	remoteRecord, err := w.resolveRemote(ctx, payload)
	if err != nil {
		return err
	}

	if err := w.git.Push(ctx, remoteRecord); err != nil {
		if payload.Attempt >= maxPushAttempts {
			return w.handleFinalFailure(ctx, projectRecord, remoteRecord, payload, err)
		}
		retryDelay := computePushRetryDelay(payload.Attempt)
		if w.jitter != nil {
			retryDelay = w.jitter(retryDelay)
		}
		runAfter := w.clock.Now().UTC().Add(retryDelay)
		payload.Attempt++
		_, enqueueErr := w.enqueuer.Enqueue(ctx, nil, pushExecuteJobType, deliveryDefaultPriority, payload, &runAfter)
		return enqueueErr
	}

	if err := w.publishPushEvent(ctx, projectRecord.OrganizationID, "project.push_succeeded", payload, remoteRecord.ID, ""); err != nil {
		return err
	}

	mode := strings.TrimSpace(payload.DeliveryMode)
	if mode == "" {
		mode = strings.TrimSpace(projectRecord.DeliveryMode)
	}
	deployPayload := deployTaskCreatePayload{
		ProjectID:         payload.ProjectID,
		CommitSHA:         trimSHA(payload.CommitSHA),
		TriggeredByTaskID: payload.TriggeredByTaskID,
		DeliveryMode:      mode,
		MergeQueueEntryID: payload.MergeQueueEntryID,
		RemoteID:          remoteRecord.ID,
	}
	_, err = w.enqueuer.Enqueue(ctx, nil, DeployTaskCreateJobType, deliveryDefaultPriority, deployPayload, nil)
	return err
}

func (w *PushWorker) resolveRemote(ctx context.Context, payload pushJobPayload) (repo.ProjectRemote, error) {
	if payload.RemoteID != uuid.Nil {
		return w.remotes.GetByID(ctx, payload.RemoteID)
	}
	if payload.EnvironmentID != uuid.Nil {
		env, err := w.environments.GetByID(ctx, payload.EnvironmentID)
		if err != nil {
			return repo.ProjectRemote{}, err
		}
		if env.RemoteID != nil {
			return w.remotes.GetByID(ctx, *env.RemoteID)
		}
	}
	return w.remotes.GetDefault(ctx, payload.ProjectID)
}

func (w *PushWorker) handleFinalFailure(ctx context.Context, projectRecord repo.Project, remoteRecord repo.ProjectRemote, payload pushJobPayload, pushErr error) error {
	reason := strings.TrimSpace(pushErr.Error())
	if reason == "" {
		reason = "push failed"
	}

	deployTaskID := payload.DeployTaskID
	if deployTaskID == uuid.Nil {
		if resolved := w.resolveDeployTaskID(ctx, payload.ProjectID); resolved != nil {
			deployTaskID = *resolved
		}
	}
	if deployTaskID != uuid.Nil {
		taskPayload, err := json.Marshal(map[string]any{
			"error":      reason,
			"attempt":    payload.Attempt,
			"commit_sha": trimSHA(payload.CommitSHA),
		})
		if err != nil {
			return err
		}
		if _, err := w.taskEvents.Record(ctx, repo.ProjectTaskEvent{
			TaskID:    deployTaskID,
			ProjectID: payload.ProjectID,
			EventType: "push_failed",
			ActorType: deliverySystemActorType,
			Payload:   taskPayload,
		}); err != nil {
			return err
		}
	}

	targetUserID, err := resolveAdminRecipient(ctx, w.users, projectRecord.OrganizationID)
	if err != nil {
		return err
	}
	body := "Push to remote failed after 3 retries"
	alertPayload, err := json.Marshal(map[string]any{
		"requested_type": "escalation",
		"urgency":        deliveryUrgencyUrgent,
		"project_id":     payload.ProjectID,
		"remote_id":      remoteRecord.ID,
		"commit_sha":     trimSHA(payload.CommitSHA),
		"attempt":        payload.Attempt,
		"error":          reason,
	})
	if err != nil {
		return err
	}
	item := repo.InboxItem{
		OrganizationID:  projectRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "escalation",
		SourceProjectID: &payload.ProjectID,
		SourceTaskID:    optionalUUIDPointer(deployTaskID),
		CreatedByType:   deliverySystemActorType,
		Title:           "Push failed",
		Body:            &body,
		ActionPayload:   alertPayload,
	}
	if _, err := w.inbox.Create(ctx, item); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			item.ItemType = deliverySystemAlertType
			if _, fallbackErr := w.inbox.Create(ctx, item); fallbackErr != nil {
				return fallbackErr
			}
		} else {
			return err
		}
	}

	return w.publishPushEvent(ctx, projectRecord.OrganizationID, "project.push_failed", payload, remoteRecord.ID, reason)
}

func (w *PushWorker) resolveDeployTaskID(ctx context.Context, projectID uuid.UUID) *uuid.UUID {
	envs, err := w.environments.ListByProject(ctx, projectID)
	if err != nil {
		return nil
	}
	for _, env := range envs {
		if env.DeployTaskID != nil {
			id := *env.DeployTaskID
			return &id
		}
	}
	return nil
}

func (w *PushWorker) publishPushEvent(ctx context.Context, organizationID uuid.UUID, eventType string, payload pushJobPayload, remoteID uuid.UUID, failureReason string) error {
	if w.events == nil {
		return nil
	}
	message := map[string]any{
		"project_id": payload.ProjectID,
		"remote_id":  remoteID,
		"commit_sha": trimSHA(payload.CommitSHA),
	}
	if failureReason != "" {
		message["error"] = failureReason
		message["attempt"] = payload.Attempt
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return w.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      eventType,
		ActorType:      deliverySystemActorType,
		Payload:        encoded,
	})
}

func optionalUUIDPointer(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	copyID := id
	return &copyID
}
