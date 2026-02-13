package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieFileJSONLScannerHandlesLongLinesWithExplicitLimit(t *testing.T) {
	rootDir := t.TempDir()
	path := filepath.Join(rootDir, "events.jsonl")

	longLine := "database-choice " + strings.Repeat("a", 70*1024)
	require.NoError(t, os.WriteFile(path, []byte(longLine+"\n"), 0o644))

	scanner := &EllieFileJSONLScanner{
		RootDir:      rootDir,
		MaxLineBytes: 256 * 1024,
	}
	results, err := scanner.Scan(context.Background(), EllieJSONLScanInput{
		Query: "database-choice",
		Limit: 5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Snippet, "database-choice")
}

func TestEllieFileJSONLScannerRespectsMaxBytesScanned(t *testing.T) {
	rootDir := t.TempDir()
	path := filepath.Join(rootDir, "events.jsonl")

	line := strings.Repeat("x", 60) + " keyword\n"
	payload := strings.Repeat(line, 20)
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o644))

	scanner := &EllieFileJSONLScanner{
		RootDir:         rootDir,
		MaxBytesScanned: 200,
	}
	results, err := scanner.Scan(context.Background(), EllieJSONLScanInput{
		Query: "keyword",
		Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Less(t, len(results), 20)
}

func TestEllieFileJSONLScannerUsesRelativeItemIDs(t *testing.T) {
	rootDir := t.TempDir()
	nestedDir := filepath.Join(rootDir, "nested")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))

	path := filepath.Join(nestedDir, "events.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("keyword hit\n"), 0o644))

	scanner := &EllieFileJSONLScanner{RootDir: rootDir}
	results, err := scanner.Scan(context.Background(), EllieJSONLScanInput{
		Query: "keyword",
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, filepath.ToSlash("nested/events.jsonl:1"), results[0].ID)
	require.NotContains(t, results[0].ID, rootDir)
}

func TestEllieFileJSONLScannerRejectsSymlinkEscape(t *testing.T) {
	rootDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.jsonl")
	require.NoError(t, os.WriteFile(outsidePath, []byte("keyword hit\n"), 0o644))

	escapeLink := filepath.Join(rootDir, "escape.jsonl")
	require.NoError(t, os.Symlink(outsidePath, escapeLink))

	scanner := &EllieFileJSONLScanner{RootDir: rootDir}
	_, err := scanner.Scan(context.Background(), EllieJSONLScanInput{
		Query: "keyword",
		Limit: 5,
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "escapes root")
}

func TestEllieFileJSONLScannerTruncatesSnippetLength(t *testing.T) {
	rootDir := t.TempDir()
	path := filepath.Join(rootDir, "events.jsonl")

	line := strings.Repeat("x", 4096) + " keyword"
	require.NoError(t, os.WriteFile(path, []byte(line+"\n"), 0o644))

	scanner := &EllieFileJSONLScanner{RootDir: rootDir}
	results, err := scanner.Scan(context.Background(), EllieJSONLScanInput{
		Query: "keyword",
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.LessOrEqual(t, len(results[0].Snippet), 1024)
}
