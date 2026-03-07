package taskcheckpoint

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ContentMigrationMetadataKey = "content_migration_checkpoint"
	checkpointDir               = ".ottercamp/checkpoints"
	checkpointVersion           = 1
	maxTrackedFilesPerClass     = 8
)

type WorkspaceFile struct {
	Path       string `json:"path"`
	ByteSize   int64  `json:"byte_size,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

type ContentMigrationSnapshot struct {
	Artifacts []WorkspaceFile `json:"artifacts,omitempty"`
	Scripts   []WorkspaceFile `json:"scripts,omitempty"`
	Outputs   []WorkspaceFile `json:"outputs,omitempty"`
}

type ContentMigrationCheckpoint struct {
	Version               int             `json:"version"`
	CheckpointPath        string          `json:"checkpoint_path"`
	HistoryStartMessageID string          `json:"history_start_message_id,omitempty"`
	UpdatedAt             string          `json:"updated_at,omitempty"`
	Artifacts             []WorkspaceFile `json:"artifacts,omitempty"`
	Scripts               []WorkspaceFile `json:"scripts,omitempty"`
	Outputs               []WorkspaceFile `json:"outputs,omitempty"`
}

func IsContentMigrationTask(title string, description *string) bool {
	text := strings.TrimSpace(title)
	if description != nil {
		if desc := strings.TrimSpace(*description); desc != "" {
			if text != "" {
				text += "\n"
			}
			text += desc
		}
	}
	if text == "" {
		return false
	}

	lower := strings.ToLower(text)
	migrationSignal := false
	for _, token := range []string{
		"migrate",
		"migration",
		"import",
		"backfill",
		"scrape",
		"convert",
	} {
		if strings.Contains(lower, token) {
			migrationSignal = true
			break
		}
	}
	if !migrationSignal {
		return false
	}

	for _, token := range []string{
		"content",
		"blog",
		"post",
		"article",
		"page",
		"docs",
		"markdown",
		"manifest",
		"site",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func CheckpointRelativePath(taskNumber int, taskID uuid.UUID) string {
	label := "task-" + taskID.String()[:8]
	if taskNumber > 0 {
		label = fmt.Sprintf("oc-%d-content-migration", taskNumber)
	}
	return filepath.ToSlash(filepath.Join(checkpointDir, label+".md"))
}

func ScanWorkspace(root string) (ContentMigrationSnapshot, error) {
	trimmedRoot := strings.TrimSpace(root)
	if trimmedRoot == "" {
		return ContentMigrationSnapshot{}, nil
	}
	if _, err := os.Stat(trimmedRoot); err != nil {
		if errorsIsNotExist(err) {
			return ContentMigrationSnapshot{}, nil
		}
		return ContentMigrationSnapshot{}, err
	}

	snapshot := ContentMigrationSnapshot{}
	err := filepath.WalkDir(trimmedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == trimmedRoot {
			return nil
		}

		rel, err := filepath.Rel(trimmedRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if entry.IsDir() {
			lower := strings.ToLower(rel)
			if lower == ".git" || strings.HasPrefix(lower, ".git/") || lower == ".ottercamp" || strings.HasPrefix(lower, ".ottercamp/") || lower == "node_modules" || strings.HasPrefix(lower, "node_modules/") {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := WorkspaceFile{
			Path:       rel,
			ByteSize:   info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
		}

		switch {
		case isScriptPath(rel):
			snapshot.Scripts = append(snapshot.Scripts, item)
		case isArtifactPath(rel):
			snapshot.Artifacts = append(snapshot.Artifacts, item)
		case isOutputPath(rel):
			snapshot.Outputs = append(snapshot.Outputs, item)
		}
		return nil
	})
	if err != nil {
		return ContentMigrationSnapshot{}, err
	}

	snapshot.Artifacts = normalizeTrackedFiles(snapshot.Artifacts)
	snapshot.Scripts = normalizeTrackedFiles(snapshot.Scripts)
	snapshot.Outputs = normalizeTrackedFiles(snapshot.Outputs)
	return snapshot, nil
}

func ParseContentMigrationCheckpoint(metadata json.RawMessage) (ContentMigrationCheckpoint, bool) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ContentMigrationCheckpoint{}, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ContentMigrationCheckpoint{}, false
	}
	raw, ok := payload[ContentMigrationMetadataKey]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ContentMigrationCheckpoint{}, false
	}
	var checkpoint ContentMigrationCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return ContentMigrationCheckpoint{}, false
	}
	if checkpoint.Version <= 0 {
		checkpoint.Version = checkpointVersion
	}
	return checkpoint, true
}

func MergeContentMigrationCheckpoint(metadata json.RawMessage, checkpoint ContentMigrationCheckpoint) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(metadata) != 0 && json.Valid(metadata) {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return nil, err
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if checkpoint.Version <= 0 {
		checkpoint.Version = checkpointVersion
	}
	payload[ContentMigrationMetadataKey] = checkpoint
	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func HistoryStartMessageID(checkpoint *ContentMigrationCheckpoint) *uuid.UUID {
	if checkpoint == nil {
		return nil
	}
	id, err := uuid.Parse(strings.TrimSpace(checkpoint.HistoryStartMessageID))
	if err != nil || id == uuid.Nil {
		return nil
	}
	return &id
}

func PromptStrategyLines(checkpoint *ContentMigrationCheckpoint) []string {
	lines := []string{
		"Migration Execution Strategy:",
		"- Persist fetched pages, manifests, and intermediate transforms to workspace files immediately.",
		"- Resume from scripts and persisted artifacts instead of replaying raw page bodies in chat.",
		"- Produce migrated content files incrementally during the run; do not wait for one final bulk write.",
	}
	if checkpoint == nil {
		return lines
	}
	if path := strings.TrimSpace(checkpoint.CheckpointPath); path != "" {
		lines = append(lines, "- Latest checkpoint: "+path)
	}
	if summary := summarizeFiles("Persisted artifacts", checkpoint.Artifacts, 3); summary != "" {
		lines = append(lines, "- "+summary)
	}
	if summary := summarizeFiles("Persisted scripts", checkpoint.Scripts, 3); summary != "" {
		lines = append(lines, "- "+summary)
	}
	if summary := summarizeFiles("Persisted outputs", checkpoint.Outputs, 3); summary != "" {
		lines = append(lines, "- "+summary)
	}
	return lines
}

func BuildSystemMessage(checkpoint ContentMigrationCheckpoint) string {
	parts := []string{
		"[Content migration checkpoint] Resume from persisted workspace state instead of replaying raw page content.",
	}
	if path := strings.TrimSpace(checkpoint.CheckpointPath); path != "" {
		parts = append(parts, "Checkpoint: "+path+".")
	}
	if summary := summarizeFiles("Artifacts", checkpoint.Artifacts, 2); summary != "" {
		parts = append(parts, summary+".")
	}
	if summary := summarizeFiles("Scripts", checkpoint.Scripts, 2); summary != "" {
		parts = append(parts, summary+".")
	}
	if summary := summarizeFiles("Outputs", checkpoint.Outputs, 2); summary != "" {
		parts = append(parts, summary+".")
	}
	parts = append(parts, "Read and update these files directly; do not re-paste fetched page bodies into chat.")
	return strings.Join(parts, " ")
}

func BuildCheckpointDocument(taskLabel string, checkpoint ContentMigrationCheckpoint) string {
	lines := []string{
		"# Content Migration Checkpoint",
		"",
		"Generated: " + strings.TrimSpace(checkpoint.UpdatedAt),
		"Task: " + strings.TrimSpace(taskLabel),
		"Checkpoint Path: " + strings.TrimSpace(checkpoint.CheckpointPath),
		"",
		"## Resume Rules",
		"- Read and update the persisted files listed below instead of replaying raw fetch content in chat.",
		"- Keep raw fetches/manifests as files or artifacts and use scripts to transform them.",
		"- Write migrated output files incrementally during the run.",
	}
	lines = append(lines, renderCheckpointSection("Artifacts", checkpoint.Artifacts)...)
	lines = append(lines, renderCheckpointSection("Scripts", checkpoint.Scripts)...)
	lines = append(lines, renderCheckpointSection("Outputs", checkpoint.Outputs)...)
	return strings.Join(lines, "\n") + "\n"
}

func normalizeTrackedFiles(items []WorkspaceFile) []WorkspaceFile {
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ModifiedAt == items[j].ModifiedAt {
			return items[i].Path < items[j].Path
		}
		return items[i].ModifiedAt > items[j].ModifiedAt
	})
	if len(items) > maxTrackedFilesPerClass {
		items = append([]WorkspaceFile(nil), items[:maxTrackedFilesPerClass]...)
	} else {
		items = append([]WorkspaceFile(nil), items...)
	}
	return items
}

func renderCheckpointSection(title string, items []WorkspaceFile) []string {
	lines := []string{"", "## " + title}
	if len(items) == 0 {
		return append(lines, "(none)")
	}
	for _, item := range items {
		line := "- " + item.Path
		if item.ModifiedAt != "" {
			line += " (" + item.ModifiedAt + ")"
		}
		lines = append(lines, line)
	}
	return lines
}

func summarizeFiles(label string, items []WorkspaceFile, limit int) string {
	if len(items) == 0 {
		return ""
	}
	names := make([]string, 0, len(items))
	for i, item := range items {
		if i >= limit {
			break
		}
		names = append(names, item.Path)
	}
	return label + ": " + strings.Join(names, ", ")
}

func isScriptPath(rel string) bool {
	lower := strings.ToLower(rel)
	ext := strings.ToLower(filepath.Ext(lower))
	switch ext {
	case ".py", ".sh", ".js", ".ts", ".mjs", ".cjs", ".rb", ".go":
	default:
		return false
	}
	return strings.Contains(lower, "/scripts/") ||
		strings.HasPrefix(lower, "scripts/") ||
		strings.Contains(lower, "migrate") ||
		strings.Contains(lower, "scrape") ||
		strings.Contains(lower, "import")
}

func isArtifactPath(rel string) bool {
	lower := strings.ToLower(rel)
	if strings.Contains(lower, "/artifacts/") || strings.HasPrefix(lower, "artifacts/") ||
		strings.Contains(lower, "/raw/") || strings.HasPrefix(lower, "raw/") ||
		strings.Contains(lower, "manifest") ||
		strings.Contains(lower, "checkpoint") ||
		strings.Contains(lower, "state") ||
		strings.Contains(lower, "fetch") ||
		strings.Contains(lower, "scrape") {
		return true
	}
	switch strings.ToLower(filepath.Ext(lower)) {
	case ".json", ".jsonl", ".yaml", ".yml", ".csv":
		return true
	default:
		return false
	}
}

func isOutputPath(rel string) bool {
	lower := strings.ToLower(rel)
	if strings.HasPrefix(lower, ".") {
		return false
	}
	if !hasRecognizedContentDir(lower) {
		return false
	}
	switch filepath.Ext(lower) {
	case ".md", ".mdx", ".html":
		return true
	default:
		return false
	}
}

func hasRecognizedContentDir(lower string) bool {
	return strings.Contains(lower, "/content/") || strings.HasPrefix(lower, "content/") ||
		strings.Contains(lower, "/posts/") || strings.HasPrefix(lower, "posts/") ||
		strings.Contains(lower, "/articles/") || strings.HasPrefix(lower, "articles/") ||
		strings.Contains(lower, "/pages/") || strings.HasPrefix(lower, "pages/") ||
		strings.Contains(lower, "/blog/") || strings.HasPrefix(lower, "blog/")
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
