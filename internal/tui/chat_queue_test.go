package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestQueuedMessageStateMachineSendEditSteerDeleteCancel(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel

	model.chatInput = "first"
	model.sendOrQueueInput()
	if !model.ActiveTurn() {
		t.Fatal("turn should be active after initial send")
	}
	if model.QueueDepth() != 0 {
		t.Fatalf("queue depth = %d, want 0", model.QueueDepth())
	}

	model.chatInput = "queued"
	model.sendOrQueueInput()
	if model.QueueDepth() != 1 {
		t.Fatalf("queue depth = %d, want 1", model.QueueDepth())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	queue := model.QueueSnapshot()
	if !queue[0].Edited {
		t.Fatal("queue item should be edited after e action")
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	queue = model.QueueSnapshot()
	if !queue[0].Steer {
		t.Fatal("queue item should be steer-marked after s action")
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if model.QueueDepth() != 0 {
		t.Fatalf("queue depth = %d, want 0 after delete", model.QueueDepth())
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.ActiveTurn() {
		t.Fatal("turn should be inactive after escape cancel")
	}
}

func TestChatInputBehaviorsNewlineHistoryAndMentionAutocomplete(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if got := model.ChatInput(); got != "@frank " {
		t.Fatalf("mention autocomplete = %q, want %q", got, "@frank ")
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if got := model.ChatInput(); got != "@frank \n" {
		t.Fatalf("alt-enter newline input = %q, want %q", got, "@frank \n")
	}

	model.chatInput = "history-entry"
	model.sendOrQueueInput()
	model.chatInput = ""
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyUp})
	if got := model.ChatInput(); got != "history-entry" {
		t.Fatalf("history recall = %q, want %q", got, "history-entry")
	}
}
