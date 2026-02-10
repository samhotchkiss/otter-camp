package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	tunerHardMinRecallMinRelevance = 0.60
	tunerDefaultApplyInterval      = 24 * time.Hour
)

type TunerConfig struct {
	RecallMinRelevance float64 `json:"recall_min_relevance"`
	RecallMaxResults   int     `json:"recall_max_results"`
	RecallMaxChars     int     `json:"recall_max_chars"`
	Sensitivity        string  `json:"sensitivity"`
	Scope              string  `json:"scope"`
}

type TunerBounds struct {
	MinRelevance  float64
	MaxRelevance  float64
	RelevanceStep float64

	MinResults  int
	MaxResults  int
	ResultsStep int

	MinChars  int
	MaxChars  int
	CharsStep int
}

type TuningAttempt struct {
	ID              string          `json:"id"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     time.Time       `json:"completed_at"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason,omitempty"`
	BaselineConfig  TunerConfig     `json:"baseline_config"`
	CandidateConfig TunerConfig     `json:"candidate_config"`
	BaselineResult  EvaluatorResult `json:"baseline_result"`
	CandidateResult EvaluatorResult `json:"candidate_result"`
}

type TuningDecision struct {
	AttemptID       string          `json:"attempt_id"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason,omitempty"`
	Applied         bool            `json:"applied"`
	RolledBack      bool            `json:"rolled_back"`
	BaselineConfig  TunerConfig     `json:"baseline_config"`
	CandidateConfig TunerConfig     `json:"candidate_config"`
	BaselineResult  EvaluatorResult `json:"baseline_result"`
	CandidateResult EvaluatorResult `json:"candidate_result"`
}

type TuningAuditSink interface {
	Record(ctx context.Context, attempt TuningAttempt) error
}

type JSONLTuningAuditStore struct {
	Path string
}

func (s JSONLTuningAuditStore) Record(_ context.Context, attempt TuningAttempt) error {
	path := s.Path
	if path == "" {
		return errors.New("jsonl tuning audit path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create tuning audit directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open tuning audit file: %w", err)
	}
	defer file.Close()

	encoded, err := json.Marshal(attempt)
	if err != nil {
		return fmt.Errorf("marshal tuning audit record: %w", err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write tuning audit record: %w", err)
	}
	return nil
}

type Tuner struct {
	Bounds           TunerBounds
	Evaluator        func(ctx context.Context, cfg TunerConfig) (EvaluatorResult, error)
	Apply            func(ctx context.Context, cfg TunerConfig) error
	Rollback         func(ctx context.Context, cfg TunerConfig) error
	AuditSink        TuningAuditSink
	LastAppliedAtFn  func(ctx context.Context) (time.Time, bool, error)
	MinApplyInterval time.Duration
	MutationRandFn   func(n int) int
	NowFn            func() time.Time
	IDFn             func() string
}

func DefaultTunerBounds() TunerBounds {
	return TunerBounds{
		MinRelevance:  tunerHardMinRecallMinRelevance,
		MaxRelevance:  0.95,
		RelevanceStep: 0.05,
		MinResults:    1,
		MaxResults:    10,
		ResultsStep:   1,
		MinChars:      500,
		MaxChars:      8000,
		CharsStep:     250,
	}
}

func (t Tuner) RunOnce(ctx context.Context, current TunerConfig) (TuningDecision, error) {
	if t.Evaluator == nil {
		return TuningDecision{}, errors.New("tuner evaluator is required")
	}
	if t.Apply == nil {
		return TuningDecision{}, errors.New("tuner apply callback is required")
	}

	nowFn := t.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	idFn := t.IDFn
	if idFn == nil {
		idFn = func() string {
			return fmt.Sprintf("attempt-%d", nowFn().UnixNano())
		}
	}

	bounds := t.Bounds.normalized()
	baseline := normalizeTunerConfig(current, bounds)
	randFn := t.MutationRandFn
	if randFn == nil {
		randFn = rand.Intn
	}
	candidate := mutateTunerConfig(
		baseline,
		bounds,
		randFn(3),
		directionFromChoice(randFn(2)),
	)

	attempt := TuningAttempt{
		ID:              idFn(),
		StartedAt:       nowFn().UTC(),
		BaselineConfig:  baseline,
		CandidateConfig: candidate,
	}
	decision := TuningDecision{
		AttemptID:       attempt.ID,
		BaselineConfig:  baseline,
		CandidateConfig: candidate,
	}
	if err := t.enforceRateLimit(ctx, nowFn().UTC(), &attempt, &decision); err != nil {
		return decision, err
	}
	if decision.Status == "skipped" && decision.Reason == "rate_limited" {
		return decision, nil
	}

	baselineResult, err := t.Evaluator(ctx, baseline)
	if err != nil {
		return decision, fmt.Errorf("run baseline evaluator: %w", err)
	}
	candidateResult, err := t.Evaluator(ctx, candidate)
	if err != nil {
		return decision, fmt.Errorf("run candidate evaluator: %w", err)
	}

	attempt.BaselineResult = baselineResult
	attempt.CandidateResult = candidateResult
	decision.BaselineResult = baselineResult
	decision.CandidateResult = candidateResult

	better, hasRegression := compareEvaluatorMetrics(candidateResult.Metrics, baselineResult.Metrics)
	switch {
	case !candidateResult.Passed:
		decision.Status = "skipped"
		decision.Reason = "candidate_failed_gates"
	case hasRegression:
		decision.Status = "skipped"
		decision.Reason = "candidate_regressed"
	case !better:
		decision.Status = "skipped"
		decision.Reason = "no_objective_improvement"
	default:
		if err := t.Apply(ctx, candidate); err != nil {
			decision.Status = "rolled_back"
			decision.Reason = fmt.Sprintf("apply_failed: %v", err)
			decision.RolledBack = true
			if t.Rollback != nil {
				if rollbackErr := t.Rollback(ctx, baseline); rollbackErr != nil {
					decision.Status = "rollback_failed"
					decision.Reason = fmt.Sprintf("apply_failed: %v; rollback_failed: %v", err, rollbackErr)
				}
			}
		} else {
			decision.Status = "applied"
			decision.Applied = true
		}
	}

	attempt.Status = decision.Status
	attempt.Reason = decision.Reason
	attempt.CompletedAt = nowFn().UTC()
	if err := t.persistAttempt(ctx, attempt); err != nil {
		return decision, err
	}
	return decision, nil
}

func (t Tuner) persistAttempt(ctx context.Context, attempt TuningAttempt) error {
	if t.AuditSink == nil {
		return nil
	}
	if err := t.AuditSink.Record(ctx, attempt); err != nil {
		return fmt.Errorf("persist tuning attempt: %w", err)
	}
	return nil
}

func (t Tuner) enforceRateLimit(
	ctx context.Context,
	now time.Time,
	attempt *TuningAttempt,
	decision *TuningDecision,
) error {
	if t.LastAppliedAtFn == nil {
		return nil
	}
	lastAppliedAt, ok, err := t.LastAppliedAtFn(ctx)
	if err != nil {
		return fmt.Errorf("get last applied time: %w", err)
	}
	if !ok {
		return nil
	}
	interval := t.MinApplyInterval
	if interval <= 0 {
		interval = tunerDefaultApplyInterval
	}
	if now.Sub(lastAppliedAt.UTC()) >= interval {
		return nil
	}
	decision.Status = "skipped"
	decision.Reason = "rate_limited"
	attempt.Status = decision.Status
	attempt.Reason = decision.Reason
	attempt.CompletedAt = now
	if err := t.persistAttempt(ctx, *attempt); err != nil {
		return err
	}
	return nil
}

func (bounds TunerBounds) normalized() TunerBounds {
	normalized := bounds
	defaults := DefaultTunerBounds()

	if !isFinitePositive(normalized.MinRelevance) {
		normalized.MinRelevance = defaults.MinRelevance
	}
	if normalized.MinRelevance < tunerHardMinRecallMinRelevance {
		normalized.MinRelevance = tunerHardMinRecallMinRelevance
	}
	if !isFinitePositive(normalized.MaxRelevance) {
		normalized.MaxRelevance = defaults.MaxRelevance
	}
	if normalized.MaxRelevance < normalized.MinRelevance {
		normalized.MaxRelevance = normalized.MinRelevance
	}
	if normalized.MinRelevance > normalized.MaxRelevance {
		normalized.MinRelevance = defaults.MinRelevance
		normalized.MaxRelevance = defaults.MaxRelevance
	}
	if !isFinitePositive(normalized.RelevanceStep) {
		normalized.RelevanceStep = defaults.RelevanceStep
	}

	if normalized.MinResults <= 0 {
		normalized.MinResults = defaults.MinResults
	}
	if normalized.MaxResults <= 0 {
		normalized.MaxResults = defaults.MaxResults
	}
	if normalized.MinResults > normalized.MaxResults {
		normalized.MinResults = defaults.MinResults
		normalized.MaxResults = defaults.MaxResults
	}
	if normalized.ResultsStep <= 0 {
		normalized.ResultsStep = defaults.ResultsStep
	}

	if normalized.MinChars <= 0 {
		normalized.MinChars = defaults.MinChars
	}
	if normalized.MaxChars <= 0 {
		normalized.MaxChars = defaults.MaxChars
	}
	if normalized.MinChars > normalized.MaxChars {
		normalized.MinChars = defaults.MinChars
		normalized.MaxChars = defaults.MaxChars
	}
	if normalized.CharsStep <= 0 {
		normalized.CharsStep = defaults.CharsStep
	}

	return normalized
}

func normalizeTunerConfig(cfg TunerConfig, bounds TunerBounds) TunerConfig {
	normalized := cfg
	normalized.RecallMinRelevance = clampFloat(normalized.RecallMinRelevance, bounds.MinRelevance, bounds.MaxRelevance)
	normalized.RecallMaxResults = clampInt(normalized.RecallMaxResults, bounds.MinResults, bounds.MaxResults)
	normalized.RecallMaxChars = clampInt(normalized.RecallMaxChars, bounds.MinChars, bounds.MaxChars)
	normalized.Sensitivity = normalizeSensitivity(normalized.Sensitivity)
	normalized.Scope = normalizeScope(normalized.Scope)
	return normalized
}

func mutateTunerConfig(current TunerConfig, bounds TunerBounds, parameterIndex, direction int) TunerConfig {
	candidate := current
	switch normalizeParameterIndex(parameterIndex) {
	case 0:
		candidate.RecallMinRelevance = clampFloat(
			current.RecallMinRelevance+bounds.RelevanceStep*float64(direction),
			bounds.MinRelevance,
			bounds.MaxRelevance,
		)
	case 1:
		candidate.RecallMaxResults = clampInt(
			current.RecallMaxResults+bounds.ResultsStep*direction,
			bounds.MinResults,
			bounds.MaxResults,
		)
	default:
		candidate.RecallMaxChars = clampInt(
			current.RecallMaxChars+bounds.CharsStep*direction,
			bounds.MinChars,
			bounds.MaxChars,
		)
	}
	// Never widen sensitivity/scope protections during autonomous tuning.
	candidate.Sensitivity = current.Sensitivity
	candidate.Scope = current.Scope
	return candidate
}

func directionFromChoice(choice int) int {
	if choice%2 == 0 {
		return -1
	}
	return 1
}

func normalizeParameterIndex(value int) int {
	if value < 0 {
		return (-value) % 3
	}
	return value % 3
}

func normalizeSensitivity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "public":
		return "public"
	case "restricted":
		return "restricted"
	default:
		return "internal"
	}
}

func normalizeScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "agent":
		return "agent"
	case "team":
		return "team"
	default:
		return "org"
	}
}

func compareEvaluatorMetrics(candidate, baseline EvaluatorMetrics) (better bool, hasRegression bool) {
	const epsilon = 1e-9

	if candidate.PrecisionAtK+epsilon < baseline.PrecisionAtK {
		hasRegression = true
	}
	if candidate.FalseInjectionRate > baseline.FalseInjectionRate+epsilon {
		hasRegression = true
	}
	if candidate.RecoverySuccessRate+epsilon < baseline.RecoverySuccessRate {
		hasRegression = true
	}
	if candidate.P95LatencyMs > baseline.P95LatencyMs+epsilon {
		hasRegression = true
	}

	if candidate.PrecisionAtK > baseline.PrecisionAtK+epsilon {
		better = true
	}
	if candidate.FalseInjectionRate+epsilon < baseline.FalseInjectionRate {
		better = true
	}
	if candidate.RecoverySuccessRate > baseline.RecoverySuccessRate+epsilon {
		better = true
	}
	if candidate.P95LatencyMs+epsilon < baseline.P95LatencyMs {
		better = true
	}

	return better, hasRegression
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return minValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
