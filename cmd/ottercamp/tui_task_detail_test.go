package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoadTUITaskDetailPopulatesSessionBindingsEX254(t *testing.T) {
	t.Parallel()

	taskID := "00000000-0000-0000-0000-000000000254"
	now := time.Now().UTC()

	cases := []struct {
		name                  string
		sessions              []map[string]any
		wantSessionID         string
		wantDiscussionID      string
		wantActiveExecutionID string
		wantRecentExecutionID string
	}{
		{
			name: "discussion and active execution",
			sessions: []map[string]any{
				{
					"id":              "00000000-0000-0000-0000-000000000441",
					"scope_type":      "project_task",
					"scope_id":        taskID,
					"mode":            "sync",
					"status":          "active",
					"title":           "Sandbox Discussion",
					"message_count":   3,
					"created_at":      now,
					"last_message_at": now,
				},
				{
					"id":              "00000000-0000-0000-0000-000000000442",
					"scope_type":      "project_task",
					"scope_id":        taskID,
					"mode":            "async",
					"status":          "active",
					"title":           "Sandbox Execution",
					"message_count":   2,
					"created_at":      now.Add(-time.Minute),
					"last_message_at": now.Add(-time.Minute),
				},
			},
			wantSessionID:         "00000000-0000-0000-0000-000000000442",
			wantDiscussionID:      "00000000-0000-0000-0000-000000000441",
			wantActiveExecutionID: "00000000-0000-0000-0000-000000000442",
			wantRecentExecutionID: "00000000-0000-0000-0000-000000000442",
		},
		{
			name: "discussion only",
			sessions: []map[string]any{
				{
					"id":              "00000000-0000-0000-0000-000000000443",
					"scope_type":      "project_task",
					"scope_id":        taskID,
					"mode":            "sync",
					"status":          "active",
					"title":           "Sandbox Discussion",
					"message_count":   3,
					"created_at":      now,
					"last_message_at": now,
				},
			},
			wantDiscussionID: "00000000-0000-0000-0000-000000000443",
		},
		{
			name: "recent execution without active run",
			sessions: []map[string]any{
				{
					"id":              "00000000-0000-0000-0000-000000000444",
					"scope_type":      "project_task",
					"scope_id":        taskID,
					"mode":            "sync",
					"status":          "active",
					"title":           "Sandbox Discussion",
					"message_count":   3,
					"created_at":      now,
					"last_message_at": now,
				},
				{
					"id":              "00000000-0000-0000-0000-000000000445",
					"scope_type":      "project_task",
					"scope_id":        taskID,
					"mode":            "async",
					"status":          "archived",
					"title":           "Sandbox Execution",
					"message_count":   2,
					"created_at":      now.Add(-time.Minute),
					"last_message_at": now.Add(-time.Minute),
				},
			},
			wantSessionID:         "00000000-0000-0000-0000-000000000445",
			wantDiscussionID:      "00000000-0000-0000-0000-000000000444",
			wantRecentExecutionID: "00000000-0000-0000-0000-000000000445",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v1/tasks/"+taskID:
					writeTUITestJSON(t, w, map[string]any{
						"data": map[string]any{
							"id":                    taskID,
							"task_number":           254,
							"title":                 "UX Task Detail Sandbox",
							"description":           "Sandbox task",
							"work_status":           "in_progress",
							"priority":              2,
							"assigned_agent_id":     "",
							"requires_human_review": false,
							"branch_name":           nil,
							"current_flow_node":     nil,
						},
					})
				case r.URL.Path == "/v1/tasks/"+taskID+"/flow":
					writeTUITestJSON(t, w, map[string]any{"data": map[string]any{"executions": []any{}, "subtasks": []any{}}})
				case r.URL.Path == "/v1/tasks/"+taskID+"/dependencies":
					writeTUITestJSON(t, w, map[string]any{"data": []any{}})
				case r.URL.Path == "/v1/tasks/"+taskID+"/events":
					writeTUITestJSON(t, w, map[string]any{"data": []any{}})
				case r.URL.Path == "/v1/chat-sessions":
					if got := r.URL.Query().Get("scope_type"); got != "project_task" {
						t.Fatalf("scope_type query = %q, want project_task", got)
					}
					if got := r.URL.Query().Get("scope_id"); got != taskID {
						t.Fatalf("scope_id query = %q, want %q", got, taskID)
					}
					writeTUITestJSON(t, w, map[string]any{"data": tc.sessions, "meta": map[string]any{}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client, err := newCLIAPIClient(server.URL, "test-key")
			if err != nil {
				t.Fatalf("newCLIAPIClient: %v", err)
			}

			item, err := loadTUITaskDetail(context.Background(), client, taskID)
			if err != nil {
				t.Fatalf("loadTUITaskDetail: %v", err)
			}

			if item.DiscussionSessionID != tc.wantDiscussionID {
				t.Fatalf("DiscussionSessionID = %q, want %q", item.DiscussionSessionID, tc.wantDiscussionID)
			}
			if item.ActiveExecutionID != tc.wantActiveExecutionID {
				t.Fatalf("ActiveExecutionID = %q, want %q", item.ActiveExecutionID, tc.wantActiveExecutionID)
			}
			if item.RecentExecutionID != tc.wantRecentExecutionID {
				t.Fatalf("RecentExecutionID = %q, want %q", item.RecentExecutionID, tc.wantRecentExecutionID)
			}
			if item.SessionID != tc.wantSessionID {
				t.Fatalf("SessionID = %q, want %q", item.SessionID, tc.wantSessionID)
			}
		})
	}
}

func writeTUITestJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
