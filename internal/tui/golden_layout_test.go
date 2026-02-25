package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLayoutGoldenSnapshots(t *testing.T) {
	updateGolden := os.Getenv("UPDATE_GOLDEN") == "1"
	shells := []string{"board", "chat", "inbox"}
	classes := []SizeClass{SizeXS, SizeS, SizeM, SizeL, SizeXL}

	for _, class := range classes {
		t.Run(string(class), func(t *testing.T) {
			width, height := snapshotDimensions(class)
			model := NewModel(DefaultState())
			model = pressMsg(model, tea.WindowSizeMsg{Width: width, Height: height})
			model.statusMessage = "snapshot"

			var builder strings.Builder
			for _, shell := range shells {
				builder.WriteString("### ")
				builder.WriteString(string(class))
				builder.WriteString(" ")
				builder.WriteString(shell)
				builder.WriteString("\n")
				builder.WriteString(model.viewForShell(shell))
				builder.WriteString("\n\n")
			}
			got := strings.TrimSpace(builder.String()) + "\n"

			goldenPath := filepath.Join("testdata", "layout_"+strings.ToLower(string(class))+".golden")
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
				t.Fatalf("golden snapshot mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", class, got, string(want))
			}
		})
	}
}
