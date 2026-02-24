package memory

import (
	"strings"
	"unicode"
)

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func tokenCount(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	return len(strings.Fields(trimmed))
}

func normalizeEntityName(value string) string {
	normalized := normalizeWhitespace(value)
	if normalized == "" {
		return ""
	}

	words := strings.Fields(normalized)
	for index, word := range words {
		runes := []rune(strings.ToLower(word))
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		words[index] = string(runes)
	}
	return strings.Join(words, " ")
}

func chooseMoreSpecificCanonical(current, candidate string) string {
	current = normalizeWhitespace(current)
	candidate = normalizeWhitespace(candidate)
	if current == "" {
		return candidate
	}
	if candidate == "" {
		return current
	}

	currentWords := tokenCount(current)
	candidateWords := tokenCount(candidate)
	if candidateWords > currentWords {
		return candidate
	}
	if candidateWords < currentWords {
		return current
	}
	if len(candidate) > len(current) {
		return candidate
	}
	return current
}

func levenshteinDistance(a, b string) int {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == b {
		return 0
	}
	if a == "" {
		return len([]rune(b))
	}
	if b == "" {
		return len([]rune(a))
	}

	ra := []rune(a)
	rb := []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := 0; j <= len(rb); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			insert := curr[j-1] + 1
			delete := prev[j] + 1
			replace := prev[j-1] + cost
			curr[j] = minInt(insert, minInt(delete, replace))
		}
		copy(prev, curr)
	}
	return prev[len(rb)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
