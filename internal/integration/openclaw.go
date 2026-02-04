package integration

import (
	"encoding/json"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/webhook"
)

// WebhookPayload matches the OpenClaw dispatch schema.
type WebhookPayload struct {
	Event        string      `json:"event"`
	Timestamp    time.Time   `json:"timestamp"`
	Installation string      `json:"installation"`
	Agent        string      `json:"agent"`
	Task         WebhookTask `json:"task"`
	CallbackURL  string      `json:"callback_url"`
}

// WebhookTask contains the dispatched task data.
type WebhookTask struct {
	ID           string             `json:"id"`
	Number       int                `json:"number"`
	Title        string             `json:"title"`
	Body         string             `json:"body"`
	Status       string             `json:"status"`
	Priority     int                `json:"priority"`
	Context      WebhookTaskContext `json:"context"`
	Dependencies []string           `json:"dependencies"`
	Labels       []string           `json:"labels"`
	Project      WebhookProject     `json:"project"`
	CreatedBy    string             `json:"created_by"`
	CreatedAt    time.Time          `json:"created_at"`
}

// WebhookTaskContext provides structured context for the task.
type WebhookTaskContext struct {
	Files      []WebhookContextFile   `json:"files"`
	Decisions  []string               `json:"decisions"`
	Acceptance []string               `json:"acceptance"`
	Custom     map[string]interface{} `json:"custom"`
}

// WebhookContextFile identifies a referenced file in context.
type WebhookContextFile struct {
	Repo string `json:"repo"`
	Path string `json:"path"`
}

// WebhookProject contains project metadata.
type WebhookProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MarshalWebhookPayload encodes the payload as JSON.
func MarshalWebhookPayload(payload WebhookPayload) ([]byte, error) {
	return json.Marshal(payload)
}

// SignWebhookPayload signs the payload bytes with HMAC-SHA256.
func SignWebhookPayload(body []byte, secret string) string {
	return webhook.Sign(body, secret)
}
