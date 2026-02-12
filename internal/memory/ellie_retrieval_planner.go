package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type EllieRetrievalPlannerStore interface {
	GetActiveStrategy(ctx context.Context, orgID string) (*store.EllieRetrievalStrategy, error)
}

type EllieRetrievalPlanInput struct {
	OrgID     string
	ProjectID string
	Query     string
}

type EllieRetrievalPlanStep struct {
	Scope  string
	Query  string
	Reason string
}

type EllieRetrievalPlan struct {
	StrategyVersion int
	Steps           []EllieRetrievalPlanStep
}

type EllieRetrievalPlanner struct {
	Store EllieRetrievalPlannerStore
}

type elliePlannerRules struct {
	TopicExpansions map[string][]string `json:"topic_expansions"`
}

func NewEllieRetrievalPlanner(store EllieRetrievalPlannerStore) *EllieRetrievalPlanner {
	return &EllieRetrievalPlanner{Store: store}
}

func (p *EllieRetrievalPlanner) BuildPlan(ctx context.Context, input EllieRetrievalPlanInput) (EllieRetrievalPlan, error) {
	if p == nil || p.Store == nil {
		return EllieRetrievalPlan{}, fmt.Errorf("ellie retrieval planner store is required")
	}
	orgID := strings.TrimSpace(input.OrgID)
	projectID := strings.TrimSpace(input.ProjectID)
	query := strings.TrimSpace(input.Query)
	if orgID == "" {
		return EllieRetrievalPlan{}, fmt.Errorf("org_id is required")
	}
	if query == "" {
		return EllieRetrievalPlan{}, fmt.Errorf("query is required")
	}

	strategy, err := p.Store.GetActiveStrategy(ctx, orgID)
	if err != nil {
		return EllieRetrievalPlan{}, err
	}
	version := 1
	rules := elliePlannerRules{TopicExpansions: map[string][]string{}}
	if strategy != nil {
		version = strategy.Version
		if len(strategy.Rules) > 0 {
			if err := json.Unmarshal(strategy.Rules, &rules); err != nil {
				return EllieRetrievalPlan{}, fmt.Errorf("invalid strategy rules: %w", err)
			}
		}
	}
	if rules.TopicExpansions == nil {
		rules.TopicExpansions = map[string][]string{}
	}

	steps := make([]EllieRetrievalPlanStep, 0, 6)
	if projectID != "" {
		steps = append(steps, EllieRetrievalPlanStep{
			Scope:  "project",
			Query:  query,
			Reason: "project_context",
		})
	}
	steps = append(steps, EllieRetrievalPlanStep{
		Scope:  "org",
		Query:  query,
		Reason: "org_context",
	})

	lowerQuery := strings.ToLower(query)
	for keyword, expansions := range rules.TopicExpansions {
		normalizedKeyword := strings.TrimSpace(strings.ToLower(keyword))
		if normalizedKeyword == "" || !strings.Contains(lowerQuery, normalizedKeyword) {
			continue
		}
		for _, expansion := range expansions {
			normalizedExpansion := strings.TrimSpace(strings.ToLower(expansion))
			if normalizedExpansion == "" {
				continue
			}
			steps = append(steps, EllieRetrievalPlanStep{
				Scope:  "org",
				Query:  normalizedExpansion,
				Reason: "topic_expansion:" + normalizedKeyword,
			})
		}
	}

	steps = dedupePlanSteps(steps)
	return EllieRetrievalPlan{StrategyVersion: version, Steps: steps}, nil
}

func dedupePlanSteps(steps []EllieRetrievalPlanStep) []EllieRetrievalPlanStep {
	seen := make(map[string]struct{}, len(steps))
	out := make([]EllieRetrievalPlanStep, 0, len(steps))
	for _, step := range steps {
		scope := strings.TrimSpace(strings.ToLower(step.Scope))
		query := strings.TrimSpace(strings.ToLower(step.Query))
		if scope == "" || query == "" {
			continue
		}
		key := scope + "::" + query
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, step)
	}
	return out
}
