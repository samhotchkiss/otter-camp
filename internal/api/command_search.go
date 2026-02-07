package api

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	commandSearchLimit          = 10
	commandSearchCandidateLimit = 50
)

type CommandSearchResult struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Action      string `json:"action"`
}

type CommandSearchResponse struct {
	Query   string                `json:"query"`
	OrgID   string                `json:"org_id"`
	Results []CommandSearchResult `json:"results"`
}

const commandSearchTasksSQL = `
SELECT
    id::text,
    number,
    title,
    COALESCE(description, '') AS description,
    updated_at
FROM tasks
WHERE org_id = $1
  AND (
    title ILIKE $2 ESCAPE '\\'
    OR COALESCE(description, '') ILIKE $2 ESCAPE '\\'
  )
ORDER BY updated_at DESC
LIMIT $3;
`

const commandSearchProjectsSQL = `
SELECT
    id::text,
    name,
    COALESCE(description, '') AS description,
    updated_at
FROM projects
WHERE org_id = $1
  AND (
    name ILIKE $2 ESCAPE '\\'
    OR COALESCE(description, '') ILIKE $2 ESCAPE '\\'
  )
ORDER BY updated_at DESC
LIMIT $3;
`

const commandSearchAgentsSQL = `
SELECT
    id::text,
    slug,
    display_name,
    updated_at
FROM agents
WHERE org_id = $1
  AND (
    display_name ILIKE $2 ESCAPE '\\'
    OR slug ILIKE $2 ESCAPE '\\'
  )
ORDER BY updated_at DESC
LIMIT $3;
`

const commandSearchRecentSQL = `
SELECT
    al.id::text,
    al.action,
    COALESCE(al.metadata::text, '') AS metadata_text,
    al.created_at,
    t.number,
    a.slug
FROM activity_log al
LEFT JOIN tasks t ON al.task_id = t.id
LEFT JOIN agents a ON al.agent_id = a.id
WHERE al.org_id = $1
  AND (
    al.action ILIKE $2 ESCAPE '\\'
    OR COALESCE(al.metadata::text, '') ILIKE $2 ESCAPE '\\'
  )
ORDER BY al.created_at DESC
LIMIT $3;
`

type commandSearchCandidate struct {
	result   CommandSearchResult
	score    float64
	sortTime time.Time
}

// CommandSearchHandler handles GET /api/commands/search?q=<query>&org_id=<uuid>
func CommandSearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))

	if q == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: q"})
		return
	}
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	db, err := getSearchDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	ctx := r.Context()
	pattern := buildFuzzyPattern(q)
	candidates := make([]commandSearchCandidate, 0, commandSearchCandidateLimit*4)

	taskRows, err := db.QueryContext(ctx, commandSearchTasksSQL, orgID, pattern, commandSearchCandidateLimit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search query failed"})
		return
	}
	defer taskRows.Close()

	for taskRows.Next() {
		var id string
		var number int32
		var title string
		var description string
		var updatedAt time.Time

		if err := taskRows.Scan(&id, &number, &title, &description, &updatedAt); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read search results"})
			return
		}

		desc := strings.TrimSpace(description)
		if desc == "" {
			desc = fmt.Sprintf("Task #%d", number)
		}
		result := CommandSearchResult{
			ID:          id,
			Type:        "task",
			Title:       title,
			Description: desc,
			Action:      fmt.Sprintf("/tasks/%d", number),
		}
		score := commandSearchScore(result, q)
		if score >= 0 {
			candidates = append(candidates, commandSearchCandidate{
				result:   result,
				score:    score,
				sortTime: updatedAt,
			})
		}
	}

	if err := taskRows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search results error"})
		return
	}

	projectRows, err := db.QueryContext(ctx, commandSearchProjectsSQL, orgID, pattern, commandSearchCandidateLimit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search query failed"})
		return
	}
	defer projectRows.Close()

	for projectRows.Next() {
		var id string
		var name string
		var description string
		var updatedAt time.Time

		if err := projectRows.Scan(&id, &name, &description, &updatedAt); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read search results"})
			return
		}

		desc := strings.TrimSpace(description)
		if desc == "" {
			desc = "Project"
		}
		result := CommandSearchResult{
			ID:          id,
			Type:        "project",
			Title:       name,
			Description: desc,
			Action:      fmt.Sprintf("/projects/%s", id),
		}
		score := commandSearchScore(result, q)
		if score >= 0 {
			candidates = append(candidates, commandSearchCandidate{
				result:   result,
				score:    score,
				sortTime: updatedAt,
			})
		}
	}

	if err := projectRows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search results error"})
		return
	}

	agentRows, err := db.QueryContext(ctx, commandSearchAgentsSQL, orgID, pattern, commandSearchCandidateLimit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search query failed"})
		return
	}
	defer agentRows.Close()

	for agentRows.Next() {
		var id string
		var slug string
		var displayName string
		var updatedAt time.Time

		if err := agentRows.Scan(&id, &slug, &displayName, &updatedAt); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read search results"})
			return
		}

		result := CommandSearchResult{
			ID:          id,
			Type:        "agent",
			Title:       displayName,
			Description: slug,
			Action:      fmt.Sprintf("/agents/%s", slug),
		}
		score := commandSearchScore(result, q)
		if score >= 0 {
			candidates = append(candidates, commandSearchCandidate{
				result:   result,
				score:    score,
				sortTime: updatedAt,
			})
		}
	}

	if err := agentRows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search results error"})
		return
	}

	recentRows, err := db.QueryContext(ctx, commandSearchRecentSQL, orgID, pattern, commandSearchCandidateLimit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search query failed"})
		return
	}
	defer recentRows.Close()

	for recentRows.Next() {
		var id string
		var actionLabel string
		var metadata string
		var createdAt time.Time
		var taskNumber sql.NullInt32
		var agentSlug sql.NullString

		if err := recentRows.Scan(&id, &actionLabel, &metadata, &createdAt, &taskNumber, &agentSlug); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read search results"})
			return
		}

		action := "/feed"
		if taskNumber.Valid {
			action = fmt.Sprintf("/tasks/%d", taskNumber.Int32)
		} else if agentSlug.Valid && strings.TrimSpace(agentSlug.String) != "" {
			action = fmt.Sprintf("/agents/%s", agentSlug.String)
		}

		desc := strings.TrimSpace(metadata)
		if desc == "" {
			desc = "Recent activity"
		}
		result := CommandSearchResult{
			ID:          id,
			Type:        "recent",
			Title:       actionLabel,
			Description: desc,
			Action:      action,
		}
		score := commandSearchScore(result, q)
		if score >= 0 {
			candidates = append(candidates, commandSearchCandidate{
				result:   result,
				score:    score,
				sortTime: createdAt,
			})
		}
	}

	if err := recentRows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search results error"})
		return
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].sortTime.After(candidates[j].sortTime)
		}
		return candidates[i].score > candidates[j].score
	})

	results := make([]CommandSearchResult, 0, commandSearchLimit)
	for _, candidate := range candidates {
		if len(results) >= commandSearchLimit {
			break
		}
		results = append(results, candidate.result)
	}

	sendJSON(w, http.StatusOK, CommandSearchResponse{
		Query:   q,
		OrgID:   orgID,
		Results: results,
	})
}

func buildFuzzyPattern(query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "%"
	}

	trimmed = strings.ReplaceAll(trimmed, " ", "")

	var builder strings.Builder
	builder.Grow(len(trimmed) * 2)
	builder.WriteString("%")
	for _, r := range trimmed {
		switch r {
		case '%', '_', '\\':
			builder.WriteRune('\\')
		}
		builder.WriteRune(r)
		builder.WriteString("%")
	}

	return builder.String()
}

func commandSearchScore(result CommandSearchResult, query string) float64 {
	return maxFloat64(
		fuzzyScore(result.Title, query),
		fuzzyScore(result.Description, query),
		fuzzyScore(result.Type, query),
	)
}

func fuzzyScore(text, query string) float64 {
	source := []rune(strings.ToLower(strings.TrimSpace(text)))
	needle := []rune(strings.ToLower(strings.TrimSpace(query)))

	if len(needle) == 0 {
		return 1
	}

	score := 0.0
	lastIndex := -1

	for _, char := range needle {
		index := -1
		for i := lastIndex + 1; i < len(source); i++ {
			if source[i] == char {
				index = i
				break
			}
		}

		if index == -1 {
			return -1
		}

		distance := index - lastIndex
		if distance > 12 {
			distance = 12
		}
		score += float64(12 - distance)
		lastIndex = index
	}

	diff := len(source) - len(needle)
	if diff < 0 {
		diff = 0
	}
	score -= math.Max(float64(diff), 0) * 0.15

	return score
}

func maxFloat64(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}

	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	return max
}
