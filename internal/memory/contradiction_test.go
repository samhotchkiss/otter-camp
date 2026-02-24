package memory

import "testing"

func TestContradictionDetectorKeepsLowConfidenceAsCandidate(t *testing.T) {
	if !keepAsCandidateAfterContradiction(0.19) {
		t.Fatal("expected confidence < 0.2 to remain candidate")
	}
	if keepAsCandidateAfterContradiction(0.2) {
		t.Fatal("expected confidence >= 0.2 to avoid forced candidate status")
	}
}
