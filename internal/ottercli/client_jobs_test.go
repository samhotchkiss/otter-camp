package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAgentJobsMethodsUseExpectedPathsAndPayloads(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs":
			_, _ = w.Write([]byte(`{"items":[{"id":"job-1","org_id":"org-1","agent_id":"agent-1","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"status":"active","run_count":2,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}],"total":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs":
			_, _ = w.Write([]byte(`{"id":"job-1","org_id":"org-1","agent_id":"agent-1","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"status":"active","run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job-1/pause":
			_, _ = w.Write([]byte(`{"id":"job-1","status":"paused","org_id":"org-1","agent_id":"agent-1","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job-1/resume":
			_, _ = w.Write([]byte(`{"id":"job-1","status":"active","org_id":"org-1","agent_id":"agent-1","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job-1/run":
			_, _ = w.Write([]byte(`{"id":"job-1","status":"active","org_id":"org-1","agent_id":"agent-1","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/job-1/runs":
			_, _ = w.Write([]byte(`{"items":[{"id":"run-1","job_id":"job-1","org_id":"org-1","status":"success","started_at":"2026-02-12T00:00:10Z","created_at":"2026-02-12T00:00:10Z","payload_text":"ping"}],"total":1}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/jobs/job-1":
			_, _ = w.Write([]byte(`{"deleted":true,"id":"job-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/import/openclaw-cron":
			_, _ = w.Write([]byte(`{"total":2,"imported":1,"updated":1,"skipped":0,"warnings":[]}`))
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

	listed, err := client.ListJobs(map[string]string{
		"agent_id": "agent-1",
		"limit":    "10",
	})
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 {
		t.Fatalf("ListJobs() response = %#v", listed)
	}
	if gotMethod != http.MethodGet || !strings.HasPrefix(gotPath, "/api/v1/jobs?") {
		t.Fatalf("ListJobs request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotPath, "agent_id=agent-1") || !strings.Contains(gotPath, "limit=10") {
		t.Fatalf("ListJobs query mismatch: %s", gotPath)
	}

	created, err := client.CreateJob(map[string]any{
		"agent_id":      "agent-1",
		"name":          "Heartbeat",
		"schedule_kind": "interval",
		"interval_ms":   30000,
		"payload_kind":  "message",
		"payload_text":  "ping",
	})
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if created.ID != "job-1" {
		t.Fatalf("CreateJob() id = %q, want job-1", created.ID)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/jobs" {
		t.Fatalf("CreateJob request = %s %s", gotMethod, gotPath)
	}
	if gotBody["schedule_kind"] != "interval" || gotBody["interval_ms"] != float64(30000) {
		t.Fatalf("CreateJob payload = %#v", gotBody)
	}

	paused, err := client.PauseJob("job-1")
	if err != nil {
		t.Fatalf("PauseJob() error = %v", err)
	}
	if paused.Status != "paused" {
		t.Fatalf("PauseJob() status = %q, want paused", paused.Status)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/jobs/job-1/pause" {
		t.Fatalf("PauseJob request = %s %s", gotMethod, gotPath)
	}

	resumed, err := client.ResumeJob("job-1")
	if err != nil {
		t.Fatalf("ResumeJob() error = %v", err)
	}
	if resumed.Status != "active" {
		t.Fatalf("ResumeJob() status = %q, want active", resumed.Status)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/jobs/job-1/resume" {
		t.Fatalf("ResumeJob request = %s %s", gotMethod, gotPath)
	}

	triggered, err := client.RunJobNow("job-1")
	if err != nil {
		t.Fatalf("RunJobNow() error = %v", err)
	}
	if triggered.ID != "job-1" {
		t.Fatalf("RunJobNow() id = %q, want job-1", triggered.ID)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/jobs/job-1/run" {
		t.Fatalf("RunJobNow request = %s %s", gotMethod, gotPath)
	}

	runs, err := client.ListJobRuns("job-1", 5)
	if err != nil {
		t.Fatalf("ListJobRuns() error = %v", err)
	}
	if runs.Total != 1 || len(runs.Items) != 1 {
		t.Fatalf("ListJobRuns() response = %#v", runs)
	}
	if gotMethod != http.MethodGet || !strings.HasPrefix(gotPath, "/api/v1/jobs/job-1/runs?") {
		t.Fatalf("ListJobRuns request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotPath, "limit=5") {
		t.Fatalf("ListJobRuns query mismatch: %s", gotPath)
	}

	deleted, err := client.DeleteJob("job-1")
	if err != nil {
		t.Fatalf("DeleteJob() error = %v", err)
	}
	if deleted["deleted"] != true {
		t.Fatalf("DeleteJob() payload = %#v", deleted)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/jobs/job-1" {
		t.Fatalf("DeleteJob request = %s %s", gotMethod, gotPath)
	}

	imported, err := client.ImportOpenClawCronJobs()
	if err != nil {
		t.Fatalf("ImportOpenClawCronJobs() error = %v", err)
	}
	if imported.Total != 2 || imported.Imported != 1 || imported.Updated != 1 {
		t.Fatalf("ImportOpenClawCronJobs() response = %#v", imported)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/jobs/import/openclaw-cron" {
		t.Fatalf("ImportOpenClawCronJobs request = %s %s", gotMethod, gotPath)
	}
}

func TestClientAgentJobsValidation(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.example.com",
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    &http.Client{},
	}

	_, err := client.CreateJob(nil)
	if err == nil || !strings.Contains(err.Error(), "job payload is required") {
		t.Fatalf("CreateJob(nil) error = %v", err)
	}

	_, err = client.PauseJob("")
	if err == nil || !strings.Contains(err.Error(), "job id is required") {
		t.Fatalf("PauseJob(\"\") error = %v", err)
	}

	_, err = client.ResumeJob("")
	if err == nil || !strings.Contains(err.Error(), "job id is required") {
		t.Fatalf("ResumeJob(\"\") error = %v", err)
	}

	_, err = client.RunJobNow("")
	if err == nil || !strings.Contains(err.Error(), "job id is required") {
		t.Fatalf("RunJobNow(\"\") error = %v", err)
	}

	_, err = client.ListJobRuns("", 5)
	if err == nil || !strings.Contains(err.Error(), "job id is required") {
		t.Fatalf("ListJobRuns(\"\") error = %v", err)
	}

	_, err = client.DeleteJob("")
	if err == nil || !strings.Contains(err.Error(), "job id is required") {
		t.Fatalf("DeleteJob(\"\") error = %v", err)
	}
}
