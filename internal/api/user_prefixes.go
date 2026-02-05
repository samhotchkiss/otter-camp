package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"
)

const (
	maxUserCommandPrefixLength  = 32
	maxUserCommandCommandLength = 512
)

type UserCommandPrefix struct {
	ID        string    `json:"id"`
	Prefix    string    `json:"prefix"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type userCommandPrefixesResponse struct {
	Prefixes []UserCommandPrefix `json:"prefixes"`
}

type createUserCommandPrefixRequest struct {
	Prefix  string `json:"prefix"`
	Command string `json:"command"`
}

type sessionIdentity struct {
	OrgID  string
	UserID string
}

// HandleUserCommandPrefixesList handles GET /api/user/prefixes.
func HandleUserCommandPrefixesList(w http.ResponseWriter, r *http.Request) {
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

	rows, err := db.QueryContext(
		r.Context(),
		`SELECT id::text, prefix, command, created_at, updated_at
		 FROM user_command_prefixes
		 WHERE org_id = $1 AND user_id = $2
		 ORDER BY prefix ASC`,
		identity.OrgID,
		identity.UserID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list prefixes"})
		return
	}
	defer rows.Close()

	prefixes := make([]UserCommandPrefix, 0)
	for rows.Next() {
		var p UserCommandPrefix
		if err := rows.Scan(&p.ID, &p.Prefix, &p.Command, &p.CreatedAt, &p.UpdatedAt); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read prefixes"})
			return
		}
		prefixes = append(prefixes, p)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read prefixes"})
		return
	}

	sendJSON(w, http.StatusOK, userCommandPrefixesResponse{Prefixes: prefixes})
}

// HandleUserCommandPrefixesCreate handles POST /api/user/prefixes.
func HandleUserCommandPrefixesCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req createUserCommandPrefixRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	prefix := strings.TrimSpace(req.Prefix)
	if prefix == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing prefix"})
		return
	}
	if strings.ContainsAny(prefix, " \t\n\r") {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid prefix"})
		return
	}
	if len(prefix) > maxUserCommandPrefixLength {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "prefix too long"})
		return
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing command"})
		return
	}
	if len(command) > maxUserCommandCommandLength {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "command too long"})
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

	p, err := insertUserCommandPrefix(r.Context(), db, identity, prefix, command)
	if err != nil {
		if errors.Is(err, errPrefixAlreadyExists) {
			sendJSON(w, http.StatusConflict, errorResponse{Error: "prefix already exists"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create prefix"})
		return
	}

	sendJSON(w, http.StatusOK, p)
}

// HandleUserCommandPrefixesDelete handles DELETE /api/user/prefixes/{id}.
func HandleUserCommandPrefixesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
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

	deleted, err := deleteUserCommandPrefix(r.Context(), db, identity.UserID, id)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to delete prefix"})
		return
	}
	if !deleted {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "prefix not found"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

var errPrefixAlreadyExists = errors.New("prefix already exists")

func insertUserCommandPrefix(ctx context.Context, db *sql.DB, identity sessionIdentity, prefix, command string) (UserCommandPrefix, error) {
	var p UserCommandPrefix
	err := db.QueryRowContext(
		ctx,
		`INSERT INTO user_command_prefixes (org_id, user_id, prefix, command)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, prefix, command, created_at, updated_at`,
		identity.OrgID,
		identity.UserID,
		prefix,
		command,
	).Scan(&p.ID, &p.Prefix, &p.Command, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return p, errPrefixAlreadyExists
		}
		return p, err
	}
	return p, nil
}

func deleteUserCommandPrefix(ctx context.Context, db *sql.DB, userID, id string) (bool, error) {
	var deletedID string
	err := db.QueryRowContext(
		ctx,
		`DELETE FROM user_command_prefixes
		 WHERE id = $1 AND user_id = $2
		 RETURNING id::text`,
		id,
		userID,
	).Scan(&deletedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func requireSessionIdentity(ctx context.Context, db *sql.DB, r *http.Request) (sessionIdentity, error) {
	token := extractSessionToken(r)
	if token == "" {
		return sessionIdentity{}, errors.New("missing authentication")
	}
	if !strings.HasPrefix(token, "oc_sess_") {
		return sessionIdentity{}, errors.New("invalid session token")
	}

	var identity sessionIdentity
	err := db.QueryRowContext(
		ctx,
		`SELECT org_id::text, user_id::text
		 FROM sessions
		 WHERE token = $1
		   AND revoked_at IS NULL
		   AND expires_at > NOW()`,
		token,
	).Scan(&identity.OrgID, &identity.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sessionIdentity{}, errors.New("invalid session token")
		}
		return sessionIdentity{}, errors.New("authentication error")
	}
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

