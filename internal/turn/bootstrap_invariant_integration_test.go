//go:build integration

package turn

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/tools"
)

func TestTurnEngineIntegrationBootstrapInvariantHarness(t *testing.T) {
	t.Run("success_promotes_first_wave_and_claims_jobs", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		enableTaskQueueProcessor(t, fixture)
		enableTurnEngineUserMessageEnqueue(t, fixture)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            4,
			parentTaskCount:        1,
			childTaskCount:         3,
			completeBootstrapSetup: true,
			livePromotion:          true,
		})

		if result.bootstrapState.Status != projectBootstrapStatusCompleted {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusCompleted)
		}
		if result.bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveJobsClaimed {
			t.Fatalf("bootstrap current_phase = %q, want %q", result.bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveJobsClaimed)
		}
		if result.bootstrapState.FirstWaveExecutionCount == 0 {
			t.Fatalf("bootstrap first_wave_execution_count = %d, want > 0", result.bootstrapState.FirstWaveExecutionCount)
		}
		if result.bootstrapState.FirstWaveJobCount == 0 {
			t.Fatalf("bootstrap first_wave_job_count = %d, want > 0", result.bootstrapState.FirstWaveJobCount)
		}
		if result.project.Status != "active" {
			t.Fatalf("project status = %q, want active", result.project.Status)
		}
		if result.runnableJobs == 0 {
			t.Fatal("expected runnable first-wave jobs")
		}
	})

	t.Run("draft_only_setup_keeps_project_non_runnable_until_gate_opens", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            4,
			parentTaskCount:        1,
			childTaskCount:         9,
			completeBootstrapSetup: false,
			livePromotion:          false,
		})

		if result.assignmentCount != 4 {
			t.Fatalf("assignment count = %d, want 4", result.assignmentCount)
		}
		if result.totalTaskCount != 18 {
			t.Fatalf("task count = %d, want 18", result.totalTaskCount)
		}
		if result.totalDraftCount != result.totalTaskCount {
			t.Fatalf("draft task count = %d, want all %d tasks draft", result.totalDraftCount, result.totalTaskCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusActive {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusActive)
		}
		if result.bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
			t.Fatalf("bootstrap current_phase = %q, want %q", result.bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
		}
		if result.project.Status != "active" {
			t.Fatalf("project status = %q, want active while bootstrap remains incomplete", result.project.Status)
		}
		if result.activeExecutions != 0 {
			t.Fatalf("active flow executions = %d, want 0", result.activeExecutions)
		}
		if result.runnableJobs != 0 {
			t.Fatalf("runnable first-wave jobs = %d, want 0", result.runnableJobs)
		}
	})

	t.Run("rebuild_v8_shape_without_gate_opening_stays_non_runnable", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            5,
			parentTaskCount:        1,
			childTaskCount:         12,
			completeBootstrapSetup: false,
			livePromotion:          false,
		})

		if result.assignmentCount != 5 {
			t.Fatalf("assignment count = %d, want 5", result.assignmentCount)
		}
		if result.totalTaskCount != 21 {
			t.Fatalf("task count = %d, want 21", result.totalTaskCount)
		}
		if result.totalDraftCount != result.totalTaskCount {
			t.Fatalf("draft task count = %d, want all %d tasks draft", result.totalDraftCount, result.totalTaskCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusActive {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusActive)
		}
		if result.bootstrapState.AssignmentCount != 5 {
			t.Fatalf("bootstrap assignment_count = %d, want 5", result.bootstrapState.AssignmentCount)
		}
		if result.bootstrapState.PlannedFlowTemplateCount != 1 {
			t.Fatalf("bootstrap planned_flow_template_count = %d, want 1", result.bootstrapState.PlannedFlowTemplateCount)
		}
		if result.bootstrapState.PlannedTaskCount != 13 {
			t.Fatalf("bootstrap planned_task_count = %d, want 13", result.bootstrapState.PlannedTaskCount)
		}
		if result.bootstrapState.FirstWaveTaskCount != 12 {
			t.Fatalf("bootstrap first_wave_task_count = %d, want 12", result.bootstrapState.FirstWaveTaskCount)
		}
		if result.bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
			t.Fatalf("bootstrap current_phase = %q, want %q", result.bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
		}
		if result.project.Status != "active" {
			t.Fatalf("project status = %q, want active while bootstrap remains incomplete", result.project.Status)
		}
		if result.activeExecutions != 0 {
			t.Fatalf("active flow executions = %d, want 0", result.activeExecutions)
		}
		if result.runnableJobs != 0 {
			t.Fatalf("runnable first-wave jobs = %d, want 0", result.runnableJobs)
		}
	})

	t.Run("samblog_rebuild_shape_promotes_first_wave_and_claims_jobs", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		enableTaskQueueProcessor(t, fixture)
		enableTurnEngineUserMessageEnqueue(t, fixture)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            4,
			topLevelTaskCount:      5,
			flowTemplateCount:      2,
			completeBootstrapSetup: true,
			livePromotion:          true,
		})

		if result.assignmentCount != 4 {
			t.Fatalf("assignment count = %d, want 4", result.assignmentCount)
		}
		if result.totalTaskCount != 13 {
			t.Fatalf("task count = %d, want 13", result.totalTaskCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusCompleted {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusCompleted)
		}
		if result.bootstrapState.PlannedTaskCount != 5 {
			t.Fatalf("bootstrap planned_task_count = %d, want 5", result.bootstrapState.PlannedTaskCount)
		}
		if result.bootstrapState.PlannedFlowTemplateCount != 2 {
			t.Fatalf("bootstrap planned_flow_template_count = %d, want 2", result.bootstrapState.PlannedFlowTemplateCount)
		}
		if result.bootstrapState.FirstWaveTaskCount != 5 {
			t.Fatalf("bootstrap first_wave_task_count = %d, want 5", result.bootstrapState.FirstWaveTaskCount)
		}
		if result.bootstrapState.FirstWaveExecutionCount == 0 {
			t.Fatalf("bootstrap first_wave_execution_count = %d, want > 0", result.bootstrapState.FirstWaveExecutionCount)
		}
		if result.bootstrapState.FirstWaveJobCount == 0 {
			t.Fatalf("bootstrap first_wave_job_count = %d, want > 0", result.bootstrapState.FirstWaveJobCount)
		}
		if result.project.Status != "active" {
			t.Fatalf("project status = %q, want active", result.project.Status)
		}
		if result.runnableJobs == 0 {
			t.Fatal("expected runnable first-wave jobs for reduced samblog rebuild shape")
		}
	})

	t.Run("samblog_rebuild_shape_without_live_promotion_fails_closed", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            4,
			topLevelTaskCount:      5,
			flowTemplateCount:      2,
			completeBootstrapSetup: true,
			livePromotion:          false,
		})

		if result.assignmentCount != 4 {
			t.Fatalf("assignment count = %d, want 4", result.assignmentCount)
		}
		if result.totalTaskCount != 13 {
			t.Fatalf("task count = %d, want 13", result.totalTaskCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusFailed {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusFailed)
		}
		if result.bootstrapState.PlannedTaskCount != 5 {
			t.Fatalf("bootstrap planned_task_count = %d, want 5", result.bootstrapState.PlannedTaskCount)
		}
		if result.bootstrapState.PlannedFlowTemplateCount != 2 {
			t.Fatalf("bootstrap planned_flow_template_count = %d, want 2", result.bootstrapState.PlannedFlowTemplateCount)
		}
		if result.bootstrapState.FirstWaveTaskCount != 5 {
			t.Fatalf("bootstrap first_wave_task_count = %d, want 5", result.bootstrapState.FirstWaveTaskCount)
		}
		if result.bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
			t.Fatalf("bootstrap current_phase = %q, want %q", result.bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
		}
		if result.project.Status != "archived" {
			t.Fatalf("project status = %q, want archived", result.project.Status)
		}
		if result.activeExecutions != 0 {
			t.Fatalf("active flow executions = %d, want 0", result.activeExecutions)
		}
		if result.runnableJobs != 0 {
			t.Fatalf("runnable first-wave jobs = %d, want 0", result.runnableJobs)
		}
	})

	t.Run("staffed_project_without_pm_fails_closed", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            4,
			topLevelTaskCount:      4,
			flowTemplateCount:      1,
			completeBootstrapSetup: true,
			livePromotion:          false,
			omitPM:                 true,
		})

		if result.bootstrapState.Status != projectBootstrapStatusFailed {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusFailed)
		}
		if result.bootstrapState.ValidationFailureClass != projectBootstrapFailureMissingPM {
			t.Fatalf("bootstrap validation_failure_class = %q, want %q", result.bootstrapState.ValidationFailureClass, projectBootstrapFailureMissingPM)
		}
		if result.bootstrapState.FailureClass != projectBootstrapFailureMissingPM {
			t.Fatalf("bootstrap failure_class = %q, want %q", result.bootstrapState.FailureClass, projectBootstrapFailureMissingPM)
		}
		if result.project.Status != "archived" {
			t.Fatalf("project status = %q, want archived", result.project.Status)
		}
		if result.runnableJobs != 0 {
			t.Fatalf("runnable first-wave jobs = %d, want 0", result.runnableJobs)
		}
	})

	t.Run("wp_rebuild_v2_restart_shape_without_live_promotion_fails_closed", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            3,
			topLevelTaskCount:      17,
			flowTemplateCount:      2,
			completeBootstrapSetup: true,
			livePromotion:          false,
		})

		if result.assignmentCount != 3 {
			t.Fatalf("assignment count = %d, want 3", result.assignmentCount)
		}
		if result.totalTaskCount != 25 {
			t.Fatalf("task count = %d, want 25", result.totalTaskCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusFailed {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusFailed)
		}
		if result.bootstrapState.PlannedFlowTemplateCount != 2 {
			t.Fatalf("bootstrap planned_flow_template_count = %d, want 2", result.bootstrapState.PlannedFlowTemplateCount)
		}
		if result.bootstrapState.FirstWaveTaskCount != 17 {
			t.Fatalf("bootstrap first_wave_task_count = %d, want 17", result.bootstrapState.FirstWaveTaskCount)
		}
		if result.bootstrapState.FirstWaveExecutionCount != 0 {
			t.Fatalf("bootstrap first_wave_execution_count = %d, want 0", result.bootstrapState.FirstWaveExecutionCount)
		}
		if result.bootstrapState.FirstWaveJobCount != 0 {
			t.Fatalf("bootstrap first_wave_job_count = %d, want 0", result.bootstrapState.FirstWaveJobCount)
		}
		if result.bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
			t.Fatalf("bootstrap current_phase = %q, want %q", result.bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
		}
		if result.project.Status != "archived" {
			t.Fatalf("project status = %q, want archived", result.project.Status)
		}
		if result.activeExecutions != 0 {
			t.Fatalf("active flow executions = %d, want 0", result.activeExecutions)
		}
		if result.runnableJobs != 0 {
			t.Fatalf("runnable first-wave jobs = %d, want 0", result.runnableJobs)
		}
	})

	t.Run("wp_staging_parent_child_shape_promotes_runnable_child_executions", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		enableTaskQueueProcessor(t, fixture)
		enableTurnEngineUserMessageEnqueue(t, fixture)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            4,
			parentTaskCount:        3,
			childTaskCount:         3,
			completeBootstrapSetup: true,
			livePromotion:          true,
		})

		if result.totalTaskCount != 14 {
			t.Fatalf("task count = %d, want 14 including bootstrap gate/setup tasks", result.totalTaskCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusCompleted {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusCompleted)
		}
		if result.bootstrapState.PlannedTaskCount != 6 {
			t.Fatalf("bootstrap planned_task_count = %d, want 6", result.bootstrapState.PlannedTaskCount)
		}
		if result.bootstrapState.FirstWaveTaskCount != 3 {
			t.Fatalf("bootstrap first_wave_task_count = %d, want 3 executable child tasks", result.bootstrapState.FirstWaveTaskCount)
		}
		if result.bootstrapState.FirstWaveExecutionCount != 3 {
			t.Fatalf("bootstrap first_wave_execution_count = %d, want 3", result.bootstrapState.FirstWaveExecutionCount)
		}
		if result.bootstrapState.FirstWaveJobCount != 3 {
			t.Fatalf("bootstrap first_wave_job_count = %d, want 3", result.bootstrapState.FirstWaveJobCount)
		}
		if result.parentDraftCount != 3 {
			t.Fatalf("parent draft count = %d, want 3 orchestration-only parents", result.parentDraftCount)
		}
		if result.childPromotedCount != 3 {
			t.Fatalf("promoted child count = %d, want 3 promoted child tasks", result.childPromotedCount)
		}
		if result.project.Status != "active" {
			t.Fatalf("project status = %q, want active", result.project.Status)
		}
	})

	t.Run("wp_staging_parent_child_shape_without_promotion_fails_and_archives", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            4,
			parentTaskCount:        3,
			childTaskCount:         3,
			completeBootstrapSetup: true,
			livePromotion:          false,
		})

		if result.totalTaskCount != 14 {
			t.Fatalf("task count = %d, want 14 including bootstrap gate/setup tasks", result.totalTaskCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusFailed {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusFailed)
		}
		if result.bootstrapState.PlannedTaskCount != 6 {
			t.Fatalf("bootstrap planned_task_count = %d, want 6", result.bootstrapState.PlannedTaskCount)
		}
		if result.bootstrapState.FirstWaveTaskCount != 3 {
			t.Fatalf("bootstrap first_wave_task_count = %d, want 3 executable child tasks", result.bootstrapState.FirstWaveTaskCount)
		}
		if result.bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
			t.Fatalf("bootstrap current_phase = %q, want %q", result.bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
		}
		if result.parentDraftCount != 3 {
			t.Fatalf("parent draft count = %d, want 3 orchestration-only parents", result.parentDraftCount)
		}
		if result.childDraftCount != 3 {
			t.Fatalf("child draft count = %d, want 3 unpromoted child tasks", result.childDraftCount)
		}
		if result.activeExecutions != 0 {
			t.Fatalf("active flow executions = %d, want 0", result.activeExecutions)
		}
		if result.runnableJobs != 0 {
			t.Fatalf("runnable first-wave jobs = %d, want 0", result.runnableJobs)
		}
		if result.project.Status != "archived" {
			t.Fatalf("project status = %q, want archived", result.project.Status)
		}
	})

	t.Run("playbook_aware_persisted_setup_without_live_promotion_fails_closed", func(t *testing.T) {
		fixture := newIntegrationFixture(t)
		ctx := context.Background()

		result := runBootstrapInvariantScenario(t, ctx, fixture, bootstrapInvariantScenario{
			assignments:            5,
			topLevelTaskCount:      5,
			flowTemplateCount:      1,
			livePromotion:          false,
			completeBootstrapSetup: true,
			planningAwareTopLevel:  true,
		})

		if result.assignmentCount != 5 {
			t.Fatalf("assignment count = %d, want 5", result.assignmentCount)
		}
		if result.totalTaskCount != 13 {
			t.Fatalf("task count = %d, want 13", result.totalTaskCount)
		}
		if result.bootstrapState.PlannedFlowTemplateCount != 1 {
			t.Fatalf("bootstrap planned_flow_template_count = %d, want 1", result.bootstrapState.PlannedFlowTemplateCount)
		}
		if result.bootstrapState.PlannedTaskCount != 5 {
			t.Fatalf("bootstrap planned_task_count = %d, want 5", result.bootstrapState.PlannedTaskCount)
		}
		if result.bootstrapState.FirstWaveTaskCount != 5 {
			t.Fatalf("bootstrap first_wave_task_count = %d, want 5", result.bootstrapState.FirstWaveTaskCount)
		}
		if result.bootstrapState.FirstWaveExecutionCount != 0 {
			t.Fatalf("bootstrap first_wave_execution_count = %d, want 0", result.bootstrapState.FirstWaveExecutionCount)
		}
		if result.bootstrapState.FirstWaveJobCount != 0 {
			t.Fatalf("bootstrap first_wave_job_count = %d, want 0", result.bootstrapState.FirstWaveJobCount)
		}
		if result.bootstrapState.Status != projectBootstrapStatusFailed {
			t.Fatalf("bootstrap status = %q, want %q", result.bootstrapState.Status, projectBootstrapStatusFailed)
		}
		if result.bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
			t.Fatalf("bootstrap current_phase = %q, want %q", result.bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
		}
		if result.project.Status != "archived" {
			t.Fatalf("project status = %q, want archived", result.project.Status)
		}
		if result.activeExecutions != 0 {
			t.Fatalf("active flow executions = %d, want 0", result.activeExecutions)
		}
		if result.runnableJobs != 0 {
			t.Fatalf("runnable first-wave jobs = %d, want 0", result.runnableJobs)
		}
	})
}

type bootstrapInvariantScenario struct {
	assignments            int
	parentTaskCount        int
	childTaskCount         int
	topLevelTaskCount      int
	flowTemplateCount      int
	completeBootstrapSetup bool
	livePromotion          bool
	planningAwareTopLevel  bool
	omitPM                 bool
}

type bootstrapInvariantResult struct {
	bootstrapState     projectBootstrapState
	project            repo.Project
	assignmentCount    int
	totalTaskCount     int
	totalDraftCount    int
	parentDraftCount   int
	childDraftCount    int
	childPromotedCount int
	activeExecutions   int
	runnableJobs       int
}

func countNonStarterAssignments(t *testing.T, ctx context.Context, pool *pgxpool.Pool, assignments []repo.AgentProjectAssignment) int {
	t.Helper()

	if len(assignments) == 0 {
		return 0
	}
	agents := repo.NewAgentRepo(pool)
	count := 0
	for _, assignment := range assignments {
		agentRecord, err := agents.GetByID(ctx, assignment.AgentID)
		if err != nil {
			t.Fatalf("GetByID assignment agent %s: %v", assignment.AgentID, err)
		}
		if agentRecord.IsStarterTrio {
			continue
		}
		count++
	}
	return count
}

func runBootstrapInvariantScenario(t *testing.T, ctx context.Context, fixture *integrationFixture, scenario bootstrapInvariantScenario) bootstrapInvariantResult {
	t.Helper()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: persist the canonical bootstrap state machine and hand first-wave work into execution.")

	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)
	workerA := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	workerB := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	workerC := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	workerD := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	workerE := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	assignedAgents := []repo.Agent{pmAgent, workerA, workerB, workerC, workerD, workerE}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}
	if !scenario.livePromotion {
		fixture.engine.taskTransitions = &fakeTaskTransitionService{}
	}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will persist bootstrap now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-invariant-persist",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}

		roles := []string{"pm", "worker", "reviewer", "observer", "worker", "specialist"}
		if scenario.omitPM {
			roles = []string{"worker", "reviewer", "observer", "worker", "specialist", "worker"}
		}
		for i := 0; i < scenario.assignments && i < len(assignedAgents); i++ {
			if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
				AgentID:        assignedAgents[i].ID,
				ProjectID:      project.ID,
				Role:           roles[i],
				AssignedByType: "agent",
				AssignedByID:   &lori.ID,
			}); err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
		}

		templateCount := scenario.flowTemplateCount
		if templateCount <= 0 {
			templateCount = 1
		}
		templates := make([]repo.FlowTemplate, 0, templateCount)
		for i := 0; i < templateCount; i++ {
			templates = append(templates, mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID))
		}
		taskRepo := repo.NewProjectTaskRepo(fixture.pool)
		parentTasks := make([]repo.ProjectTask, 0, scenario.parentTaskCount)
		for i := 0; i < scenario.parentTaskCount; i++ {
			description := fmt.Sprintf("Coordinate executable wave %d without absorbing deliverable work.", i+1)
			assignedID := pmAgent.ID
			if scenario.omitPM {
				assignedID = workerA.ID
			}
			parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
				OrganizationID:  fixture.org.ID,
				ProjectID:       project.ID,
				Title:           fmt.Sprintf("Wave %d orchestration parent", i+1),
				Description:     &description,
				WorkStatus:      "draft",
				FlowTemplateID:  &templates[i%len(templates)].ID,
				AssignedAgentID: &assignedID,
				CreatedByType:   "agent",
				CreatedByID:     &lori.ID,
			})
			if err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			parentTasks = append(parentTasks, parentTask)
		}

		for i := 0; i < scenario.childTaskCount; i++ {
			parentTask := parentTasks[i%len(parentTasks)]
			description := fmt.Sprintf("Deliver bounded first-wave slice %d.", i+1)
			assignedID := assignedAgents[(i%max(1, scenario.assignments-1))+1].ID
			if _, err := taskRepo.Create(ctx, repo.ProjectTask{
				OrganizationID:  fixture.org.ID,
				ProjectID:       project.ID,
				Title:           fmt.Sprintf("Implement first-wave slice %d", i+1),
				Description:     &description,
				WorkStatus:      "draft",
				FlowTemplateID:  &templates[i%len(templates)].ID,
				AssignedAgentID: &assignedID,
				Metadata: mustJSON(t, map[string]any{
					"decomposition_parent_task_id": parentTask.ID.String(),
					"workstream_index":             i + 1,
				}),
				CreatedByType: "agent",
				CreatedByID:   &lori.ID,
			}); err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
		}

		for i := 0; i < scenario.topLevelTaskCount; i++ {
			description := fmt.Sprintf("Deliver bounded top-level first-wave slice %d.", i+1)
			assignedID := assignedAgents[(i%max(1, scenario.assignments-1))+1].ID
			metadata := mustJSON(t, map[string]any{
				"first_wave_index": i + 1,
			})
			if scenario.planningAwareTopLevel {
				metadata = taskplan.ApplyMetadata(metadata, taskplan.Analyze(fmt.Sprintf("Implementation slice %d", i+1), &description))
			}
			if _, err := taskRepo.Create(ctx, repo.ProjectTask{
				OrganizationID:  fixture.org.ID,
				ProjectID:       project.ID,
				Title:           fmt.Sprintf("Implement top-level first-wave slice %d", i+1),
				Description:     &description,
				WorkStatus:      "draft",
				FlowTemplateID:  &templates[i%len(templates)].ID,
				AssignedAgentID: &assignedID,
				Metadata:        metadata,
				CreatedByType:   "agent",
				CreatedByID:     &lori.ID,
			}); err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
		}

		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"assignment_count":    scenario.assignments,
				"planned_tasks":       scenario.parentTaskCount + scenario.childTaskCount + scenario.topLevelTaskCount,
				"flow_template_count": len(templates),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if scenario.completeBootstrapSetup {
		signoffTask := completeBootstrapSetupTasks(t, ctx, fixture.pool, project.ID, "")
		if err := fixture.engine.HandleTaskStatusChangedEvent(ctx, eventbus.DomainEvent{
			OrganizationID: fixture.org.ID,
			EventType:      "task.status_changed",
			Payload: mustJSON(t, map[string]any{
				"task_id":    signoffTask.ID.String(),
				"project_id": project.ID.String(),
				"to_status":  "done",
			}),
		}); err != nil {
			t.Fatalf("HandleTaskStatusChangedEvent bootstrap sign-off: %v", err)
		}
	} else {
		if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
			OrganizationID: fixture.org.ID,
			EventType:      "chat.turn.completed",
			Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
		}); err != nil {
			t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
		}
	}

	assignments, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject assignments: %v", err)
	}
	tasks, err := repo.NewProjectTaskRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}

	firstWaveTaskIDs := make([]uuid.UUID, 0, len(tasks))
	totalDraftCount := 0
	parentDraftCount := 0
	childDraftCount := 0
	childPromotedCount := 0
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		if task.WorkStatus == "draft" {
			totalDraftCount++
		}
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			continue
		}
		if bootstrapSetupTask, _ := metadata["bootstrap_setup_task"].(bool); bootstrapSetupTask {
			continue
		}
		if parentID := taskdecomp.ParseParentTaskID(task.Metadata); parentID != uuid.Nil {
			if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
				childDraftCount++
			}
			if projectBootstrapTaskEnteredExecution(task.WorkStatus) {
				childPromotedCount++
			}
		} else if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
			parentDraftCount++
		}
		firstWaveTaskIDs = append(firstWaveTaskIDs, task.ID)
	}

	var activeExecutions int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_node_execution
		WHERE status = 'active'
	`).Scan(&activeExecutions); err != nil {
		t.Fatalf("count active flow executions: %v", err)
	}

	sessionRecord, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}

	projectRecord := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	return bootstrapInvariantResult{
		bootstrapState:     projectBootstrapStateFromMetadata(sessionRecord.Metadata),
		project:            projectRecord,
		assignmentCount:    countNonStarterAssignments(t, ctx, fixture.pool, assignments),
		totalTaskCount:     len(tasks),
		totalDraftCount:    totalDraftCount,
		parentDraftCount:   parentDraftCount,
		childDraftCount:    childDraftCount,
		childPromotedCount: childPromotedCount,
		activeExecutions:   activeExecutions,
		runnableJobs:       countRunnableAgentTurnJobsForTasks(t, ctx, fixture.pool, firstWaveTaskIDs),
	}
}
