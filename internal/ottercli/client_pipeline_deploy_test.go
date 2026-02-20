package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientPipelineRoleMethods(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/project-1/pipeline-roles":
			_, _ = w.Write([]byte(`{"planner":{"agentId":"agent-planner"},"worker":{"agentId":"agent-worker"},"reviewer":{"agentId":null}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/projects/project-1/pipeline-roles":
			_, _ = w.Write([]byte(`{"planner":{"agentId":"agent-planner"},"worker":{"agentId":null},"reviewer":{"agentId":"agent-reviewer"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	current, err := client.GetPipelineRoles("project-1")
	if err != nil {
		t.Fatalf("GetPipelineRoles() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/projects/project-1/pipeline-roles" {
		t.Fatalf("GetPipelineRoles request = %s %s", gotMethod, gotPath)
	}
	if current.Planner.AgentID == nil || *current.Planner.AgentID != "agent-planner" {
		t.Fatalf("GetPipelineRoles planner = %#v", current.Planner.AgentID)
	}

	next, err := client.SetPipelineRoles("project-1", PipelineRoles{
		Planner:  PipelineRoleAssignment{AgentID: stringPtr("agent-planner")},
		Worker:   PipelineRoleAssignment{AgentID: nil},
		Reviewer: PipelineRoleAssignment{AgentID: stringPtr("agent-reviewer")},
	})
	if err != nil {
		t.Fatalf("SetPipelineRoles() error = %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/projects/project-1/pipeline-roles" {
		t.Fatalf("SetPipelineRoles request = %s %s", gotMethod, gotPath)
	}
	if gotBody["planner"] == nil || gotBody["reviewer"] == nil {
		t.Fatalf("SetPipelineRoles payload = %#v", gotBody)
	}
	if next.Reviewer.AgentID == nil || *next.Reviewer.AgentID != "agent-reviewer" {
		t.Fatalf("SetPipelineRoles reviewer = %#v", next.Reviewer.AgentID)
	}
}

func TestClientPipelineSteps(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/project-1/pipeline-steps":
			_, _ = w.Write([]byte(`{"items":[{"id":"step-1","project_id":"project-1","step_number":1,"name":"Draft","description":"","assigned_agent_id":"agent-1","step_type":"agent_work","auto_advance":true}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/projects/project-1/pipeline-steps":
			_, _ = w.Write([]byte(`{"id":"step-2","project_id":"project-1","step_number":2,"name":"Review","description":"","assigned_agent_id":null,"step_type":"human_review","auto_advance":false}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/projects/project-1/pipeline-steps/reorder":
			_, _ = w.Write([]byte(`{"items":[{"id":"step-2","project_id":"project-1","step_number":1,"name":"Review","description":"","assigned_agent_id":null,"step_type":"human_review","auto_advance":false},{"id":"step-1","project_id":"project-1","step_number":2,"name":"Draft","description":"","assigned_agent_id":"agent-1","step_type":"agent_work","auto_advance":true}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/projects/project-1/pipeline-steps/step-2":
			_, _ = w.Write([]byte(`{"deleted":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	steps, err := client.ListPipelineSteps("project-1")
	if err != nil {
		t.Fatalf("ListPipelineSteps() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/projects/project-1/pipeline-steps" {
		t.Fatalf("ListPipelineSteps request = %s %s", gotMethod, gotPath)
	}
	if len(steps) != 1 || steps[0].ID != "step-1" {
		t.Fatalf("ListPipelineSteps response = %#v", steps)
	}

	created, err := client.CreatePipelineStep("project-1", PipelineStepCreateInput{
		StepNumber:  2,
		Name:        "Review",
		StepType:    "human_review",
		AutoAdvance: false,
	})
	if err != nil {
		t.Fatalf("CreatePipelineStep() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/projects/project-1/pipeline-steps" {
		t.Fatalf("CreatePipelineStep request = %s %s", gotMethod, gotPath)
	}
	if gotBody["step_number"] != float64(2) || gotBody["name"] != "Review" {
		t.Fatalf("CreatePipelineStep payload = %#v", gotBody)
	}
	if created.ID != "step-2" {
		t.Fatalf("CreatePipelineStep response = %#v", created)
	}

	reordered, err := client.ReorderPipelineSteps("project-1", []string{"step-2", "step-1"})
	if err != nil {
		t.Fatalf("ReorderPipelineSteps() error = %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/projects/project-1/pipeline-steps/reorder" {
		t.Fatalf("ReorderPipelineSteps request = %s %s", gotMethod, gotPath)
	}
	if gotBody["step_ids"] == nil {
		t.Fatalf("ReorderPipelineSteps payload = %#v", gotBody)
	}
	if len(reordered) != 2 || reordered[0].ID != "step-2" {
		t.Fatalf("ReorderPipelineSteps response = %#v", reordered)
	}

	if err := client.DeletePipelineStep("project-1", "step-2"); err != nil {
		t.Fatalf("DeletePipelineStep() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/projects/project-1/pipeline-steps/step-2" {
		t.Fatalf("DeletePipelineStep request = %s %s", gotMethod, gotPath)
	}
}

func TestClientDeployConfigMethods(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/project-1/deploy-config":
			_, _ = w.Write([]byte(`{"deployMethod":"none","githubBranch":"main","githubRepoUrl":null,"cliCommand":null}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/projects/project-1/deploy-config":
			_, _ = w.Write([]byte(`{"deployMethod":"cli_command","githubBranch":"main","githubRepoUrl":null,"cliCommand":"railway up"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	current, err := client.GetDeployConfig("project-1")
	if err != nil {
		t.Fatalf("GetDeployConfig() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/projects/project-1/deploy-config" {
		t.Fatalf("GetDeployConfig request = %s %s", gotMethod, gotPath)
	}
	if current.DeployMethod != "none" {
		t.Fatalf("GetDeployConfig deploy method = %q", current.DeployMethod)
	}

	updated, err := client.SetDeployConfig("project-1", DeployConfig{
		DeployMethod: "cli_command",
		CLICommand:   stringPtr("railway up"),
	})
	if err != nil {
		t.Fatalf("SetDeployConfig() error = %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/projects/project-1/deploy-config" {
		t.Fatalf("SetDeployConfig request = %s %s", gotMethod, gotPath)
	}
	if gotBody["deployMethod"] != "cli_command" {
		t.Fatalf("SetDeployConfig payload = %#v", gotBody)
	}
	if updated.CLICommand == nil || *updated.CLICommand != "railway up" {
		t.Fatalf("SetDeployConfig cliCommand = %#v", updated.CLICommand)
	}
}

func TestClientSetProjectRequireHumanReview(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}

		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch && r.URL.Path == "/api/projects/project-1" {
			_, _ = w.Write([]byte(`{"id":"project-1","name":"Project One","status":"active","require_human_review":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	project, err := client.SetProjectRequireHumanReview("project-1", true)
	if err != nil {
		t.Fatalf("SetProjectRequireHumanReview() error = %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/projects/project-1" {
		t.Fatalf("SetProjectRequireHumanReview request = %s %s", gotMethod, gotPath)
	}
	if gotBody["requireHumanReview"] != true {
		t.Fatalf("SetProjectRequireHumanReview payload = %#v", gotBody)
	}
	if !project.RequireHumanReview {
		t.Fatalf("SetProjectRequireHumanReview response = %#v", project)
	}
}

func stringPtr(v string) *string {
	return &v
}
