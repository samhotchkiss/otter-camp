package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTerminalMatrixNativeAndTmux(t *testing.T) {
	t.Parallel()

	scenarios := []struct {
		name    string
		runtime RuntimeHints
	}{
		{name: "native", runtime: RuntimeHints{}},
		{name: "tmux", runtime: RuntimeHints{ModifierReliabilityUncertain: true}},
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			for _, class := range []SizeClass{SizeXS, SizeS, SizeXL} {
				width, height := snapshotDimensions(class)
				model := NewModelWithRuntime(DefaultState(), scenario.runtime)
				model = pressMsg(model, tea.WindowSizeMsg{Width: width, Height: height})
				model = pressMsg(model, ConnectionStateMsg{State: ConnectionConnected, Degraded: false})
				model = pressMsg(model, ReplaySyncedMsg{})
				view := model.View()
				layout := model.CurrentLayout()

				if layout.visible[MainPanel] && !strings.Contains(view, "Main state:") {
					t.Fatalf("missing main panel state line for %s/%s: %q", scenario.name, class, view)
				}
				if layout.visible[ChatPanel] && !strings.Contains(view, "Chat state:") {
					t.Fatalf("missing chat panel state line for %s/%s: %q", scenario.name, class, view)
				}
				if layout.visible[SidebarPanel] && !strings.Contains(view, "Sidebar state:") {
					t.Fatalf("missing sidebar panel state line for %s/%s: %q", scenario.name, class, view)
				}
				if strings.Contains(view, "[Main:hidden]") && strings.Contains(view, "Main state:") {
					t.Fatalf("main state rendered while main panel hidden for %s/%s: %q", scenario.name, class, view)
				}
				if strings.Contains(view, "DEGRADED MODE") {
					t.Fatalf("unexpected degraded banner in healthy matrix case %s/%s: %q", scenario.name, class, view)
				}

				if scenario.runtime.ModifierReliabilityUncertain && !strings.Contains(view, "tmux-safe commands") {
					t.Fatalf("tmux matrix view missing tmux-safe help for %s: %q", class, view)
				}
				if !scenario.runtime.ModifierReliabilityUncertain && !strings.Contains(view, "fallback commands") {
					t.Fatalf("native matrix view missing fallback help for %s: %q", class, view)
				}
			}
		})
	}
}

func TestQualityGateFailuresReportsOutOfBudgetValues(t *testing.T) {
	t.Parallel()

	model := NewModel(DefaultState())
	model.perfMetrics = TUIPerformanceMetrics{
		InitialInteractivePaint: 2400 * time.Millisecond,
		KeypressToVisible:       150 * time.Millisecond,
		SSEDeltaRenderLatency:   400 * time.Millisecond,
		PeakMemoryBytes:         300 * 1024 * 1024,
		MemoryBoundBytes:        128 * 1024 * 1024,
	}
	failures := model.QualityGateFailures()
	if len(failures) < 4 {
		t.Fatalf("expected multiple quality gate failures, got %d (%v)", len(failures), failures)
	}
	joined := strings.Join(failures, " | ")
	for _, marker := range []string{"initial interactive paint", "keypress latency", "sse delta latency", "memory ceiling"} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("missing failure marker %q in %q", marker, joined)
		}
	}
}
