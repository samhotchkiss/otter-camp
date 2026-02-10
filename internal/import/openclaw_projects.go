package importer

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const openClawProjectMinConfidence = 2

var (
	inlineProjectHintPattern = regexp.MustCompile(`(?i)\b(?:project|repo)[:=]\s*([a-z0-9][a-z0-9._-]{1,63})`)
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
}

type OpenClawMemorySignal struct {
	AgentID     string
	Text        string
	ProjectHint string
	IssueHints  []string
}

type OpenClawProjectCandidate struct {
	Key        string
	Name       string
	RepoPath   string
	Confidence int
	Signals    []string
	Issues     []OpenClawIssueCandidate
}

type OpenClawIssueCandidate struct {
	Title  string
	Source string
}

type openClawProjectSignal struct {
	Key        string
	Name       string
	RepoPath   string
	Weight     int
	Source     string
	IssueHints []string
}

type openClawProjectAccumulator struct {
	Key         string
	Name        string
	RepoPath    string
	Confidence  int
	Signals     map[string]struct{}
	IssueByNorm map[string]OpenClawIssueCandidate
}

func InferOpenClawProjectCandidates(input OpenClawProjectImportInput) []OpenClawProjectCandidate {
	signals := ExtractOpenClawProjectSignals(input)
	if len(signals) == 0 {
		return nil
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
				Title:  title,
				Source: signal.Source,
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
			Key:        acc.Key,
			Name:       acc.Name,
			RepoPath:   acc.RepoPath,
			Confidence: acc.Confidence,
			Signals:    signals,
			Issues:     issues,
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
