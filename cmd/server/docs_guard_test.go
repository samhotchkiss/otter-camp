package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestCanonicalDocsHaveMetadataAndChangeLog(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForDocsGuard(t)
	canonicalDocs, err := listCanonicalDocs(repoRoot)
	if err != nil {
		t.Fatalf("failed to enumerate canonical docs: %v", err)
	}
	if len(canonicalDocs) == 0 {
		t.Fatalf("no canonical docs discovered")
	}

	for _, relPath := range canonicalDocs {
		absPath := filepath.Join(repoRoot, filepath.FromSlash(relPath))
		contentBytes, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", relPath, err)
		}
		content := string(contentBytes)

		if !strings.Contains(content, "> Summary:") {
			t.Fatalf("expected %s to include a summary header", relPath)
		}
		if !strings.Contains(content, "> Last updated:") {
			t.Fatalf("expected %s to include a last updated header", relPath)
		}
		if !strings.Contains(content, "## Change Log") {
			t.Fatalf("expected %s to include a bottom change log section", relPath)
		}
		if !strings.Contains(content, "- 20") {
			t.Fatalf("expected %s to include at least one dated change log entry", relPath)
		}
	}
}

func TestDocsUpdatedWhenCodeChangesOnPullRequest(t *testing.T) {
	t.Parallel()

	eventName := strings.TrimSpace(os.Getenv("GITHUB_EVENT_NAME"))
	if eventName != "pull_request" && eventName != "pull_request_target" {
		t.Skip("docs guard only enforces on pull request events")
	}

	baseRef := strings.TrimSpace(os.Getenv("DOCS_GUARD_BASE_REF"))
	if baseRef == "" {
		baseRef = strings.TrimSpace(os.Getenv("GITHUB_BASE_REF"))
	}
	if baseRef == "" {
		t.Skip("DOCS_GUARD_BASE_REF/GITHUB_BASE_REF not set; skipping docs guard")
	}

	repoRoot := repoRootForDocsGuard(t)
	originBaseRef := "origin/" + baseRef
	if !gitRefExists(repoRoot, originBaseRef) {
		if _, err := runGit(repoRoot, "fetch", "--no-tags", "--depth=200", "origin", baseRef); err != nil {
			t.Fatalf("failed to fetch %s for docs guard: %v", originBaseRef, err)
		}
	}
	if !gitRefExists(repoRoot, originBaseRef) {
		t.Fatalf("docs guard could not resolve base ref %s", originBaseRef)
	}

	output, err := runGit(repoRoot, "diff", "--name-only", "--diff-filter=ACMRTUXB", originBaseRef+"...HEAD")
	if err != nil {
		t.Fatalf("failed to compute PR diff for docs guard: %v", err)
	}

	var changedFiles []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		file := filepath.ToSlash(strings.TrimSpace(scanner.Text()))
		if file != "" {
			changedFiles = append(changedFiles, file)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("failed to parse changed files for docs guard: %v", err)
	}
	if len(changedFiles) == 0 {
		t.Skip("no changed files detected for docs guard")
	}

	codeChanged := false
	canonicalDocsChanged := false
	for _, path := range changedFiles {
		if isCanonicalDocPath(path) {
			canonicalDocsChanged = true
			continue
		}
		if strings.HasPrefix(path, "docs/") {
			continue
		}
		if isIgnoredNonSourcePath(path) {
			continue
		}
		codeChanged = true
	}

	if codeChanged && !canonicalDocsChanged {
		t.Fatalf(
			"docs guard failed: code files changed but canonical docs were not updated.\n"+
				"Update docs under docs/{memories,projects,agents} or docs/START-HERE.md/docs/ISSUES-AND-OLD-CODE.md/docs/SAM-QUESTIONS.md and add a Change Log entry.\n"+
				"For bug fixes with no behavior change, add a log entry like: '- YYYY-MM-DD: Bug fix in <area>; no behavior change.'",
		)
	}
	if codeChanged {
		addedLogEntry, err := hasAddedCanonicalChangeLogEntry(repoRoot, originBaseRef)
		if err != nil {
			t.Fatalf("docs guard could not verify change log updates: %v", err)
		}
		if !addedLogEntry {
			t.Fatalf(
				"docs guard failed: code files changed but no dated Change Log entry was added in canonical docs.\n"+
					"Add a line like '- YYYY-MM-DD: <what changed>.' in the Change Log section.\n"+
					"For no-behavior bug fixes, use '- YYYY-MM-DD: Bug fix in <area>; no behavior change.'",
			)
		}
	}
}

func repoRootForDocsGuard(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}
	return root
}

func listCanonicalDocs(repoRoot string) ([]string, error) {
	paths := []string{
		"docs/START-HERE.md",
		"docs/ISSUES-AND-OLD-CODE.md",
		"docs/SAM-QUESTIONS.md",
	}

	for _, dir := range []string{"docs/memories", "docs/projects", "docs/agents"} {
		entries, err := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(dir)))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(strings.ToLower(name), ".md") {
				paths = append(paths, filepath.ToSlash(filepath.Join(dir, name)))
			}
		}
	}

	slices.Sort(paths)
	return paths, nil
}

func isCanonicalDocPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	switch path {
	case "docs/START-HERE.md", "docs/ISSUES-AND-OLD-CODE.md", "docs/SAM-QUESTIONS.md":
		return true
	}
	if strings.HasPrefix(path, "docs/memories/") || strings.HasPrefix(path, "docs/projects/") || strings.HasPrefix(path, "docs/agents/") {
		return strings.HasSuffix(strings.ToLower(path), ".md")
	}
	return false
}

func isIgnoredNonSourcePath(path string) bool {
	return strings.HasPrefix(path, ".autowork/") ||
		strings.HasPrefix(path, "data/agents/") ||
		strings.HasPrefix(path, "node_modules/") ||
		strings.HasPrefix(path, "web/node_modules/") ||
		strings.HasPrefix(path, "web/dist/")
}

func gitRefExists(repoRoot, ref string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", ref)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func runGit(repoRoot string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func hasAddedCanonicalChangeLogEntry(repoRoot, originBaseRef string) (bool, error) {
	diff, err := runGit(
		repoRoot,
		"diff",
		"--unified=0",
		originBaseRef+"...HEAD",
		"--",
		"docs/START-HERE.md",
		"docs/ISSUES-AND-OLD-CODE.md",
		"docs/SAM-QUESTIONS.md",
		"docs/agents",
		"docs/memories",
		"docs/projects",
	)
	if err != nil {
		return false, err
	}

	entryPattern := regexp.MustCompile(`^\+\s*-\s*20\d{2}-\d{2}-\d{2}:\s+`)
	scanner := bufio.NewScanner(strings.NewReader(diff))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++") {
			continue
		}
		if entryPattern.MatchString(line) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}
