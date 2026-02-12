package memory

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	defaultEllieProactiveInjectionThreshold = 0.62
	defaultEllieProactiveInjectionMaxItems  = 3
)

type EllieProactiveInjectionConfig struct {
	Threshold float64
	MaxItems  int
}

type EllieProactiveInjectionCandidate struct {
	MemoryID           string
	Title              string
	Content            string
	Similarity         float64
	Importance         int
	Confidence         float64
	OccurredAt         time.Time
	SourceConversation string
	SupersedesMemoryID *string
}

type EllieProactiveInjectionBuildInput struct {
	Now              time.Time
	RoomMessageCount int
	PriorInjections  int
	Candidates       []EllieProactiveInjectionCandidate
}

type EllieProactiveInjectionBundleItem struct {
	EllieProactiveInjectionCandidate
	Score float64
}

type EllieProactiveInjectionBundle struct {
	Items []EllieProactiveInjectionBundleItem
	Body  string
}

type EllieProactiveInjectionService struct {
	threshold float64
	maxItems  int
}

func NewEllieProactiveInjectionService(cfg EllieProactiveInjectionConfig) *EllieProactiveInjectionService {
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = defaultEllieProactiveInjectionThreshold
	}
	if threshold > 1 {
		threshold = 1
	}
	maxItems := cfg.MaxItems
	if maxItems <= 0 {
		maxItems = defaultEllieProactiveInjectionMaxItems
	}
	if maxItems > 10 {
		maxItems = 10
	}

	return &EllieProactiveInjectionService{threshold: threshold, maxItems: maxItems}
}

func (s *EllieProactiveInjectionService) BuildBundle(input EllieProactiveInjectionBuildInput) EllieProactiveInjectionBundle {
	if s == nil {
		s = NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{})
	}

	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	items := make([]EllieProactiveInjectionBundleItem, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		score := scoreProactiveCandidate(now, input.RoomMessageCount, input.PriorInjections, candidate)
		if score < s.threshold {
			continue
		}
		items = append(items, EllieProactiveInjectionBundleItem{
			EllieProactiveInjectionCandidate: candidate,
			Score:                            score,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Similarity != items[j].Similarity {
			return items[i].Similarity > items[j].Similarity
		}
		if !items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].OccurredAt.After(items[j].OccurredAt)
		}
		return items[i].MemoryID < items[j].MemoryID
	})

	if len(items) > s.maxItems {
		items = items[:s.maxItems]
	}

	return EllieProactiveInjectionBundle{
		Items: items,
		Body:  formatEllieProactiveBundleBody(items),
	}
}

func scoreProactiveCandidate(
	now time.Time,
	roomMessageCount int,
	priorInjections int,
	candidate EllieProactiveInjectionCandidate,
) float64 {
	similarity := clampProactiveUnit(candidate.Similarity)
	importance := clampProactiveUnit(float64(candidate.Importance) / 5.0)
	confidence := clampProactiveUnit(candidate.Confidence)

	ageDays := 0.0
	if !candidate.OccurredAt.IsZero() {
		ageDays = now.Sub(candidate.OccurredAt.UTC()).Hours() / 24.0
		if ageDays < 0 {
			ageDays = 0
		}
	}
	recency := clampProactiveUnit(1.0 / (1.0 + (ageDays / 30.0)))

	novelty := 1.0
	if priorInjections > 0 {
		novelty = 1.0 / float64(1+priorInjections)
	}
	novelty = clampProactiveUnit(novelty)

	stage := 1.0
	switch {
	case roomMessageCount > 60:
		stage = 0.45
	case roomMessageCount > 20:
		stage = 0.7
	}

	// Confidence is blended into similarity to bias toward well-validated memories.
	effectiveSimilarity := (similarity * 0.8) + (confidence * 0.2)

	score :=
		0.45*effectiveSimilarity +
			0.20*recency +
			0.15*importance +
			0.12*novelty +
			0.08*stage

	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	return clampProactiveUnit(score)
}

func clampProactiveUnit(value float64) float64 {
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

func formatEllieProactiveBundleBody(items []EllieProactiveInjectionBundleItem) string {
	if len(items) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("📎 Context:\n")

	for i, item := range items {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, strings.TrimSpace(item.Title), strings.TrimSpace(item.Content)))
		if item.SupersedesMemoryID != nil && strings.TrimSpace(*item.SupersedesMemoryID) != "" {
			builder.WriteString(fmt.Sprintf("   Updated context: previous decision (%s) has been superseded.\n", strings.TrimSpace(*item.SupersedesMemoryID)))
		}
		builder.WriteString(fmt.Sprintf("   Confidence: %.2f | Score: %.2f\n", clampProactiveUnit(item.Confidence), clampProactiveUnit(item.Score)))
	}

	return strings.TrimSpace(builder.String())
}
