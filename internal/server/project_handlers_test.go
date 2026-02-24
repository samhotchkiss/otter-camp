package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestProjectRoutesRegistered(t *testing.T) {
	registrar := NewProjectRouteRegistrar(nil)
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
		"GET /projects",
		"POST /projects",
		"GET /projects/{id}",
		"PATCH /projects/{id}",
		"DELETE /projects/{id}",
		"GET /projects/{id}/flow-templates",
		"POST /projects/{id}/flow-templates",
		"GET /flow-templates",
		"GET /flow-templates/{id}",
		"PATCH /flow-templates/{id}",
		"GET /flow-templates/{id}/nodes",
		"POST /flow-templates/{id}/nodes",
		"PATCH /flow-templates/{id}/nodes/{node_id}",
		"DELETE /flow-templates/{id}/nodes/{node_id}",
		"GET /projects/{id}/schedules",
		"POST /projects/{id}/schedules",
		"PATCH /projects/{id}/schedules/{schedule_id}",
		"DELETE /projects/{id}/schedules/{schedule_id}",
		"POST /projects/{id}/schedules/{schedule_id}/enable",
		"POST /projects/{id}/schedules/{schedule_id}/disable",
	}

	for _, key := range required {
		if _, ok := routes[key]; !ok {
			t.Fatalf("missing route %q", key)
		}
	}
}

func TestCreateProjectValidationMissingSlugBeforeServiceCall(t *testing.T) {
	svc := &fakeProjectService{}
	handlers := projectHandlers{service: svc}

	req := newProjectRequest(t, http.MethodPost, "/v1/projects", map[string]any{
		"display_name":  "My Project",
		"delivery_mode": "gated",
	})
	rr := httptest.NewRecorder()

	handlers.createProject(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestMapProjectErrorMappings(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "slug taken",
			err:        projectsvc.ErrSlugTaken,
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
		},
		{
			name:       "project has active tasks",
			err:        projectsvc.ErrProjectHasActiveTasks,
			wantStatus: http.StatusConflict,
			wantCode:   "project_has_active_tasks",
		},
		{
			name:       "system template protected",
			err:        projectsvc.ErrSystemTemplateProtected,
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
		},
		{
			name:       "template in use",
			err:        projectsvc.ErrTemplateInUse,
			wantStatus: http.StatusConflict,
			wantCode:   "template_in_use",
		},
		{
			name:       "invalid cron expression",
			err:        fmt.Errorf("%w: expected exactly 5 fields, found 2", projectsvc.ErrInvalidCronExpression),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_cron_expression",
		},
		{
			name:       "agent not found",
			err:        projectsvc.ErrAgentNotFound,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_error",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mapped := mapProjectError(tc.err)
			if mapped.Status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", mapped.Status, tc.wantStatus)
			}
			if mapped.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", mapped.Code, tc.wantCode)
			}
		})
	}
}

func newProjectRequest(t *testing.T, method, path string, payload any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	}))
	return req
}

type fakeProjectService struct {
	createCalls int
}

func (f *fakeProjectService) Create(context.Context, projectsvc.CreateProjectRequest) (*projectsvc.Project, error) {
	f.createCalls++
	return nil, nil
}

func (f *fakeProjectService) Get(context.Context, uuid.UUID, uuid.UUID) (*projectsvc.Project, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) GetBySlug(context.Context, uuid.UUID, string) (*projectsvc.Project, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) List(context.Context, uuid.UUID, projectsvc.ProjectFilter) ([]*projectsvc.Project, error) {
	return nil, nil
}

func (f *fakeProjectService) Update(context.Context, uuid.UUID, uuid.UUID, projectsvc.UpdateProjectRequest) (*projectsvc.Project, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return repo.ErrNotFound
}

func (f *fakeProjectService) CreateFlowTemplate(context.Context, projectsvc.CreateFlowTemplateRequest) (*projectsvc.FlowTemplate, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) GetFlowTemplate(context.Context, uuid.UUID, uuid.UUID) (*projectsvc.FlowTemplate, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) ListFlowTemplates(context.Context, uuid.UUID, *uuid.UUID) ([]*projectsvc.FlowTemplate, error) {
	return nil, nil
}

func (f *fakeProjectService) UpdateFlowTemplate(context.Context, uuid.UUID, uuid.UUID, projectsvc.UpdateFlowTemplateRequest) (*projectsvc.FlowTemplate, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) AddFlowNode(context.Context, uuid.UUID, projectsvc.AddFlowNodeRequest) (*projectsvc.FlowNode, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) UpdateFlowNode(context.Context, uuid.UUID, projectsvc.UpdateFlowNodeRequest) (*projectsvc.FlowNode, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) RemoveFlowNode(context.Context, uuid.UUID) error {
	return repo.ErrNotFound
}

func (f *fakeProjectService) GetFlowNodes(context.Context, uuid.UUID) ([]*projectsvc.FlowNode, error) {
	return nil, nil
}

func (f *fakeProjectService) CreateSchedule(context.Context, projectsvc.CreateScheduleRequest) (*projectsvc.TaskSchedule, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) GetSchedule(context.Context, uuid.UUID) (*projectsvc.TaskSchedule, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) ListSchedules(context.Context, uuid.UUID) ([]*projectsvc.TaskSchedule, error) {
	return nil, nil
}

func (f *fakeProjectService) UpdateSchedule(context.Context, uuid.UUID, projectsvc.UpdateScheduleRequest) (*projectsvc.TaskSchedule, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) EnableSchedule(context.Context, uuid.UUID) (*projectsvc.TaskSchedule, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) DisableSchedule(context.Context, uuid.UUID) (*projectsvc.TaskSchedule, error) {
	return nil, repo.ErrNotFound
}

func (f *fakeProjectService) DeleteSchedule(context.Context, uuid.UUID) error {
	return repo.ErrNotFound
}
