package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
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

func TestToolBrokerDispatchCLIAugmentsExecutionContext(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.New()
	runID := uuid.New()
	runStepID := uuid.New()
	runAttemptID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	cliExec := &fakeCLIExecutor{output: map[string]any{"ok": true}}

	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: newFakeToolExecutionRepo(),
		Runs: &brokerFakeRunRepo{byID: map[uuid.UUID]Run{
			runID: {ID: runID, OrganizationID: orgID, ProjectID: &projectID, TaskID: &taskID},
		}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: orgID},
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			"cli.execute": {Name: "cli.execute", ToolDomain: "cli", ToolTier: "tier2", RequiredCapability: strPtr("system.cli.execute")},
		}},
		Policy: &fakeCapabilityPolicy{decision: CapabilityDecision{Allowed: true}},
		CLI:    cliExec,
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	_, err = broker.Dispatch(context.Background(), DispatchInput{
		RunID:        &runID,
		RunStepID:    &runStepID,
		RunAttemptID: &runAttemptID,
		AgentID:      agentID,
		ToolName:     "cli.execute",
		ToolTier:     "tier2",
		Input: map[string]any{
			"command": "echo hello",
		},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if cliExec.lastInput["run_id"] != runID.String() {
		t.Fatalf("run_id = %v, want %s", cliExec.lastInput["run_id"], runID)
	}
	if cliExec.lastInput["run_step_id"] != runStepID.String() {
		t.Fatalf("run_step_id = %v, want %s", cliExec.lastInput["run_step_id"], runStepID)
	}
	if cliExec.lastInput["run_attempt_id"] != runAttemptID.String() {
		t.Fatalf("run_attempt_id = %v, want %s", cliExec.lastInput["run_attempt_id"], runAttemptID)
	}
	if cliExec.lastInput["project_id"] != projectID.String() {
		t.Fatalf("project_id = %v, want %s", cliExec.lastInput["project_id"], projectID)
	}
	if cliExec.lastInput["task_id"] != taskID.String() {
		t.Fatalf("task_id = %v, want %s", cliExec.lastInput["task_id"], taskID)
	}
	if cliExec.lastInput["agent_id"] != agentID.String() {
		t.Fatalf("agent_id = %v, want %s", cliExec.lastInput["agent_id"], agentID)
	}
}

func TestToolBrokerDispatchCLIAugmentsTaskContextFromTaskSession(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.New()
	sessionID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	cliExec := &fakeCLIExecutor{output: map[string]any{"ok": true}}

	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: newFakeToolExecutionRepo(),
		Runs:       &brokerFakeRunRepo{byID: map[uuid.UUID]Run{}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: orgID},
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			"cli.execute": {Name: "cli.execute", ToolDomain: "cli", ToolTier: "tier2", RequiredCapability: strPtr("system.cli.execute")},
		}},
		Sessions: &brokerFakeSessionRepo{byID: map[uuid.UUID]repo.ChatSession{
			sessionID: {ID: sessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID},
		}},
		Tasks: &brokerFakeTaskRepo{byID: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, OrganizationID: orgID, ProjectID: projectID},
		}},
		Policy: &fakeCapabilityPolicy{decision: CapabilityDecision{Allowed: true}},
		CLI:    cliExec,
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	_, err = broker.Dispatch(context.Background(), DispatchInput{
		SessionID: &sessionID,
		AgentID:   agentID,
		ToolName:  "cli.execute",
		ToolTier:  "tier2",
		Input: map[string]any{
			"command": "echo hello",
		},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if cliExec.lastInput["project_id"] != projectID.String() {
		t.Fatalf("project_id = %v, want %s", cliExec.lastInput["project_id"], projectID)
	}
	if cliExec.lastInput["task_id"] != taskID.String() {
		t.Fatalf("task_id = %v, want %s", cliExec.lastInput["task_id"], taskID)
	}
}

func TestToolBrokerDispatchTaskSessionBindingInvariantBlocksCLI(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.New()
	sessionID := uuid.New()
	taskID := uuid.New()
	cliExec := &fakeCLIExecutor{output: map[string]any{"ok": true}}

	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: newFakeToolExecutionRepo(),
		Runs:       &brokerFakeRunRepo{byID: map[uuid.UUID]Run{}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: orgID},
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			"cli.execute": {Name: "cli.execute", ToolDomain: "cli", ToolTier: "tier2", RequiredCapability: strPtr("system.cli.execute")},
		}},
		Sessions: &brokerFakeSessionRepo{byID: map[uuid.UUID]repo.ChatSession{
			sessionID: {ID: sessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID},
		}},
		Policy: &fakeCapabilityPolicy{decision: CapabilityDecision{Allowed: true}},
		CLI:    cliExec,
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	_, err = broker.Dispatch(context.Background(), DispatchInput{
		SessionID: &sessionID,
		AgentID:   agentID,
		ToolName:  "cli.execute",
		ToolTier:  "tier2",
		Input: map[string]any{
			"command": "echo hello",
		},
	})
	if err == nil {
		t.Fatal("Dispatch error = nil, want task binding invariant")
	}
	if !strings.Contains(err.Error(), taskScopeBindingInvariantPrefix) {
		t.Fatalf("Dispatch error = %v, want invariant prefix %q", err, taskScopeBindingInvariantPrefix)
	}
	if cliExec.lastInput != nil {
		t.Fatalf("cli executor should not be invoked when task binding is missing, got input=%v", cliExec.lastInput)
	}
}

func TestToolBrokerDispatchProjectSessionClearsStaleTaskBindingBeforeNativeExecution(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.New()
	runID := uuid.New()
	sessionID := uuid.New()
	projectID := uuid.New()
	staleProjectID := uuid.New()
	staleTaskID := uuid.New()
	nativeExec := &fakeNativeExecutor{output: map[string]any{"ok": true}}

	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: newFakeToolExecutionRepo(),
		Runs: &brokerFakeRunRepo{byID: map[uuid.UUID]Run{
			runID: {ID: runID, OrganizationID: orgID, ProjectID: &staleProjectID, TaskID: &staleTaskID},
		}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: orgID},
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			"file.read": {Name: "file.read", ToolDomain: "native", ToolTier: "tier1"},
		}},
		Sessions: &brokerFakeSessionRepo{byID: map[uuid.UUID]repo.ChatSession{
			sessionID: {ID: sessionID, OrganizationID: orgID, ScopeType: "project", ScopeID: projectID},
		}},
		Tasks: &brokerFakeTaskRepo{byID: map[uuid.UUID]repo.ProjectTask{
			staleTaskID: {ID: staleTaskID, OrganizationID: orgID, ProjectID: staleProjectID},
		}},
		Native: nativeExec,
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	_, err = broker.Dispatch(context.Background(), DispatchInput{
		RunID:     &runID,
		SessionID: &sessionID,
		AgentID:   agentID,
		ToolName:  "file.read",
		ToolTier:  "tier1",
		Input:     map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if nativeExec.lastExecCtx.ProjectID == nil || *nativeExec.lastExecCtx.ProjectID != projectID {
		t.Fatalf("project_id = %v, want %s", nativeExec.lastExecCtx.ProjectID, projectID)
	}
	if nativeExec.lastExecCtx.TaskID != nil {
		t.Fatalf("task_id = %v, want nil after clearing stale cross-project task binding", nativeExec.lastExecCtx.TaskID)
	}
}

func TestToolBrokerDispatchProjectSessionPreservesSameProjectTaskBinding(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.New()
	runID := uuid.New()
	sessionID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nativeExec := &fakeNativeExecutor{output: map[string]any{"ok": true}}

	broker, err := NewToolBroker(ToolBrokerOptions{
		Executions: newFakeToolExecutionRepo(),
		Runs: &brokerFakeRunRepo{byID: map[uuid.UUID]Run{
			runID: {ID: runID, OrganizationID: orgID, ProjectID: &projectID, TaskID: &taskID},
		}},
		Agents: &brokerFakeAgentRepo{byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: orgID},
		}},
		ToolDefinitions: &brokerFakeToolDefinitionRepo{byName: map[string]repo.ToolDefinition{
			"file.read": {Name: "file.read", ToolDomain: "native", ToolTier: "tier1"},
		}},
		Sessions: &brokerFakeSessionRepo{byID: map[uuid.UUID]repo.ChatSession{
			sessionID: {ID: sessionID, OrganizationID: orgID, ScopeType: "project", ScopeID: projectID},
		}},
		Tasks: &brokerFakeTaskRepo{byID: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, OrganizationID: orgID, ProjectID: projectID},
		}},
		Native: nativeExec,
	})
	if err != nil {
		t.Fatalf("NewToolBroker: %v", err)
	}

	_, err = broker.Dispatch(context.Background(), DispatchInput{
		RunID:     &runID,
		SessionID: &sessionID,
		AgentID:   agentID,
		ToolName:  "file.read",
		ToolTier:  "tier1",
		Input:     map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if nativeExec.lastExecCtx.ProjectID == nil || *nativeExec.lastExecCtx.ProjectID != projectID {
		t.Fatalf("project_id = %v, want %s", nativeExec.lastExecCtx.ProjectID, projectID)
	}
	if nativeExec.lastExecCtx.TaskID == nil || *nativeExec.lastExecCtx.TaskID != taskID {
		t.Fatalf("task_id = %v, want %s", nativeExec.lastExecCtx.TaskID, taskID)
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

type brokerFakeSessionRepo struct {
	byID map[uuid.UUID]repo.ChatSession
}

func (f *brokerFakeSessionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	item, ok := f.byID[id]
	if !ok {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	return item, nil
}

type brokerFakeTaskRepo struct {
	byID map[uuid.UUID]repo.ProjectTask
}

func (f *brokerFakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	item, ok := f.byID[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
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
	output      map[string]any
	err         error
	lastExecCtx mcp.ExecutionContext
	lastInput   map[string]any
}

func (f *fakeNativeExecutor) Execute(ctx context.Context, _ string, input map[string]any) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastExecCtx = mcp.ExecutionContextFromContext(ctx)
	f.lastInput = cloneMap(input)
	if f.output == nil {
		return map[string]any{}, nil
	}
	return f.output, nil
}

type fakeCLIExecutor struct {
	output    map[string]any
	err       error
	lastInput map[string]any
}

func (f *fakeCLIExecutor) Execute(_ context.Context, input map[string]any) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastInput = cloneMap(input)
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
