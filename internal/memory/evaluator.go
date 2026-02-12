package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

type EvaluatorCase struct {
	ID                   string   `json:"id"`
	Query                string   `json:"query"`
	RetrievedIDs         []string `json:"retrieved_ids"`
	RelevantIDs          []string `json:"relevant_ids"`
	ShouldInject         bool     `json:"should_inject"`
	Injected             bool     `json:"injected"`
	InjectedTokens       int      `json:"injected_tokens"`
	RecoveryExpected     bool     `json:"recovery_expected"`
	RecoverySucceeded    bool     `json:"recovery_succeeded"`
	SharedPromoted       bool     `json:"shared_promoted"`
	SharedCorrect        bool     `json:"shared_correct"`
	LatencyMs            float64  `json:"latency_ms"`
	EllieInjectedCount   int      `json:"ellie_injected_count"`
	EllieReferencedCount int      `json:"ellie_referenced_count"`
	EllieMissedCount     int      `json:"ellie_missed_count"`
}

type EvaluatorConfig struct {
	K                      int
	MinPrecisionAtK        float64
	MaxFalseInjectionRate  float64
	MinRecoverySuccessRate float64
	MaxP95LatencyMs        float64
}

type EvaluatorMetrics struct {
	PrecisionAtK             float64 `json:"recall_precision_at_k"`
	FalseInjectionRate       float64 `json:"false_injection_rate"`
	RecoverySuccessRate      float64 `json:"compaction_recovery_success_rate"`
	P95LatencyMs             float64 `json:"p95_recall_latency_ms"`
	AvgInjectedTokens        float64 `json:"avg_injected_tokens"`
	SharedPromotionPrecision float64 `json:"shared_promotion_precision"`
	EllieRetrievalPrecision  float64 `json:"ellie_retrieval_precision"`
	EllieRetrievalRecall     float64 `json:"ellie_retrieval_recall"`
	CaseCount                int     `json:"case_count"`
}

type EvaluatorGateResult struct {
	Name       string  `json:"name"`
	Comparator string  `json:"comparator"`
	Actual     float64 `json:"actual"`
	Threshold  float64 `json:"threshold"`
	Passed     bool    `json:"passed"`
}

type EvaluatorResult struct {
	Metrics     EvaluatorMetrics      `json:"metrics"`
	Gates       []EvaluatorGateResult `json:"gates"`
	Passed      bool                  `json:"passed"`
	FailedGates []string              `json:"failed_gates"`
}

type Evaluator struct {
	Config EvaluatorConfig
}

func (e Evaluator) RunFromJSONL(path string) (EvaluatorResult, error) {
	cases, err := LoadEvaluatorCasesJSONL(path)
	if err != nil {
		return EvaluatorResult{}, err
	}
	return e.Run(cases), nil
}

func (e Evaluator) Run(cases []EvaluatorCase) EvaluatorResult {
	cfg := e.Config.normalized()
	metrics := EvaluatorMetrics{
		PrecisionAtK:             computePrecisionAtK(cases, cfg.K),
		FalseInjectionRate:       computeFalseInjectionRate(cases),
		RecoverySuccessRate:      computeRecoverySuccessRate(cases),
		P95LatencyMs:             computeP95LatencyMs(cases),
		AvgInjectedTokens:        computeAvgInjectedTokens(cases),
		SharedPromotionPrecision: computeSharedPromotionPrecision(cases),
		EllieRetrievalPrecision:  computeEllieRetrievalPrecision(cases),
		EllieRetrievalRecall:     computeEllieRetrievalRecall(cases),
		CaseCount:                len(cases),
	}

	gates := []EvaluatorGateResult{
		{
			Name:       "recall_precision_at_k",
			Comparator: ">=",
			Actual:     metrics.PrecisionAtK,
			Threshold:  cfg.MinPrecisionAtK,
			Passed:     metrics.PrecisionAtK >= cfg.MinPrecisionAtK,
		},
		{
			Name:       "false_injection_rate",
			Comparator: "<=",
			Actual:     metrics.FalseInjectionRate,
			Threshold:  cfg.MaxFalseInjectionRate,
			Passed:     metrics.FalseInjectionRate <= cfg.MaxFalseInjectionRate,
		},
		{
			Name:       "compaction_recovery_success_rate",
			Comparator: ">=",
			Actual:     metrics.RecoverySuccessRate,
			Threshold:  cfg.MinRecoverySuccessRate,
			Passed:     metrics.RecoverySuccessRate >= cfg.MinRecoverySuccessRate,
		},
		{
			Name:       "p95_recall_latency_ms",
			Comparator: "<=",
			Actual:     metrics.P95LatencyMs,
			Threshold:  cfg.MaxP95LatencyMs,
			Passed:     metrics.P95LatencyMs <= cfg.MaxP95LatencyMs,
		},
	}

	failed := make([]string, 0)
	for _, gate := range gates {
		if !gate.Passed {
			failed = append(failed, gate.Name)
		}
	}

	return EvaluatorResult{
		Metrics:     metrics,
		Gates:       gates,
		Passed:      len(failed) == 0,
		FailedGates: failed,
	}
}

func (cfg EvaluatorConfig) normalized() EvaluatorConfig {
	normalized := cfg
	if normalized.K <= 0 {
		normalized.K = 5
	}
	normalized.MinPrecisionAtK = clampRate(normalized.MinPrecisionAtK)
	normalized.MaxFalseInjectionRate = clampRate(normalized.MaxFalseInjectionRate)
	normalized.MinRecoverySuccessRate = clampRate(normalized.MinRecoverySuccessRate)
	if !isFinitePositive(normalized.MaxP95LatencyMs) {
		normalized.MaxP95LatencyMs = math.MaxFloat64
	}
	return normalized
}

func LoadEvaluatorCasesJSONL(path string) ([]EvaluatorCase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open evaluator fixture: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNo := 0
	cases := make([]EvaluatorCase, 0)
	for scanner.Scan() {
		lineNo += 1
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var row EvaluatorCase
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse evaluator fixture line %d: %w", lineNo, err)
		}
		if strings.TrimSpace(row.ID) == "" {
			row.ID = fmt.Sprintf("line-%d", lineNo)
		}
		if !isFinitePositiveOrZero(row.LatencyMs) {
			row.LatencyMs = 0
		}
		cases = append(cases, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan evaluator fixture: %w", err)
	}
	return cases, nil
}

func computePrecisionAtK(cases []EvaluatorCase, k int) float64 {
	if k <= 0 {
		k = 5
	}
	total := 0.0
	denominator := 0
	for _, c := range cases {
		if !c.ShouldInject {
			continue
		}
		denominator += 1

		retrieved := c.RetrievedIDs
		if len(retrieved) == 0 {
			continue
		}
		limit := minInt(k, len(retrieved))
		if limit <= 0 {
			continue
		}

		relevantSet := make(map[string]struct{}, len(c.RelevantIDs))
		for _, id := range c.RelevantIDs {
			trimmed := strings.TrimSpace(id)
			if trimmed == "" {
				continue
			}
			relevantSet[trimmed] = struct{}{}
		}
		if len(relevantSet) == 0 {
			continue
		}

		hits := 0
		for i := 0; i < limit; i += 1 {
			id := strings.TrimSpace(retrieved[i])
			if id == "" {
				continue
			}
			if _, ok := relevantSet[id]; ok {
				hits += 1
			}
		}
		total += float64(hits) / float64(k)
	}
	if denominator == 0 {
		return 0
	}
	return total / float64(denominator)
}

func computeFalseInjectionRate(cases []EvaluatorCase) float64 {
	falseInjectCount := 0
	negativeCount := 0
	for _, c := range cases {
		if c.ShouldInject {
			continue
		}
		negativeCount += 1
		if c.Injected {
			falseInjectCount += 1
		}
	}
	if negativeCount == 0 {
		return 0
	}
	return float64(falseInjectCount) / float64(negativeCount)
}

func computeRecoverySuccessRate(cases []EvaluatorCase) float64 {
	expectedCount := 0
	successCount := 0
	for _, c := range cases {
		if !c.RecoveryExpected {
			continue
		}
		expectedCount += 1
		if c.RecoverySucceeded {
			successCount += 1
		}
	}
	if expectedCount == 0 {
		return 0
	}
	return float64(successCount) / float64(expectedCount)
}

func computeAvgInjectedTokens(cases []EvaluatorCase) float64 {
	totalTokens := 0
	injectedCount := 0
	for _, c := range cases {
		if !c.Injected {
			continue
		}
		if c.InjectedTokens < 0 {
			continue
		}
		totalTokens += c.InjectedTokens
		injectedCount += 1
	}
	if injectedCount == 0 {
		return 0
	}
	return float64(totalTokens) / float64(injectedCount)
}

func computeSharedPromotionPrecision(cases []EvaluatorCase) float64 {
	promotedCount := 0
	correctCount := 0
	for _, c := range cases {
		if !c.SharedPromoted {
			continue
		}
		promotedCount += 1
		if c.SharedCorrect {
			correctCount += 1
		}
	}
	if promotedCount == 0 {
		return 0
	}
	return float64(correctCount) / float64(promotedCount)
}

func computeP95LatencyMs(cases []EvaluatorCase) float64 {
	values := make([]float64, 0, len(cases))
	for _, c := range cases {
		if !isFinitePositiveOrZero(c.LatencyMs) {
			continue
		}
		values = append(values, c.LatencyMs)
	}
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	position := int(math.Ceil(0.95*float64(len(values)))) - 1
	if position < 0 {
		position = 0
	}
	if position >= len(values) {
		position = len(values) - 1
	}
	return values[position]
}

func computeEllieRetrievalPrecision(cases []EvaluatorCase) float64 {
	injectedTotal := 0
	referencedTotal := 0
	for _, c := range cases {
		if c.EllieInjectedCount > 0 {
			injectedTotal += c.EllieInjectedCount
		}
		if c.EllieReferencedCount > 0 {
			referencedTotal += c.EllieReferencedCount
		}
	}
	if injectedTotal <= 0 {
		return 0
	}
	return clampRate(float64(referencedTotal) / float64(injectedTotal))
}

func computeEllieRetrievalRecall(cases []EvaluatorCase) float64 {
	referencedTotal := 0
	missedTotal := 0
	for _, c := range cases {
		if c.EllieReferencedCount > 0 {
			referencedTotal += c.EllieReferencedCount
		}
		if c.EllieMissedCount > 0 {
			missedTotal += c.EllieMissedCount
		}
	}
	denominator := referencedTotal + missedTotal
	if denominator <= 0 {
		return 0
	}
	return clampRate(float64(referencedTotal) / float64(denominator))
}

func clampRate(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func isFinitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

func isFinitePositiveOrZero(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
