package api

import (
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type InboxItem struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	Command         string    `json:"command,omitempty"`
	Agent           string    `json:"agent"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	TaskID          *string   `json:"task_id,omitempty"`
	ProjectID       *string   `json:"project_id,omitempty"`
	TaskNumber      *int64    `json:"task_number,omitempty"`
	TaskTitle       *string   `json:"task_title,omitempty"`
	EscalationLevel *string   `json:"escalation_level,omitempty"`
	Summary         *string   `json:"summary,omitempty"`
}

type InboxResponse struct {
	Items []InboxItem `json:"items"`
}

func HandleInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	if r.URL.Query().Get("demo") == "true" {
		sendJSON(w, http.StatusOK, InboxResponse{Items: demoInboxItems()})
		return
	}

	orgID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing or invalid workspace"})
		return
	}

	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	if status == "" {
		status = string(ExecApprovalStatusPending)
	}
	if !isValidExecApprovalStatus(status) && status != "on_hold" {
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

	items := make([]InboxItem, 0)
	if status != "on_hold" {
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
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load inbox"})
			return
		}
		defer rows.Close()

		for rows.Next() {
			approval, err := scanExecApprovalRequest(rows)
			if err != nil {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read inbox"})
				return
			}

			agent := "Unknown"
			if approval.AgentID != nil && strings.TrimSpace(*approval.AgentID) != "" {
				agent = strings.TrimSpace(*approval.AgentID)
			}

			items = append(items, InboxItem{
				ID:        approval.ID,
				Type:      "exec",
				Command:   approval.Command,
				Agent:     agent,
				Status:    approval.Status,
				CreatedAt: approval.CreatedAt,
			})
		}
		if err := rows.Err(); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read inbox"})
			return
		}
	}

	if status == string(ExecApprovalStatusPending) || status == "on_hold" {
		blockerRows, blockerErr := conn.QueryContext(
			r.Context(),
			`SELECT
				b.id,
				b.issue_id,
				b.project_id,
				b.summary,
				b.escalation_level,
				b.assigned_project_manager_agent_id,
				b.raised_at,
				i.issue_number,
				i.title
			FROM project_issue_flow_blockers b
			INNER JOIN project_issues i ON i.id = b.issue_id
			WHERE b.org_id = $1
			  AND b.status = 'open'
			  AND b.escalation_level = 'human'
			ORDER BY b.raised_at DESC
			LIMIT $2`,
			orgID,
			limit,
		)
		if blockerErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load inbox blockers"})
			return
		}
		defer blockerRows.Close()

		for blockerRows.Next() {
			var blockerID string
			var issueID string
			var projectID string
			var summary string
			var escalationLevel string
			var assignedProjectManagerAgentID sql.NullString
			var raisedAt time.Time
			var issueNumber int64
			var issueTitle string

			if err := blockerRows.Scan(
				&blockerID,
				&issueID,
				&projectID,
				&summary,
				&escalationLevel,
				&assignedProjectManagerAgentID,
				&raisedAt,
				&issueNumber,
				&issueTitle,
			); err != nil {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read inbox blockers"})
				return
			}

			agent := "Project Manager"
			if assignedProjectManagerAgentID.Valid && strings.TrimSpace(assignedProjectManagerAgentID.String) != "" {
				agent = strings.TrimSpace(assignedProjectManagerAgentID.String)
			}

			taskID := issueID
			projectIDCopy := projectID
			taskNumber := issueNumber
			taskTitle := issueTitle
			escalation := escalationLevel
			summaryCopy := summary

			items = append(items, InboxItem{
				ID:              blockerID,
				Type:            "task_blocker",
				Command:         summary,
				Agent:           agent,
				Status:          "on_hold",
				CreatedAt:       raisedAt,
				TaskID:          &taskID,
				ProjectID:       &projectIDCopy,
				TaskNumber:      &taskNumber,
				TaskTitle:       &taskTitle,
				EscalationLevel: &escalation,
				Summary:         &summaryCopy,
			})
		}
		if err := blockerRows.Err(); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read inbox blockers"})
			return
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}

	sendJSON(w, http.StatusOK, InboxResponse{Items: items})
}

func demoInboxItems() []InboxItem {
	return []InboxItem{
		{
			ID:        "demo-approval-1",
			Type:      "exec",
			Command:   "railway up --service frontend",
			Agent:     "Derek",
			Status:    "pending",
			CreatedAt: time.Date(2026, 2, 4, 20, 0, 0, 0, time.UTC),
		},
		{
			ID:        "demo-approval-2",
			Type:      "exec",
			Command:   "npm publish",
			Agent:     "Ivy",
			Status:    "pending",
			CreatedAt: time.Date(2026, 2, 4, 19, 30, 0, 0, time.UTC),
		},
	}
}
