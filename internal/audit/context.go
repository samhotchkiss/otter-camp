package audit

import (
	"context"

	"github.com/google/uuid"
)

type principalTypeContextKey struct{}
type principalIDContextKey struct{}

func WithPrincipal(ctx context.Context, principalType string, principalID uuid.UUID) context.Context {
	ctxWithType := context.WithValue(ctx, principalTypeContextKey{}, principalType)
	return context.WithValue(ctxWithType, principalIDContextKey{}, principalID)
}

func PrincipalFromContext(ctx context.Context) (string, uuid.UUID) {
	if ctx == nil {
		return "", uuid.Nil
	}

	rawType, _ := ctx.Value(principalTypeContextKey{}).(string)
	rawID, _ := ctx.Value(principalIDContextKey{}).(uuid.UUID)
	return rawType, rawID
}
