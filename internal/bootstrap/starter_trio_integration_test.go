//go:build integration

package bootstrap

import (
	"context"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRegisterStarterTrioStepCreatesAgentsIdempotently(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "bootstrap-agents", DisplayName: "Bootstrap Agents"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bootstrapper := NewBootstrapper(Options{DisableDefaultStep: true})
	RegisterStarterTrioStep(bootstrapper, agentRepo)
	state := &State{OrganizationID: org.ID}

	if err := bootstrapper.RunWithState(ctx, state); err != nil {
		t.Fatalf("first bootstrap run: %v", err)
	}
	if err := bootstrapper.RunWithState(ctx, state); err != nil {
		t.Fatalf("second bootstrap run: %v", err)
	}

	trio, err := agentRepo.GetStarterTrio(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetStarterTrio: %v", err)
	}
	if len(trio) != 3 {
		t.Fatalf("starter trio count = %d, want 3", len(trio))
	}

	names := make(map[string]int, len(trio))
	for _, agent := range trio {
		names[agent.DisplayName]++
		if agent.AgentClass != "staff" {
			t.Fatalf("%s agent_class = %q, want %q", agent.DisplayName, agent.AgentClass, "staff")
		}
		if agent.LifecycleStatus != "active" {
			t.Fatalf("%s lifecycle_status = %q, want %q", agent.DisplayName, agent.LifecycleStatus, "active")
		}
		if !agent.IsStarterTrio {
			t.Fatalf("%s should be starter trio", agent.DisplayName)
		}
		if agent.PrivateMemory {
			t.Fatalf("%s private_memory = true, want false", agent.DisplayName)
		}
	}

	for _, expected := range []string{"Frank", "Lori", "Ellie"} {
		if names[expected] != 1 {
			t.Fatalf("starter trio %s count = %d, want 1", expected, names[expected])
		}
	}
}
