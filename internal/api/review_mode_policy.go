package api

import (
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	reviewWorkflowModeGitHubPR     = "github_pr_sync"
	reviewWorkflowModeLocalIssuePR = "local_issue_review"
)

type reviewModeDecision struct {
	Mode            string `json:"mode"`
	GitHubPREnabled bool   `json:"github_pr_enabled"`
}

func resolveReviewMode(syncMode string) reviewModeDecision {
	if strings.EqualFold(strings.TrimSpace(syncMode), store.RepoSyncModePush) {
		return reviewModeDecision{
			Mode:            reviewWorkflowModeLocalIssuePR,
			GitHubPREnabled: false,
		}
	}
	return reviewModeDecision{
		Mode:            reviewWorkflowModeGitHubPR,
		GitHubPREnabled: true,
	}
}
