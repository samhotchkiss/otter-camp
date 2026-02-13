package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeOpenClawEllieBackfillRunner struct {
	called bool
	inputs []OpenClawEllieBackfillInput
	result OpenClawEllieBackfillResult
	err    error
}

func (f *fakeOpenClawEllieBackfillRunner) RunBackfill(
	_ context.Context,
	input OpenClawEllieBackfillInput,
) (OpenClawEllieBackfillResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)
	if f.err != nil {
		return OpenClawEllieBackfillResult{}, f.err
	}
	return f.result, nil
}

func TestMigrationRunnerPauseAndResumeFromCheckpoint(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-pause-resume")
	userID := createOpenClawImportTestUser(t, db, orgID, "runner-pause-resume")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(sessionDir, "main-runner.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"one"}]}}`,
		`{"type":"message","id":"u2","timestamp":"2026-01-01T10:00:02Z","message":{"role":"user","content":[{"type":"text","text":"two"}]}}`,
		`{"type":"message","id":"u3","timestamp":"2026-01-01T10:00:03Z","message":{"role":"user","content":[{"type":"text","text":"three"}]}}`,
	})

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)
	_, err = ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)

	events, err := ParseOpenClawSessionEvents(install)
	require.NoError(t, err)
	require.Len(t, events, 3)

	progressStore := store.NewMigrationProgressStore(db)
	runner := NewOpenClawMigrationRunner(db)
	runner.HistoryCheckpoint = 1
	runner.OnHistoryCheckpoint = func(processed, total int) {
		if processed != 1 {
			return
		}
		_, _ = progressStore.SetStatus(context.Background(), store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "history_backfill",
			Status:        store.MigrationProgressStatusPaused,
			CurrentLabel:  migrationRunnerStringPtr("paused by test"),
		})
	}

	firstRun, err := runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		HistoryOnly:  true,
	})
	require.NoError(t, err)
	require.True(t, firstRun.Paused)

	historyProgress, err := progressStore.GetByType(context.Background(), orgID, "history_backfill")
	require.NoError(t, err)
	require.NotNil(t, historyProgress)
	require.Equal(t, store.MigrationProgressStatusPaused, historyProgress.Status)
	require.Equal(t, 1, historyProgress.ProcessedItems)

	_, err = progressStore.SetStatus(context.Background(), store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "history_backfill",
		Status:        store.MigrationProgressStatusRunning,
		CurrentLabel:  migrationRunnerStringPtr("resumed"),
	})
	require.NoError(t, err)

	runner.OnHistoryCheckpoint = nil
	secondRun, err := runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		HistoryOnly:  true,
	})
	require.NoError(t, err)
	require.False(t, secondRun.Paused)
	require.NotNil(t, secondRun.HistoryBackfill)

	historyProgress, err = progressStore.GetByType(context.Background(), orgID, "history_backfill")
	require.NoError(t, err)
	require.NotNil(t, historyProgress)
	require.Equal(t, store.MigrationProgressStatusCompleted, historyProgress.Status)
	require.Equal(t, len(events), historyProgress.ProcessedItems)

	var messageCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE org_id = $1`, orgID).Scan(&messageCount)
	require.NoError(t, err)
	require.Equal(t, len(events), messageCount)
}

func TestMigrationRunnerStartsEllieBackfillPhase(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-ellie-backfill")
	userID := createOpenClawImportTestUser(t, db, orgID, "runner-ellie-backfill")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(sessionDir, "main-backfill-phase.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"one"}]}}`,
	})

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)
	_, err = ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)

	events, err := ParseOpenClawSessionEvents(install)
	require.NoError(t, err)
	require.Len(t, events, 1)

	backfillRunner := &fakeOpenClawEllieBackfillRunner{
		result: OpenClawEllieBackfillResult{
			ProcessedMessages: 4,
			ExtractedMemories: 2,
		},
	}

	runner := NewOpenClawMigrationRunner(db)
	runner.EllieBackfillRunner = backfillRunner

	runResult, err := runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		HistoryOnly:  true,
	})
	require.NoError(t, err)
	require.NotNil(t, runResult.HistoryBackfill)
	require.NotNil(t, runResult.EllieBackfill)
	require.Equal(t, 4, runResult.EllieBackfill.ProcessedMessages)
	require.Equal(t, 2, runResult.EllieBackfill.ExtractedMemories)

	require.True(t, backfillRunner.called)
	require.Len(t, backfillRunner.inputs, 1)
	require.Equal(t, orgID, backfillRunner.inputs[0].OrgID)

	progressStore := store.NewMigrationProgressStore(db)
	memoryProgress, err := progressStore.GetByType(context.Background(), orgID, "memory_extraction")
	require.NoError(t, err)
	require.NotNil(t, memoryProgress)
	require.Equal(t, store.MigrationProgressStatusCompleted, memoryProgress.Status)
	require.Equal(t, 4, memoryProgress.ProcessedItems)
	require.Equal(t, "memory extraction complete", memoryProgress.CurrentLabel)
}
