package version

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectStartupFreshnessCommitMismatch(t *testing.T) {
	restore := setFreshnessVars(t, "deadbeef1234", "2229")
	defer restore()

	report := detectStartupFreshness(newFreshnessTestDeps(t, freshnessTestScenario{
		headCommit:   "abc123def456",
		repoVersion:  "2229",
		gitStatusOut: "",
	}))

	if !report.Stale {
		t.Fatal("expected commit mismatch to be stale")
	}
	if report.MetadataMissing {
		t.Fatal("commit mismatch should not be classified as metadata missing")
	}
	if !strings.Contains(report.Warning(), "binary commit deadbeef1234 != HEAD abc123def456") {
		t.Fatalf("warning = %q", report.Warning())
	}
}

func TestDetectStartupFreshnessRepoVersionMismatch(t *testing.T) {
	restore := setFreshnessVars(t, "abc123def456", "2228")
	defer restore()

	report := detectStartupFreshness(newFreshnessTestDeps(t, freshnessTestScenario{
		headCommit:   "abc123def456",
		repoVersion:  "2229",
		gitStatusOut: "",
	}))

	if !report.Stale {
		t.Fatal("expected repo_version mismatch to be stale")
	}
	if !strings.Contains(report.Warning(), "binary repo_version 2228 != repo file 2229") {
		t.Fatalf("warning = %q", report.Warning())
	}
}

func TestDetectStartupFreshnessDirtyWorktree(t *testing.T) {
	restore := setFreshnessVars(t, "abc123def456", "2229")
	defer restore()

	report := detectStartupFreshness(newFreshnessTestDeps(t, freshnessTestScenario{
		headCommit:   "abc123def456",
		repoVersion:  "2229",
		gitStatusOut: " M internal/version/freshness.go",
	}))

	if !report.Stale {
		t.Fatal("expected dirty worktree to be stale")
	}
	if !strings.Contains(report.Warning(), "working tree has uncommitted tracked changes") {
		t.Fatalf("warning = %q", report.Warning())
	}
}

func TestDetectStartupFreshnessMissingMetadataOnly(t *testing.T) {
	restore := setFreshnessVars(t, "unknown", "2229")
	defer restore()

	report := detectStartupFreshness(newFreshnessTestDeps(t, freshnessTestScenario{
		headCommit:   "abc123def456",
		repoVersion:  "2229",
		gitStatusOut: "",
	}))

	if report.Stale {
		t.Fatal("metadata-only warning should not be stale")
	}
	if !report.MetadataMissing {
		t.Fatal("expected metadata missing classification")
	}
	if !report.MetadataOnly() {
		t.Fatal("expected metadata-only classification")
	}
	if !strings.Contains(report.Warning(), "binary commit metadata is unknown") {
		t.Fatalf("warning = %q", report.Warning())
	}
}

func TestDetectStartupFreshnessUsesBuildInfoCommitForPlainGoBuild(t *testing.T) {
	restore := setFreshnessVars(t, "unknown", "2229")
	defer restore()

	report := detectStartupFreshness(newFreshnessTestDeps(t, freshnessTestScenario{
		headCommit:   "abc123def456",
		repoVersion:  "2229",
		gitStatusOut: "",
		buildInfo: vcsBuildInfo{
			Commit: "abc123def4567890fedcba",
		},
	}))

	if report.Stale {
		t.Fatalf("expected build-info fallback to avoid stale warning: %q", report.Warning())
	}
	if report.MetadataMissing {
		t.Fatal("build-info fallback should satisfy commit metadata")
	}
	if report.Warning() != "" {
		t.Fatalf("warning = %q, want empty", report.Warning())
	}
}

type freshnessTestScenario struct {
	headCommit   string
	repoVersion  string
	gitStatusOut string
	buildInfo    vcsBuildInfo
}

func newFreshnessTestDeps(t *testing.T, scenario freshnessTestScenario) freshnessDeps {
	t.Helper()

	repoRoot := "/repo"
	repoVersionPath := filepath.Join(repoRoot, "internal", "version", "repo_version.txt")

	return freshnessDeps{
		getenv: func(string) string { return "" },
		getwd:  func() (string, error) { return repoRoot, nil },
		gitOutput: func(dir string, args ...string) (string, error) {
			if dir != repoRoot {
				t.Fatalf("gitOutput dir = %q, want %q", dir, repoRoot)
			}
			switch strings.Join(args, " ") {
			case "rev-parse --show-toplevel":
				return repoRoot, nil
			case "rev-parse --short=12 HEAD":
				return scenario.headCommit, nil
			case "status --porcelain --untracked-files=no":
				return scenario.gitStatusOut, nil
			default:
				t.Fatalf("unexpected git args: %v", args)
				return "", nil
			}
		},
		readFile: func(path string) ([]byte, error) {
			if path != repoVersionPath {
				return nil, errors.New("unexpected read path")
			}
			return []byte(scenario.repoVersion), nil
		},
		readBuildInfo: func() vcsBuildInfo { return scenario.buildInfo },
	}
}

func setFreshnessVars(t *testing.T, commit, repoVersion string) func() {
	t.Helper()
	oldCommit := Commit
	oldRepoVersion := RepoVersion
	Commit = commit
	RepoVersion = repoVersion
	return func() {
		Commit = oldCommit
		RepoVersion = oldRepoVersion
	}
}
