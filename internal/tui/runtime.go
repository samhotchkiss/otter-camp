package tui

import "strings"

type RuntimeHints struct {
	ModifierReliabilityUncertain bool
}

func DetectRuntimeHints(getenv func(string) string) RuntimeHints {
	tmux := strings.TrimSpace(getenv("TMUX")) != "" || strings.TrimSpace(getenv("STY")) != ""
	term := strings.ToLower(strings.TrimSpace(getenv("TERM")))
	if strings.Contains(term, "screen") || strings.Contains(term, "tmux") {
		tmux = true
	}
	return RuntimeHints{ModifierReliabilityUncertain: tmux}
}
