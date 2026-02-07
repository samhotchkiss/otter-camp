package api

import (
	"net/url"
	"strings"
)

func buildIssueReviewSourceURL(
	projectID string,
	issueID string,
	reviewCommitSHA string,
	addressedCommitSHA string,
) string {
	projectID = strings.TrimSpace(projectID)
	issueID = strings.TrimSpace(issueID)
	base := "/projects/" + projectID + "/issues/" + issueID

	query := url.Values{}
	if strings.TrimSpace(reviewCommitSHA) != "" {
		query.Set("review_sha", strings.TrimSpace(reviewCommitSHA))
	}
	if strings.TrimSpace(addressedCommitSHA) != "" {
		query.Set("addressed_sha", strings.TrimSpace(addressedCommitSHA))
	}
	encoded := query.Encode()
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}
