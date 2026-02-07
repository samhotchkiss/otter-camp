package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveReviewModeMatrix(t *testing.T) {
	local := resolveReviewMode("push")
	require.Equal(t, reviewWorkflowModeLocalIssuePR, local.Mode)
	require.False(t, local.GitHubPREnabled)

	sync := resolveReviewMode("sync")
	require.Equal(t, reviewWorkflowModeGitHubPR, sync.Mode)
	require.True(t, sync.GitHubPREnabled)

	defaultMode := resolveReviewMode("")
	require.Equal(t, reviewWorkflowModeGitHubPR, defaultMode.Mode)
	require.True(t, defaultMode.GitHubPREnabled)
}
