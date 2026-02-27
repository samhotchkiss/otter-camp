package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestTraceRoutesRegistered(t *testing.T) {
	registrar := NewTraceRouteRegistrar(nil)
	router := chi.NewRouter()
	registrar.RegisterRoutes(router)

	routes := make(map[string]struct{})
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	if _, ok := routes["GET /trace/spans"]; !ok {
		t.Fatalf("missing route %q", "GET /trace/spans")
	}
}

func TestListTraceSpansAppliesFiltersAndReturnsShape(t *testing.T) {
	orgID := uuid.New()
	runID := uuid.New()
	taskID := uuid.New()
	agentID := uuid.New()
	from := time.Date(2026, time.February, 20, 12, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	spanID := uuid.New()
	parentID := uuid.New()
	duration := 123

	fakeRepo := &fakeTraceSpanRepo{
		listByOrgFn: func(_ context.Context, gotOrgID uuid.UUID, filters repo.TraceSpanFilters) ([]repo.TraceSpan, error) {
			if gotOrgID != orgID {
				t.Fatalf("organization id = %s, want %s", gotOrgID, orgID)
			}
			if filters.RunID == nil || *filters.RunID != runID {
				t.Fatalf("run_id filter = %v, want %s", filters.RunID, runID)
			}
			if filters.TaskID == nil || *filters.TaskID != taskID {
				t.Fatalf("task_id filter = %v, want %s", filters.TaskID, taskID)
			}
			if filters.AgentID == nil || *filters.AgentID != agentID {
				t.Fatalf("agent_id filter = %v, want %s", filters.AgentID, agentID)
			}
			if filters.CreatedAfter == nil || !filters.CreatedAfter.Equal(from) {
				t.Fatalf("from filter = %v, want %s", filters.CreatedAfter, from)
			}
			if filters.CreatedBefore == nil || !filters.CreatedBefore.Equal(to) {
				t.Fatalf("to filter = %v, want %s", filters.CreatedBefore, to)
			}

			return []repo.TraceSpan{
				{
					ID:         spanID,
					ParentID:   &parentID,
					SpanName:   "model.invoke",
					DurationMS: &duration,
					Attributes: json.RawMessage(`{"run_id":"` + runID.String() + `","task_id":"` + taskID.String() + `"}`),
					CreatedAt:  from,
				},
			}, nil
		},
	}
	h := traceHandlers{spans: fakeRepo}

	req := httptest.NewRequest(http.MethodGet, "/v1/trace/spans?run_id="+runID.String()+"&task_id="+taskID.String()+"&agent_id="+agentID.String()+"&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339), nil)
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "admin",
	}))
	rr := httptest.NewRecorder()

	h.listSpans(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var envelope struct {
		Data []struct {
			SpanID       string         `json:"span_id"`
			ParentSpanID string         `json:"parent_span_id"`
			Operation    string         `json:"operation"`
			DurationMS   *int           `json:"duration_ms"`
			Metadata     map[string]any `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rr.Body.String())
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("span count = %d, want 1 body=%s", len(envelope.Data), rr.Body.String())
	}
	if got := envelope.Data[0].SpanID; got != spanID.String() {
		t.Fatalf("span_id = %q, want %q", got, spanID)
	}
	if got := envelope.Data[0].ParentSpanID; got != parentID.String() {
		t.Fatalf("parent_span_id = %q, want %q", got, parentID)
	}
	if got := envelope.Data[0].Operation; got != "model.invoke" {
		t.Fatalf("operation = %q, want %q", got, "model.invoke")
	}
	if envelope.Data[0].DurationMS == nil || *envelope.Data[0].DurationMS != duration {
		t.Fatalf("duration_ms = %v, want %d", envelope.Data[0].DurationMS, duration)
	}
	if got := envelope.Data[0].Metadata["run_id"]; got != runID.String() {
		t.Fatalf("metadata.run_id = %v, want %s", got, runID)
	}
}

func TestListTraceSpansRejectsInvalidRunID(t *testing.T) {
	h := traceHandlers{spans: &fakeTraceSpanRepo{}}

	req := httptest.NewRequest(http.MethodGet, "/v1/trace/spans?run_id=not-a-uuid", nil)
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	}))
	rr := httptest.NewRecorder()

	h.listSpans(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

type fakeTraceSpanRepo struct {
	listByOrgFn func(ctx context.Context, organizationID uuid.UUID, filters repo.TraceSpanFilters) ([]repo.TraceSpan, error)
}

func (f *fakeTraceSpanRepo) ListByOrganization(ctx context.Context, organizationID uuid.UUID, filters repo.TraceSpanFilters) ([]repo.TraceSpan, error) {
	if f.listByOrgFn == nil {
		return []repo.TraceSpan{}, nil
	}
	return f.listByOrgFn(ctx, organizationID, filters)
}
