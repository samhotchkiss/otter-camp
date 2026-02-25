package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TokenSummary struct {
	InputTokens        int64
	OutputTokens       int64
	TotalTokens        int64
	EstimatedCostCents int64
}

type CostInvocation struct {
	InputTokens        int64
	OutputTokens       int64
	InputCostPer1K     float64
	OutputCostPer1K    float64
	EstimatedCostCents float64
}

type costInvocationSource interface {
	ListForRun(ctx context.Context, runID uuid.UUID) ([]CostInvocation, error)
	ListForTask(ctx context.Context, taskID uuid.UUID) ([]CostInvocation, error)
	ListForSession(ctx context.Context, sessionID uuid.UUID) ([]CostInvocation, error)
}

type CostQuery struct {
	source costInvocationSource
}

func NewCostQuery(pool *pgxpool.Pool) *CostQuery {
	if pool == nil {
		return &CostQuery{}
	}
	return &CostQuery{source: &postgresCostInvocationSource{pool: pool}}
}

func (q *CostQuery) SumForRun(ctx context.Context, runID uuid.UUID) (TokenSummary, error) {
	if q == nil || q.source == nil {
		return TokenSummary{}, fmt.Errorf("cost query is not configured")
	}
	if runID == uuid.Nil {
		return TokenSummary{}, fmt.Errorf("run id is required")
	}
	rows, err := q.source.ListForRun(ctx, runID)
	if err != nil {
		return TokenSummary{}, err
	}
	return summarizeCost(rows), nil
}

func (q *CostQuery) SumForTask(ctx context.Context, taskID uuid.UUID) (TokenSummary, error) {
	if q == nil || q.source == nil {
		return TokenSummary{}, fmt.Errorf("cost query is not configured")
	}
	if taskID == uuid.Nil {
		return TokenSummary{}, fmt.Errorf("task id is required")
	}
	rows, err := q.source.ListForTask(ctx, taskID)
	if err != nil {
		return TokenSummary{}, err
	}
	return summarizeCost(rows), nil
}

func (q *CostQuery) SumForSession(ctx context.Context, sessionID uuid.UUID) (TokenSummary, error) {
	if q == nil || q.source == nil {
		return TokenSummary{}, fmt.Errorf("cost query is not configured")
	}
	if sessionID == uuid.Nil {
		return TokenSummary{}, fmt.Errorf("session id is required")
	}
	rows, err := q.source.ListForSession(ctx, sessionID)
	if err != nil {
		return TokenSummary{}, err
	}
	return summarizeCost(rows), nil
}

func summarizeCost(invocations []CostInvocation) TokenSummary {
	summary := TokenSummary{}
	totalCost := 0.0
	for _, invocation := range invocations {
		summary.InputTokens += invocation.InputTokens
		summary.OutputTokens += invocation.OutputTokens
		cost := invocation.EstimatedCostCents
		if cost == 0 {
			cost = float64(invocation.InputTokens)*invocation.InputCostPer1K/1000.0 + float64(invocation.OutputTokens)*invocation.OutputCostPer1K/1000.0
		}
		totalCost += cost
	}
	summary.TotalTokens = summary.InputTokens + summary.OutputTokens
	summary.EstimatedCostCents = int64(math.Round(totalCost))
	return summary
}

type postgresCostInvocationSource struct {
	pool *pgxpool.Pool
}

func (s *postgresCostInvocationSource) ListForRun(ctx context.Context, runID uuid.UUID) ([]CostInvocation, error) {
	return s.list(ctx, "run_id", runID)
}

func (s *postgresCostInvocationSource) ListForTask(ctx context.Context, taskID uuid.UUID) ([]CostInvocation, error) {
	return s.list(ctx, "project_task_id", taskID)
}

func (s *postgresCostInvocationSource) ListForSession(ctx context.Context, sessionID uuid.UUID) ([]CostInvocation, error) {
	return s.list(ctx, "session_id", sessionID)
}

func (s *postgresCostInvocationSource) list(ctx context.Context, column string, id uuid.UUID) ([]CostInvocation, error) {
	if column != "run_id" && column != "project_task_id" && column != "session_id" {
		return nil, fmt.Errorf("invalid cost query column %q", column)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
			COALESCE(mi.input_tokens, 0)::bigint,
			COALESCE(mi.output_tokens, 0)::bigint,
			COALESCE(mp.metadata, '{}'::jsonb)
		FROM model_invocation mi
		JOIN model_provider mp ON mp.id = mi.model_provider_id
		WHERE mi.`+column+` = $1
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CostInvocation, 0)
	for rows.Next() {
		var (
			inputTokens  int64
			outputTokens int64
			metadataRaw  json.RawMessage
		)
		if err := rows.Scan(&inputTokens, &outputTokens, &metadataRaw); err != nil {
			return nil, err
		}
		inputCost, outputCost := parseProviderCosts(metadataRaw)
		result = append(result, CostInvocation{
			InputTokens:     inputTokens,
			OutputTokens:    outputTokens,
			InputCostPer1K:  inputCost,
			OutputCostPer1K: outputCost,
		})
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return result, nil
}

func parseProviderCosts(metadata json.RawMessage) (float64, float64) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return 0, 0
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return 0, 0
	}

	input := readFloatValue(payload, "input_cost_per_1k")
	output := readFloatValue(payload, "output_cost_per_1k")
	if pricing, ok := payload["pricing"].(map[string]any); ok {
		if input == 0 {
			input = readFloatValue(pricing, "input_cost_per_1k")
		}
		if output == 0 {
			output = readFloatValue(pricing, "output_cost_per_1k")
		}
	}
	return input, output
}

func readFloatValue(values map[string]any, key string) float64 {
	raw, ok := values[key]
	if !ok {
		return 0
	}
	switch value := raw.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0
		}
		return parsed
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}
