//go:build integration

package policy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

type fixedUsageQuerier struct {
	tokens int64
}

func (f fixedUsageQuerier) SumTokens(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, time.Time) (int64, error) {
	return f.tokens, nil
}

func (f fixedUsageQuerier) DailyRollups(context.Context, uuid.UUID, *uuid.UUID, int) ([]budget.DailyRollup, error) {
	return nil, nil
}

func TestPolicyCacheTTLStaleThenRefresh(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	policyRepo := repo.NewCapabilityPolicyRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "policy-cache-org-" + uuid.NewString()[:8],
		DisplayName: "Policy Cache Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	created, err := policyRepo.Create(ctx, repo.CapabilityPolicy{
		PolicyLayer:    "org",
		OrganizationID: &org.ID,
		Capability:     "system.file.write",
		Effect:         "deny",
		Priority:       100,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("create org deny policy: %v", err)
	}

	fakeClock := clock.NewFake(time.Date(2026, time.February, 24, 12, 0, 0, 0, time.UTC))
	evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
		Policies: policyRepo,
		Clock:    fakeClock,
	})
	if err != nil {
		t.Fatalf("NewPolicyEvaluator: %v", err)
	}

	first := evaluator.Evaluate(ctx, EvaluationRequest{
		OrganizationID: org.ID,
		Capability:     "system.file.write",
	})
	if first.Effect != "deny" || first.Layer != "org" {
		t.Fatalf("first decision = %+v, want deny/org", first)
	}

	created.Effect = "allow"
	if _, err := policyRepo.Update(ctx, created); err != nil {
		t.Fatalf("update org policy to allow: %v", err)
	}

	stale := evaluator.Evaluate(ctx, EvaluationRequest{
		OrganizationID: org.ID,
		Capability:     "system.file.write",
	})
	if stale.Effect != "deny" || stale.Layer != "org" {
		t.Fatalf("stale decision = %+v, want cached deny/org", stale)
	}

	fakeClock.Advance(6 * time.Minute)

	fresh := evaluator.Evaluate(ctx, EvaluationRequest{
		OrganizationID: org.ID,
		Capability:     "system.file.write",
	})
	if fresh.Effect != "allow" || fresh.Layer != "org" {
		t.Fatalf("fresh decision = %+v, want allow/org after TTL expiry", fresh)
	}
}

func TestCheckBudgetGateDeniesWhenHardLimitExceeded(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	tokenBudgetRepo := repo.NewTokenBudgetRepo(pool)
	policyRepo := repo.NewCapabilityPolicyRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "policy-budget-org-" + uuid.NewString()[:8],
		DisplayName: "Policy Budget Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	hardLimit := int64(100)
	if _, err := tokenBudgetRepo.Create(ctx, repo.TokenBudget{
		OrganizationID:  org.ID,
		Period:          "daily",
		HardLimitTokens: &hardLimit,
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	}); err != nil {
		t.Fatalf("create token budget: %v", err)
	}

	budgetService, err := budget.NewService(budget.Options{
		Pool:    pool,
		Budgets: tokenBudgetRepo,
		Usage:   fixedUsageQuerier{tokens: 100},
	})
	if err != nil {
		t.Fatalf("budget.NewService: %v", err)
	}

	evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
		Policies: policyRepo,
		Budgets:  budgetService,
		Clock:    clock.NewFake(time.Date(2026, time.February, 24, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewPolicyEvaluator: %v", err)
	}

	allowed, reason := evaluator.CheckBudgetGate(ctx, org.ID, nil, nil)
	if allowed {
		t.Fatalf("allowed = true, want false")
	}
	if !strings.Contains(reason, "hard limit") {
		t.Fatalf("reason = %q, want hard limit message", reason)
	}
}
