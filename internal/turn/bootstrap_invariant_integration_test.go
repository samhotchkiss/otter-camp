//go:build integration

package turn

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
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

	t.Run("draft_only_failure_archives_before_project_remains_active", func(t *testing.T) {
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
	completeBootstrapSetup bool
	livePromotion          bool
}

type bootstrapInvariantResult struct {
	bootstrapState   projectBootstrapState
	project          repo.Project
	assignmentCount  int
	totalTaskCount   int
	totalDraftCount  int
	activeExecutions int
	runnableJobs     int
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
	assignedAgents := []repo.Agent{pmAgent, workerA, workerB, workerC}

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

		roles := []string{"pm", "worker", "reviewer", "observer"}
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

		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		taskRepo := repo.NewProjectTaskRepo(fixture.pool)
		parentTasks := make([]repo.ProjectTask, 0, scenario.parentTaskCount)
		for i := 0; i < scenario.parentTaskCount; i++ {
			description := fmt.Sprintf("Coordinate executable wave %d without absorbing deliverable work.", i+1)
			assignedID := pmAgent.ID
			parentTask, err := taskRepo.Create(ctx, repo.ProjectTask{
				OrganizationID:  fixture.org.ID,
				ProjectID:       project.ID,
				Title:           fmt.Sprintf("Wave %d orchestration parent", i+1),
				Description:     &description,
				WorkStatus:      "draft",
				FlowTemplateID:  &template.ID,
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
				FlowTemplateID:  &template.ID,
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

		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"assignment_count": scenario.assignments,
				"planned_tasks":    scenario.parentTaskCount + scenario.childTaskCount,
				"flow_template_id": template.ID.String(),
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
		bootstrapState:   projectBootstrapStateFromMetadata(sessionRecord.Metadata),
		project:          projectRecord,
		assignmentCount:  len(assignments),
		totalTaskCount:   len(tasks),
		totalDraftCount:  totalDraftCount,
		activeExecutions: activeExecutions,
		runnableJobs:     countRunnableAgentTurnJobsForTasks(t, ctx, fixture.pool, firstWaveTaskIDs),
	}
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
