package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStructuredMemoryAndKnowledgeClientMethodsUseExpectedPaths(t *testing.T) {
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/memory/entries":
			_, _ = w.Write([]byte(`{"id":"me-1","kind":"decision"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/entries":
			_, _ = w.Write([]byte(`{"items":[{"id":"me-1"}],"total":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/search":
			_, _ = w.Write([]byte(`{"items":[{"id":"me-1"}],"total":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/recall":
			_, _ = w.Write([]byte(`{"context":"[RECALLED CONTEXT]\\n- item"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/memory/entries/me-1":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/knowledge":
			_, _ = w.Write([]byte(`{"items":[{"id":"k1"}],"total":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/knowledge/import":
			_, _ = w.Write([]byte(`{"inserted":1}`))
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

	created, err := client.CreateMemoryEntry(map[string]any{
		"agent_id": "a1",
		"kind":     "decision",
		"title":    "t",
		"content":  "c",
	})
	if err != nil {
		t.Fatalf("CreateMemoryEntry() error = %v", err)
	}
	if created["id"] != "me-1" {
		t.Fatalf("CreateMemoryEntry() id = %v, want me-1", created["id"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/memory/entries" {
		t.Fatalf("CreateMemoryEntry request = %s %s", gotMethod, gotPath)
	}

	listed, err := client.ListMemoryEntries("a1", "decision", 5, 0)
	if err != nil {
		t.Fatalf("ListMemoryEntries() error = %v", err)
	}
	if listed["total"] != float64(1) {
		t.Fatalf("ListMemoryEntries() total = %v, want 1", listed["total"])
	}
	if !strings.HasPrefix(gotPath, "/api/memory/entries?") || !strings.Contains(gotPath, "agent_id=a1") {
		t.Fatalf("ListMemoryEntries path = %s", gotPath)
	}

	searched, err := client.SearchMemoryEntries("a1", "decision", 7)
	if err != nil {
		t.Fatalf("SearchMemoryEntries() error = %v", err)
	}
	if searched["total"] != float64(1) {
		t.Fatalf("SearchMemoryEntries() total = %v, want 1", searched["total"])
	}
	if !strings.HasPrefix(gotPath, "/api/memory/search?") || !strings.Contains(gotPath, "q=decision") {
		t.Fatalf("SearchMemoryEntries path = %s", gotPath)
	}

	recall, err := client.RecallMemory("a1", "decision", 4)
	if err != nil {
		t.Fatalf("RecallMemory() error = %v", err)
	}
	if recall["context"] == nil {
		t.Fatalf("RecallMemory() context missing")
	}
	if !strings.HasPrefix(gotPath, "/api/memory/recall?") || !strings.Contains(gotPath, "max_results=4") {
		t.Fatalf("RecallMemory path = %s", gotPath)
	}

	recall, err = client.RecallMemoryWithQuality("a1", "decision", 6, 0.81, 1234)
	if err != nil {
		t.Fatalf("RecallMemoryWithQuality() error = %v", err)
	}
	if recall["context"] == nil {
		t.Fatalf("RecallMemoryWithQuality() context missing")
	}
	if !strings.HasPrefix(gotPath, "/api/memory/recall?") ||
		!strings.Contains(gotPath, "max_results=6") ||
		!strings.Contains(gotPath, "min_relevance=0.81") ||
		!strings.Contains(gotPath, "max_chars=1234") {
		t.Fatalf("RecallMemoryWithQuality path = %s", gotPath)
	}

	if err := client.DeleteMemoryEntry("me-1"); err != nil {
		t.Fatalf("DeleteMemoryEntry() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/memory/entries/me-1" {
		t.Fatalf("DeleteMemoryEntry request = %s %s", gotMethod, gotPath)
	}

	knowledge, err := client.ListKnowledge(50)
	if err != nil {
		t.Fatalf("ListKnowledge() error = %v", err)
	}
	if knowledge["total"] != float64(1) {
		t.Fatalf("ListKnowledge() total = %v, want 1", knowledge["total"])
	}
	if !strings.HasPrefix(gotPath, "/api/knowledge?") || !strings.Contains(gotPath, "limit=50") {
		t.Fatalf("ListKnowledge path = %s", gotPath)
	}

	imported, err := client.ImportKnowledge([]map[string]any{
		{
			"title":      "k1",
			"content":    "body",
			"created_by": "sam",
		},
	})
	if err != nil {
		t.Fatalf("ImportKnowledge() error = %v", err)
	}
	if imported["inserted"] != float64(1) {
		t.Fatalf("ImportKnowledge() inserted = %v, want 1", imported["inserted"])
	}
	if gotMethod != http.MethodPost || gotPath != "/api/knowledge/import" {
		t.Fatalf("ImportKnowledge request = %s %s", gotMethod, gotPath)
	}
}
