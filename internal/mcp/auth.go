package mcp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

var (
	ErrMissingAuth = errors.New("missing authentication")
	ErrInvalidAuth = errors.New("invalid session token")
)

type Identity struct {
	OrgID  string
	UserID string
	Role   string
}

type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (Identity, error)
}

type DBAuthenticator struct {
	db *sql.DB
}

func NewDBAuthenticator(db *sql.DB) Authenticator {
	return &DBAuthenticator{db: db}
}

func (a *DBAuthenticator) Authenticate(ctx context.Context, r *http.Request) (Identity, error) {
	if a.db == nil {
		return Identity{}, errors.New("database not available")
	}

	token := extractBearerToken(r)
	if token == "" {
		return Identity{}, ErrMissingAuth
	}

	switch {
	case isWellFormedSessionToken(token, "oc_sess_"),
		isWellFormedSessionToken(token, "oc_magic_"),
		isWellFormedSessionToken(token, "oc_local_"):
		var identity Identity
		err := a.db.QueryRowContext(
			ctx,
			`SELECT s.org_id::text, s.user_id::text, COALESCE(u.role, 'owner')
			 FROM sessions s
			 JOIN users u
			   ON u.id = s.user_id
			  AND u.org_id = s.org_id
			 WHERE s.token = $1
			   AND s.revoked_at IS NULL
			   AND s.expires_at > NOW()`,
			token,
		).Scan(&identity.OrgID, &identity.UserID, &identity.Role)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Identity{}, ErrInvalidAuth
			}
			return Identity{}, err
		}
		identity.Role = normalizeRole(identity.Role)
		return identity, nil
	case strings.HasPrefix(token, "oc_sess_"),
		strings.HasPrefix(token, "oc_magic_"),
		strings.HasPrefix(token, "oc_local_"):
		return Identity{}, ErrInvalidAuth
	case strings.HasPrefix(token, "oc_git_"):
		var identity Identity
		err := a.db.QueryRowContext(
			ctx,
			`SELECT t.org_id::text, t.user_id::text, COALESCE(u.role, 'owner')
			 FROM git_access_tokens t
			 JOIN users u
			   ON u.id = t.user_id
			  AND u.org_id = t.org_id
			 WHERE t.token_hash = $1
			   AND t.revoked_at IS NULL`,
			hashGitSecret(token),
		).Scan(&identity.OrgID, &identity.UserID, &identity.Role)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Identity{}, ErrInvalidAuth
			}
			return Identity{}, err
		}
		_, _ = a.db.ExecContext(
			ctx,
			`UPDATE git_access_tokens SET last_used_at = NOW() WHERE token_hash = $1`,
			hashGitSecret(token),
		)
		identity.Role = normalizeRole(identity.Role)
		return identity, nil
	default:
		return Identity{}, ErrInvalidAuth
	}
}

func extractBearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
}

func isWellFormedSessionToken(token, prefix string) bool {
	if !strings.HasPrefix(token, prefix) {
		return false
	}
	suffix := token[len(prefix):]
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizeRole(role string) string {
	normalized := strings.ToLower(strings.TrimSpace(role))
	if normalized == "" {
		return "owner"
	}
	return normalized
}

func hashGitSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
