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
	err      error
}

func (f *fakeEllieRetrievalPlannerStore) GetActiveStrategy(_ context.Context, _ string) (*store.EllieRetrievalStrategy, error) {
	if f.err != nil {
		return nil, f.err
	}
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

func TestEllieRetrievalPlannerReturnsErrorForInvalidStrategyRules(t *testing.T) {
	planner := NewEllieRetrievalPlanner(&fakeEllieRetrievalPlannerStore{
		strategy: &store.EllieRetrievalStrategy{
			Version: 3,
			Rules:   json.RawMessage(`{"topic_expansions":`),
		},
	})

	_, err := planner.BuildPlan(context.Background(), EllieRetrievalPlanInput{
		OrgID:     "org-1",
		ProjectID: "project-1",
		Query:     "database migration approach",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid strategy rules")
}

func TestEllieRetrievalPlannerNilStrategyFallback(t *testing.T) {
	planner := NewEllieRetrievalPlanner(&fakeEllieRetrievalPlannerStore{})

	plan, err := planner.BuildPlan(context.Background(), EllieRetrievalPlanInput{
		OrgID:     "org-1",
		ProjectID: "project-1",
		Query:     "database migration approach",
	})
	require.NoError(t, err)
	require.Equal(t, 1, plan.StrategyVersion)
	require.GreaterOrEqual(t, len(plan.Steps), 2)

	var hasProjectScope bool
	var hasOrgScope bool
	for _, step := range plan.Steps {
		if step.Scope == "project" && step.Query == "database migration approach" {
			hasProjectScope = true
		}
		if step.Scope == "org" && step.Query == "database migration approach" {
			hasOrgScope = true
		}
	}
	require.True(t, hasProjectScope)
	require.True(t, hasOrgScope)
}

func TestEllieRetrievalPlannerPropagatesStoreError(t *testing.T) {
	planner := NewEllieRetrievalPlanner(&fakeEllieRetrievalPlannerStore{err: context.DeadlineExceeded})

	_, err := planner.BuildPlan(context.Background(), EllieRetrievalPlanInput{
		OrgID:     "org-1",
		ProjectID: "project-1",
		Query:     "database migration approach",
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestEllieRetrievalPlannerRejectsEmptyInputs(t *testing.T) {
	planner := NewEllieRetrievalPlanner(&fakeEllieRetrievalPlannerStore{})

	_, err := planner.BuildPlan(context.Background(), EllieRetrievalPlanInput{
		OrgID: "",
		Query: "database migration approach",
	})
	require.ErrorContains(t, err, "org_id is required")

	_, err = planner.BuildPlan(context.Background(), EllieRetrievalPlanInput{
		OrgID: "org-1",
		Query: "",
	})
	require.ErrorContains(t, err, "query is required")
}

func TestDedupePlanSteps(t *testing.T) {
	steps := []EllieRetrievalPlanStep{
		{Scope: "org", Query: "database", Reason: "org_context"},
		{Scope: "org", Query: "database", Reason: "topic_expansion:database"},
		{Scope: "project", Query: "database", Reason: "project_context"},
		{Scope: "project", Query: "DATABASE", Reason: "project_context_upper"},
		{Scope: "", Query: "ignored", Reason: "missing_scope"},
		{Scope: "org", Query: "", Reason: "missing_query"},
	}

	deduped := dedupePlanSteps(steps)
	require.Len(t, deduped, 2)
	require.Equal(t, "org", deduped[0].Scope)
	require.Equal(t, "database", deduped[0].Query)
	require.Equal(t, "project", deduped[1].Scope)
	require.Equal(t, "database", deduped[1].Query)
}
