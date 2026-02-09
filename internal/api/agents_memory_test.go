package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
