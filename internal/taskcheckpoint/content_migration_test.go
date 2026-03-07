package taskcheckpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIsContentMigrationTask(t *testing.T) {
	t.Run("positive title match", func(t *testing.T) {
		if !IsContentMigrationTask("Migrate blog posts", nil) {
			t.Fatal("expected blog migration title to match")
		}
	})

	t.Run("positive description match", func(t *testing.T) {
		description := "Scrape legacy site pages into markdown output files"
		if !IsContentMigrationTask("Legacy import", &description) {
			t.Fatal("expected content migration description to match")
		}
	})

	t.Run("negative non content migration", func(t *testing.T) {
		description := "Plan the billing migration rollout"
		if IsContentMigrationTask("Billing migration", &description) {
			t.Fatal("expected non-content migration to be ignored")
		}
	})

	t.Run("nil description does not match empty title", func(t *testing.T) {
		if IsContentMigrationTask("", nil) {
			t.Fatal("expected empty task to be ignored")
		}
	})

	t.Run("empty strings are ignored", func(t *testing.T) {
		description := "   "
		if IsContentMigrationTask("   ", &description) {
			t.Fatal("expected empty strings to be ignored")
		}
	})
}

func TestParseContentMigrationCheckpoint(t *testing.T) {
	tests := []struct {
		name     string
		metadata json.RawMessage
		wantOK   bool
		wantPath string
	}{
		{
			name:     "valid json",
			metadata: json.RawMessage(`{"content_migration_checkpoint":{"version":1,"checkpoint_path":".ottercamp/checkpoints/task.md"}}`),
			wantOK:   true,
			wantPath: ".ottercamp/checkpoints/task.md",
		},
		{
			name:     "invalid json",
			metadata: json.RawMessage(`{`),
			wantOK:   false,
		},
		{
			name:     "missing key",
			metadata: json.RawMessage(`{"other":{"version":1}}`),
			wantOK:   false,
		},
		{
			name:     "null value",
			metadata: json.RawMessage(`{"content_migration_checkpoint":null}`),
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseContentMigrationCheckpoint(tt.metadata)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if ok && got.CheckpointPath != tt.wantPath {
				t.Fatalf("checkpoint path = %q, want %q", got.CheckpointPath, tt.wantPath)
			}
		})
	}
}

func TestMergeContentMigrationCheckpoint(t *testing.T) {
	t.Run("empty metadata", func(t *testing.T) {
		merged, err := MergeContentMigrationCheckpoint(nil, ContentMigrationCheckpoint{
			CheckpointPath: "artifacts/checkpoint.md",
		})
		if err != nil {
			t.Fatalf("MergeContentMigrationCheckpoint: %v", err)
		}

		checkpoint := mustParseMergedCheckpoint(t, merged)
		if checkpoint.CheckpointPath != "artifacts/checkpoint.md" {
			t.Fatalf("checkpoint path = %q, want %q", checkpoint.CheckpointPath, "artifacts/checkpoint.md")
		}
		if checkpoint.Version != checkpointVersion {
			t.Fatalf("version = %d, want %d", checkpoint.Version, checkpointVersion)
		}
	})

	t.Run("existing metadata preserved", func(t *testing.T) {
		merged, err := MergeContentMigrationCheckpoint(
			json.RawMessage(`{"other":{"keep":true}}`),
			ContentMigrationCheckpoint{
				Version:        7,
				CheckpointPath: "content/posts/checkpoint.md",
			},
		)
		if err != nil {
			t.Fatalf("MergeContentMigrationCheckpoint: %v", err)
		}

		var payload map[string]json.RawMessage
		if err := json.Unmarshal(merged, &payload); err != nil {
			t.Fatalf("json.Unmarshal merged: %v", err)
		}
		if _, ok := payload["other"]; !ok {
			t.Fatal("expected unrelated metadata to be preserved")
		}

		checkpoint := mustParseMergedCheckpoint(t, merged)
		if checkpoint.Version != 7 {
			t.Fatalf("version = %d, want 7", checkpoint.Version)
		}
	})

	t.Run("version defaulting", func(t *testing.T) {
		merged, err := MergeContentMigrationCheckpoint(
			json.RawMessage(`{"existing":"value"}`),
			ContentMigrationCheckpoint{
				Version:        0,
				CheckpointPath: "content/posts/default-version.md",
			},
		)
		if err != nil {
			t.Fatalf("MergeContentMigrationCheckpoint: %v", err)
		}

		checkpoint := mustParseMergedCheckpoint(t, merged)
		if checkpoint.Version != checkpointVersion {
			t.Fatalf("version = %d, want %d", checkpoint.Version, checkpointVersion)
		}
	})
}

func TestScanWorkspaceClassifiesAndSkipsDirectories(t *testing.T) {
	root := t.TempDir()
	baseTime := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)

	writeFileWithTime(t, root, "artifacts/post-manifest.json", "{}", baseTime.Add(1*time.Minute))
	writeFileWithTime(t, root, "raw/fetch-state.yaml", "items: []", baseTime.Add(2*time.Minute))
	writeFileWithTime(t, root, "scripts/migrate.py", "print('ok')", baseTime.Add(3*time.Minute))
	writeFileWithTime(t, root, "README.md", "# ignored", baseTime.Add(4*time.Minute))
	writeFileWithTime(t, root, ".git/objects/ignored.md", "ignored", baseTime.Add(5*time.Minute))
	writeFileWithTime(t, root, ".ottercamp/checkpoints/ignored.json", "{}", baseTime.Add(6*time.Minute))
	writeFileWithTime(t, root, "node_modules/pkg/ignored.md", "ignored", baseTime.Add(7*time.Minute))

	for i := 0; i < maxTrackedFilesPerClass+1; i++ {
		name := fmt.Sprintf("content/posts/post-%02d.md", i)
		writeFileWithTime(t, root, name, fmt.Sprintf("# Post %d", i), baseTime.Add(time.Duration(10+i)*time.Minute))
	}

	snapshot, err := ScanWorkspace(root)
	if err != nil {
		t.Fatalf("ScanWorkspace: %v", err)
	}

	assertPathsEqual(t, snapshot.Artifacts, []string{
		"raw/fetch-state.yaml",
		"artifacts/post-manifest.json",
	})
	assertPathsEqual(t, snapshot.Scripts, []string{
		"scripts/migrate.py",
	})

	if len(snapshot.Outputs) != maxTrackedFilesPerClass {
		t.Fatalf("output count = %d, want %d", len(snapshot.Outputs), maxTrackedFilesPerClass)
	}
	if snapshot.Outputs[0].Path != "content/posts/post-08.md" {
		t.Fatalf("newest output = %q, want %q", snapshot.Outputs[0].Path, "content/posts/post-08.md")
	}
	if snapshot.Outputs[len(snapshot.Outputs)-1].Path != "content/posts/post-01.md" {
		t.Fatalf("oldest retained output = %q, want %q", snapshot.Outputs[len(snapshot.Outputs)-1].Path, "content/posts/post-01.md")
	}
	for _, item := range snapshot.Outputs {
		if item.Path == "README.md" {
			t.Fatal("README.md should not be classified as output")
		}
		if hasIgnoredPrefix(item.Path) {
			t.Fatalf("ignored directory leaked into outputs: %q", item.Path)
		}
	}
}

func TestNormalizeTrackedFilesSortsAndTruncates(t *testing.T) {
	baseTime := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	items := []WorkspaceFile{
		{Path: "beta.md", ModifiedAt: baseTime.Add(10 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "alpha.md", ModifiedAt: baseTime.Add(10 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "charlie.md", ModifiedAt: baseTime.Add(9 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "delta.md", ModifiedAt: baseTime.Add(8 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "echo.md", ModifiedAt: baseTime.Add(7 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "foxtrot.md", ModifiedAt: baseTime.Add(6 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "golf.md", ModifiedAt: baseTime.Add(5 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "hotel.md", ModifiedAt: baseTime.Add(4 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "india.md", ModifiedAt: baseTime.Add(3 * time.Minute).Format(time.RFC3339Nano)},
		{Path: "juliet.md", ModifiedAt: baseTime.Add(2 * time.Minute).Format(time.RFC3339Nano)},
	}

	got := normalizeTrackedFiles(items)
	if len(got) != maxTrackedFilesPerClass {
		t.Fatalf("len(normalizeTrackedFiles) = %d, want %d", len(got), maxTrackedFilesPerClass)
	}

	assertPathsEqual(t, got, []string{
		"alpha.md",
		"beta.md",
		"charlie.md",
		"delta.md",
		"echo.md",
		"foxtrot.md",
		"golf.md",
		"hotel.md",
	})
}

func TestIsOutputPathRequiresRecognizedContentDir(t *testing.T) {
	if isOutputPath("README.md") {
		t.Fatal("expected README.md to be excluded")
	}
	if !isOutputPath("content/posts/hello.md") {
		t.Fatal("expected content/posts/hello.md to be classified as output")
	}
}

func TestContentMigrationCheckpointGuidancePrioritizesFirstOutput(t *testing.T) {
	checkpoint := ContentMigrationCheckpoint{
		CheckpointPath: ".ottercamp/checkpoints/oc-296-content-migration.md",
		Artifacts: []WorkspaceFile{
			{Path: "artifacts/raw/post-1.html"},
		},
		Scripts: []WorkspaceFile{
			{Path: "scripts/migrate.py"},
			{Path: "scrape_posts.py"},
		},
	}

	promptLines := strings.Join(PromptStrategyLines(&checkpoint), "\n")
	if !strings.Contains(promptLines, "no migrated output files are on disk yet") {
		t.Fatalf("prompt guidance missing no-output directive:\n%s", promptLines)
	}
	if !strings.Contains(promptLines, "do not spend the next turn re-listing workspace state") {
		t.Fatalf("prompt guidance missing anti-loop directive:\n%s", promptLines)
	}
	if !strings.Contains(promptLines, "artifacts/raw/post-1.html") {
		t.Fatalf("prompt guidance missing artifact path:\n%s", promptLines)
	}
	if !strings.Contains(promptLines, "scripts/migrate.py") {
		t.Fatalf("prompt guidance missing script path:\n%s", promptLines)
	}

	systemMessage := BuildSystemMessage(checkpoint)
	if !strings.Contains(systemMessage, "no migrated output files are on disk yet") {
		t.Fatalf("system message missing no-output directive:\n%s", systemMessage)
	}

	document := BuildCheckpointDocument("Task #296", checkpoint)
	if !strings.Contains(document, "## Immediate Next Action") {
		t.Fatalf("checkpoint doc missing immediate next action section:\n%s", document)
	}
	if !strings.Contains(document, "write at least one migrated output file") {
		t.Fatalf("checkpoint doc missing output directive:\n%s", document)
	}
}

func mustParseMergedCheckpoint(t *testing.T, merged json.RawMessage) ContentMigrationCheckpoint {
	t.Helper()

	checkpoint, ok := ParseContentMigrationCheckpoint(merged)
	if !ok {
		t.Fatal("expected merged checkpoint metadata to parse")
	}
	return checkpoint
}

func writeFileWithTime(t *testing.T, root, relPath, contents string, modTime time.Time) {
	t.Helper()

	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", relPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", relPath, err)
	}
	if err := os.Chtimes(fullPath, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(%q): %v", relPath, err)
	}
}

func assertPathsEqual(t *testing.T, items []WorkspaceFile, want []string) {
	t.Helper()

	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.Path)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func hasIgnoredPrefix(path string) bool {
	return strings.HasPrefix(path, ".git/") ||
		strings.HasPrefix(path, ".ottercamp/") ||
		strings.HasPrefix(path, "node_modules/")
}
