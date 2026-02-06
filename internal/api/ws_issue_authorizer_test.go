package api

import (
	"context"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestWSIssueSubscriptionAuthorizerAllowsVisibleIssue(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "ws-issue-auth-allow-org")
	projectID := insertProjectTestProject(t, db, orgID, "WS Issue Authorizer Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Authorizer issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	authorizer := wsIssueSubscriptionAuthorizer{IssueStore: issueStore}
	allowed, err := authorizer.CanSubscribeIssue(context.Background(), orgID, issue.ID)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestWSIssueSubscriptionAuthorizerRejectsCrossOrgIssue(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "ws-issue-auth-org-a")
	orgB := insertMessageTestOrganization(t, db, "ws-issue-auth-org-b")
	projectB := insertProjectTestProject(t, db, orgB, "WS Issue Authorizer Project B")

	issueStore := store.NewProjectIssueStore(db)
	issueB, err := issueStore.CreateIssue(issueTestCtx(orgB), store.CreateProjectIssueInput{
		ProjectID: projectB,
		Title:     "Org B issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	authorizer := wsIssueSubscriptionAuthorizer{IssueStore: issueStore}
	allowed, err := authorizer.CanSubscribeIssue(context.Background(), orgA, issueB.ID)
	require.NoError(t, err)
	require.False(t, allowed)
}
