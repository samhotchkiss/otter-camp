package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type commandExecuteTestResponse struct {
	OK          bool            `json:"ok"`
	Type        string          `json:"type"`
	RedirectURL string          `json:"redirect_url,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

func TestCommandExecuteMethodNotAllowed(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/commands/execute", nil)
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "method not allowed", payload.Error)
}

func TestCommandExecuteInvalidBody(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString("{not-json"))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "invalid request body", payload.Error)
}

func TestCommandExecuteMissingType(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "missing command type", payload.Error)
}

func TestCommandExecuteNavigate(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"type": "navigate",
		"parameters": { "url": "/agents" }
	}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload commandExecuteTestResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.Equal(t, "navigate", payload.Type)
	require.Equal(t, "/agents", payload.RedirectURL)
}

func TestCommandExecuteNavigateRejectsExternalURLs(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"type": "navigate",
		"parameters": { "url": "https://example.com" }
	}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "invalid url", payload.Error)
}

func TestCommandExecuteShortcutMapping_Action(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"shortcut": { "key": "k", "modifiers": { "cmd": true } }
	}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload commandExecuteTestResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.Equal(t, "action", payload.Type)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(payload.Result, &result))
	require.Equal(t, "open_command_palette", result["client_action"])
}

func TestCommandExecuteShortcutMapping_CreateTaskValidation(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"shortcut": { "key": "n", "modifiers": { "cmd": true } },
		"parameters": { "title": "Test task" }
	}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "missing org_id", payload.Error)
}

func TestCommandExecuteShortcutUnsupported(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"shortcut": { "key": "x" }
	}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "unsupported shortcut", payload.Error)
}

func TestCommandExecuteActionPing(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"type": "action",
		"parameters": { "name": "ping" }
	}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload commandExecuteTestResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.Equal(t, "action", payload.Type)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(payload.Result, &result))
	require.Equal(t, true, result["pong"])
}

func TestCommandExecuteActionUnsupported(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"type": "action",
		"parameters": { "name": "nope" }
	}`))
	rec := httptest.NewRecorder()

	CommandExecuteHandler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "unsupported action", payload.Error)
}

func TestCommandExecuteCreateValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		body   string
		status int
		errMsg string
	}{
		{
			name:   "missing resource",
			body:   `{"type":"create","parameters":{}}`,
			status: http.StatusBadRequest,
			errMsg: "missing resource",
		},
		{
			name:   "unsupported resource",
			body:   `{"type":"create","parameters":{"resource":"project"}}`,
			status: http.StatusBadRequest,
			errMsg: "unsupported resource",
		},
		{
			name:   "invalid org_id",
			body:   `{"type":"create","parameters":{"resource":"task","org_id":"not-a-uuid","title":"hello"}}`,
			status: http.StatusBadRequest,
			errMsg: "invalid org_id",
		},
		{
			name:   "missing title",
			body:   `{"type":"create","parameters":{"resource":"task","org_id":"00000000-0000-0000-0000-000000000000"}}`,
			status: http.StatusBadRequest,
			errMsg: "missing title",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()

			CommandExecuteHandler(rec, req)

			require.Equal(t, tc.status, rec.Code)

			var payload errorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
			require.Equal(t, tc.errMsg, payload.Error)
		})
	}
}

func TestCommandExecuteSearchValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		body   string
		status int
		errMsg string
	}{
		{
			name:   "missing q",
			body:   `{"type":"search","parameters":{"org_id":"00000000-0000-0000-0000-000000000000"}}`,
			status: http.StatusBadRequest,
			errMsg: "missing q",
		},
		{
			name:   "missing org_id",
			body:   `{"type":"search","parameters":{"q":"agent"}}`,
			status: http.StatusBadRequest,
			errMsg: "missing org_id",
		},
		{
			name:   "invalid mode",
			body:   `{"type":"search","parameters":{"q":"agent","org_id":"00000000-0000-0000-0000-000000000000","mode":"nope"}}`,
			status: http.StatusBadRequest,
			errMsg: "invalid mode",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()

			CommandExecuteHandler(rec, req)

			require.Equal(t, tc.status, rec.Code)

			var payload errorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
			require.Equal(t, tc.errMsg, payload.Error)
		})
	}
}

func TestCommandExecuteCreateTaskIntegration(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	tasksDBOnce = sync.Once{}
	tasksDBErr = nil
	if tasksDB != nil {
		_ = tasksDB.Close()
		tasksDB = nil
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "command-execute-create")

	router := NewRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"type": "create",
		"parameters": {
			"resource": "task",
			"org_id": "`+orgID+`",
			"title": "Command created task",
			"description": "From /api/commands/execute",
			"priority": "P1"
		}
	}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload commandExecuteTestResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.Equal(t, "create", payload.Type)
	require.Contains(t, payload.RedirectURL, "/tasks/")

	var created Task
	require.NoError(t, json.Unmarshal(payload.Result, &created))
	require.Equal(t, orgID, created.OrgID)
	require.Equal(t, "Command created task", created.Title)
	require.Equal(t, "P1", created.Priority)
	require.Equal(t, "queued", created.Status)
}

func TestCommandExecuteSearchCommandsIntegration(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	searchDBOnce = sync.Once{}
	searchDBErr = nil
	if searchDB != nil {
		_ = searchDB.Close()
		searchDB = nil
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "command-execute-search")
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	var taskID string
	require.NoError(t, db.QueryRow(
		"INSERT INTO tasks (org_id, title, description, status, priority, updated_at) VALUES ($1, $2, $3, 'queued', 'P2', $4) RETURNING id",
		orgID,
		"Agent onboarding checklist",
		"Prepare Scout Agent for launch",
		now.Add(-30*time.Minute),
	).Scan(&taskID))
	_ = taskID

	router := NewRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/commands/execute", bytes.NewBufferString(`{
		"type": "search",
		"parameters": {
			"mode": "commands",
			"q": "agent",
			"org_id": "`+orgID+`"
		}
	}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload commandExecuteTestResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.Equal(t, "search", payload.Type)

	var searchResp CommandSearchResponse
	require.NoError(t, json.Unmarshal(payload.Result, &searchResp))
	require.Equal(t, "agent", searchResp.Query)
	require.Equal(t, orgID, searchResp.OrgID)
	require.NotEmpty(t, searchResp.Results)
}
