package api

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	slackThreadTaskPattern = regexp.MustCompile(`(?i)^slack:(g-[a-z0-9]+)-thread-[a-z0-9._:-]+$`)
	webchatTaskPattern     = regexp.MustCompile(`(?i)^webchat:g-agent-([a-z0-9-]+)-main$`)
	slackEmojiPattern      = regexp.MustCompile(`:[a-z0-9_+-]+:`)
)

var slackThreadChannelHints = map[string]string{
	"g-c0abhd38u05": "essie",
}

func normalizeCurrentTask(task string) string {
	trimmed := strings.TrimSpace(task)
	if trimmed == "" {
		return ""
	}

	upper := strings.ToUpper(trimmed)
	switch upper {
	case "HEARTBEAT_OK", "HEARTBEAT OK", "HEARTBEAT":
		return ""
	}
	if strings.HasPrefix(upper, "HEARTBEAT_") {
		return ""
	}

	if humanized, ok := humanizeStructuredCurrentTask(trimmed); ok {
		return humanized
	}

	sanitized := sanitizeSlackEmojiCodes(trimmed)
	sanitized = strings.Join(strings.Fields(sanitized), " ")
	if sanitized == "" {
		return ""
	}
	return truncateText(sanitized, 120)
}

func humanizeStructuredCurrentTask(task string) (string, bool) {
	trimmed := strings.TrimSpace(task)
	if trimmed == "" {
		return "", false
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "slack:#") {
		channel := strings.TrimSpace(trimmed[len("slack:"):])
		if channel != "" {
			return fmt.Sprintf("Active in %s", channel), true
		}
	}

	if matches := slackThreadTaskPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		channelID := strings.ToLower(strings.TrimSpace(matches[1]))
		if channelName := strings.TrimSpace(slackThreadChannelHints[channelID]); channelName != "" {
			return fmt.Sprintf("Thread in #%s", channelName), true
		}
		return "Thread in Slack", true
	}

	if matches := webchatTaskPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
		workspace := toDisplayTitle(strings.TrimSpace(matches[1]))
		if workspace != "" {
			return fmt.Sprintf("Active in %s webchat", workspace), true
		}
		return "Webchat session", true
	}
	if strings.HasPrefix(lower, "webchat:") {
		return "Webchat session", true
	}

	return "", false
}

func sanitizeSlackEmojiCodes(value string) string {
	return slackEmojiPattern.ReplaceAllString(value, "")
}

func toDisplayTitle(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	parts = slices.DeleteFunc(parts, func(part string) bool {
		return strings.TrimSpace(part) == ""
	})
	for i, part := range parts {
		lower := strings.ToLower(strings.TrimSpace(part))
		parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(parts, " ")
}

func truncateText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes-3])) + "..."
}

func normalizeLastSeenTimestamp(t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
