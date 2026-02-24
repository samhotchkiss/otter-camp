package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const headerIdempotencyKey = "Idempotency-Key"

type IdempotencyOptions struct {
	Repository interface {
		Get(ctx context.Context, organizationID uuid.UUID, keyHash string) (repo.IdempotencyKey, error)
		Store(ctx context.Context, key repo.IdempotencyKey) (repo.IdempotencyKey, error)
		Delete(ctx context.Context, organizationID uuid.UUID, keyHash string) error
	}
	Now func() time.Time
}

func Idempotency(opts IdempotencyOptions) func(http.Handler) http.Handler {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !methodSupportsIdempotency(r.Method) || opts.Repository == nil {
				next.ServeHTTP(w, r)
				return
			}

			idempotencyKey := strings.TrimSpace(r.Header.Get(headerIdempotencyKey))
			if idempotencyKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			organizationID, ok := api.OrganizationIDFromContext(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				api.NewResponder(r.Context()).Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "failed to read request body")
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			requestHash := hashRequest(idempotencyKey, r.Method, r.URL.Path, string(bodyBytes))
			keyHash := hashRequest(idempotencyKey)
			now := nowFn().UTC()

			existing, err := opts.Repository.Get(r.Context(), organizationID, keyHash)
			if err == nil {
				if existing.ExpiresAt.After(now) {
					if existing.RequestHash != requestHash {
						api.NewResponder(r.Context()).Error(w, http.StatusConflict, api.ErrCodeIdempotency, "Idempotency key reused with different request body")
						return
					}
					replayStoredResponse(w, existing)
					return
				}

				if err := opts.Repository.Delete(r.Context(), organizationID, keyHash); err != nil {
					api.NewResponder(r.Context()).Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to recycle expired idempotency key")
					return
				}
			} else if !errors.Is(err, repo.ErrNotFound) {
				api.NewResponder(r.Context()).Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load idempotency key")
				return
			}

			capture := newIdempotencyCapture(w)
			next.ServeHTTP(capture, r)

			if capture.statusCode < 200 || capture.statusCode >= 300 {
				return
			}

			_, _ = opts.Repository.Store(r.Context(), repo.IdempotencyKey{
				OrganizationID: organizationID,
				KeyHash:        keyHash,
				RequestHash:    requestHash,
				ResponseStatus: capture.statusCode,
				ResponseBody:   normalizeResponseBody(capture.body.Bytes()),
				ExpiresAt:      now.Add(24 * time.Hour),
			})
		})
	}
}

func methodSupportsIdempotency(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

func hashRequest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func replayStoredResponse(w http.ResponseWriter, stored repo.IdempotencyKey) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(stored.ResponseStatus)
	if len(stored.ResponseBody) > 0 {
		_, _ = w.Write(stored.ResponseBody)
	}
}

func normalizeResponseBody(raw []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if json.Valid(trimmed) {
		out := make([]byte, len(trimmed))
		copy(out, trimmed)
		return out
	}

	fallback, _ := json.Marshal(string(trimmed))
	return fallback
}

type idempotencyCapture struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
	body        bytes.Buffer
}

func newIdempotencyCapture(w http.ResponseWriter) *idempotencyCapture {
	return &idempotencyCapture{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (c *idempotencyCapture) WriteHeader(statusCode int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	c.statusCode = statusCode
	c.ResponseWriter.WriteHeader(statusCode)
}

func (c *idempotencyCapture) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	c.body.Write(p)
	return c.ResponseWriter.Write(p)
}
