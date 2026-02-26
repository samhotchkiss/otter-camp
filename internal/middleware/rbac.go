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

func RequireScope(scope string) func(http.Handler) http.Handler {
	return RequireAnyScope(scope)
}

func RequireAnyScope(scopes ...string) func(http.Handler) http.Handler {
	required := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if normalized == "" {
			continue
		}
		required[normalized] = struct{}{}
	}

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
			if len(required) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			for _, granted := range principal.APIKey.Scopes {
				normalized := strings.ToLower(strings.TrimSpace(granted))
				if normalized == "" {
					continue
				}
				if normalized == "admin:*" {
					next.ServeHTTP(w, r)
					return
				}
				if _, ok := required[normalized]; ok {
					next.ServeHTTP(w, r)
					return
				}
			}

			api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "missing api key scope")
		})
	}
}
