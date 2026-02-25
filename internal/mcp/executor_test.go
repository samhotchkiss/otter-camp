package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestExecutorExecuteReturnsErrCircuitOpenWithoutCall(t *testing.T) {
	connectionID := uuid.New()
	orgID := uuid.New()
	logs := &fakeMCPExecutionLogRepo{}
	caller := &fakeMCPCaller{state: CBOpen}

	executor, err := NewExecutor(ExecutorOptions{
		Connections: &fakeMCPConnectionRepo{byID: map[uuid.UUID]repo.MCPConnection{
			connectionID: {ID: connectionID, OrganizationID: orgID},
		}},
		Caller: caller,
		Logs:   logs,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	_, execErr := executor.Execute(context.Background(), "github.issue.create", map[string]any{"title": "x"}, connectionID)
	if !errors.Is(execErr, ErrCircuitOpen) {
		t.Fatalf("Execute error = %v, want ErrCircuitOpen", execErr)
	}
	if caller.calls != 0 {
		t.Fatalf("CallTool calls = %d, want 0", caller.calls)
	}
	if len(logs.completed) != 1 || logs.completed[0].status != "circuit_open" {
		t.Fatalf("completed logs = %+v, want one circuit_open record", logs.completed)
	}
}

func TestExecutorReadResourceProjectScopeRequiresAssignment(t *testing.T) {
	connectionID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()

	logs := &fakeMCPExecutionLogRepo{}
	executor, err := NewExecutor(ExecutorOptions{
		Connections: &fakeMCPConnectionRepo{byID: map[uuid.UUID]repo.MCPConnection{
			connectionID: {ID: connectionID, OrganizationID: orgID, ProjectID: &projectID},
		}},
		Caller:      &fakeMCPCaller{},
		Assignments: &fakeAssignmentRepo{err: repo.ErrNotFound},
		Logs:        logs,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	ctx := WithExecutionContext(context.Background(), ExecutionContext{AgentID: &agentID})
	_, readErr := executor.ReadResource(ctx, "repo://docs/readme", connectionID)
	if !errors.Is(readErr, ErrAccessDenied) {
		t.Fatalf("ReadResource error = %v, want ErrAccessDenied", readErr)
	}
	if len(logs.completed) != 1 || logs.completed[0].status != "error" {
		t.Fatalf("completed logs = %+v, want one error record", logs.completed)
	}
}

type fakeMCPConnectionRepo struct {
	byID map[uuid.UUID]repo.MCPConnection
}

func (f *fakeMCPConnectionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.MCPConnection, error) {
	item, ok := f.byID[id]
	if !ok {
		return repo.MCPConnection{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeMCPConnectionRepo) SetStatus(_ context.Context, id uuid.UUID, status string) (repo.MCPConnection, error) {
	item, ok := f.byID[id]
	if !ok {
		return repo.MCPConnection{}, repo.ErrNotFound
	}
	item.Status = status
	f.byID[id] = item
	return item, nil
}

type fakeMCPCaller struct {
	state  CBState
	calls  int
	result json.RawMessage
	err    error
}

func (f *fakeMCPCaller) CircuitState(_ uuid.UUID) CBState {
	if f.state == "" {
		return CBClosed
	}
	return f.state
}

func (f *fakeMCPCaller) CallTool(_ context.Context, _ CallToolRequest) (json.RawMessage, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.result) == 0 {
		return json.RawMessage(`{"ok":true}`), nil
	}
	return f.result, nil
}

type fakeAssignmentRepo struct {
	assignment repo.AgentProjectAssignment
	err        error
}

func (f *fakeAssignmentRepo) GetByAgentAndProject(_ context.Context, _, _ uuid.UUID) (repo.AgentProjectAssignment, error) {
	if f.err != nil {
		return repo.AgentProjectAssignment{}, f.err
	}
	if f.assignment.ID == uuid.Nil {
		f.assignment.ID = uuid.New()
	}
	return f.assignment, nil
}

type fakeMCPExecutionLogRepo struct {
	created   []repo.MCPExecutionLog
	completed []completedLog
}

type completedLog struct {
	id     uuid.UUID
	status string
}

func (f *fakeMCPExecutionLogRepo) Create(_ context.Context, entry repo.MCPExecutionLog) (repo.MCPExecutionLog, error) {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	f.created = append(f.created, entry)
	return entry, nil
}

func (f *fakeMCPExecutionLogRepo) Complete(_ context.Context, id uuid.UUID, status string, responsePayload json.RawMessage, errorMessage *string, latencyMS *int) (repo.MCPExecutionLog, error) {
	f.completed = append(f.completed, completedLog{id: id, status: status})
	return repo.MCPExecutionLog{ID: id, Status: status, ResponsePayload: responsePayload, ErrorMessage: errorMessage, LatencyMS: latencyMS}, nil
}
