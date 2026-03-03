package agent

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

const defaultSkillPriority = 100

var (
	ErrAssignmentAgentIDRequired   = errors.New("assignment agent_id is required")
	ErrAssignmentProjectIDRequired = errors.New("assignment project_id is required")
	ErrAssignmentSkillIDRequired   = errors.New("assignment skill_id is required")
	ErrAssignmentInvalidRole       = errors.New("assignment role must be pm, worker, reviewer, or observer")
	ErrStarterTrioRoleForbidden    = errors.New("starter trio agents (Frank, Lori, Ellie) operate at the organization level and cannot be assigned as project PM, worker, or reviewer")
	ErrPMConflict                  = repo.ErrPMConflict
)

type AssignmentActor struct {
	Type string
	ID   uuid.UUID
}

type AssignmentService interface {
	AssignToProject(ctx context.Context, agentID, projectID uuid.UUID, role string, assignedBy AssignmentActor) (*repo.AgentProjectAssignment, error)
	RemoveFromProject(ctx context.Context, agentID, projectID uuid.UUID) (*repo.AgentProjectAssignment, error)
	AttachSkill(ctx context.Context, agentID, skillID uuid.UUID, priority int, attachedBy AssignmentActor) (*repo.AgentSkillAttachment, error)
	DetachSkill(ctx context.Context, agentID, skillID uuid.UUID) (*repo.AgentSkillAttachment, error)
	ReorderSkills(ctx context.Context, agentID uuid.UUID, priorityMap map[uuid.UUID]int) error
}

type assignmentTxStarter interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

type assignmentProjectRepository interface {
	AssignTx(ctx context.Context, tx pgx.Tx, assignment repo.AgentProjectAssignment) (repo.AgentProjectAssignment, error)
	DeactivateTx(ctx context.Context, tx pgx.Tx, agentID, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
	GetByAgentAndProjectTx(ctx context.Context, tx pgx.Tx, agentID, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
	GetPMTx(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type assignmentSkillRepository interface {
	Attach(ctx context.Context, attachment repo.AgentSkillAttachment) (repo.AgentSkillAttachment, error)
	Detach(ctx context.Context, agentID, skillID uuid.UUID) (repo.AgentSkillAttachment, error)
	SetPriorityTx(ctx context.Context, tx pgx.Tx, agentID, skillID uuid.UUID, priority int) (repo.AgentSkillAttachment, error)
}

type assignmentProjectLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type assignmentAgentLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type AssignmentServiceOptions struct {
	Pool               *pgxpool.Pool
	ProjectAssignments assignmentProjectRepository
	SkillAttachments   assignmentSkillRepository
	Projects           assignmentProjectLookup
	Agents             assignmentAgentLookup
	Events             eventbus.EventBus
}

type assignmentService struct {
	txStarter          assignmentTxStarter
	projectAssignments assignmentProjectRepository
	skillAttachments   assignmentSkillRepository
	projects           assignmentProjectLookup
	agents             assignmentAgentLookup
	events             eventbus.EventBus
}

func NewAssignmentService(opts AssignmentServiceOptions) (AssignmentService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("assignment service requires a database pool")
	}

	projectAssignments := opts.ProjectAssignments
	if projectAssignments == nil {
		projectAssignments = repo.NewAgentProjectAssignmentRepo(opts.Pool)
	}
	skillAttachments := opts.SkillAttachments
	if skillAttachments == nil {
		skillAttachments = repo.NewAgentSkillAttachmentRepo(opts.Pool)
	}
	projects := opts.Projects
	if projects == nil {
		projects = repo.NewProjectRepo(opts.Pool)
	}
	agents := opts.Agents
	if agents == nil {
		agents = repo.NewAgentRepo(opts.Pool)
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("assignment service requires an event bus")
	}

	return &assignmentService{
		txStarter:          opts.Pool,
		projectAssignments: projectAssignments,
		skillAttachments:   skillAttachments,
		projects:           projects,
		agents:             agents,
		events:             opts.Events,
	}, nil
}

func (s *assignmentService) AssignToProject(ctx context.Context, agentID, projectID uuid.UUID, role string, assignedBy AssignmentActor) (*repo.AgentProjectAssignment, error) {
	if agentID == uuid.Nil {
		return nil, ErrAssignmentAgentIDRequired
	}
	if projectID == uuid.Nil {
		return nil, ErrAssignmentProjectIDRequired
	}

	role = normalizeAssignmentRole(role)
	if role == "" {
		return nil, ErrAssignmentInvalidRole
	}
	if s.agents != nil {
		agentRecord, err := s.agents.GetByID(ctx, agentID)
		if err != nil {
			return nil, err
		}
		if agentRecord.IsStarterTrio && isForbiddenStarterTrioAssignmentRole(role) {
			return nil, ErrStarterTrioRoleForbidden
		}
	}

	assignedByType, assignedByID, err := normalizeAssignmentActor(assignedBy)
	if err != nil {
		return nil, err
	}

	tx, err := s.txStarter.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin assignment transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if role == "pm" {
		currentPM, getErr := s.projectAssignments.GetPMTx(ctx, tx, projectID)
		if getErr == nil {
			if currentPM.AgentID != agentID {
				if _, deactivateErr := s.projectAssignments.DeactivateTx(ctx, tx, currentPM.AgentID, projectID); deactivateErr != nil {
					return nil, deactivateErr
				}
			}
		} else if !errors.Is(getErr, repo.ErrNotFound) {
			return nil, getErr
		}
	}

	assigned, err := s.projectAssignments.AssignTx(ctx, tx, repo.AgentProjectAssignment{
		AgentID:        agentID,
		ProjectID:      projectID,
		Role:           role,
		AssignedByType: assignedByType,
		AssignedByID:   assignedByID,
	})
	if err != nil {
		if errors.Is(err, repo.ErrPMConflict) {
			return nil, ErrPMConflict
		}
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit assignment transaction: %w", err)
	}
	return &assigned, nil
}

func (s *assignmentService) RemoveFromProject(ctx context.Context, agentID, projectID uuid.UUID) (*repo.AgentProjectAssignment, error) {
	if agentID == uuid.Nil {
		return nil, ErrAssignmentAgentIDRequired
	}
	if projectID == uuid.Nil {
		return nil, ErrAssignmentProjectIDRequired
	}

	project, err := s.projects.GetByID(ctx, projectID)
	if err != nil {
		return nil, err
	}

	tx, err := s.txStarter.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin remove assignment transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existing, err := s.projectAssignments.GetByAgentAndProjectTx(ctx, tx, agentID, projectID)
	if err != nil {
		return nil, err
	}

	updated, err := s.projectAssignments.DeactivateTx(ctx, tx, agentID, projectID)
	if err != nil {
		return nil, err
	}

	if existing.Role == "pm" && existing.IsActive {
		payload, marshalErr := json.Marshal(map[string]any{
			"agent_id":   existing.AgentID,
			"project_id": existing.ProjectID,
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal agent.pm_removed payload: %w", marshalErr)
		}
		if err := s.events.Publish(ctx, tx, eventbus.DomainEvent{
			OrganizationID: project.OrganizationID,
			EventType:      "agent.pm_removed",
			ActorType:      "system",
			Payload:        payload,
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit remove assignment transaction: %w", err)
	}
	return &updated, nil
}

func (s *assignmentService) AttachSkill(ctx context.Context, agentID, skillID uuid.UUID, priority int, attachedBy AssignmentActor) (*repo.AgentSkillAttachment, error) {
	if agentID == uuid.Nil {
		return nil, ErrAssignmentAgentIDRequired
	}
	if skillID == uuid.Nil {
		return nil, ErrAssignmentSkillIDRequired
	}

	attachedByType, attachedByID, err := normalizeAssignmentActor(attachedBy)
	if err != nil {
		return nil, err
	}

	attached, err := s.skillAttachments.Attach(ctx, repo.AgentSkillAttachment{
		AgentID:        agentID,
		SkillID:        skillID,
		Priority:       normalizePriority(priority),
		AttachedByType: attachedByType,
		AttachedByID:   attachedByID,
	})
	if err != nil {
		return nil, err
	}
	return &attached, nil
}

func (s *assignmentService) DetachSkill(ctx context.Context, agentID, skillID uuid.UUID) (*repo.AgentSkillAttachment, error) {
	if agentID == uuid.Nil {
		return nil, ErrAssignmentAgentIDRequired
	}
	if skillID == uuid.Nil {
		return nil, ErrAssignmentSkillIDRequired
	}

	detached, err := s.skillAttachments.Detach(ctx, agentID, skillID)
	if err != nil {
		return nil, err
	}
	return &detached, nil
}

func (s *assignmentService) ReorderSkills(ctx context.Context, agentID uuid.UUID, priorityMap map[uuid.UUID]int) error {
	if agentID == uuid.Nil {
		return ErrAssignmentAgentIDRequired
	}
	if len(priorityMap) == 0 {
		return nil
	}

	tx, err := s.txStarter.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin skill reorder transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	for skillID, priority := range priorityMap {
		if skillID == uuid.Nil {
			return ErrAssignmentSkillIDRequired
		}
		if _, err := s.skillAttachments.SetPriorityTx(ctx, tx, agentID, skillID, priority); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit skill reorder transaction: %w", err)
	}
	return nil
}

func normalizeAssignmentRole(role string) string {
	normalized := strings.ToLower(strings.TrimSpace(role))
	switch normalized {
	case "pm", "worker", "reviewer", "observer":
		return normalized
	default:
		return ""
	}
}

func normalizeAssignmentActor(actor AssignmentActor) (string, *uuid.UUID, error) {
	actorType, actorID, err := normalizeCreatedBy(actor.Type, actor.ID)
	if err != nil {
		return "", nil, err
	}

	if actorType == createdBySystem {
		return actorType, nil, nil
	}

	return actorType, &actorID, nil
}

func normalizePriority(priority int) int {
	if priority == 0 {
		return defaultSkillPriority
	}
	return priority
}

func isForbiddenStarterTrioAssignmentRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "pm", "worker", "reviewer":
		return true
	default:
		return false
	}
}
