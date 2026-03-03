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
