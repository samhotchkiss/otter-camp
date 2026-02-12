package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieRetrievalPlannerStore struct {
	strategy *store.EllieRetrievalStrategy
}

func (f *fakeEllieRetrievalPlannerStore) GetActiveStrategy(_ context.Context, _ string) (*store.EllieRetrievalStrategy, error) {
	if f.strategy == nil {
		return nil, nil
	}
	copy := *f.strategy
	return &copy, nil
}

func TestEllieRetrievalPlannerBuildsProjectAndOrgScopePlan(t *testing.T) {
	planner := NewEllieRetrievalPlanner(&fakeEllieRetrievalPlannerStore{
		strategy: &store.EllieRetrievalStrategy{Version: 1, Rules: json.RawMessage(`{"topic_expansions":{}}`)},
	})

	plan, err := planner.BuildPlan(context.Background(), EllieRetrievalPlanInput{
		OrgID:     "org-1",
		ProjectID: "project-1",
		Query:     "database migration approach",
	})
	require.NoError(t, err)
	require.Equal(t, 1, plan.StrategyVersion)

	var hasProjectScope bool
	var hasOrgScope bool
	for _, step := range plan.Steps {
		if step.Scope == "project" {
			hasProjectScope = true
		}
		if step.Scope == "org" {
			hasOrgScope = true
		}
	}
	require.True(t, hasProjectScope)
	require.True(t, hasOrgScope)
}

func TestEllieRetrievalPlannerAppliesTopicExpansionRules(t *testing.T) {
	planner := NewEllieRetrievalPlanner(&fakeEllieRetrievalPlannerStore{
		strategy: &store.EllieRetrievalStrategy{
			Version: 2,
			Rules: json.RawMessage(`{
				"topic_expansions": {
					"database": ["orm", "migration"]
				}
			}`),
		},
	})

	plan, err := planner.BuildPlan(context.Background(), EllieRetrievalPlanInput{
		OrgID:     "org-1",
		ProjectID: "project-1",
		Query:     "add database support",
	})
	require.NoError(t, err)

	queries := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		queries = append(queries, step.Query)
	}
	require.Contains(t, queries, "orm")
	require.Contains(t, queries, "migration")
}
