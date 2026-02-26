package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMetricsHandlerExposesRequiredMetricFamilies(t *testing.T) {
	router := chi.NewRouter()
	router.Use(HTTPMiddleware())
	router.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ping status = %d, want %d", rec.Code, http.StatusOK)
	}

	RecordModelTokens("openai", "gpt-4o-mini", 11, 5, 2)
	SetJobQueueDepth("memory_extract_turn", "pending", 3)
	SetMemoryItemsTotal("candidate", 8)
	RecordAgentTurn("completed")

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	Handler().ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", metricsRec.Code, http.StatusOK)
	}

	body := metricsRec.Body.String()
	required := []string{
		"ottercamp_api_requests_total",
		"ottercamp_api_request_duration_seconds",
		"ottercamp_model_tokens_total",
		"ottercamp_job_queue_depth",
		"ottercamp_memory_items_total",
		"ottercamp_agent_turns_total",
	}
	for _, metricName := range required {
		if !strings.Contains(body, metricName) {
			t.Fatalf("metrics body missing %q", metricName)
		}
	}
}
