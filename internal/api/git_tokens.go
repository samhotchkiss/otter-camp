package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/samhotchkiss/otter-camp/internal/gitserver"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	gitTokenPrefix        = "oc_git_"
	maxGitTokenNameLength = 64
	maxGitKeyNameLength   = 64
	maxGitKeyLength       = 8192
	tokenPrefixLength     = 12
)

type gitProjectPermissionInput struct {
	ProjectID  string `json:"project_id"`
	Permission string `json:"permission"`
}

type gitProjectPermission struct {
	ProjectID  string `json:"project_id"`
	Permission string `json:"permission"`
}

type gitTokenResponse struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	TokenPrefix string                 `json:"token_prefix"`
	Token       string                 `json:"token,omitempty"`
	Projects    []gitProjectPermission `json:"projects"`
	CreatedAt   time.Time              `json:"created_at"`
	LastUsedAt  *time.Time             `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time             `json:"revoked_at,omitempty"`
}

type gitTokensListResponse struct {
	Tokens []gitTokenResponse `json:"tokens"`
}

type createGitTokenRequest struct {
	Name     string                      `json:"name"`
	Projects []gitProjectPermissionInput `json:"projects"`
}

type gitKeyResponse struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	PublicKey   string                 `json:"public_key"`
	Fingerprint string                 `json:"fingerprint"`
	Projects    []gitProjectPermission `json:"projects"`
	CreatedAt   time.Time              `json:"created_at"`
	LastUsedAt  *time.Time             `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time             `json:"revoked_at,omitempty"`
}

type gitKeysListResponse struct {
	Keys []gitKeyResponse `json:"keys"`
}

type createGitKeyRequest struct {
	Name      string                      `json:"name"`
	PublicKey string                      `json:"public_key"`
	Projects  []gitProjectPermissionInput `json:"projects"`
}

// HandleGitTokensList handles GET /api/git/tokens.
func HandleGitTokensList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	tokens, err := listGitTokens(r.Context(), db, identity)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list tokens"})
		return
	}

	sendJSON(w, http.StatusOK, gitTokensListResponse{Tokens: tokens})
}

// HandleGitTokensCreate handles POST /api/git/tokens.
func HandleGitTokensCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req createGitTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing name"})
		return
	}
	if len(name) > maxGitTokenNameLength {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name too long"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	projects, perms, err := normalizeProjectPermissions(r.Context(), db, identity.OrgID, req.Projects)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	token, tokenHash, tokenPrefix, err := generateGitToken()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to generate token"})
		return
	}

	created, err := insertGitToken(r.Context(), db, identity, name, tokenHash, tokenPrefix, projects, perms)
	if err != nil {
		if errors.Is(err, errTokenCollision) {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to generate token"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create token"})
		return
	}
	created.Token = token

	sendJSON(w, http.StatusOK, created)
}

// HandleGitTokensRevoke handles POST /api/git/tokens/{id}/revoke.
func HandleGitTokensRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing id"})
		return
	}
	if !uuidRegex.MatchString(id) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	token, err := revokeGitToken(r.Context(), db, identity, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "token not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to revoke token"})
		return
	}

	sendJSON(w, http.StatusOK, token)
}

// HandleGitSSHKeysList handles GET /api/git/keys.
func HandleGitSSHKeysList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	keys, err := listGitKeys(r.Context(), db, identity)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list keys"})
		return
	}

	sendJSON(w, http.StatusOK, gitKeysListResponse{Keys: keys})
}

// HandleGitSSHKeysCreate handles POST /api/git/keys.
func HandleGitSSHKeysCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req createGitKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing name"})
		return
	}
	if len(name) > maxGitKeyNameLength {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name too long"})
		return
	}

	publicKey, fingerprint, err := normalizeSSHPublicKey(req.PublicKey)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	projects, perms, err := normalizeProjectPermissions(r.Context(), db, identity.OrgID, req.Projects)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	created, err := insertGitKey(r.Context(), db, identity, name, publicKey, fingerprint, projects, perms)
	if err != nil {
		if errors.Is(err, errKeyAlreadyExists) {
			sendJSON(w, http.StatusConflict, errorResponse{Error: "key already exists"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create key"})
		return
	}

	sendJSON(w, http.StatusOK, created)
}

// HandleGitSSHKeysRevoke handles POST /api/git/keys/{id}/revoke.
func HandleGitSSHKeysRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing id"})
		return
	}
	if !uuidRegex.MatchString(id) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	key, err := revokeGitKey(r.Context(), db, identity, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "key not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to revoke key"})
		return
	}

	sendJSON(w, http.StatusOK, key)
}

var errTokenCollision = errors.New("token collision")
var errKeyAlreadyExists = errors.New("key already exists")

func generateGitToken() (string, string, string, error) {
	raw, err := generateRandomToken(32)
	if err != nil {
		return "", "", "", err
	}
	token := gitTokenPrefix + raw
	hash := hashGitSecret(token)
	prefix := token
	if len(token) > tokenPrefixLength {
		prefix = token[:tokenPrefixLength]
	}
	return token, hash, prefix, nil
}

func hashGitSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func normalizeProjectPermissions(ctx context.Context, db *sql.DB, orgID string, inputs []gitProjectPermissionInput) ([]gitProjectPermission, map[string]gitserver.ProjectPermission, error) {
	if len(inputs) == 0 {
		return nil, nil, errors.New("missing projects")
	}

	projectStore := store.NewProjectStore(db)
	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, orgID)

	seen := make(map[string]struct{}, len(inputs))
	normalized := make([]gitProjectPermission, 0, len(inputs))
	perms := make(map[string]gitserver.ProjectPermission, len(inputs))

	for _, entry := range inputs {
		projectID := strings.TrimSpace(entry.ProjectID)
		if projectID == "" {
			return nil, nil, errors.New("missing project_id")
		}
		if !uuidRegex.MatchString(projectID) {
			return nil, nil, errors.New("invalid project_id")
		}
		if _, exists := seen[projectID]; exists {
			return nil, nil, errors.New("duplicate project_id")
		}

		perm, err := parseProjectPermission(entry.Permission)
		if err != nil {
			return nil, nil, err
		}

		if _, err := projectStore.GetByID(workspaceCtx, projectID); err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
				return nil, nil, errors.New("project not found")
			}
			return nil, nil, errors.New("failed to validate projects")
		}

		seen[projectID] = struct{}{}
		perms[projectID] = perm
		normalized = append(normalized, gitProjectPermission{
			ProjectID:  projectID,
			Permission: string(perm),
		})
	}

	return normalized, perms, nil
}

func parseProjectPermission(raw string) (gitserver.ProjectPermission, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "read":
		return gitserver.PermissionRead, nil
	case "write":
		return gitserver.PermissionWrite, nil
	default:
		return "", errors.New("invalid permission")
	}
}

func insertGitToken(ctx context.Context, db *sql.DB, identity sessionIdentity, name, tokenHash, tokenPrefix string, projects []gitProjectPermission, perms map[string]gitserver.ProjectPermission) (gitTokenResponse, error) {
	var created gitTokenResponse

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return created, err
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO git_access_tokens (org_id, user_id, name, token_hash, token_prefix)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id::text, name, token_prefix, created_at, last_used_at, revoked_at`,
		identity.OrgID,
		identity.UserID,
		name,
		tokenHash,
		tokenPrefix,
	).Scan(&created.ID, &created.Name, &created.TokenPrefix, &created.CreatedAt, &created.LastUsedAt, &created.RevokedAt)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return created, errTokenCollision
		}
		return created, err
	}

	for _, entry := range projects {
		perm := perms[entry.ProjectID]
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO git_access_token_projects (org_id, token_id, project_id, permission)
			 VALUES ($1, $2, $3, $4)`,
			identity.OrgID,
			created.ID,
			entry.ProjectID,
			string(perm),
		); err != nil {
			return created, err
		}
	}

	if err := tx.Commit(); err != nil {
		return created, err
	}

	created.Projects = projects
	return created, nil
}

func listGitTokens(ctx context.Context, db *sql.DB, identity sessionIdentity) ([]gitTokenResponse, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT id::text, name, token_prefix, created_at, last_used_at, revoked_at
		 FROM git_access_tokens
		 WHERE org_id = $1 AND user_id = $2
		 ORDER BY created_at DESC`,
		identity.OrgID,
		identity.UserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := make([]gitTokenResponse, 0)
	index := make(map[string]int)
	for rows.Next() {
		var token gitTokenResponse
		var lastUsedAt sql.NullTime
		var revokedAt sql.NullTime
		if err := rows.Scan(&token.ID, &token.Name, &token.TokenPrefix, &token.CreatedAt, &lastUsedAt, &revokedAt); err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			token.LastUsedAt = &lastUsedAt.Time
		}
		if revokedAt.Valid {
			token.RevokedAt = &revokedAt.Time
		}
		index[token.ID] = len(tokens)
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(tokens) == 0 {
		return tokens, nil
	}

	tokenIDs := make([]string, 0, len(tokens))
	for _, token := range tokens {
		tokenIDs = append(tokenIDs, token.ID)
	}

	permRows, err := db.QueryContext(
		ctx,
		`SELECT token_id::text, project_id::text, permission
		 FROM git_access_token_projects
		 WHERE token_id = ANY($1)`,
		pq.Array(tokenIDs),
	)
	if err != nil {
		return nil, err
	}
	defer permRows.Close()

	for permRows.Next() {
		var tokenID, projectID, permission string
		if err := permRows.Scan(&tokenID, &projectID, &permission); err != nil {
			return nil, err
		}
		idx, ok := index[tokenID]
		if !ok {
			continue
		}
		tokens[idx].Projects = append(tokens[idx].Projects, gitProjectPermission{
			ProjectID:  projectID,
			Permission: permission,
		})
	}
	if err := permRows.Err(); err != nil {
		return nil, err
	}

	return tokens, nil
}

func revokeGitToken(ctx context.Context, db *sql.DB, identity sessionIdentity, id string) (gitTokenResponse, error) {
	var token gitTokenResponse
	var lastUsedAt sql.NullTime
	var revokedAt sql.NullTime
	err := db.QueryRowContext(
		ctx,
		`UPDATE git_access_tokens
		 SET revoked_at = COALESCE(revoked_at, NOW())
		 WHERE id = $1 AND org_id = $2 AND user_id = $3
		 RETURNING id::text, name, token_prefix, created_at, last_used_at, revoked_at`,
		id,
		identity.OrgID,
		identity.UserID,
	).Scan(&token.ID, &token.Name, &token.TokenPrefix, &token.CreatedAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return token, err
	}
	if lastUsedAt.Valid {
		token.LastUsedAt = &lastUsedAt.Time
	}
	if revokedAt.Valid {
		token.RevokedAt = &revokedAt.Time
	}

	projects, err := listGitTokenProjects(ctx, db, id)
	if err != nil {
		return token, err
	}
	token.Projects = projects

	return token, nil
}

func listGitTokenProjects(ctx context.Context, db *sql.DB, tokenID string) ([]gitProjectPermission, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT project_id::text, permission
		 FROM git_access_token_projects
		 WHERE token_id = $1
		 ORDER BY project_id`,
		tokenID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := make([]gitProjectPermission, 0)
	for rows.Next() {
		var entry gitProjectPermission
		if err := rows.Scan(&entry.ProjectID, &entry.Permission); err != nil {
			return nil, err
		}
		projects = append(projects, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

func insertGitKey(ctx context.Context, db *sql.DB, identity sessionIdentity, name, publicKey, fingerprint string, projects []gitProjectPermission, perms map[string]gitserver.ProjectPermission) (gitKeyResponse, error) {
	var created gitKeyResponse

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return created, err
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO git_ssh_keys (org_id, user_id, name, public_key, fingerprint)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id::text, name, public_key, fingerprint, created_at, last_used_at, revoked_at`,
		identity.OrgID,
		identity.UserID,
		name,
		publicKey,
		fingerprint,
	).Scan(&created.ID, &created.Name, &created.PublicKey, &created.Fingerprint, &created.CreatedAt, &created.LastUsedAt, &created.RevokedAt)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return created, errKeyAlreadyExists
		}
		return created, err
	}

	for _, entry := range projects {
		perm := perms[entry.ProjectID]
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO git_ssh_key_projects (org_id, key_id, project_id, permission)
			 VALUES ($1, $2, $3, $4)`,
			identity.OrgID,
			created.ID,
			entry.ProjectID,
			string(perm),
		); err != nil {
			return created, err
		}
	}

	if err := tx.Commit(); err != nil {
		return created, err
	}

	created.Projects = projects
	return created, nil
}

func listGitKeys(ctx context.Context, db *sql.DB, identity sessionIdentity) ([]gitKeyResponse, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT id::text, name, public_key, fingerprint, created_at, last_used_at, revoked_at
		 FROM git_ssh_keys
		 WHERE org_id = $1 AND user_id = $2
		 ORDER BY created_at DESC`,
		identity.OrgID,
		identity.UserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]gitKeyResponse, 0)
	index := make(map[string]int)
	for rows.Next() {
		var key gitKeyResponse
		var lastUsedAt sql.NullTime
		var revokedAt sql.NullTime
		if err := rows.Scan(&key.ID, &key.Name, &key.PublicKey, &key.Fingerprint, &key.CreatedAt, &lastUsedAt, &revokedAt); err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			key.LastUsedAt = &lastUsedAt.Time
		}
		if revokedAt.Valid {
			key.RevokedAt = &revokedAt.Time
		}
		index[key.ID] = len(keys)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return keys, nil
	}

	keyIDs := make([]string, 0, len(keys))
	for _, key := range keys {
		keyIDs = append(keyIDs, key.ID)
	}

	permRows, err := db.QueryContext(
		ctx,
		`SELECT key_id::text, project_id::text, permission
		 FROM git_ssh_key_projects
		 WHERE key_id = ANY($1)`,
		pq.Array(keyIDs),
	)
	if err != nil {
		return nil, err
	}
	defer permRows.Close()

	for permRows.Next() {
		var keyID, projectID, permission string
		if err := permRows.Scan(&keyID, &projectID, &permission); err != nil {
			return nil, err
		}
		idx, ok := index[keyID]
		if !ok {
			continue
		}
		keys[idx].Projects = append(keys[idx].Projects, gitProjectPermission{
			ProjectID:  projectID,
			Permission: permission,
		})
	}
	if err := permRows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}

func revokeGitKey(ctx context.Context, db *sql.DB, identity sessionIdentity, id string) (gitKeyResponse, error) {
	var key gitKeyResponse
	var lastUsedAt sql.NullTime
	var revokedAt sql.NullTime
	err := db.QueryRowContext(
		ctx,
		`UPDATE git_ssh_keys
		 SET revoked_at = COALESCE(revoked_at, NOW())
		 WHERE id = $1 AND org_id = $2 AND user_id = $3
		 RETURNING id::text, name, public_key, fingerprint, created_at, last_used_at, revoked_at`,
		id,
		identity.OrgID,
		identity.UserID,
	).Scan(&key.ID, &key.Name, &key.PublicKey, &key.Fingerprint, &key.CreatedAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return key, err
	}
	if lastUsedAt.Valid {
		key.LastUsedAt = &lastUsedAt.Time
	}
	if revokedAt.Valid {
		key.RevokedAt = &revokedAt.Time
	}

	projects, err := listGitKeyProjects(ctx, db, id)
	if err != nil {
		return key, err
	}
	key.Projects = projects

	return key, nil
}

func listGitKeyProjects(ctx context.Context, db *sql.DB, keyID string) ([]gitProjectPermission, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT project_id::text, permission
		 FROM git_ssh_key_projects
		 WHERE key_id = $1
		 ORDER BY project_id`,
		keyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := make([]gitProjectPermission, 0)
	for rows.Next() {
		var entry gitProjectPermission
		if err := rows.Scan(&entry.ProjectID, &entry.Permission); err != nil {
			return nil, err
		}
		projects = append(projects, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

func normalizeSSHPublicKey(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", errors.New("missing public_key")
	}
	if len(trimmed) > maxGitKeyLength {
		return "", "", errors.New("public_key too long")
	}

	parts := strings.Fields(trimmed)
	if len(parts) < 2 {
		return "", "", errors.New("invalid public_key")
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", errors.New("invalid public_key")
		}
	}

	sum := sha256.Sum256(decoded)
	fingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	return trimmed, fingerprint, nil
}

func validateGitToken(ctx context.Context, db *sql.DB, token string) (gitserver.AuthInfo, error) {
	var info gitserver.AuthInfo

	raw := strings.TrimSpace(token)
	if !strings.HasPrefix(raw, gitTokenPrefix) {
		return info, errors.New("invalid token")
	}

	tokenHash := hashGitSecret(raw)

	err := db.QueryRowContext(
		ctx,
		`SELECT id::text, org_id::text, user_id::text
		 FROM git_access_tokens
		 WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash,
	).Scan(&info.TokenID, &info.OrgID, &info.UserID)
	if err != nil {
		return info, err
	}

	perms := make(map[string]gitserver.ProjectPermission)
	rows, err := db.QueryContext(
		ctx,
		`SELECT project_id::text, permission
		 FROM git_access_token_projects
		 WHERE token_id = $1 AND org_id = $2`,
		info.TokenID,
		info.OrgID,
	)
	if err != nil {
		return info, err
	}
	defer rows.Close()

	for rows.Next() {
		var projectID, permission string
		if err := rows.Scan(&projectID, &permission); err != nil {
			return info, err
		}
		switch permission {
		case string(gitserver.PermissionRead):
			perms[projectID] = gitserver.PermissionRead
		case string(gitserver.PermissionWrite):
			perms[projectID] = gitserver.PermissionWrite
		}
	}
	if err := rows.Err(); err != nil {
		return info, err
	}

	info.Permissions = perms

	_, _ = db.ExecContext(ctx, `UPDATE git_access_tokens SET last_used_at = NOW() WHERE id = $1`, info.TokenID)

	return info, nil
}
