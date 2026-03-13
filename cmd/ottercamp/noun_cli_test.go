package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGlobalCLIOptionsExtractsConnectionFlagsFromTail(t *testing.T) {
	opts, remaining, err := parseGlobalCLIOptions([]string{
		"agent", "list", "--server-url", "https://example.test", "--api-key", "test-key",
	})
	if err != nil {
		t.Fatalf("parseGlobalCLIOptions error: %v", err)
	}
	if opts.ServerURL != "https://example.test" {
		t.Fatalf("ServerURL = %q, want %q", opts.ServerURL, "https://example.test")
	}
	if opts.APIKey != "test-key" {
		t.Fatalf("APIKey = %q, want %q", opts.APIKey, "test-key")
	}
	if got := strings.Join(remaining, " "); got != "agent list" {
		t.Fatalf("remaining = %q, want %q", got, "agent list")
	}
}

func TestRunAgentCreateRequiresName(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runAgentCreate([]string{})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "agent create requires --name") {
		t.Fatalf("stderr = %q, want name validation", stderr)
	}
}

func TestRunProjectCreateRequiresName(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runProjectCreate([]string{})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "project create requires --name") {
		t.Fatalf("stderr = %q, want name validation", stderr)
	}
}

func TestRunProjectRelaunchRequiresProjectSelector(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runProjectRelaunch([]string{})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "project relaunch requires --project-id or --project") {
		t.Fatalf("stderr = %q, want project selector validation", stderr)
	}
}

func TestRunProjectArchiveRequiresProjectSelector(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runProjectArchive([]string{})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "project archive requires --project-id, --project, or --slug-prefix") {
		t.Fatalf("stderr = %q, want project selector validation", stderr)
	}
}

func TestRunProjectArchiveSlugPrefixRequiresYes(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runProjectArchive([]string{"--slug-prefix", "sam-blog"})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "project archive with --slug-prefix requires --yes") {
		t.Fatalf("stderr = %q, want confirmation validation", stderr)
	}
}

func TestRunTaskListRequiresProjectSelector(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runTaskList([]string{})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "task list requires --project-id or --project") {
		t.Fatalf("stderr = %q, want project selector validation", stderr)
	}
}

func TestRunTaskCreateRequiresTitle(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runTaskCreate([]string{"--project-id", "11111111-1111-1111-1111-111111111111"})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "task create requires --title") {
		t.Fatalf("stderr = %q, want title validation", stderr)
	}
}

func TestRunTaskQueueRequiresValidTaskID(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runTaskQueue([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "task queue requires a valid task id") {
		t.Fatalf("stderr = %q, want id validation", stderr)
	}
}

func TestRunTaskResumeRequiresValidTaskID(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runTaskResume([]string{"not-a-uuid"})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "task resume requires a valid task id") {
		t.Fatalf("stderr = %q, want id validation", stderr)
	}
}

func TestRunOrgCreateRequiresName(t *testing.T) {
	code, _, stderr := captureCommandOutput(t, func() int {
		return runOrgCreate([]string{})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "org create requires --name") {
		t.Fatalf("stderr = %q, want name validation", stderr)
	}
}

func TestRunAgentListAcceptsTrailingGlobalConnectionFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/v1/agents")
		}
		if got := strings.TrimSpace(r.Header.Get("X-API-Key")); got != "tail-key" {
			t.Fatalf("X-API-Key = %q, want %q", got, "tail-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	origServerURL := globalServerURL
	origAPIKey := globalAPIKey
	origOutput := defaultOutputMode
	origNoColor := defaultNoColor
	globalServerURL = ""
	globalAPIKey = ""
	defaultOutputMode = "table"
	defaultNoColor = true
	defer func() {
		globalServerURL = origServerURL
		globalAPIKey = origAPIKey
		defaultOutputMode = origOutput
		defaultNoColor = origNoColor
	}()

	code, _, stderr := captureCommandOutput(t, func() int {
		return run([]string{"agent", "list", "--server-url", server.URL, "--api-key", "tail-key", "--output", "quiet"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}
}

func TestRunTaskResumeCallsResumeEndpoint(t *testing.T) {
	taskID := "11111111-1111-1111-1111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tasks/"+taskID+"/resume" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/v1/tasks/"+taskID+"/resume")
		}
		if got := strings.TrimSpace(r.Header.Get("X-API-Key")); got != "resume-key" {
			t.Fatalf("X-API-Key = %q, want %q", got, "resume-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"` + taskID + `","task_number":7,"title":"Resume task","work_status":"queued"}}`))
	}))
	defer server.Close()

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runTaskResume([]string{"--server-url", server.URL, "--api-key", "resume-key", "--output", "quiet", taskID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != taskID {
		t.Fatalf("stdout = %q, want %q", got, taskID)
	}
}

func TestRunProjectRelaunchCallsRelaunchEndpoint(t *testing.T) {
	projectID := "11111111-1111-1111-1111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/"+projectID+"/relaunch" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/v1/projects/"+projectID+"/relaunch")
		}
		if got := strings.TrimSpace(r.Header.Get("X-API-Key")); got != "relaunch-key" {
			t.Fatalf("X-API-Key = %q, want %q", got, "relaunch-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"` + projectID + `","slug":"sam-blog-restart","display_name":"Sam.blog Restart","delivery_mode":"gated","status":"active"}}`))
	}))
	defer server.Close()

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runProjectRelaunch([]string{"--server-url", server.URL, "--api-key", "relaunch-key", "--output", "quiet", "--project-id", projectID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != projectID {
		t.Fatalf("stdout = %q, want %q", got, projectID)
	}
}

func TestRunProjectArchiveCallsArchiveEndpoint(t *testing.T) {
	projectID := "11111111-1111-1111-1111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/"+projectID+"/archive" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/v1/projects/"+projectID+"/archive")
		}
		if got := strings.TrimSpace(r.Header.Get("X-API-Key")); got != "archive-key" {
			t.Fatalf("X-API-Key = %q, want %q", got, "archive-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"` + projectID + `","slug":"sam-blog","display_name":"Sam.blog","delivery_mode":"gated","status":"archived"}}`))
	}))
	defer server.Close()

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runProjectArchive([]string{"--server-url", server.URL, "--api-key", "archive-key", "--output", "quiet", "--project-id", projectID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != projectID {
		t.Fatalf("stdout = %q, want %q", got, projectID)
	}
}

func TestRunProjectArchiveSlugPrefixCallsListAndArchiveEndpoints(t *testing.T) {
	projectIDs := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
	}
	archiveCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			if got := r.URL.Query().Get("slug_prefix"); got != "sam-blog" {
				t.Fatalf("slug_prefix = %q, want %q", got, "sam-blog")
			}
			if got := r.URL.Query().Get("status"); got != "active" {
				t.Fatalf("status = %q, want %q", got, "active")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"` + projectIDs[0] + `","slug":"sam-blog-1","display_name":"Sam.blog","delivery_mode":"gated","status":"active"},{"id":"` + projectIDs[1] + `","slug":"sam-blog-2","display_name":"Sam.blog","delivery_mode":"gated","status":"active"}]}`))
		case "/v1/projects/" + projectIDs[0] + "/archive", "/v1/projects/" + projectIDs[1] + "/archive":
			archiveCalls++
			w.Header().Set("Content-Type", "application/json")
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/projects/"), "/archive")
			_, _ = w.Write([]byte(`{"data":{"id":"` + id + `","slug":"sam-blog","display_name":"Sam.blog","delivery_mode":"gated","status":"archived"}}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runProjectArchive([]string{"--server-url", server.URL, "--api-key", "archive-key", "--output", "quiet", "--slug-prefix", "sam-blog", "--yes"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}
	lines := strings.Fields(strings.TrimSpace(stdout))
	if len(lines) != 2 {
		t.Fatalf("stdout lines = %q, want 2 ids", stdout)
	}
	if archiveCalls != 2 {
		t.Fatalf("archiveCalls = %d, want 2", archiveCalls)
	}
}
