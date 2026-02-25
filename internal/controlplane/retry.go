package controlplane

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/mcp"
)

type RetryTrigger string

const (
	RetryTriggerTransient RetryTrigger = "retry_transient"
)

type FailureClass string

const (
	FailureClassTransient      FailureClass = "transient"
	FailureClassPermanent      FailureClass = "permanent"
	FailureClassPolicyDenied   FailureClass = "policy_denied"
	FailureClassBudgetExceeded FailureClass = "budget_exceeded"
	FailureClassTimeout        FailureClass = "timeout"
)

var httpStatusPattern = regexp.MustCompile(`(?i)\bhttp status (\d{3})\b`)

type RetryDecider struct{}

func (RetryDecider) ShouldRetry(exec ToolExecution, attempt RunAttempt) (bool, RetryTrigger) {
	failureClass := normalizeFailureClassPointer(attempt.FailureClass)
	if failureClass != string(FailureClassTransient) {
		return false, ""
	}
	if attempt.AttemptNumber >= MaxAttemptsForDomain(exec.ToolDomain) {
		return false, ""
	}
	return true, RetryTriggerTransient
}

func (RetryDecider) ClassifyFailure(err error) FailureClass {
	if err == nil {
		return FailureClassPermanent
	}
	if errors.Is(err, ErrCapabilityDenied) || errors.Is(err, ErrAgentDenyList) {
		return FailureClassPolicyDenied
	}
	if errors.Is(err, ErrBudgetExceeded) {
		return FailureClassBudgetExceeded
	}
	if errors.Is(err, mcp.ErrCircuitOpen) {
		return FailureClassTransient
	}
	if isBrowserPermanentError(err) {
		return FailureClassPermanent
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, mcp.ErrTimeout) || isNetworkTimeout(err) {
		return FailureClassTransient
	}

	if status, ok := extractHTTPStatus(err); ok {
		if status == http.StatusTooManyRequests || status >= 500 {
			return FailureClassTransient
		}
		if status >= 400 {
			return FailureClassPermanent
		}
	}

	return FailureClassPermanent
}

func MaxAttemptsForDomain(domain string) int {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case "mcp":
		return 3
	case "browser", "cli":
		return 2
	case "native", "internal":
		return 1
	default:
		return 1
	}
}

// RetryBackoffDelay computes the caller-side retry delay with exponential backoff and jitter.
func RetryBackoffDelay(attemptNumber int, baseDelay time.Duration, retryAfterHeader string, now time.Time, randFloat float64) time.Duration {
	if attemptNumber <= 1 {
		return 0
	}
	if baseDelay <= 0 {
		baseDelay = 2 * time.Second
	}
	if retryAfter := parseRetryAfter(strings.TrimSpace(retryAfterHeader), now); retryAfter > 0 {
		return retryAfter
	}

	delay := baseDelay << (attemptNumber - 1)
	if delay < 0 {
		delay = baseDelay
	}

	if randFloat < 0 {
		randFloat = 0
	}
	if randFloat > 1 {
		randFloat = 1
	}
	factor := 0.5 + randFloat
	return time.Duration(float64(delay) * factor)
}

func parseRetryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(header); err == nil {
		wait := retryAt.Sub(now)
		if wait > 0 {
			return wait
		}
	}
	return 0
}

func isNetworkTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func extractHTTPStatus(err error) (int, bool) {
	matches := httpStatusPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(err.Error())))
	if len(matches) != 2 {
		return 0, false
	}
	status, convErr := strconv.Atoi(matches[1])
	if convErr != nil {
		return 0, false
	}
	return status, true
}

func normalizeFailureClassPointer(value *string) string {
	if value == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*value))
}

func isBrowserPermanentError(err error) bool {
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lower, "browser: session revoked") || strings.Contains(lower, "browser: domain policy denied")
}
