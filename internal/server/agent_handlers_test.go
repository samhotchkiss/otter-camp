package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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
