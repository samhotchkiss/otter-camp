package gitserver

import (
	"context"
	"net/http"
	"strings"
)

// ProjectPermission indicates allowed git actions for a project.
type ProjectPermission string

const (
	PermissionRead  ProjectPermission = "read"
	PermissionWrite ProjectPermission = "write"
)

// AuthInfo carries authenticated identity and project permissions.
type AuthInfo struct {
	OrgID       string
	UserID      string
	TokenID     string
	Permissions map[string]ProjectPermission
}

// AuthFunc validates credentials and returns AuthInfo.
// Called with either Bearer token or HTTP Basic credentials.
type AuthFunc func(ctx context.Context, token string) (AuthInfo, error)

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

			info, err := authFunc(r.Context(), token)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
				http.Error(w, "Invalid credentials", http.StatusUnauthorized)
				return
			}

			// Store auth info in context for downstream handlers
			ctx := r.Context()
			ctx = context.WithValue(ctx, ctxKeyOrgID, info.OrgID)
			ctx = context.WithValue(ctx, ctxKeyUserID, info.UserID)
			ctx = context.WithValue(ctx, ctxKeyTokenID, info.TokenID)
			ctx = context.WithValue(ctx, ctxKeyPermissions, info.Permissions)
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
	ctxKeyOrgID       ctxKey = "gitserver.orgID"
	ctxKeyUserID      ctxKey = "gitserver.userID"
	ctxKeyTokenID     ctxKey = "gitserver.tokenID"
	ctxKeyPermissions ctxKey = "gitserver.permissions"
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

// TokenIDFromContext returns the authenticated token ID.
func TokenIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTokenID).(string)
	return v
}

// ProjectPermissionFor returns the permission for a project, if present.
func ProjectPermissionFor(ctx context.Context, projectID string) (ProjectPermission, bool) {
	perms, _ := ctx.Value(ctxKeyPermissions).(map[string]ProjectPermission)
	if perms == nil {
		return "", false
	}
	perm, ok := perms[projectID]
	return perm, ok
}
