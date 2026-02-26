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
	setFlowNodeCalls    int
	updateStatusCalls   int
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

func (m *mockTaskRepo) Update(context.Context, repo.ProjectTask) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, errors.New("not implemented")
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
