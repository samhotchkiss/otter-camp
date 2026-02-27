package server

import (
	"context"
	"fmt"
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
	defaultAuditLimit       = 50
	maxAuditLimit           = 200
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
	router.Get("/orgs/current", r.handlers.getCurrentOrg)
	router.With(middleware.RequireAnyScope(requireReadScope("audit")...)).Get("/audit", r.handlers.listAudit)
	router.With(middleware.RequireAnyScope(requireReadScope("audit")...)).Get("/audit/events", r.handlers.listAudit) // alias for REST-style clients
	router.With(middleware.RequireAnyScope(requireReadScope("audit")...)).Get("/audit-events", r.handlers.listAuditEvents)
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
	IP            string     `json:"ip"`
	Outcome       string     `json:"outcome"`
	CreatedAt     time.Time  `json:"created_at"`
}

type auditEventListResponse struct {
	ID            uuid.UUID      `json:"id"`
	Action        string         `json:"action"`
	PrincipalType string         `json:"principal_type"`
	PrincipalID   uuid.UUID      `json:"principal_id"`
	IP            string         `json:"ip"`
	Outcome       string         `json:"outcome"`
	Context       map[string]any `json:"context"`
	CreatedAt     time.Time      `json:"created_at"`
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

	filters, err := parseAuditFilters(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, err.Error())
		return
	}

	events, err := h.audits.ListByOrg(r.Context(), principal.OrganizationID, filters, repo.Pagination{
		Limit:  parseAuditLimit(r.URL.Query().Get("limit"), defaultAuditLimit),
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
			IP:            event.IP,
			Outcome:       event.Outcome,
			CreatedAt:     event.CreatedAt,
		})
	}
	responder.JSON(w, http.StatusOK, response)
}

func (h orgAuditHandlers) listAuditEvents(w http.ResponseWriter, r *http.Request) {
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
	if !isOwnerOrAdmin(principal.Role) {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	filters, err := parseAuditFilters(r)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, err.Error())
		return
	}

	events, err := h.audits.ListByOrg(r.Context(), principal.OrganizationID, filters, repo.Pagination{
		Limit:  parseAuditLimit(r.URL.Query().Get("limit"), defaultAuditLimit),
		Offset: parseQueryInt(r.URL.Query().Get("offset"), 0),
	})
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list audit events")
		return
	}

	response := make([]auditEventListResponse, 0, len(events))
	for _, event := range events {
		response = append(response, auditEventListResponse{
			ID:            event.ID,
			Action:        eventTypeToAction(event.EventType),
			PrincipalType: event.PrincipalType,
			PrincipalID:   event.PrincipalID,
			IP:            event.IP,
			Outcome:       event.Outcome,
			Context:       cloneMetadata(event.Metadata),
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

func parseAuditLimit(raw string, fallback int) int {
	limit := parseQueryInt(raw, fallback)
	if limit <= 0 {
		return fallback
	}
	if limit > maxAuditLimit {
		return maxAuditLimit
	}
	return limit
}

func parseAuditFilters(r *http.Request) (repo.AuditEventFilters, error) {
	filters := repo.AuditEventFilters{
		EventType: actionToEventType(r.URL.Query().Get("action")),
	}

	principalType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("principal_type")))
	if principalType != "" {
		switch principalType {
		case "human", "agent", "system":
			filters.PrincipalType = &principalType
		default:
			return repo.AuditEventFilters{}, fmt.Errorf("principal_type must be one of: human, agent, system")
		}
	}

	principalIDRaw := strings.TrimSpace(r.URL.Query().Get("principal_id"))
	if principalIDRaw != "" {
		principalID, err := uuid.Parse(principalIDRaw)
		if err != nil {
			return repo.AuditEventFilters{}, fmt.Errorf("principal_id must be a valid UUID")
		}
		filters.PrincipalID = &principalID
	}

	from, err := parseRFC3339QueryTime(r.URL.Query().Get("from"))
	if err != nil {
		return repo.AuditEventFilters{}, fmt.Errorf("from must be an RFC3339 timestamp")
	}
	to, err := parseRFC3339QueryTime(r.URL.Query().Get("to"))
	if err != nil {
		return repo.AuditEventFilters{}, fmt.Errorf("to must be an RFC3339 timestamp")
	}
	if from != nil && to != nil && from.After(*to) {
		return repo.AuditEventFilters{}, fmt.Errorf("from must be before to")
	}
	filters.CreatedAfter = from
	filters.CreatedBefore = to

	return filters, nil
}

func parseRFC3339QueryTime(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, err
	}
	utc := parsed.UTC()
	return &utc, nil
}

func isOwnerOrAdmin(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func cloneMetadata(source map[string]any) map[string]any {
	if len(source) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
