package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationDoesNotMutateSourceWorkspaceFiles(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-readonly-org")
	userID := createOpenClawImportTestUser(t, db, orgID, "readonly-user")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	sessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(sessionDir, "readonly.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
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

	guard, err := NewOpenClawSourceGuard(root)
	require.NoError(t, err)
	before, err := guard.CaptureSnapshot()
	require.NoError(t, err)

	runner := NewOpenClawMigrationRunner(db)
	_, err = runner.Run(context.Background(), RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		HistoryOnly:  true,
	})
	require.NoError(t, err)
	require.NoError(t, guard.VerifyUnchanged(before))
}

func TestOpenClawMigrationRejectsUnsafeWriteOperations(t *testing.T) {
	root := t.TempDir()

	guard, err := NewOpenClawSourceGuard(root)
	require.NoError(t, err)

	writePath := filepath.Join(root, "openclaw.json")
	err = guard.RejectWritePath(writePath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read-only")

	outsidePath := filepath.Clean(filepath.Join(root, "..", "outside.json"))
	err = guard.RejectWritePath(outsidePath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside openclaw root")
}

func TestOpenClawMigrationSummaryReport(t *testing.T) {
	report := BuildOpenClawMigrationSummaryReport(RunOpenClawMigrationResult{
		AgentImport: &OpenClawAgentImportResult{
			ImportedAgents: 6,
		},
		HistoryBackfill: &OpenClawHistoryBackfillResult{
			EventsProcessed:  42,
			MessagesInserted: 39,
		},
		EllieBackfill: &OpenClawEllieBackfillResult{
			ProcessedMessages: 39,
		},
		EntitySynthesis: &OpenClawEntitySynthesisResult{
			ProcessedEntities: 11,
		},
		Dedup: &OpenClawDedupResult{
			ProcessedClusters: 12,
		},
		TaxonomyPhase: &OpenClawTaxonomyClassificationResult{
			ProcessedMemories: 12,
		},
		ProjectDiscovery: &OpenClawProjectDiscoveryResult{
			ProcessedItems: 7,
		},
		Paused: true,
	})

	require.Equal(t, 6, report.AgentImportProcessed)
	require.Equal(t, 42, report.HistoryEventsProcessed)
	require.Equal(t, 39, report.HistoryMessagesInserted)
	require.Equal(t, 39, report.MemoryExtractionProcessed)
	require.Equal(t, 11, report.EntitySynthesisProcessed)
	require.Equal(t, 12, report.MemoryDedupProcessed)
	require.Equal(t, 12, report.TaxonomyClassificationProcessed)
	require.Equal(t, 7, report.ProjectDiscoveryProcessed)
	require.Equal(t, 0, report.FailedItems)
	require.Len(t, report.Warnings, 1)
	require.Equal(t, "migration paused before all phases completed", report.Warnings[0])

	// Summary generation is deterministic and should not depend on wall-clock time.
	reportAgain := BuildOpenClawMigrationSummaryReport(RunOpenClawMigrationResult{
		AgentImport:      &OpenClawAgentImportResult{ImportedAgents: 6},
		HistoryBackfill:  &OpenClawHistoryBackfillResult{EventsProcessed: 42, MessagesInserted: 39},
		EllieBackfill:    &OpenClawEllieBackfillResult{ProcessedMessages: 39},
		EntitySynthesis:  &OpenClawEntitySynthesisResult{ProcessedEntities: 11},
		Dedup:            &OpenClawDedupResult{ProcessedClusters: 12},
		TaxonomyPhase:    &OpenClawTaxonomyClassificationResult{ProcessedMemories: 12},
		ProjectDiscovery: &OpenClawProjectDiscoveryResult{ProcessedItems: 7},
		Paused:           true,
	})
	require.Equal(t, report, reportAgain)
}

func TestOpenClawMigrationSummaryReportIncludesEntityAndDedup(t *testing.T) {
	report := BuildOpenClawMigrationSummaryReport(RunOpenClawMigrationResult{
		EntitySynthesis: &OpenClawEntitySynthesisResult{
			ProcessedEntities: 11,
		},
		Dedup: &OpenClawDedupResult{
			ProcessedClusters: 8,
		},
	})

	require.Equal(t, 11, report.EntitySynthesisProcessed)
	require.Equal(t, 8, report.MemoryDedupProcessed)
}
