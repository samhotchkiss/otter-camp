package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type auditSinkRecorder struct {
	records []TuningAttempt
}

func (r *auditSinkRecorder) Record(_ context.Context, attempt TuningAttempt) error {
	r.records = append(r.records, attempt)
	return nil
}

func deterministicRand(values ...int) func(int) int {
	index := 0
	return func(n int) int {
		if n <= 0 {
			return 0
		}
		if len(values) == 0 {
			return 0
		}
		value := values[index%len(values)]
		index += 1
		if value < 0 {
			value = -value
		}
		return value % n
	}
}

func changedParameterCount(left, right TunerConfig) int {
	changed := 0
	if left.RecallMinRelevance != right.RecallMinRelevance {
		changed += 1
	}
	if left.RecallMaxResults != right.RecallMaxResults {
		changed += 1
	}
	if left.RecallMaxChars != right.RecallMaxChars {
		changed += 1
	}
	return changed
}

func TestTuner(t *testing.T) {
	t.Run("applies candidate when evaluator gates improve", func(t *testing.T) {
		audit := &auditSinkRecorder{}
		applied := false
		rollbackCalled := false

		tuner := Tuner{
			Bounds: DefaultTunerBounds(),
			Evaluator: func(_ context.Context, cfg TunerConfig) (EvaluatorResult, error) {
				if cfg.RecallMinRelevance >= 0.70 {
					return EvaluatorResult{
						Passed: true,
						Metrics: EvaluatorMetrics{
							PrecisionAtK:        0.40,
							FalseInjectionRate:  0.20,
							RecoverySuccessRate: 0.70,
							P95LatencyMs:        180,
						},
					}, nil
				}
				return EvaluatorResult{
					Passed: true,
					Metrics: EvaluatorMetrics{
						PrecisionAtK:        0.55,
						FalseInjectionRate:  0.10,
						RecoverySuccessRate: 0.80,
						P95LatencyMs:        140,
					},
				}, nil
			},
			Apply: func(_ context.Context, _ TunerConfig) error {
				applied = true
				return nil
			},
			Rollback: func(_ context.Context, _ TunerConfig) error {
				rollbackCalled = true
				return nil
			},
			AuditSink:      audit,
			MutationRandFn: deterministicRand(0, 0),
			IDFn: func() string {
				return "attempt-1"
			},
			NowFn: func() time.Time {
				return time.Date(2026, 2, 10, 9, 40, 0, 0, time.UTC)
			},
		}

		decision, err := tuner.RunOnce(context.Background(), TunerConfig{
			RecallMinRelevance: 0.70,
			RecallMaxResults:   3,
			RecallMaxChars:     2000,
		})
		require.NoError(t, err)
		require.True(t, decision.Applied)
		require.False(t, decision.RolledBack)
		require.Equal(t, "applied", decision.Status)
		require.True(t, applied)
		require.False(t, rollbackCalled)
		require.Len(t, audit.records, 1)
		require.Equal(t, "applied", audit.records[0].Status)
		require.Equal(t, "attempt-1", audit.records[0].ID)
	})

	t.Run("skips candidate when no objective improvement", func(t *testing.T) {
		audit := &auditSinkRecorder{}
		applied := false

		tuner := Tuner{
			Bounds: DefaultTunerBounds(),
			Evaluator: func(_ context.Context, cfg TunerConfig) (EvaluatorResult, error) {
				if cfg.RecallMinRelevance >= 0.70 {
					return EvaluatorResult{
						Passed: true,
						Metrics: EvaluatorMetrics{
							PrecisionAtK:        0.55,
							FalseInjectionRate:  0.12,
							RecoverySuccessRate: 0.82,
							P95LatencyMs:        130,
						},
					}, nil
				}
				return EvaluatorResult{
					Passed: true,
					Metrics: EvaluatorMetrics{
						PrecisionAtK:        0.53,
						FalseInjectionRate:  0.13,
						RecoverySuccessRate: 0.81,
						P95LatencyMs:        140,
					},
				}, nil
			},
			Apply:          func(_ context.Context, _ TunerConfig) error { applied = true; return nil },
			AuditSink:      audit,
			MutationRandFn: deterministicRand(0, 0),
		}

		decision, err := tuner.RunOnce(context.Background(), TunerConfig{
			RecallMinRelevance: 0.70,
			RecallMaxResults:   3,
			RecallMaxChars:     2000,
		})
		require.NoError(t, err)
		require.False(t, decision.Applied)
		require.False(t, decision.RolledBack)
		require.Equal(t, "skipped", decision.Status)
		require.False(t, applied)
		require.Len(t, audit.records, 1)
		require.Equal(t, "skipped", audit.records[0].Status)
		require.NotEmpty(t, audit.records[0].Reason)
	})

	t.Run("rolls back when apply fails after improved candidate", func(t *testing.T) {
		audit := &auditSinkRecorder{}
		rollbackCalled := false

		tuner := Tuner{
			Bounds: DefaultTunerBounds(),
			Evaluator: func(_ context.Context, cfg TunerConfig) (EvaluatorResult, error) {
				if cfg.RecallMinRelevance >= 0.70 {
					return EvaluatorResult{
						Passed: true,
						Metrics: EvaluatorMetrics{
							PrecisionAtK:        0.40,
							FalseInjectionRate:  0.20,
							RecoverySuccessRate: 0.70,
							P95LatencyMs:        180,
						},
					}, nil
				}
				return EvaluatorResult{
					Passed: true,
					Metrics: EvaluatorMetrics{
						PrecisionAtK:        0.60,
						FalseInjectionRate:  0.08,
						RecoverySuccessRate: 0.84,
						P95LatencyMs:        120,
					},
				}, nil
			},
			Apply: func(_ context.Context, _ TunerConfig) error {
				return os.ErrPermission
			},
			Rollback: func(_ context.Context, cfg TunerConfig) error {
				rollbackCalled = true
				require.InDelta(t, 0.70, cfg.RecallMinRelevance, 0.0001)
				require.Equal(t, 3, cfg.RecallMaxResults)
				require.Equal(t, 2000, cfg.RecallMaxChars)
				return nil
			},
			AuditSink:      audit,
			MutationRandFn: deterministicRand(0, 0),
		}

		decision, err := tuner.RunOnce(context.Background(), TunerConfig{
			RecallMinRelevance: 0.70,
			RecallMaxResults:   3,
			RecallMaxChars:     2000,
		})
		require.NoError(t, err)
		require.False(t, decision.Applied)
		require.True(t, decision.RolledBack)
		require.Equal(t, "rolled_back", decision.Status)
		require.True(t, rollbackCalled)
		require.Len(t, audit.records, 1)
		require.Equal(t, "rolled_back", audit.records[0].Status)
	})

	t.Run("writes audit records to jsonl store", func(t *testing.T) {
		tempDir := t.TempDir()
		storePath := filepath.Join(tempDir, "audit", "tuning-attempts.jsonl")
		store := JSONLTuningAuditStore{Path: storePath}

		err := store.Record(context.Background(), TuningAttempt{
			ID:        "attempt-jsonl",
			Status:    "applied",
			Reason:    "test",
			StartedAt: time.Date(2026, 2, 10, 9, 45, 0, 0, time.UTC),
		})
		require.NoError(t, err)

		raw, err := os.ReadFile(storePath)
		require.NoError(t, err)
		require.Contains(t, string(raw), "\"id\":\"attempt-jsonl\"")
		require.Contains(t, string(raw), "\"status\":\"applied\"")
	})
}

func TestTunerRateLimit(t *testing.T) {
	now := time.Date(2026, 2, 10, 11, 0, 0, 0, time.UTC)
	evalCalls := 0
	applyCalls := 0
	audit := &auditSinkRecorder{}

	tuner := Tuner{
		Bounds: DefaultTunerBounds(),
		Evaluator: func(_ context.Context, _ TunerConfig) (EvaluatorResult, error) {
			evalCalls += 1
			return EvaluatorResult{
				Passed: true,
				Metrics: EvaluatorMetrics{
					PrecisionAtK:        0.9,
					FalseInjectionRate:  0.01,
					RecoverySuccessRate: 0.9,
					P95LatencyMs:        40,
				},
			}, nil
		},
		Apply: func(_ context.Context, _ TunerConfig) error {
			applyCalls += 1
			return nil
		},
		LastAppliedAtFn: func(context.Context) (time.Time, bool, error) {
			return now.Add(-2 * time.Hour), true, nil
		},
		NowFn:     func() time.Time { return now },
		AuditSink: audit,
	}

	decision, err := tuner.RunOnce(context.Background(), TunerConfig{
		RecallMinRelevance: 0.70,
		RecallMaxResults:   3,
		RecallMaxChars:     2000,
	})
	require.NoError(t, err)
	require.Equal(t, "skipped", decision.Status)
	require.Equal(t, "rate_limited", decision.Reason)
	require.Zero(t, evalCalls)
	require.Zero(t, applyCalls)
	require.Len(t, audit.records, 1)
	require.Equal(t, "rate_limited", audit.records[0].Reason)
}

func TestTunerNeverLowersSensitivity(t *testing.T) {
	bounds := DefaultTunerBounds().normalized()
	current := normalizeTunerConfig(TunerConfig{
		RecallMinRelevance: bounds.MinRelevance,
		RecallMaxResults:   5,
		RecallMaxChars:     2500,
		Sensitivity:        "restricted",
		Scope:              "team",
	}, bounds)

	candidate := mutateTunerConfig(current, bounds, 0, -1)
	require.Equal(t, current.Sensitivity, candidate.Sensitivity)
	require.Equal(t, current.Scope, candidate.Scope)
	require.GreaterOrEqual(t, candidate.RecallMinRelevance, tunerHardMinRecallMinRelevance)
	require.InDelta(t, current.RecallMinRelevance, candidate.RecallMinRelevance, 0.0001)
}

func TestTunerBidirectional(t *testing.T) {
	bounds := DefaultTunerBounds().normalized()
	current := normalizeTunerConfig(TunerConfig{
		RecallMinRelevance: 0.75,
		RecallMaxResults:   4,
		RecallMaxChars:     2400,
		Sensitivity:        "internal",
		Scope:              "team",
	}, bounds)

	decrease := mutateTunerConfig(current, bounds, 1, -1)
	increase := mutateTunerConfig(current, bounds, 1, 1)

	require.Equal(t, current.RecallMaxResults-bounds.ResultsStep, decrease.RecallMaxResults)
	require.Equal(t, current.RecallMaxResults+bounds.ResultsStep, increase.RecallMaxResults)
	require.Equal(t, 1, changedParameterCount(current, decrease))
	require.Equal(t, 1, changedParameterCount(current, increase))
	require.Equal(t, current.Sensitivity, decrease.Sensitivity)
	require.Equal(t, current.Scope, decrease.Scope)
}
