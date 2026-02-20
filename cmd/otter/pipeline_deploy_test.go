package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

func TestHandlePipelineSetRoleRejectsAgentAndNone(t *testing.T) {
	factoryCalled := false
	err := runPipelineCommand(
		[]string{"set-role", "--project", "Alpha", "--role", "planner", "--agent", "agent-1", "--none"},
		func(org string) (pipelineCommandClient, error) {
			factoryCalled = true
			return nil, nil
		},
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), "use only one of --agent or --none") {
		t.Fatalf("expected --agent/--none validation error, got %v", err)
	}
	if factoryCalled {
		t.Fatalf("factory should not be called on parse/validation failure")
	}
}

func TestHandlePipelineSetRoleCallsSetPipelineRoles(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		pipelineRoles: ottercli.PipelineRoles{
			Planner:  ottercli.PipelineRoleAssignment{AgentID: nil},
			Worker:   ottercli.PipelineRoleAssignment{AgentID: strPtr("worker-1")},
			Reviewer: ottercli.PipelineRoleAssignment{AgentID: strPtr("reviewer-1")},
		},
		resolveAgent: ottercli.Agent{ID: "planner-9", Name: "Planner Nine"},
	}

	var out bytes.Buffer
	err := runPipelineCommand(
		[]string{"set-role", "--project", "Alpha", "--role", "planner", "--agent", "planner-nine", "--org", "org-9", "--json"},
		func(org string) (pipelineCommandClient, error) {
			if org != "org-9" {
				t.Fatalf("org override = %q, want org-9", org)
			}
			return client, nil
		},
		&out,
	)
	if err != nil {
		t.Fatalf("runPipelineCommand() error = %v", err)
	}

	if client.findProjectQuery != "Alpha" {
		t.Fatalf("FindProject query = %q, want Alpha", client.findProjectQuery)
	}
	if client.resolveAgentQuery != "planner-nine" {
		t.Fatalf("ResolveAgent query = %q, want planner-nine", client.resolveAgentQuery)
	}
	if client.setPipelineProjectID != "project-123" {
		t.Fatalf("SetPipelineRoles project id = %q, want project-123", client.setPipelineProjectID)
	}
	if got := client.setPipelinePayload.Planner.AgentID; got == nil || *got != "planner-9" {
		t.Fatalf("planner assignment = %#v, want planner-9", got)
	}
	if got := client.setPipelinePayload.Worker.AgentID; got == nil || *got != "worker-1" {
		t.Fatalf("worker assignment = %#v, want worker-1", got)
	}
	if got := client.setPipelinePayload.Reviewer.AgentID; got == nil || *got != "reviewer-1" {
		t.Fatalf("reviewer assignment = %#v, want reviewer-1", got)
	}
	if !strings.Contains(out.String(), `"planner"`) {
		t.Fatalf("expected json output to include planner role, got %q", out.String())
	}
}

func TestHandlePipelineRolesShowsRoles(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		pipelineRoles: ottercli.PipelineRoles{
			Planner:  ottercli.PipelineRoleAssignment{AgentID: strPtr("planner-1")},
			Worker:   ottercli.PipelineRoleAssignment{AgentID: nil},
			Reviewer: ottercli.PipelineRoleAssignment{AgentID: strPtr("reviewer-1")},
		},
	}

	var out bytes.Buffer
	err := runPipelineCommand(
		[]string{"roles", "--project", "Alpha"},
		func(org string) (pipelineCommandClient, error) {
			if org != "" {
				t.Fatalf("org override = %q, want empty", org)
			}
			return client, nil
		},
		&out,
	)
	if err != nil {
		t.Fatalf("runPipelineCommand() error = %v", err)
	}

	if client.findProjectQuery != "Alpha" {
		t.Fatalf("FindProject query = %q, want Alpha", client.findProjectQuery)
	}
	if client.getPipelineRolesProject != "project-123" {
		t.Fatalf("GetPipelineRoles project id = %q, want project-123", client.getPipelineRolesProject)
	}
	output := out.String()
	if !strings.Contains(output, "Planner:  planner-1") {
		t.Fatalf("expected planner assignment in output, got %q", output)
	}
	if !strings.Contains(output, "Worker:   unassigned") {
		t.Fatalf("expected worker assignment in output, got %q", output)
	}
	if !strings.Contains(output, "Reviewer: reviewer-1") {
		t.Fatalf("expected reviewer assignment in output, got %q", output)
	}
}

func TestHandlePipelineSetRoleClearsAssignment(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		pipelineRoles: ottercli.PipelineRoles{
			Planner:  ottercli.PipelineRoleAssignment{AgentID: strPtr("planner-1")},
			Worker:   ottercli.PipelineRoleAssignment{AgentID: strPtr("worker-1")},
			Reviewer: ottercli.PipelineRoleAssignment{AgentID: strPtr("reviewer-1")},
		},
	}

	var out bytes.Buffer
	err := runPipelineCommand(
		[]string{"set-role", "--project", "Alpha", "--role", "planner", "--none"},
		func(org string) (pipelineCommandClient, error) {
			return client, nil
		},
		&out,
	)
	if err != nil {
		t.Fatalf("runPipelineCommand() error = %v", err)
	}

	if client.resolveAgentQuery != "" {
		t.Fatalf("ResolveAgent should not be called for --none, got query %q", client.resolveAgentQuery)
	}
	if got := client.setPipelinePayload.Planner.AgentID; got != nil {
		t.Fatalf("planner assignment = %#v, want nil", got)
	}
	if got := client.setPipelinePayload.Worker.AgentID; got == nil || *got != "worker-1" {
		t.Fatalf("worker assignment = %#v, want worker-1", got)
	}
	if got := client.setPipelinePayload.Reviewer.AgentID; got == nil || *got != "reviewer-1" {
		t.Fatalf("reviewer assignment = %#v, want reviewer-1", got)
	}
	if !strings.Contains(out.String(), "to unassigned") {
		t.Fatalf("expected clear output message, got %q", out.String())
	}
}

func TestHandlePipelineSetUpdatesRequireHumanReview(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		setRequireHumanReviewProject: ottercli.Project{
			ID:                 "project-123",
			Name:               "Alpha",
			RequireHumanReview: true,
		},
	}

	err := runPipelineCommand(
		[]string{"set", "--project", "Alpha", "--require-human-review", "true"},
		func(org string) (pipelineCommandClient, error) {
			if org != "" {
				t.Fatalf("org override = %q, want empty", org)
			}
			return client, nil
		},
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runPipelineCommand() error = %v", err)
	}
	if client.setRequireHumanReviewProjectID != "project-123" {
		t.Fatalf("SetProjectRequireHumanReview project id = %q, want project-123", client.setRequireHumanReviewProjectID)
	}
	if !client.setRequireHumanReviewValue {
		t.Fatalf("SetProjectRequireHumanReview value = false, want true")
	}
}

func TestHandlePipelineShowListsSteps(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		pipelineSteps: []ottercli.PipelineStep{
			{
				ID:              "step-1",
				ProjectID:       "project-123",
				StepNumber:      1,
				Name:            "Draft",
				StepType:        "agent_work",
				AssignedAgentID: strPtr("agent-1"),
				AutoAdvance:     true,
			},
			{
				ID:          "step-2",
				ProjectID:   "project-123",
				StepNumber:  2,
				Name:        "Review",
				StepType:    "human_review",
				AutoAdvance: false,
			},
		},
	}

	var out bytes.Buffer
	err := runPipelineCommand(
		[]string{"show", "--project", "Alpha"},
		func(org string) (pipelineCommandClient, error) {
			return client, nil
		},
		&out,
	)
	if err != nil {
		t.Fatalf("runPipelineCommand() error = %v", err)
	}
	if client.listPipelineStepsProject != "project-123" {
		t.Fatalf("ListPipelineSteps project id = %q, want project-123", client.listPipelineStepsProject)
	}
	output := out.String()
	if !strings.Contains(output, "1. Draft [agent_work]") {
		t.Fatalf("expected draft row in output, got %q", output)
	}
	if !strings.Contains(output, "2. Review [human_review]") {
		t.Fatalf("expected review row in output, got %q", output)
	}
}

func TestHandlePipelineSetStepsUsesPipelineStepEndpoints(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		pipelineSteps: []ottercli.PipelineStep{
			{ID: "old-1", ProjectID: "project-123", StepNumber: 1, Name: "Old A", StepType: "agent_work", AutoAdvance: true},
			{ID: "old-2", ProjectID: "project-123", StepNumber: 2, Name: "Old B", StepType: "agent_review", AutoAdvance: true},
		},
		resolveAgent: ottercli.Agent{ID: "agent-2", Name: "Agent 2"},
	}

	err := runPipelineCommand(
		[]string{"set", "--project", "Alpha", "--steps", `[{"name":"Draft","type":"agent_work","agent":"stone"},{"name":"Review","type":"human_review","auto_advance":false}]`},
		func(org string) (pipelineCommandClient, error) {
			return client, nil
		},
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runPipelineCommand() error = %v", err)
	}

	if client.listPipelineStepsProject != "project-123" {
		t.Fatalf("ListPipelineSteps project id = %q, want project-123", client.listPipelineStepsProject)
	}
	if len(client.deletePipelineStepIDs) != 2 || client.deletePipelineStepIDs[0] != "old-1" || client.deletePipelineStepIDs[1] != "old-2" {
		t.Fatalf("DeletePipelineStep calls = %#v, want old-1 then old-2", client.deletePipelineStepIDs)
	}
	if client.resolveAgentQuery != "stone" {
		t.Fatalf("ResolveAgent query = %q, want stone", client.resolveAgentQuery)
	}
	if len(client.createPipelineStepPayloads) != 2 {
		t.Fatalf("CreatePipelineStep payload count = %d, want 2", len(client.createPipelineStepPayloads))
	}
	if client.createPipelineStepPayloads[0].Name != "Draft" || client.createPipelineStepPayloads[0].StepNumber != 1 {
		t.Fatalf("first payload = %#v", client.createPipelineStepPayloads[0])
	}
	if client.createPipelineStepPayloads[0].AssignedAgentID == nil || *client.createPipelineStepPayloads[0].AssignedAgentID != "agent-2" {
		t.Fatalf("first payload agent = %#v, want agent-2", client.createPipelineStepPayloads[0].AssignedAgentID)
	}
	if client.createPipelineStepPayloads[1].StepType != "human_review" || client.createPipelineStepPayloads[1].AutoAdvance {
		t.Fatalf("second payload = %#v", client.createPipelineStepPayloads[1])
	}
}

func TestHandlePipelineAddStepReordersPipeline(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		pipelineSteps: []ottercli.PipelineStep{
			{ID: "step-1", ProjectID: "project-123", StepNumber: 1, Name: "Draft", StepType: "agent_work", AutoAdvance: true},
			{ID: "step-2", ProjectID: "project-123", StepNumber: 2, Name: "Review", StepType: "agent_review", AutoAdvance: true},
		},
		resolveAgent: ottercli.Agent{ID: "agent-9", Name: "Agent 9"},
	}

	err := runPipelineCommand(
		[]string{"add-step", "--project", "Alpha", "--name", "Fact Check", "--type", "agent_review", "--position", "2", "--agent", "agent-nine"},
		func(org string) (pipelineCommandClient, error) {
			return client, nil
		},
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runPipelineCommand() error = %v", err)
	}
	if len(client.createPipelineStepPayloads) != 1 {
		t.Fatalf("CreatePipelineStep payload count = %d, want 1", len(client.createPipelineStepPayloads))
	}
	if client.createPipelineStepPayloads[0].StepNumber != 3 {
		t.Fatalf("CreatePipelineStep step number = %d, want 3", client.createPipelineStepPayloads[0].StepNumber)
	}
	if client.resolveAgentQuery != "agent-nine" {
		t.Fatalf("ResolveAgent query = %q, want agent-nine", client.resolveAgentQuery)
	}
	if client.reorderPipelineStepsProjectID != "project-123" {
		t.Fatalf("ReorderPipelineSteps project id = %q, want project-123", client.reorderPipelineStepsProjectID)
	}
	expectedOrder := []string{"step-1", "created-step-1", "step-2"}
	if strings.Join(client.reorderPipelineStepIDs, ",") != strings.Join(expectedOrder, ",") {
		t.Fatalf("ReorderPipelineSteps ids = %#v, want %#v", client.reorderPipelineStepIDs, expectedOrder)
	}
}

func TestHandleDeploySetValidationForCliCommandRequiresCommand(t *testing.T) {
	factoryCalled := false
	err := runDeployCommand(
		[]string{"set", "--project", "Alpha", "--method", "cli_command"},
		func(org string) (deployCommandClient, error) {
			factoryCalled = true
			return nil, nil
		},
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), "--command is required for --method cli_command") {
		t.Fatalf("expected --command validation error, got %v", err)
	}
	if factoryCalled {
		t.Fatalf("factory should not be called on parse/validation failure")
	}
}

func TestHandleDeploySetCallsSetDeployConfig(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		setDeployConfigResult: ottercli.DeployConfig{
			DeployMethod:  "github_push",
			GitHubRepoURL: strPtr("https://github.com/acme/alpha"),
			GitHubBranch:  "main",
		},
	}

	var out bytes.Buffer
	err := runDeployCommand(
		[]string{"set", "--project", "Alpha", "--method", "github_push", "--repo", "https://github.com/acme/alpha", "--branch", "main", "--json"},
		func(org string) (deployCommandClient, error) {
			if org != "" {
				t.Fatalf("org override = %q, want empty", org)
			}
			return client, nil
		},
		&out,
	)
	if err != nil {
		t.Fatalf("runDeployCommand() error = %v", err)
	}
	if client.setDeployConfigProjectID != "project-123" {
		t.Fatalf("SetDeployConfig project id = %q, want project-123", client.setDeployConfigProjectID)
	}
	if client.setDeployConfigPayload.DeployMethod != "github_push" {
		t.Fatalf("deploy method = %q, want github_push", client.setDeployConfigPayload.DeployMethod)
	}
	if client.setDeployConfigPayload.GitHubRepoURL == nil || *client.setDeployConfigPayload.GitHubRepoURL != "https://github.com/acme/alpha" {
		t.Fatalf("repo url = %#v, want https://github.com/acme/alpha", client.setDeployConfigPayload.GitHubRepoURL)
	}
	if client.setDeployConfigPayload.GitHubBranch != "main" {
		t.Fatalf("branch = %q, want main", client.setDeployConfigPayload.GitHubBranch)
	}
	if !strings.Contains(out.String(), `"deployMethod"`) {
		t.Fatalf("expected json output to include deployMethod, got %q", out.String())
	}
}

func TestDeploySetMethodNoneSendsEmptyBranch(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		setDeployConfigResult: ottercli.DeployConfig{
			DeployMethod: "none",
		},
	}

	err := runDeployCommand(
		[]string{"set", "--project", "Alpha", "--method", "none"},
		func(org string) (deployCommandClient, error) {
			return client, nil
		},
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runDeployCommand() error = %v", err)
	}

	if client.setDeployConfigPayload.DeployMethod != "none" {
		t.Fatalf("deploy method = %q, want none", client.setDeployConfigPayload.DeployMethod)
	}
	if client.setDeployConfigPayload.GitHubBranch != "" {
		t.Fatalf("branch = %q, want empty", client.setDeployConfigPayload.GitHubBranch)
	}
	if client.setDeployConfigPayload.GitHubRepoURL != nil {
		t.Fatalf("repo url = %#v, want nil", client.setDeployConfigPayload.GitHubRepoURL)
	}
	if client.setDeployConfigPayload.CLICommand != nil {
		t.Fatalf("cli command = %#v, want nil", client.setDeployConfigPayload.CLICommand)
	}
}

func TestHandleDeployConfigCallsGetDeployConfig(t *testing.T) {
	client := &fakeSettingsCommandClient{
		project: ottercli.Project{ID: "project-123", Name: "Alpha"},
		getDeployConfigResult: ottercli.DeployConfig{
			DeployMethod: "none",
			GitHubBranch: "main",
		},
	}

	var out bytes.Buffer
	err := runDeployCommand(
		[]string{"config", "--project", "Alpha"},
		func(org string) (deployCommandClient, error) {
			return client, nil
		},
		&out,
	)
	if err != nil {
		t.Fatalf("runDeployCommand() error = %v", err)
	}
	if client.getDeployConfigProjectID != "project-123" {
		t.Fatalf("GetDeployConfig project id = %q, want project-123", client.getDeployConfigProjectID)
	}
	if !strings.Contains(out.String(), "Method: none") {
		t.Fatalf("expected human output to include deploy method, got %q", out.String())
	}
}

type fakeSettingsCommandClient struct {
	project          ottercli.Project
	findProjectErr   error
	findProjectQuery string

	pipelineRoles            ottercli.PipelineRoles
	getPipelineRolesErr      error
	getPipelineRolesProject  string
	setPipelineRolesErr      error
	setPipelineProjectID     string
	setPipelinePayload       ottercli.PipelineRoles
	setPipelineResult        ottercli.PipelineRoles
	resolveAgentErr          error
	resolveAgent             ottercli.Agent
	resolveAgentQuery        string
	setRequireHumanReviewErr error

	setRequireHumanReviewProjectID string
	setRequireHumanReviewValue     bool
	setRequireHumanReviewProject   ottercli.Project

	pipelineSteps                 []ottercli.PipelineStep
	listPipelineStepsErr          error
	listPipelineStepsProject      string
	createPipelineStepErr         error
	createPipelineStepProjectID   string
	createPipelineStepPayloads    []ottercli.PipelineStepCreateInput
	deletePipelineStepErr         error
	deletePipelineStepProjectID   string
	deletePipelineStepIDs         []string
	reorderPipelineStepsErr       error
	reorderPipelineStepsProjectID string
	reorderPipelineStepIDs        []string
	reorderPipelineStepsResult    []ottercli.PipelineStep

	getDeployConfigErr       error
	getDeployConfigProjectID string
	getDeployConfigResult    ottercli.DeployConfig
	setDeployConfigErr       error
	setDeployConfigProjectID string
	setDeployConfigPayload   ottercli.DeployConfig
	setDeployConfigResult    ottercli.DeployConfig
}

func (f *fakeSettingsCommandClient) FindProject(query string) (ottercli.Project, error) {
	f.findProjectQuery = query
	if f.findProjectErr != nil {
		return ottercli.Project{}, f.findProjectErr
	}
	if strings.TrimSpace(f.project.ID) == "" {
		return ottercli.Project{ID: "project-1", Name: "Project One"}, nil
	}
	return f.project, nil
}

func (f *fakeSettingsCommandClient) ResolveAgent(query string) (ottercli.Agent, error) {
	f.resolveAgentQuery = query
	if f.resolveAgentErr != nil {
		return ottercli.Agent{}, f.resolveAgentErr
	}
	if strings.TrimSpace(f.resolveAgent.ID) == "" {
		return ottercli.Agent{}, errors.New("agent not found")
	}
	return f.resolveAgent, nil
}

func (f *fakeSettingsCommandClient) GetPipelineRoles(projectID string) (ottercli.PipelineRoles, error) {
	f.getPipelineRolesProject = projectID
	if f.getPipelineRolesErr != nil {
		return ottercli.PipelineRoles{}, f.getPipelineRolesErr
	}
	return f.pipelineRoles, nil
}

func (f *fakeSettingsCommandClient) SetPipelineRoles(projectID string, roles ottercli.PipelineRoles) (ottercli.PipelineRoles, error) {
	f.setPipelineProjectID = projectID
	f.setPipelinePayload = roles
	if f.setPipelineRolesErr != nil {
		return ottercli.PipelineRoles{}, f.setPipelineRolesErr
	}
	if !pipelineRolesHasAnyAssignment(f.setPipelineResult) {
		return roles, nil
	}
	return f.setPipelineResult, nil
}

func (f *fakeSettingsCommandClient) SetProjectRequireHumanReview(projectID string, requireHumanReview bool) (ottercli.Project, error) {
	f.setRequireHumanReviewProjectID = projectID
	f.setRequireHumanReviewValue = requireHumanReview
	if f.setRequireHumanReviewErr != nil {
		return ottercli.Project{}, f.setRequireHumanReviewErr
	}
	if strings.TrimSpace(f.setRequireHumanReviewProject.ID) == "" {
		return ottercli.Project{ID: projectID, RequireHumanReview: requireHumanReview}, nil
	}
	return f.setRequireHumanReviewProject, nil
}

func (f *fakeSettingsCommandClient) ListPipelineSteps(projectID string) ([]ottercli.PipelineStep, error) {
	f.listPipelineStepsProject = projectID
	if f.listPipelineStepsErr != nil {
		return nil, f.listPipelineStepsErr
	}
	return append([]ottercli.PipelineStep(nil), f.pipelineSteps...), nil
}

func (f *fakeSettingsCommandClient) CreatePipelineStep(projectID string, input ottercli.PipelineStepCreateInput) (ottercli.PipelineStep, error) {
	f.createPipelineStepProjectID = projectID
	f.createPipelineStepPayloads = append(f.createPipelineStepPayloads, input)
	if f.createPipelineStepErr != nil {
		return ottercli.PipelineStep{}, f.createPipelineStepErr
	}
	createdID := fmt.Sprintf("created-step-%d", len(f.createPipelineStepPayloads))
	step := ottercli.PipelineStep{
		ID:              createdID,
		ProjectID:       projectID,
		StepNumber:      input.StepNumber,
		Name:            input.Name,
		Description:     input.Description,
		AssignedAgentID: input.AssignedAgentID,
		StepType:        input.StepType,
		AutoAdvance:     input.AutoAdvance,
	}
	f.pipelineSteps = append(f.pipelineSteps, step)
	return step, nil
}

func (f *fakeSettingsCommandClient) DeletePipelineStep(projectID string, stepID string) error {
	f.deletePipelineStepProjectID = projectID
	f.deletePipelineStepIDs = append(f.deletePipelineStepIDs, stepID)
	if f.deletePipelineStepErr != nil {
		return f.deletePipelineStepErr
	}
	next := make([]ottercli.PipelineStep, 0, len(f.pipelineSteps))
	for _, step := range f.pipelineSteps {
		if step.ID != stepID {
			next = append(next, step)
		}
	}
	f.pipelineSteps = next
	return nil
}

func (f *fakeSettingsCommandClient) ReorderPipelineSteps(projectID string, stepIDs []string) ([]ottercli.PipelineStep, error) {
	f.reorderPipelineStepsProjectID = projectID
	f.reorderPipelineStepIDs = append([]string(nil), stepIDs...)
	if f.reorderPipelineStepsErr != nil {
		return nil, f.reorderPipelineStepsErr
	}
	if len(f.reorderPipelineStepsResult) > 0 {
		return append([]ottercli.PipelineStep(nil), f.reorderPipelineStepsResult...), nil
	}

	byID := make(map[string]ottercli.PipelineStep, len(f.pipelineSteps))
	for _, step := range f.pipelineSteps {
		byID[step.ID] = step
	}
	ordered := make([]ottercli.PipelineStep, 0, len(stepIDs))
	for idx, id := range stepIDs {
		step, ok := byID[id]
		if !ok {
			continue
		}
		step.StepNumber = idx + 1
		ordered = append(ordered, step)
	}
	f.pipelineSteps = ordered
	return append([]ottercli.PipelineStep(nil), ordered...), nil
}

func (f *fakeSettingsCommandClient) GetDeployConfig(projectID string) (ottercli.DeployConfig, error) {
	f.getDeployConfigProjectID = projectID
	if f.getDeployConfigErr != nil {
		return ottercli.DeployConfig{}, f.getDeployConfigErr
	}
	return f.getDeployConfigResult, nil
}

func (f *fakeSettingsCommandClient) SetDeployConfig(projectID string, cfg ottercli.DeployConfig) (ottercli.DeployConfig, error) {
	f.setDeployConfigProjectID = projectID
	f.setDeployConfigPayload = cfg
	if f.setDeployConfigErr != nil {
		return ottercli.DeployConfig{}, f.setDeployConfigErr
	}
	if strings.TrimSpace(f.setDeployConfigResult.DeployMethod) == "" {
		return cfg, nil
	}
	return f.setDeployConfigResult, nil
}

func strPtr(v string) *string {
	return &v
}

func pipelineRolesHasAnyAssignment(roles ottercli.PipelineRoles) bool {
	return roleHasAgent(roles.Planner) || roleHasAgent(roles.Worker) || roleHasAgent(roles.Reviewer)
}

func roleHasAgent(role ottercli.PipelineRoleAssignment) bool {
	return role.AgentID != nil && strings.TrimSpace(*role.AgentID) != ""
}
