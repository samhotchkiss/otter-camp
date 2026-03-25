package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/db"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
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

func TestDraftTaskAutoCompletesRejectsBroadTask(t *testing.T) {
	description := "- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.\n- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.\n- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots."
	plan := taskplan.Analyze("Migration task", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)
	contracts := taskplan.ArtifactContractForPlan(plan)
	artifacts := make([]taskplan.ArtifactEvidence, 0, len(contracts))
	for _, contract := range contracts {
		artifacts = append(artifacts, taskplan.ArtifactEvidence{
			Slug:     contract.Slug,
			Summary:  contract.Title + " complete.",
			Sections: append([]string(nil), contract.RequiredSections...),
		})
	}
	updated, _, _, err := taskplan.ApplyProcessUpdate(metadata, taskplan.ProcessUpdate{
		HasArtifactChanges: true,
		Artifacts:          artifacts,
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

	if draftTaskAutoCompletes(repo.ProjectTask{
		ID:          uuid.New(),
		Title:       "Migration task",
		Description: &description,
		WorkStatus:  "draft",
		Metadata:    updated,
	}) {
		t.Fatal("draftTaskAutoCompletes = true, want false for broad task")
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

func TestStartupCleanupProjectDraftsSkipsSatisfiedDraftWithoutFlowTemplate(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "startup-cleanup-skip-missing-flow",
		DisplayName: "Startup Cleanup Skip Missing Flow",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "startup-cleanup-skip-missing-flow-project",
		DisplayName:    "Startup Cleanup Skip Missing Flow Project",
		Description:    "Project for startup cleanup coverage",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	description := "Document findings on sourcing channels, qualification criteria, and intake workflows."
	plan := taskplan.Analyze("Document sourcing findings", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)
	metadata, _, _, err = taskplan.ApplyProcessUpdate(metadata, taskplan.ProcessUpdate{
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
	metadata, err = taskorchestration.Apply(metadata, taskorchestration.Update{
		OutcomeAssessment: taskorchestration.NewOutcomeAssessment(true, "The deliverable is complete.", mustTime(t)),
	})
	if err != nil {
		t.Fatalf("taskorchestration.Apply: %v", err)
	}

	createdByID := uuid.New()
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		TaskNumber:     1,
		Title:          "Document sourcing findings",
		Description:    &description,
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    &createdByID,
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	bus := eventbus.New(pool, nil, eventbus.Config{})
	tasks, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("task service: %v", err)
	}

	parentRepaired, draftSettled, gatesCancelled, err := startupCleanupProjectDrafts(ctx, repo.NewProjectTaskRepo(pool), tasks, project.ID)
	if err != nil {
		t.Fatalf("startupCleanupProjectDrafts: %v", err)
	}
	if parentRepaired != 0 || draftSettled != 0 || gatesCancelled != 0 {
		t.Fatalf("cleanup counts = (%d,%d,%d), want all zero", parentRepaired, draftSettled, gatesCancelled)
	}

	updated, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.WorkStatus != "draft" {
		t.Fatalf("work status = %q, want draft", updated.WorkStatus)
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
