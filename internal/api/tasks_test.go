package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper validation functions

func TestIsValidStatus(t *testing.T) {
	t.Parallel()

	validStatuses := []string{"queued", "dispatched", "in_progress", "blocked", "review", "done", "cancelled"}
	for _, status := range validStatuses {
		assert.True(t, isValidStatus(status), "expected %q to be valid", status)
	}

	invalidStatuses := []string{"invalid", "pending", "completed", "", "QUEUED", "Done"}
	for _, status := range invalidStatuses {
		assert.False(t, isValidStatus(status), "expected %q to be invalid", status)
	}
}

func TestIsValidPriority(t *testing.T) {
	t.Parallel()

	validPriorities := []string{"P0", "P1", "P2", "P3"}
	for _, priority := range validPriorities {
		assert.True(t, isValidPriority(priority), "expected %q to be valid", priority)
	}

	invalidPriorities := []string{"p0", "P4", "high", "low", "", "0"}
	for _, priority := range invalidPriorities {
		assert.False(t, isValidPriority(priority), "expected %q to be invalid", priority)
	}
}

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"queued", "queued"},
		{"QUEUED", "queued"},
		{"  Queued  ", "queued"},
		{"IN_PROGRESS", "in_progress"},
		{"", ""},
	}

	for _, tt := range tests {
		result := normalizeStatus(tt.input)
		assert.Equal(t, tt.expected, result, "normalizeStatus(%q)", tt.input)
	}
}

func TestNormalizePriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"P0", "P0"},
		{"p1", "P1"},
		{"  p2  ", "P2"},
		{"p3", "P3"},
		{"", ""},
	}

	for _, tt := range tests {
		result := normalizePriority(tt.input)
		assert.Equal(t, tt.expected, result, "normalizePriority(%q)", tt.input)
	}
}

func TestNormalizeContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    json.RawMessage
		expected string
	}{
		{"nil becomes empty object", nil, "{}"},
		{"empty becomes empty object", json.RawMessage{}, "{}"},
		{"null becomes empty object", json.RawMessage("null"), "{}"},
		{"valid JSON preserved", json.RawMessage(`{"key":"value"}`), `{"key":"value"}`},
		{"array preserved", json.RawMessage(`[1,2,3]`), `[1,2,3]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeContext(tt.input)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestValidateOptionalUUID(t *testing.T) {
	t.Parallel()

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	invalidUUID := "not-a-uuid"
	emptyUUID := ""
	whitespaceUUID := "  550e8400-e29b-41d4-a716-446655440000  "

	// nil is valid
	err := validateOptionalUUID(nil, "test_field")
	assert.NoError(t, err)

	// valid UUID
	ptr := &validUUID
	err = validateOptionalUUID(ptr, "test_field")
	assert.NoError(t, err)
	assert.Equal(t, validUUID, *ptr) // should be unchanged

	// invalid UUID
	ptr = &invalidUUID
	err = validateOptionalUUID(ptr, "test_field")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid test_field")

	// empty string
	ptr = &emptyUUID
	err = validateOptionalUUID(ptr, "test_field")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be empty")

	// whitespace is trimmed
	ptr = &whitespaceUUID
	err = validateOptionalUUID(ptr, "test_field")
	assert.NoError(t, err)
	assert.Equal(t, validUUID, *ptr) // should be trimmed
}

func TestNullableString(t *testing.T) {
	t.Parallel()

	// nil returns nil
	assert.Nil(t, nullableString(nil))

	// empty string returns nil
	empty := ""
	assert.Nil(t, nullableString(&empty))

	// whitespace-only returns nil
	whitespace := "   "
	assert.Nil(t, nullableString(&whitespace))

	// non-empty string returns trimmed value
	value := "  test value  "
	result := nullableString(&value)
	assert.Equal(t, "test value", result)
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "first", firstNonEmpty("first", "second", "third"))
	assert.Equal(t, "second", firstNonEmpty("", "second", "third"))
	assert.Equal(t, "third", firstNonEmpty("", "", "third"))
	assert.Equal(t, "", firstNonEmpty("", "", ""))
	assert.Equal(t, "", firstNonEmpty())
	assert.Equal(t, "value", firstNonEmpty("  ", "  ", "value"))
}

func TestParseOptionalStringField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      map[string]json.RawMessage
		key      string
		wantVal  *string
		wantSet  bool
		wantErr  bool
	}{
		{
			name:    "key not present",
			raw:     map[string]json.RawMessage{},
			key:     "missing",
			wantVal: nil,
			wantSet: false,
		},
		{
			name:    "null value",
			raw:     map[string]json.RawMessage{"key": json.RawMessage("null")},
			key:     "key",
			wantVal: nil,
			wantSet: true,
		},
		{
			name:    "empty raw message",
			raw:     map[string]json.RawMessage{"key": json.RawMessage{}},
			key:     "key",
			wantVal: nil,
			wantSet: true,
		},
		{
			name:    "valid string",
			raw:     map[string]json.RawMessage{"key": json.RawMessage(`"hello"`)},
			key:     "key",
			wantVal: strPtr("hello"),
			wantSet: true,
		},
		{
			name:    "invalid JSON",
			raw:     map[string]json.RawMessage{"key": json.RawMessage(`not valid`)},
			key:     "key",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, set, err := parseOptionalStringField(tt.raw, tt.key)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSet, set)
			if tt.wantVal == nil {
				assert.Nil(t, val)
			} else {
				require.NotNil(t, val)
				assert.Equal(t, *tt.wantVal, *val)
			}
		})
	}
}

func TestParseOptionalRawField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     map[string]json.RawMessage
		key     string
		wantVal json.RawMessage
		wantSet bool
	}{
		{
			name:    "key not present",
			raw:     map[string]json.RawMessage{},
			key:     "missing",
			wantVal: nil,
			wantSet: false,
		},
		{
			name:    "null value",
			raw:     map[string]json.RawMessage{"key": json.RawMessage("null")},
			key:     "key",
			wantVal: nil,
			wantSet: true,
		},
		{
			name:    "valid JSON object",
			raw:     map[string]json.RawMessage{"key": json.RawMessage(`{"nested":"value"}`)},
			key:     "key",
			wantVal: json.RawMessage(`{"nested":"value"}`),
			wantSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, set, err := parseOptionalRawField(tt.raw, tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.wantSet, set)
			if tt.wantVal == nil {
				assert.Nil(t, val)
			} else {
				assert.Equal(t, string(tt.wantVal), string(val))
			}
		})
	}
}

func TestBuildListTasksQuery(t *testing.T) {
	t.Parallel()

	orgID := "org-123"

	// Basic query with just org_id
	query, args := buildListTasksQuery(orgID, "", nil, nil)
	assert.Contains(t, query, "org_id = $1")
	assert.Len(t, args, 1)
	assert.Equal(t, orgID, args[0])

	// With status filter
	query, args = buildListTasksQuery(orgID, "queued", nil, nil)
	assert.Contains(t, query, "status = $2")
	assert.Len(t, args, 2)
	assert.Equal(t, "queued", args[1])

	// With project filter
	projectID := "project-456"
	query, args = buildListTasksQuery(orgID, "", &projectID, nil)
	assert.Contains(t, query, "project_id = $2")
	assert.Len(t, args, 2)
	assert.Equal(t, projectID, args[1])

	// With agent filter
	agentID := "agent-789"
	query, args = buildListTasksQuery(orgID, "", nil, &agentID)
	assert.Contains(t, query, "assigned_agent_id = $2")
	assert.Len(t, args, 2)
	assert.Equal(t, agentID, args[1])

	// All filters combined
	query, args = buildListTasksQuery(orgID, "in_progress", &projectID, &agentID)
	assert.Contains(t, query, "org_id = $1")
	assert.Contains(t, query, "status = $2")
	assert.Contains(t, query, "project_id = $3")
	assert.Contains(t, query, "assigned_agent_id = $4")
	assert.Len(t, args, 4)

	// Query ends with ORDER BY
	assert.True(t, strings.HasSuffix(query, "ORDER BY created_at DESC"))
}

func TestTaskHandler_ListTasks_MissingOrgID(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	rec := httptest.NewRecorder()

	handler.ListTasks(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "missing query parameter: org_id", resp.Error)
}

func TestTaskHandler_ListTasks_InvalidOrgID(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks?org_id=invalid", nil)
	rec := httptest.NewRecorder()

	handler.ListTasks(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid org_id", resp.Error)
}

func TestTaskHandler_ListTasks_InvalidStatus(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	orgID := "550e8400-e29b-41d4-a716-446655440000"
	req := httptest.NewRequest(http.MethodGet, "/api/tasks?org_id="+orgID+"&status=invalid", nil)
	rec := httptest.NewRecorder()

	handler.ListTasks(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid status", resp.Error)
}

func TestTaskHandler_ListTasks_InvalidProjectID(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	orgID := "550e8400-e29b-41d4-a716-446655440000"
	req := httptest.NewRequest(http.MethodGet, "/api/tasks?org_id="+orgID+"&project_id=invalid", nil)
	rec := httptest.NewRecorder()

	handler.ListTasks(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid project", resp.Error)
}

func TestTaskHandler_ListTasks_InvalidAgentID(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	orgID := "550e8400-e29b-41d4-a716-446655440000"
	req := httptest.NewRequest(http.MethodGet, "/api/tasks?org_id="+orgID+"&agent_id=invalid", nil)
	rec := httptest.NewRecorder()

	handler.ListTasks(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid agent", resp.Error)
}

func TestTaskHandler_CreateTask_InvalidJSON(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString("{invalid}"))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid request body", resp.Error)
}

func TestTaskHandler_CreateTask_MissingOrgID(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	body := `{"title":"Test Task"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "missing org_id", resp.Error)
}

func TestTaskHandler_CreateTask_InvalidOrgID(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	body := `{"org_id":"invalid","title":"Test Task"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid org_id", resp.Error)
}

func TestTaskHandler_CreateTask_MissingTitle(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	body := `{"org_id":"550e8400-e29b-41d4-a716-446655440000"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "missing title", resp.Error)
}

func TestTaskHandler_CreateTask_InvalidStatus(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	body := `{"org_id":"550e8400-e29b-41d4-a716-446655440000","title":"Test","status":"invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid status", resp.Error)
}

func TestTaskHandler_CreateTask_InvalidPriority(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	body := `{"org_id":"550e8400-e29b-41d4-a716-446655440000","title":"Test","priority":"invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid priority", resp.Error)
}

func TestTaskHandler_CreateTask_ValidContext_NoDatabaseFails(t *testing.T) {
	t.Parallel()

	// Note: Context validation happens after database connection.
	// A valid JSON string like "not json object" is actually valid JSON.
	// This test verifies that valid context passes validation but fails at DB step.
	handler := &TaskHandler{}
	body := `{"org_id":"550e8400-e29b-41d4-a716-446655440000","title":"Test","context":{"key":"value"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	// Without DATABASE_URL, should fail at database connection
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestTaskHandler_CreateTask_InvalidProjectID(t *testing.T) {
	t.Parallel()

	handler := &TaskHandler{}
	body := `{"org_id":"550e8400-e29b-41d4-a716-446655440000","title":"Test","project_id":"invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Error, "invalid project_id")
}

func TestCreateTaskRequest_JSONParsing(t *testing.T) {
	t.Parallel()

	jsonStr := `{
		"org_id": "550e8400-e29b-41d4-a716-446655440000",
		"project_id": "550e8400-e29b-41d4-a716-446655440001",
		"title": "Test Task",
		"description": "A test description",
		"status": "queued",
		"priority": "P1",
		"context": {"key": "value"},
		"assigned_agent_id": "550e8400-e29b-41d4-a716-446655440002",
		"parent_task_id": "550e8400-e29b-41d4-a716-446655440003"
	}`

	var req CreateTaskRequest
	err := json.Unmarshal([]byte(jsonStr), &req)
	require.NoError(t, err)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", req.OrgID)
	assert.NotNil(t, req.ProjectID)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440001", *req.ProjectID)
	assert.Equal(t, "Test Task", req.Title)
	assert.NotNil(t, req.Description)
	assert.Equal(t, "A test description", *req.Description)
	assert.Equal(t, "queued", req.Status)
	assert.Equal(t, "P1", req.Priority)
	assert.Equal(t, `{"key": "value"}`, string(req.Context))
	assert.NotNil(t, req.AssignedAgentID)
	assert.NotNil(t, req.ParentTaskID)
}

func TestTask_JSONSerialization(t *testing.T) {
	t.Parallel()

	projectID := "proj-123"
	desc := "A description"
	agentID := "agent-456"
	parentID := "parent-789"

	task := Task{
		ID:              "task-000",
		OrgID:           "org-111",
		ProjectID:       &projectID,
		Number:          42,
		Title:           "Test Task",
		Description:     &desc,
		Status:          "queued",
		Priority:        "P2",
		Context:         json.RawMessage(`{"key":"value"}`),
		AssignedAgentID: &agentID,
		ParentTaskID:    &parentID,
	}

	data, err := json.Marshal(task)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "task-000", parsed["id"])
	assert.Equal(t, "org-111", parsed["org_id"])
	assert.Equal(t, "proj-123", parsed["project_id"])
	assert.Equal(t, float64(42), parsed["number"])
	assert.Equal(t, "Test Task", parsed["title"])
	assert.Equal(t, "A description", parsed["description"])
	assert.Equal(t, "queued", parsed["status"])
	assert.Equal(t, "P2", parsed["priority"])
}

func TestTasksResponse_JSONSerialization(t *testing.T) {
	t.Parallel()

	projectID := "proj-123"
	agentID := "agent-456"

	resp := TasksResponse{
		OrgID:     "org-111",
		Status:    "queued",
		ProjectID: &projectID,
		AgentID:   &agentID,
		Tasks:     []Task{},
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "org-111", parsed["org_id"])
	assert.Equal(t, "queued", parsed["status"])
	assert.Equal(t, "proj-123", parsed["project_id"])
	assert.Equal(t, "agent-456", parsed["agent_id"])
}

func TestTaskStatusRequest_JSONParsing(t *testing.T) {
	t.Parallel()

	jsonStr := `{"status": "in_progress"}`

	var req TaskStatusRequest
	err := json.Unmarshal([]byte(jsonStr), &req)
	require.NoError(t, err)

	assert.Equal(t, "in_progress", req.Status)
}

// Helper function moved to test_helpers_test.go or feed_push_test.go
