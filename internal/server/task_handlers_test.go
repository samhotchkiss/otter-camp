package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
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

func TestPatchTaskUnknownFieldIgnoredNot400(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPatch, "/v1/tasks/"+taskID.String(), bytes.NewBufferString(`{"unknown_field":"value"}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: "member"}))
	req = withRouteParams(req, map[string]string{"id": taskID.String()})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.patchTask(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
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

type fakeTaskService struct {
	transitionCalls int
}

func (f *fakeTaskService) CreateTask(context.Context, tasksvc.CreateTaskRequest) (*tasksvc.ProjectTask, error) {
	return nil, nil
}

func (f *fakeTaskService) TransitionStatus(_ context.Context, _ uuid.UUID, _ string, _ tasksvc.Actor) (*tasksvc.ProjectTask, error) {
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

func (f *fakeTaskService) GetMergeQueueStatus(context.Context, uuid.UUID) ([]*tasksvc.MergeQueueEntry, error) {
	return nil, nil
}
