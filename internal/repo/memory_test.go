package repo

import (
	"testing"
	"time"
)

func TestCandidatePromotionCutoffUsesDefaultHoldWindow(t *testing.T) {
	now := time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)
	got := CandidatePromotionCutoff(now)
	want := now.Add(-CandidatePromotionHoldDuration)
	if !got.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", got, want)
	}
}
