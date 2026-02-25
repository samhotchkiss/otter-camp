package health

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

const (
	checkTimeout       = 2 * time.Second
	healthSentinelPath = "health/sentinel"
)

type Handler struct {
	pool  *pgxpool.Pool
	store storage.Store
}

type Options struct {
	Pool  *pgxpool.Pool
	Store storage.Store
}

func NewHandler(opts Options) *Handler {
	return &Handler{pool: opts.Pool, store: opts.Store}
}

func (h *Handler) Liveness(w http.ResponseWriter, _ *http.Request) {
	api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	checks := map[string]bool{
		"db":         false,
		"pgvector":   false,
		"storage":    false,
		"migrations": false,
	}

	checks["db"] = h.runCheck(r.Context(), h.checkDB)
	checks["pgvector"] = h.runCheck(r.Context(), h.checkVector)
	checks["storage"] = h.runCheck(r.Context(), h.checkStorage)
	checks["migrations"] = h.runCheck(r.Context(), h.checkPendingMigrations)

	for _, ok := range checks {
		if !ok {
			api.JSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "checks": checks})
			return
		}
	}

	api.JSON(w, http.StatusOK, map[string]any{"status": "ok", "checks": checks})
}

func (h *Handler) runCheck(ctx context.Context, fn func(context.Context) bool) bool {
	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	return fn(checkCtx)
}

func (h *Handler) checkDB(ctx context.Context) bool {
	if h.pool == nil {
		return false
	}
	var one int
	return h.pool.QueryRow(ctx, `SELECT 1`).Scan(&one) == nil
}

func (h *Handler) checkVector(ctx context.Context) bool {
	if h.pool == nil {
		return false
	}
	var one int
	err := h.pool.QueryRow(ctx, `
		SELECT 1
		FROM pg_extension
		WHERE extname = 'vector'
		LIMIT 1
	`).Scan(&one)
	return err == nil
}

func (h *Handler) checkStorage(ctx context.Context) bool {
	if h.store == nil {
		return false
	}
	_, err := h.store.Exists(ctx, healthSentinelPath)
	return err == nil
}

func (h *Handler) checkPendingMigrations(ctx context.Context) bool {
	if h.pool == nil {
		return false
	}

	var hasAppliedColumn bool
	err := h.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = 'schema_migrations'
			  AND column_name = 'applied'
		)
	`).Scan(&hasAppliedColumn)
	if err != nil {
		return false
	}
	if !hasAppliedColumn {
		return true
	}

	var pending int
	if err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE applied = false`).Scan(&pending); err != nil {
		return false
	}
	return pending == 0
}

func IsHealthPath(path string) bool {
	normalized := strings.TrimSpace(path)
	switch normalized {
	case "/health", "/health/live", "/health/ready", "/ready":
		return true
	default:
		return false
	}
}
