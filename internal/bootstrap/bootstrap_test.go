package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRegisterStepReplacesStep7AliasAndPreservesOrder(t *testing.T) {
	b := NewBootstrapper(Options{DisableDefaultStep: true})

	order := []string{}
	b.RegisterStep("one", func(context.Context, *State) error {
		order = append(order, "one")
		return nil
	})
	b.RegisterStep("create-starter-trio", func(context.Context, *State) error {
		order = append(order, "stub")
		return nil
	})
	b.RegisterStep("three", func(context.Context, *State) error {
		order = append(order, "three")
		return nil
	})
	b.RegisterStep("create-agents", func(context.Context, *State) error {
		order = append(order, "replacement")
		return nil
	})

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := strings.Join(order, ",")
	want := "one,replacement,three"
	if got != want {
		t.Fatalf("step execution order = %q, want %q", got, want)
	}

	names := strings.Join(b.StepNames(), ",")
	if names != "one,create-starter-trio,three" {
		t.Fatalf("step names = %q, want %q", names, "one,create-starter-trio,three")
	}
}

func TestRunRecoversStepPanic(t *testing.T) {
	b := NewBootstrapper(Options{DisableDefaultStep: true})
	b.RegisterStep("panic-step", func(context.Context, *State) error {
		panic("boom")
	})

	err := b.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("error %q does not mention panic", err)
	}
}

func TestProfileNeedsRotation(t *testing.T) {
	base := repo.ModelProfile{
		ProviderID:          uuid.New(),
		ModelName:           "model-a",
		ContextWindowTokens: 1000,
		MaxOutputTokens:     100,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	}

	if profileNeedsRotation(base, base) {
		t.Fatal("expected no rotation when profile matches")
	}

	next := base
	next.ModelName = "model-b"
	if !profileNeedsRotation(base, next) {
		t.Fatal("expected rotation when model name changes")
	}
}

func TestNormalizeStepNameAlias(t *testing.T) {
	if got := normalizeStepName("create-agents"); got != "create-starter-trio" {
		t.Fatalf("normalized step name = %q, want %q", got, "create-starter-trio")
	}
}

func TestSkippedError(t *testing.T) {
	err := skipped("already bootstrapped")
	var skipErr skipStep
	if !errors.As(err, &skipErr) {
		t.Fatalf("expected skipStep error, got %T", err)
	}
	if skipErr.Reason() != "already bootstrapped" {
		t.Fatalf("skip reason = %q, want %q", skipErr.Reason(), "already bootstrapped")
	}
}
