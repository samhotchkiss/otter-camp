package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoParsesRateLimitHeadersAndComputesBudget(t *testing.T) {
	now := time.Date(2026, 2, 6, 10, 0, 0, 0, time.UTC)
	resetAt := now.Add(10 * time.Minute)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL,
		WithClock(func() time.Time { return now }),
		WithJobBudgets(map[JobType]JobBudget{
			JobTypeImport: {MaxRequests: 20, ReserveRequests: 10},
		}),
	)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	request, err := client.NewRequest(context.Background(), http.MethodGet, "/repos/test/issues", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}

	response, err := client.Do(context.Background(), JobTypeImport, request)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}

	if response.RateLimit.Limit != 5000 || response.RateLimit.Remaining != 4999 {
		t.Fatalf("unexpected rate limit: %+v", response.RateLimit)
	}
	if !response.RateLimit.ResetAt.Equal(resetAt) {
		t.Fatalf("expected reset %s, got %s", resetAt, response.RateLimit.ResetAt)
	}

	budget := client.BudgetState(JobTypeImport)
	if budget.Used != 1 {
		t.Fatalf("expected used=1, got %d", budget.Used)
	}
	if budget.JobRemaining != 19 {
		t.Fatalf("expected job remaining=19, got %d", budget.JobRemaining)
	}
	if budget.APIRemaining != 4989 {
		t.Fatalf("expected api remaining=4989, got %d", budget.APIRemaining)
	}
	if budget.EffectiveRemaining != 19 {
		t.Fatalf("expected effective remaining=19, got %d", budget.EffectiveRemaining)
	}
}

func TestDoReturnsRateLimitErrorFor403And429(t *testing.T) {
	now := time.Date(2026, 2, 6, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		status    int
		headers   map[string]string
		body      string
		secondary bool
		wait      time.Duration
	}{
		{
			name:   "429 uses retry-after",
			status: http.StatusTooManyRequests,
			headers: map[string]string{
				"Retry-After": "2",
			},
			body:      `{"message":"rate limit exceeded"}`,
			secondary: false,
			wait:      2 * time.Second,
		},
		{
			name:   "403 secondary limit uses retry-after",
			status: http.StatusForbidden,
			headers: map[string]string{
				"Retry-After": "3",
			},
			body:      `{"message":"You have exceeded a secondary rate limit."}`,
			secondary: true,
			wait:      3 * time.Second,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for key, value := range tc.headers {
					w.Header().Set(key, value)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client, err := NewClient(server.URL, WithClock(func() time.Time { return now }))
			if err != nil {
				t.Fatalf("NewClient error: %v", err)
			}

			request, err := client.NewRequest(context.Background(), http.MethodGet, "/repos/test/issues", nil)
			if err != nil {
				t.Fatalf("NewRequest error: %v", err)
			}

			_, err = client.Do(context.Background(), JobTypeSync, request)
			if err == nil {
				t.Fatalf("expected rate limit error")
			}

			var rateLimitError *RateLimitError
			if !errors.As(err, &rateLimitError) {
				t.Fatalf("expected RateLimitError, got %T (%v)", err, err)
			}
			if rateLimitError.Secondary != tc.secondary {
				t.Fatalf("expected secondary=%v, got %v", tc.secondary, rateLimitError.Secondary)
			}
			if rateLimitError.RetryAfter != tc.wait {
				t.Fatalf("expected retry=%s, got %s", tc.wait, rateLimitError.RetryAfter)
			}
		})
	}
}

func TestFetchNextPagePausesAndResumesWithoutDataLoss(t *testing.T) {
	now := time.Date(2026, 2, 6, 10, 0, 0, 0, time.UTC)
	var currentTime atomic.Int64
	currentTime.Store(now.Unix())

	resetAt := now.Add(5 * time.Minute)
	page2Hits := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RawQuery {
		case "page=1":
			w.Header().Set("Link", "</items?page=2>; rel=\"next\"")
			w.Header().Set("X-RateLimit-Remaining", "1")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`["item-1"]`))
		case "page=2":
			page2Hits.Add(1)
			w.Header().Set("Link", "</items?page=3>; rel=\"next\"")
			w.Header().Set("X-RateLimit-Remaining", "100")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`["item-2"]`))
		case "page=3":
			w.Header().Set("X-RateLimit-Remaining", "99")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`["item-3"]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL,
		WithClock(func() time.Time { return time.Unix(currentTime.Load(), 0).UTC() }),
		WithJobBudgets(map[JobType]JobBudget{
			JobTypeImport: {MaxRequests: 10, ReserveRequests: 1},
		}),
	)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	checkpoint := PaginationCheckpoint{}
	collected := make([]string, 0, 3)

	response, updatedCheckpoint, err := client.FetchNextPage(context.Background(), JobTypeImport, checkpoint, "/items?page=1")
	if err != nil {
		t.Fatalf("fetch page 1 error: %v", err)
	}
	checkpoint = updatedCheckpoint
	decodeItems(t, response.Body, &collected)

	_, updatedCheckpoint, err = client.FetchNextPage(context.Background(), JobTypeImport, checkpoint, "/items?page=1")
	checkpoint = updatedCheckpoint
	if err == nil {
		t.Fatalf("expected pause error before page 2")
	}
	var pauseError *PauseError
	if !errors.As(err, &pauseError) {
		t.Fatalf("expected PauseError, got %T (%v)", err, err)
	}
	if checkpoint.PausedUntil == nil {
		t.Fatalf("expected paused checkpoint")
	}
	if page2Hits.Load() != 0 {
		t.Fatalf("expected page 2 to be deferred, hits=%d", page2Hits.Load())
	}

	currentTime.Store(resetAt.Add(1 * time.Second).Unix())

	response, updatedCheckpoint, err = client.FetchNextPage(context.Background(), JobTypeImport, checkpoint, "/items?page=1")
	if err != nil {
		t.Fatalf("fetch page 2 error after resume: %v", err)
	}
	checkpoint = updatedCheckpoint
	decodeItems(t, response.Body, &collected)

	response, updatedCheckpoint, err = client.FetchNextPage(context.Background(), JobTypeImport, checkpoint, "/items?page=1")
	if err != nil {
		t.Fatalf("fetch page 3 error: %v", err)
	}
	checkpoint = updatedCheckpoint
	decodeItems(t, response.Body, &collected)

	if checkpoint.NextURL != "" {
		t.Fatalf("expected empty next url after final page, got %q", checkpoint.NextURL)
	}
	if len(collected) != 3 {
		t.Fatalf("expected 3 collected items, got %d", len(collected))
	}
	if collected[0] != "item-1" || collected[1] != "item-2" || collected[2] != "item-3" {
		t.Fatalf("unexpected items: %v", collected)
	}
	if page2Hits.Load() != 1 {
		t.Fatalf("expected page 2 to be fetched exactly once, got %d", page2Hits.Load())
	}
}

func TestWebhookBudgetDefersSafelyWhenExhausted(t *testing.T) {
	hits := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithJobBudgets(map[JobType]JobBudget{
		JobTypeWebhook: {MaxRequests: 2, ReserveRequests: 0},
	}))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	for i := 0; i < 2; i++ {
		request, err := client.NewRequest(context.Background(), http.MethodGet, "/events", nil)
		if err != nil {
			t.Fatalf("NewRequest error: %v", err)
		}
		if _, err := client.Do(context.Background(), JobTypeWebhook, request); err != nil {
			t.Fatalf("unexpected webhook request error on attempt %d: %v", i+1, err)
		}
	}

	request, err := client.NewRequest(context.Background(), http.MethodGet, "/events", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	_, err = client.Do(context.Background(), JobTypeWebhook, request)
	if err == nil {
		t.Fatalf("expected budget exceeded error")
	}

	var budgetExceededError *BudgetExceededError
	if !errors.As(err, &budgetExceededError) {
		t.Fatalf("expected BudgetExceededError, got %T (%v)", err, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected only two requests to reach server, got %d", hits.Load())
	}
}

func decodeItems(t *testing.T, body []byte, out *[]string) {
	t.Helper()

	var items []string
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode page body: %v", err)
	}
	*out = append(*out, items...)
}
