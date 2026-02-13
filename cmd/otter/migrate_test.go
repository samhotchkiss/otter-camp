package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
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
