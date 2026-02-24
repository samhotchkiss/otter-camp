//go:build integration

package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAgentServiceCreateTempRespectsConcurrentLimit(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	svc, err := NewService(Options{
		Pool:   pool,
		Agents: agentRepo,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "agent-svc-limit",
		DisplayName: "Agent Service Limit",
		Settings: repo.OrganizationSettings{
			Agents: repo.OrganizationAgentsSettings{MaxConcurrentTemps: 2},
		},
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	for i := 0; i < 2; i++ {
		_, createErr := svc.CreateTemp(ctx, org.ID, CreateTempAgentRequest{
			DisplayName:   "Temp " + uuid.NewString(),
			AgentType:     "worker",
			TempProjectID: uuid.New(),
			CreatedByType: "system",
		})
		if createErr != nil {
			t.Fatalf("CreateTemp %d: %v", i, createErr)
		}
	}

	_, err = svc.CreateTemp(ctx, org.ID, CreateTempAgentRequest{
		DisplayName:   "Temp 3",
		AgentType:     "worker",
		TempProjectID: uuid.New(),
		CreatedByType: "system",
	})
	if !errors.Is(err, ErrConcurrentTempLimitReached) {
		t.Fatalf("third CreateTemp error = %v, want ErrConcurrentTempLimitReached", err)
	}
}

func TestAgentServiceRetireExpiredTempsExpiresOnlyExpiredRows(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	svc, err := NewService(Options{
		Pool:   pool,
		Agents: agentRepo,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "agent-svc-expire", DisplayName: "Agent Service Expire"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	past := time.Now().UTC().Add(-2 * time.Hour)
	future := time.Now().UTC().Add(2 * time.Hour)

	expiredIDs := make([]uuid.UUID, 0, 5)
	futureIDs := make([]uuid.UUID, 0, 2)

	for i := 0; i < 5; i++ {
		agent, createErr := agentRepo.Create(ctx, repo.Agent{
			OrganizationID:       org.ID,
			DisplayName:          "Expired Temp " + uuid.NewString(),
			AgentClass:           "temp",
			LifecycleStatus:      "active",
			SystemPrompt:         "temp",
			OperatorInstructions: "",
			AgentType:            "worker",
			PrivateMemory:        false,
			MemoryReadScopes:     []string{"org", "project", "agent"},
			ToolAllowList:        []string{},
			ToolDenyList:         []string{},
			TempProjectID:        ptrUUID(uuid.New()),
			TempExpiresAt:        &past,
			CreatedByType:        "system",
			CreatedByID:          uuid.Nil,
		})
		if createErr != nil {
			t.Fatalf("seed expired temp %d: %v", i, createErr)
		}
		expiredIDs = append(expiredIDs, agent.ID)
	}

	for i := 0; i < 2; i++ {
		agent, createErr := agentRepo.Create(ctx, repo.Agent{
			OrganizationID:       org.ID,
			DisplayName:          "Future Temp " + uuid.NewString(),
			AgentClass:           "temp",
			LifecycleStatus:      "active",
			SystemPrompt:         "temp",
			OperatorInstructions: "",
			AgentType:            "worker",
			PrivateMemory:        false,
			MemoryReadScopes:     []string{"org", "project", "agent"},
			ToolAllowList:        []string{},
			ToolDenyList:         []string{},
			TempProjectID:        ptrUUID(uuid.New()),
			TempExpiresAt:        &future,
			CreatedByType:        "system",
			CreatedByID:          uuid.Nil,
		})
		if createErr != nil {
			t.Fatalf("seed future temp %d: %v", i, createErr)
		}
		futureIDs = append(futureIDs, agent.ID)
	}

	if err := svc.RetireExpiredTemps(ctx); err != nil {
		t.Fatalf("RetireExpiredTemps: %v", err)
	}

	for _, id := range expiredIDs {
		item, getErr := agentRepo.GetByID(ctx, id)
		if getErr != nil {
			t.Fatalf("GetByID expired temp %s: %v", id, getErr)
		}
		if item.LifecycleStatus != statusExpired {
			t.Fatalf("expired temp %s lifecycle_status = %q, want %q", id, item.LifecycleStatus, statusExpired)
		}
	}
	for _, id := range futureIDs {
		item, getErr := agentRepo.GetByID(ctx, id)
		if getErr != nil {
			t.Fatalf("GetByID future temp %s: %v", id, getErr)
		}
		if item.LifecycleStatus != statusActive {
			t.Fatalf("future temp %s lifecycle_status = %q, want %q", id, item.LifecycleStatus, statusActive)
		}
	}
}

func TestAgentServicePromotionWorkflow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	svc, err := NewService(Options{
		Pool:   pool,
		Agents: agentRepo,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "agent-svc-promote", DisplayName: "Agent Service Promote"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	temp, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:        org.ID,
		DisplayName:           "Temp Promotable",
		AgentClass:            "temp",
		LifecycleStatus:       "active",
		SystemPrompt:          "prompt",
		OperatorInstructions:  "instructions",
		AgentType:             "worker",
		PrivateMemory:         false,
		MemoryReadScopes:      []string{"org", "project", "agent"},
		ToolAllowList:         []string{"file.*"},
		ToolDenyList:          []string{"secret.*"},
		DefaultModelProfileID: ptrString("balanced"),
		TempProjectID:         ptrUUID(uuid.New()),
		CreatedByType:         "system",
		CreatedByID:           uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}

	staff, err := svc.PromoteTemp(ctx, org.ID, temp.ID, PromoteTempRequest{CreatedByType: "system"})
	if err != nil {
		t.Fatalf("PromoteTemp: %v", err)
	}

	if staff.AgentClass != agentClassStaff {
		t.Fatalf("promoted staff class = %q, want %q", staff.AgentClass, agentClassStaff)
	}
	if staff.LifecycleStatus != statusDraft {
		t.Fatalf("promoted staff lifecycle_status = %q, want %q", staff.LifecycleStatus, statusDraft)
	}

	updatedTemp, err := agentRepo.GetByID(ctx, temp.ID)
	if err != nil {
		t.Fatalf("GetByID temp after promotion: %v", err)
	}
	if updatedTemp.LifecycleStatus != statusPromoted {
		t.Fatalf("temp lifecycle_status after promotion = %q, want %q", updatedTemp.LifecycleStatus, statusPromoted)
	}
	if updatedTemp.PromotedToAgentID == nil || *updatedTemp.PromotedToAgentID != staff.ID {
		t.Fatalf("temp promoted_to_agent_id = %v, want %s", updatedTemp.PromotedToAgentID, staff.ID)
	}

	var promotedEventCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'agent.promoted'
		  AND payload->>'temp_agent_id' = $2
		  AND payload->>'promoted_staff_agent_id' = $3
	`, org.ID, temp.ID.String(), staff.ID.String()).Scan(&promotedEventCount); err != nil {
		t.Fatalf("query promoted event: %v", err)
	}
	if promotedEventCount != 1 {
		t.Fatalf("agent.promoted event count = %d, want 1", promotedEventCount)
	}
}

func ptrUUID(v uuid.UUID) *uuid.UUID {
	return &v
}

func ptrString(v string) *string {
	return &v
}
