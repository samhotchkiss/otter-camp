package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionInjectedViaLdflags(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "ottercamp-test")
	ldflags := strings.Join([]string{
		"-X github.com/samhotchkiss/otter-camp/internal/version.Version=v9.9.9-test",
		"-X github.com/samhotchkiss/otter-camp/internal/version.Commit=abc123test",
		"-X github.com/samhotchkiss/otter-camp/internal/version.BuiltAt=2026-02-25T00:00:00Z",
	}, " ")

	build := exec.Command("go", "build", "-o", binary, "-ldflags", ldflags, "./cmd/ottercamp")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	run := exec.Command(binary, "version", "--json")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("version command failed: %v\n%s", err, out)
	}

	var payload map[string]string
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal version json: %v\n%s", err, out)
	}

	if got := payload["version"]; got != "v9.9.9-test" {
		t.Fatalf("version = %q, want %q", got, "v9.9.9-test")
	}
	if got := payload["commit"]; got != "abc123test" {
		t.Fatalf("commit = %q, want %q", got, "abc123test")
	}
	if got := payload["built_at"]; got != "2026-02-25T00:00:00Z" {
		t.Fatalf("built_at = %q, want %q", got, "2026-02-25T00:00:00Z")
	}
	if got := payload["go_version"]; got != runtime.Version() {
		t.Fatalf("go_version = %q, want %q", got, runtime.Version())
	}
}
