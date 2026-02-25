package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

type ChatScope string

const (
	ScopeOrg     ChatScope = "org"
	ScopeProject ChatScope = "project"
	ScopeTask    ChatScope = "task"
)

var scopeOrder = []ChatScope{ScopeOrg, ScopeProject, ScopeTask}

type ToolCallStatus struct {
	Name   string
	Status string
}

type ChatMessage struct {
	ID        string
	Role      string
	Content   string
	Finalized bool
	ToolCalls []ToolCallStatus
}

type QueuedMessage struct {
	Text   string
	Steer  bool
	Edited bool
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
	return fmt.Sprintf("%s-session", scope)
}

func normalizeRole(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user", "assistant", "system", "tool":
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
		glamour.WithStandardStyle("notty"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return text
	}

	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimSpace(rendered)
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
