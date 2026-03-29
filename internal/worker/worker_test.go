package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/db"
	"github.com/samhotchkiss/otter-camp/internal/delivery"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
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

func TestDraftTaskAutoCompletesWhenBroadSingleFileDeliverableIsSatisfied(t *testing.T) {
	description := "Write `planning/sambot-feature-spec.md` as a complete SamBot feature specification covering mission, target users, personality, architecture, data sources, UI/UX, mobile responsiveness, accessibility, and implementation checklist."
	plan := taskplan.Analyze("Write SamBot feature specification", &description)
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
		OutcomeAssessment: taskorchestration.NewOutcomeAssessment(true, "planning/sambot-feature-spec.md contains the full SamBot feature specification and the deliverable is complete.", mustTime(t)),
	})
	if err != nil {
		t.Fatalf("taskorchestration.Apply: %v", err)
	}

	if !draftTaskAutoCompletes(repo.ProjectTask{
		ID:          uuid.New(),
		Title:       "Write SamBot feature specification",
		Description: &description,
		WorkStatus:  "draft",
		Metadata:    updated,
	}) {
		t.Fatal("draftTaskAutoCompletes = false, want true for satisfied single-file planning deliverable")
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

func TestStartupCleanupProjectDraftsCompletesSatisfiedSingleFilePlanningDraft(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "startup-cleanup-single-file",
		DisplayName: "Startup Cleanup Single File",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "startup-cleanup-single-file-project",
		DisplayName:    "Startup Cleanup Single File Project",
		Description:    "Project for startup cleanup single-file planning coverage",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "startup-cleanup-single-file-template",
		DisplayName:    "Startup Cleanup Single File Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	description := "Write `planning/sambot-feature-spec.md` as a complete SamBot feature specification covering mission, target users, personality, architecture, data sources, UI/UX, mobile responsiveness, accessibility, and implementation checklist."
	plan := taskplan.Analyze("Write SamBot feature specification", &description)
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
	metadata, _, _, err = taskplan.ApplyProcessUpdate(metadata, taskplan.ProcessUpdate{
		HasArtifactChanges: true,
		Artifacts:          artifacts,
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}
	metadata, err = taskorchestration.Apply(metadata, taskorchestration.Update{
		OutcomeAssessment: taskorchestration.NewOutcomeAssessment(true, "planning/sambot-feature-spec.md contains the full SamBot feature specification and the deliverable is complete.", mustTime(t)),
	})
	if err != nil {
		t.Fatalf("taskorchestration.Apply: %v", err)
	}

	createdByID := uuid.New()
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		TaskNumber:     1,
		Title:          "Write SamBot feature specification",
		Description:    &description,
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    &createdByID,
		FlowTemplateID: &template.ID,
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
	if parentRepaired != 0 || draftSettled != 1 || gatesCancelled != 0 {
		t.Fatalf("cleanup counts = (%d,%d,%d), want (0,1,0)", parentRepaired, draftSettled, gatesCancelled)
	}

	updated, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.WorkStatus != "done" {
		t.Fatalf("work status = %q, want done", updated.WorkStatus)
	}
}

func TestRepairMissingMergeQueueEntriesForActiveProjectsEnqueuesMergeJobs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "merge-repair-org",
		DisplayName: "Merge Repair Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "merge-repair-project",
		DisplayName:    "Merge Repair Project",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "merge-repair-template",
		DisplayName:    "Merge Repair Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	branch := "task/" + uuid.NewString()[:8]
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Completed task awaiting merge",
		WorkStatus:     "done",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    uuidPtr(uuid.New()),
		BranchName:     &branch,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	}); err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	bus := eventbus.New(pool, nil, eventbus.Config{})
	tasks, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("task service: %v", err)
	}

	repaired, err := repairMissingMergeQueueEntriesForActiveProjects(ctx, pool, tasks)
	if err != nil {
		t.Fatalf("repairMissingMergeQueueEntriesForActiveProjects: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired = %d, want 1", repaired)
	}

	entry, err := repo.NewMergeQueueEntryRepo(pool).GetByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByTask merge entry: %v", err)
	}
	if entry.Status != "queued" {
		t.Fatalf("merge entry status = %q, want queued", entry.Status)
	}

	var jobCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'merge_execute'
		  AND payload->>'merge_queue_entry_id' = $1
		  AND payload->>'project_id' = $2
	`, entry.ID.String(), project.ID.String()).Scan(&jobCount); err != nil {
		t.Fatalf("count merge_execute jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("merge_execute jobs = %d, want 1", jobCount)
	}
}

func TestRearmQueuedMergeExecuteJobsForActiveProjectsEnqueuesReplacementJob(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "merge-rearm-org",
		DisplayName: "Merge Rearm Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "merge-rearm-project",
		DisplayName:    "Merge Rearm Project",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "merge-rearm-template",
		DisplayName:    "Merge Rearm Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	branch := "task/" + uuid.NewString()[:8]
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Completed task with dead-letter merge job",
		WorkStatus:     "done",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    uuidPtr(uuid.New()),
		BranchName:     &branch,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	entry, err := repo.NewMergeQueueEntryRepo(pool).Enqueue(ctx, repo.MergeQueueEntry{
		ProjectID:  project.ID,
		TaskID:     taskRecord.ID,
		BranchName: branch,
		Status:     "queued",
	})
	if err != nil {
		t.Fatalf("enqueue merge entry: %v", err)
	}
	if _, err := jobqueue.New(pool, nil, jobqueue.Config{}).Enqueue(ctx, nil, delivery.MergeExecuteJobType, 100, map[string]any{
		"merge_queue_entry_id": entry.ID,
		"project_id":           project.ID,
	}, nil); err != nil {
		t.Fatalf("enqueue merge job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE job_queue
		SET status = 'dead_letter', attempts = max_attempts, last_error = 'forced dead-letter for test'
		WHERE job_type = 'merge_execute'
		  AND payload->>'merge_queue_entry_id' = $1
	`, entry.ID.String()); err != nil {
		t.Fatalf("dead-letter merge job: %v", err)
	}

	rearmed, err := rearmQueuedMergeExecuteJobsForActiveProjects(ctx, pool)
	if err != nil {
		t.Fatalf("rearmQueuedMergeExecuteJobsForActiveProjects: %v", err)
	}
	if rearmed != 1 {
		t.Fatalf("rearmed = %d, want 1", rearmed)
	}

	var pendingCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'merge_execute'
		  AND status = 'pending'
		  AND payload->>'merge_queue_entry_id' = $1
	`, entry.ID.String()).Scan(&pendingCount); err != nil {
		t.Fatalf("count pending merge jobs: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("pending merge_execute jobs = %d, want 1", pendingCount)
	}
}

func mustTime(t *testing.T) (now time.Time) {
	t.Helper()
	return time.Unix(1700000000, 0).UTC()
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	return &id
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
