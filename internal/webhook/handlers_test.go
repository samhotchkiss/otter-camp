package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStatusEvent(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantEvent string
		wantOrgID string
		wantErr   bool
	}{
		{
			name:      "task.started event",
			body:      `{"event":"task.started","org_id":"550e8400-e29b-41d4-a716-446655440000","task_id":"550e8400-e29b-41d4-a716-446655440001"}`,
			wantEvent: EventTaskStarted,
			wantOrgID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:      "task.completed event",
			body:      `{"event":"task.completed","org_id":"550e8400-e29b-41d4-a716-446655440000","task":{"id":"550e8400-e29b-41d4-a716-446655440001","status":"done","previous_status":"in_progress"}}`,
			wantEvent: EventTaskCompleted,
			wantOrgID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:      "task.failed event",
			body:      `{"event":"task.failed","org_id":"550e8400-e29b-41d4-a716-446655440000","task_id":"550e8400-e29b-41d4-a716-446655440001"}`,
			wantEvent: EventTaskFailed,
			wantOrgID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:      "agent.status event",
			body:      `{"event":"agent.status","org_id":"550e8400-e29b-41d4-a716-446655440000","agent":{"id":"550e8400-e29b-41d4-a716-446655440002","status":"active"}}`,
			wantEvent: EventAgentStatus,
			wantOrgID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:      "organization_id fallback",
			body:      `{"event":"task.started","organization_id":"550e8400-e29b-41d4-a716-446655440000","task_id":"550e8400-e29b-41d4-a716-446655440001"}`,
			wantEvent: EventTaskStarted,
			wantOrgID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:    "invalid JSON",
			body:    `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := ParseStatusEvent([]byte(tt.body))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantEvent, event.Event)
			assert.Equal(t, tt.wantOrgID, event.OrgID)
			assert.Equal(t, json.RawMessage(tt.body), event.RawBody)
		})
	}
}

func TestIsSupportedEvent(t *testing.T) {
	tests := []struct {
		eventType string
		want      bool
	}{
		{EventTaskStarted, true},
		{EventTaskCompleted, true},
		{EventTaskFailed, true},
		{EventTaskUpdated, true},
		{EventTaskProgress, true},
		{EventAgentStatus, true},
		{"task.unknown", false},
		{"agent.unknown", false},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			got := IsSupportedEvent(tt.eventType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStatusHandler_resolveTaskID(t *testing.T) {
	h := &StatusHandler{}

	tests := []struct {
		name  string
		event StatusEvent
		want  string
	}{
		{
			name: "from task.id",
			event: StatusEvent{
				TaskID: "task-id-1",
				Task:   &TaskPayload{ID: "task-id-2"},
			},
			want: "task-id-2",
		},
		{
			name: "from task_id fallback",
			event: StatusEvent{
				TaskID: "task-id-1",
			},
			want: "task-id-1",
		},
		{
			name: "with whitespace",
			event: StatusEvent{
				TaskID: "  task-id-1  ",
			},
			want: "task-id-1",
		},
		{
			name:  "empty",
			event: StatusEvent{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.resolveTaskID(tt.event)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStatusHandler_resolveAgentID(t *testing.T) {
	h := &StatusHandler{}

	tests := []struct {
		name  string
		event StatusEvent
		want  string
	}{
		{
			name: "from agent.id",
			event: StatusEvent{
				AgentID: "agent-id-1",
				Agent:   &AgentPayload{ID: "agent-id-2"},
			},
			want: "agent-id-2",
		},
		{
			name: "from agent_id fallback",
			event: StatusEvent{
				AgentID: "agent-id-1",
			},
			want: "agent-id-1",
		},
		{
			name: "with whitespace",
			event: StatusEvent{
				AgentID: "  agent-id-1  ",
			},
			want: "agent-id-1",
		},
		{
			name:  "empty",
			event: StatusEvent{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.resolveAgentID(tt.event)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStatusHandler_HandleEvent_UnsupportedEvent(t *testing.T) {
	h := &StatusHandler{}
	
	err := h.HandleEvent(context.Background(), StatusEvent{
		Event: "unknown.event",
		OrgID: "550e8400-e29b-41d4-a716-446655440000",
	})
	
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported event type")
}

func TestStatusHandler_HandleEvent_MissingTaskID(t *testing.T) {
	h := &StatusHandler{}

	tests := []string{EventTaskStarted, EventTaskCompleted, EventTaskFailed}
	for _, eventType := range tests {
		t.Run(eventType, func(t *testing.T) {
			err := h.HandleEvent(context.Background(), StatusEvent{
				Event: eventType,
				OrgID: "550e8400-e29b-41d4-a716-446655440000",
				// No TaskID
			})

			assert.Error(t, err)
			assert.Contains(t, err.Error(), "missing task ID")
		})
	}
}

func TestStatusHandler_HandleEvent_MissingAgentID(t *testing.T) {
	h := &StatusHandler{}

	err := h.HandleEvent(context.Background(), StatusEvent{
		Event: EventAgentStatus,
		OrgID: "550e8400-e29b-41d4-a716-446655440000",
		// No AgentID
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing agent ID")
}

func TestTaskPayload(t *testing.T) {
	body := `{"event":"task.completed","org_id":"org-1","task":{"id":"task-1","status":"done","previous_status":"in_progress"}}`
	
	event, err := ParseStatusEvent([]byte(body))
	require.NoError(t, err)
	
	require.NotNil(t, event.Task)
	assert.Equal(t, "task-1", event.Task.ID)
	assert.Equal(t, "done", event.Task.Status)
	assert.Equal(t, "in_progress", event.Task.PreviousStatus)
}

func TestIsValidTaskTransition(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		expected bool
	}{
		{name: "queued to dispatched", from: "queued", to: "dispatched", expected: true},
		{name: "queued to in_progress", from: "queued", to: "in_progress", expected: true},
		{name: "queued to cancelled", from: "queued", to: "cancelled", expected: true},
		{name: "dispatched to in_progress", from: "dispatched", to: "in_progress", expected: true},
		{name: "dispatched to queued", from: "dispatched", to: "queued", expected: true},
		{name: "in_progress to review", from: "in_progress", to: "review", expected: true},
		{name: "in_progress to done", from: "in_progress", to: "done", expected: true},
		{name: "in_progress to blocked", from: "in_progress", to: "blocked", expected: true},
		{name: "review to in_progress", from: "review", to: "in_progress", expected: true},
		{name: "review to done", from: "review", to: "done", expected: true},
		{name: "blocked to queued", from: "blocked", to: "queued", expected: true},
		{name: "done to queued", from: "done", to: "queued", expected: true},
		{name: "cancelled to queued", from: "cancelled", to: "queued", expected: true},
		{name: "invalid transition", from: "done", to: "in_progress", expected: false},
		{name: "unknown from", from: "unknown", to: "queued", expected: false},
		{name: "unknown to", from: "queued", to: "unknown", expected: false},
		{name: "empty from", from: "", to: "queued", expected: false},
		{name: "empty to", from: "queued", to: "", expected: false},
		{name: "same status", from: "queued", to: "queued", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isValidTaskTransition(tt.from, tt.to))
		})
	}
}

func TestAgentPayload(t *testing.T) {
	body := `{"event":"agent.status","org_id":"org-1","agent":{"id":"agent-1","status":"busy"}}`
	
	event, err := ParseStatusEvent([]byte(body))
	require.NoError(t, err)
	
	require.NotNil(t, event.Agent)
	assert.Equal(t, "agent-1", event.Agent.ID)
	assert.Equal(t, "busy", event.Agent.Status)
}
