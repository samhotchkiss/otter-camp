package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLaunchctlState(t *testing.T) {
	output := `gui/501/com.ottercamp.server = {
	state = running
	path = /Users/sam/Library/LaunchAgents/com.ottercamp.server.plist
}`
	if got := parseLaunchctlState(output); got != "running" {
		t.Fatalf("parseLaunchctlState() = %q, want running", got)
	}
}

func TestShellSingleQuote(t *testing.T) {
	input := "/tmp/it's-test"
	got := shellSingleQuote(input)
	want := "'/tmp/it'\"'\"'s-test'"
	if got != want {
		t.Fatalf("shellSingleQuote() = %q, want %q", got, want)
	}
}

func TestIsOtterCampRepoRoot(t *testing.T) {
	root := t.TempDir()
	required := []string{
		filepath.Join(root, "Makefile"),
		filepath.Join(root, "cmd", "server", "main.go"),
		filepath.Join(root, "web", "package.json"),
	}
	for _, path := range required {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if !isOtterCampRepoRoot(root) {
		t.Fatalf("expected temp root to pass repo root check")
	}
}

func TestParseLocalControlOptions(t *testing.T) {
	root, deep, err := parseLocalControlOptions("repair", []string{"--root", "/tmp/demo", "--deep"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != "/tmp/demo" {
		t.Fatalf("root = %q", root)
	}
	if !deep {
		t.Fatalf("deep should be true")
	}

	_, _, err = parseLocalControlOptions("start", []string{"--deep"})
	if err == nil {
		t.Fatalf("expected error for --deep with start")
	}
}
