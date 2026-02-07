package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultGitHubAPIBaseURL = "https://api.github.com"

type GitHubIssueCloser interface {
	ResolveIssue(ctx context.Context, input GitHubIssueResolutionInput) (GitHubIssueResolutionResult, error)
}

type GitHubIssueResolutionInput struct {
	RepositoryFullName string
	IssueNumber        int64
	CommentBody        string
	IdempotencyMarker  string
}

type GitHubIssueResolutionResult struct {
	CommentPosted bool
	IssueClosed   bool
}

type githubIssueCloser struct {
	baseURL *url.URL
	token   string
	client  *http.Client
}

func newGitHubIssueCloserFromEnv() GitHubIssueCloser {
	token := strings.TrimSpace(firstNonEmptyString(
		os.Getenv("GITHUB_PUBLISH_TOKEN"),
		os.Getenv("GITHUB_TOKEN"),
	))
	if token == "" {
		return nil
	}

	baseURLRaw := strings.TrimSpace(os.Getenv("GITHUB_API_BASE_URL"))
	if baseURLRaw == "" {
		baseURLRaw = defaultGitHubAPIBaseURL
	}
	baseURL, err := url.Parse(baseURLRaw)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		baseURL, _ = url.Parse(defaultGitHubAPIBaseURL)
	}
	return &githubIssueCloser{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *githubIssueCloser) ResolveIssue(
	ctx context.Context,
	input GitHubIssueResolutionInput,
) (GitHubIssueResolutionResult, error) {
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return GitHubIssueResolutionResult{}, fmt.Errorf("repository_full_name is required")
	}
	if input.IssueNumber <= 0 {
		return GitHubIssueResolutionResult{}, fmt.Errorf("issue_number must be greater than zero")
	}

	result := GitHubIssueResolutionResult{}
	issueState, err := c.fetchIssueState(ctx, repo, input.IssueNumber)
	if err != nil {
		return result, err
	}

	marker := strings.TrimSpace(input.IdempotencyMarker)
	if marker == "" {
		return result, fmt.Errorf("idempotency marker is required")
	}
	commentExists, err := c.commentExists(ctx, repo, input.IssueNumber, marker)
	if err != nil {
		return result, err
	}
	if !commentExists {
		if err := c.postComment(ctx, repo, input.IssueNumber, input.CommentBody); err != nil {
			return result, err
		}
		result.CommentPosted = true
	}

	if issueState != "closed" {
		if err := c.closeIssue(ctx, repo, input.IssueNumber); err != nil {
			return result, err
		}
	}
	result.IssueClosed = true
	return result, nil
}

func (c *githubIssueCloser) fetchIssueState(ctx context.Context, repo string, issueNumber int64) (string, error) {
	endpoint := "/repos/" + repo + "/issues/" + strconv.FormatInt(issueNumber, 10)
	var payload struct {
		State string `json:"state"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &payload); err != nil {
		return "", err
	}
	state := strings.TrimSpace(strings.ToLower(payload.State))
	if state == "" {
		return "", fmt.Errorf("github issue state is empty")
	}
	return state, nil
}

func (c *githubIssueCloser) commentExists(
	ctx context.Context,
	repo string,
	issueNumber int64,
	marker string,
) (bool, error) {
	endpoint := "/repos/" + repo + "/issues/" + strconv.FormatInt(issueNumber, 10) +
		"/comments?per_page=100&sort=created&direction=desc"
	var comments []struct {
		Body string `json:"body"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &comments); err != nil {
		return false, err
	}
	for _, item := range comments {
		if strings.Contains(item.Body, marker) {
			return true, nil
		}
	}
	return false, nil
}

func (c *githubIssueCloser) postComment(
	ctx context.Context,
	repo string,
	issueNumber int64,
	commentBody string,
) error {
	commentBody = strings.TrimSpace(commentBody)
	if commentBody == "" {
		return fmt.Errorf("comment body is required")
	}
	endpoint := "/repos/" + repo + "/issues/" + strconv.FormatInt(issueNumber, 10) + "/comments"
	return c.doJSON(ctx, http.MethodPost, endpoint, map[string]string{
		"body": commentBody,
	}, nil)
}

func (c *githubIssueCloser) closeIssue(ctx context.Context, repo string, issueNumber int64) error {
	endpoint := "/repos/" + repo + "/issues/" + strconv.FormatInt(issueNumber, 10)
	return c.doJSON(ctx, http.MethodPatch, endpoint, map[string]string{
		"state": "closed",
	}, nil)
}

func (c *githubIssueCloser) doJSON(
	ctx context.Context,
	method string,
	endpoint string,
	requestBody any,
	responseBody any,
) error {
	requestURL, err := c.resolveURL(endpoint)
	if err != nil {
		return err
	}

	var bodyReader io.Reader
	if requestBody != nil {
		payload, marshalErr := json.Marshal(requestBody)
		if marshalErr != nil {
			return fmt.Errorf("marshal github request body failed: %w", marshalErr)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return fmt.Errorf("build github request failed: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read github response failed: %w", readErr)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		trimmed := strings.TrimSpace(string(body))
		if len(trimmed) > 300 {
			trimmed = trimmed[:300]
		}
		return fmt.Errorf("github request %s %s failed with status %d: %s", method, endpoint, resp.StatusCode, trimmed)
	}

	if responseBody != nil && len(body) > 0 {
		if err := json.Unmarshal(body, responseBody); err != nil {
			return fmt.Errorf("decode github response failed: %w", err)
		}
	}
	return nil
}

func (c *githubIssueCloser) resolveURL(endpoint string) (string, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return "", fmt.Errorf("endpoint is required")
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse github endpoint failed: %w", err)
	}
	return c.baseURL.ResolveReference(parsed).String(), nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
