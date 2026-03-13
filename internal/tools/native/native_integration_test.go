//go:build integration

package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
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

func TestIntegrationExecutorAutoBuildsTaskServiceWhenPoolAndEventsProvided(t *testing.T) {
	pool := testdb.New(t)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	executor := NewExecutor(ExecutorOptions{
		Pool:          pool,
		Events:        bus,
		WorkspaceRoot: t.TempDir(),
	})
	if executor.taskService == nil {
		t.Fatal("taskService = nil, want auto-built canonical task service")
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

func TestIntegrationFileWriteUsesSlugWorkspacePath(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	session := testutil.MakeSession(t, pool, orgID, "project", project.ID)
	dataDir := t.TempDir()

	executor := NewExecutor(ExecutorOptions{
		Pool:    pool,
		DataDir: dataDir,
	})

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agent.ID,
		SessionID:      &session.ID,
	})

	if _, err := executor.Execute(ctx, "file.write", map[string]any{
		"path":        "notes/plan.md",
		"content":     "ship it",
		"create_dirs": true,
	}); err != nil {
		t.Fatalf("file.write: %v", err)
	}

	projectRecord, err := repo.NewProjectRepo(pool).GetByID(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	expectedPath := filepath.Join(dataDir, "workspaces", projectRecord.Slug, "notes", "plan.md")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected file at slug workspace path %q: %v", expectedPath, err)
	}
	if strings.Contains(expectedPath, orgID.String()) || strings.Contains(expectedPath, project.ID.String()) {
		t.Fatalf("workspace path should not include UUIDs: %q", expectedPath)
	}
}

func TestIntegrationFileWriteRepeatedValidWritesStayDeterministicInTaskWorkspace(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{})
	agent := testutil.MakeAgent(t, pool, orgID)
	session := testutil.MakeSession(t, pool, orgID, "project_task", task.ID)
	dataDir := t.TempDir()

	executor := NewExecutor(ExecutorOptions{
		Pool:    pool,
		DataDir: dataDir,
	})
	ctx := integrationExecCtxWithSession(orgID, agent.ID, session.ID)

	writes := []struct {
		path    string
		content string
	}{
		{path: "templates/minimal/README.md", content: "# Minimal template"},
		{path: "templates/minimal/styles.css", content: "body { color: #204a3a; }"},
		{path: "templates/minimal/index.html", content: "<main>Otter Camp</main>"},
	}

	for i, write := range writes {
		out, err := executor.Execute(ctx, "file.write", map[string]any{
			"path":        write.path,
			"content":     write.content,
			"create_dirs": true,
		})
		if err != nil {
			t.Fatalf("file.write %d: %v", i, err)
		}
		if got := out["path"]; got != write.path {
			t.Fatalf("write %d path = %v, want %s", i, got, write.path)
		}
		if got := out["byte_size"]; got != len(write.content) {
			t.Fatalf("write %d byte_size = %v, want %d", i, got, len(write.content))
		}
		if got := out["created"]; got != true {
			t.Fatalf("write %d created = %v, want true", i, got)
		}
	}

	projectRecord, err := repo.NewProjectRepo(pool).GetByID(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	for _, write := range writes {
		diskPath := filepath.Join(dataDir, "workspaces", projectRecord.Slug, filepath.FromSlash(write.path))
		body, err := os.ReadFile(diskPath)
		if err != nil {
			t.Fatalf("read %s: %v", write.path, err)
		}
		if string(body) != write.content {
			t.Fatalf("%s content = %q, want %q", write.path, string(body), write.content)
		}
	}
}

func TestIntegrationFileWriteSamBlogAlternatingRawPayloadsReturnContentRequired(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{})
	agent := testutil.MakeAgent(t, pool, orgID)
	session := testutil.MakeSession(t, pool, orgID, "project_task", task.ID)
	dataDir := t.TempDir()

	executor := NewExecutor(ExecutorOptions{
		Pool:    pool,
		DataDir: dataDir,
	})
	ctx := integrationExecCtxWithSession(orgID, agent.ID, session.ID)

	attempts := []struct {
		name        string
		input       map[string]any
		wantError   string
		wantPath    string
		wantContent string
	}{
		{
			name: "success-1",
			input: map[string]any{
				"_raw": `{"path":"scripts/migrate.py","content":"print('migrate')","create_dirs":true`,
			},
			wantPath:    "scripts/migrate.py",
			wantContent: "print('migrate')",
		},
		{
			name: "sam-blog-missing-content-1",
			input: map[string]any{
				"_raw": `{"path":"content/posts/stop-preparing-your-kids-for-jobs.md","create_dirs":true}`,
			},
			wantError: "content_required",
			wantPath:  "content/posts/stop-preparing-your-kids-for-jobs.md",
		},
		{
			name: "success-2",
			input: map[string]any{
				"_raw": `{"path":"convert_all.py","content":"print('convert all')","create_dirs":true`,
			},
			wantPath:    "convert_all.py",
			wantContent: "print('convert all')",
		},
		{
			name: "sam-blog-missing-content-2",
			input: map[string]any{
				"_raw": `{"path":"content/posts/stop-preparing-your-kids-for-jobs.md","create_dirs":true}`,
			},
			wantError: "content_required",
			wantPath:  "content/posts/stop-preparing-your-kids-for-jobs.md",
		},
	}

	projectRecord, err := repo.NewProjectRepo(pool).GetByID(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	for i, attempt := range attempts {
		out, err := executor.Execute(ctx, "file.write", attempt.input)
		if err != nil {
			t.Fatalf("%s file.write: %v", attempt.name, err)
		}
		if attempt.wantError != "" {
			if got := out["error"]; got != attempt.wantError {
				t.Fatalf("%s error = %v, want %s", attempt.name, got, attempt.wantError)
			}
			if got := out["error"]; got == "path_required" {
				t.Fatalf("%s error = %v, want content_required", attempt.name, got)
			}
			message, _ := out["message"].(string)
			if !strings.Contains(message, "requires content") {
				t.Fatalf("%s message = %q, want actionable content guidance", attempt.name, message)
			}
			diskPath := filepath.Join(dataDir, "workspaces", projectRecord.Slug, filepath.FromSlash(attempt.wantPath))
			if _, err := os.Stat(diskPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s should not create %s, stat err = %v", attempt.name, diskPath, err)
			}
			continue
		}
		if got := out["path"]; got != attempt.wantPath {
			t.Fatalf("attempt %d path = %v, want %s", i, got, attempt.wantPath)
		}
		diskPath := filepath.Join(dataDir, "workspaces", projectRecord.Slug, filepath.FromSlash(attempt.wantPath))
		body, err := os.ReadFile(diskPath)
		if err != nil {
			t.Fatalf("read %s: %v", attempt.wantPath, err)
		}
		if string(body) != attempt.wantContent {
			t.Fatalf("%s content = %q, want %q", attempt.wantPath, string(body), attempt.wantContent)
		}
	}
}

func TestIntegrationFileReadUsesSlugWorkspacePath(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	session := testutil.MakeSession(t, pool, orgID, "project", project.ID)
	dataDir := t.TempDir()

	projectRecord, err := repo.NewProjectRepo(pool).GetByID(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	workspacePath := filepath.Join(dataDir, "workspaces", projectRecord.Slug)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("mkdir workspace path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, "existing.md"), []byte("hello from slug workspace"), 0o644); err != nil {
		t.Fatalf("write existing workspace file: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{
		Pool:    pool,
		DataDir: dataDir,
	})

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agent.ID,
		SessionID:      &session.ID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{
		"path": "existing.md",
	})
	if err != nil {
		t.Fatalf("file.read: %v", err)
	}
	if got := out["content"]; got != "hello from slug workspace" {
		t.Fatalf("file.read content = %v, want hello from slug workspace", got)
	}
}

func TestIntegrationMemoryRecordPersistsAndQueryReturnsMemory(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{
		Pool:          pool,
		WorkspaceRoot: t.TempDir(),
	})
	ctx := integrationExecCtxWith(orgID, agent.ID)

	recorded, err := executor.Execute(ctx, "memory.record", map[string]any{
		"content": "deployment runbook includes rollback checklist",
		"scope":   "agent",
	})
	if err != nil {
		t.Fatalf("memory.record: %v", err)
	}
	if got := recorded["status"]; got != "stored" {
		t.Fatalf("record status = %v, want stored", got)
	}
	memoryID := mustUUIDValue(t, recorded["memory_id"])

	stored, err := repo.NewMemoryRepo(pool).GetByID(context.Background(), memoryID)
	if err != nil {
		t.Fatalf("load recorded memory: %v", err)
	}
	if stored.Status != "active" {
		t.Fatalf("stored status = %q, want active", stored.Status)
	}
	if len(stored.Embedding) != memoryRecordEmbeddingDims {
		t.Fatalf("stored embedding dims = %d, want %d", len(stored.Embedding), memoryRecordEmbeddingDims)
	}

	retriever, err := memory.NewRetriever(memory.RetrieverOptions{
		Pool:     pool,
		Embedder: deterministicMemoryQueryEmbedder{},
	})
	if err != nil {
		t.Fatalf("new retriever: %v", err)
	}
	result, err := retriever.Query(context.Background(), memory.RetrievalRequest{
		OrganizationID: orgID,
		AgentID:        &agent.ID,
		Query:          "rollback checklist",
		Mode:           memory.RetrievalModePassive,
		MaxResults:     5,
	})
	if err != nil {
		t.Fatalf("memory query: %v", err)
	}
	found := false
	for _, ranked := range result.Memories {
		if ranked.Memory.ID == memoryID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("retrieval did not include recorded memory %s", memoryID)
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

	duplicateResp, err := executor.Execute(ctx, "task.add_dependency", map[string]any{
		"source_type":     "project_task",
		"source_id":       taskAID.String(),
		"depends_on_type": "project_task",
		"depends_on_id":   taskBID.String(),
	})
	if err != nil {
		t.Fatalf("task.add_dependency duplicate A->B: %v", err)
	}
	if got := mustUUIDValue(t, duplicateResp["dependency_id"]); got != dependencyID {
		t.Fatalf("duplicate dependency_id = %s, want existing %s", got, dependencyID)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM project_task_dependency
		WHERE source_type = 'project_task'
		  AND source_id = $1
		  AND depends_on_type = 'project_task'
		  AND depends_on_id = $2
	`, taskAID, taskBID).Scan(&count); err != nil {
		t.Fatalf("count duplicate dependency edge: %v", err)
	}
	if count != 1 {
		t.Fatalf("duplicate dependency edge count = %d, want 1", count)
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

func TestIntegrationTaskAddDependencyAcceptsTaskAliasTypes(t *testing.T) {
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
		"source_type":     "task",
		"source_id":       taskAID.String(),
		"depends_on_type": "task",
		"depends_on_id":   taskBID.String(),
	})
	if err != nil {
		t.Fatalf("task.add_dependency alias types: %v", err)
	}
	if addResp["error"] != nil {
		t.Fatalf("task.add_dependency alias error = %v, want nil", addResp["error"])
	}
	if _, ok := addResp["dependency_id"]; !ok {
		t.Fatalf("dependency_id missing from response: %v", addResp)
	}
}

func TestIntegrationTaskAddDependencyRejectsUnknownTypesActionably(t *testing.T) {
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
		"source_type":     "wave_task",
		"source_id":       taskAID.String(),
		"depends_on_type": "wave_task",
		"depends_on_id":   taskBID.String(),
	})
	if err != nil {
		t.Fatalf("task.add_dependency invalid types: %v", err)
	}
	if addResp["error"] != "invalid_dependency_type" {
		t.Fatalf("error = %v, want invalid_dependency_type", addResp["error"])
	}
	if addResp["message"] != "Use source_type and depends_on_type of project_task or project_subtask." {
		t.Fatalf("message = %v, want actionable guidance", addResp["message"])
	}
}

func TestIntegrationTaskCreateSubjectiveMultiOptionUsesReviewRefinementPlanning(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	template := seedReviewRefinementSystemTemplate(t, ctx, pool)
	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":   project.ID.String(),
		"title":        "Homepage design options",
		"description":  "Generate 10 homepage design options, compare them, shortlist the strongest directions, and recommend one with tradeoffs.",
		"blocks_scope": "none",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
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
	if planning["default_template_slug"] != "default-review-refinement" {
		t.Fatalf("default_template_slug = %v, want default-review-refinement", planning["default_template_slug"])
	}

	taskID := nestedUUID(t, out, "task", "id")
	taskRecord, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if taskRecord.FlowTemplateID == nil {
		t.Fatal("flow_template_id is nil, want resolved review-refinement template")
	}
	if *taskRecord.FlowTemplateID != template.ID {
		t.Fatalf("flow_template_id = %s, want %s", *taskRecord.FlowTemplateID, template.ID)
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok {
		t.Fatal("taskplan.Parse(metadata) = false, want true")
	}
	if plan.Playbook != taskplan.PlaybookStrategy {
		t.Fatalf("persisted playbook = %q, want %s", plan.Playbook, taskplan.PlaybookStrategy)
	}

	nodes, err := repo.NewFlowNodeRepo(pool).GetByTemplateOrdered(ctx, template.ID)
	if err != nil {
		t.Fatalf("load template nodes: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("template node count = %d, want 4", len(nodes))
	}
	if nodes[0].DisplayName != "Generation" || nodes[1].DisplayName != "Internal Review" || nodes[2].DisplayName != "Human Review" || nodes[3].DisplayName != "Complete" {
		t.Fatalf("unexpected node sequence = [%s %s %s %s]", nodes[0].DisplayName, nodes[1].DisplayName, nodes[2].DisplayName, nodes[3].DisplayName)
	}
}

func TestIntegrationTaskCreateUsesCanonicalTaskServiceForHumanReviewOverride(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := seedReviewRefinementSystemTemplate(t, ctx, pool)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":            project.ID.String(),
		"title":                 "Needs explicit human review",
		"description":           "Build options, then require an explicit human checkpoint before merge.",
		"flow_template_id":      template.ID.String(),
		"requires_human_review": true,
		"blocks_scope":          "all",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	taskID := nestedUUID(t, out, "task", "id")
	taskRecord, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if !taskRecord.RequiresHumanReview {
		t.Fatal("requires_human_review = false, want true")
	}
	if taskRecord.WorkStatus != "draft" {
		t.Fatalf("work_status = %q, want draft", taskRecord.WorkStatus)
	}
	if taskRecord.BlocksScope != "all" {
		t.Fatalf("blocks_scope = %q, want all", taskRecord.BlocksScope)
	}
}

func TestIntegrationTaskCreateFailsClosedWithoutCanonicalTaskService(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	executor.taskService = nil
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id": project.ID.String(),
		"title":      "Should fail closed",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	if out["error"] != "canonical_task_service_unavailable" {
		t.Fatalf("error = %v, want canonical_task_service_unavailable", out["error"])
	}

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("task count = %d, want 0", len(tasks))
	}
}

func TestIntegrationTaskUpdateFailsClosedWithoutCanonicalTaskService(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := seedReviewRefinementSystemTemplate(t, ctx, pool)
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Native update fail-closed",
		WorkStatus:     "draft",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	executor.taskService = nil
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
		"task_id":     taskRecord.ID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != "canonical_task_service_unavailable" {
		t.Fatalf("error = %v, want canonical_task_service_unavailable", out["error"])
	}

	stored, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.WorkStatus != "draft" {
		t.Fatalf("work_status = %q, want draft", stored.WorkStatus)
	}
}

func TestIntegrationDelegatedCreativeWorkflowUsesInternalReviewWithoutHumanCheckpoint(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	worker := testutil.MakeAgent(t, pool, orgID)
	reviewer := testutil.MakeAgent(t, pool, orgID)

	template := seedInternalReviewSystemTemplate(t, ctx, pool)
	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	updated, err := executor.Execute(integrationExecCtxWith(orgID, worker.ID), "project.update", map[string]any{
		"project_id": project.ID.String(),
		"review_policy": map[string]any{
			"mode":       taskplan.PolicyDelegatedAuthority,
			"guardrails": []string{"Use OtterCamp voice", "Avoid unsupported claims"},
		},
	})
	if err != nil {
		t.Fatalf("project.update: %v", err)
	}
	projectPayload, ok := updated["project"].(map[string]any)
	if !ok {
		t.Fatalf("project output = %T, want map[string]any", updated["project"])
	}
	reviewPolicy, ok := projectPayload["review_policy"].(map[string]any)
	if !ok {
		t.Fatalf("project.review_policy = %T, want map[string]any", projectPayload["review_policy"])
	}
	if reviewPolicy["mode"] != taskplan.PolicyDelegatedAuthority {
		t.Fatalf("project.review_policy.mode = %v, want %s", reviewPolicy["mode"], taskplan.PolicyDelegatedAuthority)
	}

	createTask := func(title, description, wantPlaybook string) (uuid.UUID, map[string]any) {
		t.Helper()
		out, err := executor.Execute(integrationExecCtxWith(orgID, worker.ID), "task.create", map[string]any{
			"project_id":  project.ID.String(),
			"title":       title,
			"description": description,
		})
		if err != nil {
			t.Fatalf("task.create %q: %v", title, err)
		}
		planning, ok := out["planning"].(map[string]any)
		if !ok {
			t.Fatalf("planning output for %q = %T, want map[string]any", title, out["planning"])
		}
		if planning["mode"] != taskplan.ModeAutonomousInternal {
			t.Fatalf("planning.mode for %q = %v, want %s", title, planning["mode"], taskplan.ModeAutonomousInternal)
		}
		if planning["playbook"] != wantPlaybook {
			t.Fatalf("planning.playbook for %q = %v, want %s", title, planning["playbook"], wantPlaybook)
		}
		if planning["default_template_slug"] != taskplan.InternalReviewTemplate {
			t.Fatalf("default_template_slug for %q = %v, want %s", title, planning["default_template_slug"], taskplan.InternalReviewTemplate)
		}
		return nestedUUID(t, out, "task", "id"), planning
	}

	firstTaskID, _ := createTask(
		"Homepage design options",
		"Generate 10 homepage design options, compare them, and recommend the strongest one within the brand guardrails.",
		taskplan.PlaybookStrategy,
	)
	secondTaskID, _ := createTask(
		"Launch-week blog post ideas",
		"Brainstorm 8 launch-week blog post ideas, compare them, and recommend which ones to ship next.",
		taskplan.PlaybookGTMLaunch,
	)

	taskRepo := repo.NewProjectTaskRepo(pool)
	firstTask, err := taskRepo.GetByID(ctx, firstTaskID)
	if err != nil {
		t.Fatalf("load first task: %v", err)
	}
	secondTask, err := taskRepo.GetByID(ctx, secondTaskID)
	if err != nil {
		t.Fatalf("load second task: %v", err)
	}
	if firstTask.FlowTemplateID == nil || *firstTask.FlowTemplateID != template.ID {
		t.Fatalf("first flow_template_id = %v, want %s", firstTask.FlowTemplateID, template.ID)
	}
	if secondTask.FlowTemplateID == nil || *secondTask.FlowTemplateID != template.ID {
		t.Fatalf("second flow_template_id = %v, want %s", secondTask.FlowTemplateID, template.ID)
	}
	if plan, ok := taskplan.Parse(firstTask.Metadata); !ok || plan.Playbook != taskplan.PlaybookStrategy {
		t.Fatalf("first task playbook = %#v, want %s", plan, taskplan.PlaybookStrategy)
	}
	if plan, ok := taskplan.Parse(secondTask.Metadata); !ok || plan.Playbook != taskplan.PlaybookGTMLaunch {
		t.Fatalf("second task playbook = %#v, want %s", plan, taskplan.PlaybookGTMLaunch)
	}

	if _, err := taskRepo.UpdateStatus(ctx, firstTaskID, "in_progress"); err != nil {
		t.Fatalf("UpdateStatus in_progress: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService flow: %v", err)
	}

	if _, err := flowService.StartFlow(ctx, firstTaskID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := flowService.AdvanceFlow(ctx, firstTaskID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow work: %v", err)
	}
	override, err := executor.Execute(integrationExecCtxWith(orgID, worker.ID), "task.update", map[string]any{
		"task_id":                  firstTaskID.String(),
		"planning_override_reason": "Internal review is approving the creative direction before the strategy packet is fully documented.",
	})
	if err != nil {
		t.Fatalf("task.update planning override: %v", err)
	}
	if override["error"] != nil {
		t.Fatalf("task.update planning override error = %v, want nil", override["error"])
	}
	if _, err := flowService.AdvanceFlow(ctx, firstTaskID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("AdvanceFlow internal review: %v", err)
	}
	if _, err := flowService.AdvanceFlow(ctx, firstTaskID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("AdvanceFlow completion: %v", err)
	}

	completedTask, err := taskRepo.GetByID(ctx, firstTaskID)
	if err != nil {
		t.Fatalf("reload completed task: %v", err)
	}
	if completedTask.WorkStatus != "done" {
		t.Fatalf("work_status = %q, want done", completedTask.WorkStatus)
	}

	var inboxCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM inbox_item
		WHERE source_task_id = $1
		  AND item_type = 'task_review'
	`, firstTaskID).Scan(&inboxCount); err != nil {
		t.Fatalf("count task review inbox items: %v", err)
	}
	if inboxCount != 0 {
		t.Fatalf("task_review inbox count = %d, want 0", inboxCount)
	}
}

func TestIntegrationBlogIdeasCreateReviewPacketInboxHandoff(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	worker := testutil.MakeAgent(t, pool, orgID)
	reviewer := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()
	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	seedReviewRefinementSystemTemplate(t, ctx, pool)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	out, err := executor.Execute(integrationExecCtxWith(orgID, worker.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "Launch-week blog post ideas",
		"description": "Brainstorm 12 launch-week blog post ideas, compare them, shortlist the best options, and recommend a sequence with tradeoffs.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	createdArtifact := firstArtifactPayload(t, planning["artifacts"])
	artifactRepoPath := artifactStringValue(t, createdArtifact, "repo_path")
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(artifactRepoPath))); err != nil {
		t.Fatalf("expected planning artifact at %q: %v", artifactRepoPath, err)
	}

	taskID := nestedUUID(t, out, "task", "id")
	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.UpdateStatus(ctx, taskID, "in_progress"); err != nil {
		t.Fatalf("UpdateStatus in_progress: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService flow: %v", err)
	}

	if _, err := flowService.StartFlow(ctx, taskID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := flowService.AdvanceFlow(ctx, taskID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow generation: %v", err)
	}

	afterGeneration, err := taskRepo.GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetByID after generation: %v", err)
	}
	if afterGeneration.WorkStatus == "done" {
		t.Fatalf("task work_status = %q after generation, want non-terminal", afterGeneration.WorkStatus)
	}

	if _, err := flowService.AdvanceFlow(ctx, taskID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("AdvanceFlow internal review: %v", err)
	}

	taskRecord, err := taskRepo.GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetByID after human-review handoff: %v", err)
	}
	if taskRecord.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", taskRecord.WorkStatus)
	}

	var (
		title         string
		body          *string
		actionPayload json.RawMessage
	)
	if err := pool.QueryRow(ctx, `
		SELECT title, body, action_payload
		FROM inbox_item
		WHERE organization_id = $1
		  AND item_type = 'task_review'
		  AND source_task_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, orgID, taskID).Scan(&title, &body, &actionPayload); err != nil {
		t.Fatalf("load task_review inbox item: %v", err)
	}
	if title != "Review options and recommendation" {
		t.Fatalf("inbox title = %q, want review packet title", title)
	}
	if body == nil || !strings.Contains(*body, "comparison") || !strings.Contains(*body, "recommendation") {
		t.Fatalf("inbox body = %v, want review packet sections", body)
	}
	if body == nil || !strings.Contains(*body, "Playbook: gtm_launch") {
		t.Fatalf("inbox body = %v, want playbook details", body)
	}
	if body == nil || !strings.Contains(*body, "@ "+artifactRepoPath) {
		t.Fatalf("inbox body = %v, want artifact repo path %q", body, artifactRepoPath)
	}

	var payload map[string]any
	if err := json.Unmarshal(actionPayload, &payload); err != nil {
		t.Fatalf("unmarshal action_payload: %v", err)
	}
	if payload["playbook"] != taskplan.PlaybookGTMLaunch {
		t.Fatalf("payload playbook = %v, want %s", payload["playbook"], taskplan.PlaybookGTMLaunch)
	}
	if payload["process_status"] != taskplan.ProcessStatusPending {
		t.Fatalf("payload process_status = %v, want %s", payload["process_status"], taskplan.ProcessStatusPending)
	}
	rawPacket, ok := payload["review_packet"].(map[string]any)
	if !ok {
		t.Fatalf("review_packet payload = %T, want map[string]any", payload["review_packet"])
	}
	sections, ok := rawPacket["sections"].([]any)
	if !ok {
		t.Fatalf("review packet sections = %T, want []any", rawPacket["sections"])
	}
	joined := make([]string, 0, len(sections))
	for _, item := range sections {
		joined = append(joined, fmt.Sprintf("%v", item))
	}
	if !containsString(joined, "comparison") || !containsString(joined, "shortlist") || !containsString(joined, "recommendation") {
		t.Fatalf("review packet sections = %v, want comparison/shortlist/recommendation", joined)
	}
	checklist, ok := payload["review_checklist"].([]any)
	if !ok {
		t.Fatalf("review_checklist payload = %T, want []any", payload["review_checklist"])
	}
	checklistJoined := make([]string, 0, len(checklist))
	for _, item := range checklist {
		checklistJoined = append(checklistJoined, fmt.Sprintf("%v", item))
	}
	if !containsString(checklistJoined, "Verify the launch brief defines scope, beachhead segment, ICP, and success metrics for the rollout.") {
		t.Fatalf("review_checklist = %v, want GTM rubric", checklistJoined)
	}
	artifacts, ok := payload["artifacts"].([]any)
	if !ok || len(artifacts) == 0 {
		t.Fatalf("payload artifacts = %T, want non-empty []any", payload["artifacts"])
	}
	artifactPayload, ok := artifacts[0].(map[string]any)
	if !ok {
		t.Fatalf("payload first artifact = %T, want map[string]any", artifacts[0])
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", artifactPayload["repo_path"])); got != artifactRepoPath {
		t.Fatalf("payload artifact repo_path = %q, want %q", got, artifactRepoPath)
	}
	if got := int(artifactPayload["version"].(float64)); got < 1 {
		t.Fatalf("payload artifact version = %d, want >= 1", got)
	}
}

func TestIntegrationTaskCreatePersistsRepoBackedPlanningArtifactsAndVersions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "PRD for billing migration",
		"description": "Write the PRD, implementation plan, acceptance criteria, and dependency log for the billing migration.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	createdArtifact := firstArtifactPayload(t, planning["artifacts"])
	artifactID := mustUUIDValue(t, createdArtifact["artifact_id"])
	artifactRepoPath := artifactStringValue(t, createdArtifact, "repo_path")
	if kind := artifactStringValue(t, createdArtifact, "kind"); kind != taskplan.ArtifactKindPRDSpec {
		t.Fatalf("artifact kind = %q, want %q", kind, taskplan.ArtifactKindPRDSpec)
	}
	if version := artifactVersionValue(t, createdArtifact["version"]); version != 1 {
		t.Fatalf("artifact version = %d, want 1", version)
	}

	artifactPath := filepath.Join(repoRoot, filepath.FromSlash(artifactRepoPath))
	content, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read planning artifact: %v", err)
	}
	if !strings.Contains(string(content), "Playbook: execution_spec") {
		t.Fatalf("artifact content missing expected playbook marker:\n%s", string(content))
	}

	taskID := nestedUUID(t, out, "task", "id")
	taskView, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.get", map[string]any{
		"task_id": taskID.String(),
	})
	if err != nil {
		t.Fatalf("task.get: %v", err)
	}
	taskArtifact := firstArtifactPayload(t, taskView["planning"].(map[string]any)["artifacts"])
	if got := artifactStringValue(t, taskArtifact, "repo_path"); got != artifactRepoPath {
		t.Fatalf("task.get artifact repo_path = %q, want %q", got, artifactRepoPath)
	}
	if version := artifactVersionValue(t, taskArtifact["version"]); version != 1 {
		t.Fatalf("task.get artifact version = %d, want 1", version)
	}

	projectView, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "project.get", map[string]any{
		"project_id": project.ID.String(),
	})
	if err != nil {
		t.Fatalf("project.get: %v", err)
	}
	projectArtifacts := artifactPayloads(t, projectView["planning_artifacts"])
	foundProjectArtifact := false
	for _, artifact := range projectArtifacts {
		if artifactStringValue(t, artifact, "repo_path") == artifactRepoPath {
			foundProjectArtifact = true
			break
		}
	}
	if !foundProjectArtifact {
		t.Fatalf("project.get planning_artifacts missing repo_path %q: %#v", artifactRepoPath, projectArtifacts)
	}

	revised := string(content) + "\n## Revision\nUpdated implementation constraints.\n"
	if err := os.WriteFile(artifactPath, []byte(revised), 0o644); err != nil {
		t.Fatalf("write revised planning artifact: %v", err)
	}
	updated, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
		"task_id": taskID.String(),
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	updatedArtifact := firstArtifactPayload(t, updated["planning"].(map[string]any)["artifacts"])
	if version := artifactVersionValue(t, updatedArtifact["version"]); version != 2 {
		t.Fatalf("updated artifact version = %d, want 2", version)
	}

	versions, err := repo.NewPlanningArtifactRepo(pool).ListVersions(ctx, artifactID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("artifact version rows = %d, want 2", len(versions))
	}
	if versions[0].VersionNumber != 2 || versions[1].VersionNumber != 1 {
		t.Fatalf("artifact versions = [%d %d], want [2 1]", versions[0].VersionNumber, versions[1].VersionNumber)
	}
}

func TestIntegrationTaskCreateDiscoveryArtifactsVaryByDiscoveryMode(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	taskRepo := repo.NewProjectTaskRepo(pool)

	tests := []struct {
		name               string
		title              string
		description        string
		wantMode           string
		wantFollowOnPart   string
		wantContentPhrases []string
	}{
		{
			name:             "new product",
			title:            "Validate the new product onboarding concept",
			description:      "Run customer interviews, capture assumptions, and design low-cost validation for this new product idea before we commit scope.",
			wantMode:         taskplan.DiscoveryModeNewProduct,
			wantFollowOnPart: "low-cost desirability tests",
			wantContentPhrases: []string{
				"Discovery mode: new_product",
				"## Ideas Explored",
				"## Assumptions",
				"## Validation Experiments",
				"## Low-Cost Tests",
				"## Desirability Signals",
				"## Decision Framework",
			},
		},
		{
			name:             "existing product",
			title:            "Investigate checkout drop-off in the existing product",
			description:      "Review support tickets, current funnel instrumentation, and usage data for the existing product before defining experiments.",
			wantMode:         taskplan.DiscoveryModeExistingProduct,
			wantFollowOnPart: "instrumentation, usage data, and prior feedback",
			wantContentPhrases: []string{
				"Discovery mode: existing_product",
				"## Ideas Explored",
				"## Assumptions",
				"## Validation Experiments",
				"## Prior Feedback",
				"## Instrumentation Baseline",
				"## Decision Framework",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
				"project_id":  project.ID.String(),
				"title":       tc.title,
				"description": tc.description,
			})
			if err != nil {
				t.Fatalf("task.create: %v", err)
			}

			planning, ok := out["planning"].(map[string]any)
			if !ok {
				t.Fatalf("planning output = %T, want map[string]any", out["planning"])
			}
			if planning["playbook"] != taskplan.PlaybookDiscovery {
				t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookDiscovery)
			}
			contextPayload := planningContextPayload(t, planning)
			if got := strings.TrimSpace(fmt.Sprintf("%v", contextPayload["discovery_mode"])); got != tc.wantMode {
				t.Fatalf("planning.context.discovery_mode = %q, want %q", got, tc.wantMode)
			}

			followOns := planningStringSlice(t, planning["follow_on_suggestions"])
			if len(followOns) == 0 {
				t.Fatal("follow_on_suggestions = empty, want non-empty suggestions")
			}
			if !containsSubstring(followOns, tc.wantFollowOnPart) {
				t.Fatalf("follow_on_suggestions = %#v, want phrase %q", followOns, tc.wantFollowOnPart)
			}

			validationArtifact := artifactPayloadBySlug(t, planning["artifacts"], "validation-plan")
			artifactPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, validationArtifact, "repo_path")))
			content, err := os.ReadFile(artifactPath)
			if err != nil {
				t.Fatalf("read validation artifact: %v", err)
			}
			body := string(content)
			for _, phrase := range tc.wantContentPhrases {
				if !strings.Contains(body, phrase) {
					t.Fatalf("validation artifact missing %q:\n%s", phrase, body)
				}
			}

			taskID := nestedUUID(t, out, "task", "id")
			taskRecord, err := taskRepo.GetByID(ctx, taskID)
			if err != nil {
				t.Fatalf("load task: %v", err)
			}
			plan, ok := taskplan.Parse(taskRecord.Metadata)
			if !ok {
				t.Fatal("taskplan.Parse(metadata) = false, want true")
			}
			if plan.DiscoveryMode != tc.wantMode {
				t.Fatalf("persisted DiscoveryMode = %q, want %q", plan.DiscoveryMode, tc.wantMode)
			}
		})
	}
}

func TestIntegrationTaskCreateDiscoveryIncludesStructuredFollowOnCandidate(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "Validate the onboarding problem",
		"description": "Run customer interviews, document assumptions, and build a validation plan for this new product idea before we commit scope.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	followOn := planningFollowOnPayload(t, planning)
	if got := strings.TrimSpace(fmt.Sprintf("%v", followOn["status"])); got != taskplan.FollowOnStatusProposed {
		t.Fatalf("follow_on.status = %q, want %q", got, taskplan.FollowOnStatusProposed)
	}
	candidates := planningFollowOnCandidates(t, planning)
	first := candidates[0]
	if got := strings.TrimSpace(fmt.Sprintf("%v", first["action_type"])); got != taskplan.FollowOnActionDraftTask {
		t.Fatalf("follow_on.candidates[0].action_type = %q, want %q", got, taskplan.FollowOnActionDraftTask)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", first["work_status"])); got != "draft" {
		t.Fatalf("follow_on.candidates[0].work_status = %q, want draft", got)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", first["target_playbook"])); got != taskplan.PlaybookExecutionSpec {
		t.Fatalf("follow_on.candidates[0].target_playbook = %q, want %q", got, taskplan.PlaybookExecutionSpec)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", first["source_artifact_slug"])); got != "validation-plan" {
		t.Fatalf("follow_on.candidates[0].source_artifact_slug = %q, want validation-plan", got)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", first["title"])); !strings.Contains(got, "PRD/spec") {
		t.Fatalf("follow_on.candidates[0].title = %q, want PRD/spec guidance", got)
	}
}

func TestIntegrationTaskCreateRiskReadinessArtifactsAndFollowOnSuggestions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "Public launch readiness for billing migration",
		"description": "Build the pre-mortem, risk register, mitigation plan, and readiness checklist for the risky public launch and customer-facing billing migration before go live.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["playbook"] != taskplan.PlaybookRiskReadiness {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookRiskReadiness)
	}

	artifacts := artifactPayloads(t, planning["artifacts"])
	artifactBySlug := make(map[string]map[string]any, len(artifacts))
	for _, artifact := range artifacts {
		artifactBySlug[artifactStringValue(t, artifact, "slug")] = artifact
	}

	premortemPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, artifactBySlug["premortem"], "repo_path")))
	premortemContent, err := os.ReadFile(premortemPath)
	if err != nil {
		t.Fatalf("read premortem artifact: %v", err)
	}
	if !strings.Contains(string(premortemContent), "## Failure Modes") || !strings.Contains(string(premortemContent), "## Triggers") || !strings.Contains(string(premortemContent), "## Responses") {
		t.Fatalf("premortem artifact missing expected sections:\n%s", string(premortemContent))
	}

	riskRegisterPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, artifactBySlug["risk-register"], "repo_path")))
	riskRegisterContent, err := os.ReadFile(riskRegisterPath)
	if err != nil {
		t.Fatalf("read risk register artifact: %v", err)
	}
	if !strings.Contains(string(riskRegisterContent), "## Major Risks") || !strings.Contains(string(riskRegisterContent), "## Severity") || !strings.Contains(string(riskRegisterContent), "## Impact") {
		t.Fatalf("risk register artifact missing expected sections:\n%s", string(riskRegisterContent))
	}

	readinessPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, artifactBySlug["readiness-checklist"], "repo_path")))
	readinessContent, err := os.ReadFile(readinessPath)
	if err != nil {
		t.Fatalf("read readiness artifact: %v", err)
	}
	if !strings.Contains(string(readinessContent), "## Go / No-Go Checklist") {
		t.Fatalf("readiness artifact missing go/no-go checklist:\n%s", string(readinessContent))
	}

	var followOns []string
	switch typed := planning["follow_on_suggestions"].(type) {
	case []string:
		followOns = append([]string(nil), typed...)
	case []any:
		for _, item := range typed {
			followOns = append(followOns, fmt.Sprintf("%v", item))
		}
	default:
		t.Fatalf("follow_on_suggestions = %T, want slice", planning["follow_on_suggestions"])
	}
	foundTargetedTests := false
	for _, followOn := range followOns {
		if strings.Contains(followOn, "Create targeted test scenarios") {
			foundTargetedTests = true
			break
		}
	}
	if !foundTargetedTests {
		t.Fatalf("follow_on_suggestions = %#v, want targeted test scenario follow-on", followOns)
	}

	projectView, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "project.get", map[string]any{
		"project_id": project.ID.String(),
	})
	if err != nil {
		t.Fatalf("project.get: %v", err)
	}
	projectArtifacts := artifactPayloads(t, projectView["planning_artifacts"])
	foundPremortem := false
	for _, artifact := range projectArtifacts {
		if artifactStringValue(t, artifact, "slug") == "premortem" {
			foundPremortem = true
			break
		}
	}
	if !foundPremortem {
		t.Fatalf("project.get planning_artifacts missing premortem entry: %#v", projectArtifacts)
	}
}

func TestIntegrationTaskCreateMetricsPlanningProducesRepoBackedArtifact(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	taskRepo := repo.NewProjectTaskRepo(pool)

	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "Metric tree and instrumentation plan",
		"description": "Define the north-star metric, input metrics, health metrics, counter-metrics, dashboard requirements, and weekly review cadence for activation.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["playbook"] != taskplan.PlaybookMetrics {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookMetrics)
	}

	followOns := planningStringSlice(t, planning["follow_on_suggestions"])
	if !containsSubstring(followOns, "Create dashboard, scorecard, or success-tracking tasks") {
		t.Fatalf("follow_on_suggestions = %#v, want metrics follow-on task guidance", followOns)
	}

	metricArtifact := artifactPayloadBySlug(t, planning["artifacts"], "metric-tree")
	metricPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, metricArtifact, "repo_path")))
	metricBody, err := os.ReadFile(metricPath)
	if err != nil {
		t.Fatalf("read metric-tree artifact: %v", err)
	}
	for _, phrase := range []string{"## North Star", "## Input Metrics", "## Health Metrics", "## Counter Metrics"} {
		if !strings.Contains(string(metricBody), phrase) {
			t.Fatalf("metric-tree artifact missing %q:\n%s", phrase, string(metricBody))
		}
	}

	cadenceArtifact := artifactPayloadBySlug(t, planning["artifacts"], "review-cadence")
	cadencePath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, cadenceArtifact, "repo_path")))
	cadenceBody, err := os.ReadFile(cadencePath)
	if err != nil {
		t.Fatalf("read review-cadence artifact: %v", err)
	}
	for _, phrase := range []string{"## Schedule", "## Thresholds"} {
		if !strings.Contains(string(cadenceBody), phrase) {
			t.Fatalf("review-cadence artifact missing %q:\n%s", phrase, string(cadenceBody))
		}
	}

	taskID := nestedUUID(t, out, "task", "id")
	taskRecord, err := taskRepo.GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok {
		t.Fatal("taskplan.Parse(metadata) = false, want true")
	}
	foundMetricArtifact := false
	for _, artifact := range plan.Artifacts {
		if artifact.Slug != "metric-tree" {
			continue
		}
		foundMetricArtifact = true
		if strings.TrimSpace(artifact.RepoPath) == "" {
			t.Fatalf("metric-tree repo_path = %q, want repo-backed artifact path", artifact.RepoPath)
		}
	}
	if !foundMetricArtifact {
		t.Fatalf("persisted artifacts missing metric-tree: %#v", plan.Artifacts)
	}

	projectView, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "project.get", map[string]any{
		"project_id": project.ID.String(),
	})
	if err != nil {
		t.Fatalf("project.get: %v", err)
	}
	projectArtifacts := artifactPayloads(t, projectView["planning_artifacts"])
	foundProjectMetricArtifact := false
	for _, artifact := range projectArtifacts {
		if artifactStringValue(t, artifact, "slug") == "metric-tree" {
			foundProjectMetricArtifact = true
			break
		}
	}
	if !foundProjectMetricArtifact {
		t.Fatalf("project.get planning_artifacts missing metric-tree entry: %#v", projectArtifacts)
	}
}

func TestIntegrationTaskCreateStrategyIncludesDownstreamFollowOnCandidates(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "Positioning and roadmap for analytics expansion",
		"description": "Define the product strategy, positioning tradeoffs, and the roadmap sequence for the analytics platform.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["playbook"] != taskplan.PlaybookStrategy {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookStrategy)
	}

	followOn := planningFollowOnPayload(t, planning)
	if got := strings.TrimSpace(fmt.Sprintf("%v", followOn["status"])); got != taskplan.FollowOnStatusProposed {
		t.Fatalf("follow_on.status = %q, want %q", got, taskplan.FollowOnStatusProposed)
	}
	candidates := planningFollowOnCandidates(t, planning)
	if got := strings.TrimSpace(fmt.Sprintf("%v", candidates[0]["action_type"])); got != taskplan.FollowOnActionReadyTask {
		t.Fatalf("follow_on.candidates[0].action_type = %q, want %q", got, taskplan.FollowOnActionReadyTask)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", candidates[0]["work_status"])); got != "queued" {
		t.Fatalf("follow_on.candidates[0].work_status = %q, want queued", got)
	}

	foundMetrics := false
	foundGTM := false
	for _, candidate := range candidates {
		switch strings.TrimSpace(fmt.Sprintf("%v", candidate["target_playbook"])) {
		case taskplan.PlaybookMetrics:
			foundMetrics = true
		case taskplan.PlaybookGTMLaunch:
			foundGTM = true
		}
	}
	if !foundMetrics || !foundGTM {
		t.Fatalf("follow_on candidates = %#v, want metrics and gtm follow-ons", candidates)
	}
}

func TestIntegrationTaskCreateLaunchPlanningProducesRepoBackedArtifactAndFollowOns(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	taskRepo := repo.NewProjectTaskRepo(pool)

	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "Go-to-market launch plan",
		"description": "Create the GTM launch plan with beachhead segment, ICP, positioning and messaging, channel strategy, launch timeline, success metrics, and expansion plan for the analytics release.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["playbook"] != taskplan.PlaybookGTMLaunch {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookGTMLaunch)
	}

	followOns := planningStringSlice(t, planning["follow_on_suggestions"])
	if !containsSubstring(followOns, "Create launch-checklist, channel-execution, or enablement tasks") {
		t.Fatalf("follow_on_suggestions = %#v, want launch follow-on task guidance", followOns)
	}

	launchBrief := artifactPayloadBySlug(t, planning["artifacts"], "launch-brief")
	launchBriefPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, launchBrief, "repo_path")))
	launchBriefBody, err := os.ReadFile(launchBriefPath)
	if err != nil {
		t.Fatalf("read launch-brief artifact: %v", err)
	}
	for _, phrase := range []string{"## Launch Scope", "## Beachhead Segment", "## ICP", "## Success Metrics"} {
		if !strings.Contains(string(launchBriefBody), phrase) {
			t.Fatalf("launch-brief artifact missing %q:\n%s", phrase, string(launchBriefBody))
		}
	}

	audienceMessaging := artifactPayloadBySlug(t, planning["artifacts"], "audience-messaging")
	audienceMessagingPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, audienceMessaging, "repo_path")))
	audienceMessagingBody, err := os.ReadFile(audienceMessagingPath)
	if err != nil {
		t.Fatalf("read audience-messaging artifact: %v", err)
	}
	for _, phrase := range []string{"## Positioning", "## Messaging", "## Proof"} {
		if !strings.Contains(string(audienceMessagingBody), phrase) {
			t.Fatalf("audience-messaging artifact missing %q:\n%s", phrase, string(audienceMessagingBody))
		}
	}

	channelPlan := artifactPayloadBySlug(t, planning["artifacts"], "channel-plan")
	channelPlanPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, channelPlan, "repo_path")))
	channelPlanBody, err := os.ReadFile(channelPlanPath)
	if err != nil {
		t.Fatalf("read channel-plan artifact: %v", err)
	}
	for _, phrase := range []string{"## Channel Strategy", "## Launch Timeline"} {
		if !strings.Contains(string(channelPlanBody), phrase) {
			t.Fatalf("channel-plan artifact missing %q:\n%s", phrase, string(channelPlanBody))
		}
	}

	launchChecklist := artifactPayloadBySlug(t, planning["artifacts"], "launch-checklist")
	launchChecklistPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, launchChecklist, "repo_path")))
	launchChecklistBody, err := os.ReadFile(launchChecklistPath)
	if err != nil {
		t.Fatalf("read launch-checklist artifact: %v", err)
	}
	if !strings.Contains(string(launchChecklistBody), "## Expansion Plan") {
		t.Fatalf("launch-checklist artifact missing expansion plan:\n%s", string(launchChecklistBody))
	}

	taskID := nestedUUID(t, out, "task", "id")
	taskRecord, err := taskRepo.GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok {
		t.Fatal("taskplan.Parse(metadata) = false, want true")
	}
	foundLaunchBrief := false
	for _, artifact := range plan.Artifacts {
		if artifact.Slug != "launch-brief" {
			continue
		}
		foundLaunchBrief = true
		if strings.TrimSpace(artifact.RepoPath) == "" {
			t.Fatalf("launch-brief repo_path = %q, want repo-backed artifact path", artifact.RepoPath)
		}
	}
	if !foundLaunchBrief {
		t.Fatalf("persisted artifacts missing launch-brief: %#v", plan.Artifacts)
	}

	projectView, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "project.get", map[string]any{
		"project_id": project.ID.String(),
	})
	if err != nil {
		t.Fatalf("project.get: %v", err)
	}
	projectArtifacts := artifactPayloads(t, projectView["planning_artifacts"])
	foundProjectLaunchArtifact := false
	for _, artifact := range projectArtifacts {
		if artifactStringValue(t, artifact, "slug") == "launch-brief" {
			foundProjectLaunchArtifact = true
			break
		}
	}
	if !foundProjectLaunchArtifact {
		t.Fatalf("project.get planning_artifacts missing launch-brief entry: %#v", projectArtifacts)
	}
}

func TestIntegrationTaskCompletionRequiresPlanningContractOrOverride(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	taskRepo := repo.NewProjectTaskRepo(pool)
	execRepo := repo.NewFlowNodeExecutionRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template := testutil.MakeFlowTemplate(t, pool, project.ID, 2)
	nodes, err := nodeRepo.GetByTemplateOrdered(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("template nodes = %d, want 2", len(nodes))
	}
	workNode := nodes[0]
	reviewNode := nodes[1]
	reviewNode.NodeType = "review"
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("Update review node: %v", err)
	}

	tests := []struct {
		title          string
		description    string
		wantPlaybook   string
		artifactSlug   string
		overrideReason string
	}{
		{
			title:          "Validate the onboarding problem",
			description:    "Run customer interviews, document assumptions, and build a validation plan for this new product idea before we commit scope.",
			wantPlaybook:   taskplan.PlaybookDiscovery,
			artifactSlug:   "problem-brief",
			overrideReason: "Interview slots are booked next week, so the remaining sections will be completed after the research kickoff.",
		},
		{
			title:          "Positioning tradeoffs for analytics expansion",
			description:    "Define the product strategy, positioning tradeoffs, and the roadmap sequence for the analytics platform.",
			wantPlaybook:   taskplan.PlaybookStrategy,
			artifactSlug:   "strategy-brief",
			overrideReason: "Leadership still owes the final audience input; approval needs to unblock the downstream package today.",
		},
		{
			title:          "PRD for billing migration",
			description:    "Write the PRD, implementation plan, acceptance criteria, and dependency log for the billing migration.",
			wantPlaybook:   taskplan.PlaybookExecutionSpec,
			artifactSlug:   "prd",
			overrideReason: "Dependency owners are still confirming the rollout sequence.",
		},
		{
			title:          "Metric tree and instrumentation plan",
			description:    "Define north-star metrics, KPI instrumentation, the dashboard spec, and weekly metric review cadence.",
			wantPlaybook:   taskplan.PlaybookMetrics,
			artifactSlug:   "metric-tree",
			overrideReason: "Telemetry ownership is split across teams; the missing sections will land after the instrumentation handoff.",
		},
		{
			title:          "Go-to-market launch plan",
			description:    "Create the GTM launch plan, messaging brief, channel plan, and sales enablement checklist for release.",
			wantPlaybook:   taskplan.PlaybookGTMLaunch,
			artifactSlug:   "launch-brief",
			overrideReason: "Channel owners are still finalizing timing, but the launch review must proceed.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.wantPlaybook, func(t *testing.T) {
			out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
				"project_id":  project.ID.String(),
				"title":       tc.title,
				"description": tc.description,
			})
			if err != nil {
				t.Fatalf("task.create: %v", err)
			}
			planning, ok := out["planning"].(map[string]any)
			if !ok {
				t.Fatalf("planning output = %T, want map[string]any", out["planning"])
			}
			if planning["playbook"] != tc.wantPlaybook {
				t.Fatalf("playbook = %v, want %s", planning["playbook"], tc.wantPlaybook)
			}

			taskID := nestedUUID(t, out, "task", "id")
			taskRecord, err := taskRepo.GetByID(ctx, taskID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			taskRecord.FlowTemplateID = &template.ID
			taskRecord.CurrentFlowNodeID = &reviewNode.ID
			taskRecord.WorkStatus = "review"
			if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
				t.Fatalf("taskRepo.Update: %v", err)
			}
			if _, err := execRepo.Create(ctx, repo.FlowNodeExecution{
				TaskID:      taskID,
				FlowNodeID:  workNode.ID,
				VisitNumber: 1,
				Status:      "completed",
			}); err != nil {
				t.Fatalf("Create work execution: %v", err)
			}
			if _, err := execRepo.Create(ctx, repo.FlowNodeExecution{
				TaskID:      taskID,
				FlowNodeID:  reviewNode.ID,
				VisitNumber: 1,
				Status:      "completed",
			}); err != nil {
				t.Fatalf("Create review execution: %v", err)
			}

			out, err = executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
				"task_id":     taskID.String(),
				"work_status": "done",
			})
			if err != nil {
				t.Fatalf("task.update without contract: %v", err)
			}
			if got := fmt.Sprintf("%v", out["error"]); !strings.Contains(got, "planning artifact contract is incomplete") {
				t.Fatalf("error = %v, want planning artifact contract failure", out["error"])
			}

			out, err = executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
				"task_id":     taskID.String(),
				"work_status": "done",
				"planning_artifacts": []map[string]any{
					{
						"slug":     tc.artifactSlug,
						"summary":  "Draft artifact recorded for review.",
						"sections": []string{"objective"},
					},
				},
				"planning_override_reason": tc.overrideReason,
			})
			if err != nil {
				t.Fatalf("task.update with override: %v", err)
			}
			if out["error"] != nil {
				t.Fatalf("task.update override error = %v, want nil", out["error"])
			}

			taskRecord, err = taskRepo.GetByID(ctx, taskID)
			if err != nil {
				t.Fatalf("GetByID after override: %v", err)
			}
			if taskRecord.WorkStatus != "done" {
				t.Fatalf("work_status = %q, want done", taskRecord.WorkStatus)
			}
			plan, ok := taskplan.Parse(taskRecord.Metadata)
			if !ok {
				t.Fatal("taskplan.Parse(metadata) = false, want true")
			}
			report := taskplan.Evaluate(plan)
			if report.ProcessStatus != taskplan.ProcessStatusOverridden {
				t.Fatalf("process_status = %q, want %s", report.ProcessStatus, taskplan.ProcessStatusOverridden)
			}
			if plan.Override == nil || plan.Override.Reason != tc.overrideReason {
				t.Fatalf("override = %#v, want reason %q", plan.Override, tc.overrideReason)
			}
		})
	}
}

func TestIntegrationTaskCreateBacklogDecompositionSupportsMultipleStoryFormats(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})
	taskRepo := repo.NewProjectTaskRepo(pool)

	tests := []struct {
		name               string
		title              string
		description        string
		wantFormat         string
		wantStoryPhrases   []string
		wantFollowOnPhrase string
	}{
		{
			name:        "user stories",
			title:       "Break the approved checkout scope into user stories",
			description: "Decompose the approved checkout scope into user stories with acceptance criteria, dependency ordering, and owners for the delivery team.",
			wantFormat:  taskplan.BacklogFormatUserStories,
			wantStoryPhrases: []string{
				"- Backlog format: user_stories",
				"## User Stories",
				"## Acceptance Criteria",
				"## Technical Notes",
				"## Open Questions",
			},
			wantFollowOnPhrase: "Generate test scenarios directly from the user stories",
		},
		{
			name:        "job stories",
			title:       "Break the approved checkout scope into job stories",
			description: "Decompose the approved checkout scope into job stories with acceptance criteria, dependency ordering, and owners for the payments team.",
			wantFormat:  taskplan.BacklogFormatJobStories,
			wantStoryPhrases: []string{
				"- Backlog format: job_stories",
				"## Job Stories",
				"## Acceptance Criteria",
				"## Technical Notes",
				"## Open Questions",
			},
			wantFollowOnPhrase: "Generate test scenarios directly from the job stories",
		},
		{
			name:        "why what acceptance",
			title:       "Break the approved onboarding scope into why/what/acceptance format",
			description: "Decompose the approved onboarding scope into why/what/acceptance format for the cross-functional launch team with owners and dependency ordering.",
			wantFormat:  taskplan.BacklogFormatWhyWhatAcceptance,
			wantStoryPhrases: []string{
				"- Backlog format: why_what_acceptance",
				"## Why",
				"## What",
				"## Acceptance Criteria",
				"## Technical Notes",
				"## Open Questions",
			},
			wantFollowOnPhrase: "Generate test scenarios directly from the why/what/acceptance",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
				"project_id":  project.ID.String(),
				"title":       tc.title,
				"description": tc.description,
			})
			if err != nil {
				t.Fatalf("task.create: %v", err)
			}

			planning, ok := out["planning"].(map[string]any)
			if !ok {
				t.Fatalf("planning output = %T, want map[string]any", out["planning"])
			}
			if planning["playbook"] != taskplan.PlaybookBacklogDecomposition {
				t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookBacklogDecomposition)
			}
			contextPayload := planningContextPayload(t, planning)
			if got := strings.TrimSpace(fmt.Sprintf("%v", contextPayload["backlog_format"])); got != tc.wantFormat {
				t.Fatalf("planning.context.backlog_format = %q, want %q", got, tc.wantFormat)
			}

			followOns := planningStringSlice(t, planning["follow_on_suggestions"])
			if !containsSubstring(followOns, tc.wantFollowOnPhrase) {
				t.Fatalf("follow_on_suggestions = %#v, want phrase %q", followOns, tc.wantFollowOnPhrase)
			}

			storyArtifact := artifactPayloadBySlug(t, planning["artifacts"], "story-cards")
			storyPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, storyArtifact, "repo_path")))
			storyBody, err := os.ReadFile(storyPath)
			if err != nil {
				t.Fatalf("read story-cards artifact: %v", err)
			}
			for _, phrase := range tc.wantStoryPhrases {
				if !strings.Contains(string(storyBody), phrase) {
					t.Fatalf("story-cards artifact missing %q:\n%s", phrase, string(storyBody))
				}
			}

			sequencingArtifact := artifactPayloadBySlug(t, planning["artifacts"], "sequencing-plan")
			sequencingPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, sequencingArtifact, "repo_path")))
			sequencingBody, err := os.ReadFile(sequencingPath)
			if err != nil {
				t.Fatalf("read sequencing artifact: %v", err)
			}
			for _, phrase := range []string{"## Order", "## Dependencies", "## Design Input", "## Technical Spikes"} {
				if !strings.Contains(string(sequencingBody), phrase) {
					t.Fatalf("sequencing artifact missing %q:\n%s", phrase, string(sequencingBody))
				}
			}

			taskID := nestedUUID(t, out, "task", "id")
			taskRecord, err := taskRepo.GetByID(ctx, taskID)
			if err != nil {
				t.Fatalf("load task: %v", err)
			}
			plan, ok := taskplan.Parse(taskRecord.Metadata)
			if !ok {
				t.Fatal("taskplan.Parse(metadata) = false, want true")
			}
			if plan.BacklogFormat != tc.wantFormat {
				t.Fatalf("persisted BacklogFormat = %q, want %q", plan.BacklogFormat, tc.wantFormat)
			}
		})
	}
}

func TestIntegrationTaskCreateBacklogDecompositionSuggestsTestScenarioGeneration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	repoRoot := t.TempDir()

	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     func() *string { path := repoRoot; return &path }(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create project environment: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: repoRoot})

	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":  project.ID.String(),
		"title":       "Break the approved onboarding scope into why/what/acceptance format",
		"description": "Decompose the approved onboarding scope into why/what/acceptance format for the cross-functional launch team so test scenarios can follow directly from the backlog pack.",
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}

	planning, ok := out["planning"].(map[string]any)
	if !ok {
		t.Fatalf("planning output = %T, want map[string]any", out["planning"])
	}
	if planning["playbook"] != taskplan.PlaybookBacklogDecomposition {
		t.Fatalf("planning.playbook = %v, want %s", planning["playbook"], taskplan.PlaybookBacklogDecomposition)
	}

	followOns := planningStringSlice(t, planning["follow_on_suggestions"])
	if !containsSubstring(followOns, "Generate test scenarios directly from the why/what/acceptance") {
		t.Fatalf("follow_on_suggestions = %#v, want test-scenario generation guidance", followOns)
	}

	storyArtifact := artifactPayloadBySlug(t, planning["artifacts"], "story-cards")
	storyPath := filepath.Join(repoRoot, filepath.FromSlash(artifactStringValue(t, storyArtifact, "repo_path")))
	storyBody, err := os.ReadFile(storyPath)
	if err != nil {
		t.Fatalf("read story-cards artifact: %v", err)
	}
	body := string(storyBody)
	for _, phrase := range []string{"## Why", "## What", "## Acceptance Criteria"} {
		if !strings.Contains(body, phrase) {
			t.Fatalf("story-cards artifact missing %q:\n%s", phrase, body)
		}
	}
}

func TestIntegrationTaskCreateSelectsDifferentPlaybooksAndOutputs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	taskRepo := repo.NewProjectTaskRepo(pool)

	tests := []struct {
		title              string
		description        string
		wantPlaybook       string
		wantFirstArtifact  string
		wantFollowOnPhrase string
	}{
		{
			title:              "Validate the onboarding problem",
			description:        "Run customer interviews, document assumptions, and build a validation plan for this new product idea before we commit scope.",
			wantPlaybook:       taskplan.PlaybookDiscovery,
			wantFirstArtifact:  "Problem brief",
			wantFollowOnPhrase: "Schedule research or discovery interviews",
		},
		{
			title:              "PRD for billing migration",
			description:        "Write the PRD, implementation plan, acceptance criteria, and dependency log for the billing migration.",
			wantPlaybook:       taskplan.PlaybookExecutionSpec,
			wantFirstArtifact:  "PRD / requirements spec",
			wantFollowOnPhrase: "Review the spec with delivery owners",
		},
		{
			title:              "Go-to-market launch plan",
			description:        "Create the GTM launch plan, messaging brief, channel plan, and sales enablement checklist for release.",
			wantPlaybook:       taskplan.PlaybookGTMLaunch,
			wantFirstArtifact:  "Launch brief",
			wantFollowOnPhrase: "Align sales, support, and product owners",
		},
	}

	for _, tc := range tests {
		out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
			"project_id":  project.ID.String(),
			"title":       tc.title,
			"description": tc.description,
		})
		if err != nil {
			t.Fatalf("task.create %q: %v", tc.title, err)
		}

		planning, ok := out["planning"].(map[string]any)
		if !ok {
			t.Fatalf("planning output for %q = %T, want map[string]any", tc.title, out["planning"])
		}
		if planning["playbook"] != tc.wantPlaybook {
			t.Fatalf("planning.playbook for %q = %v, want %s", tc.title, planning["playbook"], tc.wantPlaybook)
		}

		var firstArtifact map[string]any
		switch typedArtifacts := planning["artifacts"].(type) {
		case []map[string]any:
			if len(typedArtifacts) == 0 {
				t.Fatalf("planning.artifacts for %q = empty, want artifact list", tc.title)
			}
			firstArtifact = typedArtifacts[0]
		case []any:
			if len(typedArtifacts) == 0 {
				t.Fatalf("planning.artifacts for %q = empty, want artifact list", tc.title)
			}
			cast, castOK := typedArtifacts[0].(map[string]any)
			if !castOK {
				t.Fatalf("first artifact for %q = %T, want map[string]any", tc.title, typedArtifacts[0])
			}
			firstArtifact = cast
		default:
			t.Fatalf("planning.artifacts for %q = %T, want slice", tc.title, planning["artifacts"])
		}
		if firstArtifact["title"] != tc.wantFirstArtifact {
			t.Fatalf("first artifact title for %q = %v, want %q", tc.title, firstArtifact["title"], tc.wantFirstArtifact)
		}

		var firstFollowOn string
		switch typedFollowOns := planning["follow_on_suggestions"].(type) {
		case []string:
			if len(typedFollowOns) == 0 {
				t.Fatalf("follow_on_suggestions for %q = empty, want suggestions", tc.title)
			}
			firstFollowOn = typedFollowOns[0]
		case []any:
			if len(typedFollowOns) == 0 {
				t.Fatalf("follow_on_suggestions for %q = empty, want suggestions", tc.title)
			}
			firstFollowOn = fmt.Sprintf("%v", typedFollowOns[0])
		default:
			t.Fatalf("follow_on_suggestions for %q = %T, want slice", tc.title, planning["follow_on_suggestions"])
		}
		if !strings.Contains(firstFollowOn, tc.wantFollowOnPhrase) {
			t.Fatalf("first follow-on for %q = %q, want phrase %q", tc.title, firstFollowOn, tc.wantFollowOnPhrase)
		}

		taskID := nestedUUID(t, out, "task", "id")
		taskRecord, err := taskRepo.GetByID(ctx, taskID)
		if err != nil {
			t.Fatalf("load task %q: %v", tc.title, err)
		}
		plan, ok := taskplan.Parse(taskRecord.Metadata)
		if !ok {
			t.Fatalf("taskplan.Parse(metadata) for %q = false, want true", tc.title)
		}
		if plan.Playbook != tc.wantPlaybook {
			t.Fatalf("persisted playbook for %q = %q, want %s", tc.title, plan.Playbook, tc.wantPlaybook)
		}
		if len(plan.Artifacts) == 0 || plan.Artifacts[0].Title != tc.wantFirstArtifact {
			t.Fatalf("persisted artifacts for %q = %#v, want first title %q", tc.title, plan.Artifacts, tc.wantFirstArtifact)
		}
		if len(plan.FollowOnSuggestions) == 0 || !strings.Contains(plan.FollowOnSuggestions[0], tc.wantFollowOnPhrase) {
			t.Fatalf("persisted follow-ons for %q = %#v, want phrase %q", tc.title, plan.FollowOnSuggestions, tc.wantFollowOnPhrase)
		}
	}
}

func TestIntegrationAgentAssignProjectCreatesPMAssignment(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	assignee := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)

	out, err := executor.Execute(ctx, "agent.assign_project", map[string]any{
		"agent_id":   assignee.ID.String(),
		"project_id": project.ID.String(),
		"role":       "pm",
	})
	if err != nil {
		t.Fatalf("agent.assign_project: %v", err)
	}
	assignment, ok := out["assignment"].(map[string]any)
	if !ok {
		t.Fatalf("assignment output = %T, want map[string]any", out["assignment"])
	}
	if mustUUIDValue(t, assignment["agent_id"]) != assignee.ID {
		t.Fatalf("assignment agent_id = %v, want %s", assignment["agent_id"], assignee.ID)
	}
	if mustUUIDValue(t, assignment["project_id"]) != project.ID {
		t.Fatalf("assignment project_id = %v, want %s", assignment["project_id"], project.ID)
	}
	if assignment["role"] != "project_manager" {
		t.Fatalf("assignment role = %v, want project_manager", assignment["role"])
	}

	var activeAssignments int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND agent_id = $2
		  AND role = 'project_manager'
		  AND is_active = true
	`, project.ID, assignee.ID).Scan(&activeAssignments); err != nil {
		t.Fatalf("count pm assignments: %v", err)
	}
	if activeAssignments != 1 {
		t.Fatalf("active pm assignments = %d, want 1", activeAssignments)
	}
}

func TestIntegrationFreshPMCandidateCanBeCreatedAndAssigned(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)

	createdOut, err := executor.Execute(ctx, "agent.create_staff", map[string]any{
		"name":          "Emiliano",
		"agent_type":    "pm",
		"system_prompt": "You are a project manager.",
	})
	if err != nil {
		t.Fatalf("agent.create_staff: %v", err)
	}
	createdAgent, ok := createdOut["agent"].(map[string]any)
	if !ok {
		t.Fatalf("agent.create_staff output = %T, want map[string]any", createdOut["agent"])
	}
	agentID := mustUUIDValue(t, createdAgent["id"])
	if createdAgent["agent_class"] != "staff" {
		t.Fatalf("created agent_class = %v, want staff", createdAgent["agent_class"])
	}
	if createdAgent["lifecycle_status"] != "draft" {
		t.Fatalf("created lifecycle_status = %v, want draft", createdAgent["lifecycle_status"])
	}

	assignOut, err := executor.Execute(ctx, "agent.assign_project", map[string]any{
		"agent_id":   agentID.String(),
		"project_id": project.ID.String(),
		"role":       "project_manager",
	})
	if err != nil {
		t.Fatalf("agent.assign_project: %v", err)
	}
	assignment, ok := assignOut["assignment"].(map[string]any)
	if !ok {
		t.Fatalf("assignment output = %T, want map[string]any", assignOut["assignment"])
	}
	if mustUUIDValue(t, assignment["agent_id"]) != agentID {
		t.Fatalf("assignment agent_id = %v, want %s", assignment["agent_id"], agentID)
	}

	assignedRecord, err := repo.NewAgentRepo(pool).GetByID(context.Background(), agentID)
	if err != nil {
		t.Fatalf("load assigned PM: %v", err)
	}
	if assignedRecord.AgentClass != "staff" {
		t.Fatalf("assigned agent_class = %q, want staff", assignedRecord.AgentClass)
	}
	if assignedRecord.LifecycleStatus != "active" {
		t.Fatalf("assigned lifecycle_status = %q, want active", assignedRecord.LifecycleStatus)
	}
}

func TestIntegrationAgentAssignProjectRejectsSecondPM(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	firstAgent := testutil.MakeAgent(t, pool, orgID)
	secondAgent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)

	if _, err := executor.Execute(ctx, "agent.assign_project", map[string]any{
		"agent_id":   firstAgent.ID.String(),
		"project_id": project.ID.String(),
		"role":       "pm",
	}); err != nil {
		t.Fatalf("agent.assign_project first pm: %v", err)
	}

	secondAssign, err := executor.Execute(ctx, "agent.assign_project", map[string]any{
		"agent_id":   secondAgent.ID.String(),
		"project_id": project.ID.String(),
		"role":       "pm",
	})
	if err != nil {
		t.Fatalf("agent.assign_project second pm: %v", err)
	}
	if secondAssign["error"] != "pm_conflict" {
		t.Fatalf("second pm error = %v, want pm_conflict", secondAssign["error"])
	}

	var activePMCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND role = 'project_manager'
		  AND is_active = true
	`, project.ID).Scan(&activePMCount); err != nil {
		t.Fatalf("count active pm assignments: %v", err)
	}
	if activePMCount != 1 {
		t.Fatalf("active pm assignments = %d, want 1", activePMCount)
	}
}

func TestIntegrationTempPMCandidateReturnsActionableValidationError(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)

	tempOut, err := executor.Execute(ctx, "agent.create_temp", map[string]any{
		"name":          "Temp Emiliano",
		"system_prompt": "You are a project manager.",
		"scope_type":    "project",
		"scope_id":      project.ID.String(),
	})
	if err != nil {
		t.Fatalf("agent.create_temp: %v", err)
	}
	tempAgent, ok := tempOut["agent"].(map[string]any)
	if !ok {
		t.Fatalf("agent.create_temp output = %T, want map[string]any", tempOut["agent"])
	}
	tempAgentID := mustUUIDValue(t, tempAgent["id"])

	assignOut, err := executor.Execute(ctx, "agent.assign_project", map[string]any{
		"agent_id":   tempAgentID.String(),
		"project_id": project.ID.String(),
		"role":       "project_manager",
	})
	if err != nil {
		t.Fatalf("agent.assign_project temp PM candidate: %v", err)
	}
	if assignOut["error"] != "project_manager_requires_staff_agent" {
		t.Fatalf("error = %v, want project_manager_requires_staff_agent", assignOut["error"])
	}
	if assignOut["message"] != staffPMCreationMessage {
		t.Fatalf("message = %v, want %q", assignOut["message"], staffPMCreationMessage)
	}

	var assignmentCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND agent_id = $2
	`, project.ID, tempAgentID).Scan(&assignmentCount); err != nil {
		t.Fatalf("count temp PM assignments: %v", err)
	}
	if assignmentCount != 0 {
		t.Fatalf("temp PM assignment rows = %d, want 0", assignmentCount)
	}
}

func TestIntegrationAgentAssignProjectRejectsStarterTrioProjectRoles(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)

	starterTrio, err := repo.NewAgentRepo(pool).Create(context.Background(), repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Frank",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "general",
		IsStarterTrio:        true,
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create starter trio agent: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)
	for _, role := range []string{"pm", "worker", "reviewer", "observer"} {
		out, err := executor.Execute(ctx, "agent.assign_project", map[string]any{
			"agent_id":   starterTrio.ID.String(),
			"project_id": project.ID.String(),
			"role":       role,
		})
		if err != nil {
			t.Fatalf("agent.assign_project %s: %v", role, err)
		}
		if out["error"] != agentsvc.StarterTrioProjectRoleErrorCode {
			t.Fatalf("%s error = %v, want %s", role, out["error"], agentsvc.StarterTrioProjectRoleErrorCode)
		}
		if out["message"] != agentsvc.ErrAssignmentStarterTrioRole.Error() {
			t.Fatalf("%s message = %v, want %q", role, out["message"], agentsvc.ErrAssignmentStarterTrioRole.Error())
		}
	}

	var assignments int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND agent_id = $2
	`, project.ID, starterTrio.ID).Scan(&assignments); err != nil {
		t.Fatalf("count starter trio assignments: %v", err)
	}
	if assignments != 0 {
		t.Fatalf("starter trio assignments = %d, want 0", assignments)
	}
}

func TestIntegrationTaskUpdateSetsFlowTemplateAndAssignedAgent(t *testing.T) {
	pool := testdb.New(t)
	backgroundCtx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	assignee := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, backgroundCtx, pool, project.ID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{})

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)
	out, err := executor.Execute(ctx, "task.update", map[string]any{
		"task_id":           task.ID.String(),
		"flow_template_id":  template.ID.String(),
		"assigned_agent_id": assignee.ID.String(),
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("task.update error = %v, want nil", out["error"])
	}

	updated, err := repo.NewProjectTaskRepo(pool).GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("load updated task: %v", err)
	}
	if updated.FlowTemplateID == nil || *updated.FlowTemplateID != template.ID {
		t.Fatalf("flow_template_id = %v, want %s", updated.FlowTemplateID, template.ID)
	}
	if updated.AssignedAgentID == nil || *updated.AssignedAgentID != assignee.ID {
		t.Fatalf("assigned_agent_id = %v, want %s", updated.AssignedAgentID, assignee.ID)
	}
}

func TestIntegrationTaskUpdateRejectsFlowTemplateChangeOutsideDraft(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	initialTemplate := testutil.MakeFlowTemplate(t, pool, project.ID, 1)
	replacementTemplate := testutil.MakeFlowTemplate(t, pool, project.ID, 1)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		FlowTemplateID: &initialTemplate.ID,
		WorkStatus:     "in_progress",
	})

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)
	out, err := executor.Execute(ctx, "task.update", map[string]any{
		"task_id":          task.ID.String(),
		"flow_template_id": replacementTemplate.ID.String(),
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != "flow_template_id can only be changed while task is draft" {
		t.Fatalf("error = %v, want draft-only flow template message", out["error"])
	}

	updated, err := repo.NewProjectTaskRepo(pool).GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("load task after rejected update: %v", err)
	}
	if updated.FlowTemplateID == nil || *updated.FlowTemplateID != initialTemplate.ID {
		t.Fatalf("flow_template_id = %v, want %s", updated.FlowTemplateID, initialTemplate.ID)
	}
}

func TestIntegrationFlowListTemplatesReturnsNodeSummaries(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	actorType := "agent"

	defaultReview, err := templateRepo.Create(context.Background(), repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &project.ID,
		Slug:           "default-review-" + uuid.NewString()[:8],
		DisplayName:    "Default Review",
		Description:    "default review flow",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create default review template: %v", err)
	}
	reviewWork, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: defaultReview.ID,
		DisplayName:    "Draft",
		NodeType:       "work",
		Position:       1,
		ActorType:      &actorType,
		ActorID:        &agent.ID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create default review work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: defaultReview.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		ActorType:      &actorType,
		ActorID:        &agent.ID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create default review review node: %v", err)
	}
	reviewWork.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(context.Background(), reviewWork); err != nil {
		t.Fatalf("link default review nodes: %v", err)
	}
	defaultReview.StartNodeID = &reviewWork.ID
	if _, err := templateRepo.Update(context.Background(), defaultReview); err != nil {
		t.Fatalf("set default review start node: %v", err)
	}

	bugfixTemplate, err := templateRepo.Create(context.Background(), repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &project.ID,
		Slug:           "bugfix-flow-" + uuid.NewString()[:8],
		DisplayName:    "Bugfix Flow",
		Description:    "bugfix flow",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create bugfix template: %v", err)
	}
	bugfixNode, err := nodeRepo.Create(context.Background(), repo.FlowNode{
		FlowTemplateID: bugfixTemplate.ID,
		DisplayName:    "Fix",
		NodeType:       "work",
		Position:       1,
		ActorType:      &actorType,
		ActorID:        &agent.ID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create bugfix node: %v", err)
	}
	bugfixTemplate.StartNodeID = &bugfixNode.ID
	if _, err := templateRepo.Update(context.Background(), bugfixTemplate); err != nil {
		t.Fatalf("set bugfix start node: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, agent.ID)
	out, err := executor.Execute(ctx, "flow.list_templates", map[string]any{
		"project_id": project.ID.String(),
	})
	if err != nil {
		t.Fatalf("flow.list_templates: %v", err)
	}

	rawTemplates, ok := out["templates"].([]map[string]any)
	if !ok {
		rawAny, castOK := out["templates"].([]any)
		if !castOK {
			t.Fatalf("templates output type = %T, want slice", out["templates"])
		}
		rawTemplates = make([]map[string]any, 0, len(rawAny))
		for _, item := range rawAny {
			row, rowOK := item.(map[string]any)
			if !rowOK {
				t.Fatalf("template row type = %T, want map[string]any", item)
			}
			rawTemplates = append(rawTemplates, row)
		}
	}
	if len(rawTemplates) < 2 {
		t.Fatalf("templates count = %d, want >= 2", len(rawTemplates))
	}

	foundDefaultReview := false
	for _, template := range rawTemplates {
		if strings.TrimSpace(fmt.Sprintf("%v", template["display_name"])) != "Default Review" {
			continue
		}
		foundDefaultReview = true
		nodes := make([]map[string]any, 0)
		switch typed := template["nodes"].(type) {
		case []map[string]any:
			nodes = typed
		case []any:
			for _, item := range typed {
				row, rowOK := item.(map[string]any)
				if !rowOK {
					t.Fatalf("node row type = %T, want map[string]any", item)
				}
				nodes = append(nodes, row)
			}
		default:
			t.Fatalf("template nodes type = %T, want slice", template["nodes"])
		}
		if len(nodes) < 2 {
			t.Fatalf("default review nodes count = %d, want >= 2", len(nodes))
		}
		first := nodes[0]
		if strings.TrimSpace(fmt.Sprintf("%v", first["display_name"])) == "" {
			t.Fatalf("first node display_name = %v, want non-empty", first["display_name"])
		}
		if strings.TrimSpace(fmt.Sprintf("%v", first["node_type"])) == "" {
			t.Fatalf("first node node_type = %v, want non-empty", first["node_type"])
		}
		if _, ok := first["position"]; !ok {
			t.Fatalf("first node missing position: %#v", first)
		}
		break
	}
	if !foundDefaultReview {
		t.Fatalf("expected template display_name %q in list", "Default Review")
	}
}

func TestIntegrationFlowCreateTemplateAcceptsWorkReviewSuccessPath(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "flow.create_template", map[string]any{
		"project_id": project.ID.String(),
		"name":       "Planner Minimum Flow",
		"nodes": []any{
			map[string]any{"display_name": "Work", "node_type": "work"},
			map[string]any{"display_name": "Review", "node_type": "review"},
			map[string]any{"display_name": "Success", "node_type": "success"},
		},
	})
	if err != nil {
		t.Fatalf("flow.create_template: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("flow.create_template error = %v, want nil", out["error"])
	}

	templateOut, ok := out["template"].(map[string]any)
	if !ok {
		t.Fatalf("template output = %T, want map[string]any", out["template"])
	}
	templateID := mustUUIDValue(t, templateOut["id"])

	template, err := repo.NewFlowTemplateRepo(pool).GetByID(ctx, templateID)
	if err != nil {
		t.Fatalf("load flow template: %v", err)
	}
	nodes, err := repo.NewFlowNodeRepo(pool).GetByTemplateOrdered(ctx, templateID)
	if err != nil {
		t.Fatalf("list flow nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("flow nodes = %d, want 3", len(nodes))
	}
	if template.StartNodeID == nil || *template.StartNodeID != nodes[0].ID {
		t.Fatalf("start_node_id = %v, want %s", template.StartNodeID, nodes[0].ID)
	}
	if nodes[0].NodeType != "work" || nodes[1].NodeType != "review" || nodes[2].NodeType != "merge" {
		t.Fatalf("node types = [%s %s %s], want [work review merge]", nodes[0].NodeType, nodes[1].NodeType, nodes[2].NodeType)
	}
	if nodes[0].NextNodeID == nil || *nodes[0].NextNodeID != nodes[1].ID {
		t.Fatalf("work next_node_id = %v, want %s", nodes[0].NextNodeID, nodes[1].ID)
	}
	if nodes[1].NextNodeID == nil || *nodes[1].NextNodeID != nodes[2].ID {
		t.Fatalf("review next_node_id = %v, want %s", nodes[1].NextNodeID, nodes[2].ID)
	}
	if nodes[2].NextNodeID != nil {
		t.Fatalf("merge next_node_id = %v, want nil", nodes[2].NextNodeID)
	}
}

func TestIntegrationFlowCreateTemplateNormalizesHumanReviewAlias(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "flow.create_template", map[string]any{
		"project_id": project.ID.String(),
		"name":       "Planner Human Review Flow",
		"nodes": []any{
			map[string]any{"display_name": "Work", "node_type": "work"},
			map[string]any{"display_name": "Human Review", "node_type": "human_review"},
			map[string]any{"display_name": "Completion", "node_type": "completion"},
		},
	})
	if err != nil {
		t.Fatalf("flow.create_template: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("flow.create_template error = %v, want nil", out["error"])
	}

	templateOut, ok := out["template"].(map[string]any)
	if !ok {
		t.Fatalf("template output = %T, want map[string]any", out["template"])
	}
	nodes, err := repo.NewFlowNodeRepo(pool).GetByTemplateOrdered(ctx, mustUUIDValue(t, templateOut["id"]))
	if err != nil {
		t.Fatalf("list flow nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("flow nodes = %d, want 3", len(nodes))
	}
	if nodes[1].NodeType != "review" {
		t.Fatalf("review node type = %q, want review", nodes[1].NodeType)
	}
	if !nodes[1].RequiresHumanReview {
		t.Fatal("requires_human_review = false, want true")
	}
	if nodes[2].NodeType != "merge" {
		t.Fatalf("completion node type = %q, want merge", nodes[2].NodeType)
	}
}

func TestIntegrationFlowCreateTemplateRejectsInvalidNodeTypeWithBoundedError(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "flow.create_template", map[string]any{
		"project_id": project.ID.String(),
		"name":       "Invalid Planner Flow",
		"nodes": []any{
			map[string]any{"display_name": "Work", "node_type": "work"},
			map[string]any{"display_name": "QA Gate", "node_type": "qa_gate"},
			map[string]any{"display_name": "Merge", "node_type": "merge"},
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
	if strings.TrimSpace(fmt.Sprintf("%v", out["message"])) == flowTemplateValidationMessage {
		t.Fatalf("message = %v, want bounded invalid-node guidance", out["message"])
	}
	if _, ok := out["template"]; ok {
		t.Fatalf("template output = %#v, want no template on invalid node type", out["template"])
	}
}

func TestIntegrationTaskUpdatePublishesStatusChangedDomainEvent(t *testing.T) {
	pool := testdb.New(t)
	backgroundCtx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, backgroundCtx, pool, project.ID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		FlowTemplateID: &template.ID,
		WorkStatus:     "draft",
	})

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)
	out, err := executor.Execute(ctx, "task.update", map[string]any{
		"task_id":     task.ID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("task.update error = %v, want nil", out["error"])
	}

	var eventCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'task.status_changed'
		  AND payload->>'task_id' = $2
		  AND payload->>'to_status' = 'queued'
	`, orgID, task.ID.String()).Scan(&eventCount); err != nil {
		t.Fatalf("count task.status_changed domain events: %v", err)
	}
	if eventCount < 1 {
		t.Fatalf("task.status_changed domain events = %d, want >= 1", eventCount)
	}
}

func TestIntegrationTaskUpdateAcceptsReadyAliasForQueued(t *testing.T) {
	pool := testdb.New(t)
	backgroundCtx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, backgroundCtx, pool, project.ID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		FlowTemplateID: &template.ID,
		WorkStatus:     "draft",
	})

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, actor.ID)
	out, err := executor.Execute(ctx, "task.update", map[string]any{
		"task_id":     task.ID.String(),
		"work_status": "ready",
	})
	if err != nil {
		t.Fatalf("task.update ready alias: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("task.update ready alias error = %v, want nil", out["error"])
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", out["work_status"])); got != "queued" {
		t.Fatalf("work_status = %q, want queued", got)
	}

	stored, err := repo.NewProjectTaskRepo(pool).GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetByID queued task: %v", err)
	}
	if stored.WorkStatus != "queued" {
		t.Fatalf("stored work_status = %q, want queued", stored.WorkStatus)
	}
}

func TestIntegrationSessionCreateProjectScopeReusesExistingSession(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	ctx := integrationExecCtxWith(orgID, agent.ID)

	first, err := executor.Execute(ctx, "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Sam.blog recovery",
	})
	if err != nil {
		t.Fatalf("first session.create: %v", err)
	}
	second, err := executor.Execute(ctx, "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Sam.blog recovery 2",
	})
	if err != nil {
		t.Fatalf("second session.create: %v", err)
	}

	firstSession := first["session"].(map[string]any)
	secondSession := second["session"].(map[string]any)
	firstID := mustUUIDValue(t, firstSession["id"])
	secondID := mustUUIDValue(t, secondSession["id"])
	if firstID != secondID {
		t.Fatalf("session ids = %s and %s, want reuse of canonical session", firstID, secondID)
	}

	var sessionCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM chat_session
		WHERE organization_id = $1
		  AND scope_type = 'project'
		  AND scope_id = $2
		  AND status = 'active'
	`, orgID, project.ID).Scan(&sessionCount); err != nil {
		t.Fatalf("count active project sessions: %v", err)
	}
	if sessionCount != 1 {
		t.Fatalf("active project sessions = %d, want 1", sessionCount)
	}
}

func TestIntegrationProjectCreateReusesArchivedSlugThroughProjectService(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	agent := testutil.MakeAgent(t, pool, orgID)

	projectRepo := repo.NewProjectRepo(pool)
	archived, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           "samblog",
		DisplayName:    "Sam.blog",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("seed archived project: %v", err)
	}
	if err := projectRepo.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("archive seeded project: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "project.create", map[string]any{
		"name": "Sam.blog",
		"slug": "samblog",
	})
	if err != nil {
		t.Fatalf("project.create archived slug reuse: %v", err)
	}

	projectOut, ok := out["project"].(map[string]any)
	if !ok {
		t.Fatalf("project output = %T, want map[string]any", out["project"])
	}
	createdID := mustUUIDValue(t, projectOut["id"])
	if createdID == archived.ID {
		t.Fatalf("created project id = %s, want fresh project distinct from archived %s", createdID, archived.ID)
	}
	gotSlug := strings.TrimSpace(fmt.Sprintf("%v", projectOut["slug"]))
	if gotSlug == "samblog" {
		t.Fatalf("created slug = %q, want archived slug suffixing", gotSlug)
	}

	created, err := projectRepo.GetByID(ctx, createdID)
	if err != nil {
		t.Fatalf("GetByID created project: %v", err)
	}
	if created.Status != "active" {
		t.Fatalf("created project status = %q, want active", created.Status)
	}

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListByProject bootstrap tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("expected bootstrap task tree to be created through native project.create")
	}
}

func TestIntegrationSessionCreateTaskBoundAsyncWorkUsesTaskSession(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		WorkStatus: "draft",
	})

	sessionRepo := repo.NewChatSessionRepo(pool)
	messageRepo := repo.NewChatMessageRepo(pool)
	projectSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"native-task-binding-test","kind":"project"}`),
	})
	if err != nil {
		t.Fatalf("create project async session: %v", err)
	}
	taskSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"native-task-binding-test","kind":"task"}`),
	})
	if err != nil {
		t.Fatalf("create task async session: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	execCtx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agent.ID,
		SessionID:      &projectSession.ID,
		ProjectID:      &project.ID,
		TaskID:         &task.ID,
	})

	out, err := executor.Execute(execCtx, "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "WS1: Landing Page - Execution",
	})
	if err != nil {
		t.Fatalf("session.create in task-bound project context: %v", err)
	}
	session := out["session"].(map[string]any)
	if got := mustUUIDValue(t, session["id"]); got != taskSession.ID {
		t.Fatalf("session id = %s, want task session %s", got, taskSession.ID)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", session["scope_type"])); got != "project_task" {
		t.Fatalf("session scope_type = %q, want project_task", got)
	}
	if got := mustUUIDValue(t, session["scope_id"]); got != task.ID {
		t.Fatalf("session scope_id = %s, want task %s", got, task.ID)
	}

	if _, err := executor.Execute(execCtx, "message.send", map[string]any{
		"session_id": taskSession.ID.String(),
		"role":       "user",
		"content":    "resume execution for this task only",
	}); err != nil {
		t.Fatalf("message.send to bound task session: %v", err)
	}

	taskMessages, err := messageRepo.ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession task messages: %v", err)
	}
	if len(taskMessages) != 1 {
		t.Fatalf("task session messages = %d, want 1", len(taskMessages))
	}
	if taskMessages[0].SessionID != taskSession.ID {
		t.Fatalf("task message session_id = %s, want %s", taskMessages[0].SessionID, taskSession.ID)
	}

	projectMessages, err := messageRepo.ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project messages: %v", err)
	}
	if len(projectMessages) != 0 {
		t.Fatalf("project session messages = %d, want 0", len(projectMessages))
	}

	var taskSessionCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE organization_id = $1
		  AND scope_type = 'project_task'
		  AND scope_id = $2
		  AND mode = 'async'
		  AND status = 'active'
	`, orgID, task.ID).Scan(&taskSessionCount); err != nil {
		t.Fatalf("count active task async sessions: %v", err)
	}
	if taskSessionCount != 1 {
		t.Fatalf("active task async sessions = %d, want 1", taskSessionCount)
	}
}

func TestIntegrationSessionInviteAgentIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	session := testutil.MakeSession(t, pool, orgID, "project", project.ID)
	participantRepo := repo.NewChatParticipantRepo(pool)

	existing, err := participantRepo.Create(ctx, repo.ChatParticipant{
		SessionID:              session.ID,
		ParticipantType:        "agent",
		ParticipantID:          agent.ID,
		NotificationPreference: "all",
		Role:                   "member",
	})
	if err != nil {
		t.Fatalf("seed existing participant: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "session.invite_agent", map[string]any{
		"session_id": session.ID.String(),
		"agent_id":   agent.ID.String(),
	})
	if err != nil {
		t.Fatalf("session.invite_agent duplicate: %v", err)
	}
	if out["already_present"] != true {
		t.Fatalf("already_present = %v, want true", out["already_present"])
	}
	if got := mustUUIDValue(t, out["participant_id"]); got != existing.ID {
		t.Fatalf("participant_id = %s, want existing %s", got, existing.ID)
	}

	participants, err := participantRepo.ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession participants: %v", err)
	}
	if len(participants) != 1 {
		t.Fatalf("participant rows = %d, want 1", len(participants))
	}
}

func TestIntegrationProjectArchiveClosesScopedSessions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{WorkStatus: "draft"})

	sessionRepo := repo.NewChatSessionRepo(pool)
	projectSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"native-project-archive-test","kind":"project"}`),
	})
	if err != nil {
		t.Fatalf("create project session: %v", err)
	}
	taskSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"native-project-archive-test","kind":"task"}`),
	})
	if err != nil {
		t.Fatalf("create task session: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "project.archive", map[string]any{
		"project_id": project.ID.String(),
	})
	if err != nil {
		t.Fatalf("project.archive: %v", err)
	}
	if out["status"] != "archived" {
		t.Fatalf("project.archive status = %v, want archived", out["status"])
	}

	storedProject, err := repo.NewProjectRepo(pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetByID project: %v", err)
	}
	if storedProject.Status != "archived" {
		t.Fatalf("stored project status = %q, want archived", storedProject.Status)
	}

	for _, sessionID := range []uuid.UUID{projectSession.ID, taskSession.ID} {
		stored, getErr := sessionRepo.GetByID(ctx, sessionID)
		if getErr != nil {
			t.Fatalf("GetByID session %s: %v", sessionID, getErr)
		}
		if stored.Status != "closed" || stored.ClosedAt == nil {
			t.Fatalf("stored session %+v, want closed with closed_at", stored)
		}
	}
}

func TestIntegrationTaskListDefaultsToCurrentProjectSessionScope(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	projectA := testutil.MakeProject(t, pool, orgID)
	projectB := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)

	taskA := testutil.MakeTask(t, pool, projectA.ID, testutil.MakeTaskOptions{
		Title:      "Archived rebuild workstream",
		WorkStatus: "draft",
	})
	taskB := testutil.MakeTask(t, pool, projectB.ID, testutil.MakeTaskOptions{
		Title:      "Fresh project kickoff",
		WorkStatus: "draft",
	})

	sessionRepo := repo.NewChatSessionRepo(pool)
	projectSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project",
		ScopeID:        projectB.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"native-task-list-scope-test"}`),
	})
	if err != nil {
		t.Fatalf("create project session: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWithSession(orgID, actor.ID, projectSession.ID), "task.list", map[string]any{})
	if err != nil {
		t.Fatalf("task.list: %v", err)
	}

	tasksPayload, ok := out["tasks"].([]map[string]any)
	if !ok {
		raw, ok := out["tasks"].([]any)
		if !ok {
			t.Fatalf("task.list tasks payload = %T, want []map[string]any", out["tasks"])
		}
		tasksPayload = make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			record, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("task.list task item = %T, want map[string]any", item)
			}
			tasksPayload = append(tasksPayload, record)
		}
	}

	if len(tasksPayload) != 1 {
		t.Fatalf("task.list returned %d tasks, want 1 in current project scope: %#v", len(tasksPayload), tasksPayload)
	}
	if got := mustUUIDValue(t, tasksPayload[0]["id"]); got != taskB.ID {
		t.Fatalf("task.list returned task %s, want current project task %s and not foreign task %s", got, taskB.ID, taskA.ID)
	}
}

func TestIntegrationTaskCreateRejectsNonBootstrapWorkWhileBootstrapGateIsOpen(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Bootstrap governance gate",
		WorkStatus:     "draft",
		BlocksScope:    "all",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{"bootstrap_gate":true}`),
	}); err != nil {
		t.Fatalf("create gate task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Bootstrap gated project session",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])

	_, err = executor.Execute(integrationExecCtxWithSession(orgID, actor.ID, sessionID), "task.create", map[string]any{
		"project_id":       project.ID.String(),
		"title":            "WS1: Regular workstream before bootstrap",
		"description":      "This should be blocked until bootstrap is complete.",
		"flow_template_id": template.ID.String(),
	})
	if !errors.Is(err, tasksvc.ErrProjectGateBlockingCreate) {
		t.Fatalf("task.create err = %v, want ErrProjectGateBlockingCreate", err)
	}

	tasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count after blocked native task.create = %d, want 1 gate task only", len(tasks))
	}
}

func TestIntegrationSessionListAndCreateIgnoreArchivedProjectTaskSessions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{WorkStatus: "draft"})

	sessionRepo := repo.NewChatSessionRepo(pool)
	staleTaskSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"native-project-archive-test","kind":"stale-task"}`),
	})
	if err != nil {
		t.Fatalf("create stale task session: %v", err)
	}
	if err := repo.NewProjectRepo(pool).Archive(ctx, project.ID); err != nil {
		t.Fatalf("Archive project directly: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	projectsOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "project.list", map[string]any{})
	if err != nil {
		t.Fatalf("project.list: %v", err)
	}
	projects, ok := projectsOut["projects"].([]map[string]any)
	if !ok {
		t.Fatalf("project.list projects payload type = %T, want []map[string]any", projectsOut["projects"])
	}
	if len(projects) != 0 {
		t.Fatalf("project.list projects = %+v, want no active projects after archive", projects)
	}

	sessionsOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "session.list", map[string]any{
		"scope_type": "project_task",
		"status":     "active",
	})
	if err != nil {
		t.Fatalf("session.list: %v", err)
	}
	sessions, ok := sessionsOut["sessions"].([]map[string]any)
	if !ok {
		t.Fatalf("session.list sessions payload type = %T, want []map[string]any", sessionsOut["sessions"])
	}
	if len(sessions) != 0 {
		t.Fatalf("session.list sessions = %+v, want archived-project task sessions filtered out", sessions)
	}

	createOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "session.create", map[string]any{
		"scope_type": "project_task",
		"scope_id":   task.ID.String(),
		"mode":       "async",
	})
	if err != nil {
		t.Fatalf("session.create archived task scope: %v", err)
	}
	if createOut["error"] != "scope_archived" {
		t.Fatalf("session.create error = %v, want scope_archived", createOut["error"])
	}

	stored, err := sessionRepo.GetByID(ctx, staleTaskSession.ID)
	if err != nil {
		t.Fatalf("GetByID stale task session: %v", err)
	}
	if stored.Status != "active" {
		t.Fatalf("legacy stale task session status = %q, want unchanged active row for filter regression", stored.Status)
	}
}

func TestIntegrationProjectSessionTaskPlanningIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Sam.blog recovery",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, agent.ID, sessionID)

	plannedTasks := []struct {
		title       string
		description string
	}{
		{
			title:       "Import legacy posts into the CMS",
			description: "Map legacy markdown entries into the CMS schema and verify canonical slug preservation.",
		},
		{
			title:       "Backfill media asset redirects",
			description: "Upload migrated media assets and validate redirect coverage for existing production URLs.",
		},
		{
			title:       "Validate taxonomy parity",
			description: "Compare migrated tags and categories against the legacy site and fix parity gaps.",
		},
	}

	firstPassIDs := make([]uuid.UUID, 0, len(plannedTasks))
	for _, plannedTask := range plannedTasks {
		out, execErr := executor.Execute(projectCtx, "task.create", map[string]any{
			"project_id":  project.ID.String(),
			"title":       plannedTask.title,
			"description": plannedTask.description,
		})
		if execErr != nil {
			t.Fatalf("first-pass task.create for %q: %v", plannedTask.title, execErr)
		}
		firstPassIDs = append(firstPassIDs, mustUUIDValue(t, out["task"].(map[string]any)["id"]))
	}

	for idx, plannedTask := range plannedTasks {
		out, execErr := executor.Execute(projectCtx, "task.create", map[string]any{
			"project_id":  project.ID.String(),
			"title":       plannedTask.title,
			"description": plannedTask.description,
		})
		if execErr != nil {
			t.Fatalf("second-pass task.create for %q: %v", plannedTask.title, execErr)
		}
		if repeatedID := mustUUIDValue(t, out["task"].(map[string]any)["id"]); repeatedID != firstPassIDs[idx] {
			t.Fatalf("second-pass task id for %q = %s, want %s", plannedTask.title, repeatedID, firstPassIDs[idx])
		}
	}

	taskRepo := repo.NewProjectTaskRepo(pool)
	tasks, err := taskRepo.ListByProject(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("list project tasks: %v", err)
	}
	if len(tasks) != len(plannedTasks) {
		t.Fatalf("project task count = %d, want %d", len(tasks), len(plannedTasks))
	}

	titleSet := make(map[string]struct{}, len(tasks))
	numberSet := make(map[int]struct{}, len(tasks))
	for _, task := range tasks {
		if _, exists := titleSet[task.Title]; exists {
			t.Fatalf("duplicate task title found after repeated recovery: %q", task.Title)
		}
		titleSet[task.Title] = struct{}{}
		if _, exists := numberSet[task.TaskNumber]; exists {
			t.Fatalf("duplicate task number found after repeated recovery: %d", task.TaskNumber)
		}
		numberSet[task.TaskNumber] = struct{}{}
	}
}

func TestIntegrationProjectSessionQueueKeepsPlannedTaskSetFlat(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, context.Background(), pool, project.ID)
	agent := testutil.MakeAgent(t, pool, orgID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Sam.blog fresh kickoff",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, agent.ID, sessionID)

	plannedTasks := []struct {
		title       string
		description string
	}{
		{
			title:       "Landing page rebuild",
			description: "Redesign the landing page hero and proof sections for the new launch.",
		},
		{
			title:       "Billing integration",
			description: "Wire Stripe checkout into the app for the first paid-plan launch slice.",
		},
		{
			title:       "Analytics foundation",
			description: "Define the first set of activation events for onboarding analytics.",
		},
		{
			title:       "Content migration",
			description: "Import the first batch of legacy posts into the new content model.",
		},
		{
			title:       "Launch operations",
			description: "Prepare the public launch rollback checklist for the release lead.",
		},
	}

	plannedIDs := make([]uuid.UUID, 0, len(plannedTasks))
	for _, plannedTask := range plannedTasks {
		out, execErr := executor.Execute(projectCtx, "task.create", map[string]any{
			"project_id":       project.ID.String(),
			"title":            plannedTask.title,
			"description":      plannedTask.description,
			"flow_template_id": template.ID.String(),
		})
		if execErr != nil {
			t.Fatalf("first-pass task.create for %q: %v", plannedTask.title, execErr)
		}
		plannedIDs = append(plannedIDs, mustUUIDValue(t, out["task"].(map[string]any)["id"]))
	}

	for idx, plannedTask := range plannedTasks {
		out, execErr := executor.Execute(projectCtx, "task.create", map[string]any{
			"project_id":       project.ID.String(),
			"title":            plannedTask.title,
			"description":      plannedTask.description,
			"flow_template_id": template.ID.String(),
		})
		if execErr != nil {
			t.Fatalf("second-pass task.create for %q: %v", plannedTask.title, execErr)
		}
		if repeatedID := mustUUIDValue(t, out["task"].(map[string]any)["id"]); repeatedID != plannedIDs[idx] {
			t.Fatalf("second-pass task id for %q = %s, want %s", plannedTask.title, repeatedID, plannedIDs[idx])
		}
	}

	for _, taskID := range plannedIDs {
		out, execErr := executor.Execute(projectCtx, "task.update", map[string]any{
			"task_id":     taskID.String(),
			"work_status": "queued",
		})
		if execErr != nil {
			t.Fatalf("task.update queued for %s: %v", taskID, execErr)
		}
		taskOut, ok := out["task"].(map[string]any)
		if !ok {
			t.Fatalf("task.update output task = %T, want map[string]any", out["task"])
		}
		if taskOut["work_status"] != "queued" {
			t.Fatalf("queued work_status = %v, want queued", taskOut["work_status"])
		}
		if _, hasDecomposition := out["decomposition"]; hasDecomposition {
			t.Fatalf("queue response unexpectedly included decomposition for %s: %v", taskID, out["decomposition"])
		}
	}

	taskRepo := repo.NewProjectTaskRepo(pool)
	tasks, err := taskRepo.ListByProject(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("list project tasks: %v", err)
	}
	if len(tasks) != len(plannedTasks) {
		t.Fatalf("project task count after queueing = %d, want %d", len(tasks), len(plannedTasks))
	}

	for _, task := range tasks {
		if strings.Contains(task.Title, "(Workstream ") {
			t.Fatalf("unexpected decomposed workstream task created during kickoff queueing: %q", task.Title)
		}
		if task.WorkStatus != "queued" {
			t.Fatalf("task %q work_status = %q, want queued", task.Title, task.WorkStatus)
		}
	}
}

func TestIntegrationQueueingDecomposedChildTaskDoesNotReDecomposeIt(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, context.Background(), pool, project.ID)
	agent := testutil.MakeAgent(t, pool, orgID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "WS1: Content Migration",
		Description:    stringPtr("Orchestration parent"),
		WorkStatus:     "draft",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}
	childTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "WS1.1: Discover all blog post URLs on technonymous.org",
		Description:    stringPtr("Use the browser to visit technonymous.org, navigate the site structure (homepage, archive pages, categories, pagination), and compile a complete list of all blog post URLs. Output: a text file listing every post URL found."),
		WorkStatus:     "draft",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
		Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentTask.ID, 2),
	})
	if err != nil {
		t.Fatalf("create child task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
		"task_id":     childTask.ID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update queued child task: %v", err)
	}
	if _, hasDecomposition := out["decomposition"]; hasDecomposition {
		t.Fatalf("queue response unexpectedly included decomposition for child task: %v", out["decomposition"])
	}

	tasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("list project tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("project task count after queueing child = %d, want 2", len(tasks))
	}
	for _, task := range tasks {
		if strings.Contains(task.Title, "(Workstream ") {
			t.Fatalf("unexpected decomposed workstream task created from child queueing: %q", task.Title)
		}
	}
	updatedChild, err := taskRepo.GetByID(ctx, childTask.ID)
	if err != nil {
		t.Fatalf("GetByID child task: %v", err)
	}
	if updatedChild.WorkStatus != "queued" {
		t.Fatalf("child work_status = %q, want queued", updatedChild.WorkStatus)
	}
}

func TestIntegrationProjectKickoffTaskCreateBindsCanonicalRepoBeforeTaskTree(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	actor := testutil.MakeAgent(t, pool, orgID)
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           "kickoff-repo-" + uuid.NewString()[:8],
		DisplayName:    "Kickoff Repo Binding",
		DeliveryMode:   "gated",
		CreatedByType:  "agent",
		CreatedByID:    actor.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	dataDir := t.TempDir()
	executor := NewExecutor(ExecutorOptions{Pool: pool, DataDir: dataDir})
	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Sam.blog fresh kickoff",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, actor.ID, sessionID)

	for _, title := range []string{"Kickoff parent workstream", "Kickoff child task"} {
		if _, err := executor.Execute(projectCtx, "task.create", map[string]any{
			"project_id": project.ID.String(),
			"title":      title,
		}); err != nil {
			t.Fatalf("task.create %q: %v", title, err)
		}
	}

	environments, err := repo.NewProjectEnvironmentRepo(pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject environments: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("project environment count = %d, want 1", len(environments))
	}

	wantRepoPath, err := workspace.ProjectRoot(dataDir, project.Slug)
	if err != nil {
		t.Fatalf("workspace.ProjectRoot: %v", err)
	}
	if got := strings.TrimSpace(derefString(environments[0].RepoPath)); got != wantRepoPath {
		t.Fatalf("repo_path = %q, want %q", got, wantRepoPath)
	}
	if environments[0].TargetBranch != "main" {
		t.Fatalf("target_branch = %q, want main", environments[0].TargetBranch)
	}

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("project task count = %d, want 2", len(tasks))
	}

	gitStatus, err := executor.Execute(projectCtx, "git.status", map[string]any{})
	if err != nil {
		t.Fatalf("git.status after kickoff repo binding: %v", err)
	}
	if got, _ := gitStatus["error"].(string); got != "" {
		t.Fatalf("git.status error = %q, want empty after canonical repo binding", got)
	}
	if branch := strings.TrimSpace(fmt.Sprintf("%v", gitStatus["branch"])); branch != "main" {
		t.Fatalf("git.status branch = %q, want main", branch)
	}
}

func TestIntegrationBootstrapGateAllowsPlanningTaskCreateBeforeGateClears(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	actor := testutil.MakeAgent(t, pool, orgID)
	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	projectOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "project.create", map[string]any{
		"name":        "Bootstrap Create Allowed",
		"slug":        "bootstrap-create-allowed-" + uuid.NewString()[:8],
		"description": "Verify bootstrap gating allows planning-time task persistence.",
	})
	if err != nil {
		t.Fatalf("project.create: %v", err)
	}
	projectID := mustUUIDValue(t, projectOut["project"].(map[string]any)["id"])
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, projectID)

	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
		"title":      "Bootstrap planning",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, actor.ID, sessionID)

	createOut, err := executor.Execute(projectCtx, "task.create", map[string]any{
		"project_id":       projectID.String(),
		"title":            "WS1: Bootstrap-planned research slice",
		"description":      "Create the first bounded task during bootstrap while the governance gate is still open.",
		"flow_template_id": template.ID.String(),
	})
	if err != nil {
		t.Fatalf("task.create during bootstrap gate: %v", err)
	}
	if createOut["error"] != nil {
		t.Fatalf("task.create error = %v, want nil", createOut["error"])
	}
	taskID := mustUUIDValue(t, createOut["task"].(map[string]any)["id"])
	createdTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if createdTask.WorkStatus != "draft" {
		t.Fatalf("created task status = %q, want draft", createdTask.WorkStatus)
	}
}

func TestIntegrationBootstrapPlanningTaskCreateSupportsMultipleParentWorkstreamsInSameProjectSession(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	actor := testutil.MakeAgent(t, pool, orgID)
	dataDir := t.TempDir()
	seedReviewRefinementSystemTemplate(t, ctx, pool)
	executor := NewExecutor(ExecutorOptions{Pool: pool, DataDir: dataDir})

	projectOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "project.create", map[string]any{
		"name":        "Bootstrap Multi Workstreams",
		"slug":        "bootstrap-multi-workstreams-" + uuid.NewString()[:8],
		"description": "Verify repeated planning task.create calls succeed during bootstrap in the same project session.",
	})
	if err != nil {
		t.Fatalf("project.create: %v", err)
	}
	projectID := mustUUIDValue(t, projectOut["project"].(map[string]any)["id"])

	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
		"title":      "Sam.blog bootstrap repro",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, actor.ID, sessionID)

	titles := []string{
		"Content Strategy",
		"Site Architecture & Design",
		"Site Build",
		"Content Creation",
	}
	for _, title := range titles {
		out, err := executor.Execute(projectCtx, "task.create", map[string]any{
			"project_id": projectID.String(),
			"title":      title,
		})
		if err != nil {
			t.Fatalf("task.create %q: %v", title, err)
		}
		if got := strings.TrimSpace(fmt.Sprintf("%v", out["error"])); got != "" && got != "<nil>" {
			t.Fatalf("task.create %q error = %q, want empty", title, got)
		}
	}

	environments, err := repo.NewProjectEnvironmentRepo(pool).ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListByProject environments: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("environment count = %d, want 1", len(environments))
	}
	repoPath := strings.TrimSpace(derefString(environments[0].RepoPath))
	if repoPath == "" {
		t.Fatal("expected repo_path on active environment")
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		t.Fatalf("stat .git after repeated planning task.create: %v", err)
	}

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	if len(tasks) < 12 {
		t.Fatalf("project task count = %d, want at least bootstrap tasks plus 4 workstreams", len(tasks))
	}
	for _, task := range tasks {
		switch strings.TrimSpace(task.Title) {
		case "Content Strategy",
			"Site Architecture & Design",
			"Site Build",
			"Content Creation":
			if task.FlowTemplateID == nil || *task.FlowTemplateID == uuid.Nil {
				t.Fatalf("bootstrap parent task %q flow_template_id = %v, want resolved template", task.Title, task.FlowTemplateID)
			}
		}
	}
}

func TestIntegrationBootstrapSetupPersistCompletesRequestedSetupSteps(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	actor := testutil.MakeAgent(t, pool, orgID)
	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	projectOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "project.create", map[string]any{
		"name":        "Bootstrap Persist Tool",
		"slug":        "bootstrap-persist-tool-" + uuid.NewString()[:8],
		"description": "Verify bootstrap.setup.persist completes setup checklist tasks.",
	})
	if err != nil {
		t.Fatalf("project.create: %v", err)
	}
	projectID := mustUUIDValue(t, projectOut["project"].(map[string]any)["id"])

	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
		"title":      "Bootstrap persist",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, actor.ID, sessionID)

	out, err := executor.Execute(projectCtx, "bootstrap.setup.persist", map[string]any{
		"completed_step_slugs": []string{"bind-repo-environment", "staff-project", "record-frank-sign-off"},
		"sign_off_summary":     "Frank approved the bootstrap setup.",
	})
	if err != nil {
		t.Fatalf("bootstrap.setup.persist: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("bootstrap.setup.persist error = %v, want nil", out["error"])
	}
	if out["status"] != "persisted" {
		t.Fatalf("status = %v, want persisted", out["status"])
	}

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("list project tasks: %v", err)
	}
	statuses := map[string]string{}
	for _, taskRecord := range tasks {
		metadata := metadataObject(taskRecord.Metadata)
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); !setupTask {
			continue
		}
		statuses[readStringValue(metadata["bootstrap_step_slug"])] = taskRecord.WorkStatus
	}
	for _, slug := range []string{"bind-repo-environment", "staff-project", "record-frank-sign-off"} {
		if got := statuses[slug]; got != "done" {
			t.Fatalf("bootstrap step %q status = %q, want done", slug, got)
		}
	}
}

func TestIntegrationBootstrapSetupPersistAcceptsNaturalStepAliases(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	actor := testutil.MakeAgent(t, pool, orgID)
	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	projectOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "project.create", map[string]any{
		"name":        "Bootstrap Persist Aliases",
		"slug":        "bootstrap-persist-aliases-" + uuid.NewString()[:8],
		"description": "Verify bootstrap.setup.persist accepts natural alias slugs.",
	})
	if err != nil {
		t.Fatalf("project.create: %v", err)
	}
	projectID := mustUUIDValue(t, projectOut["project"].(map[string]any)["id"])

	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
		"title":      "Bootstrap persist aliases",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, actor.ID, sessionID)

	out, err := executor.Execute(projectCtx, "bootstrap.setup.persist", map[string]any{
		"completed_step_slugs": []string{
			"bootstrap-governance",
			"bind_repo",
			"task_assignments",
			"decompose_tasks",
			"validate_tasks",
			"attach_flows",
			"first_wave_promotion",
			"frank_sign_off",
		},
		"sign_off_summary": "Frank approved the bootstrap setup.",
	})
	if err != nil {
		t.Fatalf("bootstrap.setup.persist aliases: %v", err)
	}
	if out["error"] != nil {
		t.Fatalf("bootstrap.setup.persist aliases error = %v, want nil", out["error"])
	}
	completedSteps, ok := out["completed_steps"].([]map[string]any)
	if !ok {
		rawSteps, ok := out["completed_steps"].([]any)
		if !ok {
			t.Fatalf("completed_steps type = %T, want slice", out["completed_steps"])
		}
		completedSteps = make([]map[string]any, 0, len(rawSteps))
		for _, item := range rawSteps {
			typed, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("completed_steps item type = %T, want map[string]any", item)
			}
			completedSteps = append(completedSteps, typed)
		}
	}
	var governanceAccepted bool
	for _, item := range completedSteps {
		if readStringValue(item["step_slug"]) != "bootstrap-governance-gate" {
			continue
		}
		governanceAccepted = readStringValue(item["status"]) == "accepted_noop"
	}
	if !governanceAccepted {
		t.Fatalf("completed_steps = %#v, want bootstrap-governance-gate accepted_noop marker", completedSteps)
	}

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("list project tasks: %v", err)
	}
	statuses := map[string]string{}
	for _, taskRecord := range tasks {
		metadata := metadataObject(taskRecord.Metadata)
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); !setupTask {
			continue
		}
		statuses[readStringValue(metadata["bootstrap_step_slug"])] = taskRecord.WorkStatus
	}
	for _, slug := range []string{
		"bind-repo-environment",
		"staff-project",
		"decompose-workstreams",
		"validate-task-shape",
		"attach-validate-flow-templates",
		"select-first-wave",
		"record-frank-sign-off",
	} {
		if got := statuses[slug]; got != "done" {
			t.Fatalf("bootstrap alias mapped step %q status = %q, want done", slug, got)
		}
	}
}

func TestIntegrationBootstrapSetupPersistUnknownStepReturnsValidCanonicalSlugs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	actor := testutil.MakeAgent(t, pool, orgID)
	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})

	projectOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "project.create", map[string]any{
		"name":        "Bootstrap Persist Unknown Step",
		"slug":        "bootstrap-persist-unknown-step-" + uuid.NewString()[:8],
		"description": "Verify bootstrap.setup.persist returns canonical valid slugs for unknown steps.",
	})
	if err != nil {
		t.Fatalf("project.create: %v", err)
	}
	projectID := mustUUIDValue(t, projectOut["project"].(map[string]any)["id"])

	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   projectID.String(),
		"mode":       "async",
		"title":      "Bootstrap persist unknown step",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, actor.ID, sessionID)

	out, err := executor.Execute(projectCtx, "bootstrap.setup.persist", map[string]any{
		"completed_step_slugs": []string{"flow_attachment", "first_wave_selection"},
	})
	if err != nil {
		t.Fatalf("bootstrap.setup.persist unknown step: %v", err)
	}
	if out["error"] != "unknown_bootstrap_step" {
		t.Fatalf("error = %v, want unknown_bootstrap_step", out["error"])
	}
	if out["message"] != "Use the canonical bootstrap setup step slugs returned in valid_step_slugs." {
		t.Fatalf("message = %v, want canonical slug guidance", out["message"])
	}

	valid, ok := out["valid_step_slugs"].([]string)
	if !ok {
		raw, ok := out["valid_step_slugs"].([]any)
		if !ok {
			t.Fatalf("valid_step_slugs type = %T, want slice", out["valid_step_slugs"])
		}
		valid = make([]string, 0, len(raw))
		for _, item := range raw {
			typed, ok := item.(string)
			if !ok {
				t.Fatalf("valid_step_slugs item type = %T, want string", item)
			}
			valid = append(valid, typed)
		}
	}

	want := []string{
		"bootstrap-governance-gate",
		"bind-repo-environment",
		"staff-project",
		"decompose-workstreams",
		"validate-task-shape",
		"attach-validate-flow-templates",
		"select-first-wave",
		"record-frank-sign-off",
	}
	if !reflect.DeepEqual(valid, want) {
		t.Fatalf("valid_step_slugs = %v, want %v", valid, want)
	}
}

func TestIntegrationTaskUpdateQueueKeepsDecomposedParentDraftAndQueuesChildren(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	description := strings.Join([]string{
		"- Define the first execution slice for the launch.",
		"- Draft the landing page structure for the launch.",
		"- Outline the initial analytics instrumentation checklist.",
	}, "\n")
	parentTask, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Launch readiness workstream",
		Description:    &description,
		WorkStatus:     "draft",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
		"task_id":     parentTask.ID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update queued: %v", err)
	}

	taskOut, ok := out["task"].(map[string]any)
	if !ok {
		t.Fatalf("task.update output task = %T, want map[string]any", out["task"])
	}
	if taskOut["work_status"] != "draft" {
		t.Fatalf("task output work_status = %v, want draft orchestration-only state", taskOut["work_status"])
	}
	decomposition, ok := out["decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("decomposition output = %T, want map[string]any", out["decomposition"])
	}
	childIDs, ok := decomposition["child_task_ids"].([]string)
	if !ok || len(childIDs) == 0 {
		t.Fatalf("decomposition.child_task_ids = %v, want non-empty []string", decomposition["child_task_ids"])
	}

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("list project tasks: %v", err)
	}
	var queuedChildren int
	for _, task := range tasks {
		if task.ID == parentTask.ID {
			if task.WorkStatus != "draft" {
				t.Fatalf("parent work_status = %q, want draft", task.WorkStatus)
			}
			continue
		}
		if taskdecomp.ParseParentTaskID(task.Metadata) != parentTask.ID {
			continue
		}
		if task.WorkStatus != "queued" {
			t.Fatalf("child task %q work_status = %q, want queued", task.Title, task.WorkStatus)
		}
		queuedChildren++
	}
	if queuedChildren != len(childIDs) {
		t.Fatalf("queued child task count = %d, want %d", queuedChildren, len(childIDs))
	}
}

func TestIntegrationTaskUpdateRejectsQueuedPromotionForOrchestrationParentWithoutChildren(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	description := "Coordinate the HTML template workstream and delegate the actual template builds into bounded child tasks."
	plan := taskplan.Analyze("WS2: HTML Layout Templates", &description)
	plan.FollowOnStopReason = "Parent task is orchestration-only; child subtasks provide the executable work."
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)

	parentTask, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "WS2: HTML Layout Templates",
		Description:    &description,
		WorkStatus:     "draft",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
		"task_id":     parentTask.ID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update queued: %v", err)
	}
	if out["error"] != taskNeedsChildTasksMessage {
		t.Fatalf("task.update error = %v, want %q", out["error"], taskNeedsChildTasksMessage)
	}

	storedTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, parentTask.ID)
	if err != nil {
		t.Fatalf("GetByID parent task: %v", err)
	}
	if storedTask.WorkStatus != "draft" {
		t.Fatalf("parent work_status = %q, want draft", storedTask.WorkStatus)
	}

	projectTasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("list project tasks: %v", err)
	}
	if len(projectTasks) != 1 {
		t.Fatalf("project task count = %d, want 1 with no synthesized children", len(projectTasks))
	}
}

func TestIntegrationProjectSessionTaskUpdateRejectsInProgressPromotionWithoutCanonicalExecution(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	assignee := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Bootstrap planning session",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])
	projectCtx := integrationExecCtxWithSession(orgID, actor.ID, sessionID)

	createOut, err := executor.Execute(projectCtx, "task.create", map[string]any{
		"project_id":       project.ID.String(),
		"title":            "WS1: Site Audit & Environment Verification",
		"description":      "Verify repo, staging environment, and migration constraints before execution begins.",
		"flow_template_id": template.ID.String(),
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	taskID := mustUUIDValue(t, createOut["task"].(map[string]any)["id"])

	queueOut, err := executor.Execute(projectCtx, "task.update", map[string]any{
		"task_id":     taskID.String(),
		"work_status": "queued",
	})
	if err != nil {
		t.Fatalf("task.update queue: %v", err)
	}
	if queueOut["error"] != nil {
		t.Fatalf("task.update queue error = %v, want nil", queueOut["error"])
	}

	taskAfterQueue, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load queued task: %v", err)
	}
	if taskAfterQueue.WorkStatus != "queued" {
		t.Fatalf("task work_status after queue = %q, want queued", taskAfterQueue.WorkStatus)
	}
	if taskAfterQueue.CurrentFlowNodeID != nil {
		t.Fatalf("current_flow_node_id after queue = %v, want nil before canonical execution claims it", taskAfterQueue.CurrentFlowNodeID)
	}

	updateOut, err := executor.Execute(projectCtx, "task.update", map[string]any{
		"task_id":           taskID.String(),
		"assigned_agent_id": assignee.ID.String(),
		"work_status":       "in_progress",
	})
	if err == nil {
		t.Fatalf("task.update error = nil, want active flow guard")
	}
	if !strings.Contains(err.Error(), "active flow") {
		t.Fatalf("task.update error = %v, want active flow guard", err)
	}
	if updateOut != nil {
		t.Fatalf("task.update output = %v, want nil on rejected direct promotion", updateOut)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if taskRecord.WorkStatus != "queued" {
		t.Fatalf("task work_status = %q, want queued after rejected direct promotion", taskRecord.WorkStatus)
	}
	if taskRecord.AssignedAgentID == nil || *taskRecord.AssignedAgentID != assignee.ID {
		t.Fatalf("assigned_agent_id = %v, want %s after rejected promotion", taskRecord.AssignedAgentID, assignee.ID)
	}
	if taskRecord.CurrentFlowNodeID != nil {
		t.Fatalf("current_flow_node_id = %v, want nil after rejected direct promotion", taskRecord.CurrentFlowNodeID)
	}

	executions, err := repo.NewFlowNodeExecutionRepo(pool).ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("list flow executions: %v", err)
	}
	if len(executions) != 0 {
		t.Fatalf("flow execution count = %d, want 0 after rejected direct promotion", len(executions))
	}
}

func TestIntegrationParentTaskCanReopenCompletedChildWithFeedback(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Launch integration gate",
		Description:    stringPtr("Verify the bounded launch work together."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}
	childTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Billing workstream",
		Description:    stringPtr("Finish the bounded billing handoff."),
		WorkStatus:     "done",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
		Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentTask.ID, 2),
	})
	if err != nil {
		t.Fatalf("create child task: %v", err)
	}
	parentTask.Metadata = taskdecomp.AppendChildTaskID(parentTask.Metadata, childTask.ID)
	if _, err := taskRepo.Update(ctx, parentTask); err != nil {
		t.Fatalf("update parent metadata: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, Events: bus, WorkspaceRoot: t.TempDir()})
	if _, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.update", map[string]any{
		"task_id":         childTask.ID.String(),
		"work_status":     "queued",
		"reopen_feedback": "Fix the checkout mismatch found during parent integration verification.",
	}); err != nil {
		t.Fatalf("task.update reopen child: %v", err)
	}

	reopened, err := taskRepo.GetByID(ctx, childTask.ID)
	if err != nil {
		t.Fatalf("GetByID reopened child: %v", err)
	}
	if reopened.WorkStatus != "queued" {
		t.Fatalf("child work_status = %q, want queued", reopened.WorkStatus)
	}
	if reopened.Description == nil || !strings.Contains(*reopened.Description, "Fix the checkout mismatch found during parent integration verification.") {
		t.Fatalf("child description = %v, want reopen feedback", reopened.Description)
	}
	var metadata map[string]any
	if err := json.Unmarshal(reopened.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal reopened metadata: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", metadata["parent_integration_feedback"])); got != "Fix the checkout mismatch found during parent integration verification." {
		t.Fatalf("parent_integration_feedback = %q, want reopen feedback", got)
	}
	if strings.TrimSpace(fmt.Sprintf("%v", metadata["parent_integration_feedback_recorded_at"])) == "" {
		t.Fatal("expected parent_integration_feedback_recorded_at")
	}
}

func TestIntegrationParentTaskCanCreateBoundedFollowOnChild(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Launch integration gate",
		Description:    stringPtr("Verify the bounded launch work together."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}
	existingChild, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Landing page workstream",
		Description:    stringPtr("Finish the landing page content pass."),
		WorkStatus:     "done",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
		Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentTask.ID, 2),
	})
	if err != nil {
		t.Fatalf("create existing child: %v", err)
	}
	parentTask.Metadata = taskdecomp.AppendChildTaskID(parentTask.Metadata, existingChild.ID)
	if _, err := taskRepo.Update(ctx, parentTask); err != nil {
		t.Fatalf("update parent metadata: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Analytics handoff patch",
		"description":    "Add the missing analytics event coverage for checkout success only.",
	})
	if err != nil {
		t.Fatalf("task.create follow-on child: %v", err)
	}

	childID := nestedUUID(t, out, "task", "id")
	createdChild, err := taskRepo.GetByID(ctx, childID)
	if err != nil {
		t.Fatalf("GetByID created child: %v", err)
	}
	if taskdecomp.ParseParentTaskID(createdChild.Metadata) != parentTask.ID {
		t.Fatalf("created child parent_task_id = %s, want %s", taskdecomp.ParseParentTaskID(createdChild.Metadata), parentTask.ID)
	}
	if createdChild.FlowTemplateID == nil || *createdChild.FlowTemplateID != template.ID {
		t.Fatalf("created child flow_template_id = %v, want %s", createdChild.FlowTemplateID, template.ID)
	}

	updatedParent, err := taskRepo.GetByID(ctx, parentTask.ID)
	if err != nil {
		t.Fatalf("GetByID updated parent: %v", err)
	}
	childIDs := taskdecomp.ParseChildTaskIDs(updatedParent.Metadata)
	if len(childIDs) != 2 {
		t.Fatalf("parent child_task_ids len = %d, want 2", len(childIDs))
	}
	if childIDs[1] != createdChild.ID {
		t.Fatalf("parent child_task_ids[1] = %s, want %s", childIDs[1], createdChild.ID)
	}
}

func TestIntegrationParentTaskCanDecomposeBroadFollowOnChildRequest(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Generate 20 new blog post concepts",
		Description:    stringPtr("Break the concepts into bounded batches that can be reviewed independently."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Generate 20 new blog post ideas across all pillars",
		"description":    "Develop the content backlog for the launch.",
	})
	if err != nil {
		t.Fatalf("task.create decomposed child follow-on: %v", err)
	}

	decomposition, ok := out["decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("decomposition = %T, want map", out["decomposition"])
	}
	if applied, _ := decomposition["applied"].(bool); !applied {
		t.Fatalf("decomposition.applied = %v, want true", decomposition["applied"])
	}

	rawTasks, ok := out["tasks"].([]map[string]any)
	if !ok {
		t.Fatalf("tasks = %T, want []map[string]any", out["tasks"])
	}
	if len(rawTasks) != 2 {
		t.Fatalf("tasks len = %d, want 2 decomposed child tasks", len(rawTasks))
	}

	updatedParent, err := taskRepo.GetByID(ctx, parentTask.ID)
	if err != nil {
		t.Fatalf("GetByID updated parent: %v", err)
	}
	childIDs := taskdecomp.ParseChildTaskIDs(updatedParent.Metadata)
	if len(childIDs) != 2 {
		t.Fatalf("parent child_task_ids len = %d, want 2", len(childIDs))
	}

	projectTasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	childCount := 0
	for _, projectTask := range projectTasks {
		if taskdecomp.ParseParentTaskID(projectTask.Metadata) != parentTask.ID {
			continue
		}
		childCount++
		if projectTask.FlowTemplateID == nil || *projectTask.FlowTemplateID != template.ID {
			t.Fatalf("child flow_template_id = %v, want %s", projectTask.FlowTemplateID, template.ID)
		}
	}
	if childCount != 2 {
		t.Fatalf("project child count = %d, want 2", childCount)
	}
}

func TestIntegrationParentTaskOversizedChildReturnsSuggestedDecomposition(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Content Strategy",
		Description:    stringPtr("Create the content strategy workstream."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Define audience personas: speakers & consultants",
		"description":    "Create 2 audience personas: (1) Event organizers seeking speakers on AI/ethics, (2) Companies seeking AI/ethics consultants. For each: demographics, goals, pain points, content habits, discovery path, desired action, and buying objections. Output: personas-speakers-consultants.md in repo.",
	})
	if err != nil {
		t.Fatalf("task.create oversized child: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", out["error"])); !strings.Contains(got, "task exceeds bounded size policy") {
		t.Fatalf("error = %q, want bounded size policy failure", got)
	}
	suggested, ok := out["suggested_decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("suggested_decomposition = %T, want map", out["suggested_decomposition"])
	}
	if mode := strings.TrimSpace(fmt.Sprintf("%v", suggested["mode"])); mode != "parallel_children" {
		t.Fatalf("suggested_decomposition.mode = %q, want parallel_children", mode)
	}
	if nextAction := strings.TrimSpace(fmt.Sprintf("%v", suggested["next_action"])); !strings.Contains(nextAction, "Do not retry the rejected task title") {
		t.Fatalf("suggested_decomposition.next_action = %q, want anti-retry guidance", nextAction)
	}
	childTitles, ok := suggested["child_titles"].([]any)
	if !ok || len(childTitles) < 2 {
		t.Fatalf("suggested_decomposition.child_titles = %#v, want at least 2 entries", suggested["child_titles"])
	}
}

func TestIntegrationParentTaskRepeatedDecompositionReusesCanonicalChildren(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Generate 20 new blog post ideas",
		Description:    stringPtr("Break the ideas into bounded child tasks that can be reviewed independently."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	firstOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Generate 20 new blog post ideas across all pillars",
		"description":    "Develop the launch backlog across every content pillar.",
	})
	if err != nil {
		t.Fatalf("task.create first decomposition: %v", err)
	}

	secondOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Generate 20 new blog post ideas across all pillars",
		"description":    "Retry with tighter descriptions for the same twenty launch backlog ideas.",
	})
	if err != nil {
		t.Fatalf("task.create repeated decomposition: %v", err)
	}

	firstDecomposition, ok := firstOut["decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("first decomposition = %T, want map", firstOut["decomposition"])
	}
	secondDecomposition, ok := secondOut["decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("second decomposition = %T, want map", secondOut["decomposition"])
	}

	firstIDsAny := parseUUIDSlicePayload(t, firstDecomposition["child_task_ids"])
	secondIDsAny := parseUUIDSlicePayload(t, secondDecomposition["child_task_ids"])
	if len(firstIDsAny) == 0 {
		t.Fatal("first decomposition child_task_ids empty")
	}
	if len(firstIDsAny) != len(secondIDsAny) {
		t.Fatalf("repeated child_task_ids len = %d, want %d", len(secondIDsAny), len(firstIDsAny))
	}
	for i := range firstIDsAny {
		if secondIDsAny[i] != firstIDsAny[i] {
			t.Fatalf("repeated child_task_ids[%d] = %s, want %s", i, secondIDsAny[i], firstIDsAny[i])
		}
	}

	updatedParent, err := taskRepo.GetByID(ctx, parentTask.ID)
	if err != nil {
		t.Fatalf("GetByID updated parent: %v", err)
	}
	parentChildIDs := taskdecomp.ParseChildTaskIDs(updatedParent.Metadata)
	if len(parentChildIDs) != len(firstIDsAny) {
		t.Fatalf("parent child_task_ids len = %d, want %d", len(parentChildIDs), len(firstIDsAny))
	}

	projectTasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	childCount := 0
	for _, projectTask := range projectTasks {
		if taskdecomp.ParseParentTaskID(projectTask.Metadata) != parentTask.ID {
			continue
		}
		childCount++
	}
	if childCount != len(firstIDsAny) {
		t.Fatalf("project child count after repeated decomposition = %d, want %d", childCount, len(firstIDsAny))
	}
}

func TestIntegrationParentTaskReusesOverlappingManualChildByCanonicalTitle(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Site Design — Build 10 HTML Layout Templates",
		Description:    stringPtr("Parent workstream for layout options."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	firstOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Template 6: Asymmetric Modern layout",
		"description":    "Build HTML template #6 with broken grid and overlapping elements.",
	})
	if err != nil {
		t.Fatalf("task.create first child: %v", err)
	}
	firstTaskID := nestedUUIDPath(t, firstOut, "task", "id")

	secondOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Template 6: Asymmetric Modern",
		"description":    "Retry Template 6 with a shorter bounded description.",
	})
	if err != nil {
		t.Fatalf("task.create overlapping child: %v", err)
	}
	secondTaskID := nestedUUIDPath(t, secondOut, "task", "id")

	if secondTaskID != firstTaskID {
		t.Fatalf("overlapping child id = %s, want reuse %s", secondTaskID, firstTaskID)
	}

	projectTasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	childCount := 0
	for _, projectTask := range projectTasks {
		if taskdecomp.ParseParentTaskID(projectTask.Metadata) != parentTask.ID {
			continue
		}
		childCount++
	}
	if childCount != 1 {
		t.Fatalf("project child count after overlapping retry = %d, want 1", childCount)
	}
}

func nestedUUIDPath(t *testing.T, raw map[string]any, path ...string) uuid.UUID {
	t.Helper()
	var current any = raw
	for _, key := range path {
		typed, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("nestedUUIDPath path %v hit %T", path, current)
		}
		current = typed[key]
	}
	switch typed := current.(type) {
	case uuid.UUID:
		return typed
	case string:
		parsed, err := uuid.Parse(strings.TrimSpace(typed))
		if err != nil {
			t.Fatalf("nestedUUIDPath parse %v: %v", path, err)
		}
		return parsed
	default:
		t.Fatalf("nestedUUIDPath path %v = %T", path, current)
		return uuid.Nil
	}
}

func parseUUIDSlicePayload(t *testing.T, raw any) []uuid.UUID {
	t.Helper()
	switch typed := raw.(type) {
	case []uuid.UUID:
		return append([]uuid.UUID(nil), typed...)
	case []string:
		out := make([]uuid.UUID, 0, len(typed))
		for i, item := range typed {
			parsed, err := uuid.Parse(strings.TrimSpace(item))
			if err != nil {
				t.Fatalf("uuid slice payload[%d] parse: %v", i, err)
			}
			out = append(out, parsed)
		}
		return out
	case []any:
		out := make([]uuid.UUID, 0, len(typed))
		for i, item := range typed {
			switch value := item.(type) {
			case uuid.UUID:
				out = append(out, value)
			case string:
				parsed, err := uuid.Parse(strings.TrimSpace(value))
				if err != nil {
					t.Fatalf("uuid slice payload[%d] parse: %v", i, err)
				}
				out = append(out, parsed)
			default:
				t.Fatalf("uuid slice payload[%d] = %T, want uuid/string", i, item)
			}
		}
		return out
	default:
		t.Fatalf("uuid slice payload = %T, want []uuid.UUID/[]string/[]any", raw)
		return nil
	}
}

func TestIntegrationParentTaskRepeatedDecompositionRequestReusesExistingChildren(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Build 10 HTML Layout Template Options",
		Description:    stringPtr("Build Template 2: Long-form Essay. Build Template 3: Technical Post. Build Template 4: Photography Gallery. Build Template 5: About/Speaker Page. Build Template 6: Archive/Index."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	input := map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          "Build 10 HTML Layout Template Options",
		"description":    "Build Template 2: Long-form Essay. Build Template 3: Technical Post. Build Template 4: Photography Gallery. Build Template 5: About/Speaker Page. Build Template 6: Archive/Index.",
	}

	firstOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", input)
	if err != nil {
		t.Fatalf("first task.create decomposition: %v", err)
	}
	firstTasks, ok := firstOut["tasks"].([]map[string]any)
	if !ok {
		t.Fatalf("first tasks = %T, want []map[string]any", firstOut["tasks"])
	}
	if len(firstTasks) != 5 {
		t.Fatalf("first tasks len = %d, want 5", len(firstTasks))
	}

	secondOut, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", input)
	if err != nil {
		t.Fatalf("second task.create decomposition: %v", err)
	}
	secondTasks, ok := secondOut["tasks"].([]map[string]any)
	if !ok {
		t.Fatalf("second tasks = %T, want []map[string]any", secondOut["tasks"])
	}
	if len(secondTasks) != 5 {
		t.Fatalf("second tasks len = %d, want 5 reused tasks", len(secondTasks))
	}

	updatedParent, err := taskRepo.GetByID(ctx, parentTask.ID)
	if err != nil {
		t.Fatalf("GetByID updated parent: %v", err)
	}
	if primary := taskdecomp.ParsePrimaryDeliverable(updatedParent.Metadata); strings.TrimSpace(primary) == "" {
		t.Fatalf("parent primary deliverable missing after decomposition metadata sync")
	}
	childIDs := taskdecomp.ParseChildTaskIDs(updatedParent.Metadata)
	if len(childIDs) != 5 {
		t.Fatalf("parent child_task_ids len = %d, want 5", len(childIDs))
	}

	projectTasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	childCount := 0
	for _, projectTask := range projectTasks {
		if taskdecomp.ParseParentTaskID(projectTask.Metadata) != parentTask.ID {
			continue
		}
		childCount++
	}
	if childCount != 5 {
		t.Fatalf("project child count after repeated decomposition = %d, want 5", childCount)
	}
}

func TestIntegrationParentTaskDecompositionDoesNotPersistOversizedPrimaryDeliverable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, ctx, pool, project.ID)

	taskRepo := repo.NewProjectTaskRepo(pool)
	parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "Content Strategy",
		Description:    stringPtr("Break content strategy work into bounded deliverables."),
		WorkStatus:     "review",
		FlowTemplateID: &template.ID,
		CreatedByType:  "agent",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	title := "Create a 3-month editorial calendar for Sam.blog with posting cadence, topic assignments across pillars, seasonal/topical hooks, and a distribution plan (SEO, social amplification, newsletter)"
	out, err := executor.Execute(integrationExecCtxWith(orgID, agent.ID), "task.create", map[string]any{
		"project_id":     project.ID.String(),
		"parent_task_id": parentTask.ID.String(),
		"title":          title,
		"description":    title,
	})
	if err != nil {
		t.Fatalf("task.create decomposed editorial calendar: %v", err)
	}
	if errText, ok := out["error"].(string); ok && strings.TrimSpace(errText) == "" {
		t.Fatalf("task.create error = %q, want non-empty bounded-size explanation when decomposition cannot persist safely", errText)
	}

	projectTasks, err := taskRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	childCount := 0
	for _, projectTask := range projectTasks {
		if taskdecomp.ParseParentTaskID(projectTask.Metadata) != parentTask.ID {
			continue
		}
		childCount++
		if projectTask.Title == title {
			t.Fatalf("persisted oversized child title = %q, want decomposition to split before persistence", projectTask.Title)
		}
	}
	if childCount != 0 {
		t.Fatalf("project child count = %d, want 0 because oversized primary deliverable must not persist unchanged", childCount)
	}
}

func TestIntegrationProjectSessionCreateAutoAddsAssignedProjectAgentsButNotStarterTrio(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	worker := testutil.MakeAgent(t, pool, orgID)
	pm := testutil.MakeAgent(t, pool, orgID)

	starterFrank, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Frank",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "general",
		IsStarterTrio:        true,
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create starter Frank: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	execCtx := integrationExecCtxWith(orgID, actor.ID)

	if _, err := executor.Execute(execCtx, "agent.assign_project", map[string]any{
		"agent_id":   worker.ID.String(),
		"project_id": project.ID.String(),
		"role":       "worker",
	}); err != nil {
		t.Fatalf("assign worker: %v", err)
	}
	if _, err := executor.Execute(execCtx, "agent.assign_project", map[string]any{
		"agent_id":   pm.ID.String(),
		"project_id": project.ID.String(),
		"role":       "project_manager",
	}); err != nil {
		t.Fatalf("assign pm: %v", err)
	}

	out, err := executor.Execute(execCtx, "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Assigned agents only",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}

	sessionID := mustUUIDValue(t, out["session"].(map[string]any)["id"])
	participantRepo := repo.NewChatParticipantRepo(pool)
	participants, err := participantRepo.ListBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListBySession participants: %v", err)
	}

	seenAgents := map[uuid.UUID]bool{}
	for _, participant := range participants {
		if participant.ParticipantType != "agent" || participant.RemovedAt != nil {
			continue
		}
		seenAgents[participant.ParticipantID] = true
	}

	if !seenAgents[worker.ID] {
		t.Fatalf("expected assigned worker %s in project session participants", worker.ID)
	}
	if !seenAgents[pm.ID] {
		t.Fatalf("expected assigned pm %s in project session participants", pm.ID)
	}
	if seenAgents[starterFrank.ID] {
		t.Fatalf("starter trio agent %s should not be auto-added to project session participants", starterFrank.ID)
	}
}

func TestIntegrationTaskSyncSessionCreateAutoAddsProjectPMAndWorkerButNotStarterTrio(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	worker := testutil.MakeAgent(t, pool, orgID)
	pm := testutil.MakeAgent(t, pool, orgID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		AssignedAgentID: &worker.ID,
	})

	starterFrank, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Frank",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "general",
		IsStarterTrio:        true,
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create starter Frank: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	execCtx := integrationExecCtxWith(orgID, actor.ID)

	if _, err := executor.Execute(execCtx, "agent.assign_project", map[string]any{
		"agent_id":   worker.ID.String(),
		"project_id": project.ID.String(),
		"role":       "worker",
	}); err != nil {
		t.Fatalf("assign worker: %v", err)
	}
	if _, err := executor.Execute(execCtx, "agent.assign_project", map[string]any{
		"agent_id":   pm.ID.String(),
		"project_id": project.ID.String(),
		"role":       "project_manager",
	}); err != nil {
		t.Fatalf("assign pm: %v", err)
	}

	out, err := executor.Execute(execCtx, "session.create", map[string]any{
		"scope_type": "project_task",
		"scope_id":   task.ID.String(),
		"mode":       "sync",
		"title":      "Task discussion",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}

	sessionID := mustUUIDValue(t, out["session"].(map[string]any)["id"])
	participantRepo := repo.NewChatParticipantRepo(pool)
	participants, err := participantRepo.ListBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListBySession participants: %v", err)
	}

	seenAgents := map[uuid.UUID]bool{}
	for _, participant := range participants {
		if participant.ParticipantType != "agent" || participant.RemovedAt != nil {
			continue
		}
		seenAgents[participant.ParticipantID] = true
	}

	if !seenAgents[worker.ID] {
		t.Fatalf("expected assigned worker %s in task session participants", worker.ID)
	}
	if !seenAgents[pm.ID] {
		t.Fatalf("expected assigned pm %s in task session participants", pm.ID)
	}
	if seenAgents[starterFrank.ID] {
		t.Fatalf("starter trio agent %s should not be auto-added to task session participants", starterFrank.ID)
	}
}

func TestIntegrationProjectSessionPlanningIgnoresStaleCrossProjectTaskBinding(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	staleProject := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	assignee := testutil.MakeAgent(t, pool, orgID)
	staleTask := testutil.MakeTask(t, pool, staleProject.ID, testutil.MakeTaskOptions{})

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	sessionOut, err := executor.Execute(integrationExecCtxWith(orgID, actor.ID), "session.create", map[string]any{
		"scope_type": "project",
		"scope_id":   project.ID.String(),
		"mode":       "async",
		"title":      "Sam.blog fresh kickoff",
	})
	if err != nil {
		t.Fatalf("session.create: %v", err)
	}
	sessionID := mustUUIDValue(t, sessionOut["session"].(map[string]any)["id"])

	projectExecCtx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &actor.ID,
		SessionID:      &sessionID,
		ProjectID:      &staleProject.ID,
		TaskID:         &staleTask.ID,
	})

	if _, err := executor.Execute(projectExecCtx, "agent.assign_project", map[string]any{
		"agent_id":   assignee.ID.String(),
		"project_id": project.ID.String(),
		"role":       "pm",
	}); err != nil {
		t.Fatalf("agent.assign_project: %v", err)
	}

	templateOut, err := executor.Execute(projectExecCtx, "flow.create_template", map[string]any{
		"project_id": project.ID.String(),
		"name":       "Fresh Kickoff Flow",
		"nodes": []any{
			map[string]any{"display_name": "Work", "node_type": "work"},
			map[string]any{"display_name": "Review", "node_type": "review", "requires_human_review": true},
			map[string]any{"display_name": "Merge", "node_type": "merge"},
		},
	})
	if err != nil {
		t.Fatalf("flow.create_template: %v", err)
	}
	templateID := nestedUUID(t, templateOut, "template", "id")

	taskOut, err := executor.Execute(projectExecCtx, "task.create", map[string]any{
		"project_id":       project.ID.String(),
		"title":            "Sam.blog kickoff planning",
		"description":      "Assign staffing, create workstreams, and bind the kickoff flow template.",
		"flow_template_id": templateID.String(),
	})
	if err != nil {
		t.Fatalf("task.create: %v", err)
	}
	taskID := nestedUUID(t, taskOut, "task", "id")

	createdTask, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("load created task: %v", err)
	}
	if createdTask.ProjectID != project.ID {
		t.Fatalf("created task project_id = %s, want %s", createdTask.ProjectID, project.ID)
	}
	if createdTask.FlowTemplateID == nil || *createdTask.FlowTemplateID != templateID {
		t.Fatalf("created task flow_template_id = %v, want %s", createdTask.FlowTemplateID, templateID)
	}

	var assignmentCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND agent_id = $2
		  AND is_active = true
	`, project.ID, assignee.ID).Scan(&assignmentCount); err != nil {
		t.Fatalf("count project assignments: %v", err)
	}
	if assignmentCount != 1 {
		t.Fatalf("project assignment count = %d, want 1", assignmentCount)
	}

	var templateCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_template
		WHERE project_id = $1
	`, project.ID).Scan(&templateCount); err != nil {
		t.Fatalf("count flow templates: %v", err)
	}
	if templateCount != 1 {
		t.Fatalf("project flow template count = %d, want 1", templateCount)
	}

	var taskCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE project_id = $1
		  AND title = 'Sam.blog kickoff planning'
	`, project.ID).Scan(&taskCount); err != nil {
		t.Fatalf("count project tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("project task count = %d, want 1", taskCount)
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

func TestIntegrationFlowAdvanceBackfillsMissingExecutionFromTaskScope(t *testing.T) {
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

	sessionRepo := repo.NewChatSessionRepo(pool)
	session, err := sessionRepo.Create(context.Background(), repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create task-scoped session: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{Pool: pool, WorkspaceRoot: t.TempDir()})
	sessionID := session.ID
	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agent.ID,
		SessionID:      &sessionID,
	})

	out, err := executor.Execute(ctx, "flow.advance", map[string]any{
		"flow_node_execution_id": uuid.NewString(),
		"commit_sha":             "abc123",
	})
	if err != nil {
		t.Fatalf("flow.advance with missing execution: %v", err)
	}
	if out["flow_completed"] != false {
		t.Fatalf("flow_completed = %v, want false", out["flow_completed"])
	}
	if mustUUIDValue(t, out["advanced_to_node_id"]) != nodes[1].ID {
		t.Fatalf("advanced_to_node_id = %v, want %s", out["advanced_to_node_id"], nodes[1].ID)
	}

	executionRepo := repo.NewFlowNodeExecutionRepo(pool)
	executions, err := executionRepo.ListByTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("list executions: %v", err)
	}
	var (
		backfilledExecutionSessionID uuid.UUID
		foundBackfilledCompleted     bool
		foundBackfilledSession       bool
		foundNextActiveWithSession   bool
	)
	for _, execution := range executions {
		switch execution.FlowNodeID {
		case nodes[0].ID:
			if execution.Status != "completed" {
				t.Fatalf("current node execution status = %q, want completed", execution.Status)
			}
			if execution.CommitSHA == nil || *execution.CommitSHA != "abc123" {
				t.Fatalf("current node commit_sha = %v, want abc123", execution.CommitSHA)
			}
			if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
				t.Fatalf("current node execution session_id = %v, want non-nil", execution.SessionID)
			}
			backfilledExecutionSessionID = *execution.SessionID
			foundBackfilledCompleted = true
			foundBackfilledSession = true
		case nodes[1].ID:
			if execution.Status == "active" {
				if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
					t.Fatalf("next node execution session_id = %v, want non-nil", execution.SessionID)
				}
				foundNextActiveWithSession = true
			}
		}
	}
	if !foundBackfilledCompleted {
		t.Fatal("expected backfilled execution for current node")
	}
	if !foundBackfilledSession {
		t.Fatal("expected backfilled execution to be linked to a chat session")
	}
	if !foundNextActiveWithSession {
		t.Fatal("expected active execution for next node with a chat session")
	}

	var sessionCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM chat_session
		WHERE id = $1
		  AND organization_id = $2
		  AND scope_type = 'project_task'
		  AND scope_id = $3
	`, backfilledExecutionSessionID, orgID, task.ID).Scan(&sessionCount); err != nil {
		t.Fatalf("count chat_session rows for backfilled task session: %v", err)
	}
	if sessionCount < 1 {
		t.Fatalf("chat_session rows for backfilled task session = %d, want >= 1", sessionCount)
	}
}

func TestIntegrationFlowAdvanceTerminalPublishesStatusChangedDomainEvent(t *testing.T) {
	pool := testdb.New(t)
	backgroundCtx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	template := makeExecutableProjectFlowTemplate(t, backgroundCtx, pool, project.ID)
	agent := testutil.MakeAgent(t, pool, orgID)

	nodeRepo := repo.NewFlowNodeRepo(pool)
	nodes, err := nodeRepo.GetByTemplateOrdered(context.Background(), template.ID)
	if err != nil {
		t.Fatalf("list flow nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes count = %d, want 3", len(nodes))
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
	if _, err := taskRepo.SetFlowNode(context.Background(), task.ID, &nodes[2].ID); err != nil {
		t.Fatalf("set task current flow node: %v", err)
	}

	executionRepo := repo.NewFlowNodeExecutionRepo(pool)
	if _, err := executionRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("create completed work execution: %v", err)
	}
	if _, err := executionRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  nodes[1].ID,
		VisitNumber: 1,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("create completed review execution: %v", err)
	}
	execution, err := executionRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  nodes[2].ID,
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

	var updatedStatus string
	if err := pool.QueryRow(context.Background(), `SELECT work_status FROM project_task WHERE id = $1`, task.ID).Scan(&updatedStatus); err != nil {
		t.Fatalf("load task status: %v", err)
	}
	if updatedStatus != "done" {
		t.Fatalf("task work_status = %q, want done", updatedStatus)
	}

	var statusChangedEvents int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'task.status_changed'
		  AND payload->>'task_id' = $2
	`, orgID, task.ID.String()).Scan(&statusChangedEvents); err != nil {
		t.Fatalf("count task.status_changed domain events: %v", err)
	}
	if statusChangedEvents < 1 {
		t.Fatalf("task.status_changed domain events = %d, want >= 1", statusChangedEvents)
	}
}

type failEventPublisher struct {
	failOn string
}

func (f *failEventPublisher) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	if strings.TrimSpace(event.EventType) == strings.TrimSpace(f.failOn) {
		return fmt.Errorf("forced publish failure for %s", event.EventType)
	}
	return nil
}

func TestIntegrationFlowAdvanceTerminalRollsBackTaskUpdateWhenEventPublishFails(t *testing.T) {
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

	executor := NewExecutor(ExecutorOptions{
		Pool:          pool,
		WorkspaceRoot: t.TempDir(),
		Events:        &failEventPublisher{failOn: "task.status_changed"},
	})
	ctx := integrationExecCtxWith(orgID, agent.ID)
	if _, err := executor.Execute(ctx, "flow.advance", map[string]any{
		"flow_node_execution_id": execution.ID.String(),
	}); err == nil {
		t.Fatal("expected flow.advance failure when domain event publish fails")
	}

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("load updated task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress after rollback", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[0].ID {
		t.Fatalf("task current_flow_node_id = %v, want %s after rollback", updatedTask.CurrentFlowNodeID, nodes[0].ID)
	}

	var statusChangedEvents int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'task.status_changed'
		  AND payload->>'task_id' = $2
	`, orgID, task.ID.String()).Scan(&statusChangedEvents); err != nil {
		t.Fatalf("count task.status_changed domain events: %v", err)
	}
	if statusChangedEvents != 0 {
		t.Fatalf("task.status_changed domain events = %d, want 0 after rollback", statusChangedEvents)
	}
}

func TestIntegrationFlowAdvanceToNextNodeRollsBackRuntimeStateWhenEventPublishFails(t *testing.T) {
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

	executionRepo := repo.NewFlowNodeExecutionRepo(pool)
	originalExecution, err := executionRepo.Create(context.Background(), repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("create flow execution: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{
		Pool:          pool,
		WorkspaceRoot: t.TempDir(),
		Events:        &failEventPublisher{failOn: "flow.advanced"},
	})
	ctx := integrationExecCtxWith(orgID, agent.ID)
	if _, err := executor.Execute(ctx, "flow.advance", map[string]any{
		"flow_node_execution_id": originalExecution.ID.String(),
	}); err == nil {
		t.Fatal("expected flow.advance failure when flow.advanced publish fails")
	}

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("load updated task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress after rollback", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[0].ID {
		t.Fatalf("task current_flow_node_id = %v, want %s after rollback", updatedTask.CurrentFlowNodeID, nodes[0].ID)
	}

	executions, err := executionRepo.ListByTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("list executions: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("execution count = %d, want 1 after rollback", len(executions))
	}
	if executions[0].ID != originalExecution.ID {
		t.Fatalf("execution[0].id = %s, want original %s", executions[0].ID, originalExecution.ID)
	}
	if executions[0].Status != "active" {
		t.Fatalf("execution[0].status = %q, want active after rollback", executions[0].Status)
	}

	var advancedEvents int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'flow.advanced'
		  AND payload->>'task_id' = $2
	`, orgID, task.ID.String()).Scan(&advancedEvents); err != nil {
		t.Fatalf("count flow.advanced domain events: %v", err)
	}
	if advancedEvents != 0 {
		t.Fatalf("flow.advanced domain events = %d, want 0 after rollback", advancedEvents)
	}
}

func TestIntegrationFlowRejectRollsBackRuntimeStateWhenEventPublishFails(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	projectRecord, err := repo.NewProjectRepo(pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &projectRecord.OrganizationID,
		ProjectID:      &project.ID,
		Slug:           "reject-flow-" + strings.ToLower(uuid.NewString()[:8]),
		DisplayName:    "Reject Flow " + uuid.NewString()[:8],
		Description:    "test flow template with reject path",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create merge node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("update work node: %v", err)
	}
	reviewNode.NextNodeID = &mergeNode.ID
	reviewNode.RejectNodeID = &workNode.ID
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("update review node: %v", err)
	}
	template.StartNodeID = &workNode.ID
	if _, err := templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("update template: %v", err)
	}

	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{
		FlowTemplateID: &template.ID,
		WorkStatus:     "review",
		CreatedByType:  "system",
		CreatedByID: func() *uuid.UUID {
			value := uuid.Nil
			return &value
		}(),
	})
	taskRepo := repo.NewProjectTaskRepo(pool)
	if _, err := taskRepo.SetFlowNode(ctx, task.ID, &reviewNode.ID); err != nil {
		t.Fatalf("set task current flow node: %v", err)
	}

	executionRepo := repo.NewFlowNodeExecutionRepo(pool)
	originalExecution, err := executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  reviewNode.ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("create review execution: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{
		Pool:          pool,
		WorkspaceRoot: t.TempDir(),
		Events:        &failEventPublisher{failOn: "flow.rejected"},
	})
	execCtx := integrationExecCtxWith(orgID, agent.ID)
	if _, err := executor.Execute(execCtx, "flow.review_decision", map[string]any{
		"flow_node_execution_id": originalExecution.ID.String(),
		"decision":               "reject",
	}); err == nil {
		t.Fatal("expected flow.review_decision rejection failure when flow.rejected publish fails")
	}

	updatedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("load updated task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review after rollback", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != reviewNode.ID {
		t.Fatalf("task current_flow_node_id = %v, want %s after rollback", updatedTask.CurrentFlowNodeID, reviewNode.ID)
	}

	executions, err := executionRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list executions: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("execution count = %d, want 1 after rollback", len(executions))
	}
	if executions[0].ID != originalExecution.ID {
		t.Fatalf("execution[0].id = %s, want original %s", executions[0].ID, originalExecution.ID)
	}
	if executions[0].Status != "active" {
		t.Fatalf("execution[0].status = %q, want active after rollback", executions[0].Status)
	}

	var rejectedEvents int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'flow.rejected'
		  AND payload->>'task_id' = $2
	`, orgID, task.ID.String()).Scan(&rejectedEvents); err != nil {
		t.Fatalf("count flow.rejected domain events: %v", err)
	}
	if rejectedEvents != 0 {
		t.Fatalf("flow.rejected domain events = %d, want 0 after rollback", rejectedEvents)
	}
}

func integrationExecCtxWith(orgID, agentID uuid.UUID) context.Context {
	return mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})
}

func integrationExecCtxWithSession(orgID, agentID, sessionID uuid.UUID) context.Context {
	return mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
		SessionID:      &sessionID,
	})
}

func stringPtr(value string) *string {
	return &value
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

func seedReviewRefinementSystemTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.FlowTemplate {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		Slug:          "default-review-refinement",
		DisplayName:   "Default Review Refinement",
		Description:   "Subjective multi-option review refinement",
		IsCurrent:     true,
		Version:       1,
		IsSystem:      true,
		CreatedByType: "system",
		CreatedByID:   uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create system review refinement template: %v", err)
	}

	generation, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Generation",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create generation node: %v", err)
	}
	internalReview, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Internal Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create internal review node: %v", err)
	}
	humanReview, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID:      template.ID,
		DisplayName:         "Human Review",
		NodeType:            "review",
		Position:            3,
		MaxVisits:           10,
		RequiresHumanReview: true,
	})
	if err != nil {
		t.Fatalf("create human review node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Complete",
		NodeType:       "merge",
		Position:       4,
		MaxVisits:      1,
	})
	if err != nil {
		t.Fatalf("create merge node: %v", err)
	}

	generation.NextNodeID = &internalReview.ID
	if _, err := nodeRepo.Update(ctx, generation); err != nil {
		t.Fatalf("link generation node: %v", err)
	}
	internalReview.NextNodeID = &humanReview.ID
	if _, err := nodeRepo.Update(ctx, internalReview); err != nil {
		t.Fatalf("link internal review node: %v", err)
	}
	humanReview.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(ctx, humanReview); err != nil {
		t.Fatalf("link human review node: %v", err)
	}

	template.StartNodeID = &generation.ID
	updated, err := templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("set review refinement start node: %v", err)
	}
	return updated
}

func makeExecutableProjectFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) repo.FlowTemplate {
	t.Helper()

	projectRecord, err := repo.NewProjectRepo(pool).GetByID(ctx, projectID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &projectRecord.OrganizationID,
		ProjectID:      &projectID,
		Slug:           "flow-" + strings.ToLower(uuid.NewString()[:8]),
		DisplayName:    "Executable Flow " + uuid.NewString()[:8],
		Description:    "test flow template with terminal review path",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create executable flow template: %v", err)
	}

	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Complete",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create merge node: %v", err)
	}

	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("link work node: %v", err)
	}
	reviewNode.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("link review node: %v", err)
	}

	template.StartNodeID = &workNode.ID
	updated, err := templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("set executable flow start node: %v", err)
	}
	return updated
}

func seedInternalReviewSystemTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.FlowTemplate {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		Slug:          "default-review",
		DisplayName:   "Default Review",
		Description:   "Delegated creative work with internal review",
		IsCurrent:     true,
		Version:       1,
		IsSystem:      true,
		CreatedByType: "system",
		CreatedByID:   uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create system internal review template: %v", err)
	}

	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	internalReview, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Internal Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      10,
	})
	if err != nil {
		t.Fatalf("create internal review node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Complete",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      1,
	})
	if err != nil {
		t.Fatalf("create merge node: %v", err)
	}

	workNode.NextNodeID = &internalReview.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("link work node: %v", err)
	}
	internalReview.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(ctx, internalReview); err != nil {
		t.Fatalf("link internal review node: %v", err)
	}

	template.StartNodeID = &workNode.ID
	updated, err := templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("set internal review start node: %v", err)
	}
	return updated
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func containsSubstring(items []string, needle string) bool {
	for _, item := range items {
		if strings.Contains(item, needle) {
			return true
		}
	}
	return false
}

func firstArtifactPayload(t *testing.T, raw any) map[string]any {
	t.Helper()
	items := artifactPayloads(t, raw)
	return items[0]
}

func artifactPayloadBySlug(t *testing.T, raw any, slug string) map[string]any {
	t.Helper()
	for _, artifact := range artifactPayloads(t, raw) {
		if artifactStringValue(t, artifact, "slug") == slug {
			return artifact
		}
	}
	t.Fatalf("artifact payload missing slug %q", slug)
	return nil
}

func artifactPayloads(t *testing.T, raw any) []map[string]any {
	t.Helper()
	switch typed := raw.(type) {
	case []map[string]any:
		if len(typed) == 0 {
			t.Fatal("artifact payload = empty, want at least one artifact")
		}
		return typed
	case []any:
		if len(typed) == 0 {
			t.Fatal("artifact payload = empty, want at least one artifact")
		}
		out := make([]map[string]any, 0, len(typed))
		for i, item := range typed {
			row, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("artifact payload[%d] = %T, want map[string]any", i, item)
			}
			out = append(out, row)
		}
		return out
	default:
		t.Fatalf("artifact payload = %T, want slice", raw)
	}
	return nil
}

func planningContextPayload(t *testing.T, planning map[string]any) map[string]any {
	t.Helper()
	raw, ok := planning["context"]
	if !ok {
		t.Fatalf("planning missing context: %#v", planning)
	}
	context, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("planning.context = %T, want map[string]any", raw)
	}
	return context
}

func planningFollowOnPayload(t *testing.T, planning map[string]any) map[string]any {
	t.Helper()
	raw, ok := planning["follow_on"]
	if !ok {
		t.Fatalf("planning missing follow_on: %#v", planning)
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("planning.follow_on = %T, want map[string]any", raw)
	}
	return payload
}

func planningFollowOnCandidates(t *testing.T, planning map[string]any) []map[string]any {
	t.Helper()
	payload := planningFollowOnPayload(t, planning)
	raw, ok := payload["candidates"]
	if !ok {
		t.Fatalf("planning.follow_on missing candidates: %#v", payload)
	}
	switch typed := raw.(type) {
	case []map[string]any:
		if len(typed) == 0 {
			t.Fatal("planning.follow_on.candidates = empty, want candidates")
		}
		return typed
	case []any:
		if len(typed) == 0 {
			t.Fatal("planning.follow_on.candidates = empty, want candidates")
		}
		out := make([]map[string]any, 0, len(typed))
		for i, item := range typed {
			candidate, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("planning.follow_on.candidates[%d] = %T, want map[string]any", i, item)
			}
			out = append(out, candidate)
		}
		return out
	default:
		t.Fatalf("planning.follow_on.candidates = %T, want slice", raw)
	}
	return nil
}

func planningStringSlice(t *testing.T, raw any) []string {
	t.Helper()
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for i, item := range typed {
			value, ok := item.(string)
			if !ok {
				t.Fatalf("planning string slice[%d] = %T, want string", i, item)
			}
			out = append(out, value)
		}
		return out
	default:
		t.Fatalf("planning string slice = %T, want []string/[]any", raw)
	}
	return nil
}

func artifactStringValue(t *testing.T, artifact map[string]any, key string) string {
	t.Helper()
	raw, ok := artifact[key]
	if !ok {
		t.Fatalf("artifact missing key %q: %#v", key, artifact)
	}
	value, ok := raw.(string)
	if !ok {
		t.Fatalf("artifact[%q] = %T, want string", key, raw)
	}
	return strings.TrimSpace(value)
}

func artifactVersionValue(t *testing.T, raw any) int {
	t.Helper()
	switch typed := raw.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		t.Fatalf("artifact version = %T, want numeric type", raw)
	}
	return 0
}

func integrationExecCtx() context.Context {
	orgID := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	agentID := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	return mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})
}

type deterministicMemoryQueryEmbedder struct{}

func (deterministicMemoryQueryEmbedder) Embed(_ context.Context, _ uuid.UUID, _ string, inputs []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		vectors = append(vectors, deterministicMemoryEmbedding(input))
	}
	return vectors, nil
}
