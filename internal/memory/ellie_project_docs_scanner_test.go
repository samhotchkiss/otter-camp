package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieProjectDocsScannerDiscoversMarkdownFiles(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "docs", "guides"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "overview.md"), []byte("# Overview\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "guides", "setup.md"), []byte("# Setup\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "notes.txt"), []byte("ignore"), 0o644))

	scanner := &EllieProjectDocsScanner{}
	result, err := scanner.Scan(context.Background(), EllieProjectDocsScanInput{
		ProjectRoot: projectRoot,
	})
	require.NoError(t, err)
	require.Len(t, result.Documents, 2)
	require.Equal(t, "docs/guides/setup.md", result.Documents[0].FilePath)
	require.Equal(t, EllieProjectDocChangeStatusNew, result.Documents[0].ChangeStatus)
	require.Equal(t, "docs/overview.md", result.Documents[1].FilePath)
	require.Equal(t, EllieProjectDocChangeStatusNew, result.Documents[1].ChangeStatus)
}

func TestEllieProjectDocsScannerParsesStartHereLinks(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "docs", "guides"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "overview.md"), []byte("# Overview\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "guides", "setup.md"), []byte("# Setup\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "docs", "START-HERE.md"),
		[]byte("# Start Here\n- [Overview](./overview.md)\n- [Setup](guides/setup.md)\n- [External](https://example.com)\n"),
		0o644,
	))

	scanner := &EllieProjectDocsScanner{}
	result, err := scanner.Scan(context.Background(), EllieProjectDocsScanInput{
		ProjectRoot: projectRoot,
	})
	require.NoError(t, err)

	linked := map[string]bool{}
	for _, doc := range result.Documents {
		linked[doc.FilePath] = doc.StartHereLinked
	}
	require.True(t, linked["docs/overview.md"])
	require.True(t, linked["docs/guides/setup.md"])
	require.False(t, linked["docs/START-HERE.md"])
}

func TestEllieProjectDocsScannerDetectsChangedAndDeletedFiles(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "keep.md"), []byte("# Keep\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "docs", "changed.md"), []byte("# Changed v2\n"), 0o644))

	keepHash := md5HexString([]byte("# Keep\n"))

	scanner := &EllieProjectDocsScanner{}
	result, err := scanner.Scan(context.Background(), EllieProjectDocsScanInput{
		ProjectRoot: projectRoot,
		KnownDocs: []EllieKnownProjectDoc{
			{FilePath: "docs/keep.md", ContentHash: keepHash},
			{FilePath: "docs/changed.md", ContentHash: "old-hash"},
			{FilePath: "docs/deleted.md", ContentHash: "deleted-hash"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"docs/deleted.md"}, result.DeletedPaths)

	statusByPath := map[string]EllieProjectDocChangeStatus{}
	for _, doc := range result.Documents {
		statusByPath[doc.FilePath] = doc.ChangeStatus
	}
	require.Equal(t, EllieProjectDocChangeStatusUnchanged, statusByPath["docs/keep.md"])
	require.Equal(t, EllieProjectDocChangeStatusChanged, statusByPath["docs/changed.md"])
}
