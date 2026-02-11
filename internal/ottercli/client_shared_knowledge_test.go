package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedKnowledgeClientMethodsUseExpectedPathsAndPayloads(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/shared-knowledge":
			_, _ = w.Write([]byte(`{"items":[{"id":"k1"}],"total":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/shared-knowledge/search":
			_, _ = w.Write([]byte(`{"items":[{"id":"k2"}],"total":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/shared-knowledge":
			_, _ = w.Write([]byte(`{"id":"k3","title":"Shared rule"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/shared-knowledge/k3/confirm":
			_, _ = w.Write([]byte(`{"id":"k3","confirmations":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/shared-knowledge/k3/contradict":
			_, _ = w.Write([]byte(`{"id":"k3","contradictions":1}`))
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

	listResponse, err := client.ListSharedKnowledge("a1", 5)
	if err != nil {
		t.Fatalf("ListSharedKnowledge() error = %v", err)
	}
	if listResponse["total"] != float64(1) {
		t.Fatalf("ListSharedKnowledge() total = %v, want 1", listResponse["total"])
	}
	if gotMethod != http.MethodGet || !strings.HasPrefix(gotPath, "/api/shared-knowledge?") {
		t.Fatalf("ListSharedKnowledge request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotPath, "agent_id=a1") || !strings.Contains(gotPath, "limit=5") {
		t.Fatalf("ListSharedKnowledge query mismatch: %s", gotPath)
	}

	searchResponse, err := client.SearchSharedKnowledge("shared rule", 4, 0.6, []string{"decision"}, []string{"active"})
	if err != nil {
		t.Fatalf("SearchSharedKnowledge() error = %v", err)
	}
	if searchResponse["total"] != float64(1) {
		t.Fatalf("SearchSharedKnowledge() total = %v, want 1", searchResponse["total"])
	}
	if gotMethod != http.MethodGet || !strings.HasPrefix(gotPath, "/api/shared-knowledge/search?") {
		t.Fatalf("SearchSharedKnowledge request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotPath, "q=shared+rule") || !strings.Contains(gotPath, "min_quality=0.6") {
		t.Fatalf("SearchSharedKnowledge query mismatch: %s", gotPath)
	}

	created, err := client.CreateSharedKnowledge(map[string]any{
		"source_agent_id": "a1",
		"kind":            "decision",
		"title":           "Shared rule",
		"content":         "Always include links.",
	})
	if err != nil {
		t.Fatalf("CreateSharedKnowledge() error = %v", err)
	}
	if created["id"] != "k3" {
		t.Fatalf("CreateSharedKnowledge() id = %v, want k3", created["id"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/shared-knowledge" {
		t.Fatalf("CreateSharedKnowledge request = %s %s", gotMethod, gotPath)
	}
	if gotBody["title"] != "Shared rule" {
		t.Fatalf("CreateSharedKnowledge payload title = %v", gotBody["title"])
	}

	confirmed, err := client.ConfirmSharedKnowledge("k3")
	if err != nil {
		t.Fatalf("ConfirmSharedKnowledge() error = %v", err)
	}
	if confirmed["confirmations"] != float64(1) {
		t.Fatalf("ConfirmSharedKnowledge() confirmations = %v, want 1", confirmed["confirmations"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/shared-knowledge/k3/confirm" {
		t.Fatalf("ConfirmSharedKnowledge request = %s %s", gotMethod, gotPath)
	}

	contradicted, err := client.ContradictSharedKnowledge("k3")
	if err != nil {
		t.Fatalf("ContradictSharedKnowledge() error = %v", err)
	}
	if contradicted["contradictions"] != float64(1) {
		t.Fatalf("ContradictSharedKnowledge() contradictions = %v, want 1", contradicted["contradictions"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/shared-knowledge/k3/contradict" {
		t.Fatalf("ContradictSharedKnowledge request = %s %s", gotMethod, gotPath)
	}
}

func TestSharedKnowledgeClientMethodsValidation(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.example.com",
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    &http.Client{},
	}

	_, err := client.ListSharedKnowledge("", 5)
	if err == nil || !strings.Contains(err.Error(), "agent id is required") {
		t.Fatalf("ListSharedKnowledge() error = %v, want agent id error", err)
	}

	_, err = client.SearchSharedKnowledge("", 5, 0.5, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("SearchSharedKnowledge() error = %v, want query error", err)
	}

	_, err = client.SearchSharedKnowledge("q", 5, 1.2, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "min_quality must be between 0 and 1") {
		t.Fatalf("SearchSharedKnowledge() error = %v, want min_quality error", err)
	}

	_, err = client.ConfirmSharedKnowledge("")
	if err == nil || !strings.Contains(err.Error(), "knowledge id is required") {
		t.Fatalf("ConfirmSharedKnowledge() error = %v, want id error", err)
	}

	_, err = client.ContradictSharedKnowledge("")
	if err == nil || !strings.Contains(err.Error(), "knowledge id is required") {
		t.Fatalf("ContradictSharedKnowledge() error = %v, want id error", err)
	}
}
