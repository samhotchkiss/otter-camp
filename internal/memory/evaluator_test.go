package memory

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluatorAllMetrics(t *testing.T) {
	evaluator := Evaluator{
		Config: EvaluatorConfig{
			K:                      3,
			MinPrecisionAtK:        0.45,
			MaxFalseInjectionRate:  0.20,
			MinRecoverySuccessRate: 0.70,
			MaxP95LatencyMs:        300,
		},
	}

	fixturePath := filepath.Join("testdata", "evaluator_benchmark_v1.jsonl")
	result, err := evaluator.RunFromJSONL(fixturePath)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Greater(t, result.Metrics.PrecisionAtK, 0.0)
	require.GreaterOrEqual(t, result.Metrics.AvgInjectedTokens, 1.0)
	require.Greater(t, result.Metrics.SharedPromotionPrecision, 0.0)
	require.Greater(t, result.Metrics.P95LatencyMs, 0.0)
	require.GreaterOrEqual(t, result.Metrics.CaseCount, 20)
}

func TestEvaluatorEmptyRecovery(t *testing.T) {
	evaluator := Evaluator{
		Config: EvaluatorConfig{
			K:                      3,
			MinPrecisionAtK:        0.0,
			MaxFalseInjectionRate:  1.0,
			MinRecoverySuccessRate: 0.0,
			MaxP95LatencyMs:        500,
		},
	}

	result := evaluator.Run([]EvaluatorCase{
		{
			ID:           "c1",
			RetrievedIDs: []string{"r1", "r2"},
			RelevantIDs:  []string{"r1"},
			ShouldInject: true,
			Injected:     true,
			LatencyMs:    60,
		},
		{
			ID:           "c2",
			RetrievedIDs: []string{"n1", "n2"},
			RelevantIDs:  []string{},
			ShouldInject: false,
			Injected:     false,
			LatencyMs:    75,
		},
	})

	require.InDelta(t, 0.0, result.Metrics.RecoverySuccessRate, 0.0001)
}

func TestEvaluatorReportsFailingGates(t *testing.T) {
	evaluator := Evaluator{
		Config: EvaluatorConfig{
			K:                      3,
			MinPrecisionAtK:        0.70,
			MaxFalseInjectionRate:  0.10,
			MinRecoverySuccessRate: 1.0,
			MaxP95LatencyMs:        120,
		},
	}

	cases := []EvaluatorCase{
		{
			ID:                "c1",
			RetrievedIDs:      []string{"r1", "n1", "n2"},
			RelevantIDs:       []string{"r1", "r2"},
			ShouldInject:      true,
			Injected:          true,
			RecoveryExpected:  true,
			RecoverySucceeded: false,
			LatencyMs:         150,
		},
		{
			ID:               "c2",
			RetrievedIDs:     []string{"n3", "n4", "n5"},
			RelevantIDs:      []string{"r3"},
			ShouldInject:     false,
			Injected:         true,
			RecoveryExpected: false,
			LatencyMs:        200,
		},
	}

	result := evaluator.Run(cases)
	require.False(t, result.Passed)
	require.ElementsMatch(t, []string{
		"recall_precision_at_k",
		"false_injection_rate",
		"compaction_recovery_success_rate",
		"p95_recall_latency_ms",
	}, result.FailedGates)

	require.Len(t, result.Gates, 4)
	gatesByName := make(map[string]EvaluatorGateResult, len(result.Gates))
	for _, gate := range result.Gates {
		gatesByName[gate.Name] = gate
	}
	require.False(t, gatesByName["recall_precision_at_k"].Passed)
	require.Equal(t, ">=", gatesByName["recall_precision_at_k"].Comparator)
	require.False(t, gatesByName["false_injection_rate"].Passed)
	require.Equal(t, "<=", gatesByName["false_injection_rate"].Comparator)
	require.False(t, gatesByName["compaction_recovery_success_rate"].Passed)
	require.False(t, gatesByName["p95_recall_latency_ms"].Passed)
}

func TestLoadEvaluatorCasesJSONL(t *testing.T) {
	fixturePath := filepath.Join("testdata", "evaluator_benchmark_v1.jsonl")
	cases, err := LoadEvaluatorCasesJSONL(fixturePath)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cases), 20)
	require.Equal(t, "recall-hit-1", cases[0].ID)
	require.Greater(t, cases[0].InjectedTokens, 0)
}

func TestEvaluatorPrecisionAtKUsesConfiguredKDenominator(t *testing.T) {
	evaluator := Evaluator{Config: EvaluatorConfig{K: 5}}

	result := evaluator.Run([]EvaluatorCase{
		{
			ID:           "short-list-one",
			RetrievedIDs: []string{"mem-1"},
			RelevantIDs:  []string{"mem-1", "mem-2"},
			ShouldInject: true,
		},
		{
			ID:           "short-list-two",
			RetrievedIDs: []string{"mem-3", "noise-1"},
			RelevantIDs:  []string{"mem-3"},
			ShouldInject: true,
		},
	})

	require.InDelta(t, 0.2, result.Metrics.PrecisionAtK, 0.0001)
}
