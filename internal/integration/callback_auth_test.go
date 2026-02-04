package integration

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type staticRegistry map[string]struct{}

func (r staticRegistry) IsRegistered(instance string) bool {
	_, ok := r[instance]
	return ok
}

func TestCallbackAuthSuccess(t *testing.T) {
	registry := staticRegistry{"sam-openclaw": {}}
	auth := NewCallbackAuthenticator("test-secret", registry)

	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	payload := []byte(`{"event":"task.updated"}`)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := SignCallback(payload, timestamp, "test-secret")

	err := auth.Verify(payload, signature, timestamp, "sam-openclaw")
	require.NoError(t, err)
}

func TestCallbackAuthSignatureValidation(t *testing.T) {
	registry := staticRegistry{"sam-openclaw": {}}
	auth := NewCallbackAuthenticator("test-secret", registry)

	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	payload := []byte(`{"event":"task.updated"}`)
	timestamp := strconv.FormatInt(now.Unix(), 10)

	cases := []struct {
		name      string
		signature string
		err       error
	}{
		{name: "missing signature", signature: "", err: ErrMissingSignature},
		{name: "invalid signature format", signature: "deadbeef", err: ErrInvalidSignature},
		{name: "signature mismatch", signature: SignCallback(payload, timestamp, "wrong-secret"), err: ErrSignatureMismatch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.Verify(payload, tc.signature, timestamp, "sam-openclaw")
			require.ErrorIs(t, err, tc.err)
		})
	}
}

func TestCallbackAuthTimestampValidation(t *testing.T) {
	registry := staticRegistry{"sam-openclaw": {}}
	auth := NewCallbackAuthenticator("test-secret", registry)

	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	payload := []byte(`{"event":"task.updated"}`)
	validTimestamp := strconv.FormatInt(now.Unix(), 10)
	signature := SignCallback(payload, validTimestamp, "test-secret")

	cases := []struct {
		name      string
		timestamp string
		err       error
	}{
		{name: "missing timestamp", timestamp: "", err: ErrMissingTimestamp},
		{name: "invalid timestamp", timestamp: "not-a-number", err: ErrInvalidTimestamp},
		{name: "expired timestamp", timestamp: strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10), err: ErrExpiredTimestamp},
		{name: "future timestamp", timestamp: strconv.FormatInt(now.Add(2*time.Minute).Unix(), 10), err: ErrFutureTimestamp},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.Verify(payload, signature, tc.timestamp, "sam-openclaw")
			require.ErrorIs(t, err, tc.err)
		})
	}
}

func TestCallbackAuthInstanceValidation(t *testing.T) {
	registry := staticRegistry{"sam-openclaw": {}}
	auth := NewCallbackAuthenticator("test-secret", registry)

	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	payload := []byte(`{"event":"task.updated"}`)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := SignCallback(payload, timestamp, "test-secret")

	cases := []struct {
		name     string
		instance string
		err      error
	}{
		{name: "missing instance", instance: "", err: ErrMissingInstance},
		{name: "unregistered instance", instance: "other-openclaw", err: ErrUnregisteredInstance},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.Verify(payload, signature, timestamp, tc.instance)
			require.ErrorIs(t, err, tc.err)
		})
	}
}
