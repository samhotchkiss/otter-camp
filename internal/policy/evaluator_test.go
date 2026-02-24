package policy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type fakePolicyRepo struct {
	policies []repo.CapabilityPolicy
	calls    map[string]int
}

func (f *fakePolicyRepo) ListByLayer(_ context.Context, layer string, capability string, organizationID, projectID, agentID *uuid.UUID) ([]repo.CapabilityPolicy, error) {
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[layer]++

	out := make([]repo.CapabilityPolicy, 0)
	for _, item := range f.policies {
		if item.PolicyLayer != layer {
			continue
		}
		if capability != "" && item.Capability != capability {
			continue
		}
		switch layer {
		case "org":
			if organizationID == nil || item.OrganizationID == nil || *organizationID != *item.OrganizationID {
				continue
			}
		case "project":
			if projectID == nil || item.ProjectID == nil || *projectID != *item.ProjectID {
				continue
			}
		case "agent_profile":
			if agentID == nil || item.AgentID == nil || *agentID != *item.AgentID {
				continue
			}
		case "request":
			if organizationID == nil || item.OrganizationID == nil || *organizationID != *item.OrganizationID {
				continue
			}
			if item.ProjectID != nil && (projectID == nil || *projectID != *item.ProjectID) {
				continue
			}
			if item.AgentID != nil && (agentID == nil || *agentID != *item.AgentID) {
				continue
			}
		}
		out = append(out, item)
	}
	return out, nil
}

type fakeBudgetService struct {
	result *budget.BudgetCheckResult
	err    error
}

func (f *fakeBudgetService) CheckBudget(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, int64) (*budget.BudgetCheckResult, error) {
	return f.result, f.err
}

func (f *fakeBudgetService) RecordUsage(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, int64) error {
	return nil
}

func (f *fakeBudgetService) ScanForAnomalies(context.Context) error {
	return nil
}

func (f *fakeBudgetService) RegisterJobs(budget.JobRegistrar) {}

func (f *fakeBudgetService) Create(context.Context, budget.CreateBudgetRequest) (*budget.TokenBudget, error) {
	return nil, nil
}

func (f *fakeBudgetService) Update(context.Context, uuid.UUID, budget.UpdateBudgetRequest) (*budget.TokenBudget, error) {
	return nil, nil
}

func (f *fakeBudgetService) Delete(context.Context, uuid.UUID) error {
	return nil
}

func (f *fakeBudgetService) List(context.Context, uuid.UUID) ([]*budget.TokenBudget, error) {
	return nil, nil
}

func mustJSON(t *testing.T, value map[string]any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return encoded
}

func TestEvaluatePrecedence(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	capability := "system.file.write"

	cases := []struct {
		name       string
		policies   []repo.CapabilityPolicy
		wantLayer  string
		wantEffect string
	}{
		{
			name: "instance deny overrides all lower allows",
			policies: []repo.CapabilityPolicy{
				{PolicyLayer: "instance", Capability: capability, Effect: "deny"},
				{PolicyLayer: "org", OrganizationID: &orgID, Capability: capability, Effect: "allow"},
				{PolicyLayer: "project", OrganizationID: &orgID, ProjectID: &projectID, Capability: capability, Effect: "allow"},
				{PolicyLayer: "agent_profile", OrganizationID: &orgID, AgentID: &agentID, Capability: capability, Effect: "allow"},
			},
			wantLayer:  "instance",
			wantEffect: "deny",
		},
		{
			name: "org deny overrides agent allow",
			policies: []repo.CapabilityPolicy{
				{PolicyLayer: "org", OrganizationID: &orgID, Capability: capability, Effect: "deny"},
				{PolicyLayer: "agent_profile", OrganizationID: &orgID, AgentID: &agentID, Capability: capability, Effect: "allow"},
			},
			wantLayer:  "org",
			wantEffect: "deny",
		},
		{
			name: "project deny overrides agent allow",
			policies: []repo.CapabilityPolicy{
				{PolicyLayer: "org", OrganizationID: &orgID, Capability: capability, Effect: "allow"},
				{PolicyLayer: "project", OrganizationID: &orgID, ProjectID: &projectID, Capability: capability, Effect: "deny"},
				{PolicyLayer: "agent_profile", OrganizationID: &orgID, AgentID: &agentID, Capability: capability, Effect: "allow"},
			},
			wantLayer:  "project",
			wantEffect: "deny",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policies := &fakePolicyRepo{policies: tc.policies}
			evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
				Policies: policies,
				Clock:    clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)),
			})
			if err != nil {
				t.Fatalf("NewPolicyEvaluator: %v", err)
			}
			if err := evaluator.LoadInstancePolicies(context.Background()); err != nil {
				t.Fatalf("LoadInstancePolicies: %v", err)
			}

			decision := evaluator.Evaluate(context.Background(), EvaluationRequest{
				OrganizationID: orgID,
				ProjectID:      &projectID,
				AgentID:        &agentID,
				Capability:     capability,
			})
			if decision.Effect != tc.wantEffect {
				t.Fatalf("effect = %q, want %q", decision.Effect, tc.wantEffect)
			}
			if decision.Layer != tc.wantLayer {
				t.Fatalf("layer = %q, want %q", decision.Layer, tc.wantLayer)
			}
		})
	}
}

func TestEvaluateSilencePasses(t *testing.T) {
	evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
		Policies: &fakePolicyRepo{},
		Clock:    clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewPolicyEvaluator: %v", err)
	}

	decision := evaluator.Evaluate(context.Background(), EvaluationRequest{
		OrganizationID: uuid.New(),
		Capability:     "system.file.write",
	})
	if decision.Effect != "allow" || decision.Layer != "none" || decision.Reason != "silence passes" {
		t.Fatalf("silence decision = %+v, want allow/none/silence passes", decision)
	}
}

func TestEvaluateConditionsAndFallthrough(t *testing.T) {
	orgID := uuid.New()
	capability := "system.file.write"
	policies := &fakePolicyRepo{
		policies: []repo.CapabilityPolicy{
			{
				PolicyLayer:    "org",
				OrganizationID: &orgID,
				Capability:     capability,
				Effect:         "allow",
				Conditions:     mustJSON(t, map[string]any{"max_file_size_kb": 512}),
			},
		},
	}

	evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
		Policies: policies,
		Clock:    clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewPolicyEvaluator: %v", err)
	}

	allow := evaluator.Evaluate(context.Background(), EvaluationRequest{
		OrganizationID: orgID,
		Capability:     capability,
		Context:        map[string]any{"file_size_kb": 256},
	})
	if allow.Effect != "allow" || allow.Layer != "org" {
		t.Fatalf("condition-allow decision = %+v, want allow/org", allow)
	}

	fallthroughDecision := evaluator.Evaluate(context.Background(), EvaluationRequest{
		OrganizationID: orgID,
		Capability:     capability,
		Context:        map[string]any{"file_size_kb": 1024},
	})
	if fallthroughDecision.Effect != "allow" || fallthroughDecision.Layer != "none" {
		t.Fatalf("condition-fallthrough decision = %+v, want allow/none", fallthroughDecision)
	}
}

func TestEvaluateDenyWinsWithinLayer(t *testing.T) {
	orgID := uuid.New()
	capability := "browser.navigate"
	policies := &fakePolicyRepo{
		policies: []repo.CapabilityPolicy{
			{
				PolicyLayer:    "org",
				OrganizationID: &orgID,
				Capability:     capability,
				Effect:         "allow",
				Priority:       50,
			},
			{
				PolicyLayer:    "org",
				OrganizationID: &orgID,
				Capability:     capability,
				Effect:         "deny",
				Priority:       100,
			},
		},
	}
	evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
		Policies: policies,
		Clock:    clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewPolicyEvaluator: %v", err)
	}

	decision := evaluator.Evaluate(context.Background(), EvaluationRequest{
		OrganizationID: orgID,
		Capability:     capability,
	})
	if decision.Effect != "deny" || decision.Layer != "org" {
		t.Fatalf("deny-wins decision = %+v, want deny/org", decision)
	}
}

func TestLoadInstancePoliciesUsesCachedInstanceRules(t *testing.T) {
	capability := "system.file.write"
	policies := &fakePolicyRepo{
		policies: []repo.CapabilityPolicy{
			{PolicyLayer: "instance", Capability: capability, Effect: "deny"},
		},
	}
	evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
		Policies: policies,
		Clock:    clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewPolicyEvaluator: %v", err)
	}
	if err := evaluator.LoadInstancePolicies(context.Background()); err != nil {
		t.Fatalf("LoadInstancePolicies: %v", err)
	}

	policies.policies = nil
	first := evaluator.Evaluate(context.Background(), EvaluationRequest{
		OrganizationID: uuid.New(),
		Capability:     capability,
	})
	second := evaluator.Evaluate(context.Background(), EvaluationRequest{
		OrganizationID: uuid.New(),
		Capability:     capability,
	})
	if first.Effect != "deny" || second.Effect != "deny" {
		t.Fatalf("cached instance decisions = %+v and %+v, want deny", first, second)
	}
	if policies.calls["instance"] != 1 {
		t.Fatalf("instance repo calls = %d, want 1 (load only)", policies.calls["instance"])
	}
}

func TestCheckBudgetGateHardLimitDenied(t *testing.T) {
	evaluator, err := NewPolicyEvaluator(EvaluatorOptions{
		Policies: &fakePolicyRepo{},
		Budgets: &fakeBudgetService{
			result: &budget.BudgetCheckResult{
				Allowed:      false,
				HardLimitHit: true,
			},
		},
		Clock: clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewPolicyEvaluator: %v", err)
	}

	allowed, reason := evaluator.CheckBudgetGate(context.Background(), uuid.New(), nil, nil)
	if allowed {
		t.Fatalf("allowed = true, want false")
	}
	if reason == "" {
		t.Fatalf("reason should be non-empty")
	}
}
