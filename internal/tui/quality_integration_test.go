//go:build integration

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDegradedModeBannerShowsRecoveryGuidance(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	model = pressRealtimeMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressRealtimeMsg(model, ConnectionStateMsg{State: ConnectionDisconnected, Degraded: true})

	view := model.View()
	if !strings.Contains(view, "DEGRADED MODE") {
		t.Fatalf("view missing degraded banner: %q", view)
	}
	// EX-100: message changed to be context-aware; disconnected shows "Reconnecting".
	if !strings.Contains(view, "Reconnecting") && !strings.Contains(view, "stale") {
		t.Fatalf("view missing degraded actionable message: %q", view)
	}
	for _, marker := range []string{"Main state:", "Chat state:", "Sidebar state:"} {
		if strings.Contains(view, marker) {
			t.Fatalf("view contains debug panel state marker %q: %q", marker, view)
		}
	}

	model = pressRealtimeMsg(model, ConnectionStateMsg{State: ConnectionConnected, Degraded: false})
	if strings.Contains(model.View(), "DEGRADED MODE") {
		t.Fatalf("degraded banner should clear after recovery: %q", model.View())
	}
}

func TestRealtimeClientExtendedReplayStability(t *testing.T) {
	t.Parallel()

	server := newScriptedSSEServer(t, []scriptedResponse{
		{frames: synthFrames(1, 80)},
		{frames: synthFrames(81, 160)},
		{frames: synthFrames(161, 240)},
	})
	defer server.Close()

	reducer := NewEventReducer(nil)
	applied := 0
	replaySyncedSignals := 0

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	client := &RealtimeClient{
		Connector: HTTPSSEConnector{URL: server.URL()},
		Snapshots: SnapshotFetcherFunc(func(context.Context, []string) error { return nil }),
		Reducer:   reducer,
		Backoff:   []time.Duration{10 * time.Millisecond},
		OnReplaySynced: func() {
			replaySyncedSignals++
		},
		OnEvent: func(event EventEnvelope, wasApplied bool) {
			if wasApplied {
				applied++
			}
			if event.Seq == 240 {
				cancel()
			}
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()
	if err := <-done; err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if got := reducer.LastSeq(); got != 240 {
		t.Fatalf("LastSeq() = %d, want 240", got)
	}
	if applied != 240 {
		t.Fatalf("applied events = %d, want 240", applied)
	}
	if replaySyncedSignals != 1 {
		t.Fatalf("replay synced signals = %d, want 1", replaySyncedSignals)
	}
}

func TestTUIPerformanceBudgets(t *testing.T) {
	t.Parallel()

	clock := &integrationStepClock{
		now:  time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
		step: 40 * time.Millisecond,
	}
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		Clock:                       clock.Now,
		MemorySteadyStateBoundBytes: 512 * 1024 * 1024,
		DisableMemorySampler:        true,
	})

	model = pressRealtimeMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressRealtimeMsg(model, ConnectionStateMsg{State: ConnectionConnected})
	model = pressRealtimeMsg(model, tea.KeyMsg{Type: tea.KeyTab})

	deltaPayload := map[string]string{"message_id": "msg-perf", "role": "assistant", "delta": "ok"}
	rawPayload, _ := json.Marshal(deltaPayload)
	model = pressRealtimeMsg(model, ChatEnvelopeMsg{Envelope: EventEnvelope{
		Seq:        1,
		EventID:    "evt-perf-1",
		EventType:  "chat.message.delta",
		OccurredAt: clock.Peek().Add(-80 * time.Millisecond),
		OrgID:      "org-1",
		Payload:    rawPayload,
	}})
	model = pressRealtimeMsg(model, memorySampleMsg{})

	metrics := model.PerformanceMetrics()
	if metrics.InitialInteractivePaint <= 0 || metrics.InitialInteractivePaint > 1200*time.Millisecond {
		t.Fatalf("initial paint latency out of bounds: %v", metrics.InitialInteractivePaint)
	}
	if metrics.KeypressToVisible <= 0 || metrics.KeypressToVisible > keypressLatencyBudget {
		t.Fatalf("keypress latency out of bounds: %v", metrics.KeypressToVisible)
	}
	if metrics.SSEDeltaRenderLatency <= 0 || metrics.SSEDeltaRenderLatency > sseRenderLatencyBudget {
		t.Fatalf("sse render latency out of bounds: %v", metrics.SSEDeltaRenderLatency)
	}
	if metrics.PeakMemoryBytes == 0 {
		t.Fatal("peak memory sample should be captured")
	}
	if failures := model.QualityGateFailures(); len(failures) > 0 {
		t.Fatalf("quality gate failures: %v", failures)
	}
}

func TestConnectionConnectedLoadsActiveProjectsIntoSidebarEX244(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadProjects: func(context.Context) ([]SidebarProjectItem, error) {
			return []SidebarProjectItem{{ID: "proj-active", DisplayName: "Active Project"}}, nil
		},
		LoadProjectTasks: func(_ context.Context, projectID string) ([]SidebarTaskItem, error) {
			if projectID != "proj-active" {
				t.Fatalf("LoadProjectTasks projectID = %q, want proj-active", projectID)
			}
			return []SidebarTaskItem{{ID: "task-1", Title: "Ship sidebar fix", WorkStatus: "in_progress", TaskNumber: 1}}, nil
		},
	})

	model = connectAndLoadSidebar(t, model)

	gotProjects := model.workspace.existingProjects()
	if len(gotProjects) != 1 || gotProjects[0].ID != "proj-active" || gotProjects[0].DisplayName != "Active Project" {
		t.Fatalf("projects after connect = %+v, want active project", gotProjects)
	}

	model = pressRealtimeMsg(model, loadProjectTasksCmd("proj-active", model.runtimeHints, false)())
	if task := model.workspace.tasks["task-1"]; task == nil || task.ProjectID != "proj-active" {
		t.Fatalf("task-1 after project task load = %+v, want project-backed task record", task)
	}
}

func TestSidebarAndDashboardUseProjectDisplayNameWhenAvailable(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadProjects: func(context.Context) ([]SidebarProjectItem, error) {
			return []SidebarProjectItem{{ID: "proj-display", Slug: "display-slug", DisplayName: "Display Project"}}, nil
		},
		LoadProjectDetail: func(_ context.Context, projectID string) (*ProjectDetail, error) {
			if projectID != "proj-display" {
				t.Fatalf("LoadProjectDetail projectID = %q, want proj-display", projectID)
			}
			return &ProjectDetail{ID: projectID, Slug: "display-slug", DisplayName: "Display Project"}, nil
		},
	})

	model = connectAndLoadSidebar(t, model)
	model.workspace.selectedProjectID = "proj-display"
	model = pressRealtimeMsg(model, loadProjectDetailCmd("proj-display", model.runtimeHints)())

	gotProjects := model.workspace.existingProjects()
	if len(gotProjects) != 1 || gotProjects[0].DisplayName != "Display Project" {
		t.Fatalf("projects after detail load = %+v, want display name label", gotProjects)
	}

	rendered := strings.Join(model.renderDashboardView(130, 30), "\n")
	if !strings.Contains(rendered, "Display Project — Task Board") {
		t.Fatalf("dashboard header missing resolved display name:\n%s", rendered)
	}
}

func TestSidebarAndDashboardUseProjectSlugWhenDisplayNameMissing(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadProjects: func(context.Context) ([]SidebarProjectItem, error) {
			return []SidebarProjectItem{{ID: "proj-slug", Slug: "slug-only-project"}}, nil
		},
		LoadProjectDetail: func(_ context.Context, projectID string) (*ProjectDetail, error) {
			if projectID != "proj-slug" {
				t.Fatalf("LoadProjectDetail projectID = %q, want proj-slug", projectID)
			}
			return &ProjectDetail{ID: projectID, Slug: "slug-only-project"}, nil
		},
	})

	model = connectAndLoadSidebar(t, model)
	model.workspace.selectedProjectID = "proj-slug"
	model = pressRealtimeMsg(model, loadProjectDetailCmd("proj-slug", model.runtimeHints)())

	gotProjects := model.workspace.existingProjects()
	if len(gotProjects) != 1 || gotProjects[0].DisplayName != "slug-only-project" {
		t.Fatalf("projects after detail load = %+v, want slug label", gotProjects)
	}

	rendered := strings.Join(model.renderDashboardView(130, 30), "\n")
	if !strings.Contains(rendered, "slug-only-project — Task Board") {
		t.Fatalf("dashboard header missing slug fallback:\n%s", rendered)
	}
	if strings.Contains(rendered, "Project proj-sl") {
		t.Fatalf("dashboard header leaked raw project-id fragment:\n%s", rendered)
	}
}

func TestFreshStartupMatchesRunningClientProjectListEX244(t *testing.T) {
	t.Parallel()

	runtimeHints := RuntimeHints{
		LoadProjects: func(context.Context) ([]SidebarProjectItem, error) {
			return []SidebarProjectItem{
				{ID: "proj-alpha", DisplayName: "Alpha"},
				{ID: "proj-beta", DisplayName: "Beta"},
			}, nil
		},
	}

	running := NewModelWithRuntime(DefaultState(), runtimeHints)
	running.workspace.rebuildSidebar(
		"org-old",
		nil,
		[]SidebarProjectItem{{ID: "proj-stale", DisplayName: "Stale"}},
	)
	running = connectAndLoadSidebar(t, running)

	fresh := NewModelWithRuntime(DefaultState(), runtimeHints)
	fresh = connectAndLoadSidebar(t, fresh)

	runningProjects := running.workspace.existingProjects()
	freshProjects := fresh.workspace.existingProjects()
	if len(runningProjects) != len(freshProjects) {
		t.Fatalf("project counts differ: running=%+v fresh=%+v", runningProjects, freshProjects)
	}
	for i := range freshProjects {
		if runningProjects[i] != freshProjects[i] {
			t.Fatalf("project[%d] mismatch: running=%+v fresh=%+v", i, runningProjects[i], freshProjects[i])
		}
	}
}

func TestSidebarEmptyProjectsPayloadKeepsTaskBackedProjectEX244(t *testing.T) {
	t.Parallel()

	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadProjects: func(context.Context) ([]SidebarProjectItem, error) {
			return nil, nil
		},
	})
	model.workspace.selectedProjectID = "proj-active"
	model.workspace.selectedProject = &ProjectDetail{ID: "proj-active", DisplayName: "Active Project"}
	model.workspace.tasks["task-1"] = &taskRecord{
		ID:        "task-1",
		ProjectID: "proj-active",
		Title:     "Ship sidebar fix",
		Status:    "in_progress",
	}

	model = connectAndLoadSidebar(t, model)

	gotProjects := model.workspace.existingProjects()
	if len(gotProjects) != 1 || gotProjects[0].ID != "proj-active" || gotProjects[0].DisplayName != "Active Project" {
		t.Fatalf("projects after empty payload = %+v, want task-backed active project", gotProjects)
	}
	if got := model.statusMessage; got != "Project list returned empty while active project data exists — keeping known active projects." {
		t.Fatalf("statusMessage = %q, want unexpected-empty warning", got)
	}
	if activity := strings.Join(model.ActivityEntries(), " | "); !strings.Contains(activity, "project list returned empty; preserving known active projects") {
		t.Fatalf("activity missing unexpected-empty warning: %q", activity)
	}
}

func connectAndLoadSidebar(t *testing.T, model Model) Model {
	t.Helper()

	updated, cmd := model.Update(ConnectionStateMsg{State: ConnectionConnected})
	if cmd == nil {
		t.Fatal("ConnectionConnected should schedule sidebar load")
	}

	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model type = %T, want tui.Model", updated)
	}
	msg := cmd()
	sidebarMsg, ok := msg.(sidebarDataLoadedMsg)
	if !ok {
		t.Fatalf("sidebar load cmd returned %T, want sidebarDataLoadedMsg", msg)
	}

	return pressRealtimeMsg(next, sidebarMsg)
}

func synthFrames(start, end int64) []string {
	frames := make([]string, 0, end-start+1)
	for seq := start; seq <= end; seq++ {
		frames = append(frames, encodeEnvelopeFrame(seq, fmt.Sprintf("evt-%d", seq), "chat.message.delta", map[string]any{"message_id": "msg", "role": "assistant", "delta": "x"}))
	}
	return frames
}

type integrationStepClock struct {
	now  time.Time
	step time.Duration
}

func (c *integrationStepClock) Now() time.Time {
	value := c.now
	c.now = c.now.Add(c.step)
	return value
}

func (c *integrationStepClock) Peek() time.Time {
	return c.now
}
