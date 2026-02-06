package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type issueReviewChangesResponse struct {
	IssueID               string                  `json:"issue_id"`
	ProjectID             string                  `json:"project_id"`
	DocumentPath          string                  `json:"document_path"`
	HeadSHA               string                  `json:"head_sha"`
	BaseSHA               string                  `json:"base_sha"`
	FallbackToFirstCommit bool                    `json:"fallback_to_first_commit"`
	Files                 []projectCommitDiffFile `json:"files"`
	Total                 int                     `json:"total"`
}

type issueReviewHistoryItem struct {
	SHA                  string  `json:"sha"`
	Subject              string  `json:"subject"`
	Body                 *string `json:"body,omitempty"`
	Message              string  `json:"message"`
	AuthorName           string  `json:"author_name"`
	AuthorEmail          *string `json:"author_email,omitempty"`
	AuthoredAt           string  `json:"authored_at"`
	BranchName           string  `json:"branch_name"`
	IsReviewCheckpoint   bool    `json:"is_review_checkpoint"`
	AddressedInCommitSHA *string `json:"addressed_in_commit_sha,omitempty"`
}

type issueReviewHistoryResponse struct {
	IssueID             string                   `json:"issue_id"`
	ProjectID           string                   `json:"project_id"`
	DocumentPath        string                   `json:"document_path"`
	LastReviewCommitSHA *string                  `json:"last_review_commit_sha,omitempty"`
	Items               []issueReviewHistoryItem `json:"items"`
	Total               int                      `json:"total"`
}

type issueReviewVersionResponse struct {
	IssueID      string `json:"issue_id"`
	ProjectID    string `json:"project_id"`
	DocumentPath string `json:"document_path"`
	SHA          string `json:"sha"`
	Content      string `json:"content"`
	ReadOnly     bool   `json:"read_only"`
}

func (h *IssuesHandler) ReviewChanges(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil || h.CommitStore == nil || h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	issue, documentPath, touchingCommits, checkpoint, repoPath, repoRelativePath, err := h.loadIssueReviewContext(r, issueID)
	if err != nil {
		h.handleIssueReviewError(w, err)
		return
	}
	if len(touchingCommits) == 0 {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "no commits found for linked document"})
		return
	}

	headSHA := touchingCommits[0].SHA
	baseSHA, fallback := resolveReviewDiffBaseSHA(touchingCommits, checkpoint)
	if baseSHA == "" {
		baseSHA = headSHA
	}

	files, err := buildIssueReviewDiffFiles(r.Context(), repoPath, baseSHA, headSHA, repoRelativePath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	sendJSON(w, http.StatusOK, issueReviewChangesResponse{
		IssueID:               issue.ID,
		ProjectID:             issue.ProjectID,
		DocumentPath:          documentPath,
		HeadSHA:               headSHA,
		BaseSHA:               baseSHA,
		FallbackToFirstCommit: fallback,
		Files:                 files,
		Total:                 len(files),
	})
}

func (h *IssuesHandler) ReviewHistory(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil || h.CommitStore == nil || h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}

	issue, documentPath, touchingCommits, checkpoint, _, _, err := h.loadIssueReviewContext(r, issueID)
	if err != nil {
		h.handleIssueReviewError(w, err)
		return
	}

	var checkpointSHA string
	if checkpoint != nil {
		checkpointSHA = strings.TrimSpace(checkpoint.LastReviewCommitSHA)
	}

	items := make([]issueReviewHistoryItem, 0, len(touchingCommits))
	for _, commit := range touchingCommits {
		items = append(items, issueReviewHistoryItem{
			SHA:                commit.SHA,
			Subject:            commit.Subject,
			Body:               trimOptionalString(commit.Body),
			Message:            commit.Message,
			AuthorName:         commit.AuthorName,
			AuthorEmail:        trimOptionalString(commit.AuthorEmail),
			AuthoredAt:         commit.AuthoredAt.UTC().Format(time.RFC3339),
			BranchName:         commit.BranchName,
			IsReviewCheckpoint: checkpointSHA != "" && commit.SHA == checkpointSHA,
		})
	}

	sendJSON(w, http.StatusOK, issueReviewHistoryResponse{
		IssueID:             issue.ID,
		ProjectID:           issue.ProjectID,
		DocumentPath:        documentPath,
		LastReviewCommitSHA: trimOptionalString(&checkpointSHA),
		Items:               items,
		Total:               len(items),
	})
}

func (h *IssuesHandler) ReviewVersion(w http.ResponseWriter, r *http.Request) {
	if h.IssueStore == nil || h.CommitStore == nil || h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	issueID := strings.TrimSpace(chi.URLParam(r, "id"))
	if issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "issue id is required"})
		return
	}
	sha := strings.TrimSpace(chi.URLParam(r, "sha"))
	if sha == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "sha is required"})
		return
	}

	issue, documentPath, touchingCommits, _, repoPath, repoRelativePath, err := h.loadIssueReviewContext(r, issueID)
	if err != nil {
		h.handleIssueReviewError(w, err)
		return
	}

	found := false
	for _, commit := range touchingCommits {
		if strings.TrimSpace(commit.SHA) == sha {
			found = true
			break
		}
	}
	if !found {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "commit is not in linked document history"})
		return
	}

	content, err := runGitInRepo(r.Context(), repoPath, "show", sha+":"+filepath.ToSlash(repoRelativePath))
	if err != nil {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "document version not found in commit"})
		return
	}

	sendJSON(w, http.StatusOK, issueReviewVersionResponse{
		IssueID:      issue.ID,
		ProjectID:    issue.ProjectID,
		DocumentPath: documentPath,
		SHA:          sha,
		Content:      content,
		ReadOnly:     true,
	})
}

func (h *IssuesHandler) loadIssueReviewContext(
	r *http.Request,
	issueID string,
) (
	*store.ProjectIssue,
	string,
	[]store.ProjectCommit,
	*store.ProjectIssueReviewCheckpoint,
	string,
	string,
	error,
) {
	issue, err := h.IssueStore.GetIssueByID(r.Context(), issueID)
	if err != nil {
		return nil, "", nil, nil, "", "", err
	}
	if issue.DocumentPath == nil || strings.TrimSpace(*issue.DocumentPath) == "" {
		return nil, "", nil, nil, "", "", errors.New("issue has no linked document")
	}
	documentPath, err := validateContentReadPath(*issue.DocumentPath)
	if err != nil {
		return nil, "", nil, nil, "", "", errors.New("issue linked document path is invalid")
	}

	binding, err := h.ProjectRepos.GetBinding(r.Context(), issue.ProjectID)
	if err != nil {
		return nil, "", nil, nil, "", "", err
	}
	if !binding.Enabled {
		return nil, "", nil, nil, "", "", errors.New("github integration is disabled for this project")
	}
	if binding.LocalRepoPath == nil || strings.TrimSpace(*binding.LocalRepoPath) == "" {
		return nil, "", nil, nil, "", "", errors.New("project has no local repo path configured")
	}
	repoPath := strings.TrimSpace(*binding.LocalRepoPath)
	if err := ensureGitRepoPath(repoPath); err != nil {
		return nil, "", nil, nil, "", "", err
	}

	commits, err := h.CommitStore.ListCommits(r.Context(), store.ProjectCommitFilter{
		ProjectID: issue.ProjectID,
		Limit:     200,
		Offset:    0,
	})
	if err != nil {
		return nil, "", nil, nil, "", "", err
	}
	touchingCommits := make([]store.ProjectCommit, 0, len(commits))
	for _, commit := range commits {
		if commitTouchesDocumentPath(commit, documentPath) {
			touchingCommits = append(touchingCommits, commit)
		}
	}

	var checkpoint *store.ProjectIssueReviewCheckpoint
	checkpoint, err = h.IssueStore.GetReviewCheckpoint(r.Context(), issue.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, "", nil, nil, "", "", err
	}
	if errors.Is(err, store.ErrNotFound) {
		checkpoint = nil
	}

	return issue, documentPath, touchingCommits, checkpoint, repoPath, strings.TrimPrefix(documentPath, "/"), nil
}

func (h *IssuesHandler) handleIssueReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	default:
		handleIssueStoreError(w, err)
	}
}

func commitTouchesDocumentPath(commit store.ProjectCommit, documentPath string) bool {
	target := strings.TrimSpace(documentPath)
	if target == "" {
		return false
	}
	targetRelative := strings.TrimPrefix(target, "/")

	for _, file := range commitDiffFilesFromMetadata(commit.Metadata) {
		candidate := strings.TrimSpace(strings.ReplaceAll(file.Path, "\\", "/"))
		if candidate == "" {
			continue
		}
		if !strings.HasPrefix(candidate, "/") {
			candidate = "/" + candidate
		}
		if candidate == target {
			return true
		}
		if strings.TrimPrefix(candidate, "/") == targetRelative {
			return true
		}
	}
	return false
}

func resolveReviewDiffBaseSHA(
	commits []store.ProjectCommit,
	checkpoint *store.ProjectIssueReviewCheckpoint,
) (string, bool) {
	if len(commits) == 0 {
		return "", false
	}
	if checkpoint != nil {
		checkpointSHA := strings.TrimSpace(checkpoint.LastReviewCommitSHA)
		if checkpointSHA != "" {
			for _, commit := range commits {
				if strings.TrimSpace(commit.SHA) == checkpointSHA {
					return checkpointSHA, false
				}
			}
		}
	}
	return strings.TrimSpace(commits[len(commits)-1].SHA), true
}

func buildIssueReviewDiffFiles(
	ctx context.Context,
	repoPath string,
	baseSHA string,
	headSHA string,
	repoRelativePath string,
) ([]projectCommitDiffFile, error) {
	baseSHA = strings.TrimSpace(baseSHA)
	headSHA = strings.TrimSpace(headSHA)
	repoRelativePath = strings.TrimSpace(repoRelativePath)
	if baseSHA == "" || headSHA == "" {
		return nil, fmt.Errorf("base_sha and head_sha are required")
	}
	if repoRelativePath == "" {
		return nil, fmt.Errorf("repo_relative_path is required")
	}
	if baseSHA == headSHA {
		return []projectCommitDiffFile{}, nil
	}

	rangeSpec := baseSHA + ".." + headSHA
	statusOutput, err := runGitInRepo(ctx, repoPath, "diff", "--name-status", rangeSpec, "--", repoRelativePath)
	if err != nil {
		return nil, err
	}
	patchOutput, err := runGitInRepo(ctx, repoPath, "diff", "--patch", "--no-color", rangeSpec, "--", repoRelativePath)
	if err != nil {
		return nil, err
	}

	files := parseIssueReviewNameStatus(statusOutput)
	patch := strings.TrimSpace(patchOutput)
	if patch != "" && len(files) > 0 {
		files[0].Patch = &patch
	}

	order := map[string]int{"added": 0, "modified": 1, "removed": 2, "renamed": 3}
	sort.SliceStable(files, func(i, j int) bool {
		left := files[i]
		right := files[j]
		if order[left.ChangeType] == order[right.ChangeType] {
			return left.Path < right.Path
		}
		return order[left.ChangeType] < order[right.ChangeType]
	})
	return files, nil
}

func parseIssueReviewNameStatus(output string) []projectCommitDiffFile {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	files := make([]projectCommitDiffFile, 0, len(lines))
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		status := strings.TrimSpace(fields[0])
		path := ""
		switch {
		case strings.HasPrefix(status, "R") && len(fields) >= 3:
			path = strings.TrimSpace(fields[2])
		default:
			path = strings.TrimSpace(fields[1])
		}
		if path == "" {
			continue
		}
		changeType := "modified"
		if len(status) > 0 {
			switch status[0] {
			case 'A':
				changeType = "added"
			case 'D':
				changeType = "removed"
			case 'R':
				changeType = "renamed"
			default:
				changeType = "modified"
			}
		}
		path = strings.ReplaceAll(path, "\\", "/")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		files = append(files, projectCommitDiffFile{
			Path:       path,
			ChangeType: changeType,
		})
	}
	return files
}
