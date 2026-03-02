package tui

import (
	"strings"
	"testing"
)

func TestRenderProjectViewShowsFilesSectionWhenPresent(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	projectID := "project-files"
	model.workspace.selectedProjectID = projectID
	model.workspace.nodes["project-"+projectID] = &sidebarNode{
		ID:        "project-" + projectID,
		Kind:      sidebarKindProject,
		ProjectID: projectID,
		Label:     "Project Files",
	}
	model.workspace.selectedProject = &ProjectDetail{
		ID:          projectID,
		DisplayName: "Project Files",
		Files: []ProjectFileEntry{
			{Path: "README.md", IsDir: false, Depth: 1},
			{Path: "cmd/ottercamp/main.go", IsDir: false, Depth: 3},
		},
	}

	rendered := strings.Join(model.renderProjectView(120, 40), "\n")
	if !strings.Contains(rendered, "FILES (2)") {
		t.Fatalf("project view missing files header:\n%s", rendered)
	}
	if !strings.Contains(rendered, "hidden  ·  f·show files") {
		t.Fatalf("project view missing collapsed files hint:\n%s", rendered)
	}
}

func TestRenderProjectViewHidesFilesSectionWhenAbsent(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	projectID := "project-no-files"
	model.workspace.selectedProjectID = projectID
	model.workspace.nodes["project-"+projectID] = &sidebarNode{
		ID:        "project-" + projectID,
		Kind:      sidebarKindProject,
		ProjectID: projectID,
		Label:     "No Files Project",
	}
	model.workspace.selectedProject = &ProjectDetail{
		ID:          projectID,
		DisplayName: "No Files Project",
		Files:       nil,
	}

	rendered := strings.Join(model.renderProjectView(120, 40), "\n")
	if strings.Contains(rendered, "FILES (") {
		t.Fatalf("project view unexpectedly shows files section:\n%s", rendered)
	}
}
