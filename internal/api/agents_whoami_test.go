package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type whoamiResponsePayload struct {
	Profile             string `json:"profile"`
	Soul                string `json:"soul,omitempty"`
	Identity            string `json:"identity,omitempty"`
	Instructions        string `json:"instructions,omitempty"`
	SoulSummary         string `json:"soul_summary,omitempty"`
	IdentitySummary     string `json:"identity_summary,omitempty"`
	InstructionsSummary string `json:"instructions_summary,omitempty"`
	Agent               struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Role  string `json:"role"`
		Emoji string `json:"emoji"`
	} `json:"agent"`
	ActiveTasks []struct {
		Project string `json:"project"`
		Issue   string `json:"issue"`
		Title   string `json:"title"`
		Status  string `json:"status"`
	} `json:"active_tasks"`
}

func TestAgentWhoAmIDefaultsToCompactProfile(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-whoami-compact")
	agentID := insertWhoAmITestAgent(t, db, orgID, "derek", "Derek")
	insertWhoAmITestTask(t, db, orgID, agentID, "Ship parser guards", "in_progress")

	handler := &AgentsHandler{Store: store.NewAgentStore(db), DB: db}
	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/%s/whoami?org_id=%s&session_key=agent:chameleon:oc:%s", agentID, orgID, agentID),
		nil,
	)
	req = addWhoAmIRouteParam(req, "id", agentID)
	rec := httptest.NewRecorder()

	handler.WhoAmI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload whoamiResponsePayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "compact", payload.Profile)
	require.Equal(t, agentID, payload.Agent.ID)
	require.NotEmpty(t, payload.SoulSummary)
	require.NotEmpty(t, payload.IdentitySummary)
	require.NotEmpty(t, payload.InstructionsSummary)
	require.Empty(t, payload.Soul)
	require.Empty(t, payload.Identity)
	require.Empty(t, payload.Instructions)
	require.NotEmpty(t, payload.ActiveTasks)
}

func TestAgentWhoAmIFullProfileIncludesRawIdentity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-whoami-full")
	agentID := insertWhoAmITestAgent(t, db, orgID, "nova", "Nova")

	handler := &AgentsHandler{Store: store.NewAgentStore(db), DB: db}
	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/%s/whoami?org_id=%s&profile=full&session_key=agent:chameleon:oc:%s", agentID, orgID, agentID),
		nil,
	)
	req = addWhoAmIRouteParam(req, "id", agentID)
	rec := httptest.NewRecorder()

	handler.WhoAmI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload whoamiResponsePayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "full", payload.Profile)
	require.NotEmpty(t, payload.Soul)
	require.NotEmpty(t, payload.Identity)
	require.NotEmpty(t, payload.Instructions)
}

func TestAgentWhoAmIRejectsMalformedSessionKey(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-whoami-invalid")
	agentID := insertWhoAmITestAgent(t, db, orgID, "stone", "Stone")

	handler := &AgentsHandler{Store: store.NewAgentStore(db), DB: db}
	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/%s/whoami?org_id=%s&session_key=agent:main:slack", agentID, orgID),
		nil,
	)
	req = addWhoAmIRouteParam(req, "id", agentID)
	rec := httptest.NewRecorder()

	handler.WhoAmI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "session_key must match canonical")
}

func TestAgentWhoAmIRejectsMismatchedSessionAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-whoami-mismatch")
	agentID := insertWhoAmITestAgent(t, db, orgID, "ivy", "Ivy")
	otherAgentID := insertWhoAmITestAgent(t, db, orgID, "beau", "Beau")

	handler := &AgentsHandler{Store: store.NewAgentStore(db), DB: db}
	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/%s/whoami?org_id=%s&session_key=agent:chameleon:oc:%s", agentID, orgID, otherAgentID),
		nil,
	)
	req = addWhoAmIRouteParam(req, "id", agentID)
	rec := httptest.NewRecorder()

	handler.WhoAmI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "session agent does not match")
}

func TestCapWhoAmITextIsRuneSafe(t *testing.T) {
	got := capWhoAmIText("🙂🙂🙂🙂🙂🙂", 5)
	require.Equal(t, "🙂🙂...", got)
	require.True(t, utf8.ValidString(got))

	got = capWhoAmIText("海豚营地", 3)
	require.Equal(t, "海豚营", got)
	require.True(t, utf8.ValidString(got))
}

func TestScanWhoAmIActiveTasksReturnsScanError(t *testing.T) {
	rows := &fakeWhoAmIActiveTaskRows{
		records: []fakeWhoAmIActiveTaskRecord{
			{project: "Alpha", number: sql.NullInt64{Int64: 42, Valid: true}, title: "Ship fix", status: "in_progress"},
		},
		scanErr: errors.New("scan failed"),
	}

	pointers, err := scanWhoAmIActiveTasks(rows)
	require.Error(t, err)
	require.Nil(t, pointers)
	require.Contains(t, err.Error(), "scan active task row")
}

func TestScanWhoAmIActiveTasksReturnsRowsErr(t *testing.T) {
	rows := &fakeWhoAmIActiveTaskRows{
		rowErr: errors.New("row iteration failed"),
	}

	pointers, err := scanWhoAmIActiveTasks(rows)
	require.Error(t, err)
	require.Nil(t, pointers)
	require.Contains(t, err.Error(), "iterate active tasks rows")
}

func TestListWhoAmIActiveTasksLogsQueryError(t *testing.T) {
	db, err := sql.Open("postgres", "not a valid postgres dsn")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	handler := &AgentsHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/agents/test/whoami", nil)

	var logBuf bytes.Buffer
	prevOutput := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(prevOutput)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})

	pointers := handler.listWhoAmIActiveTasks(
		req,
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
	)
	require.Nil(t, pointers)
	require.Contains(t, logBuf.String(), "listWhoAmIActiveTasks query failed")
}

type fakeWhoAmIActiveTaskRecord struct {
	project string
	number  sql.NullInt64
	title   string
	status  string
}

type fakeWhoAmIActiveTaskRows struct {
	records []fakeWhoAmIActiveTaskRecord
	scanErr error
	rowErr  error
	index   int
}

func (f *fakeWhoAmIActiveTaskRows) Next() bool {
	return f.index < len(f.records)
}

func (f *fakeWhoAmIActiveTaskRows) Scan(dest ...interface{}) error {
	if f.scanErr != nil {
		return f.scanErr
	}
	if f.index >= len(f.records) {
		return errors.New("scan called without next row")
	}
	record := f.records[f.index]
	f.index++

	project, ok := dest[0].(*string)
	if !ok {
		return errors.New("dest[0] not *string")
	}
	number, ok := dest[1].(*sql.NullInt64)
	if !ok {
		return errors.New("dest[1] not *sql.NullInt64")
	}
	title, ok := dest[2].(*string)
	if !ok {
		return errors.New("dest[2] not *string")
	}
	status, ok := dest[3].(*string)
	if !ok {
		return errors.New("dest[3] not *string")
	}

	*project = record.project
	*number = record.number
	*title = record.title
	*status = record.status
	return nil
}

func (f *fakeWhoAmIActiveTaskRows) Err() error {
	return f.rowErr
}

func insertWhoAmITestAgent(t *testing.T, db *sql.DB, orgID, slug, displayName string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO agents (
			org_id, slug, display_name, status, role, emoji, soul_md, identity_md, instructions_md
		) VALUES ($1, $2, $3, 'active', $4, $5, $6, $7, $8)
		RETURNING id`,
		orgID,
		slug,
		displayName,
		"Engineering Lead",
		":otter:",
		fmt.Sprintf("# SOUL\n\nYou are %s.", displayName),
		fmt.Sprintf("# IDENTITY\n\n- Name: %s", displayName),
		"# AGENTS\n\nAlways verify identity with OtterCamp.",
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertWhoAmITestTask(t *testing.T, db *sql.DB, orgID, agentID, title, status string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO tasks (org_id, assigned_agent_id, title, status, priority)
		 VALUES ($1, $2, $3, $4, 'P2')`,
		orgID,
		agentID,
		strings.TrimSpace(title),
		status,
	)
	require.NoError(t, err)
}

func addWhoAmIRouteParam(req *http.Request, key, value string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}
