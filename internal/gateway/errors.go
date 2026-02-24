package gateway

import (
	"errors"
	"fmt"
	"net"
	"time"
)

var (
	ErrQueueTimeout        = errors.New("gateway queue timeout")
	ErrNoHealthyConnection = errors.New("no healthy provider connection available")
)

type ProviderHTTPError struct {
	StatusCode int
	RetryAfter time.Duration
	Err        error
}

func (e ProviderHTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("provider http %d: %v", e.StatusCode, e.Err)
	}
	return fmt.Sprintf("provider http %d", e.StatusCode)
}

func (e ProviderHTTPError) Unwrap() error {
	return e.Err
}

func isRetryableGatewayError(err error) bool {
	if err == nil {
		return false
	}

	var providerErr ProviderHTTPError
	if errors.As(err, &providerErr) {
		if providerErr.StatusCode == 429 {
			return true
		}
		return providerErr.StatusCode >= 500
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isRateLimitedError(err error) (time.Duration, bool) {
	var providerErr ProviderHTTPError
	if !errors.As(err, &providerErr) {
		return 0, false
	}
	if providerErr.StatusCode != 429 {
		return 0, false
	}
	return providerErr.RetryAfter, true
}

func isServerError(err error) bool {
	var providerErr ProviderHTTPError
	return errors.As(err, &providerErr) && providerErr.StatusCode >= 500
}

func retryDelay(err error, retryNumber int) time.Duration {
	if retryAfter, ok := isRateLimitedError(err); ok {
		if retryAfter > 0 {
			return retryAfter
		}
		return 2 * time.Second
	}

	base := 500 * time.Millisecond
	if retryNumber > 1 {
		base = 1 * time.Second
	}
	return base
}
