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
	require.Equal(t, "queued", project.Issues[0].Status)
	require.Equal(t, "Wire onboarding bootstrap endpoint", project.Issues[1].Title)
	require.Equal(t, "queued", project.Issues[1].Status)
}

func TestOpenClawProjectImportPrefersTerminalIssueStatus(t *testing.T) {
	input := OpenClawProjectImportInput{
		Sessions: []OpenClawSessionSignal{
			{
				AgentID:     "main",
				ProjectHint: "otter-camp",
				IssueSignals: []OpenClawIssueSignal{
					{Title: "Migrate ingestion to bridge", Status: "in_progress"},
				},
				OccurredAt: time.Date(2026, 2, 10, 10, 0, 0, 0, time.UTC),
			},
			{
				AgentID:     "main",
				ProjectHint: "otter-camp",
				IssueSignals: []OpenClawIssueSignal{
					{Title: "Migrate ingestion to bridge", Status: "completed"},
				},
				OccurredAt: time.Date(2026, 2, 11, 10, 0, 0, 0, time.UTC),
			},
		},
	}

	candidates := InferOpenClawProjectCandidates(input)
	require.Len(t, candidates, 1)
	require.Len(t, candidates[0].Issues, 1)
	require.Equal(t, "Migrate ingestion to bridge", candidates[0].Issues[0].Title)
	require.Equal(t, "done", candidates[0].Issues[0].Status)
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

func TestBuildOpenClawProjectDescriptionUsesIssueFocusAndSanitizedEvidence(t *testing.T) {
	lastDiscussed := time.Date(2026, 2, 16, 15, 28, 23, 0, time.UTC)
	description := BuildOpenClawProjectDescription(OpenClawProjectCandidate{
		Name:            "Technonymous",
		RepoPath:        "/Users/sam/dev/technonymous",
		Status:          "active",
		LastDiscussedAt: &lastDiscussed,
		Signals:         []string{"workspace:main", "workspace:lori", "session:main", "memory:ellie"},
		Issues: []OpenClawIssueCandidate{
			{Title: "Fix API rate limiting"},
			{Title: "Auth flow refactor"},
			{Title: "Memory sync error"},
		},
	})

	require.Contains(t, description, "Imported from OpenClaw activity. Initial focus:")
	require.Contains(t, description, "Fix API rate limiting")
	require.Contains(t, description, "Auth flow refactor")
	require.Contains(t, description, "Memory sync error")
	require.Contains(t, description, "Last discussed on 2026-02-16.")
	require.Contains(t, description, "Evidence: workspace (2), session (1), memory (1).")
	require.Contains(t, description, "Inferred status: active.")
	require.NotContains(t, description, "workspace:main")
	require.NotContains(t, description, "session:main")
}

func TestBuildOpenClawProjectDescriptionFallsBackToRepositoryContext(t *testing.T) {
	description := BuildOpenClawProjectDescription(OpenClawProjectCandidate{
		Name:     "Otter Camp",
		RepoPath: "/Users/sam/dev/otter-camp.git",
		Status:   "completed",
	})

	require.Contains(t, description, "Imported from OpenClaw activity for repository otter-camp.")
	require.Contains(t, description, "Inferred status: completed.")
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

func TestOpenClawProjectDiscoveryPersistsTerminalIssueStatus(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)
	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-project-discovery-terminal-status")

	result, err := DiscoverOpenClawProjects(context.Background(), db, OpenClawProjectDiscoveryPersistInput{
		OrgID: orgID,
		ImportInput: OpenClawProjectImportInput{
			Memories: []OpenClawMemorySignal{
				{
					AgentID:     "ellie",
					ProjectHint: "otter-camp",
					IssueSignals: []OpenClawIssueSignal{
						{Title: "Finalize hosted bridge migration", Status: "completed"},
					},
					OccurredAt: time.Date(2026, 2, 12, 11, 0, 0, 0, time.UTC),
				},
			},
		},
		ReferenceTime: time.Date(2026, 2, 12, 17, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.ProjectsCreated)
	require.Equal(t, 1, result.IssuesCreated)

	var (
		state      string
		workStatus string
	)
	err = db.QueryRow(
		`SELECT state, work_status
		   FROM project_issues
		  WHERE org_id = $1
		  LIMIT 1`,
		orgID,
	).Scan(&state, &workStatus)
	require.NoError(t, err)
	require.Equal(t, "closed", state)
	require.Equal(t, "done", workStatus)
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

func TestOpenClawProjectDiscoveryRejectsMalformedOrgID(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	_, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, OpenClawProjectDiscoveryInput{
		OrgID: "------------------------------------",
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug: "main",
				Body:      "project:otter-camp issue: add migration status endpoint",
				CreatedAt: time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC),
			},
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid org_id")
}

func TestOpenClawProjectDiscoveryIssueNumberUniqueness(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-project-discovery-issue-unique")

	var projectID string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status)
		 VALUES ($1, 'Otter Camp', 'active')
		 RETURNING id::text`,
		orgID,
	).Scan(&projectID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, 1, 'Issue One', 'open', 'local')`,
		orgID,
		projectID,
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, 1, 'Issue Duplicate', 'open', 'local')`,
		orgID,
		projectID,
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "duplicate key value")
}

func TestProjectDiscoveryCreatesTaxonomyNode(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)
	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-project-taxonomy-node")

	_, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, OpenClawProjectDiscoveryInput{
		OrgID: orgID,
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug: "main",
				Body:      "project:otter-camp issue: add taxonomy discovery link",
				CreatedAt: time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
			},
		},
		ReferenceTime: time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	var projectsRootID string
	err = db.QueryRow(
		`SELECT id::text
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1
		    AND parent_id IS NULL
		    AND slug = 'projects'`,
		orgID,
	).Scan(&projectsRootID)
	require.NoError(t, err)

	var projectNodeCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1
		    AND parent_id = $2
		    AND slug = 'otter-camp'`,
		orgID,
		projectsRootID,
	).Scan(&projectNodeCount)
	require.NoError(t, err)
	require.Equal(t, 1, projectNodeCount)
}

func TestProjectDiscoveryTaxonomyIdempotent(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)
	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-project-taxonomy-idempotent")

	input := OpenClawProjectDiscoveryInput{
		OrgID: orgID,
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug: "main",
				Body:      "project:otter-camp issue: add taxonomy discovery link",
				CreatedAt: time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
			},
		},
		ReferenceTime: time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
	}

	_, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, input)
	require.NoError(t, err)
	_, err = DiscoverOpenClawProjectsFromHistory(context.Background(), db, input)
	require.NoError(t, err)

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM ellie_taxonomy_nodes n
		   JOIN ellie_taxonomy_nodes p ON p.id = n.parent_id
		  WHERE n.org_id = $1
		    AND p.org_id = $1
		    AND p.slug = 'projects'
		    AND n.slug = 'otter-camp'`,
		orgID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestProjectDiscoveryTaxonomyOrgIsolation(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)
	orgA := createOpenClawImportTestOrganization(t, db, "openclaw-project-taxonomy-org-a")
	orgB := createOpenClawImportTestOrganization(t, db, "openclaw-project-taxonomy-org-b")

	_, err := DiscoverOpenClawProjectsFromHistory(context.Background(), db, OpenClawProjectDiscoveryInput{
		OrgID: orgA,
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug: "main",
				Body:      "project:otter-camp issue: add taxonomy discovery link",
				CreatedAt: time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
			},
		},
		ReferenceTime: time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	var orgARoots int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1
		    AND parent_id IS NULL
		    AND slug = 'projects'`,
		orgA,
	).Scan(&orgARoots)
	require.NoError(t, err)
	require.Equal(t, 1, orgARoots)

	var orgBRoots int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1
		    AND parent_id IS NULL
		    AND slug = 'projects'`,
		orgB,
	).Scan(&orgBRoots)
	require.NoError(t, err)
	require.Equal(t, 0, orgBRoots)
}
