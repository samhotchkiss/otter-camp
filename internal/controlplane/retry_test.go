package controlplane

import (
	"context"
	"errors"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/mcp"
)

func TestRetryDeciderShouldRetry(t *testing.T) {
	decider := RetryDecider{}
	cases := []struct {
		name        string
		domain      string
		attempt     int
		failure     string
		wantRetry   bool
		wantTrigger RetryTrigger
	}{
		{name: "policy denied never retries", domain: "mcp", attempt: 1, failure: "policy_denied", wantRetry: false, wantTrigger: ""},
		{name: "budget exceeded never retries", domain: "mcp", attempt: 1, failure: "budget_exceeded", wantRetry: false, wantTrigger: ""},
		{name: "permanent never retries", domain: "browser", attempt: 1, failure: "permanent", wantRetry: false, wantTrigger: ""},
		{name: "timeout class does not retry", domain: "cli", attempt: 1, failure: "timeout", wantRetry: false, wantTrigger: ""},
		{name: "native attempt two exceeds max one", domain: "native", attempt: 2, failure: "transient", wantRetry: false, wantTrigger: ""},
		{name: "mcp attempt two within max three retries", domain: "mcp", attempt: 2, failure: "transient", wantRetry: true, wantTrigger: RetryTriggerTransient},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := tc.failure
			retry, trigger := decider.ShouldRetry(ToolExecution{ToolDomain: tc.domain}, RunAttempt{
				AttemptNumber: tc.attempt,
				FailureClass:  &failure,
			})
			if retry != tc.wantRetry {
				t.Fatalf("ShouldRetry retry = %v, want %v", retry, tc.wantRetry)
			}
			if trigger != tc.wantTrigger {
				t.Fatalf("ShouldRetry trigger = %q, want %q", trigger, tc.wantTrigger)
			}
		})
	}
}

func TestRetryDeciderClassifyFailure(t *testing.T) {
	decider := RetryDecider{}
	if got := decider.ClassifyFailure(errors.New("http status 429: too many requests")); got != FailureClassTransient {
		t.Fatalf("ClassifyFailure(429) = %q, want transient", got)
	}
	if got := decider.ClassifyFailure(errors.New("http status 400: bad request")); got != FailureClassPermanent {
		t.Fatalf("ClassifyFailure(400) = %q, want permanent", got)
	}
	if got := decider.ClassifyFailure(mcp.ErrCircuitOpen); got != FailureClassTransient {
		t.Fatalf("ClassifyFailure(ErrCircuitOpen) = %q, want transient", got)
	}
	if got := decider.ClassifyFailure(errors.New("unknown failure")); got != FailureClassPermanent {
		t.Fatalf("ClassifyFailure(unknown) = %q, want permanent", got)
	}
	if got := decider.ClassifyFailure(context.DeadlineExceeded); got != FailureClassTransient {
		t.Fatalf("ClassifyFailure(context.DeadlineExceeded) = %q, want transient", got)
	}
	if got := decider.ClassifyFailure(ErrCapabilityDenied); got != FailureClassPolicyDenied {
		t.Fatalf("ClassifyFailure(ErrCapabilityDenied) = %q, want policy_denied", got)
	}
}
