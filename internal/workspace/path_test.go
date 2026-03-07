package workspace

import (
	"path/filepath"
	"testing"
)

func TestResolveDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("empty uses default", func(t *testing.T) {
		t.Setenv("OTTERCAMP_DATA_DIR", "")

		got := ResolveDataDir("")
		want := filepath.Join(home, "otter-data")
		if got != want {
			t.Fatalf("ResolveDataDir(\"\") = %q, want %q", got, want)
		}
	})

	t.Run("env var fallback", func(t *testing.T) {
		t.Setenv("OTTERCAMP_DATA_DIR", "~/custom-data")

		got := ResolveDataDir("")
		want := filepath.Join(home, "custom-data")
		if got != want {
			t.Fatalf("ResolveDataDir env fallback = %q, want %q", got, want)
		}
	})

	t.Run("tilde expansion", func(t *testing.T) {
		got := ResolveDataDir("~/projects/data")
		want := filepath.Join(home, "projects", "data")
		if got != want {
			t.Fatalf("ResolveDataDir tilde = %q, want %q", got, want)
		}
	})
}

func TestExpandDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("empty error", func(t *testing.T) {
		if _, err := ExpandDataDir(""); err == nil {
			t.Fatal("expected empty path error")
		}
	})

	t.Run("tilde", func(t *testing.T) {
		got, err := ExpandDataDir("~/workspace-data")
		if err != nil {
			t.Fatalf("ExpandDataDir tilde: %v", err)
		}

		want := filepath.Join(home, "workspace-data")
		if got != want {
			t.Fatalf("ExpandDataDir tilde = %q, want %q", got, want)
		}
	})

	t.Run("absolute", func(t *testing.T) {
		want := filepath.Join(home, "absolute", "path")
		got, err := ExpandDataDir(want)
		if err != nil {
			t.Fatalf("ExpandDataDir absolute: %v", err)
		}
		if got != want {
			t.Fatalf("ExpandDataDir absolute = %q, want %q", got, want)
		}
	})
}

func TestProjectRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("empty slug error", func(t *testing.T) {
		if _, err := ProjectRoot("", ""); err == nil {
			t.Fatal("expected empty slug error")
		}
	})

	t.Run("normal slug", func(t *testing.T) {
		got, err := ProjectRoot("~/otter-data", "sam-blog")
		if err != nil {
			t.Fatalf("ProjectRoot: %v", err)
		}

		want := filepath.Join(home, "otter-data", "workspaces", "sam-blog")
		if got != want {
			t.Fatalf("ProjectRoot = %q, want %q", got, want)
		}
	})
}
