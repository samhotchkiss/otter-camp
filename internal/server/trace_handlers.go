package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type traceSpanRepository interface {
	ListByOrganization(ctx context.Context, organizationID uuid.UUID, filters repo.TraceSpanFilters) ([]repo.TraceSpan, error)
}

type TraceRouteRegistrar struct {
	handlers traceHandlers
}

func NewTraceRouteRegistrar(pool *pgxpool.Pool) *TraceRouteRegistrar {
	var spans traceSpanRepository
	if pool != nil {
		spans = repo.NewTraceSpanRepo(pool)
	}
	return &TraceRouteRegistrar{handlers: traceHandlers{spans: spans}}
}

func (r *TraceRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(middleware.RequireAnyScope(requireReadScope("control")...)).Get("/trace/spans", r.handlers.listSpans)
}

type traceHandlers struct {
	spans traceSpanRepository
}

type traceSpanResponse struct {
	SpanID       uuid.UUID       `json:"span_id"`
	ParentSpanID *uuid.UUID      `json:"parent_span_id,omitempty"`
	Operation    string          `json:"operation"`
	DurationMS   *int            `json:"duration_ms,omitempty"`
	Metadata     json.RawMessage `json:"metadata"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (h traceHandlers) listSpans(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.spans == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "trace span repository unavailable")
		return
	}

	runID, err := parseOptionalUUID(r.URL.Query().Get("run_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid run_id")
		return
	}
	taskID, err := parseOptionalUUID(r.URL.Query().Get("task_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task_id")
		return
	}
	agentID, err := parseOptionalUUID(r.URL.Query().Get("agent_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent_id")
		return
	}
	from, err := parseRFC3339QueryTime(r.URL.Query().Get("from"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid from")
		return
	}
	to, err := parseRFC3339QueryTime(r.URL.Query().Get("to"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid to")
		return
	}
	if from != nil && to != nil && from.After(*to) {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "from must be before to")
		return
	}

	items, err := h.spans.ListByOrganization(r.Context(), principal.OrganizationID, repo.TraceSpanFilters{
		RunID:         runID,
		TaskID:        taskID,
		AgentID:       agentID,
		CreatedAfter:  from,
		CreatedBefore: to,
	})
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list trace spans")
		return
	}

	response := make([]traceSpanResponse, 0, len(items))
	for _, item := range items {
		metadata := item.Attributes
		if len(metadata) == 0 {
			metadata = json.RawMessage(`{}`)
		}
		response = append(response, traceSpanResponse{
			SpanID:       item.ID,
			ParentSpanID: item.ParentID,
			Operation:    item.SpanName,
			DurationMS:   item.DurationMS,
			Metadata:     metadata,
			CreatedAt:    item.CreatedAt.UTC(),
		})
	}

	responder.JSON(w, http.StatusOK, response)
}
