package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluatorAndTunerAcceptEllieQualityMetrics(t *testing.T) {
	evaluator := Evaluator{
		Config: EvaluatorConfig{
			K:                      5,
			MinPrecisionAtK:        0.0,
			MaxFalseInjectionRate:  1.0,
			MinRecoverySuccessRate: 0.0,
			MaxP95LatencyMs:        1000,
		},
	}

	evalResult := evaluator.Run([]EvaluatorCase{
		{
			ID:                   "ellie-quality-1",
			ShouldInject:         true,
			RetrievedIDs:         []string{"mem-1", "mem-2"},
			RelevantIDs:          []string{"mem-1"},
			EllieInjectedCount:   4,
			EllieReferencedCount: 3,
			EllieMissedCount:     1,
		},
		{
			ID:                   "ellie-quality-2",
			ShouldInject:         true,
			RetrievedIDs:         []string{"mem-3"},
			RelevantIDs:          []string{"mem-3"},
			EllieInjectedCount:   2,
			EllieReferencedCount: 1,
			EllieMissedCount:     2,
		},
	})
	require.InDelta(t, 4.0/6.0, evalResult.Metrics.EllieRetrievalPrecision, 0.0001)
	require.InDelta(t, 4.0/7.0, evalResult.Metrics.EllieRetrievalRecall, 0.0001)

	evalCalls := 0
	applied := false
	tuner := Tuner{
		Bounds: DefaultTunerBounds(),
		Evaluator: func(_ context.Context, _ TunerConfig) (EvaluatorResult, error) {
			evalCalls += 1
			if evalCalls == 1 {
				return EvaluatorResult{
					Passed: true,
					Metrics: EvaluatorMetrics{
						PrecisionAtK:            0.8,
						FalseInjectionRate:      0.1,
						RecoverySuccessRate:     0.9,
						P95LatencyMs:            120,
						EllieRetrievalPrecision: 0.40,
						EllieRetrievalRecall:    0.30,
					},
				}, nil
			}
			return EvaluatorResult{
				Passed: true,
				Metrics: EvaluatorMetrics{
					PrecisionAtK:            0.8,
					FalseInjectionRate:      0.1,
					RecoverySuccessRate:     0.9,
					P95LatencyMs:            120,
					EllieRetrievalPrecision: 0.75,
					EllieRetrievalRecall:    0.70,
				},
			}, nil
		},
		Apply: func(_ context.Context, _ TunerConfig) error {
			applied = true
			return nil
		},
		MutationRandFn: deterministicRand(0, 0),
	}

	decision, err := tuner.RunOnce(context.Background(), TunerConfig{
		RecallMinRelevance: 0.7,
		RecallMaxResults:   5,
		RecallMaxChars:     2000,
	})
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "applied", decision.Status)
}
