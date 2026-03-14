package gateway

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

func TestHealthCheckerClientErrorsDoNotDegradeConnection(t *testing.T) {
	health := NewHealthChecker()
	gateway := &LiveModelGateway{health: health}
	connectionID := uuid.New()

	for i := 0; i < 6; i++ {
		err, retryable := gateway.mapProviderError(connectionID, ProviderHTTPError{StatusCode: http.StatusBadRequest})
		if retryable {
			t.Fatalf("retryable for 400 = true, want false")
		}
		var providerErr ProviderHTTPError
		if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("err for 400 = %v, want provider 400 error", err)
		}
	}

	if state := health.GetState(connectionID); state != HealthStateHealthy {
		t.Fatalf("state after repeated 400 errors = %q, want %q", state, HealthStateHealthy)
	}

	err, retryable := gateway.mapProviderError(connectionID, ProviderHTTPError{StatusCode: http.StatusForbidden})
	if retryable {
		t.Fatalf("retryable for 403 = true, want false")
	}
	if !errors.Is(err, turn.ErrAuthFailed) {
		t.Fatalf("err for 403 = %v, want turn.ErrAuthFailed", err)
	}
	if state := health.GetState(connectionID); state != HealthStateUnavailable {
		t.Fatalf("state after 403 error = %q, want %q", state, HealthStateUnavailable)
	}
}

func TestMapProviderErrorServerAndNetworkErrorsRecordFailure(t *testing.T) {
	health := NewHealthChecker()
	gateway := &LiveModelGateway{health: health}

	serverConnectionID := uuid.New()
	for i := 0; i < 2; i++ {
		err, retryable := gateway.mapProviderError(serverConnectionID, ProviderHTTPError{StatusCode: http.StatusInternalServerError})
		if !retryable {
			t.Fatalf("retryable for 500 = false, want true")
		}
		if !errors.Is(err, turn.ErrModelTransient) {
			t.Fatalf("err for 500 = %v, want turn.ErrModelTransient", err)
		}
	}
	if state := health.GetState(serverConnectionID); state != HealthStateDegraded {
		t.Fatalf("state after repeated 500 errors = %q, want %q", state, HealthStateDegraded)
	}

	networkConnectionID := uuid.New()
	for i := 0; i < 2; i++ {
		err, retryable := gateway.mapProviderError(networkConnectionID, mockNetworkError{msg: "dial timeout"})
		if !retryable {
			t.Fatalf("retryable for network error = false, want true")
		}
		if !errors.Is(err, turn.ErrModelTransient) {
			t.Fatalf("err for network error = %v, want turn.ErrModelTransient", err)
		}
	}
	if state := health.GetState(networkConnectionID); state != HealthStateDegraded {
		t.Fatalf("state after repeated network errors = %q, want %q", state, HealthStateDegraded)
	}
}

func TestMapProviderErrorRateLimitMarksRateLimited(t *testing.T) {
	health := NewHealthChecker()
	gateway := &LiveModelGateway{health: health}
	connectionID := uuid.New()
	retryAfter := 45 * time.Second

	err, retryable := gateway.mapProviderError(connectionID, ProviderHTTPError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: retryAfter,
	})
	if !retryable {
		t.Fatalf("retryable for 429 = false, want true")
	}
	if !errors.Is(err, turn.ErrRateLimited) {
		t.Fatalf("err for 429 = %v, want turn.ErrRateLimited", err)
	}
	var retryHint interface {
		RateLimitRetryAfter() time.Duration
	}
	if !errors.As(err, &retryHint) {
		t.Fatalf("err for 429 = %v, want retry hint", err)
	}
	if got := retryHint.RateLimitRetryAfter(); got != retryAfter {
		t.Fatalf("retry_after = %s, want %s", got, retryAfter)
	}
	var providerErr ProviderHTTPError
	if !errors.As(err, &providerErr) {
		t.Fatalf("err for 429 = %v, want underlying ProviderHTTPError", err)
	}
	if providerErr.StatusCode != http.StatusTooManyRequests || providerErr.RetryAfter != retryAfter {
		t.Fatalf("provider err = %+v, want status=429 retry_after=%s", providerErr, retryAfter)
	}
	if state := health.GetState(connectionID); state != HealthStateRateLimited {
		t.Fatalf("state after 429 = %q, want %q", state, HealthStateRateLimited)
	}
}

func TestMapProviderErrorRateLimitKeepsConnectionLimitedForProviderWindow(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, time.February, 24, 14, 0, 0, 0, time.UTC)}
	health := newHealthCheckerWithClock(clock.Now)
	gateway := &LiveModelGateway{health: health}
	connectionID := uuid.New()
	retryAfter := 2 * time.Hour

	_, retryable := gateway.mapProviderError(connectionID, ProviderHTTPError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: retryAfter,
	})
	if !retryable {
		t.Fatal("retryable for 429 = false, want true")
	}

	clock.now = clock.now.Add(time.Hour)
	if state := health.GetState(connectionID); state != HealthStateRateLimited {
		t.Fatalf("state one hour into retry window = %q, want %q", state, HealthStateRateLimited)
	}

	clock.now = clock.now.Add(time.Hour)
	if state := health.GetState(connectionID); state != HealthStateDegraded {
		t.Fatalf("state after retry window elapsed = %q, want %q", state, HealthStateDegraded)
	}
}

type mockNetworkError struct {
	msg string
}

func (e mockNetworkError) Error() string {
	return e.msg
}

func (mockNetworkError) Timeout() bool {
	return false
}

func (mockNetworkError) Temporary() bool {
	return false
}
