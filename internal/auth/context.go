package auth

import (
	"context"

	"github.com/google/uuid"
)

type orgContextKey struct{}

func WithOrgID(ctx context.Context, orgID uuid.UUID) context.Context {
	return context.WithValue(ctx, orgContextKey{}, orgID)
}

func OrgIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	value := ctx.Value(orgContextKey{})
	if value == nil {
		return uuid.Nil, false
	}
	orgID, ok := value.(uuid.UUID)
	if !ok || orgID == uuid.Nil {
		return uuid.Nil, false
	}
	return orgID, true
}
