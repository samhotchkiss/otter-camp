package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

func TestBuildSystemTemplateNodeSeedPlan(t *testing.T) {
	templateID := uuid.New()
	startID := uuid.New()
	existingNodeID := uuid.New()

	t.Run("single-agent template seeds work then review", func(t *testing.T) {
		plan, err := buildSystemTemplateNodeSeedPlan(repo.FlowTemplate{
			ID:       templateID,
			Slug:     "default-single-agent",
			IsSystem: true,
		}, nil)
		if err != nil {
			t.Fatalf("buildSystemTemplateNodeSeedPlan: %v", err)
		}
		if plan.StartNodeID != nil {
			t.Fatalf("StartNodeID = %v, want nil", plan.StartNodeID)
		}
		if len(plan.Seeds) != 3 {
			t.Fatalf("seed count = %d, want 3", len(plan.Seeds))
		}
		if plan.Seeds[0].NodeType != "work" {
			t.Fatalf("seed node_type = %q, want %q", plan.Seeds[0].NodeType, "work")
		}
		if plan.Seeds[0].ActorType == nil || *plan.Seeds[0].ActorType != "role" {
			t.Fatalf("seed actor_type = %v, want role", plan.Seeds[0].ActorType)
		}
		if plan.Seeds[0].NextSeedIndex == nil || *plan.Seeds[0].NextSeedIndex != 1 {
			t.Fatalf("seed NextSeedIndex = %v, want 1", plan.Seeds[0].NextSeedIndex)
		}
		if plan.Seeds[1].NodeType != "review" {
			t.Fatalf("second node_type = %q, want %q", plan.Seeds[1].NodeType, "review")
		}
		if plan.Seeds[2].NodeType != "merge" {
			t.Fatalf("third node_type = %q, want %q", plan.Seeds[2].NodeType, "merge")
		}
	})

	t.Run("review template seeds work then internal review with edge", func(t *testing.T) {
		plan, err := buildSystemTemplateNodeSeedPlan(repo.FlowTemplate{
			ID:       templateID,
			Slug:     "default-review",
			IsSystem: true,
		}, nil)
		if err != nil {
			t.Fatalf("buildSystemTemplateNodeSeedPlan: %v", err)
		}
		if len(plan.Seeds) != 3 {
			t.Fatalf("seed count = %d, want 3", len(plan.Seeds))
		}
		if plan.Seeds[0].NodeType != "work" {
			t.Fatalf("first node_type = %q, want %q", plan.Seeds[0].NodeType, "work")
		}
		if plan.Seeds[0].NextSeedIndex == nil || *plan.Seeds[0].NextSeedIndex != 1 {
			t.Fatalf("first NextSeedIndex = %v, want 1", plan.Seeds[0].NextSeedIndex)
		}
		if plan.Seeds[1].NodeType != "review" {
			t.Fatalf("second node_type = %q, want %q", plan.Seeds[1].NodeType, "review")
		}
		if plan.Seeds[1].DisplayName != "Internal Review" {
			t.Fatalf("second display_name = %q, want Internal Review", plan.Seeds[1].DisplayName)
		}
		if plan.Seeds[1].RequiresHumanReview {
			t.Fatal("second node RequiresHumanReview = true, want false")
		}
		if plan.Seeds[2].NodeType != "merge" {
			t.Fatalf("third node_type = %q, want %q", plan.Seeds[2].NodeType, "merge")
		}
	})

	t.Run("review refinement template seeds generation internal review then human review", func(t *testing.T) {
		plan, err := buildSystemTemplateNodeSeedPlan(repo.FlowTemplate{
			ID:       templateID,
			Slug:     "default-review-refinement",
			IsSystem: true,
		}, nil)
		if err != nil {
			t.Fatalf("buildSystemTemplateNodeSeedPlan: %v", err)
		}
		if len(plan.Seeds) != 4 {
			t.Fatalf("seed count = %d, want 4", len(plan.Seeds))
		}
		if plan.Seeds[0].DisplayName != "Generation" {
			t.Fatalf("first display_name = %q, want Generation", plan.Seeds[0].DisplayName)
		}
		if plan.Seeds[1].DisplayName != "Internal Review" {
			t.Fatalf("second display_name = %q, want Internal Review", plan.Seeds[1].DisplayName)
		}
		if plan.Seeds[2].DisplayName != "Human Review" {
			t.Fatalf("third display_name = %q, want Human Review", plan.Seeds[2].DisplayName)
		}
		if !plan.Seeds[2].RequiresHumanReview {
			t.Fatal("third node RequiresHumanReview = false, want true")
		}
		if plan.Seeds[3].NodeType != "merge" {
			t.Fatalf("fourth node_type = %q, want %q", plan.Seeds[3].NodeType, "merge")
		}
	})

	t.Run("mismatched existing nodes trigger reconcile instead of silent backfill", func(t *testing.T) {
		plan, err := buildSystemTemplateNodeSeedPlan(repo.FlowTemplate{
			ID:          templateID,
			Slug:        "default-single-agent",
			IsSystem:    true,
			StartNodeID: nil,
		}, []repo.FlowNode{
			{ID: existingNodeID, FlowTemplateID: templateID},
		})
		if err != nil {
			t.Fatalf("buildSystemTemplateNodeSeedPlan: %v", err)
		}
		if len(plan.Seeds) != 3 {
			t.Fatalf("seed count = %d, want 3 for reconcile", len(plan.Seeds))
		}
		if !plan.Reconcile {
			t.Fatal("Reconcile = false, want true for mismatched existing nodes")
		}
		if plan.StartNodeID != nil {
			t.Fatalf("StartNodeID = %v, want nil during reconcile", plan.StartNodeID)
		}
	})

	t.Run("mismatched existing nodes with start node still reconcile", func(t *testing.T) {
		plan, err := buildSystemTemplateNodeSeedPlan(repo.FlowTemplate{
			ID:          templateID,
			Slug:        "default-single-agent",
			IsSystem:    true,
			StartNodeID: &startID,
		}, []repo.FlowNode{
			{ID: existingNodeID, FlowTemplateID: templateID},
		})
		if err != nil {
			t.Fatalf("buildSystemTemplateNodeSeedPlan: %v", err)
		}
		if len(plan.Seeds) != 3 {
			t.Fatalf("seed count = %d, want 3 for reconcile", len(plan.Seeds))
		}
		if plan.StartNodeID != nil {
			t.Fatalf("StartNodeID = %v, want nil", plan.StartNodeID)
		}
		if !plan.Reconcile {
			t.Fatal("Reconcile = false, want true for mismatched existing nodes")
		}
	})
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

func TestAdminUserExists(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: boolRow(true),
			},
		}

		exists, err := b.adminUserExists(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("adminUserExists returned error: %v", err)
		}
		if !exists {
			t.Fatal("adminUserExists = false, want true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: boolRow(false),
			},
		}

		exists, err := b.adminUserExists(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("adminUserExists returned error: %v", err)
		}
		if exists {
			t.Fatal("adminUserExists = true, want false")
		}
	})

	t.Run("scan error", func(t *testing.T) {
		expected := errors.New("db failure")
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: errRow(expected),
			},
		}

		_, err := b.adminUserExists(context.Background(), uuid.New())
		if !errors.Is(err, expected) {
			t.Fatalf("adminUserExists error = %v, want %v", err, expected)
		}
	})
}

func TestCurrentOrgProfileExists(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: boolRow(true),
			},
		}

		exists, err := b.currentOrgProfileExists(context.Background(), uuid.New(), "standard")
		if err != nil {
			t.Fatalf("currentOrgProfileExists returned error: %v", err)
		}
		if !exists {
			t.Fatal("currentOrgProfileExists = false, want true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: boolRow(false),
			},
		}

		exists, err := b.currentOrgProfileExists(context.Background(), uuid.New(), "standard")
		if err != nil {
			t.Fatalf("currentOrgProfileExists returned error: %v", err)
		}
		if exists {
			t.Fatal("currentOrgProfileExists = true, want false")
		}
	})

	t.Run("scan error", func(t *testing.T) {
		expected := errors.New("db failure")
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: errRow(expected),
			},
		}

		_, err := b.currentOrgProfileExists(context.Background(), uuid.New(), "standard")
		if !errors.Is(err, expected) {
			t.Fatalf("currentOrgProfileExists error = %v, want %v", err, expected)
		}
	})
}

func TestBootstrapAuditEventExists(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: boolRow(true),
			},
		}

		exists, err := b.bootstrapAuditEventExists(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("bootstrapAuditEventExists returned error: %v", err)
		}
		if !exists {
			t.Fatal("bootstrapAuditEventExists = false, want true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: boolRow(false),
			},
		}

		exists, err := b.bootstrapAuditEventExists(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("bootstrapAuditEventExists returned error: %v", err)
		}
		if exists {
			t.Fatal("bootstrapAuditEventExists = true, want false")
		}
	})

	t.Run("scan error", func(t *testing.T) {
		expected := errors.New("db failure")
		b := &Bootstrapper{
			rowQuerier: fakeRowQuerier{
				row: errRow(expected),
			},
		}

		_, err := b.bootstrapAuditEventExists(context.Background(), uuid.New())
		if !errors.Is(err, expected) {
			t.Fatalf("bootstrapAuditEventExists error = %v, want %v", err, expected)
		}
	})
}

type fakeRowQuerier struct {
	row pgx.Row
}

func (f fakeRowQuerier) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return f.row
}

type fakeRow struct {
	scanFn func(dest ...any) error
}

func (f fakeRow) Scan(dest ...any) error {
	return f.scanFn(dest...)
}

func boolRow(value bool) pgx.Row {
	return fakeRow{
		scanFn: func(dest ...any) error {
			target, ok := dest[0].(*bool)
			if !ok {
				return errors.New("expected *bool scan target")
			}
			*target = value
			return nil
		},
	}
}

func errRow(err error) pgx.Row {
	return fakeRow{
		scanFn: func(_ ...any) error {
			return err
		},
	}
}
