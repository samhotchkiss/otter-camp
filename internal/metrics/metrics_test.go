package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsHandlerExposesRequiredMetricFamilies(t *testing.T) {
	resetMetricsForTests()

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

	RecordModelTokens("openai", "gpt-4o", 100, 50, 0)
	SetJobQueueDepth("memory_extract_turn", "pending", 3)
	SetMemoryItemsTotal("candidate", 8)
	RecordAgentTurn("started")
	RecordAgentTurn("completed")
	RecordAgentTurnDispatchSuppressed("duplicate_enqueue")

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	Handler().ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", metricsRec.Code, http.StatusOK)
	}

	body := metricsRec.Body.String()
	required := []string{
		`ottercamp_api_requests_total{method="GET",path="/ping",status="200"} 1`,
		`ottercamp_api_request_duration_seconds_count{method="GET",path="/ping"} 1`,
		`ottercamp_model_tokens_total{model="gpt-4o",provider="openai",type="input"} 100`,
		`ottercamp_model_tokens_total{model="gpt-4o",provider="openai",type="output"} 50`,
		`ottercamp_job_queue_depth{job_type="memory_extract_turn",status="pending"} 3`,
		`ottercamp_memory_items_total{status="candidate"} 8`,
		`ottercamp_agent_turns_total{status="started"} 1`,
		`ottercamp_agent_turns_total{status="completed"} 1`,
		`ottercamp_agent_turn_dispatch_suppressed_total{reason="duplicate_enqueue"} 1`,
	}
	for _, line := range required {
		if !strings.Contains(body, line) {
			t.Fatalf("metrics body missing line %q", line)
		}
	}
}

func TestHTTPMiddlewarePreservesFlusher(t *testing.T) {
	wrapped := HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped writer does not implement http.Flusher")
		}
		flusher.Flush()
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	rec := &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	wrapped.ServeHTTP(rec, req)

	if !rec.flushed {
		t.Fatal("expected wrapped flusher to delegate Flush to underlying response writer")
	}
}

func TestRefreshJobQueueDepthRowsErrKeepsPreviousValues(t *testing.T) {
	resetMetricsForTests()
	SetJobQueueDepth("build", "pending", 9)

	rows := &fakeRows{
		values:    [][]any{{"build", "pending", int64(3)}},
		rowsErr:   errors.New("rows failed"),
		scanErrAt: -1,
	}
	err := refreshJobQueueDepth(context.Background(), queryFromRows(rows, nil))
	if err == nil {
		t.Fatal("expected refreshJobQueueDepth to return error")
	}

	if got := testutil.ToFloat64(JobQueueDepth.WithLabelValues("build", "pending")); got != 9 {
		t.Fatalf("job_queue_depth changed on rows.Err, got=%v want=9", got)
	}
}

func TestRefreshJobQueueDepthScanErrKeepsPreviousValues(t *testing.T) {
	resetMetricsForTests()
	SetJobQueueDepth("build", "pending", 9)
	SetJobQueueDepth("deploy", "queued", 4)

	rows := &fakeRows{
		values: [][]any{
			{"build", "pending", int64(2)},
			{"deploy", "queued", int64(1)},
		},
		scanErrAt: 1,
		scanErr:   errors.New("scan failed"),
	}
	err := refreshJobQueueDepth(context.Background(), queryFromRows(rows, nil))
	if err == nil {
		t.Fatal("expected refreshJobQueueDepth to return error")
	}

	if got := testutil.ToFloat64(JobQueueDepth.WithLabelValues("build", "pending")); got != 9 {
		t.Fatalf("build/pending changed on scan error, got=%v want=9", got)
	}
	if got := testutil.ToFloat64(JobQueueDepth.WithLabelValues("deploy", "queued")); got != 4 {
		t.Fatalf("deploy/queued changed on scan error, got=%v want=4", got)
	}
}

func TestRefreshMemoryItemsRowsErrKeepsPreviousValues(t *testing.T) {
	resetMetricsForTests()
	SetMemoryItemsTotal("candidate", 7)

	rows := &fakeRows{
		values:    [][]any{{"candidate", int64(2)}},
		rowsErr:   errors.New("rows failed"),
		scanErrAt: -1,
	}
	err := refreshMemoryItems(context.Background(), queryFromRows(rows, nil))
	if err == nil {
		t.Fatal("expected refreshMemoryItems to return error")
	}

	if got := testutil.ToFloat64(MemoryItemsTotal.WithLabelValues("candidate")); got != 7 {
		t.Fatalf("memory_items_total changed on rows.Err, got=%v want=7", got)
	}
}

func TestRefreshMemoryItemsScanErrKeepsPreviousValues(t *testing.T) {
	resetMetricsForTests()
	SetMemoryItemsTotal("candidate", 7)
	SetMemoryItemsTotal("active", 5)

	rows := &fakeRows{
		values: [][]any{
			{"candidate", int64(2)},
			{"active", int64(1)},
		},
		scanErrAt: 1,
		scanErr:   errors.New("scan failed"),
	}
	err := refreshMemoryItems(context.Background(), queryFromRows(rows, nil))
	if err == nil {
		t.Fatal("expected refreshMemoryItems to return error")
	}

	if got := testutil.ToFloat64(MemoryItemsTotal.WithLabelValues("candidate")); got != 7 {
		t.Fatalf("candidate changed on scan error, got=%v want=7", got)
	}
	if got := testutil.ToFloat64(MemoryItemsTotal.WithLabelValues("active")); got != 5 {
		t.Fatalf("active changed on scan error, got=%v want=5", got)
	}
}

type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (r *flusherRecorder) Flush() {
	r.flushed = true
	r.ResponseRecorder.Flush()
}

func resetMetricsForTests() {
	APIRequestsTotal.Reset()
	APIRequestDurationSeconds.Reset()
	ModelTokensTotal.Reset()
	JobQueueDepth.Reset()
	MemoryItemsTotal.Reset()
	AgentTurnsTotal.Reset()
	AgentTurnDispatchSuppressedTotal.Reset()
}

func queryFromRows(rows metricsRows, queryErr error) metricsQueryFunc {
	return func(context.Context, string, ...any) (metricsRows, error) {
		if queryErr != nil {
			return nil, queryErr
		}
		return rows, nil
	}
}

type fakeRows struct {
	values    [][]any
	index     int
	rowsErr   error
	scanErrAt int
	scanErr   error
}

func (r *fakeRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	rowIndex := r.index - 1
	if r.scanErrAt >= 0 && rowIndex == r.scanErrAt {
		if r.scanErr != nil {
			return r.scanErr
		}
		return errors.New("scan failed")
	}
	if rowIndex < 0 || rowIndex >= len(r.values) {
		return errors.New("scan called with invalid row index")
	}
	row := r.values[rowIndex]
	if len(dest) > len(row) {
		return fmt.Errorf("destination len %d exceeds row len %d", len(dest), len(row))
	}
	for i := range dest {
		switch ptr := dest[i].(type) {
		case *string:
			value, ok := row[i].(string)
			if !ok {
				return fmt.Errorf("row[%d] type %T is not string", i, row[i])
			}
			*ptr = value
		case *int64:
			switch value := row[i].(type) {
			case int64:
				*ptr = value
			case int:
				*ptr = int64(value)
			default:
				return fmt.Errorf("row[%d] type %T is not int64-compatible", i, row[i])
			}
		default:
			return fmt.Errorf("unsupported scan destination type %T", dest[i])
		}
	}
	return nil
}

func (r *fakeRows) Err() error {
	return r.rowsErr
}

func (r *fakeRows) Close() {}
