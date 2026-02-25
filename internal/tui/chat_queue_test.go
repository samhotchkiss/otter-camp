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
	if got := model.ChatInput(); got != "queued" {
		t.Fatalf("chat input after edit action = %q, want %q", got, "queued")
	}
	if model.QueueDepth() != 0 {
		t.Fatalf("queue depth after edit action = %d, want 0", model.QueueDepth())
	}

	model.chatInput = model.ChatInput() + " updated"
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	queue := model.QueueSnapshot()
	if model.QueueDepth() != 1 {
		t.Fatalf("queue depth after re-send = %d, want 1", model.QueueDepth())
	}
	if !queue[0].Edited {
		t.Fatal("re-queued item should be marked edited")
	}
	if got := queue[0].Text; got != "queued updated" {
		t.Fatalf("re-queued text = %q, want %q", got, "queued updated")
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
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if got := model.ChatInput(); got != "@frank " {
		t.Fatalf("mention autocomplete with prefix = %q, want %q", got, "@frank ")
	}

	model = pressKey(model, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if got := model.ChatInput(); got != "@frank \n" {
		t.Fatalf("alt-enter newline input = %q, want %q", got, "@frank \n")
	}
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\n'}})
	if got := model.ChatInput(); got != "@frank \n\n" {
		t.Fatalf("shift-enter newline input = %q, want %q", got, "@frank \\n\\n")
	}

	model.chatInput = "history-entry"
	model.sendOrQueueInput()
	model.chatInput = ""
	model = pressKey(model, tea.KeyMsg{Type: tea.KeyUp})
	if got := model.ChatInput(); got != "history-entry" {
		t.Fatalf("history recall = %q, want %q", got, "history-entry")
	}
}
