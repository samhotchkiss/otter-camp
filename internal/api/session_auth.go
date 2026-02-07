package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type sessionIdentity struct {
	OrgID  string
	UserID string
	Role   string
}

var (
	errMissingAuthentication = errors.New("missing authentication")
	errInvalidSessionToken   = errors.New("invalid session token")
	errAuthentication        = errors.New("authentication error")
	errWorkspaceMismatch     = errors.New("workspace mismatch")
)

func requireSessionIdentity(ctx context.Context, db *sql.DB, r *http.Request) (sessionIdentity, error) {
	token := extractSessionToken(r)
	if token == "" {
		return sessionIdentity{}, errMissingAuthentication
	}
	var identity sessionIdentity
	var err error
	if strings.HasPrefix(token, "oc_sess_") || strings.HasPrefix(token, "oc_magic_") {
		err = db.QueryRowContext(
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
				return sessionIdentity{}, errInvalidSessionToken
			}
			return sessionIdentity{}, errAuthentication
		}
	} else if strings.HasPrefix(token, "oc_git_") {
		err = db.QueryRowContext(
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
				return sessionIdentity{}, errInvalidSessionToken
			}
			return sessionIdentity{}, errAuthentication
		}

		_, _ = db.ExecContext(
			ctx,
			`UPDATE git_access_tokens SET last_used_at = NOW() WHERE token_hash = $1`,
			hashGitSecret(token),
		)
	} else {
		return sessionIdentity{}, errInvalidSessionToken
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sessionIdentity{}, errInvalidSessionToken
		}
		return sessionIdentity{}, errAuthentication
	}

	if workspaceID := middleware.WorkspaceFromContext(ctx); workspaceID != "" && workspaceID != identity.OrgID {
		return sessionIdentity{}, errWorkspaceMismatch
	}

	identity.Role = normalizeRole(identity.Role)
	return identity, nil
}

func extractSessionToken(r *http.Request) string {
	if r == nil {
		return ""
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token != "" {
			return token
		}
	}

	if token := strings.TrimSpace(r.Header.Get("X-Session-Token")); token != "" {
		return token
	}

	return ""
}
