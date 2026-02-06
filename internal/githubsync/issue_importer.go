package githubsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/github"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

const issueImportCheckpointResource = "issues"

type IssueImportStore interface {
	UpsertIssueFromGitHub(
		ctx context.Context,
		input store.UpsertProjectIssueFromGitHubInput,
	) (*store.ProjectIssue, bool, error)
	UpsertSyncCheckpoint(
		ctx context.Context,
		input store.UpsertProjectIssueSyncCheckpointInput,
	) (*store.ProjectIssueSyncCheckpoint, error)
}

type IssueImportInput struct {
	ProjectID          string
	RepositoryFullName string
	Cursor             *string
}

type IssueImportResult struct {
	Imported   int
	Updated    int
	Checkpoint *store.ProjectIssueSyncCheckpoint
}

type IssueImporter struct {
	Client *github.Client
	Store  IssueImportStore
	now    func() time.Time
}

func NewIssueImporter(client *github.Client, importStore IssueImportStore) *IssueImporter {
	return &IssueImporter{
		Client: client,
		Store:  importStore,
		now:    time.Now,
	}
}

func (i *IssueImporter) ImportProject(
	ctx context.Context,
	input IssueImportInput,
) (*IssueImportResult, error) {
	if i.Client == nil {
		return nil, fmt.Errorf("github client is required")
	}
	if i.Store == nil {
		return nil, fmt.Errorf("issue store is required")
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if !storeUUIDPattern.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}

	baseEndpoint := issueImportEndpoint(repo)
	checkpoint := github.PaginationCheckpoint{}
	if input.Cursor != nil {
		checkpoint.NextURL = strings.TrimSpace(*input.Cursor)
	}

	imported := 0
	updated := 0
	for {
		response, nextCheckpoint, err := i.Client.FetchNextPage(
			ctx,
			github.JobTypeImport,
			checkpoint,
			baseEndpoint,
		)
		if err != nil {
			return nil, err
		}

		var pageItems []githubIssueImportRecord
		if err := json.Unmarshal(response.Body, &pageItems); err != nil {
			return nil, fmt.Errorf("decode github issues page: %w", err)
		}

		for _, item := range pageItems {
			if item.Number <= 0 || strings.TrimSpace(item.Title) == "" {
				continue
			}
			_, created, err := i.Store.UpsertIssueFromGitHub(ctx, store.UpsertProjectIssueFromGitHubInput{
				ProjectID:          projectID,
				RepositoryFullName: repo,
				GitHubNumber:       item.Number,
				Title:              strings.TrimSpace(item.Title),
				Body:               stringPtrOrNil(item.Body),
				State:              item.State,
				GitHubURL:          githubIssueURL(item, repo),
				ClosedAt:           item.ClosedAt,
			})
			if err != nil {
				return nil, err
			}
			if created {
				imported++
			} else {
				updated++
			}
		}

		checkpoint = nextCheckpoint
		if strings.TrimSpace(checkpoint.NextURL) == "" {
			break
		}
	}

	var cursor *string
	if trimmed := strings.TrimSpace(checkpoint.NextURL); trimmed != "" {
		cursor = &trimmed
	}
	now := i.now().UTC()
	syncCheckpoint, err := i.Store.UpsertSyncCheckpoint(ctx, store.UpsertProjectIssueSyncCheckpointInput{
		ProjectID:          projectID,
		RepositoryFullName: repo,
		Resource:           issueImportCheckpointResource,
		Cursor:             cursor,
		LastSyncedAt:       &now,
	})
	if err != nil {
		return nil, err
	}

	return &IssueImportResult{
		Imported:   imported,
		Updated:    updated,
		Checkpoint: syncCheckpoint,
	}, nil
}

type githubIssueImportRecord struct {
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

func issueImportEndpoint(repositoryFullName string) string {
	return "/repos/" + strings.TrimSpace(repositoryFullName) + "/issues?state=all&sort=updated&direction=asc&per_page=100"
}

func githubIssueURL(item githubIssueImportRecord, repositoryFullName string) *string {
	if raw := strings.TrimSpace(item.HTMLURL); raw != "" {
		return &raw
	}

	repo := strings.TrimSpace(repositoryFullName)
	if repo == "" || item.Number <= 0 {
		return nil
	}
	escapedRepo := (&url.URL{Path: repo}).EscapedPath()
	if item.PullRequest != nil {
		value := "https://github.com/" + escapedRepo + "/pull/" + fmt.Sprintf("%d", item.Number)
		return &value
	}
	value := "https://github.com/" + escapedRepo + "/issues/" + fmt.Sprintf("%d", item.Number)
	return &value
}

func stringPtrOrNil(raw string) *string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	return &value
}

var storeUUIDPattern = storeUUIDRegex()

func storeUUIDRegex() interface{ MatchString(string) bool } {
	// keep validation behavior aligned with store package without exposing internals.
	return uuidRegexWrapper{}
}

type uuidRegexWrapper struct{}

func (uuidRegexWrapper) MatchString(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	dashPositions := map[int]bool{8: true, 13: true, 18: true, 23: true}
	for index, ch := range value {
		if dashPositions[index] {
			if ch != '-' {
				return false
			}
			continue
		}
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
			return false
		}
	}
	return true
}
