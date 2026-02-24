package memory

import (
	"math"
	"strings"
	"time"
)

const (
	relevanceWeightCosine     = 0.5
	relevanceWeightRecency    = 0.3
	relevanceWeightConfidence = 0.2
)

func relevanceScore(cosineSimilarity float64, createdAt time.Time, memoryType string, confidence float64, now time.Time) float64 {
	recency := recencyScore(createdAt, memoryType, now)
	return (relevanceWeightCosine * cosineSimilarity) +
		(relevanceWeightRecency * recency) +
		(relevanceWeightConfidence * clampFloat(confidence, 0, 1))
}

func recencyScore(createdAt time.Time, memoryType string, now time.Time) float64 {
	if createdAt.IsZero() {
		return 0
	}
	ageHours := now.Sub(createdAt.UTC()).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	ageDays := ageHours / 24
	halfLife := halfLifeDays(memoryType)
	// Exponential decay where value is 0.5 at the half-life boundary.
	return math.Exp(-math.Ln2 * (ageDays / halfLife))
}

func halfLifeDays(memoryType string) float64 {
	switch strings.TrimSpace(memoryType) {
	case "episodic", "execution_summary":
		return 30
	default:
		return 180
	}
}

func cosineSimilarity(queryEmbedding, memoryEmbedding []float32) float64 {
	if len(queryEmbedding) == 0 || len(memoryEmbedding) == 0 {
		return 0
	}
	limit := len(queryEmbedding)
	if len(memoryEmbedding) < limit {
		limit = len(memoryEmbedding)
	}
	if limit == 0 {
		return 0
	}

	var dot float64
	var normA float64
	var normB float64
	for i := 0; i < limit; i++ {
		a := float64(queryEmbedding[i])
		b := float64(memoryEmbedding[i])
		dot += a * b
		normA += a * a
		normB += b * b
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
