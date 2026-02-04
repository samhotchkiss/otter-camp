// Package middleware provides HTTP middleware for workspace isolation.
package middleware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// ContextKey is the type for context keys in this package.
type ContextKey string

const (
	// WorkspaceIDKey is the context key for the current workspace/org ID.
	WorkspaceIDKey ContextKey = "workspace_id"
	// UserIDKey is the context key for the authenticated user ID.
	UserIDKey ContextKey = "user_id"
)

var uuidRegex = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

// jwtClaims represents minimal JWT claims for workspace extraction.
type jwtClaims struct {
	OrgID          string `json:"org_id"`
	OrganizationID string `json:"organization_id"`
	WorkspaceID    string `json:"workspace_id"`
	Sub            string `json:"sub"`
}

// WorkspaceFromContext retrieves the workspace ID from the request context.
// Returns empty string if not set.
func WorkspaceFromContext(ctx context.Context) string {
	if v := ctx.Value(WorkspaceIDKey); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// UserFromContext retrieves the user ID from the request context.
// Returns empty string if not set.
func UserFromContext(ctx context.Context) string {
	if v := ctx.Value(UserIDKey); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// RequireWorkspace is middleware that ensures a valid workspace ID is present.
// It extracts the workspace from:
// 1. JWT Bearer token claims (org_id, organization_id, or workspace_id)
// 2. X-Workspace-ID header (for service-to-service calls)
// 3. X-Org-ID header (legacy/webhook support)
//
// If no valid workspace is found, it returns 401 Unauthorized.
func RequireWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID := extractWorkspaceID(r)
		if workspaceID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing or invalid workspace"}`))
			return
		}

		ctx := context.WithValue(r.Context(), WorkspaceIDKey, workspaceID)

		// Also extract user ID if available from JWT
		if userID := extractUserID(r); userID != "" {
			ctx = context.WithValue(ctx, UserIDKey, userID)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalWorkspace is middleware that extracts workspace ID if present
// but does not require it. Useful for public endpoints that may optionally
// be scoped to a workspace.
func OptionalWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID := extractWorkspaceID(r)
		ctx := r.Context()

		if workspaceID != "" {
			ctx = context.WithValue(ctx, WorkspaceIDKey, workspaceID)
		}

		if userID := extractUserID(r); userID != "" {
			ctx = context.WithValue(ctx, UserIDKey, userID)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractWorkspaceID attempts to extract workspace ID from various sources.
func extractWorkspaceID(r *http.Request) string {
	// 1. Try JWT Bearer token first
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		if claims := parseJWTClaims(token); claims != nil {
			if id := firstValidUUID(claims.OrgID, claims.OrganizationID, claims.WorkspaceID); id != "" {
				return id
			}
		}
	}

	// 2. Try explicit workspace header
	if id := strings.TrimSpace(r.Header.Get("X-Workspace-ID")); id != "" && uuidRegex.MatchString(id) {
		return id
	}

	// 3. Try legacy org header
	if id := strings.TrimSpace(r.Header.Get("X-Org-ID")); id != "" && uuidRegex.MatchString(id) {
		return id
	}

	// 4. Try query parameter (for specific endpoints that allow it)
	if id := strings.TrimSpace(r.URL.Query().Get("org_id")); id != "" && uuidRegex.MatchString(id) {
		return id
	}

	return ""
}

// extractUserID attempts to extract user ID from JWT.
func extractUserID(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		if claims := parseJWTClaims(token); claims != nil && claims.Sub != "" {
			return claims.Sub
		}
	}
	return ""
}

// parseJWTClaims extracts claims from a JWT without verifying the signature.
// Signature verification is expected to be done by an upstream service or middleware.
func parseJWTClaims(token string) *jwtClaims {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}

	// Decode the payload (second part)
	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try standard encoding
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil
		}
	}

	var claims jwtClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil
	}

	return &claims
}

// firstValidUUID returns the first non-empty, valid UUID from the given strings.
func firstValidUUID(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && uuidRegex.MatchString(v) {
			return v
		}
	}
	return ""
}
