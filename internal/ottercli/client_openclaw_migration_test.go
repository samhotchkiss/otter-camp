package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientOpenClawMigrationImportAndControlEndpoints(t *testing.T) {
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/migrations/openclaw/import/agents":
			_, _ = w.Write([]byte(`{"processed":2,"inserted":1,"updated":1,"skipped":0,"active_agents":2,"inactive_agents":0,"warnings":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/migrations/openclaw/import/history/batch":
			_, _ = w.Write([]byte(`{"events_received":2,"events_processed":2,"messages_inserted":2,"rooms_created":1,"participants_added":2,"events_skipped_unknown_agent":0,"failed_items":0,"warnings":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/migrations/openclaw/status":
			_, _ = w.Write([]byte(`{"active":true,"phases":[{"migration_type":"history_backfill","status":"running","total_items":10,"processed_items":7,"failed_items":1,"current_label":"processed 7/10 events"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/migrations/openclaw/run":
			_, _ = w.Write([]byte(`{"accepted":true,"selected_phases":["history_backfill"],"started_phases":["history_backfill"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/migrations/openclaw/pause":
			_, _ = w.Write([]byte(`{"status":"paused","updated_phases":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/migrations/openclaw/resume":
			_, _ = w.Write([]byte(`{"status":"running","updated_phases":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/migrations/openclaw/reset":
			_, _ = w.Write([]byte(`{"status":"reset","paused_phases":1,"progress_rows_deleted":3,"deleted":{"chat_messages":2,"rooms":1},"total_deleted":3}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "00000000-0000-0000-0000-000000000111",
		HTTP:    srv.Client(),
	}

	agentsResult, err := client.ImportOpenClawMigrationAgents(OpenClawMigrationImportAgentsInput{
		Identities: []OpenClawMigrationImportAgentIdentity{
			{ID: "main", Name: "Frank"},
			{ID: "lori", Name: "Lori"},
		},
	})
	if err != nil {
		t.Fatalf("ImportOpenClawMigrationAgents() error = %v", err)
	}
	if agentsResult.Processed != 2 || agentsResult.Inserted != 1 || agentsResult.Updated != 1 {
		t.Fatalf("ImportOpenClawMigrationAgents() result = %#v", agentsResult)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/migrations/openclaw/import/agents" {
		t.Fatalf("ImportOpenClawMigrationAgents request = %s %s", gotMethod, gotPath)
	}
	if gotBody["identities"] == nil {
		t.Fatalf("ImportOpenClawMigrationAgents payload = %#v", gotBody)
	}

	createdAt := time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)
	historyResult, err := client.ImportOpenClawMigrationHistoryBatch(OpenClawMigrationImportHistoryBatchInput{
		UserID: "00000000-0000-0000-0000-000000000111",
		Batch: OpenClawMigrationImportBatch{
			ID:    "batch-1",
			Index: 1,
			Total: 1,
		},
		Events: []OpenClawMigrationImportHistoryEvent{
			{
				AgentSlug: "main",
				Role:      "assistant",
				Body:      "hello",
				CreatedAt: createdAt,
			},
		},
	})
	if err != nil {
		t.Fatalf("ImportOpenClawMigrationHistoryBatch() error = %v", err)
	}
	if historyResult.EventsReceived != 2 || historyResult.MessagesInserted != 2 {
		t.Fatalf("ImportOpenClawMigrationHistoryBatch() result = %#v", historyResult)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/migrations/openclaw/import/history/batch" {
		t.Fatalf("ImportOpenClawMigrationHistoryBatch request = %s %s", gotMethod, gotPath)
	}
	if gotBody["user_id"] != "00000000-0000-0000-0000-000000000111" {
		t.Fatalf("ImportOpenClawMigrationHistoryBatch payload = %#v", gotBody)
	}

	statusResult, err := client.GetOpenClawMigrationStatus()
	if err != nil {
		t.Fatalf("GetOpenClawMigrationStatus() error = %v", err)
	}
	if !statusResult.Active || len(statusResult.Phases) != 1 {
		t.Fatalf("GetOpenClawMigrationStatus() result = %#v", statusResult)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/migrations/openclaw/status" {
		t.Fatalf("GetOpenClawMigrationStatus request = %s %s", gotMethod, gotPath)
	}

	runResult, err := client.RunOpenClawMigration(OpenClawMigrationRunRequest{
		HistoryOnly: true,
		StartPhase:  "history_backfill",
	})
	if err != nil {
		t.Fatalf("RunOpenClawMigration() error = %v", err)
	}
	if !runResult.Accepted || len(runResult.StartedPhases) != 1 {
		t.Fatalf("RunOpenClawMigration() result = %#v", runResult)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/migrations/openclaw/run" {
		t.Fatalf("RunOpenClawMigration request = %s %s", gotMethod, gotPath)
	}
	if gotBody["start_phase"] != "history_backfill" || gotBody["history_only"] != true {
		t.Fatalf("RunOpenClawMigration payload = %#v", gotBody)
	}

	pauseResult, err := client.PauseOpenClawMigration()
	if err != nil {
		t.Fatalf("PauseOpenClawMigration() error = %v", err)
	}
	if pauseResult.Status != "paused" || pauseResult.UpdatedPhases != 1 {
		t.Fatalf("PauseOpenClawMigration() result = %#v", pauseResult)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/migrations/openclaw/pause" {
		t.Fatalf("PauseOpenClawMigration request = %s %s", gotMethod, gotPath)
	}

	resumeResult, err := client.ResumeOpenClawMigration()
	if err != nil {
		t.Fatalf("ResumeOpenClawMigration() error = %v", err)
	}
	if resumeResult.Status != "running" || resumeResult.UpdatedPhases != 1 {
		t.Fatalf("ResumeOpenClawMigration() result = %#v", resumeResult)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/migrations/openclaw/resume" {
		t.Fatalf("ResumeOpenClawMigration request = %s %s", gotMethod, gotPath)
	}

	resetResult, err := client.ResetOpenClawMigration(OpenClawMigrationResetRequest{
		Confirm: "RESET_OPENCLAW_MIGRATION",
	})
	if err != nil {
		t.Fatalf("ResetOpenClawMigration() error = %v", err)
	}
	if resetResult.Status != "reset" || resetResult.PausedPhases != 1 || resetResult.ProgressRowsDeleted != 3 || resetResult.TotalDeleted != 3 {
		t.Fatalf("ResetOpenClawMigration() result = %#v", resetResult)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/migrations/openclaw/reset" {
		t.Fatalf("ResetOpenClawMigration request = %s %s", gotMethod, gotPath)
	}
	if gotBody["confirm"] != "RESET_OPENCLAW_MIGRATION" {
		t.Fatalf("ResetOpenClawMigration payload = %#v", gotBody)
	}
}

func TestClientOpenClawMigrationEndpointValidation(t *testing.T) {
	client := &Client{
		BaseURL: "https://api.example.com",
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    &http.Client{},
	}

	_, err := client.ImportOpenClawMigrationAgents(OpenClawMigrationImportAgentsInput{})
	if err == nil {
		t.Fatalf("expected ImportOpenClawMigrationAgents() validation error")
	}

	_, err = client.ImportOpenClawMigrationHistoryBatch(OpenClawMigrationImportHistoryBatchInput{})
	if err == nil {
		t.Fatalf("expected ImportOpenClawMigrationHistoryBatch() validation error")
	}

	_, err = client.ResetOpenClawMigration(OpenClawMigrationResetRequest{})
	if err == nil {
		t.Fatalf("expected ResetOpenClawMigration() validation error")
	}
}
