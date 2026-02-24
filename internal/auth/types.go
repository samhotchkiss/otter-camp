package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Service interface {
	Login(ctx context.Context, email, password, ipAddr, userAgent string) (*LoginResult, error)
	Logout(ctx context.Context, sessionToken string) error
	RefreshSession(ctx context.Context, sessionToken string) (*SessionInfo, error)
	ValidateSession(ctx context.Context, sessionToken string) (*SessionInfo, error)
	ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyInfo, error)
	IssueAPIKey(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*IssueResult, error)
	RevokeAPIKey(ctx context.Context, keyID, requestingUserID uuid.UUID) error
	ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]*APIKeyInfo, error)
	MagicLink(ctx context.Context, email string) (*MagicLinkResult, error)
	ResetPassword(ctx context.Context, token, newPassword string) error
	UnlockAccount(ctx context.Context, userID uuid.UUID) error
}

type LoginResult struct {
	SessionToken string
	Session      *SessionInfo
}

type SessionInfo struct {
	SessionID      uuid.UUID
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	Email          string
	Role           string
	ExpiresAt      time.Time
	LastUsedAt     time.Time
	RevokedAt      *time.Time
}

type APIKeyInfo struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	KeyPrefix   string
	DisplayName string
	Scopes      []string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

type IssueResult struct {
	RawKey string
	APIKey *APIKeyInfo
}

type MagicLinkResult struct {
	Token     string
	ExpiresAt time.Time
}

type AuditRecorder interface {
	RecordAuthEvent(ctx context.Context, action string, attrs map[string]any) error
}
