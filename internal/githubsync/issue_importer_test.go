package githubsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/github"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeIssueImportStore struct {
	issuesByKey       map[string]store.ProjectIssue
	upsertInputs      []store.UpsertProjectIssueFromGitHubInput
	checkpointInputs  []store.UpsertProjectIssueSyncCheckpointInput
	nextIssueSequence int64
}

func newFakeIssueImportStore() *fakeIssueImportStore {
	return &fakeIssueImportStore{
		issuesByKey:       make(map[string]store.ProjectIssue),
		nextIssueSequence: 1,
	}
}

func (f *fakeIssueImportStore) UpsertIssueFromGitHub(
	_ context.Context,
	input store.UpsertProjectIssueFromGitHubInput,
) (*store.ProjectIssue, bool, error) {
	f.upsertInputs = append(f.upsertInputs, input)

	key := input.RepositoryFullName + "#" + strconv.FormatInt(input.GitHubNumber, 10)
	existing, found := f.issuesByKey[key]
	if !found {
		existing = store.ProjectIssue{
			ID:          fmt.Sprintf("issue-%d", f.nextIssueSequence),
			ProjectID:   input.ProjectID,
			IssueNumber: f.nextIssueSequence,
			Origin:      "github",
			CreatedAt:   time.Now().UTC(),
		}
		f.nextIssueSequence++
	}

	existing.Title = input.Title
	existing.Body = input.Body
	existing.State = strings.ToLower(strings.TrimSpace(input.State))
	existing.ClosedAt = input.ClosedAt
	existing.UpdatedAt = time.Now().UTC()
	if existing.State == "" {
		existing.State = "open"
	}
	f.issuesByKey[key] = existing
	return &existing, !found, nil
}

func (f *fakeIssueImportStore) UpsertSyncCheckpoint(
	_ context.Context,
	input store.UpsertProjectIssueSyncCheckpointInput,
) (*store.ProjectIssueSyncCheckpoint, error) {
	f.checkpointInputs = append(f.checkpointInputs, input)
	lastSyncedAt := time.Now().UTC()
	if input.LastSyncedAt != nil {
		lastSyncedAt = input.LastSyncedAt.UTC()
	}
	record := &store.ProjectIssueSyncCheckpoint{
		ID:                 "checkpoint-1",
		ProjectID:          input.ProjectID,
		RepositoryFullName: input.RepositoryFullName,
		Resource:           input.Resource,
		Cursor:             input.Cursor,
		LastSyncedAt:       lastSyncedAt,
	}
	return record, nil
}

func TestIssueImporterMapsGitHubIssueFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/samhotchkiss/otter-camp/issues", r.URL.Path)
		require.Equal(t, "all", r.URL.Query().Get("state"))
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		payload := []map[string]any{
			{
				"number":   101,
				"title":    "Imported bug",
				"body":     "Body text",
				"state":    "open",
				"html_url": "https://github.com/samhotchkiss/otter-camp/issues/101",
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(payload))
	}))
	defer server.Close()

	client, err := github.NewClient(server.URL)
	require.NoError(t, err)
	fakeStore := newFakeIssueImportStore()
	importer := NewIssueImporter(client, fakeStore)

	result, err := importer.ImportProject(context.Background(), IssueImportInput{
		ProjectID:          "550e8400-e29b-41d4-a716-446655440000",
		RepositoryFullName: "samhotchkiss/otter-camp",
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Imported)
	require.Equal(t, 0, result.Updated)
	require.Len(t, fakeStore.upsertInputs, 1)
	require.Equal(t, int64(101), fakeStore.upsertInputs[0].GitHubNumber)
	require.Equal(t, "Imported bug", fakeStore.upsertInputs[0].Title)
	require.Equal(t, "open", fakeStore.upsertInputs[0].State)
	require.NotNil(t, fakeStore.upsertInputs[0].GitHubURL)
	require.Equal(
		t,
		"https://github.com/samhotchkiss/otter-camp/issues/101",
		*fakeStore.upsertInputs[0].GitHubURL,
	)
}

func TestIssueImporterMapsPullRequestRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payload := []map[string]any{
			{
				"number": 302,
				"title":  "Imported PR",
				"body":   "PR body",
				"state":  "closed",
				"pull_request": map[string]any{
					"url": "https://api.github.com/repos/samhotchkiss/otter-camp/pulls/302",
				},
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(payload))
	}))
	defer server.Close()

	client, err := github.NewClient(server.URL)
	require.NoError(t, err)
	fakeStore := newFakeIssueImportStore()
	importer := NewIssueImporter(client, fakeStore)

	result, err := importer.ImportProject(context.Background(), IssueImportInput{
		ProjectID:          "550e8400-e29b-41d4-a716-446655440000",
		RepositoryFullName: "samhotchkiss/otter-camp",
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Imported)
	require.Len(t, fakeStore.upsertInputs, 1)
	require.NotNil(t, fakeStore.upsertInputs[0].GitHubURL)
	require.Contains(t, *fakeStore.upsertInputs[0].GitHubURL, "/pull/302")
}

func TestIssueImporterBackfillsOpenAndClosedHistoryAcrossPages(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "2":
			payload := []map[string]any{
				{
					"number":   2,
					"title":    "Closed issue",
					"body":     "done",
					"state":    "closed",
					"html_url": "https://github.com/samhotchkiss/otter-camp/issues/2",
				},
			}
			require.NoError(t, json.NewEncoder(w).Encode(payload))
		default:
			next := serverURL + "/repos/samhotchkiss/otter-camp/issues?state=all&sort=updated&direction=asc&per_page=100&page=2"
			w.Header().Set("Link", "<"+next+">; rel=\"next\"")
			payload := []map[string]any{
				{
					"number":   1,
					"title":    "Open issue",
					"body":     "todo",
					"state":    "open",
					"html_url": "https://github.com/samhotchkiss/otter-camp/issues/1",
				},
			}
			require.NoError(t, json.NewEncoder(w).Encode(payload))
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client, err := github.NewClient(server.URL)
	require.NoError(t, err)
	fakeStore := newFakeIssueImportStore()
	importer := NewIssueImporter(client, fakeStore)

	result, err := importer.ImportProject(context.Background(), IssueImportInput{
		ProjectID:          "550e8400-e29b-41d4-a716-446655440000",
		RepositoryFullName: "samhotchkiss/otter-camp",
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.Imported)
	require.Equal(t, 0, result.Updated)
	require.Len(t, fakeStore.issuesByKey, 2)
	require.NotNil(t, result.Checkpoint)
	require.Equal(t, issueImportCheckpointResource, result.Checkpoint.Resource)
}

func TestIssueImporterRerunIsIdempotentAndUpdatesChangedRecords(t *testing.T) {
	run := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		title := "Initial title"
		if run > 0 {
			title = "Updated title"
		}
		payload := []map[string]any{
			{
				"number":   77,
				"title":    title,
				"body":     "body",
				"state":    "open",
				"html_url": "https://github.com/samhotchkiss/otter-camp/issues/77",
			},
		}
		require.NoError(t, json.NewEncoder(w).Encode(payload))
	}))
	defer server.Close()

	client, err := github.NewClient(server.URL)
	require.NoError(t, err)
	fakeStore := newFakeIssueImportStore()
	importer := NewIssueImporter(client, fakeStore)

	first, err := importer.ImportProject(context.Background(), IssueImportInput{
		ProjectID:          "550e8400-e29b-41d4-a716-446655440000",
		RepositoryFullName: "samhotchkiss/otter-camp",
	})
	require.NoError(t, err)
	require.Equal(t, 1, first.Imported)
	require.Equal(t, 0, first.Updated)

	run = 1
	second, err := importer.ImportProject(context.Background(), IssueImportInput{
		ProjectID:          "550e8400-e29b-41d4-a716-446655440000",
		RepositoryFullName: "samhotchkiss/otter-camp",
	})
	require.NoError(t, err)
	require.Equal(t, 0, second.Imported)
	require.Equal(t, 1, second.Updated)
	require.Equal(t, "Updated title", fakeStore.issuesByKey["samhotchkiss/otter-camp#77"].Title)
}
