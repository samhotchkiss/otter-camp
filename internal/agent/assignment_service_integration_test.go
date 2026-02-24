//go:build integration

package agent

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAssignmentServiceAssignToProjectPMSwap(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	svc, err := NewAssignmentService(AssignmentServiceOptions{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewAssignmentService: %v", err)
	}

	org, project, agentA, agentB := seedAssignmentServiceFixtures(t, ctx, pool)
	_ = org

	if _, err := svc.AssignToProject(ctx, agentA.ID, project.ID, "pm", AssignmentActor{Type: "system"}); err != nil {
		t.Fatalf("AssignToProject agentA: %v", err)
	}
	if _, err := svc.AssignToProject(ctx, agentB.ID, project.ID, "pm", AssignmentActor{Type: "system"}); err != nil {
		t.Fatalf("AssignToProject agentB: %v", err)
	}

	projectAssignments := repo.NewAgentProjectAssignmentRepo(pool)
	rowA, err := projectAssignments.GetByAgentAndProject(ctx, agentA.ID, project.ID)
	if err != nil {
		t.Fatalf("GetByAgentAndProject agentA: %v", err)
	}
	if rowA.IsActive {
		t.Fatalf("agentA assignment is_active = true, want false")
	}
	if rowA.DeactivatedAt == nil {
		t.Fatalf("agentA deactivated_at = nil, want non-nil")
	}

	rowB, err := projectAssignments.GetByAgentAndProject(ctx, agentB.ID, project.ID)
	if err != nil {
		t.Fatalf("GetByAgentAndProject agentB: %v", err)
	}
	if !rowB.IsActive {
		t.Fatalf("agentB assignment is_active = false, want true")
	}
	if rowB.Role != "pm" {
		t.Fatalf("agentB role = %q, want pm", rowB.Role)
	}

	currentPM, err := projectAssignments.GetPM(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetPM: %v", err)
	}
	if currentPM.AgentID != agentB.ID {
		t.Fatalf("GetPM agent_id = %s, want %s", currentPM.AgentID, agentB.ID)
	}
}

func TestAssignmentServiceRemoveFromProjectDeactivatesAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	svc, err := NewAssignmentService(AssignmentServiceOptions{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewAssignmentService: %v", err)
	}

	org, project, agentA, _ := seedAssignmentServiceFixtures(t, ctx, pool)

	if _, err := svc.AssignToProject(ctx, agentA.ID, project.ID, "pm", AssignmentActor{Type: "system"}); err != nil {
		t.Fatalf("AssignToProject PM: %v", err)
	}

	removed, err := svc.RemoveFromProject(ctx, agentA.ID, project.ID)
	if err != nil {
		t.Fatalf("RemoveFromProject: %v", err)
	}
	if removed.IsActive {
		t.Fatalf("removed assignment is_active = true, want false")
	}
	if removed.DeactivatedAt == nil {
		t.Fatalf("removed deactivated_at = nil, want non-nil")
	}

	var eventCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'agent.pm_removed'
		  AND payload->>'agent_id' = $2
		  AND payload->>'project_id' = $3
	`, org.ID, agentA.ID.String(), project.ID.String()).Scan(&eventCount); err != nil {
		t.Fatalf("query pm_removed event count: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("pm_removed event count = %d, want 1", eventCount)
	}
}

func seedAssignmentServiceFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (repo.Organization, repo.Project, repo.Agent, repo.Agent) {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "assign-svc-org-" + uuid.NewString()[:8],
		DisplayName: "Assignment Service Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "assign-svc-project-" + uuid.NewString()[:8],
		DisplayName:    "Assignment Service Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	agentA, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Svc Agent A",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent A: %v", err)
	}

	agentB, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Svc Agent B",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent B: %v", err)
	}

	return org, project, agentA, agentB
}
