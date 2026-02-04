package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/webhook"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	openClawSignatureHeader = "X-OpenClaw-Signature"
	gitClawSignatureHeader  = "X-GitClaw-Signature"
	aiHubSignatureHeader    = "X-AIHub-Signature"
)

var (
	webhooksDB     *sql.DB
	webhooksDBErr  error
	webhooksDBOnce sync.Once
)

type openClawWebhookPayload struct {
	Event          string                `json:"event"`
	OrgID          string                `json:"org_id"`
	OrganizationID string                `json:"organization_id"`
	TaskID         string                `json:"task_id"`
	AgentID        string                `json:"agent_id"`
	Task           *openClawWebhookTask  `json:"task,omitempty"`
	Agent          *openClawWebhookAgent `json:"agent,omitempty"`
}

type openClawWebhookTask struct {
	ID             string `json:"id"`
	Status         string `json:"status,omitempty"`
	PreviousStatus string `json:"previous_status,omitempty"`
}

type openClawWebhookAgent struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

type openClawWebhookResponse struct {
	OK bool `json:"ok"`
}

// WebhookHandler handles webhook requests with WebSocket broadcasting.
type WebhookHandler struct {
	Hub *ws.Hub
}

// OpenClawHandler handles POST /api/webhooks/openclaw with status updates.
func (h *WebhookHandler) OpenClawHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	secret := strings.TrimSpace(os.Getenv("OPENCLAW_WEBHOOK_SECRET"))
	if secret == "" {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "webhook secret not configured"})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unable to read request body"})
		return
	}

	signature := firstNonEmpty(
		strings.TrimSpace(r.Header.Get(openClawSignatureHeader)),
		strings.TrimSpace(r.Header.Get(gitClawSignatureHeader)),
		strings.TrimSpace(r.Header.Get(aiHubSignatureHeader)),
	)
	if signature == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing signature"})
		return
	}
	if !verifyOpenClawSignature(body, signature, secret) {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid signature"})
		return
	}

	event, err := webhook.ParseStatusEvent(body)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if event.OrgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
		return
	}
	if !uuidRegex.MatchString(event.OrgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	if event.Event == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing event"})
		return
	}

	// Validate task/agent IDs based on event type
	if strings.HasPrefix(event.Event, "task.") {
		taskID := event.TaskID
		if event.Task != nil && event.Task.ID != "" {
			taskID = event.Task.ID
		}
		if taskID == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing task id"})
			return
		}
		if !uuidRegex.MatchString(taskID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid task id"})
			return
		}
	} else if strings.HasPrefix(event.Event, "agent.") {
		agentID := event.AgentID
		if event.Agent != nil && event.Agent.ID != "" {
			agentID = event.Agent.ID
		}
		if agentID == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing agent id"})
			return
		}
		if !uuidRegex.MatchString(agentID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid agent id"})
			return
		}
	} else {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unsupported event"})
		return
	}

	db, err := getWebhooksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	// Process status events with updates and broadcasting
	if webhook.IsSupportedEvent(event.Event) {
		statusHandler := webhook.NewStatusHandler(db, h.Hub)
		if err := statusHandler.HandleEvent(r.Context(), *event); err != nil {
			// Log error but still return OK - activity is logged regardless
			// The status update may fail if task/agent doesn't exist
			_ = err
		}
	} else {
		// For unsupported events, just log to activity feed
		var taskArg interface{}
		if event.TaskID != "" {
			taskArg = event.TaskID
		}
		var agentArg interface{}
		if event.AgentID != "" {
			agentArg = event.AgentID
		}

		_, err = db.ExecContext(
			r.Context(),
			"INSERT INTO activity_log (org_id, task_id, agent_id, action, metadata) VALUES ($1, $2, $3, $4, $5)",
			event.OrgID,
			taskArg,
			agentArg,
			event.Event,
			json.RawMessage(body),
		)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to record webhook"})
			return
		}
	}

	sendJSON(w, http.StatusOK, openClawWebhookResponse{OK: true})
}

// OpenClawWebhookHandler handles POST /api/webhooks/openclaw (legacy, no WebSocket support)
func OpenClawWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	secret := strings.TrimSpace(os.Getenv("OPENCLAW_WEBHOOK_SECRET"))
	if secret == "" {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "webhook secret not configured"})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unable to read request body"})
		return
	}

	signature := firstNonEmpty(
		strings.TrimSpace(r.Header.Get(openClawSignatureHeader)),
		strings.TrimSpace(r.Header.Get(gitClawSignatureHeader)),
		strings.TrimSpace(r.Header.Get(aiHubSignatureHeader)),
	)
	if signature == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing signature"})
		return
	}
	if !verifyOpenClawSignature(body, signature, secret) {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid signature"})
		return
	}

	var payload openClawWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	orgID := strings.TrimSpace(payload.OrgID)
	if orgID == "" {
		orgID = strings.TrimSpace(payload.OrganizationID)
	}
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	event := strings.TrimSpace(payload.Event)
	if event == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing event"})
		return
	}

	taskID := strings.TrimSpace(payload.TaskID)
	agentID := strings.TrimSpace(payload.AgentID)
	if payload.Task != nil && strings.TrimSpace(payload.Task.ID) != "" {
		taskID = strings.TrimSpace(payload.Task.ID)
	}
	if payload.Agent != nil && strings.TrimSpace(payload.Agent.ID) != "" {
		agentID = strings.TrimSpace(payload.Agent.ID)
	}

	if strings.HasPrefix(event, "task.") {
		if taskID == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing task id"})
			return
		}
		if !uuidRegex.MatchString(taskID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid task id"})
			return
		}
	} else if strings.HasPrefix(event, "agent.") {
		if agentID == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing agent id"})
			return
		}
		if !uuidRegex.MatchString(agentID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid agent id"})
			return
		}
	} else {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unsupported event"})
		return
	}

	db, err := getWebhooksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	var taskArg interface{}
	if taskID != "" {
		taskArg = taskID
	}
	var agentArg interface{}
	if agentID != "" {
		agentArg = agentID
	}

	_, err = db.ExecContext(
		r.Context(),
		"INSERT INTO activity_log (org_id, task_id, agent_id, action, metadata) VALUES ($1, $2, $3, $4, $5)",
		orgID,
		taskArg,
		agentArg,
		event,
		json.RawMessage(body),
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to record webhook"})
		return
	}

	sendJSON(w, http.StatusOK, openClawWebhookResponse{OK: true})
}

func getWebhooksDB() (*sql.DB, error) {
	webhooksDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			webhooksDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			webhooksDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			webhooksDBErr = err
			return
		}

		webhooksDB = db
	})

	return webhooksDB, webhooksDBErr
}

func verifyOpenClawSignature(body []byte, signature, secret string) bool {
	if signature == "" || secret == "" {
		return false
	}

	expected := computeOpenClawSignature(body, secret)
	sig := strings.TrimSpace(signature)
	if !strings.HasPrefix(sig, "sha256=") {
		sig = "sha256=" + sig
	}

	return hmac.Equal([]byte(expected), []byte(sig))
}

func computeOpenClawSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	sum := mac.Sum(nil)
	return "sha256=" + hex.EncodeToString(sum)
}
