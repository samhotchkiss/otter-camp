package cli

import (
	"regexp"
	"strings"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type Classification struct {
	RiskLevel RiskLevel
	Denied    bool
	ErrorCode string
	Pattern   string
}

type RiskClassifier struct {
	denylist []string
}

func NewRiskClassifier() *RiskClassifier {
	return &RiskClassifier{denylist: []string{
		"rm -rf /",
		"sudo",
		"su",
		"passwd",
		"chmod 777",
		"curl * | bash",
		"wget * | sh",
		"eval",
		"exec",
	}}
}

func (c *RiskClassifier) Classify(command string) RiskLevel {
	return c.Evaluate(command).RiskLevel
}

func (c *RiskClassifier) Evaluate(command string) Classification {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return Classification{RiskLevel: RiskLow}
	}

	classifier := c
	if classifier == nil {
		classifier = NewRiskClassifier()
	}

	parts := splitCompoundCommands(trimmed)
	if len(parts) == 0 {
		parts = []string{trimmed}
	}

	if pattern, ok := classifier.matchDenylist(trimmed); ok {
		return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: "command_denied", Pattern: pattern}
	}

	overall := RiskLow
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}

		if containsRedirect(candidate) {
			return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: "redirect_not_supported"}
		}
		if hasShellInjection(candidate) {
			return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: "command_denied", Pattern: "shell_injection"}
		}
		if pattern, ok := classifier.matchDenylist(candidate); ok {
			return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: "command_denied", Pattern: pattern}
		}
		if denied, code := deniedGitPush(candidate); denied {
			return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: code}
		}

		overall = maxRiskLevel(overall, classifySingle(candidate))
	}

	return Classification{RiskLevel: overall}
}

func classifySingle(command string) RiskLevel {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return RiskLow
	}

	if isDestructiveCommand(lower) {
		return RiskCritical
	}

	commandName := firstToken(lower)
	switch commandName {
	case "git":
		return classifyGitCommand(lower)
	case "curl":
		if modifiesRemote(lower) {
			return RiskHigh
		}
		return RiskLow
	case "wget", "ssh", "scp", "rsync":
		return RiskHigh
	case "npm", "go", "make", "cp", "mv", "mkdir", "touch", "chmod", "chown":
		return RiskMedium
	case "cat", "ls", "grep", "find", "head", "tail", "echo", "pwd", "which", "type", "env", "printenv", "wc", "diff", "stat":
		return RiskLow
	default:
		return RiskMedium
	}
}

func classifyGitCommand(command string) RiskLevel {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return RiskLow
	}
	sub := fields[1]
	switch sub {
	case "status", "log", "diff", "show", "branch":
		return RiskLow
	case "add", "commit", "restore", "checkout", "switch", "reset", "rebase", "merge":
		return RiskMedium
	case "push", "fetch", "pull":
		return RiskHigh
	default:
		return RiskMedium
	}
}

func isDestructiveCommand(command string) bool {
	for _, pattern := range []string{
		"rm -rf",
		"drop table",
		"truncate",
		" dd ",
		"mkfs",
		"format",
	} {
		if strings.Contains(command, pattern) {
			return true
		}
	}
	if strings.HasPrefix(command, "dd ") {
		return true
	}
	return false
}

func modifiesRemote(command string) bool {
	methodFlags := []string{"-x post", "-x put", "-x patch", "-x delete", "--request post", "--request put", "--request patch", "--request delete"}
	for _, flag := range methodFlags {
		if strings.Contains(command, flag) {
			return true
		}
	}
	return false
}

func firstToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func maxRiskLevel(left, right RiskLevel) RiskLevel {
	if riskRank(right) > riskRank(left) {
		return right
	}
	return left
}

func riskRank(level RiskLevel) int {
	switch level {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskCritical:
		return 4
	default:
		return 0
	}
}

func splitCompoundCommands(command string) []string {
	parts := make([]string, 0)
	start := 0
	inSingle := false
	inDouble := false
	inBacktick := false
	subshellDepth := 0
	escaped := false
	bytes := []byte(command)

	for i := 0; i < len(bytes); i++ {
		ch := bytes[i]

		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}

		switch ch {
		case '\'':
			if !inDouble && !inBacktick {
				inSingle = !inSingle
			}
			continue
		case '"':
			if !inSingle && !inBacktick {
				inDouble = !inDouble
			}
			continue
		case '`':
			if !inSingle && !inDouble {
				inBacktick = !inBacktick
			}
			continue
		}

		if inSingle || inDouble || inBacktick {
			continue
		}

		if ch == '$' && i+1 < len(bytes) && bytes[i+1] == '(' {
			subshellDepth++
			i++
			continue
		}
		if ch == ')' && subshellDepth > 0 {
			subshellDepth--
			continue
		}
		if subshellDepth > 0 {
			continue
		}

		separatorWidth := 0
		if i+1 < len(bytes) {
			next := bytes[i+1]
			if (ch == '&' && next == '&') || (ch == '|' && next == '|') {
				separatorWidth = 2
			}
		}
		if separatorWidth == 0 && (ch == ';' || ch == '|') {
			separatorWidth = 1
		}
		if separatorWidth == 0 {
			continue
		}

		piece := strings.TrimSpace(command[start:i])
		if piece != "" {
			parts = append(parts, piece)
		}
		start = i + separatorWidth
		if separatorWidth == 2 {
			i++
		}
	}

	last := strings.TrimSpace(command[start:])
	if last != "" {
		parts = append(parts, last)
	}
	return parts
}

func deniedGitPush(command string) (bool, string) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(command)))
	if len(fields) < 2 || fields[0] != "git" || fields[1] != "push" {
		return false, ""
	}

	force := false
	positional := make([]string, 0)
	for _, token := range fields[2:] {
		if strings.HasPrefix(token, "-") {
			if token == "--force" || token == "-f" || strings.HasPrefix(token, "--force=") {
				force = true
			}
			continue
		}
		positional = append(positional, token)
	}

	if len(positional) == 0 {
		return false, ""
	}

	branch := positional[len(positional)-1]
	normalizedBranch := strings.TrimPrefix(branch, "refs/heads/")
	if normalizedBranch == "main" || normalizedBranch == "master" {
		return true, "git_push_to_main_denied"
	}
	if force && strings.HasPrefix(normalizedBranch, "shared/") {
		return true, "git_force_push_shared_denied"
	}
	return false, ""
}

func (c *RiskClassifier) matchDenylist(command string) (string, bool) {
	normalized := normalizeCommand(command)
	for _, pattern := range c.denylist {
		n := normalizeCommand(pattern)
		if wildcardContains(normalized, n) {
			return pattern, true
		}
	}
	return "", false
}

func normalizeCommand(command string) string {
	whitespace := regexp.MustCompile(`\s+`)
	return whitespace.ReplaceAllString(strings.ToLower(strings.TrimSpace(command)), " ")
}

func wildcardContains(text, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return strings.Contains(text, pattern)
	}

	parts := strings.Split(pattern, "*")
	idx := 0
	for _, part := range parts {
		if part == "" {
			continue
		}
		next := strings.Index(text[idx:], part)
		if next < 0 {
			return false
		}
		idx += next + len(part)
	}
	return true
}

func hasShellInjection(command string) bool {
	return strings.Contains(command, "$(") || strings.Contains(command, "`")
}

func containsRedirect(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if ch == '<' || ch == '>' {
			return true
		}
	}
	return false
}
