package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieRetrievalPlannerStrategyVersioning(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-planner-strategy-org")
	plannerStore := NewEllieRetrievalPlannerStore(db)

	rulesV1 := json.RawMessage(`{"topic_expansions":{"database":["orm"]}}`)
	err := plannerStore.UpsertStrategy(context.Background(), UpsertEllieRetrievalStrategyInput{
		OrgID:    orgID,
		Version:  1,
		Name:     "v1",
		Rules:    rulesV1,
		IsActive: true,
	})
	require.NoError(t, err)

	rulesV2 := json.RawMessage(`{"topic_expansions":{"database":["orm","migration"]}}`)
	err = plannerStore.UpsertStrategy(context.Background(), UpsertEllieRetrievalStrategyInput{
		OrgID:    orgID,
		Version:  2,
		Name:     "v2",
		Rules:    rulesV2,
		IsActive: true,
	})
	require.NoError(t, err)

	strategy, err := plannerStore.GetActiveStrategy(context.Background(), orgID)
	require.NoError(t, err)
	require.NotNil(t, strategy)
	require.Equal(t, 2, strategy.Version)
	require.Equal(t, "v2", strategy.Name)
}
