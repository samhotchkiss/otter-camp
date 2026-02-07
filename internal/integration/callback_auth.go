// Package integration provides authentication for incoming OpenClaw callbacks.
package integration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	// SignatureHeader is the HTTP header containing the HMAC signature.
	SignatureHeader = "X-OpenClaw-Signature"
	// TimestampHeader is the HTTP header containing the request timestamp.
	TimestampHeader = "X-OpenClaw-Timestamp"
	// InstanceHeader is the HTTP header containing the OpenClaw instance name.
	InstanceHeader = "X-OpenClaw-Instance"

	// DefaultMaxAge is the default maximum age for callback requests (5 minutes).
	DefaultMaxAge = 5 * time.Minute

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
	// ErrExpiredTimestamp is returned when the timestamp is too old.
	ErrExpiredTimestamp = errors.New("request expired")
	// ErrFutureTimestamp is returned when the timestamp is in the future.
	ErrFutureTimestamp = errors.New("request timestamp in future")
	// ErrMissingInstance is returned when the instance header is missing.
	ErrMissingInstance = errors.New("missing instance header")
	// ErrUnregisteredInstance is returned when the instance is not registered.
	ErrUnregisteredInstance = errors.New("unregistered instance")
)

// InstanceRegistry checks whether a given OpenClaw instance is registered.
type InstanceRegistry interface {
	IsRegistered(instance string) bool
}

// CallbackAuthenticator validates OpenClaw callback authenticity.
type CallbackAuthenticator struct {
	secret   []byte
	maxAge   time.Duration
	registry InstanceRegistry
	now      func() time.Time
}

// NewCallbackAuthenticator creates a new callback authenticator.
func NewCallbackAuthenticator(secret string, registry InstanceRegistry) *CallbackAuthenticator {
	return &CallbackAuthenticator{
		secret:   []byte(secret),
		maxAge:   DefaultMaxAge,
		registry: registry,
		now:      time.Now,
	}
}

// WithMaxAge sets the maximum age for callback requests.
func (a *CallbackAuthenticator) WithMaxAge(maxAge time.Duration) *CallbackAuthenticator {
	a.maxAge = maxAge
	return a
}

// Verify performs callback authentication: instance, timestamp, and signature.
func (a *CallbackAuthenticator) Verify(payload []byte, signature, timestamp, instance string) error {
	if err := a.verifyInstance(instance); err != nil {
		return err
	}

	if err := a.verifyTimestamp(timestamp); err != nil {
		return err
	}

	if err := a.verifySignature(payload, signature, timestamp); err != nil {
		return err
	}

	return nil
}

func (a *CallbackAuthenticator) verifyInstance(instance string) error {
	if strings.TrimSpace(instance) == "" {
		return ErrMissingInstance
	}

	if a.registry == nil || !a.registry.IsRegistered(instance) {
		return ErrUnregisteredInstance
	}

	return nil
}

func (a *CallbackAuthenticator) verifyTimestamp(timestampStr string) error {
	if strings.TrimSpace(timestampStr) == "" {
		return ErrMissingTimestamp
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}

	requestTime := time.Unix(timestamp, 0)
	now := a.now()

	if now.Sub(requestTime) > a.maxAge {
		return ErrExpiredTimestamp
	}

	if requestTime.Sub(now) > time.Minute {
		return ErrFutureTimestamp
	}

	return nil
}

func (a *CallbackAuthenticator) verifySignature(payload []byte, signature, timestamp string) error {
	if strings.TrimSpace(signature) == "" {
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

	expected := a.computeSignature(payload, timestamp)
	if !hmac.Equal(providedBytes, expected) {
		return ErrSignatureMismatch
	}

	return nil
}

func (a *CallbackAuthenticator) computeSignature(payload []byte, timestamp string) []byte {
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

// SignCallback creates a signature for a callback payload (useful for tests).
func SignCallback(payload []byte, timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(payload)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}
