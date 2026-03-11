package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDashboardRuntimeShortcutOpensTaskDetail(t *testing.T) {
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		LoadTaskDetail: func(_ context.Context, id string) (*TaskDetailItem, error) {
			return &TaskDetailItem{
				ID:        id,
				ProjectID: "proj-ops",
				Title:     "Inspect stale worker",
			}, nil
		},
		LoadProjectDetail: func(_ context.Context, id string) (*ProjectDetail, error) {
			return &ProjectDetail{ID: id, DisplayName: "Ops"}, nil
		},
	})
	model.focus = MainPanel
	model.workspace.setMainView(ViewDashboard)
	model.workspace.setOperatorDashboard(&OperatorDashboardData{
		Summary: OperatorDashboardSummary{Health: "attention_required"},
		Stale: OperatorDashboardSection{
			Items: []OperatorDashboardItem{
				{
					Title:   "OC-17: Inspect stale worker",
					Summary: "execution quiet past stale threshold",
					Project: &OperatorDashboardRef{ID: "proj-ops", Label: "Ops"},
					Task:    &OperatorDashboardTaskRef{ID: "task-17", TaskNumber: 17, Label: "OC-17: Inspect stale worker"},
				},
			},
		},
	})

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})

	if got := model.workspace.selectedTaskID; got != "task-17" {
		t.Fatalf("selectedTaskID = %q, want %q", got, "task-17")
	}
	if got := model.workspace.mainView; got != ViewTask {
		t.Fatalf("mainView = %q, want %q", got, ViewTask)
	}
	if got := model.statusMessage; !strings.Contains(got, "Opened runtime item") {
		t.Fatalf("statusMessage = %q, want runtime open feedback", got)
	}
}

func TestDashboardRuntimeShortcutShowsPanelHintOutsideDashboard(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = MainPanel
	model.workspace.setMainView(ViewProject)

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})

	if got := model.statusMessage; got != "Panels: 1 sidebar · 2 main · 3 chat. Press ? for full key reference." {
		t.Fatalf("statusMessage = %q", got)
	}
}

func TestDashboardRendersRuntimeHealthSection(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = MainPanel
	model.workspace.setMainView(ViewDashboard)
	model = pressMsg(model, tea.WindowSizeMsg{Width: 140, Height: 42})
	model.workspace.setOperatorDashboard(&OperatorDashboardData{
		Summary: OperatorDashboardSummary{
			Health:          "attention_required",
			ActiveProjects:  1,
			ActiveTasks:     2,
			ActiveRuns:      1,
			StaleTasks:      1,
			StaleExecutions: 1,
			BlockedItems:    1,
			RecentFailures:  1,
		},
		Active: OperatorDashboardSection{
			Count:      1,
			TotalCount: 3,
			Items: []OperatorDashboardItem{
				{
					Shortcut: 4,
					Title:    "OC-12: Ship runtime dashboard",
					Summary:  "run in progress",
				},
			},
		},
	})

	rendered := model.View()
	if !strings.Contains(rendered, "Runtime Health") {
		t.Fatalf("render missing runtime health section:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Attention required") {
		t.Fatalf("render missing runtime health headline:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Active work across 1 project(s) · 2 task(s) · 1 run(s)") {
		t.Fatalf("render missing active-work summary copy:\n%s", rendered)
	}
	if !strings.Contains(rendered, "4. OC-12: Ship runtime dashboard") {
		t.Fatalf("render missing runtime shortcut row:\n%s", rendered)
	}
	if !strings.Contains(rendered, "+2 more") {
		t.Fatalf("render missing total-count overflow hint:\n%s", rendered)
	}
}
