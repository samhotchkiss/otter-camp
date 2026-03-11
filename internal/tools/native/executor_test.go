package native

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestResolveDataDirDefaultsToHomeOtterData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OTTERCAMP_DATA_DIR", "")

	got := resolveDataDir("")
	want := filepath.Join(home, "otter-data")
	if got != want {
		t.Fatalf("resolveDataDir default = %q, want %q", got, want)
	}
}

func TestResolveDataDirUsesEnvOverride(t *testing.T) {
	home := t.TempDir()
	override := filepath.Join(home, "custom-data")
	t.Setenv("HOME", home)
	t.Setenv("OTTERCAMP_DATA_DIR", override)

	got := resolveDataDir("")
	if got != override {
		t.Fatalf("resolveDataDir env override = %q, want %q", got, override)
	}
}

func TestWorkspaceForContextUsesGeneralWorkspaceWithoutOrgSlug(t *testing.T) {
	dataDir := t.TempDir()
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("resolve data dir symlink: %v", err)
	}
	orgID := uuid.New()

	exec := NewExecutor(ExecutorOptions{DataDir: dataDir})
	exec.organizations = &stubOrgRepo{
		byID: map[uuid.UUID]repo.Organization{
			orgID: {ID: orgID, Slug: "acme"},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
	})

	wd, _, err := exec.workspaceForContext(ctx)
	if err != nil {
		t.Fatalf("workspaceForContext: %v", err)
	}

	want := filepath.Join(resolvedDataDir, "workspaces", "general")
	if wd.Root() != want {
		t.Fatalf("workspace root = %q, want %q", wd.Root(), want)
	}
}

func TestWorkspaceForContextUsesProjectSlugWorkspaceWithoutOrgSlug(t *testing.T) {
	dataDir := t.TempDir()
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("resolve data dir symlink: %v", err)
	}
	orgID := uuid.New()
	projectID := uuid.New()
	sessionID := uuid.New()

	exec := NewExecutor(ExecutorOptions{DataDir: dataDir})
	exec.organizations = &stubOrgRepo{
		byID: map[uuid.UUID]repo.Organization{
			orgID: {ID: orgID, Slug: "acme"},
		},
	}
	exec.projects = &stubProjectRepo{
		byID: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, OrganizationID: orgID, Slug: "site-redesign"},
		},
	}
	exec.chatSessions = &stubChatSessionRepo{
		byID: map[uuid.UUID]repo.ChatSession{
			sessionID: {ID: sessionID, OrganizationID: orgID, ScopeType: "project", ScopeID: projectID},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
	})

	wd, _, err := exec.workspaceForContext(ctx)
	if err != nil {
		t.Fatalf("workspaceForContext: %v", err)
	}

	want := filepath.Join(resolvedDataDir, "workspaces", "site-redesign")
	if wd.Root() != want {
		t.Fatalf("workspace root = %q, want %q", wd.Root(), want)
	}
}

func TestWorkspaceForContextTaskScopeSharesProjectWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("resolve data dir symlink: %v", err)
	}
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	projectSessionID := uuid.New()
	taskSessionID := uuid.New()

	exec := NewExecutor(ExecutorOptions{DataDir: dataDir})
	exec.organizations = &stubOrgRepo{
		byID: map[uuid.UUID]repo.Organization{
			orgID: {ID: orgID, Slug: "acme"},
		},
	}
	exec.projects = &stubProjectRepo{
		byID: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, OrganizationID: orgID, Slug: "site-redesign"},
		},
	}
	exec.tasks = &stubTaskRepo{
		byID: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, OrganizationID: orgID, ProjectID: projectID},
		},
	}
	exec.chatSessions = &stubChatSessionRepo{
		byID: map[uuid.UUID]repo.ChatSession{
			projectSessionID: {ID: projectSessionID, OrganizationID: orgID, ScopeType: "project", ScopeID: projectID},
			taskSessionID:    {ID: taskSessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID},
		},
	}

	projectCtx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &projectSessionID,
	})
	taskCtx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &taskSessionID,
	})

	projectWD, _, err := exec.workspaceForContext(projectCtx)
	if err != nil {
		t.Fatalf("workspaceForContext project scope: %v", err)
	}
	taskWD, _, err := exec.workspaceForContext(taskCtx)
	if err != nil {
		t.Fatalf("workspaceForContext task scope: %v", err)
	}

	want := filepath.Join(resolvedDataDir, "workspaces", "site-redesign")
	if projectWD.Root() != want {
		t.Fatalf("project workspace root = %q, want %q", projectWD.Root(), want)
	}
	if taskWD.Root() != want {
		t.Fatalf("task workspace root = %q, want %q", taskWD.Root(), want)
	}
	if len(exec.workspaces) != 1 {
		t.Fatalf("workspace cache entries = %d, want 1", len(exec.workspaces))
	}
}

func TestWorkspaceForContextTaskScopeRequiresTaskBinding(t *testing.T) {
	dataDir := t.TempDir()
	orgID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	exec := NewExecutor(ExecutorOptions{DataDir: dataDir})
	exec.chatSessions = &stubChatSessionRepo{
		byID: map[uuid.UUID]repo.ChatSession{
			sessionID: {ID: sessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
	})

	_, _, err := exec.workspaceForContext(ctx)
	if err == nil {
		t.Fatal("workspaceForContext error = nil, want task binding invariant")
	}
	if !strings.Contains(err.Error(), "internal invariant: task-scoped execution is missing bound task context") {
		t.Fatalf("workspaceForContext error = %v, want task binding invariant", err)
	}
}

func TestWorkspaceForContextProjectScopeClearsStaleCrossProjectTaskBinding(t *testing.T) {
	dataDir := t.TempDir()
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("resolve data dir symlink: %v", err)
	}
	orgID := uuid.New()
	projectID := uuid.New()
	staleProjectID := uuid.New()
	staleTaskID := uuid.New()
	sessionID := uuid.New()

	exec := NewExecutor(ExecutorOptions{DataDir: dataDir})
	exec.organizations = &stubOrgRepo{
		byID: map[uuid.UUID]repo.Organization{
			orgID: {ID: orgID, Slug: "acme"},
		},
	}
	exec.projects = &stubProjectRepo{
		byID: map[uuid.UUID]repo.Project{
			projectID:      {ID: projectID, OrganizationID: orgID, Slug: "kickoff-project"},
			staleProjectID: {ID: staleProjectID, OrganizationID: orgID, Slug: "stale-project"},
		},
	}
	exec.tasks = &stubTaskRepo{
		byID: map[uuid.UUID]repo.ProjectTask{
			staleTaskID: {ID: staleTaskID, OrganizationID: orgID, ProjectID: staleProjectID},
		},
	}
	exec.chatSessions = &stubChatSessionRepo{
		byID: map[uuid.UUID]repo.ChatSession{
			sessionID: {ID: sessionID, OrganizationID: orgID, ScopeType: "project", ScopeID: projectID},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		ProjectID:      &staleProjectID,
		TaskID:         &staleTaskID,
	})

	wd, scope, err := exec.workspaceForContext(ctx)
	if err != nil {
		t.Fatalf("workspaceForContext: %v", err)
	}

	want := filepath.Join(resolvedDataDir, "workspaces", "kickoff-project")
	if wd.Root() != want {
		t.Fatalf("workspace root = %q, want %q", wd.Root(), want)
	}
	if scope.projectID == nil || *scope.projectID != projectID {
		t.Fatalf("scope.projectID = %v, want %s", scope.projectID, projectID)
	}
	if scope.taskID != nil {
		t.Fatalf("scope.taskID = %v, want nil after clearing stale cross-project task binding", scope.taskID)
	}
}

type stubOrgRepo struct {
	byID map[uuid.UUID]repo.Organization
}

func (s *stubOrgRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Organization, error) {
	org, ok := s.byID[id]
	if !ok {
		return repo.Organization{}, repo.ErrNotFound
	}
	return org, nil
}

type stubProjectRepo struct {
	byID map[uuid.UUID]repo.Project
}

func (s *stubProjectRepo) Create(context.Context, repo.Project) (repo.Project, error) {
	return repo.Project{}, repo.ErrNotFound
}

func (s *stubProjectRepo) List(context.Context, uuid.UUID) ([]repo.Project, error) {
	return nil, nil
}

func (s *stubProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	project, ok := s.byID[id]
	if !ok {
		return repo.Project{}, repo.ErrNotFound
	}
	return project, nil
}

func (s *stubProjectRepo) GetBySlug(_ context.Context, organizationID uuid.UUID, slug string) (repo.Project, error) {
	for _, project := range s.byID {
		if project.OrganizationID == organizationID && project.Slug == slug {
			return project, nil
		}
	}
	return repo.Project{}, repo.ErrNotFound
}

func (s *stubProjectRepo) Update(context.Context, repo.Project) (repo.Project, error) {
	return repo.Project{}, repo.ErrNotFound
}

func (s *stubProjectRepo) Archive(context.Context, uuid.UUID) error {
	return repo.ErrNotFound
}

type stubTaskRepo struct {
	byID map[uuid.UUID]repo.ProjectTask
}

func (s *stubTaskRepo) Create(context.Context, repo.ProjectTask) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, repo.ErrNotFound
}

func (s *stubTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	task, ok := s.byID[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	return task, nil
}

func (s *stubTaskRepo) ListByProject(context.Context, uuid.UUID, ...string) ([]repo.ProjectTask, error) {
	return nil, nil
}

func (s *stubTaskRepo) SetFlowNode(context.Context, uuid.UUID, *uuid.UUID) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, repo.ErrNotFound
}

func (s *stubTaskRepo) UpdateStatus(context.Context, uuid.UUID, string) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, repo.ErrNotFound
}

func (s *stubTaskRepo) UpdateMetadata(_ context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error) {
	task, ok := s.byID[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	task.Metadata = append(json.RawMessage(nil), metadata...)
	s.byID[id] = task
	return task, nil
}

func (s *stubTaskRepo) Update(context.Context, repo.ProjectTask) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, repo.ErrNotFound
}

type stubChatSessionRepo struct {
	byID map[uuid.UUID]repo.ChatSession
}

func (s *stubChatSessionRepo) Create(context.Context, repo.ChatSession) (repo.ChatSession, error) {
	return repo.ChatSession{}, repo.ErrNotFound
}

func (s *stubChatSessionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
	session, ok := s.byID[id]
	if !ok {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	return session, nil
}

func (s *stubChatSessionRepo) ListByOrg(context.Context, uuid.UUID) ([]repo.ChatSession, error) {
	return nil, nil
}

func (s *stubChatSessionRepo) Close(context.Context, uuid.UUID) (repo.ChatSession, error) {
	return repo.ChatSession{}, repo.ErrNotFound
}
