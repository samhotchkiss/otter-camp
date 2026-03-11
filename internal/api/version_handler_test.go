package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionHandlerReturnsBuildInfo(t *testing.T) {
	handler := NewVersionHandler(BuildInfo{
		Version:     "0.1.0",
		Commit:      "a3f8c1d",
		BuiltAt:     "2024-01-15T10:00:00Z",
		RepoVersion: "2258",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	data := payload["data"].(map[string]any)
	if got := data["version"]; got != "0.1.0" {
		t.Fatalf("version = %v, want %q", got, "0.1.0")
	}
	if got := data["commit"]; got != "a3f8c1d" {
		t.Fatalf("commit = %v, want %q", got, "a3f8c1d")
	}
	if got := data["built_at"]; got != "2024-01-15T10:00:00Z" {
		t.Fatalf("built_at = %v, want %q", got, "2024-01-15T10:00:00Z")
	}
	if got := data["repo_version"]; got != "2258" {
		t.Fatalf("repo_version = %v, want %q", got, "2258")
	}
}
