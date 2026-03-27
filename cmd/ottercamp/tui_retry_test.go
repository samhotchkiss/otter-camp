package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithTUIRateLimitRetryErrRetries429(t *testing.T) {
	original := tuiRateLimitBackoffSchedule
	tuiRateLimitBackoffSchedule = []time.Duration{0, 0, 0}
	defer func() {
		tuiRateLimitBackoffSchedule = original
	}()

	attempts := 0
	err := withTUIRateLimitRetryErr(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return &cliAPIError{StatusCode: 429}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withTUIRateLimitRetryErr returned err=%v, want nil", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestWithTUIRateLimitRetryErrDoesNotRetryNon429(t *testing.T) {
	original := tuiRateLimitBackoffSchedule
	tuiRateLimitBackoffSchedule = []time.Duration{0, 0, 0}
	defer func() {
		tuiRateLimitBackoffSchedule = original
	}()

	want := errors.New("boom")
	attempts := 0
	err := withTUIRateLimitRetryErr(context.Background(), func() error {
		attempts++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("withTUIRateLimitRetryErr err=%v, want %v", err, want)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
