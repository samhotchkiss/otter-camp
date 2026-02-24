package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type stubAgentService struct {
	createCalled bool
}

func (s *stubAgentService) Create(context.Context, agentsvc.CreateAgentRequest) (*agentsvc.Agent, error) {
	s.createCalled = true
	created := &agentsvc.Agent{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		DisplayName:    "created",
	}
	return created, nil
}

func (s *stubAgentService) Get(context.Context, uuid.UUID, uuid.UUID) (*agentsvc.Agent, error) {
	return nil, repo.ErrNotFound
}

func (s *stubAgentService) List(context.Context, uuid.UUID, agentsvc.AgentFilter) ([]*agentsvc.Agent, error) {
	return nil, nil
}

func (s *stubAgentService) Update(context.Context, uuid.UUID, uuid.UUID, agentsvc.UpdateAgentRequest) (*agentsvc.Agent, error) {
	return nil, nil
}

func (s *stubAgentService) Pause(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (s *stubAgentService) Unpause(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (s *stubAgentService) Retire(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (s *stubAgentService) Cancel(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (s *stubAgentService) CreateTemp(context.Context, uuid.UUID, agentsvc.CreateTempAgentRequest) (*agentsvc.Agent, error) {
	return nil, nil
}

func (s *stubAgentService) ExpireTemp(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

func (s *stubAgentService) PromoteTemp(context.Context, uuid.UUID, uuid.UUID, agentsvc.PromoteTempRequest) (*agentsvc.Agent, error) {
	return nil, nil
}

func (s *stubAgentService) EnforceMaxConcurrentTemps(context.Context, uuid.UUID) error {
	return nil
}

func (s *stubAgentService) RetireExpiredTemps(context.Context) error {
	return nil
}

func TestCreateAgentRequestValidationHappensBeforeServiceCall(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{
			name: "missing display name",
			body: `{"agent_class":"staff","agent_type":"worker"}`,
		},
		{
			name: "missing agent class",
			body: `{"display_name":"build helper","agent_type":"worker"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubAgentService{}
			handlers := &agentRouteRegistrar{agentService: svc}

			req := httptest.NewRequest(http.MethodPost, "/v1/agents", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			orgID := uuid.New()
			ctx := middleware.WithPrincipal(req.Context(), middleware.Principal{
				UserID:         uuid.New(),
				OrganizationID: orgID,
				Role:           "admin",
			})
			req = req.WithContext(api.WithOrganizationID(ctx, orgID))

			rec := httptest.NewRecorder()
			handlers.createAgent(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
			}
			if svc.createCalled {
				t.Fatal("service Create should not have been called")
			}
		})
	}
}

func TestMapAgentError(t *testing.T) {
	testCases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid transition",
			err:        agentsvc.ErrInvalidTransition,
			wantStatus: http.StatusConflict,
			wantCode:   "invalid_transition",
		},
		{
			name:       "invalid for temp",
			err:        agentsvc.ErrInvalidForTempAgent,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_for_temp_agent",
		},
		{
			name:       "temp limit reached",
			err:        agentsvc.ErrConcurrentTempLimitReached,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "temp_limit_reached",
		},
		{
			name:       "starter trio protected",
			err:        agentsvc.ErrStarterTrioProtected,
			wantStatus: http.StatusForbidden,
			wantCode:   "starter_trio_protected",
		},
		{
			name:       "not found",
			err:        repo.ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   api.ErrCodeResourceNotFound,
		},
		{
			name:       "wrapped invalid transition",
			err:        fmt.Errorf("wrapped: %w", agentsvc.ErrInvalidTransition),
			wantStatus: http.StatusConflict,
			wantCode:   "invalid_transition",
		},
	}

	for _, tc := range testCases {
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
