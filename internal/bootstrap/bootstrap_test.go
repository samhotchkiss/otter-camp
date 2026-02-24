package bootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRegisterStepReplacesCreateAgentsStub(t *testing.T) {
	b := newNoopBootstrapper()

	called := 0
	b.RegisterStep("create-agents", func(context.Context, *State) error {
		called++
		return nil
	})

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("replacement step called %d times, want 1", called)
	}
}

func TestRegisterStepPreservesOrder(t *testing.T) {
	b := NewBootstrapper(Options{})

	before := b.StepNames()
	b.RegisterStep("create-agents", func(context.Context, *State) error { return nil })
	after := b.StepNames()

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("step order changed: before=%v after=%v", before, after)
	}
}

func TestRunRecoversStepPanic(t *testing.T) {
	b := newNoopBootstrapper()
	b.RegisterStep("create-agents", func(context.Context, *State) error {
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

func TestLoadDefaultSkillSeedsRequiresCoreSet(t *testing.T) {
	_, err := loadDefaultSkillSeeds(fstest.MapFS{
		"defaults/skills/summarize.md": {Data: []byte("# Summarize")},
	})
	if err == nil {
		t.Fatal("expected missing embedded skills error")
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

func newNoopBootstrapper() *Bootstrapper {
	b := NewBootstrapper(Options{})
	for _, name := range b.StepNames() {
		stepName := name
		b.RegisterStep(stepName, func(context.Context, *State) error { return nil })
	}
	return b
}
