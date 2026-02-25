package native

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func readString(input map[string]any, key string) (string, bool) {
	if input == nil {
		return "", false
	}
	raw, ok := input[key]
	if !ok || raw == nil {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v), true
	case fmt.Stringer:
		return strings.TrimSpace(v.String()), true
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v)), true
	}
}

func readBool(input map[string]any, key string, def bool) bool {
	if input == nil {
		return def
	}
	raw, ok := input[key]
	if !ok || raw == nil {
		return def
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return def
		}
		return parsed
	default:
		return def
	}
}

func readInt(input map[string]any, key string, def int) int {
	if input == nil {
		return def
	}
	raw, ok := input[key]
	if !ok || raw == nil {
		return def
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return def
		}
		return parsed
	default:
		return def
	}
}

func readUUID(input map[string]any, key string) (uuid.UUID, bool) {
	raw, ok := readString(input, key)
	if !ok || raw == "" {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return parsed, true
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func toRFC3339(ts *time.Time) any {
	if ts == nil {
		return nil
	}
	return ts.UTC().Format(time.RFC3339)
}

func encodeCursor(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return ""
	}
	return string(decoded)
}
