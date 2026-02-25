package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestToolBrokerDispatchTier2CapabilityDenied(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.New()
	runID := uuid.New()
	projectID := uuid.New()

	execRepo := newFakeToolExecutionRepo()
	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: execRepo,
		Runs: &brokerFakeRunRepo{byID: map[uuid.UUID]Run{
			runID: {ID: runID, OrganizationID: orgID, ProjectID: &projectID},
		}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: orgID},
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			"file.write": {Name: "file.write", ToolDomain: "native", RequiredCapability: strPtr("system.file.write")},
		}},
		Policy: &fakeCapabilityPolicy{decision: CapabilityDecision{Allowed: false, Reason: "denied by policy"}},
		Native: &fakeNativeExecutor{},
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	_, dispatchErr := broker.Dispatch(context.Background(), DispatchInput{
		RunID:    &runID,
		AgentID:  agentID,
		ToolName: "file.write",
		ToolTier: "tier2",
		Input:    map[string]any{"path": "README.md"},
	})
	if !errors.Is(dispatchErr, ErrCapabilityDenied) {
		t.Fatalf("Dispatch error = %v, want ErrCapabilityDenied", dispatchErr)
	}
	if len(execRepo.created) != 1 {
		t.Fatalf("created rows = %d, want 1", len(execRepo.created))
	}
	row := execRepo.created[0]
	if row.PolicyDecision != "denied" {
		t.Fatalf("policy_decision = %q, want denied", row.PolicyDecision)
	}
	if row.Status != "policy_denied" {
		t.Fatalf("status = %q, want policy_denied", row.Status)
	}
}

func TestToolBrokerDispatchAgentDenyList(t *testing.T) {
	agentID := uuid.New()
	execRepo := newFakeToolExecutionRepo()
	broker := mustNewTestBroker(t, execRepo, agentID, repo.Agent{
		ID:             agentID,
		OrganizationID: uuid.New(),
		ToolDenyList:   []string{"browser.*"},
	}, repo.ToolDefinition{Name: "browser.navigate", ToolDomain: "browser", ToolTier: "tier1"})

	_, err := broker.Dispatch(context.Background(), DispatchInput{
		AgentID:  agentID,
		ToolName: "browser.navigate",
		ToolTier: "tier1",
		Input:    map[string]any{"url": "https://example.com"},
	})
	if !errors.Is(err, ErrAgentDenyList) {
		t.Fatalf("Dispatch error = %v, want ErrAgentDenyList", err)
	}
}

func TestToolBrokerDispatchAgentAllowList(t *testing.T) {
	agentID := uuid.New()
	execRepo := newFakeToolExecutionRepo()
	broker := mustNewTestBroker(t, execRepo, agentID, repo.Agent{
		ID:             agentID,
		OrganizationID: uuid.New(),
		ToolAllowList:  []string{"git.*"},
	}, repo.ToolDefinition{Name: "file.read", ToolDomain: "native", ToolTier: "tier1"})

	_, err := broker.Dispatch(context.Background(), DispatchInput{
		AgentID:  agentID,
		ToolName: "file.read",
		ToolTier: "tier1",
		Input:    map[string]any{"path": "go.mod"},
	})
	if !errors.Is(err, ErrAgentDenyList) {
		t.Fatalf("Dispatch error = %v, want ErrAgentDenyList", err)
	}
}

func TestToolBrokerDispatchTier1RedactsSecretsAndSkipsPolicy(t *testing.T) {
	agentID := uuid.New()
	execRepo := newFakeToolExecutionRepo()
	policy := &fakeCapabilityPolicy{decision: CapabilityDecision{Allowed: false}}
	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: execRepo,
		Runs:       &brokerFakeRunRepo{byID: map[uuid.UUID]Run{}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: uuid.New()},
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			"file.read": {Name: "file.read", ToolDomain: "native", ToolTier: "tier1"},
		}},
		Policy: policy,
		Native: &fakeNativeExecutor{output: map[string]any{"ok": true}},
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	_, dispatchErr := broker.Dispatch(context.Background(), DispatchInput{
		AgentID:  agentID,
		ToolName: "file.read",
		ToolTier: "tier1",
		Input: map[string]any{
			"api_key": "ref:prod_key",
			"token":   "Bearer abc123xyz",
		},
	})
	if dispatchErr != nil {
		t.Fatalf("Dispatch: %v", dispatchErr)
	}
	if policy.calls != 0 {
		t.Fatalf("policy calls = %d, want 0 for tier1", policy.calls)
	}
	if len(execRepo.created) != 1 {
		t.Fatalf("created rows = %d, want 1", len(execRepo.created))
	}
	row := execRepo.created[0]
	if row.PolicyDecision != "not_checked" {
		t.Fatalf("policy_decision = %q, want not_checked", row.PolicyDecision)
	}

	stored := map[string]any{}
	if err := json.Unmarshal(row.Input, &stored); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	apiKey, _ := stored["api_key"].(string)
	if apiKey != "[SECRET:prod_key]" {
		t.Fatalf("api_key = %q, want [SECRET:prod_key]", apiKey)
	}
	token, _ := stored["token"].(string)
	if !strings.Contains(token, "[REDACTED]") {
		t.Fatalf("token = %q, want [REDACTED]", token)
	}
}

func mustNewTestBroker(t *testing.T, execRepo *fakeToolExecutionRepo, agentID uuid.UUID, agent repo.Agent, definition repo.ToolDefinition) *ToolBroker {
	t.Helper()
	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: execRepo,
		Runs:       &brokerFakeRunRepo{byID: map[uuid.UUID]Run{}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: agent,
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			definition.Name: definition,
		}},
		Native:  &fakeNativeExecutor{output: map[string]any{"ok": true}},
		Browser: &fakeBrowserExecutor{output: map[string]any{"ok": true}},
		CLI:     &fakeCLIExecutor{output: map[string]any{"ok": true}},
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}
	return broker
}

type fakeToolExecutionRepo struct {
	created []ToolExecution
	byID    map[uuid.UUID]ToolExecution
}

func newFakeToolExecutionRepo() *fakeToolExecutionRepo {
	return &fakeToolExecutionRepo{byID: map[uuid.UUID]ToolExecution{}}
}

func (f *fakeToolExecutionRepo) Create(_ context.Context, exec ToolExecution) (ToolExecution, error) {
	if exec.ID == uuid.Nil {
		exec.ID = uuid.New()
	}
	f.created = append(f.created, exec)
	f.byID[exec.ID] = exec
	return exec, nil
}

func (f *fakeToolExecutionRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string, output json.RawMessage, errorMessage *string) (ToolExecution, error) {
	item, ok := f.byID[id]
	if !ok {
		return ToolExecution{}, ErrNotFound
	}
	item.Status = status
	if len(output) > 0 {
		item.Output = output
	}
	item.ErrorMessage = errorMessage
	f.byID[id] = item
	return item, nil
}

type brokerFakeRunRepo struct {
	byID map[uuid.UUID]Run
}

func (f *brokerFakeRunRepo) Get(_ context.Context, id uuid.UUID) (Run, error) {
	item, ok := f.byID[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return item, nil
}

type brokerFakeAgentRepo struct {
	byID map[uuid.UUID]repo.Agent
}

func (f *brokerFakeAgentRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Agent, error) {
	item, ok := f.byID[id]
	if !ok {
		return repo.Agent{}, repo.ErrNotFound
	}
	return item, nil
}

type brokerFakeToolDefinitionRepo struct {
	byName map[string]repo.ToolDefinition
}

func (f *brokerFakeToolDefinitionRepo) GetByName(_ context.Context, name string) (repo.ToolDefinition, error) {
	item, ok := f.byName[name]
	if !ok {
		return repo.ToolDefinition{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeCapabilityPolicy struct {
	decision CapabilityDecision
	err      error
	calls    int
}

func (f *fakeCapabilityPolicy) EvaluateCapability(_ context.Context, _ uuid.UUID, _ *uuid.UUID, _ uuid.UUID, _ string) (CapabilityDecision, error) {
	f.calls++
	if f.err != nil {
		return CapabilityDecision{}, f.err
	}
	return f.decision, nil
}

type fakeNativeExecutor struct {
	output map[string]any
	err    error
}

func (f *fakeNativeExecutor) Execute(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.output == nil {
		return map[string]any{}, nil
	}
	return f.output, nil
}

type fakeCLIExecutor struct {
	output map[string]any
	err    error
}

func (f *fakeCLIExecutor) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.output == nil {
		return map[string]any{}, nil
	}
	return f.output, nil
}

type fakeBrowserExecutor struct {
	output map[string]any
	err    error
}

func (f *fakeBrowserExecutor) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.output == nil {
		return map[string]any{}, nil
	}
	return f.output, nil
}

func strPtr(value string) *string {
	return &value
}
