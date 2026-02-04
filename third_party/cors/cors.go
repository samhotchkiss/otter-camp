package cors

import (
	"net/http"
	"strconv"
	"strings"
)

type Options struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

func Handler(options Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := ""
			if len(options.AllowedOrigins) > 0 {
				origin = options.AllowedOrigins[0]
			}
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			if len(options.AllowedMethods) > 0 {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(options.AllowedMethods, ","))
			}
			if len(options.AllowedHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(options.AllowedHeaders, ","))
			}
			if len(options.ExposedHeaders) > 0 {
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(options.ExposedHeaders, ","))
			}
			if options.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		if options.MaxAge > 0 {
			w.Header().Set("Access-Control-Max-Age", strconv.Itoa(options.MaxAge))
		}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
