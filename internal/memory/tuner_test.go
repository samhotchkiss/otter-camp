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
			AuditSink: audit,
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
			Apply: func(_ context.Context, _ TunerConfig) error {
				applied = true
				return nil
			},
			AuditSink: audit,
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
			AuditSink: audit,
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
