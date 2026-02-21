package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

func TestHandleIssueStepDone(t *testing.T) {
	client := &fakeIssuePipelineCommandClient{
		project: ottercli.Project{ID: "project-1", Name: "Alpha"},
		issues: []ottercli.Issue{
			{ID: "issue-42", ProjectID: "project-1", IssueNumber: 42, Title: "Pipeline issue"},
		},
		stepDoneResponse: ottercli.IssuePipelineActionResponse{
			Result: ottercli.IssuePipelineProgressionResult{
				IssueID:               "issue-42",
				CompletedStepID:       "step-1",
				CurrentPipelineStepID: issuePipelineStringPtr("step-2"),
			},
			Status: ottercli.IssuePipelineStatus{
				Issue: ottercli.Issue{
					ID:          "issue-42",
					IssueNumber: 42,
					Title:       "Pipeline issue",
				},
			},
		},
	}

	var out bytes.Buffer
	err := runIssueStepDoneCommand(client, "Alpha", "42", "", "", false, &out)
	if err != nil {
		t.Fatalf("runIssueStepDoneCommand() error = %v", err)
	}
	if client.stepDoneIssueID != "issue-42" {
		t.Fatalf("IssuePipelineStepDone issue id = %q, want issue-42", client.stepDoneIssueID)
	}
	if client.stepDoneNotes != "" {
		t.Fatalf("IssuePipelineStepDone notes = %q, want empty", client.stepDoneNotes)
	}
	if !strings.Contains(out.String(), "Completed pipeline step for issue #42") {
		t.Fatalf("expected completion message, got %q", out.String())
	}

	err = runIssueStepDoneCommand(client, "Alpha", "not-a-valid-ref", "", "", false, &out)
	if err == nil || !strings.Contains(err.Error(), "issue reference must be UUID or issue number") {
		t.Fatalf("expected bad issue reference error, got %v", err)
	}
}

func TestHandleIssueStepReject(t *testing.T) {
	client := &fakeIssuePipelineCommandClient{
		project:      ottercli.Project{ID: "project-1", Name: "Alpha"},
		resolveAgent: ottercli.Agent{ID: "agent-9", Name: "Agent Nine"},
		issues: []ottercli.Issue{
			{ID: "issue-42", ProjectID: "project-1", IssueNumber: 42, Title: "Pipeline issue"},
		},
		stepRejectResponse: ottercli.IssuePipelineActionResponse{
			Result: ottercli.IssuePipelineProgressionResult{
				IssueID:               "issue-42",
				CompletedStepID:       "step-2",
				CurrentPipelineStepID: issuePipelineStringPtr("step-1"),
			},
			Status: ottercli.IssuePipelineStatus{
				Issue: ottercli.Issue{
					ID:          "issue-42",
					IssueNumber: 42,
					Title:       "Pipeline issue",
				},
			},
		},
	}

	var out bytes.Buffer
	err := runIssueStepRejectCommand(client, "Alpha", "42", "", "", false, &out)
	if err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("expected missing reason error, got %v", err)
	}

	err = runIssueStepRejectCommand(client, "Alpha", "42", "Needs more evidence", "agent-nine", false, &out)
	if err != nil {
		t.Fatalf("runIssueStepRejectCommand() error = %v", err)
	}
	if client.resolveAgentQuery != "agent-nine" {
		t.Fatalf("ResolveAgent query = %q, want agent-nine", client.resolveAgentQuery)
	}
	if client.stepRejectIssueID != "issue-42" {
		t.Fatalf("IssuePipelineStepReject issue id = %q, want issue-42", client.stepRejectIssueID)
	}
	if client.stepRejectReason != "Needs more evidence" {
		t.Fatalf("IssuePipelineStepReject reason = %q, want Needs more evidence", client.stepRejectReason)
	}
	if client.stepRejectAgentID == nil || *client.stepRejectAgentID != "agent-9" {
		t.Fatalf("IssuePipelineStepReject agent id = %#v, want agent-9", client.stepRejectAgentID)
	}
}

func TestHandleIssuePipelineStatus(t *testing.T) {
	client := &fakeIssuePipelineCommandClient{
		project: ottercli.Project{ID: "project-1", Name: "Alpha"},
		issues: []ottercli.Issue{
			{ID: "issue-42", ProjectID: "project-1", IssueNumber: 42, Title: "Pipeline issue"},
		},
		statusResponse: ottercli.IssuePipelineStatus{
			Issue: ottercli.Issue{
				ID:          "issue-42",
				IssueNumber: 42,
				Title:       "Pipeline issue",
			},
			Pipeline: ottercli.IssuePipelineStatusPayload{
				CurrentStepID:       issuePipelineStringPtr("step-2"),
				PipelineStartedAt:   issuePipelineStringPtr("2026-02-20T08:00:00Z"),
				PipelineCompletedAt: nil,
				CurrentStep: &ottercli.PipelineStep{
					ID:         "step-2",
					StepNumber: 2,
					Name:       "Review",
					StepType:   "human_review",
				},
				History: []ottercli.IssuePipelineHistoryEntry{
					{ID: "hist-1", StepID: "step-1", Result: "completed"},
				},
			},
		},
	}

	var out bytes.Buffer
	err := runIssuePipelineStatusCommand(client, "Alpha", "42", false, &out)
	if err != nil {
		t.Fatalf("runIssuePipelineStatusCommand() error = %v", err)
	}
	if client.statusIssueID != "issue-42" {
		t.Fatalf("GetIssuePipelineStatus issue id = %q, want issue-42", client.statusIssueID)
	}
	output := out.String()
	if !strings.Contains(output, "Issue #42: Pipeline issue") {
		t.Fatalf("expected issue heading in output, got %q", output)
	}
	if !strings.Contains(output, "Current step: 2. Review [human_review]") {
		t.Fatalf("expected current step in output, got %q", output)
	}
	if !strings.Contains(output, "History entries: 1") {
		t.Fatalf("expected history count in output, got %q", output)
	}
}

type fakeIssuePipelineCommandClient struct {
	project        ottercli.Project
	projectErr     error
	findProjectArg string

	issues        []ottercli.Issue
	issuesErr     error
	listProjectID string
	listFilters   map[string]string

	resolveAgent      ottercli.Agent
	resolveAgentErr   error
	resolveAgentQuery string

	stepDoneResponse ottercli.IssuePipelineActionResponse
	stepDoneErr      error
	stepDoneIssueID  string
	stepDoneAgentID  *string
	stepDoneNotes    string

	stepRejectResponse ottercli.IssuePipelineActionResponse
	stepRejectErr      error
	stepRejectIssueID  string
	stepRejectAgentID  *string
	stepRejectReason   string

	statusResponse ottercli.IssuePipelineStatus
	statusErr      error
	statusIssueID  string
}

func (f *fakeIssuePipelineCommandClient) FindProject(query string) (ottercli.Project, error) {
	f.findProjectArg = query
	if f.projectErr != nil {
		return ottercli.Project{}, f.projectErr
	}
	return f.project, nil
}

func (f *fakeIssuePipelineCommandClient) ListIssues(projectID string, filters map[string]string) ([]ottercli.Issue, error) {
	f.listProjectID = projectID
	f.listFilters = filters
	if f.issuesErr != nil {
		return nil, f.issuesErr
	}
	return f.issues, nil
}

func (f *fakeIssuePipelineCommandClient) ResolveAgent(query string) (ottercli.Agent, error) {
	f.resolveAgentQuery = query
	if f.resolveAgentErr != nil {
		return ottercli.Agent{}, f.resolveAgentErr
	}
	if strings.TrimSpace(f.resolveAgent.ID) == "" {
		return ottercli.Agent{}, errors.New("agent not found")
	}
	return f.resolveAgent, nil
}

func (f *fakeIssuePipelineCommandClient) IssuePipelineStepDone(issueID string, agentID *string, notes string) (ottercli.IssuePipelineActionResponse, error) {
	f.stepDoneIssueID = issueID
	f.stepDoneAgentID = agentID
	f.stepDoneNotes = notes
	if f.stepDoneErr != nil {
		return ottercli.IssuePipelineActionResponse{}, f.stepDoneErr
	}
	return f.stepDoneResponse, nil
}

func (f *fakeIssuePipelineCommandClient) IssuePipelineStepReject(issueID string, agentID *string, reason string) (ottercli.IssuePipelineActionResponse, error) {
	f.stepRejectIssueID = issueID
	f.stepRejectAgentID = agentID
	f.stepRejectReason = reason
	if f.stepRejectErr != nil {
		return ottercli.IssuePipelineActionResponse{}, f.stepRejectErr
	}
	return f.stepRejectResponse, nil
}

func (f *fakeIssuePipelineCommandClient) GetIssuePipelineStatus(issueID string) (ottercli.IssuePipelineStatus, error) {
	f.statusIssueID = issueID
	if f.statusErr != nil {
		return ottercli.IssuePipelineStatus{}, f.statusErr
	}
	return f.statusResponse, nil
}

func issuePipelineStringPtr(v string) *string {
	return &v
}
