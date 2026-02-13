package importer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	OpenClawSessionEventRoleUser       = "user"
	OpenClawSessionEventRoleAssistant  = "assistant"
	OpenClawSessionEventRoleToolResult = "toolResult"
)

type OpenClawSessionEvent struct {
	AgentSlug   string
	SessionID   string
	SessionPath string
	EventID     string
	ParentID    string
	Role        string
	Body        string
	CreatedAt   time.Time
	Line        int
}

type openClawSessionRawEvent struct {
	Type      string                     `json:"type"`
	ID        string                     `json:"id"`
	ParentID  string                     `json:"parentId"`
	Timestamp string                     `json:"timestamp"`
	Message   *openClawSessionRawMessage `json:"message"`
}

type openClawSessionRawMessage struct {
	Role     string          `json:"role"`
	ToolName string          `json:"toolName"`
	Content  json.RawMessage `json:"content"`
}

func ParseOpenClawSessionEvents(install *OpenClawInstallation) ([]OpenClawSessionEvent, error) {
	if install == nil {
		return nil, fmt.Errorf("installation is required")
	}

	root := strings.TrimSpace(install.RootDir)
	if root == "" {
		return nil, fmt.Errorf("openclaw root dir is required")
	}
	sourceGuard, err := NewOpenClawSourceGuard(root)
	if err != nil {
		return nil, err
	}

	sessionFiles, err := filepath.Glob(filepath.Join(root, "agents", "*", "sessions", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("failed to discover openclaw session files: %w", err)
	}
	sort.Strings(sessionFiles)

	events := make([]OpenClawSessionEvent, 0)
	for _, sessionPath := range sessionFiles {
		fileInfo, err := os.Lstat(sessionPath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat openclaw session file %s: %w", sessionPath, err)
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("openclaw session file %s must not be a symlink", sessionPath)
		}
		if err := sourceGuard.ValidateReadPath(sessionPath); err != nil {
			return nil, err
		}
		sessionEvents, err := parseOpenClawSessionFile(sessionPath)
		if err != nil {
			return nil, err
		}
		events = append(events, sessionEvents...)
	}

	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].CreatedAt.Before(events[j].CreatedAt)
		}
		if events[i].AgentSlug != events[j].AgentSlug {
			return events[i].AgentSlug < events[j].AgentSlug
		}
		if events[i].SessionPath != events[j].SessionPath {
			return events[i].SessionPath < events[j].SessionPath
		}
		return events[i].Line < events[j].Line
	})

	return events, nil
}

func parseOpenClawSessionFile(sessionPath string) ([]OpenClawSessionEvent, error) {
	file, err := os.Open(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open openclaw session file %s: %w", sessionPath, err)
	}
	defer file.Close()

	agentSlug, sessionID := deriveOpenClawSessionPathMetadata(sessionPath)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	events := make([]OpenClawSessionEvent, 0)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw openClawSessionRawEvent
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("failed to parse openclaw jsonl %s:%d: %w", sessionPath, lineNo, err)
		}

		event, include, err := normalizeOpenClawSessionEvent(raw, sessionPath, agentSlug, sessionID, lineNo)
		if err != nil {
			return nil, fmt.Errorf("failed to normalize openclaw session event %s:%d: %w", sessionPath, lineNo, err)
		}
		if !include {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed reading openclaw session file %s: %w", sessionPath, err)
	}

	return events, nil
}

func deriveOpenClawSessionPathMetadata(sessionPath string) (agentSlug, sessionID string) {
	clean := filepath.Clean(sessionPath)
	sessionID = strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))

	sessionsDir := filepath.Dir(clean)
	agentDir := filepath.Dir(sessionsDir)
	agentSlug = strings.TrimSpace(filepath.Base(agentDir))

	return agentSlug, sessionID
}

func normalizeOpenClawSessionEvent(
	raw openClawSessionRawEvent,
	sessionPath, agentSlug, sessionID string,
	lineNo int,
) (OpenClawSessionEvent, bool, error) {
	if strings.ToLower(strings.TrimSpace(raw.Type)) != "message" {
		return OpenClawSessionEvent{}, false, nil
	}
	if raw.Message == nil {
		return OpenClawSessionEvent{}, false, nil
	}

	role := strings.ToLower(strings.TrimSpace(raw.Message.Role))
	var (
		normalizedRole string
		body           string
	)

	switch role {
	case "user":
		normalizedRole = OpenClawSessionEventRoleUser
		body = extractOpenClawSessionContentText(raw.Message.Content, false)
	case "assistant":
		normalizedRole = OpenClawSessionEventRoleAssistant
		body = extractOpenClawSessionContentText(raw.Message.Content, false)
	case "toolresult", "tool_result":
		normalizedRole = OpenClawSessionEventRoleToolResult
		body = summarizeOpenClawToolResult(raw.Message.ToolName, raw.Message.Content)
	default:
		return OpenClawSessionEvent{}, false, nil
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return OpenClawSessionEvent{}, false, nil
	}

	createdAt, err := parseOpenClawEventTimestamp(raw.Timestamp)
	if err != nil {
		return OpenClawSessionEvent{}, false, err
	}

	return OpenClawSessionEvent{
		AgentSlug:   agentSlug,
		SessionID:   sessionID,
		SessionPath: filepath.Clean(sessionPath),
		EventID:     strings.TrimSpace(raw.ID),
		ParentID:    strings.TrimSpace(raw.ParentID),
		Role:        normalizedRole,
		Body:        body,
		CreatedAt:   createdAt,
		Line:        lineNo,
	}, true, nil
}

func parseOpenClawEventTimestamp(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("timestamp is required")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format %q", trimmed)
}

func extractOpenClawSessionContentText(raw json.RawMessage, includeThinking bool) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return normalizeOpenClawSessionText(asString)
	}

	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			text := extractOpenClawSessionBlockText(block, includeThinking)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}

	var values []any
	if err := json.Unmarshal(raw, &values); err == nil {
		parts := make([]string, 0, len(values))
		for _, value := range values {
			switch typed := value.(type) {
			case string:
				text := normalizeOpenClawSessionText(typed)
				if text != "" {
					parts = append(parts, text)
				}
			case map[string]any:
				text := extractOpenClawSessionBlockText(typed, includeThinking)
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}

	return ""
}

func extractOpenClawSessionBlockText(block map[string]any, includeThinking bool) string {
	blockType := strings.ToLower(strings.TrimSpace(openClawSessionAnyString(block["type"])))
	switch blockType {
	case "text":
		return normalizeOpenClawSessionText(
			firstNonEmpty(
				openClawSessionAnyString(block["text"]),
				openClawSessionAnyString(block["content"]),
			),
		)
	case "thinking":
		if !includeThinking {
			return ""
		}
		return normalizeOpenClawSessionText(
			firstNonEmpty(
				openClawSessionAnyString(block["thinking"]),
				openClawSessionAnyString(block["text"]),
				openClawSessionAnyString(block["content"]),
			),
		)
	default:
		if blockType == "" {
			return normalizeOpenClawSessionText(openClawSessionAnyString(block["text"]))
		}
		return ""
	}
}

func summarizeOpenClawToolResult(toolName string, content json.RawMessage) string {
	toolName = strings.TrimSpace(toolName)
	prefix := "Tool result"
	if toolName != "" {
		prefix = fmt.Sprintf("Tool %s result", toolName)
	}

	summary := extractOpenClawSessionContentText(content, false)
	if summary == "" {
		summary = extractOpenClawToolResultFallbackText(content)
	}
	summary = truncateOpenClawSessionText(summary, 220)
	if summary == "" {
		return prefix
	}
	return prefix + ": " + summary
}

func extractOpenClawToolResultFallbackText(content json.RawMessage) string {
	var parsed any
	if err := json.Unmarshal(content, &parsed); err != nil {
		return ""
	}

	parts := make([]string, 0, 3)
	collectOpenClawToolResultText(parsed, &parts, 0)
	return strings.Join(parts, " | ")
}

func collectOpenClawToolResultText(value any, parts *[]string, depth int) {
	if depth > 4 || len(*parts) >= 3 {
		return
	}

	switch typed := value.(type) {
	case string:
		text := normalizeOpenClawSessionText(typed)
		if text != "" {
			*parts = append(*parts, text)
		}
	case []any:
		for _, item := range typed {
			collectOpenClawToolResultText(item, parts, depth+1)
			if len(*parts) >= 3 {
				return
			}
		}
	case map[string]any:
		for _, key := range []string{"text", "output", "result", "content", "message"} {
			next, ok := typed[key]
			if !ok {
				continue
			}
			collectOpenClawToolResultText(next, parts, depth+1)
			if len(*parts) >= 3 {
				return
			}
		}
	}
}

func openClawSessionAnyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func normalizeOpenClawSessionText(value string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}

func truncateOpenClawSessionText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}
