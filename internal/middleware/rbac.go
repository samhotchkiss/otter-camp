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
	required := normalizeScopeList(scopes)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
				return
			}

			if principal.APIKey == nil || len(required) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			for _, granted := range principal.APIKey.Scopes {
				if scopeMatchesAny(granted, required) {
					next.ServeHTTP(w, r)
					return
				}
			}

			api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "missing api key scope")
		})
	}
}

func normalizeScopeList(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func scopeMatchesAny(granted string, required []string) bool {
	normalizedGranted := strings.ToLower(strings.TrimSpace(granted))
	if normalizedGranted == "" {
		return false
	}

	for _, candidate := range required {
		if scopesEquivalent(normalizedGranted, candidate) {
			return true
		}
	}
	return false
}

func scopesEquivalent(a, b string) bool {
	if a == b {
		return true
	}
	aLeft, aRight, aOK := splitScope(a)
	bLeft, bRight, bOK := splitScope(b)
	if !aOK || !bOK {
		return false
	}
	return aLeft == bRight && aRight == bLeft
}

func splitScope(value string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	left := strings.TrimSpace(parts[0])
	right := strings.TrimSpace(parts[1])
	if left == "" || right == "" {
		return "", "", false
	}
	return left, right, true
}
