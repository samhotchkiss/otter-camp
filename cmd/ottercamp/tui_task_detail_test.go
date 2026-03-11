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
		wantActiveExecutionSessionID string
		wantRecentExecutionSessionID string
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
			wantActiveExecutionSessionID: "00000000-0000-0000-0000-000000000442",
			wantRecentExecutionSessionID: "00000000-0000-0000-0000-000000000442",
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
			wantRecentExecutionSessionID: "00000000-0000-0000-0000-000000000445",
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
			if item.ActiveExecutionSessionID != tc.wantActiveExecutionSessionID {
				t.Fatalf("ActiveExecutionSessionID = %q, want %q", item.ActiveExecutionSessionID, tc.wantActiveExecutionSessionID)
			}
			if item.RecentExecutionSessionID != tc.wantRecentExecutionSessionID {
				t.Fatalf("RecentExecutionSessionID = %q, want %q", item.RecentExecutionSessionID, tc.wantRecentExecutionSessionID)
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

func TestLoadTUIProjectsResolvesDisplayNameFallback(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects" {
			http.NotFound(w, r)
			return
		}
		writeTUITestJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"id": "proj-name", "slug": "name-slug", "display_name": "Project Name", "updated_at": "2026-03-06T00:00:00Z"},
				{"id": "proj-slug", "slug": "slug-only", "display_name": "", "is_paused": true, "pause_reason": "waiting on review", "updated_at": "2026-03-06T00:00:00Z"},
			},
		})
	}))
	defer server.Close()

	client, err := newCLIAPIClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("newCLIAPIClient: %v", err)
	}

	items, err := loadTUIProjects(context.Background(), client)
	if err != nil {
		t.Fatalf("loadTUIProjects: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("project count = %d, want 2", len(items))
	}
	if items[0].DisplayName != "Project Name" {
		t.Fatalf("items[0].DisplayName = %q, want Project Name", items[0].DisplayName)
	}
	if items[1].DisplayName != "slug-only" {
		t.Fatalf("items[1].DisplayName = %q, want slug-only", items[1].DisplayName)
	}
	if !items[1].IsPaused {
		t.Fatal("items[1].IsPaused = false, want true")
	}
	if items[1].PauseReason != "waiting on review" {
		t.Fatalf("items[1].PauseReason = %q, want waiting on review", items[1].PauseReason)
	}
}

func TestLoadTUIProjectDetailResolvesSlugFallback(t *testing.T) {
	t.Parallel()

	projectID := "proj-slug"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/" + projectID:
			writeTUITestJSON(t, w, map[string]any{
				"data": map[string]any{
					"id":            projectID,
					"slug":          "slug-only-project",
					"display_name":  "",
					"description":   "Slug fallback test",
					"delivery_mode": "manual",
				},
			})
		case "/v1/projects/" + projectID + "/tasks":
			writeTUITestJSON(t, w, map[string]any{"data": []any{}})
		case "/v1/projects/" + projectID + "/agents":
			writeTUITestJSON(t, w, map[string]any{"data": []any{}})
		case "/v1/projects/" + projectID + "/remotes":
			writeTUITestJSON(t, w, map[string]any{"data": []any{}})
		case "/v1/projects/" + projectID + "/files":
			writeTUITestJSON(t, w, map[string]any{"data": map[string]any{"files": []any{}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newCLIAPIClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("newCLIAPIClient: %v", err)
	}

	detail, err := loadTUIProjectDetail(context.Background(), client, projectID)
	if err != nil {
		t.Fatalf("loadTUIProjectDetail: %v", err)
	}
	if detail.DisplayName != "slug-only-project" {
		t.Fatalf("DisplayName = %q, want slug-only-project", detail.DisplayName)
	}
	if detail.Slug != "slug-only-project" {
		t.Fatalf("Slug = %q, want slug-only-project", detail.Slug)
	}
}

func TestLoadTUIProjectDetailCarriesPauseState(t *testing.T) {
	t.Parallel()

	projectID := "proj-paused"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/" + projectID:
			writeTUITestJSON(t, w, map[string]any{
				"data": map[string]any{
					"id":            projectID,
					"slug":          "paused-project",
					"display_name":  "Paused Project",
					"description":   "Paused project detail test",
					"delivery_mode": "gated",
					"is_paused":     true,
					"pause_reason":  "waiting for operator review",
				},
			})
		case "/v1/projects/" + projectID + "/tasks":
			writeTUITestJSON(t, w, map[string]any{"data": []any{}})
		case "/v1/projects/" + projectID + "/agents":
			writeTUITestJSON(t, w, map[string]any{"data": []any{}})
		case "/v1/projects/" + projectID + "/remotes":
			writeTUITestJSON(t, w, map[string]any{"data": []any{}})
		case "/v1/projects/" + projectID + "/files":
			writeTUITestJSON(t, w, map[string]any{"data": map[string]any{"files": []any{}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newCLIAPIClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("newCLIAPIClient: %v", err)
	}

	detail, err := loadTUIProjectDetail(context.Background(), client, projectID)
	if err != nil {
		t.Fatalf("loadTUIProjectDetail: %v", err)
	}
	if !detail.IsPaused {
		t.Fatal("IsPaused = false, want true")
	}
	if detail.PauseReason != "waiting for operator review" {
		t.Fatalf("PauseReason = %q, want waiting for operator review", detail.PauseReason)
	}
}
