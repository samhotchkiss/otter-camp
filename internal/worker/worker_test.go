package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/db"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

func TestWorkerDBMaxConnsDefaultsAboveGlobalFloor(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "")

	got, err := workerDBMaxConns()
	if err != nil {
		t.Fatalf("workerDBMaxConns returned error: %v", err)
	}
	if want := maxInt32(db.DefaultMaxConns, defaultWorkerMaxConns); got != want {
		t.Fatalf("worker max conns = %d, want %d", got, want)
	}
}

func TestWorkerConcurrencyUsesOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_CONCURRENCY", "12")

	got, err := workerConcurrency()
	if err != nil {
		t.Fatalf("workerConcurrency returned error: %v", err)
	}
	if got != 12 {
		t.Fatalf("worker concurrency = %d, want 12", got)
	}
}

func TestWorkerConcurrencyDefaultsWhenUnset(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_CONCURRENCY", "")

	got, err := workerConcurrency()
	if err != nil {
		t.Fatalf("workerConcurrency returned error: %v", err)
	}
	if got != 0 {
		t.Fatalf("worker concurrency = %d, want 0 when unset", got)
	}
}

func TestWorkerConcurrencyRejectsInvalidOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_CONCURRENCY", "bad")

	if _, err := workerConcurrency(); err == nil {
		t.Fatal("expected error for invalid worker concurrency override")
	}
}

func TestWorkerDBMaxConnsPrefersWorkerOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "41")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "24")

	got, err := workerDBMaxConns()
	if err != nil {
		t.Fatalf("workerDBMaxConns returned error: %v", err)
	}
	if got != 41 {
		t.Fatalf("worker max conns = %d, want 41", got)
	}
}

func TestWorkerDBMaxConnsFallsBackToGlobalOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "24")

	got, err := workerDBMaxConns()
	if err != nil {
		t.Fatalf("workerDBMaxConns returned error: %v", err)
	}
	if got != 24 {
		t.Fatalf("worker max conns = %d, want 24", got)
	}
}

func TestWorkerDBMaxConnsRejectsInvalidOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "bad")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "")

	if _, err := workerDBMaxConns(); err == nil {
		t.Fatal("expected error for invalid worker override")
	}
}

func TestDraftTaskAutoCompletesWhenPlanningAndOutcomeAreSatisfied(t *testing.T) {
	description := "Document findings on sourcing channels, qualification criteria, and intake workflows."
	plan := taskplan.Analyze("Document sourcing findings", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)
	updated, _, _, err := taskplan.ApplyProcessUpdate(metadata, taskplan.ProcessUpdate{
		HasArtifactChanges: true,
		Artifacts: []taskplan.ArtifactEvidence{
			{Slug: "prd", Summary: "Scope complete.", Sections: []string{"goals", "non-goals", "scope", "constraints", "success metrics", "open questions"}},
			{Slug: "implementation-plan", Summary: "Implementation complete.", Sections: []string{"milestones", "phasing", "owners", "rollout"}},
			{Slug: "acceptance-criteria", Summary: "Acceptance complete.", Sections: []string{"scenarios", "edge cases", "verification"}},
			{Slug: "dependency-log", Summary: "Dependencies complete.", Sections: []string{"dependencies", "risks", "mitigations"}},
		},
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}
	updated, err = taskorchestration.Apply(updated, taskorchestration.Update{
		OutcomeAssessment: taskorchestration.NewOutcomeAssessment(true, "The deliverable is complete.", mustTime(t)),
	})
	if err != nil {
		t.Fatalf("taskorchestration.Apply: %v", err)
	}

	if !draftTaskAutoCompletes(repo.ProjectTask{WorkStatus: "draft", Metadata: updated}) {
		t.Fatal("draftTaskAutoCompletes = false, want true")
	}
}

func TestDraftTaskAutoCompletesRejectsIncompletePlanning(t *testing.T) {
	description := "Document findings on sourcing channels, qualification criteria, and intake workflows."
	plan := taskplan.Analyze("Document sourcing findings", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)
	updated, err := taskorchestration.Apply(metadata, taskorchestration.Update{
		OutcomeAssessment: taskorchestration.NewOutcomeAssessment(true, "The deliverable is complete.", mustTime(t)),
	})
	if err != nil {
		t.Fatalf("taskorchestration.Apply: %v", err)
	}

	if draftTaskAutoCompletes(repo.ProjectTask{WorkStatus: "draft", Metadata: updated}) {
		t.Fatal("draftTaskAutoCompletes = true, want false")
	}
}

func mustTime(t *testing.T) (now time.Time) {
	t.Helper()
	return time.Unix(1700000000, 0).UTC()
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
