package feed

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizer_AllTypes(t *testing.T) {
	t.Parallel()

	s := NewSummarizer()

	agentName := "TestBot"
	taskTitle := "Fix the bug"

	tests := []struct {
		name          string
		itemType      string
		metadata      json.RawMessage
		wantContains  []string
		wantNotContain []string
	}{
		{
			name:         "task_created with task",
			itemType:     "task_created",
			wantContains: []string{agentName, "created task", taskTitle},
		},
		{
			name:         "task_created without task",
			itemType:     "task_created",
			wantContains: []string{agentName, "created a task"},
		},
		{
			name:         "task_update with task",
			itemType:     "task_update",
			wantContains: []string{agentName, "updated task", taskTitle},
		},
		{
			name:         "task_updated with task",
			itemType:     "task_updated",
			wantContains: []string{agentName, "updated task", taskTitle},
		},
		{
			name:         "task_status_changed with status",
			itemType:     "task_status_changed",
			metadata:     json.RawMessage(`{"new_status":"done"}`),
			wantContains: []string{agentName, "changed task", taskTitle, "done"},
		},
		{
			name:         "task_status_changed without status",
			itemType:     "task_status_changed",
			wantContains: []string{agentName, "changed a task status"},
		},
		{
			name:         "message with preview",
			itemType:     "message",
			metadata:     json.RawMessage(`{"preview":"Hello world"}`),
			wantContains: []string{agentName, "Hello world"},
		},
		{
			name:         "message with long preview",
			itemType:     "message",
			metadata:     json.RawMessage(`{"preview":"This is a very long preview message that should be truncated at fifty characters or so"}`),
			wantContains: []string{agentName, "..."},
		},
		{
			name:         "message without preview",
			itemType:     "message",
			wantContains: []string{agentName, "sent a message"},
		},
		{
			name:         "comment with task",
			itemType:     "comment",
			wantContains: []string{agentName, "commented on", taskTitle},
		},
		{
			name:         "comment without task",
			itemType:     "comment",
			wantContains: []string{agentName, "added a comment"},
		},
		{
			name:         "commit with repo and message",
			itemType:     "commit",
			metadata:     json.RawMessage(`{"repo":"myrepo","message":"Fixed issue"}`),
			wantContains: []string{agentName, "committed to", "myrepo", "Fixed issue"},
		},
		{
			name:         "commit with repo only",
			itemType:     "commit",
			metadata:     json.RawMessage(`{"repo":"myrepo"}`),
			wantContains: []string{agentName, "committed to", "myrepo"},
		},
		{
			name:         "commit with long message",
			itemType:     "commit",
			metadata:     json.RawMessage(`{"repo":"myrepo","message":"This is a very long commit message that should be truncated"}`),
			wantContains: []string{agentName, "myrepo", "..."},
		},
		{
			name:         "commit minimal",
			itemType:     "commit",
			wantContains: []string{agentName, "made a commit"},
		},
		{
			name:         "dispatch with task",
			itemType:     "dispatch",
			wantContains: []string{"Task", taskTitle, "dispatched to", agentName},
		},
		{
			name:         "dispatch without task",
			itemType:     "dispatch",
			wantContains: []string{"dispatched to", agentName},
		},
		{
			name:         "assignment with task",
			itemType:     "assignment",
			wantContains: []string{agentName, "was assigned to", taskTitle},
		},
		{
			name:         "assignment without task",
			itemType:     "assignment",
			wantContains: []string{agentName, "received an assignment"},
		},
		{
			name:         "unknown type with task",
			itemType:     "custom_event",
			wantContains: []string{agentName, "custom_event", taskTitle},
		},
		{
			name:         "unknown type without task",
			itemType:     "custom_event",
			wantContains: []string{agentName, "custom_event"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &Item{
				ID:        "item-1",
				OrgID:     "org-1",
				Type:      tt.itemType,
				Metadata:  tt.metadata,
				CreatedAt: time.Now(),
				AgentName: &agentName,
			}

			// Some tests need task title, some don't
			if tt.name != "task_created without task" &&
				tt.name != "comment without task" &&
				tt.name != "dispatch without task" &&
				tt.name != "assignment without task" &&
				tt.name != "unknown type without task" &&
				tt.name != "task_status_changed without status" &&
				tt.name != "message with preview" &&
				tt.name != "message with long preview" &&
				tt.name != "message without preview" &&
				tt.name != "commit with repo and message" &&
				tt.name != "commit with repo only" &&
				tt.name != "commit with long message" &&
				tt.name != "commit minimal" {
				item.TaskTitle = &taskTitle
			}

			summary := s.Summarize(item)

			for _, want := range tt.wantContains {
				assert.Contains(t, summary, want, "summary should contain %q", want)
			}
		})
	}
}

func TestSummarizer_NoAgent(t *testing.T) {
	t.Parallel()

	s := NewSummarizer()

	item := &Item{
		ID:        "item-1",
		OrgID:     "org-1",
		Type:      "task_created",
		CreatedAt: time.Now(),
		// No AgentName
	}

	summary := s.Summarize(item)
	assert.Contains(t, summary, "Someone")
}

func TestSummarizer_EmptyAgentName(t *testing.T) {
	t.Parallel()

	s := NewSummarizer()

	emptyName := ""
	item := &Item{
		ID:        "item-1",
		OrgID:     "org-1",
		Type:      "task_created",
		CreatedAt: time.Now(),
		AgentName: &emptyName,
	}

	summary := s.Summarize(item)
	assert.Contains(t, summary, "Someone")
}

func TestSummarizer_SummarizeItems(t *testing.T) {
	t.Parallel()

	s := NewSummarizer()

	agentName := "Bot"
	items := []*Item{
		{ID: "1", Type: "task_created", AgentName: &agentName},
		{ID: "2", Type: "message", AgentName: &agentName},
		{ID: "3", Type: "commit", AgentName: &agentName},
	}

	s.SummarizeItems(items)

	for _, item := range items {
		assert.NotEmpty(t, item.Summary)
	}
}

func TestExtractMetadataString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata json.RawMessage
		key      string
		want     string
	}{
		{
			name:     "valid key",
			metadata: json.RawMessage(`{"key":"value"}`),
			key:      "key",
			want:     "value",
		},
		{
			name:     "missing key",
			metadata: json.RawMessage(`{"other":"value"}`),
			key:      "key",
			want:     "",
		},
		{
			name:     "nil metadata",
			metadata: nil,
			key:      "key",
			want:     "",
		},
		{
			name:     "empty metadata",
			metadata: json.RawMessage(`{}`),
			key:      "key",
			want:     "",
		},
		{
			name:     "invalid JSON",
			metadata: json.RawMessage(`{invalid}`),
			key:      "key",
			want:     "",
		},
		{
			name:     "non-string value",
			metadata: json.RawMessage(`{"key":123}`),
			key:      "key",
			want:     "",
		},
		{
			name:     "nested object",
			metadata: json.RawMessage(`{"nested":{"inner":"value"}}`),
			key:      "nested",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMetadataString(tt.metadata, tt.key)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestItem_JSONSerialization(t *testing.T) {
	t.Parallel()

	taskID := "task-123"
	agentID := "agent-456"
	taskTitle := "Test Task"
	agentName := "TestBot"
	now := time.Now().UTC().Truncate(time.Second)

	item := Item{
		ID:        "item-789",
		OrgID:     "org-111",
		TaskID:    &taskID,
		AgentID:   &agentID,
		Type:      "task_created",
		Metadata:  json.RawMessage(`{"key":"value"}`),
		CreatedAt: now,
		TaskTitle: &taskTitle,
		AgentName: &agentName,
		Summary:   "TestBot created task \"Test Task\"",
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	var parsed Item
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, item.ID, parsed.ID)
	assert.Equal(t, item.OrgID, parsed.OrgID)
	assert.Equal(t, *item.TaskID, *parsed.TaskID)
	assert.Equal(t, *item.AgentID, *parsed.AgentID)
	assert.Equal(t, item.Type, parsed.Type)
	assert.Equal(t, *item.TaskTitle, *parsed.TaskTitle)
	assert.Equal(t, *item.AgentName, *parsed.AgentName)
	assert.Equal(t, item.Summary, parsed.Summary)
}

func TestItem_JSONSerialization_Optional(t *testing.T) {
	t.Parallel()

	item := Item{
		ID:        "item-1",
		OrgID:     "org-1",
		Type:      "message",
		Metadata:  json.RawMessage(`{}`),
		CreatedAt: time.Now(),
		// No optional fields
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	// Verify optional fields are omitted
	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	_, hasTaskID := parsed["task_id"]
	_, hasAgentID := parsed["agent_id"]
	_, hasTaskTitle := parsed["task_title"]
	_, hasAgentName := parsed["agent_name"]

	assert.False(t, hasTaskID, "task_id should be omitted when nil")
	assert.False(t, hasAgentID, "agent_id should be omitted when nil")
	assert.False(t, hasTaskTitle, "task_title should be omitted when nil")
	assert.False(t, hasAgentName, "agent_name should be omitted when nil")
}

func TestNewSummarizer(t *testing.T) {
	t.Parallel()

	s := NewSummarizer()
	assert.NotNil(t, s)
}
