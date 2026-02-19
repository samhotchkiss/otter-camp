package importer

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const openClawProjectMinConfidence = 2

var (
	inlineProjectHintPattern = regexp.MustCompile(`(?i)\b(?:project|repo)[:=]\s*([a-z0-9][a-z0-9._-]{1,63})`)
	inlineIssueHintPattern   = regexp.MustCompile(`(?i)\b(?:issue|task|todo)[:=]\s*([^\n.;]+)`)
	completionSignalPattern  = regexp.MustCompile(`(?i)\b(?:shipped|completed|done|resolved|closed)\b`)
	genericProjectKeys       = map[string]struct{}{
		"agent":      {},
		"agents":     {},
		"default":    {},
		"home":       {},
		"main":       {},
		"misc":       {},
		"root":       {},
		"tmp":        {},
		"workspace":  {},
		"workspaces": {},
	}
)

type OpenClawProjectImportInput struct {
	Workspaces []OpenClawWorkspaceSignal
	Sessions   []OpenClawSessionSignal
	Memories   []OpenClawMemorySignal
}

type OpenClawWorkspaceSignal struct {
	AgentID      string
	WorkspaceDir string
	RepoPath     string
	ProjectHint  string
	IssueHints   []string
	IssueSignals []OpenClawIssueSignal
}

type OpenClawSessionSignal struct {
	AgentID      string
	Summary      string
	RepoPath     string
	ProjectHint  string
	IssueHints   []string
	IssueSignals []OpenClawIssueSignal
	OccurredAt   time.Time
}

type OpenClawMemorySignal struct {
	AgentID      string
	Text         string
	ProjectHint  string
	IssueHints   []string
	IssueSignals []OpenClawIssueSignal
	OccurredAt   time.Time
}

type OpenClawProjectCandidate struct {
	Key             string
	Name            string
	RepoPath        string
	Status          string
	LastDiscussedAt *time.Time
	Confidence      int
	Signals         []string
	Issues          []OpenClawIssueCandidate
}

type OpenClawIssueCandidate struct {
	Title      string
	Status     string
	Source     string
	OccurredAt time.Time
}

type OpenClawIssueSignal struct {
	Title      string
	Status     string
	Source     string
	OccurredAt time.Time
}

type openClawProjectSignal struct {
	Key          string
	Name         string
	RepoPath     string
	Weight       int
	Source       string
	IssueSignals []OpenClawIssueSignal
	OccurredAt   time.Time
	Completed    bool
}

type openClawProjectAccumulator struct {
	Key             string
	Name            string
	RepoPath        string
	Confidence      int
	HasCompletion   bool
	LastDiscussedAt *time.Time
	Signals         map[string]struct{}
	IssueByNorm     map[string]OpenClawIssueCandidate
}

func InferOpenClawProjectCandidates(input OpenClawProjectImportInput) []OpenClawProjectCandidate {
	return InferOpenClawProjectCandidatesAt(input, time.Now().UTC())
}

func InferOpenClawProjectCandidatesAt(input OpenClawProjectImportInput, now time.Time) []OpenClawProjectCandidate {
	signals := ExtractOpenClawProjectSignals(input)
	if len(signals) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	accumulators := map[string]*openClawProjectAccumulator{}
	for _, signal := range signals {
		if signal.Key == "" {
			continue
		}
		acc := accumulators[signal.Key]
		if acc == nil {
			acc = &openClawProjectAccumulator{
				Key:         signal.Key,
				Name:        signal.Name,
				RepoPath:    signal.RepoPath,
				Signals:     map[string]struct{}{},
				IssueByNorm: map[string]OpenClawIssueCandidate{},
			}
			accumulators[signal.Key] = acc
		}

		if strings.TrimSpace(acc.Name) == "" {
			acc.Name = signal.Name
		}
		if strings.TrimSpace(acc.RepoPath) == "" && strings.TrimSpace(signal.RepoPath) != "" {
			acc.RepoPath = signal.RepoPath
		}
		acc.Confidence += signal.Weight
		if signal.Source != "" {
			acc.Signals[signal.Source] = struct{}{}
		}
		if signal.Completed {
			acc.HasCompletion = true
		}
		if !signal.OccurredAt.IsZero() {
			occurredAt := signal.OccurredAt.UTC()
			if acc.LastDiscussedAt == nil || occurredAt.After(acc.LastDiscussedAt.UTC()) {
				acc.LastDiscussedAt = &occurredAt
			}
		}

		for _, issueSignal := range signal.IssueSignals {
			title := normalizeIssueTitle(issueSignal.Title)
			if title == "" {
				continue
			}
			norm := strings.ToLower(title)
			next := OpenClawIssueCandidate{
				Title:      title,
				Status:     normalizeOpenClawIssueStatus(issueSignal.Status, issueSignal.Title),
				Source:     firstNonEmpty(issueSignal.Source, signal.Source),
				OccurredAt: issueSignal.OccurredAt.UTC(),
			}
			if existing, exists := acc.IssueByNorm[norm]; exists {
				acc.IssueByNorm[norm] = mergeOpenClawIssueCandidate(existing, next)
				continue
			}
			acc.IssueByNorm[norm] = next
		}
	}

	candidates := make([]OpenClawProjectCandidate, 0, len(accumulators))
	for _, acc := range accumulators {
		if acc.Confidence < openClawProjectMinConfidence {
			continue
		}

		signals := make([]string, 0, len(acc.Signals))
		for signal := range acc.Signals {
			signals = append(signals, signal)
		}
		sort.Strings(signals)

		issues := make([]OpenClawIssueCandidate, 0, len(acc.IssueByNorm))
		for _, issue := range acc.IssueByNorm {
			issues = append(issues, issue)
		}
		sort.Slice(issues, func(i, j int) bool {
			return issues[i].Title < issues[j].Title
		})

		candidates = append(candidates, OpenClawProjectCandidate{
			Key:             acc.Key,
			Name:            acc.Name,
			RepoPath:        acc.RepoPath,
			Status:          inferOpenClawProjectStatus(acc.LastDiscussedAt, acc.HasCompletion, now),
			LastDiscussedAt: cloneOpenClawTimestamp(acc.LastDiscussedAt),
			Confidence:      acc.Confidence,
			Signals:         signals,
			Issues:          issues,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Confidence == candidates[j].Confidence {
			return candidates[i].Name < candidates[j].Name
		}
		return candidates[i].Confidence > candidates[j].Confidence
	})
	return candidates
}

func ExtractOpenClawProjectSignals(input OpenClawProjectImportInput) []openClawProjectSignal {
	signals := make([]openClawProjectSignal, 0, len(input.Workspaces)+len(input.Sessions)+len(input.Memories))

	for _, workspace := range input.Workspaces {
		hint := firstNonEmpty(workspace.ProjectHint, projectHintFromPath(workspace.RepoPath), projectHintFromPath(workspace.WorkspaceDir))
		key, name := normalizeProjectKeyAndName(hint)
		if key == "" {
			continue
		}
		signals = append(signals, openClawProjectSignal{
			Key:          key,
			Name:         name,
			RepoPath:     filepath.Clean(strings.TrimSpace(workspace.RepoPath)),
			Weight:       3,
			Source:       firstNonEmpty("workspace:"+workspace.AgentID, "workspace"),
			IssueSignals: mergeOpenClawIssueSignals(firstNonEmpty("workspace:"+workspace.AgentID, "workspace"), time.Time{}, workspace.IssueHints, workspace.IssueSignals),
			Completed:    hasOpenClawCompletionSignal(strings.Join(workspace.IssueHints, " ")),
		})
	}

	for _, session := range input.Sessions {
		hint := firstNonEmpty(
			session.ProjectHint,
			extractInlineProjectHint(session.Summary),
			projectHintFromPath(session.RepoPath),
		)
		key, name := normalizeProjectKeyAndName(hint)
		if key == "" {
			continue
		}
		signals = append(signals, openClawProjectSignal{
			Key:          key,
			Name:         name,
			RepoPath:     filepath.Clean(strings.TrimSpace(session.RepoPath)),
			Weight:       2,
			Source:       firstNonEmpty("session:"+session.AgentID, "session"),
			IssueSignals: mergeOpenClawIssueSignals(firstNonEmpty("session:"+session.AgentID, "session"), session.OccurredAt.UTC(), session.IssueHints, session.IssueSignals),
			OccurredAt:   session.OccurredAt.UTC(),
			Completed:    hasOpenClawCompletionSignal(session.Summary + " " + strings.Join(session.IssueHints, " ")),
		})
	}

	for _, memory := range input.Memories {
		hint := firstNonEmpty(memory.ProjectHint, extractInlineProjectHint(memory.Text))
		key, name := normalizeProjectKeyAndName(hint)
		if key == "" {
			continue
		}
		signals = append(signals, openClawProjectSignal{
			Key:          key,
			Name:         name,
			Weight:       1,
			Source:       firstNonEmpty("memory:"+memory.AgentID, "memory"),
			IssueSignals: mergeOpenClawIssueSignals(firstNonEmpty("memory:"+memory.AgentID, "memory"), memory.OccurredAt.UTC(), memory.IssueHints, memory.IssueSignals),
			OccurredAt:   memory.OccurredAt.UTC(),
			Completed:    hasOpenClawCompletionSignal(memory.Text + " " + strings.Join(memory.IssueHints, " ")),
		})
	}

	return signals
}

func extractInlineProjectHint(value string) string {
	match := inlineProjectHintPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func projectHintFromPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	base := filepath.Base(value)
	base = strings.TrimSuffix(base, ".git")
	return strings.TrimSpace(base)
}

func normalizeProjectKeyAndName(value string) (string, string) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "", ""
	}
	replacer := strings.NewReplacer("_", "-", " ", "-", ".", "-")
	value = replacer.Replace(value)

	var b strings.Builder
	lastDash := false
	for _, ch := range value {
		isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if isAlnum {
			b.WriteRune(ch)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}

	key := strings.Trim(b.String(), "-")
	if key == "" {
		return "", ""
	}
	if _, isGeneric := genericProjectKeys[key]; isGeneric {
		return "", ""
	}

	name := humanizeProjectName(key)
	return key, name
}

func humanizeProjectName(key string) string {
	parts := strings.Split(strings.TrimSpace(key), "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func normalizeIssueHint(value string) string {
	words := strings.Fields(strings.TrimSpace(value))
	if len(words) < 2 {
		return ""
	}
	return strings.Join(words, " ")
}

func normalizeIssueTitle(value string) string {
	words := strings.Fields(strings.TrimSpace(value))
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}

func extractInlineIssueHints(value string) []string {
	matches := inlineIssueHintPattern.FindAllStringSubmatch(strings.TrimSpace(value), -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		normalized := normalizeIssueHint(match[1])
		if normalized == "" {
			continue
		}
		normKey := strings.ToLower(normalized)
		if _, exists := seen[normKey]; exists {
			continue
		}
		seen[normKey] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func inferOpenClawProjectStatus(lastDiscussedAt *time.Time, hasCompletion bool, now time.Time) string {
	if hasCompletion {
		return "completed"
	}
	if lastDiscussedAt != nil && !lastDiscussedAt.IsZero() {
		if lastDiscussedAt.UTC().Before(now.UTC().AddDate(0, 0, -60)) {
			return "archived"
		}
	}
	return "active"
}

func mergeOpenClawIssueSignals(
	fallbackSource string,
	fallbackOccurredAt time.Time,
	hints []string,
	issueSignals []OpenClawIssueSignal,
) []OpenClawIssueSignal {
	out := make([]OpenClawIssueSignal, 0, len(hints)+len(issueSignals))
	seen := map[string]struct{}{}

	for _, hint := range hints {
		title := normalizeIssueHint(hint)
		if title == "" {
			continue
		}
		key := strings.ToLower(title)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, OpenClawIssueSignal{
			Title:      title,
			Status:     normalizeOpenClawIssueStatus("", hint),
			Source:     fallbackSource,
			OccurredAt: fallbackOccurredAt.UTC(),
		})
	}

	for _, signal := range issueSignals {
		title := normalizeIssueTitle(signal.Title)
		if title == "" {
			continue
		}
		key := strings.ToLower(title)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		status := normalizeOpenClawIssueStatus(signal.Status, signal.Title)
		out = append(out, OpenClawIssueSignal{
			Title:      title,
			Status:     status,
			Source:     firstNonEmpty(signal.Source, fallbackSource),
			OccurredAt: firstNonZeroOpenClawTime(signal.OccurredAt, fallbackOccurredAt).UTC(),
		})
	}

	return out
}

func mergeOpenClawIssueCandidate(existing, next OpenClawIssueCandidate) OpenClawIssueCandidate {
	merged := existing
	if !next.OccurredAt.IsZero() && (merged.OccurredAt.IsZero() || next.OccurredAt.After(merged.OccurredAt)) {
		merged.Source = next.Source
		merged.OccurredAt = next.OccurredAt
	}
	if merged.Status == "" {
		merged.Status = next.Status
		return merged
	}
	if isOpenClawTerminalIssueStatus(merged.Status) {
		return merged
	}
	if isOpenClawTerminalIssueStatus(next.Status) {
		merged.Status = next.Status
		return merged
	}
	if issueStatusRank(next.Status) > issueStatusRank(merged.Status) {
		merged.Status = next.Status
	}
	return merged
}

func normalizeOpenClawIssueStatus(rawStatus, fallbackText string) string {
	normalized := strings.TrimSpace(strings.ToLower(rawStatus))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	switch normalized {
	case "open", "queued", "todo", "backlog", "ready", "ready_for_work", "planning":
		return "queued"
	case "in_progress", "inprogress", "active", "working":
		return "in_progress"
	case "blocked", "flagged", "stuck":
		return "blocked"
	case "review", "ready_for_review", "needs_review":
		return "review"
	case "done", "completed", "complete", "resolved", "closed", "shipped":
		return "done"
	case "cancelled", "canceled", "wontfix":
		return "cancelled"
	}
	if hasOpenClawCompletionSignal(fallbackText) {
		return "done"
	}
	return "queued"
}

func isOpenClawTerminalIssueStatus(status string) bool {
	status = strings.TrimSpace(strings.ToLower(status))
	return status == "done" || status == "cancelled"
}

func issueStatusRank(status string) int {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "done", "cancelled":
		return 5
	case "review":
		return 4
	case "blocked":
		return 3
	case "in_progress":
		return 2
	default:
		return 1
	}
}

func firstNonZeroOpenClawTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func hasOpenClawCompletionSignal(value string) bool {
	return completionSignalPattern.MatchString(strings.TrimSpace(value))
}

func cloneOpenClawTimestamp(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := value.UTC()
	return &clone
}
