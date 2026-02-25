package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceGoldenSnapshots(t *testing.T) {
	updateGolden := os.Getenv("UPDATE_GOLDEN") == "1"
	views := []MainView{
		ViewDashboard,
		ViewProject,
		ViewTask,
		ViewInbox,
		ViewActivity,
		ViewAgents,
		ViewMerges,
		ViewSchedules,
	}
	sizes := []SizeClass{SizeM, SizeL}

	for _, size := range sizes {
		t.Run(string(size), func(t *testing.T) {
			model := NewModel(DefaultState())

			var builder strings.Builder
			for _, view := range views {
				model.workspace.setMainView(view)
				builder.WriteString("### ")
				builder.WriteString(string(size))
				builder.WriteString(" ")
				builder.WriteString(string(view))
				builder.WriteString("\n")
				builder.WriteString(model.WorkspaceRender(size))
				builder.WriteString("\n\n")
			}
			got := strings.TrimSpace(builder.String()) + "\n"

			goldenPath := filepath.Join("testdata", "workspace_"+strings.ToLower(string(size))+".golden")
			if updateGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("WriteFile(%s): %v", goldenPath, err)
				}
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", goldenPath, err)
			}
			if string(want) != got {
				t.Fatalf("golden snapshot mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", size, got, string(want))
			}
		})
	}
}
