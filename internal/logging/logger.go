package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
)

type requestIDKey struct{}
type orgIDKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey{}, strings.TrimSpace(requestID))
}

func WithOrgID(ctx context.Context, orgID uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, orgIDKey{}, orgID)
}

func FromContext(ctx context.Context) *slog.Logger {
	logger := slog.Default()
	attrs := make([]any, 0, 4)

	requestID, ok := requestIDFromContext(ctx)
	if !ok {
		requestID, _ = api.RequestIDFromContext(ctx)
	}
	if requestID == "" {
		requestID = "unknown"
	}
	attrs = append(attrs, "request_id", requestID)

	orgID, ok := orgIDFromContext(ctx)
	if !ok {
		orgID, ok = api.OrganizationIDFromContext(ctx)
	}
	if ok && orgID != uuid.Nil {
		attrs = append(attrs, "organization_id", orgID.String())
	}
	attrs = append(attrs, "service", "ottercamp", "env", strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")))

	return logger.With(attrs...)
}

func requestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(requestIDKey{}).(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func orgIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	value, ok := ctx.Value(orgIDKey{}).(uuid.UUID)
	if !ok || value == uuid.Nil {
		return uuid.Nil, false
	}
	return value, true
}
