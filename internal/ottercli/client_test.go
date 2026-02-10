package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Project", "my-project"},
		{"sprint-42-docs", "sprint-42-docs"},
		{"  Hello World  ", "hello-world"},
		{"A/B Test!", "a-b-test"},
		{"", "project"},
		{"---", "project"},
		{"UPPER CASE", "upper-case"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProjectSlug(t *testing.T) {
	withServerSlug := Project{Name: "My Cool Project", URLSlug: "server-slug"}
	if got := withServerSlug.Slug(); got != "server-slug" {
		t.Errorf("Slug() with URLSlug = %q, want %q", got, "server-slug")
	}

	withoutServerSlug := Project{Name: "My Cool Project"}
	if got := withoutServerSlug.Slug(); got != "my-cool-project" {
		t.Errorf("Slug() fallback = %q, want %q", got, "my-cool-project")
	}
}

func TestClientOnboardingBootstrapUsesExpectedPathAndPayload(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]any
	var gotAuth string
	var gotOrg string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		gotOrg = r.Header.Get("X-Org-ID")
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"org_id":"org-1","org_slug":"my-team","user_id":"user-1","token":"oc_sess_abc","expires_at":"2026-02-11T00:00:00Z","project_id":"project-1","project_name":"Getting Started","issue_id":"issue-1","issue_number":1,"issue_title":"Welcome to Otter Camp"}`))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
	}

	resp, err := client.OnboardingBootstrap(OnboardingBootstrapRequest{
		Name:             "Sam",
		Email:            "sam@example.com",
		OrganizationName: "My Team",
	})
	if err != nil {
		t.Fatalf("OnboardingBootstrap() error = %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/api/onboarding/bootstrap" {
		t.Fatalf("OnboardingBootstrap request = %s %s", gotMethod, gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("expected no auth header for onboarding bootstrap, got %q", gotAuth)
	}
	if gotOrg != "" {
		t.Fatalf("expected no org header for onboarding bootstrap, got %q", gotOrg)
	}
	if gotBody["name"] != "Sam" || gotBody["email"] != "sam@example.com" || gotBody["organization_name"] != "My Team" {
		t.Fatalf("OnboardingBootstrap payload = %#v", gotBody)
	}
	if resp.OrgID != "org-1" || resp.Token != "oc_sess_abc" || resp.ProjectName != "Getting Started" {
		t.Fatalf("OnboardingBootstrap response = %#v", resp)
	}
}

func TestClientOnboardingBootstrapParsesExpiresAtTime(t *testing.T) {
	const rawExpires = "2026-02-11T00:00:00Z"
	var expected time.Time
	var err error
	expected, err = time.Parse(time.RFC3339, rawExpires)
	if err != nil {
		t.Fatalf("failed to parse expected time: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"org_id":"org-1","org_slug":"my-team","user_id":"user-1","token":"oc_sess_abc","expires_at":"2026-02-11T00:00:00Z","project_id":"project-1","project_name":"Getting Started","issue_id":"issue-1","issue_number":1,"issue_title":"Welcome to Otter Camp"}`))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
	}

	resp, err := client.OnboardingBootstrap(OnboardingBootstrapRequest{
		Name:             "Sam",
		Email:            "sam@example.com",
		OrganizationName: "My Team",
	})
	if err != nil {
		t.Fatalf("OnboardingBootstrap() error = %v", err)
	}
	if resp.ExpiresAt.IsZero() {
		t.Fatalf("expected non-zero expires_at time")
	}
	if !resp.ExpiresAt.Equal(expected) {
		t.Fatalf("expires_at = %s, want %s", resp.ExpiresAt.Format(time.RFC3339), expected.Format(time.RFC3339))
	}
}

func TestClientIssueMethodsUseExpectedPathsAndPayloads(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()

		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/projects/project-123/issues":
			_, _ = w.Write([]byte(`{"id":"issue-1","project_id":"project-123","parent_issue_id":"issue-parent-1","issue_number":12,"title":"Test issue","state":"open","origin":"local","approval_state":"draft","work_status":"queued","priority":"P2"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues":
			_, _ = w.Write([]byte(`{"items":[{"id":"issue-1","project_id":"project-123","issue_number":12,"title":"Test issue","state":"open","origin":"local","approval_state":"draft","work_status":"queued","priority":"P2"}],"total":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues/issue-1":
			_, _ = w.Write([]byte(`{"issue":{"id":"issue-1","project_id":"project-123","issue_number":12,"title":"Test issue","state":"open","origin":"local","approval_state":"draft","work_status":"queued","priority":"P2"}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/issues/issue-1":
			_, _ = w.Write([]byte(`{"id":"issue-1","project_id":"project-123","issue_number":12,"title":"Test issue","state":"open","origin":"local","approval_state":"draft","work_status":"in_progress","priority":"P1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues/issue-1/comments":
			_, _ = w.Write([]byte(`{"ok":true}`))
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

	issue, err := client.CreateIssue("project-123", map[string]any{
		"title":           "Test issue",
		"priority":        "P2",
		"parent_issue_id": "issue-parent-1",
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if issue.ID != "issue-1" {
		t.Fatalf("CreateIssue() id = %q, want issue-1", issue.ID)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/projects/project-123/issues" {
		t.Fatalf("CreateIssue request = %s %s", gotMethod, gotPath)
	}
	if gotBody["title"] != "Test issue" {
		t.Fatalf("CreateIssue payload title = %v", gotBody["title"])
	}
	if gotBody["parent_issue_id"] != "issue-parent-1" {
		t.Fatalf("CreateIssue payload parent_issue_id = %v", gotBody["parent_issue_id"])
	}

	issues, err := client.ListIssues("project-123", map[string]string{"work_status": "queued"})
	if err != nil {
		t.Fatalf("ListIssues() error = %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("ListIssues() len = %d, want 1", len(issues))
	}
	if gotMethod != http.MethodGet || gotPath != "/api/issues?project_id=project-123&work_status=queued" {
		t.Fatalf("ListIssues request = %s %s", gotMethod, gotPath)
	}

	gotIssue, err := client.GetIssue("issue-1")
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if gotIssue.ID != "issue-1" {
		t.Fatalf("GetIssue() id = %q, want issue-1", gotIssue.ID)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/issues/issue-1" {
		t.Fatalf("GetIssue request = %s %s", gotMethod, gotPath)
	}

	patched, err := client.PatchIssue("issue-1", map[string]any{"work_status": "in_progress", "priority": "P1"})
	if err != nil {
		t.Fatalf("PatchIssue() error = %v", err)
	}
	if patched.WorkStatus != "in_progress" {
		t.Fatalf("PatchIssue() work_status = %q, want in_progress", patched.WorkStatus)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/issues/issue-1" {
		t.Fatalf("PatchIssue request = %s %s", gotMethod, gotPath)
	}

	err = client.CommentIssue("issue-1", "agent-1", "Looks good")
	if err != nil {
		t.Fatalf("CommentIssue() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/issues/issue-1/comments" {
		t.Fatalf("CommentIssue request = %s %s", gotMethod, gotPath)
	}
	if gotBody["author_agent_id"] != "agent-1" || gotBody["body"] != "Looks good" {
		t.Fatalf("CommentIssue payload = %#v", gotBody)
	}
}

func TestClientResolveAgentByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/agents" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[{"id":"a1","name":"Stone","slug":"stone"},{"id":"a2","name":"Ivy","slug":"ivy"}]}`))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	agent, err := client.ResolveAgent("stone")
	if err != nil {
		t.Fatalf("ResolveAgent() error = %v", err)
	}
	if agent.ID != "a1" {
		t.Fatalf("ResolveAgent() id = %q, want a1", agent.ID)
	}
}

func TestClientAskIssueQuestionnaire(t *testing.T) {
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
		if r.Method == http.MethodPost && r.URL.Path == "/api/issues/issue-1/questionnaire" {
			_, _ = w.Write([]byte(`{"id":"qn-1","context_type":"issue","context_id":"issue-1","author":"Sam","title":"Design choices","questions":[{"id":"q1","text":"Protocol?","type":"select","required":true,"options":["WebSocket","Polling"]}],"created_at":"2026-02-08T17:10:00Z"}`))
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

	title := "Design choices"
	created, err := client.AskIssueQuestionnaire("issue-1", CreateIssueQuestionnaireInput{
		Author: "Sam",
		Title:  &title,
		Questions: []QuestionnaireQuestion{
			{
				ID:       "q1",
				Text:     "Protocol?",
				Type:     "select",
				Required: true,
				Options:  []string{"WebSocket", "Polling"},
			},
		},
	})
	if err != nil {
		t.Fatalf("AskIssueQuestionnaire() error = %v", err)
	}
	if created.ID != "qn-1" {
		t.Fatalf("AskIssueQuestionnaire() id = %q, want qn-1", created.ID)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/issues/issue-1/questionnaire" {
		t.Fatalf("AskIssueQuestionnaire request = %s %s", gotMethod, gotPath)
	}
	if gotBody["author"] != "Sam" || gotBody["title"] != "Design choices" {
		t.Fatalf("AskIssueQuestionnaire payload = %#v", gotBody)
	}
	questions, ok := gotBody["questions"].([]interface{})
	if !ok || len(questions) != 1 {
		t.Fatalf("AskIssueQuestionnaire questions payload = %#v", gotBody["questions"])
	}
}

func TestClientRespondIssueQuestionnaire(t *testing.T) {
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
		if r.Method == http.MethodPost && r.URL.Path == "/api/questionnaires/qn-1/response" {
			_, _ = w.Write([]byte(`{"id":"qn-1","context_type":"issue","context_id":"issue-1","author":"Sam","questions":[{"id":"q1","text":"Protocol?","type":"select","required":true,"options":["WebSocket","Polling"]}],"responses":{"q1":"WebSocket"},"responded_by":"Riley","responded_at":"2026-02-08T17:12:00Z","created_at":"2026-02-08T17:10:00Z"}`))
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

	responded, err := client.RespondIssueQuestionnaire("qn-1", RespondIssueQuestionnaireInput{
		RespondedBy: "Riley",
		Responses: map[string]any{
			"q1": "WebSocket",
			"q2": true,
		},
	})
	if err != nil {
		t.Fatalf("RespondIssueQuestionnaire() error = %v", err)
	}
	if responded.ID != "qn-1" {
		t.Fatalf("RespondIssueQuestionnaire() id = %q, want qn-1", responded.ID)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/questionnaires/qn-1/response" {
		t.Fatalf("RespondIssueQuestionnaire request = %s %s", gotMethod, gotPath)
	}
	if gotBody["responded_by"] != "Riley" {
		t.Fatalf("RespondIssueQuestionnaire payload responded_by = %#v", gotBody["responded_by"])
	}
	responses, ok := gotBody["responses"].(map[string]interface{})
	if !ok || responses["q1"] != "WebSocket" || responses["q2"] != true {
		t.Fatalf("RespondIssueQuestionnaire payload responses = %#v", gotBody["responses"])
	}
}

func TestClientProjectMethodsUseExpectedPathsAndPayloads(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/project-123":
			_, _ = w.Write([]byte(`{"id":"project-123","org_id":"org-1","name":"My Project","status":"active","repo_url":"https://example.com/repo.git"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/projects/project-123":
			_, _ = w.Write([]byte(`{"id":"project-123","org_id":"org-1","name":"My Project","status":"archived","repo_url":"https://example.com/repo.git"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/projects/project-123":
			_, _ = w.Write([]byte(`{"ok":true}`))
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

	project, err := client.GetProject("project-123")
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if project.ID != "project-123" {
		t.Fatalf("GetProject() id = %q, want project-123", project.ID)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/projects/project-123" {
		t.Fatalf("GetProject request = %s %s", gotMethod, gotPath)
	}

	patched, err := client.PatchProject("project-123", map[string]any{"status": "archived"})
	if err != nil {
		t.Fatalf("PatchProject() error = %v", err)
	}
	if patched.Status != "archived" {
		t.Fatalf("PatchProject() status = %q, want archived", patched.Status)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/projects/project-123" {
		t.Fatalf("PatchProject request = %s %s", gotMethod, gotPath)
	}
	if gotBody["status"] != "archived" {
		t.Fatalf("PatchProject payload status = %v", gotBody["status"])
	}

	if err := client.DeleteProject("project-123"); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/projects/project-123" {
		t.Fatalf("DeleteProject request = %s %s", gotMethod, gotPath)
	}
}

func TestClientWorkflowProjectMethodsUseExpectedPaths(t *testing.T) {
	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.String() == "/api/projects?workflow=true":
			_, _ = w.Write([]byte(`{"projects":[{"id":"project-123","name":"Workflow Project","status":"active","workflow_enabled":true}],"total":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/projects/project-123/runs/trigger":
			_, _ = w.Write([]byte(`{"run":{"id":"issue-1","project_id":"project-123","issue_number":11,"title":"Run 11","state":"open","work_status":"queued","priority":"P2","created_at":"2026-02-09T00:00:00Z"},"run_number":11}`))
		case r.Method == http.MethodGet && r.URL.String() == "/api/projects/project-123/runs?limit=5":
			_, _ = w.Write([]byte(`{"runs":[{"id":"issue-1","project_id":"project-123","issue_number":11,"title":"Run 11","state":"open","work_status":"queued","priority":"P2","created_at":"2026-02-09T00:00:00Z"}]}`))
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

	projects, err := client.ListProjectsWithWorkflow(true)
	if err != nil {
		t.Fatalf("ListProjectsWithWorkflow(true) error = %v", err)
	}
	if len(projects) != 1 || !projects[0].WorkflowEnabled {
		t.Fatalf("ListProjectsWithWorkflow(true) projects = %#v", projects)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/projects?workflow=true" {
		t.Fatalf("ListProjectsWithWorkflow request = %s %s", gotMethod, gotPath)
	}

	triggered, err := client.TriggerProjectRun("project-123")
	if err != nil {
		t.Fatalf("TriggerProjectRun() error = %v", err)
	}
	if triggered.RunNumber != 11 || triggered.Run.ID != "issue-1" {
		t.Fatalf("TriggerProjectRun() response = %#v", triggered)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/projects/project-123/runs/trigger" {
		t.Fatalf("TriggerProjectRun request = %s %s", gotMethod, gotPath)
	}

	runs, err := client.ListProjectRuns("project-123", 5)
	if err != nil {
		t.Fatalf("ListProjectRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].IssueNumber != 11 {
		t.Fatalf("ListProjectRuns() runs = %#v", runs)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/projects/project-123/runs?limit=5" {
		t.Fatalf("ListProjectRuns request = %s %s", gotMethod, gotPath)
	}
}
