// Package webhook provides authentication and verification for incoming webhooks.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// SignatureHeader is the HTTP header containing the HMAC signature.
	SignatureHeader = "X-OpenClaw-Signature"

	// TimestampHeader is the HTTP header containing the request timestamp.
	TimestampHeader = "X-OpenClaw-Timestamp"

	// NonceHeader is the HTTP header containing the unique request nonce.
	NonceHeader = "X-OpenClaw-Nonce"

	// DefaultMaxAge is the default maximum age for webhook requests (5 minutes).
	DefaultMaxAge = 5 * time.Minute

	// NonceExpiry is how long to remember nonces for replay prevention.
	NonceExpiry = 10 * time.Minute

	// signaturePrefix is the expected prefix for HMAC-SHA256 signatures.
	signaturePrefix = "sha256="
)

var (
	// ErrMissingSignature is returned when the signature header is missing.
	ErrMissingSignature = errors.New("missing signature header")

	// ErrInvalidSignature is returned when the signature format is invalid.
	ErrInvalidSignature = errors.New("invalid signature format")

	// ErrSignatureMismatch is returned when the signature doesn't match.
	ErrSignatureMismatch = errors.New("signature mismatch")

	// ErrMissingTimestamp is returned when the timestamp header is missing.
	ErrMissingTimestamp = errors.New("missing timestamp header")

	// ErrInvalidTimestamp is returned when the timestamp format is invalid.
	ErrInvalidTimestamp = errors.New("invalid timestamp format")

	// ErrExpiredRequest is returned when the request timestamp is too old.
	ErrExpiredRequest = errors.New("request expired")

	// ErrFutureRequest is returned when the request timestamp is in the future.
	ErrFutureRequest = errors.New("request timestamp in future")

	// ErrMissingNonce is returned when the nonce header is missing.
	ErrMissingNonce = errors.New("missing nonce header")

	// ErrReplayedNonce is returned when a nonce has been seen before.
	ErrReplayedNonce = errors.New("replayed nonce detected")
)

// Verifier verifies HMAC signatures on incoming webhook requests.
type Verifier struct {
	secret     []byte
	maxAge     time.Duration
	nonceStore *NonceStore
	now        func() time.Time // for testing
}

// NewVerifier creates a new webhook signature verifier.
func NewVerifier(secret string) *Verifier {
	return &Verifier{
		secret:     []byte(secret),
		maxAge:     DefaultMaxAge,
		nonceStore: NewNonceStore(NonceExpiry),
		now:        time.Now,
	}
}

// WithMaxAge sets the maximum age for webhook requests.
func (v *Verifier) WithMaxAge(maxAge time.Duration) *Verifier {
	v.maxAge = maxAge
	return v
}

// VerifySignature verifies the HMAC signature of a payload.
// It uses timing-safe comparison to prevent timing attacks.
func (v *Verifier) VerifySignature(payload []byte, signature string) error {
	if signature == "" {
		return ErrMissingSignature
	}

	if !strings.HasPrefix(signature, signaturePrefix) {
		return ErrInvalidSignature
	}

	providedSig := strings.TrimPrefix(signature, signaturePrefix)
	providedBytes, err := hex.DecodeString(providedSig)
	if err != nil {
		return ErrInvalidSignature
	}

	expectedSig := v.computeSignature(payload)

	// Use subtle.ConstantTimeCompare for timing-safe comparison
	if subtle.ConstantTimeCompare(providedBytes, expectedSig) != 1 {
		return ErrSignatureMismatch
	}

	return nil
}

// VerifyTimestamp verifies that the request timestamp is within acceptable bounds.
func (v *Verifier) VerifyTimestamp(timestampStr string) error {
	if timestampStr == "" {
		return ErrMissingTimestamp
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}

	requestTime := time.Unix(timestamp, 0)
	now := v.now()

	// Check if request is too old
	if now.Sub(requestTime) > v.maxAge {
		return ErrExpiredRequest
	}

	// Check if request is from the future (with small tolerance)
	if requestTime.Sub(now) > time.Minute {
		return ErrFutureRequest
	}

	return nil
}

// VerifyNonce checks if a nonce has been used before and records it.
func (v *Verifier) VerifyNonce(nonce string) error {
	if nonce == "" {
		return ErrMissingNonce
	}

	if v.nonceStore.HasSeen(nonce) {
		return ErrReplayedNonce
	}

	v.nonceStore.Record(nonce)
	return nil
}

// VerifyRequest performs full verification of a webhook request:
// signature, timestamp, and nonce (replay prevention).
func (v *Verifier) VerifyRequest(payload []byte, signature, timestamp, nonce string) error {
	// Verify timestamp first (fast check)
	if err := v.VerifyTimestamp(timestamp); err != nil {
		return err
	}

	// Verify signature (cryptographic check)
	if err := v.VerifySignature(payload, signature); err != nil {
		return err
	}

	// Verify nonce (replay prevention)
	if err := v.VerifyNonce(nonce); err != nil {
		return err
	}

	return nil
}

// computeSignature computes the HMAC-SHA256 signature for a payload.
func (v *Verifier) computeSignature(payload []byte) []byte {
	mac := hmac.New(sha256.New, v.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

// NonceStore tracks seen nonces for replay prevention.
type NonceStore struct {
	mu      sync.RWMutex
	nonces  map[string]time.Time
	expiry  time.Duration
	now     func() time.Time // for testing
	cleanup time.Time        // last cleanup time
}

// NewNonceStore creates a new nonce store with the given expiry duration.
func NewNonceStore(expiry time.Duration) *NonceStore {
	return &NonceStore{
		nonces: make(map[string]time.Time),
		expiry: expiry,
		now:    time.Now,
	}
}

// HasSeen returns true if the nonce has been seen before (within expiry window).
func (s *NonceStore) HasSeen(nonce string) bool {
	s.mu.RLock()
	_, exists := s.nonces[nonce]
	s.mu.RUnlock()
	return exists
}

// Record records a nonce as seen. It also performs periodic cleanup.
func (s *NonceStore) Record(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.nonces[nonce] = now

	// Cleanup expired nonces periodically (every minute)
	if now.Sub(s.cleanup) > time.Minute {
		s.cleanupLocked(now)
		s.cleanup = now
	}
}

// cleanupLocked removes expired nonces. Must be called with mu held.
func (s *NonceStore) cleanupLocked(now time.Time) {
	for nonce, recorded := range s.nonces {
		if now.Sub(recorded) > s.expiry {
			delete(s.nonces, nonce)
		}
	}
}

// Middleware returns an HTTP middleware that verifies webhook signatures.
// It reads the request body, verifies the signature, and rejects invalid requests.
type Middleware struct {
	verifier       *Verifier
	getSecret      func() string // dynamic secret lookup
	requireNonce   bool
	onError        func(w http.ResponseWriter, err error)
}

// NewMiddleware creates a new webhook authentication middleware.
func NewMiddleware(secret string) *Middleware {
	return &Middleware{
		verifier:     NewVerifier(secret),
		requireNonce: true,
		onError:      defaultErrorHandler,
	}
}

// NewMiddlewareWithSecretFunc creates middleware that looks up the secret dynamically.
// Useful when the secret may change or is loaded from environment at request time.
func NewMiddlewareWithSecretFunc(getSecret func() string) *Middleware {
	return &Middleware{
		getSecret:    getSecret,
		requireNonce: true,
		onError:      defaultErrorHandler,
	}
}

// WithoutNonceVerification disables nonce verification (not recommended).
func (m *Middleware) WithoutNonceVerification() *Middleware {
	m.requireNonce = false
	return m
}

// WithErrorHandler sets a custom error handler.
func (m *Middleware) WithErrorHandler(handler func(w http.ResponseWriter, err error)) *Middleware {
	m.onError = handler
	return m
}

// Handler wraps an http.Handler with webhook signature verification.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := m.verify(r); err != nil {
			m.onError(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HandlerFunc wraps an http.HandlerFunc with webhook signature verification.
func (m *Middleware) HandlerFunc(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := m.verify(r); err != nil {
			m.onError(w, err)
			return
		}
		next(w, r)
	}
}

// verify performs the actual verification logic.
func (m *Middleware) verify(r *http.Request) error {
	// Get verifier (may be dynamic)
	verifier := m.verifier
	if m.getSecret != nil {
		verifier = NewVerifier(m.getSecret())
	}

	// Read body (we need it for signature verification)
	body, err := readBody(r)
	if err != nil {
		return fmt.Errorf("failed to read body: %w", err)
	}

	// Get headers
	signature := r.Header.Get(SignatureHeader)
	timestamp := r.Header.Get(TimestampHeader)
	nonce := r.Header.Get(NonceHeader)

	// Verify timestamp
	if err := verifier.VerifyTimestamp(timestamp); err != nil {
		return err
	}

	// Verify signature
	if err := verifier.VerifySignature(body, signature); err != nil {
		return err
	}

	// Verify nonce if required
	if m.requireNonce {
		if err := verifier.VerifyNonce(nonce); err != nil {
			return err
		}
	}

	return nil
}

// readBody reads the request body and replaces it so it can be read again.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	// Read body
	body := make([]byte, 0, 1024)
	buf := make([]byte, 512)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	r.Body.Close()

	// Replace body so it can be read again
	r.Body = &readCloser{data: body}

	return body, nil
}

// readCloser is an io.ReadCloser backed by a byte slice.
type readCloser struct {
	data []byte
	pos  int
}

func (rc *readCloser) Read(p []byte) (int, error) {
	if rc.pos >= len(rc.data) {
		return 0, io.EOF
	}
	n := copy(p, rc.data[rc.pos:])
	rc.pos += n
	return n, nil
}

func (rc *readCloser) Close() error {
	return nil
}

// defaultErrorHandler writes a JSON error response.
func defaultErrorHandler(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case errors.Is(err, ErrMissingSignature),
		errors.Is(err, ErrInvalidSignature),
		errors.Is(err, ErrSignatureMismatch),
		errors.Is(err, ErrMissingTimestamp),
		errors.Is(err, ErrInvalidTimestamp),
		errors.Is(err, ErrExpiredRequest),
		errors.Is(err, ErrFutureRequest),
		errors.Is(err, ErrMissingNonce),
		errors.Is(err, ErrReplayedNonce):
		w.WriteHeader(http.StatusUnauthorized)
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}

	fmt.Fprintf(w, `{"error":%q}`, err.Error())
}

// Sign creates a signature for a payload using the given secret.
// Useful for testing and for signing outgoing webhooks.
func Sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}
