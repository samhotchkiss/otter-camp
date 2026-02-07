package integration

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleWebhookPayload() WebhookPayload {
	return WebhookPayload{
		Event:        "task.dispatch",
		Timestamp:    time.Date(2026, time.February, 3, 11, 30, 0, 0, time.UTC),
		Installation: "sam-openclaw",
		Agent:        "derek",
		Task: WebhookTask{
			ID:       "eng-042",
			Number:   42,
			Title:    "Implement retry logic for 500 errors",
			Body:     "When Anthropic returns HTTP 500, we should retry 2-3 times...",
			Status:   "dispatched",
			Priority: 1,
			Context: WebhookTaskContext{
				Files: []WebhookContextFile{
					{Repo: "pearl", Path: "src/providers/anthropic.ts"},
					{Repo: "pearl", Path: "src/core/retry.ts"},
				},
				Decisions:  []string{"Use exponential backoff with jitter", "Max 3 retries for 500s"},
				Acceptance: []string{"500 errors trigger retry before failover", "Retry count logged to session"},
				Custom: map[string]interface{}{
					"related_issue": "https://github.com/example/repo/issues/123",
					"slack_thread":  "https://example.slack.com/archives/C123/p456",
				},
			},
			Dependencies: []string{},
			Labels:       []string{"backend", "reliability"},
			Project: WebhookProject{
				ID:   "pearl",
				Name: "Pearl",
			},
			CreatedBy: "frank",
			CreatedAt: time.Date(2026, time.February, 3, 10, 0, 0, 0, time.UTC),
		},
		CallbackURL: "https://hub.example.com/api/tasks/eng-042/status",
	}
}

func TestWebhookPayloadSchema(t *testing.T) {
	payload := sampleWebhookPayload()

	body, err := MarshalWebhookPayload(payload)
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &got))

	expected := map[string]interface{}{
		"event":        "task.dispatch",
		"timestamp":    "2026-02-03T11:30:00Z",
		"installation": "sam-openclaw",
		"agent":        "derek",
		"callback_url": "https://hub.example.com/api/tasks/eng-042/status",
		"task": map[string]interface{}{
			"id":       "eng-042",
			"number":   float64(42),
			"title":    "Implement retry logic for 500 errors",
			"body":     "When Anthropic returns HTTP 500, we should retry 2-3 times...",
			"status":   "dispatched",
			"priority": float64(1),
			"context": map[string]interface{}{
				"files": []interface{}{
					map[string]interface{}{
						"repo": "pearl",
						"path": "src/providers/anthropic.ts",
					},
					map[string]interface{}{
						"repo": "pearl",
						"path": "src/core/retry.ts",
					},
				},
				"decisions": []interface{}{
					"Use exponential backoff with jitter",
					"Max 3 retries for 500s",
				},
				"acceptance": []interface{}{
					"500 errors trigger retry before failover",
					"Retry count logged to session",
				},
				"custom": map[string]interface{}{
					"related_issue": "https://github.com/example/repo/issues/123",
					"slack_thread":  "https://example.slack.com/archives/C123/p456",
				},
			},
			"dependencies": []interface{}{},
			"labels": []interface{}{
				"backend",
				"reliability",
			},
			"project": map[string]interface{}{
				"id":   "pearl",
				"name": "Pearl",
			},
			"created_by": "frank",
			"created_at": "2026-02-03T10:00:00Z",
		},
	}

	assert.Equal(t, expected, got)
}

func TestWebhookPayloadTask(t *testing.T) {
	payload := sampleWebhookPayload()

	assert.NotEmpty(t, payload.Task.ID)
	assert.NotZero(t, payload.Task.Number)
	assert.NotEmpty(t, payload.Task.Title)
	assert.NotEmpty(t, payload.Task.Body)
	assert.NotEmpty(t, payload.Task.Status)
	assert.NotZero(t, payload.Task.Priority)
	assert.NotNil(t, payload.Task.Context)
	assert.NotNil(t, payload.Task.Dependencies)
	assert.NotNil(t, payload.Task.Labels)
	assert.NotEmpty(t, payload.Task.Project.ID)
	assert.NotEmpty(t, payload.Task.Project.Name)
	assert.NotEmpty(t, payload.Task.CreatedBy)
	assert.False(t, payload.Task.CreatedAt.IsZero())
}

func TestWebhookPayloadContext(t *testing.T) {
	payload := sampleWebhookPayload()

	require.Len(t, payload.Task.Context.Files, 2)
	require.Len(t, payload.Task.Context.Decisions, 2)
	require.Len(t, payload.Task.Context.Acceptance, 2)
	require.NotEmpty(t, payload.Task.Context.Custom)
}

func TestWebhookPayloadCallback(t *testing.T) {
	payload := sampleWebhookPayload()

	parsed, err := url.Parse(payload.CallbackURL)
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.NotEmpty(t, parsed.Host)
}
