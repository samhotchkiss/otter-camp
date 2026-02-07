package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/githubsync"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type githubIssueWebhookPayload struct {
	Action       string                    `json:"action"`
	Repository   githubWebhookRepository   `json:"repository"`
	Installation githubWebhookInstallation `json:"installation"`
	Issue        githubWebhookIssueRecord  `json:"issue"`
}

type githubPullRequestWebhookPayload struct {
	Action       string                    `json:"action"`
	Number       int64                     `json:"number"`
	Repository   githubWebhookRepository   `json:"repository"`
	Installation githubWebhookInstallation `json:"installation"`
	PullRequest  struct {
		Number   int64      `json:"number"`
		Title    string     `json:"title"`
		Body     string     `json:"body"`
		State    string     `json:"state"`
		HTMLURL  string     `json:"html_url"`
		Merged   bool       `json:"merged"`
		ClosedAt *time.Time `json:"closed_at"`
	} `json:"pull_request"`
}

type githubIssueCommentWebhookPayload struct {
	Action       string                    `json:"action"`
	Repository   githubWebhookRepository   `json:"repository"`
	Installation githubWebhookInstallation `json:"installation"`
	Issue        githubWebhookIssueRecord  `json:"issue"`
	Comment      struct {
		Body      string     `json:"body"`
		HTMLURL   string     `json:"html_url"`
		CreatedAt *time.Time `json:"created_at"`
		UpdatedAt *time.Time `json:"updated_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
}

type githubWebhookRepository struct {
	FullName string `json:"full_name"`
}

type githubWebhookInstallation struct {
	ID int64 `json:"id"`
}

type githubWebhookIssueRecord struct {
	Number      int64      `json:"number"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	State       string     `json:"state"`
	HTMLURL     string     `json:"html_url"`
	ClosedAt    *time.Time `json:"closed_at"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request,omitempty"`
}

func (h *GitHubIntegrationHandler) handleIssueWebhookEvent(
	ctx context.Context,
	orgID string,
	projectID *string,
	eventType string,
	body []byte,
	deliveryID string,
) error {
	if projectID == nil || strings.TrimSpace(*projectID) == "" || h.IssueStore == nil {
		return nil
	}

	switch eventType {
	case "issues":
		return h.handleIssuesWebhook(ctx, orgID, *projectID, body, deliveryID)
	case "pull_request":
		return h.handlePullRequestWebhook(ctx, orgID, *projectID, body, deliveryID)
	case "issue_comment":
		return h.handleIssueCommentWebhook(ctx, orgID, *projectID, body, deliveryID)
	default:
		return nil
	}
}

func (h *GitHubIntegrationHandler) handleIssuesWebhook(
	ctx context.Context,
	orgID, projectID string,
	body []byte,
	deliveryID string,
) error {
	var payload githubIssueWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	switch action {
	case "opened", "edited", "reopened", "closed":
	default:
		return nil
	}

	issue, _, err := h.upsertIssueFromWebhook(ctx, projectID, payload.Repository.FullName, payload.Issue)
	if err != nil {
		return err
	}
	if issue == nil {
		return nil
	}

	_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.issue."+action, map[string]any{
		"delivery_id":      deliveryID,
		"repository":       payload.Repository.FullName,
		"issue_id":         issue.ID,
		"issue_number":     payload.Issue.Number,
		"github_issue_url": payload.Issue.HTMLURL,
	})
	return nil
}

func (h *GitHubIntegrationHandler) handlePullRequestWebhook(
	ctx context.Context,
	orgID, projectID string,
	body []byte,
	deliveryID string,
) error {
	var payload githubPullRequestWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	transition, err := githubsync.MapPullRequestWebhookTransition(action, payload.PullRequest.Merged, time.Now().UTC())
	if err != nil {
		return nil
	}

	number := payload.PullRequest.Number
	if number <= 0 {
		number = payload.Number
	}
	record := githubWebhookIssueRecord{
		Number:   number,
		Title:    payload.PullRequest.Title,
		Body:     payload.PullRequest.Body,
		State:    transition.State,
		HTMLURL:  payload.PullRequest.HTMLURL,
		ClosedAt: payload.PullRequest.ClosedAt,
		PullRequest: &struct {
			URL string `json:"url"`
		}{URL: payload.PullRequest.HTMLURL},
	}
	if transition.ClosedAt != nil {
		record.ClosedAt = transition.ClosedAt
	}

	issue, _, err := h.upsertIssueFromWebhook(ctx, projectID, payload.Repository.FullName, record)
	if err != nil {
		return err
	}
	if issue == nil {
		return nil
	}

	_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.pull_request."+action, map[string]any{
		"delivery_id":             deliveryID,
		"repository":              payload.Repository.FullName,
		"issue_id":                issue.ID,
		"pull_request_number":     number,
		"github_pull_request_url": payload.PullRequest.HTMLURL,
	})
	return nil
}

func (h *GitHubIntegrationHandler) handleIssueCommentWebhook(
	ctx context.Context,
	orgID, projectID string,
	body []byte,
	deliveryID string,
) error {
	var payload githubIssueCommentWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}

	action := strings.TrimSpace(strings.ToLower(payload.Action))
	if action != "created" && action != "edited" {
		return nil
	}

	issue, _, err := h.upsertIssueFromWebhook(ctx, projectID, payload.Repository.FullName, payload.Issue)
	if err != nil {
		return err
	}
	if issue == nil {
		return nil
	}

	_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.issue_comment."+action, map[string]any{
		"delivery_id":        deliveryID,
		"repository":         payload.Repository.FullName,
		"issue_id":           issue.ID,
		"issue_number":       payload.Issue.Number,
		"github_comment_url": payload.Comment.HTMLURL,
		"comment_author":     payload.Comment.User.Login,
	})
	return nil
}

func (h *GitHubIntegrationHandler) upsertIssueFromWebhook(
	ctx context.Context,
	projectID, repositoryFullName string,
	record githubWebhookIssueRecord,
) (*store.ProjectIssue, bool, error) {
	if record.Number <= 0 || strings.TrimSpace(record.Title) == "" {
		return nil, false, nil
	}
	githubURL := strings.TrimSpace(record.HTMLURL)
	if githubURL == "" {
		if record.PullRequest != nil {
			githubURL = fmt.Sprintf("https://github.com/%s/pull/%d", strings.TrimSpace(repositoryFullName), record.Number)
		} else {
			githubURL = fmt.Sprintf("https://github.com/%s/issues/%d", strings.TrimSpace(repositoryFullName), record.Number)
		}
	}

	issue, created, err := h.IssueStore.UpsertIssueFromGitHub(ctx, store.UpsertProjectIssueFromGitHubInput{
		ProjectID:          projectID,
		RepositoryFullName: strings.TrimSpace(repositoryFullName),
		GitHubNumber:       record.Number,
		Title:              strings.TrimSpace(record.Title),
		Body:               stringPtrOrNil(record.Body),
		State:              strings.TrimSpace(record.State),
		GitHubURL:          &githubURL,
		ClosedAt:           record.ClosedAt,
	})
	if err != nil {
		return nil, false, err
	}
	return issue, created, nil
}

func stringPtrOrNil(raw string) *string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	return &value
}
