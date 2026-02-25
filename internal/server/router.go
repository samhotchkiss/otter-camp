package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/health"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/security"
	"github.com/samhotchkiss/otter-camp/internal/storage"
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
	authHandlers := newAuthHandlers(opts.AuthService, authSessionRepo)
	mobileHandlers := newMobileHandlers(opts.Pool)
	versionHandler := api.NewVersionHandler(api.BuildInfo{
		Version: opts.Version,
		Commit:  opts.Commit,
		BuiltAt: opts.BuiltAt,
	})
	searchHandler := api.NewSearchHandler(opts.Pool)
	diffHandler := api.NewDiffHandler(opts.Pool, opts.GitService)
	healthHandler := health.NewHandler(health.Options{Pool: opts.Pool, Store: opts.Store})
	scrubber := security.NewSecretScrubber()
	perIPLimiter := security.NewRateLimiter(100, 20)
	perAPIKeyLimiter := security.NewRateLimiter(1000, 100)
	metrics.Register()

	r := chi.NewRouter()
	r.Use(middleware.RequestID(logger))
	r.Use(security.CORSMiddleware(security.AllowedOriginsFromEnv()))
	r.Use(security.PerIPRateLimitMiddleware(perIPLimiter, time.Minute))
	r.Use(security.InputValidationMiddleware(security.NewInputValidator()))
	r.Use(security.OutputSanitizerMiddleware(security.NewOutputSanitizer(scrubber)))
	r.Use(middleware.PrefixEnforcement())
	r.Get("/health/live", healthHandler.Liveness)
	r.Get("/health/ready", healthHandler.Readiness)
	r.Get("/health", healthHandler.Liveness)
	r.Get("/ready", healthHandler.Readiness)
	r.Handle("/metrics", metrics.Handler())
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
		if opts.Pool != nil {
			v1.Use(middleware.Idempotency(middleware.IdempotencyOptions{
				Repository: repo.NewIdempotencyKeyRepo(opts.Pool),
			}))
		}

		v1.Get("/version", versionHandler.Get)
		v1.Post("/auth/login", authHandlers.login)

		v1.Group(func(protected chi.Router) {
			protected.Use(middleware.Auth(middleware.AuthOptions{Service: opts.AuthService, Logger: logger}))
			protected.Use(security.PerAPIKeyRateLimitMiddleware(perAPIKeyLimiter, time.Minute))
			protected.Post("/auth/logout", authHandlers.logout)
			protected.Post("/auth/refresh", authHandlers.refresh)
			protected.Get("/auth/me", authHandlers.me)
			protected.Get("/auth/sessions", authHandlers.listSessions)
			protected.Delete("/auth/sessions/{id}", authHandlers.revokeSession)
			protected.Delete("/auth/sessions", authHandlers.revokeOtherSessions)
			protected.Post("/api-keys", authHandlers.issueAPIKey)
			protected.Delete("/api-keys/{id}", authHandlers.revokeAPIKey)
			protected.Get("/api-keys", authHandlers.listAPIKeys)
			protected.Get("/mobile/dashboard", mobileHandlers.dashboard)
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

	return r
}
