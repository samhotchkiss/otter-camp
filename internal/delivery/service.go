package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const (
	pushExecuteJobType = "push_execute"

	defaultJobPriority            = 100
	continuousDeliveryLockTimeout = 5 * time.Second
	continuousDeliveryRetryDelay  = 30 * time.Second
	maxAutoPushAttempts           = 3
	maxDeployTaskRemoteURLLen     = 64
	orgAdminRole                  = "admin"
	orgOwnerRole                  = "owner"
	systemActorType               = "system"
	inboxTypeSystemAlert          = "system_alert"
	scheduledDeliveryMode         = "scheduled"
	continuousDeliveryMode        = "continuous"
)

var (
	ErrEnvironmentIDRequired = errors.New("environment_id is required")
)

type pushExecutePayload struct {
	ProjectID     uuid.UUID `json:"project_id"`
	EnvironmentID uuid.UUID `json:"environment_id"`
	CommitSHA     string    `json:"commit_sha"`
	Attempt       int       `json:"attempt"`
}

type projectRemoteRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectRemote, error)
	GetDefault(ctx context.Context, projectID uuid.UUID) (repo.ProjectRemote, error)
}

type projectEnvironmentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectEnvironment, error)
	SetDeployTask(ctx context.Context, environmentID uuid.UUID, deployTaskID *uuid.UUID) (repo.ProjectEnvironment, error)
	RecordDeployment(ctx context.Context, environmentID uuid.UUID, commitSHA string, deployedAt time.Time) (repo.ProjectEnvironment, error)
	GetActiveByMode(ctx context.Context, mode string) ([]repo.ProjectEnvironment, error)
}

type projectTaskRepository interface {
	Create(ctx context.Context, task repo.ProjectTask) (repo.ProjectTask, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type assignmentRepository interface {
	GetPM(ctx context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type inboxRepository interface {
	Create(ctx context.Context, item repo.InboxItem) (repo.InboxItem, error)
}

type userRepository interface {
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
}

type mergeQueueRepository interface {
	Archive(ctx context.Context, id uuid.UUID, archivedAt time.Time) (repo.MergeQueueEntry, error)
}

type jobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type Options struct {
	Pool   *pgxpool.Pool
	Events eventbus.EventBus

	Remotes      projectRemoteRepository
	Environments projectEnvironmentRepository
	Tasks        projectTaskRepository
	Projects     projectRepository
	Assignments  assignmentRepository
	Inbox        inboxRepository
	Users        userRepository
	MergeQueue   mergeQueueRepository
	Enqueuer     jobEnqueuer

	Clock clock.Clock
}

type Service struct {
	pool *pgxpool.Pool

	remotes      projectRemoteRepository
	environments projectEnvironmentRepository
	tasks        projectTaskRepository
	projects     projectRepository
	assignments  assignmentRepository
	inbox        inboxRepository
	users        userRepository
	mergeQueue   mergeQueueRepository
	enqueuer     jobEnqueuer
	clock        clock.Clock
	taskService  tasksvc.TaskService

	// lockAcquiredHook is test-only and runs after lock acquisition.
	lockAcquiredHook func()
}

func NewService(opts Options) (*Service, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("delivery service requires a database pool")
	}

	svc := &Service{
		pool:  opts.Pool,
		clock: opts.Clock,
	}
	if svc.clock == nil {
		svc.clock = clock.Real{}
	}
	if opts.Remotes != nil {
		svc.remotes = opts.Remotes
	} else {
		svc.remotes = repo.NewProjectRemoteRepo(opts.Pool)
	}
	if opts.Environments != nil {
		svc.environments = opts.Environments
	} else {
		svc.environments = repo.NewProjectEnvironmentRepo(opts.Pool)
	}
	if opts.Tasks != nil {
		svc.tasks = opts.Tasks
	} else {
		svc.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if opts.Projects != nil {
		svc.projects = opts.Projects
	} else {
		svc.projects = repo.NewProjectRepo(opts.Pool)
	}
	if opts.Assignments != nil {
		svc.assignments = opts.Assignments
	} else {
		svc.assignments = repo.NewAgentProjectAssignmentRepo(opts.Pool)
	}
	if opts.Inbox != nil {
		svc.inbox = opts.Inbox
	} else {
		svc.inbox = repo.NewInboxItemRepo(opts.Pool)
	}
	if opts.Users != nil {
		svc.users = opts.Users
	} else {
		svc.users = repo.NewHumanUserRepo(opts.Pool)
	}
	svc.mergeQueue = opts.MergeQueue
	if opts.Enqueuer != nil {
		svc.enqueuer = opts.Enqueuer
	} else {
		svc.enqueuer = jobqueue.New(opts.Pool, nil, jobqueue.Config{})
	}
	if opts.Events != nil {
		taskService, err := tasksvc.NewService(tasksvc.Options{
			Pool:     opts.Pool,
			EventBus: opts.Events,
		})
		if err != nil {
			return nil, err
		}
		svc.taskService = taskService
	}

	return svc, nil
}

func (s *Service) TriggerContinuousDelivery(ctx context.Context, environmentID uuid.UUID, commitSHA string, attempt int) (uuid.UUID, error) {
	if environmentID == uuid.Nil {
		return uuid.Nil, ErrEnvironmentIDRequired
	}

	environment, err := s.environments.GetByID(ctx, environmentID)
	if err != nil {
		return uuid.Nil, err
	}

	payload := pushExecutePayload{
		ProjectID:     environment.ProjectID,
		EnvironmentID: environment.ID,
		CommitSHA:     strings.TrimSpace(commitSHA),
		Attempt:       attempt,
	}

	lockConn, err := s.pool.Acquire(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("acquire advisory lock connection: %w", err)
	}
	defer lockConn.Release()

	lockCtx, cancel := context.WithTimeout(ctx, continuousDeliveryLockTimeout)
	defer cancel()

	lockKey := advisoryLockKeyInput(environment.ProjectID, environment.ID)
	var locked bool
	if err := lockConn.QueryRow(lockCtx, `
		SELECT pg_try_advisory_lock(hashtext($1)::bigint)
	`, lockKey).Scan(&locked); err != nil {
		return uuid.Nil, fmt.Errorf("acquire advisory lock: %w", err)
	}

	if !locked {
		runAfter := s.clock.Now().UTC().Add(continuousDeliveryRetryDelay)
		return s.enqueuePushExecute(ctx, payload, &runAfter)
	}
	defer func() {
		_, _ = lockConn.Exec(context.Background(), `
			SELECT pg_advisory_unlock(hashtext($1)::bigint)
		`, lockKey)
	}()

	if s.lockAcquiredHook != nil {
		s.lockAcquiredHook()
	}

	return s.enqueuePushExecute(ctx, payload, nil)
}

func (s *Service) RequestGatedDelivery(ctx context.Context, environmentID uuid.UUID, commitSHA string) (repo.InboxItem, error) {
	if environmentID == uuid.Nil {
		return repo.InboxItem{}, ErrEnvironmentIDRequired
	}

	environment, err := s.environments.GetByID(ctx, environmentID)
	if err != nil {
		return repo.InboxItem{}, err
	}
	projectRecord, err := s.projects.GetByID(ctx, environment.ProjectID)
	if err != nil {
		return repo.InboxItem{}, err
	}
	remoteRecord, err := s.resolveEnvironmentRemote(ctx, environment)
	if err != nil {
		return repo.InboxItem{}, err
	}

	targetUserID, err := s.resolveAdminRecipient(ctx, projectRecord.OrganizationID)
	if err != nil {
		return repo.InboxItem{}, err
	}

	payloadJSON, err := json.Marshal(map[string]any{
		"action":         "deploy_task_create",
		"environment_id": environment.ID,
		"commit_sha":     strings.TrimSpace(commitSHA),
	})
	if err != nil {
		return repo.InboxItem{}, fmt.Errorf("marshal gated delivery payload: %w", err)
	}

	body := fmt.Sprintf("Deploy request pending for %s to %s.", environment.Name, remoteRecord.URL)
	return s.inbox.Create(ctx, repo.InboxItem{
		OrganizationID:  projectRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        inboxTypeSystemAlert,
		SourceProjectID: &projectRecord.ID,
		CreatedByType:   systemActorType,
		Title:           fmt.Sprintf("Deploy approval requested for %s", environment.Name),
		Body:            &body,
		ActionPayload:   payloadJSON,
	})
}

func (s *Service) CreateDeployTask(ctx context.Context, environmentID uuid.UUID, commitSHA string) (repo.ProjectTask, error) {
	if environmentID == uuid.Nil {
		return repo.ProjectTask{}, ErrEnvironmentIDRequired
	}

	environment, err := s.environments.GetByID(ctx, environmentID)
	if err != nil {
		return repo.ProjectTask{}, err
	}
	projectRecord, err := s.projects.GetByID(ctx, environment.ProjectID)
	if err != nil {
		return repo.ProjectTask{}, err
	}
	remoteRecord, err := s.resolveEnvironmentRemote(ctx, environment)
	if err != nil {
		return repo.ProjectTask{}, err
	}
	pmAssignment, err := s.assignments.GetPM(ctx, environment.ProjectID)
	if err != nil {
		return repo.ProjectTask{}, err
	}

	payloadJSON, err := json.Marshal(map[string]any{
		"task_type":  deliveryTaskTypeDeploy,
		"commit_sha": strings.TrimSpace(commitSHA),
		"delivery": map[string]any{
			"environment_id": environment.ID,
			"commit_sha":     strings.TrimSpace(commitSHA),
			"delivery_mode":  environment.DeliveryMode,
		},
	})
	if err != nil {
		return repo.ProjectTask{}, fmt.Errorf("marshal deploy task metadata: %w", err)
	}

	var deployTask repo.ProjectTask
	if s.taskService != nil {
		created, createErr := s.taskService.CreateTask(ctx, tasksvc.CreateTaskRequest{
			ProjectID:       environment.ProjectID,
			Title:           formatDeployTaskTitle(environment.Name, remoteRecord.URL),
			FlowTemplateID:  projectRecord.DeployFlowTemplateID,
			AssignedAgentID: &pmAssignment.AgentID,
			CreatedByType:   systemActorType,
			Metadata:        payloadJSON,
		})
		if createErr != nil {
			return repo.ProjectTask{}, createErr
		}
		queued, queueErr := s.taskService.TransitionStatus(ctx, created.ID, "queued", tasksvc.Actor{Type: systemActorType})
		if queueErr != nil {
			return repo.ProjectTask{}, queueErr
		}
		deployTask = repo.ProjectTask(*queued)
	} else {
		deployTask, err = s.tasks.Create(ctx, repo.ProjectTask{
			OrganizationID:  projectRecord.OrganizationID,
			ProjectID:       environment.ProjectID,
			Title:           formatDeployTaskTitle(environment.Name, remoteRecord.URL),
			WorkStatus:      "queued",
			FlowTemplateID:  projectRecord.DeployFlowTemplateID,
			CreatedByType:   systemActorType,
			CreatedByID:     nil,
			AssignedAgentID: &pmAssignment.AgentID,
			Metadata:        payloadJSON,
		})
		if err != nil {
			return repo.ProjectTask{}, err
		}
	}

	if _, err := s.environments.SetDeployTask(ctx, environmentID, &deployTask.ID); err != nil {
		return repo.ProjectTask{}, err
	}

	return deployTask, nil
}

func (s *Service) RecordDeployment(ctx context.Context, environmentID uuid.UUID, commitSHA string, mergeQueueEntryID *uuid.UUID) (repo.ProjectEnvironment, error) {
	if environmentID == uuid.Nil {
		return repo.ProjectEnvironment{}, ErrEnvironmentIDRequired
	}

	recorded, err := s.environments.RecordDeployment(ctx, environmentID, commitSHA, s.clock.Now().UTC())
	if err != nil {
		return repo.ProjectEnvironment{}, err
	}

	if mergeQueueEntryID != nil && s.mergeQueue != nil {
		if _, archiveErr := s.mergeQueue.Archive(ctx, *mergeQueueEntryID, s.clock.Now().UTC()); archiveErr != nil {
			return repo.ProjectEnvironment{}, archiveErr
		}
	}

	return recorded, nil
}

func (s *Service) HandlePushFailure(ctx context.Context, environmentID uuid.UUID, commitSHA string, attempt int, cause error) error {
	if environmentID == uuid.Nil {
		return ErrEnvironmentIDRequired
	}

	environment, err := s.environments.GetByID(ctx, environmentID)
	if err != nil {
		return err
	}

	if attempt >= maxAutoPushAttempts-1 {
		return s.createFinalFailureInboxAlert(ctx, environment, commitSHA, attempt, cause)
	}

	runAfter := s.clock.Now().UTC().Add(pushRetryDelay(attempt))
	_, err = s.enqueuePushExecute(ctx, pushExecutePayload{
		ProjectID:     environment.ProjectID,
		EnvironmentID: environment.ID,
		CommitSHA:     strings.TrimSpace(commitSHA),
		Attempt:       attempt + 1,
	}, &runAfter)
	return err
}

func (s *Service) TriggerScheduledDelivery(ctx context.Context, commitSHA string) ([]repo.ProjectTask, error) {
	environments, err := s.environments.GetActiveByMode(ctx, scheduledDeliveryMode)
	if err != nil {
		return nil, err
	}

	tasks := make([]repo.ProjectTask, 0, len(environments))
	for _, environment := range environments {
		deployTask, createErr := s.CreateDeployTask(ctx, environment.ID, commitSHA)
		if createErr != nil {
			return nil, createErr
		}
		tasks = append(tasks, deployTask)
	}

	return tasks, nil
}

func (s *Service) enqueuePushExecute(ctx context.Context, payload pushExecutePayload, runAfter *time.Time) (uuid.UUID, error) {
	return s.enqueuer.Enqueue(ctx, nil, pushExecuteJobType, defaultJobPriority, payload, runAfter)
}

func (s *Service) resolveEnvironmentRemote(ctx context.Context, environment repo.ProjectEnvironment) (repo.ProjectRemote, error) {
	if environment.RemoteID != nil {
		return s.remotes.GetByID(ctx, *environment.RemoteID)
	}
	return s.remotes.GetDefault(ctx, environment.ProjectID)
}

func (s *Service) resolveAdminRecipient(ctx context.Context, organizationID uuid.UUID) (*uuid.UUID, error) {
	users, err := s.users.List(ctx, organizationID)
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		if !user.IsActive {
			continue
		}
		if user.Role == orgOwnerRole || user.Role == orgAdminRole {
			id := user.ID
			return &id, nil
		}
	}

	return nil, nil
}

func (s *Service) createFinalFailureInboxAlert(ctx context.Context, environment repo.ProjectEnvironment, commitSHA string, attempt int, cause error) error {
	projectRecord, err := s.projects.GetByID(ctx, environment.ProjectID)
	if err != nil {
		return err
	}

	targetUserID, err := s.resolveAdminRecipient(ctx, projectRecord.OrganizationID)
	if err != nil {
		return err
	}

	reason := "push failed"
	if cause != nil {
		reason = strings.TrimSpace(cause.Error())
	}

	payloadJSON, err := json.Marshal(map[string]any{
		"environment_id": environment.ID,
		"commit_sha":     strings.TrimSpace(commitSHA),
		"attempt":        attempt,
		"error":          reason,
	})
	if err != nil {
		return fmt.Errorf("marshal push failure payload: %w", err)
	}

	body := fmt.Sprintf("Automatic delivery failed after %d attempts: %s", attempt+1, reason)
	_, err = s.inbox.Create(ctx, repo.InboxItem{
		OrganizationID:  projectRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        inboxTypeSystemAlert,
		SourceProjectID: &projectRecord.ID,
		CreatedByType:   systemActorType,
		Title:           fmt.Sprintf("Delivery failed for %s", environment.Name),
		Body:            &body,
		ActionPayload:   payloadJSON,
	})
	return err
}

func advisoryLockKeyInput(projectID, environmentID uuid.UUID) string {
	return projectID.String() + environmentID.String()
}

func pushRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 0:
		return 30 * time.Second
	case 1:
		return 120 * time.Second
	default:
		return 480 * time.Second
	}
}

func formatDeployTaskTitle(environmentName, remoteURL string) string {
	name := strings.TrimSpace(environmentName)
	trimmedURL := strings.TrimSpace(remoteURL)
	if len(trimmedURL) > maxDeployTaskRemoteURLLen {
		trimmedURL = trimmedURL[:maxDeployTaskRemoteURLLen]
	}
	return fmt.Sprintf("Deploy %s to %s", name, trimmedURL)
}
