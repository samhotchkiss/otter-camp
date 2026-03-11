package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
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

func TestFileWriteMalformedRawMissingPathReturnsActionableError(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})

	out, err := executor.Execute(testExecCtx(), "file.write", map[string]any{
		"_raw": `{"content":"hello"}`,
	})
	if err != nil {
		t.Fatalf("file.write: %v", err)
	}
	if out["error"] != "path_required" {
		t.Fatalf("error = %v, want path_required", out["error"])
	}
	message, _ := out["message"].(string)
	if !strings.Contains(message, "non-empty path") {
		t.Fatalf("message = %q, want actionable path guidance", message)
	}
}

func TestFileWritePathOnlyRawReturnsContentRequired(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	out, err := executor.Execute(testExecCtx(), "file.write", map[string]any{
		"_raw": `{"path":"content/posts/stop-preparing-your-kids-for-jobs.md","create_dirs":true}`,
	})
	if err != nil {
		t.Fatalf("file.write: %v", err)
	}
	if out["error"] != "content_required" {
		t.Fatalf("error = %v, want content_required", out["error"])
	}
	message, _ := out["message"].(string)
	if !strings.Contains(message, "requires content") {
		t.Fatalf("message = %q, want actionable content guidance", message)
	}
	if _, err := os.Stat(filepath.Join(root, "content")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("content directory should not be created on invalid write, stat err = %v", err)
	}
}

func TestFileWriteAllowsExplicitEmptyStringContent(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	out, err := executor.Execute(testExecCtx(), "file.write", map[string]any{
		"path":        "docs/empty.txt",
		"content":     "",
		"create_dirs": true,
	})
	if err != nil {
		t.Fatalf("file.write: %v", err)
	}
	if got := out["byte_size"]; got != 0 {
		t.Fatalf("byte_size = %v, want 0", got)
	}
	body, err := os.ReadFile(filepath.Join(root, "docs", "empty.txt"))
	if err != nil {
		t.Fatalf("read empty file: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("empty file length = %d, want 0", len(body))
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
			if len(args) == 1 && args[0] == "remote" {
				return helperCommand(ctx, "git-status", nil)
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

type stubMemoryWriter struct {
	called  bool
	last    repo.Memory
	created repo.Memory
}

func (s *stubMemoryWriter) Create(_ context.Context, memory repo.Memory) (repo.Memory, error) {
	s.called = true
	s.last = memory
	created := memory
	if s.created.ID == uuid.Nil {
		created.ID = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	} else {
		created.ID = s.created.ID
	}
	s.created = created
	return created, nil
}

func TestMemoryRecordFallbackPersistsActiveMemoryWithEmbedding(t *testing.T) {
	writer := &stubMemoryWriter{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
	})
	executor.memories = writer

	out, err := executor.Execute(testExecCtx(), "memory.record", map[string]any{
		"content": "remember deployment runbook",
		"scope":   "agent",
	})
	if err != nil {
		t.Fatalf("memory.record: %v", err)
	}
	if !writer.called {
		t.Fatal("expected memory writer to be called")
	}
	if writer.last.Status != "active" {
		t.Fatalf("status = %q, want active", writer.last.Status)
	}
	if len(writer.last.Embedding) != memoryRecordEmbeddingDims {
		t.Fatalf("embedding dims = %d, want %d", len(writer.last.Embedding), memoryRecordEmbeddingDims)
	}
	if out["status"] != "stored" {
		t.Fatalf("status = %v, want stored", out["status"])
	}
	memoryID, ok := out["memory_id"].(uuid.UUID)
	if !ok {
		t.Fatalf("memory_id type = %T, want uuid.UUID", out["memory_id"])
	}
	if memoryID != writer.created.ID {
		t.Fatalf("memory_id = %s, want %s", memoryID, writer.created.ID)
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
	listByProjectTasks  []repo.ProjectTask
	createdTasks        []repo.ProjectTask
	getByIDErr          error
	setFlowNodeErr      error
	updateStatusErr     error
	updateErr           error
	createCalls         int
	setFlowNodeCalls    int
	updateStatusCalls   int
	updateCalls         int
	lastSetFlowNodeID   *uuid.UUID
	lastUpdatedTaskID   uuid.UUID
	lastUpdatedTaskStat string
}

func (m *mockTaskRepo) Create(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	m.createCalls++
	created := task
	if created.ID == uuid.Nil {
		created.ID = uuid.New()
	}
	if created.TaskNumber == 0 {
		created.TaskNumber = 100 + m.createCalls
	}
	m.createdTasks = append(m.createdTasks, created)
	m.storeTask(created)
	return created, nil
}

func (m *mockTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	if m.getByIDErr != nil {
		return repo.ProjectTask{}, m.getByIDErr
	}
	if m.task.ID == id {
		return m.task, nil
	}
	for _, task := range m.listByProjectTasks {
		if task.ID == id {
			return task, nil
		}
	}
	for _, task := range m.createdTasks {
		if task.ID == id {
			return task, nil
		}
	}
	return m.task, nil
}

func (m *mockTaskRepo) ListByProject(_ context.Context, projectID uuid.UUID, _ ...string) ([]repo.ProjectTask, error) {
	if m.listByProjectTasks != nil {
		items := make([]repo.ProjectTask, 0, len(m.listByProjectTasks))
		for _, task := range m.listByProjectTasks {
			if projectID == uuid.Nil || task.ProjectID == projectID {
				items = append(items, task)
			}
		}
		return items, nil
	}
	items := make([]repo.ProjectTask, 0, 1+len(m.createdTasks))
	seen := map[uuid.UUID]struct{}{}
	if m.task.ID != uuid.Nil && (projectID == uuid.Nil || m.task.ProjectID == projectID) {
		items = append(items, m.task)
		seen[m.task.ID] = struct{}{}
	}
	for _, task := range m.createdTasks {
		if projectID != uuid.Nil && task.ProjectID != projectID {
			continue
		}
		if _, ok := seen[task.ID]; ok {
			continue
		}
		items = append(items, task)
		seen[task.ID] = struct{}{}
	}
	return items, nil
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
	m.storeTask(task)
	return m.task, nil
}

func (m *mockTaskRepo) UpdateMetadata(_ context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error) {
	m.updateCalls++
	if m.updateErr != nil {
		return repo.ProjectTask{}, m.updateErr
	}
	task, err := m.GetByID(context.Background(), id)
	if err != nil {
		return repo.ProjectTask{}, err
	}
	task.Metadata = append(json.RawMessage(nil), metadata...)
	m.task = task
	m.storeTask(task)
	return task, nil
}

func (m *mockTaskRepo) storeTask(task repo.ProjectTask) {
	if task.ID == uuid.Nil {
		return
	}
	if m.task.ID == task.ID {
		m.task = task
	}
	replaced := false
	for i := range m.listByProjectTasks {
		if m.listByProjectTasks[i].ID == task.ID {
			m.listByProjectTasks[i] = task
			replaced = true
			break
		}
	}
	if !replaced && m.listByProjectTasks != nil {
		m.listByProjectTasks = append(m.listByProjectTasks, task)
	}
	for i := range m.createdTasks {
		if m.createdTasks[i].ID == task.ID {
			m.createdTasks[i] = task
			return
		}
	}
}

type mockFlowNodeRepo struct {
	nodes         map[uuid.UUID]repo.FlowNode
	templateNodes map[uuid.UUID][]repo.FlowNode
	getByTplErr   error
}

func (m *mockFlowNodeRepo) Create(_ context.Context, node repo.FlowNode) (repo.FlowNode, error) {
	if node.ID == uuid.Nil {
		node.ID = uuid.New()
	}
	if m.nodes == nil {
		m.nodes = make(map[uuid.UUID]repo.FlowNode)
	}
	m.nodes[node.ID] = node
	if m.templateNodes == nil {
		m.templateNodes = make(map[uuid.UUID][]repo.FlowNode)
	}
	m.templateNodes[node.FlowTemplateID] = append(m.templateNodes[node.FlowTemplateID], node)
	return node, nil
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

func (m *mockFlowNodeRepo) GetByTemplateOrdered(_ context.Context, templateID uuid.UUID) ([]repo.FlowNode, error) {
	if m.getByTplErr != nil {
		return nil, m.getByTplErr
	}
	if m.templateNodes != nil {
		return append([]repo.FlowNode(nil), m.templateNodes[templateID]...), nil
	}
	if m.nodes == nil {
		return nil, nil
	}
	nodes := make([]repo.FlowNode, 0)
	for _, node := range m.nodes {
		if node.FlowTemplateID == templateID {
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

func (m *mockFlowNodeRepo) Update(_ context.Context, node repo.FlowNode) (repo.FlowNode, error) {
	if m.nodes == nil {
		m.nodes = make(map[uuid.UUID]repo.FlowNode)
	}
	m.nodes[node.ID] = node
	if m.templateNodes != nil {
		updated := make([]repo.FlowNode, 0, len(m.templateNodes[node.FlowTemplateID]))
		replaced := false
		for _, existing := range m.templateNodes[node.FlowTemplateID] {
			if existing.ID == node.ID {
				updated = append(updated, node)
				replaced = true
				continue
			}
			updated = append(updated, existing)
		}
		if !replaced {
			updated = append(updated, node)
		}
		m.templateNodes[node.FlowTemplateID] = updated
	}
	return node, nil
}

type mockFlowTemplateRepo struct {
	templates map[uuid.UUID]repo.FlowTemplate
}

func validExecutableTemplateNodeList(flowTemplateID uuid.UUID) []repo.FlowNode {
	mergeNodeID := uuid.New()
	reviewNodeID := uuid.New()
	return []repo.FlowNode{
		{
			ID:             uuid.New(),
			FlowTemplateID: flowTemplateID,
			NodeType:       "work",
			Position:       1,
			NextNodeID:     &reviewNodeID,
		},
		{
			ID:             reviewNodeID,
			FlowTemplateID: flowTemplateID,
			NodeType:       "review",
			Position:       2,
			NextNodeID:     &mergeNodeID,
		},
		{
			ID:             mergeNodeID,
			FlowTemplateID: flowTemplateID,
			NodeType:       "merge",
			Position:       3,
		},
	}
}

func (m *mockFlowTemplateRepo) Create(_ context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error) {
	if template.ID == uuid.Nil {
		template.ID = uuid.New()
	}
	if template.Version == 0 {
		template.Version = 1
	}
	if m.templates == nil {
		m.templates = make(map[uuid.UUID]repo.FlowTemplate)
	}
	m.templates[template.ID] = template
	return template, nil
}

func (m *mockFlowTemplateRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowTemplate, error) {
	if m.templates == nil {
		return repo.FlowTemplate{}, repo.ErrNotFound
	}
	template, ok := m.templates[id]
	if !ok {
		return repo.FlowTemplate{}, repo.ErrNotFound
	}
	return template, nil
}

func (m *mockFlowTemplateRepo) ListCurrent(context.Context, *uuid.UUID, *uuid.UUID) ([]repo.FlowTemplate, error) {
	return nil, nil
}

func (m *mockFlowTemplateRepo) GetCurrentBySlug(_ context.Context, organizationID, projectID *uuid.UUID, slug string) (repo.FlowTemplate, error) {
	for _, template := range m.templates {
		if template.Slug != slug {
			continue
		}
		switch {
		case organizationID != nil:
			if template.OrganizationID != nil && *template.OrganizationID == *organizationID {
				return template, nil
			}
		case projectID != nil:
			if template.ProjectID != nil && *template.ProjectID == *projectID {
				return template, nil
			}
		default:
			if template.OrganizationID == nil && template.ProjectID == nil {
				return template, nil
			}
		}
	}
	return repo.FlowTemplate{}, repo.ErrNotFound
}

func (m *mockFlowTemplateRepo) Update(_ context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error) {
	if m.templates == nil {
		m.templates = make(map[uuid.UUID]repo.FlowTemplate)
	}
	m.templates[template.ID] = template
	return template, nil
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
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

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

func TestTaskUpdateQueuedOversizedTaskCreatesDecomposedChildWorkUnits(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			Title:          "Blog migration epic",
			Description:    &description,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
			Metadata:       taskdecomp.ApplyQueueDecompositionMode(json.RawMessage(`{}`), taskdecomp.QueueDecompositionModeParallelChildren),
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if tasks.createCalls < 1 {
		t.Fatalf("create calls = %d, want >= 1", tasks.createCalls)
	}
	if tasks.updateCalls != tasks.createCalls+1 {
		t.Fatalf("update calls = %d, want %d parent+child queue updates", tasks.updateCalls, tasks.createCalls+1)
	}
	if tasks.task.Description == nil || !strings.Contains(*tasks.task.Description, "Migrate all legacy markdown posts") {
		t.Fatalf("updated description = %v, want focused primary deliverable", tasks.task.Description)
	}
	if tasks.task.WorkStatus != "draft" {
		t.Fatalf("parent work_status = %q, want draft orchestration-only state", tasks.task.WorkStatus)
	}
	decomp, ok := out["decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("decomposition output = %T, want map[string]any", out["decomposition"])
	}
	if applied, _ := decomp["applied"].(bool); !applied {
		t.Fatalf("decomposition.applied = %v, want true", decomp["applied"])
	}
	childIDs, ok := decomp["child_task_ids"].([]string)
	if !ok {
		t.Fatalf("decomposition.child_task_ids = %T, want []string", decomp["child_task_ids"])
	}
	if len(childIDs) != tasks.createCalls {
		t.Fatalf("decomposition.child_task_ids len = %d, want %d", len(childIDs), tasks.createCalls)
	}
	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if got := taskOut["work_status"]; got != "draft" {
		t.Fatalf("task output work_status = %v, want draft", got)
	}
	queuedChildren := 0
	for _, created := range tasks.createdTasks {
		if created.WorkStatus == "queued" {
			queuedChildren++
		}
	}
	if queuedChildren != tasks.createCalls {
		t.Fatalf("queued child count = %d, want %d", queuedChildren, tasks.createCalls)
	}
}

func TestTaskUpdateQueuedOversizedTaskAutoDecomposesCompoundWork(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			Title:          "Blog migration epic",
			Description:    &description,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
			Metadata:       json.RawMessage(`{}`),
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if tasks.createCalls < 1 {
		t.Fatalf("create calls = %d, want >= 1 for compound work", tasks.createCalls)
	}
	if _, ok := out["decomposition"]; !ok {
		t.Fatalf("decomposition output missing for compound work: %v", out)
	}
	if tasks.task.Description == nil || !strings.Contains(*tasks.task.Description, "Migrate all legacy markdown posts") {
		t.Fatalf("updated description = %v, want focused primary deliverable", tasks.task.Description)
	}
}

func TestTaskUpdateQueuedBroadEnumeratedTitleAutoDecomposesCompoundWork(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	description := "Develop the content backlog for the launch."
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			Title:          "Generate 20 new blog post ideas across all pillars",
			Description:    &description,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
			Metadata:       json.RawMessage(`{}`),
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if tasks.createCalls < 1 {
		t.Fatalf("create calls = %d, want >= 1 for broad enumerated title", tasks.createCalls)
	}
	if _, ok := out["decomposition"]; !ok {
		t.Fatalf("decomposition output missing for broad enumerated title: %v", out)
	}
	if tasks.task.Description == nil || !strings.Contains(*tasks.task.Description, "Generate 20 new blog post ideas across all pillars") {
		t.Fatalf("updated description = %v, want focused primary deliverable copied from title", tasks.task.Description)
	}
}

func TestTaskUpdateQueuedOversizedTaskPublishesTaskCreatedEventsForDecomposedChildren(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			Title:          "Blog migration epic",
			Description:    &description,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
			Metadata:       taskdecomp.ApplyQueueDecompositionMode(json.RawMessage(`{}`), taskdecomp.QueueDecompositionModeParallelChildren),
		},
	}
	publisher := &recordingEventPublisher{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

	if _, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	}); err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if tasks.createCalls < 1 {
		t.Fatalf("create calls = %d, want >= 1", tasks.createCalls)
	}

	createdEvents := make([]eventbus.DomainEvent, 0, len(publisher.events))
	for _, event := range publisher.events {
		if event.EventType == "task.created" {
			createdEvents = append(createdEvents, event)
		}
	}
	if len(createdEvents) != tasks.createCalls {
		t.Fatalf("task.created events = %d, want %d", len(createdEvents), tasks.createCalls)
	}

	for _, event := range createdEvents {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal task.created payload: %v", err)
		}
		if payload["decomposition_applied"] != true {
			t.Fatalf("decomposition_applied = %v, want true", payload["decomposition_applied"])
		}
		if payload["decomposition_parent"] != taskID.String() {
			t.Fatalf("decomposition_parent = %v, want %s", payload["decomposition_parent"], taskID.String())
		}
	}
}

func TestTaskUpdateQueuedOversizedTaskReusesExistingDecomposedChildren(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")
	childOneID := uuid.New()
	childTwoID := uuid.New()
	childOneMetadata := json.RawMessage(fmt.Sprintf(`{"decomposition_parent_task_id":"%s","workstream_index":2}`, taskID))
	childTwoMetadata := json.RawMessage(fmt.Sprintf(`{"decomposition_parent_task_id":"%s","workstream_index":3}`, taskID))

	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      projectID,
			Title:          "Blog migration epic",
			Description:    &description,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
			Metadata:       taskdecomp.ApplyQueueDecompositionMode(json.RawMessage(`{}`), taskdecomp.QueueDecompositionModeParallelChildren),
		},
		listByProjectTasks: []repo.ProjectTask{
			{
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				Title:          "Blog migration epic",
				Description:    &description,
				WorkStatus:     "draft",
				FlowTemplateID: &flowTemplateID,
				Metadata:       taskdecomp.ApplyQueueDecompositionMode(json.RawMessage(`{}`), taskdecomp.QueueDecompositionModeParallelChildren),
			},
			{
				ID:             childOneID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     2,
				Title:          "Blog migration epic (Workstream 2)",
				Description: func() *string {
					value := "Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage."
					return &value
				}(),
				WorkStatus:     "draft",
				FlowTemplateID: &flowTemplateID,
				Metadata:       childOneMetadata,
			},
			{
				ID:             childTwoID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     3,
				Title:          "Blog migration epic (Workstream 3)",
				Description: func() *string {
					value := "Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots."
					return &value
				}(),
				WorkStatus:     "draft",
				FlowTemplateID: &flowTemplateID,
				Metadata:       childTwoMetadata,
			},
		},
	}
	publisher := &recordingEventPublisher{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if tasks.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", tasks.createCalls)
	}

	decomp, ok := out["decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("decomposition output = %T, want map[string]any", out["decomposition"])
	}
	childIDs, ok := decomp["child_task_ids"].([]string)
	if !ok {
		t.Fatalf("decomposition.child_task_ids = %T, want []string", decomp["child_task_ids"])
	}
	if len(childIDs) != 2 {
		t.Fatalf("decomposition.child_task_ids len = %d, want 2", len(childIDs))
	}
	if mustUUIDFromAny(t, childIDs[0]) != childOneID {
		t.Fatalf("first child task id = %v, want %s", childIDs[0], childOneID)
	}
	if mustUUIDFromAny(t, childIDs[1]) != childTwoID {
		t.Fatalf("second child task id = %v, want %s", childIDs[1], childTwoID)
	}
	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if got := taskOut["work_status"]; got != "draft" {
		t.Fatalf("task output work_status = %v, want draft", got)
	}
	if tasks.task.WorkStatus != "draft" {
		t.Fatalf("parent work_status = %q, want draft orchestration-only state", tasks.task.WorkStatus)
	}
	for _, task := range tasks.listByProjectTasks {
		if task.ID != childOneID && task.ID != childTwoID {
			continue
		}
		if task.WorkStatus != "queued" {
			t.Fatalf("child task %s work_status = %q, want queued", task.ID, task.WorkStatus)
		}
	}

	for _, event := range publisher.events {
		if event.EventType == "task.created" {
			t.Fatalf("unexpected task.created event for reused decomposition child: %+v", event)
		}
	}
}

func TestTaskCreateProjectSessionReusesExistingDraftTask(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()
	existingTaskID := uuid.New()

	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                  existingTaskID,
			OrganizationID:      orgID,
			ProjectID:           projectID,
			TaskNumber:          3,
			Title:               "Set up CMS import mapping",
			WorkStatus:          "draft",
			BlocksScope:         "none",
			RequiresHumanReview: false,
			Metadata:            json.RawMessage(`{}`),
		},
		listByProjectTasks: []repo.ProjectTask{
			{
				ID:                  existingTaskID,
				OrganizationID:      orgID,
				ProjectID:           projectID,
				TaskNumber:          3,
				Title:               "Set up CMS import mapping",
				WorkStatus:          "draft",
				BlocksScope:         "none",
				RequiresHumanReview: false,
				Metadata:            json.RawMessage(`{}`),
			},
		},
	}
	sessions := &fakeChatSessionRepo{
		sessions: []repo.ChatSession{
			{
				ID:             sessionID,
				OrganizationID: orgID,
				ScopeType:      "project",
				ScopeID:        projectID,
				Mode:           "async",
				Status:         "active",
			},
		},
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.chatSessions = sessions

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
		SessionID:      &sessionID,
	})

	out, err := executor.Execute(ctx, "task.create", map[string]any{
		"project_id":            projectID.String(),
		"title":                 "Set up CMS import mapping",
		"description":           "Map legacy markdown content to the CMS schema and validate canonical slugs.",
		"blocks_scope":          "all",
		"requires_human_review": true,
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	if tasks.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", tasks.createCalls)
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}
	if tasks.task.Description == nil || !strings.Contains(*tasks.task.Description, "Map legacy markdown content") {
		t.Fatalf("description = %v, want repaired description", tasks.task.Description)
	}
	if tasks.task.BlocksScope != "all" {
		t.Fatalf("blocks_scope = %q, want all", tasks.task.BlocksScope)
	}
	if !tasks.task.RequiresHumanReview {
		t.Fatal("requires_human_review = false, want true")
	}

	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if mustUUIDFromAny(t, taskOut["id"]) != existingTaskID {
		t.Fatalf("task.id = %v, want %s", taskOut["id"], existingTaskID)
	}
	if taskOut["task_number"] != 3 {
		t.Fatalf("task_number = %v, want 3", taskOut["task_number"])
	}
}

func TestTaskCreateSubjectiveMultiOptionAutoAssignsReviewRefinementTemplate(t *testing.T) {
	projectID := uuid.New()
	orgID := uuid.New()
	templateID := uuid.New()
	internalReviewID := uuid.New()
	humanReviewID := uuid.New()
	mergeID := uuid.New()
	description := "Generate 10 homepage design options, compare them, shortlist the best ones, and recommend a direction with tradeoffs."

	tasks := &mockTaskRepo{}
	projects := &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: orgID,
				Slug:           "design-project",
				DisplayName:    "Design Project",
			},
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.projects = projects
	executor.flowTemplates = &mockFlowTemplateRepo{
		templates: map[uuid.UUID]repo.FlowTemplate{
			templateID: {
				ID:             templateID,
				OrganizationID: nil,
				ProjectID:      nil,
				Slug:           "default-review-refinement",
				DisplayName:    "Default Review Refinement",
				IsCurrent:      true,
			},
		},
	}
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			templateID: {
				{
					ID:             uuid.New(),
					FlowTemplateID: templateID,
					DisplayName:    "Generation",
					NodeType:       "work",
					Position:       1,
					NextNodeID:     &internalReviewID,
				},
				{
					ID:             internalReviewID,
					FlowTemplateID: templateID,
					DisplayName:    "Internal Review",
					NodeType:       "review",
					Position:       2,
					NextNodeID:     &humanReviewID,
				},
				{
					ID:                  humanReviewID,
					FlowTemplateID:      templateID,
					DisplayName:         "Human Review",
					NodeType:            "review",
					Position:            3,
					RequiresHumanReview: true,
					NextNodeID:          &mergeID,
				},
				{
					ID:             mergeID,
					FlowTemplateID: templateID,
					DisplayName:    "Merge",
					NodeType:       "merge",
					Position:       4,
				},
			},
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.create", map[string]any{
		"project_id":   projectID.String(),
		"title":        "Homepage design options",
		"description":  description,
		"blocks_scope": "none",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	if len(tasks.createdTasks) != 1 {
		t.Fatalf("created task count = %d, want 1", len(tasks.createdTasks))
	}
	if tasks.createdTasks[0].FlowTemplateID == nil || *tasks.createdTasks[0].FlowTemplateID != templateID {
		t.Fatalf("flow_template_id = %v, want %s", tasks.createdTasks[0].FlowTemplateID, templateID)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["mode"] != "review_and_refinement" {
		t.Fatalf("planning.mode = %v, want review_and_refinement", planning["mode"])
	}
	if planning["playbook"] != taskplan.PlaybookStrategy {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookStrategy)
	}
	stages, ok := planning["planned_stages"].([]string)
	if !ok {
		rawStages, castOK := planning["planned_stages"].([]any)
		if !castOK {
			t.Fatalf("planned_stages = %T, want slice", planning["planned_stages"])
		}
		stages = make([]string, 0, len(rawStages))
		for _, item := range rawStages {
			stages = append(stages, fmt.Sprintf("%v", item))
		}
	}
	if len(stages) != 3 {
		t.Fatalf("planned stages len = %d, want 3", len(stages))
	}

	var metadata map[string]any
	if err := json.Unmarshal(tasks.createdTasks[0].Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	planningMeta, ok := metadata["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning metadata = %T, want map[string]any", metadata["planning"])
	}
	if planningMeta["default_template_slug"] != "default-review-refinement" {
		t.Fatalf("default_template_slug = %v, want default-review-refinement", planningMeta["default_template_slug"])
	}
	if planningMeta["playbook"] != taskplan.PlaybookStrategy {
		t.Fatalf("playbook = %v, want %s", planningMeta["playbook"], taskplan.PlaybookStrategy)
	}
}

func TestTaskCreateDelegatedCreativePolicyUsesInternalReviewTemplate(t *testing.T) {
	projectID := uuid.New()
	orgID := uuid.New()
	templateID := uuid.New()
	internalReviewID := uuid.New()
	mergeID := uuid.New()
	description := "Generate 10 homepage design options, compare them, and recommend a direction that stays within the brand guardrails."

	tasks := &mockTaskRepo{}
	projects := &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: orgID,
				Slug:           "editorial-project",
				DisplayName:    "Editorial Project",
				Settings: taskplan.ApplyReviewPolicy(nil, taskplan.ReviewPolicy{
					Mode:       taskplan.PolicyDelegatedAuthority,
					Guardrails: []string{"Use OtterCamp voice", "Avoid unsupported claims"},
				}),
			},
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.projects = projects
	executor.flowTemplates = &mockFlowTemplateRepo{
		templates: map[uuid.UUID]repo.FlowTemplate{
			templateID: {
				ID:        templateID,
				Slug:      "default-review",
				IsCurrent: true,
			},
		},
	}
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			templateID: {
				{
					ID:             uuid.New(),
					FlowTemplateID: templateID,
					DisplayName:    "Work",
					NodeType:       "work",
					Position:       1,
					NextNodeID:     &internalReviewID,
				},
				{
					ID:             internalReviewID,
					FlowTemplateID: templateID,
					DisplayName:    "Internal Review",
					NodeType:       "review",
					Position:       2,
					NextNodeID:     &mergeID,
				},
				{
					ID:             mergeID,
					FlowTemplateID: templateID,
					DisplayName:    "Merge",
					NodeType:       "merge",
					Position:       3,
				},
			},
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.create", map[string]any{
		"project_id":   projectID.String(),
		"title":        "Homepage design options",
		"description":  description,
		"blocks_scope": "none",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	if len(tasks.createdTasks) != 1 {
		t.Fatalf("created task count = %d, want 1", len(tasks.createdTasks))
	}
	if tasks.createdTasks[0].FlowTemplateID == nil || *tasks.createdTasks[0].FlowTemplateID != templateID {
		t.Fatalf("flow_template_id = %v, want %s", tasks.createdTasks[0].FlowTemplateID, templateID)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["mode"] != taskplan.ModeAutonomousInternal {
		t.Fatalf("planning.mode = %v, want %s", planning["mode"], taskplan.ModeAutonomousInternal)
	}
	if planning["default_template_slug"] != taskplan.InternalReviewTemplate {
		t.Fatalf("default_template_slug = %v, want %s", planning["default_template_slug"], taskplan.InternalReviewTemplate)
	}
	if planning["playbook"] != taskplan.PlaybookStrategy {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookStrategy)
	}
	reviewPolicy, ok := planning["review_policy"].(map[string]any)
	if !ok {
		t.Fatalf("planning.review_policy = %T, want map[string]any", planning["review_policy"])
	}
	if reviewPolicy["mode"] != taskplan.PolicyDelegatedAuthority {
		t.Fatalf("planning.review_policy.mode = %v, want %s", reviewPolicy["mode"], taskplan.PolicyDelegatedAuthority)
	}

	var metadata map[string]any
	if err := json.Unmarshal(tasks.createdTasks[0].Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	planningMeta, ok := metadata["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning metadata = %T, want map[string]any", metadata["planning"])
	}
	if planningMeta["review_policy_mode"] != taskplan.PolicyDelegatedAuthority {
		t.Fatalf("review_policy_mode = %v, want %s", planningMeta["review_policy_mode"], taskplan.PolicyDelegatedAuthority)
	}
	if planningMeta["default_template_slug"] != taskplan.InternalReviewTemplate {
		t.Fatalf("default_template_slug = %v, want %s", planningMeta["default_template_slug"], taskplan.InternalReviewTemplate)
	}
	if planningMeta["playbook"] != taskplan.PlaybookStrategy {
		t.Fatalf("playbook = %v, want %s", planningMeta["playbook"], taskplan.PlaybookStrategy)
	}
}

func TestTaskCreateVerifiableRequestDoesNotAutoAssignReviewRefinementTemplate(t *testing.T) {
	projectID := uuid.New()
	orgID := uuid.New()

	tasks := &mockTaskRepo{}
	projects := &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: orgID,
				Slug:           "ops-project",
				DisplayName:    "Ops Project",
			},
		},
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.projects = projects
	executor.flowTemplates = &mockFlowTemplateRepo{}
	executor.flowNodes = &mockFlowNodeRepo{}

	out, err := executor.Execute(testExecCtx(), "task.create", map[string]any{
		"project_id":  projectID.String(),
		"title":       "Verify webhook retry backoff",
		"description": "Confirm whether the webhook retry policy still uses exponential backoff and report the configured maximum delay.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["mode"] != taskplan.ModeExecutionFirst {
		t.Fatalf("planning.mode = %v, want %s", planning["mode"], taskplan.ModeExecutionFirst)
	}
	if planning["playbook"] != taskplan.PlaybookExecutionSpec {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookExecutionSpec)
	}
	if len(tasks.createdTasks) != 1 {
		t.Fatalf("created task count = %d, want 1", len(tasks.createdTasks))
	}
	if tasks.createdTasks[0].FlowTemplateID != nil {
		t.Fatalf("flow_template_id = %v, want nil", tasks.createdTasks[0].FlowTemplateID)
	}
	var metadata map[string]any
	if err := json.Unmarshal(tasks.createdTasks[0].Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	planningMeta, ok := metadata["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning metadata = %T, want map[string]any", metadata["planning"])
	}
	if planningMeta["playbook"] != taskplan.PlaybookExecutionSpec {
		t.Fatalf("playbook = %v, want %s", planningMeta["playbook"], taskplan.PlaybookExecutionSpec)
	}
}

func TestTaskUpdateSetsFlowTemplateIDWhileDraft(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
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
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":          taskID.String(),
		"flow_template_id": flowTemplateID.String(),
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if _, ok := out["task"].(map[string]any); !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if tasks.task.FlowTemplateID == nil || *tasks.task.FlowTemplateID != flowTemplateID {
		t.Fatalf("flow_template_id = %v, want %s", tasks.task.FlowTemplateID, flowTemplateID)
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}
}

func TestTaskUpdateRejectsFlowTemplateChangeOutsideDraft(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			WorkStatus:     "in_progress",
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":          taskID.String(),
		"flow_template_id": flowTemplateID.String(),
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != "flow_template_id can only be changed while task is draft" {
		t.Fatalf("error = %v, want draft-only flow template message", out["error"])
	}
	if tasks.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", tasks.updateCalls)
	}
}

func TestTaskUpdateReviewPolicyOverrideBeatsProjectPolicyWhenQueuing(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	reviewTemplateID := uuid.New()
	reviewRefinementTemplateID := uuid.New()
	reviewInternalID := uuid.New()
	reviewHumanID := uuid.New()
	reviewMergeID := uuid.New()
	reviewRefinementMergeID := uuid.New()
	description := "Generate 10 homepage design options, compare them, and recommend the strongest one."

	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Homepage design options",
			Description:    &description,
			WorkStatus:     "draft",
			BlocksScope:    "none",
			Metadata:       json.RawMessage(`{}`),
			CreatedByType:  "agent",
		},
	}
	projects := &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: orgID,
				Settings: taskplan.ApplyReviewPolicy(nil, taskplan.ReviewPolicy{
					Mode: taskplan.PolicyHumanReviewRequired,
				}),
			},
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks
	executor.projects = projects
	executor.flowTemplates = &mockFlowTemplateRepo{
		templates: map[uuid.UUID]repo.FlowTemplate{
			reviewTemplateID: {
				ID:        reviewTemplateID,
				Slug:      "default-review",
				IsCurrent: true,
			},
			reviewRefinementTemplateID: {
				ID:        reviewRefinementTemplateID,
				Slug:      "default-review-refinement",
				IsCurrent: true,
			},
		},
	}
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			reviewTemplateID: {
				{
					ID:             uuid.New(),
					FlowTemplateID: reviewTemplateID,
					DisplayName:    "Work",
					NodeType:       "work",
					Position:       1,
					NextNodeID:     &reviewInternalID,
				},
				{
					ID:             reviewInternalID,
					FlowTemplateID: reviewTemplateID,
					DisplayName:    "Internal Review",
					NodeType:       "review",
					Position:       2,
					NextNodeID:     &reviewMergeID,
				},
				{
					ID:             reviewMergeID,
					FlowTemplateID: reviewTemplateID,
					DisplayName:    "Merge",
					NodeType:       "merge",
					Position:       3,
				},
			},
			reviewRefinementTemplateID: {
				{
					ID:             uuid.New(),
					FlowTemplateID: reviewRefinementTemplateID,
					DisplayName:    "Generation",
					NodeType:       "work",
					Position:       1,
					NextNodeID:     &reviewInternalID,
				},
				{
					ID:             reviewInternalID,
					FlowTemplateID: reviewRefinementTemplateID,
					DisplayName:    "Internal Review",
					NodeType:       "review",
					Position:       2,
					NextNodeID:     &reviewHumanID,
				},
				{
					ID:                  reviewHumanID,
					FlowTemplateID:      reviewRefinementTemplateID,
					DisplayName:         "Human Review",
					NodeType:            "review",
					Position:            3,
					RequiresHumanReview: true,
					NextNodeID:          &reviewRefinementMergeID,
				},
				{
					ID:             reviewRefinementMergeID,
					FlowTemplateID: reviewRefinementTemplateID,
					DisplayName:    "Merge",
					NodeType:       "merge",
					Position:       4,
				},
			},
		},
	}

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
		"review_policy": map[string]any{
			"mode":       taskplan.PolicyDelegatedAuthority,
			"guardrails": []string{"Use OtterCamp voice"},
		},
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if tasks.task.FlowTemplateID == nil || *tasks.task.FlowTemplateID != reviewTemplateID {
		t.Fatalf("flow_template_id = %v, want %s", tasks.task.FlowTemplateID, reviewTemplateID)
	}
	if tasks.task.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", tasks.task.WorkStatus)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["default_template_slug"] != taskplan.InternalReviewTemplate {
		t.Fatalf("default_template_slug = %v, want %s", planning["default_template_slug"], taskplan.InternalReviewTemplate)
	}
	if planning["playbook"] != taskplan.PlaybookStrategy {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookStrategy)
	}
	policy, ok := taskplan.ParseReviewPolicy(tasks.task.Metadata)
	if !ok {
		t.Fatalf("ParseReviewPolicy(metadata) = false, want true")
	}
	if policy.Mode != taskplan.PolicyDelegatedAuthority {
		t.Fatalf("policy mode = %q, want %s", policy.Mode, taskplan.PolicyDelegatedAuthority)
	}
}

func TestFlowCreateTemplateRejectsNodesWithoutReview(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.flowTemplates = &mockFlowTemplateRepo{}
	executor.flowNodes = &mockFlowNodeRepo{}
	projectID := uuid.New()

	out, err := executor.Execute(testExecCtx(), "flow.create_template", map[string]any{
		"project_id": projectID.String(),
		"name":       "No Review Template",
		"nodes": []any{
			map[string]any{
				"display_name": "Work",
				"node_type":    "work",
			},
		},
	})
	if err != nil {
		t.Fatalf("flow.create_template: %v", err)
	}
	if out["error"] != flowTemplateValidationMessage {
		t.Fatalf("error = %v, want executable-flow validation message", out["error"])
	}
}

func TestFlowCreateTemplateAllowsNonMapNodeWhenReviewAndMergeNodesPresent(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.flowTemplates = &mockFlowTemplateRepo{}
	executor.flowNodes = &mockFlowNodeRepo{}
	projectID := uuid.New()

	out, err := executor.Execute(testExecCtx(), "flow.create_template", map[string]any{
		"project_id": projectID.String(),
		"name":       "Template With Review",
		"nodes": []any{
			map[string]any{
				"display_name": "Work",
				"node_type":    "work",
			},
			map[string]any{
				"display_name": "Review",
				"node_type":    "review",
			},
			map[string]any{
				"display_name": "Merge",
				"node_type":    "merge",
			},
			"ignore-me",
		},
	})
	if err != nil {
		t.Fatalf("flow.create_template: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("error = %v, want nil", out["error"])
	}
	templateOut, ok := out["template"].(map[string]any)
	if !ok {
		t.Fatalf("template output = %T, want map[string]any", out["template"])
	}
	if templateOut["id"] == nil {
		t.Fatalf("template id = %v, want non-nil", templateOut["id"])
	}
}

func TestFlowCreateTemplateRejectsInvalidNodeTypeWithBoundedError(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.flowTemplates = &mockFlowTemplateRepo{}
	executor.flowNodes = &mockFlowNodeRepo{}
	projectID := uuid.New()

	out, err := executor.Execute(testExecCtx(), "flow.create_template", map[string]any{
		"project_id": projectID.String(),
		"name":       "Invalid Node Type",
		"nodes": []any{
			map[string]any{
				"display_name": "Work",
				"node_type":    "work",
			},
			map[string]any{
				"display_name": "QA Gate",
				"node_type":    "qa_gate",
			},
			map[string]any{
				"display_name": "Merge",
				"node_type":    "merge",
			},
		},
	})
	if err != nil {
		t.Fatalf("flow.create_template: %v", err)
	}
	if out["error"] != "invalid_node_type" {
		t.Fatalf("error = %v, want invalid_node_type", out["error"])
	}
	if out["invalid_node_type"] != "qa_gate" {
		t.Fatalf("invalid_node_type = %v, want qa_gate", out["invalid_node_type"])
	}
	if out["message"] == flowTemplateValidationMessage {
		t.Fatalf("message = %v, want bounded invalid-node guidance", out["message"])
	}
}

func TestTaskUpdateSetsAssignedAgentID(t *testing.T) {
	taskID := uuid.New()
	assignedAgentID := uuid.New()
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
		"task_id":           taskID.String(),
		"assigned_agent_id": assignedAgentID.String(),
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if _, ok := out["task"].(map[string]any); !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if tasks.task.AssignedAgentID == nil || *tasks.task.AssignedAgentID != assignedAgentID {
		t.Fatalf("assigned_agent_id = %v, want %s", tasks.task.AssignedAgentID, assignedAgentID)
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
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

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
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

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
	const flowTemplateRequiredMessage = "task requires a flow template before it can be queued"
	if out["error"] != flowTemplateRequiredMessage {
		t.Fatalf("error = %v, want %q", out["error"], flowTemplateRequiredMessage)
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

func TestTaskUpdateRejectsDoneWhenPlanningContractIncomplete(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	flowNodeID := uuid.New()
	description := "Run customer interviews, capture assumptions, and design a validation plan for this new product idea."
	plan := taskplan.Analyze("Validate the onboarding problem before writing specs", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)
	metadata, _, _, err := taskplan.ApplyProcessUpdate(metadata, taskplan.ProcessUpdate{
		Artifacts: []taskplan.ArtifactEvidence{
			{
				Slug:     "problem-brief",
				Summary:  "We have a real onboarding drop-off.",
				Sections: []string{"problem"},
			},
		},
		HasArtifactChanges: true,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}

	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                taskID,
			OrganizationID:    uuid.New(),
			ProjectID:         uuid.New(),
			WorkStatus:        "review",
			FlowTemplateID:    &flowTemplateID,
			CurrentFlowNodeID: &flowNodeID,
			Metadata:          metadata,
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
	if got := fmt.Sprintf("%v", out["error"]); !strings.Contains(got, "planning artifact contract is incomplete") {
		t.Fatalf("error = %v, want planning artifact contract failure", out["error"])
	}
	if tasks.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", tasks.updateCalls)
	}
}

func TestTaskUpdateAllowsDoneWithPlanningOverrideReason(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	flowNodeID := uuid.New()
	description := "Write the PRD, requirements, implementation plan, and acceptance criteria for the billing migration."
	plan := taskplan.Analyze("PRD for billing migration", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)

	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                taskID,
			OrganizationID:    uuid.New(),
			ProjectID:         uuid.New(),
			WorkStatus:        "review",
			FlowTemplateID:    &flowTemplateID,
			CurrentFlowNodeID: &flowNodeID,
			Metadata:          metadata,
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
		"planning_artifacts": []map[string]any{
			{
				"slug":     "prd",
				"summary":  "Billing migration scope and core requirements.",
				"sections": []string{"objective", "scope"},
			},
		},
		"planning_override_reason": "Dependency owners are still confirming rollout sequencing.",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("task.update error = %v, want nil", out["error"])
	}
	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task output = %T, want map[string]any", out["task"])
	}
	if got := taskOut["work_status"]; got != "done" {
		t.Fatalf("work_status = %v, want done", got)
	}
	planningOut, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planningOut["process_status"] != taskplan.ProcessStatusOverridden {
		t.Fatalf("process_status = %v, want %s", planningOut["process_status"], taskplan.ProcessStatusOverridden)
	}
	override, ok := planningOut["override"].(map[string]any)
	if !ok {
		t.Fatalf("override output = %T, want map[string]any", planningOut["override"])
	}
	if override["reason"] != "Dependency owners are still confirming rollout sequencing." {
		t.Fatalf("override reason = %v, want recorded reason", override["reason"])
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}
}

func TestTaskUpdateHydratesPlanningMetadataBeforePersistingArtifacts(t *testing.T) {
	taskID := uuid.New()
	description := "Implement the billing migration plan, document scope boundaries, and capture the acceptance criteria before development starts."

	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			Title:          "Billing migration implementation plan",
			Description:    &description,
			WorkStatus:     "draft",
			Metadata:       json.RawMessage(`{}`),
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id": taskID.String(),
		"planning_artifacts": []map[string]any{
			{
				"slug":     "prd",
				"summary":  "Billing migration scope, constraints, and implementation expectations.",
				"sections": []string{"goals", "scope", "constraints"},
			},
		},
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("task.update error = %v, want nil", out["error"])
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}

	plan, ok := taskplan.Parse(tasks.task.Metadata)
	if !ok {
		t.Fatal("taskplan.Parse(metadata) = false, want true")
	}
	if plan.Playbook != taskplan.PlaybookExecutionSpec {
		t.Fatalf("playbook = %q, want %q", plan.Playbook, taskplan.PlaybookExecutionSpec)
	}
	if len(plan.ArtifactEvidence) != 1 {
		t.Fatalf("artifact_evidence = %d, want 1", len(plan.ArtifactEvidence))
	}
	if plan.ArtifactEvidence[0].Slug != "prd" {
		t.Fatalf("artifact slug = %q, want prd", plan.ArtifactEvidence[0].Slug)
	}
	planningOut, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planningOut["playbook"] != taskplan.PlaybookExecutionSpec {
		t.Fatalf("planning.playbook = %v, want %s", planningOut["playbook"], taskplan.PlaybookExecutionSpec)
	}
}

func TestTaskUpdatePersistsPlanningFollowOnStopReason(t *testing.T) {
	taskID := uuid.New()
	description := "Run customer interviews, document assumptions, and build a validation plan for this new product idea before we commit scope."
	plan := taskplan.Analyze("Validate the onboarding problem", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)

	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: uuid.New(),
			ProjectID:      uuid.New(),
			WorkStatus:     "draft",
			Metadata:       metadata,
		},
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.tasks = tasks

	stopReason := "The PM wants to choose the winning opportunity before any downstream planning task is created."
	out, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":                        taskID.String(),
		"planning_follow_on_stop_reason": stopReason,
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("task.update error = %v, want nil", out["error"])
	}
	if tasks.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", tasks.updateCalls)
	}

	planningOut, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	followOn, ok := planningOut["follow_on"].(map[string]any)
	if !ok {
		t.Fatalf("planning.follow_on = %T, want map[string]any", planningOut["follow_on"])
	}
	if followOn["status"] != taskplan.FollowOnStatusStopped {
		t.Fatalf("follow_on.status = %v, want %s", followOn["status"], taskplan.FollowOnStatusStopped)
	}
	if followOn["stop_reason"] != stopReason {
		t.Fatalf("follow_on.stop_reason = %v, want recorded reason", followOn["stop_reason"])
	}

	parsed, ok := taskplan.Parse(tasks.task.Metadata)
	if !ok {
		t.Fatal("taskplan.Parse(metadata) = false, want true")
	}
	if parsed.FollowOnStopReason != stopReason {
		t.Fatalf("FollowOnStopReason = %q, want %q", parsed.FollowOnStopReason, stopReason)
	}
	if parsed.FollowOn.Status != taskplan.FollowOnStatusStopped {
		t.Fatalf("FollowOn.Status = %q, want %q", parsed.FollowOn.Status, taskplan.FollowOnStatusStopped)
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

func TestTaskUpdatePublishesStatusChangedEventWhenWorkStatusChanges(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	flowTemplateID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
		},
	}
	publisher := &recordingEventPublisher{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.tasks = tasks
	executor.flowNodes = &mockFlowNodeRepo{
		templateNodes: map[uuid.UUID][]repo.FlowNode{
			flowTemplateID: validExecutableTemplateNodeList(flowTemplateID),
		},
	}

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
	if taskOut["work_status"] != "queued" {
		t.Fatalf("work_status = %v, want queued", taskOut["work_status"])
	}
	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.events))
	}
	if publisher.events[0].EventType != "task.status_changed" {
		t.Fatalf("event type = %q, want task.status_changed", publisher.events[0].EventType)
	}

	var payload map[string]any
	if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["from_status"] != "draft" {
		t.Fatalf("from_status = %v, want draft", payload["from_status"])
	}
	if payload["to_status"] != "queued" {
		t.Fatalf("to_status = %v, want queued", payload["to_status"])
	}
	if payload["task_id"] != taskID.String() {
		t.Fatalf("task_id = %v, want %s", payload["task_id"], taskID.String())
	}
}

func TestTaskUpdateDoesNotPublishStatusChangedEventWhenWorkStatusUnchanged(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	flowTemplateID := uuid.New()
	tasks := &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
		},
	}
	publisher := &recordingEventPublisher{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Events:        publisher,
	})
	executor.tasks = tasks

	if _, err := executor.Execute(testExecCtx(), "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "draft",
	}); err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("published events = %d, want 0", len(publisher.events))
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

func mustUUIDFromAny(t *testing.T, raw any) uuid.UUID {
	t.Helper()
	switch typed := raw.(type) {
	case uuid.UUID:
		return typed
	case string:
		parsed, err := uuid.Parse(strings.TrimSpace(typed))
		if err != nil {
			t.Fatalf("parse uuid %q: %v", typed, err)
		}
		return parsed
	default:
		t.Fatalf("unsupported uuid value type %T (%v)", raw, raw)
		return uuid.Nil
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
func (f *fakeProjectRepo) Update(_ context.Context, projectRecord repo.Project) (repo.Project, error) {
	if f.projects == nil {
		f.projects = make(map[uuid.UUID]repo.Project)
	}
	f.projects[projectRecord.ID] = projectRecord
	return projectRecord, nil
}
func (f *fakeProjectRepo) Archive(context.Context, uuid.UUID) error { return nil }

type fakeAssignmentRepo struct {
	assignments []repo.AgentProjectAssignment
	assignErr   error
}

func (f *fakeAssignmentRepo) Assign(_ context.Context, a repo.AgentProjectAssignment) (repo.AgentProjectAssignment, error) {
	if f.assignErr != nil {
		return repo.AgentProjectAssignment{}, f.assignErr
	}
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

type fakeAgentRepo struct {
	agents map[uuid.UUID]repo.Agent
}

func (f *fakeAgentRepo) Create(_ context.Context, agent repo.Agent) (repo.Agent, error) {
	agent.ID = uuid.New()
	if f.agents == nil {
		f.agents = make(map[uuid.UUID]repo.Agent)
	}
	f.agents[agent.ID] = agent
	return agent, nil
}

func (f *fakeAgentRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.agents == nil {
		return repo.Agent{}, repo.ErrNotFound
	}
	agent, ok := f.agents[id]
	if !ok {
		return repo.Agent{}, repo.ErrNotFound
	}
	return agent, nil
}

func (f *fakeAgentRepo) List(_ context.Context, _ uuid.UUID) ([]repo.Agent, error) {
	items := make([]repo.Agent, 0, len(f.agents))
	for _, agent := range f.agents {
		items = append(items, agent)
	}
	return items, nil
}

func (f *fakeAgentRepo) Update(_ context.Context, agent repo.Agent) (repo.Agent, error) {
	if f.agents == nil {
		return repo.Agent{}, repo.ErrNotFound
	}
	if _, ok := f.agents[agent.ID]; !ok {
		return repo.Agent{}, repo.ErrNotFound
	}
	f.agents[agent.ID] = agent
	return agent, nil
}

type fakeAgentService struct {
	repo       *fakeAgentRepo
	created    []agentsvc.CreateAgentRequest
	createErr  error
	unpauseErr error
}

func (f *fakeAgentService) Create(ctx context.Context, req agentsvc.CreateAgentRequest) (*agentsvc.Agent, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.repo == nil {
		f.repo = &fakeAgentRepo{}
	}
	created, err := f.repo.Create(ctx, repo.Agent{
		OrganizationID:       req.OrganizationID,
		DisplayName:          req.DisplayName,
		AgentClass:           "staff",
		LifecycleStatus:      "draft",
		SystemPrompt:         req.SystemPrompt,
		OperatorInstructions: req.OperatorInstructions,
		AgentType:            req.AgentType,
		PrivateMemory:        req.PrivateMemory,
		MemoryReadScopes:     req.MemoryReadScopes,
		ToolAllowList:        req.ToolAllowList,
		ToolDenyList:         req.ToolDenyList,
		CreatedByType:        req.CreatedByType,
		CreatedByID:          req.CreatedByID,
	})
	if err != nil {
		return nil, err
	}
	f.created = append(f.created, req)
	result := agentsvc.Agent(created)
	return &result, nil
}

func (f *fakeAgentService) Unpause(ctx context.Context, _ uuid.UUID, agentID uuid.UUID) error {
	if f.unpauseErr != nil {
		return f.unpauseErr
	}
	if f.repo == nil {
		return repo.ErrNotFound
	}
	current, err := f.repo.GetByID(ctx, agentID)
	if err != nil {
		return err
	}
	current.LifecycleStatus = "active"
	_, err = f.repo.Update(ctx, current)
	return err
}

type fakeChatSessionRepo struct {
	lastCreated repo.ChatSession
	sessions    []repo.ChatSession
}

func (f *fakeChatSessionRepo) Create(_ context.Context, s repo.ChatSession) (repo.ChatSession, error) {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	f.lastCreated = s
	f.sessions = append(f.sessions, s)
	return s, nil
}
func (f *fakeChatSessionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	for _, session := range f.sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return repo.ChatSession{}, repo.ErrNotFound
}
func (f *fakeChatSessionRepo) ListByOrg(_ context.Context, organizationID uuid.UUID) ([]repo.ChatSession, error) {
	items := make([]repo.ChatSession, 0, len(f.sessions))
	for _, session := range f.sessions {
		if session.OrganizationID == organizationID {
			items = append(items, session)
		}
	}
	return items, nil
}
func (f *fakeChatSessionRepo) Close(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	for i := range f.sessions {
		if f.sessions[i].ID != id {
			continue
		}
		f.sessions[i].Status = "closed"
		now := time.Now().UTC()
		f.sessions[i].ClosedAt = &now
		return f.sessions[i], nil
	}
	return repo.ChatSession{}, repo.ErrNotFound
}

type fakeParticipantRepo struct {
	participants []repo.ChatParticipant
}

func (f *fakeParticipantRepo) Create(_ context.Context, p repo.ChatParticipant) (repo.ChatParticipant, error) {
	for _, existing := range f.participants {
		if existing.RemovedAt != nil {
			continue
		}
		if existing.SessionID != p.SessionID {
			continue
		}
		if existing.ParticipantType != p.ParticipantType {
			continue
		}
		if existing.ParticipantID != p.ParticipantID {
			continue
		}
		return repo.ChatParticipant{}, repo.ErrConflict
	}
	p.ID = uuid.New()
	f.participants = append(f.participants, p)
	return p, nil
}
func (f *fakeParticipantRepo) ListBySession(_ context.Context, sessionID uuid.UUID) ([]repo.ChatParticipant, error) {
	items := make([]repo.ChatParticipant, 0, len(f.participants))
	for _, participant := range f.participants {
		if participant.SessionID == sessionID && participant.RemovedAt == nil {
			items = append(items, participant)
		}
	}
	return items, nil
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

func TestProjectCreatePersistsReviewPolicyInSettings(t *testing.T) {
	orgID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	agentID := uuid.MustParse("66666666-6666-6666-6666-666666666666")

	projects := &fakeProjectRepo{}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.projects = projects

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})

	out, err := executor.Execute(ctx, "project.create", map[string]any{
		"name": "Editorial Engine",
		"review_policy": map[string]any{
			"mode":            taskplan.PolicyDelegatedAuthority,
			"guardrails":      []string{"Use OtterCamp voice", "Avoid unsupported claims"},
			"summary_cadence": "weekly",
		},
	})
	if err != nil {
		t.Fatalf("project.create: %v", err)
	}

	policy, ok := taskplan.ParseReviewPolicy(projects.lastCreated.Settings)
	if !ok {
		t.Fatalf("ParseReviewPolicy(settings) = false, want true; settings=%s", string(projects.lastCreated.Settings))
	}
	if policy.Mode != taskplan.PolicyDelegatedAuthority {
		t.Fatalf("Mode = %q, want %s", policy.Mode, taskplan.PolicyDelegatedAuthority)
	}
	if policy.SummaryCadence != "weekly" {
		t.Fatalf("SummaryCadence = %q, want weekly", policy.SummaryCadence)
	}

	project, ok := out["project"].(map[string]any)
	if !ok {
		t.Fatalf("project output = %T, want map[string]any", out["project"])
	}
	responsePolicy, ok := project["review_policy"].(map[string]any)
	if !ok {
		t.Fatalf("project.review_policy = %T, want map[string]any", project["review_policy"])
	}
	if responsePolicy["mode"] != taskplan.PolicyDelegatedAuthority {
		t.Fatalf("project.review_policy.mode = %v, want %s", responsePolicy["mode"], taskplan.PolicyDelegatedAuthority)
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

func TestSessionCreateProjectScopeReusesCanonicalSession(t *testing.T) {
	frankID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	loriID := uuid.New()
	orgID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	projectID := uuid.New()
	sessionID := uuid.New()

	sessions := &fakeChatSessionRepo{
		sessions: []repo.ChatSession{
			{
				ID:             sessionID,
				OrganizationID: orgID,
				ScopeType:      "project",
				ScopeID:        projectID,
				Mode:           "async",
				Status:         "active",
			},
		},
	}
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

	first, err := executor.Execute(ctx, "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
		"title":      "Sam.blog recovery",
	})
	if err != nil {
		t.Fatalf("first session.create: %v", err)
	}
	second, err := executor.Execute(ctx, "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
		"title":      "Sam.blog recovery 2",
	})
	if err != nil {
		t.Fatalf("second session.create: %v", err)
	}

	firstSession := first["session"].(map[string]any)
	secondSession := second["session"].(map[string]any)
	if mustUUIDFromAny(t, firstSession["id"]) != sessionID {
		t.Fatalf("first session id = %v, want %s", firstSession["id"], sessionID)
	}
	if mustUUIDFromAny(t, secondSession["id"]) != sessionID {
		t.Fatalf("second session id = %v, want %s", secondSession["id"], sessionID)
	}
	if len(sessions.sessions) != 1 {
		t.Fatalf("session rows = %d, want 1", len(sessions.sessions))
	}
	if len(participants.participants) != 2 {
		t.Fatalf("participant rows = %d, want 2", len(participants.participants))
	}
	if autoParticipants, ok := second["auto_participants"]; ok && autoParticipants != nil {
		t.Fatalf("second auto_participants = %v, want nil/absent when reusing participants", autoParticipants)
	}
}

func TestSessionInviteAgentIsIdempotent(t *testing.T) {
	agentID := uuid.New()
	orgID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	sessionID := uuid.New()

	participants := &fakeParticipantRepo{
		participants: []repo.ChatParticipant{{
			ID:                     uuid.New(),
			SessionID:              sessionID,
			ParticipantType:        "agent",
			ParticipantID:          agentID,
			Role:                   "member",
			NotificationPreference: "all",
		}},
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.participants = participants

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})

	out, err := executor.Execute(ctx, "session.invite_agent", map[string]any{
		"session_id": sessionID.String(),
		"agent_id":   agentID.String(),
	})
	if err != nil {
		t.Fatalf("session.invite_agent duplicate: %v", err)
	}
	if got := out["already_present"]; got != true {
		t.Fatalf("already_present = %v, want true", got)
	}
	if len(participants.participants) != 1 {
		t.Fatalf("participant rows = %d, want 1", len(participants.participants))
	}
}

func TestAgentAssignProjectCreatesAssignment(t *testing.T) {
	assigneeID := uuid.New()
	projectID := uuid.New()
	assignments := &fakeAssignmentRepo{}
	agents := &fakeAgentRepo{
		agents: map[uuid.UUID]repo.Agent{
			assigneeID: {ID: assigneeID, AgentClass: "staff", IsStarterTrio: false},
		},
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.assignments = assignments
	executor.agents = agents

	out, err := executor.Execute(testExecCtx(), "agent.assign_project", map[string]any{
		"agent_id":   assigneeID.String(),
		"project_id": projectID.String(),
		"role":       "pm",
	})
	if err != nil {
		t.Fatalf("agent.assign_project: %v", err)
	}

	assignment, ok := out["assignment"].(map[string]any)
	if !ok {
		t.Fatalf("assignment output = %T, want map[string]any", out["assignment"])
	}
	if mustUUIDFromAny(t, assignment["agent_id"]) != assigneeID {
		t.Fatalf("agent_id = %v, want %s", assignment["agent_id"], assigneeID)
	}
	if mustUUIDFromAny(t, assignment["project_id"]) != projectID {
		t.Fatalf("project_id = %v, want %s", assignment["project_id"], projectID)
	}
	if assignment["role"] != "project_manager" {
		t.Fatalf("role = %v, want project_manager", assignment["role"])
	}
	if len(assignments.assignments) != 1 {
		t.Fatalf("assignment repo entries = %d, want 1", len(assignments.assignments))
	}
	if assignments.assignments[0].AssignedByType != "agent" {
		t.Fatalf("assigned_by_type = %q, want agent", assignments.assignments[0].AssignedByType)
	}
	if assignments.assignments[0].AssignedByID == nil {
		t.Fatal("assigned_by_id = nil, want execution agent id")
	}
	execAgentID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	if *assignments.assignments[0].AssignedByID != execAgentID {
		t.Fatalf("assigned_by_id = %s, want %s", *assignments.assignments[0].AssignedByID, execAgentID)
	}
}

func TestAgentCreateStaffCreatesDraftCandidate(t *testing.T) {
	agents := &fakeAgentRepo{}
	service := &fakeAgentService{repo: agents}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		AgentService:  service,
	})
	executor.agents = agents

	out, err := executor.Execute(testExecCtx(), "agent.create_staff", map[string]any{
		"name":          "Emiliano",
		"agent_type":    "pm",
		"system_prompt": "You are a project manager.",
	})
	if err != nil {
		t.Fatalf("agent.create_staff: %v", err)
	}

	agentOut, ok := out["agent"].(map[string]any)
	if !ok {
		t.Fatalf("agent output = %T, want map[string]any", out["agent"])
	}
	if agentOut["agent_class"] != "staff" {
		t.Fatalf("agent_class = %v, want staff", agentOut["agent_class"])
	}
	if agentOut["agent_type"] != "pm" {
		t.Fatalf("agent_type = %v, want pm", agentOut["agent_type"])
	}
	if agentOut["lifecycle_status"] != "draft" {
		t.Fatalf("lifecycle_status = %v, want draft", agentOut["lifecycle_status"])
	}
	if len(service.created) != 1 {
		t.Fatalf("created requests = %d, want 1", len(service.created))
	}
	if got := service.created[0].MemoryReadScopes; !reflect.DeepEqual(got, []string{"org", "assigned_projects", "current_task"}) {
		t.Fatalf("memory_read_scopes = %#v, want default staff scopes", got)
	}
}

func TestAgentAssignProjectRejectsStarterTrioProjectRoles(t *testing.T) {
	for _, role := range []string{"pm", "worker", "reviewer", "observer"} {
		role := role
		t.Run(role, func(t *testing.T) {
			starterID := uuid.New()
			assignments := &fakeAssignmentRepo{}
			agents := &fakeAgentRepo{
				agents: map[uuid.UUID]repo.Agent{
					starterID: {ID: starterID, IsStarterTrio: true},
				},
			}

			executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
			executor.assignments = assignments
			executor.agents = agents

			out, err := executor.Execute(testExecCtx(), "agent.assign_project", map[string]any{
				"agent_id":   starterID.String(),
				"project_id": uuid.NewString(),
				"role":       role,
			})
			if err != nil {
				t.Fatalf("agent.assign_project: %v", err)
			}
			if out["error"] != agentsvc.StarterTrioProjectRoleErrorCode {
				t.Fatalf("error = %v, want %s", out["error"], agentsvc.StarterTrioProjectRoleErrorCode)
			}
			if out["message"] != agentsvc.ErrAssignmentStarterTrioRole.Error() {
				t.Fatalf("message = %v, want %q", out["message"], agentsvc.ErrAssignmentStarterTrioRole.Error())
			}
			if len(assignments.assignments) != 0 {
				t.Fatalf("assignment repo entries = %d, want 0", len(assignments.assignments))
			}
		})
	}
}

func TestAgentAssignProjectActivatesDraftStaffProjectManagerCandidate(t *testing.T) {
	assigneeID := uuid.New()
	projectID := uuid.New()
	assignments := &fakeAssignmentRepo{}
	agents := &fakeAgentRepo{
		agents: map[uuid.UUID]repo.Agent{
			assigneeID: {ID: assigneeID, AgentClass: "staff", LifecycleStatus: "draft", IsStarterTrio: false},
		},
	}
	service := &fakeAgentService{repo: agents}

	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		AgentService:  service,
	})
	executor.assignments = assignments
	executor.agents = agents

	out, err := executor.Execute(testExecCtx(), "agent.assign_project", map[string]any{
		"agent_id":   assigneeID.String(),
		"project_id": projectID.String(),
		"role":       "project_manager",
	})
	if err != nil {
		t.Fatalf("agent.assign_project: %v", err)
	}
	if _, ok := out["assignment"].(map[string]any); !ok {
		t.Fatalf("assignment output = %T, want map[string]any", out["assignment"])
	}
	updated, err := agents.GetByID(context.Background(), assigneeID)
	if err != nil {
		t.Fatalf("GetByID activated candidate: %v", err)
	}
	if updated.LifecycleStatus != "active" {
		t.Fatalf("lifecycle_status after assignment = %q, want active", updated.LifecycleStatus)
	}
}

func TestAgentAssignProjectRejectsProjectManagerRoleForTempAgent(t *testing.T) {
	assigneeID := uuid.New()
	assignments := &fakeAssignmentRepo{}
	agents := &fakeAgentRepo{
		agents: map[uuid.UUID]repo.Agent{
			assigneeID: {ID: assigneeID, AgentClass: "temp", IsStarterTrio: false},
		},
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.assignments = assignments
	executor.agents = agents

	out, err := executor.Execute(testExecCtx(), "agent.assign_project", map[string]any{
		"agent_id":   assigneeID.String(),
		"project_id": uuid.NewString(),
		"role":       "project_manager",
	})
	if err != nil {
		t.Fatalf("agent.assign_project: %v", err)
	}
	if out["error"] != "project_manager_requires_staff_agent" {
		t.Fatalf("error = %v, want project_manager_requires_staff_agent", out["error"])
	}
	if out["message"] != staffPMCreationMessage {
		t.Fatalf("message = %v, want %q", out["message"], staffPMCreationMessage)
	}
	if len(assignments.assignments) != 0 {
		t.Fatalf("assignment repo entries = %d, want 0", len(assignments.assignments))
	}
}

func TestAgentAssignProjectMapsPMConflict(t *testing.T) {
	assigneeID := uuid.New()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.assignments = &fakeAssignmentRepo{assignErr: repo.ErrPMConflict}
	executor.agents = &fakeAgentRepo{
		agents: map[uuid.UUID]repo.Agent{
			assigneeID: {ID: assigneeID, AgentClass: "staff", IsStarterTrio: false},
		},
	}

	out, err := executor.Execute(testExecCtx(), "agent.assign_project", map[string]any{
		"agent_id":   assigneeID.String(),
		"project_id": uuid.NewString(),
		"role":       "pm",
	})
	if err != nil {
		t.Fatalf("agent.assign_project: %v", err)
	}
	if out["error"] != "pm_conflict" {
		t.Fatalf("error = %v, want pm_conflict", out["error"])
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
