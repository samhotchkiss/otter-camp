package dispatch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/webhook"
)

func TestWebhookDeliverySuccess(t *testing.T) {
	payload := []byte(`{"event":"task.dispatch"}`)
	secret := "test-secret"
	fixedTime := time.Unix(1700000000, 0)
	fixedNonce := "fixed-nonce"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != string(payload) {
			t.Fatalf("expected payload %q, got %q", payload, body)
		}

		expectedSig := webhook.Sign(payload, secret)
		if got := r.Header.Get(webhook.SignatureHeader); got != expectedSig {
			t.Fatalf("expected signature %q, got %q", expectedSig, got)
		}
		if got := r.Header.Get(webhook.TimestampHeader); got != "1700000000" {
			t.Fatalf("expected timestamp header, got %q", got)
		}
		if got := r.Header.Get(webhook.NonceHeader); got != fixedNonce {
			t.Fatalf("expected nonce header %q, got %q", fixedNonce, got)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := NewWebhookDispatcher(WebhookDispatcherOptions{
		Client: server.Client(),
		Secret: secret,
		Now:    func() time.Time { return fixedTime },
		Nonce:  func() string { return fixedNonce },
		Sleep:  func(time.Duration) {},
	})

	status, err := dispatcher.Deliver(context.Background(), DeliveryRequest{
		TaskID:  "task-1",
		URL:     server.URL,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if status.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", status.Attempts)
	}
	if !status.Delivered {
		t.Fatalf("expected delivered status")
	}

	if stored, ok := dispatcher.Status("task-1"); !ok || stored == nil {
		t.Fatalf("expected stored status for task")
	}
}

func TestWebhookDeliveryRetries(t *testing.T) {
	payload := []byte(`{"event":"task.dispatch"}`)
	var hits int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&hits, 1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var sleeps []time.Duration
	backoffs := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	backoff := func(retry int) time.Duration {
		if retry-1 < len(backoffs) {
			return backoffs[retry-1]
		}
		return time.Millisecond
	}

	dispatcher := NewWebhookDispatcher(WebhookDispatcherOptions{
		Client:     server.Client(),
		MaxRetries: 2,
		Backoff:    backoff,
		Sleep: func(d time.Duration) {
			sleeps = append(sleeps, d)
		},
	})

	status, err := dispatcher.Deliver(context.Background(), DeliveryRequest{
		TaskID:  "task-2",
		URL:     server.URL,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if status.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", status.Attempts)
	}
	if !status.Delivered {
		t.Fatalf("expected delivered status")
	}
	if len(sleeps) != 2 {
		t.Fatalf("expected 2 sleeps, got %d", len(sleeps))
	}
	if sleeps[0] != backoffs[0] || sleeps[1] != backoffs[1] {
		t.Fatalf("unexpected backoff sequence: %v", sleeps)
	}
}

func TestWebhookDeliveryFailsAfterRetries(t *testing.T) {
	payload := []byte(`{"event":"task.dispatch"}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	var sleeps []time.Duration
	dispatcher := NewWebhookDispatcher(WebhookDispatcherOptions{
		Client:     server.Client(),
		MaxRetries: 2,
		Backoff:    func(retry int) time.Duration { return time.Millisecond },
		Sleep: func(d time.Duration) {
			sleeps = append(sleeps, d)
		},
	})

	status, err := dispatcher.Deliver(context.Background(), DeliveryRequest{
		TaskID:  "task-3",
		URL:     server.URL,
		Payload: payload,
	})
	if err == nil {
		t.Fatalf("expected error after retries")
	}
	if status.Delivered {
		t.Fatalf("expected delivery failure")
	}
	if status.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", status.Attempts)
	}
	if len(sleeps) != 2 {
		t.Fatalf("expected 2 sleeps, got %d", len(sleeps))
	}
}
