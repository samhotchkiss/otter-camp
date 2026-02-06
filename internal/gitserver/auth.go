package gitserver

import (
	"context"
	"net/http"
	"strings"
)

// AuthFunc validates credentials and returns (orgID, userID, error).
// Called with either Bearer token or HTTP Basic credentials.
type AuthFunc func(ctx context.Context, token string) (orgID, userID string, err error)

// AuthMiddleware extracts and validates git credentials.
// Supports:
// - Authorization: Bearer <token>
// - HTTP Basic Auth (password treated as token)
func AuthMiddleware(authFunc AuthFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)
			if token == "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
				http.Error(w, "Authentication required", http.StatusUnauthorized)
				return
			}

			orgID, userID, err := authFunc(r.Context(), token)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
				http.Error(w, "Invalid credentials", http.StatusUnauthorized)
				return
			}

			// Store auth info in context for downstream handlers
			ctx := r.Context()
			ctx = context.WithValue(ctx, ctxKeyOrgID, orgID)
			ctx = context.WithValue(ctx, ctxKeyUserID, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractToken gets token from Authorization header or Basic auth password
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")

	// Bearer token
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// HTTP Basic - use password as token
	_, password, ok := r.BasicAuth()
	if ok && password != "" {
		return password
	}

	return ""
}

// Context keys
type ctxKey string

const (
	ctxKeyOrgID  ctxKey = "gitserver.orgID"
	ctxKeyUserID ctxKey = "gitserver.userID"
)

// OrgIDFromContext returns the authenticated org ID.
func OrgIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyOrgID).(string)
	return v
}

// UserIDFromContext returns the authenticated user ID.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}
