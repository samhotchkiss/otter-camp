package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAgentsHandlerListEnsuresProtectedElephantAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "agents-list-protected-elephant")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)

	handler := &AgentsHandler{
		Store: store.NewAgentStore(db),
		DB:    db,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Agents []AgentResponse `json:"agents"`
		Total  int             `json:"total"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.GreaterOrEqual(t, payload.Total, 2)

	foundElephant := false
	for _, agent := range payload.Agents {
		if agent.Role == "elephant" {
			foundElephant = true
			require.Equal(t, "Elephant", agent.Name)
			break
		}
	}
	require.True(t, foundElephant)

	var elephantStatus string
	err = db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = 'elephant'`, orgID).Scan(&elephantStatus)
	require.NoError(t, err)
	require.Equal(t, "active", elephantStatus)
}
