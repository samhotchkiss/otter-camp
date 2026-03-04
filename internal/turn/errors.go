package turn

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrAuthFailed  = errors.New("model provider authentication failed")
	ErrRateLimited = errors.New("model provider rate limited")
)

// RateLimitedError preserves provider retry timing while still matching
// ErrRateLimited via errors.Is.
type RateLimitedError struct {
	RetryAfter time.Duration
	Cause      error
}

func NewRateLimitedError(retryAfter time.Duration, cause error) error {
	if retryAfter < 0 {
		retryAfter = 0
	}
	return RateLimitedError{
		RetryAfter: retryAfter,
		Cause:      cause,
	}
}

func (e RateLimitedError) Error() string {
	base := ErrRateLimited.Error()
	if e.RetryAfter > 0 {
		base = fmt.Sprintf("%s (retry_after=%s)", base, e.RetryAfter.Round(time.Second))
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", base, e.Cause)
	}
	return base
}

func (e RateLimitedError) Unwrap() []error {
	errs := []error{ErrRateLimited}
	if e.Cause != nil {
		errs = append(errs, e.Cause)
	}
	return errs
}

func (e RateLimitedError) RateLimitRetryAfter() time.Duration {
	if e.RetryAfter < 0 {
		return 0
	}
	return e.RetryAfter
}
