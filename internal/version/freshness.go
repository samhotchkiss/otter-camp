package version

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	startupFreshnessOnce    sync.Once
	startupFreshnessWarning string
)

// StartupFreshnessWarning reports whether the running binary appears stale
// compared to the current git checkout in the working directory.
func StartupFreshnessWarning() string {
	if strings.TrimSpace(os.Getenv("OTTERCAMP_SKIP_FRESHNESS_WARN")) == "1" {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	repoRoot, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}

	headCommit, err := gitOutput(repoRoot, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return ""
	}

	warnings := make([]string, 0, 3)
	builtCommit := strings.TrimSpace(Commit)
	if builtCommit == "" || builtCommit == "unknown" {
		warnings = append(warnings, fmt.Sprintf("binary commit metadata is unknown (HEAD %s)", headCommit))
	} else if !commitMatches(headCommit, builtCommit) {
		warnings = append(warnings, fmt.Sprintf("binary commit %s != HEAD %s", builtCommit, headCommit))
	}

	repoVersionPath := filepath.Join(repoRoot, "internal", "version", "repo_version.txt")
	if data, readErr := os.ReadFile(repoVersionPath); readErr == nil {
		repoVersionOnDisk := strings.TrimSpace(string(data))
		if repoVersionOnDisk != "" && strings.TrimSpace(RepoVersion) != "" && repoVersionOnDisk != RepoVersion {
			warnings = append(warnings, fmt.Sprintf("binary repo_version %s != repo file %s", RepoVersion, repoVersionOnDisk))
		}
	}

	if statusOut, statusErr := gitOutput(repoRoot, "status", "--porcelain", "--untracked-files=no"); statusErr == nil && strings.TrimSpace(statusOut) != "" {
		warnings = append(warnings, "working tree has uncommitted tracked changes")
	}

	return strings.Join(warnings, "; ")
}

// CachedStartupFreshnessWarning computes startup freshness once per process.
func CachedStartupFreshnessWarning() string {
	startupFreshnessOnce.Do(func() {
		startupFreshnessWarning = StartupFreshnessWarning()
	})
	return startupFreshnessWarning
}

func commitMatches(head, built string) bool {
	head = strings.TrimSpace(head)
	built = strings.TrimSpace(built)
	if head == "" || built == "" {
		return false
	}
	return strings.HasPrefix(head, built) || strings.HasPrefix(built, head)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
