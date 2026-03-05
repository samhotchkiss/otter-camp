package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestChatHeaderSegmentsIncludesTimestampWhenSpaceAllows(t *testing.T) {
	// Use today's date so chatTimestampLabel returns just "HH:MM" (same-day format)
	now := time.Now()
	ts := time.Date(now.Year(), now.Month(), now.Day(), 15, 4, 0, 0, time.Local)
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
	now := time.Now()
	ts := time.Date(now.Year(), now.Month(), now.Day(), 15, 4, 0, 0, time.Local)
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
	// Seed a selected task so "session-task-current" resolves to a title.
	model.workspace.tasks["task-current"] = &taskRecord{ID: "task-current", Title: "Current Task"}
	model.workspace.selectedTaskID = "task-current"
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
	if !strings.Contains(panel, "Frank / General") {
		t.Fatalf("org scope header regressed, missing Frank / General: %q", panel)
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
	// The markdown renderer may word-wrap with ANSI codes between words.
	if !strings.Contains(rendered, "Paused execution for policy") {
		t.Fatalf("interjection content missing from rendered output: %q", rendered)
	}
}

func TestMarkdownRenderedForAssistantMessages(t *testing.T) {
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

func TestPlainTextForUserMessages(t *testing.T) {
	model := NewModel(DefaultState())
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-user-markdown",
			Role:      "user",
			Content:   "**bold**",
			Finalized: true,
			Timestamp: time.Date(2026, time.January, 2, 15, 5, 0, 0, time.Local),
		},
	}

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(rendered, "**bold**") {
		t.Fatalf("user messages should preserve literal markdown markers: %q", rendered)
	}
}

func TestStreamingAssistantMessageRendersMarkdown(t *testing.T) {
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
	if strings.Contains(rendered, "**streaming**") {
		t.Fatalf("streaming assistant markdown should be rendered without literal markers: %q", rendered)
	}
	if !strings.Contains(rendered, "streaming") {
		t.Fatalf("streaming assistant markdown text missing from rendered output: %q", rendered)
	}
	if !strings.Contains(rendered, "▌") {
		t.Fatalf("streaming cursor missing: %q", rendered)
	}
}

func TestThinkingIndicatorShowsWhenActiveTurnAndNoStreamingMessage(t *testing.T) {
	model := NewModel(DefaultState())
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-user",
			Role:      "user",
			Content:   "Hello Frank",
			Finalized: true,
			Timestamp: time.Date(2026, time.January, 2, 15, 10, 0, 0, time.Local),
		},
	}
	// Simulate a turn having been started but no assistant response yet
	model.activeTurn = true
	model.activeTurnSessionID = ""

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(rendered, "thinking") {
		t.Fatalf("thinking indicator missing when activeTurn=true and no streaming message: %q", rendered)
	}
	if !strings.Contains(rendered, "◌") {
		t.Fatalf("◌ spinner missing when activeTurn=true and no streaming message: %q", rendered)
	}
}

func TestThinkingIndicatorHiddenWhenActiveTurnFalse(t *testing.T) {
	model := NewModel(DefaultState())
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-user",
			Role:      "user",
			Content:   "Hello Frank",
			Finalized: true,
			Timestamp: time.Date(2026, time.January, 2, 15, 10, 0, 0, time.Local),
		},
	}
	model.activeTurn = false

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	if strings.Contains(rendered, "thinking") {
		t.Fatalf("thinking indicator should not appear when activeTurn=false: %q", rendered)
	}
}

func TestThinkingIndicatorHiddenWhenAssistantAlreadyStreaming(t *testing.T) {
	model := NewModel(DefaultState())
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-user",
			Role:      "user",
			Content:   "Hello Frank",
			Finalized: true,
			Timestamp: time.Date(2026, time.January, 2, 15, 10, 0, 0, time.Local),
		},
		{
			ID:        "msg-streaming",
			Role:      "assistant",
			Content:   "I'm responding now...",
			Finalized: false, // currently streaming
			Timestamp: time.Date(2026, time.January, 2, 15, 10, 1, 0, time.Local),
		},
	}
	model.activeTurn = true

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	// Should show ▌ streaming cursor but NOT the thinking indicator
	if !strings.Contains(rendered, "▌") {
		t.Fatalf("streaming cursor missing when assistant is streaming: %q", rendered)
	}
	if strings.Contains(rendered, "thinking") {
		t.Fatalf("thinking indicator should not show when assistant is already streaming: %q", rendered)
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

func TestAssistantLabelFallsBackToFrankForOrgSession(t *testing.T) {
	model := NewModel(DefaultState())
	model.activeScope = ScopeOrg

	if got := model.assistantLabel(); got != "Frank" {
		t.Fatalf("assistantLabel() = %q, want %q in org scope", got, "Frank")
	}
}

func TestAssistantLabelUsesAgentNameForTaskSession(t *testing.T) {
	model := NewModel(DefaultState())
	model.activeScope = ScopeTask
	model.workspace.selectedTaskID = "task-abc"
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	model.workspace.tasks["task-abc"] = &taskRecord{
		ID:        "task-abc",
		AgentName: "Ellie",
	}

	if got := model.assistantLabel(); got != "Ellie" {
		t.Fatalf("assistantLabel() = %q, want %q in task scope with agent", got, "Ellie")
	}
}

func TestAssistantLabelFallsBackToFrankWhenTaskHasNoAgent(t *testing.T) {
	model := NewModel(DefaultState())
	model.activeScope = ScopeTask
	model.workspace.selectedTaskID = "task-no-agent"
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	model.workspace.tasks["task-no-agent"] = &taskRecord{ID: "task-no-agent"}

	if got := model.assistantLabel(); got != "Frank" {
		t.Fatalf("assistantLabel() = %q, want %q when task has no agent name", got, "Frank")
	}
}

func TestAssistantMessagesShowAgentNameFromTask(t *testing.T) {
	model := NewModel(DefaultState())
	model.activeScope = ScopeTask
	model.workspace.selectedTaskID = "task-pm"
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	model.workspace.tasks["task-pm"] = &taskRecord{ID: "task-pm", AgentName: "Project Manager"}
	model.chatMessages = []ChatMessage{
		{
			ID:        "msg-agent",
			Role:      "assistant",
			Content:   "I am working on this task.",
			Finalized: true,
			Timestamp: time.Date(2026, time.January, 2, 15, 10, 0, 0, time.Local),
		},
	}

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(rendered, "Project Manager") {
		t.Fatalf("agent name missing from chat message label: %q", rendered)
	}
	if strings.Contains(rendered, "\nFrank\n") || strings.HasPrefix(rendered, "Frank") {
		t.Fatalf("hardcoded Frank label should not appear when task has agent name: %q", rendered)
	}
}

func TestQueuedMessageAndHintVisibleInChatPanel(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.activeTurn = true
	model.queuedMessages = []QueuedMessage{
		{Text: "follow-up question"},
	}
	// chatInput is empty so the hint should appear
	model.chatInput = ""

	panel := model.renderChatPanel(80, 20, true)
	if !strings.Contains(panel, "q1:") {
		t.Fatalf("queued message q1 label missing from chat panel: %q", panel)
	}
	if !strings.Contains(panel, "follow-up question") {
		t.Fatalf("queued message text missing from chat panel: %q", panel)
	}
	if !strings.Contains(panel, "e·edit") {
		t.Fatalf("queue action hint missing from chat panel: %q", panel)
	}
}

func TestQueueHintHiddenWhenInputIsNonEmpty(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.activeTurn = true
	model.queuedMessages = []QueuedMessage{
		{Text: "queued text"},
	}
	model.chatInput = "typing something"

	panel := model.renderChatPanel(80, 20, true)
	if strings.Contains(panel, "e·edit") {
		t.Fatalf("queue action hint should be hidden when input is non-empty: %q", panel)
	}
}

func TestTaskScopedChatEmptyStateShowsTaskName(t *testing.T) {
	model := NewModel(DefaultState())
	model.activeScope = ScopeTask
	model.workspace.selectedTaskID = "task-xyz"
	if model.workspace.tasks == nil {
		model.workspace.tasks = make(map[string]*taskRecord)
	}
	model.workspace.tasks["task-xyz"] = &taskRecord{
		ID:         "task-xyz",
		TaskNumber: 7,
		Title:      "Build login flow",
	}
	// chatMessages is empty — should render empty state

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(rendered, "no messages yet") {
		t.Fatalf("empty state header missing: %q", rendered)
	}
	if !strings.Contains(rendered, "OC-7: Build login flow") {
		t.Fatalf("task name missing from task-scoped empty state: %q", rendered)
	}
}

func TestOrgScopedChatEmptyStateDoesNotShowTaskName(t *testing.T) {
	model := NewModel(DefaultState())
	model.activeScope = ScopeOrg
	// chatMessages is empty

	rendered := strings.Join(model.renderChatMessages(80), "\n")
	if !strings.Contains(rendered, "no messages yet") {
		t.Fatalf("empty state header missing: %q", rendered)
	}
	// Should not show any "OC-" style task label
	if strings.Contains(rendered, "OC-") {
		t.Fatalf("org-scoped empty state should not show a task label: %q", rendered)
	}
}

func TestRenderChatInputBoxWrapsMultilinePasteWithoutOverflow(t *testing.T) {
	model := NewModel(DefaultState())
	model.focus = ChatPanel
	model.chatInput = "The new Sam.blog will be a central hub\t\nEvery project in OtterCamp has a connected Git repo, right? This line should wrap safely."

	const inputWidth = 40
	box := model.renderChatInputBox(inputWidth, true)
	if strings.Contains(box, "\t") {
		t.Fatalf("rendered input box should not contain raw tabs: %q", box)
	}
	for i, line := range strings.Split(box, "\n") {
		if got := lipgloss.Width(line); got > inputWidth {
			t.Fatalf("input line %d width = %d, want <= %d: %q", i, got, inputWidth, line)
		}
	}
	if !strings.Contains(box, "Every project in OtterCamp") {
		t.Fatalf("expected multiline pasted content to remain visible in input box: %q", box)
	}
}
