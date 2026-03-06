//go:build integration

package native

import (
	"context"
	"encoding/json"
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
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
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
	if len(nodes) != 3 {
		t.Fatalf("template node count = %d, want 3", len(nodes))
	}
	if nodes[0].DisplayName != "Generation" || nodes[1].DisplayName != "Internal Review" || nodes[2].DisplayName != "Human Review" {
		t.Fatalf("unexpected node sequence = [%s %s %s]", nodes[0].DisplayName, nodes[1].DisplayName, nodes[2].DisplayName)
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
	if _, err := flowService.AdvanceFlow(ctx, firstTaskID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("AdvanceFlow internal review: %v", err)
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
	if !containsString(checklistJoined, "Verify the launch brief defines scope, audience, and timing for the rollout.") {
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

	template := testutil.MakeFlowTemplate(t, pool, project.ID, 1)
	nodes, err := nodeRepo.GetByTemplateOrdered(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("template nodes = %d, want 1", len(nodes))
	}
	terminalNode := nodes[0]

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
			taskRecord.CurrentFlowNodeID = &terminalNode.ID
			taskRecord.WorkStatus = "review"
			if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
				t.Fatalf("taskRepo.Update: %v", err)
			}
			if _, err := execRepo.Create(ctx, repo.FlowNodeExecution{
				TaskID:      taskID,
				FlowNodeID:  terminalNode.ID,
				VisitNumber: 1,
				Status:      "completed",
			}); err != nil {
				t.Fatalf("Create flow execution: %v", err)
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
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	assignee := testutil.MakeAgent(t, pool, orgID)
	template := testutil.MakeFlowTemplate(t, pool, project.ID, 1)
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

func TestIntegrationTaskUpdatePublishesStatusChangedDomainEvent(t *testing.T) {
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	actor := testutil.MakeAgent(t, pool, orgID)
	template := testutil.MakeFlowTemplate(t, pool, project.ID, 1)
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
		backfilledExecutionID      uuid.UUID
		foundBackfilledCompleted   bool
		foundBackfilledSession     bool
		foundNextActiveWithSession bool
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
			backfilledExecutionID = execution.ID
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
		WHERE organization_id = $1
		  AND scope_type = 'project_task'
		  AND scope_id = $2
		  AND metadata->>'flow_node_execution_id' = $3
	`, orgID, task.ID, backfilledExecutionID.String()).Scan(&sessionCount); err != nil {
		t.Fatalf("count chat_session rows for backfilled execution: %v", err)
	}
	if sessionCount < 1 {
		t.Fatalf("chat_session rows for backfilled execution = %d, want >= 1", sessionCount)
	}
}

func TestIntegrationFlowAdvanceTerminalPublishesStatusChangedDomainEvent(t *testing.T) {
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

	generation.NextNodeID = &internalReview.ID
	if _, err := nodeRepo.Update(ctx, generation); err != nil {
		t.Fatalf("link generation node: %v", err)
	}
	internalReview.NextNodeID = &humanReview.ID
	if _, err := nodeRepo.Update(ctx, internalReview); err != nil {
		t.Fatalf("link internal review node: %v", err)
	}

	template.StartNodeID = &generation.ID
	updated, err := templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("set review refinement start node: %v", err)
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

	workNode.NextNodeID = &internalReview.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("link work node: %v", err)
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

func firstArtifactPayload(t *testing.T, raw any) map[string]any {
	t.Helper()
	items := artifactPayloads(t, raw)
	return items[0]
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
