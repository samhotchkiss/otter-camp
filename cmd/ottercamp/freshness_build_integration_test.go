//go:build integration

package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTUIFreshnessInjectedBuildIsCleanEX263(t *testing.T) {
	repoDir := snapshotRepoForFreshnessBuild(t)
	headCommit := mustGitOutput(t, repoDir, "rev-parse", "--short=12", "HEAD")

	binary := buildOttercampBinary(t, repoDir, []string{
		"-X github.com/samhotchkiss/otter-camp/internal/version.Commit=" + headCommit,
		"-X github.com/samhotchkiss/otter-camp/internal/version.BuiltAt=2026-03-06T00:00:00Z",
	})

	_, stderr, code := runTUINonInteractive(t, repoDir, binary)
	if code != 0 {
		t.Fatalf("tui --non-interactive exit=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "warning:") {
		t.Fatalf("expected injected clean build to avoid freshness warning, stderr=%q", stderr)
	}
}

func TestTUIFreshnessPlainGoBuildDoesNotFalsePositiveEX263(t *testing.T) {
	repoDir := snapshotRepoForFreshnessBuild(t)
	binary := buildOttercampBinary(t, repoDir, nil)

	_, stderr, code := runTUINonInteractive(t, repoDir, binary)
	if code != 0 {
		t.Fatalf("tui --non-interactive exit=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "warning:") {
		t.Fatalf("expected plain go build to avoid stale warning, stderr=%q", stderr)
	}
}

func TestTUIFreshnessTrueStaleBuildWarnsEX263(t *testing.T) {
	repoDir := snapshotRepoForFreshnessBuild(t)
	binary := buildOttercampBinary(t, repoDir, nil)

	runCommand(t, repoDir, "git", "commit", "--allow-empty", "-m", "stale-after-build")

	_, stderr, code := runTUINonInteractive(t, repoDir, binary)
	if code != 0 {
		t.Fatalf("tui --non-interactive exit=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "warning: binary commit") {
		t.Fatalf("expected TUI stale build warning to stay in-app, not stderr=%q", stderr)
	}
}

func snapshotRepoForFreshnessBuild(t *testing.T) string {
	t.Helper()

	srcRoot := filepath.Clean(filepath.Join("..", ".."))
	dstRoot := filepath.Join(t.TempDir(), "repo")
	copyRepoTree(t, srcRoot, dstRoot)

	runCommand(t, dstRoot, "git", "init")
	runCommand(t, dstRoot, "git", "config", "user.email", "freshness-test@example.com")
	runCommand(t, dstRoot, "git", "config", "user.name", "Freshness Test")
	runCommand(t, dstRoot, "git", "add", ".")
	runCommand(t, dstRoot, "git", "commit", "-m", "snapshot")
	return dstRoot
}

func copyRepoTree(t *testing.T, srcRoot, dstRoot string) {
	t.Helper()

	if err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		targetPath := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(targetPath), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(targetPath, data, info.Mode().Perm())
	}); err != nil {
		t.Fatalf("copy repo tree: %v", err)
	}
}

func buildOttercampBinary(t *testing.T, repoDir string, ldflags []string) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "ottercamp")
	args := []string{"build", "-o", binary}
	if len(ldflags) > 0 {
		args = append(args, "-ldflags", strings.Join(ldflags, " "))
	}
	args = append(args, "./cmd/ottercamp")

	cmd := exec.Command("go", args...)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return binary
}

func runTUINonInteractive(t *testing.T, repoDir, binary string) (string, string, int) {
	t.Helper()

	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}

	cmd := exec.Command(binary, "tui", "--non-interactive")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"XDG_STATE_HOME="+filepath.Join(homeDir, ".local", "state"),
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run tui --non-interactive: %v", err)
	return "", "", 1
}

func runCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func mustGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
