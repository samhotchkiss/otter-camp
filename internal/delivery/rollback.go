package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrProjectIDRequired       = errors.New("project_id is required")
	ErrTargetCommitSHARequired = errors.New("target_commit_sha is required")
	ErrTargetCommitNotFound    = errors.New("target_commit_sha was not found in project history")
)

type RollbackServiceOptions struct {
	Pool   *pgxpool.Pool
	Git    GitService
	Events eventbus.EventBus
	Clock  clock.Clock
}

type RollbackService struct {
	pool   *pgxpool.Pool
	git    GitService
	events eventbus.EventBus
	clock  clock.Clock

	projects    *repo.ProjectRepo
	tasks       *repo.ProjectTaskRepo
	assignments *repo.AgentProjectAssignmentRepo
}

func NewRollbackService(opts RollbackServiceOptions) (*RollbackService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("rollback service requires a database pool")
	}
	if opts.Git == nil {
		opts.Git = UnavailableGitService{}
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	return &RollbackService{
		pool:        opts.Pool,
		git:         opts.Git,
		events:      opts.Events,
		clock:       opts.Clock,
		projects:    repo.NewProjectRepo(opts.Pool),
		tasks:       repo.NewProjectTaskRepo(opts.Pool),
		assignments: repo.NewAgentProjectAssignmentRepo(opts.Pool),
	}, nil
}

func (s *RollbackService) InitiateRollback(ctx context.Context, projectID uuid.UUID, targetCommitSHA string) (uuid.UUID, error) {
	if s == nil {
		return uuid.Nil, fmt.Errorf("rollback service is not configured")
	}
	if projectID == uuid.Nil {
		return uuid.Nil, ErrProjectIDRequired
	}
	targetCommitSHA = strings.TrimSpace(targetCommitSHA)
	if targetCommitSHA == "" {
		return uuid.Nil, ErrTargetCommitSHARequired
	}

	projectRecord, err := s.projects.GetByID(ctx, projectID)
	if err != nil {
		return uuid.Nil, err
	}

	exists, err := s.git.CommitExists(ctx, projectID, targetCommitSHA)
	if err != nil {
		return uuid.Nil, err
	}
	if !exists {
		return uuid.Nil, ErrTargetCommitNotFound
	}

	timestamp := s.clock.Now().UTC()
	branch := fmt.Sprintf("rollback-%s", timestamp.Format("20060102150405"))
	short := shortSHA(targetCommitSHA)
	title := "Rollback"
	if short != "" {
		title = fmt.Sprintf("Rollback to %s", short)
	}

	metadata, err := json.Marshal(rollbackTaskMetadata{
		TaskType:        deliveryTaskTypeDeploy,
		Rollback:        true,
		TargetCommitSHA: targetCommitSHA,
	})
	if err != nil {
		return uuid.Nil, err
	}

	var assignedAgentID *uuid.UUID
	if assignment, assignErr := s.assignments.GetPM(ctx, projectID); assignErr == nil && assignment.IsActive {
		assignedAgentID = &assignment.AgentID
	}

	branchCopy := branch
	taskRecord, err := s.tasks.Create(ctx, repo.ProjectTask{
		OrganizationID:      projectRecord.OrganizationID,
		ProjectID:           projectID,
		Title:               title,
		WorkStatus:          "queued",
		BranchName:          &branchCopy,
		RequiresHumanReview: false,
		CreatedByType:       deliverySystemActorType,
		CreatedByID:         nil,
		AssignedAgentID:     assignedAgentID,
		Metadata:            metadata,
	})
	if err != nil {
		return uuid.Nil, err
	}

	if s.events != nil {
		payload, err := json.Marshal(map[string]any{
			"project_id":        projectID,
			"target_commit_sha": targetCommitSHA,
			"rollback_task_id":  taskRecord.ID,
		})
		if err != nil {
			return uuid.Nil, err
		}
		if err := s.events.Publish(ctx, nil, eventbus.DomainEvent{
			OrganizationID: projectRecord.OrganizationID,
			EventType:      "project.rollback_initiated",
			ActorType:      deliverySystemActorType,
			Payload:        payload,
		}); err != nil {
			return uuid.Nil, err
		}
	}

	return taskRecord.ID, nil
}

func rollbackBranchName(now time.Time) string {
	return fmt.Sprintf("rollback-%s", now.UTC().Format("20060102150405"))
}
