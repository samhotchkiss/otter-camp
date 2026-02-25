package native

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSessionWorkDirResolvePathTraversalRelative(t *testing.T) {
	wd := mustWorkDir(t)

	_, err := wd.ResolvePath("../../../etc/passwd")
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("ResolvePath traversal err = %v, want ErrPathTraversal", err)
	}
}

func TestSessionWorkDirResolvePathTraversalAbsolute(t *testing.T) {
	wd := mustWorkDir(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")

	_, err := wd.ResolvePath(outside)
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("ResolvePath absolute outside err = %v, want ErrPathTraversal", err)
	}
}

func TestSessionWorkDirResolvePathRelative(t *testing.T) {
	wd := mustWorkDir(t)

	resolved, err := wd.ResolvePath("./subdir/file.txt")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	want := filepath.Join(wd.Root(), "subdir", "file.txt")
	if resolved != want {
		t.Fatalf("ResolvePath = %q, want %q", resolved, want)
	}
}

func mustWorkDir(t *testing.T) SessionWorkDir {
	t.Helper()
	wd, err := NewSessionWorkDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionWorkDir: %v", err)
	}
	return wd
}
