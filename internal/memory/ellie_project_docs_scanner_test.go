package memory

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestEllieProjectDocsScannerSummarizesAndEmbedsChangedDocs(t *testing.T) {
	summarizer := &fakeProjectDocSummarizer{}
	embedder := &fakeProjectDocEmbedder{}
	scanner := &EllieProjectDocsScanner{
		Summarizer:      summarizer,
		EmbeddingClient: embedder,
		MaxSectionChars: 1024,
	}

	docs := []EllieDiscoveredProjectDoc{
		{
			FilePath:     "docs/changed.md",
			Content:      "# Changed\nContent",
			ChangeStatus: EllieProjectDocChangeStatusChanged,
		},
		{
			FilePath:     "docs/new.md",
			Content:      "# New\nContent",
			ChangeStatus: EllieProjectDocChangeStatusNew,
		},
	}

	enriched, err := scanner.SummarizeAndEmbedDocuments(context.Background(), docs)
	require.NoError(t, err)
	require.Len(t, enriched, 2)
	require.Equal(t, "summary:docs/changed.md:1", enriched[0].Summary)
	require.Len(t, enriched[0].SummaryEmbedding, 1536)
	require.Equal(t, "summary:docs/new.md:1", enriched[1].Summary)
	require.Len(t, enriched[1].SummaryEmbedding, 1536)
	require.Len(t, summarizer.calls, 2)
	require.Len(t, embedder.calls, 2)
}

func TestEllieProjectDocsScannerSkipsUnchangedDocs(t *testing.T) {
	summarizer := &fakeProjectDocSummarizer{}
	embedder := &fakeProjectDocEmbedder{}
	scanner := &EllieProjectDocsScanner{
		Summarizer:      summarizer,
		EmbeddingClient: embedder,
		MaxSectionChars: 1024,
	}

	docs := []EllieDiscoveredProjectDoc{
		{
			FilePath:     "docs/unchanged.md",
			Content:      "# Unchanged\nNo-op",
			ChangeStatus: EllieProjectDocChangeStatusUnchanged,
		},
	}

	enriched, err := scanner.SummarizeAndEmbedDocuments(context.Background(), docs)
	require.NoError(t, err)
	require.Len(t, enriched, 1)
	require.Empty(t, enriched[0].Summary)
	require.Nil(t, enriched[0].SummaryEmbedding)
	require.Empty(t, summarizer.calls)
	require.Empty(t, embedder.calls)
}

func TestEllieProjectDocsScannerSplitsLargeDocsIntoSections(t *testing.T) {
	summarizer := &fakeProjectDocSummarizer{}
	embedder := &fakeProjectDocEmbedder{}
	scanner := &EllieProjectDocsScanner{
		Summarizer:      summarizer,
		EmbeddingClient: embedder,
		MaxSectionChars: 40,
	}

	docs := []EllieDiscoveredProjectDoc{
		{
			FilePath: "docs/large.md",
			Content: strings.Join([]string{
				"# Large",
				"",
				"Section one paragraph that exceeds the section limit.",
				"",
				"Section two paragraph also exceeds the section limit.",
			}, "\n"),
			ChangeStatus: EllieProjectDocChangeStatusChanged,
		},
	}

	enriched, err := scanner.SummarizeAndEmbedDocuments(context.Background(), docs)
	require.NoError(t, err)
	require.Len(t, enriched, 1)
	require.Greater(t, len(summarizer.calls), 1)
	require.Len(t, embedder.calls, 1)
	require.Contains(t, enriched[0].Summary, "summary:docs/large.md:1")
	require.Contains(t, enriched[0].Summary, "summary:docs/large.md:2")
}

type fakeProjectDocSummarizer struct {
	calls []EllieProjectDocSummaryInput
}

func (f *fakeProjectDocSummarizer) Summarize(
	_ context.Context,
	input EllieProjectDocSummaryInput,
) (string, error) {
	f.calls = append(f.calls, input)
	return "summary:" + input.FilePath + ":" + strconv.Itoa(input.SectionIndex+1), nil
}

type fakeProjectDocEmbedder struct {
	calls [][]string
}

func (f *fakeProjectDocEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	f.calls = append(f.calls, append([]string(nil), inputs...))
	vector := make([]float64, 1536)
	vector[0] = 1
	return [][]float64{vector}, nil
}
