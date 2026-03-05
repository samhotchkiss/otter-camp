package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChatPasteLiteralSkipsGlobalShortcuts(t *testing.T) {
	model := NewModel(DefaultState())
	model = pressMsg(model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model.focus = ChatPanel

	beforeLayout := model.CurrentLayout()
	beforeView := model.workspace.mainView

	pasted := ":heading\r\n<resize?>\n?help\n/filter\n123"
	model = pressKey(model, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(pasted),
		Paste: true,
	})

	if got, want := model.ChatInput(), normalizeChatNewlines(pasted); got != want {
		t.Fatalf("pasted chat input = %q, want %q", got, want)
	}
	if model.commandMode {
		t.Fatal("paste should not enter command mode")
	}
	if model.searchMode {
		t.Fatal("paste should not enter search mode")
	}
	if model.workspace.mainView != beforeView {
		t.Fatalf("main view changed during paste: got %v, want %v", model.workspace.mainView, beforeView)
	}
	if model.focus != ChatPanel {
		t.Fatalf("focus changed during paste: got %v, want %v", model.focus, ChatPanel)
	}
	afterLayout := model.CurrentLayout()
	if afterLayout.widths != beforeLayout.widths {
		t.Fatalf("panel widths changed during paste: before=%v after=%v", beforeLayout.widths, afterLayout.widths)
	}
}

func TestSendOrQueueInputPreservesMultilineFormatting(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	// EX-400: seed a real UUID session so send is not blocked by placeholder checks.
	model.activeSession = "11111111-2222-3333-4444-555555555555"

	original := "line 1\r\n\r\n    indented\ttext  \nline 4  "
	model.chatInput = original
	model.sendOrQueueInput()

	if len(model.chatMessages) == 0 {
		t.Fatal("expected a local user message after send")
	}
	got := model.chatMessages[len(model.chatMessages)-1].Content
	want := normalizeChatNewlines(original)
	if got != want {
		t.Fatalf("sent content = %q, want %q", got, want)
	}
	if len(model.chatHistory) == 0 || model.chatHistory[len(model.chatHistory)-1] != want {
		t.Fatalf("chat history should preserve multiline content, got %q", model.chatHistory[len(model.chatHistory)-1])
	}
}

func TestWrapTextPreserveWhitespaceKeepsIndentationAndBlankLines(t *testing.T) {
	got := wrapTextPreserveWhitespace("first\n\n    indented  line", 80)
	want := []string{"first", "", "    indented  line"}
	if len(got) != len(want) {
		t.Fatalf("wrapped line count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}
