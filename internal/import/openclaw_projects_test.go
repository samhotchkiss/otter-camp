package importer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenClawProjectImportBuildsDeterministicCandidates(t *testing.T) {
	input := OpenClawProjectImportInput{
		Workspaces: []OpenClawWorkspaceSignal{
			{
				AgentID:      "main",
				WorkspaceDir: "/Users/sam/.openclaw/workspaces/main",
				RepoPath:     "/Users/sam/dev/otter-camp",
				ProjectHint:  "otter-camp",
				IssueHints: []string{
					"Wire onboarding bootstrap endpoint",
					"Wire onboarding bootstrap endpoint",
				},
			},
			{
				AgentID:      "2b",
				WorkspaceDir: "/Users/sam/.openclaw/workspaces/2b",
				RepoPath:     "/Users/sam/dev/otter-camp",
				IssueHints: []string{
					"Add OpenClaw importer tests",
				},
			},
		},
		Sessions: []OpenClawSessionSignal{
			{
				AgentID:    "main",
				Summary:    "project:otter-camp landing checklist",
				IssueHints: []string{"Add OpenClaw importer tests"},
			},
		},
		Memories: []OpenClawMemorySignal{
			{
				AgentID:     "main",
				Text:        "repo:otter-camp",
				ProjectHint: "",
			},
		},
	}

	candidates := InferOpenClawProjectCandidates(input)
	require.Len(t, candidates, 1)

	project := candidates[0]
	require.Equal(t, "otter-camp", project.Key)
	require.Equal(t, "Otter Camp", project.Name)
	require.Equal(t, filepath.Clean("/Users/sam/dev/otter-camp"), project.RepoPath)
	require.Equal(t, 9, project.Confidence) // workspace(3+3) + session(2) + memory(1)
	require.Equal(t, []string{"memory:main", "session:main", "workspace:2b", "workspace:main"}, project.Signals)
	require.Len(t, project.Issues, 2)
	require.Equal(t, "Add OpenClaw importer tests", project.Issues[0].Title)
	require.Equal(t, "Wire onboarding bootstrap endpoint", project.Issues[1].Title)
}

func TestOpenClawProjectImportFiltersAmbiguousLowSignalInputs(t *testing.T) {
	input := OpenClawProjectImportInput{
		Workspaces: []OpenClawWorkspaceSignal{
			{
				AgentID:      "main",
				WorkspaceDir: "/Users/sam/.openclaw/workspaces/main",
			},
		},
		Sessions: []OpenClawSessionSignal{
			{
				AgentID: "main",
				Summary: "working on things",
			},
		},
		Memories: []OpenClawMemorySignal{
			{
				AgentID:     "main",
				ProjectHint: "misc",
				Text:        "random housekeeping",
			},
		},
	}

	candidates := InferOpenClawProjectCandidates(input)
	require.Empty(t, candidates)
}

func TestOpenClawProjectImportHandlesEmptyInput(t *testing.T) {
	require.Empty(t, InferOpenClawProjectCandidates(OpenClawProjectImportInput{}))
	require.Empty(t, InferOpenClawProjectCandidates(OpenClawProjectImportInput{
		Workspaces: nil,
		Sessions:   nil,
		Memories:   nil,
	}))
}

func TestOpenClawProjectDiscoveryBuildsProjectsAndIssuesFromHistory(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)
	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-project-discovery-history")

	result, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, OpenClawProjectDiscoveryInput{
		OrgID: orgID,
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug: "main",
				Body:      "project:otter-camp issue: Add migration status endpoint",
				CreatedAt: time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC),
			},
			{
				AgentSlug: "main",
				Body:      "repo:otter-camp task: Harden migration retries",
				CreatedAt: time.Date(2026, 1, 11, 10, 0, 0, 0, time.UTC),
			},
		},
		ReferenceTime: time.Date(2026, 2, 12, 17, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.ProjectsCreated)
	require.Equal(t, 2, result.IssuesCreated)
	require.Equal(t, 1, result.ProcessedItems)

	var (
		projectID string
		status    string
	)
	err = db.QueryRow(
		`SELECT id::text, status
		   FROM projects
		  WHERE org_id = $1
		    AND LOWER(name) = LOWER('Otter Camp')`,
		orgID,
	).Scan(&projectID, &status)
	require.NoError(t, err)
	require.Equal(t, "active", status)

	var issueCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM project_issues
		  WHERE org_id = $1
		    AND project_id = $2`,
		orgID,
		projectID,
	).Scan(&issueCount)
	require.NoError(t, err)
	require.Equal(t, 2, issueCount)
}

func TestOpenClawProjectDiscoveryDedupesCrossConversationReferences(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)
	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-project-discovery-dedupe")

	input := OpenClawProjectDiscoveryInput{
		OrgID: orgID,
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug: "main",
				Body:      "project:itsalive issue: Fix webhook retry behavior",
				CreatedAt: time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC),
			},
			{
				AgentSlug: "lori",
				Body:      "repo:itsalive issue:   Fix webhook retry behavior   ",
				CreatedAt: time.Date(2026, 1, 16, 9, 0, 0, 0, time.UTC),
			},
		},
		ReferenceTime: time.Date(2026, 2, 12, 17, 0, 0, 0, time.UTC),
	}

	first, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, input)
	require.NoError(t, err)
	require.Equal(t, 1, first.ProjectsCreated)
	require.Equal(t, 1, first.IssuesCreated)

	second, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, input)
	require.NoError(t, err)
	require.Equal(t, 0, second.ProjectsCreated)
	require.Equal(t, 0, second.IssuesCreated)

	var projectCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM projects WHERE org_id = $1`, orgID).Scan(&projectCount)
	require.NoError(t, err)
	require.Equal(t, 1, projectCount)

	var issueCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM project_issues WHERE org_id = $1`, orgID).Scan(&issueCount)
	require.NoError(t, err)
	require.Equal(t, 1, issueCount)
}

func TestOpenClawProjectDiscoveryStatusInference(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)
	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-project-discovery-status")

	_, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, OpenClawProjectDiscoveryInput{
		OrgID: orgID,
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug: "main",
				Body:      "project:legacy-app issue: Capture remaining TODO items",
				CreatedAt: time.Date(2025, 10, 1, 12, 0, 0, 0, time.UTC),
			},
			{
				AgentSlug: "main",
				Body:      "project:phoenix issue: Final rollout completed and shipped",
				CreatedAt: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC),
			},
			{
				AgentSlug: "lori",
				Body:      "project:otter-camp issue: Continue migration work",
				CreatedAt: time.Date(2026, 2, 11, 11, 0, 0, 0, time.UTC),
			},
		},
		ReferenceTime: time.Date(2026, 2, 12, 17, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	rows, err := db.Query(`SELECT name, status FROM projects WHERE org_id = $1`, orgID)
	require.NoError(t, err)
	defer rows.Close()

	statusByName := map[string]string{}
	for rows.Next() {
		var name, status string
		require.NoError(t, rows.Scan(&name, &status))
		statusByName[name] = status
	}
	require.NoError(t, rows.Err())

	require.Equal(t, "archived", statusByName["Legacy App"])
	require.Equal(t, "completed", statusByName["Phoenix"])
	require.Equal(t, "active", statusByName["Otter Camp"])

	var phoenixIssueState string
	err = db.QueryRow(
		`SELECT state
		   FROM project_issues pi
		   JOIN projects p ON p.id = pi.project_id
		  WHERE p.org_id = $1
		    AND p.name = 'Phoenix'
		  LIMIT 1`,
		orgID,
	).Scan(&phoenixIssueState)
	require.NoError(t, err)
	require.Equal(t, "closed", phoenixIssueState)
}
