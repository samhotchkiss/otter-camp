package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestFileWriteAtomicLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	out, err := executor.Execute(testExecCtx(), "file.write", map[string]any{
		"path":        "docs/readme.txt",
		"content":     "hello",
		"create_dirs": true,
	})
	if err != nil {
		t.Fatalf("file.write: %v", err)
	}
	if out["created"] != true {
		t.Fatalf("created = %v, want true", out["created"])
	}
	entries, err := filepath.Glob(filepath.Join(root, "docs", ".ottercamp-tmp-*"))
	if err != nil {
		t.Fatalf("glob tmp files: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected temp files: %v", entries)
	}
}

func TestFileEditAmbiguousMatchDoesNotModifyFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "dup.txt")
	if err := os.WriteFile(target, []byte("alpha\nalpha\n"), 0o644); err != nil {
		t.Fatalf("write dup file: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.edit", map[string]any{
		"path":       "dup.txt",
		"old_string": "alpha",
		"new_string": "beta",
	})
	if err != nil {
		t.Fatalf("file.edit: %v", err)
	}
	if out["error"] != "ambiguous_match" {
		t.Fatalf("error = %v, want ambiguous_match", out["error"])
	}
	if out["count"] != 2 {
		t.Fatalf("count = %v, want 2", out["count"])
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read dup file: %v", err)
	}
	if string(got) != "alpha\nalpha\n" {
		t.Fatalf("file changed unexpectedly: %q", string(got))
	}
}

func TestFileEditOldStringNotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.edit", map[string]any{
		"path":       "file.txt",
		"old_string": "missing",
		"new_string": "value",
	})
	if err != nil {
		t.Fatalf("file.edit: %v", err)
	}
	if out["error"] != "old_string_not_found" {
		t.Fatalf("error = %v, want old_string_not_found", out["error"])
	}
}

func TestGitCommitMainBranchReturnsPayloadError(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if len(args) == 3 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" && args[2] == "HEAD" {
				return helperCommand(ctx, "git-branch-main", nil)
			}
			t.Fatalf("unexpected git invocation: %v", args)
			return nil
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.commit", map[string]any{"message": "test commit"})
	if err != nil {
		t.Fatalf("git.commit: %v", err)
	}
	if out["error"] != "cannot_commit_to_main" {
		t.Fatalf("error = %v, want cannot_commit_to_main", out["error"])
	}
}

func TestGitPushForceProtectedBranchDeniedWithoutGitInvocation(t *testing.T) {
	calls := 0
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(_ context.Context, _ string, _ ...string) *exec.Cmd {
			calls++
			return nil
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.push", map[string]any{
		"branch": "shared/staging",
		"force":  true,
	})
	if err != nil {
		t.Fatalf("git.push: %v", err)
	}
	if out["error"] != "force_push_denied" {
		t.Fatalf("error = %v, want force_push_denied", out["error"])
	}
	if calls != 0 {
		t.Fatalf("git should not be invoked, calls=%d", calls)
	}
}

func TestGitPushForceFeatureBranchUsesForceFlag(t *testing.T) {
	var pushArgs []string
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if len(args) == 0 {
				t.Fatal("missing git args")
			}
			switch args[0] {
			case "rev-list":
				return helperCommand(ctx, "git-rev-list-2", nil)
			case "push":
				pushArgs = append([]string(nil), args...)
				return helperCommand(ctx, "git-push-ok", nil)
			default:
				t.Fatalf("unexpected git invocation: %v", args)
				return nil
			}
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.push", map[string]any{
		"remote": "origin",
		"branch": "feature/demo",
		"force":  true,
	})
	if err != nil {
		t.Fatalf("git.push: %v", err)
	}
	if out["branch"] != "feature/demo" {
		t.Fatalf("branch = %v, want feature/demo", out["branch"])
	}
	if !containsArg(pushArgs, "--force") {
		t.Fatalf("push args missing --force: %v", pushArgs)
	}
}

type stubMemoryRecorder struct {
	called      bool
	lastAgentID uuid.UUID
	lastContent string
}

func (s *stubMemoryRecorder) RecordExplicit(_ context.Context, agentID uuid.UUID, content, scope, sensitivity string, tags []string) (uuid.UUID, string, error) {
	s.called = true
	s.lastAgentID = agentID
	s.lastContent = content
	return uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), "queued", nil
}

func TestMemoryRecordDelegatesToRecorder(t *testing.T) {
	recorder := &stubMemoryRecorder{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot:  t.TempDir(),
		MemoryRecorder: recorder,
	})

	out, err := executor.Execute(testExecCtx(), "memory.record", map[string]any{
		"content": "store this",
		"scope":   "agent",
	})
	if err != nil {
		t.Fatalf("memory.record: %v", err)
	}
	if !recorder.called {
		t.Fatal("expected memory recorder to be called")
	}
	if recorder.lastContent != "store this" {
		t.Fatalf("content = %q, want store this", recorder.lastContent)
	}
	if out["status"] != "queued" {
		t.Fatalf("status = %v, want queued", out["status"])
	}
}

type fakeInboxRepo struct {
	created []repo.InboxItem
}

func (f *fakeInboxRepo) Create(_ context.Context, item repo.InboxItem) (repo.InboxItem, error) {
	item.ID = uuid.New()
	f.created = append(f.created, item)
	return item, nil
}

func (f *fakeInboxRepo) ListForUser(context.Context, uuid.UUID, uuid.UUID, repo.InboxListOptions) ([]repo.InboxItem, error) {
	return nil, nil
}

func (f *fakeInboxRepo) ListBroadcast(context.Context, uuid.UUID, repo.InboxListOptions) ([]repo.InboxItem, error) {
	return nil, nil
}

func (f *fakeInboxRepo) MarkActed(context.Context, uuid.UUID, uuid.UUID) (repo.InboxItem, error) {
	return repo.InboxItem{}, nil
}

func TestEmailComposeCreatesDraftActionReviewInboxItem(t *testing.T) {
	inbox := &fakeInboxRepo{}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.inbox = inbox

	out, err := executor.Execute(testExecCtx(), "email.compose", map[string]any{
		"to":      "ops@example.com",
		"subject": "Status",
	})
	if err != nil {
		t.Fatalf("email.compose: %v", err)
	}
	if out["status"] != "pending_review" {
		t.Fatalf("status = %v, want pending_review", out["status"])
	}
	if len(inbox.created) != 1 {
		t.Fatalf("created inbox items = %d, want 1", len(inbox.created))
	}
	if inbox.created[0].ItemType != "draft_action_review" {
		t.Fatalf("item_type = %q, want draft_action_review", inbox.created[0].ItemType)
	}
	var payload map[string]any
	if err := json.Unmarshal(inbox.created[0].ActionPayload, &payload); err != nil {
		t.Fatalf("unmarshal action payload: %v", err)
	}
	if payload["action"] != "email.compose" {
		t.Fatalf("action = %v, want email.compose", payload["action"])
	}
	input, _ := payload["input"].(map[string]any)
	if strings.TrimSpace(fmt.Sprintf("%v", input["to"])) != "ops@example.com" {
		t.Fatalf("payload input.to = %v, want ops@example.com", input["to"])
	}
}

type mockTaskRepo struct {
	task                repo.ProjectTask
	getByIDErr          error
	setFlowNodeErr      error
	updateStatusErr     error
	updateErr           error
	setFlowNodeCalls    int
	updateStatusCalls   int
	updateCalls         int
	lastSetFlowNodeID   *uuid.UUID
	lastUpdatedTaskID   uuid.UUID
	lastUpdatedTaskStat string
}

func (m *mockTaskRepo) Create(context.Context, repo.ProjectTask) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, errors.New("not implemented")
}

func (m *mockTaskRepo) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	if m.getByIDErr != nil {
		return repo.ProjectTask{}, m.getByIDErr
	}
	return m.task, nil
}

func (m *mockTaskRepo) ListByProject(context.Context, uuid.UUID, ...string) ([]repo.ProjectTask, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTaskRepo) SetFlowNode(_ context.Context, id uuid.UUID, flowNodeID *uuid.UUID) (repo.ProjectTask, error) {
	m.setFlowNodeCalls++
	m.lastUpdatedTaskID = id
	m.lastSetFlowNodeID = flowNodeID
	if m.setFlowNodeErr != nil {
		return repo.ProjectTask{}, m.setFlowNodeErr
	}
	m.task.CurrentFlowNodeID = flowNodeID
	return m.task, nil
}

func (m *mockTaskRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string) (repo.ProjectTask, error) {
	m.updateStatusCalls++
	m.lastUpdatedTaskID = id
	m.lastUpdatedTaskStat = status
	if m.updateStatusErr != nil {
		return repo.ProjectTask{}, m.updateStatusErr
	}
	m.task.WorkStatus = status
	return m.task, nil
}

func (m *mockTaskRepo) Update(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	m.updateCalls++
	if m.updateErr != nil {
		return repo.ProjectTask{}, m.updateErr
	}
	m.task = task
	return m.task, nil
}

type mockFlowNodeRepo struct {
	nodes map[uuid.UUID]repo.FlowNode
}

func (m *mockFlowNodeRepo) Create(context.Context, repo.FlowNode) (repo.FlowNode, error) {
	return repo.FlowNode{}, errors.New("not implemented")
}

func (m *mockFlowNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	if m.nodes == nil {
		return repo.FlowNode{}, repo.ErrNotFound
	}
	node, ok := m.nodes[id]
	if !ok {
		return repo.FlowNode{}, repo.ErrNotFound
	}
	return node, nil
}

func (m *mockFlowNodeRepo) GetByTemplateOrdered(context.Context, uuid.UUID) ([]repo.FlowNode, error) {
	return nil, errors.New("not implemented")
}

func (m *mockFlowNodeRepo) Update(context.Context, repo.FlowNode) (repo.FlowNode, error) {
	return repo.FlowNode{}, errors.New("not implemented")
}

type mockFlowExecutionRepo struct {
	byTask map[uuid.UUID][]repo.FlowNodeExecution
}

func (m *mockFlowExecutionRepo) Complete(context.Context, uuid.UUID) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, errors.New("not implemented")
}

func (m *mockFlowExecutionRepo) Create(context.Context, repo.FlowNodeExecution) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, errors.New("not implemented")
}

func (m *mockFlowExecutionRepo) GetByID(context.Context, uuid.UUID) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, errors.New("not implemented")
}

func (m *mockFlowExecutionRepo) ListByTask(_ context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error) {
	if m.byTask == nil {
		return nil, nil
	}
	return append([]repo.FlowNodeExecution(nil), m.byTask[taskID]...), nil
}

func (m *mockFlowExecutionRepo) RecordCommitSHA(context.Context, uuid.UUID, string) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, errors.New("not implemented")
}

func (m *mockFlowExecutionRepo) Reject(context.Context, uuid.UUID) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, errors.New("not implemented")
}

func TestTaskUpdateRejectsDraftToQueuedWithoutFlowTemplate(t *testing.T) {
	taskID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			WorkStatus:     "draft",
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != "task requires a flow template before it can be queued" {
		t.Fatalf("error = %v, want flow template required message", out["error"])
	}
	if tasks.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", tasks.updateCalls)
	}
}

func TestTaskUpdateAllowsDraftToQueuedWithFlowTemplate(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if got := taskOut["work_status"]; got != "queued" {
		t.Fatalf("work_status = %v, want queued", got)
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}
}

func TestTaskUpdateRejectsDraftToQueuedWithoutPMWhenProjectRequiresPM(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      projectID,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
		},
	}
	projects := &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"requires_pm_assignment_before_queue":true}`),
			},
		},
	}
	assignments := &fakeAssignmentRepo{}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.projects = projects
	executor.assignments = assignments

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != "project has no active PM assignment" {
		t.Fatalf("error = %v, want PM assignment required message", out["error"])
	}
	if tasks.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", tasks.updateCalls)
	}
}

func TestTaskUpdateAllowsDraftToQueuedWithPMWhenProjectRequiresPM(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      projectID,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
		},
	}
	projects := &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"requires_pm_assignment_before_queue":true}`),
			},
		},
	}
	assignments := &fakeAssignmentRepo{
		assignments: []repo.AgentProjectAssignment{
			{ID: uuid.New(), ProjectID: projectID, Role: "pm", IsActive: true},
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.projects = projects
	executor.assignments = assignments

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if got := taskOut["work_status"]; got != "queued" {
		t.Fatalf("work_status = %v, want queued", got)
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}
}

func TestTaskUpdateRejectsDoneWithoutFlowTemplate(t *testing.T) {
	taskID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			WorkStatus:     "review",
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "done",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != taskDoneTerminalNodeMessage {
		t.Fatalf("error = %v, want %q", out["error"], taskDoneTerminalNodeMessage)
	}
	if tasks.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", tasks.updateCalls)
	}
}

func TestTaskUpdateRejectsDoneWhenFlowNodeNotTerminal(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	flowNodeID := uuid.New()
	nextNodeID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                taskID,
			OrganizationID:    uuid.New(),
			ProjectID:         uuid.New(),
			WorkStatus:        "review",
			FlowTemplateID:    &flowTemplateID,
			CurrentFlowNodeID: &flowNodeID,
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		nodes: map[uuid.UUID]repo.FlowNode{
			flowNodeID: {ID: flowNodeID, NextNodeID: &nextNodeID},
		},
	}
	executor.flowExecs = &mockFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {{ID: uuid.New(), TaskID: taskID, FlowNodeID: flowNodeID, Status: "completed"}},
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "done",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != taskDoneTerminalNodeMessage {
		t.Fatalf("error = %v, want %q", out["error"], taskDoneTerminalNodeMessage)
	}
	if tasks.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", tasks.updateCalls)
	}
}

func TestTaskUpdateAllowsDoneWhenTerminalExecutionCompleted(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	flowNodeID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                taskID,
			OrganizationID:    uuid.New(),
			ProjectID:         uuid.New(),
			WorkStatus:        "review",
			FlowTemplateID:    &flowTemplateID,
			CurrentFlowNodeID: &flowNodeID,
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		nodes: map[uuid.UUID]repo.FlowNode{
			flowNodeID: {ID: flowNodeID, NextNodeID: nil},
		},
	}
	executor.flowExecs = &mockFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {{ID: uuid.New(), TaskID: taskID, FlowNodeID: flowNodeID, Status: "completed"}},
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "done",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if got := taskOut["work_status"]; got != "done" {
		t.Fatalf("work_status = %v, want done", got)
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}
}

func TestTaskUpdateAllowsCancelledWithoutFlowTemplate(t *testing.T) {
	taskID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			WorkStatus:     "draft",
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "cancelled",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if got := taskOut["work_status"]; got != "cancelled" {
		t.Fatalf("work_status = %v, want cancelled", got)
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}
}

type recordingEventPublisher struct {
	events []eventbus.DomainEvent
	err    error
}

func (r *recordingEventPublisher) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, event)
	return nil
}

func TestFlowAdvanceTerminalSetsDoneAndPublishesEvents(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                  taskID,
			OrganizationID:      orgID,
			ProjectID:           projectID,
			WorkStatus:          "in_progress",
			RequiresHumanReview: false,
		},
	}
	publisher := &recordingEventPublisher{}

	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.tasks = tasks

	out, err := executor.advanceExecutionToNode(testExecCtx(), taskID, nil)
	if err != nil {
		t.Fatalf("advanceExecutionToNode: %v", err)
	}
	if out["flow_completed"] != true {
		t.Fatalf("flow_completed = %v, want true", out["flow_completed"])
	}
	if tasks.updateStatusCalls != 1 || tasks.lastUpdatedTaskStat != "done" {
		t.Fatalf("status update calls=%d status=%q, want 1 and done", tasks.updateStatusCalls, tasks.lastUpdatedTaskStat)
	}
	if tasks.setFlowNodeCalls != 1 {
		t.Fatalf("set flow node calls=%d, want 1", tasks.setFlowNodeCalls)
	}
	if tasks.lastSetFlowNodeID != nil {
		t.Fatalf("last flow node id = %v, want nil", tasks.lastSetFlowNodeID)
	}
	if len(publisher.events) != 2 {
		t.Fatalf("published events=%d, want 2", len(publisher.events))
	}
	if publisher.events[0].EventType != "task.status_changed" {
		t.Fatalf("event[0].type = %q, want task.status_changed", publisher.events[0].EventType)
	}
	if publisher.events[1].EventType != "task.completed" {
		t.Fatalf("event[1].type = %q, want task.completed", publisher.events[1].EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["to_status"] != "done" {
		t.Fatalf("to_status = %v, want done", payload["to_status"])
	}
	if payload["from_status"] != "in_progress" {
		t.Fatalf("from_status = %v, want in_progress", payload["from_status"])
	}
	if payload["task_id"] != taskID.String() {
		t.Fatalf("task_id = %v, want %s", payload["task_id"], taskID.String())
	}
}

func TestFlowAdvanceTerminalRequiresReviewPublishesStatusOnly(t *testing.T) {
	taskID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                  taskID,
			OrganizationID:      uuid.New(),
			ProjectID:           uuid.New(),
			WorkStatus:          "in_progress",
			RequiresHumanReview: true,
		},
	}
	publisher := &recordingEventPublisher{}

	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.tasks = tasks

	if _, err := executor.advanceExecutionToNode(testExecCtx(), taskID, nil); err != nil {
		t.Fatalf("advanceExecutionToNode: %v", err)
	}
	if tasks.lastUpdatedTaskStat != "review" {
		t.Fatalf("updated status = %q, want review", tasks.lastUpdatedTaskStat)
	}
	if len(publisher.events) != 1 {
		t.Fatalf("published events=%d, want 1", len(publisher.events))
	}
	if publisher.events[0].EventType != "task.status_changed" {
		t.Fatalf("event type = %q, want task.status_changed", publisher.events[0].EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["to_status"] != "review" {
		t.Fatalf("to_status = %v, want review", payload["to_status"])
	}
}

func TestFlowAdvanceTerminalPropagatesGetByIDError(t *testing.T) {
	tasks := &mockTaskRepo{
		getByIDErr: errors.New("lookup failed"),
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	if _, err := executor.advanceExecutionToNode(testExecCtx(), uuid.New(), nil); err == nil || err.Error() != "lookup failed" {
		t.Fatalf("error = %v, want lookup failed", err)
	}
	if tasks.setFlowNodeCalls != 0 {
		t.Fatalf("set flow node calls=%d, want 0", tasks.setFlowNodeCalls)
	}
}

// ── Project creation auto-assignment tests ──────────────────────────────

type fakeProjectRepo struct {
	lastCreated repo.Project
	projects    map[uuid.UUID]repo.Project
}

func (f *fakeProjectRepo) Create(_ context.Context, p repo.Project) (repo.Project, error) {
	p.ID = uuid.New()
	f.lastCreated = p
	if f.projects == nil {
		f.projects = make(map[uuid.UUID]repo.Project)
	}
	f.projects[p.ID] = p
	return p, nil
}
func (f *fakeProjectRepo) List(context.Context, uuid.UUID) ([]repo.Project, error) {
	return nil, nil
}
func (f *fakeProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if f.projects == nil {
		return repo.Project{}, repo.ErrNotFound
	}
	projectRecord, ok := f.projects[id]
	if !ok {
		return repo.Project{}, repo.ErrNotFound
	}
	return projectRecord, nil
}
func (f *fakeProjectRepo) GetBySlug(context.Context, uuid.UUID, string) (repo.Project, error) {
	return repo.Project{}, nil
}
func (f *fakeProjectRepo) Update(context.Context, repo.Project) (repo.Project, error) {
	return repo.Project{}, nil
}
func (f *fakeProjectRepo) Archive(context.Context, uuid.UUID) error { return nil }

type fakeAssignmentRepo struct {
	assignments []repo.AgentProjectAssignment
}

func (f *fakeAssignmentRepo) Assign(_ context.Context, a repo.AgentProjectAssignment) (repo.AgentProjectAssignment, error) {
	a.ID = uuid.New()
	a.IsActive = true
	f.assignments = append(f.assignments, a)
	return a, nil
}
func (f *fakeAssignmentRepo) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error) {
	var result []repo.AgentProjectAssignment
	for _, a := range f.assignments {
		if a.ProjectID == projectID {
			result = append(result, a)
		}
	}
	return result, nil
}

type fakeChatSessionRepo struct {
	lastCreated repo.ChatSession
}

func (f *fakeChatSessionRepo) Create(_ context.Context, s repo.ChatSession) (repo.ChatSession, error) {
	s.ID = uuid.New()
	f.lastCreated = s
	return s, nil
}
func (f *fakeChatSessionRepo) GetByID(context.Context, uuid.UUID) (repo.ChatSession, error) {
	return repo.ChatSession{}, nil
}
func (f *fakeChatSessionRepo) ListByOrg(context.Context, uuid.UUID) ([]repo.ChatSession, error) {
	return nil, nil
}

type fakeParticipantRepo struct {
	participants []repo.ChatParticipant
}

func (f *fakeParticipantRepo) Create(_ context.Context, p repo.ChatParticipant) (repo.ChatParticipant, error) {
	p.ID = uuid.New()
	f.participants = append(f.participants, p)
	return p, nil
}
func (f *fakeParticipantRepo) ListBySession(context.Context, uuid.UUID) ([]repo.ChatParticipant, error) {
	return nil, nil
}

func TestProjectCreatePublishesStaffingEventAndDoesNotAutoAssignAgents(t *testing.T) {
	frankID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	orgID := uuid.MustParse("99999999-9999-9999-9999-999999999999")

	projects := &fakeProjectRepo{}
	assignments := &fakeAssignmentRepo{}
	publisher := &recordingEventPublisher{}

	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.projects = projects
	executor.assignments = assignments

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &frankID,
	})

	out, err := executor.Execute(ctx, "project.create", map[string]any{
		"name":        "Super Bowl Ad",
		"description": "Create a Super Bowl ad for OtterCamp",
	})
	if err != nil {
		t.Fatalf("project.create: %v", err)
	}

	// Verify project was created
	proj, ok := out["project"].(map[string]any)
	if !ok {
		t.Fatalf("missing project in output: %v", out)
	}
	if proj["name"] != "Super Bowl Ad" {
		t.Fatalf("project name = %v, want Super Bowl Ad", proj["name"])
	}
	if _, ok := out["assigned_agents"]; ok {
		t.Fatalf("assigned_agents should be omitted, got %v", out["assigned_agents"])
	}
	if len(assignments.assignments) != 0 {
		t.Fatalf("assignment repo entries = %d, want 0", len(assignments.assignments))
	}
	if !strings.Contains(string(projects.lastCreated.Settings), `"requires_pm_assignment_before_queue":true`) {
		t.Fatalf("project settings = %s, want requires_pm_assignment_before_queue=true", string(projects.lastCreated.Settings))
	}
	if len(publisher.events) != 2 {
		t.Fatalf("published events=%d, want 2", len(publisher.events))
	}
	if publisher.events[0].EventType != "project.created" {
		t.Fatalf("event[0].type = %q, want project.created", publisher.events[0].EventType)
	}
	if publisher.events[1].EventType != "project.staffing_needed" {
		t.Fatalf("event[1].type = %q, want project.staffing_needed", publisher.events[1].EventType)
	}
}

func TestSessionCreateAutoAddsProjectParticipants(t *testing.T) {
	frankID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	loriID := uuid.New()
	orgID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	projectID := uuid.New()

	sessions := &fakeChatSessionRepo{}
	participants := &fakeParticipantRepo{}
	assignments := &fakeAssignmentRepo{
		assignments: []repo.AgentProjectAssignment{
			{AgentID: frankID, ProjectID: projectID, Role: "pm", IsActive: true},
			{AgentID: loriID, ProjectID: projectID, Role: "worker", IsActive: true},
		},
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.chatSessions = sessions
	executor.participants = participants
	executor.assignments = assignments

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &frankID,
	})

	out, err := executor.Execute(ctx, "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}

	// Verify session was created
	session, ok := out["session"].(map[string]any)
	if !ok {
		t.Fatalf("missing session in output: %v", out)
	}
	if session["mode"] != "async" {
		t.Fatalf("session mode = %v, want async", session["mode"])
	}

	// Verify auto-participants were added
	autoParticipants, ok := out["auto_participants"].([]map[string]any)
	if !ok || len(autoParticipants) != 2 {
		t.Fatalf("auto_participants = %v, want 2 entries", out["auto_participants"])
	}

	// Worker should be added FIRST (so they become primary responder)
	if autoParticipants[0]["project_role"] != "worker" {
		t.Fatalf("first participant role = %v, want worker", autoParticipants[0]["project_role"])
	}
	if autoParticipants[1]["project_role"] != "pm" {
		t.Fatalf("second participant role = %v, want pm", autoParticipants[1]["project_role"])
	}

	// Verify participant records: worker first, then PM
	if len(participants.participants) != 2 {
		t.Fatalf("participant entries = %d, want 2", len(participants.participants))
	}
	if participants.participants[0].ParticipantID != loriID {
		t.Fatalf("first participant = %v, want Lori", participants.participants[0].ParticipantID)
	}
	if participants.participants[1].ParticipantID != frankID {
		t.Fatalf("second participant = %v, want Frank", participants.participants[1].ParticipantID)
	}
}

func TestFlowAdvanceTerminalPropagatesUpdateStatusError(t *testing.T) {
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                  uuid.New(),
			OrganizationID:      uuid.New(),
			ProjectID:           uuid.New(),
			WorkStatus:          "in_progress",
			RequiresHumanReview: false,
		},
		updateStatusErr: errors.New("update failed"),
	}
	publisher := &recordingEventPublisher{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.tasks = tasks

	if _, err := executor.advanceExecutionToNode(testExecCtx(), tasks.task.ID, nil); err == nil || err.Error() != "update failed" {
		t.Fatalf("error = %v, want update failed", err)
	}
	if tasks.setFlowNodeCalls != 1 {
		t.Fatalf("set flow node calls=%d, want 1", tasks.setFlowNodeCalls)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("published events=%d, want 0", len(publisher.events))
	}
}
