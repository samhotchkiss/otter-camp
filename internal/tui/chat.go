package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
)

type ChatScope string

const (
	ScopeOrg     ChatScope = "org"
	ScopeProject ChatScope = "project"
	ScopeTask    ChatScope = "task"
)

var scopeOrder = []ChatScope{ScopeOrg, ScopeProject, ScopeTask}

var (
	headingMarkerPattern = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	boldMarkerPattern    = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
)

type ToolCallStatus struct {
	ID     string
	Name   string
	Status string
	Result string
}

type ChatMessage struct {
	ID        string
	Role      string
	Content   string
	Finalized bool
	Timestamp time.Time
	ToolCalls []ToolCallStatus
}

type QueuedMessage struct {
	Text   string
	Steer  bool
	Edited bool
}

func toolCallIdentity(call ToolCallStatus, fallbackIndex int) string {
	if strings.TrimSpace(call.ID) != "" {
		return strings.TrimSpace(call.ID)
	}
	if strings.TrimSpace(call.Name) != "" {
		return strings.TrimSpace(call.Name)
	}
	return fmt.Sprintf("tool-%d", fallbackIndex)
}

func normalizeScope(raw string) ChatScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ScopeOrg):
		return ScopeOrg
	case string(ScopeTask):
		return ScopeTask
	default:
		return ScopeProject
	}
}

func cycleScope(current ChatScope, forward bool) ChatScope {
	for i, scope := range scopeOrder {
		if scope != current {
			continue
		}
		if forward {
			return scopeOrder[(i+1)%len(scopeOrder)]
		}
		return scopeOrder[(i+len(scopeOrder)-1)%len(scopeOrder)]
	}
	if forward {
		return ScopeProject
	}
	return ScopeTask
}

func sessionForScope(scope ChatScope) string {
	switch scope {
	case ScopeOrg:
		return "session-org-general"
	case ScopeTask:
		return "session-task-current"
	default:
		return fmt.Sprintf("session-%s-current", scope)
	}
}

func normalizeRole(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user", "assistant", "system", "tool", "interjection":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "assistant"
	}
}

func markdownToPlain(raw string, width int) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if width < 20 {
		width = 20
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("auto"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return text
	}

	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}

	cleaned := strings.TrimSpace(rendered)
	if cleaned == "" {
		return ""
	}
	if containsLiteralMarkdownMarkers(cleaned) {
		cleaned = normalizeRenderedMarkdown(cleaned)
	}
	return cleaned
}

func containsLiteralMarkdownMarkers(rendered string) bool {
	if strings.Contains(rendered, "**") {
		return true
	}
	for _, line := range strings.Split(rendered, "\n") {
		if headingMarkerPattern.MatchString(line) {
			return true
		}
	}
	return false
}

func normalizeRenderedMarkdown(rendered string) string {
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		indentLen := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if headingMarkerPattern.MatchString(trimmed) {
			headingText := headingMarkerPattern.ReplaceAllString(trimmed, "")
			if headingText != "" {
				lines[i] = strings.Repeat(" ", indentLen) + styleBold.Render(headingText)
				continue
			}
		}
		lines[i] = boldMarkerPattern.ReplaceAllStringFunc(line, func(token string) string {
			matches := boldMarkerPattern.FindStringSubmatch(token)
			if len(matches) != 2 {
				return token
			}
			return styleBold.Render(matches[1])
		})
	}
	return strings.Join(lines, "\n")
}

func autocompleteMention(seed string) string {
	candidates := []string{"@frank", "@lori", "@ellie"}
	normalized := strings.ToLower(strings.TrimSpace(seed))
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, normalized) {
			return candidate
		}
	}
	return "@frank"
}
