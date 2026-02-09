package main

import (
	"bytes"
	"errors"
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
