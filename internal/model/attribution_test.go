package model

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestAttributionMiddlewarePopulateWithRunAttribution(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	projectID := uuid.New()
	projectTaskID := uuid.New()
	sessionID := uuid.New()
	turnID := uuid.New()
	runID := uuid.New()
	runStepID := uuid.New()
	runAttemptID := uuid.New()

	ctx := WithInvocationContext(context.Background(), InvocationContext{
		OrganizationID:    orgID,
		AgentID:           &agentID,
		ProjectID:         &projectID,
		ProjectTaskID:     &projectTaskID,
		SessionID:         &sessionID,
		TurnID:            &turnID,
		RunID:             &runID,
		RunStepID:         &runStepID,
		RunAttemptID:      &runAttemptID,
		InvocationPurpose: "agent_turn",
	})

	got := NewAttributionMiddleware().Populate(ctx)

	if got.OrganizationID != orgID {
		t.Fatalf("organization_id = %v, want %v", got.OrganizationID, orgID)
	}
	if got.AgentID == nil || *got.AgentID != agentID {
		t.Fatalf("agent_id = %v, want %v", got.AgentID, agentID)
	}
	if got.ProjectID == nil || *got.ProjectID != projectID {
		t.Fatalf("project_id = %v, want %v", got.ProjectID, projectID)
	}
	if got.ProjectTaskID == nil || *got.ProjectTaskID != projectTaskID {
		t.Fatalf("project_task_id = %v, want %v", got.ProjectTaskID, projectTaskID)
	}
	if got.SessionID == nil || *got.SessionID != sessionID {
		t.Fatalf("session_id = %v, want %v", got.SessionID, sessionID)
	}
	if got.TurnID == nil || *got.TurnID != turnID {
		t.Fatalf("turn_id = %v, want %v", got.TurnID, turnID)
	}
	if got.RunID == nil || *got.RunID != runID {
		t.Fatalf("run_id = %v, want %v", got.RunID, runID)
	}
	if got.RunStepID == nil || *got.RunStepID != runStepID {
		t.Fatalf("run_step_id = %v, want %v", got.RunStepID, runStepID)
	}
	if got.RunAttemptID == nil || *got.RunAttemptID != runAttemptID {
		t.Fatalf("run_attempt_id = %v, want %v", got.RunAttemptID, runAttemptID)
	}
	if got.InvocationPurpose != "agent_turn" {
		t.Fatalf("invocation_purpose = %q, want agent_turn", got.InvocationPurpose)
	}
}

func TestAttributionMiddlewarePopulateEmptyContext(t *testing.T) {
	got := NewAttributionMiddleware().Populate(context.Background())

	if got.OrganizationID != uuid.Nil {
		t.Fatalf("organization_id = %v, want nil uuid", got.OrganizationID)
	}
	if got.AgentID != nil {
		t.Fatalf("agent_id = %v, want nil", got.AgentID)
	}
	if got.ProjectID != nil {
		t.Fatalf("project_id = %v, want nil", got.ProjectID)
	}
	if got.ProjectTaskID != nil {
		t.Fatalf("project_task_id = %v, want nil", got.ProjectTaskID)
	}
	if got.SessionID != nil {
		t.Fatalf("session_id = %v, want nil", got.SessionID)
	}
	if got.TurnID != nil {
		t.Fatalf("turn_id = %v, want nil", got.TurnID)
	}
	if got.RunID != nil {
		t.Fatalf("run_id = %v, want nil", got.RunID)
	}
	if got.RunStepID != nil {
		t.Fatalf("run_step_id = %v, want nil", got.RunStepID)
	}
	if got.RunAttemptID != nil {
		t.Fatalf("run_attempt_id = %v, want nil", got.RunAttemptID)
	}
	if got.InvocationPurpose != "" {
		t.Fatalf("invocation_purpose = %q, want empty", got.InvocationPurpose)
	}
}

func TestNormalizeInvocationPurposeDefaultsToAgentTurn(t *testing.T) {
	if got := normalizeInvocationPurpose(""); got != DefaultInvocationPurpose {
		t.Fatalf("normalizeInvocationPurpose(empty) = %q, want %q", got, DefaultInvocationPurpose)
	}
	if got := normalizeInvocationPurpose(" memory_extraction "); got != "memory_extraction" {
		t.Fatalf("normalizeInvocationPurpose = %q, want memory_extraction", got)
	}
}
