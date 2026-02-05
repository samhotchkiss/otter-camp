package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	defaultExecApprovalLimit = 50
	maxExecApprovalLimit     = 200
)

var (
	execApprovalsDB     *sql.DB
	execApprovalsDBErr  error
	execApprovalsDBOnce sync.Once
)

type ExecApprovalStatus string

const (
	ExecApprovalStatusPending    ExecApprovalStatus = "pending"
	ExecApprovalStatusProcessing ExecApprovalStatus = "processing"
	ExecApprovalStatusApproved   ExecApprovalStatus = "approved"
	ExecApprovalStatusDenied     ExecApprovalStatus = "denied"
	ExecApprovalStatusCancelled  ExecApprovalStatus = "cancelled"
	ExecApprovalStatusExpired    ExecApprovalStatus = "expired"
)

// ExecApprovalRequest is the API payload for an exec approval request.
type ExecApprovalRequest struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"org_id"`
	ExternalID  *string         `json:"external_id,omitempty"`
	AgentID     *string         `json:"agent_id,omitempty"`
	TaskID      *string         `json:"task_id,omitempty"`
	Status      string          `json:"status"`
	Command     string          `json:"command"`
	Cwd         *string         `json:"cwd,omitempty"`
	Shell       *string         `json:"shell,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	Env         json.RawMessage `json:"env,omitempty"`
	Message     *string         `json:"message,omitempty"`
	CallbackURL *string         `json:"callback_url,omitempty"`
	Request     json.RawMessage `json:"request,omitempty"`
	Response    json.RawMessage `json:"response,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	ResolvedAt  *time.Time      `json:"resolved_at,omitempty"`
}

type ExecApprovalListResponse struct {
	OrgID    string                `json:"org_id"`
	Status   string                `json:"status,omitempty"`
	Limit    int                   `json:"limit"`
	Requests []ExecApprovalRequest `json:"requests"`
}

type ExecApprovalRespondRequest struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment,omitempty"`
}

type ExecApprovalRespondResponse struct {
	OK      bool                `json:"ok"`
	Request ExecApprovalRequest `json:"request"`
}

// ExecApprovalsHandler manages exec approval endpoints.
type ExecApprovalsHandler struct {
	Hub *ws.Hub
}

// List handles GET /api/approvals/exec
func (h *ExecApprovalsHandler) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	if status == "" {
		status = string(ExecApprovalStatusPending)
	}
	if !isValidExecApprovalStatus(status) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid status"})
		return
	}

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), defaultExecApprovalLimit)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}
	if limit > maxExecApprovalLimit {
		limit = maxExecApprovalLimit
	}

	db, err := getExecApprovalsDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	conn, err := store.WithWorkspaceID(r.Context(), db, orgID)
	if err != nil {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: err.Error()})
		return
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		r.Context(),
		`SELECT id, org_id, external_id, agent_id, task_id, status, command, cwd, shell, args, env, message, callback_url, request, response, created_at, resolved_at
		 FROM exec_approval_requests
		 WHERE org_id = $1 AND status = $2
		 ORDER BY created_at DESC
		 LIMIT $3`,
		orgID,
		status,
		limit,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load exec approvals"})
		return
	}
	defer rows.Close()

	requests := make([]ExecApprovalRequest, 0)
	for rows.Next() {
		req, err := scanExecApprovalRequest(rows)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read exec approvals"})
			return
		}
		requests = append(requests, req)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read exec approvals"})
		return
	}

	sendJSON(w, http.StatusOK, ExecApprovalListResponse{
		OrgID:    orgID,
		Status:   status,
		Limit:    limit,
		Requests: requests,
	})
}

// Respond handles POST /api/approvals/exec/{id}/respond
func (h *ExecApprovalsHandler) Respond(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	requestID := strings.TrimSpace(chi.URLParam(r, "id"))
	if requestID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing request id"})
		return
	}
	if !uuidRegex.MatchString(requestID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request id"})
		return
	}

	var req ExecApprovalRespondRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	decision := strings.TrimSpace(strings.ToLower(req.Decision))
	if decision != "approve" && decision != "deny" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid decision"})
		return
	}

	db, err := getExecApprovalsDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	conn, err := store.WithWorkspaceID(r.Context(), db, orgID)
	if err != nil {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: err.Error()})
		return
	}
	defer conn.Close()

	current, err := getExecApprovalByID(r.Context(), conn, orgID, requestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "approval request not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load approval request"})
		return
	}

	if current.Status != string(ExecApprovalStatusPending) {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "approval request is not pending"})
		return
	}

	callbackURL := strings.TrimSpace(pointerString(current.CallbackURL))
	if callbackURL != "" {
		if err := postExecApprovalCallback(r.Context(), callbackURL, current, decision, req.Comment); err != nil {
			sendJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error()})
			return
		}
	}

	updated, err := resolveExecApproval(r.Context(), conn, orgID, requestID, decision, req.Comment)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusConflict, errorResponse{Error: "approval request already resolved"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to resolve approval request"})
		return
	}

	if h != nil && h.Hub != nil {
		broadcastExecApprovalResolved(h.Hub, orgID, updated)
	}

	sendJSON(w, http.StatusOK, ExecApprovalRespondResponse{OK: true, Request: updated})
}

func isValidExecApprovalStatus(status string) bool {
	switch status {
	case string(ExecApprovalStatusPending),
		string(ExecApprovalStatusProcessing),
		string(ExecApprovalStatusApproved),
		string(ExecApprovalStatusDenied),
		string(ExecApprovalStatusCancelled),
		string(ExecApprovalStatusExpired):
		return true
	default:
		return false
	}
}

func getExecApprovalsDB() (*sql.DB, error) {
	execApprovalsDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			execApprovalsDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			execApprovalsDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			execApprovalsDBErr = err
			return
		}

		execApprovalsDB = db
	})

	return execApprovalsDB, execApprovalsDBErr
}

func scanExecApprovalRequest(scanner interface{ Scan(...any) error }) (ExecApprovalRequest, error) {
	var out ExecApprovalRequest
	var externalID sql.NullString
	var agentID sql.NullString
	var taskID sql.NullString
	var cwd sql.NullString
	var shell sql.NullString
	var argsBytes []byte
	var envBytes []byte
	var message sql.NullString
	var callbackURL sql.NullString
	var requestBytes []byte
	var responseBytes []byte
	var resolvedAt sql.NullTime

	err := scanner.Scan(
		&out.ID,
		&out.OrgID,
		&externalID,
		&agentID,
		&taskID,
		&out.Status,
		&out.Command,
		&cwd,
		&shell,
		&argsBytes,
		&envBytes,
		&message,
		&callbackURL,
		&requestBytes,
		&responseBytes,
		&out.CreatedAt,
		&resolvedAt,
	)
	if err != nil {
		return out, err
	}

	if externalID.Valid {
		out.ExternalID = &externalID.String
	}
	if agentID.Valid {
		out.AgentID = &agentID.String
	}
	if taskID.Valid {
		out.TaskID = &taskID.String
	}
	if cwd.Valid {
		out.Cwd = &cwd.String
	}
	if shell.Valid {
		out.Shell = &shell.String
	}
	if len(argsBytes) > 0 {
		out.Args = json.RawMessage(argsBytes)
	}
	if len(envBytes) > 0 {
		out.Env = json.RawMessage(envBytes)
	}
	if message.Valid {
		out.Message = &message.String
	}
	if callbackURL.Valid {
		out.CallbackURL = &callbackURL.String
	}
	if len(requestBytes) > 0 {
		out.Request = json.RawMessage(requestBytes)
	}
	if len(responseBytes) > 0 {
		out.Response = json.RawMessage(responseBytes)
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		out.ResolvedAt = &t
	}

	return out, nil
}

func getExecApprovalByID(ctx context.Context, q store.Querier, orgID, id string) (ExecApprovalRequest, error) {
	row := q.QueryRowContext(
		ctx,
		`SELECT id, org_id, external_id, agent_id, task_id, status, command, cwd, shell, args, env, message, callback_url, request, response, created_at, resolved_at
		 FROM exec_approval_requests
		 WHERE org_id = $1 AND id = $2`,
		orgID,
		id,
	)
	return scanExecApprovalRequest(row)
}

func resolveExecApproval(ctx context.Context, q store.Querier, orgID, id, decision, comment string) (ExecApprovalRequest, error) {
	status := string(ExecApprovalStatusDenied)
	if decision == "approve" {
		status = string(ExecApprovalStatusApproved)
	}

	responsePayload := map[string]interface{}{
		"decision":     decision,
		"comment":      strings.TrimSpace(comment),
		"responded_at": time.Now().UTC().Format(time.RFC3339),
	}
	if responsePayload["comment"] == "" {
		delete(responsePayload, "comment")
	}
	responseBytes, _ := json.Marshal(responsePayload)

	row := q.QueryRowContext(
		ctx,
		`UPDATE exec_approval_requests
		 SET status = $1, response = $2, resolved_at = NOW()
		 WHERE org_id = $3 AND id = $4 AND status = 'pending'
		 RETURNING id, org_id, external_id, agent_id, task_id, status, command, cwd, shell, args, env, message, callback_url, request, response, created_at, resolved_at`,
		status,
		json.RawMessage(responseBytes),
		orgID,
		id,
	)
	return scanExecApprovalRequest(row)
}

func postExecApprovalCallback(ctx context.Context, callbackURL string, approval ExecApprovalRequest, decision, comment string) error {
	payload := map[string]interface{}{
		"hub_request_id": approval.ID,
		"request_id":     firstNonEmpty(pointerString(approval.ExternalID), approval.ID),
		"decision":       decision,
		"approved":       decision == "approve",
		"comment":        strings.TrimSpace(comment),
		"responded_at":   time.Now().UTC().Format(time.RFC3339),
	}
	if payload["comment"] == "" {
		delete(payload, "comment")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("failed to encode callback payload")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("failed to build callback request")
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("failed to deliver callback")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if len(b) > 0 {
			return errors.New("callback rejected: " + strings.TrimSpace(string(b)))
		}
		return errors.New("callback rejected")
	}

	return nil
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func broadcastExecApprovalResolved(hub *ws.Hub, orgID string, approval ExecApprovalRequest) {
	if hub == nil {
		return
	}

	payload, err := json.Marshal(map[string]interface{}{
		"type": "ExecApprovalResolved",
		"data": approval,
	})
	if err != nil {
		return
	}

	hub.Broadcast(orgID, payload)
}
