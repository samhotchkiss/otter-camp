package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testSecret = "test-webhook-secret-key"

func TestCallbackHMAC(t *testing.T) {
	// Test that valid HMAC signatures are accepted
	verifier := NewVerifier(testSecret)

	payload := []byte(`{"event":"task.updated","org_id":"12345"}`)
	signature := Sign(payload, testSecret)

	err := verifier.VerifySignature(payload, signature)
	require.NoError(t, err, "valid HMAC signature should be accepted")
}

func TestCallbackInvalidHMAC(t *testing.T) {
	verifier := NewVerifier(testSecret)

	payload := []byte(`{"event":"task.updated","org_id":"12345"}`)

	testCases := []struct {
		name      string
		signature string
		wantErr   error
	}{
		{
			name:      "missing signature",
			signature: "",
			wantErr:   ErrMissingSignature,
		},
		{
			name:      "missing prefix",
			signature: "deadbeefcafe",
			wantErr:   ErrInvalidSignature,
		},
		{
			name:      "invalid hex",
			signature: "sha256=notvalidhex!",
			wantErr:   ErrInvalidSignature,
		},
		{
			name:      "wrong signature",
			signature: "sha256=deadbeefcafe1234567890abcdef1234567890abcdef1234567890abcdef1234",
			wantErr:   ErrSignatureMismatch,
		},
		{
			name:      "tampered payload signature",
			signature: Sign([]byte(`{"event":"different"}`), testSecret),
			wantErr:   ErrSignatureMismatch,
		},
		{
			name:      "wrong secret",
			signature: Sign(payload, "wrong-secret"),
			wantErr:   ErrSignatureMismatch,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifier.VerifySignature(payload, tc.signature)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestCallbackExpiry(t *testing.T) {
	verifier := NewVerifier(testSecret)
	now := time.Now()
	verifier.now = func() time.Time { return now }

	testCases := []struct {
		name      string
		timestamp string
		wantErr   error
	}{
		{
			name:      "valid timestamp",
			timestamp: strconv.FormatInt(now.Add(-1*time.Minute).Unix(), 10),
			wantErr:   nil,
		},
		{
			name:      "missing timestamp",
			timestamp: "",
			wantErr:   ErrMissingTimestamp,
		},
		{
			name:      "invalid format",
			timestamp: "not-a-number",
			wantErr:   ErrInvalidTimestamp,
		},
		{
			name:      "expired 10 minutes ago",
			timestamp: strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10),
			wantErr:   ErrExpiredRequest,
		},
		{
			name:      "expired exactly at max age",
			timestamp: strconv.FormatInt(now.Add(-DefaultMaxAge-time.Second).Unix(), 10),
			wantErr:   ErrExpiredRequest,
		},
		{
			name:      "future timestamp (2 minutes)",
			timestamp: strconv.FormatInt(now.Add(2*time.Minute).Unix(), 10),
			wantErr:   ErrFutureRequest,
		},
		{
			name:      "slight future (30 seconds) is ok",
			timestamp: strconv.FormatInt(now.Add(30*time.Second).Unix(), 10),
			wantErr:   nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifier.VerifyTimestamp(tc.timestamp)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCallbackReplay(t *testing.T) {
	verifier := NewVerifier(testSecret)

	nonce := "unique-request-id-12345"

	// First use should succeed
	err := verifier.VerifyNonce(nonce)
	require.NoError(t, err, "first nonce use should succeed")

	// Second use should fail (replay attack)
	err = verifier.VerifyNonce(nonce)
	require.ErrorIs(t, err, ErrReplayedNonce, "replayed nonce should be rejected")

	// Different nonce should work
	err = verifier.VerifyNonce("different-nonce-67890")
	require.NoError(t, err, "different nonce should succeed")

	// Empty nonce should fail
	err = verifier.VerifyNonce("")
	require.ErrorIs(t, err, ErrMissingNonce, "empty nonce should be rejected")
}

func TestNonceStoreExpiry(t *testing.T) {
	// Test that nonces expire after the configured duration
	store := NewNonceStore(100 * time.Millisecond)

	now := time.Now()
	store.now = func() time.Time { return now }

	nonce := "test-nonce-expiry"

	// Record nonce
	require.False(t, store.HasSeen(nonce))
	store.Record(nonce)
	require.True(t, store.HasSeen(nonce))

	// Advance time past expiry and trigger cleanup
	now = now.Add(200 * time.Millisecond)
	store.cleanup = time.Time{} // force cleanup on next Record
	store.Record("trigger-cleanup")

	// Original nonce should be expired
	require.False(t, store.HasSeen(nonce), "nonce should be expired")
}

func TestVerifyRequest(t *testing.T) {
	verifier := NewVerifier(testSecret)
	now := time.Now()
	verifier.now = func() time.Time { return now }

	payload := []byte(`{"event":"test"}`)
	signature := Sign(payload, testSecret)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	nonce := "full-verify-nonce"

	// Full verification should pass
	err := verifier.VerifyRequest(payload, signature, timestamp, nonce)
	require.NoError(t, err)

	// Replay should fail
	err = verifier.VerifyRequest(payload, signature, timestamp, nonce)
	require.ErrorIs(t, err, ErrReplayedNonce)

	// Expired should fail
	expiredTimestamp := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	err = verifier.VerifyRequest(payload, signature, expiredTimestamp, "new-nonce-1")
	require.ErrorIs(t, err, ErrExpiredRequest)

	// Bad signature should fail
	err = verifier.VerifyRequest(payload, "sha256=bad", timestamp, "new-nonce-2")
	require.Error(t, err)
}

func TestMiddlewareRejectsInvalidSignature(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	middleware := NewMiddleware(testSecret)
	wrapped := middleware.Handler(handler)

	now := time.Now()
	payload := []byte(`{"event":"test"}`)

	testCases := []struct {
		name       string
		signature  string
		timestamp  string
		nonce      string
		wantStatus int
	}{
		{
			name:       "missing signature",
			signature:  "",
			timestamp:  strconv.FormatInt(now.Unix(), 10),
			nonce:      "nonce-1",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid signature",
			signature:  "sha256=invalid",
			timestamp:  strconv.FormatInt(now.Unix(), 10),
			nonce:      "nonce-2",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing timestamp",
			signature:  Sign(payload, testSecret),
			timestamp:  "",
			nonce:      "nonce-3",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "expired timestamp",
			signature:  Sign(payload, testSecret),
			timestamp:  strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10),
			nonce:      "nonce-4",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing nonce",
			signature:  Sign(payload, testSecret),
			timestamp:  strconv.FormatInt(now.Unix(), 10),
			nonce:      "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid request",
			signature:  Sign(payload, testSecret),
			timestamp:  strconv.FormatInt(now.Unix(), 10),
			nonce:      "nonce-valid",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
			req.Header.Set(SignatureHeader, tc.signature)
			req.Header.Set(TimestampHeader, tc.timestamp)
			req.Header.Set(NonceHeader, tc.nonce)

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)
		})
	}
}

func TestMiddlewareWithoutNonceVerification(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := NewMiddleware(testSecret).WithoutNonceVerification()
	wrapped := middleware.Handler(handler)

	now := time.Now()
	payload := []byte(`{"event":"test"}`)

	// Request without nonce should succeed when nonce verification is disabled
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set(SignatureHeader, Sign(payload, testSecret))
	req.Header.Set(TimestampHeader, strconv.FormatInt(now.Unix(), 10))
	// Intentionally not setting nonce

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMiddlewareWithDynamicSecret(t *testing.T) {
	secretValue := "initial-secret"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := NewMiddlewareWithSecretFunc(func() string {
		return secretValue
	}).WithoutNonceVerification()
	wrapped := middleware.Handler(handler)

	now := time.Now()
	payload := []byte(`{"event":"test"}`)
	timestamp := strconv.FormatInt(now.Unix(), 10)

	// Request with initial secret should work
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set(SignatureHeader, Sign(payload, "initial-secret"))
	req.Header.Set(TimestampHeader, timestamp)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Change secret
	secretValue = "new-secret"

	// Request with old secret should fail
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set(SignatureHeader, Sign(payload, "initial-secret"))
	req.Header.Set(TimestampHeader, timestamp)
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Request with new secret should work
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set(SignatureHeader, Sign(payload, "new-secret"))
	req.Header.Set(TimestampHeader, timestamp)
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMiddlewareBodyPreserved(t *testing.T) {
	var receivedBody []byte

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	})

	middleware := NewMiddleware(testSecret).WithoutNonceVerification()
	wrapped := middleware.Handler(handler)

	now := time.Now()
	payload := []byte(`{"event":"test","data":"important"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set(SignatureHeader, Sign(payload, testSecret))
	req.Header.Set(TimestampHeader, strconv.FormatInt(now.Unix(), 10))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, payload, receivedBody, "handler should receive original body")
}

func TestSignFunction(t *testing.T) {
	payload := []byte(`test payload`)
	secret := "my-secret"

	signature := Sign(payload, secret)

	// Should have correct prefix
	require.True(t, len(signature) > len(signaturePrefix))
	require.Equal(t, signaturePrefix, signature[:len(signaturePrefix)])

	// Should produce valid HMAC
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := signaturePrefix + hex.EncodeToString(mac.Sum(nil))
	require.Equal(t, expected, signature)
}

func TestTimingSafeComparison(t *testing.T) {
	// This test verifies we're using constant-time comparison.
	// While we can't directly test timing, we verify the implementation
	// uses subtle.ConstantTimeCompare by checking behavior is correct.
	verifier := NewVerifier(testSecret)

	payload := []byte(`test payload`)
	correctSig := Sign(payload, testSecret)

	// Generate signatures with varying number of matching bytes
	// All should fail with same error (no early exit behavior)
	wrongSigs := []string{
		"sha256=0000000000000000000000000000000000000000000000000000000000000000",
		"sha256=0" + correctSig[8:], // 1 byte wrong at start
		correctSig[:len(correctSig)-2] + "00", // 1 byte wrong at end
	}

	for i, sig := range wrongSigs {
		t.Run(fmt.Sprintf("wrong_sig_%d", i), func(t *testing.T) {
			err := verifier.VerifySignature(payload, sig)
			require.ErrorIs(t, err, ErrSignatureMismatch)
		})
	}

	// Correct signature should work
	err := verifier.VerifySignature(payload, correctSig)
	require.NoError(t, err)
}

func TestCustomErrorHandler(t *testing.T) {
	customCalled := false

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := NewMiddleware(testSecret).WithErrorHandler(func(w http.ResponseWriter, err error) {
		customCalled = true
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("custom error"))
	})
	wrapped := middleware.Handler(handler)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(`{}`)))
	// Missing signature should trigger error
	req.Header.Set(TimestampHeader, strconv.FormatInt(time.Now().Unix(), 10))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	require.True(t, customCalled, "custom error handler should be called")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "custom error", rec.Body.String())
}

func TestHandlerFunc(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}

	middleware := NewMiddleware(testSecret).WithoutNonceVerification()
	wrapped := middleware.HandlerFunc(handler)

	now := time.Now()
	payload := []byte(`{"event":"test"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set(SignatureHeader, Sign(payload, testSecret))
	req.Header.Set(TimestampHeader, strconv.FormatInt(now.Unix(), 10))

	rec := httptest.NewRecorder()
	wrapped(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", rec.Body.String())
}
