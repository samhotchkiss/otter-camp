package tui

import (
	"strings"
	"testing"
	"time"
)

func TestChatHeaderSegmentsIncludesTimestampWhenSpaceAllows(t *testing.T) {
	ts := time.Date(2026, time.January, 2, 15, 4, 0, 0, time.Local)
	left, right, gap := chatHeaderSegments("Frank", ts, 20)

	if left != "Frank" {
		t.Fatalf("left label = %q, want %q", left, "Frank")
	}
	want := ts.Local().Format("15:04")
	if right != want {
		t.Fatalf("right label = %q, want %q", right, want)
	}
	if gap < 1 {
		t.Fatalf("gap = %d, want >= 1", gap)
	}
}

func TestChatHeaderSegmentsDropsTimestampWhenNarrow(t *testing.T) {
	ts := time.Date(2026, time.January, 2, 15, 4, 0, 0, time.Local)
	_, right, _ := chatHeaderSegments("Frank", ts, 5)

	if right != "" {
		t.Fatalf("right label = %q, want empty for narrow width", right)
	}
}

func TestChatViewportLinesClampsOffset(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4", "5", "6"}
	visible, offset, maxOffset := chatViewportLines(lines, 3, 99)

	if maxOffset != 4 {
		t.Fatalf("max offset = %d, want 4", maxOffset)
	}
	if offset != 4 {
		t.Fatalf("clamped offset = %d, want 4", offset)
	}
	if got := strings.Join(visible, ","); got != "0,1,2" {
		t.Fatalf("visible lines = %q, want %q", got, "0,1,2")
	}
}

func TestChatPanelKeepsInputVisibleWithLongHistory(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	for i := 0; i < 40; i++ {
		model.appendMessage("seed", "assistant", strings.Repeat("very long message ", 6), true)
	}

	panel := model.renderChatPanel(80, 20, true)
	if !strings.Contains(panel, "▌") {
		t.Fatalf("chat input cursor missing from panel: %q", panel)
	}
}

func TestChatHeaderShowsAllScopeLevel(t *testing.T) {
	model := NewModel(DefaultState())
	model.switchScope(ScopeTask)

	panel := model.renderChatPanel(80, 20, true)
	for _, token := range []string{"[task]", "[project]", "[org]"} {
		if !strings.Contains(panel, token) {
			t.Fatalf("chat header missing scope token %q: %q", token, panel)
		}
	}
	if strings.Contains(panel, "session-task-current") {
		t.Fatalf("chat header should not show raw task scope session id: %q", panel)
	}

	model.switchScope(ScopeOrg)
	panel = model.renderChatPanel(80, 20, true)
	if !strings.Contains(panel, "General / Frank") {
		t.Fatalf("org scope header regressed, missing General / Frank: %q", panel)
	}
}

func TestInterjectionMessagesRenderWithInterjectedLabel(t *testing.T) {
	model := NewModel(DefaultState())
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-interjection",
			Role:      "interjection",
			Content:   "Paused execution for policy review.",
			Finalized: true,
			Timestamp: time.Date(2026, time.January, 2, 15, 6, 0, 0, time.Local),
		},
	}

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(rendered, "(interjected)") {
		t.Fatalf("interjection label missing from rendered output: %q", rendered)
	}
	if !strings.Contains(rendered, "Paused execution for policy review.") {
		t.Fatalf("interjection content missing from rendered output: %q", rendered)
	}
}

func TestMarkdownRenderedInChatMessages(t *testing.T) {
	model := NewModel(DefaultState())
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-markdown",
			Role:      "assistant",
			Content:   "### Heading\n\n**bold** with `code`\n- item",
			Finalized: true,
			Timestamp: time.Date(2026, time.January, 2, 15, 4, 0, 0, time.Local),
		},
	}

	lines := model.renderChatMessages(80)
	rendered := strings.Join(lines, "\n")
	for _, raw := range []string{"### Heading", "**bold**", "`code`"} {
		if strings.Contains(rendered, raw) {
			t.Fatalf("raw markdown marker %q should not be present after rendering: %q", raw, rendered)
		}
	}
	for _, want := range []string{"Heading", "bold", "code", "item"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown missing expected text %q: %q", want, rendered)
		}
	}
}

func TestStreamingMessageKeepsLiteralMarkdown(t *testing.T) {
	model := NewModel(DefaultState())
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-streaming",
			Role:      "assistant",
			Content:   "**streaming**",
			Finalized: false,
			Timestamp: time.Date(2026, time.January, 2, 15, 5, 0, 0, time.Local),
		},
	}

	lines := model.renderChatMessages(80)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "**streaming**") {
		t.Fatalf("streaming markdown should remain literal until finalized: %q", rendered)
	}
	if !strings.Contains(rendered, "▌") {
		t.Fatalf("streaming cursor missing: %q", rendered)
	}
}

func TestNormalizeRenderedMarkdownRemovesInlineCodeBackticks(t *testing.T) {
	raw := "### Heading\n\n**bold** and `code`"
	normalized := normalizeRenderedMarkdown(raw)
	if strings.Contains(normalized, "`code`") {
		t.Fatalf("inline code backticks were not removed: %q", normalized)
	}
	for _, want := range []string{"Heading", "bold", "code"} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("normalized output missing %q: %q", want, normalized)
		}
	}
}
