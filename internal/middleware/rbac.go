package middleware

import (
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/api"
)

var roleRank = map[string]int{
	"member": 1,
	"admin":  2,
}

func RequireRole(role string) func(http.Handler) http.Handler {
	required := roleRank[strings.ToLower(strings.TrimSpace(role))]

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
				return
			}

			if roleRank[strings.ToLower(strings.TrimSpace(principal.Role))] < required {
				api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// EnforceAPIKeyScopes is a global middleware that enforces API key scope
// restrictions. When a principal is authenticated via an API key with declared
// scopes (and no wildcard "*" scope), the request must match a scope that
// covers the requested resource and HTTP method. Non-API-key principals (human
// session auth) bypass scope enforcement entirely.
func EnforceAPIKeyScopes() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok || principal.APIKey == nil || len(principal.APIKey.Scopes) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Wildcard or admin scope grants full access.
			for _, s := range principal.APIKey.Scopes {
				s = strings.TrimSpace(s)
				if s == "*" || s == "admin" {
					next.ServeHTTP(w, r)
					return
				}
			}

			required := apiKeyScopeForRequest(r)
			if required == "" {
				// No scope mapping for this path — allow through.
				next.ServeHTTP(w, r)
				return
			}

			for _, granted := range principal.APIKey.Scopes {
				if strings.TrimSpace(granted) == required {
					next.ServeHTTP(w, r)
					return
				}
			}

			api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "insufficient api key scope")
		})
	}
}

// apiKeyScopeForRequest maps an HTTP request to the minimum scope required to
// access it. Returns "" when the path has no scope mapping (allow through).
func apiKeyScopeForRequest(r *http.Request) string {
	isWrite := r.Method == http.MethodPost || r.Method == http.MethodPut ||
		r.Method == http.MethodPatch || r.Method == http.MethodDelete
	action := "read"
	if isWrite {
		action = "write"
	}

	path := r.URL.Path
	switch {
	case strings.Contains(path, "/projects"), strings.Contains(path, "/flow-templates"), strings.Contains(path, "/tasks"), strings.Contains(path, "/inbox"), strings.Contains(path, "/environments"), strings.Contains(path, "/remotes"):
		return action + ":projects"
	case strings.Contains(path, "/chat-sessions"):
		return action + ":chat"
	case strings.Contains(path, "/agents"):
		return action + ":agents"
	case strings.Contains(path, "/memory"):
		return action + ":memory"
	case strings.Contains(path, "/model"):
		return action + ":models"
	case strings.Contains(path, "/mcp"):
		return action + ":mcp"
	case strings.Contains(path, "/audit"), strings.Contains(path, "/admin"), strings.Contains(path, "/control"):
		return "admin"
	default:
		return ""
	}
}

func RequireScope(scope string) func(http.Handler) http.Handler {
	normalized := strings.TrimSpace(scope)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
				return
			}

			if principal.APIKey == nil {
				next.ServeHTTP(w, r)
				return
			}

			for _, granted := range principal.APIKey.Scopes {
				if strings.TrimSpace(granted) == normalized {
					next.ServeHTTP(w, r)
					return
				}
			}

			api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "missing api key scope")
		})
	}
}
