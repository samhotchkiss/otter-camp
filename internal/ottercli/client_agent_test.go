package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentClientMethodsUseExpectedPathsAndPayloads(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/a1/whoami":
			_, _ = w.Write([]byte(`{"profile":"compact","agent":{"id":"a1","name":"Derek"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/agents":
			_, _ = w.Write([]byte(`{"message":"command queued","command_id":"cmd-1"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/a1":
			_, _ = w.Write([]byte(`{"id":"a1","display_name":"Derek","role":"Engineering Lead"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/agents/a1/retire":
			_, _ = w.Write([]byte(`{"status":"queued"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/agents/retire/project/project-1":
			_, _ = w.Write([]byte(`{"ok":true,"project_id":"project-1","total":2,"retired":2,"failed":0}`))
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

	whoami, err := client.AgentWhoAmI("a1", "agent:chameleon:oc:a1", "compact")
	if err != nil {
		t.Fatalf("AgentWhoAmI() error = %v", err)
	}
	if whoami["profile"] != "compact" {
		t.Fatalf("AgentWhoAmI() profile = %v, want compact", whoami["profile"])
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("AgentWhoAmI method = %s, want GET", gotMethod)
	}
	if !strings.HasPrefix(gotPath, "/api/agents/a1/whoami?") {
		t.Fatalf("AgentWhoAmI path = %s", gotPath)
	}
	if !strings.Contains(gotPath, "profile=compact") || !strings.Contains(gotPath, "session_key=agent%3Achameleon%3Aoc%3Aa1") {
		t.Fatalf("AgentWhoAmI query missing expected params: %s", gotPath)
	}

	created, err := client.CreateAgent(map[string]any{
		"slot":         "derek",
		"display_name": "Derek",
		"model":        "gpt-5.2-codex",
		"role":         "Engineering Lead",
	})
	if err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}
	if created["command_id"] != "cmd-1" {
		t.Fatalf("CreateAgent() command_id = %v, want cmd-1", created["command_id"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/admin/agents" {
		t.Fatalf("CreateAgent request = %s %s", gotMethod, gotPath)
	}
	if gotBody["slot"] != "derek" {
		t.Fatalf("CreateAgent payload slot = %v", gotBody["slot"])
	}
	if gotBody["role"] != "Engineering Lead" {
		t.Fatalf("CreateAgent payload role = %v", gotBody["role"])
	}

	updated, err := client.UpdateAgent("a1", map[string]any{"display_name": "Derek"})
	if err != nil {
		t.Fatalf("UpdateAgent() error = %v", err)
	}
	if updated["id"] != "a1" {
		t.Fatalf("UpdateAgent() id = %v, want a1", updated["id"])
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/agents/a1" {
		t.Fatalf("UpdateAgent request = %s %s", gotMethod, gotPath)
	}
	if gotBody["display_name"] != "Derek" {
		t.Fatalf("UpdateAgent payload display_name = %v", gotBody["display_name"])
	}

	if err := client.ArchiveAgent("a1"); err != nil {
		t.Fatalf("ArchiveAgent() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/admin/agents/a1/retire" {
		t.Fatalf("ArchiveAgent request = %s %s", gotMethod, gotPath)
	}

	bulkArchived, err := client.ArchiveProjectEphemeralAgents("project-1")
	if err != nil {
		t.Fatalf("ArchiveProjectEphemeralAgents() error = %v", err)
	}
	if bulkArchived["retired"] != float64(2) {
		t.Fatalf("ArchiveProjectEphemeralAgents retired = %v, want 2", bulkArchived["retired"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/admin/agents/retire/project/project-1" {
		t.Fatalf("ArchiveProjectEphemeralAgents request = %s %s", gotMethod, gotPath)
	}
}
