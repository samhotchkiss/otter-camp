package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/push"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type pushPreferenceService interface {
	GetPreferences(ctx context.Context, userID uuid.UUID) (push.PushPreferences, error)
	UpdatePreferences(ctx context.Context, userID uuid.UUID, update push.PushPreferenceUpdate) (push.PushPreferences, error)
}

type pushPreferenceRepository interface {
	RegisterToken(ctx context.Context, userID uuid.UUID, token push.PushToken) error
	RevokeToken(ctx context.Context, userID uuid.UUID, deviceID string) error
}

type PushRouteRegistrar struct {
	handlers pushHandlers
}

func NewPushRouteRegistrar(service *push.PreferenceService, repository *push.PreferenceRepository) *PushRouteRegistrar {
	return &PushRouteRegistrar{handlers: pushHandlers{service: service, repository: repository}}
}

func (r *PushRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(requireReadScope("push")).Get("/me/push-preferences", r.handlers.getPushPreferences)
	router.With(requireWriteScope("push")).Patch("/me/push-preferences", r.handlers.patchPushPreferences)
	router.With(requireWriteScope("push")).Post("/me/push-token", r.handlers.registerPushToken)
	router.With(requireWriteScope("push")).Delete("/me/push-token/{device_id}", r.handlers.revokePushToken)
}

type pushHandlers struct {
	service    pushPreferenceService
	repository pushPreferenceRepository
}

type registerPushTokenRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
	DeviceID string `json:"device_id"`
}

func (h pushHandlers) getPushPreferences(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "push preference service unavailable")
		return
	}

	prefs, err := h.service.GetPreferences(r.Context(), principal.UserID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load push preferences")
		return
	}
	responder.JSON(w, http.StatusOK, prefs)
}

func (h pushHandlers) patchPushPreferences(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.service == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "push preference service unavailable")
		return
	}

	var req push.PushPreferenceUpdate
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	prefs, err := h.service.UpdatePreferences(r.Context(), principal.UserID, req)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		if isPushValidationError(err) {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
			return
		}
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to update push preferences")
		return
	}
	responder.JSON(w, http.StatusOK, prefs)
}

func (h pushHandlers) registerPushToken(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.repository == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "push preference repository unavailable")
		return
	}

	var req registerPushTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Token) == "" || strings.TrimSpace(req.DeviceID) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "token and device_id are required")
		return
	}
	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if platform != "apns" && platform != "fcm" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "platform must be apns or fcm")
		return
	}

	if err := h.repository.RegisterToken(r.Context(), principal.UserID, push.PushToken{
		Token:    req.Token,
		Platform: platform,
		DeviceID: req.DeviceID,
	}); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		if isPushValidationError(err) {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
			return
		}
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to register push token")
		return
	}

	responder.JSON(w, http.StatusOK, map[string]bool{"registered": true})
}

func (h pushHandlers) revokePushToken(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.repository == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "push preference repository unavailable")
		return
	}

	deviceID := strings.TrimSpace(chi.URLParam(r, "device_id"))
	if deviceID == "" {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid device id")
		return
	}

	if err := h.repository.RevokeToken(r.Context(), principal.UserID, deviceID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		if isPushValidationError(err) {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
			return
		}
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to revoke push token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func isPushValidationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "invalid") || strings.Contains(message, "required")
}
