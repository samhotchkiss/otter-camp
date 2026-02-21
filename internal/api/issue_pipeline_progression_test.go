package api

import (
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestIssuePipelineProgressionService(t *testing.T) {
	t.Run("AutoAdvance", func(t *testing.T) {
		db := setupMessageTestDB(t)
		orgID := insertMessageTestOrganization(t, db, "pipeline-progress-auto-org")
		projectID := insertProjectTestProject(t, db, orgID, "Pipeline Progress Auto")
		agentID := insertMessageTestAgent(t, db, orgID, "pipeline-auto-agent")

		ctx := issueTestCtx(orgID)
		issueStore := store.NewProjectIssueStore(db)
		issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
			ProjectID: projectID,
			Title:     "Auto advance issue",
			Origin:    "local",
		})
		require.NoError(t, err)

		stepStore := store.NewPipelineStepStore(db)
		stepOne, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:       projectID,
			StepNumber:      1,
			Name:            "Draft",
			AssignedAgentID: &agentID,
			StepType:        store.PipelineStepTypeAgentWork,
			AutoAdvance:     true,
		})
		require.NoError(t, err)
		stepTwo, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:       projectID,
			StepNumber:      2,
			Name:            "Review",
			AssignedAgentID: &agentID,
			StepType:        store.PipelineStepTypeAgentReview,
			AutoAdvance:     true,
		})
		require.NoError(t, err)

		startedAt := time.Date(2026, 2, 20, 6, 0, 0, 0, time.UTC)
		err = stepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
			IssueID:               issue.ID,
			CurrentPipelineStepID: &stepOne.ID,
			PipelineStartedAt:     &startedAt,
		})
		require.NoError(t, err)

		now := startedAt.Add(10 * time.Minute)
		service := &IssuePipelineProgressionService{
			PipelineStepStore: stepStore,
			IssueStore:        issueStore,
			Now: func() time.Time {
				return now
			},
		}

		firstResult, err := service.CompleteCurrentStep(ctx, issue.ID, nil, "Draft complete")
		require.NoError(t, err)
		require.NotNil(t, firstResult.CurrentPipelineStepID)
		require.Equal(t, stepTwo.ID, *firstResult.CurrentPipelineStepID)
		require.False(t, firstResult.CompletedPipeline)
		require.False(t, firstResult.ParkedForHumanReview)

		stateAfterFirst, err := stepStore.GetIssuePipelineState(ctx, issue.ID)
		require.NoError(t, err)
		require.NotNil(t, stateAfterFirst.CurrentPipelineStepID)
		require.Equal(t, stepTwo.ID, *stateAfterFirst.CurrentPipelineStepID)
		require.NotNil(t, stateAfterFirst.PipelineStartedAt)
		require.Equal(t, startedAt.UTC(), stateAfterFirst.PipelineStartedAt.UTC())
		require.Nil(t, stateAfterFirst.PipelineCompletedAt)

		historyAfterFirst, err := stepStore.ListIssuePipelineHistory(ctx, issue.ID)
		require.NoError(t, err)
		require.Len(t, historyAfterFirst, 1)
		require.Equal(t, stepOne.ID, historyAfterFirst[0].StepID)
		require.Equal(t, store.IssuePipelineResultCompleted, historyAfterFirst[0].Result)
		require.Equal(t, "Draft complete", historyAfterFirst[0].Notes)

		loadedAfterFirst, err := issueStore.GetIssueByID(ctx, issue.ID)
		require.NoError(t, err)
		require.Equal(t, store.IssueWorkStatusInProgress, loadedAfterFirst.WorkStatus)

		now = now.Add(10 * time.Minute)
		finalResult, err := service.CompleteCurrentStep(ctx, issue.ID, nil, "Review complete")
		require.NoError(t, err)
		require.Nil(t, finalResult.CurrentPipelineStepID)
		require.True(t, finalResult.CompletedPipeline)

		stateAfterFinal, err := stepStore.GetIssuePipelineState(ctx, issue.ID)
		require.NoError(t, err)
		require.Nil(t, stateAfterFinal.CurrentPipelineStepID)
		require.NotNil(t, stateAfterFinal.PipelineCompletedAt)

		historyAfterFinal, err := stepStore.ListIssuePipelineHistory(ctx, issue.ID)
		require.NoError(t, err)
		require.Len(t, historyAfterFinal, 2)
		require.Equal(t, stepTwo.ID, historyAfterFinal[1].StepID)
		require.Equal(t, store.IssuePipelineResultCompleted, historyAfterFinal[1].Result)

		loadedAfterFinal, err := issueStore.GetIssueByID(ctx, issue.ID)
		require.NoError(t, err)
		require.Equal(t, store.IssueWorkStatusDone, loadedAfterFinal.WorkStatus)
	})

	t.Run("HumanReview", func(t *testing.T) {
		db := setupMessageTestDB(t)
		orgID := insertMessageTestOrganization(t, db, "pipeline-progress-human-org")
		projectID := insertProjectTestProject(t, db, orgID, "Pipeline Progress Human")
		agentID := insertMessageTestAgent(t, db, orgID, "pipeline-human-agent")

		ctx := issueTestCtx(orgID)
		issueStore := store.NewProjectIssueStore(db)
		issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
			ProjectID: projectID,
			Title:     "Human review issue",
			Origin:    "local",
		})
		require.NoError(t, err)

		stepStore := store.NewPipelineStepStore(db)
		stepOne, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:       projectID,
			StepNumber:      1,
			Name:            "Draft",
			AssignedAgentID: &agentID,
			StepType:        store.PipelineStepTypeAgentWork,
			AutoAdvance:     true,
		})
		require.NoError(t, err)
		stepTwo, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:   projectID,
			StepNumber:  2,
			Name:        "Human Review",
			StepType:    store.PipelineStepTypeHumanReview,
			AutoAdvance: true,
		})
		require.NoError(t, err)
		_, err = stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:       projectID,
			StepNumber:      3,
			Name:            "Finalize",
			AssignedAgentID: &agentID,
			StepType:        store.PipelineStepTypeAgentReview,
			AutoAdvance:     true,
		})
		require.NoError(t, err)

		startedAt := time.Date(2026, 2, 20, 7, 0, 0, 0, time.UTC)
		err = stepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
			IssueID:               issue.ID,
			CurrentPipelineStepID: &stepOne.ID,
			PipelineStartedAt:     &startedAt,
		})
		require.NoError(t, err)

		service := &IssuePipelineProgressionService{
			PipelineStepStore: stepStore,
			IssueStore:        issueStore,
			Now: func() time.Time {
				return startedAt.Add(5 * time.Minute)
			},
		}
		result, err := service.CompleteCurrentStep(ctx, issue.ID, nil, "Ready for human review")
		require.NoError(t, err)
		require.NotNil(t, result.CurrentPipelineStepID)
		require.Equal(t, stepTwo.ID, *result.CurrentPipelineStepID)
		require.True(t, result.ParkedForHumanReview)

		stateAfter, err := stepStore.GetIssuePipelineState(ctx, issue.ID)
		require.NoError(t, err)
		require.NotNil(t, stateAfter.CurrentPipelineStepID)
		require.Equal(t, stepTwo.ID, *stateAfter.CurrentPipelineStepID)

		loadedIssue, err := issueStore.GetIssueByID(ctx, issue.ID)
		require.NoError(t, err)
		require.Equal(t, store.IssueWorkStatusReview, loadedIssue.WorkStatus)
	})

	t.Run("Reject", func(t *testing.T) {
		db := setupMessageTestDB(t)
		orgID := insertMessageTestOrganization(t, db, "pipeline-progress-reject-org")
		projectID := insertProjectTestProject(t, db, orgID, "Pipeline Progress Reject")
		agentID := insertMessageTestAgent(t, db, orgID, "pipeline-reject-agent")

		ctx := issueTestCtx(orgID)
		issueStore := store.NewProjectIssueStore(db)
		issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
			ProjectID: projectID,
			Title:     "Reject routing issue",
			Origin:    "local",
		})
		require.NoError(t, err)

		stepStore := store.NewPipelineStepStore(db)
		stepOne, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:       projectID,
			StepNumber:      1,
			Name:            "Draft",
			AssignedAgentID: &agentID,
			StepType:        store.PipelineStepTypeAgentWork,
			AutoAdvance:     true,
		})
		require.NoError(t, err)
		_, err = stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:   projectID,
			StepNumber:  2,
			Name:        "Human Review",
			StepType:    store.PipelineStepTypeHumanReview,
			AutoAdvance: true,
		})
		require.NoError(t, err)
		stepThree, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
			ProjectID:       projectID,
			StepNumber:      3,
			Name:            "Finalize",
			AssignedAgentID: &agentID,
			StepType:        store.PipelineStepTypeAgentReview,
			AutoAdvance:     true,
		})
		require.NoError(t, err)

		startedAt := time.Date(2026, 2, 20, 8, 0, 0, 0, time.UTC)
		err = stepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
			IssueID:               issue.ID,
			CurrentPipelineStepID: &stepThree.ID,
			PipelineStartedAt:     &startedAt,
		})
		require.NoError(t, err)

		now := startedAt.Add(30 * time.Minute)
		service := &IssuePipelineProgressionService{
			PipelineStepStore: stepStore,
			IssueStore:        issueStore,
			Now: func() time.Time {
				return now
			},
		}
		result, err := service.RejectCurrentStep(ctx, issue.ID, nil, "Missing acceptance criteria")
		require.NoError(t, err)
		require.NotNil(t, result.CurrentPipelineStepID)
		require.Equal(t, stepOne.ID, *result.CurrentPipelineStepID)

		stateAfter, err := stepStore.GetIssuePipelineState(ctx, issue.ID)
		require.NoError(t, err)
		require.NotNil(t, stateAfter.CurrentPipelineStepID)
		require.Equal(t, stepOne.ID, *stateAfter.CurrentPipelineStepID)
		require.Nil(t, stateAfter.PipelineCompletedAt)

		history, err := stepStore.ListIssuePipelineHistory(ctx, issue.ID)
		require.NoError(t, err)
		require.Len(t, history, 1)
		require.Equal(t, stepThree.ID, history[0].StepID)
		require.Equal(t, store.IssuePipelineResultRejected, history[0].Result)
		require.Equal(t, "Missing acceptance criteria", history[0].Notes)

		loadedIssue, err := issueStore.GetIssueByID(ctx, issue.ID)
		require.NoError(t, err)
		require.Equal(t, store.IssueWorkStatusInProgress, loadedIssue.WorkStatus)
	})
}
