package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawSourceGuardRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	outsideFile := filepath.Join(outside, "outside.jsonl")
	require.NoError(t, os.WriteFile(outsideFile, []byte("unsafe\n"), 0o644))

	escapeLink := filepath.Join(sessionDir, "escape.jsonl")
	require.NoError(t, os.Symlink(outsideFile, escapeLink))

	guard, err := NewOpenClawSourceGuard(root)
	require.NoError(t, err)

	err = guard.ValidateReadPath(escapeLink)
	require.Error(t, err)
	require.ErrorContains(t, err, "outside openclaw root")
}

func TestOpenClawSourceGuardAllowsSymlinkInsideRoot(t *testing.T) {
	root := t.TempDir()

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	insideFile := filepath.Join(root, "allowed.jsonl")
	require.NoError(t, os.WriteFile(insideFile, []byte("safe\n"), 0o644))

	insideLink := filepath.Join(sessionDir, "inside.jsonl")
	require.NoError(t, os.Symlink(insideFile, insideLink))

	guard, err := NewOpenClawSourceGuard(root)
	require.NoError(t, err)

	err = guard.ValidateReadPath(insideLink)
	require.NoError(t, err)
}
