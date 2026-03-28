//go:build integration

package flow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestFlowExecutionServiceStartFlowWithDefaultSingleAgentTemplate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	bootstrapper := bootstrap.NewBootstrapper(bootstrap.Options{
		Pool:      pool,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		SkillsDir: t.TempDir(),
		OrgSlug:   "default",
		OrgName:   "OtterCamp",
		Version:   "test-version",
	})
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, err := fixture.templateRepo.GetCurrentBySlug(ctx, nil, nil, "default-single-agent")
	if err != nil {
		t.Fatalf("GetCurrentBySlug default-single-agent: %v", err)
	}
	if template.StartNodeID == nil {
		t.Fatal("default-single-agent start_node_id is nil")
	}

	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task (default template)", "in_progress", &template.ID)

	started, err := fixture.service.StartFlow(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if started.FlowNodeID != *template.StartNodeID {
		t.Fatalf("start flow_node_id = %s, want %s", started.FlowNodeID, *template.StartNodeID)
	}

	var executionCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_node_execution
		WHERE task_id = $1
	`, taskRecord.ID).Scan(&executionCount); err != nil {
		t.Fatalf("count flow_node_execution rows: %v", err)
	}
	if executionCount != 1 {
		t.Fatalf("flow_node_execution count = %d, want 1", executionCount)
	}
}

func TestFlowExecutionServiceAdvanceThroughTerminalNode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task", "in_progress", &template.ID)

	started, err := fixture.service.StartFlow(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if started.VisitNumber != 1 {
		t.Fatalf("start visit_number = %d, want 1", started.VisitNumber)
	}

	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 1: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 2: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow terminal step: %v", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "done" {
		t.Fatalf("task work_status = %q, want done", updatedTask.WorkStatus)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 3 {
		t.Fatalf("execution row count = %d, want 3", len(executions))
	}
	for i, execution := range executions {
		if execution.Status != "completed" {
			t.Fatalf("execution[%d] status = %q, want completed", i, execution.Status)
		}
	}

	if nodes[0].ID != executions[0].FlowNodeID || nodes[1].ID != executions[1].FlowNodeID || nodes[2].ID != executions[2].FlowNodeID {
		t.Fatalf("unexpected flow node execution order")
	}
}

func TestFlowExecutionServiceTerminalAdvanceEnqueuesMergeForBranchedTask(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task with merge branch", "in_progress", &template.ID)
	branch := "task/" + uuid.NewString()[:8]
	if _, err := fixture.taskRepo.SetBranch(ctx, taskRecord.ID, &branch); err != nil {
		t.Fatalf("SetBranch: %v", err)
	}

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 1: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 2: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow terminal step: %v", err)
	}

	entry, err := repo.NewMergeQueueEntryRepo(pool).GetByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByTask merge entry: %v", err)
	}
	if entry.Status != "queued" {
		t.Fatalf("merge entry status = %q, want queued", entry.Status)
	}
	if entry.BranchName != branch {
		t.Fatalf("merge entry branch_name = %q, want %q", entry.BranchName, branch)
	}

	var jobCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'merge_execute'
		  AND payload->>'merge_queue_entry_id' = $1
		  AND payload->>'project_id' = $2
	`, entry.ID.String(), fixture.project.ID.String()).Scan(&jobCount); err != nil {
		t.Fatalf("count merge_execute jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("merge_execute jobs = %d, want 1", jobCount)
	}
}

func TestFlowExecutionServiceTerminalAdvanceActivatesDraftOrchestrationParent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)

	parent := seedFlowTask(t, ctx, fixture, "OC-1: Validate Speaker Pipeline Routing & Assignment", "draft", &template.ID)
	reportTitle := "Produce final validation report with pass/fail determination, risk summary, and recommendations"
	reportDescription := reportTitle
	report := seedFlowTask(t, ctx, fixture, reportTitle, "draft", &template.ID)
	report.Description = &reportDescription
	report.Metadata = taskdecomp.ApplyChildMetadata(report.Metadata, parent.ID, 2)
	if _, err := fixture.taskRepo.Update(ctx, report); err != nil {
		t.Fatalf("update report child metadata: %v", err)
	}

	childDescription := "Execute Test Scenario 1 (Happy Path)"
	child := seedFlowTask(t, ctx, fixture, "OC-2: Execute Test Scenario 1 (Happy Path)", "in_progress", &template.ID)
	child.Description = &childDescription
	child.Metadata = taskdecomp.ApplyChildMetadata(child.Metadata, parent.ID, 3)
	if _, err := fixture.taskRepo.Update(ctx, child); err != nil {
		t.Fatalf("update active child metadata: %v", err)
	}

	parent.Metadata = taskdecomp.ApplyMetadata(parent.Metadata, taskdecomp.Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    "Coordinate all test scenarios, SLA validation, and final consolidation.",
		Deliverables:          []string{reportTitle, childDescription},
	}, "Coordinate all test scenarios, SLA validation, and final consolidation.", []uuid.UUID{report.ID, child.ID})
	if _, err := fixture.taskRepo.Update(ctx, parent); err != nil {
		t.Fatalf("update parent metadata: %v", err)
	}

	if _, err := fixture.service.StartFlow(ctx, child.ID); err != nil {
		t.Fatalf("StartFlow child: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, child.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow child to review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, child.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow child review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, child.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow child terminal: %v", err)
	}

	updatedParent, err := fixture.taskRepo.GetByID(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetByID parent: %v", err)
	}
	if updatedParent.WorkStatus != "draft" {
		t.Fatalf("parent work_status = %q, want draft orchestration-only state after activation", updatedParent.WorkStatus)
	}

	updatedReport, err := fixture.taskRepo.GetByID(ctx, report.ID)
	if err != nil {
		t.Fatalf("GetByID report child: %v", err)
	}
	if updatedReport.WorkStatus != "queued" {
		t.Fatalf("report child work_status = %q, want queued after parent activation", updatedReport.WorkStatus)
	}
}

func TestFlowExecutionServiceTerminalAdvanceActivatesSatisfiedDraftTaskDependents(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)

	prereqA := seedFlowTask(t, ctx, fixture, "Scenario Design A", "in_progress", &template.ID)
	prereqB := seedFlowTask(t, ctx, fixture, "Scenario Design B", "in_progress", &template.ID)
	dependent := seedFlowTask(t, ctx, fixture, "Execute Validation", "draft", &template.ID)

	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      dependent.ID,
		DependsOnType: "project_task",
		DependsOnID:   prereqA.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency dependent->prereqA: %v", err)
	}
	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      dependent.ID,
		DependsOnType: "project_task",
		DependsOnID:   prereqB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency dependent->prereqB: %v", err)
	}

	if _, err := fixture.service.StartFlow(ctx, prereqA.ID); err != nil {
		t.Fatalf("StartFlow prereqA: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, prereqA.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow prereqA to review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, prereqA.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow prereqA review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, prereqA.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow prereqA terminal: %v", err)
	}

	stillDraft, err := fixture.taskRepo.GetByID(ctx, dependent.ID)
	if err != nil {
		t.Fatalf("GetByID dependent after prereqA: %v", err)
	}
	if stillDraft.WorkStatus != "draft" {
		t.Fatalf("dependent work_status after first prerequisite = %q, want draft", stillDraft.WorkStatus)
	}

	if _, err := fixture.service.StartFlow(ctx, prereqB.ID); err != nil {
		t.Fatalf("StartFlow prereqB: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, prereqB.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow prereqB to review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, prereqB.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow prereqB review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, prereqB.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow prereqB terminal: %v", err)
	}

	activated, err := fixture.taskRepo.GetByID(ctx, dependent.ID)
	if err != nil {
		t.Fatalf("GetByID dependent after prereqB: %v", err)
	}
	if activated.WorkStatus != "queued" {
		t.Fatalf("dependent work_status after all prerequisites = %q, want queued", activated.WorkStatus)
	}
}

func TestFlowExecutionServiceRejectsSelfReview(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Self review task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); !errors.Is(err, ErrSelfReviewForbidden) {
		t.Fatalf("AdvanceFlow self-review err = %v, want ErrSelfReviewForbidden", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.CurrentFlowNodeID == nil {
		t.Fatal("current_flow_node_id = nil, want active review node")
	}
	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("execution row count = %d, want 2", len(executions))
	}
	if executions[1].Status != "active" {
		t.Fatalf("review execution status = %q, want active", executions[1].Status)
	}
	if executions[1].RuntimeSubstate == nil || *executions[1].RuntimeSubstate != "waiting_for_review" {
		t.Fatalf("review execution runtime_substate = %v, want waiting_for_review", executions[1].RuntimeSubstate)
	}
}

func TestFlowExecutionServiceAllowsHumanReviewAfterManualHumanAdvance(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Human review task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review with human actor: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID}); err != nil {
		t.Fatalf("AdvanceFlow human review err = %v, want nil", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID}); err != nil {
		t.Fatalf("AdvanceFlow human terminal err = %v, want nil", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "done" {
		t.Fatalf("task work_status = %q, want done", updatedTask.WorkStatus)
	}
}

func TestFlowExecutionServiceRejectsPlanningReviewWhenArtifactsRemainScaffolds(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Planning scaffold review guard", "in_progress", &template.ID)
	taskRecord.Metadata = json.RawMessage(`{
		"planning": {
			"mode": "execution_first",
			"playbook": "execution_spec",
			"work_type": "execution_spec",
			"project_stage": "delivery",
			"evidence_maturity": "validated",
			"risk_level": "low",
			"process_enforced": true,
			"artifacts": [
				{"slug":"prd","title":"PRD / requirements spec","kind":"prd_spec","repo_path":"planning/prd-spec/oc-99-prd.md"},
				{"slug":"implementation-plan","title":"Implementation plan","kind":"prd_spec","repo_path":"planning/prd-spec/oc-99-implementation-plan.md"},
				{"slug":"acceptance-criteria","title":"Acceptance criteria","kind":"prd_spec","repo_path":"planning/prd-spec/oc-99-acceptance-criteria.md"},
				{"slug":"dependency-log","title":"Dependency log","kind":"prd_spec","repo_path":"planning/prd-spec/oc-99-dependency-log.md"}
			]
		}
	}`)
	if _, err := fixture.taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task metadata: %v", err)
	}

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	_, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID})
	var contractErr taskplan.ContractError
	if !errors.As(err, &contractErr) {
		t.Fatalf("AdvanceFlow err = %v, want taskplan.ContractError", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[0].ID {
		t.Fatalf("current_flow_node_id = %v, want work node %s", updatedTask.CurrentFlowNodeID, nodes[0].ID)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("execution row count = %d, want 1", len(executions))
	}
	if executions[0].Status != "active" || executions[0].FlowNodeID != nodes[0].ID {
		t.Fatalf("execution[0] = %+v, want active work execution", executions[0])
	}
}

func TestFlowExecutionServiceAdvanceFlowBackfillsMissingPlanningEvidenceWhenPartialEvidenceExists(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Validation report synthesis", "in_progress", &template.ID)
	taskRecord.Metadata = json.RawMessage(`{
		"planning": {
			"mode": "execution_first",
			"playbook": "discovery",
			"work_type": "generic",
			"project_stage": "validation",
			"evidence_maturity": "unknown",
			"risk_level": "low",
			"discovery_mode": "new_product",
			"process_enforced": true,
			"artifacts": [
				{"slug":"problem-brief","title":"Problem brief","kind":"discovery_plan","repo_path":"planning/discovery-plan/oc-99-problem-brief.md","artifact_id":"a1","content_sha256":"sha1"},
				{"slug":"research-plan","title":"Research plan","kind":"discovery_plan","repo_path":"planning/discovery-plan/oc-99-research-plan.md","artifact_id":"a2","content_sha256":"sha2"},
				{"slug":"assumption-log","title":"Assumption log","kind":"discovery_plan","repo_path":"planning/discovery-plan/oc-99-assumption-log.md","artifact_id":"a3","content_sha256":"sha3"},
				{"slug":"validation-plan","title":"Validation plan","kind":"discovery_plan","repo_path":"planning/discovery-plan/oc-99-validation-plan.md","artifact_id":"a4","content_sha256":"sha4"}
			],
			"artifact_evidence": [
				{"slug":"validation-report","title":"Validation report","summary":"Extra non-contract artifact evidence","sections":["Executive Summary"],"asset_refs":["deliverables/oc-99-validation-report.md"]}
			]
		}
	}`)
	if _, err := fixture.taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task metadata: %v", err)
	}

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("current_flow_node_id = %v, want review node %s", updatedTask.CurrentFlowNodeID, nodes[1].ID)
	}

	plan, ok := taskplan.Parse(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected planning metadata")
	}
	if len(plan.ArtifactEvidence) != 5 {
		t.Fatalf("artifact evidence len = %d, want 5", len(plan.ArtifactEvidence))
	}
	for _, slug := range []string{"problem-brief", "research-plan", "assumption-log", "validation-plan"} {
		found := false
		for _, evidence := range plan.ArtifactEvidence {
			if strings.EqualFold(strings.TrimSpace(evidence.Slug), slug) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing synthesized artifact evidence for %s", slug)
		}
	}
}

func TestFlowExecutionServiceTerminalAdvanceSynthesizesPlanningArtifactEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Metrics reporting script", "in_progress", &template.ID)
	taskRecord.Metadata = json.RawMessage(`{
		"planning": {
			"mode": "execution_first",
			"playbook": "metrics",
			"work_type": "metrics",
			"project_stage": "delivery",
			"evidence_maturity": "validated",
			"risk_level": "low",
			"process_enforced": true,
			"artifacts": [
				{"slug":"metric-tree","title":"Metric tree","kind":"metrics_framework","repo_path":"planning/metrics-framework/oc-99-metric-tree.md","artifact_id":"a1","content_sha256":"sha1"},
				{"slug":"instrumentation-plan","title":"Instrumentation plan","kind":"metrics_framework","repo_path":"planning/metrics-framework/oc-99-instrumentation-plan.md","artifact_id":"a2","content_sha256":"sha2"},
				{"slug":"dashboard-spec","title":"Dashboard spec","kind":"metrics_framework","repo_path":"planning/metrics-framework/oc-99-dashboard-spec.md","artifact_id":"a3","content_sha256":"sha3"},
				{"slug":"review-cadence","title":"Metric review cadence","kind":"metrics_framework","repo_path":"planning/metrics-framework/oc-99-review-cadence.md","artifact_id":"a4","content_sha256":"sha4"}
			]
		}
	}`)
	if _, err := fixture.taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task metadata: %v", err)
	}

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow terminal: %v", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "done" {
		t.Fatalf("task work_status = %q, want done", updatedTask.WorkStatus)
	}
	plan, ok := taskplan.Parse(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected planning metadata")
	}
	if len(plan.ArtifactEvidence) != 4 {
		t.Fatalf("artifact evidence len = %d, want 4", len(plan.ArtifactEvidence))
	}
}

func TestFlowExecutionServiceTerminalAdvanceRequiresCompletedReview(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, err := fixture.templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &fixture.organization.ID,
		ProjectID:      &fixture.project.ID,
		Slug:           "work-only-" + uuid.NewString()[:8],
		DisplayName:    "Work Only",
		Description:    "missing internal review",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	workNode, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	template.StartNodeID = &workNode.ID
	if _, err := fixture.templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("update template start node: %v", err)
	}

	taskRecord := seedFlowTask(t, ctx, fixture, "Terminal review required", "in_progress", &template.ID)
	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); !errors.Is(err, tasksvc.ErrDoneRequiresTerminalFlow) {
		t.Fatalf("AdvanceFlow terminal err = %v, want ErrDoneRequiresTerminalFlow", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != workNode.ID {
		t.Fatalf("current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, workNode.ID)
	}
	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("execution row count = %d, want 1", len(executions))
	}
	if executions[0].Status != "active" {
		t.Fatalf("execution status = %q, want active", executions[0].Status)
	}
}

func TestFlowExecutionServiceAdvanceFlowBackfillsMissingExecutionFromTemplate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task missing execution", "in_progress", &template.ID)

	advanced, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID})
	if err != nil {
		t.Fatalf("AdvanceFlow: %v", err)
	}
	if advanced.FlowNodeID != nodes[1].ID {
		t.Fatalf("advanced flow_node_id = %s, want %s", advanced.FlowNodeID, nodes[1].ID)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, nodes[1].ID)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("execution row count = %d, want 2", len(executions))
	}
	if executions[0].FlowNodeID != nodes[0].ID || executions[0].Status != "completed" {
		t.Fatalf("execution[0] = %+v, want node=%s status=completed", executions[0], nodes[0].ID)
	}
	if executions[1].FlowNodeID != nodes[1].ID || executions[1].Status != "active" {
		t.Fatalf("execution[1] = %+v, want node=%s status=active", executions[1], nodes[1].ID)
	}
}

func TestFlowExecutionServiceAdvanceFlowBackfillsMissingExecutionForCurrentNode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task missing active row", "in_progress", &template.ID)
	if _, err := fixture.taskRepo.SetFlowNode(ctx, taskRecord.ID, &nodes[0].ID); err != nil {
		t.Fatalf("SetFlowNode: %v", err)
	}

	advanced, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID})
	if err != nil {
		t.Fatalf("AdvanceFlow: %v", err)
	}
	if advanced.FlowNodeID != nodes[1].ID {
		t.Fatalf("advanced flow_node_id = %s, want %s", advanced.FlowNodeID, nodes[1].ID)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("execution row count = %d, want 2", len(executions))
	}
	if executions[0].FlowNodeID != nodes[0].ID || executions[0].Status != "completed" {
		t.Fatalf("execution[0] = %+v, want node=%s status=completed", executions[0], nodes[0].ID)
	}
	if executions[1].FlowNodeID != nodes[1].ID || executions[1].Status != "active" {
		t.Fatalf("execution[1] = %+v, want node=%s status=active", executions[1], nodes[1].ID)
	}
}

func TestFlowExecutionServiceRejectionLoopVisitIncrements(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, true, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Rejection loop task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review node: %v", err)
	}

	firstReject, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode first: %v", err)
	}
	if firstReject.VisitNumber != 2 {
		t.Fatalf("first rejection visit_number = %d, want 2", firstReject.VisitNumber)
	}
	rejectedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after first rejection: %v", err)
	}
	if rejectedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status after rejection = %q, want in_progress", rejectedTask.WorkStatus)
	}

	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow back to review: %v", err)
	}
	secondReject, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode second: %v", err)
	}
	if secondReject.VisitNumber != 3 {
		t.Fatalf("second rejection visit_number = %d, want 3", secondReject.VisitNumber)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	visitCount := 0
	for _, execution := range executions {
		if execution.FlowNodeID == nodes[0].ID {
			visitCount++
		}
	}
	if visitCount != 3 {
		t.Fatalf("reject target visit count = %d, want 3", visitCount)
	}
}

func TestFlowExecutionServiceRejectFlowNodeKeepsOrchestrationParentDraft(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, true, 5)
	parent := seedFlowTask(t, ctx, fixture, "OC-11: Workstream C: Wave Gating Validation", "review", &template.ID)
	parentDescription := "Parent/orchestration task for wave gating validation. Validates direct child tasks. Does not do execution work itself. Deliverable: Work/OC-11-WORKSTREAM-C-WAVE-GATING-VALIDATION.md"
	parent.Description = &parentDescription

	childDescription := "Produce wave gating validation summary"
	child := seedFlowTask(t, ctx, fixture, "OC-13: Produce wave gating validation summary", "in_progress", &template.ID)
	child.Description = &childDescription
	child.Metadata = taskdecomp.ApplyChildMetadata(child.Metadata, parent.ID, 2)
	if _, err := fixture.taskRepo.Update(ctx, child); err != nil {
		t.Fatalf("update child metadata: %v", err)
	}

	parent.Metadata = taskdecomp.ApplyMetadata(parent.Metadata, taskdecomp.Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    "Coordinate wave gating validation across direct child tasks.",
		Deliverables:          []string{childDescription},
	}, "Coordinate wave gating validation across direct child tasks.", []uuid.UUID{child.ID})
	if _, err := fixture.taskRepo.Update(ctx, parent); err != nil {
		t.Fatalf("update parent metadata: %v", err)
	}
	if _, err := fixture.taskRepo.SetFlowNode(ctx, parent.ID, &nodes[1].ID); err != nil {
		t.Fatalf("set parent current flow node: %v", err)
	}

	if _, err := fixture.executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      parent.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("seed completed parent work execution: %v", err)
	}
	if _, err := fixture.executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      parent.ID,
		FlowNodeID:  nodes[1].ID,
		VisitNumber: 1,
		Status:      "active",
	}); err != nil {
		t.Fatalf("seed active parent review execution: %v", err)
	}

	rejected, err := fixture.service.RejectFlowNode(ctx, parent.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode orchestration parent: %v", err)
	}
	if rejected.FlowNodeID != nodes[0].ID {
		t.Fatalf("rejected flow_node_id = %s, want work node %s", rejected.FlowNodeID, nodes[0].ID)
	}
	if rejected.VisitNumber != 2 {
		t.Fatalf("rejected visit_number = %d, want 2", rejected.VisitNumber)
	}

	updatedParent, err := fixture.taskRepo.GetByID(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetByID parent after rejection: %v", err)
	}
	if updatedParent.WorkStatus != "draft" {
		t.Fatalf("parent work_status after rejection = %q, want draft orchestration-only state", updatedParent.WorkStatus)
	}
	if updatedParent.CurrentFlowNodeID == nil || *updatedParent.CurrentFlowNodeID != nodes[0].ID {
		t.Fatalf("parent current_flow_node_id = %v, want work node %s", updatedParent.CurrentFlowNodeID, nodes[0].ID)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ListByTask parent executions: %v", err)
	}
	if len(executions) != 3 {
		t.Fatalf("execution row count = %d, want 3", len(executions))
	}
	if executions[2].FlowNodeID != nodes[0].ID || executions[2].Status != "active" {
		t.Fatalf("execution[2] = %+v, want active work-node execution", executions[2])
	}
}

func TestFlowExecutionServiceRejectFlowNodeMaxVisits(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, true, 2)
	taskRecord := seedFlowTask(t, ctx, fixture, "Max visits task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}
	if _, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID}); err != nil {
		t.Fatalf("RejectFlowNode first: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review again: %v", err)
	}

	rejected, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode max visits err = %v, want nil", err)
	}
	if rejected == nil {
		t.Fatal("RejectFlowNode returned nil execution")
	}
	if rejected.Status != "rejected" {
		t.Fatalf("returned execution status = %q, want rejected", rejected.Status)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("task current_flow_node_id = %v, want review node %s", updatedTask.CurrentFlowNodeID, nodes[1].ID)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	visitsToRejectTarget := 0
	for _, execution := range executions {
		if execution.VisitNumber >= 3 {
			visitsToRejectTarget++
		}
	}
	if visitsToRejectTarget != 0 {
		t.Fatalf("unexpected execution created beyond max visits")
	}
}

func TestFlowExecutionServiceRejectFlowNodeFallsBackToPreviousOrderedNodeWhenReviewPathIsImplicit(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Implicit rejection loop task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}

	rejected, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode: %v", err)
	}
	if rejected.FlowNodeID != nodes[0].ID {
		t.Fatalf("rejected flow_node_id = %s, want %s", rejected.FlowNodeID, nodes[0].ID)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[0].ID {
		t.Fatalf("task current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, nodes[0].ID)
	}
}

func TestFlowExecutionServiceDependencyCycleDetection(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskA := seedFlowTask(t, ctx, fixture, "Task A", "in_progress", &template.ID)
	taskB := seedFlowTask(t, ctx, fixture, "Task B", "in_progress", &template.ID)

	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency A->B: %v", err)
	}

	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskB.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskA.ID,
		CreatedByType: "system",
	}); err == nil || err != ErrCyclicDependency {
		t.Fatalf("AddDependency B->A err = %v, want ErrCyclicDependency", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_task_dependency`).Scan(&count); err != nil {
		t.Fatalf("count dependencies: %v", err)
	}
	if count != 1 {
		t.Fatalf("dependency row count = %d, want 1", count)
	}
}

func TestFlowExecutionServiceAddDependencyIsIdempotentForDuplicateEdge(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskA := seedFlowTask(t, ctx, fixture, "Task A", "in_progress", &template.ID)
	taskB := seedFlowTask(t, ctx, fixture, "Task B", "in_progress", &template.ID)

	first, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("AddDependency first A->B: %v", err)
	}

	second, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("AddDependency duplicate A->B: %v", err)
	}
	if second == nil || first == nil {
		t.Fatal("expected dependency records from idempotent AddDependency")
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate dependency id = %s, want existing %s", second.ID, first.ID)
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task_dependency
		WHERE source_type = 'project_task'
		  AND source_id = $1
		  AND depends_on_type = 'project_task'
		  AND depends_on_id = $2
	`, taskA.ID, taskB.ID).Scan(&count); err != nil {
		t.Fatalf("count duplicate dependency edge: %v", err)
	}
	if count != 1 {
		t.Fatalf("duplicate dependency edge count = %d, want 1", count)
	}
}

func TestFlowExecutionServiceOnTaskCancelledMarksDependentsBlocked(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskA := seedFlowTask(t, ctx, fixture, "Task A", "in_progress", &template.ID)
	taskB := seedFlowTask(t, ctx, fixture, "Task B", "in_progress", &template.ID)

	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency A->B: %v", err)
	}

	if _, err := fixture.taskRepo.UpdateStatus(ctx, taskB.ID, "cancelled"); err != nil {
		t.Fatalf("UpdateStatus task B cancelled: %v", err)
	}
	if err := fixture.service.OnTaskCancelled(ctx, taskB.ID); err != nil {
		t.Fatalf("OnTaskCancelled: %v", err)
	}

	updatedA, err := fixture.taskRepo.GetByID(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("GetByID task A: %v", err)
	}
	if updatedA.WorkStatus != "blocked" {
		t.Fatalf("task A work_status = %q, want blocked", updatedA.WorkStatus)
	}

	tasks, err := fixture.taskRepo.ListByProject(ctx, fixture.project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("project task count = %d, want 2 without auto-created blocker resolution tasks", len(tasks))
	}
}

type integrationFixture struct {
	service       FlowExecutionService
	organization  repo.Organization
	project       repo.Project
	pmUser        repo.HumanUser
	pmAgent       repo.Agent
	reviewerAgent repo.Agent
	taskRepo      *repo.ProjectTaskRepo
	templateRepo  *repo.FlowTemplateRepo
	nodeRepo      *repo.FlowNodeRepo
	executionRepo *repo.FlowNodeExecutionRepo
}

func seedFlowIntegrationFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) integrationFixture {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	executionRepo := repo.NewFlowNodeExecutionRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	assignmentRepo := repo.NewAgentProjectAssignmentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "flow-org-" + uuid.NewString()[:8],
		DisplayName: "Flow Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "flow-project-" + uuid.NewString()[:8],
		DisplayName:    "Flow Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	pmUser, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "pm+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "PM User",
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create pm user: %v", err)
	}
	pmAgent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "PM Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "pm",
		CreatedByType:        "human_user",
		CreatedByID:          pmUser.ID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create pm agent: %v", err)
	}
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        pmAgent.ID,
		ProjectID:      project.ID,
		Role:           "pm",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign pm: %v", err)
	}
	reviewerAgent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Reviewer Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "reviewer",
		CreatedByType:        "human_user",
		CreatedByID:          pmUser.ID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create reviewer agent: %v", err)
	}
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        reviewerAgent.ID,
		ProjectID:      project.ID,
		Role:           "reviewer",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign reviewer: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	svc, err := NewService(Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return integrationFixture{
		service:       svc,
		organization:  org,
		project:       project,
		pmUser:        pmUser,
		pmAgent:       pmAgent,
		reviewerAgent: reviewerAgent,
		taskRepo:      taskRepo,
		templateRepo:  templateRepo,
		nodeRepo:      nodeRepo,
		executionRepo: executionRepo,
	}
}

func seedLinearTemplate(t *testing.T, ctx context.Context, fixture integrationFixture, withReject bool, maxVisits int) (repo.FlowTemplate, []repo.FlowNode) {
	t.Helper()

	template, err := fixture.templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &fixture.organization.ID,
		ProjectID:      &fixture.project.ID,
		Slug:           "flow-template-" + uuid.NewString()[:8],
		DisplayName:    "Flow Template",
		Description:    "integration template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	node1, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Node 1",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      maxVisits,
	})
	if err != nil {
		t.Fatalf("create node1: %v", err)
	}
	node2, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Node 2",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      maxVisits,
	})
	if err != nil {
		t.Fatalf("create node2: %v", err)
	}
	node3, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      maxVisits,
	})
	if err != nil {
		t.Fatalf("create node3: %v", err)
	}

	node1.NextNodeID = &node2.ID
	if withReject {
		node2.RejectNodeID = &node1.ID
	}
	node2.NextNodeID = &node3.ID

	if _, err := fixture.nodeRepo.Update(ctx, node1); err != nil {
		t.Fatalf("update node1: %v", err)
	}
	if _, err := fixture.nodeRepo.Update(ctx, node2); err != nil {
		t.Fatalf("update node2: %v", err)
	}

	template.StartNodeID = &node1.ID
	if _, err := fixture.templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("update template start node: %v", err)
	}

	return template, []repo.FlowNode{node1, node2, node3}
}

func seedFlowTask(t *testing.T, ctx context.Context, fixture integrationFixture, title string, status string, templateID *uuid.UUID) repo.ProjectTask {
	t.Helper()
	taskRecord, err := fixture.taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: fixture.organization.ID,
		ProjectID:      fixture.project.ID,
		Title:          title,
		WorkStatus:     status,
		FlowTemplateID: templateID,
		CreatedByType:  "system",
		CreatedByID:    nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskRecord
}
