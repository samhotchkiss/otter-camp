package importer

import (
	"path/filepath"
	"testing"

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
