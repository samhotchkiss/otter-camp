package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Panel int

const (
	SidebarPanel Panel = iota
	MainPanel
	ChatPanel
)

type Model struct {
	state          UIState
	focus          Panel
	commandMode    bool
	commandBuffer  string
	statusMessage  string
	width          int
	height         int
	quitting       bool
	sidebarVisible bool
}

func NewModel(state UIState) Model {
	normalized := normalizeState(state)
	panel, ok := panelFromView(normalized.LastActiveView)
	if !ok {
		panel = MainPanel
	}
	return Model{
		state:          normalized,
		focus:          panel,
		statusMessage:  "Tab/Shift-Tab cycle focus. Alt-1/2/3 direct focus. :focus or :quit commands available.",
		sidebarVisible: normalized.SidebarVisible,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(typed)
	default:
		return m, nil
	}
}

func (m Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commandMode {
		return m.updateCommandInput(key)
	}

	switch key.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		m.statusMessage = "Exiting TUI."
		return m, tea.Quit
	case tea.KeyTab:
		m.focus = nextPanel(m.focus)
		m.statusMessage = "Focus: " + panelLabel(m.focus)
		return m, nil
	case tea.KeyShiftTab:
		m.focus = previousPanel(m.focus)
		m.statusMessage = "Focus: " + panelLabel(m.focus)
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) == 1 && key.Runes[0] == ':' {
			m.commandMode = true
			m.commandBuffer = ":"
			m.statusMessage = "Command mode"
			return m, nil
		}
		if key.Alt && len(key.Runes) == 1 {
			switch key.Runes[0] {
			case '1':
				m.focus = SidebarPanel
			case '2':
				m.focus = MainPanel
			case '3':
				m.focus = ChatPanel
			default:
				return m, nil
			}
			m.statusMessage = "Focus: " + panelLabel(m.focus)
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) updateCommandInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.commandMode = false
		m.commandBuffer = ""
		m.statusMessage = "Command cancelled."
		return m, nil
	case tea.KeyEnter:
		m.commandMode = false
		command := strings.TrimSpace(m.commandBuffer)
		m.commandBuffer = ""
		m.executeCommand(command)
		if m.quitting {
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyBackspace:
		runes := []rune(m.commandBuffer)
		if len(runes) > 1 {
			m.commandBuffer = string(runes[:len(runes)-1])
		} else {
			m.commandBuffer = ":"
		}
		return m, nil
	case tea.KeyRunes:
		if len(key.Runes) > 0 {
			m.commandBuffer += string(key.Runes)
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) executeCommand(raw string) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, ":"))
	if trimmed == "" {
		m.statusMessage = "No command entered."
		return
	}

	fields := strings.Fields(trimmed)
	switch strings.ToLower(fields[0]) {
	case "quit":
		m.quitting = true
		m.statusMessage = "Exiting TUI."
	case "focus":
		if len(fields) != 2 {
			m.statusMessage = "Usage: :focus sidebar|main|chat"
			return
		}
		panel, ok := panelFromView(fields[1])
		if !ok {
			m.statusMessage = "Unknown panel. Use sidebar, main, or chat."
			return
		}
		m.focus = panel
		m.statusMessage = "Focus: " + panelLabel(m.focus)
	default:
		m.statusMessage = "Unknown command: " + fields[0]
	}
}

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 120
	}

	state := m.State()
	widths := panelWidths(width, state.PanelProportions, state.SidebarVisible)
	header := fmt.Sprintf(
		"%s | %s | %s",
		renderPanelHeader("Sidebar", SidebarPanel == m.focus, widths[0], state.SidebarVisible),
		renderPanelHeader("Main", MainPanel == m.focus, widths[1], true),
		renderPanelHeader("Chat", ChatPanel == m.focus, widths[2], true),
	)

	commandLine := ""
	if m.commandMode {
		commandLine = "\nCommand> " + m.commandBuffer
	}

	status := fmt.Sprintf(
		"Status: %s | View=%s | ChatSession=%s",
		m.statusMessage,
		state.LastActiveView,
		valueOrPlaceholder(state.LastActiveChatSession),
	)
	return header + "\n" + status + commandLine
}

func (m Model) FocusedPanel() Panel {
	return m.focus
}

func (m Model) Quitting() bool {
	return m.quitting
}

func (m Model) State() UIState {
	next := m.state
	next.LastActiveView = viewFromPanel(m.focus)
	next.SidebarVisible = m.sidebarVisible
	return normalizeState(next)
}

func nextPanel(panel Panel) Panel {
	return Panel((int(panel) + 1) % 3)
}

func previousPanel(panel Panel) Panel {
	return Panel((int(panel) + 2) % 3)
}

func panelFromView(view string) (Panel, bool) {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "sidebar":
		return SidebarPanel, true
	case "main":
		return MainPanel, true
	case "chat":
		return ChatPanel, true
	default:
		return MainPanel, false
	}
}

func viewFromPanel(panel Panel) string {
	switch panel {
	case SidebarPanel:
		return "sidebar"
	case ChatPanel:
		return "chat"
	default:
		return "main"
	}
}

func panelLabel(panel Panel) string {
	switch panel {
	case SidebarPanel:
		return "sidebar"
	case ChatPanel:
		return "chat"
	default:
		return "main"
	}
}

func panelWidths(total int, proportions [3]float64, sidebarVisible bool) [3]int {
	activeProportions := proportions
	if !sidebarVisible {
		activeProportions[0] = 0
		activeProportions = normalizeProportions(activeProportions, [3]float64{0, 0.6, 0.4})
	}

	sidebarWidth := int(activeProportions[0] * float64(total))
	mainWidth := int(activeProportions[1] * float64(total))
	chatWidth := total - sidebarWidth - mainWidth
	if chatWidth < 1 {
		chatWidth = 1
	}
	return [3]int{sidebarWidth, mainWidth, chatWidth}
}

func renderPanelHeader(name string, focused bool, width int, visible bool) string {
	if !visible || width <= 0 {
		return "[hidden]"
	}

	label := name
	if focused {
		label = "*" + name + "*"
	}
	text := "[" + label + "]"
	if width <= len(text) {
		return text
	}
	padding := width - len(text)
	return text + strings.Repeat(" ", padding)
}

func valueOrPlaceholder(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "none"
	}
	return trimmed
}
