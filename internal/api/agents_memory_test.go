package api

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type memoryListResponsePayload struct {
	AgentID string `json:"agent_id"`
	Daily   []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Date    string `json:"date,omitempty"`
		Content string `json:"content"`
	} `json:"daily"`
	LongTerm []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Content string `json:"content"`
	} `json:"long_term"`
}

type memorySearchResponsePayload struct {
	Items []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Date    string `json:"date,omitempty"`
		Content string `json:"content"`
	} `json:"items"`
	Total int `json:"total"`
}

func TestAgentMemoryCreateReadAndSearch(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-memory-crud")
	agentID := insertWhoAmITestAgent(t, db, orgID, "memory-agent", "Memory Agent")

	handler := &AgentsHandler{
		Store:       store.NewAgentStore(db),
		MemoryStore: store.NewAgentMemoryStore(db),
		DB:          db,
	}

	createDaily := map[string]string{
		"kind":    "daily",
		"content": "Shipped parser guard and opened follow-up issue.",
	}
	dailyBody, err := json.Marshal(createDaily)
	require.NoError(t, err)
	createReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s", agentID, orgID),
		bytes.NewReader(dailyBody),
	)
	createReq = addWhoAmIRouteParam(createReq, "id", agentID)
	createRec := httptest.NewRecorder()
	handler.CreateMemory(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	createLongTerm := map[string]string{
		"kind":    "long_term",
		"content": "Always verify identity using whoami before privileged writes.",
	}
	longTermBody, err := json.Marshal(createLongTerm)
	require.NoError(t, err)
	createLongReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s", agentID, orgID),
		bytes.NewReader(longTermBody),
	)
	createLongReq = addWhoAmIRouteParam(createLongReq, "id", agentID)
	createLongRec := httptest.NewRecorder()
	handler.CreateMemory(createLongRec, createLongReq)
	require.Equal(t, http.StatusCreated, createLongRec.Code)

	getReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s&days=3&include_long_term=true", agentID, orgID),
		nil,
	)
	getReq = addWhoAmIRouteParam(getReq, "id", agentID)
	getRec := httptest.NewRecorder()
	handler.GetMemory(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var listPayload memoryListResponsePayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&listPayload))
	require.Equal(t, agentID, listPayload.AgentID)
	require.Len(t, listPayload.Daily, 1)
	require.Len(t, listPayload.LongTerm, 1)

	searchReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/%s/memory/search?org_id=%s&q=privileged writes", agentID, orgID),
		nil,
	)
	searchReq = addWhoAmIRouteParam(searchReq, "id", agentID)
	searchRec := httptest.NewRecorder()
	handler.SearchMemory(searchRec, searchReq)
	require.Equal(t, http.StatusOK, searchRec.Code)

	var searchPayload memorySearchResponsePayload
	require.NoError(t, json.NewDecoder(searchRec.Body).Decode(&searchPayload))
	require.NotZero(t, searchPayload.Total)
}

func TestAgentMemoryRejectsInvalidAgentID(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-memory-invalid")

	handler := &AgentsHandler{
		Store:       store.NewAgentStore(db),
		MemoryStore: store.NewAgentMemoryStore(db),
		DB:          db,
	}

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/not-a-uuid/memory?org_id=%s&days=2", orgID),
		nil,
	)
	req = addWhoAmIRouteParam(req, "id", "not-a-uuid")
	rec := httptest.NewRecorder()
	handler.GetMemory(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "agent id must be a UUID")
}

func TestAgentMemoryCrossOrgDenied(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "agents-memory-org-a")
	orgB := insertMessageTestOrganization(t, db, "agents-memory-org-b")
	agentID := insertWhoAmITestAgent(t, db, orgA, "cross-org-agent", "Cross Org Agent")

	handler := &AgentsHandler{
		Store:       store.NewAgentStore(db),
		MemoryStore: store.NewAgentMemoryStore(db),
		DB:          db,
	}

	req := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s&days=2", agentID, orgB),
		nil,
	)
	req = addWhoAmIRouteParam(req, "id", agentID)
	rec := httptest.NewRecorder()
	handler.GetMemory(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAgentMemoryCreateUsesTypedValidationErrors(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-memory-typed-errors")
	agentID := insertWhoAmITestAgent(t, db, orgID, "typed-memory-agent", "Typed Memory Agent")

	handler := &AgentsHandler{
		Store:       store.NewAgentStore(db),
		MemoryStore: store.NewAgentMemoryStore(db),
		DB:          db,
	}

	invalidKindBody := []byte(`{"kind":"bad_kind","content":"some memory"}`)
	invalidKindReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s", agentID, orgID),
		bytes.NewReader(invalidKindBody),
	)
	invalidKindReq = addWhoAmIRouteParam(invalidKindReq, "id", agentID)
	invalidKindRec := httptest.NewRecorder()
	handler.CreateMemory(invalidKindRec, invalidKindReq)
	require.Equal(t, http.StatusBadRequest, invalidKindRec.Code)
	require.Contains(t, invalidKindRec.Body.String(), "unsupported memory kind")

	missingContentBody := []byte(`{"kind":"note","content":"  "}`)
	missingContentReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s", agentID, orgID),
		bytes.NewReader(missingContentBody),
	)
	missingContentReq = addWhoAmIRouteParam(missingContentReq, "id", agentID)
	missingContentRec := httptest.NewRecorder()
	handler.CreateMemory(missingContentRec, missingContentReq)
	require.Equal(t, http.StatusBadRequest, missingContentRec.Code)
	require.Contains(t, missingContentRec.Body.String(), "memory content is required")
}

func TestAgentMemoryCreateRejectsOversizedBody(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-memory-oversized")
	agentID := insertWhoAmITestAgent(t, db, orgID, "oversized-memory-agent", "Oversized Memory Agent")

	handler := &AgentsHandler{
		Store:       store.NewAgentStore(db),
		MemoryStore: store.NewAgentMemoryStore(db),
		DB:          db,
	}

	oversizedBody := fmt.Sprintf(`{"kind":"note","content":"%s"}`, strings.Repeat("a", (1<<20)+32))
	oversizedReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s", agentID, orgID),
		strings.NewReader(oversizedBody),
	)
	oversizedReq = addWhoAmIRouteParam(oversizedReq, "id", agentID)
	oversizedRec := httptest.NewRecorder()
	handler.CreateMemory(oversizedRec, oversizedReq)
	require.Equal(t, http.StatusRequestEntityTooLarge, oversizedRec.Code)

	normalBody := []byte(`{"kind":"note","content":"normal-sized memory entry"}`)
	normalReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/agents/%s/memory?org_id=%s", agentID, orgID),
		bytes.NewReader(normalBody),
	)
	normalReq = addWhoAmIRouteParam(normalReq, "id", agentID)
	normalRec := httptest.NewRecorder()
	handler.CreateMemory(normalRec, normalReq)
	require.Equal(t, http.StatusCreated, normalRec.Code)
}

func TestDecodeCreateMemoryRequestRejectsOversizedBody(t *testing.T) {
	payload := map[string]string{
		"kind":    "note",
		"content": strings.Repeat("a", (1<<20)+32),
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/x/memory", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	var decoded createAgentMemoryRequest

	status, decodeErr := decodeCreateMemoryRequest(rec, req, &decoded)
	require.Error(t, decodeErr)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
}

func TestDecodeCreateMemoryRequestAcceptsValidBody(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agents/x/memory",
		strings.NewReader(`{"kind":"note","content":"normal-sized memory entry"}`),
	)
	rec := httptest.NewRecorder()
	var decoded createAgentMemoryRequest

	status, decodeErr := decodeCreateMemoryRequest(rec, req, &decoded)
	require.NoError(t, decodeErr)
	require.Equal(t, 0, status)
	require.Equal(t, "note", decoded.Kind)
	require.Equal(t, "normal-sized memory entry", decoded.Content)
}

func TestMapCreateMemoryErrorUsesTypedErrors(t *testing.T) {
	status, message := mapCreateMemoryError(fmt.Errorf("changed wording: %w", store.ErrAgentMemoryInvalidKind))
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, message, "changed wording")

	status, message = mapCreateMemoryError(fmt.Errorf("still wrapped: %w", store.ErrAgentMemoryContentMissing))
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, message, "still wrapped")

	status, message = mapCreateMemoryError(stderrors.New("unexpected store failure"))
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, "failed to create memory", message)
}

func TestParseMemoryDaysParamClampsLargeValues(t *testing.T) {
	days, err := parseMemoryDaysParam("999")
	require.NoError(t, err)
	require.Equal(t, 365, days)
}

func TestParseMemorySearchLimitParamClampsLargeValues(t *testing.T) {
	limit, err := parseMemorySearchLimitParam("9999")
	require.NoError(t, err)
	require.Equal(t, 500, limit)
}
