package main

import (
	"os"
	"testing"

	tuiapp "github.com/samhotchkiss/otter-camp/internal/tui"
)

func TestRunTUICommandHelp(t *testing.T) {
	if code := runTUICommand([]string{"--help"}); code != 0 {
		t.Fatalf("runTUICommand(--help) = %d, want 0", code)
	}
}

func TestRunTUICommandNonInteractive(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	if code := runTUICommand([]string{"--non-interactive"}); code != 0 {
		t.Fatalf("runTUICommand(--non-interactive) = %d, want 0", code)
	}

	path, err := tuiapp.ResolveStatePath(os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatalf("ResolveStatePath: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file stat error: %v", err)
	}
}
