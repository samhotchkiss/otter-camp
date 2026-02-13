package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
	"github.com/stretchr/testify/require"
)

func TestHandleJobs(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs":
			_, _ = w.Write([]byte(`{"items":[{"id":"job-1","org_id":"org-1","agent_id":"` + agentID + `","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"status":"active","run_count":1,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}],"total":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs":
			_, _ = w.Write([]byte(`{"id":"job-1","org_id":"org-1","agent_id":"` + agentID + `","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"status":"active","run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job-1/pause":
			_, _ = w.Write([]byte(`{"id":"job-1","status":"paused","org_id":"org-1","agent_id":"` + agentID + `","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job-1/resume":
			_, _ = w.Write([]byte(`{"id":"job-1","status":"active","org_id":"org-1","agent_id":"` + agentID + `","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/job-1/run":
			_, _ = w.Write([]byte(`{"id":"job-1","status":"active","org_id":"org-1","agent_id":"` + agentID + `","name":"Heartbeat","schedule_kind":"interval","payload_kind":"message","payload_text":"ping","enabled":true,"run_count":0,"error_count":0,"max_failures":5,"consecutive_failures":0,"timezone":"UTC","created_at":"2026-02-12T00:00:00Z","updated_at":"2026-02-12T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/job-1/runs":
			_, _ = w.Write([]byte(`{"items":[{"id":"run-1","job_id":"job-1","org_id":"org-1","status":"success","started_at":"2026-02-12T00:00:10Z","created_at":"2026-02-12T00:00:10Z","payload_text":"ping"}],"total":1}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/jobs/job-1":
			_, _ = w.Write([]byte(`{"deleted":true,"id":"job-1"}`))
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

	handleJobs([]string{"list", "--agent", agentID, "--limit", "10", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/v1/jobs?"))
	require.Contains(t, gotPath, "agent_id="+agentID)
	require.Contains(t, gotPath, "limit=10")
	mu.Unlock()

	handleJobs([]string{
		"create",
		"--agent", agentID,
		"--name", "Heartbeat",
		"--every", "30s",
		"--payload", "ping",
		"--json",
	})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/v1/jobs", gotPath)
	require.Equal(t, "Heartbeat", gotBody["name"])
	require.Equal(t, "interval", gotBody["schedule_kind"])
	require.Equal(t, float64(30000), gotBody["interval_ms"])
	require.Equal(t, "message", gotBody["payload_kind"])
	require.Equal(t, "ping", gotBody["payload_text"])
	mu.Unlock()

	handleJobs([]string{"pause", "job-1", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/v1/jobs/job-1/pause", gotPath)
	mu.Unlock()

	handleJobs([]string{"resume", "job-1", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/v1/jobs/job-1/resume", gotPath)
	mu.Unlock()

	handleJobs([]string{"run", "job-1", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/v1/jobs/job-1/run", gotPath)
	mu.Unlock()

	handleJobs([]string{"history", "job-1", "--limit", "5", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodGet, gotMethod)
	require.True(t, strings.HasPrefix(gotPath, "/api/v1/jobs/job-1/runs?"))
	require.Contains(t, gotPath, "limit=5")
	mu.Unlock()

	handleJobs([]string{"delete", "job-1", "--json"})
	mu.Lock()
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api/v1/jobs/job-1", gotPath)
	mu.Unlock()
}

func TestHandleJobsScheduleValidation(t *testing.T) {
	spec, err := resolveJobCreateSchedule("*/10 * * * *", "", "")
	require.NoError(t, err)
	require.Equal(t, "cron", spec.Kind)
	require.NotNil(t, spec.CronExpr)
	require.Equal(t, "*/10 * * * *", *spec.CronExpr)

	spec, err = resolveJobCreateSchedule("", "30s", "")
	require.NoError(t, err)
	require.Equal(t, "interval", spec.Kind)
	require.NotNil(t, spec.IntervalMS)
	require.Equal(t, int64(30000), *spec.IntervalMS)

	spec, err = resolveJobCreateSchedule("", "", "2026-02-14T01:30:00Z")
	require.NoError(t, err)
	require.Equal(t, "once", spec.Kind)
	require.NotNil(t, spec.RunAt)
	require.Equal(t, "2026-02-14T01:30:00Z", *spec.RunAt)

	_, err = resolveJobCreateSchedule("", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must provide exactly one")

	_, err = resolveJobCreateSchedule("* * * * *", "30s", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must provide exactly one")

	_, err = resolveJobCreateSchedule("", "nonsense", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--every")

	_, err = resolveJobCreateSchedule("", "", "not-a-time")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--at")
}
