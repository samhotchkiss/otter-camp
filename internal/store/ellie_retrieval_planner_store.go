package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type EllieRetrievalStrategy struct {
	ID        string
	OrgID     string
	Version   int
	Name      string
	Rules     json.RawMessage
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type UpsertEllieRetrievalStrategyInput struct {
	OrgID    string
	Version  int
	Name     string
	Rules    json.RawMessage
	IsActive bool
}

type EllieRetrievalPlannerStore struct {
	db *sql.DB
}

type ellieRetrievalPlannerRules struct {
	TopicExpansions map[string][]string `json:"topic_expansions"`
}

func NewEllieRetrievalPlannerStore(db *sql.DB) *EllieRetrievalPlannerStore {
	return &EllieRetrievalPlannerStore{db: db}
}

func (s *EllieRetrievalPlannerStore) UpsertStrategy(ctx context.Context, input UpsertEllieRetrievalStrategyInput) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie retrieval planner store is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if input.Version <= 0 {
		return fmt.Errorf("version must be greater than zero")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	rules := input.Rules
	if len(strings.TrimSpace(string(rules))) == 0 {
		rules = json.RawMessage(`{"topic_expansions":{}}`)
	}
	var parsedRules ellieRetrievalPlannerRules
	if err := json.Unmarshal(rules, &parsedRules); err != nil {
		return fmt.Errorf("invalid rules: %w", err)
	}
	if parsedRules.TopicExpansions == nil {
		parsedRules.TopicExpansions = map[string][]string{}
	}
	normalizedRules, err := json.Marshal(parsedRules)
	if err != nil {
		return fmt.Errorf("invalid rules: %w", err)
	}
	rules = json.RawMessage(normalizedRules)

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO ellie_retrieval_strategies (
			org_id,
			version,
			name,
			rules,
			is_active
		) VALUES (
			$1, $2, $3, $4::jsonb, $5
		)
		ON CONFLICT (org_id, version) DO UPDATE
		SET
			name = EXCLUDED.name,
			rules = EXCLUDED.rules,
			is_active = EXCLUDED.is_active,
			updated_at = NOW()`,
		orgID,
		input.Version,
		name,
		rules,
		input.IsActive,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert ellie retrieval strategy: %w", err)
	}
	return nil
}

func (s *EllieRetrievalPlannerStore) GetActiveStrategy(ctx context.Context, orgID string) (*EllieRetrievalStrategy, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval planner store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	var strategy EllieRetrievalStrategy
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, org_id, version, name, rules, is_active, created_at, updated_at
		 FROM ellie_retrieval_strategies
		 WHERE org_id = $1
		   AND is_active = true
		 ORDER BY version DESC, created_at DESC
		 LIMIT 1`,
		orgID,
	).Scan(
		&strategy.ID,
		&strategy.OrgID,
		&strategy.Version,
		&strategy.Name,
		&strategy.Rules,
		&strategy.IsActive,
		&strategy.CreatedAt,
		&strategy.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load active ellie retrieval strategy: %w", err)
	}
	if len(strategy.Rules) == 0 {
		strategy.Rules = json.RawMessage(`{"topic_expansions":{}}`)
	}
	return &strategy, nil
}
