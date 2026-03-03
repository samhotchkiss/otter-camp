package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/secret"
)

type SecretRouteRegistrar struct {
	handlers secretHandlers
}

func NewSecretRouteRegistrar(service secret.Service) *SecretRouteRegistrar {
	return &SecretRouteRegistrar{handlers: secretHandlers{service: service}}
}

func (r *SecretRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(middleware.RequireAnyScope(requireWriteScope("secrets")...)).Post("/secrets", r.handlers.createOrUpdate)
	router.With(middleware.RequireAnyScope(requireReadScope("secrets")...)).Get("/secrets", r.handlers.list)
	router.With(middleware.RequireAnyScope(requireWriteScope("secrets")...)).Delete("/secrets/{slug}", r.handlers.delete)
}

type secretHandlers struct {
	service secret.Service
}

type writeSecretRequest struct {
	Slug        string `json:"slug"`
	Value       string `json:"value"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type secretInfoResponse struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description,omitempty"`
	KeyVersion  int       `json:"key_version"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

func (h secretHandlers) createOrUpdate(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	orgID, ok := api.OrganizationIDFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var req writeSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "invalid request body")
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = req.Slug
	}

	by := secret.Principal{Type: "human", ID: principal.UserID}
	err := h.service.Set(r.Context(), orgID, req.Slug, displayName, strings.TrimSpace(req.Description), req.Value, by)
	if err != nil {
		status, code, message := mapSecretError(err)
		responder.Error(w, status, code, message)
		return
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"slug":   req.Slug,
		"status": "ok",
	})
}

func (h secretHandlers) list(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	orgID, ok := api.OrganizationIDFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	items, err := h.service.List(r.Context(), orgID)
	if err != nil {
		status, code, message := mapSecretError(err)
		responder.Error(w, status, code, message)
		return
	}

	out := make([]secretInfoResponse, 0, len(items))
	for _, item := range items {
		out = append(out, secretInfoResponse{
			ID:          item.ID,
			Slug:        item.Slug,
			DisplayName: item.DisplayName,
			Description: item.Description,
			KeyVersion:  item.KeyVersion,
			CreatedAt:   item.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			UpdatedAt:   item.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	responder.JSON(w, http.StatusOK, out)
}

func (h secretHandlers) delete(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	orgID, ok := api.OrganizationIDFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	slug := chi.URLParam(r, "slug")
	if strings.TrimSpace(slug) == "" {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeValidation, "slug is required")
		return
	}

	by := secret.Principal{Type: "human", ID: principal.UserID}
	err := h.service.Delete(r.Context(), orgID, slug, by)
	if err != nil {
		status, code, message := mapSecretError(err)
		responder.Error(w, status, code, message)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func mapSecretError(err error) (int, string, string) {
	switch {
	case errors.Is(err, secret.ErrInvalidSlug):
		return http.StatusBadRequest, api.ErrCodeValidation, err.Error()
	case errors.Is(err, secret.ErrDisplayNameRequired):
		return http.StatusBadRequest, api.ErrCodeValidation, err.Error()
	case errors.Is(err, secret.ErrSecretValueRequired):
		return http.StatusBadRequest, api.ErrCodeValidation, err.Error()
	case errors.Is(err, secret.ErrInvalidPrincipalType):
		return http.StatusBadRequest, api.ErrCodeValidation, err.Error()
	case errors.Is(err, secret.ErrSecretInUse):
		return http.StatusConflict, api.ErrCodeConflict, err.Error()
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "secret not found"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}
