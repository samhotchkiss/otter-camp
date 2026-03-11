package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

func TestTaskRoutesRegistered(t *testing.T) {
	registrar := NewTaskRouteRegistrar(nil, nil, nil, nil)
	router := chi.NewRouter()
	registrar.RegisterRoutes(router)

	routes := make(map[string]struct{})
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[strings.ToUpper(method)+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	required := []string{
		"GET /projects/{id}/tasks",
		"POST /projects/{id}/tasks",
		"GET /tasks/{id}",
		"PATCH /tasks/{id}",
		"POST /tasks/{id}/queue",
		"POST /tasks/{id}/cancel",
		"POST /tasks/{id}/advance-flow",
		"POST /tasks/{id}/reject-flow",
		"POST /tasks/{id}/review-decision",
		"GET /tasks/{id}/flow",
		"GET /tasks/{id}/subtasks",
		"POST /tasks/{id}/subtasks",
		"PATCH /tasks/{id}/subtasks/{sid}",
		"POST /tasks/{id}/dependencies",
		"DELETE /tasks/{id}/dependencies/{did}",
		"GET /tasks/{id}/events",
		"GET /tasks/{id}/participants",
		"GET /inbox",
		"POST /inbox/{id}/act",
		"GET /projects/{id}/merge-queue",
		"GET /projects/{id}/remotes",
		"POST /projects/{id}/remotes",
		"PATCH /projects/{id}/remotes/{rid}",
		"DELETE /projects/{id}/remotes/{rid}",
		"GET /projects/{id}/environments",
		"GET /projects/{id}/files",
		"POST /projects/{id}/environments",
		"PATCH /projects/{id}/environments/{eid}",
		"POST /projects/{id}/deploy",
		"POST /projects/{id}/environments/{eid}/deploy",
		"POST /projects/{id}/rollback",
	}

	for _, key := range required {
		if _, ok := routes[key]; !ok {
			t.Fatalf("missing route %q", key)
		}
	}
}

func TestReviewDecisionInvalidDecisionReturns422(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()
	principal := middleware.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: "member"}

	h := taskHandlers{}
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/"+taskID.String()+"/review-decision", bytes.NewBufferString(`{"decision":"invalid"}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), principal))
	req = withRouteParams(req, map[string]string{"id": taskID.String()})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.reviewDecision(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
}

func TestMapTaskErrorProjectPausedReturnsConflict(t *testing.T) {
	status, code, message := mapTaskError(projectpause.NewError("operator pause"))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if code != "project_paused" {
		t.Fatalf("code = %q, want %q", code, "project_paused")
	}
	if !strings.Contains(message, "project is paused") {
		t.Fatalf("message = %q, want paused message", message)
	}
}

func TestPatchTaskRejectsForbiddenStateFields(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()

	fakeTasks := &fakeProjectTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      uuid.New(),
				Title:          "task title",
				WorkStatus:     "draft",
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}
	h := taskHandlers{tasks: fakeTasks}

	cases := []string{
		`{"work_status":"done"}`,
		`{"flow_template_id":"` + uuid.NewString() + `"}`,
		`{"current_flow_node_id":"` + uuid.NewString() + `"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPatch, "/v1/tasks/"+taskID.String(), bytes.NewBufferString(body))
		req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: "member"}))
		req = withRouteParams(req, map[string]string{"id": taskID.String()})
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		h.patchTask(rr, req)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body=%s status = %d, want %d body=%s", body, rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
		}
	}
}

func TestPatchTaskPriorityUpdated(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()

	fakeTasks := &fakeProjectTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      uuid.New(),
				Title:          "priority task",
				WorkStatus:     "draft",
				Priority:       0,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}
	h := taskHandlers{tasks: fakeTasks}

	req := httptest.NewRequest(http.MethodPatch, "/v1/tasks/"+taskID.String(), bytes.NewBufferString(`{"priority":3}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: "member"}))
	req = withRouteParams(req, map[string]string{"id": taskID.String()})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.patchTask(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if fakeTasks.items[taskID].Priority != 3 {
		t.Fatalf("stored priority = %d, want 3", fakeTasks.items[taskID].Priority)
	}
	var payload struct {
		Data struct {
			Priority int `json:"priority"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if payload.Data.Priority != 3 {
		t.Fatalf("response priority = %d, want 3 body=%s", payload.Data.Priority, rr.Body.String())
	}
}

func TestPatchTaskPriorityOutOfRangeReturns422(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()

	fakeTasks := &fakeProjectTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      uuid.New(),
				Title:          "priority validation",
				WorkStatus:     "draft",
				Priority:       0,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}
	h := taskHandlers{tasks: fakeTasks}

	req := httptest.NewRequest(http.MethodPatch, "/v1/tasks/"+taskID.String(), bytes.NewBufferString(`{"priority":9}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: "member"}))
	req = withRouteParams(req, map[string]string{"id": taskID.String()})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.patchTask(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
	if fakeTasks.items[taskID].Priority != 0 {
		t.Fatalf("stored priority = %d, want 0", fakeTasks.items[taskID].Priority)
	}
}

func TestReviewDecisionNonTargetUserReturns403(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()
	requestUserID := uuid.New()
	otherUserID := uuid.New()

	fakeTasks := &fakeProjectTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      uuid.New(),
				Title:          "review task",
				WorkStatus:     "review",
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}
	fakeService := &fakeTaskService{}
	h := taskHandlers{
		tasks:       fakeTasks,
		taskService: fakeService,
		findPendingReviewInbox: func(context.Context, uuid.UUID) (*repo.InboxItem, error) {
			return &repo.InboxItem{ID: uuid.New(), TargetUserID: &otherUserID}, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/"+taskID.String()+"/review-decision", bytes.NewBufferString(`{"decision":"approve"}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: requestUserID, OrganizationID: orgID, Role: "member"}))
	req = withRouteParams(req, map[string]string{"id": taskID.String()})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.reviewDecision(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
	if fakeService.transitionCalls != 0 {
		t.Fatalf("transition calls = %d, want 0", fakeService.transitionCalls)
	}
}

func TestReviewDecisionApproveUsesFlowAdvanceNotRawStatusTransition(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()

	fakeTasks := &fakeProjectTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         uuid.New(),
				Title:             "review task",
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
				CreatedAt:         time.Now().UTC(),
				UpdatedAt:         time.Now().UTC(),
				Metadata:          json.RawMessage(`{}`),
			},
		},
	}
	fakeService := &fakeTaskService{}
	fakeFlow := &fakeTaskFlowService{
		advanceFlowFn: func(_ context.Context, gotTaskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
			if gotTaskID != taskID {
				t.Fatalf("advance task id = %s, want %s", gotTaskID, taskID)
			}
			if actor.Type != "human_user" || actor.ID != userID {
				t.Fatalf("advance actor = %+v, want human_user %s", actor, userID)
			}
			return &repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, Status: "completed"}, nil
		},
	}
	h := taskHandlers{
		tasks:       fakeTasks,
		taskService: fakeService,
		flowService: fakeFlow,
		findPendingReviewInbox: func(context.Context, uuid.UUID) (*repo.InboxItem, error) {
			return nil, repo.ErrNotFound
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/"+taskID.String()+"/review-decision", bytes.NewBufferString(`{"decision":"approve"}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: userID, OrganizationID: orgID, Role: "member"}))
	req = withRouteParams(req, map[string]string{"id": taskID.String()})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.reviewDecision(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if fakeFlow.advanceCalls != 1 {
		t.Fatalf("advance calls = %d, want 1", fakeFlow.advanceCalls)
	}
	if fakeFlow.rejectCalls != 0 {
		t.Fatalf("reject calls = %d, want 0", fakeFlow.rejectCalls)
	}
	if fakeService.transitionCalls != 0 {
		t.Fatalf("transition calls = %d, want 0", fakeService.transitionCalls)
	}
}

func TestReviewDecisionRejectUsesFlowRejectNotRawStatusTransition(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()

	fakeTasks := &fakeProjectTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         uuid.New(),
				Title:             "review task",
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
				CreatedAt:         time.Now().UTC(),
				UpdatedAt:         time.Now().UTC(),
				Metadata:          json.RawMessage(`{}`),
			},
		},
	}
	fakeService := &fakeTaskService{}
	fakeFlow := &fakeTaskFlowService{
		rejectFlowFn: func(_ context.Context, gotTaskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
			if gotTaskID != taskID {
				t.Fatalf("reject task id = %s, want %s", gotTaskID, taskID)
			}
			if actor.Type != "human_user" || actor.ID != userID {
				t.Fatalf("reject actor = %+v, want human_user %s", actor, userID)
			}
			return &repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, Status: "completed"}, nil
		},
	}
	h := taskHandlers{
		tasks:       fakeTasks,
		taskService: fakeService,
		flowService: fakeFlow,
		findPendingReviewInbox: func(context.Context, uuid.UUID) (*repo.InboxItem, error) {
			return nil, repo.ErrNotFound
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/"+taskID.String()+"/review-decision", bytes.NewBufferString(`{"decision":"reject"}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: userID, OrganizationID: orgID, Role: "member"}))
	req = withRouteParams(req, map[string]string{"id": taskID.String()})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.reviewDecision(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if fakeFlow.rejectCalls != 1 {
		t.Fatalf("reject calls = %d, want 1", fakeFlow.rejectCalls)
	}
	if fakeFlow.advanceCalls != 0 {
		t.Fatalf("advance calls = %d, want 0", fakeFlow.advanceCalls)
	}
	if fakeService.transitionCalls != 0 {
		t.Fatalf("transition calls = %d, want 0", fakeService.transitionCalls)
	}
}

func TestListProjectFilesReturnsTreeForConfiguredProject(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "cmd", "ottercamp"), 0o755); err != nil {
		t.Fatalf("mkdir tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("readme"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "cmd", "ottercamp", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	h := taskHandlers{
		projects: &fakeProjectRepoForTaskHandlers{
			items: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, OrganizationID: orgID, DisplayName: "Project Files"},
			},
		},
		environments: &fakeEnvironmentRepoForTaskHandlers{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {
					{
						ID:           uuid.New(),
						ProjectID:    projectID,
						Name:         "prod",
						IsActive:     true,
						RepoPath:     strPtrTaskHandler(repoRoot),
						DeliveryMode: "gated",
					},
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/"+projectID.String()+"/files", nil)
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "member",
	}))
	req = withRouteParams(req, map[string]string{"id": projectID.String()})
	rr := httptest.NewRecorder()

	h.listProjectFiles(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var payload struct {
		Data struct {
			Files []struct {
				Path  string `json:"path"`
				IsDir bool   `json:"is_dir"`
				Depth int    `json:"depth"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if len(payload.Data.Files) == 0 {
		t.Fatalf("files length = 0, want > 0 body=%s", rr.Body.String())
	}
	paths := make([]string, 0, len(payload.Data.Files))
	for _, item := range payload.Data.Files {
		paths = append(paths, item.Path)
	}
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, "README.md") {
		t.Fatalf("expected README.md in files; got paths:\n%s", joined)
	}
	if !strings.Contains(joined, "cmd/ottercamp/main.go") {
		t.Fatalf("expected cmd/ottercamp/main.go in files; got paths:\n%s", joined)
	}
}

func TestListProjectFilesReturnsEmptyForUnconfiguredProject(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	h := taskHandlers{
		projects: &fakeProjectRepoForTaskHandlers{
			items: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, OrganizationID: orgID, DisplayName: "No Repo Project"},
			},
		},
		environments: &fakeEnvironmentRepoForTaskHandlers{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {
					{
						ID:           uuid.New(),
						ProjectID:    projectID,
						Name:         "prod",
						IsActive:     true,
						DeliveryMode: "gated",
					},
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/"+projectID.String()+"/files", nil)
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "member",
	}))
	req = withRouteParams(req, map[string]string{"id": projectID.String()})
	rr := httptest.NewRecorder()

	h.listProjectFiles(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var payload struct {
		Data struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if len(payload.Data.Files) != 0 {
		t.Fatalf("files length = %d, want 0 body=%s", len(payload.Data.Files), rr.Body.String())
	}
}

type fakeProjectTaskRepo struct {
	items map[uuid.UUID]repo.ProjectTask
}

func (f *fakeProjectTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeProjectTaskRepo) Update(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	f.items[task.ID] = task
	return task, nil
}

func (f *fakeProjectTaskRepo) UpdateMetadata(_ context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	item.Metadata = append(json.RawMessage(nil), metadata...)
	f.items[id] = item
	return item, nil
}

type fakeProjectRepoForTaskHandlers struct {
	items map[uuid.UUID]repo.Project
}

func (f *fakeProjectRepoForTaskHandlers) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.Project{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeEnvironmentRepoForTaskHandlers struct {
	byProject map[uuid.UUID][]repo.ProjectEnvironment
	byID      map[uuid.UUID]repo.ProjectEnvironment
}

func (f *fakeEnvironmentRepoForTaskHandlers) Create(_ context.Context, environment repo.ProjectEnvironment) (repo.ProjectEnvironment, error) {
	if environment.ID == uuid.Nil {
		environment.ID = uuid.New()
	}
	if f.byID == nil {
		f.byID = map[uuid.UUID]repo.ProjectEnvironment{}
	}
	if f.byProject == nil {
		f.byProject = map[uuid.UUID][]repo.ProjectEnvironment{}
	}
	f.byID[environment.ID] = environment
	f.byProject[environment.ProjectID] = append(f.byProject[environment.ProjectID], environment)
	return environment, nil
}

func (f *fakeEnvironmentRepoForTaskHandlers) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectEnvironment, error) {
	if item, ok := f.byID[id]; ok {
		return item, nil
	}
	return repo.ProjectEnvironment{}, repo.ErrNotFound
}

func (f *fakeEnvironmentRepoForTaskHandlers) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error) {
	if rows, ok := f.byProject[projectID]; ok {
		return append([]repo.ProjectEnvironment(nil), rows...), nil
	}
	return []repo.ProjectEnvironment{}, nil
}

func (f *fakeEnvironmentRepoForTaskHandlers) Update(_ context.Context, environment repo.ProjectEnvironment) (repo.ProjectEnvironment, error) {
	if f.byID == nil {
		return repo.ProjectEnvironment{}, repo.ErrNotFound
	}
	if _, ok := f.byID[environment.ID]; !ok {
		return repo.ProjectEnvironment{}, repo.ErrNotFound
	}
	f.byID[environment.ID] = environment
	rows := f.byProject[environment.ProjectID]
	for i := range rows {
		if rows[i].ID == environment.ID {
			rows[i] = environment
		}
	}
	f.byProject[environment.ProjectID] = rows
	return environment, nil
}

func strPtrTaskHandler(value string) *string {
	copy := value
	return &copy
}

type fakeTaskService struct {
	transitionCalls int
	createTaskFn    func(context.Context, tasksvc.CreateTaskRequest) (*tasksvc.ProjectTask, error)
	transitionFn    func(context.Context, uuid.UUID, string, tasksvc.Actor) (*tasksvc.ProjectTask, error)
}

func (f *fakeTaskService) CreateTask(ctx context.Context, req tasksvc.CreateTaskRequest) (*tasksvc.ProjectTask, error) {
	if f.createTaskFn != nil {
		return f.createTaskFn(ctx, req)
	}
	return nil, nil
}

func (f *fakeTaskService) TransitionStatus(ctx context.Context, taskID uuid.UUID, status string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	f.transitionCalls++
	if f.transitionFn != nil {
		return f.transitionFn(ctx, taskID, status, actor)
	}
	return &tasksvc.ProjectTask{}, nil
}

func (f *fakeTaskService) TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, status string, actor tasksvc.Actor, _ map[string]any) (*tasksvc.ProjectTask, error) {
	return f.TransitionStatus(ctx, taskID, status, actor)
}

func (f *fakeTaskService) ResumeValidationBlockedTask(_ context.Context, _ uuid.UUID, _ tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	f.transitionCalls++
	return &tasksvc.ProjectTask{}, nil
}

func (f *fakeTaskService) RequestHumanApproval(context.Context, uuid.UUID) (*tasksvc.InboxItem, error) {
	return nil, nil
}

func (f *fakeTaskService) ApproveTask(context.Context, uuid.UUID, uuid.UUID) (*tasksvc.ProjectTask, error) {
	return nil, nil
}

func (f *fakeTaskService) RejectTask(context.Context, uuid.UUID, uuid.UUID, string) (*tasksvc.ProjectTask, error) {
	return nil, nil
}

func (f *fakeTaskService) MarkBlocked(context.Context, uuid.UUID, string, tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	return nil, nil
}

func (f *fakeTaskService) CreateInboxItem(context.Context, tasksvc.CreateInboxItemRequest) (*tasksvc.InboxItem, error) {
	return nil, nil
}

func (f *fakeTaskService) ActOnInboxItem(context.Context, uuid.UUID, uuid.UUID, string, json.RawMessage) error {
	return nil
}

func (f *fakeTaskService) EnqueueForMerge(context.Context, uuid.UUID) (*tasksvc.MergeQueueEntry, error) {
	return nil, nil
}

func (f *fakeTaskService) DequeueFromMerge(context.Context, uuid.UUID, string) (*tasksvc.MergeQueueEntry, error) {
	return nil, nil
}

type fakeTaskFlowService struct {
	startFlowFn   func(context.Context, uuid.UUID) (*repo.FlowNodeExecution, error)
	advanceFlowFn func(context.Context, uuid.UUID, flowsvc.Actor) (*repo.FlowNodeExecution, error)
	rejectFlowFn  func(context.Context, uuid.UUID, flowsvc.Actor) (*repo.FlowNodeExecution, error)
	advanceCalls  int
	rejectCalls   int
}

func (f *fakeTaskFlowService) StartFlow(ctx context.Context, taskID uuid.UUID) (*repo.FlowNodeExecution, error) {
	if f.startFlowFn != nil {
		return f.startFlowFn(ctx, taskID)
	}
	return &repo.FlowNodeExecution{}, nil
}

func (f *fakeTaskFlowService) EnsureActiveExecution(context.Context, uuid.UUID) (*repo.FlowNodeExecution, error) {
	return &repo.FlowNodeExecution{}, nil
}

func (f *fakeTaskFlowService) PauseAtReviewCheckpoint(context.Context, uuid.UUID, flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	return &repo.FlowNodeExecution{}, nil
}

func (f *fakeTaskFlowService) AdvanceFlow(ctx context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	f.advanceCalls++
	if f.advanceFlowFn != nil {
		return f.advanceFlowFn(ctx, taskID, actor)
	}
	return &repo.FlowNodeExecution{}, nil
}

func (f *fakeTaskFlowService) RejectFlowNode(ctx context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	f.rejectCalls++
	if f.rejectFlowFn != nil {
		return f.rejectFlowFn(ctx, taskID, actor)
	}
	return &repo.FlowNodeExecution{}, nil
}

func (f *fakeTaskFlowService) RecordNodeCommit(context.Context, uuid.UUID, string, string) (*repo.FlowNodeExecution, error) {
	return &repo.FlowNodeExecution{}, nil
}

func (f *fakeTaskFlowService) CreateSubtask(context.Context, uuid.UUID, string, *string, flowsvc.SubtaskAssignee) (*repo.ProjectSubtask, error) {
	return &repo.ProjectSubtask{}, nil
}

func (f *fakeTaskFlowService) UpdateSubtaskStatus(context.Context, uuid.UUID, string) (*repo.ProjectSubtask, error) {
	return &repo.ProjectSubtask{}, nil
}

func (f *fakeTaskFlowService) ListSubtasks(context.Context, uuid.UUID) ([]repo.ProjectSubtask, error) {
	return nil, nil
}

func (f *fakeTaskFlowService) AddDependency(context.Context, flowsvc.AddDependencyRequest) (*repo.ProjectTaskDependency, error) {
	return &repo.ProjectTaskDependency{}, nil
}

func (f *fakeTaskFlowService) RemoveDependency(context.Context, string, uuid.UUID, string, uuid.UUID) error {
	return nil
}

func (f *fakeTaskFlowService) OnTaskCancelled(context.Context, uuid.UUID) error {
	return nil
}

func (f *fakeTaskFlowService) SubscribeTaskCancellationHandler(*uuid.UUID) eventbus.Subscription {
	return eventbus.Subscription{}
}

type fakeDeliveryTaskCreator struct {
	createDeployTaskFn func(context.Context, uuid.UUID, string) (repo.ProjectTask, error)
}

func (f *fakeDeliveryTaskCreator) CreateDeployTask(ctx context.Context, environmentID uuid.UUID, commitSHA string) (repo.ProjectTask, error) {
	if f.createDeployTaskFn != nil {
		return f.createDeployTaskFn(ctx, environmentID, commitSHA)
	}
	return repo.ProjectTask{}, nil
}

type fakeDeployCompletionUpdater struct {
	completed []uuid.UUID
}

func (f *fakeDeployCompletionUpdater) OnDeployCompleted(_ context.Context, deployTaskID uuid.UUID) error {
	f.completed = append(f.completed, deployTaskID)
	return nil
}

func TestCreateTaskTestModeStartsFlowBeforeInProgressTransition(t *testing.T) {
	t.Setenv("OTTERCAMP_MODE", "test")

	projectID := uuid.New()
	taskID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()
	flowTemplateID := uuid.New()
	flowStarted := false
	callOrder := make([]string, 0, 3)
	taskRepo := &fakeProjectTaskRepo{items: map[uuid.UUID]repo.ProjectTask{}}

	taskSvc := &fakeTaskService{}
	taskSvc.createTaskFn = func(_ context.Context, req tasksvc.CreateTaskRequest) (*tasksvc.ProjectTask, error) {
		created := tasksvc.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      req.ProjectID,
			Title:          req.Title,
			WorkStatus:     "draft",
			FlowTemplateID: &flowTemplateID,
		}
		taskRepo.items[taskID] = repo.ProjectTask{
			ID:             created.ID,
			OrganizationID: created.OrganizationID,
			ProjectID:      created.ProjectID,
			Title:          created.Title,
			WorkStatus:     created.WorkStatus,
			FlowTemplateID: created.FlowTemplateID,
		}
		return &created, nil
	}
	taskSvc.transitionFn = func(_ context.Context, gotTaskID uuid.UUID, status string, _ tasksvc.Actor) (*tasksvc.ProjectTask, error) {
		if gotTaskID != taskID {
			t.Fatalf("task id = %s, want %s", gotTaskID, taskID)
		}
		callOrder = append(callOrder, status)
		switch status {
		case "queued":
			queued := tasksvc.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Test task",
				WorkStatus:     "queued",
				FlowTemplateID: &flowTemplateID,
			}
			taskRepo.items[taskID] = repo.ProjectTask{
				ID:             queued.ID,
				OrganizationID: queued.OrganizationID,
				ProjectID:      queued.ProjectID,
				Title:          queued.Title,
				WorkStatus:     queued.WorkStatus,
				FlowTemplateID: queued.FlowTemplateID,
			}
			return &queued, nil
		case "in_progress":
			if !flowStarted {
				return nil, tasksvc.ErrActiveFlowRequired
			}
			nodeID := uuid.New()
			inProgress := tasksvc.ProjectTask{
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         projectID,
				Title:             "Test task",
				WorkStatus:        "in_progress",
				FlowTemplateID:    &flowTemplateID,
				CurrentFlowNodeID: &nodeID,
			}
			taskRepo.items[taskID] = repo.ProjectTask{
				ID:                inProgress.ID,
				OrganizationID:    inProgress.OrganizationID,
				ProjectID:         inProgress.ProjectID,
				Title:             inProgress.Title,
				WorkStatus:        inProgress.WorkStatus,
				FlowTemplateID:    inProgress.FlowTemplateID,
				CurrentFlowNodeID: inProgress.CurrentFlowNodeID,
			}
			return &inProgress, nil
		default:
			t.Fatalf("unexpected status transition %q", status)
			return nil, nil
		}
	}

	flowSvc := &fakeTaskFlowService{
		startFlowFn: func(_ context.Context, gotTaskID uuid.UUID) (*repo.FlowNodeExecution, error) {
			if gotTaskID != taskID {
				t.Fatalf("StartFlow task id = %s, want %s", gotTaskID, taskID)
			}
			flowStarted = true
			callOrder = append(callOrder, "start_flow")
			return &repo.FlowNodeExecution{ID: uuid.New(), TaskID: taskID, FlowNodeID: uuid.New(), Status: "active"}, nil
		},
	}

	h := taskHandlers{
		taskService: taskSvc,
		flowService: flowSvc,
		tasks:       taskRepo,
		projects: &fakeProjectRepoForTaskHandlers{items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: orgID,
				DisplayName:    "Test project",
			},
		}},
	}

	body := bytes.NewBufferString(`{"title":"Test task","flow_template_id":"` + flowTemplateID.String() + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/"+projectID.String()+"/tasks", body)
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: userID, OrganizationID: orgID, Role: "member"}))
	req = withRouteParams(req, map[string]string{"id": projectID.String()})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.createTask(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
	}
	gotOrder := strings.Join(callOrder, ",")
	if gotOrder != "queued,start_flow,in_progress" {
		t.Fatalf("call order = %q, want %q", gotOrder, "queued,start_flow,in_progress")
	}
}

func TestCreateDeployTaskTestModeStartsFlowBeforeInProgressTransition(t *testing.T) {
	t.Setenv("OTTERCAMP_MODE", "test")

	environmentID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	flowStarted := false
	callOrder := make([]string, 0, 3)

	taskSvc := &fakeTaskService{}
	taskSvc.transitionFn = func(_ context.Context, gotTaskID uuid.UUID, status string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
		if gotTaskID != taskID {
			t.Fatalf("task id = %s, want %s", gotTaskID, taskID)
		}
		callOrder = append(callOrder, status)
		switch status {
		case "in_progress":
			if !flowStarted {
				return nil, tasksvc.ErrActiveFlowRequired
			}
			nodeID := uuid.New()
			return &tasksvc.ProjectTask{
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         projectID,
				Title:             "Deploy test task",
				WorkStatus:        "in_progress",
				FlowTemplateID:    &flowTemplateID,
				CurrentFlowNodeID: &nodeID,
			}, nil
		case "done":
			if !actor.AllowDoneBypass {
				t.Fatal("expected AllowDoneBypass in test-mode deploy completion")
			}
			return &tasksvc.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Deploy test task",
				WorkStatus:     "done",
				FlowTemplateID: &flowTemplateID,
			}, nil
		default:
			t.Fatalf("unexpected status transition %q", status)
			return nil, nil
		}
	}

	flowSvc := &fakeTaskFlowService{
		startFlowFn: func(_ context.Context, gotTaskID uuid.UUID) (*repo.FlowNodeExecution, error) {
			if gotTaskID != taskID {
				t.Fatalf("StartFlow task id = %s, want %s", gotTaskID, taskID)
			}
			flowStarted = true
			callOrder = append(callOrder, "start_flow")
			return &repo.FlowNodeExecution{ID: uuid.New(), TaskID: taskID, FlowNodeID: uuid.New(), Status: "active"}, nil
		},
	}

	deliverySvc := &fakeDeliveryTaskCreator{
		createDeployTaskFn: func(_ context.Context, gotEnvironmentID uuid.UUID, commitSHA string) (repo.ProjectTask, error) {
			if gotEnvironmentID != environmentID {
				t.Fatalf("environment id = %s, want %s", gotEnvironmentID, environmentID)
			}
			if commitSHA != "deadbeef" {
				t.Fatalf("commit sha = %q, want deadbeef", commitSHA)
			}
			return repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Deploy test task",
				WorkStatus:     "queued",
				FlowTemplateID: &flowTemplateID,
			}, nil
		},
	}

	envUpdater := &fakeDeployCompletionUpdater{}
	h := taskHandlers{
		taskService:     taskSvc,
		flowService:     flowSvc,
		deliveryService: deliverySvc,
		envUpdater:      envUpdater,
	}

	deployTask, err := h.createDeployTask(context.Background(), repo.ProjectEnvironment{ID: environmentID, ProjectID: projectID}, uuid.New(), "deadbeef", nil)
	if err != nil {
		t.Fatalf("createDeployTask: %v", err)
	}
	if deployTask.WorkStatus != "done" {
		t.Fatalf("deploy task work_status = %q, want done", deployTask.WorkStatus)
	}
	gotOrder := strings.Join(callOrder, ",")
	if gotOrder != "start_flow,in_progress,done" {
		t.Fatalf("call order = %q, want %q", gotOrder, "start_flow,in_progress,done")
	}
	if len(envUpdater.completed) != 1 || envUpdater.completed[0] != taskID {
		t.Fatalf("completed deploy callbacks = %v, want [%s]", envUpdater.completed, taskID)
	}
}

func (f *fakeTaskService) GetMergeQueueStatus(context.Context, uuid.UUID) ([]*tasksvc.MergeQueueEntry, error) {
	return nil, nil
}
