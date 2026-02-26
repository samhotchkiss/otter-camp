//go:build e2e

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFirstRunColdOpenTourAndProofOfLife(t *testing.T) {
	clock := newStepClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), 25*time.Millisecond)
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{
		FirstRun:                    true,
		Clock:                       clock.Now,
		ColdOpenDuration:            1100 * time.Millisecond,
		TourDuration:                2 * time.Minute,
		MemorySteadyStateBoundBytes: 256 * 1024 * 1024,
		DisableMemorySampler:        true,
	})

	if view := model.View(); !strings.Contains(view, "FIRST RUN") {
		t.Fatalf("initial view missing cold-open frame: %q", view)
	}

	model = pressTMUXE2EMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model = pressTMUXE2EMsg(model, coldOpenCompleteMsg{})
	if !strings.Contains(model.View(), "Tour overlay (non-blocking)") {
		t.Fatalf("tour overlay not visible after cold-open completion: %q", model.View())
	}

	model = pressTMUXE2EMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune("dashboard") {
		model = pressTMUXE2EMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = pressTMUXE2EMsg(model, tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.MainView(); got != ViewDashboard {
		t.Fatalf("main view while tour active = %s, want %s", got, ViewDashboard)
	}

	model = pressTMUXE2EMsg(model, ConnectionStateMsg{State: ConnectionConnected})
	model = pressTMUXE2EMsg(model, ReplaySyncedMsg{})
	view := model.View()
	if !strings.Contains(view, "realtime connected") || !strings.Contains(view, "replay synced") {
		t.Fatalf("proof-of-life line missing required markers: %q", view)
	}

	model = pressTMUXE2EMsg(model, tourOverlayExpiredMsg{})
	if strings.Contains(model.View(), "Tour overlay (non-blocking)") {
		t.Fatalf("tour overlay should expire after timeout: %q", model.View())
	}
}

type stepClock struct {
	now  time.Time
	step time.Duration
}

func newStepClock(start time.Time, step time.Duration) *stepClock {
	return &stepClock{now: start.UTC(), step: step}
}

func (c *stepClock) Now() time.Time {
	value := c.now
	c.now = c.now.Add(c.step)
	return value
}
