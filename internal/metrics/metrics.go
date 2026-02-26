package metrics

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsQueryTimeout = 2 * time.Second

var (
	registerBaseOnce sync.Once
	handlerOnce      sync.Once
	handler          http.Handler

	poolMu sync.RWMutex
	pool   *pgxpool.Pool

	APIRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_api_requests_total",
		Help: "Total API requests.",
	}, []string{"method", "path", "status"})
	APIRequestDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ottercamp_api_request_duration_seconds",
		Help:    "API request durations in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
	ModelTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_model_tokens_total",
		Help: "Total model tokens by provider/model/type.",
	}, []string{"provider", "model", "type"})
	JobQueueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ottercamp_job_queue_depth",
		Help: "Current job queue depth by job type and status.",
	}, []string{"job_type", "status"})
	MemoryItemsTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ottercamp_memory_items_total",
		Help: "Current memory item counts by status.",
	}, []string{"status"})
	AgentTurnsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_agent_turns_total",
		Help: "Total agent turns by status.",
	}, []string{"status"})
)

func Register() {
	registerBaseOnce.Do(func() {
		prometheus.MustRegister(
			APIRequestsTotal,
			APIRequestDurationSeconds,
			ModelTokensTotal,
			JobQueueDepth,
			MemoryItemsTotal,
			AgentTurnsTotal,
		)
	})
}

func RegisterWithPool(dbPool *pgxpool.Pool) {
	Register()
	if dbPool == nil {
		return
	}
	poolMu.Lock()
	pool = dbPool
	poolMu.Unlock()
}

func Handler() http.Handler {
	Register()
	handlerOnce.Do(func() {
		promHandler := promhttp.Handler()
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			refreshDynamicMetrics(r.Context())
			promHandler.ServeHTTP(w, r)
		})
	})
	return handler
}

func HTTPMiddleware() func(http.Handler) http.Handler {
	Register()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if next == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			startedAt := time.Now()
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)

			method := normalizeLabel(strings.ToUpper(strings.TrimSpace(r.Method)), "UNKNOWN")
			path := routePatternOrPath(r)
			status := strconv.Itoa(recorder.statusCode())

			APIRequestsTotal.WithLabelValues(method, path, status).Inc()
			APIRequestDurationSeconds.WithLabelValues(method, path).Observe(time.Since(startedAt).Seconds())
		})
	}
}

func RecordModelTokens(provider, model string, inputTokens, outputTokens, cacheReadTokens int) {
	Register()
	provider = normalizeLabel(provider, "unknown")
	model = normalizeLabel(model, "unknown")
	if inputTokens > 0 {
		ModelTokensTotal.WithLabelValues(provider, model, "input").Add(float64(inputTokens))
	}
	if outputTokens > 0 {
		ModelTokensTotal.WithLabelValues(provider, model, "output").Add(float64(outputTokens))
	}
	if cacheReadTokens > 0 {
		ModelTokensTotal.WithLabelValues(provider, model, "cache_read").Add(float64(cacheReadTokens))
	}
}

func RecordAgentTurn(status string) {
	Register()
	AgentTurnsTotal.WithLabelValues(normalizeLabel(status, "unknown")).Inc()
}

func SetJobQueueDepth(jobType, status string, count float64) {
	Register()
	JobQueueDepth.WithLabelValues(normalizeLabel(jobType, "unknown"), normalizeLabel(status, "unknown")).Set(count)
}

func SetMemoryItemsTotal(status string, count float64) {
	Register()
	MemoryItemsTotal.WithLabelValues(normalizeLabel(status, "unknown")).Set(count)
}

func refreshDynamicMetrics(ctx context.Context) {
	poolMu.RLock()
	dbPool := pool
	poolMu.RUnlock()
	if dbPool == nil {
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}
	queryCtx, cancel := context.WithTimeout(ctx, metricsQueryTimeout)
	defer cancel()

	refreshJobQueueDepth(queryCtx, dbPool)
	refreshMemoryItems(queryCtx, dbPool)
}

func refreshJobQueueDepth(ctx context.Context, dbPool *pgxpool.Pool) {
	rows, err := dbPool.Query(ctx, `
		SELECT job_type, status, COUNT(*)::bigint
		FROM job_queue
		GROUP BY job_type, status
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	JobQueueDepth.Reset()
	for rows.Next() {
		var (
			jobType string
			status  string
			count   int64
		)
		if scanErr := rows.Scan(&jobType, &status, &count); scanErr != nil {
			return
		}
		SetJobQueueDepth(jobType, status, float64(count))
	}
}

func refreshMemoryItems(ctx context.Context, dbPool *pgxpool.Pool) {
	rows, err := dbPool.Query(ctx, `
		SELECT status, COUNT(*)::bigint
		FROM memory
		GROUP BY status
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	MemoryItemsTotal.Reset()
	for rows.Next() {
		var (
			status string
			count  int64
		)
		if scanErr := rows.Scan(&status, &count); scanErr != nil {
			return
		}
		SetMemoryItemsTotal(status, float64(count))
	}
}

func routePatternOrPath(r *http.Request) string {
	if r != nil {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if pattern := strings.TrimSpace(rctx.RoutePattern()); pattern != "" {
				return pattern
			}
		}
		if r.URL != nil {
			if path := strings.TrimSpace(r.URL.Path); path != "" {
				return path
			}
		}
	}
	return "unknown"
}

func normalizeLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(data)
}

func (r *statusRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
