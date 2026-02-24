package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRegisterStepReplacesCreateStarterTrioStub(t *testing.T) {
	b := NewBootstrapper()

	called := 0
	b.RegisterStep("create-starter-trio", func(context.Context, *State) error {
		called++
		return nil
	})

	err := b.Run(context.Background(), &State{OrganizationID: uuid.New()})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("replacement step called %d times, want 1", called)
	}
}

func TestRunRecoversStepPanic(t *testing.T) {
	b := NewBootstrapper()
	b.RegisterStep("create-starter-trio", func(context.Context, *State) error {
		panic("boom")
	})

	err := b.Run(context.Background(), &State{OrganizationID: uuid.New()})
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("error %q does not mention panic", err)
	}
}

func TestMissingStarterTrioIdempotencySelection(t *testing.T) {
	existing := []repo.Agent{
		{DisplayName: "Frank", IsStarterTrio: true},
		{DisplayName: "Lori", IsStarterTrio: true},
		{DisplayName: "Worker Temp", IsStarterTrio: false},
	}

	missing := missingStarterTrio(existing)
	if len(missing) != 1 {
		t.Fatalf("missing starter trio count = %d, want 1", len(missing))
	}
	if missing[0].displayName != "Ellie" {
		t.Fatalf("missing starter trio display name = %q, want %q", missing[0].displayName, "Ellie")
	}
}
