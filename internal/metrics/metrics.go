package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registerOnce sync.Once

	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_http_requests_total",
		Help: "Total HTTP requests.",
	}, []string{"method", "path", "status_code"})
	HTTPRequestDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ottercamp_http_request_duration_seconds",
		Help:    "HTTP request durations in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
	ModelInvocationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_model_invocations_total",
		Help: "Total model invocations.",
	}, []string{"provider", "purpose", "status"})
	ModelTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_model_tokens_total",
		Help: "Total model tokens.",
	}, []string{"provider", "direction"})
	RunCreatedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_run_created_total",
		Help: "Total runs created.",
	}, []string{"domain", "principal_type"})
	RunDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ottercamp_run_duration_seconds",
		Help:    "Run duration seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"domain", "status"})
	JobQueueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ottercamp_job_queue_depth",
		Help: "Current job queue depth.",
	}, []string{"priority"})
	ActiveBrowserSessions = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ottercamp_active_browser_sessions",
		Help: "Number of active browser sessions.",
	})
	ActiveRuns = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ottercamp_active_runs",
		Help: "Number of active runs.",
	})
	MemoryExtractionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ottercamp_memory_extraction_total",
		Help: "Total memory extraction stage outcomes.",
	}, []string{"stage", "outcome"})
)

func Register() {
	registerOnce.Do(func() {
		prometheus.MustRegister(
			HTTPRequestsTotal,
			HTTPRequestDurationSeconds,
			ModelInvocationsTotal,
			ModelTokensTotal,
			RunCreatedTotal,
			RunDurationSeconds,
			JobQueueDepth,
			ActiveBrowserSessions,
			ActiveRuns,
			MemoryExtractionTotal,
		)
	})
}

func Handler() http.Handler {
	Register()
	return promhttp.Handler()
}
