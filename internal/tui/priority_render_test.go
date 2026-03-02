package tui

import (
	"strings"
	"testing"
)

func TestRenderTaskViewShowsPriorityLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		priority int
		label    string
	}{
		{priority: 1, label: "Low"},
		{priority: 2, label: "Medium"},
		{priority: 3, label: "High"},
		{priority: 4, label: "Critical"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			model := NewModel(DefaultState())
			model.workspace.setMainView(ViewTask)
			model.workspace.selectedTaskID = "task-priority"
			model.workspace.tasks["task-priority"] = &taskRecord{
				ID:       "task-priority",
				Title:    "Priority task",
				Status:   "in_progress",
				Priority: tc.priority,
			}

			rendered := strings.Join(model.renderTaskView(100, 30), "\n")
			want := "Priority: " + tc.label
			if !strings.Contains(rendered, want) {
				t.Fatalf("rendered task view missing %q:\n%s", want, rendered)
			}
		})
	}
}

func TestRenderDashboardAndProjectShowPriorityBadges(t *testing.T) {
	t.Parallel()

	tasks := []SidebarTaskItem{
		{ID: "task-low", Title: "Low priority", WorkStatus: "in_progress", TaskNumber: 1, Priority: 1},
		{ID: "task-med", Title: "Medium priority", WorkStatus: "in_progress", TaskNumber: 2, Priority: 2},
		{ID: "task-high", Title: "High priority", WorkStatus: "in_progress", TaskNumber: 3, Priority: 3},
		{ID: "task-critical", Title: "Critical priority", WorkStatus: "in_progress", TaskNumber: 4, Priority: 4},
	}

	t.Run("dashboard", func(t *testing.T) {
		model := NewModel(DefaultState())
		model.workspace.selectedTaskID = "task-critical"
		for _, task := range tasks {
			model.workspace.tasks[task.ID] = &taskRecord{
				ID:       task.ID,
				Title:    task.Title,
				Status:   task.WorkStatus,
				Priority: task.Priority,
			}
			model.workspace.taskOrder = append(model.workspace.taskOrder, task.ID)
		}

		rendered := strings.Join(model.renderDashboardView(130, 40), "\n")
		for _, badge := range []string{"[LOW]", "[MED]", "[HIGH]", "[CRIT]"} {
			if !strings.Contains(rendered, badge) {
				t.Fatalf("dashboard view missing %q badge:\n%s", badge, rendered)
			}
		}
	})

	t.Run("project", func(t *testing.T) {
		model := NewModel(DefaultState())
		projectID := "project-priority"
		model.workspace.selectedProjectID = projectID
		model.workspace.nodes["project-"+projectID] = &sidebarNode{
			ID:        "project-" + projectID,
			Kind:      sidebarKindProject,
			ProjectID: projectID,
			Label:     "Priority Project",
		}
		model.workspace.selectedProject = &ProjectDetail{
			ID:          projectID,
			DisplayName: "Priority Project",
			Tasks:       tasks,
		}

		rendered := strings.Join(model.renderProjectView(130, 40), "\n")
		for _, badge := range []string{"[LOW]", "[MED]", "[HIGH]", "[CRIT]"} {
			if !strings.Contains(rendered, badge) {
				t.Fatalf("project view missing %q badge:\n%s", badge, rendered)
			}
		}
	})
}
