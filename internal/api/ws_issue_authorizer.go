package api

import (
	"context"
	"errors"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type wsIssueSubscriptionAuthorizer struct {
	IssueStore *store.ProjectIssueStore
}

func (a wsIssueSubscriptionAuthorizer) CanSubscribeIssue(
	ctx context.Context,
	orgID, issueID string,
) (bool, error) {
	if a.IssueStore == nil {
		return false, nil
	}

	orgID = strings.TrimSpace(orgID)
	issueID = strings.TrimSpace(issueID)
	if orgID == "" || issueID == "" {
		return false, nil
	}

	scopedCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, orgID)
	_, err := a.IssueStore.GetIssueByID(scopedCtx, issueID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNoWorkspace) {
		return false, nil
	}
	return false, err
}
