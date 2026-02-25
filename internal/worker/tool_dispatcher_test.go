package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

func TestLiveToolDispatcherDispatchTier1(t *testing.T) {
	agentID := uuid.New()
	sessionID := uuid.New()

	broker := &fakeBroker{
		dispatchFn: func(_ context.Context, input controlplane.DispatchInput) (controlplane.ToolExecution, error) {
			if input.AgentID != agentID {
				t.Fatalf("AgentID = %s, want %s", input.AgentID, agentID)
			}
			if input.SessionID == nil || *input.SessionID != sessionID {
				t.Fatalf("SessionID = %v, want %s", input.SessionID, sessionID)
			}
			return controlplane.ToolExecution{
				Output: json.RawMessage(`{"ok":true}`),
			}, nil
		},
	}

	dispatcher, err := newLiveToolDispatcher(broker, &fakeRunLifecycle{}, &fakeAgentRepo{})
	if err != nil {
		t.Fatalf("newLiveToolDispatcher: %v", err)
	}

	result, err := dispatcher.DispatchTier1(context.Background(), turn.ToolCall{
		ID:   "tool-1",
		Name: "project.list",
		Arguments: map[string]any{
			"agent_id":   agentID.String(),
			"session_id": sessionID.String(),
		},
	})
	if err != nil {
		t.Fatalf("DispatchTier1 err: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("result.Error = %q, want empty", result.Error)
	}
	if ok, _ := result.Output["ok"].(bool); !ok {
		t.Fatalf("result.Output = %#v, want ok=true", result.Output)
	}
}

func TestLiveToolDispatcherDispatchTier2Lifecycle(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.New()
	runID := uuid.New()
	stepID := uuid.New()
	mcpConnectionID := uuid.New()

	runs := &fakeRunLifecycle{
		createRunFn: func(_ context.Context, input controlplane.CreateRunInput) (controlplane.Run, error) {
			if input.OrganizationID != orgID {
				t.Fatalf("OrganizationID = %s, want %s", input.OrganizationID, orgID)
			}
			if input.PrincipalID != agentID {
				t.Fatalf("PrincipalID = %s, want %s", input.PrincipalID, agentID)
			}
			return controlplane.Run{ID: runID}, nil
		},
		createStepFn: func(_ context.Context, runIDArg uuid.UUID, toolName, toolTier string) (controlplane.RunStep, error) {
			if runIDArg != runID {
				t.Fatalf("CreateStep runID = %s, want %s", runIDArg, runID)
			}
			if toolName != "system.cli.execute" {
				t.Fatalf("toolName = %q, want system.cli.execute", toolName)
			}
			if toolTier != "tier2" {
				t.Fatalf("toolTier = %q, want tier2", toolTier)
			}
			return controlplane.RunStep{ID: stepID, RunID: runID}, nil
		},
	}
	broker := &fakeBroker{
		dispatchFn: func(_ context.Context, input controlplane.DispatchInput) (controlplane.ToolExecution, error) {
			if input.RunID == nil || *input.RunID != runID {
				t.Fatalf("Dispatch RunID = %v, want %s", input.RunID, runID)
			}
			if input.RunStepID == nil || *input.RunStepID != stepID {
				t.Fatalf("Dispatch RunStepID = %v, want %s", input.RunStepID, stepID)
			}
			if got, _ := input.Input["mcp_connection_id"].(string); got != mcpConnectionID.String() {
				t.Fatalf("mcp_connection_id = %q, want %q", got, mcpConnectionID.String())
			}
			return controlplane.ToolExecution{
				Output: json.RawMessage(`{"status":"done"}`),
			}, nil
		},
	}
	agents := &fakeAgentRepo{
		byID: map[uuid.UUID]repo.Agent{
			agentID: {ID: agentID, OrganizationID: orgID},
		},
	}
	dispatcher, err := newLiveToolDispatcher(broker, runs, agents)
	if err != nil {
		t.Fatalf("newLiveToolDispatcher: %v", err)
	}

	var startedRunID uuid.UUID
	result, err := dispatcher.DispatchTier2(context.Background(), turn.ToolCall{
		ID:              "tool-2",
		Name:            "system.cli.execute",
		MCPConnectionID: &mcpConnectionID,
		Arguments: map[string]any{
			"agent_id":        agentID.String(),
			"organization_id": orgID.String(),
			"command":         "echo hello",
		},
	}, func(runID uuid.UUID) {
		startedRunID = runID
	})
	if err != nil {
		t.Fatalf("DispatchTier2 err: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("result.Error = %q, want empty", result.Error)
	}
	if result.RunID == nil || *result.RunID != runID {
		t.Fatalf("result.RunID = %v, want %s", result.RunID, runID)
	}
	if startedRunID != runID {
		t.Fatalf("onRunStarted runID = %s, want %s", startedRunID, runID)
	}
	if result.Output["status"] != "done" {
		t.Fatalf("result.Output = %#v, want status=done", result.Output)
	}
	if runs.startedRuns != 1 || runs.completedRuns != 1 {
		t.Fatalf("runs started/completed = %d/%d, want 1/1", runs.startedRuns, runs.completedRuns)
	}
}

func TestFailureClassForToolError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "capability denied", err: controlplane.ErrCapabilityDenied, want: "policy_denied"},
		{name: "mcp timeout", err: mcp.ErrTimeout, want: "timeout"},
		{name: "unsupported", err: controlplane.ErrToolNotSupported, want: "permanent"},
		{name: "default", err: errors.New("boom"), want: "transient"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := failureClassForToolError(tc.err)
			if got != tc.want {
				t.Fatalf("failureClassForToolError() = %q, want %q", got, tc.want)
			}
		})
	}
}

type fakeBroker struct {
	dispatchFn func(ctx context.Context, input controlplane.DispatchInput) (controlplane.ToolExecution, error)
}

func (f *fakeBroker) Dispatch(ctx context.Context, input controlplane.DispatchInput) (controlplane.ToolExecution, error) {
	if f.dispatchFn != nil {
		return f.dispatchFn(ctx, input)
	}
	return controlplane.ToolExecution{}, nil
}

type fakeRunLifecycle struct {
	createRunFn  func(ctx context.Context, input controlplane.CreateRunInput) (controlplane.Run, error)
	createStepFn func(ctx context.Context, runID uuid.UUID, toolName, toolTier string) (controlplane.RunStep, error)

	startedRuns   int
	completedRuns int
}

func (f *fakeRunLifecycle) CreateRun(ctx context.Context, input controlplane.CreateRunInput) (controlplane.Run, error) {
	if f.createRunFn != nil {
		return f.createRunFn(ctx, input)
	}
	return controlplane.Run{ID: uuid.New()}, nil
}

func (f *fakeRunLifecycle) StartRun(context.Context, uuid.UUID) error {
	f.startedRuns++
	return nil
}

func (f *fakeRunLifecycle) CompleteRun(context.Context, uuid.UUID, json.RawMessage) error {
	f.completedRuns++
	return nil
}

func (f *fakeRunLifecycle) FailRun(context.Context, uuid.UUID, string, string) error { return nil }

func (f *fakeRunLifecycle) CreateStep(ctx context.Context, runID uuid.UUID, toolName, toolTier string) (controlplane.RunStep, error) {
	if f.createStepFn != nil {
		return f.createStepFn(ctx, runID, toolName, toolTier)
	}
	return controlplane.RunStep{ID: uuid.New(), RunID: runID}, nil
}

func (f *fakeRunLifecycle) StartStep(context.Context, uuid.UUID) error    { return nil }
func (f *fakeRunLifecycle) CompleteStep(context.Context, uuid.UUID) error { return nil }
func (f *fakeRunLifecycle) FailStep(context.Context, uuid.UUID, string) error {
	return nil
}

type fakeAgentRepo struct {
	byID map[uuid.UUID]repo.Agent
}

func (f *fakeAgentRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.byID == nil {
		return repo.Agent{}, repo.ErrNotFound
	}
	item, ok := f.byID[id]
	if !ok {
		return repo.Agent{}, repo.ErrNotFound
	}
	return item, nil
}
