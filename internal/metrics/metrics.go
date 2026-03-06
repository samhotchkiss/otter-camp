package metrics

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
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
const metricsSecretEnv = "OTTERCAMP_METRICS_SECRET"

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
	AgentTurnDispatchSuppressedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_agent_turn_dispatch_suppressed_total",
		Help: "Total suppressed agent turn dispatch attempts by reason.",
	}, []string{"reason"})
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
			AgentTurnDispatchSuppressedTotal,
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
			// When no secret is configured, /metrics remains intentionally open.
			if secret := strings.TrimSpace(os.Getenv(metricsSecretEnv)); secret != "" && !hasMetricsSecret(r, secret) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("unauthorized"))
				return
			}
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

func RecordAgentTurnDispatchSuppressed(reason string) {
	Register()
	AgentTurnDispatchSuppressedTotal.WithLabelValues(normalizeLabel(reason, "unknown")).Inc()
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

	query := func(ctx context.Context, sql string, args ...any) (metricsRows, error) {
		return dbPool.Query(ctx, sql, args...)
	}
	_ = refreshJobQueueDepth(queryCtx, query)
	_ = refreshMemoryItems(queryCtx, query)
}

type metricsRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

type metricsQueryFunc func(ctx context.Context, sql string, args ...any) (metricsRows, error)

func refreshJobQueueDepth(ctx context.Context, query metricsQueryFunc) error {
	rows, err := query(ctx, `
		SELECT job_type, status, COUNT(*)::bigint
		FROM job_queue
		GROUP BY job_type, status
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rowValue struct {
		jobType string
		status  string
		count   int64
	}
	values := make([]rowValue, 0, 8)
	for rows.Next() {
		var (
			jobType string
			status  string
			count   int64
		)
		if scanErr := rows.Scan(&jobType, &status, &count); scanErr != nil {
			return scanErr
		}
		values = append(values, rowValue{
			jobType: jobType,
			status:  status,
			count:   count,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	JobQueueDepth.Reset()
	for _, value := range values {
		SetJobQueueDepth(value.jobType, value.status, float64(value.count))
	}
	return nil
}

func refreshMemoryItems(ctx context.Context, query metricsQueryFunc) error {
	rows, err := query(ctx, `
		SELECT status, COUNT(*)::bigint
		FROM memory
		GROUP BY status
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rowValue struct {
		status string
		count  int64
	}
	values := make([]rowValue, 0, 8)
	for rows.Next() {
		var (
			status string
			count  int64
		)
		if scanErr := rows.Scan(&status, &count); scanErr != nil {
			return scanErr
		}
		values = append(values, rowValue{
			status: status,
			count:  count,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	MemoryItemsTotal.Reset()
	for _, value := range values {
		SetMemoryItemsTotal(value.status, float64(value.count))
	}
	return nil
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

func hasMetricsSecret(r *http.Request, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	if r == nil {
		return false
	}
	if token := strings.TrimSpace(r.Header.Get("X-Metrics-Token")); token == expected {
		return true
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		token := strings.TrimSpace(authHeader[len("Bearer "):])
		return token == expected
	}
	return false
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

func (r *statusRecorder) Flush() {
	flusher, ok := r.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	pusher, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}
