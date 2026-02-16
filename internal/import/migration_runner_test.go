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

type fakeOpenClawProjectDiscoveryRunner struct {
	called bool
	inputs []OpenClawProjectDiscoveryInput
	result OpenClawProjectDiscoveryResult
	err    error
	onCall func()
}

type fakeOpenClawEntitySynthesisRunner struct {
	called bool
	inputs []OpenClawEntitySynthesisInput
	result OpenClawEntitySynthesisResult
	err    error
}

type fakeOpenClawProjectDocsScanningRunner struct {
	called bool
	inputs []OpenClawProjectDocsScanningInput
	result OpenClawProjectDocsScanningResult
	err    error
	onCall func()
}

func (f *fakeOpenClawProjectDiscoveryRunner) Discover(
	_ context.Context,
	input OpenClawProjectDiscoveryInput,
) (OpenClawProjectDiscoveryResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)
	if f.onCall != nil {
		f.onCall()
	}
	if f.err != nil {
		return OpenClawProjectDiscoveryResult{}, f.err
	}
	return f.result, nil
}

func (f *fakeOpenClawEntitySynthesisRunner) RunSynthesis(
	_ context.Context,
	input OpenClawEntitySynthesisInput,
) (OpenClawEntitySynthesisResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)
	if f.err != nil {
		return OpenClawEntitySynthesisResult{}, f.err
	}
	return f.result, nil
}

func (f *fakeOpenClawProjectDocsScanningRunner) RunScan(
	_ context.Context,
	input OpenClawProjectDocsScanningInput,
) (OpenClawProjectDocsScanningResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)
	if f.onCall != nil {
		f.onCall()
	}
	if f.err != nil {
		return OpenClawProjectDocsScanningResult{}, f.err
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
		HistoryOnly:  false,
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

func TestMigrationRunnerStartsProjectDiscoveryPhase(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-project-discovery")
	userID := createOpenClawImportTestUser(t, db, orgID, "runner-project-discovery")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(sessionDir, "main-project-discovery.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"project:otter-camp issue: add migration endpoint"}]}}`,
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

	projectDiscoveryRunner := &fakeOpenClawProjectDiscoveryRunner{
		result: OpenClawProjectDiscoveryResult{
			ProjectsCreated: 1,
			IssuesCreated:   2,
			ProcessedItems:  5,
		},
	}

	runner := NewOpenClawMigrationRunner(db)
	runner.ProjectDiscoveryRunner = projectDiscoveryRunner

	runResult, err := runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		HistoryOnly:  false,
	})
	require.NoError(t, err)
	require.NotNil(t, runResult.ProjectDiscovery)
	require.Equal(t, 1, runResult.ProjectDiscovery.ProjectsCreated)
	require.Equal(t, 2, runResult.ProjectDiscovery.IssuesCreated)

	require.True(t, projectDiscoveryRunner.called)
	require.Len(t, projectDiscoveryRunner.inputs, 1)
	require.Equal(t, orgID, projectDiscoveryRunner.inputs[0].OrgID)
	require.Len(t, projectDiscoveryRunner.inputs[0].ParsedEvents, 1)

	progressStore := store.NewMigrationProgressStore(db)
	discoveryProgress, err := progressStore.GetByType(context.Background(), orgID, "project_discovery")
	require.NoError(t, err)
	require.NotNil(t, discoveryProgress)
	require.Equal(t, store.MigrationProgressStatusCompleted, discoveryProgress.Status)
	require.Equal(t, 5, discoveryProgress.ProcessedItems)
	require.Equal(t, "project discovery complete", discoveryProgress.CurrentLabel)
}

func TestMigrationRunnerStartsEntitySynthesisPhase(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-entity-synthesis")
	userID := createOpenClawImportTestUser(t, db, orgID, "runner-entity-synthesis")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(sessionDir, "main-entity-synthesis.jsonl"), []string{
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
			ProcessedMessages: 2,
			ExtractedMemories: 1,
		},
	}
	entityRunner := &fakeOpenClawEntitySynthesisRunner{
		result: OpenClawEntitySynthesisResult{
			ProcessedEntities: 3,
			SynthesizedItems:  2,
		},
	}
	projectDiscoveryRunner := &fakeOpenClawProjectDiscoveryRunner{
		result: OpenClawProjectDiscoveryResult{
			ProcessedItems: 5,
		},
	}

	runner := NewOpenClawMigrationRunner(db)
	runner.EllieBackfillRunner = backfillRunner
	runner.EntitySynthesisRunner = entityRunner
	runner.ProjectDiscoveryRunner = projectDiscoveryRunner

	runResult, err := runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		HistoryOnly:  false,
	})
	require.NoError(t, err)
	require.NotNil(t, runResult.EntitySynthesis)
	require.Equal(t, 3, runResult.EntitySynthesis.ProcessedEntities)
	require.Equal(t, 2, runResult.EntitySynthesis.SynthesizedItems)
	require.True(t, entityRunner.called)
	require.Len(t, entityRunner.inputs, 1)
	require.Equal(t, orgID, entityRunner.inputs[0].OrgID)

	progressStore := store.NewMigrationProgressStore(db)
	entityProgress, err := progressStore.GetByType(context.Background(), orgID, "entity_synthesis")
	require.NoError(t, err)
	require.NotNil(t, entityProgress)
	require.Equal(t, store.MigrationProgressStatusCompleted, entityProgress.Status)
	require.Equal(t, 3, entityProgress.ProcessedItems)
	require.Equal(t, "entity synthesis complete", entityProgress.CurrentLabel)
}

func TestOpenClawMigrationRunnerInvokesProjectDocsScanningAfterProjectDiscovery(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-project-docs-phase")
	userID := createOpenClawImportTestUser(t, db, orgID, "runner-project-docs-phase")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(sessionDir, "main-project-docs-phase.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"project:otter-camp issue: document architecture"}]}}`,
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

	callOrder := make([]string, 0, 2)
	projectDiscoveryRunner := &fakeOpenClawProjectDiscoveryRunner{
		result: OpenClawProjectDiscoveryResult{
			ProcessedItems:  3,
			ProjectsCreated: 1,
			IssuesCreated:   1,
		},
		onCall: func() {
			callOrder = append(callOrder, "project_discovery")
		},
	}
	projectDocsRunner := &fakeOpenClawProjectDocsScanningRunner{
		result: OpenClawProjectDocsScanningResult{
			ProcessedDocs:   4,
			UpdatedDocs:     3,
			InactivatedDocs: 1,
		},
		onCall: func() {
			callOrder = append(callOrder, "project_docs_scanning")
		},
	}

	runner := NewOpenClawMigrationRunner(db)
	runner.ProjectDiscoveryRunner = projectDiscoveryRunner
	runner.ProjectDocsScanningRunner = projectDocsRunner

	runResult, err := runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
	})
	require.NoError(t, err)
	require.NotNil(t, runResult.ProjectDocsScanning)
	require.Equal(t, 4, runResult.ProjectDocsScanning.ProcessedDocs)
	require.True(t, projectDiscoveryRunner.called)
	require.True(t, projectDocsRunner.called)
	require.Equal(t, []string{"project_discovery", "project_docs_scanning"}, callOrder)

	progressStore := store.NewMigrationProgressStore(db)
	docsProgress, err := progressStore.GetByType(context.Background(), orgID, "project_docs_scanning")
	require.NoError(t, err)
	require.NotNil(t, docsProgress)
	require.Equal(t, store.MigrationProgressStatusCompleted, docsProgress.Status)
	require.Equal(t, 4, docsProgress.ProcessedItems)
	require.Equal(t, "project docs scanning complete", docsProgress.CurrentLabel)
}

func TestOpenClawMigrationRunnerSkipsCompletedProjectDocsPhase(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-project-docs-complete")
	progressStore := store.NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), store.StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "project_docs_scanning",
	})
	require.NoError(t, err)
	_, err = progressStore.SetStatus(context.Background(), store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "project_docs_scanning",
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationRunnerStringPtr("already complete"),
	})
	require.NoError(t, err)

	projectDocsRunner := &fakeOpenClawProjectDocsScanningRunner{}
	runner := NewOpenClawMigrationRunner(db)
	runner.ProjectDocsScanningRunner = projectDocsRunner

	result, runErr := runner.runProjectDocsScanningPhase(context.Background(), RunOpenClawMigrationInput{
		OrgID: orgID,
	})
	require.NoError(t, runErr)
	require.Nil(t, result)
	require.False(t, projectDocsRunner.called)
}

func TestMigrationRunnerSkipsEntitySynthesisWhenHistoryOnly(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-history-only")
	userID := createOpenClawImportTestUser(t, db, orgID, "runner-history-only")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(sessionDir, "main-history-only.jsonl"), []string{
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

	backfillRunner := &fakeOpenClawEllieBackfillRunner{}
	entityRunner := &fakeOpenClawEntitySynthesisRunner{}
	projectDiscoveryRunner := &fakeOpenClawProjectDiscoveryRunner{}

	runner := NewOpenClawMigrationRunner(db)
	runner.EllieBackfillRunner = backfillRunner
	runner.EntitySynthesisRunner = entityRunner
	runner.ProjectDiscoveryRunner = projectDiscoveryRunner

	runResult, err := runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		HistoryOnly:  true,
	})
	require.NoError(t, err)
	require.NotNil(t, runResult.HistoryBackfill)
	require.Nil(t, runResult.EllieBackfill)
	require.Nil(t, runResult.EntitySynthesis)
	require.Nil(t, runResult.ProjectDiscovery)
	require.False(t, backfillRunner.called)
	require.False(t, entityRunner.called)
	require.False(t, projectDiscoveryRunner.called)
}

func TestMigrationRunnerResumeSkipsCompletedPhase(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-skip-completed")
	progressStore := store.NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), store.StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
	})
	require.NoError(t, err)
	_, err = progressStore.SetStatus(context.Background(), store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationRunnerStringPtr("already done"),
	})
	require.NoError(t, err)

	backfillRunner := &fakeOpenClawEllieBackfillRunner{}
	runner := NewOpenClawMigrationRunner(db)
	runner.EllieBackfillRunner = backfillRunner

	result, runErr := runner.runEllieBackfillPhase(context.Background(), RunOpenClawMigrationInput{
		OrgID: orgID,
	})
	require.NoError(t, runErr)
	require.Nil(t, result)
	require.False(t, backfillRunner.called)
}

func TestMigrationRunnerResumeDoesNotAutoResetFailedPhase(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-skip-failed")
	progressStore := store.NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), store.StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
	})
	require.NoError(t, err)
	_, err = progressStore.SetStatus(context.Background(), store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
		Status:        store.MigrationProgressStatusFailed,
		Error:         migrationRunnerStringPtr("boom"),
	})
	require.NoError(t, err)

	backfillRunner := &fakeOpenClawEllieBackfillRunner{}
	runner := NewOpenClawMigrationRunner(db)
	runner.EllieBackfillRunner = backfillRunner

	result, runErr := runner.runEllieBackfillPhase(context.Background(), RunOpenClawMigrationInput{
		OrgID: orgID,
	})
	require.Error(t, runErr)
	require.ErrorContains(t, runErr, "reset status before rerun")
	require.Nil(t, result)
	require.False(t, backfillRunner.called)
}

func TestMigrationRunnerEntitySynthesisPhaseFailedStateGuard(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-migration-runner-entity-synthesis-failed")
	progressStore := store.NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), store.StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "entity_synthesis",
	})
	require.NoError(t, err)
	_, err = progressStore.SetStatus(context.Background(), store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "entity_synthesis",
		Status:        store.MigrationProgressStatusFailed,
		Error:         migrationRunnerStringPtr("boom"),
	})
	require.NoError(t, err)

	entityRunner := &fakeOpenClawEntitySynthesisRunner{}
	runner := NewOpenClawMigrationRunner(db)
	runner.EntitySynthesisRunner = entityRunner

	result, runErr := runner.runEntitySynthesisPhase(context.Background(), RunOpenClawMigrationInput{
		OrgID: orgID,
	})
	require.Error(t, runErr)
	require.ErrorContains(t, runErr, "reset status before rerun")
	require.Nil(t, result)
	require.False(t, entityRunner.called)
}
