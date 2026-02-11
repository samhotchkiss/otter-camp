package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAdminAgentsListReturnsMergedRoster(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "admin-agents-list-a")
	orgB := insertMessageTestOrganization(t, db, "admin-agents-list-b")

	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES
		    ($1, 'main', 'Frank', 'active'),
		    ($1, 'three-stones', 'Stone', 'offline'),
		    ($2, 'hidden-agent', 'Hidden', 'active')`,
		orgA,
		orgB,
	)
	require.NoError(t, err)

	now := time.Now().UTC()
	_, err = db.Exec(
		`INSERT INTO agent_sync_state
		    (org_id, id, name, status, model, context_tokens, total_tokens, channel, session_key, last_seen, updated_at)
		 VALUES
		    ($1, 'main', 'Frank', 'online', 'gpt-5.2-codex', 2200, 10240, 'slack:#engineering', 'agent:main:main', 'just now', $3),
		    ($2, 'hidden-agent', 'Hidden', 'online', 'gpt-5.2-codex', 100, 1000, 'slack:#secret', 'agent:hidden-agent:main', 'just now', $3)
		 ON CONFLICT (org_id, id) DO UPDATE SET
		    name = EXCLUDED.name,
		    status = EXCLUDED.status,
		    model = EXCLUDED.model,
		    context_tokens = EXCLUDED.context_tokens,
		    total_tokens = EXCLUDED.total_tokens,
		    channel = EXCLUDED.channel,
		    session_key = EXCLUDED.session_key,
		    last_seen = EXCLUDED.last_seen,
		    updated_at = EXCLUDED.updated_at`,
		orgA,
		orgB,
		now,
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO openclaw_agent_configs (id, heartbeat_every, updated_at)
		 VALUES ('main', '15m', NOW())
		 ON CONFLICT (id) DO UPDATE SET heartbeat_every = EXCLUDED.heartbeat_every, updated_at = EXCLUDED.updated_at`,
	)
	require.NoError(t, err)

	handler := &AdminAgentsHandler{
		DB:    db,
		Store: store.NewAgentStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgA))
	rec := httptest.NewRecorder()
	handler.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminAgentsListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Agents, 3)
	require.Equal(t, 3, payload.Total)
	agentsByID := make(map[string]adminAgentSummary, len(payload.Agents))
	for _, agent := range payload.Agents {
		agentsByID[agent.ID] = agent
	}

	mainAgent, ok := agentsByID["main"]
	require.True(t, ok)
	require.Equal(t, "Frank", mainAgent.Name)
	require.Equal(t, "online", mainAgent.Status)
	require.Equal(t, "gpt-5.2-codex", mainAgent.Model)
	require.Equal(t, "15m", mainAgent.HeartbeatEvery)
	require.Equal(t, "slack:#engineering", mainAgent.Channel)
	require.Equal(t, "agent:main:main", mainAgent.SessionKey)

	stoneAgent, ok := agentsByID["three-stones"]
	require.True(t, ok)
	require.Equal(t, "Stone", stoneAgent.Name)
	require.Equal(t, "offline", stoneAgent.Status)
	require.Equal(t, "", stoneAgent.Model)
	require.Equal(t, "", stoneAgent.Channel)

	elephantAgent, ok := agentsByID["elephant"]
	require.True(t, ok)
	require.Equal(t, "Elephant", elephantAgent.Name)
	require.Equal(t, "online", elephantAgent.Status)
}

func TestAdminAgentsListEnforcesWorkspace(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := &AdminAgentsHandler{
		DB:    db,
		Store: store.NewAgentStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminAgentsListMatchesCanonicalChameleonSessionAgentID(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-canonical-session-id")

	var workspaceAgentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'marcus', 'Marcus', 'active')
		 RETURNING id::text`,
		orgID,
	).Scan(&workspaceAgentID)
	require.NoError(t, err)

	now := time.Now().UTC()
	_, err = db.Exec(
		`INSERT INTO agent_sync_state
		    (org_id, id, name, status, model, context_tokens, total_tokens, channel, session_key, last_seen, updated_at)
		 VALUES
		    ($1, $2, 'Marcus', 'online', 'gpt-5.2-codex', 512, 2048, 'webchat', 'agent:chameleon:oc:' || $2, 'just now', $3)
		 ON CONFLICT (org_id, id) DO UPDATE SET
		    name = EXCLUDED.name,
		    status = EXCLUDED.status,
		    model = EXCLUDED.model,
		    context_tokens = EXCLUDED.context_tokens,
		    total_tokens = EXCLUDED.total_tokens,
		    channel = EXCLUDED.channel,
		    session_key = EXCLUDED.session_key,
		    last_seen = EXCLUDED.last_seen,
		    updated_at = EXCLUDED.updated_at`,
		orgID,
		workspaceAgentID,
		now,
	)
	require.NoError(t, err)

	handler := &AdminAgentsHandler{
		DB:    db,
		Store: store.NewAgentStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminAgentsListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Agents, 2)
	agentsByID := make(map[string]adminAgentSummary, len(payload.Agents))
	for _, agent := range payload.Agents {
		agentsByID[agent.ID] = agent
	}
	marcusAgent, ok := agentsByID["marcus"]
	require.True(t, ok)
	require.Equal(t, "gpt-5.2-codex", marcusAgent.Model)
	require.Equal(t, 512, marcusAgent.ContextTokens)
	require.Equal(t, 2048, marcusAgent.TotalTokens)
	require.Equal(t, "webchat", marcusAgent.Channel)

	elephantAgent, ok := agentsByID["elephant"]
	require.True(t, ok)
	require.Equal(t, "Elephant", elephantAgent.Name)
}

func TestAdminAgentsGetReturnsMergedDetail(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-get")

	var workspaceAgentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&workspaceAgentID)
	require.NoError(t, err)

	now := time.Now().UTC()
	_, err = db.Exec(
		`INSERT INTO agent_sync_state
		    (org_id, id, name, status, model, context_tokens, total_tokens, channel, session_key, current_task, last_seen, updated_at)
		 VALUES
		    ($1, 'main', 'Frank', 'busy', 'claude-opus-4-6', 3400, 15120, 'slack:#ops', 'agent:main:main', 'Handling incident triage', '1m ago', $2)
		 ON CONFLICT (org_id, id) DO UPDATE SET
		    name = EXCLUDED.name,
		    status = EXCLUDED.status,
		    model = EXCLUDED.model,
		    context_tokens = EXCLUDED.context_tokens,
		    total_tokens = EXCLUDED.total_tokens,
		    channel = EXCLUDED.channel,
		    session_key = EXCLUDED.session_key,
		    current_task = EXCLUDED.current_task,
		    last_seen = EXCLUDED.last_seen,
		    updated_at = EXCLUDED.updated_at`,
		orgID,
		now,
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO openclaw_agent_configs (id, heartbeat_every, updated_at)
		 VALUES ('main', '10m', NOW())
		 ON CONFLICT (id) DO UPDATE SET heartbeat_every = EXCLUDED.heartbeat_every, updated_at = EXCLUDED.updated_at`,
	)
	require.NoError(t, err)

	handler := &AdminAgentsHandler{
		DB:    db,
		Store: store.NewAgentStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "main")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminAgentDetailResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "main", payload.Agent.ID)
	require.Equal(t, workspaceAgentID, payload.Agent.WorkspaceAgentID)
	require.Equal(t, "Frank", payload.Agent.Name)
	require.Equal(t, "online", payload.Agent.Status)
	require.Equal(t, "claude-opus-4-6", payload.Agent.Model)
	require.Equal(t, "10m", payload.Agent.HeartbeatEvery)
	require.NotNil(t, payload.Sync)
	require.Equal(t, 3400, payload.Sync.ContextTokens)
	require.Equal(t, 15120, payload.Sync.TotalTokens)
	require.Equal(t, "Handling incident triage", payload.Sync.CurrentTask)
}

func TestAdminAgentsGetMissingAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-missing")

	handler := &AdminAgentsHandler{
		DB:    db,
		Store: store.NewAgentStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/missing", nil)
	req = addRouteParam(req, "id", "missing")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminAgentsGetCrossOrgForbidden(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "admin-agents-cross-a")
	orgB := insertMessageTestOrganization(t, db, "admin-agents-cross-b")

	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'hidden-agent', 'Hidden Agent', 'active')`,
		orgB,
	)
	require.NoError(t, err)

	handler := &AdminAgentsHandler{
		DB:    db,
		Store: store.NewAgentStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/hidden-agent", nil)
	req = addRouteParam(req, "id", "hidden-agent")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgA))

	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
