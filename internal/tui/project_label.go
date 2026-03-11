package tui

import "strings"

// ResolveProjectLabel returns the operator-facing project label used across the
// sidebar, dashboard, project view, and task context.
func ResolveProjectLabel(displayName, slug, sessionTitle string) string {
	if label := cleanProjectDisplayLabel(displayName); label != "" {
		return label
	}
	if label := strings.TrimSpace(slug); label != "" {
		return label
	}
	if label := cleanProjectSessionLabel(sessionTitle); label != "" {
		return label
	}
	return "Untitled project"
}

func cleanProjectDisplayLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || isSyntheticProjectIDLabel(trimmed) {
		return ""
	}
	return trimmed
}

func cleanProjectSessionLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "project session") || isSyntheticProjectIDLabel(trimmed) {
		return ""
	}
	return trimmed
}

func isSyntheticProjectIDLabel(value string) bool {
	trimmed := strings.TrimSpace(value)
	if looksLikeRawUUID(trimmed) {
		return true
	}
	if len(trimmed) <= len("Project ") || !strings.HasPrefix(strings.ToLower(trimmed), "project ") {
		return false
	}
	suffix := strings.TrimSpace(trimmed[len("Project "):])
	hexLen := 0
	for _, r := range suffix {
		switch {
		case r == '-':
			continue
		case r >= '0' && r <= '9':
			hexLen++
		case r >= 'a' && r <= 'f':
			hexLen++
		case r >= 'A' && r <= 'F':
			hexLen++
		default:
			return false
		}
	}
	return hexLen >= 8
}

func looksLikeRawUUID(value string) bool {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 36 {
		return false
	}
	return trimmed[8] == '-' && trimmed[13] == '-' && trimmed[18] == '-' && trimmed[23] == '-'
}
