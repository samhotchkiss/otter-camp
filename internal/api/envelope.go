package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const HeaderRequestID = "X-Request-ID"

const (
	ErrCodeResourceNotFound  = "resource_not_found"
	ErrCodeValidation        = "validation_error"
	ErrCodeUnauthorized      = "unauthorized"
	ErrCodeForbidden         = "forbidden"
	ErrCodeRateLimitExceeded = "rate_limit_exceeded"
	ErrCodeConflict          = "conflict"
	ErrCodeInternal          = "internal_error"
	ErrCodeNotImplemented    = "not_implemented"
	ErrCodeIdempotency       = "idempotency_conflict"

	// Legacy aliases still used by earlier tasks.
	ErrCodeBadRequest         = "bad_request"
	ErrCodeInvalidCredentials = "invalid_credentials"
	ErrCodeSessionExpired     = "session_expired"
	ErrCodeSessionRevoked     = "session_revoked"
	ErrCodeInvalidAPIKey      = "invalid_api_key"
	ErrCodeRateLimited        = "rate_limited"
	ErrCodeNotFound           = "not_found"
	ErrCodeServiceUnavailable = "service_unavailable"
)

var ErrorCodes = []string{
	ErrCodeResourceNotFound,
	ErrCodeValidation,
	ErrCodeUnauthorized,
	ErrCodeForbidden,
	ErrCodeRateLimitExceeded,
	ErrCodeConflict,
	ErrCodeInternal,
	ErrCodeNotImplemented,
	ErrCodeIdempotency,
	ErrCodeBadRequest,
	ErrCodeInvalidCredentials,
	ErrCodeSessionExpired,
	ErrCodeSessionRevoked,
	ErrCodeInvalidAPIKey,
	ErrCodeRateLimited,
	ErrCodeNotFound,
	ErrCodeServiceUnavailable,
}

type PaginationMeta struct {
	NextCursor *string `json:"next_cursor"`
	PrevCursor *string `json:"prev_cursor"`
	HasMore    bool    `json:"has_more"`
	Limit      int     `json:"limit"`
	Total      *int    `json:"total"`
}

type responseMeta struct {
	RequestID  string          `json:"request_id"`
	Pagination *PaginationMeta `json:"pagination,omitempty"`
}

type successEnvelope struct {
	Data any          `json:"data"`
	Meta responseMeta `json:"meta"`
}

type errorEnvelope struct {
	Error apiError     `json:"error"`
	Meta  responseMeta `json:"meta"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Details   any    `json:"details,omitempty"`
}

type Responder struct {
	ctx context.Context
}

func NewResponder(ctx context.Context) Responder {
	return Responder{ctx: ctx}
}

func (r Responder) JSON(w http.ResponseWriter, status int, data any) {
	requestID := resolveRequestID(w, r.ctx)
	writeEnvelope(w, status, successEnvelope{
		Data: data,
		Meta: responseMeta{RequestID: requestID},
	})
}

func (r Responder) JSONList(w http.ResponseWriter, status int, data any, pg PaginationMeta) {
	requestID := resolveRequestID(w, r.ctx)
	pgCopy := pg
	writeEnvelope(w, status, successEnvelope{
		Data: data,
		Meta: responseMeta{
			RequestID:  requestID,
			Pagination: &pgCopy,
		},
	})
}

func (r Responder) Error(w http.ResponseWriter, status int, code, message string) {
	r.ErrorWithDetails(w, status, code, message, nil)
}

func (r Responder) ErrorWithDetails(w http.ResponseWriter, status int, code, message string, details any) {
	requestID := resolveRequestID(w, r.ctx)
	if strings.TrimSpace(code) == "" {
		code = ErrCodeInternal
	}
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(status)
	}

	writeEnvelope(w, status, errorEnvelope{
		Error: apiError{
			Code:      code,
			Message:   message,
			RequestID: requestID,
			Details:   details,
		},
		Meta: responseMeta{RequestID: requestID},
	})
}

func JSON(w http.ResponseWriter, status int, data any) {
	NewResponder(nil).JSON(w, status, data)
}

func JSONList(w http.ResponseWriter, status int, data any, pg PaginationMeta) {
	NewResponder(nil).JSONList(w, status, data, pg)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	NewResponder(nil).Error(w, status, code, message)
}

func ErrorWithDetails(w http.ResponseWriter, status int, code, message string, details any) {
	NewResponder(nil).ErrorWithDetails(w, status, code, message, details)
}

func resolveRequestID(w http.ResponseWriter, ctx context.Context) string {
	if requestID, ok := RequestIDFromContext(ctx); ok {
		w.Header().Set(HeaderRequestID, requestID)
		return requestID
	}

	if requestID := strings.TrimSpace(w.Header().Get(HeaderRequestID)); requestID != "" {
		return requestID
	}

	requestID := uuid.NewString()
	w.Header().Set(HeaderRequestID, requestID)
	return requestID
}

func writeEnvelope(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
