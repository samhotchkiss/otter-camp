package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	bootstrapAuditAction    = "bootstrap_complete"
	bootstrapAuditEventType = "system.bootstrap"
)

type orgRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Organization, error)
}

type auditEventRepository interface {
	ListByOrg(ctx context.Context, organizationID uuid.UUID, filters repo.AuditEventFilters, pagination repo.Pagination) ([]repo.AuditEvent, error)
}

type OrgAuditRouteRegistrar struct {
	handlers orgAuditHandlers
}

func NewOrgAuditRouteRegistrar(pool *pgxpool.Pool) *OrgAuditRouteRegistrar {
	handlers := orgAuditHandlers{}
	if pool != nil {
		handlers.orgs = repo.NewOrgRepo(pool)
		handlers.audits = repo.NewAuditEventRepo(pool)
	}
	return &OrgAuditRouteRegistrar{handlers: handlers}
}

func (r *OrgAuditRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(requireReadScope("orgs")).Get("/orgs/current", r.handlers.getCurrentOrg)
	router.With(requireReadScope("audit")).Get("/audit", r.handlers.listAudit)
}

type orgAuditHandlers struct {
	orgs   orgRepository
	audits auditEventRepository
}

type currentOrganizationResponse struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

type auditEventResponse struct {
	ID            uuid.UUID  `json:"id"`
	Action        string     `json:"action"`
	EventType     string     `json:"event_type"`
	PrincipalType string     `json:"principal_type"`
	PrincipalID   uuid.UUID  `json:"principal_id"`
	TargetType    *string    `json:"target_type"`
	TargetID      *uuid.UUID `json:"target_id"`
	CreatedAt     time.Time  `json:"created_at"`
}

func (h orgAuditHandlers) getCurrentOrg(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.orgs == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "organization repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	org, err := h.orgs.GetByID(r.Context(), principal.OrganizationID)
	if err != nil {
		if err == repo.ErrNotFound {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load organization")
		return
	}

	responder.JSON(w, http.StatusOK, currentOrganizationResponse{
		ID:   org.ID,
		Name: org.DisplayName,
		Slug: org.Slug,
	})
}

func (h orgAuditHandlers) listAudit(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	if h.audits == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "audit repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	filters := repo.AuditEventFilters{
		EventType: actionToEventType(r.URL.Query().Get("action")),
	}

	events, err := h.audits.ListByOrg(r.Context(), principal.OrganizationID, filters, repo.Pagination{
		Limit:  parseQueryInt(r.URL.Query().Get("limit"), 50),
		Offset: parseQueryInt(r.URL.Query().Get("offset"), 0),
	})
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list audit events")
		return
	}

	response := make([]auditEventResponse, 0, len(events))
	for _, event := range events {
		response = append(response, auditEventResponse{
			ID:            event.ID,
			Action:        eventTypeToAction(event.EventType),
			EventType:     event.EventType,
			PrincipalType: event.PrincipalType,
			PrincipalID:   event.PrincipalID,
			TargetType:    event.TargetType,
			TargetID:      event.TargetID,
			CreatedAt:     event.CreatedAt,
		})
	}
	responder.JSON(w, http.StatusOK, response)
}

func actionToEventType(action string) *string {
	trimmed := strings.TrimSpace(action)
	if trimmed == "" {
		return nil
	}
	if strings.EqualFold(trimmed, bootstrapAuditAction) {
		mapped := bootstrapAuditEventType
		return &mapped
	}
	return &trimmed
}

func eventTypeToAction(eventType string) string {
	if strings.EqualFold(strings.TrimSpace(eventType), bootstrapAuditEventType) {
		return bootstrapAuditAction
	}
	return strings.TrimSpace(eventType)
}

func parseQueryInt(raw string, fallback int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return fallback
	}
	return value
}
