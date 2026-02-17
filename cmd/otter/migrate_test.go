package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/ottercli"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestMigrateFromOpenClawCommandParsesModesAndFlags(t *testing.T) {
	full, err := parseMigrateFromOpenClawOptions([]string{"--openclaw-dir", "/tmp/openclaw"})
	require.NoError(t, err)
	require.Equal(t, "/tmp/openclaw", full.OpenClawDir)
	require.False(t, full.AgentsOnly)
	require.False(t, full.HistoryOnly)
	require.False(t, full.DryRun)
	require.Nil(t, full.Since)

	agentsOnly, err := parseMigrateFromOpenClawOptions([]string{"--agents-only", "--dry-run", "--org", "org-1"})
	require.NoError(t, err)
	require.True(t, agentsOnly.AgentsOnly)
	require.False(t, agentsOnly.HistoryOnly)
	require.True(t, agentsOnly.DryRun)
	require.Equal(t, "org-1", agentsOnly.OrgID)

	historyOnly, err := parseMigrateFromOpenClawOptions([]string{"--history-only", "--since", "2026-01-01"})
	require.NoError(t, err)
	require.True(t, historyOnly.HistoryOnly)
	require.False(t, historyOnly.AgentsOnly)
	require.NotNil(t, historyOnly.Since)
	require.Equal(t, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), historyOnly.Since.UTC())

	_, err = parseMigrateFromOpenClawOptions([]string{"--agents-only", "--history-only"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

func TestParseMigrateTransportOptions(t *testing.T) {
	full, err := parseMigrateFromOpenClawOptions([]string{"--transport", "api"})
	require.NoError(t, err)
	require.Equal(t, migrateTransportAPI, full.Transport)

	orgOnly, err := parseMigrateOrgOnlyOptions("migrate status", []string{"--org", "org-1", "--transport", "db"})
	require.NoError(t, err)
	require.Equal(t, "org-1", orgOnly.OrgID)
	require.Equal(t, migrateTransportDB, orgOnly.Transport)

	autoDefault, err := parseMigrateOrgOnlyOptions("migrate pause", []string{"--org", "org-1"})
	require.NoError(t, err)
	require.Equal(t, migrateTransportAuto, autoDefault.Transport)

	_, err = parseMigrateFromOpenClawOptions([]string{"--transport", "smtp"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --transport")
}

func TestMigrateFromOpenClawAutoSelectsAPITransportForHostedBaseURL(t *testing.T) {
	resolved := resolveMigrateTransport(migrateTransportAuto, "https://swh.otter.camp")
	require.Equal(t, migrateTransportAPI, resolved)
}

func TestMigrateFromOpenClawAutoSelectsDBTransportForLocalhostBaseURL(t *testing.T) {
	tests := []string{
		"http://localhost:4200",
		"http://127.0.0.1:4200/api",
		"http://[::1]:4200",
		"http://otter.local:4200",
	}

	for _, baseURL := range tests {
		t.Run(baseURL, func(t *testing.T) {
			resolved := resolveMigrateTransport(migrateTransportAuto, baseURL)
			require.Equal(t, migrateTransportDB, resolved)
		})
	}
}

func TestResolveMigrateDatabaseURLPrefersEnvOverConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://env-user:env-pass@localhost:5432/envdb?sslmode=disable")

	originalLoadCfg := loadMigrateConfig
	t.Cleanup(func() {
		loadMigrateConfig = originalLoadCfg
	})
	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{
			DatabaseURL: "postgres://cfg-user:cfg-pass@localhost:5432/cfgdb?sslmode=disable",
		}, nil
	}

	got, err := resolveMigrateDatabaseURL()
	require.NoError(t, err)
	require.Equal(t, "postgres://env-user:env-pass@localhost:5432/envdb?sslmode=disable", got)
}

func TestResolveMigrateDatabaseURLFallsBackToConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	originalLoadCfg := loadMigrateConfig
	t.Cleanup(func() {
		loadMigrateConfig = originalLoadCfg
	})
	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{
			DatabaseURL: "postgres://cfg-user:cfg-pass@localhost:5432/cfgdb?sslmode=disable",
		}, nil
	}

	got, err := resolveMigrateDatabaseURL()
	require.NoError(t, err)
	require.Equal(t, "postgres://cfg-user:cfg-pass@localhost:5432/cfgdb?sslmode=disable", got)
}

func TestResolveMigrateDatabaseURLErrorsWhenUnset(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	originalLoadCfg := loadMigrateConfig
	t.Cleanup(func() {
		loadMigrateConfig = originalLoadCfg
	})
	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{}, nil
	}

	_, err := resolveMigrateDatabaseURL()
	require.Error(t, err)
	require.Contains(t, err.Error(), "DATABASE_URL is required")
}

func TestResolveMigrateDatabaseURLReturnsConfigLoadError(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	originalLoadCfg := loadMigrateConfig
	t.Cleanup(func() {
		loadMigrateConfig = originalLoadCfg
	})
	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{}, fmt.Errorf("permission denied")
	}

	_, err := resolveMigrateDatabaseURL()
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

func TestMigrateFromOpenClawDryRunOutput(t *testing.T) {
	originalDetect := detectMigrateOpenClawInstallation
	originalIdentities := importMigrateOpenClawAgentIdentities
	originalEvents := parseMigrateOpenClawSessionEvents
	t.Cleanup(func() {
		detectMigrateOpenClawInstallation = originalDetect
		importMigrateOpenClawAgentIdentities = originalIdentities
		parseMigrateOpenClawSessionEvents = originalEvents
	})

	detectMigrateOpenClawInstallation = func(opts importer.DetectOpenClawOptions) (*importer.OpenClawInstallation, error) {
		return &importer.OpenClawInstallation{
			RootDir: opts.HomeDir,
		}, nil
	}
	importMigrateOpenClawAgentIdentities = func(_ *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return []importer.ImportedAgentIdentity{
			{ID: "main"},
			{ID: "lori"},
		}, nil
	}
	parseMigrateOpenClawSessionEvents = func(_ *importer.OpenClawInstallation) ([]importer.OpenClawSessionEvent, error) {
		return []importer.OpenClawSessionEvent{
			{AgentSlug: "main", CreatedAt: time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC)},
			{AgentSlug: "lori", CreatedAt: time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC)},
			{AgentSlug: "main", CreatedAt: time.Date(2026, 1, 1, 10, 0, 3, 0, time.UTC)},
		}, nil
	}

	var out bytes.Buffer
	err := runMigrateFromOpenClaw(&out, migrateFromOpenClawOptions{
		OpenClawDir: "/tmp/openclaw",
		DryRun:      true,
	})
	require.NoError(t, err)

	rendered := out.String()
	require.Contains(t, rendered, "Dry run: otter migrate from-openclaw")
	require.Contains(t, rendered, "Mode: full")
	require.Contains(t, rendered, "Agents to import: 2")
	require.Contains(t, rendered, "Conversation events to import: 3")
	require.Contains(t, rendered, "Agent slugs: lori, main")
}

func TestMigrateFromOpenClawAPITransportNeverOpensDatabase(t *testing.T) {
	originalDetect := detectMigrateOpenClawInstallation
	originalIdentities := importMigrateOpenClawAgentIdentities
	originalEvents := parseMigrateOpenClawSessionEvents
	originalLoadCfg := loadMigrateConfig
	originalNewClient := newMigrateClient
	originalImportAgentsAPI := importMigrateOpenClawAgentsAPI
	originalWhoAmI := migrateWhoAmIUserID
	originalImportHistoryAPI := importMigrateOpenClawHistoryBatchAPI
	originalRunAPI := runMigrateOpenClawAPI
	originalStatusAPI := statusMigrateOpenClawAPI
	originalOpenDB := openMigrateDatabase
	t.Cleanup(func() {
		detectMigrateOpenClawInstallation = originalDetect
		importMigrateOpenClawAgentIdentities = originalIdentities
		parseMigrateOpenClawSessionEvents = originalEvents
		loadMigrateConfig = originalLoadCfg
		newMigrateClient = originalNewClient
		importMigrateOpenClawAgentsAPI = originalImportAgentsAPI
		migrateWhoAmIUserID = originalWhoAmI
		importMigrateOpenClawHistoryBatchAPI = originalImportHistoryAPI
		runMigrateOpenClawAPI = originalRunAPI
		statusMigrateOpenClawAPI = originalStatusAPI
		openMigrateDatabase = originalOpenDB
	})

	detectMigrateOpenClawInstallation = func(opts importer.DetectOpenClawOptions) (*importer.OpenClawInstallation, error) {
		return &importer.OpenClawInstallation{RootDir: opts.HomeDir}, nil
	}
	importMigrateOpenClawAgentIdentities = func(_ *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return []importer.ImportedAgentIdentity{{ID: "main", Name: "Frank"}}, nil
	}
	parseMigrateOpenClawSessionEvents = func(_ *importer.OpenClawInstallation) ([]importer.OpenClawSessionEvent, error) {
		return []importer.OpenClawSessionEvent{
			{AgentSlug: "main", Role: "assistant", Body: "hello", CreatedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		}, nil
	}
	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{
			APIBaseURL: "https://swh.otter.camp",
			Token:      "token-1",
			DefaultOrg: "org-1",
		}, nil
	}
	newMigrateClient = func(cfg ottercli.Config, orgOverride string) (*ottercli.Client, error) {
		return &ottercli.Client{BaseURL: cfg.APIBaseURL, Token: cfg.Token, OrgID: orgOverride, HTTP: &http.Client{}}, nil
	}
	importMigrateOpenClawAgentsAPI = func(
		_ *ottercli.Client,
		_ ottercli.OpenClawMigrationImportAgentsInput,
	) (ottercli.OpenClawMigrationImportAgentsResult, error) {
		return ottercli.OpenClawMigrationImportAgentsResult{Processed: 1}, nil
	}
	migrateWhoAmIUserID = func(_ *ottercli.Client) (string, error) {
		return "00000000-0000-0000-0000-000000000111", nil
	}
	importMigrateOpenClawHistoryBatchAPI = func(
		_ *ottercli.Client,
		_ ottercli.OpenClawMigrationImportHistoryBatchInput,
	) (ottercli.OpenClawMigrationImportHistoryBatchResult, error) {
		return ottercli.OpenClawMigrationImportHistoryBatchResult{
			EventsReceived:   1,
			EventsProcessed:  1,
			MessagesInserted: 1,
		}, nil
	}
	runMigrateOpenClawAPI = func(
		_ *ottercli.Client,
		_ ottercli.OpenClawMigrationRunRequest,
	) (ottercli.OpenClawMigrationRunResponse, error) {
		return ottercli.OpenClawMigrationRunResponse{Accepted: true}, nil
	}
	statusMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationStatus, error) {
		return ottercli.OpenClawMigrationStatus{Active: false}, nil
	}

	openDBCalled := false
	openMigrateDatabase = func() (*sql.DB, error) {
		openDBCalled = true
		return nil, nil
	}

	var out bytes.Buffer
	err := runMigrateFromOpenClaw(&out, migrateFromOpenClawOptions{
		OpenClawDir: "/tmp/openclaw",
		OrgID:       "org-1",
		Transport:   migrateTransportAPI,
	})
	require.NoError(t, err)
	require.False(t, openDBCalled, "api transport must not open DATABASE_URL connection")
}

func TestMigrateFromOpenClawAPITransportImportsAndRunsMigration(t *testing.T) {
	originalDetect := detectMigrateOpenClawInstallation
	originalIdentities := importMigrateOpenClawAgentIdentities
	originalEvents := parseMigrateOpenClawSessionEvents
	originalLoadCfg := loadMigrateConfig
	originalNewClient := newMigrateClient
	originalImportAgentsAPI := importMigrateOpenClawAgentsAPI
	originalWhoAmI := migrateWhoAmIUserID
	originalImportHistoryAPI := importMigrateOpenClawHistoryBatchAPI
	originalRunAPI := runMigrateOpenClawAPI
	originalStatusAPI := statusMigrateOpenClawAPI
	originalOpenDB := openMigrateDatabase
	originalBatchSize := migrateOpenClawHistoryBatchSize
	t.Cleanup(func() {
		detectMigrateOpenClawInstallation = originalDetect
		importMigrateOpenClawAgentIdentities = originalIdentities
		parseMigrateOpenClawSessionEvents = originalEvents
		loadMigrateConfig = originalLoadCfg
		newMigrateClient = originalNewClient
		importMigrateOpenClawAgentsAPI = originalImportAgentsAPI
		migrateWhoAmIUserID = originalWhoAmI
		importMigrateOpenClawHistoryBatchAPI = originalImportHistoryAPI
		runMigrateOpenClawAPI = originalRunAPI
		statusMigrateOpenClawAPI = originalStatusAPI
		openMigrateDatabase = originalOpenDB
		migrateOpenClawHistoryBatchSize = originalBatchSize
	})

	detectMigrateOpenClawInstallation = func(opts importer.DetectOpenClawOptions) (*importer.OpenClawInstallation, error) {
		return &importer.OpenClawInstallation{RootDir: opts.HomeDir}, nil
	}
	importMigrateOpenClawAgentIdentities = func(_ *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return []importer.ImportedAgentIdentity{{ID: "main"}, {ID: "lori"}}, nil
	}
	parseMigrateOpenClawSessionEvents = func(_ *importer.OpenClawInstallation) ([]importer.OpenClawSessionEvent, error) {
		return []importer.OpenClawSessionEvent{
			{AgentSlug: "main", Role: "assistant", Body: "a", CreatedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
			{AgentSlug: "main", Role: "assistant", Body: "b", CreatedAt: time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC)},
			{AgentSlug: "lori", Role: "assistant", Body: "c", CreatedAt: time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC)},
		}, nil
	}
	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{
			APIBaseURL: "https://swh.otter.camp",
			Token:      "token-1",
		}, nil
	}
	newMigrateClient = func(cfg ottercli.Config, orgOverride string) (*ottercli.Client, error) {
		return &ottercli.Client{BaseURL: cfg.APIBaseURL, Token: cfg.Token, OrgID: orgOverride, HTTP: &http.Client{}}, nil
	}

	agentsImported := 0
	importMigrateOpenClawAgentsAPI = func(
		_ *ottercli.Client,
		input ottercli.OpenClawMigrationImportAgentsInput,
	) (ottercli.OpenClawMigrationImportAgentsResult, error) {
		agentsImported = len(input.Identities)
		return ottercli.OpenClawMigrationImportAgentsResult{Processed: len(input.Identities)}, nil
	}
	migrateWhoAmIUserID = func(_ *ottercli.Client) (string, error) {
		return "00000000-0000-0000-0000-000000000111", nil
	}

	historyCalls := 0
	historyBatchIndexes := make([]int, 0, 2)
	importMigrateOpenClawHistoryBatchAPI = func(
		_ *ottercli.Client,
		input ottercli.OpenClawMigrationImportHistoryBatchInput,
	) (ottercli.OpenClawMigrationImportHistoryBatchResult, error) {
		historyCalls++
		historyBatchIndexes = append(historyBatchIndexes, input.Batch.Index)
		return ottercli.OpenClawMigrationImportHistoryBatchResult{
			EventsReceived:   len(input.Events),
			EventsProcessed:  len(input.Events),
			MessagesInserted: len(input.Events),
		}, nil
	}

	runCalled := false
	runStartPhase := ""
	runMigrateOpenClawAPI = func(
		_ *ottercli.Client,
		input ottercli.OpenClawMigrationRunRequest,
	) (ottercli.OpenClawMigrationRunResponse, error) {
		runCalled = true
		runStartPhase = input.StartPhase
		return ottercli.OpenClawMigrationRunResponse{Accepted: true}, nil
	}
	statusMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationStatus, error) {
		return ottercli.OpenClawMigrationStatus{Active: false}, nil
	}

	openMigrateDatabase = func() (*sql.DB, error) {
		return nil, errors.New("db should not be opened in api mode")
	}
	migrateOpenClawHistoryBatchSize = 2

	var out bytes.Buffer
	err := runMigrateFromOpenClaw(&out, migrateFromOpenClawOptions{
		OpenClawDir: "/tmp/openclaw",
		OrgID:       "org-1",
		Transport:   migrateTransportAPI,
	})
	require.NoError(t, err)
	require.Equal(t, 2, agentsImported)
	require.Equal(t, 2, historyCalls)
	require.Equal(t, []int{1, 2}, historyBatchIndexes)
	require.True(t, runCalled)
	require.Equal(t, "memory_extraction", runStartPhase)
	require.Contains(t, out.String(), "OpenClaw migration summary:")
}

func TestMigrateFromOpenClawDryRunSkipsWritesForAllTransports(t *testing.T) {
	originalDetect := detectMigrateOpenClawInstallation
	originalIdentities := importMigrateOpenClawAgentIdentities
	originalEvents := parseMigrateOpenClawSessionEvents
	originalOpenDB := openMigrateDatabase
	originalRunRunner := runMigrateRunner
	originalImportAgentsAPI := importMigrateOpenClawAgentsAPI
	originalImportHistoryAPI := importMigrateOpenClawHistoryBatchAPI
	originalRunAPI := runMigrateOpenClawAPI
	t.Cleanup(func() {
		detectMigrateOpenClawInstallation = originalDetect
		importMigrateOpenClawAgentIdentities = originalIdentities
		parseMigrateOpenClawSessionEvents = originalEvents
		openMigrateDatabase = originalOpenDB
		runMigrateRunner = originalRunRunner
		importMigrateOpenClawAgentsAPI = originalImportAgentsAPI
		importMigrateOpenClawHistoryBatchAPI = originalImportHistoryAPI
		runMigrateOpenClawAPI = originalRunAPI
	})

	detectMigrateOpenClawInstallation = func(opts importer.DetectOpenClawOptions) (*importer.OpenClawInstallation, error) {
		return &importer.OpenClawInstallation{RootDir: opts.HomeDir}, nil
	}
	importMigrateOpenClawAgentIdentities = func(_ *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return []importer.ImportedAgentIdentity{{ID: "main"}}, nil
	}
	parseMigrateOpenClawSessionEvents = func(_ *importer.OpenClawInstallation) ([]importer.OpenClawSessionEvent, error) {
		return []importer.OpenClawSessionEvent{
			{AgentSlug: "main", Role: "assistant", Body: "hello", CreatedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		}, nil
	}

	dbWrites := 0
	openMigrateDatabase = func() (*sql.DB, error) {
		dbWrites++
		return nil, nil
	}
	runMigrateRunner = func(
		_ context.Context,
		_ *importer.OpenClawMigrationRunner,
		_ importer.RunOpenClawMigrationInput,
	) (importer.RunOpenClawMigrationResult, error) {
		dbWrites++
		return importer.RunOpenClawMigrationResult{}, nil
	}
	apiWrites := 0
	importMigrateOpenClawAgentsAPI = func(
		_ *ottercli.Client,
		_ ottercli.OpenClawMigrationImportAgentsInput,
	) (ottercli.OpenClawMigrationImportAgentsResult, error) {
		apiWrites++
		return ottercli.OpenClawMigrationImportAgentsResult{}, nil
	}
	importMigrateOpenClawHistoryBatchAPI = func(
		_ *ottercli.Client,
		_ ottercli.OpenClawMigrationImportHistoryBatchInput,
	) (ottercli.OpenClawMigrationImportHistoryBatchResult, error) {
		apiWrites++
		return ottercli.OpenClawMigrationImportHistoryBatchResult{}, nil
	}
	runMigrateOpenClawAPI = func(
		_ *ottercli.Client,
		_ ottercli.OpenClawMigrationRunRequest,
	) (ottercli.OpenClawMigrationRunResponse, error) {
		apiWrites++
		return ottercli.OpenClawMigrationRunResponse{}, nil
	}

	transports := []migrateTransport{migrateTransportAuto, migrateTransportAPI, migrateTransportDB}
	for _, transport := range transports {
		t.Run(string(transport), func(t *testing.T) {
			var out bytes.Buffer
			err := runMigrateFromOpenClaw(&out, migrateFromOpenClawOptions{
				OpenClawDir: "/tmp/openclaw",
				DryRun:      true,
				OrgID:       "org-1",
				Transport:   transport,
			})
			require.NoError(t, err)
			require.Contains(t, out.String(), "Dry run: otter migrate from-openclaw")
		})
	}

	require.Equal(t, 0, dbWrites)
	require.Equal(t, 0, apiWrites)
}

func TestMigrateFromOpenClawSinceFilter(t *testing.T) {
	originalDetect := detectMigrateOpenClawInstallation
	originalIdentities := importMigrateOpenClawAgentIdentities
	originalEvents := parseMigrateOpenClawSessionEvents
	t.Cleanup(func() {
		detectMigrateOpenClawInstallation = originalDetect
		importMigrateOpenClawAgentIdentities = originalIdentities
		parseMigrateOpenClawSessionEvents = originalEvents
	})

	detectMigrateOpenClawInstallation = func(opts importer.DetectOpenClawOptions) (*importer.OpenClawInstallation, error) {
		return &importer.OpenClawInstallation{
			RootDir: opts.HomeDir,
		}, nil
	}
	importMigrateOpenClawAgentIdentities = func(_ *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return []importer.ImportedAgentIdentity{
			{ID: "main"},
		}, nil
	}
	parseMigrateOpenClawSessionEvents = func(_ *importer.OpenClawInstallation) ([]importer.OpenClawSessionEvent, error) {
		return []importer.OpenClawSessionEvent{
			{AgentSlug: "main", CreatedAt: time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)},
			{AgentSlug: "main", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{AgentSlug: "main", CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		}, nil
	}

	since, err := parseMigrateSince("2026-01-01")
	require.NoError(t, err)
	require.NotNil(t, since)

	var out bytes.Buffer
	err = runMigrateFromOpenClaw(&out, migrateFromOpenClawOptions{
		OpenClawDir: "/tmp/openclaw",
		DryRun:      true,
		Since:       since,
	})
	require.NoError(t, err)

	rendered := out.String()
	require.Contains(t, rendered, "Since: 2026-01-01T00:00:00Z")
	require.Contains(t, rendered, "Conversation events to import: 2")
	require.True(t, strings.Contains(rendered, "Mode: full"))
}

func TestMigrateStatusPauseResumeCommands(t *testing.T) {
	originalOpenDB := openMigrateDatabase
	originalList := listMigrateProgressByOrg
	originalUpdate := updateMigrateProgressByOrgStatus
	originalQueryTimeout := migrateQueryTimeout
	t.Cleanup(func() {
		openMigrateDatabase = originalOpenDB
		listMigrateProgressByOrg = originalList
		updateMigrateProgressByOrgStatus = originalUpdate
		migrateQueryTimeout = originalQueryTimeout
	})

	openMigrateDatabase = func() (*sql.DB, error) {
		return nil, nil
	}
	statusContextHasDeadline := false
	listMigrateProgressByOrg = func(ctx context.Context, _ *sql.DB, _ string) ([]store.MigrationProgress, error) {
		_, statusContextHasDeadline = ctx.Deadline()
		total := 10
		return []store.MigrationProgress{
			{
				MigrationType:  "agent_import",
				Status:         store.MigrationProgressStatusCompleted,
				TotalItems:     &total,
				ProcessedItems: 10,
				CurrentLabel:   "agent import complete",
			},
			{
				MigrationType:  "history_backfill",
				Status:         store.MigrationProgressStatusRunning,
				TotalItems:     &total,
				ProcessedItems: 3,
				CurrentLabel:   "processed 3/10 events",
			},
		}, nil
	}

	updates := make([][2]store.MigrationProgressStatus, 0, 2)
	updateContextsWithDeadline := 0
	updateMigrateProgressByOrgStatus = func(
		ctx context.Context,
		_ *sql.DB,
		_ string,
		fromStatus store.MigrationProgressStatus,
		toStatus store.MigrationProgressStatus,
	) (int, error) {
		if _, ok := ctx.Deadline(); ok {
			updateContextsWithDeadline++
		}
		updates = append(updates, [2]store.MigrationProgressStatus{fromStatus, toStatus})
		return 1, nil
	}
	migrateQueryTimeout = 500 * time.Millisecond

	var statusOut bytes.Buffer
	require.NoError(t, runMigrateStatus(&statusOut, "org-1"))
	require.Contains(t, statusOut.String(), "Migration Status:")
	require.Contains(t, statusOut.String(), "agent_import: completed (10 / 10)")
	require.Contains(t, statusOut.String(), "history_backfill: running (3 / 10)")

	var pauseOut bytes.Buffer
	require.NoError(t, runMigratePause(&pauseOut, "org-1"))
	require.Contains(t, pauseOut.String(), "Paused 1 running migration phase(s).")

	var resumeOut bytes.Buffer
	require.NoError(t, runMigrateResume(&resumeOut, "org-1"))
	require.Contains(t, resumeOut.String(), "Resumed 1 paused migration phase(s).")

	require.Len(t, updates, 2)
	require.Equal(t, store.MigrationProgressStatusRunning, updates[0][0])
	require.Equal(t, store.MigrationProgressStatusPaused, updates[0][1])
	require.Equal(t, store.MigrationProgressStatusPaused, updates[1][0])
	require.Equal(t, store.MigrationProgressStatusRunning, updates[1][1])
	require.True(t, statusContextHasDeadline)
	require.Equal(t, 2, updateContextsWithDeadline)
}

func TestMigrateStatusPauseResumeUseAPIEndpointsInAPITransport(t *testing.T) {
	originalLoadCfg := loadMigrateConfig
	originalNewClient := newMigrateClient
	originalStatusAPI := statusMigrateOpenClawAPI
	originalPauseAPI := pauseMigrateOpenClawAPI
	originalResumeAPI := resumeMigrateOpenClawAPI
	originalOpenDB := openMigrateDatabase
	t.Cleanup(func() {
		loadMigrateConfig = originalLoadCfg
		newMigrateClient = originalNewClient
		statusMigrateOpenClawAPI = originalStatusAPI
		pauseMigrateOpenClawAPI = originalPauseAPI
		resumeMigrateOpenClawAPI = originalResumeAPI
		openMigrateDatabase = originalOpenDB
	})

	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{
			APIBaseURL: "https://swh.otter.camp",
			Token:      "token-1",
			DefaultOrg: "org-1",
		}, nil
	}
	newMigrateClient = func(cfg ottercli.Config, orgOverride string) (*ottercli.Client, error) {
		return &ottercli.Client{BaseURL: cfg.APIBaseURL, Token: cfg.Token, OrgID: orgOverride, HTTP: &http.Client{}}, nil
	}
	statusMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationStatus, error) {
		total := 10
		return ottercli.OpenClawMigrationStatus{
			Active: true,
			Phases: []ottercli.OpenClawMigrationPhaseStatus{
				{
					MigrationType:  "history_backfill",
					Status:         "running",
					TotalItems:     &total,
					ProcessedItems: 3,
					CurrentLabel:   "processed 3/10 events",
				},
			},
		}, nil
	}
	pauseMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationMutationResponse, error) {
		return ottercli.OpenClawMigrationMutationResponse{
			Status:        "paused",
			UpdatedPhases: 2,
		}, nil
	}
	resumeMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationMutationResponse, error) {
		return ottercli.OpenClawMigrationMutationResponse{
			Status:        "running",
			UpdatedPhases: 1,
		}, nil
	}

	dbOpened := false
	openMigrateDatabase = func() (*sql.DB, error) {
		dbOpened = true
		return nil, nil
	}

	var statusOut bytes.Buffer
	require.NoError(t, runMigrateStatusWithTransport(&statusOut, "org-1", migrateTransportAPI))
	require.Contains(t, statusOut.String(), "Migration Status:")
	require.Contains(t, statusOut.String(), "history_backfill: running (3 / 10)")

	var pauseOut bytes.Buffer
	require.NoError(t, runMigratePauseWithTransport(&pauseOut, "org-1", migrateTransportAPI))
	require.Contains(t, pauseOut.String(), "Paused 2 running migration phase(s).")

	var resumeOut bytes.Buffer
	require.NoError(t, runMigrateResumeWithTransport(&resumeOut, "org-1", migrateTransportAPI))
	require.Contains(t, resumeOut.String(), "Resumed 1 paused migration phase(s).")

	require.False(t, dbOpened, "api transport should not open database")
}

func TestMigrateStatusPauseResumeUseDBInDBTransport(t *testing.T) {
	originalOpenDB := openMigrateDatabase
	originalList := listMigrateProgressByOrg
	originalUpdate := updateMigrateProgressByOrgStatus
	originalStatusAPI := statusMigrateOpenClawAPI
	originalPauseAPI := pauseMigrateOpenClawAPI
	originalResumeAPI := resumeMigrateOpenClawAPI
	t.Cleanup(func() {
		openMigrateDatabase = originalOpenDB
		listMigrateProgressByOrg = originalList
		updateMigrateProgressByOrgStatus = originalUpdate
		statusMigrateOpenClawAPI = originalStatusAPI
		pauseMigrateOpenClawAPI = originalPauseAPI
		resumeMigrateOpenClawAPI = originalResumeAPI
	})

	openMigrateDatabase = func() (*sql.DB, error) {
		return nil, nil
	}
	total := 4
	listMigrateProgressByOrg = func(ctx context.Context, _ *sql.DB, _ string) ([]store.MigrationProgress, error) {
		return []store.MigrationProgress{
			{
				MigrationType:  "agent_import",
				Status:         store.MigrationProgressStatusCompleted,
				TotalItems:     &total,
				ProcessedItems: 4,
			},
		}, nil
	}
	updateMigrateProgressByOrgStatus = func(
		_ context.Context,
		_ *sql.DB,
		_ string,
		fromStatus store.MigrationProgressStatus,
		toStatus store.MigrationProgressStatus,
	) (int, error) {
		if fromStatus == store.MigrationProgressStatusRunning && toStatus == store.MigrationProgressStatusPaused {
			return 1, nil
		}
		if fromStatus == store.MigrationProgressStatusPaused && toStatus == store.MigrationProgressStatusRunning {
			return 2, nil
		}
		return 0, nil
	}

	apiCalls := 0
	statusMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationStatus, error) {
		apiCalls++
		return ottercli.OpenClawMigrationStatus{}, nil
	}
	pauseMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationMutationResponse, error) {
		apiCalls++
		return ottercli.OpenClawMigrationMutationResponse{}, nil
	}
	resumeMigrateOpenClawAPI = func(_ *ottercli.Client) (ottercli.OpenClawMigrationMutationResponse, error) {
		apiCalls++
		return ottercli.OpenClawMigrationMutationResponse{}, nil
	}

	var statusOut bytes.Buffer
	require.NoError(t, runMigrateStatusWithTransport(&statusOut, "org-1", migrateTransportDB))
	require.Contains(t, statusOut.String(), "agent_import: completed (4 / 4)")

	var pauseOut bytes.Buffer
	require.NoError(t, runMigratePauseWithTransport(&pauseOut, "org-1", migrateTransportDB))
	require.Contains(t, pauseOut.String(), "Paused 1 running migration phase(s).")

	var resumeOut bytes.Buffer
	require.NoError(t, runMigrateResumeWithTransport(&resumeOut, "org-1", migrateTransportDB))
	require.Contains(t, resumeOut.String(), "Resumed 2 paused migration phase(s).")

	require.Equal(t, 0, apiCalls, "db transport should not call api endpoints")
}

func TestMigrateFromOpenClawResetCallsAPIEndpoint(t *testing.T) {
	originalLoadCfg := loadMigrateConfig
	originalNewClient := newMigrateClient
	originalResetAPI := resetMigrateOpenClawAPI
	originalOpenDB := openMigrateDatabase
	t.Cleanup(func() {
		loadMigrateConfig = originalLoadCfg
		newMigrateClient = originalNewClient
		resetMigrateOpenClawAPI = originalResetAPI
		openMigrateDatabase = originalOpenDB
	})

	loadMigrateConfig = func() (ottercli.Config, error) {
		return ottercli.Config{
			APIBaseURL: "https://swh.otter.camp",
			Token:      "token-1",
			DefaultOrg: "org-1",
		}, nil
	}
	newMigrateClient = func(cfg ottercli.Config, orgOverride string) (*ottercli.Client, error) {
		return &ottercli.Client{BaseURL: cfg.APIBaseURL, Token: cfg.Token, OrgID: orgOverride, HTTP: &http.Client{}}, nil
	}

	resetCalled := false
	resetConfirm := ""
	resetMigrateOpenClawAPI = func(
		_ *ottercli.Client,
		input ottercli.OpenClawMigrationResetRequest,
	) (ottercli.OpenClawMigrationResetResponse, error) {
		resetCalled = true
		resetConfirm = input.Confirm
		return ottercli.OpenClawMigrationResetResponse{
			Status:              "reset",
			PausedPhases:        2,
			ProgressRowsDeleted: 5,
			Deleted: map[string]int{
				"chat_messages": 3,
				"rooms":         1,
			},
			TotalDeleted: 4,
		}, nil
	}

	dbOpened := false
	openMigrateDatabase = func() (*sql.DB, error) {
		dbOpened = true
		return nil, nil
	}

	var out bytes.Buffer
	err := runMigrateFromOpenClawReset(&out, migrateFromOpenClawResetOptions{
		Confirm:   true,
		OrgID:     "org-1",
		Transport: migrateTransportAPI,
	})
	require.NoError(t, err)
	require.True(t, resetCalled)
	require.Equal(t, migrateOpenClawResetConfirmToken, resetConfirm)
	require.False(t, dbOpened, "reset api transport must not open database")

	rendered := out.String()
	require.Contains(t, rendered, "OpenClaw migration reset complete.")
	require.Contains(t, rendered, "paused_phases=2")
	require.Contains(t, rendered, "progress_rows_deleted=5")
	require.Contains(t, rendered, "deleted.chat_messages=3")
	require.Contains(t, rendered, "total_deleted=4")
}

func TestMigrateFromOpenClawResetRequiresConfirmFlag(t *testing.T) {
	originalResetAPI := resetMigrateOpenClawAPI
	t.Cleanup(func() {
		resetMigrateOpenClawAPI = originalResetAPI
	})

	resetCalled := false
	resetMigrateOpenClawAPI = func(
		_ *ottercli.Client,
		_ ottercli.OpenClawMigrationResetRequest,
	) (ottercli.OpenClawMigrationResetResponse, error) {
		resetCalled = true
		return ottercli.OpenClawMigrationResetResponse{}, nil
	}

	var out bytes.Buffer
	err := runMigrateFromOpenClawReset(&out, migrateFromOpenClawResetOptions{
		Confirm:   false,
		OrgID:     "org-1",
		Transport: migrateTransportAPI,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--confirm is required")
	require.False(t, resetCalled)
}

func TestMigrateFromOpenClawResetRejectsDBTransport(t *testing.T) {
	var out bytes.Buffer
	err := runMigrateFromOpenClawReset(&out, migrateFromOpenClawResetOptions{
		Confirm:   true,
		OrgID:     "org-1",
		Transport: migrateTransportDB,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "only available with api transport")
}

func TestMigrateFromOpenClawRunUsesExecutionTimeoutContext(t *testing.T) {
	originalDetect := detectMigrateOpenClawInstallation
	originalIdentities := importMigrateOpenClawAgentIdentities
	originalNewRunner := newOpenClawMigrationRunner
	originalOpenDB := openMigrateDatabase
	originalRunRunner := runMigrateRunner
	originalExecutionTimeout := migrateExecutionTimeout
	t.Cleanup(func() {
		detectMigrateOpenClawInstallation = originalDetect
		importMigrateOpenClawAgentIdentities = originalIdentities
		newOpenClawMigrationRunner = originalNewRunner
		openMigrateDatabase = originalOpenDB
		runMigrateRunner = originalRunRunner
		migrateExecutionTimeout = originalExecutionTimeout
	})

	detectMigrateOpenClawInstallation = func(opts importer.DetectOpenClawOptions) (*importer.OpenClawInstallation, error) {
		return &importer.OpenClawInstallation{RootDir: opts.HomeDir}, nil
	}
	importMigrateOpenClawAgentIdentities = func(_ *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return []importer.ImportedAgentIdentity{{ID: "main"}}, nil
	}
	openMigrateDatabase = func() (*sql.DB, error) {
		return nil, nil
	}
	newOpenClawMigrationRunner = func(_ *sql.DB) *importer.OpenClawMigrationRunner {
		return &importer.OpenClawMigrationRunner{}
	}

	runnerContextHasDeadline := false
	runMigrateRunner = func(
		ctx context.Context,
		_ *importer.OpenClawMigrationRunner,
		_ importer.RunOpenClawMigrationInput,
	) (importer.RunOpenClawMigrationResult, error) {
		_, runnerContextHasDeadline = ctx.Deadline()
		<-ctx.Done()
		return importer.RunOpenClawMigrationResult{}, ctx.Err()
	}

	migrateExecutionTimeout = 5 * time.Millisecond

	var out bytes.Buffer
	err := runMigrateFromOpenClaw(&out, migrateFromOpenClawOptions{
		OpenClawDir: "/tmp/openclaw",
		AgentsOnly:  true,
		OrgID:       "org-1",
		Transport:   migrateTransportDB,
	})
	require.Error(t, err)
	require.True(t, runnerContextHasDeadline)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}

func TestMigrateFromOpenClawRendersHistoryProgressWithETA(t *testing.T) {
	originalDetect := detectMigrateOpenClawInstallation
	originalIdentities := importMigrateOpenClawAgentIdentities
	originalOpenDB := openMigrateDatabase
	originalResolveUser := resolveMigrateBackfillUserIDFn
	originalNewRunner := newOpenClawMigrationRunner
	originalRunRunner := runMigrateRunner
	t.Cleanup(func() {
		detectMigrateOpenClawInstallation = originalDetect
		importMigrateOpenClawAgentIdentities = originalIdentities
		openMigrateDatabase = originalOpenDB
		resolveMigrateBackfillUserIDFn = originalResolveUser
		newOpenClawMigrationRunner = originalNewRunner
		runMigrateRunner = originalRunRunner
	})

	detectMigrateOpenClawInstallation = func(opts importer.DetectOpenClawOptions) (*importer.OpenClawInstallation, error) {
		return &importer.OpenClawInstallation{RootDir: opts.HomeDir}, nil
	}
	importMigrateOpenClawAgentIdentities = func(_ *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return []importer.ImportedAgentIdentity{{ID: "main"}}, nil
	}
	openMigrateDatabase = func() (*sql.DB, error) {
		return nil, nil
	}
	resolveMigrateBackfillUserIDFn = func(_ context.Context, _ *sql.DB, _ string) (string, error) {
		return "user-1", nil
	}
	newOpenClawMigrationRunner = func(_ *sql.DB) *importer.OpenClawMigrationRunner {
		return &importer.OpenClawMigrationRunner{}
	}
	runMigrateRunner = func(
		_ context.Context,
		runner *importer.OpenClawMigrationRunner,
		_ importer.RunOpenClawMigrationInput,
	) (importer.RunOpenClawMigrationResult, error) {
		if runner.OnHistoryCheckpoint != nil {
			runner.OnHistoryCheckpoint(10, 100)
			runner.OnHistoryCheckpoint(100, 100)
		}
		return importer.RunOpenClawMigrationResult{
			Summary: importer.OpenClawMigrationSummaryReport{},
		}, nil
	}

	openClawDir := t.TempDir()
	var out bytes.Buffer
	err := runMigrateFromOpenClaw(&out, migrateFromOpenClawOptions{
		OpenClawDir: openClawDir,
		OrgID:       "org-1",
		Transport:   migrateTransportDB,
	})
	require.NoError(t, err)
	rendered := out.String()
	require.Contains(t, rendered, "History progress: 10/100 (10.0%) ETA")
	require.Contains(t, rendered, "History progress: 100/100 (100.0%) ETA 0s")
}

func TestRenderMigrateOpenClawSummaryIncludesSkippedBreakdown(t *testing.T) {
	var out bytes.Buffer
	renderMigrateOpenClawSummary(&out, importer.OpenClawMigrationSummaryReport{
		HistoryEventsProcessed: 100,
		HistoryEventsSkipped:   3259,
		HistorySkippedUnknownAgentCounts: map[string]int{
			"codex":       3247,
			"sandbox-bot": 12,
		},
	})

	rendered := out.String()
	require.Contains(t, rendered, "history_backfill.events_processed=100")
	require.Contains(t, rendered, "history_backfill.events_skipped=3259")
	require.Contains(t, rendered, "history_backfill.skipped_unknown_agent.codex=3247")
	require.Contains(t, rendered, "history_backfill.skipped_unknown_agent.sandbox-bot=12")
}
