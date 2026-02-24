package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	agentdomain "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestCreateAgentValidationRequiresDisplayName(t *testing.T) {
	fake := &fakeAgentService{}
	handler := agentHandlers{service: fake}

	req := httptest.NewRequest(http.MethodPost, "/v1/agents", bytes.NewBufferString(`{"agent_class":"staff","agent_type":"worker"}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	}))
	rr := httptest.NewRecorder()

	handler.createAgent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if fake.createCalled {
		t.Fatal("service Create should not be called on validation failure")
	}
}

func TestCreateAgentValidationRequiresAgentClass(t *testing.T) {
	fake := &fakeAgentService{}
	handler := agentHandlers{service: fake}

	req := httptest.NewRequest(http.MethodPost, "/v1/agents", bytes.NewBufferString(`{"display_name":"Nora","agent_type":"worker"}`))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	}))
	rr := httptest.NewRecorder()

	handler.createAgent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if fake.createCalled {
		t.Fatal("service Create should not be called on validation failure")
	}
}

func TestMapAgentError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid transition",
			err:        agentdomain.ErrInvalidTransition,
			wantStatus: http.StatusConflict,
			wantCode:   "invalid_transition",
		},
		{
			name:       "invalid for temp",
			err:        agentdomain.ErrInvalidForTempAgent,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_for_temp_agent",
		},
		{
			name:       "temp limit",
			err:        agentdomain.ErrConcurrentTempLimitReached,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "temp_limit_reached",
		},
		{
			name:       "starter trio protected",
			err:        agentdomain.ErrStarterTrioProtected,
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
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
			gotStatus, gotCode, _ := mapAgentError(tc.err)
			if gotStatus != tc.wantStatus {
				t.Fatalf("status = %d, want %d", gotStatus, tc.wantStatus)
			}
			if gotCode != tc.wantCode {
				t.Fatalf("code = %q, want %q", gotCode, tc.wantCode)
			}
		})
	}
}

type fakeAgentService struct {
	createCalled bool
}

func (f *fakeAgentService) Create(context.Context, agentdomain.CreateAgentRequest) (*repo.Agent, error) {
	f.createCalled = true
	return nil, nil
}

func (f *fakeAgentService) Get(context.Context, uuid.UUID, uuid.UUID) (*repo.Agent, error) {
	return nil, nil
}

func (f *fakeAgentService) List(context.Context, uuid.UUID, agentdomain.AgentFilter) ([]*repo.Agent, error) {
	return nil, nil
}

func (f *fakeAgentService) Update(context.Context, uuid.UUID, uuid.UUID, agentdomain.UpdateAgentRequest) (*repo.Agent, error) {
	return nil, nil
}

func (f *fakeAgentService) Pause(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeAgentService) Unpause(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeAgentService) Retire(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeAgentService) Cancel(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeAgentService) CreateTemp(context.Context, uuid.UUID, agentdomain.CreateTempAgentRequest) (*repo.Agent, error) {
	f.createCalled = true
	return nil, nil
}

func (f *fakeAgentService) ExpireTemp(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

func (f *fakeAgentService) PromoteTemp(context.Context, uuid.UUID, uuid.UUID, agentdomain.PromoteTempRequest) (*repo.Agent, error) {
	return nil, nil
}

func (f *fakeAgentService) EnforceMaxConcurrentTemps(context.Context, uuid.UUID) error {
	return nil
}

func (f *fakeAgentService) RetireExpiredTemps(context.Context) error {
	return nil
}

func mustDecodeResponse[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return out
}
