package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/health"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/security"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/web"
)

type RouteRegistrar interface {
	RegisterRoutes(r chi.Router)
}

type TestResetter interface {
	Reset(ctx context.Context) error
}

type HandlerOptions struct {
	Version         string
	Commit          string
	BuiltAt         string
	Logger          *slog.Logger
	AuthService     auth.Service
	Pool            *pgxpool.Pool
	Store           storage.Store
	GitService      api.GitService
	RouteRegistrars []RouteRegistrar
	TestMode        bool
	TestResetter    TestResetter
}

func NewHandlerWithOptions(opts HandlerOptions) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var authSessionRepo *repo.AuthSessionRepo
	if opts.Pool != nil {
		authSessionRepo = repo.NewAuthSessionRepo(opts.Pool)
	}
	var userRepo *repo.HumanUserRepo
	if opts.Pool != nil {
		userRepo = repo.NewHumanUserRepo(opts.Pool)
	}
	var orgRepo *repo.OrgRepo
	if opts.Pool != nil {
		orgRepo = repo.NewOrgRepo(opts.Pool)
	}
	authHandlers := newAuthHandlers(opts.AuthService, userRepo, authSessionRepo, orgRepo)
	if opts.Pool != nil {
		authHandlers.auditRecorder = audit.NewService(repo.NewAuditEventRepo(opts.Pool), logger)
	}
	mobileHandlers := newMobileHandlers(opts.Pool)
	versionHandler := api.NewVersionHandler(api.BuildInfo{
		Version: opts.Version,
		Commit:  opts.Commit,
		BuiltAt: opts.BuiltAt,
	})
	searchHandler := api.NewSearchHandler(opts.Pool)
	diffHandler := api.NewDiffHandler(opts.Pool, opts.GitService)
	staticFileServer := web.NewStaticFileServer(web.Options{})
	healthHandler := health.NewHandler(health.Options{Pool: opts.Pool, Store: opts.Store})
	scrubber := security.NewSecretScrubber()
	ipRequests, ipBurst := 100, 20
	apiKeyRequests, apiKeyBurst := 1000, 100
	if opts.TestMode {
		ipRequests, ipBurst = 10000, 10000
		apiKeyRequests, apiKeyBurst = 100000, 100000
	}
	perIPLimiter := security.NewRateLimiter(ipRequests, ipBurst)
	perAPIKeyLimiter := security.NewRateLimiter(apiKeyRequests, apiKeyBurst)
	metrics.RegisterWithPool(opts.Pool)

	r := chi.NewRouter()
	r.Use(middleware.RequestID(logger))
	r.Use(security.SecurityHeadersMiddleware())
	r.Use(security.CORSMiddleware(security.AllowedOriginsFromEnv()))
	r.Use(security.PerIPRateLimitMiddleware(perIPLimiter, time.Minute))
	r.Use(security.InputValidationMiddleware(security.NewInputValidator()))
	r.Use(security.OutputSanitizerMiddleware(security.NewOutputSanitizer(scrubber)))
	r.Use(middleware.PrefixEnforcement())
	r.Use(metrics.HTTPMiddleware())
	r.Handle("/metrics", metrics.Handler())
	r.Get("/health/live", healthHandler.Liveness)
	r.Get("/health/ready", healthHandler.Readiness)
	r.Get("/health", healthHandler.Liveness)
	r.Get("/ready", healthHandler.Readiness)
	r.Get("/auth/magic", authHandlers.consumeMagicLink)
	if opts.TestMode && opts.TestResetter != nil {
		r.Post("/test/reset", func(w http.ResponseWriter, req *http.Request) {
			if err := opts.TestResetter.Reset(req.Context()); err != nil {
				api.Error(w, http.StatusInternalServerError, "test_reset_failed", err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	r.Route("/v1", func(v1 chi.Router) {
		v1.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				// Limit request body size to 4MB for all /v1 endpoints to prevent DoS.
				// File upload endpoints apply their own limit on top of this.
				if r.Body != nil && r.Body != http.NoBody {
					r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
				}
				next.ServeHTTP(w, r)
			})
		})
		if opts.Pool != nil {
			v1.Use(middleware.Idempotency(middleware.IdempotencyOptions{
				Repository: repo.NewIdempotencyKeyRepo(opts.Pool),
			}))
		}

		v1.Get("/version", versionHandler.Get)
		v1.Post("/auth/login", authHandlers.login)
		v1.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		})

		v1.Group(func(protected chi.Router) {
			protected.Use(middleware.Auth(middleware.AuthOptions{Service: opts.AuthService, Logger: logger}))
			protected.Use(security.PerAPIKeyRateLimitMiddleware(perAPIKeyLimiter, time.Minute))
			protected.Post("/auth/logout", authHandlers.logout)
			protected.Post("/auth/refresh", authHandlers.refresh)
			protected.Get("/auth/me", authHandlers.me)
			protected.Get("/users/me", authHandlers.me) // alias for spec compatibility
			protected.Get("/auth/sessions", authHandlers.listSessions)
			protected.Delete("/auth/sessions/{id}", authHandlers.revokeSession)
			protected.Delete("/auth/sessions", authHandlers.revokeOtherSessions)
			protected.Post("/api-keys", authHandlers.issueAPIKey)
			protected.Delete("/api-keys/{id}", authHandlers.revokeAPIKey)
			protected.Get("/api-keys", authHandlers.listAPIKeys)
			protected.With(
				middleware.RequireRole("admin"),
				middleware.RequireAnyScope(requireAdminScope("auth")...),
			).Post("/users", authHandlers.createUser)
			protected.With(
				middleware.RequireRole("admin"),
				middleware.RequireAnyScope(requireAdminScope("auth")...),
			).Post("/orgs", authHandlers.createOrganization)
			protected.Get("/mobile/dashboard", mobileHandlers.dashboard)
			protected.With(
				middleware.RequireRole("admin"),
				middleware.RequireAnyScope(requireAdminScope("auth")...),
			).Get("/admin/users", authHandlers.listAdminUsers)
			protected.With(
				middleware.RequireRole("admin"),
				middleware.RequireAnyScope(requireAdminScope("auth")...),
			).Post("/admin/users/{id}/reset-password", authHandlers.adminResetPassword)
			protected.With(
				middleware.RequireRole("admin"),
				middleware.RequireAnyScope(requireAdminScope("auth")...),
			).Post("/admin/users/{id}/magic-link", authHandlers.adminMagicLink)
			protected.With(
				middleware.RequireRole("admin"),
				middleware.RequireAnyScope(requireAdminScope("auth")...),
			).Post("/admin/users/{id}/unlock", authHandlers.adminUnlockAccount)
			protected.With(
				middleware.RequireRole("admin"),
				middleware.RequireAnyScope(requireAdminScope("auth")...),
			).Patch("/admin/users/{id}/role", authHandlers.adminUpdateUserRole)
			// GET /v1/search is the Cmd-K global search endpoint.
			// It is registered explicitly here (before the SPA fallback) to prevent interception.
			protected.Get("/search", searchHandler.Search)
			protected.Get("/tasks/{id}/diff", diffHandler.GetTaskDiff)

			for _, registrar := range opts.RouteRegistrars {
				if registrar == nil {
					continue
				}
				registrar.RegisterRoutes(protected)
			}
		})
	})
	r.Get("/assets/*", staticFileServer.ServeAsset)
	r.Get("/favicon.ico", staticFileServer.ServeFavicon)
	spaFallback := web.SPAFallbackHandler(staticFileServer)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/v1" {
			api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		if web.ShouldServeSPAFallback(r) {
			spaFallback.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})

	return r
}

// localhostOnly wraps a handler so it only responds to requests from loopback
// addresses (127.0.0.1, ::1). Other callers receive 403. This is used to
// restrict the /metrics endpoint to local Prometheus scrapers.
func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.RemoteAddr
		// Strip port if present.
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		switch host {
		case "127.0.0.1", "::1", "localhost":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	})
}
