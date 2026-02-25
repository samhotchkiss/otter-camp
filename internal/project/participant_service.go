package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrParticipantTaskIDRequired = errors.New("task_id is required")
	ErrInvalidParticipantType    = errors.New("invalid participant type")
	ErrInvalidParticipantRole    = errors.New("invalid participant role")
)

type TaskParticipant = repo.ProjectTaskParticipant

type taskParticipantRepository interface {
	Add(ctx context.Context, participant repo.ProjectTaskParticipant) (repo.ProjectTaskParticipant, error)
	Remove(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) (repo.ProjectTaskParticipant, error)
	ListActive(ctx context.Context, taskID uuid.UUID) ([]repo.ProjectTaskParticipant, error)
	GetByParticipant(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) (repo.ProjectTaskParticipant, error)
}

type taskParticipantTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type taskParticipantEventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type TaskParticipantService interface {
	AddParticipant(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID, role string) error
	RemoveParticipant(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) error
	ListParticipants(ctx context.Context, taskID uuid.UUID) ([]TaskParticipant, error)
}

type TaskParticipantServiceOptions struct {
	Pool         *pgxpool.Pool
	Participants taskParticipantRepository
	Tasks        taskParticipantTaskRepository
	Events       taskParticipantEventPublisher
}

type taskParticipantService struct {
	participants taskParticipantRepository
	tasks        taskParticipantTaskRepository
	events       taskParticipantEventPublisher
}

func NewTaskParticipantService(opts TaskParticipantServiceOptions) (TaskParticipantService, error) {
	requiresPool := opts.Participants == nil || opts.Tasks == nil
	if opts.Pool == nil && requiresPool {
		return nil, fmt.Errorf("task participant service requires a database pool")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("task participant service requires an event bus")
	}

	svc := &taskParticipantService{events: opts.Events}
	if opts.Participants != nil {
		svc.participants = opts.Participants
	} else {
		svc.participants = repo.NewProjectTaskParticipantRepo(opts.Pool)
	}
	if opts.Tasks != nil {
		svc.tasks = opts.Tasks
	} else {
		svc.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	return svc, nil
}

func (s *taskParticipantService) AddParticipant(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID, role string) error {
	participantType = strings.TrimSpace(participantType)
	role = strings.TrimSpace(role)
	if taskID == uuid.Nil {
		return ErrParticipantTaskIDRequired
	}
	if participantID == uuid.Nil {
		return ErrInvalidParticipantType
	}
	if !isValidTaskParticipantType(participantType) {
		return ErrInvalidParticipantType
	}
	if !isValidTaskParticipantRole(role) {
		return ErrInvalidParticipantRole
	}

	existing, err := s.participants.GetByParticipant(ctx, taskID, participantType, participantID)
	if err == nil && existing.LeftAt == nil {
		return nil
	}
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return err
	}

	if _, err := s.participants.Add(ctx, repo.ProjectTaskParticipant{
		TaskID:          taskID,
		ParticipantType: participantType,
		ParticipantID:   participantID,
		Role:            role,
	}); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			return nil
		}
		return err
	}
	return s.publishParticipantEvent(ctx, taskID, "task.participant_added", participantType, participantID, role)
}

func (s *taskParticipantService) RemoveParticipant(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) error {
	participantType = strings.TrimSpace(participantType)
	if taskID == uuid.Nil {
		return ErrParticipantTaskIDRequired
	}
	if participantID == uuid.Nil {
		return ErrInvalidParticipantType
	}
	if !isValidTaskParticipantType(participantType) {
		return ErrInvalidParticipantType
	}

	removed, err := s.participants.Remove(ctx, taskID, participantType, participantID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	return s.publishParticipantEvent(ctx, taskID, "task.participant_removed", participantType, participantID, removed.Role)
}

func (s *taskParticipantService) ListParticipants(ctx context.Context, taskID uuid.UUID) ([]TaskParticipant, error) {
	if taskID == uuid.Nil {
		return nil, ErrParticipantTaskIDRequired
	}
	return s.participants.ListActive(ctx, taskID)
}

func (s *taskParticipantService) publishParticipantEvent(ctx context.Context, taskID uuid.UUID, eventType, participantType string, participantID uuid.UUID, role string) error {
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"task_id":          taskID.String(),
		"participant_type": participantType,
		"participant_id":   participantID.String(),
		"role":             role,
	})
	if err != nil {
		return err
	}
	return s.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: taskRecord.OrganizationID,
		EventType:      eventType,
		ActorType:      "system",
		Payload:        payload,
	})
}

func isValidTaskParticipantType(value string) bool {
	switch value {
	case "human_user", "agent":
		return true
	default:
		return false
	}
}

func isValidTaskParticipantRole(value string) bool {
	switch value {
	case "planner", "worker", "reviewer", "observer":
		return true
	default:
		return false
	}
}
