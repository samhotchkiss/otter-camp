package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	versionpkg "github.com/samhotchkiss/otter-camp/internal/version"
)

func TestStatusBarBinaryFreshnessIndicatorDistinguishesMetadataAndStaleEX263(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		model := NewModel(DefaultState())
		model = pressMsg(model, tea.WindowSizeMsg{Width: 160, Height: 28})

		bar := model.renderStatusBar(model.CurrentLayout(), model.FocusedPanel())
		if !strings.Contains(bar, "rv"+repoVersionPlain()) {
			t.Fatalf("status bar missing repo version: %q", bar)
		}
		if strings.Contains(bar, "rv"+repoVersionPlain()+"!") || strings.Contains(bar, "rv"+repoVersionPlain()+"?") {
			t.Fatalf("clean status bar should not show freshness marker: %q", bar)
		}
	})

	t.Run("metadata missing", func(t *testing.T) {
		model := NewModelWithRuntime(DefaultState(), RuntimeHints{BinaryMetadataWarning: true})
		model = pressMsg(model, tea.WindowSizeMsg{Width: 160, Height: 28})

		bar := model.renderStatusBar(model.CurrentLayout(), model.FocusedPanel())
		if !strings.Contains(bar, "rv"+repoVersionPlain()+"?") {
			t.Fatalf("metadata-missing status bar = %q, want rv marker with ?", bar)
		}
		if strings.Contains(bar, "rv"+repoVersionPlain()+"!") {
			t.Fatalf("metadata-missing status bar should not render stale marker: %q", bar)
		}
	})

	t.Run("stale", func(t *testing.T) {
		model := NewModelWithRuntime(DefaultState(), RuntimeHints{
			BinaryStale:           true,
			BinaryMetadataWarning: true,
		})
		model = pressMsg(model, tea.WindowSizeMsg{Width: 160, Height: 28})

		bar := model.renderStatusBar(model.CurrentLayout(), model.FocusedPanel())
		if !strings.Contains(bar, "rv"+repoVersionPlain()+"!") {
			t.Fatalf("stale status bar = %q, want rv marker with !", bar)
		}
		if strings.Contains(bar, "rv"+repoVersionPlain()+"?") {
			t.Fatalf("stale status bar should prefer ! over ?: %q", bar)
		}
	})
}

func repoVersionPlain() string {
	return strings.TrimSpace(versionpkg.RepoVersion)
}
