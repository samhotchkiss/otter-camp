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

func TestNormalizeMemoryCompactionErrorMessageAddsDefaultForFailedStatus(t *testing.T) {
	got := normalizeMemoryCompactionErrorMessage("failed", nil)
	if got == nil {
		t.Fatal("error message = nil, want default")
	}
	if *got != MemoryCompactionDefaultFailedMessage {
		t.Fatalf("error message = %q, want %q", *got, MemoryCompactionDefaultFailedMessage)
	}
}

func TestNormalizeMemoryCompactionErrorMessageTrimsProvidedMessage(t *testing.T) {
	input := "  downstream timeout  "
	got := normalizeMemoryCompactionErrorMessage("failed", &input)
	if got == nil {
		t.Fatal("error message = nil, want non-nil")
	}
	if *got != "downstream timeout" {
		t.Fatalf("error message = %q, want %q", *got, "downstream timeout")
	}
}
