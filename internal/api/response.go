package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const HeaderRequestID = "X-Request-ID"

const (
	ErrCodeBadRequest         = "bad_request"
	ErrCodeUnauthorized       = "unauthorized"
	ErrCodeInvalidCredentials = "invalid_credentials"
	ErrCodeSessionExpired     = "session_expired"
	ErrCodeSessionRevoked     = "session_revoked"
	ErrCodeInvalidAPIKey      = "invalid_api_key"
	ErrCodeForbidden          = "forbidden"
	ErrCodeRateLimited        = "rate_limited"
	ErrCodeNotFound           = "not_found"
	ErrCodeInternal           = "internal_error"
	ErrCodeServiceUnavailable = "service_unavailable"
)

var ErrorCodes = []string{
	ErrCodeBadRequest,
	ErrCodeUnauthorized,
	ErrCodeInvalidCredentials,
	ErrCodeSessionExpired,
	ErrCodeSessionRevoked,
	ErrCodeInvalidAPIKey,
	ErrCodeForbidden,
	ErrCodeRateLimited,
	ErrCodeNotFound,
	ErrCodeInternal,
	ErrCodeServiceUnavailable,
}

type successEnvelope struct {
	Data any         `json:"data"`
	Meta successMeta `json:"meta"`
}

type successMeta struct {
	RequestID string `json:"request_id"`
	Timestamp string `json:"timestamp"`
}

type errorEnvelope struct {
	Error apiError  `json:"error"`
	Meta  errorMeta `json:"meta"`
}

type errorMeta struct {
	RequestID string `json:"request_id"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	requestID := ensureRequestIDHeader(w)

	write(w, status, successEnvelope{
		Data: data,
		Meta: successMeta{
			RequestID: requestID,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
}

func Error(w http.ResponseWriter, status int, code, message string) {
	ErrorWithDetails(w, status, code, message, nil)
}

func ErrorWithDetails(w http.ResponseWriter, status int, code, message string, details any) {
	requestID := ensureRequestIDHeader(w)
	if strings.TrimSpace(code) == "" {
		code = ErrCodeInternal
	}
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(status)
	}

	write(w, status, errorEnvelope{
		Error: apiError{
			Code:    code,
			Message: message,
			Details: details,
		},
		Meta: errorMeta{RequestID: requestID},
	})
}

func ensureRequestIDHeader(w http.ResponseWriter) string {
	requestID := strings.TrimSpace(w.Header().Get(HeaderRequestID))
	if requestID == "" {
		requestID = uuid.NewString()
		w.Header().Set(HeaderRequestID, requestID)
	}
	return requestID
}

func write(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type requestIDKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey{}, strings.TrimSpace(requestID))
}

func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	requestID, ok := ctx.Value(requestIDKey{}).(string)
	if !ok || strings.TrimSpace(requestID) == "" {
		return "", false
	}
	return requestID, true
}
