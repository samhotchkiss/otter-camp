package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
	"github.com/stretchr/testify/require"
)

func TestHandleMemory(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var mu sync.Mutex
	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod = r.Method
		gotPath = r.URL.String()
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/memory/entries":
			_, _ = w.Write([]byte(`{"id":"me-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/entries":
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/search":
			_, _ = w.Write([]byte(`{"items":[{"id":"me-1"}],"total":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/recall":
			_, _ = w.Write([]byte(`{"context":"[RECALLED CONTEXT]"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/memory/entries/me-1":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	require.NoError(t, ottercli.SaveConfig(ottercli.Config{
		APIBaseURL: srv.URL,
		Token:      "token-1",
		DefaultOrg: "org-1",
	}))

	agentID := "00000000-0000-0000-0000-000000000111"
	handleMemory([]string{"create", "--agent", agentID, "--kind", "decision", "--title", "Adopt recall", "--json", "ship it"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/memory/entries", gotPath)
	mu.Unlock()

	handleMemory([]string{"list", "--agent", agentID, "--limit", "5", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/memory/entries?"))
	require.Contains(t, gotPath, "agent_id="+agentID)
	mu.Unlock()

	handleMemory([]string{"search", "--agent", agentID, "--limit", "5", "--json", "recall"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/memory/search?"))
	require.Contains(t, gotPath, "q=recall")
	mu.Unlock()

	handleMemory([]string{"recall", "--agent", agentID, "--max-results", "3", "--json", "recall"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/memory/recall?"))
	require.Contains(t, gotPath, "max_results=3")
	mu.Unlock()

	handleMemory([]string{"delete", "--json", "me-1"})
	mu.Lock()
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api/memory/entries/me-1", gotPath)
	mu.Unlock()
}

func TestHandleKnowledge(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var mu sync.Mutex
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
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

	require.NoError(t, ottercli.SaveConfig(ottercli.Config{
		APIBaseURL: srv.URL,
		Token:      "token-1",
		DefaultOrg: "org-1",
	}))

	handleKnowledge([]string{"list", "--limit", "50", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/knowledge?"))
	require.Contains(t, gotPath, "limit=50")
	mu.Unlock()

	importPath := filepath.Join(t.TempDir(), "knowledge.json")
	require.NoError(t, os.WriteFile(importPath, []byte(`[{"title":"k1","content":"body","created_by":"sam"}]`), 0o644))
	handleKnowledge([]string{"import", "--json", importPath})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/knowledge/import", gotPath)
	entries, ok := gotBody["entries"].([]any)
	require.True(t, ok)
	require.Len(t, entries, 1)
	mu.Unlock()
}

func TestHandleMemoryWriteRead(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var mu sync.Mutex
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	agentID := "00000000-0000-0000-0000-000000000111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/"+agentID+"/memory":
			_, _ = w.Write([]byte(`{"id":"m1","agent_id":"` + agentID + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/"+agentID+"/memory":
			_, _ = w.Write([]byte(`{"agent_id":"` + agentID + `","daily":[{"id":"m1"}],"long_term":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	require.NoError(t, ottercli.SaveConfig(ottercli.Config{
		APIBaseURL: srv.URL,
		Token:      "token-1",
		DefaultOrg: "org-1",
	}))

	handleMemory([]string{"write", "--agent", agentID, "--daily", "--date", "2026-02-10", "--json", "Daily note"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/agents/"+agentID+"/memory", gotPath)
	require.Equal(t, "daily", gotBody["kind"])
	require.Equal(t, "Daily note", gotBody["content"])
	require.Equal(t, "2026-02-10", gotBody["date"])
	mu.Unlock()

	handleMemory([]string{"read", "--agent", agentID, "--days", "3", "--include-long-term=false", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/agents/"+agentID+"/memory?"))
	require.Contains(t, gotPath, "days=3")
	require.NotContains(t, gotPath, "include_long_term=true")
	mu.Unlock()
}
