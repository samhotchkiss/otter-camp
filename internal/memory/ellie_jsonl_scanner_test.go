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

	longLine := strings.Repeat("a", 70*1024) + " database-choice"
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
