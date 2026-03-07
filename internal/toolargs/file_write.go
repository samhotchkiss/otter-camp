package toolargs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	fileWritePathPattern       = regexp.MustCompile(`(?s)"path"\s*:\s*"((?:\\.|[^"\\])*)"`)
	fileWriteContentPattern    = regexp.MustCompile(`(?s)"content"\s*:\s*"((?:\\.|[^"\\])*)"`)
	fileWriteEncodingPattern   = regexp.MustCompile(`(?s)"encoding"\s*:\s*"((?:\\.|[^"\\])*)"`)
	fileWriteCreateDirsPattern = regexp.MustCompile(`(?s)"create_dirs"\s*:\s*(true|false)`)
	fileWriteLooseKeys         = []string{"path", "content", "contents", "body", "text", "encoding", "create_dirs"}
)

// Normalize returns a shallow copy of input with tool-specific argument recovery applied.
func Normalize(toolName string, input map[string]any) map[string]any {
	cloned := cloneMap(input)
	switch canonicalToolName(toolName) {
	case "file.write":
		return normalizeFileWriteInput(cloned)
	default:
		return cloned
	}
}

// AttemptFingerprint returns a stable fingerprint for one attempted tool invocation.
func AttemptFingerprint(toolName string, input map[string]any) string {
	name := canonicalToolName(toolName)
	normalized := Normalize(name, input)
	switch name {
	case "file.write":
		return fileWriteAttemptFingerprint(input, normalized)
	default:
		return strings.ToLower(name)
	}
}

func normalizeFileWriteInput(input map[string]any) map[string]any {
	raw, ok := rawArgumentString(input)
	if !ok {
		return input
	}

	if decoded, ok := decodeJSONObject(raw); ok {
		mergeRecoveredFields(input, decoded)
		delete(input, "_raw")
		return input
	}

	if shouldRecoverString(input, "path") {
		if recovered, ok := recoverJSONStringField(raw, fileWritePathPattern); ok {
			input["path"] = recovered
		}
	}
	if shouldRecoverString(input, "content") {
		if recovered, ok := recoverFileWriteContent(raw); ok {
			input["content"] = recovered
		} else if recovered, ok := recoverJSONStringField(raw, fileWriteContentPattern); ok {
			input["content"] = recovered
		}
	}
	if shouldRecoverString(input, "encoding") {
		if recovered, ok := recoverJSONStringField(raw, fileWriteEncodingPattern); ok {
			input["encoding"] = recovered
		}
	}
	if _, exists := input["create_dirs"]; !exists {
		if recovered, ok := recoverJSONBoolField(raw, fileWriteCreateDirsPattern); ok {
			input["create_dirs"] = recovered
		}
	}
	delete(input, "_raw")
	return input
}

func fileWriteAttemptFingerprint(original, normalized map[string]any) string {
	pathValue, hasPath := stringField(normalized, "path")
	contentValue, hasContent := stringField(normalized, "content")
	encodingValue, hasEncoding := stringField(normalized, "encoding")
	createDirsValue, hasCreateDirs := boolField(normalized, "create_dirs")

	builder := strings.Builder{}
	builder.WriteString("tool=file.write")
	builder.WriteString("\npath=")
	if hasPath {
		builder.WriteString(pathValue)
	}
	builder.WriteString("\ncontent_sha=")
	if hasContent {
		builder.WriteString(hashString(contentValue))
	}
	builder.WriteString("\nencoding=")
	if hasEncoding {
		builder.WriteString(strings.ToLower(strings.TrimSpace(encodingValue)))
	}
	builder.WriteString("\ncreate_dirs=")
	if hasCreateDirs {
		builder.WriteString(strconv.FormatBool(createDirsValue))
	}
	if (!hasPath || !hasContent) && original != nil {
		if raw, ok := rawArgumentString(original); ok {
			builder.WriteString("\nraw_sha=")
			builder.WriteString(hashString(raw))
		}
	}
	return "file.write:" + hashString(builder.String())
}

func canonicalToolName(toolName string) string {
	trimmed := strings.ToLower(strings.TrimSpace(toolName))
	switch trimmed {
	case "file_write":
		return "file.write"
	default:
		return trimmed
	}
}

func rawArgumentString(input map[string]any) (string, bool) {
	if input == nil {
		return "", false
	}
	raw, ok := input["_raw"]
	if !ok || raw == nil {
		return "", false
	}
	switch typed := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	default:
		trimmed := strings.TrimSpace(fmt.Sprintf("%v", typed))
		return trimmed, trimmed != ""
	}
}

func decodeJSONObject(raw string) (map[string]any, bool) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil || decoded == nil {
		return nil, false
	}
	return decoded, true
}

func mergeRecoveredFields(target, recovered map[string]any) {
	if target == nil || recovered == nil {
		return
	}
	for _, key := range []string{"path", "encoding", "create_dirs"} {
		value, exists := recovered[key]
		if !exists {
			continue
		}
		switch key {
		case "path", "content", "encoding":
			if !shouldRecoverString(target, key) {
				continue
			}
		case "create_dirs":
			if _, exists := target[key]; exists {
				continue
			}
		}
		target[key] = value
	}
	if shouldRecoverString(target, "content") {
		if recoveredValue, ok := lookupRecoveredField(recovered, "content", "contents", "body", "text"); ok {
			target["content"] = recoveredValue
		}
	}
}

func lookupRecoveredField(recovered map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		value, exists := recovered[key]
		if !exists || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
		default:
			if strings.TrimSpace(fmt.Sprintf("%v", typed)) == "" {
				continue
			}
		}
		return value, true
	}
	return nil, false
}

func shouldRecoverString(input map[string]any, key string) bool {
	if input == nil {
		return true
	}
	value, exists := input[key]
	if !exists || value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed)) == ""
	}
}

func recoverJSONStringField(raw string, pattern *regexp.Regexp) (string, bool) {
	if pattern == nil {
		return "", false
	}
	matches := pattern.FindStringSubmatch(raw)
	if len(matches) != 2 {
		return "", false
	}
	unquoted, err := strconv.Unquote(`"` + matches[1] + `"`)
	if err != nil {
		return "", false
	}
	return unquoted, true
}

func recoverJSONBoolField(raw string, pattern *regexp.Regexp) (bool, bool) {
	if pattern == nil {
		return false, false
	}
	matches := pattern.FindStringSubmatch(raw)
	if len(matches) != 2 {
		return false, false
	}
	parsed, err := strconv.ParseBool(matches[1])
	if err != nil {
		return false, false
	}
	return parsed, true
}

func recoverFileWriteContent(raw string) (string, bool) {
	for _, key := range []string{"content", "contents", "body", "text"} {
		if recovered, ok := recoverLenientJSONLikeStringField(raw, key); ok {
			return recovered, true
		}
	}
	return "", false
}

func recoverLenientJSONLikeStringField(raw, key string) (string, bool) {
	keyIndex := strings.Index(raw, `"`+key+`"`)
	if keyIndex < 0 {
		return "", false
	}
	cursor := keyIndex + len(key) + 2
	for cursor < len(raw) && isJSONWhitespace(raw[cursor]) {
		cursor++
	}
	if cursor >= len(raw) || raw[cursor] != ':' {
		return "", false
	}
	cursor++
	for cursor < len(raw) && isJSONWhitespace(raw[cursor]) {
		cursor++
	}
	if cursor >= len(raw) {
		return "", false
	}
	if raw[cursor] != '"' {
		return "", false
	}

	start := cursor + 1
	for idx := start; idx < len(raw); idx++ {
		if raw[idx] != '"' {
			continue
		}
		if !isLikelyFieldTerminator(raw, idx+1) {
			continue
		}
		return decodeRecoveredString(raw[start:idx])
	}
	return decodeRecoveredString(strings.TrimRightFunc(raw[start:], func(r rune) bool {
		return r == '}' || isJSONWhitespace(byte(r))
	}))
}

func isLikelyFieldTerminator(raw string, cursor int) bool {
	for cursor < len(raw) && isJSONWhitespace(raw[cursor]) {
		cursor++
	}
	if cursor >= len(raw) {
		return true
	}
	if raw[cursor] == '}' {
		return true
	}
	if raw[cursor] != ',' {
		return false
	}
	cursor++
	for cursor < len(raw) && isJSONWhitespace(raw[cursor]) {
		cursor++
	}
	if cursor >= len(raw) {
		return true
	}
	for _, key := range fileWriteLooseKeys {
		if strings.HasPrefix(raw[cursor:], `"`+key+`"`) {
			return true
		}
	}
	return false
}

func decodeRecoveredString(value string) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	if strict, err := strconv.Unquote(`"` + value + `"`); err == nil {
		return strict, true
	}
	replacer := strings.NewReplacer(
		`\r`, "\r",
		`\n`, "\n",
		`\t`, "\t",
		`\"`, `"`,
		`\\`, `\`,
	)
	return replacer.Replace(value), true
}

func isJSONWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func stringField(input map[string]any, key string) (string, bool) {
	if input == nil {
		return "", false
	}
	value, exists := input[key]
	if !exists || value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	default:
		return fmt.Sprintf("%v", typed), true
	}
}

func boolField(input map[string]any, key string) (bool, bool) {
	if input == nil {
		return false, false
	}
	value, exists := input[key]
	if !exists || value == nil {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, false
		}
		return parsed, true
	default:
		return false, false
	}
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
