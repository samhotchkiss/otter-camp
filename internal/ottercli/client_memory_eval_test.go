package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryEvaluationClientMethodsUseExpectedPaths(t *testing.T) {
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

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	latest, err := client.GetLatestMemoryEvaluation()
	if err != nil {
		t.Fatalf("GetLatestMemoryEvaluation() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/memory/evaluations/latest" {
		t.Fatalf("GetLatestMemoryEvaluation request = %s %s", gotMethod, gotPath)
	}
	if latest["run"] == nil {
		t.Fatalf("GetLatestMemoryEvaluation() expected run payload")
	}

	runs, err := client.ListMemoryEvaluations(5)
	if err != nil {
		t.Fatalf("ListMemoryEvaluations() error = %v", err)
	}
	if gotMethod != http.MethodGet || !strings.HasPrefix(gotPath, "/api/memory/evaluations/runs?") {
		t.Fatalf("ListMemoryEvaluations request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotPath, "limit=5") {
		t.Fatalf("ListMemoryEvaluations query mismatch: %s", gotPath)
	}
	if runs["total"] != float64(1) {
		t.Fatalf("ListMemoryEvaluations() total = %v, want 1", runs["total"])
	}

	runResp, err := client.RunMemoryEvaluation("/tmp/eval.jsonl")
	if err != nil {
		t.Fatalf("RunMemoryEvaluation() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/memory/evaluations/run" {
		t.Fatalf("RunMemoryEvaluation request = %s %s", gotMethod, gotPath)
	}
	if gotBody["fixture_path"] != "/tmp/eval.jsonl" {
		t.Fatalf("RunMemoryEvaluation payload fixture_path = %v", gotBody["fixture_path"])
	}
	if runResp["id"] != "eval-2" {
		t.Fatalf("RunMemoryEvaluation() id = %v, want eval-2", runResp["id"])
	}

	tuneResp, err := client.TuneMemoryEvaluation(true)
	if err != nil {
		t.Fatalf("TuneMemoryEvaluation() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/memory/evaluations/tune" {
		t.Fatalf("TuneMemoryEvaluation request = %s %s", gotMethod, gotPath)
	}
	if gotBody["apply"] != true {
		t.Fatalf("TuneMemoryEvaluation payload apply = %v, want true", gotBody["apply"])
	}
	if tuneResp["attempt_id"] != "attempt-1" {
		t.Fatalf("TuneMemoryEvaluation() attempt_id = %v, want attempt-1", tuneResp["attempt_id"])
	}
}
