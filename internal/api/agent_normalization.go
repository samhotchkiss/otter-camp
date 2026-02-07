package api

import (
	"strings"
	"time"
)

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

	return trimmed
}

func normalizeLastSeenTimestamp(t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
