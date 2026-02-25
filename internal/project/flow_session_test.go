package project

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestFlowSessionBridgeEnsureNodeSessionCreatesSessionAndPersistsID(t *testing.T) {
	taskID := uuid.New()
	flowNodeID := uuid.New()
	executionID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()

	executions := &fakeFlowSessionExecutionRepo{}
	tasks := &fakeFlowSessionTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
		},
	}
	chats := &fakeFlowSessionChatService{
		createdSession: &repo.ChatSession{ID: sessionID, OrganizationID: orgID},
	}

	bridge, err := NewFlowSessionBridge(FlowSessionBridgeOptions{
		Executions: executions,
		Tasks:      tasks,
		Chats:      chats,
	})
	if err != nil {
		t.Fatalf("NewFlowSessionBridge: %v", err)
	}

	session, err := bridge.EnsureNodeSession(context.Background(), repo.FlowNodeExecution{
		ID:         executionID,
		TaskID:     taskID,
		FlowNodeID: flowNodeID,
	})
	if err != nil {
		t.Fatalf("EnsureNodeSession: %v", err)
	}
	if session.ID != sessionID {
		t.Fatalf("session.id = %s, want %s", session.ID, sessionID)
	}
	if chats.createCalls != 1 {
		t.Fatalf("CreateSession calls = %d, want 1", chats.createCalls)
	}
	if chats.lastCreateInput.ScopeType != "project_task" {
		t.Fatalf("scope_type = %q, want project_task", chats.lastCreateInput.ScopeType)
	}
	if chats.lastCreateInput.ScopeID != taskID {
		t.Fatalf("scope_id = %s, want %s", chats.lastCreateInput.ScopeID, taskID)
	}
	if chats.lastCreateInput.Mode != "async" {
		t.Fatalf("mode = %q, want async", chats.lastCreateInput.Mode)
	}
	if chats.lastCreateInput.OrganizationID != orgID {
		t.Fatalf("organization_id = %s, want %s", chats.lastCreateInput.OrganizationID, orgID)
	}
	if executions.setSessionCalls != 1 {
		t.Fatalf("SetSessionID calls = %d, want 1", executions.setSessionCalls)
	}
	if executions.lastSetExecutionID != executionID || executions.lastSetSessionID != sessionID {
		t.Fatalf("SetSessionID args = (%s,%s), want (%s,%s)", executions.lastSetExecutionID, executions.lastSetSessionID, executionID, sessionID)
	}

	var metadata map[string]string
	if err := json.Unmarshal(chats.lastCreateInput.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if metadata["flow_node_execution_id"] != executionID.String() {
		t.Fatalf("metadata flow_node_execution_id = %q, want %q", metadata["flow_node_execution_id"], executionID.String())
	}
	if metadata["flow_node_id"] != flowNodeID.String() {
		t.Fatalf("metadata flow_node_id = %q, want %q", metadata["flow_node_id"], flowNodeID.String())
	}
}

func TestFlowSessionBridgeEnsureNodeSessionReturnsExistingSession(t *testing.T) {
	taskID := uuid.New()
	flowNodeID := uuid.New()
	executionID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()

	executions := &fakeFlowSessionExecutionRepo{}
	chats := &fakeFlowSessionChatService{
		getSession: &repo.ChatSession{
			ID:             sessionID,
			OrganizationID: orgID,
			ScopeType:      "project_task",
			ScopeID:        taskID,
			Mode:           "async",
		},
	}

	bridge, err := NewFlowSessionBridge(FlowSessionBridgeOptions{
		Executions: executions,
		Tasks:      &fakeFlowSessionTaskRepo{},
		Chats:      chats,
	})
	if err != nil {
		t.Fatalf("NewFlowSessionBridge: %v", err)
	}

	got, err := bridge.EnsureNodeSession(context.Background(), repo.FlowNodeExecution{
		ID:         executionID,
		TaskID:     taskID,
		FlowNodeID: flowNodeID,
		SessionID:  &sessionID,
	})
	if err != nil {
		t.Fatalf("EnsureNodeSession: %v", err)
	}
	if got.ID != sessionID {
		t.Fatalf("session.id = %s, want %s", got.ID, sessionID)
	}
	if chats.getCalls != 1 {
		t.Fatalf("GetSession calls = %d, want 1", chats.getCalls)
	}
	if chats.createCalls != 0 {
		t.Fatalf("CreateSession calls = %d, want 0", chats.createCalls)
	}
	if executions.setSessionCalls != 0 {
		t.Fatalf("SetSessionID calls = %d, want 0", executions.setSessionCalls)
	}
}

func TestFlowSessionBridgeRecordCommitSHA(t *testing.T) {
	executionID := uuid.New()
	executions := &fakeFlowSessionExecutionRepo{}
	bridge, err := NewFlowSessionBridge(FlowSessionBridgeOptions{
		Executions: executions,
		Tasks:      &fakeFlowSessionTaskRepo{},
		Chats:      &fakeFlowSessionChatService{},
	})
	if err != nil {
		t.Fatalf("NewFlowSessionBridge: %v", err)
	}

	if err := bridge.RecordCommitSHA(context.Background(), executionID, " abc123 "); err != nil {
		t.Fatalf("RecordCommitSHA non-empty: %v", err)
	}
	if executions.recordCommitCalls != 1 {
		t.Fatalf("RecordCommitSHA calls = %d, want 1", executions.recordCommitCalls)
	}
	if executions.lastCommitSHA != "abc123" {
		t.Fatalf("commit_sha = %q, want %q", executions.lastCommitSHA, "abc123")
	}

	if err := bridge.RecordCommitSHA(context.Background(), executionID, "   "); err != nil {
		t.Fatalf("RecordCommitSHA empty: %v", err)
	}
	if executions.recordCommitCalls != 2 {
		t.Fatalf("RecordCommitSHA calls = %d, want 2", executions.recordCommitCalls)
	}
	if executions.lastCommitSHA != "" {
		t.Fatalf("commit_sha = %q, want empty", executions.lastCommitSHA)
	}
}

func TestFlowSessionBridgeRouteRunToSessionAppendsRunLogMessage(t *testing.T) {
	runID := uuid.New()
	taskID := uuid.New()
	flowNodeID := uuid.New()
	executionID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()

	executions := &fakeFlowSessionExecutionRepo{
		activeExecution: repo.FlowNodeExecution{
			ID:         executionID,
			TaskID:     taskID,
			FlowNodeID: flowNodeID,
			Status:     "active",
		},
	}
	tasks := &fakeFlowSessionTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
		},
	}
	chats := &fakeFlowSessionChatService{
		createdSession: &repo.ChatSession{
			ID:             sessionID,
			OrganizationID: orgID,
		},
		appendedMessage: &repo.ChatMessage{ID: uuid.New()},
	}

	bridge, err := NewFlowSessionBridge(FlowSessionBridgeOptions{
		Executions: executions,
		Tasks:      tasks,
		Chats:      chats,
	})
	if err != nil {
		t.Fatalf("NewFlowSessionBridge: %v", err)
	}

	if err := bridge.RouteRunToSession(context.Background(), FlowRun{
		ID:         runID,
		TaskID:     &taskID,
		FlowNodeID: &flowNodeID,
		Summary:    "mcp.execute",
	}); err != nil {
		t.Fatalf("RouteRunToSession: %v", err)
	}
	if executions.getActiveCalls != 1 {
		t.Fatalf("GetActive calls = %d, want 1", executions.getActiveCalls)
	}
	if chats.appendCalls != 1 {
		t.Fatalf("AppendMessage calls = %d, want 1", chats.appendCalls)
	}
	if chats.lastAppendInput.SessionID != sessionID {
		t.Fatalf("append session_id = %s, want %s", chats.lastAppendInput.SessionID, sessionID)
	}
	if chats.lastAppendInput.Role != "tool_result" {
		t.Fatalf("append role = %q, want tool_result", chats.lastAppendInput.Role)
	}
	if chats.lastAppendInput.ContentFormat != "tool_json" {
		t.Fatalf("append content_format = %q, want tool_json", chats.lastAppendInput.ContentFormat)
	}
	if chats.lastAppendInput.AuthorType != nil || chats.lastAppendInput.AuthorID != nil {
		t.Fatalf("append author should be nil for system message")
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(chats.lastAppendInput.Content), &payload); err != nil {
		t.Fatalf("unmarshal append content: %v", err)
	}
	if payload["type"] != "run_log_link" {
		t.Fatalf("content type = %q, want run_log_link", payload["type"])
	}
	if payload["run_id"] != runID.String() {
		t.Fatalf("content run_id = %q, want %q", payload["run_id"], runID.String())
	}
	if payload["summary"] != "mcp.execute" {
		t.Fatalf("content summary = %q, want %q", payload["summary"], "mcp.execute")
	}
}

func TestFlowSessionBridgeRouteRunToSessionNoOpWithoutFlowNode(t *testing.T) {
	executions := &fakeFlowSessionExecutionRepo{}
	chats := &fakeFlowSessionChatService{}
	bridge, err := NewFlowSessionBridge(FlowSessionBridgeOptions{
		Executions: executions,
		Tasks:      &fakeFlowSessionTaskRepo{},
		Chats:      chats,
	})
	if err != nil {
		t.Fatalf("NewFlowSessionBridge: %v", err)
	}

	if err := bridge.RouteRunToSession(context.Background(), FlowRun{
		ID:         uuid.New(),
		TaskID:     nil,
		FlowNodeID: nil,
		Summary:    "api",
	}); err != nil {
		t.Fatalf("RouteRunToSession: %v", err)
	}

	if executions.getActiveCalls != 0 {
		t.Fatalf("GetActive calls = %d, want 0", executions.getActiveCalls)
	}
	if chats.appendCalls != 0 {
		t.Fatalf("AppendMessage calls = %d, want 0", chats.appendCalls)
	}
}

type fakeFlowSessionExecutionRepo struct {
	byID map[uuid.UUID]repo.FlowNodeExecution

	activeExecution repo.FlowNodeExecution
	activeErr       error
	getActiveCalls  int

	setSessionCalls    int
	lastSetExecutionID uuid.UUID
	lastSetSessionID   uuid.UUID

	recordCommitCalls int
	lastCommitID      uuid.UUID
	lastCommitSHA     string
}

func (f *fakeFlowSessionExecutionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNodeExecution, error) {
	if f.byID != nil {
		if item, ok := f.byID[id]; ok {
			return item, nil
		}
	}
	return repo.FlowNodeExecution{}, repo.ErrNotFound
}

func (f *fakeFlowSessionExecutionRepo) GetActive(_ context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error) {
	f.getActiveCalls++
	if f.activeErr != nil {
		return repo.FlowNodeExecution{}, f.activeErr
	}
	if f.activeExecution.TaskID == taskID && f.activeExecution.FlowNodeID == flowNodeID {
		return f.activeExecution, nil
	}
	return repo.FlowNodeExecution{}, repo.ErrNotFound
}

func (f *fakeFlowSessionExecutionRepo) SetSessionID(_ context.Context, id, sessionID uuid.UUID) (repo.FlowNodeExecution, error) {
	f.setSessionCalls++
	f.lastSetExecutionID = id
	f.lastSetSessionID = sessionID
	execution := repo.FlowNodeExecution{
		ID:        id,
		SessionID: &sessionID,
	}
	if item, ok := f.byID[id]; ok {
		item.SessionID = &sessionID
		f.byID[id] = item
		execution = item
	}
	return execution, nil
}

func (f *fakeFlowSessionExecutionRepo) RecordCommitSHA(_ context.Context, id uuid.UUID, commitSHA string) (repo.FlowNodeExecution, error) {
	f.recordCommitCalls++
	f.lastCommitID = id
	f.lastCommitSHA = commitSHA
	return repo.FlowNodeExecution{ID: id}, nil
}

type fakeFlowSessionTaskRepo struct {
	task repo.ProjectTask
	err  error
}

func (f *fakeFlowSessionTaskRepo) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	return f.task, nil
}

type fakeFlowSessionChatService struct {
	createCalls     int
	lastCreateInput chat.CreateSessionInput
	createdSession  *repo.ChatSession
	createErr       error

	getCalls   int
	getSession *repo.ChatSession
	getErr     error

	appendCalls     int
	lastAppendInput chat.AppendMessageInput
	appendedMessage *repo.ChatMessage
	appendErr       error
}

func (f *fakeFlowSessionChatService) CreateSession(_ context.Context, input chat.CreateSessionInput) (*repo.ChatSession, error) {
	f.createCalls++
	f.lastCreateInput = input
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createdSession != nil {
		return f.createdSession, nil
	}
	return &repo.ChatSession{ID: uuid.New()}, nil
}

func (f *fakeFlowSessionChatService) GetSession(context.Context, uuid.UUID) (*repo.ChatSession, error) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getSession, nil
}

func (f *fakeFlowSessionChatService) AppendMessage(_ context.Context, input chat.AppendMessageInput) (*repo.ChatMessage, error) {
	f.appendCalls++
	f.lastAppendInput = input
	if f.appendErr != nil {
		return nil, f.appendErr
	}
	if f.appendedMessage != nil {
		return f.appendedMessage, nil
	}
	return &repo.ChatMessage{ID: uuid.New()}, nil
}

var _ flowSessionExecutionRepository = (*fakeFlowSessionExecutionRepo)(nil)
var _ flowSessionTaskRepository = (*fakeFlowSessionTaskRepo)(nil)
var _ flowSessionChatService = (*fakeFlowSessionChatService)(nil)
