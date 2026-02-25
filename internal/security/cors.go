package security

import (
	"net/http"
	"os"
	"strings"
)

var (
	corsAllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	corsAllowedHeaders = []string{"Content-Type", "Authorization", "X-Request-ID", "Idempotency-Key", "Last-Event-ID"}
)

func AllowedOriginsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("OTTERCAMP_CORS_ORIGINS"))
	if raw == "" {
		return []string{"http://localhost:4110"}
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		origins = append(origins, trimmed)
	}
	if len(origins) == 0 {
		return []string{"http://localhost:4110"}
	}
	return origins
}

func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}

	methodsValue := strings.Join(corsAllowedMethods, ", ")
	headersValue := strings.Join(corsAllowedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			_, allowed := set[origin]
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Add("Vary", "Origin")
			}

			if strings.EqualFold(strings.TrimSpace(r.Method), http.MethodOptions) {
				w.Header().Set("Access-Control-Allow-Methods", methodsValue)
				w.Header().Set("Access-Control-Allow-Headers", headersValue)
				w.Header().Set("Access-Control-Max-Age", "3600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
