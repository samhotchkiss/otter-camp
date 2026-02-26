//go:build e2e

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTmuxCommandPaletteFallbackWorkflow(t *testing.T) {
	model := NewModelWithRuntime(DefaultState(), RuntimeHints{ModifierReliabilityUncertain: true})
	model = pressTMUXE2EMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})

	model = runTMUXE2ECommand(model, "inbox")
	model = runTMUXE2ECommand(model, "inbox open")
	if got := model.MainView(); got != ViewTask {
		t.Fatalf("main view after :inbox open = %s, want %s", got, ViewTask)
	}

	model = runTMUXE2ECommand(model, "focus chat")
	for _, r := range []rune("palette fallback path") {
		model = pressTMUXE2EMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = runTMUXE2ECommand(model, "send")
	if !model.ActiveTurn() {
		t.Fatal("active turn should be true after :send")
	}

	initialMain := model.MainView()
	model = runTMUXE2ECommand(model, "frank")
	if got := model.ActiveChatSession(); got != "session-org-general" {
		t.Fatalf("session after :frank = %q, want session-org-general", got)
	}
	if got := model.MainView(); got != initialMain {
		t.Fatalf("main view changed after :frank: got=%s want=%s", got, initialMain)
	}

	view := model.View()
	if !strings.Contains(view, "tmux-safe commands:") {
		t.Fatalf("view missing tmux command fallback help: %q", view)
	}
}

func runTMUXE2ECommand(model Model, command string) Model {
	model = pressTMUXE2EMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	for _, r := range []rune(command) {
		model = pressTMUXE2EMsg(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return pressTMUXE2EMsg(model, tea.KeyMsg{Type: tea.KeyEnter})
}

func pressTMUXE2EMsg(model Model, msg tea.Msg) Model {
	updated, _ := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		panic("unexpected model type")
	}
	return next
}
