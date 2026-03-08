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

type RiskClassifier struct{}

func NewRiskClassifier() *RiskClassifier {
	return &RiskClassifier{}
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

	segments := splitCommandChain(trimmed)
	if len(segments) == 0 {
		segments = []commandSegment{{Text: trimmed}}
	}

	if pattern, ok := matchDeniedPipeline(segments); ok {
		return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: "command_denied", Pattern: pattern}
	}

	overall := RiskLow
	for _, segment := range segments {
		candidate := strings.TrimSpace(segment.Text)
		if candidate == "" {
			continue
		}

		if pattern, ok := classifier.matchDeniedInvocation(candidate); ok {
			return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: "command_denied", Pattern: pattern}
		}
		if hasShellInjection(candidate) {
			return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: "command_denied", Pattern: "shell_injection"}
		}
		if denied, code := deniedGitPush(candidate); denied {
			return Classification{RiskLevel: RiskCritical, Denied: true, ErrorCode: code}
		}

		if containsRedirect(candidate) {
			overall = maxRiskLevel(overall, RiskMedium)
		}
		overall = maxRiskLevel(overall, classifySingle(candidate))
	}

	return Classification{RiskLevel: overall}
}

type commandSegment struct {
	Text      string
	Separator string
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
	return strings.HasPrefix(command, "dd ")
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

func splitCommandChain(command string) []commandSegment {
	segments := make([]commandSegment, 0)
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
			segments = append(segments, commandSegment{
				Text:      piece,
				Separator: string(bytes[i : i+separatorWidth]),
			})
		}
		start = i + separatorWidth
		if separatorWidth == 2 {
			i++
		}
	}

	last := strings.TrimSpace(command[start:])
	if last != "" {
		segments = append(segments, commandSegment{Text: last})
	}
	return segments
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

func (c *RiskClassifier) matchDeniedInvocation(command string) (string, bool) {
	normalized := normalizeCommand(command)
	commandToken := invokedCommandToken(normalized)
	switch commandToken {
	case "sudo", "su", "passwd", "eval", "exec":
		return "command_token:" + commandToken, true
	case "rm":
		if strings.Contains(normalized, " -rf ") && strings.Contains(normalized, " /") {
			return "invocation:rm -rf /", true
		}
	case "chmod":
		if strings.Contains(normalized, " 777") {
			return "invocation:chmod 777", true
		}
	}
	return "", false
}

func normalizeCommand(command string) string {
	whitespace := regexp.MustCompile(`\s+`)
	return whitespace.ReplaceAllString(strings.ToLower(strings.TrimSpace(command)), " ")
}

func invokedCommandToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}

	idx := 0
	for idx < len(fields) && isEnvAssignment(fields[idx]) {
		idx++
	}
	if idx >= len(fields) {
		return ""
	}
	if fields[idx] == "env" {
		idx++
		for idx < len(fields) && (strings.HasPrefix(fields[idx], "-") || isEnvAssignment(fields[idx])) {
			idx++
		}
	}
	if idx >= len(fields) {
		return ""
	}
	return fields[idx]
}

func isEnvAssignment(token string) bool {
	if strings.HasPrefix(token, "-") {
		return false
	}
	name, _, ok := strings.Cut(token, "=")
	return ok && strings.TrimSpace(name) != ""
}

func matchDeniedPipeline(segments []commandSegment) (string, bool) {
	sourceToken := ""
	for _, segment := range segments {
		token := invokedCommandToken(normalizeCommand(segment.Text))
		if isDeniedPipelineSource(token) && sourceToken == "" {
			sourceToken = token
		}
		if sourceToken != "" && isDeniedPipelineSink(token) {
			return "pipeline:" + sourceToken + "|" + token, true
		}
		if segment.Separator != "|" {
			sourceToken = ""
		}
	}
	return "", false
}

func isDeniedPipelineSource(token string) bool {
	switch token {
	case "curl", "wget":
		return true
	default:
		return false
	}
}

func isDeniedPipelineSink(token string) bool {
	switch token {
	case "bash", "sh", "ash", "dash", "ksh", "zsh":
		return true
	default:
		return false
	}
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
