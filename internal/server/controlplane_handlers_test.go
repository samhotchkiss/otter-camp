package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

func TestControlPlaneRoutesRegistered(t *testing.T) {
	registrar := NewControlPlaneRouteRegistrar(ControlPlaneRouteOptions{})
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
		"GET /control/dashboard",
		"POST /control/runs",
		"GET /control/runs",
		"GET /control/runs/{id}",
		"GET /control/runs/{id}/steps",
		"GET /control/runs/{id}/steps/{step_id}/attempts",
		"GET /control/runs/{id}/events",
		"GET /control/runs/{id}/events/stream",
		"GET /control/runs/{id}/artifacts",
		"GET /control/runs/{id}/artifacts/{artifact_id}/download",
		"POST /control/runs/{id}/cancel",
		"POST /control/runs/{id}/retry",
		"GET /control/tool-executions",
		"GET /control/tool-executions/{id}",
		"GET /control/cost/summary",
		"GET /control/health",
	}

	for _, route := range required {
		if _, ok := routes[route]; !ok {
			t.Fatalf("missing route %q", route)
		}
	}
}

func TestCreateRunDuplicateIdempotencyReturns200AndHeader(t *testing.T) {
	orgID := uuid.New()
	runID := uuid.New()
	key := "idem-key"
	now := time.Now().UTC()

	runs := &fakeControlRunRepository{
		getByIdempotencyKeyFn: func(context.Context, uuid.UUID, string) (controlplane.Run, error) {
			return controlplane.Run{ID: runID, OrganizationID: orgID, Status: "created", TriggerType: "api", PrincipalType: "human_user", PrincipalID: uuid.New(), Metadata: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	svc := &fakeControlRunService{
		createRunFn: func(context.Context, controlplane.CreateRunInput) (controlplane.Run, error) {
			return controlplane.Run{ID: runID, OrganizationID: orgID, Status: "created", TriggerType: "api", PrincipalType: "human_user", PrincipalID: uuid.New(), Metadata: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	h := controlPlaneHandlers{runService: svc, runs: runs}

	req := newControlPlaneRequest(t, http.MethodPost, "/v1/control/runs", map[string]any{
		"trigger_type":    "api",
		"idempotency_key": key,
	}, orgID, nil)
	rr := httptest.NewRecorder()

	h.createRun(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if hit := rr.Header().Get("Idempotency-Key-Hit"); hit != "true" {
		t.Fatalf("Idempotency-Key-Hit = %q, want true", hit)
	}
	if got := controlJSONPathString(t, rr.Body.Bytes(), "data", "id"); got != runID.String() {
		t.Fatalf("data.id = %q, want %q body=%s", got, runID, rr.Body.String())
	}
}

func TestCreateRunPolicyDeniedReturns403(t *testing.T) {
	orgID := uuid.New()
	h := controlPlaneHandlers{
		runService: &fakeControlRunService{
			createRunFn: func(context.Context, controlplane.CreateRunInput) (controlplane.Run, error) {
				return controlplane.Run{}, fmt.Errorf("%w: denied", controlplane.ErrPolicyDenied)
			},
		},
	}

	req := newControlPlaneRequest(t, http.MethodPost, "/v1/control/runs", map[string]any{
		"trigger_type": "api",
	}, orgID, nil)
	rr := httptest.NewRecorder()

	h.createRun(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
	if got := errorCode(t, rr.Body.Bytes()); got != "run_policy_denied" {
		t.Fatalf("error.code = %q, want %q body=%s", got, "run_policy_denied", rr.Body.String())
	}
}

func TestListRunsUsesPrincipalOrgScope(t *testing.T) {
	orgA := uuid.New()
	orgB := uuid.New()
	now := time.Now().UTC()

	h := controlPlaneHandlers{
		runs: &fakeControlRunRepository{
			listFn: func(_ context.Context, filter controlplane.RunListFilter) ([]controlplane.Run, error) {
				if filter.OrganizationID != orgA {
					return nil, errors.New("wrong org scope")
				}
				return []controlplane.Run{{
					ID:             uuid.New(),
					OrganizationID: orgA,
					Status:         "created",
					TriggerType:    "api",
					PrincipalType:  "human_user",
					PrincipalID:    uuid.New(),
					Metadata:       json.RawMessage(`{}`),
					CreatedAt:      now,
					UpdatedAt:      now,
				}}, nil
			},
		},
	}

	req := newControlPlaneRequest(t, http.MethodGet, "/v1/control/runs", nil, orgA, nil)
	rr := httptest.NewRecorder()

	h.listRuns(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := controlJSONPathString(t, rr.Body.Bytes(), "data", "0", "organization_id"); got != orgA.String() {
		t.Fatalf("organization_id = %q, want %q body=%s", got, orgA, rr.Body.String())
	}
	if got := controlJSONPathString(t, rr.Body.Bytes(), "data", "0", "organization_id"); got == orgB.String() {
		t.Fatalf("unexpected org B row in response body=%s", rr.Body.String())
	}
}

func TestCancelRunTerminalStateReturns409(t *testing.T) {
	orgID := uuid.New()
	runID := uuid.New()
	now := time.Now().UTC()

	h := controlPlaneHandlers{
		runs: &fakeControlRunRepository{
			getFn: func(context.Context, uuid.UUID) (controlplane.Run, error) {
				return controlplane.Run{ID: runID, OrganizationID: orgID, Status: "completed", TriggerType: "api", PrincipalType: "human_user", PrincipalID: uuid.New(), Metadata: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}, nil
			},
		},
		runService: &fakeControlRunService{
			requestCancelFn: func(context.Context, uuid.UUID, controlplane.CancelRequestActor) error {
				return controlplane.ErrTerminalState
			},
		},
	}

	req := newControlPlaneRequest(t, http.MethodPost, "/v1/control/runs/"+runID.String()+"/cancel", map[string]any{}, orgID, map[string]string{"id": runID.String()})
	rr := httptest.NewRecorder()

	h.cancelRun(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusConflict, rr.Body.String())
	}
}

func TestDownloadRunArtifactRedirectsToPresignedURL(t *testing.T) {
	orgID := uuid.New()
	runID := uuid.New()
	artifactID := uuid.New()
	now := time.Now().UTC()
	presigned := "https://example.test/presigned"

	h := controlPlaneHandlers{
		runs: &fakeControlRunRepository{
			getFn: func(context.Context, uuid.UUID) (controlplane.Run, error) {
				return controlplane.Run{ID: runID, OrganizationID: orgID, Status: "in_progress", TriggerType: "api", PrincipalType: "human_user", PrincipalID: uuid.New(), Metadata: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}, nil
			},
		},
		runArtifacts: &fakeControlRunArtifactRepository{
			getFn: func(context.Context, uuid.UUID) (controlplane.RunArtifact, error) {
				return controlplane.RunArtifact{ID: artifactID, RunID: runID, StorageKey: "k", ArtifactType: "stdout", ContentType: "text/plain", ByteSize: 4096, CreatedAt: now}, nil
			},
		},
		urlSigner: fakeArtifactURLSigner{url: presigned},
	}

	req := newControlPlaneRequest(t, http.MethodGet, "/v1/control/runs/"+runID.String()+"/artifacts/"+artifactID.String()+"/download", nil, orgID, map[string]string{
		"id":          runID.String(),
		"artifact_id": artifactID.String(),
	})
	rr := httptest.NewRecorder()

	h.downloadRunArtifact(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusFound, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != presigned {
		t.Fatalf("Location = %q, want %q", got, presigned)
	}
}

func newControlPlaneRequest(t *testing.T, method, path string, payload any, orgID uuid.UUID, routeParams map[string]string) *http.Request {
	t.Helper()
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = encoded
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "admin",
	}))
	if routeParams != nil {
		req = withRouteParams(req, routeParams)
	}
	return req
}

type fakeControlRunService struct {
	createRunFn     func(context.Context, controlplane.CreateRunInput) (controlplane.Run, error)
	requestCancelFn func(context.Context, uuid.UUID, controlplane.CancelRequestActor) error
	createRetryFn   func(context.Context, uuid.UUID, string) (controlplane.RunAttempt, error)
	getRunFn        func(context.Context, uuid.UUID) (controlplane.Run, error)
}

func (f *fakeControlRunService) CreateRun(ctx context.Context, input controlplane.CreateRunInput) (controlplane.Run, error) {
	if f.createRunFn != nil {
		return f.createRunFn(ctx, input)
	}
	return controlplane.Run{}, nil
}

func (f *fakeControlRunService) RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy controlplane.CancelRequestActor) error {
	if f.requestCancelFn != nil {
		return f.requestCancelFn(ctx, runID, requestedBy)
	}
	return nil
}

func (f *fakeControlRunService) CreateRetryAttempt(ctx context.Context, runStepID uuid.UUID, trigger string) (controlplane.RunAttempt, error) {
	if f.createRetryFn != nil {
		return f.createRetryFn(ctx, runStepID, trigger)
	}
	return controlplane.RunAttempt{}, nil
}

func (f *fakeControlRunService) GetRun(ctx context.Context, runID uuid.UUID) (controlplane.Run, error) {
	if f.getRunFn != nil {
		return f.getRunFn(ctx, runID)
	}
	return controlplane.Run{}, controlplane.ErrNotFound
}

type fakeControlRunRepository struct {
	getFn                 func(context.Context, uuid.UUID) (controlplane.Run, error)
	getByIdempotencyKeyFn func(context.Context, uuid.UUID, string) (controlplane.Run, error)
	listFn                func(context.Context, controlplane.RunListFilter) ([]controlplane.Run, error)
}

func (f *fakeControlRunRepository) Get(ctx context.Context, id uuid.UUID) (controlplane.Run, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return controlplane.Run{}, controlplane.ErrNotFound
}

func (f *fakeControlRunRepository) GetByIdempotencyKey(ctx context.Context, organizationID uuid.UUID, key string) (controlplane.Run, error) {
	if f.getByIdempotencyKeyFn != nil {
		return f.getByIdempotencyKeyFn(ctx, organizationID, key)
	}
	return controlplane.Run{}, controlplane.ErrNotFound
}

func (f *fakeControlRunRepository) List(ctx context.Context, filter controlplane.RunListFilter) ([]controlplane.Run, error) {
	if f.listFn != nil {
		return f.listFn(ctx, filter)
	}
	return nil, nil
}

type fakeControlRunArtifactRepository struct {
	getFn  func(context.Context, uuid.UUID) (controlplane.RunArtifact, error)
	listFn func(context.Context, uuid.UUID) ([]controlplane.RunArtifact, error)
}

func (f *fakeControlRunArtifactRepository) Get(ctx context.Context, id uuid.UUID) (controlplane.RunArtifact, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return controlplane.RunArtifact{}, controlplane.ErrNotFound
}

func (f *fakeControlRunArtifactRepository) ListByRun(ctx context.Context, runID uuid.UUID) ([]controlplane.RunArtifact, error) {
	if f.listFn != nil {
		return f.listFn(ctx, runID)
	}
	return nil, nil
}

type fakeArtifactURLSigner struct {
	url string
}

func (f fakeArtifactURLSigner) PresignGetURL(context.Context, string, time.Duration) (string, error) {
	return f.url, nil
}

func controlJSONPathString(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("decode response: %v body=%s", err, string(body))
	}
	current := data
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			value, ok := typed[segment]
			if !ok {
				t.Fatalf("missing json path segment %q in %v", segment, path)
			}
			current = value
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				t.Fatalf("invalid array index %q in %v", segment, path)
			}
			current = typed[index]
		default:
			t.Fatalf("unexpected type at segment %q: %T", segment, current)
		}
	}
	value, ok := current.(string)
	if !ok {
		t.Fatalf("json path %v type=%T, want string", path, current)
	}
	return value
}
