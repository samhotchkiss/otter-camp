package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type forbiddenCapabilityResponse struct {
	Error      string `json:"error"`
	Capability string `json:"capability"`
}

// RequireCapability enforces session authentication and role-based capability checks.
func RequireCapability(db *sql.DB, capability string) func(http.Handler) http.Handler {
	capability = strings.TrimSpace(capability)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if db == nil {
				sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
				return
			}

			if capability == "" {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "capability not configured"})
				return
			}

			identity, err := requireSessionIdentity(r.Context(), db, r)
			if err != nil {
				handleCapabilityAuthError(w, err, capability)
				return
			}

			if !roleAllowsCapability(identity.Role, capability) {
				sendJSON(w, http.StatusForbidden, forbiddenCapabilityResponse{
					Error:      "forbidden",
					Capability: capability,
				})
				return
			}

			ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, identity.OrgID)
			ctx = context.WithValue(ctx, middleware.UserIDKey, identity.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func handleCapabilityAuthError(w http.ResponseWriter, err error, capability string) {
	switch {
	case errors.Is(err, errMissingAuthentication),
		errors.Is(err, errInvalidSessionToken),
		errors.Is(err, errAuthentication):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
	case errors.Is(err, errWorkspaceMismatch):
		sendJSON(w, http.StatusForbidden, forbiddenCapabilityResponse{
			Error:      err.Error(),
			Capability: capability,
		})
	default:
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication error"})
	}
}
