package api

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type githubPublishIssueResolutionSummary struct {
	Attempted int
	Closed    int
	Failed    int
}

type githubPublishIssueCandidate struct {
	Issue store.ProjectIssue
	Link  store.ProjectIssueGitHubLink
}

func (h *GitHubIntegrationHandler) resolveLinkedGitHubIssuesAfterPublish(
	ctx context.Context,
	orgID string,
	projectID string,
	publishedHeadSHA string,
) githubPublishIssueResolutionSummary {
	summary := githubPublishIssueResolutionSummary{}
	if h.IssueStore == nil || h.DB == nil {
		return summary
	}

	candidates, err := loadPublishIssueCandidates(ctx, h.IssueStore, projectID)
	if err != nil {
		_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.publish_issue_close_failed", map[string]any{
			"project_id": projectID,
			"error":      err.Error(),
			"reason":     "candidate_load_failed",
		})
		summary.Failed = 1
		return summary
	}
	if len(candidates) == 0 {
		return summary
	}
	if h.IssueCloser == nil {
		_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.publish_issue_close_failed", map[string]any{
			"project_id":      projectID,
			"reason":          "issue_closer_not_configured",
			"eligible_issues": len(candidates),
		})
		summary.Failed = len(candidates)
		return summary
	}

	for _, candidate := range candidates {
		summary.Attempted++
		marker := buildPublishResolutionMarker(candidate.Issue.ID, publishedHeadSHA)
		commitLinks := buildPublishCommitLinks(candidate.Link, publishedHeadSHA)
		commentBody := buildPublishResolutionComment(candidate.Issue, candidate.Link, commitLinks, marker)

		result, resolveErr := h.IssueCloser.ResolveIssue(ctx, GitHubIssueResolutionInput{
			RepositoryFullName: candidate.Link.RepositoryFullName,
			IssueNumber:        candidate.Link.GitHubNumber,
			CommentBody:        commentBody,
			IdempotencyMarker:  marker,
		})
		if resolveErr != nil {
			summary.Failed++
			_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.publish_issue_close_failed", map[string]any{
				"project_id":      projectID,
				"issue_id":        candidate.Issue.ID,
				"issue_number":    candidate.Issue.IssueNumber,
				"github_number":   candidate.Link.GitHubNumber,
				"repository":      candidate.Link.RepositoryFullName,
				"comment_posted":  result.CommentPosted,
				"issue_closed":    result.IssueClosed,
				"published_head":  strings.TrimSpace(publishedHeadSHA),
				"resolution_mark": marker,
				"error":           resolveErr.Error(),
			})
			continue
		}

		if !result.IssueClosed {
			summary.Failed++
			_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.publish_issue_close_failed", map[string]any{
				"project_id":      projectID,
				"issue_id":        candidate.Issue.ID,
				"issue_number":    candidate.Issue.IssueNumber,
				"github_number":   candidate.Link.GitHubNumber,
				"repository":      candidate.Link.RepositoryFullName,
				"comment_posted":  result.CommentPosted,
				"issue_closed":    result.IssueClosed,
				"published_head":  strings.TrimSpace(publishedHeadSHA),
				"resolution_mark": marker,
				"error":           "issue was not closed by resolver",
			})
			continue
		}

		_, upsertErr := h.IssueStore.UpsertGitHubLink(ctx, store.UpsertProjectIssueGitHubLinkInput{
			IssueID:            candidate.Issue.ID,
			RepositoryFullName: candidate.Link.RepositoryFullName,
			GitHubNumber:       candidate.Link.GitHubNumber,
			GitHubURL:          candidate.Link.GitHubURL,
			GitHubState:        "closed",
		})
		if upsertErr != nil {
			summary.Failed++
			_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.publish_issue_close_failed", map[string]any{
				"project_id":      projectID,
				"issue_id":        candidate.Issue.ID,
				"issue_number":    candidate.Issue.IssueNumber,
				"github_number":   candidate.Link.GitHubNumber,
				"repository":      candidate.Link.RepositoryFullName,
				"comment_posted":  result.CommentPosted,
				"issue_closed":    true,
				"published_head":  strings.TrimSpace(publishedHeadSHA),
				"resolution_mark": marker,
				"error":           upsertErr.Error(),
			})
			continue
		}

		summary.Closed++
		_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.publish_issue_closed", map[string]any{
			"project_id":      projectID,
			"issue_id":        candidate.Issue.ID,
			"issue_number":    candidate.Issue.IssueNumber,
			"github_number":   candidate.Link.GitHubNumber,
			"repository":      candidate.Link.RepositoryFullName,
			"comment_posted":  result.CommentPosted,
			"published_head":  strings.TrimSpace(publishedHeadSHA),
			"resolution_mark": marker,
		})
	}

	return summary
}

func loadPublishIssueCandidates(
	ctx context.Context,
	issueStore *store.ProjectIssueStore,
	projectID string,
) ([]githubPublishIssueCandidate, error) {
	closedState := "closed"
	issueKind := "issue"
	issues, err := issueStore.ListIssues(ctx, store.ProjectIssueFilter{
		ProjectID: projectID,
		State:     &closedState,
		Kind:      &issueKind,
		Limit:     200,
	})
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return []githubPublishIssueCandidate{}, nil
	}

	issueIDs := make([]string, 0, len(issues))
	for _, issue := range issues {
		issueIDs = append(issueIDs, issue.ID)
	}
	linksByIssueID, err := issueStore.ListGitHubLinksByIssueIDs(ctx, issueIDs)
	if err != nil {
		return nil, err
	}

	sort.Slice(issues, func(i, j int) bool {
		return issues[i].IssueNumber < issues[j].IssueNumber
	})

	candidates := make([]githubPublishIssueCandidate, 0, len(issues))
	for _, issue := range issues {
		link, ok := linksByIssueID[issue.ID]
		if !ok {
			continue
		}
		if normalizeGitHubIssueState(link.GitHubState) != "open" {
			continue
		}
		if isPullRequestURL(link.GitHubURL) {
			continue
		}
		candidates = append(candidates, githubPublishIssueCandidate{
			Issue: issue,
			Link:  link,
		})
	}
	return candidates, nil
}

func buildPublishResolutionComment(
	issue store.ProjectIssue,
	link store.ProjectIssueGitHubLink,
	commitLinks []string,
	marker string,
) string {
	lines := []string{
		"Resolved in OtterCamp and published.",
		"",
		fmt.Sprintf("OtterCamp issue ID: %s", strings.TrimSpace(issue.ID)),
		fmt.Sprintf("OtterCamp issue number: #%d", issue.IssueNumber),
		fmt.Sprintf("GitHub issue: #%d", link.GitHubNumber),
		"",
		"Commit links:",
	}
	if len(commitLinks) == 0 {
		lines = append(lines, "- unavailable")
	} else {
		for _, item := range commitLinks {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			lines = append(lines, "- "+trimmed)
		}
	}
	if marker != "" {
		lines = append(lines, "", marker)
	}
	return strings.Join(lines, "\n")
}

func buildPublishResolutionMarker(issueID, headSHA string) string {
	issueID = strings.TrimSpace(issueID)
	headSHA = strings.TrimSpace(headSHA)
	if headSHA == "" {
		headSHA = "unknown"
	}
	return fmt.Sprintf("<!-- ottercamp-publish-resolution issue_id=%s sha=%s -->", issueID, headSHA)
}

func buildPublishCommitLinks(link store.ProjectIssueGitHubLink, headSHA string) []string {
	commitURL := buildRepositoryCommitURL(link, headSHA)
	if commitURL == "" {
		return []string{}
	}
	return []string{commitURL}
}

func buildRepositoryCommitURL(link store.ProjectIssueGitHubLink, headSHA string) string {
	headSHA = strings.TrimSpace(headSHA)
	repo := strings.TrimSpace(link.RepositoryFullName)
	if headSHA == "" || repo == "" {
		return ""
	}
	base := repositoryWebBase(link)
	return strings.TrimSuffix(base, "/") + "/" + repo + "/commit/" + headSHA
}

func repositoryWebBase(link store.ProjectIssueGitHubLink) string {
	if link.GitHubURL != nil {
		if parsed, err := url.Parse(strings.TrimSpace(*link.GitHubURL)); err == nil {
			if parsed.Scheme != "" && parsed.Host != "" {
				return parsed.Scheme + "://" + parsed.Host
			}
		}
	}
	return "https://github.com"
}

func normalizeGitHubIssueState(state string) string {
	return strings.TrimSpace(strings.ToLower(state))
}

func isPullRequestURL(rawURL *string) bool {
	if rawURL == nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(*rawURL)), "/pull/")
}
