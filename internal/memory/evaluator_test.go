package memory

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluator(t *testing.T) {
	t.Run("runs fixture and passes quality gates", func(t *testing.T) {
		evaluator := Evaluator{
			Config: EvaluatorConfig{
				K:                      3,
				MinPrecisionAtK:        0.30,
				MaxFalseInjectionRate:  0.20,
				MinRecoverySuccessRate: 0.75,
				MaxP95LatencyMs:        250,
			},
		}

		fixturePath := filepath.Join("testdata", "evaluator_benchmark_v1.jsonl")
		result, err := evaluator.RunFromJSONL(fixturePath)
		require.NoError(t, err)
		require.True(t, result.Passed)
		require.InDelta(t, 1.0/3.0, result.Metrics.PrecisionAtK, 0.0001)
		require.InDelta(t, 0.0, result.Metrics.FalseInjectionRate, 0.0001)
		require.InDelta(t, 1.0, result.Metrics.RecoverySuccessRate, 0.0001)
		require.InDelta(t, 200, result.Metrics.P95LatencyMs, 0.0001)
		require.Empty(t, result.FailedGates)
	})

	t.Run("reports failing gates with threshold details", func(t *testing.T) {
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
			"precision_at_k",
			"false_injection_rate",
			"recovery_success_rate",
			"p95_latency_ms",
		}, result.FailedGates)

		require.Len(t, result.Gates, 4)
		gatesByName := make(map[string]EvaluatorGateResult, len(result.Gates))
		for _, gate := range result.Gates {
			gatesByName[gate.Name] = gate
		}
		require.False(t, gatesByName["precision_at_k"].Passed)
		require.Equal(t, ">=", gatesByName["precision_at_k"].Comparator)
		require.False(t, gatesByName["false_injection_rate"].Passed)
		require.Equal(t, "<=", gatesByName["false_injection_rate"].Comparator)
		require.False(t, gatesByName["recovery_success_rate"].Passed)
		require.False(t, gatesByName["p95_latency_ms"].Passed)
	})

	t.Run("loads benchmark fixtures from jsonl", func(t *testing.T) {
		fixturePath := filepath.Join("testdata", "evaluator_benchmark_v1.jsonl")
		cases, err := LoadEvaluatorCasesJSONL(fixturePath)
		require.NoError(t, err)
		require.Len(t, cases, 4)
		require.Equal(t, "recall-hit-1", cases[0].ID)
		require.Equal(t, 140.0, cases[0].LatencyMs)
	})
}
