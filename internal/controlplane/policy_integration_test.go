//go:build integration

package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestPolicy_Eval_Allow(t *testing.T) {
	ctx := context.Background()
	fx := newPolicyFixture(t)
	defer fx.Close()

	mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
		PolicyLayer:    "org",
		OrganizationID: &fx.Org.ID,
		Capability:     "system.file.write",
		Effect:         "allow",
		Priority:       100,
		CreatedByType:  "system",
	})

	evaluator := newPolicyEvaluator(t, fx.Pool)
	policySvc := &evaluatorCapabilityPolicyService{
		evaluator: evaluator,
	}
	broker := newPolicyBroker(t, fx.Pool, policySvc)

	mustCreateToolDefinition(t, fx.Pool, repo.ToolDefinition{
		Name:               "tooltest.file.write",
		DisplayName:        "File Write",
		Description:        "Write file",
		ToolTier:           "tier2",
		ToolDomain:         "native",
		RequiredCapability: strPointer("system.file.write"),
		IsEnabled:          true,
	})

	execRecord, err := broker.Dispatch(ctx, controlplane.DispatchInput{
		RunID:        &fx.Run.ID,
		RunStepID:    &fx.Step.ID,
		RunAttemptID: &fx.Attempt.ID,
		AgentID:      fx.Agent.ID,
		ToolName:     "tooltest.file.write",
		ToolTier:     "tier2",
		Input:        map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if execRecord.PolicyDecision != "allowed" {
		t.Fatalf("policy_decision = %q, want allowed", execRecord.PolicyDecision)
	}
}

func TestPolicy_Eval_Deny_Absolute(t *testing.T) {
	ctx := context.Background()
	fx := newPolicyFixture(t)
	defer fx.Close()

	otherAgent := mustCreateAgent(t, fx.Pool, fx.Org.ID, policyAgentOptions{
		DisplayName:   "Policy Agent B",
		ToolAllow:     []string{},
		ToolDeny:      []string{},
		BudgetCap:     nil,
		CreatedByType: "system",
		CreatedByID:   uuid.Nil,
	})

	pairs := []struct {
		name  string
		setup func()
		req   policy.EvaluationRequest
	}{
		{
			name: "org deny beats project allow",
			setup: func() {
				mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
					PolicyLayer:    "org",
					OrganizationID: &fx.Org.ID,
					Capability:     "system.file.write",
					Effect:         "deny",
					Priority:       100,
					CreatedByType:  "system",
				})
				mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
					PolicyLayer:    "project",
					OrganizationID: &fx.Org.ID,
					ProjectID:      &fx.Project.ID,
					Capability:     "system.file.write",
					Effect:         "allow",
					Priority:       100,
					CreatedByType:  "system",
				})
			},
			req: policy.EvaluationRequest{
				OrganizationID: fx.Org.ID,
				ProjectID:      &fx.Project.ID,
				AgentID:        &fx.Agent.ID,
				Capability:     "system.file.write",
			},
		},
		{
			name: "instance deny beats org allow",
			setup: func() {
				mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
					PolicyLayer:   "instance",
					Capability:    "system.browser.interact",
					Effect:        "deny",
					Priority:      100,
					CreatedByType: "system",
				})
				mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
					PolicyLayer:    "org",
					OrganizationID: &fx.Org.ID,
					Capability:     "system.browser.interact",
					Effect:         "allow",
					Priority:       100,
					CreatedByType:  "system",
				})
			},
			req: policy.EvaluationRequest{
				OrganizationID: fx.Org.ID,
				ProjectID:      &fx.Project.ID,
				AgentID:        &fx.Agent.ID,
				Capability:     "system.browser.interact",
			},
		},
		{
			name: "project deny beats agent allow",
			setup: func() {
				mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
					PolicyLayer:    "project",
					OrganizationID: &fx.Org.ID,
					ProjectID:      &fx.Project.ID,
					Capability:     "system.file.read",
					Effect:         "deny",
					Priority:       100,
					CreatedByType:  "system",
				})
				mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
					PolicyLayer:    "agent_profile",
					OrganizationID: &fx.Org.ID,
					AgentID:        &otherAgent.ID,
					Capability:     "system.file.read",
					Effect:         "allow",
					Priority:       100,
					CreatedByType:  "system",
				})
			},
			req: policy.EvaluationRequest{
				OrganizationID: fx.Org.ID,
				ProjectID:      &fx.Project.ID,
				AgentID:        &otherAgent.ID,
				Capability:     "system.file.read",
			},
		},
	}

	evaluator := newPolicyEvaluator(t, fx.Pool)
	for _, pair := range pairs {
		pair.setup()
		if err := evaluator.LoadInstancePolicies(ctx); err != nil {
			t.Fatalf("LoadInstancePolicies (%s): %v", pair.name, err)
		}
		decision := evaluator.Evaluate(ctx, pair.req)
		if decision.Effect != "deny" {
			t.Fatalf("%s: effect = %s, want deny", pair.name, decision.Effect)
		}
	}

	policySvc := &evaluatorCapabilityPolicyService{evaluator: evaluator}
	broker := newPolicyBroker(t, fx.Pool, policySvc)
	mustCreateToolDefinition(t, fx.Pool, repo.ToolDefinition{
		Name:               "tooltest.browser.navigate",
		DisplayName:        "Browser Navigate",
		Description:        "Navigate browser",
		ToolTier:           "tier2",
		ToolDomain:         "native",
		RequiredCapability: strPointer("system.browser.interact"),
		IsEnabled:          true,
	})
	execRecord, err := broker.Dispatch(ctx, controlplane.DispatchInput{
		RunID:        &fx.Run.ID,
		RunStepID:    &fx.Step.ID,
		RunAttemptID: &fx.Attempt.ID,
		AgentID:      fx.Agent.ID,
		ToolName:     "tooltest.browser.navigate",
		ToolTier:     "tier2",
		Input:        map[string]any{"url": "https://example.com"},
	})
	if !errors.Is(err, controlplane.ErrCapabilityDenied) {
		t.Fatalf("Dispatch error = %v, want ErrCapabilityDenied", err)
	}
	if execRecord.PolicyDecision != "denied" {
		t.Fatalf("policy_decision = %q, want denied", execRecord.PolicyDecision)
	}
}

func TestPolicy_Eval_InstanceLayer_Unoverridable(t *testing.T) {
	ctx := context.Background()
	fx := newPolicyFixture(t)
	defer fx.Close()

	mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
		PolicyLayer:   "instance",
		Capability:    "system.browser.interact",
		Effect:        "deny",
		Priority:      100,
		CreatedByType: "system",
	})
	mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
		PolicyLayer:    "org",
		OrganizationID: &fx.Org.ID,
		Capability:     "system.browser.interact",
		Effect:         "allow",
		Priority:       100,
		CreatedByType:  "system",
	})

	evaluator := newPolicyEvaluator(t, fx.Pool)
	if err := evaluator.LoadInstancePolicies(ctx); err != nil {
		t.Fatalf("LoadInstancePolicies: %v", err)
	}
	decision := evaluator.Evaluate(ctx, policy.EvaluationRequest{
		OrganizationID: fx.Org.ID,
		ProjectID:      &fx.Project.ID,
		AgentID:        &fx.Agent.ID,
		Capability:     "system.browser.interact",
	})
	if decision.Effect != "deny" || decision.Layer != "instance" {
		t.Fatalf("decision = (%s,%s), want (deny,instance)", decision.Effect, decision.Layer)
	}

	httpServer := newPolicyHTTPServer(t)
	defer httpServer.Close()
	adminToken := loginToken(t, httpServer.URL, httpServer.Admin.Email, "admin-password")
	response := mustJSON(t, http.MethodPost, httpServer.URL+"/v1/control/policies", map[string]any{
		"policy_layer": "instance",
		"capability":   "system.browser.interact",
		"effect":       "deny",
		"priority":     100,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("POST instance policy status = %d, want %d body=%s", response.StatusCode, http.StatusForbidden, string(response.Body))
	}
	waitForAuditEvent(t, httpServer.Pool, httpServer.Org.ID, "policy.instance_write_attempt")
}

func TestPolicy_Eval_Escalate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "policy-escalate-org")
	projectRecord, taskRecord, targetUserID := seedEscalationProjectTaskWithPM(t, ctx, pool, org.ID)

	svc := newRunService(t, pool)
	runRecord, err := svc.CreateRun(ctx, controlplane.CreateRunInput{
		OrganizationID: org.ID,
		ProjectID:      &projectRecord.ID,
		TaskID:         &taskRecord.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := svc.PauseRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("PauseRun: %v", err)
	}

	item, err := repo.NewInboxItemRepo(pool).Create(ctx, repo.InboxItem{
		OrganizationID:  org.ID,
		TargetUserID:    &targetUserID,
		ItemType:        "system_alert",
		SourceProjectID: &projectRecord.ID,
		SourceTaskID:    &taskRecord.ID,
		CreatedByType:   "system",
		CreatedByID:     nil,
		Title:           "Policy escalation",
		Body:            strPointer("Approve capability use"),
		ActionPayload:   json.RawMessage(`{"requested_type":"escalation","decision":"approve"}`),
	})
	if err != nil {
		t.Fatalf("create escalation inbox item: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE inbox_item
		SET is_acted = true,
		    acted_at = now(),
		    acted_by_id = $2
		WHERE id = $1
	`, item.ID, targetUserID); err != nil {
		t.Fatalf("mark inbox item acted: %v", err)
	}
	if err := svc.ResumeRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}

	updatedRun, err := controlplane.NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "in_progress" {
		t.Fatalf("run status after approval = %s, want in_progress", updatedRun.Status)
	}
}

func TestPolicy_Eval_Silence_Passes(t *testing.T) {
	ctx := context.Background()
	fx := newPolicyFixture(t)
	defer fx.Close()

	evaluator := newPolicyEvaluator(t, fx.Pool)
	policySvc := &evaluatorCapabilityPolicyService{
		evaluator: evaluator,
	}
	broker := newPolicyBroker(t, fx.Pool, policySvc)

	mustCreateToolDefinition(t, fx.Pool, repo.ToolDefinition{
		Name:               "tooltest.file.read",
		DisplayName:        "File Read",
		Description:        "Read file",
		ToolTier:           "tier2",
		ToolDomain:         "native",
		RequiredCapability: strPointer("system.file.read"),
		IsEnabled:          true,
	})

	execRecord, err := broker.Dispatch(ctx, controlplane.DispatchInput{
		RunID:        &fx.Run.ID,
		RunStepID:    &fx.Step.ID,
		RunAttemptID: &fx.Attempt.ID,
		AgentID:      fx.Agent.ID,
		ToolName:     "tooltest.file.read",
		ToolTier:     "tier2",
		Input:        map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if execRecord.PolicyDecision != "allowed" {
		t.Fatalf("policy_decision = %q, want allowed", execRecord.PolicyDecision)
	}
}

func TestPolicy_Eval_BudgetGate(t *testing.T) {
	ctx := context.Background()
	fx := newPolicyFixture(t)
	defer fx.Close()

	usage := budget.NewPostgresUsageQuerier(fx.Pool)
	budgets, err := budget.NewService(budget.Options{
		Pool:   fx.Pool,
		Usage:  usage,
		Logger: discardLogger(),
	})
	if err != nil {
		t.Fatalf("new budget service: %v", err)
	}

	hardLimit := int64(10)
	if _, err := budgets.Create(ctx, budget.CreateBudgetRequest{
		OrganizationID:  fx.Org.ID,
		ProjectID:       nil,
		Period:          "daily",
		HardLimitTokens: &hardLimit,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	}); err != nil {
		t.Fatalf("create org hard budget: %v", err)
	}

	provider := mustCreateProvider(t, fx.Pool)
	insertModelInvocation(t, fx.Pool, repo.ModelInvocation{
		OrganizationID:    fx.Org.ID,
		ModelProviderID:   provider.ID,
		ProjectID:         &fx.Project.ID,
		AgentID:           &fx.Agent.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "gpt-4o-mini",
		InputTokens:       intPointer(8),
		OutputTokens:      intPointer(5),
	})

	evaluator, err := policy.NewPolicyEvaluator(policy.EvaluatorOptions{
		Policies: repo.NewCapabilityPolicyRepo(fx.Pool),
		Budgets:  budgets,
	})
	if err != nil {
		t.Fatalf("new policy evaluator: %v", err)
	}

	policySvc := &evaluatorCapabilityPolicyService{
		evaluator: evaluator,
		essential: map[string]bool{
			"system.notify.operator": true,
		},
	}

	allowed, reason := evaluator.CheckBudgetGate(ctx, fx.Org.ID, &fx.Project.ID, &fx.Agent.ID)
	if allowed {
		t.Fatalf("CheckBudgetGate allowed=%v reason=%q, want denied for exhausted org budget", allowed, reason)
	}

	decision, err := policySvc.EvaluateCapability(ctx, fx.Org.ID, &fx.Project.ID, fx.Agent.ID, "system.file.write")
	if err != nil {
		t.Fatalf("EvaluateCapability non-essential: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("non-essential capability allowed=%v, want denied", decision.Allowed)
	}
	if policySvc.evalCalls != 0 {
		t.Fatalf("policy eval calls = %d, want 0 when budget gate denies first", policySvc.evalCalls)
	}

	essentialDecision, err := policySvc.EvaluateCapability(ctx, fx.Org.ID, &fx.Project.ID, fx.Agent.ID, "system.notify.operator")
	if err != nil {
		t.Fatalf("EvaluateCapability essential: %v", err)
	}
	if !essentialDecision.Allowed {
		t.Fatalf("essential capability allowed=%v, want true", essentialDecision.Allowed)
	}

	agentCap := int64(13)
	if _, err := fx.Pool.Exec(ctx, `UPDATE agent SET budget_cap_tokens = $2, budget_period = 'daily' WHERE id = $1`, fx.Agent.ID, agentCap); err != nil {
		t.Fatalf("update agent budget cap: %v", err)
	}
	agentAllowed, _ := evaluator.CheckBudgetGate(ctx, fx.Org.ID, &fx.Project.ID, &fx.Agent.ID)
	if agentAllowed {
		t.Fatal("expected agent budget cap to deny after reaching cap")
	}

	since := time.Now().UTC().Add(-time.Hour)
	orgBefore := sumTokens(t, usage, fx.Org.ID, nil, nil, since)
	projectBefore := sumTokens(t, usage, fx.Org.ID, &fx.Project.ID, nil, since)
	agentBefore := sumTokens(t, usage, fx.Org.ID, nil, &fx.Agent.ID, since)

	insertModelInvocation(t, fx.Pool, repo.ModelInvocation{
		OrganizationID:    fx.Org.ID,
		ModelProviderID:   provider.ID,
		ProjectID:         &fx.Project.ID,
		AgentID:           &fx.Agent.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "gpt-4o-mini",
		InputTokens:       intPointer(7),
		OutputTokens:      intPointer(6),
	})

	orgAfter := sumTokens(t, usage, fx.Org.ID, nil, nil, since)
	projectAfter := sumTokens(t, usage, fx.Org.ID, &fx.Project.ID, nil, since)
	agentAfter := sumTokens(t, usage, fx.Org.ID, nil, &fx.Agent.ID, since)

	const invocationTokens int64 = 13
	if orgAfter-orgBefore != invocationTokens {
		t.Fatalf("org token delta = %d, want %d", orgAfter-orgBefore, invocationTokens)
	}
	if projectAfter-projectBefore != invocationTokens {
		t.Fatalf("project token delta = %d, want %d", projectAfter-projectBefore, invocationTokens)
	}
	if agentAfter-agentBefore != invocationTokens {
		t.Fatalf("agent token delta = %d, want %d", agentAfter-agentBefore, invocationTokens)
	}
}

func TestPolicy_Eval_AgentAllowDenyList(t *testing.T) {
	ctx := context.Background()
	fx := newPolicyFixtureWithAgentLists(t, []string{"tooltest.file.read", "tooltest.file.write"}, []string{"tooltest.file.write"})
	defer fx.Close()

	mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
		PolicyLayer:    "org",
		OrganizationID: &fx.Org.ID,
		Capability:     "system.file.read",
		Effect:         "allow",
		Priority:       100,
		CreatedByType:  "system",
	})
	mustCreateCapabilityPolicy(t, fx.Pool, repo.CapabilityPolicy{
		PolicyLayer:    "org",
		OrganizationID: &fx.Org.ID,
		Capability:     "system.file.write",
		Effect:         "allow",
		Priority:       100,
		CreatedByType:  "system",
	})

	evaluator := newPolicyEvaluator(t, fx.Pool)
	policySvc := &evaluatorCapabilityPolicyService{evaluator: evaluator}
	broker := newPolicyBroker(t, fx.Pool, policySvc)

	mustCreateToolDefinition(t, fx.Pool, repo.ToolDefinition{
		Name:               "tooltest.file.read",
		DisplayName:        "File Read",
		Description:        "Read file",
		ToolTier:           "tier2",
		ToolDomain:         "native",
		RequiredCapability: strPointer("system.file.read"),
		IsEnabled:          true,
	})
	mustCreateToolDefinition(t, fx.Pool, repo.ToolDefinition{
		Name:               "tooltest.file.write",
		DisplayName:        "File Write",
		Description:        "Write file",
		ToolTier:           "tier2",
		ToolDomain:         "native",
		RequiredCapability: strPointer("system.file.write"),
		IsEnabled:          true,
	})

	allowExec, err := broker.Dispatch(ctx, controlplane.DispatchInput{
		RunID:        &fx.Run.ID,
		RunStepID:    &fx.Step.ID,
		RunAttemptID: &fx.Attempt.ID,
		AgentID:      fx.Agent.ID,
		ToolName:     "tooltest.file.read",
		ToolTier:     "tier2",
		Input:        map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatalf("Dispatch file.read: %v", err)
	}
	if allowExec.PolicyDecision != "allowed" {
		t.Fatalf("file.read policy_decision = %q, want allowed", allowExec.PolicyDecision)
	}

	denyExec, err := broker.Dispatch(ctx, controlplane.DispatchInput{
		RunID:        &fx.Run.ID,
		RunStepID:    &fx.Step.ID,
		RunAttemptID: &fx.Attempt.ID,
		AgentID:      fx.Agent.ID,
		ToolName:     "tooltest.file.write",
		ToolTier:     "tier2",
		Input:        map[string]any{"path": "README.md", "content": "x"},
	})
	if !errors.Is(err, controlplane.ErrAgentDenyList) {
		t.Fatalf("Dispatch file.write error = %v, want ErrAgentDenyList", err)
	}
	if denyExec.PolicyDecision != "denied" {
		t.Fatalf("file.write policy_decision = %q, want denied", denyExec.PolicyDecision)
	}
}

type evaluatorCapabilityPolicyService struct {
	evaluator *policy.PolicyEvaluator
	essential map[string]bool

	budgetChecks int
	evalCalls    int
}

func (s *evaluatorCapabilityPolicyService) EvaluateCapability(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, agentID uuid.UUID, capability string) (controlplane.CapabilityDecision, error) {
	if s.evaluator == nil {
		return controlplane.CapabilityDecision{Allowed: true}, nil
	}

	agentIDCopy := agentID
	s.budgetChecks++
	allowed, reason := s.evaluator.CheckBudgetGate(ctx, organizationID, projectID, &agentIDCopy)
	if !allowed && !s.essential[capability] {
		return controlplane.CapabilityDecision{
			Allowed: false,
			Reason:  reason,
		}, nil
	}

	s.evalCalls++
	decision := s.evaluator.Evaluate(ctx, policy.EvaluationRequest{
		OrganizationID: organizationID,
		ProjectID:      projectID,
		AgentID:        &agentIDCopy,
		Capability:     capability,
	})
	if strings.EqualFold(decision.Effect, "deny") {
		return controlplane.CapabilityDecision{Allowed: false, Reason: decision.Reason}, nil
	}
	return controlplane.CapabilityDecision{Allowed: true, Reason: decision.Reason}, nil
}

type policyFixture struct {
	Pool    *pgxpool.Pool
	Org     repo.Organization
	Project repo.Project
	Agent   repo.Agent
	Run     controlplane.Run
	Step    controlplane.RunStep
	Attempt controlplane.RunAttempt
}

func (f *policyFixture) Close() {
	if f == nil || f.Pool == nil {
		return
	}
	f.Pool.Close()
}

func newPolicyFixture(t *testing.T) *policyFixture {
	t.Helper()
	return newPolicyFixtureWithAgentLists(t, []string{}, []string{})
}

func newPolicyFixtureWithAgentLists(t *testing.T, allowList, denyList []string) *policyFixture {
	t.Helper()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "policy-org-"+uuid.NewString()[:8])
	projectRecord := mustCreateProject(t, pool, org.ID, "policy-proj")
	agent := mustCreateAgent(t, pool, org.ID, policyAgentOptions{
		DisplayName:   "Policy Agent",
		ToolAllow:     allowList,
		ToolDeny:      denyList,
		CreatedByType: "system",
		CreatedByID:   uuid.Nil,
	})
	runRecord := mustCreateRun(t, pool, org.ID, &projectRecord.ID)
	step := mustCreateRunStep(t, pool, runRecord.ID, 1)
	attempt := mustCreateRunAttempt(t, pool, step.ID, 1)

	return &policyFixture{
		Pool:    pool,
		Org:     org,
		Project: projectRecord,
		Agent:   agent,
		Run:     runRecord,
		Step:    step,
		Attempt: attempt,
	}
}

func newPolicyEvaluator(t *testing.T, pool *pgxpool.Pool) *policy.PolicyEvaluator {
	t.Helper()
	evaluator, err := policy.NewPolicyEvaluator(policy.EvaluatorOptions{
		Policies: repo.NewCapabilityPolicyRepo(pool),
	})
	if err != nil {
		t.Fatalf("new policy evaluator: %v", err)
	}
	if err := evaluator.LoadInstancePolicies(context.Background()); err != nil {
		t.Fatalf("LoadInstancePolicies: %v", err)
	}
	return evaluator
}

func newPolicyBroker(t *testing.T, pool *pgxpool.Pool, policySvc controlplane.CapabilityPolicyService) *controlplane.ToolBroker {
	t.Helper()
	broker, err := controlplane.NewToolBroker(controlplane.ToolBrokerOptions{
		Pool:   pool,
		Policy: policySvc,
		Native: &nativeExecutor{},
	})
	if err != nil {
		t.Fatalf("new tool broker: %v", err)
	}
	return broker
}

type nativeExecutor struct{}

func (nativeExecutor) Execute(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

type policyAgentOptions struct {
	DisplayName   string
	ToolAllow     []string
	ToolDeny      []string
	BudgetCap     *int64
	CreatedByType string
	CreatedByID   uuid.UUID
}

func mustCreateOrg(t *testing.T, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()
	row, err := repo.NewOrgRepo(pool).Create(context.Background(), repo.Organization{
		Slug:        slug,
		DisplayName: slug,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return row
}

func mustCreateProject(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, prefix string) repo.Project {
	t.Helper()
	row, err := repo.NewProjectRepo(pool).Create(context.Background(), repo.Project{
		OrganizationID: orgID,
		Slug:           prefix + "-" + uuid.NewString()[:8],
		DisplayName:    "Policy Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return row
}

func mustCreateAgent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, opts policyAgentOptions) repo.Agent {
	t.Helper()
	row, err := repo.NewAgentRepo(pool).Create(context.Background(), repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          opts.DisplayName,
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        opts.ToolAllow,
		ToolDenyList:         opts.ToolDeny,
		BudgetCapTokens:      opts.BudgetCap,
		BudgetPeriod:         strPointer("daily"),
		CreatedByType:        opts.CreatedByType,
		CreatedByID:          opts.CreatedByID,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return row
}

func mustCreateRun(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, projectID *uuid.UUID) controlplane.Run {
	t.Helper()
	row, err := controlplane.NewRunRepository(pool).Create(context.Background(), controlplane.Run{
		OrganizationID: orgID,
		ProjectID:      projectID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "in_progress",
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return row
}

func mustCreateRunStep(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID, stepNumber int) controlplane.RunStep {
	t.Helper()
	row, err := controlplane.NewRunStepRepository(pool).Create(context.Background(), controlplane.RunStep{
		RunID:      runID,
		StepNumber: stepNumber,
		Status:     "pending",
		Metadata:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create run step: %v", err)
	}
	return row
}

func mustCreateRunAttempt(t *testing.T, pool *pgxpool.Pool, stepID uuid.UUID, attemptNumber int) controlplane.RunAttempt {
	t.Helper()
	row, err := controlplane.NewRunAttemptRepository(pool).Create(context.Background(), controlplane.RunAttempt{
		RunStepID:     stepID,
		AttemptNumber: attemptNumber,
		Trigger:       "initial",
		Status:        "pending",
		Metadata:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create run attempt: %v", err)
	}
	return row
}

func mustCreateToolDefinition(t *testing.T, pool *pgxpool.Pool, def repo.ToolDefinition) {
	t.Helper()
	if _, err := repo.NewToolDefinitionRepo(pool).Create(context.Background(), def); err != nil {
		t.Fatalf("create tool definition %s: %v", def.Name, err)
	}
}

func mustCreateCapabilityPolicy(t *testing.T, pool *pgxpool.Pool, row repo.CapabilityPolicy) repo.CapabilityPolicy {
	t.Helper()
	created, err := repo.NewCapabilityPolicyRepo(pool).Create(context.Background(), row)
	if err != nil {
		t.Fatalf("create capability policy %s/%s: %v", row.PolicyLayer, row.Capability, err)
	}
	return created
}

func mustCreateProvider(t *testing.T, pool *pgxpool.Pool) repo.ModelProvider {
	t.Helper()
	provider, err := repo.NewModelProviderRepo(pool).Create(context.Background(), repo.ModelProvider{
		Slug:        "policy-provider-" + uuid.NewString()[:8],
		DisplayName: "Policy Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return provider
}

func insertModelInvocation(t *testing.T, pool *pgxpool.Pool, item repo.ModelInvocation) {
	t.Helper()
	if _, err := repo.NewModelInvocationRepo(pool).Create(context.Background(), item); err != nil {
		t.Fatalf("create model invocation: %v", err)
	}
}

func sumTokens(t *testing.T, usage *budget.PostgresUsageQuerier, orgID uuid.UUID, projectID, agentID *uuid.UUID, since time.Time) int64 {
	t.Helper()
	sum, err := usage.SumTokens(context.Background(), orgID, projectID, agentID, since)
	if err != nil {
		t.Fatalf("sum tokens: %v", err)
	}
	return sum
}

type policyHTTPServer struct {
	URL   string
	Pool  *pgxpool.Pool
	Org   repo.Organization
	Admin repo.HumanUser
	ts    *httptest.Server
}

func (s *policyHTTPServer) Close() {
	if s == nil {
		return
	}
	s.ts.Close()
}

func newPolicyHTTPServer(t *testing.T) *policyHTTPServer {
	t.Helper()
	pool := testdb.New(t)
	logger := discardLogger()
	org := mustCreateOrg(t, pool, "policy-http-org-"+uuid.NewString()[:8])
	admin := mustCreateUser(t, pool, org.ID, "admin", "admin@example.com", "admin-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	policyRepo := repo.NewCapabilityPolicyRepo(pool)
	evaluator, err := policy.NewPolicyEvaluator(policy.EvaluatorOptions{
		Policies: policyRepo,
	})
	if err != nil {
		t.Fatalf("new policy evaluator: %v", err)
	}
	if err := evaluator.LoadInstancePolicies(context.Background()); err != nil {
		t.Fatalf("load instance policies: %v", err)
	}

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:     "test-version",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []server.RouteRegistrar{
			server.NewCapabilityPolicyRouteRegistrar(server.CapabilityPolicyRouteOptions{
				Policies:      policyRepo,
				Projects:      repo.NewProjectRepo(pool),
				Agents:        repo.NewAgentRepo(pool),
				Evaluator:     evaluator,
				AuditRecorder: audit.NewService(repo.NewAuditEventRepo(pool), logger),
			}),
		},
	})

	ts := httptest.NewServer(handler)
	return &policyHTTPServer{
		URL:   ts.URL,
		Pool:  pool,
		Org:   org,
		Admin: admin,
		ts:    ts,
	}
}

func mustCreateUser(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, role, email, password string) repo.HumanUser {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	passwordHash := string(hash)
	user, err := repo.NewHumanUserRepo(pool).Create(context.Background(), repo.HumanUser{
		OrganizationID: orgID,
		Email:          email,
		DisplayName:    strings.Split(email, "@")[0],
		PasswordHash:   &passwordHash,
		Role:           role,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

type jsonResponse struct {
	StatusCode int
	Body       []byte
}

func mustJSON(t *testing.T, method, url string, payload any, headers map[string]string) jsonResponse {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return jsonResponse{
		StatusCode: resp.StatusCode,
		Body:       rawBody,
	}
}

func loginToken(t *testing.T, baseURL, email, password string) string {
	t.Helper()
	response := mustJSON(t, http.MethodPost, baseURL+"/v1/auth/login", map[string]any{
		"email":    email,
		"password": password,
	}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d body=%s", response.StatusCode, http.StatusOK, string(response.Body))
	}
	var envelope map[string]any
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	data, _ := envelope["data"].(map[string]any)
	token, _ := data["token"].(string)
	return token
}

func waitForAuditEvent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, eventType string) {
	t.Helper()
	repository := repo.NewAuditEventRepo(pool)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		filter := eventType
		events, err := repository.ListByOrg(context.Background(), orgID, repo.AuditEventFilters{
			EventType: &filter,
		}, repo.Pagination{Limit: 10})
		if err != nil {
			t.Fatalf("list audit events: %v", err)
		}
		if len(events) > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected audit event %q not found", eventType)
}

func seedEscalationProjectTaskWithPM(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (repo.Project, repo.ProjectTask, uuid.UUID) {
	t.Helper()
	projectRecord := mustCreateProject(t, pool, orgID, "policy-escalate-project")
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectRecord.ID,
		Title:          "Escalation Task",
		WorkStatus:     "queued",
		CreatedByType:  "system",
		CreatedByID:    nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	pmUser := mustCreateUser(t, pool, orgID, "admin", "escalation-admin@example.com", "admin-password")
	pmAgent := mustCreateAgent(t, pool, orgID, policyAgentOptions{
		DisplayName:   "Escalation PM",
		ToolAllow:     []string{},
		ToolDeny:      []string{},
		CreatedByType: "human_user",
		CreatedByID:   pmUser.ID,
	})

	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        pmAgent.ID,
		ProjectID:      projectRecord.ID,
		Role:           "pm",
		AssignedByType: "human_user",
		AssignedByID:   &pmUser.ID,
	}); err != nil {
		t.Fatalf("assign PM agent: %v", err)
	}
	return projectRecord, taskRecord, pmUser.ID
}

func newRunService(t *testing.T, pool *pgxpool.Pool) controlplane.RunService {
	t.Helper()
	bus := eventbus.New(pool, discardLogger(), eventbus.Config{})
	svc, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Logger:   discardLogger(),
		Policy:   allowRunCreation{},
	})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	return svc
}

type allowRunCreation struct{}

func (allowRunCreation) EvaluateRunCreation(context.Context, uuid.UUID, controlplane.Principal) (controlplane.RunCreationPolicyDecision, error) {
	return controlplane.RunCreationPolicyDecision{Allowed: true}, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func strPointer(value string) *string {
	copied := value
	return &copied
}

func intPointer(value int) *int {
	copied := value
	return &copied
}
