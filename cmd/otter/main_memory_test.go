package main

import (
	"encoding/json"
	"math"
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

func TestHandleKnowledgeSharedCommands(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents":
			_, _ = w.Write([]byte(`{"agents":[{"id":"` + agentID + `","name":"Marcus","slug":"marcus"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/shared-knowledge":
			_, _ = w.Write([]byte(`{"items":[{"id":"k1"}],"total":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/shared-knowledge/search":
			_, _ = w.Write([]byte(`{"items":[{"id":"k2"}],"total":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/shared-knowledge":
			_, _ = w.Write([]byte(`{"id":"k3"}`))
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

	require.NoError(t, ottercli.SaveConfig(ottercli.Config{
		APIBaseURL: srv.URL,
		Token:      "token-1",
		DefaultOrg: "org-1",
	}))

	handleKnowledge([]string{"list", "--agent", "marcus", "--limit", "5", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/shared-knowledge?"))
	require.Contains(t, gotPath, "agent_id="+agentID)
	require.Contains(t, gotPath, "limit=5")
	mu.Unlock()

	handleKnowledge([]string{"search", "--limit", "5", "--min-quality", "0.7", "--json", "shared rule"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/shared-knowledge/search?"))
	require.Contains(t, gotPath, "q=shared+rule")
	require.Contains(t, gotPath, "min_quality=0.7")
	mu.Unlock()

	handleKnowledge([]string{"share", "--agent", "marcus", "--title", "Rule", "--scope", "org", "--quality", "0.8", "--json", "Always link issues"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/shared-knowledge", gotPath)
	require.Equal(t, agentID, gotBody["source_agent_id"])
	require.Equal(t, "Rule", gotBody["title"])
	require.Equal(t, "Always link issues", gotBody["content"])
	mu.Unlock()

	handleKnowledge([]string{"confirm", "--json", "k3"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/shared-knowledge/k3/confirm", gotPath)
	mu.Unlock()

	handleKnowledge([]string{"contradict", "--json", "k3"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/shared-knowledge/k3/contradict", gotPath)
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

func TestHandleMemoryRecallFlags(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var mu sync.Mutex
	var gotMethod string
	var gotPath string
	agentID := "00000000-0000-0000-0000-000000000111"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod = r.Method
		gotPath = r.URL.String()
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/recall":
			_, _ = w.Write([]byte(`{"context":"[RECALLED CONTEXT]"}`))
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

	handleMemory([]string{
		"recall",
		"--agent", agentID,
		"--max-results", "4",
		"--min-relevance", "0.83",
		"--max-chars", "1234",
		"--json",
		"recall quality query",
	})

	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/memory/recall?"))
	require.Contains(t, gotPath, "max_results=4")
	require.Contains(t, gotPath, "min_relevance=0.83")
	require.Contains(t, gotPath, "max_chars=1234")
	mu.Unlock()

	err := validateMemoryRecallQualityFlags(-0.01, 100)
	require.ErrorContains(t, err, "--min-relevance must be between 0 and 1")
	err = validateMemoryRecallQualityFlags(1.01, 100)
	require.ErrorContains(t, err, "--min-relevance must be between 0 and 1")
	err = validateMemoryRecallQualityFlags(0.5, 0)
	require.ErrorContains(t, err, "--max-chars must be positive")
}

func TestHandleMemoryEvalCommands(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/evaluations/latest":
			_, _ = w.Write([]byte(`{"run":{"id":"eval-1","passed":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/memory/evaluations/runs":
			_, _ = w.Write([]byte(`{"items":[{"id":"eval-1"}],"total":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/memory/evaluations/run":
			_, _ = w.Write([]byte(`{"id":"eval-2","passed":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/memory/evaluations/tune":
			_, _ = w.Write([]byte(`{"attempt_id":"attempt-1","status":"skipped"}`))
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

	handleMemory([]string{"eval", "latest", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api/memory/evaluations/latest", gotPath)
	mu.Unlock()

	handleMemory([]string{"eval", "runs", "--limit", "5", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/memory/evaluations/runs?"))
	require.Contains(t, gotPath, "limit=5")
	mu.Unlock()

	handleMemory([]string{"eval", "run", "--fixture", "/tmp/eval.jsonl", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/memory/evaluations/run", gotPath)
	require.Equal(t, "/tmp/eval.jsonl", gotBody["fixture_path"])
	mu.Unlock()

	handleMemory([]string{"eval", "tune", "--apply", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/memory/evaluations/tune", gotPath)
	require.Equal(t, true, gotBody["apply"])
	mu.Unlock()
}

func TestHandleMemoryCreateValidatesRanges(t *testing.T) {
	err := validateMemoryCreateFlags(0, 0.5, "internal")
	require.ErrorContains(t, err, "--importance must be between 1 and 5")

	err = validateMemoryCreateFlags(6, 0.5, "internal")
	require.ErrorContains(t, err, "--importance must be between 1 and 5")

	err = validateMemoryCreateFlags(3, -0.1, "internal")
	require.ErrorContains(t, err, "--confidence must be between 0 and 1")

	err = validateMemoryCreateFlags(3, 1.1, "internal")
	require.ErrorContains(t, err, "--confidence must be between 0 and 1")

	err = validateMemoryCreateFlags(3, math.NaN(), "internal")
	require.ErrorContains(t, err, "--confidence must be between 0 and 1")

	err = validateMemoryCreateFlags(3, 0.5, "secret")
	require.ErrorContains(t, err, "--sensitivity must be one of public|internal|restricted")

	require.NoError(t, validateMemoryCreateFlags(3, 0.5, "restricted"))
}

func TestHandleKnowledgeImportRejectsOversizeFile(t *testing.T) {
	oversizedPath := filepath.Join(t.TempDir(), "oversized.json")
	require.NoError(t, os.WriteFile(oversizedPath, make([]byte, knowledgeImportMaxFileBytes+1), 0o644))

	err := validateKnowledgeImportFileSize(oversizedPath, knowledgeImportMaxFileBytes)
	require.ErrorContains(t, err, "knowledge import file exceeds")

	validPath := filepath.Join(t.TempDir(), "valid.json")
	require.NoError(t, os.WriteFile(validPath, []byte(`[]`), 0o644))
	require.NoError(t, validateKnowledgeImportFileSize(validPath, knowledgeImportMaxFileBytes))
}
