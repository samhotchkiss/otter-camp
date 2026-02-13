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
}

type OpenClawSessionSignal struct {
	AgentID     string
	Summary     string
	RepoPath    string
	ProjectHint string
	IssueHints  []string
	OccurredAt  time.Time
}

type OpenClawMemorySignal struct {
	AgentID     string
	Text        string
	ProjectHint string
	IssueHints  []string
	OccurredAt  time.Time
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
	Source     string
	OccurredAt time.Time
}

type openClawProjectSignal struct {
	Key        string
	Name       string
	RepoPath   string
	Weight     int
	Source     string
	IssueHints []string
	OccurredAt time.Time
	Completed  bool
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

		for _, hint := range signal.IssueHints {
			title := normalizeIssueHint(hint)
			if title == "" {
				continue
			}
			norm := strings.ToLower(title)
			if _, exists := acc.IssueByNorm[norm]; exists {
				continue
			}
			acc.IssueByNorm[norm] = OpenClawIssueCandidate{
				Title:      title,
				Source:     signal.Source,
				OccurredAt: signal.OccurredAt.UTC(),
			}
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
			Key:        key,
			Name:       name,
			RepoPath:   filepath.Clean(strings.TrimSpace(workspace.RepoPath)),
			Weight:     3,
			Source:     firstNonEmpty("workspace:"+workspace.AgentID, "workspace"),
			IssueHints: workspace.IssueHints,
			Completed:  hasOpenClawCompletionSignal(strings.Join(workspace.IssueHints, " ")),
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
			Key:        key,
			Name:       name,
			RepoPath:   filepath.Clean(strings.TrimSpace(session.RepoPath)),
			Weight:     2,
			Source:     firstNonEmpty("session:"+session.AgentID, "session"),
			IssueHints: session.IssueHints,
			OccurredAt: session.OccurredAt.UTC(),
			Completed:  hasOpenClawCompletionSignal(session.Summary + " " + strings.Join(session.IssueHints, " ")),
		})
	}

	for _, memory := range input.Memories {
		hint := firstNonEmpty(memory.ProjectHint, extractInlineProjectHint(memory.Text))
		key, name := normalizeProjectKeyAndName(hint)
		if key == "" {
			continue
		}
		signals = append(signals, openClawProjectSignal{
			Key:        key,
			Name:       name,
			Weight:     1,
			Source:     firstNonEmpty("memory:"+memory.AgentID, "memory"),
			IssueHints: memory.IssueHints,
			OccurredAt: memory.OccurredAt.UTC(),
			Completed:  hasOpenClawCompletionSignal(memory.Text + " " + strings.Join(memory.IssueHints, " ")),
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
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	words := strings.Fields(value)
	if len(words) < 2 {
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
