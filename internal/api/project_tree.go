package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type ProjectTreeHandler struct {
	ProjectStore *store.ProjectStore
	ProjectRepos *store.ProjectRepoStore
}

type projectTreeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
	Size *int64 `json:"size,omitempty"`
}

type projectTreeResponse struct {
	Ref     string             `json:"ref"`
	Path    string             `json:"path"`
	Entries []projectTreeEntry `json:"entries"`
}

type projectBlobResponse struct {
	Ref      string `json:"ref"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding"`
}

type gitRepoMode string

const (
	gitRepoModeWorktree gitRepoMode = "worktree"
	gitRepoModeBare     gitRepoMode = "bare"
	noRepoConfiguredMsg             = "No repository configured for this project"
)

var errProjectRepoNotConfigured = errors.New("project repository is not configured")
var errRepositoryEmpty = errors.New("repository has no commits")

func (h *ProjectTreeHandler) GetTree(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	repoPath, repoMode, defaultRef, err := h.resolveBrowseRepository(r.Context(), projectID)
	if err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	refProvided := ref != ""
	if ref == "" {
		ref = defaultRef
	}
	if ref == "" {
		ref = "HEAD"
	}

	repoPathQuery := r.URL.Query().Get("path")
	if strings.TrimSpace(repoPathQuery) == "" {
		repoPathQuery = "/"
	}
	normalizedPath, err := normalizeRepositoryPath(repoPathQuery)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	resolvedRef, output, err := readTreeListingForBrowse(r.Context(), repoPath, repoMode, ref, normalizedPath, !refProvided)
	if err != nil {
		if errors.Is(err, errRepositoryEmpty) {
			responsePath := "/"
			if normalizedPath != "" {
				responsePath = "/" + normalizedPath
			}
			sendJSON(w, http.StatusOK, projectTreeResponse{
				Ref:     ref,
				Path:    responsePath,
				Entries: []projectTreeEntry{},
			})
			return
		}
		status, message := classifyGitBrowseError(err)
		sendJSON(w, status, errorResponse{Error: message})
		return
	}

	entries, err := parseTreeEntries(output, normalizedPath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to parse tree listing"})
		return
	}

	responsePath := "/"
	if normalizedPath != "" {
		responsePath = "/" + normalizedPath
	}

	sendJSON(w, http.StatusOK, projectTreeResponse{
		Ref:     resolvedRef,
		Path:    responsePath,
		Entries: entries,
	})
}

func (h *ProjectTreeHandler) GetBlob(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	repoPath, repoMode, defaultRef, err := h.resolveBrowseRepository(r.Context(), projectID)
	if err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	refProvided := ref != ""
	if ref == "" {
		ref = defaultRef
	}
	if ref == "" {
		ref = "HEAD"
	}

	rawPath := r.URL.Query().Get("path")
	if strings.TrimSpace(rawPath) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "path is required"})
		return
	}
	normalizedPath, err := normalizeRepositoryPath(rawPath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if normalizedPath == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "path must point to a file"})
		return
	}

	resolvedRef, contentBytes, err := readBlobForBrowse(r.Context(), repoPath, repoMode, ref, normalizedPath, !refProvided)
	if err != nil {
		status, message := classifyGitBrowseError(err)
		sendJSON(w, status, errorResponse{Error: message})
		return
	}

	encoding := "base64"
	content := base64.StdEncoding.EncodeToString(contentBytes)
	if utf8.Valid(contentBytes) && !bytes.Contains(contentBytes, []byte{0}) {
		encoding = "utf-8"
		content = string(contentBytes)
	}

	sendJSON(w, http.StatusOK, projectBlobResponse{
		Ref:      resolvedRef,
		Path:     "/" + normalizedPath,
		Content:  content,
		Size:     int64(len(contentBytes)),
		Encoding: encoding,
	})
}

func (h *ProjectTreeHandler) resolveBrowseRepository(
	ctx context.Context,
	projectID string,
) (string, gitRepoMode, string, error) {
	project, err := h.ProjectStore.GetByID(ctx, projectID)
	if err != nil {
		return "", "", "", err
	}

	repoPath := optionalStringValue(project.LocalRepoPath)
	defaultRef := "HEAD"

	if h.ProjectRepos != nil {
		binding, bindErr := h.ProjectRepos.GetBinding(ctx, projectID)
		if bindErr != nil {
			if !errors.Is(bindErr, store.ErrNotFound) {
				return "", "", "", bindErr
			}
		} else if binding != nil && binding.Enabled {
			if binding.LocalRepoPath != nil && strings.TrimSpace(*binding.LocalRepoPath) != "" {
				repoPath = strings.TrimSpace(*binding.LocalRepoPath)
			}
			if strings.TrimSpace(binding.DefaultBranch) != "" {
				defaultRef = strings.TrimSpace(binding.DefaultBranch)
			}
		}
	}

	if strings.TrimSpace(repoPath) == "" {
		bootstrappedRepoPath, pathErr := h.ProjectStore.GetRepoPath(ctx, projectID)
		if pathErr != nil {
			return "", "", "", pathErr
		}
		repoPath = strings.TrimSpace(bootstrappedRepoPath)
	}

	if strings.TrimSpace(repoPath) == "" {
		return "", "", "", errProjectRepoNotConfigured
	}

	repoMode, err := detectGitRepositoryMode(repoPath)
	if err != nil {
		return "", "", "", err
	}
	return repoPath, repoMode, defaultRef, nil
}

func readTreeListingForBrowse(
	ctx context.Context,
	repoPath string,
	repoMode gitRepoMode,
	ref string,
	normalizedPath string,
	allowHeadFallback bool,
) (string, []byte, error) {
	candidateRefs := browseRefCandidates(ref, allowHeadFallback)
	seenRefs := make(map[string]struct{}, len(candidateRefs))
	for _, candidateRef := range candidateRefs {
		seenRefs[candidateRef] = struct{}{}
		resolvedRef, output, err := readTreeListingForBrowseRef(ctx, repoPath, repoMode, candidateRef, normalizedPath)
		if err == nil {
			return resolvedRef, output, nil
		}
		if allowHeadFallback && candidateRef != "HEAD" && isRefOrPathNotFoundError(err) {
			continue
		}
		if candidateRef == "HEAD" && isRepositoryWithoutCommitsError(err) {
			continue
		}
		return "", nil, err
	}

	if allowHeadFallback {
		branchRefs, err := listGitLocalBranches(ctx, repoPath, repoMode)
		if err == nil {
			for _, branchRef := range branchRefs {
				if _, alreadyTried := seenRefs[branchRef]; alreadyTried {
					continue
				}
				resolvedRef, output, branchErr := readTreeListingForBrowseRef(ctx, repoPath, repoMode, branchRef, normalizedPath)
				if branchErr == nil {
					return resolvedRef, output, nil
				}
				if !isRefOrPathNotFoundError(branchErr) {
					return "", nil, branchErr
				}
			}
		}
	}

	isEmpty, emptyErr := isRepositoryEmpty(ctx, repoPath, repoMode)
	if emptyErr == nil && isEmpty {
		return "", nil, errRepositoryEmpty
	}
	return "", nil, fmt.Errorf("ref or path not found")
}

func readTreeListingForBrowseRef(
	ctx context.Context,
	repoPath string,
	repoMode gitRepoMode,
	candidateRef string,
	normalizedPath string,
) (string, []byte, error) {
	objectSpec := candidateRef
	if normalizedPath != "" {
		objectSpec = candidateRef + ":" + normalizedPath
		objectType, typeErr := readGitObjectType(ctx, repoPath, repoMode, objectSpec)
		if typeErr != nil {
			return "", nil, typeErr
		}
		if objectType != "tree" {
			return "", nil, fmt.Errorf("path must point to a directory")
		}
	}

	output, err := runGitBrowseCommand(ctx, repoPath, repoMode, "ls-tree", "-z", "--long", objectSpec)
	if err != nil {
		return "", nil, err
	}
	return candidateRef, output, nil
}

func listGitLocalBranches(
	ctx context.Context,
	repoPath string,
	repoMode gitRepoMode,
) ([]string, error) {
	output, err := runGitBrowseCommand(
		ctx,
		repoPath,
		repoMode,
		"for-each-ref",
		"--format=%(refname:short)",
		"refs/heads",
	)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	refs := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		refs = append(refs, trimmed)
	}
	return refs, nil
}

func isRepositoryEmpty(ctx context.Context, repoPath string, repoMode gitRepoMode) (bool, error) {
	_, err := runGitBrowseCommand(ctx, repoPath, repoMode, "rev-parse", "--verify", "HEAD^{commit}")
	if err == nil {
		return false, nil
	}
	if isRepositoryWithoutCommitsError(err) {
		return true, nil
	}
	return false, err
}

func isRepositoryWithoutCommitsError(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "not a valid object name") ||
		strings.Contains(message, "needed a single revision") ||
		strings.Contains(message, "unknown revision or path not in the working tree") ||
		strings.Contains(message, "bad revision 'head")
}

func readBlobForBrowse(
	ctx context.Context,
	repoPath string,
	repoMode gitRepoMode,
	ref string,
	normalizedPath string,
	allowHeadFallback bool,
) (string, []byte, error) {
	for _, candidateRef := range browseRefCandidates(ref, allowHeadFallback) {
		objectSpec := candidateRef + ":" + normalizedPath
		objectType, err := readGitObjectType(ctx, repoPath, repoMode, objectSpec)
		if err != nil {
			if allowHeadFallback && candidateRef != "HEAD" && isRefOrPathNotFoundError(err) {
				continue
			}
			return "", nil, err
		}
		if objectType != "blob" {
			return "", nil, fmt.Errorf("path must point to a file")
		}

		contentBytes, err := runGitBrowseCommand(ctx, repoPath, repoMode, "show", objectSpec)
		if err == nil {
			return candidateRef, contentBytes, nil
		}
		if allowHeadFallback && candidateRef != "HEAD" && isRefOrPathNotFoundError(err) {
			continue
		}
		return "", nil, err
	}
	return "", nil, fmt.Errorf("ref or path not found")
}

func browseRefCandidates(ref string, allowHeadFallback bool) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	candidates := []string{ref}
	if allowHeadFallback && !strings.EqualFold(ref, "HEAD") {
		candidates = append(candidates, "HEAD")
	}
	return candidates
}

func isRefOrPathNotFoundError(err error) bool {
	status, _ := classifyGitBrowseError(err)
	return status == http.StatusNotFound
}

func normalizeRepositoryPath(raw string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("invalid path")
	}
	if value == "" || value == "/" {
		return "", nil
	}

	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == ".." {
			return "", fmt.Errorf("path traversal is not allowed")
		}
	}

	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	cleaned := path.Clean(value)
	if cleaned == "/" || cleaned == "." {
		return "", nil
	}
	return strings.TrimPrefix(cleaned, "/"), nil
}

func detectGitRepositoryMode(repoPath string) (gitRepoMode, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return "", fmt.Errorf("project repository path is not configured")
	}

	info, err := os.Stat(repoPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("project repository path does not exist")
		}
		return "", fmt.Errorf("failed to inspect project repository path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project repository path is not a directory")
	}

	if nestedGitInfo, nestedErr := os.Stat(pathJoinOS(repoPath, ".git")); nestedErr == nil && nestedGitInfo.IsDir() {
		return gitRepoModeWorktree, nil
	}

	headPath := pathJoinOS(repoPath, "HEAD")
	objectsPath := pathJoinOS(repoPath, "objects")
	if _, headErr := os.Stat(headPath); headErr == nil {
		if objectsInfo, objectsErr := os.Stat(objectsPath); objectsErr == nil && objectsInfo.IsDir() {
			return gitRepoModeBare, nil
		}
	}

	return "", fmt.Errorf("project repository path is not a git repository")
}

func readGitObjectType(
	ctx context.Context,
	repoPath string,
	repoMode gitRepoMode,
	objectSpec string,
) (string, error) {
	output, err := runGitBrowseCommand(ctx, repoPath, repoMode, "cat-file", "-t", objectSpec)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func runGitBrowseCommand(
	ctx context.Context,
	repoPath string,
	repoMode gitRepoMode,
	args ...string,
) ([]byte, error) {
	var command *exec.Cmd
	switch repoMode {
	case gitRepoModeWorktree:
		command = exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	case gitRepoModeBare:
		command = exec.CommandContext(ctx, "git", append([]string{"--git-dir=" + repoPath}, args...)...)
	default:
		return nil, fmt.Errorf("unsupported repository mode")
	}
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	output, err := command.Output()
	if err == nil {
		return output, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if stderr == "" {
			stderr = strings.TrimSpace(string(output))
		}
		if stderr == "" {
			stderr = "unknown git error"
		}
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), stderr)
	}
	return nil, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
}

func classifyGitBrowseError(err error) (int, string) {
	if errors.Is(err, errProjectRepoNotConfigured) {
		return http.StatusConflict, noRepoConfiguredMsg
	}

	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "not a valid object name"),
		strings.Contains(lower, "invalid object name"):
		return http.StatusNotFound, "ref or path not found"
	case strings.Contains(lower, "path") && strings.Contains(lower, "does not exist"):
		return http.StatusNotFound, "ref or path not found"
	case strings.Contains(lower, "exists on disk, but not in"):
		return http.StatusNotFound, "ref or path not found"
	case strings.Contains(lower, "repository path does not exist"),
		strings.Contains(lower, "repository path is not"),
		strings.Contains(lower, "repository path is not configured"):
		if strings.Contains(lower, "repository path is not configured") {
			return http.StatusConflict, noRepoConfiguredMsg
		}
		return http.StatusConflict, message
	default:
		return http.StatusBadRequest, message
	}
}

func parseTreeEntries(output []byte, basePath string) ([]projectTreeEntry, error) {
	records := bytes.Split(output, []byte{0})
	entries := make([]projectTreeEntry, 0, len(records))
	basePath = strings.Trim(strings.TrimSpace(basePath), "/")

	for _, raw := range records {
		if len(raw) == 0 {
			continue
		}
		tabIndex := bytes.IndexByte(raw, '\t')
		if tabIndex <= 0 || tabIndex >= len(raw)-1 {
			continue
		}

		meta := strings.Fields(string(raw[:tabIndex]))
		if len(meta) < 3 {
			continue
		}
		objectType := strings.TrimSpace(meta[1])
		name := strings.TrimSpace(string(raw[tabIndex+1:]))
		if name == "" {
			continue
		}

		entryPath := name
		if basePath != "" {
			entryPath = basePath + "/" + name
		}

		switch objectType {
		case "tree":
			if !strings.HasSuffix(entryPath, "/") {
				entryPath += "/"
			}
			entries = append(entries, projectTreeEntry{
				Name: name,
				Type: "dir",
				Path: entryPath,
			})
		case "blob", "commit":
			var sizePtr *int64
			if objectType == "blob" && len(meta) >= 4 {
				sizeToken := strings.TrimSpace(meta[3])
				if sizeToken != "" && sizeToken != "-" {
					if parsedSize, err := strconv.ParseInt(sizeToken, 10, 64); err == nil {
						sizePtr = &parsedSize
					}
				}
			}
			entries = append(entries, projectTreeEntry{
				Name: name,
				Type: "file",
				Path: entryPath,
				Size: sizePtr,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	return entries, nil
}

func pathJoinOS(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	joined := parts[0]
	for _, part := range parts[1:] {
		if strings.HasSuffix(joined, string(os.PathSeparator)) {
			joined += part
		} else {
			joined += string(os.PathSeparator) + part
		}
	}
	return joined
}
