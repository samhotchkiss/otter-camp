//go:build integration

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
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

	t.Setenv("OTTERCAMP_ORG_SLUG", "bootstrap-agents")
	t.Setenv("OTTERCAMP_ORG_NAME", "Bootstrap Agents")
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "")

	store, err := storage.New(storage.Config{
		Backend: storage.BackendFS,
		FSRoot:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	bootstrapper, err := New(Options{
		Pool:    pool,
		Store:   store,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test-version",
	})
	if err != nil {
		t.Fatalf("new bootstrapper: %v", err)
	}

	RegisterStarterTrioStep(bootstrapper, agentRepo)

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("first bootstrap run: %v", err)
	}
	if err := bootstrapper.Run(ctx); err != nil {
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
