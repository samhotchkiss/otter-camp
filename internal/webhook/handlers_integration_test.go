package webhook

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusEvent_JSONSerialization(t *testing.T) {
	t.Parallel()

	event := StatusEvent{
		Event:   EventTaskStarted,
		OrgID:   "org-123",
		TaskID:  "task-456",
		AgentID: "agent-789",
		Task: &TaskPayload{
			ID:             "task-456",
			Status:         "in_progress",
			PreviousStatus: "queued",
		},
		Agent: &AgentPayload{
			ID:     "agent-789",
			Status: "busy",
		},
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	var parsed StatusEvent
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, EventTaskStarted, parsed.Event)
	assert.Equal(t, "org-123", parsed.OrgID)
	assert.Equal(t, "task-456", parsed.TaskID)
	assert.Equal(t, "agent-789", parsed.AgentID)
}

func TestTaskPayload_JSONSerialization(t *testing.T) {
	t.Parallel()

	payload := TaskPayload{
		ID:             "task-123",
		Status:         "done",
		PreviousStatus: "in_progress",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var parsed TaskPayload
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "task-123", parsed.ID)
	assert.Equal(t, "done", parsed.Status)
	assert.Equal(t, "in_progress", parsed.PreviousStatus)
}

func TestAgentPayload_JSONSerialization(t *testing.T) {
	t.Parallel()

	payload := AgentPayload{
		ID:     "agent-123",
		Status: "active",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var parsed AgentPayload
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "agent-123", parsed.ID)
	assert.Equal(t, "active", parsed.Status)
}

func TestParseStatusEvent_AllEventTypes(t *testing.T) {
	t.Parallel()

	eventTypes := []string{
		EventTaskStarted,
		EventTaskCompleted,
		EventTaskFailed,
		EventTaskUpdated,
		EventTaskProgress,
		EventAgentStatus,
	}

	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			body := `{"event":"` + eventType + `","org_id":"550e8400-e29b-41d4-a716-446655440000"}`

			event, err := ParseStatusEvent([]byte(body))
			require.NoError(t, err)
			assert.Equal(t, eventType, event.Event)
		})
	}
}

func TestParseStatusEvent_EmptyBody(t *testing.T) {
	t.Parallel()

	event, err := ParseStatusEvent([]byte("{}"))
	require.NoError(t, err)
	assert.Empty(t, event.Event)
	assert.Empty(t, event.OrgID)
}

func TestParseStatusEvent_NullValues(t *testing.T) {
	t.Parallel()

	body := `{"event":"task.started","org_id":"org-1","task":null,"agent":null}`

	event, err := ParseStatusEvent([]byte(body))
	require.NoError(t, err)
	assert.Nil(t, event.Task)
	assert.Nil(t, event.Agent)
}

func TestStatusHandler_New(t *testing.T) {
	t.Parallel()

	handler := NewStatusHandler(nil, nil)
	assert.NotNil(t, handler)
}

func TestStatusHandler_HandleEvent_AllTaskEvents_TaskResolution(t *testing.T) {
	t.Parallel()

	h := &StatusHandler{} // No database - just validates task ID resolution

	taskID := "550e8400-e29b-41d4-a716-446655440001"
	events := []string{EventTaskStarted, EventTaskCompleted, EventTaskFailed}

	for _, eventType := range events {
		t.Run(eventType+"_with_task_id", func(t *testing.T) {
			event := StatusEvent{
				Event:  eventType,
				OrgID:  "550e8400-e29b-41d4-a716-446655440000",
				TaskID: taskID,
			}
			// Validate task ID resolution
			resolved := h.resolveTaskID(event)
			assert.Equal(t, taskID, resolved)
		})
	}
}

func TestStatusHandler_HandleEvent_AgentWithPayload(t *testing.T) {
	t.Parallel()

	h := &StatusHandler{} // No database - just validates event parsing/resolution

	event := StatusEvent{
		Event: EventAgentStatus,
		OrgID: "550e8400-e29b-41d4-a716-446655440000",
		Agent: &AgentPayload{
			ID:     "550e8400-e29b-41d4-a716-446655440001",
			Status: "active",
		},
	}
	
	// Should resolve agent ID from payload
	agentID := h.resolveAgentID(event)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440001", agentID)
}

func TestStatusHandler_ResolveTaskID_Priority(t *testing.T) {
	t.Parallel()

	h := &StatusHandler{}

	// Task.ID takes priority over TaskID
	event := StatusEvent{
		TaskID: "fallback-id",
		Task:   &TaskPayload{ID: "priority-id"},
	}

	result := h.resolveTaskID(event)
	assert.Equal(t, "priority-id", result)
}

func TestStatusHandler_ResolveAgentID_Priority(t *testing.T) {
	t.Parallel()

	h := &StatusHandler{}

	// Agent.ID takes priority over AgentID
	event := StatusEvent{
		AgentID: "fallback-id",
		Agent:   &AgentPayload{ID: "priority-id"},
	}

	result := h.resolveAgentID(event)
	assert.Equal(t, "priority-id", result)
}

func TestEventConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "task.started", EventTaskStarted)
	assert.Equal(t, "task.completed", EventTaskCompleted)
	assert.Equal(t, "task.failed", EventTaskFailed)
	assert.Equal(t, "task.updated", EventTaskUpdated)
	assert.Equal(t, "task.progress", EventTaskProgress)
	assert.Equal(t, "agent.status", EventAgentStatus)
}

func TestIsSupportedEvent_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected bool
	}{
		// Supported
		{EventTaskStarted, true},
		{EventTaskCompleted, true},
		{EventTaskFailed, true},
		{EventTaskUpdated, true},
		{EventTaskProgress, true},
		{EventAgentStatus, true},

		// Case sensitivity
		{"Task.Started", false},
		{"TASK.STARTED", false},

		// Similar but invalid
		{"task.start", false},
		{"task.complete", false},
		{"task.fail", false},
		{"agent.update", false},

		// Empty and whitespace
		{"", false},
		{" ", false},
		{"  task.started  ", false},

		// Prefixes and suffixes
		{"task.started ", false},
		{" task.started", false},
		{"prefix.task.started", false},
		{"task.started.suffix", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := IsSupportedEvent(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseStatusEvent_WithRawBody(t *testing.T) {
	t.Parallel()

	body := `{"event":"task.started","org_id":"org-1","task_id":"task-1","extra":"data"}`

	event, err := ParseStatusEvent([]byte(body))
	require.NoError(t, err)

	// RawBody should contain the original payload
	assert.Equal(t, json.RawMessage(body), event.RawBody)

	// Extra fields should be accessible from RawBody
	var raw map[string]interface{}
	err = json.Unmarshal(event.RawBody, &raw)
	require.NoError(t, err)
	assert.Equal(t, "data", raw["extra"])
}

func TestStatusHandler_BroadcastTaskStatusChanged_NilHub(t *testing.T) {
	t.Parallel()

	h := &StatusHandler{hub: nil}

	// Should not panic with nil hub
	h.broadcastTaskStatusChanged(nil, "")
}

func TestStatusHandler_BroadcastAgentStatusChanged_NilHub(t *testing.T) {
	t.Parallel()

	h := &StatusHandler{hub: nil}

	// Should not panic with nil hub
	h.broadcastAgentStatusChanged("org-1", nil)
}

func TestMessageTypes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "TaskStatusChanged", string(MessageTaskStatusChanged))
	assert.Equal(t, "AgentStatusChanged", string(MessageAgentStatusChanged))
}
