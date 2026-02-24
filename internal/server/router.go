package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
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
	Mode            config.Mode
	Logger          *slog.Logger
	AuthService     auth.Service
	Pool            *pgxpool.Pool
	GitService      api.GitService
	TestResetter    TestResetter
	RouteRegistrars []RouteRegistrar
}

func NewHandlerWithOptions(opts HandlerOptions) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	authHandlers := newAuthHandlers(opts.AuthService)
	versionHandler := api.NewVersionHandler(api.BuildInfo{
		Version: opts.Version,
		Commit:  opts.Commit,
		BuiltAt: opts.BuiltAt,
	})
	searchHandler := api.NewSearchHandler(opts.Pool)
	diffHandler := api.NewDiffHandler(opts.Pool, opts.GitService)

	r := chi.NewRouter()
	r.Use(middleware.RequestID(logger))
	r.Use(middleware.PrefixEnforcement())
	r.Get("/health/live", healthOK)
	r.Get("/health/ready", healthOK)
	r.Get("/health", healthOK)
	r.Get("/ready", healthOK)
	if opts.Mode == config.ModeTest && opts.TestResetter != nil {
		r.Post("/test/reset", func(w http.ResponseWriter, r *http.Request) {
			if err := opts.TestResetter.Reset(r.Context()); err != nil {
				api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to reset test state")
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
			protected.Post("/auth/logout", authHandlers.logout)
			protected.Post("/auth/refresh", authHandlers.refresh)
			protected.Get("/auth/me", authHandlers.me)
			protected.Post("/api-keys", authHandlers.issueAPIKey)
			protected.Delete("/api-keys/{id}", authHandlers.revokeAPIKey)
			protected.Get("/api-keys", authHandlers.listAPIKeys)
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

func healthOK(w http.ResponseWriter, _ *http.Request) {
	api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
