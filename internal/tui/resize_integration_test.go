//go:build integration

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResizeTransitionsRemainStable(t *testing.T) {
	model := NewModel(DefaultState())
	sequence := []tea.WindowSizeMsg{
		{Width: 69, Height: 22},
		{Width: 90, Height: 30},
		{Width: 120, Height: 30},
		{Width: 160, Height: 34},
		{Width: 220, Height: 40},
		{Width: 100, Height: 21}, // height-based XS override
		{Width: 140, Height: 30},
	}

	for i, msg := range sequence {
		model = pressMsg(model, msg)
		_ = model.View()
		layout := model.CurrentLayout()
		if !layout.visible[model.FocusedPanel()] {
			t.Fatalf("step %d: focused panel %s hidden for size class %s", i, panelLabel(model.FocusedPanel()), layout.sizeClass)
		}

		model = pressKey(model, tea.KeyMsg{Type: tea.KeyTab})
		_ = model.View()
		layout = model.CurrentLayout()
		if !layout.visible[model.FocusedPanel()] {
			t.Fatalf("step %d after tab: focused panel %s hidden for size class %s", i, panelLabel(model.FocusedPanel()), layout.sizeClass)
		}
	}
}

func TestTmuxResizeTransitionsDeterministic(t *testing.T) {
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{ModifierReliabilityUncertain: true})
	sequence := []tea.WindowSizeMsg{
		{Width: 95, Height: 28},
		{Width: 95, Height: 28},
		{Width: 70, Height: 22},
		{Width: 150, Height: 34},
		{Width: 68, Height: 24},
		{Width: 150, Height: 34},
		{Width: 150, Height: 34},
	}

	wantByIndex := []SizeClass{SizeS, SizeS, SizeXS, SizeL, SizeXS, SizeL, SizeL}
	for i, msg := range sequence {
		model = pressMsg(model, msg)
		if got, want := model.SizeClass(), wantByIndex[i]; got != want {
			t.Fatalf("step %d size class = %s, want %s", i, got, want)
		}
		layout := model.CurrentLayout()
		if !layout.visible[model.FocusedPanel()] {
			t.Fatalf("step %d: focused panel %s hidden for size class %s", i, panelLabel(model.FocusedPanel()), layout.sizeClass)
		}
	}
}

func TestRepresentativeResponsiveLayoutsRemainReadableEX253(t *testing.T) {
	cases := []struct {
		name             string
		width            int
		height           int
		wantStatusHint   string
		wantSidebarLabel string
	}{
		{
			name:             "tablet-width-100x34",
			width:            100,
			height:           34,
			wantSidebarLabel: "INBOX",
		},
		{
			name:           "compact-width-86x30",
			width:          86,
			height:         30,
			wantStatusHint: "Show: 1 sidebar",
		},
		{
			name:           "xs-width-72x26",
			width:          72,
			height:         26,
			wantStatusHint: "Show: 1 sidebar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := NewModel(DefaultState())
			model = pressMsg(model, tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			model.statusMessage = "snapshot"
			model.tourActive = true

			view := model.View()
			lines := strings.Split(view, "\n")
			if got := len(lines); got != tc.height {
				t.Fatalf("view line count = %d, want %d\n%s", got, tc.height, view)
			}

			statusLine := lines[len(lines)-3]
			if strings.Contains(statusLine, "\n") {
				t.Fatalf("status line should remain bounded: %q", statusLine)
			}
			if tc.wantStatusHint != "" && !strings.Contains(statusLine, tc.wantStatusHint) {
				t.Fatalf("status line = %q, want hidden-pane hint %q", statusLine, tc.wantStatusHint)
			}
			if tc.wantSidebarLabel != "" && !strings.Contains(view, tc.wantSidebarLabel) {
				t.Fatalf("view should keep sidebar labels readable at %dx%d:\n%s", tc.width, tc.height, view)
			}
		})
	}
}
