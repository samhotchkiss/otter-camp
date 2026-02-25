package tui

import (
	"path/filepath"
	"testing"
)

func TestResolveStatePathXDG(t *testing.T) {
	path, err := ResolveStatePath(
		func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return "/tmp/xdg-config"
			}
			return ""
		},
		func() (string, error) { return "/home/tester", nil },
	)
	if err != nil {
		t.Fatalf("ResolveStatePath() error = %v", err)
	}
	expected := filepath.Join("/tmp/xdg-config", "ottercamp", stateFileName)
	if path != expected {
		t.Fatalf("ResolveStatePath() = %q, want %q", path, expected)
	}
}

func TestResolveStatePathHomeFallback(t *testing.T) {
	path, err := ResolveStatePath(
		func(string) string { return "" },
		func() (string, error) { return "/home/tester", nil },
	)
	if err != nil {
		t.Fatalf("ResolveStatePath() error = %v", err)
	}
	expected := filepath.Join("/home/tester", ".config", "ottercamp", stateFileName)
	if path != expected {
		t.Fatalf("ResolveStatePath() = %q, want %q", path, expected)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	original := UIState{
		LastActiveView:        "chat",
		LastActiveChatSession: "session-123",
		PanelProportions:      [3]float64{0.2, 0.5, 0.3},
		SidebarVisible:        false,
	}

	if err := SaveState(path, original); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if loaded.LastActiveView != original.LastActiveView {
		t.Fatalf("LastActiveView = %q, want %q", loaded.LastActiveView, original.LastActiveView)
	}
	if loaded.LastActiveChatSession != original.LastActiveChatSession {
		t.Fatalf("LastActiveChatSession = %q, want %q", loaded.LastActiveChatSession, original.LastActiveChatSession)
	}
	if loaded.SidebarVisible != original.SidebarVisible {
		t.Fatalf("SidebarVisible = %v, want %v", loaded.SidebarVisible, original.SidebarVisible)
	}
	if loaded.PanelProportions != original.PanelProportions {
		t.Fatalf("PanelProportions = %v, want %v", loaded.PanelProportions, original.PanelProportions)
	}
}
