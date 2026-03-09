//go:build integration

package bootstrap

import (
	"context"
	"strings"
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

func TestStarterTrioLoriPromptIncludesStaffingWorkflow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "bootstrap-lori-prompt", DisplayName: "Bootstrap Lori Prompt"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bootstrapper := NewBootstrapper(Options{DisableDefaultStep: true})
	RegisterStarterTrioStep(bootstrapper, agentRepo)
	if err := bootstrapper.RunWithState(ctx, &State{OrganizationID: org.ID}); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	trio, err := agentRepo.GetStarterTrio(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetStarterTrio: %v", err)
	}

	var loriPrompt string
	for _, agent := range trio {
		if agent.DisplayName == "Lori" {
			loriPrompt = agent.SystemPrompt
			break
		}
	}
	if loriPrompt == "" {
		t.Fatal("missing Lori prompt in starter trio")
	}

	for _, snippet := range []string{
		"project.staffing_needed",
		"PM recommendation",
		"worker/reviewer recommendations",
		"Project kickoff is a forward-progress phase, not an approval gate.",
		"there must be real persisted setup in the project",
		"Use your native tools in the same kickoff turn",
		"Default target size is 30 minutes or less",
		"Each task or subtask should produce one concrete output",
	} {
		if !strings.Contains(loriPrompt, snippet) {
			t.Fatalf("Lori prompt missing %q", snippet)
		}
	}
}
