package native

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type stubMemoryQueryService struct {
	called bool
	last   memory.RetrievalRequest
}

type stubChatSessionReader struct{}

func (stubChatSessionReader) Create(_ context.Context, session repo.ChatSession) (repo.ChatSession, error) {
	return session, nil
}

func (stubChatSessionReader) GetByID(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	return repo.ChatSession{ID: id}, nil
}

func (stubChatSessionReader) ListByOrg(_ context.Context, organizationID uuid.UUID) ([]repo.ChatSession, error) {
	return nil, nil
}

func (stubChatSessionReader) Close(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	return repo.ChatSession{ID: id}, nil
}

func (s *stubMemoryQueryService) Query(_ context.Context, req memory.RetrievalRequest) (memory.RetrievalResult, error) {
	s.called = true
	s.last = req
	return memory.RetrievalResult{Memories: []memory.RankedMemory{{
		Memory: repo.Memory{
			ID:         uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
			Content:    "remember this",
			Confidence: 0.92,
			MemoryType: "chat_summary",
			CreatedAt:  time.Unix(1700000000, 0).UTC(),
		},
	}}}, nil
}

type stubFlowNodeReader struct {
	node repo.FlowNode
	err  error
}

func (s stubFlowNodeReader) Create(_ context.Context, node repo.FlowNode) (repo.FlowNode, error) {
	return node, nil
}

func (s stubFlowNodeReader) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	if s.err != nil {
		return repo.FlowNode{}, s.err
	}
	if s.node.ID == id {
		return s.node, nil
	}
	return repo.FlowNode{}, repo.ErrNotFound
}

func (s stubFlowNodeReader) GetByTemplateOrdered(_ context.Context, _ uuid.UUID) ([]repo.FlowNode, error) {
	return nil, nil
}

func (s stubFlowNodeReader) Update(_ context.Context, node repo.FlowNode) (repo.FlowNode, error) {
	return node, nil
}

type stubFlowExecutionReader struct{}

func (stubFlowExecutionReader) Complete(_ context.Context, id uuid.UUID) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{ID: id}, nil
}

func (stubFlowExecutionReader) Create(_ context.Context, execution repo.FlowNodeExecution) (repo.FlowNodeExecution, error) {
	return execution, nil
}

func (stubFlowExecutionReader) GetByID(_ context.Context, _ uuid.UUID) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, repo.ErrNotFound
}

func (stubFlowExecutionReader) ListByTask(_ context.Context, _ uuid.UUID) ([]repo.FlowNodeExecution, error) {
	return nil, nil
}

func (stubFlowExecutionReader) RecordCommitSHA(_ context.Context, id uuid.UUID, _ string) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{ID: id}, nil
}

func (stubFlowExecutionReader) Reject(_ context.Context, id uuid.UUID) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{ID: id}, nil
}

func (stubFlowExecutionReader) UpdateMetadata(_ context.Context, id uuid.UUID, _ json.RawMessage) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{ID: id}, nil
}

func (stubFlowExecutionReader) UpdateRuntimeSubstate(_ context.Context, id uuid.UUID, _ *string) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{ID: id}, nil
}

func (stubFlowExecutionReader) Update(_ context.Context, execution repo.FlowNodeExecution) (repo.FlowNodeExecution, error) {
	return execution, nil
}

func TestMemoryQueryDelegatesToService(t *testing.T) {
	orgID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	agentID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	stub := &stubMemoryQueryService{}
	executor := NewExecutor(ExecutorOptions{Memory: stub, WorkspaceRoot: t.TempDir()})

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})
	out, err := executor.Execute(ctx, "memory.query", map[string]any{
		"query": "deploy checklist",
		"scope": "agent",
		"limit": 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !stub.called {
		t.Fatal("expected memory query service to be called")
	}
	if stub.last.OrganizationID != orgID {
		t.Fatalf("organization id = %s, want %s", stub.last.OrganizationID, orgID)
	}
	if stub.last.AgentID == nil || *stub.last.AgentID != agentID {
		t.Fatalf("agent id = %v, want %s", stub.last.AgentID, agentID)
	}
	if stub.last.MaxResults != 5 {
		t.Fatalf("max results = %d, want 5", stub.last.MaxResults)
	}

	memories, _ := out["memories"].([]map[string]any)
	if len(memories) != 1 {
		t.Fatalf("memories length = %d, want 1", len(memories))
	}
	if memories[0]["content"] != "remember this" {
		t.Fatalf("memory content = %v, want remember this", memories[0]["content"])
	}
}

func TestSessionListBlockedInReviewTaskSession(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	taskID := uuid.New()

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      uuid.New(),
			Title:          "Review validation sign-off",
			WorkStatus:     "review",
		},
	}
	executor.chatSessions = stubChatSessionReader{}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
		TaskID:         &taskID,
	})

	out, err := executor.Execute(ctx, "session.list", map[string]any{"limit": 10})
	if err != nil {
		t.Fatalf("session.list: %v", err)
	}
	if out["error"] != "review_action_required" {
		t.Fatalf("error = %v, want review_action_required", out["error"])
	}
	message, _ := out["message"].(string)
	if !strings.Contains(message, "flow.review_decision") {
		t.Fatalf("message = %q, want flow.review_decision guidance", message)
	}
}

func TestFlowGetExecutionDistinguishesFlowNodeID(t *testing.T) {
	nodeID := uuid.New()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.flowExecs = stubFlowExecutionReader{}
	executor.flowNodes = stubFlowNodeReader{node: repo.FlowNode{ID: nodeID}}

	out, err := executor.handleFlowGetExecution(context.Background(), map[string]any{
		"flow_node_execution_id": nodeID.String(),
	})
	if err != nil {
		t.Fatalf("handleFlowGetExecution: %v", err)
	}
	if out["error"] != "flow_node_execution_id_required" {
		t.Fatalf("error = %v, want flow_node_execution_id_required", out["error"])
	}
	message, _ := out["message"].(string)
	if !strings.Contains(message, "task.current_flow_node_id") {
		t.Fatalf("message = %q, want current_flow_node_id guidance", message)
	}
}
