package middleware

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/auth"
)

type Principal struct {
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	Email          string
	DisplayName    string
	Role           string
	AuthMethod     string
	Session        *auth.SessionInfo
	APIKey         *auth.APIKeyInfo
}

const (
	AuthMethodSession = "session"
	AuthMethodAPIKey  = "api_key"
	AuthMethodLocal   = "local_auto"
)

type principalContextKey struct{}
type sessionTokenContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok || principal.UserID == uuid.Nil {
		return Principal{}, false
	}
	return principal, true
}

func WithSessionToken(ctx context.Context, sessionToken string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, sessionTokenContextKey{}, strings.TrimSpace(sessionToken))
}

func SessionTokenFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	token, ok := ctx.Value(sessionTokenContextKey{}).(string)
	if !ok || strings.TrimSpace(token) == "" {
		return "", false
	}
	return token, true
}
