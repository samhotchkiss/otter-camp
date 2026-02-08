package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
	p := Project{Name: "My Cool Project"}
	if got := p.Slug(); got != "my-cool-project" {
		t.Errorf("Slug() = %q, want %q", got, "my-cool-project")
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
			_, _ = w.Write([]byte(`{"id":"issue-1","project_id":"project-123","issue_number":12,"title":"Test issue","state":"open","origin":"local","approval_state":"draft","work_status":"queued","priority":"P2"}`))
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
		"title":    "Test issue",
		"priority": "P2",
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
