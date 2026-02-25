package model

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCostQuerySumForRunAggregatesTokensAndEstimatedCost(t *testing.T) {
	runID := uuid.New()
	source := &fakeCostInvocationSource{
		runRows: []CostInvocation{
			{InputTokens: 100, OutputTokens: 50, InputCostPer1K: 2.0, OutputCostPer1K: 4.0},
			{InputTokens: 300, OutputTokens: 150, InputCostPer1K: 2.0, OutputCostPer1K: 4.0},
			{InputTokens: 100, OutputTokens: 0, InputCostPer1K: 2.0, OutputCostPer1K: 4.0},
		},
	}
	query := &CostQuery{source: source}

	summary, err := query.SumForRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("SumForRun: %v", err)
	}

	if summary.InputTokens != 500 {
		t.Fatalf("input_tokens = %d, want 500", summary.InputTokens)
	}
	if summary.OutputTokens != 200 {
		t.Fatalf("output_tokens = %d, want 200", summary.OutputTokens)
	}
	if summary.TotalTokens != 700 {
		t.Fatalf("total_tokens = %d, want 700", summary.TotalTokens)
	}
	if summary.EstimatedCostCents != 2 {
		t.Fatalf("estimated_cost_cents = %d, want 2", summary.EstimatedCostCents)
	}
	for idx, invocation := range source.runRows {
		if invocation.EstimatedCostCents != 0 {
			t.Fatalf("source invocation %d estimated cost mutated to %f", idx, invocation.EstimatedCostCents)
		}
	}
}

func TestCostQuerySumForRunWithNoInvocationsReturnsZeroValue(t *testing.T) {
	query := &CostQuery{source: &fakeCostInvocationSource{runRows: nil}}

	summary, err := query.SumForRun(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("SumForRun: %v", err)
	}
	if summary != (TokenSummary{}) {
		t.Fatalf("summary = %+v, want zero value", summary)
	}
}

type fakeCostInvocationSource struct {
	runRows     []CostInvocation
	taskRows    []CostInvocation
	sessionRows []CostInvocation
}

func (f *fakeCostInvocationSource) ListForRun(context.Context, uuid.UUID) ([]CostInvocation, error) {
	return append([]CostInvocation(nil), f.runRows...), nil
}

func (f *fakeCostInvocationSource) ListForTask(context.Context, uuid.UUID) ([]CostInvocation, error) {
	return append([]CostInvocation(nil), f.taskRows...), nil
}

func (f *fakeCostInvocationSource) ListForSession(context.Context, uuid.UUID) ([]CostInvocation, error) {
	return append([]CostInvocation(nil), f.sessionRows...), nil
}
