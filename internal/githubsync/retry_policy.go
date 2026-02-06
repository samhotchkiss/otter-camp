package githubsync

import (
	"errors"
	"math"
	"math/rand"
	"net"
	"strings"
	"time"

	ghapi "github.com/samhotchkiss/otter-camp/internal/github"
)

const (
	RetryClassRateLimited = "rate_limited"
	RetryClassNetwork     = "network"
	RetryClassUpstream5xx = "upstream_5xx"
	RetryClassConflict    = "conflict"
	RetryClassTerminal    = "terminal"
	RetryClassTransient   = "transient"
)

type Classification struct {
	Class     string
	Retryable bool
}

type RetryDecision struct {
	Class         string
	Retryable     bool
	Exhausted     bool
	Delay         time.Duration
	NextAttemptAt *time.Time
}

type RetryPolicy struct {
	BaseDelay        time.Duration
	MaxDelay         time.Duration
	JitterFraction   float64
	MaxAttemptsByJob map[string]int
	random           func() float64
}

type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	if e == nil || e.Err == nil {
		return "permanent error"
	}
	return e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		BaseDelay:      time.Second,
		MaxDelay:       5 * time.Minute,
		JitterFraction: 0.2,
		MaxAttemptsByJob: map[string]int{
			"repo_sync":     6,
			"issue_import":  8,
			"webhook_event": 4,
		},
		random: rand.Float64,
	}
}

func (p RetryPolicy) WithRandom(randomFunc func() float64) RetryPolicy {
	p.random = randomFunc
	return p
}

func (p RetryPolicy) Decide(jobType string, attempt int, err error, now time.Time) RetryDecision {
	classification := ClassifyError(err)
	decision := RetryDecision{
		Class:     classification.Class,
		Retryable: classification.Retryable,
	}

	if !classification.Retryable {
		return decision
	}

	maxAttempts := p.maxAttempts(strings.TrimSpace(jobType))
	if attempt >= maxAttempts {
		decision.Exhausted = true
		return decision
	}

	delay := p.Backoff(attempt)
	nextAttempt := now.UTC().Add(delay)
	decision.Delay = delay
	decision.NextAttemptAt = &nextAttempt
	return decision
}

func (p RetryPolicy) Backoff(attempt int) time.Duration {
	randFunc := p.random
	if randFunc == nil {
		randFunc = rand.Float64
	}

	base := p.BaseDelay
	if base <= 0 {
		base = time.Second
	}
	max := p.MaxDelay
	if max <= 0 {
		max = 5 * time.Minute
	}
	jitter := p.JitterFraction
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}

	return computeBackoff(base, max, attempt, jitter, randFunc())
}

func (p RetryPolicy) maxAttempts(jobType string) int {
	if p.MaxAttemptsByJob == nil {
		return 5
	}
	if value, ok := p.MaxAttemptsByJob[jobType]; ok && value > 0 {
		return value
	}
	if value, ok := p.MaxAttemptsByJob["default"]; ok && value > 0 {
		return value
	}
	return 5
}

func ClassifyError(err error) Classification {
	if err == nil {
		return Classification{Class: RetryClassTransient, Retryable: true}
	}

	var permanentError *PermanentError
	if errors.As(err, &permanentError) {
		return Classification{Class: RetryClassTerminal, Retryable: false}
	}

	var rateLimitError *ghapi.RateLimitError
	if errors.As(err, &rateLimitError) {
		return Classification{Class: RetryClassRateLimited, Retryable: true}
	}

	var httpError *ghapi.HTTPError
	if errors.As(err, &httpError) {
		switch {
		case httpError.StatusCode >= 500:
			return Classification{Class: RetryClassUpstream5xx, Retryable: true}
		case httpError.StatusCode == 429:
			return Classification{Class: RetryClassRateLimited, Retryable: true}
		case httpError.StatusCode == 409 || httpError.StatusCode == 408 || httpError.StatusCode == 425:
			return Classification{Class: RetryClassConflict, Retryable: true}
		default:
			return Classification{Class: RetryClassTerminal, Retryable: false}
		}
	}

	var netError net.Error
	if errors.As(err, &netError) {
		if netError.Timeout() {
			return Classification{Class: RetryClassNetwork, Retryable: true}
		}
		return Classification{Class: RetryClassNetwork, Retryable: true}
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "timeout"),
		strings.Contains(message, "temporary"),
		strings.Contains(message, "connection reset"):
		return Classification{Class: RetryClassNetwork, Retryable: true}
	case strings.Contains(message, "rate limit"):
		return Classification{Class: RetryClassRateLimited, Retryable: true}
	default:
		return Classification{Class: RetryClassTransient, Retryable: true}
	}
}

func computeBackoff(base, max time.Duration, attempt int, jitterFraction, randomFactor float64) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}

	exponent := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(base) * exponent)
	if delay > max {
		delay = max
	}

	if jitterFraction <= 0 {
		return delay
	}
	if randomFactor < 0 {
		randomFactor = 0
	}
	if randomFactor > 1 {
		randomFactor = 1
	}

	jitterRange := float64(delay) * jitterFraction
	adjusted := float64(delay) - jitterRange + (2 * jitterRange * randomFactor)
	if adjusted < 0 {
		adjusted = 0
	}
	adjustedDelay := time.Duration(adjusted)
	if adjustedDelay > max {
		return max
	}
	return adjustedDelay
}
