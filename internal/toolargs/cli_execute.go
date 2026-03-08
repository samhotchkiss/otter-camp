package toolargs

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	cliExecuteTimeoutSecondsPattern = regexp.MustCompile(`(?s)"timeout_seconds"\s*:\s*(-?[0-9]+)`)
	cliExecuteTimeoutMSPattern      = regexp.MustCompile(`(?s)"timeout_ms"\s*:\s*(-?[0-9]+)`)
	cliExecuteLooseKeys             = []string{
		"command",
		"cmd",
		"script",
		"shell_command",
		"working_directory",
		"working_dir",
		"cwd",
		"timeout_seconds",
		"timeout_ms",
		"env_overrides",
		"env",
	}
)

func normalizeCLIExecuteInput(input map[string]any) map[string]any {
	mergeCLIExecuteAliasFields(input)

	raw, ok := rawArgumentString(input)
	if !ok {
		return input
	}

	if decoded, ok := decodeJSONObject(raw); ok {
		mergeCLIExecuteRecoveredFields(input, decoded)
		delete(input, "_raw")
		return input
	}

	if shouldRecoverString(input, "command") {
		if recovered, ok := recoverCLIExecuteCommand(raw); ok {
			input["command"] = recovered
		} else if !looksLikeJSONObject(raw) {
			input["command"] = raw
		}
	}
	if shouldRecoverString(input, "working_directory") {
		if recovered, ok := recoverCLIExecuteWorkingDirectory(raw); ok {
			input["working_directory"] = recovered
		}
	}
	if _, exists := input["timeout_seconds"]; !exists {
		if recovered, ok := recoverJSONIntField(raw, cliExecuteTimeoutSecondsPattern); ok {
			input["timeout_seconds"] = recovered
		} else if recovered, ok := recoverJSONIntField(raw, cliExecuteTimeoutMSPattern); ok {
			input["timeout_seconds"] = timeoutMillisecondsToSeconds(recovered)
		}
	}
	delete(input, "_raw")
	return input
}

func cliExecuteAttemptFingerprint(original, normalized map[string]any) string {
	commandValue, hasCommand := stringField(normalized, "command")
	workingDirectoryValue, hasWorkingDirectory := stringField(normalized, "working_directory")
	timeoutSecondsValue, hasTimeoutSeconds := intField(normalized, "timeout_seconds")
	envOverridesValue, hasEnvOverrides := stringMapAnyField(normalized, "env_overrides")

	builder := strings.Builder{}
	builder.WriteString("tool=cli.execute")
	builder.WriteString("\ncommand=")
	if hasCommand {
		builder.WriteString(commandValue)
	}
	builder.WriteString("\nworking_directory=")
	if hasWorkingDirectory {
		builder.WriteString(workingDirectoryValue)
	}
	builder.WriteString("\ntimeout_seconds=")
	if hasTimeoutSeconds {
		builder.WriteString(strconv.Itoa(timeoutSecondsValue))
	}
	builder.WriteString("\nenv_overrides_sha=")
	if hasEnvOverrides {
		builder.WriteString(hashString(stableStringMapAny(envOverridesValue)))
	}
	if !hasCommand && original != nil {
		if raw, ok := rawArgumentString(original); ok {
			builder.WriteString("\nraw_sha=")
			builder.WriteString(hashString(raw))
		}
	}
	return "cli.execute:" + hashString(builder.String())
}

func mergeCLIExecuteAliasFields(input map[string]any) {
	if input == nil {
		return
	}

	if shouldRecoverString(input, "command") {
		if recovered, ok := lookupRecoveredString(input, "command", "cmd", "script", "shell_command"); ok {
			input["command"] = recovered
		}
	}
	if shouldRecoverString(input, "working_directory") {
		if recovered, ok := lookupRecoveredString(input, "working_directory", "working_dir", "cwd"); ok {
			input["working_directory"] = recovered
		}
	}
	if _, exists := input["timeout_seconds"]; !exists {
		if recovered, ok := lookupRecoveredInt(input, "timeout_seconds"); ok {
			input["timeout_seconds"] = recovered
		} else if recovered, ok := lookupRecoveredInt(input, "timeout_ms"); ok {
			input["timeout_seconds"] = timeoutMillisecondsToSeconds(recovered)
		}
	}
	if _, exists := input["env_overrides"]; !exists {
		if recovered, ok := lookupRecoveredStringMapAny(input, "env_overrides", "env"); ok {
			input["env_overrides"] = recovered
		}
	}
}

func mergeCLIExecuteRecoveredFields(target, recovered map[string]any) {
	if target == nil || recovered == nil {
		return
	}
	recoveredClone := cloneMap(recovered)
	mergeCLIExecuteAliasFields(recoveredClone)

	if shouldRecoverString(target, "command") {
		if recoveredValue, ok := lookupRecoveredString(recoveredClone, "command"); ok {
			target["command"] = recoveredValue
		}
	}
	if shouldRecoverString(target, "working_directory") {
		if recoveredValue, ok := lookupRecoveredString(recoveredClone, "working_directory"); ok {
			target["working_directory"] = recoveredValue
		}
	}
	if _, exists := target["timeout_seconds"]; !exists {
		if recoveredValue, ok := lookupRecoveredInt(recoveredClone, "timeout_seconds"); ok {
			target["timeout_seconds"] = recoveredValue
		}
	}
	if _, exists := target["env_overrides"]; !exists {
		if recoveredValue, ok := lookupRecoveredStringMapAny(recoveredClone, "env_overrides"); ok {
			target["env_overrides"] = recoveredValue
		}
	}
}

func recoverCLIExecuteCommand(raw string) (string, bool) {
	for _, key := range []string{"command", "cmd", "script", "shell_command"} {
		if recovered, ok := recoverLenientJSONLikeStringFieldWithKeys(raw, key, cliExecuteLooseKeys); ok {
			return recovered, true
		}
	}
	return "", false
}

func recoverCLIExecuteWorkingDirectory(raw string) (string, bool) {
	for _, key := range []string{"working_directory", "working_dir", "cwd"} {
		if recovered, ok := recoverLenientJSONLikeStringFieldWithKeys(raw, key, cliExecuteLooseKeys); ok {
			return recovered, true
		}
	}
	return "", false
}

func recoverJSONIntField(raw string, pattern *regexp.Regexp) (int, bool) {
	if pattern == nil {
		return 0, false
	}
	matches := pattern.FindStringSubmatch(raw)
	if len(matches) != 2 {
		return 0, false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(matches[1]))
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func lookupRecoveredString(input map[string]any, keys ...string) (string, bool) {
	value, ok := lookupRecoveredField(input, keys...)
	if !ok {
		return "", false
	}
	recovered := strings.TrimSpace(fmt.Sprintf("%v", value))
	return recovered, recovered != ""
}

func lookupRecoveredInt(input map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		if recovered, ok := intField(input, key); ok {
			return recovered, true
		}
	}
	return 0, false
}

func lookupRecoveredStringMapAny(input map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if recovered, ok := stringMapAnyField(input, key); ok {
			return recovered, true
		}
	}
	return nil, false
}

func intField(input map[string]any, key string) (int, bool) {
	if input == nil {
		return 0, false
	}
	value, exists := input[key]
	if !exists || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func stringMapAnyField(input map[string]any, key string) (map[string]any, bool) {
	if input == nil {
		return nil, false
	}
	value, exists := input[key]
	if !exists || value == nil {
		return nil, false
	}

	result := map[string]any{}
	switch typed := value.(type) {
	case map[string]any:
		for itemKey, itemValue := range typed {
			if strings.TrimSpace(itemKey) == "" {
				continue
			}
			result[itemKey] = fmt.Sprintf("%v", itemValue)
		}
	case map[string]string:
		for itemKey, itemValue := range typed {
			if strings.TrimSpace(itemKey) == "" {
				continue
			}
			result[itemKey] = itemValue
		}
	default:
		return nil, false
	}

	if len(result) == 0 {
		return nil, false
	}
	return result, true
}

func stableStringMapAny(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	builder := strings.Builder{}
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(strings.TrimSpace(fmt.Sprintf("%v", input[key])))
		builder.WriteString("\n")
	}
	return builder.String()
}

func looksLikeJSONObject(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func timeoutMillisecondsToSeconds(value int) int {
	if value <= 0 {
		return 0
	}
	return (value + 999) / 1000
}
