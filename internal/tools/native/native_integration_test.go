//go:build integration

package native

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

func TestIntegrationFileReadRoundTripAndTraversal(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	out, err := executor.Execute(integrationExecCtx(), "file.read", map[string]any{"path": "hello.txt"})
	if err != nil {
		t.Fatalf("file.read: %v", err)
	}
	if out["content"] != "hello world" {
		t.Fatalf("content = %v, want hello world", out["content"])
	}

	escaped, err := executor.Execute(integrationExecCtx(), "file.read", map[string]any{"path": "../../../etc/passwd"})
	if err != nil {
		t.Fatalf("file.read escaped: %v", err)
	}
	if escaped["error"] != "path_traversal" {
		t.Fatalf("escaped error = %v, want path_traversal", escaped["error"])
	}
}

func TestIntegrationFileSearchDirectoryTree(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.txt"), []byte("alpha\nneedle\nomega\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "b.txt"), []byte("beta\nneedle\ngamma\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "c.txt"), []byte("no match\n"), 0o644); err != nil {
		t.Fatalf("write c.txt: %v", err)
	}

	out, err := executor.Execute(integrationExecCtx(), "file.search", map[string]any{
		"path":         "docs",
		"pattern":      "needle",
		"file_pattern": "*.txt",
	})
	if err != nil {
		t.Fatalf("file.search: %v", err)
	}
	matches, _ := out["matches"].([]map[string]any)
	if len(matches) != 2 {
		t.Fatalf("matches length = %d, want 2", len(matches))
	}
}

func TestIntegrationGitStatusRealRepo(t *testing.T) {
	repoDir := t.TempDir()
	mustRunGit(t, repoDir, "init")

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: repoDir})
	clean, err := executor.Execute(integrationExecCtx(), "git.status", map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("clean git.status: %v", err)
	}
	branch, _ := clean["branch"].(string)
	if branch == "" {
		t.Fatalf("branch should not be empty")
	}
	if clean["is_dirty"] != false {
		t.Fatalf("is_dirty = %v, want false", clean["is_dirty"])
	}

	if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	dirty, err := executor.Execute(integrationExecCtx(), "git.status", map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("dirty git.status: %v", err)
	}
	if dirty["is_dirty"] != true {
		t.Fatalf("is_dirty = %v, want true", dirty["is_dirty"])
	}
}

func TestIntegrationFileWriteReadDeleteRoundTrip(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	written, err := executor.Execute(integrationExecCtx(), "file.write", map[string]any{
		"path":        "notes/out.txt",
		"content":     "hello tier2",
		"create_dirs": true,
	})
	if err != nil {
		t.Fatalf("file.write: %v", err)
	}
	if written["byte_size"] != 11 {
		t.Fatalf("byte_size = %v, want 11", written["byte_size"])
	}

	read, err := executor.Execute(integrationExecCtx(), "file.read", map[string]any{"path": "notes/out.txt"})
	if err != nil {
		t.Fatalf("file.read: %v", err)
	}
	if read["content"] != "hello tier2" {
		t.Fatalf("content = %v, want hello tier2", read["content"])
	}

	if _, err := executor.Execute(integrationExecCtx(), "file.delete", map[string]any{"path": "notes/out.txt"}); err != nil {
		t.Fatalf("file.delete: %v", err)
	}
	afterDelete, err := executor.Execute(integrationExecCtx(), "file.read", map[string]any{"path": "notes/out.txt"})
	if err != nil {
		t.Fatalf("file.read after delete: %v", err)
	}
	if afterDelete["error"] != "not_found" {
		t.Fatalf("read after delete error = %v, want not_found", afterDelete["error"])
	}
}

func TestIntegrationTaskDependencyCreateRemoveAndCycleDetection(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	ctx := integrationExecCtxWith(orgID, agent.ID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	taskAResp, err := executor.Execute(ctx, "task.create", map[string]any{
		"project_id": project.ID.String(),
		"title":      "Task A",
	})
	if err != nil {
		t.Fatalf("task.create A: %v", err)
	}
	taskBResp, err := executor.Execute(ctx, "task.create", map[string]any{
		"project_id": project.ID.String(),
		"title":      "Task B",
	})
	if err != nil {
		t.Fatalf("task.create B: %v", err)
	}
	taskAID := nestedUUID(t, taskAResp, "task", "id")
	taskBID := nestedUUID(t, taskBResp, "task", "id")

	addResp, err := executor.Execute(ctx, "task.add_dependency", map[string]any{
		"source_type":     "project_task",
		"source_id":       taskAID.String(),
		"depends_on_type": "project_task",
		"depends_on_id":   taskBID.String(),
	})
	if err != nil {
		t.Fatalf("task.add_dependency: %v", err)
	}
	dependencyID := mustUUIDValue(t, addResp["dependency_id"])

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM project_task_dependency WHERE id = $1`, dependencyID).Scan(&count); err != nil {
		t.Fatalf("count dependency row: %v", err)
	}
	if count != 1 {
		t.Fatalf("dependency count = %d, want 1", count)
	}

	if _, err := executor.Execute(ctx, "task.remove_dependency", map[string]any{
		"dependency_id": dependencyID.String(),
	}); err != nil {
		t.Fatalf("task.remove_dependency: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM project_task_dependency WHERE id = $1`, dependencyID).Scan(&count); err != nil {
		t.Fatalf("count removed dependency row: %v", err)
	}
	if count != 0 {
		t.Fatalf("dependency count after remove = %d, want 0", count)
	}

	if _, err := executor.Execute(ctx, "task.add_dependency", map[string]any{
		"source_type":     "project_task",
		"source_id":       taskAID.String(),
		"depends_on_type": "project_task",
		"depends_on_id":   taskBID.String(),
	}); err != nil {
		t.Fatalf("task.add_dependency A->B again: %v", err)
	}
	cycleResp, err := executor.Execute(ctx, "task.add_dependency", map[string]any{
		"source_type":     "project_task",
		"source_id":       taskBID.String(),
		"depends_on_type": "project_task",
		"depends_on_id":   taskAID.String(),
	})
	if err != nil {
		t.Fatalf("task.add_dependency B->A: %v", err)
	}
	if cycleResp["error"] != "cycle_detected" {
		t.Fatalf("cycle error = %v, want cycle_detected", cycleResp["error"])
	}
}

func TestIntegrationFlowAdvanceMovesToNextNode(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	template := testutil.MakeFlowTemplate(t, pool, project.ID, 2)
	agent := testutil.MakeAgent(t, pool, orgID)

	nodeRepo := repo.NewFlowNodeRepo(pool)
	nodes, err := nodeRepo.GetByTemplateOrdered(context.Background(), template.ID)
	if err != nil {
		t.Fatalf("list flow nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes count = %d, want 2", len(nodes))
	}

	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID: func() *uuid.UUID {
			value := uuid.Nil
			return &value
		}(),
	})
	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.SetFlowNode(context.Background(), task.ID, &nodes[0].ID); err != nil {
		t.Fatalf("set task current flow node: %v", err)
	}

	executionRepo := repo.NewFlowNodeExecutionRepo(pool)
	execution, err := executionRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, agent.ID)
	out, err := executor.Execute(ctx, "flow.advance", map[string]any{
		"flow_node_execution_id": execution.ID.String(),
		"commit_sha":             "abc123",
	})
	if err != nil {
		t.Fatalf("flow.advance: %v", err)
	}
	if out["flow_completed"] != false {
		t.Fatalf("flow_completed = %v, want false", out["flow_completed"])
	}
	if mustUUIDValue(t, out["advanced_to_node_id"]) != nodes[1].ID {
		t.Fatalf("advanced_to_node_id = %v, want %s", out["advanced_to_node_id"], nodes[1].ID)
	}

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("load updated task: %v", err)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("task current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, nodes[1].ID)
	}
}

func TestIntegrationFlowAdvanceTerminalMarksTaskDone(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	template := testutil.MakeFlowTemplate(t, pool, project.ID, 1)
	agent := testutil.MakeAgent(t, pool, orgID)

	nodeRepo := repo.NewFlowNodeRepo(pool)
	nodes, err := nodeRepo.GetByTemplateOrdered(context.Background(), template.ID)
	if err != nil {
		t.Fatalf("list flow nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes count = %d, want 1", len(nodes))
	}

	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		FlowTemplateID: &template.ID,
		WorkStatus:     "in_progress",
		CreatedByType:  "system",
		CreatedByID: func() *uuid.UUID {
			value := uuid.Nil
			return &value
		}(),
	})
	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.SetFlowNode(context.Background(), task.ID, &nodes[0].ID); err != nil {
		t.Fatalf("set task current flow node: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, agent.ID)
	out, err := executor.Execute(ctx, "flow.advance", map[string]any{
		"flow_node_execution_id": execution.ID.String(),
	})
	if err != nil {
		t.Fatalf("flow.advance: %v", err)
	}
	if out["flow_completed"] != true {
		t.Fatalf("flow_completed = %v, want true", out["flow_completed"])
	}
	if out["advanced_to_node_id"] != nil {
		t.Fatalf("advanced_to_node_id = %v, want nil", out["advanced_to_node_id"])
	}

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("load updated task: %v", err)
	}
	if updatedTask.WorkStatus != "done" {
		t.Fatalf("task work_status = %q, want done", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID != nil {
		t.Fatalf("task current_flow_node_id = %v, want nil", updatedTask.CurrentFlowNodeID)
	}
	if updatedTask.CompletedAt == nil {
		t.Fatal("task completed_at is nil, want populated")
	}
}

func TestIntegrationFlowAdvanceTerminalRequiresReviewStaysReview(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	template := testutil.MakeFlowTemplate(t, pool, project.ID, 1)
	agent := testutil.MakeAgent(t, pool, orgID)

	nodeRepo := repo.NewFlowNodeRepo(pool)
	nodes, err := nodeRepo.GetByTemplateOrdered(context.Background(), template.ID)
	if err != nil {
		t.Fatalf("list flow nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes count = %d, want 1", len(nodes))
	}

	requiresReview := true
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		FlowTemplateID:      &template.ID,
		WorkStatus:          "in_progress",
		RequiresHumanReview: &requiresReview,
		CreatedByType:       "system",
		CreatedByID: func() *uuid.UUID {
			value := uuid.Nil
			return &value
		}(),
	})
	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.SetFlowNode(context.Background(), task.ID, &nodes[0].ID); err != nil {
		t.Fatalf("set task current flow node: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, agent.ID)
	out, err := executor.Execute(ctx, "flow.advance", map[string]any{
		"flow_node_execution_id": execution.ID.String(),
	})
	if err != nil {
		t.Fatalf("flow.advance: %v", err)
	}
	if out["flow_completed"] != true {
		t.Fatalf("flow_completed = %v, want true", out["flow_completed"])
	}

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("load updated task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID != nil {
		t.Fatalf("task current_flow_node_id = %v, want nil", updatedTask.CurrentFlowNodeID)
	}
	if updatedTask.CompletedAt != nil {
		t.Fatalf("task completed_at = %v, want nil", updatedTask.CompletedAt)
	}
}

func integrationExecCtxWith(orgID, agentID uuid.UUID) context.Context {
	return mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})
}

func nestedUUID(t *testing.T, payload map[string]any, key, sub string) uuid.UUID {
	t.Helper()
	node, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("payload[%s] missing map: %#v", key, payload[key])
	}
	return mustUUIDValue(t, node[sub])
}

func mustUUIDValue(t *testing.T, raw any) uuid.UUID {
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
		encoded, _ := json.Marshal(raw)
		parsed, err := uuid.Parse(strings.Trim(string(encoded), `"`))
		if err != nil {
			t.Fatalf("parse uuid from %T (%v): %v", raw, raw, err)
		}
		return parsed
	}
}

func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(output))
	}
}

func integrationExecCtx() context.Context {
	orgID := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	agentID := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	return mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})
}
