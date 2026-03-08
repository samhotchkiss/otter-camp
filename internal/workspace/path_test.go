package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
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

func TestLegacyProjectRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := LegacyProjectRoot("~/otter-data", "default", "sam-blog")
	if err != nil {
		t.Fatalf("LegacyProjectRoot: %v", err)
	}

	want := filepath.Join(home, "otter-data", "workspaces", "default", "sam-blog")
	if got != want {
		t.Fatalf("LegacyProjectRoot = %q, want %q", got, want)
	}
}

func TestProjectCompatibilityRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dataDir := "~/otter-data"
	flatRoot := filepath.Join(home, "otter-data", "workspaces", "sam-blog")
	legacyRoot := filepath.Join(home, "otter-data", "workspaces", "default", "sam-blog")

	t.Run("flat root only when no legacy root exists", func(t *testing.T) {
		roots, err := ProjectCompatibilityRoots(dataDir, "default", "sam-blog")
		if err != nil {
			t.Fatalf("ProjectCompatibilityRoots: %v", err)
		}
		if len(roots) != 1 {
			t.Fatalf("root count = %d, want 1", len(roots))
		}
		if roots[0] != flatRoot {
			t.Fatalf("roots[0] = %q, want %q", roots[0], flatRoot)
		}
	})

	t.Run("includes legacy root when it already exists", func(t *testing.T) {
		if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
			t.Fatalf("mkdir legacy root: %v", err)
		}

		roots, err := ProjectCompatibilityRoots(dataDir, "default", "sam-blog")
		if err != nil {
			t.Fatalf("ProjectCompatibilityRoots: %v", err)
		}
		if len(roots) != 2 {
			t.Fatalf("root count = %d, want 2", len(roots))
		}
		if roots[0] != flatRoot {
			t.Fatalf("roots[0] = %q, want %q", roots[0], flatRoot)
		}
		if roots[1] != legacyRoot {
			t.Fatalf("roots[1] = %q, want %q", roots[1], legacyRoot)
		}
	})
}

func TestGeneralRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := GeneralRoot("~/otter-data")
	want := filepath.Join(home, "otter-data", "workspaces", "general")
	if got != want {
		t.Fatalf("GeneralRoot = %q, want %q", got, want)
	}
}

func TestProjectRootByID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	orgID := uuid.New()
	projectID := uuid.New()

	t.Run("resolves slug workspace", func(t *testing.T) {
		got, err := ProjectRootByID(context.Background(), projectLookupStub{
			project: repo.Project{ID: projectID, OrganizationID: orgID, Slug: "sam-blog"},
		}, "~/otter-data", orgID, projectID)
		if err != nil {
			t.Fatalf("ProjectRootByID: %v", err)
		}

		want := filepath.Join(home, "otter-data", "workspaces", "sam-blog")
		if got != want {
			t.Fatalf("ProjectRootByID = %q, want %q", got, want)
		}
	})

	t.Run("rejects organization mismatch", func(t *testing.T) {
		_, err := ProjectRootByID(context.Background(), projectLookupStub{
			project: repo.Project{ID: projectID, OrganizationID: uuid.New(), Slug: "sam-blog"},
		}, "~/otter-data", orgID, projectID)
		if err == nil {
			t.Fatal("ProjectRootByID error = nil, want organization mismatch")
		}
	})
}

type projectLookupStub struct {
	project repo.Project
	err     error
}

func (s projectLookupStub) GetByID(_ context.Context, _ uuid.UUID) (repo.Project, error) {
	if s.err != nil {
		return repo.Project{}, s.err
	}
	return s.project, nil
}
