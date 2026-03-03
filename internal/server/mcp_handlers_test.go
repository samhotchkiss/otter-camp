package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

func TestMCPTestHandlerCircuitStates(t *testing.T) {
	orgID := uuid.New()
	connID := uuid.New()

	t.Run("circuit open returns 200 error payload", func(t *testing.T) {
		h := mcpHandlers{service: &fakeMCPService{
			getConnectionFn: func(context.Context, uuid.UUID, uuid.UUID) (*mcp.MCPConnection, error) {
				return &mcp.MCPConnection{ID: connID, OrganizationID: orgID, Status: "active"}, nil
			},
			circuitStateFn: func(uuid.UUID) mcp.CBState { return mcp.CBOpen },
		}}

		req := newMCPRequest(t, http.MethodPost, "/v1/mcp/connections/"+connID.String()+"/test", nil, orgID, "member", map[string]string{"id": connID.String()})
		rr := httptest.NewRecorder()
		h.testConnection(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}
		if got := mcpTestStatus(t, rr.Body.Bytes()); got != "error" {
			t.Fatalf("status payload = %q, want %q", got, "error")
		}
	})

	t.Run("circuit closed returns 200 ok payload", func(t *testing.T) {
		h := mcpHandlers{service: &fakeMCPService{
			getConnectionFn: func(context.Context, uuid.UUID, uuid.UUID) (*mcp.MCPConnection, error) {
				return &mcp.MCPConnection{ID: connID, OrganizationID: orgID, Status: "active"}, nil
			},
			circuitStateFn: func(uuid.UUID) mcp.CBState { return mcp.CBClosed },
		}}

		req := newMCPRequest(t, http.MethodPost, "/v1/mcp/connections/"+connID.String()+"/test", nil, orgID, "member", map[string]string{"id": connID.String()})
		rr := httptest.NewRecorder()
		h.testConnection(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}
		if got := mcpTestStatus(t, rr.Body.Bytes()); got != "ok" {
			t.Fatalf("status payload = %q, want %q", got, "ok")
		}
	})
}

func TestMapMCPErrorMappings(t *testing.T) {
	status, code, _ := mapMCPError(mcp.ErrCircuitOpen)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if code != "circuit_open" {
		t.Fatalf("code = %q, want %q", code, "circuit_open")
	}
}

func TestValidateMCPTransportURL(t *testing.T) {
	t.Run("rejects private and localhost hosts", func(t *testing.T) {
		t.Setenv("OTTERCAMP_MODE", "prod")

		cases := []string{
			`{"url":"https://10.0.0.1/x"}`,
			`{"url":"https://172.16.0.1/x"}`,
			`{"url":"https://192.168.1.1/x"}`,
			`{"url":"https://169.254.169.254/latest/meta-data/"}`,
			`{"url":"https://127.0.0.1/x"}`,
			`{"url":"https://localhost/x"}`,
			`{"url":"https://[::1]/x"}`,
		}
		for _, raw := range cases {
			if err := validateMCPTransportURL(json.RawMessage(raw)); err == nil {
				t.Fatalf("validateMCPTransportURL(%s) err=nil, want rejection", raw)
			}
		}
	})

	t.Run("rejects http in non-test mode", func(t *testing.T) {
		t.Setenv("OTTERCAMP_MODE", "prod")
		if err := validateMCPTransportURL(json.RawMessage(`{"url":"http://example.com"}`)); err == nil {
			t.Fatalf("http URL should be rejected outside test mode")
		}
	})

	t.Run("allows https and test-mode http", func(t *testing.T) {
		t.Setenv("OTTERCAMP_MODE", "prod")
		if err := validateMCPTransportURL(json.RawMessage(`{"url":"https://example.com"}`)); err != nil {
			t.Fatalf("https URL rejected: %v", err)
		}

		t.Setenv("OTTERCAMP_MODE", "test")
		if err := validateMCPTransportURL(json.RawMessage(`{"url":"http://example.com"}`)); err != nil {
			t.Fatalf("test-mode http URL rejected: %v", err)
		}
	})
}

func TestMCPExecutionsStubReturnsTopLevelArrayData(t *testing.T) {
	connID := uuid.New()
	h := mcpHandlers{}

	req := newMCPRequest(t, http.MethodGet, "/v1/mcp/connections/"+connID.String()+"/executions", nil, uuid.New(), "member", map[string]string{"id": connID.String()})
	rr := httptest.NewRecorder()

	h.listExecutionsStub(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := rr.Header().Get("X-Stub"); got != "true" {
		t.Fatalf("X-Stub header = %q, want %q", got, "true")
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	data, ok := payload["data"].([]any)
	if !ok {
		t.Fatalf("data shape = %T, want []any body=%s", payload["data"], rr.Body.String())
	}
	if len(data) != 0 {
		t.Fatalf("data length = %d, want 0 body=%s", len(data), rr.Body.String())
	}
}

func newMCPRequest(t *testing.T, method, path string, payload any, orgID uuid.UUID, role string, urlParams map[string]string) *http.Request {
	t.Helper()

	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = encoded
	}

	req := httptest.NewRequest(method, path, bytesReader(body))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rctx := chi.NewRouteContext()
	for k, v := range urlParams {
		rctx.URLParams.Add(k, v)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           role,
	}))
	return req
}

func bytesReader(data []byte) *bytes.Reader {
	if len(data) == 0 {
		return bytes.NewReader(nil)
	}
	return bytes.NewReader(data)
}

func mcpTestStatus(t *testing.T, body []byte) string {
	t.Helper()

	var payload struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, string(body))
	}
	return payload.Data.Status
}

type fakeMCPService struct {
	getConnectionFn func(context.Context, uuid.UUID, uuid.UUID) (*mcp.MCPConnection, error)
	circuitStateFn  func(uuid.UUID) mcp.CBState
}

func (f *fakeMCPService) CreateConnection(context.Context, mcp.CreateConnectionRequest) (*mcp.MCPConnection, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) GetConnection(ctx context.Context, orgID, connID uuid.UUID) (*mcp.MCPConnection, error) {
	if f.getConnectionFn != nil {
		return f.getConnectionFn(ctx, orgID, connID)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) ListConnections(context.Context, uuid.UUID, mcp.MCPConnectionFilter) ([]*mcp.MCPConnection, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) UpdateConnection(context.Context, uuid.UUID, uuid.UUID, mcp.UpdateConnectionRequest) (*mcp.MCPConnection, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) DeleteConnection(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (f *fakeMCPService) RefreshCatalog(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}

func (f *fakeMCPService) EnableTool(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (f *fakeMCPService) DisableTool(context.Context, uuid.UUID, uuid.UUID) error {
	return errors.New("not implemented")
}

func (f *fakeMCPService) ResolveSecretBindings(context.Context, uuid.UUID) (map[string]string, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) Discover(context.Context, mcp.DiscoverRequest) ([]mcp.MCPToolCatalogEntry, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) CallTool(context.Context, mcp.CallToolRequest) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMCPService) RunHealthChecks(context.Context) error {
	return errors.New("not implemented")
}

func (f *fakeMCPService) StartHealthScheduler(context.Context) {}

func (f *fakeMCPService) CircuitState(connID uuid.UUID) mcp.CBState {
	if f.circuitStateFn != nil {
		return f.circuitStateFn(connID)
	}
	return mcp.CBClosed
}
