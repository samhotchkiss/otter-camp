package api

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

type requestIDKey struct{}
type organizationIDKey struct{}

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

func WithOrganizationID(ctx context.Context, organizationID uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, organizationIDKey{}, organizationID)
}

func OrganizationIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	organizationID, ok := ctx.Value(organizationIDKey{}).(uuid.UUID)
	if !ok || organizationID == uuid.Nil {
		return uuid.Nil, false
	}
	return organizationID, true
}
