//go:build integration

package tui

import tea "github.com/charmbracelet/bubbletea"

func seededWorkspaceModel() Model {
	model := NewModel(DefaultState())
	model = pressSeedMsg(model, tea.WindowSizeMsg{Width: 160, Height: 34})

	projectID := "alpha"
	projectNodeID := "project-" + projectID
	model.workspace.nodes[projectNodeID] = &sidebarNode{
		ID:        projectNodeID,
		Label:     "Alpha Project",
		Kind:      sidebarKindProject,
		ProjectID: projectID,
		Expanded:  true,
	}
	model.workspace.nodes["session-alpha-task-2"] = &sidebarNode{
		ID:           "session-alpha-task-2",
		Label:        "OC-2: Publish launch post",
		Kind:         sidebarKindSession,
		ParentID:     projectNodeID,
		SessionID:    "session-task-2",
		SessionScope: "project_task",
		TaskID:       "task-2",
		TaskNumber:   2,
		WorkStatus:   "in_progress",
	}
	model.workspace.nodes["session-alpha-task-1"] = &sidebarNode{
		ID:           "session-alpha-task-1",
		Label:        "OC-1: Launch docs",
		Kind:         sidebarKindSession,
		ParentID:     projectNodeID,
		SessionID:    "session-task-1",
		SessionScope: "project_task",
		TaskID:       "task-1",
		TaskNumber:   1,
		WorkStatus:   "todo",
	}
	model.workspace.topLevel = []string{
		"inbox",
		"header-chats",
		generalSidebarNodeID,
		"header-projects",
		projectNodeID,
	}
	model.workspace.tasks["task-1"] = &taskRecord{
		ID:                  "task-1",
		ProjectID:           projectID,
		TaskNumber:          1,
		Title:               "Launch docs",
		Status:              "todo",
		SessionID:           "session-task-1",
		DiscussionSessionID: "session-task-1",
		RequiresHumanReview: true,
	}
	model.workspace.tasks["task-2"] = &taskRecord{
		ID:                  "task-2",
		ProjectID:           projectID,
		TaskNumber:          2,
		Title:               "Publish launch post",
		Status:              "in_progress",
		SessionID:           "session-task-2",
		DiscussionSessionID: "session-task-2",
	}
	model.workspace.taskOrder = []string{"task-1", "task-2"}
	model.workspace.inbox = []inboxItem{
		{ID: "inbox-1", TaskID: "task-1", Summary: "Approve launch docs"},
		{ID: "inbox-2", TaskID: "task-2", Summary: "Review publish status"},
	}
	model.workspace.selectedProject = &ProjectDetail{
		ID:          projectID,
		DisplayName: "Alpha Project",
		Slug:        "alpha",
		Tasks: []SidebarTaskItem{
			{ID: "task-1", Title: "Launch docs", WorkStatus: "todo", TaskNumber: 1},
			{ID: "task-2", Title: "Publish launch post", WorkStatus: "in_progress", TaskNumber: 2},
		},
	}
	model.workspace.selectedProjectID = projectID
	model.activeSession = generalSessionID
	model.workspace.activeSessionID = generalSessionID
	model.state.LastActiveChatSession = generalSessionID
	return model
}

func pressSeedMsg(model Model, msg tea.Msg) Model {
	updated, _ := model.Update(msg)
	return updated.(Model)
}
