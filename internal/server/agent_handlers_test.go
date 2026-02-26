package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestCreateAgentValidationHappensBeforeServiceCall(t *testing.T) {
	t.Run("missing display_name", func(t *testing.T) {
		svc := &fakeAgentService{}
		h := newAgentHandlers(svc, nil)

		req := newAgentRequest(t, map[string]any{
			"agent_class": "staff",
			"agent_type":  "worker",
		})
		rr := httptest.NewRecorder()

		h.createAgent(rr, req)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
		}
		if svc.createCalls != 0 {
			t.Fatalf("Create called %d times, want 0", svc.createCalls)
		}
	})

	t.Run("missing agent_class", func(t *testing.T) {
		svc := &fakeAgentService{}
		h := newAgentHandlers(svc, nil)

		req := newAgentRequest(t, map[string]any{
			"display_name": "Builder",
			"agent_type":   "worker",
		})
		rr := httptest.NewRecorder()

		h.createAgent(rr, req)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
		}
		if svc.createCalls != 0 {
			t.Fatalf("Create called %d times, want 0", svc.createCalls)
		}
	})
}

func TestMapAgentErrorMappings(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid transition",
			err:        fmt.Errorf("%w: active->paused", agent.ErrInvalidTransition),
			wantStatus: http.StatusConflict,
			wantCode:   "invalid_transition",
		},
		{
			name:       "invalid for temp",
			err:        agent.ErrInvalidForTempAgent,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_for_temp_agent",
		},
		{
			name:       "temp limit reached",
			err:        agent.ErrConcurrentTempLimitReached,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "temp_limit_reached",
		},
		{
			name:       "starter trio protected",
			err:        agent.ErrStarterTrioProtected,
			wantStatus: http.StatusForbidden,
			wantCode:   "starter_trio_protected",
		},
		{
			name:       "not found",
			err:        repo.ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			status, code, _ := mapAgentError(tc.err)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

func TestAgentAssignmentRoutesRegistered(t *testing.T) {
	registrar := NewAgentRouteRegistrar(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := chi.NewRouter()
	registrar.RegisterRoutes(router)

	routes := make(map[string]struct{})
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := strings.ToUpper(method) + " " + route
		routes[key] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	required := []string{
		"POST /agents/{id}/project-assignments",
		"DELETE /agents/{id}/project-assignments/{pid}",
		"GET /agents/{id}/project-assignments",
		"GET /agents/{id}/skills",
		"POST /agents/{id}/skills",
		"DELETE /agents/{id}/skills/{sid}",
		"PATCH /agents/{id}/skills/{sid}",
	}
	for _, key := range required {
		if _, ok := routes[key]; !ok {
			t.Fatalf("missing route %q", key)
		}
	}
}

func TestCreateAgentProjectAssignmentRejectsInvalidRoleBeforeServiceCall(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	projectID := uuid.New()
	assignments := &fakeAssignmentService{}
	agents := &fakeAgentLookupRepo{
		getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
			return repo.Agent{ID: id, OrganizationID: orgID, LifecycleStatus: "active"}, nil
		},
	}
	projects := &fakeProjectLookupRepo{
		getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Project, error) {
			return repo.Project{ID: id, OrganizationID: orgID, Slug: "proj"}, nil
		},
	}
	h := newAgentHandlersWithAssignments(nil, nil, assignments, agents, projects, nil, nil, nil, nil)

	req := newAssignmentRequest(t, http.MethodPost, "/v1/agents/"+agentID.String()+"/project-assignments", map[string]any{
		"project_id": projectID.String(),
		"role":       "invalid",
	}, orgID, "admin")
	req = withRouteParams(req, map[string]string{"id": agentID.String()})
	rr := httptest.NewRecorder()

	h.createAgentProjectAssignment(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
	if assignments.assignCalls != 0 {
		t.Fatalf("assign calls = %d, want 0", assignments.assignCalls)
	}
}

func TestAttachAgentSkillPriorityValidation(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	skillID := uuid.New()

	agents := &fakeAgentLookupRepo{
		getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
			return repo.Agent{ID: id, OrganizationID: orgID, LifecycleStatus: "active"}, nil
		},
	}
	skills := &fakeSkillLookupRepo{
		getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Skill, error) {
			return repo.Skill{ID: id, OrganizationID: orgID, DisplayName: "Skill"}, nil
		},
	}
	attachments := &fakeSkillAttachmentRepo{
		getByAgentAndSkillFn: func(context.Context, uuid.UUID, uuid.UUID) (repo.AgentSkillAttachment, error) {
			return repo.AgentSkillAttachment{}, repo.ErrNotFound
		},
	}

	t.Run("priority below range rejected", func(t *testing.T) {
		assignments := &fakeAssignmentService{}
		h := newAgentHandlersWithAssignments(nil, nil, assignments, agents, nil, skills, nil, attachments, nil)

		req := newAssignmentRequest(t, http.MethodPost, "/v1/agents/"+agentID.String()+"/skills", map[string]any{
			"skill_id": skillID.String(),
			"priority": 0,
		}, orgID, "admin")
		req = withRouteParams(req, map[string]string{"id": agentID.String()})
		rr := httptest.NewRecorder()

		h.attachAgentSkill(rr, req)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
		}
		if assignments.attachCalls != 0 {
			t.Fatalf("attach calls = %d, want 0", assignments.attachCalls)
		}
	})

	t.Run("priority above range rejected", func(t *testing.T) {
		assignments := &fakeAssignmentService{}
		h := newAgentHandlersWithAssignments(nil, nil, assignments, agents, nil, skills, nil, attachments, nil)

		req := newAssignmentRequest(t, http.MethodPost, "/v1/agents/"+agentID.String()+"/skills", map[string]any{
			"skill_id": skillID.String(),
			"priority": 1001,
		}, orgID, "admin")
		req = withRouteParams(req, map[string]string{"id": agentID.String()})
		rr := httptest.NewRecorder()

		h.attachAgentSkill(rr, req)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
		}
		if assignments.attachCalls != 0 {
			t.Fatalf("attach calls = %d, want 0", assignments.attachCalls)
		}
	})

	t.Run("priority lower bound accepted", func(t *testing.T) {
		assignments := &fakeAssignmentService{
			attachSkillFn: func(_ context.Context, agentArg, skillArg uuid.UUID, priority int, _ agent.AssignmentActor) (*repo.AgentSkillAttachment, error) {
				return &repo.AgentSkillAttachment{
					ID:         uuid.New(),
					AgentID:    agentArg,
					SkillID:    skillArg,
					Priority:   priority,
					IsActive:   true,
					AttachedAt: timeNowUTC(),
				}, nil
			},
		}
		h := newAgentHandlersWithAssignments(nil, nil, assignments, agents, nil, skills, nil, attachments, nil)

		req := newAssignmentRequest(t, http.MethodPost, "/v1/agents/"+agentID.String()+"/skills", map[string]any{
			"skill_id": skillID.String(),
			"priority": 1,
		}, orgID, "admin")
		req = withRouteParams(req, map[string]string{"id": agentID.String()})
		rr := httptest.NewRecorder()

		h.attachAgentSkill(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
		}
		if assignments.attachCalls != 1 {
			t.Fatalf("attach calls = %d, want 1", assignments.attachCalls)
		}
	})

	t.Run("priority upper bound accepted", func(t *testing.T) {
		assignments := &fakeAssignmentService{
			attachSkillFn: func(_ context.Context, agentArg, skillArg uuid.UUID, priority int, _ agent.AssignmentActor) (*repo.AgentSkillAttachment, error) {
				return &repo.AgentSkillAttachment{
					ID:         uuid.New(),
					AgentID:    agentArg,
					SkillID:    skillArg,
					Priority:   priority,
					IsActive:   true,
					AttachedAt: timeNowUTC(),
				}, nil
			},
		}
		h := newAgentHandlersWithAssignments(nil, nil, assignments, agents, nil, skills, nil, attachments, nil)

		req := newAssignmentRequest(t, http.MethodPost, "/v1/agents/"+agentID.String()+"/skills", map[string]any{
			"skill_id": skillID.String(),
			"priority": 1000,
		}, orgID, "admin")
		req = withRouteParams(req, map[string]string{"id": agentID.String()})
		rr := httptest.NewRecorder()

		h.attachAgentSkill(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusCreated, rr.Body.String())
		}
		if assignments.attachCalls != 1 {
			t.Fatalf("attach calls = %d, want 1", assignments.attachCalls)
		}
	})
}

func TestCreateAgentProjectAssignmentCrossOrgRejectedBeforeServiceCall(t *testing.T) {
	principalOrgID := uuid.New()
	agentID := uuid.New()
	projectID := uuid.New()

	assignments := &fakeAssignmentService{}
	agents := &fakeAgentLookupRepo{
		getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
			return repo.Agent{
				ID:              id,
				OrganizationID:  principalOrgID,
				LifecycleStatus: "active",
			}, nil
		},
	}
	projects := &fakeProjectLookupRepo{
		getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Project, error) {
			return repo.Project{
				ID:             id,
				OrganizationID: uuid.New(),
			}, nil
		},
	}
	h := newAgentHandlersWithAssignments(nil, nil, assignments, agents, projects, nil, nil, nil, nil)

	req := newAssignmentRequest(t, http.MethodPost, "/v1/agents/"+agentID.String()+"/project-assignments", map[string]any{
		"project_id": projectID.String(),
		"role":       "pm",
	}, principalOrgID, "admin")
	req = withRouteParams(req, map[string]string{"id": agentID.String()})
	rr := httptest.NewRecorder()

	h.createAgentProjectAssignment(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
	if assignments.assignCalls != 0 {
		t.Fatalf("assign calls = %d, want 0", assignments.assignCalls)
	}
}

type fakeAgentService struct {
	createCalls int
	createFn    func(ctx context.Context, req agent.CreateAgentRequest) (*agent.Agent, error)
}

func (f *fakeAgentService) Create(ctx context.Context, req agent.CreateAgentRequest) (*agent.Agent, error) {
	f.createCalls++
	if f.createFn != nil {
		return f.createFn(ctx, req)
	}
	return &agent.Agent{
		ID:             uuid.New(),
		OrganizationID: req.OrganizationID,
		DisplayName:    req.DisplayName,
		AgentClass:     "staff",
		AgentType:      req.AgentType,
	}, nil
}

func (*fakeAgentService) Get(context.Context, uuid.UUID, uuid.UUID) (*agent.Agent, error) {
	return nil, errors.New("not implemented")
}

func (*fakeAgentService) List(context.Context, uuid.UUID, agent.AgentFilter) ([]*agent.Agent, error) {
	return nil, errors.New("not implemented")
}

func (*fakeAgentService) Update(context.Context, uuid.UUID, uuid.UUID, agent.UpdateAgentRequest) (*agent.Agent, error) {
	return nil, errors.New("not implemented")
}

func (*fakeAgentService) Pause(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*fakeAgentService) Unpause(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*fakeAgentService) Retire(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*fakeAgentService) Cancel(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*fakeAgentService) CreateTemp(context.Context, uuid.UUID, agent.CreateTempAgentRequest) (*agent.Agent, error) {
	return nil, errors.New("not implemented")
}

func (*fakeAgentService) ExpireTemp(context.Context, uuid.UUID, uuid.UUID, string) error {
	return errors.New("not implemented")
}

func (*fakeAgentService) PromoteTemp(context.Context, uuid.UUID, uuid.UUID, agent.PromoteTempRequest) (*agent.Agent, error) {
	return nil, errors.New("not implemented")
}

func (*fakeAgentService) EnforceMaxConcurrentTemps(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*fakeAgentService) RetireExpiredTemps(context.Context) error {
	return errors.New("not implemented")
}

type fakeAssignmentService struct {
	assignCalls         int
	removeCalls         int
	attachCalls         int
	detachCalls         int
	reorderCalls        int
	assignToProjectFn   func(ctx context.Context, agentID, projectID uuid.UUID, role string, assignedBy agent.AssignmentActor) (*repo.AgentProjectAssignment, error)
	removeFromProjectFn func(ctx context.Context, agentID, projectID uuid.UUID) (*repo.AgentProjectAssignment, error)
	attachSkillFn       func(ctx context.Context, agentID, skillID uuid.UUID, priority int, attachedBy agent.AssignmentActor) (*repo.AgentSkillAttachment, error)
	detachSkillFn       func(ctx context.Context, agentID, skillID uuid.UUID) (*repo.AgentSkillAttachment, error)
	reorderSkillsFn     func(ctx context.Context, agentID uuid.UUID, priorityMap map[uuid.UUID]int) error
}

func (f *fakeAssignmentService) AssignToProject(ctx context.Context, agentID, projectID uuid.UUID, role string, assignedBy agent.AssignmentActor) (*repo.AgentProjectAssignment, error) {
	f.assignCalls++
	if f.assignToProjectFn != nil {
		return f.assignToProjectFn(ctx, agentID, projectID, role, assignedBy)
	}
	return &repo.AgentProjectAssignment{
		ID:         uuid.New(),
		AgentID:    agentID,
		ProjectID:  projectID,
		Role:       role,
		IsActive:   true,
		AssignedAt: timeNowUTC(),
	}, nil
}

func (f *fakeAssignmentService) RemoveFromProject(ctx context.Context, agentID, projectID uuid.UUID) (*repo.AgentProjectAssignment, error) {
	f.removeCalls++
	if f.removeFromProjectFn != nil {
		return f.removeFromProjectFn(ctx, agentID, projectID)
	}
	return &repo.AgentProjectAssignment{
		ID:            uuid.New(),
		AgentID:       agentID,
		ProjectID:     projectID,
		Role:          "worker",
		IsActive:      false,
		AssignedAt:    timeNowUTC(),
		DeactivatedAt: pointerToTime(timeNowUTC()),
	}, nil
}

func (f *fakeAssignmentService) AttachSkill(ctx context.Context, agentID, skillID uuid.UUID, priority int, attachedBy agent.AssignmentActor) (*repo.AgentSkillAttachment, error) {
	f.attachCalls++
	if f.attachSkillFn != nil {
		return f.attachSkillFn(ctx, agentID, skillID, priority, attachedBy)
	}
	return &repo.AgentSkillAttachment{
		ID:         uuid.New(),
		AgentID:    agentID,
		SkillID:    skillID,
		Priority:   priority,
		IsActive:   true,
		AttachedAt: timeNowUTC(),
	}, nil
}

func (f *fakeAssignmentService) DetachSkill(ctx context.Context, agentID, skillID uuid.UUID) (*repo.AgentSkillAttachment, error) {
	f.detachCalls++
	if f.detachSkillFn != nil {
		return f.detachSkillFn(ctx, agentID, skillID)
	}
	return &repo.AgentSkillAttachment{
		ID:            uuid.New(),
		AgentID:       agentID,
		SkillID:       skillID,
		Priority:      100,
		IsActive:      false,
		AttachedAt:    timeNowUTC(),
		DeactivatedAt: pointerToTime(timeNowUTC()),
	}, nil
}

func (f *fakeAssignmentService) ReorderSkills(ctx context.Context, agentID uuid.UUID, priorityMap map[uuid.UUID]int) error {
	f.reorderCalls++
	if f.reorderSkillsFn != nil {
		return f.reorderSkillsFn(ctx, agentID, priorityMap)
	}
	return nil
}

type fakeAgentLookupRepo struct {
	getByIDFn func(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

func (f *fakeAgentLookupRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.Agent{}, repo.ErrNotFound
}

type fakeProjectLookupRepo struct {
	getByIDFn func(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

func (f *fakeProjectLookupRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.Project{}, repo.ErrNotFound
}

type fakeSkillLookupRepo struct {
	getByIDFn func(ctx context.Context, id uuid.UUID) (repo.Skill, error)
}

func (f *fakeSkillLookupRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Skill, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.Skill{}, repo.ErrNotFound
}

type fakeProjectAssignmentRepo struct {
	listByAgentFn func(ctx context.Context, agentID uuid.UUID) ([]repo.AgentProjectAssignment, error)
}

func (f *fakeProjectAssignmentRepo) ListByAgent(ctx context.Context, agentID uuid.UUID) ([]repo.AgentProjectAssignment, error) {
	if f.listByAgentFn != nil {
		return f.listByAgentFn(ctx, agentID)
	}
	return nil, nil
}

type fakeSkillAttachmentRepo struct {
	getByAgentAndSkillFn func(ctx context.Context, agentID, skillID uuid.UUID) (repo.AgentSkillAttachment, error)
	listByAgentFn        func(ctx context.Context, agentID uuid.UUID) ([]repo.AgentSkillAttachment, error)
}

func (f *fakeSkillAttachmentRepo) GetByAgentAndSkill(ctx context.Context, agentID, skillID uuid.UUID) (repo.AgentSkillAttachment, error) {
	if f.getByAgentAndSkillFn != nil {
		return f.getByAgentAndSkillFn(ctx, agentID, skillID)
	}
	return repo.AgentSkillAttachment{}, repo.ErrNotFound
}

func (f *fakeSkillAttachmentRepo) ListByAgent(ctx context.Context, agentID uuid.UUID) ([]repo.AgentSkillAttachment, error) {
	if f.listByAgentFn != nil {
		return f.listByAgentFn(ctx, agentID)
	}
	return nil, nil
}

func newAssignmentRequest(t *testing.T, method, url string, body map[string]any, organizationID uuid.UUID, role string) *http.Request {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(method, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: organizationID,
		Role:           role,
	}))
	return req
}

func withRouteParams(req *http.Request, params map[string]string) *http.Request {
	routeCtx := chi.NewRouteContext()
	for key, value := range params {
		routeCtx.URLParams.Add(key, value)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}

func pointerToTime(t time.Time) *time.Time {
	copied := t
	return &copied
}

func newAgentRequest(t *testing.T, body map[string]any) *http.Request {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/agents", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	}))
	return req
}
