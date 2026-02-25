package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type EnvUpdaterOptions struct {
	Pool   *pgxpool.Pool
	Events eventbus.EventBus
	Clock  clock.Clock
}

type EnvUpdater struct {
	pool   *pgxpool.Pool
	events eventbus.EventBus
	clock  clock.Clock

	tasks interface {
		GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
	}
	executions interface {
		ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error)
	}

	applyCompletion func(ctx context.Context, update deployCompletionUpdate) error
}

type deployCompletionUpdate struct {
	OrganizationID    uuid.UUID
	ProjectID         uuid.UUID
	DeployTaskID      uuid.UUID
	CommitSHA         string
	CompletedAt       time.Time
	MergeEntryIDs     []uuid.UUID
	TriggeredByTaskID uuid.UUID
}

func NewEnvUpdater(opts EnvUpdaterOptions) (*EnvUpdater, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("env updater requires a database pool")
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	updater := &EnvUpdater{
		pool:       opts.Pool,
		events:     opts.Events,
		clock:      opts.Clock,
		tasks:      repo.NewProjectTaskRepo(opts.Pool),
		executions: repo.NewFlowNodeExecutionRepo(opts.Pool),
	}
	updater.applyCompletion = updater.applyCompletionTx
	return updater, nil
}

func (u *EnvUpdater) OnDeployCompleted(ctx context.Context, deployTaskID uuid.UUID) error {
	if u == nil {
		return fmt.Errorf("env updater is not configured")
	}
	if deployTaskID == uuid.Nil {
		return fmt.Errorf("deployTaskID is required")
	}

	taskRecord, err := u.tasks.GetByID(ctx, deployTaskID)
	if err != nil {
		return err
	}
	if !taskMetadataTaskType(taskRecord.Metadata, deliveryTaskTypeDeploy) {
		return nil
	}

	commitSHA := extractCommitSHA(taskRecord.Metadata)
	if commitSHA == "" {
		commitSHA = u.latestExecutionCommit(ctx, deployTaskID)
	}

	now := u.clock.Now().UTC()
	update := deployCompletionUpdate{
		OrganizationID:    taskRecord.OrganizationID,
		ProjectID:         taskRecord.ProjectID,
		DeployTaskID:      deployTaskID,
		CommitSHA:         commitSHA,
		CompletedAt:       now,
		MergeEntryIDs:     extractMergeQueueEntryIDs(taskRecord.Metadata),
		TriggeredByTaskID: extractTriggeredByTaskID(taskRecord.Metadata),
	}
	apply := u.applyCompletion
	if apply == nil {
		apply = u.applyCompletionTx
	}
	return apply(ctx, update)
}

func (u *EnvUpdater) applyCompletionTx(ctx context.Context, update deployCompletionUpdate) error {
	tx, err := u.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
		UPDATE project_environment
		SET
			last_deployed_commit = NULLIF($2, ''),
			last_deployed_at = $3,
			deploy_task_id = $4
		WHERE project_id = $1
		  AND is_active = true
	`, update.ProjectID, update.CommitSHA, update.CompletedAt, update.DeployTaskID); err != nil {
		return err
	}

	if len(update.MergeEntryIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE merge_queue_entry
			SET archived_at = $2
			WHERE id = ANY($1)
			  AND archived_at IS NULL
		`, update.MergeEntryIDs, update.CompletedAt); err != nil {
			return err
		}
	}
	if update.TriggeredByTaskID != uuid.Nil {
		if _, err := tx.Exec(ctx, `
			UPDATE merge_queue_entry
			SET archived_at = $2
			WHERE task_id = $1
			  AND archived_at IS NULL
		`, update.TriggeredByTaskID, update.CompletedAt); err != nil {
			return err
		}
	}
	if len(update.MergeEntryIDs) == 0 && update.TriggeredByTaskID == uuid.Nil {
		if _, err := tx.Exec(ctx, `
			UPDATE merge_queue_entry
			SET archived_at = $2
			WHERE project_id = $1
			  AND archived_at IS NULL
		`, update.ProjectID, update.CompletedAt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE merge_queue_entry
		SET archived_at = $2
		WHERE task_id = $1
		  AND archived_at IS NULL
	`, update.DeployTaskID, update.CompletedAt); err != nil {
		return err
	}

	if u.events != nil {
		payload, err := json.Marshal(map[string]any{
			"project_id":     update.ProjectID,
			"commit_sha":     update.CommitSHA,
			"deploy_task_id": update.DeployTaskID,
		})
		if err != nil {
			return err
		}
		if err := u.events.Publish(ctx, tx, eventbus.DomainEvent{
			OrganizationID: update.OrganizationID,
			EventType:      "project.deployed",
			ActorType:      deliverySystemActorType,
			Payload:        payload,
		}); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (u *EnvUpdater) latestExecutionCommit(ctx context.Context, taskID uuid.UUID) string {
	executions, err := u.executions.ListByTask(ctx, taskID)
	if err != nil {
		return ""
	}
	for i := len(executions) - 1; i >= 0; i-- {
		if executions[i].CommitSHA == nil {
			continue
		}
		if trimmed := strings.TrimSpace(*executions[i].CommitSHA); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func taskMetadataTaskType(metadata json.RawMessage, expected string) bool {
	if len(metadata) == 0 {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal(metadata, &parsed); err != nil {
		return false
	}
	value, _ := parsed["task_type"].(string)
	return strings.TrimSpace(value) == expected
}

func extractCommitSHA(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(metadata, &parsed); err != nil {
		return ""
	}
	if direct, ok := parsed["commit_sha"].(string); ok {
		if trimmed := strings.TrimSpace(direct); trimmed != "" {
			return trimmed
		}
	}
	if direct, ok := parsed["target_commit_sha"].(string); ok {
		if trimmed := strings.TrimSpace(direct); trimmed != "" {
			return trimmed
		}
	}
	deliveryBlock, _ := parsed["delivery"].(map[string]any)
	if deliveryBlock == nil {
		return ""
	}
	if fromDelivery, ok := deliveryBlock["commit_sha"].(string); ok {
		return strings.TrimSpace(fromDelivery)
	}
	return ""
}

func extractMergeQueueEntryIDs(metadata json.RawMessage) []uuid.UUID {
	if len(metadata) == 0 {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(metadata, &parsed); err != nil {
		return nil
	}
	result := make([]uuid.UUID, 0, 2)
	appendUUID := func(raw string) {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err == nil && id != uuid.Nil {
			result = append(result, id)
		}
	}
	if raw, ok := parsed["merge_queue_entry_id"].(string); ok {
		appendUUID(raw)
	}
	deliveryBlock, _ := parsed["delivery"].(map[string]any)
	if deliveryBlock != nil {
		if raw, ok := deliveryBlock["merge_queue_entry_id"].(string); ok {
			appendUUID(raw)
		}
		if many, ok := deliveryBlock["merge_queue_entry_ids"].([]any); ok {
			for _, raw := range many {
				asString, _ := raw.(string)
				appendUUID(asString)
			}
		}
	}
	return result
}

func extractTriggeredByTaskID(metadata json.RawMessage) uuid.UUID {
	if len(metadata) == 0 {
		return uuid.Nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(metadata, &parsed); err != nil {
		return uuid.Nil
	}
	if raw, ok := parsed["triggered_by_task_id"].(string); ok {
		if id, err := uuid.Parse(strings.TrimSpace(raw)); err == nil {
			return id
		}
	}
	deliveryBlock, _ := parsed["delivery"].(map[string]any)
	if deliveryBlock == nil {
		return uuid.Nil
	}
	if raw, ok := deliveryBlock["triggered_by_task_id"].(string); ok {
		if id, err := uuid.Parse(strings.TrimSpace(raw)); err == nil {
			return id
		}
	}
	return uuid.Nil
}
