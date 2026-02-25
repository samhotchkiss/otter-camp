//go:build integration

package main

import (
	"os"
	"testing"

	tuiapp "github.com/samhotchkiss/otter-camp/internal/tui"
)

func TestTUILaunchQuitCycleRestoresState(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	path, err := tuiapp.ResolveStatePath(os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatalf("ResolveStatePath: %v", err)
	}

	seed := tuiapp.UIState{
		LastActiveView:        "chat",
		LastActiveChatSession: "session-42",
		PanelProportions:      [3]float64{0.2, 0.5, 0.3},
		SidebarVisible:        false,
	}
	if err := tuiapp.SaveState(path, seed); err != nil {
		t.Fatalf("SaveState seed: %v", err)
	}

	if code := runTUICommand([]string{"--non-interactive"}); code != 0 {
		t.Fatalf("runTUICommand(--non-interactive) = %d, want 0", code)
	}

	loaded, err := tuiapp.LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.LastActiveView != seed.LastActiveView {
		t.Fatalf("LastActiveView = %q, want %q", loaded.LastActiveView, seed.LastActiveView)
	}
	if loaded.LastActiveChatSession != seed.LastActiveChatSession {
		t.Fatalf("LastActiveChatSession = %q, want %q", loaded.LastActiveChatSession, seed.LastActiveChatSession)
	}
	if loaded.SidebarVisible != seed.SidebarVisible {
		t.Fatalf("SidebarVisible = %v, want %v", loaded.SidebarVisible, seed.SidebarVisible)
	}
	if loaded.PanelProportions != seed.PanelProportions {
		t.Fatalf("PanelProportions = %v, want %v", loaded.PanelProportions, seed.PanelProportions)
	}
}
