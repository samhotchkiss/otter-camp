package importer

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenClawSessionParserExtractsConversationEvents(t *testing.T) {
	root := t.TempDir()
	mainSessionDir := filepath.Join(root, "agents", "main", "sessions")
	loriSessionDir := filepath.Join(root, "agents", "lori", "sessions")
	require.NoError(t, os.MkdirAll(mainSessionDir, 0o755))
	require.NoError(t, os.MkdirAll(loriSessionDir, 0o755))

	mainSession := filepath.Join(mainSessionDir, "main-001.jsonl")
	loriSession := filepath.Join(loriSessionDir, "lori-001.jsonl")

	writeOpenClawSessionFixture(t, mainSession, []string{
		`{"type":"session","id":"session-main","timestamp":"2026-01-01T10:00:00Z"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":[{"type":"text","text":"Need a migration plan"}]}}`,
		`{"type":"message","id":"a1","timestamp":"2026-01-01T10:00:09Z","message":{"role":"assistant","content":[{"type":"text","text":"Here is the draft plan."}]}}`,
	})
	writeOpenClawSessionFixture(t, loriSession, []string{
		`{"type":"message","id":"u2","timestamp":"2026-01-01T10:00:07Z","message":{"role":"user","content":[{"type":"text","text":"Please sync calendars"}]}}`,
		`{"type":"message","id":"tr1","timestamp":"2026-01-01T10:00:11Z","message":{"role":"toolResult","toolName":"read","content":[{"type":"text","text":"loaded 54 rows"}]}}`,
	})

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Len(t, events, 4)

	require.Equal(t, "main", events[0].AgentSlug)
	require.Equal(t, OpenClawSessionEventRoleUser, events[0].Role)
	require.Equal(t, "Need a migration plan", events[0].Body)

	require.Equal(t, "lori", events[1].AgentSlug)
	require.Equal(t, OpenClawSessionEventRoleUser, events[1].Role)
	require.Equal(t, "Please sync calendars", events[1].Body)

	require.Equal(t, "main", events[2].AgentSlug)
	require.Equal(t, OpenClawSessionEventRoleAssistant, events[2].Role)
	require.Equal(t, "Here is the draft plan.", events[2].Body)

	require.Equal(t, "lori", events[3].AgentSlug)
	require.Equal(t, OpenClawSessionEventRoleToolResult, events[3].Role)
	require.Contains(t, events[3].Body, "Tool read result")
	require.Contains(t, events[3].Body, "loaded 54 rows")

	require.True(t, events[0].CreatedAt.Before(events[1].CreatedAt))
	require.True(t, events[1].CreatedAt.Before(events[2].CreatedAt))
	require.True(t, events[2].CreatedAt.Before(events[3].CreatedAt))
}

func TestOpenClawSessionParserSkipsThinkingAndOperationalEvents(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	sessionPath := filepath.Join(sessionDir, "main-ops.jsonl")
	writeOpenClawSessionFixture(t, sessionPath, []string{
		`{"type":"session","id":"session-main","timestamp":"2026-01-01T10:00:00Z"}`,
		`{"type":"model_change","id":"m1","timestamp":"2026-01-01T10:00:01Z","modelId":"claude-opus-4-6"}`,
		`{"type":"thinking_level_change","id":"tl1","timestamp":"2026-01-01T10:00:02Z","thinkingLevel":"low"}`,
		`{"type":"custom","id":"c1","timestamp":"2026-01-01T10:00:03Z","payload":{"note":"ops"}}`,
		`{"type":"message","id":"a-thinking","timestamp":"2026-01-01T10:00:04Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden rationale only"}]}}`,
		`{"type":"message","id":"a-mixed","timestamp":"2026-01-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private notes"},{"type":"text","text":"Public answer"}]}}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:06Z","message":{"role":"user","content":[{"type":"text","text":"Thanks"}]}}`,
	})

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Len(t, events, 2)

	require.Equal(t, OpenClawSessionEventRoleAssistant, events[0].Role)
	require.Equal(t, "Public answer", events[0].Body)
	require.NotContains(t, events[0].Body, "private notes")
	require.NotContains(t, events[0].Body, "hidden rationale")

	require.Equal(t, OpenClawSessionEventRoleUser, events[1].Role)
	require.Equal(t, "Thanks", events[1].Body)

	expectedTimes := []time.Time{
		time.Date(2026, time.January, 1, 10, 0, 5, 0, time.UTC),
		time.Date(2026, time.January, 1, 10, 0, 6, 0, time.UTC),
	}
	require.Equal(t, expectedTimes[0], events[0].CreatedAt)
	require.Equal(t, expectedTimes[1], events[1].CreatedAt)
}

func TestOpenClawSessionParserRejectsSymlinkedSessionFile(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	outsideDir := t.TempDir()
	outsideSessionPath := filepath.Join(outsideDir, "outside.jsonl")
	writeOpenClawSessionFixture(t, outsideSessionPath, []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":[{"type":"text","text":"outside"}]}}`,
	})

	symlinkSessionPath := filepath.Join(sessionDir, "linked.jsonl")
	require.NoError(t, os.Symlink(outsideSessionPath, symlinkSessionPath))

	_, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be a symlink")
}

func TestOpenClawSessionParserLenientModeSkipsMalformedLines(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	sessionPath := filepath.Join(sessionDir, "mixed-valid-invalid.jsonl")
	writeOpenClawSessionFixture(t, sessionPath, []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":[{"type":"text","text":"Need a migration plan"}]}}`,
		`{"type":"message","id":"bad","timestamp":"2026-01-01T10:00:06Z","message":`,
		`{"type":"message","id":"a1","timestamp":"2026-01-01T10:00:07Z","message":{"role":"assistant","content":[{"type":"text","text":"Here is the plan."}]}}`,
	})

	var logBuf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, OpenClawSessionEventRoleUser, events[0].Role)
	require.Equal(t, OpenClawSessionEventRoleAssistant, events[1].Role)
	require.Contains(t, strings.ToLower(logBuf.String()), "skipping malformed openclaw jsonl line")
}

func TestOpenClawSessionParserStrictModeRejectsMalformedLines(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	sessionPath := filepath.Join(sessionDir, "strict-invalid.jsonl")
	writeOpenClawSessionFixture(t, sessionPath, []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":[{"type":"text","text":"Need a migration plan"}]}}`,
		`{"type":"message","id":"bad","timestamp":"2026-01-01T10:00:06Z","message":`,
	})

	_, err := ParseOpenClawSessionEventsStrict(&OpenClawInstallation{RootDir: root})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to parse openclaw jsonl")
}

func TestOpenClawSessionParserEmptyFile(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "empty.jsonl"), []byte{}, 0o644))

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestOpenClawSessionParserBlankLinesOnly(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "blank-lines.jsonl"), []byte("\n\n  \n\t\n"), 0o644))

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestOpenClawSessionParserInvalidJSONOnly(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "invalid-only.jsonl"), []byte("{invalid json\n{also invalid\n"), 0o644))

	var logBuf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Empty(t, events)
	require.Contains(t, strings.ToLower(logBuf.String()), "skipping malformed openclaw jsonl line")
}

func TestOpenClawSessionParserNearBufferLimitLine(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	largeText := strings.Repeat("x", (8*1024*1024)-1024)
	line := fmt.Sprintf(`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":[{"type":"text","text":"%s"}]}}`, largeText)
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "large-line.jsonl"), []byte(line+"\n"), 0o644))

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, OpenClawSessionEventRoleUser, events[0].Role)
	require.NotEmpty(t, events[0].Body)
}

func TestDiscoverOpenClawSessionFilesIncludesBackupLayouts(t *testing.T) {
	root := t.TempDir()

	// Standard layout
	stdDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(stdDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stdDir, "aaa.jsonl"), []byte(`{}`+"\n"), 0o644))

	// Backup layout: <slug>-sessions/
	backupDir := filepath.Join(root, "sessions-backup-20260129", "essie-sessions")
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "bbb.jsonl"), []byte(`{}`+"\n"), 0o644))

	// Non-session jsonl (should be excluded)
	logsDir := filepath.Join(root, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "config-audit.jsonl"), []byte(`{}`+"\n"), 0o644))

	// Jsonl not under a sessions dir (should be excluded)
	require.NoError(t, os.WriteFile(filepath.Join(root, "random.jsonl"), []byte(`{}`+"\n"), 0o644))

	files, err := discoverOpenClawSessionFiles(root)
	require.NoError(t, err)
	require.Len(t, files, 2, "should find standard + backup, exclude logs + root")

	// Check that both are found
	var bases []string
	for _, f := range files {
		bases = append(bases, filepath.Base(f))
	}
	require.Contains(t, bases, "aaa.jsonl")
	require.Contains(t, bases, "bbb.jsonl")
}

func TestSessionDiscoveryIncludesDefaultArchiveBackups(t *testing.T) {
	root := t.TempDir()

	primaryDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(primaryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(primaryDir, "primary.jsonl"), []byte(`{}`+"\n"), 0o644))

	archiveDir := filepath.Join(root, "sessions-backup-20260129-230025", "raw-export", "main")
	require.NoError(t, os.MkdirAll(archiveDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "archived.jsonl"), []byte(`{}`+"\n"), 0o644))

	files, err := discoverOpenClawSessionFiles(root)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, []string{
		filepath.Join(primaryDir, "primary.jsonl"),
		filepath.Join(archiveDir, "archived.jsonl"),
	}, files)
}

func TestSessionDiscoveryIncludesConfiguredArchiveGlobs(t *testing.T) {
	root := t.TempDir()

	primaryDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(primaryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(primaryDir, "primary.jsonl"), []byte(`{}`+"\n"), 0o644))

	configuredArchiveDir := filepath.Join(root, "archives-202602", "jsonl-export")
	require.NoError(t, os.MkdirAll(configuredArchiveDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configuredArchiveDir, "configured.jsonl"), []byte(`{}`+"\n"), 0o644))

	t.Setenv("OPENCLAW_SESSION_ARCHIVE_GLOBS", "archives-*")

	files, err := discoverOpenClawSessionFiles(root)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, []string{
		filepath.Join(primaryDir, "primary.jsonl"),
		filepath.Join(root, "archives-202602", "jsonl-export", "configured.jsonl"),
	}, files)
}

func TestDeriveMetadataFromBackupLayout(t *testing.T) {
	// Standard: agents/main/sessions/uuid.jsonl → main, uuid
	slug, id := deriveOpenClawSessionPathMetadata("/root/agents/main/sessions/abc-123.jsonl")
	require.Equal(t, "main", slug)
	require.Equal(t, "abc-123", id)

	// Backup: sessions-backup/essie-sessions/uuid.jsonl → essie, uuid
	slug, id = deriveOpenClawSessionPathMetadata("/root/sessions-backup-20260129/essie-sessions/def-456.jsonl")
	require.Equal(t, "essie", slug)
	require.Equal(t, "def-456", id)

	// Backup with compound slug: email-mgmt-sessions/uuid.jsonl → email-mgmt, uuid
	slug, id = deriveOpenClawSessionPathMetadata("/root/backup/email-mgmt-sessions/xyz.jsonl")
	require.Equal(t, "email-mgmt", slug)
	require.Equal(t, "xyz", id)
}

func TestParseOpenClawSessionEventsIncludesBackupFiles(t *testing.T) {
	root := t.TempDir()

	// Standard layout
	stdDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(stdDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(stdDir, "s1.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":"hello standard"}}`,
	})

	// Backup layout
	backupDir := filepath.Join(root, "sessions-backup-20260129", "essie-sessions")
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(backupDir, "s2.jsonl"), []string{
		`{"type":"message","id":"u2","timestamp":"2026-01-02T10:00:05Z","message":{"role":"user","content":"hello backup"}}`,
	})

	events, err := ParseOpenClawSessionEvents(&OpenClawInstallation{RootDir: root})
	require.NoError(t, err)
	require.Len(t, events, 2)

	// Check agent slugs
	slugs := map[string]bool{}
	for _, e := range events {
		slugs[e.AgentSlug] = true
	}
	require.True(t, slugs["main"])
	require.True(t, slugs["essie"])
}

func writeOpenClawSessionFixture(t *testing.T, path string, lines []string) {
	t.Helper()
	content := ""
	for i, line := range lines {
		content += line
		if i < len(lines)-1 {
			content += "\n"
		}
	}
	content += "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
