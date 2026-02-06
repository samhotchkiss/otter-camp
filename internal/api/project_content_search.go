package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type projectContentSearchResult struct {
	Path       string  `json:"path"`
	Snippet    string  `json:"snippet"`
	Score      float64 `json:"score"`
	ModifiedAt string  `json:"modified_at"`
	Author     *string `json:"author,omitempty"`
	Scope      string  `json:"scope"`
}

type projectContentSearchResponse struct {
	Items []projectContentSearchResult `json:"items"`
	Total int                          `json:"total"`
}

var allowedContentSearchExtensions = map[string]struct{}{
	".md":       {},
	".markdown": {},
	".txt":      {},
}

func (h *ProjectChatHandler) SearchContent(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chiURLParamProjectID(r))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "q is required"})
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"), 25, 100)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}

	scope := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("scope")))
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "notes" && scope != "posts" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "scope must be one of all, notes, posts"})
		return
	}

	var from *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		value, err := parseDateTime(raw)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid from date"})
			return
		}
		from = &value
	}

	var to *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		value, err := parseDateTime(raw)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid to date"})
			return
		}
		to = &value
	}
	if from != nil && to != nil && from.After(*to) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "from must be before to"})
		return
	}

	var authorFilter *string
	if author := strings.TrimSpace(r.URL.Query().Get("author")); author != "" {
		authorFilter = &author
	}

	results, err := searchProjectContent(projectID, query, scope, authorFilter, from, to, limit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to search content"})
		return
	}

	sendJSON(w, http.StatusOK, projectContentSearchResponse{Items: results, Total: len(results)})
}

func searchProjectContent(
	projectID, query, scope string,
	authorFilter *string,
	from, to *time.Time,
	limit int,
) ([]projectContentSearchResult, error) {
	root := contentRootPath()
	projectRoot := filepath.Join(root, projectID)
	scopes := []string{"notes", "posts"}
	if scope == "notes" || scope == "posts" {
		scopes = []string{scope}
	}

	results := make([]projectContentSearchResult, 0)
	for _, contentScope := range scopes {
		dirPath := filepath.Join(projectRoot, contentScope)
		if stat, err := os.Stat(dirPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		} else if !stat.IsDir() {
			continue
		}

		err := filepath.WalkDir(dirPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if _, ok := allowedContentSearchExtensions[ext]; !ok {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}
			modifiedAt := info.ModTime().UTC()
			if from != nil && modifiedAt.Before(from.UTC()) {
				return nil
			}
			if to != nil && modifiedAt.After(to.UTC()) {
				return nil
			}

			contentBytes, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content := string(contentBytes)
			author := extractContentAuthor(content)
			if authorFilter != nil {
				if author == nil || !strings.EqualFold(strings.TrimSpace(*authorFilter), strings.TrimSpace(*author)) {
					return nil
				}
			}

			score, snippet := scoreAndSnippet(content, query)
			if score <= 0 {
				return nil
			}

			relativePath, err := filepath.Rel(projectRoot, path)
			if err != nil {
				return err
			}
			relativePath = "/" + filepath.ToSlash(relativePath)
			if !strings.HasPrefix(relativePath, "/notes/") && !strings.HasPrefix(relativePath, "/posts/") {
				return nil
			}

			results = append(results, projectContentSearchResult{
				Path:       relativePath,
				Snippet:    snippet,
				Score:      score,
				ModifiedAt: modifiedAt.Format(time.RFC3339),
				Author:     author,
				Scope:      contentScope,
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			if results[i].ModifiedAt == results[j].ModifiedAt {
				return results[i].Path < results[j].Path
			}
			return results[i].ModifiedAt > results[j].ModifiedAt
		}
		return results[i].Score > results[j].Score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func scoreAndSnippet(content, query string) (float64, string) {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return 0, ""
	}

	normalizedContent := strings.ToLower(trimmedContent)
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	if normalizedQuery == "" {
		return 0, ""
	}

	score := 0.0
	exactMatches := strings.Count(normalizedContent, normalizedQuery)
	score += float64(exactMatches * 10)

	tokens := strings.Fields(normalizedQuery)
	for _, token := range tokens {
		tokenMatches := strings.Count(normalizedContent, token)
		score += float64(tokenMatches)
		headingMatches := headingTokenMatches(trimmedContent, token)
		score += float64(headingMatches * 2)
	}

	if score <= 0 {
		return 0, ""
	}

	index := strings.Index(normalizedContent, normalizedQuery)
	if index < 0 {
		for _, token := range tokens {
			index = strings.Index(normalizedContent, token)
			if index >= 0 {
				break
			}
		}
	}
	if index < 0 {
		index = 0
	}

	start := index - 60
	if start < 0 {
		start = 0
	}
	end := index + 140
	if end > len(trimmedContent) {
		end = len(trimmedContent)
	}
	snippet := strings.TrimSpace(strings.ReplaceAll(trimmedContent[start:end], "\n", " "))
	if snippet == "" {
		snippet = strings.TrimSpace(trimmedContent)
	}
	return score, snippet
}

func headingTokenMatches(content, token string) int {
	pattern := regexp.MustCompile(`(?mi)^#{1,6}\s+.*` + regexp.QuoteMeta(token) + `.*$`)
	return len(pattern.FindAllString(content, -1))
}

func extractContentAuthor(content string) *string {
	frontmatterAuthorPattern := regexp.MustCompile(`(?mi)^author:\s*([^\n\r]+)$`)
	if match := frontmatterAuthorPattern.FindStringSubmatch(content); len(match) == 2 {
		author := strings.TrimSpace(match[1])
		if author != "" {
			return &author
		}
	}

	sourceAuthorPattern := regexp.MustCompile(`author=([A-Za-z0-9_.-]+)`)
	if match := sourceAuthorPattern.FindStringSubmatch(content); len(match) == 2 {
		author := strings.TrimSpace(strings.ReplaceAll(match[1], "_", " "))
		if author != "" {
			return &author
		}
	}

	return nil
}

func chiURLParamProjectID(r *http.Request) string {
	value := strings.TrimSpace(chi.URLParam(r, "id"))
	if value == "" {
		value = strings.TrimSpace(chi.URLParam(r, "projectID"))
	}
	return value
}
