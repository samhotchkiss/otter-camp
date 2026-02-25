package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResolveMainViewCommand(t *testing.T) {
	tests := map[string]MainView{
		"dashboard": ViewDashboard,
		"project":   ViewProject,
		"task":      ViewTask,
		"inbox":     ViewInbox,
		"activity":  ViewActivity,
		"agents":    ViewAgents,
		"merges":    ViewMerges,
		"schedules": ViewSchedules,
	}

	for raw, want := range tests {
		got, ok := resolveMainViewCommand(raw)
		if !ok {
			t.Fatalf("resolveMainViewCommand(%q) = not found", raw)
		}
		if got != want {
			t.Fatalf("resolveMainViewCommand(%q) = %s, want %s", raw, got, want)
		}
	}
}

func TestCommandPaletteViewCommands(t *testing.T) {
	tests := []struct {
		command string
		want    MainView
	}{
		{command: "dashboard", want: ViewDashboard},
		{command: "project", want: ViewProject},
		{command: "task", want: ViewTask},
		{command: "inbox", want: ViewInbox},
		{command: "activity", want: ViewActivity},
		{command: "agents", want: ViewAgents},
		{command: "merges", want: ViewMerges},
		{command: "schedules", want: ViewSchedules},
	}

	for _, tc := range tests {
		model := NewModel(DefaultState())
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
		for _, r := range []rune(tc.command) {
			model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
		if got := model.MainView(); got != tc.want {
			t.Fatalf(":%s resolved view = %s, want %s", tc.command, got, tc.want)
		}
	}
}
