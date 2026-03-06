package version

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
)

var (
	startupFreshnessOnce   sync.Once
	startupFreshnessReport FreshnessReport
)

type FreshnessReport struct {
	Stale           bool
	MetadataMissing bool
	Warnings        []string
}

func (r FreshnessReport) Warning() string {
	if len(r.Warnings) == 0 {
		return ""
	}
	return strings.Join(r.Warnings, "; ")
}

func (r FreshnessReport) MetadataOnly() bool {
	return r.MetadataMissing && !r.Stale
}

type vcsBuildInfo struct {
	Commit string
}

type freshnessDeps struct {
	getenv        func(string) string
	getwd         func() (string, error)
	gitOutput     func(dir string, args ...string) (string, error)
	readFile      func(string) ([]byte, error)
	readBuildInfo func() vcsBuildInfo
}

// StartupFreshness reports whether the running binary appears stale compared to
// the current git checkout in the working directory.
func StartupFreshness() FreshnessReport {
	return detectStartupFreshness(freshnessDeps{
		getenv:        os.Getenv,
		getwd:         os.Getwd,
		gitOutput:     gitOutput,
		readFile:      os.ReadFile,
		readBuildInfo: readVCSBuildInfo,
	})
}

// StartupFreshnessWarning is a compatibility wrapper around StartupFreshness.
func StartupFreshnessWarning() string {
	return StartupFreshness().Warning()
}

// CachedStartupFreshness computes startup freshness once per process.
func CachedStartupFreshness() FreshnessReport {
	startupFreshnessOnce.Do(func() {
		startupFreshnessReport = StartupFreshness()
	})
	return startupFreshnessReport
}

// CachedStartupFreshnessWarning is a compatibility wrapper around CachedStartupFreshness.
func CachedStartupFreshnessWarning() string {
	return CachedStartupFreshness().Warning()
}

func detectStartupFreshness(deps freshnessDeps) FreshnessReport {
	if deps.getenv == nil {
		deps.getenv = func(string) string { return "" }
	}
	if deps.getenv("OTTERCAMP_SKIP_FRESHNESS_WARN") == "1" {
		return FreshnessReport{}
	}
	if deps.getwd == nil || deps.gitOutput == nil || deps.readFile == nil {
		return FreshnessReport{}
	}
	if deps.readBuildInfo == nil {
		deps.readBuildInfo = func() vcsBuildInfo { return vcsBuildInfo{} }
	}

	cwd, err := deps.getwd()
	if err != nil {
		return FreshnessReport{}
	}
	repoRoot, err := deps.gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return FreshnessReport{}
	}

	headCommit, err := deps.gitOutput(repoRoot, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return FreshnessReport{}
	}

	report := FreshnessReport{Warnings: make([]string, 0, 3)}
	builtCommit, ok := resolveBuiltCommit(deps.readBuildInfo())
	if !ok {
		report.MetadataMissing = true
		report.Warnings = append(report.Warnings, fmt.Sprintf("binary commit metadata is unknown (HEAD %s)", headCommit))
	} else if !commitMatches(headCommit, builtCommit) {
		report.Stale = true
		report.Warnings = append(report.Warnings, fmt.Sprintf("binary commit %s != HEAD %s", builtCommit, headCommit))
	}

	repoVersionPath := filepath.Join(repoRoot, "internal", "version", "repo_version.txt")
	if data, readErr := deps.readFile(repoVersionPath); readErr == nil {
		repoVersionOnDisk := strings.TrimSpace(string(data))
		if repoVersionOnDisk != "" && strings.TrimSpace(RepoVersion) != "" && repoVersionOnDisk != RepoVersion {
			report.Stale = true
			report.Warnings = append(report.Warnings, fmt.Sprintf("binary repo_version %s != repo file %s", RepoVersion, repoVersionOnDisk))
		}
	}

	if statusOut, statusErr := deps.gitOutput(repoRoot, "status", "--porcelain", "--untracked-files=no"); statusErr == nil && strings.TrimSpace(statusOut) != "" {
		report.Stale = true
		report.Warnings = append(report.Warnings, "working tree has uncommitted tracked changes")
	}

	return report
}

func resolveBuiltCommit(info vcsBuildInfo) (string, bool) {
	builtCommit := strings.TrimSpace(Commit)
	if builtCommit != "" && builtCommit != "unknown" {
		return builtCommit, true
	}
	buildInfoCommit := strings.TrimSpace(info.Commit)
	if buildInfoCommit == "" {
		return "", false
	}
	return buildInfoCommit, true
}

func readVCSBuildInfo() vcsBuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return vcsBuildInfo{}
	}
	result := vcsBuildInfo{}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			result.Commit = strings.TrimSpace(setting.Value)
			break
		}
	}
	return result
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
