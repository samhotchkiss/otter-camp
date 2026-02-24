package compaction

import (
	"math"
	"testing"
	"time"
)

func TestCalculateDecayHalfLife(t *testing.T) {
	halfLife := 30 * 24 * time.Hour

	cases := []struct {
		name    string
		elapsed time.Duration
		want    float64
	}{
		{name: "31 days", elapsed: 31 * 24 * time.Hour, want: 0.3},
		{name: "62 days", elapsed: 62 * 24 * time.Hour, want: 0.15},
		{name: "93 days", elapsed: 93 * 24 * time.Hour, want: 0.075},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateDecay(0.6, tc.elapsed, halfLife)
			if math.Abs(got-tc.want) > 0.000001 {
				t.Fatalf("CalculateDecay = %f, want %f", got, tc.want)
			}
		})
	}
}

func TestScopePromotionAction(t *testing.T) {
	cases := []struct {
		memoryType      string
		entityTaskCount int
		want            string
	}{
		{memoryType: "episodic", entityTaskCount: 0, want: ScopeActionArchive},
		{memoryType: "semantic", entityTaskCount: 0, want: ScopeActionPromoteProject},
		{memoryType: "procedural", entityTaskCount: 0, want: ScopeActionPromoteProject},
		{memoryType: "preference", entityTaskCount: 0, want: ScopeActionPromoteProject},
		{memoryType: "entity_profile", entityTaskCount: 2, want: ScopeActionArchive},
		{memoryType: "entity_profile", entityTaskCount: 3, want: ScopeActionPromoteProject},
		{memoryType: "unknown", entityTaskCount: 10, want: ScopeActionArchive},
	}

	for _, tc := range cases {
		t.Run(tc.memoryType, func(t *testing.T) {
			if got := ScopePromotionAction(tc.memoryType, tc.entityTaskCount); got != tc.want {
				t.Fatalf("ScopePromotionAction(%q, %d) = %q, want %q", tc.memoryType, tc.entityTaskCount, got, tc.want)
			}
		})
	}
}
