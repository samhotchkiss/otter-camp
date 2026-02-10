package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryClientMethodsUseExpectedPathsAndPayloads(t *testing.T) {
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/a1/memory":
			_, _ = w.Write([]byte(`{"id":"m1","agent_id":"a1","kind":"daily","content":"did work"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/a1/memory":
			_, _ = w.Write([]byte(`{"agent_id":"a1","daily":[{"id":"m1"}],"long_term":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/a1/memory/search":
			_, _ = w.Write([]byte(`{"items":[{"id":"m2","kind":"note"}],"total":1}`))
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

	written, err := client.WriteAgentMemory("a1", map[string]any{
		"kind":    "daily",
		"content": "did work",
	})
	if err != nil {
		t.Fatalf("WriteAgentMemory() error = %v", err)
	}
	if written["id"] != "m1" {
		t.Fatalf("WriteAgentMemory() id = %v, want m1", written["id"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/agents/a1/memory" {
		t.Fatalf("WriteAgentMemory request = %s %s", gotMethod, gotPath)
	}
	if gotBody["kind"] != "daily" {
		t.Fatalf("WriteAgentMemory payload kind = %v", gotBody["kind"])
	}

	read, err := client.ReadAgentMemory("a1", 3, true)
	if err != nil {
		t.Fatalf("ReadAgentMemory() error = %v", err)
	}
	if read["agent_id"] != "a1" {
		t.Fatalf("ReadAgentMemory() agent_id = %v, want a1", read["agent_id"])
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("ReadAgentMemory method = %s, want GET", gotMethod)
	}
	if !strings.HasPrefix(gotPath, "/api/agents/a1/memory?") {
		t.Fatalf("ReadAgentMemory path = %s", gotPath)
	}
	if !strings.Contains(gotPath, "days=3") || !strings.Contains(gotPath, "include_long_term=true") {
		t.Fatalf("ReadAgentMemory query missing expected params: %s", gotPath)
	}

	search, err := client.SearchAgentMemory("a1", "work", 5)
	if err != nil {
		t.Fatalf("SearchAgentMemory() error = %v", err)
	}
	if search["total"] != float64(1) {
		t.Fatalf("SearchAgentMemory() total = %v, want 1", search["total"])
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("SearchAgentMemory method = %s, want GET", gotMethod)
	}
	if !strings.HasPrefix(gotPath, "/api/agents/a1/memory/search?") {
		t.Fatalf("SearchAgentMemory path = %s", gotPath)
	}
	if !strings.Contains(gotPath, "q=work") || !strings.Contains(gotPath, "limit=5") {
		t.Fatalf("SearchAgentMemory query missing expected params: %s", gotPath)
	}
}
