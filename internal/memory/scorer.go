package memory

import (
	"math"
	"regexp"
	"strings"
)

var (
	numberPattern     = regexp.MustCompile(`\d`)
	properNounPattern = regexp.MustCompile(`\b[A-Z][a-zA-Z]{2,}\b`)
)

type ScoreInput struct {
	Content         string
	MemoryType      string
	Entities        []ExtractedEntity
	UtilityEstimate float64
}

type ScoreBreakdown struct {
	Specificity int
	Durability  int
	Utility     int
	Total       int
}

func ScoreCandidate(input ScoreInput) ScoreBreakdown {
	specificity := scoreSpecificity(input)
	durability := scoreDurability(input)
	utility := scoreUtility(input)
	total := clampInt(specificity+durability+utility, 0, 100)
	return ScoreBreakdown{
		Specificity: specificity,
		Durability:  durability,
		Utility:     utility,
		Total:       total,
	}
}

func MeetsExtractionThreshold(score int) bool {
	return score >= stage2MinimumScore
}

func scoreSpecificity(input ScoreInput) int {
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return 0
	}

	words := tokenCount(content)
	score := 0
	switch {
	case words >= 20:
		score += 20
	case words >= 14:
		score += 14
	case words >= 8:
		score += 8
	default:
		score += 4
	}

	if numberPattern.MatchString(content) {
		score += 8
	}
	if properNounPattern.MatchString(content) {
		score += 6
	}
	if len(input.Entities) > 0 {
		score += 6
	}

	lowered := strings.ToLower(content)
	for _, phrase := range []string{
		"things", "stuff", "sometimes", "maybe", "kind of", "sort of", "some details",
	} {
		if strings.Contains(lowered, phrase) {
			score -= 8
		}
	}

	return clampInt(score, 0, 40)
}

func scoreDurability(input ScoreInput) int {
	score := map[string]int{
		"semantic":          18,
		"procedural":        20,
		"preference":        18,
		"entity_profile":    20,
		"episodic":          10,
		"execution_summary": 8,
	}[strings.TrimSpace(input.MemoryType)]

	lowered := strings.ToLower(input.Content)
	for _, cue := range []string{"always", "usually", "prefers", "policy", "standard", "recurring"} {
		if strings.Contains(lowered, cue) {
			score += 6
			break
		}
	}
	for _, cue := range []string{"today", "just now", "temporary", "one-time", "this morning", "this afternoon"} {
		if strings.Contains(lowered, cue) {
			score -= 8
			break
		}
	}
	if len(input.Entities) > 0 {
		score += 4
	}
	return clampInt(score, 0, 30)
}

func scoreUtility(input ScoreInput) int {
	score := int(math.Round(clampFloat(input.UtilityEstimate, 0, 1) * 15))
	lowered := strings.ToLower(input.Content)

	if len(input.Entities) > 0 {
		score += 8
	}
	for _, cue := range []string{"must", "should", "needs", "deadline", "deploy", "customer", "preference"} {
		if strings.Contains(lowered, cue) {
			score += 7
			break
		}
	}
	for _, phrase := range []string{"nothing special", "not sure", "unclear"} {
		if strings.Contains(lowered, phrase) {
			score -= 8
		}
	}

	return clampInt(score, 0, 30)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
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
