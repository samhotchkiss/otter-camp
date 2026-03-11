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
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

type DeployWorkerOptions struct {
	Pool   *pgxpool.Pool
	Events eventbus.EventBus
	Clock  clock.Clock
	Logger *slog.Logger
}

type deployWorkerStore interface {
	TryProjectLock(ctx context.Context, projectID uuid.UUID) (bool, error)
	UnlockProjectLock(ctx context.Context, projectID uuid.UUID) error
	HasInFlightDeployTask(ctx context.Context, projectID uuid.UUID) (bool, error)
	SetActiveEnvironmentDeployTask(ctx context.Context, projectID, deployTaskID uuid.UUID) error
	NextScheduledFireAt(ctx context.Context, projectID uuid.UUID) (*time.Time, error)
}

type sqlDeployWorkerStore struct {
	pool *pgxpool.Pool
}

func (s *sqlDeployWorkerStore) TryProjectLock(ctx context.Context, projectID uuid.UUID) (bool, error) {
	var locked bool
	err := s.pool.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1)::bigint)`, advisoryLockKey(projectID)).Scan(&locked)
	return locked, err
}

func (s *sqlDeployWorkerStore) UnlockProjectLock(ctx context.Context, projectID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1)::bigint)`, advisoryLockKey(projectID))
	return err
}

func (s *sqlDeployWorkerStore) HasInFlightDeployTask(ctx context.Context, projectID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_task
			WHERE project_id = $1
			  AND work_status IN ('queued', 'in_progress')
			  AND COALESCE(metadata->>'task_type', '') = 'deploy'
			LIMIT 1
		)
	`, projectID).Scan(&exists)
	return exists, err
}

func (s *sqlDeployWorkerStore) SetActiveEnvironmentDeployTask(ctx context.Context, projectID, deployTaskID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE project_environment
		SET deploy_task_id = $2
		WHERE project_id = $1
		  AND is_active = true
	`, projectID, deployTaskID)
	return err
}

func (s *sqlDeployWorkerStore) NextScheduledFireAt(ctx context.Context, projectID uuid.UUID) (*time.Time, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT ts.next_fire_at
		FROM task_schedule ts
		JOIN project_environment pe ON pe.schedule_id = ts.id
		WHERE pe.project_id = $1
		  AND pe.is_active = true
		  AND ts.is_enabled = true
		ORDER BY ts.next_fire_at ASC NULLS LAST, ts.created_at ASC
		LIMIT 1
	`, projectID)
	var nextFireAt *time.Time
	if err := row.Scan(&nextFireAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return nextFireAt, nil
}

type DeployWorker struct {
	pool   *pgxpool.Pool
	events eventbus.EventBus
	clock  clock.Clock
	logger *slog.Logger

	store       deployWorkerStore
	projects    projectRepository
	tasks       projectTaskRepository
	assignments assignmentRepository
	inbox       inboxRepository
	users       userRepository
	taskService tasksvc.TaskService
}

func NewDeployWorker(opts DeployWorkerOptions) (*DeployWorker, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("deploy worker requires a database pool")
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	worker := &DeployWorker{
		pool:        opts.Pool,
		events:      opts.Events,
		clock:       opts.Clock,
		logger:      opts.Logger,
		store:       &sqlDeployWorkerStore{pool: opts.Pool},
		projects:    repo.NewProjectRepo(opts.Pool),
		tasks:       repo.NewProjectTaskRepo(opts.Pool),
		assignments: repo.NewAgentProjectAssignmentRepo(opts.Pool),
		inbox:       repo.NewInboxItemRepo(opts.Pool),
		users:       repo.NewHumanUserRepo(opts.Pool),
	}
	if opts.Events != nil {
		taskService, err := tasksvc.NewService(tasksvc.Options{
			Pool:     opts.Pool,
			EventBus: opts.Events,
		})
		if err != nil {
			return nil, err
		}
		worker.taskService = taskService
	}
	return worker, nil
}

func (w *DeployWorker) Execute(ctx context.Context, job jobqueue.Job) error {
	if w == nil {
		return fmt.Errorf("deploy worker is not configured")
	}

	var payload deployTaskCreatePayload
	if err := decodePayload(job.Payload, &payload); err != nil {
		return err
	}

	if payload.ProjectID == uuid.Nil {
		return fmt.Errorf("project_id is required")
	}
	mode := strings.TrimSpace(payload.DeliveryMode)
	if mode == "" {
		projectRecord, err := w.projects.GetByID(ctx, payload.ProjectID)
		if err != nil {
			return err
		}
		mode = strings.TrimSpace(projectRecord.DeliveryMode)
	}

	switch mode {
	case deliveryModeGated:
		return w.handleGated(ctx, payload)
	case deliveryModeScheduled:
		ready, err := w.inScheduledWindow(ctx, payload.ProjectID)
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
		fallthrough
	case deliveryModeContinuous:
		_, err := w.createContinuousDeployTask(ctx, payload)
		return err
	default:
		return fmt.Errorf("unsupported delivery mode %q", mode)
	}
}

func (w *DeployWorker) createContinuousDeployTask(ctx context.Context, payload deployTaskCreatePayload) (uuid.UUID, error) {
	if w.pool != nil && w.taskService == nil {
		return uuid.Nil, ErrCanonicalTaskServiceRequired
	}
	projectRecord, err := w.projects.GetByID(ctx, payload.ProjectID)
	if err != nil {
		return uuid.Nil, err
	}

	locked, err := w.store.TryProjectLock(ctx, payload.ProjectID)
	if err != nil {
		return uuid.Nil, err
	}
	if !locked {
		w.logger.Info("deploy advisory lock unavailable; skipping duplicate create", "project_id", payload.ProjectID)
		return uuid.Nil, nil
	}
	defer func() {
		_ = w.store.UnlockProjectLock(context.Background(), payload.ProjectID)
	}()

	hasInFlight, err := w.store.HasInFlightDeployTask(ctx, payload.ProjectID)
	if err != nil {
		return uuid.Nil, err
	}
	if hasInFlight {
		w.logger.Info("deploy already in flight, skipping", "project_id", payload.ProjectID)
		return uuid.Nil, nil
	}

	metadata, err := json.Marshal(map[string]any{
		"task_type":  deliveryTaskTypeDeploy,
		"commit_sha": trimSHA(payload.CommitSHA),
		"delivery": map[string]any{
			"commit_sha":           trimSHA(payload.CommitSHA),
			"delivery_mode":        deliveryModeContinuous,
			"triggered_by_task_id": uuidToString(payload.TriggeredByTaskID),
			"merge_queue_entry_id": uuidToString(payload.MergeQueueEntryID),
		},
	})
	if err != nil {
		return uuid.Nil, err
	}

	var assignedAgentID *uuid.UUID
	if assignment, assignErr := w.assignments.GetPM(ctx, payload.ProjectID); assignErr == nil && assignment.IsActive {
		assignedAgentID = &assignment.AgentID
	}

	title := "Deploy"
	if sha := shortSHA(payload.CommitSHA); sha != "" {
		title = fmt.Sprintf("Deploy %s", sha)
	}

	var taskRecord repo.ProjectTask
	if w.taskService != nil {
		created, createErr := w.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
			ProjectID:       payload.ProjectID,
			Title:           title,
			FlowTemplateID:  projectRecord.DeployFlowTemplateID,
			AssignedAgentID: assignedAgentID,
			CreatedByType:   deliverySystemActorType,
			Metadata:        metadata,
		})
		if createErr != nil {
			return uuid.Nil, createErr
		}
		queued, queueErr := w.taskService.TransitionStatus(ctx, created.ID, "queued", tasksvc.Actor{Type: deliverySystemActorType})
		if queueErr != nil {
			return uuid.Nil, queueErr
		}
		taskRecord = repo.ProjectTask(*queued)
	} else {
		taskRecord, err = w.tasks.Create(ctx, repo.ProjectTask{
			OrganizationID:      projectRecord.OrganizationID,
			ProjectID:           payload.ProjectID,
			Title:               title,
			WorkStatus:          "queued",
			FlowTemplateID:      projectRecord.DeployFlowTemplateID,
			CreatedByType:       deliverySystemActorType,
			CreatedByID:         nil,
			AssignedAgentID:     assignedAgentID,
			RequiresHumanReview: false,
			Metadata:            metadata,
		})
		if err != nil {
			return uuid.Nil, err
		}
	}

	if err := w.store.SetActiveEnvironmentDeployTask(ctx, payload.ProjectID, taskRecord.ID); err != nil {
		return uuid.Nil, err
	}

	return taskRecord.ID, nil
}

func (w *DeployWorker) handleGated(ctx context.Context, payload deployTaskCreatePayload) error {
	projectRecord, err := w.projects.GetByID(ctx, payload.ProjectID)
	if err != nil {
		return err
	}
	targetUserID, err := resolveAdminRecipient(ctx, w.users, projectRecord.OrganizationID)
	if err != nil {
		return err
	}
	body := "Deployment approval requested"
	actionPayload, err := json.Marshal(map[string]any{
		"requested_type":       "deploy_approval",
		"project_id":           payload.ProjectID,
		"commit_sha":           trimSHA(payload.CommitSHA),
		"triggered_by_task_id": uuidToString(payload.TriggeredByTaskID),
	})
	if err != nil {
		return err
	}

	item := repo.InboxItem{
		OrganizationID:  projectRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "deploy_approval",
		SourceProjectID: &payload.ProjectID,
		SourceTaskID:    optionalUUIDPointer(payload.TriggeredByTaskID),
		CreatedByType:   deliverySystemActorType,
		Title:           "Deploy approval required",
		Body:            &body,
		ActionPayload:   actionPayload,
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

	if w.events != nil {
		encoded, err := json.Marshal(map[string]any{
			"project_id": payload.ProjectID,
			"commit_sha": trimSHA(payload.CommitSHA),
		})
		if err != nil {
			return err
		}
		if err := w.events.Publish(ctx, nil, eventbus.DomainEvent{
			OrganizationID: projectRecord.OrganizationID,
			EventType:      "deploy.approval_requested",
			ActorType:      deliverySystemActorType,
			Payload:        encoded,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *DeployWorker) inScheduledWindow(ctx context.Context, projectID uuid.UUID) (bool, error) {
	nextFireAt, err := w.store.NextScheduledFireAt(ctx, projectID)
	if err != nil {
		return false, err
	}
	return isWithinWindow(w.clock.Now().UTC(), nextFireAt, scheduledWindowTolerance), nil
}

func uuidToString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
