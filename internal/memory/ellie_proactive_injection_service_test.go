package memory

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEllieProactiveInjectionScoresAndThresholds(t *testing.T) {
	now := time.Date(2026, 2, 12, 15, 30, 0, 0, time.UTC)
	service := NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{
		Threshold: 0.62,
		MaxItems:  3,
	})

	bundle := service.BuildBundle(EllieProactiveInjectionBuildInput{
		Now:             now,
		RoomMessageCount: 12,
		PriorInjections:  0,
		Candidates: []EllieProactiveInjectionCandidate{
			{
				MemoryID:    "mem-high",
				Title:       "DB choice",
				Content:     "Use Postgres with explicit migrations",
				Importance:  5,
				Similarity:  0.92,
				OccurredAt:  now.Add(-24 * time.Hour),
				Confidence:  0.9,
			},
			{
				MemoryID:    "mem-low",
				Title:       "Low signal",
				Content:     "Maybe useful",
				Importance:  1,
				Similarity:  0.20,
				OccurredAt:  now.Add(-365 * 24 * time.Hour),
				Confidence:  0.4,
			},
		},
	})

	require.Len(t, bundle.Items, 1)
	require.Equal(t, "mem-high", bundle.Items[0].MemoryID)
	require.GreaterOrEqual(t, bundle.Items[0].Score, 0.62)
	require.Contains(t, bundle.Body, "Use Postgres with explicit migrations")
}

func TestEllieProactiveInjectionBundlesTopMatches(t *testing.T) {
	now := time.Date(2026, 2, 12, 15, 30, 0, 0, time.UTC)
	service := NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{
		Threshold: 0.50,
		MaxItems:  2,
	})

	bundle := service.BuildBundle(EllieProactiveInjectionBuildInput{
		Now:             now,
		RoomMessageCount: 8,
		PriorInjections:  1,
		Candidates: []EllieProactiveInjectionCandidate{
			{MemoryID: "mem-1", Title: "A", Content: "alpha", Importance: 5, Similarity: 0.95, OccurredAt: now.Add(-2 * time.Hour), Confidence: 0.9},
			{MemoryID: "mem-2", Title: "B", Content: "beta", Importance: 4, Similarity: 0.90, OccurredAt: now.Add(-6 * time.Hour), Confidence: 0.8},
			{MemoryID: "mem-3", Title: "C", Content: "gamma", Importance: 2, Similarity: 0.80, OccurredAt: now.Add(-48 * time.Hour), Confidence: 0.7},
		},
	})

	require.Len(t, bundle.Items, 2)
	require.Equal(t, "mem-1", bundle.Items[0].MemoryID)
	require.Equal(t, "mem-2", bundle.Items[1].MemoryID)
	require.Contains(t, bundle.Body, "alpha")
	require.Contains(t, bundle.Body, "beta")
	require.NotContains(t, bundle.Body, "gamma")
}

func TestEllieProactiveInjectionIncludesSupersessionNote(t *testing.T) {
	now := time.Date(2026, 2, 12, 15, 30, 0, 0, time.UTC)
	service := NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{
		Threshold: 0.4,
		MaxItems:  3,
	})
	oldMemoryID := "mem-old"

	bundle := service.BuildBundle(EllieProactiveInjectionBuildInput{
		Now:             now,
		RoomMessageCount: 5,
		PriorInjections:  0,
		Candidates: []EllieProactiveInjectionCandidate{
			{
				MemoryID:           "mem-new",
				Title:              "Database preference updated",
				Content:            "Current preference: MySQL",
				Importance:         5,
				Similarity:         0.9,
				OccurredAt:         now.Add(-time.Hour),
				Confidence:         0.95,
				SupersedesMemoryID: &oldMemoryID,
			},
		},
	})

	require.Len(t, bundle.Items, 1)
	require.Contains(t, strings.ToLower(bundle.Body), "superseded")
	require.Contains(t, bundle.Body, "Current preference: MySQL")
}
