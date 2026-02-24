package audit

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

type principalContextKey struct{}

type principalValue struct {
	principalType string
	principalID   uuid.UUID
}

func WithPrincipal(ctx context.Context, principalType string, principalID uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalContextKey{}, principalValue{
		principalType: normalizePrincipalType(principalType),
		principalID:   principalID,
	})
}

func PrincipalFromContext(ctx context.Context) (string, uuid.UUID) {
	if ctx == nil {
		return "", uuid.Nil
	}

	value, ok := ctx.Value(principalContextKey{}).(principalValue)
	if !ok {
		return "", uuid.Nil
	}

	return value.principalType, value.principalID
}

func normalizePrincipalType(principalType string) string {
	return strings.ToLower(strings.TrimSpace(principalType))
}
