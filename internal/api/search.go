package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

var (
	searchDB     *sql.DB
	searchDBErr  error
	searchDBOnce sync.Once

	uuidRegex = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
)

// SearchTaskResult represents a task in search results
type SearchTaskResult struct {
	ID                   string          `json:"id"`
	OrgID                string          `json:"org_id"`
	ProjectID            *string         `json:"project_id,omitempty"`
	Number               int32           `json:"number"`
	Title                string          `json:"title"`
	Description          *string         `json:"description,omitempty"`
	Status               string          `json:"status"`
	Priority             string          `json:"priority"`
	Context              json.RawMessage `json:"context"`
	AssignedAgentID      *string         `json:"assigned_agent_id,omitempty"`
	ParentTaskID         *string         `json:"parent_task_id,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	Rank                 float64         `json:"rank"`
	TitleHighlight       string          `json:"title_highlight"`
	DescriptionHighlight string          `json:"description_highlight"`
}

// SearchProjectResult represents a project in search results
type SearchProjectResult struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	NameHighlight        string  `json:"name_highlight,omitempty"`
	Description          *string `json:"description,omitempty"`
	DescriptionHighlight string  `json:"description_highlight,omitempty"`
	Status               string  `json:"status"`
	Rank                 float64 `json:"rank"`
}

// SearchAgentResult represents an agent in search results
type SearchAgentResult struct {
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	DisplayName          string  `json:"display_name"`
	DisplayNameHighlight string  `json:"display_name_highlight,omitempty"`
	Status               string  `json:"status"`
	Rank                 float64 `json:"rank"`
}

// SearchMessageResult represents a comment/message in search results
type SearchMessageResult struct {
	ID               string    `json:"id"`
	TaskID           string    `json:"task_id"`
	Content          string    `json:"content"`
	ContentHighlight string    `json:"content_highlight,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	Rank             float64   `json:"rank"`
}

// GlobalSearchResults contains all entity types
type GlobalSearchResults struct {
	Tasks    []SearchTaskResult    `json:"tasks"`
	Projects []SearchProjectResult `json:"projects"`
	Agents   []SearchAgentResult   `json:"agents"`
	Messages []SearchMessageResult `json:"messages"`
}

// GlobalSearchResponse is the response for the unified search endpoint
type GlobalSearchResponse struct {
	Query   string              `json:"query"`
	OrgID   string              `json:"org_id"`
	Results GlobalSearchResults `json:"results"`
}

// SearchResponse is the legacy response (tasks only)
type SearchResponse struct {
	Query   string             `json:"query"`
	OrgID   string             `json:"org_id"`
	Limit   int                `json:"limit"`
	Offset  int                `json:"offset"`
	Results []SearchTaskResult `json:"results"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// SQL queries for global search
const searchTasksSQL = `
WITH query AS (
    SELECT plainto_tsquery('english', $1) AS q
)
SELECT
    id,
    org_id,
    project_id,
    number,
    title,
    description,
    status,
    priority,
    context,
    assigned_agent_id,
    parent_task_id,
    created_at,
    updated_at,
    ts_rank(search_vector, query.q) AS rank,
    ts_headline('english', title, query.q, 'StartSel=<mark>,StopSel=</mark>') AS title_highlight,
    ts_headline('english', COALESCE(description, ''), query.q, 'StartSel=<mark>,StopSel=</mark>,MaxFragments=2,MaxWords=24,MinWords=8') AS description_highlight
FROM tasks, query
WHERE org_id = $2
  AND search_vector @@ query.q
ORDER BY rank DESC, updated_at DESC
LIMIT $3;
`

const searchProjectsSQL = `
SELECT
    id,
    name,
    description,
    status,
    similarity(name || ' ' || COALESCE(description, ''), $1) AS rank
FROM projects
WHERE org_id = $2
  AND (
    name ILIKE '%' || $1 || '%'
    OR description ILIKE '%' || $1 || '%'
  )
ORDER BY rank DESC, name ASC
LIMIT $3;
`

const searchAgentsSQL = `
SELECT
    id,
    slug,
    display_name,
    status,
    similarity(display_name || ' ' || slug, $1) AS rank
FROM agents
WHERE org_id = $2
  AND (
    display_name ILIKE '%' || $1 || '%'
    OR slug ILIKE '%' || $1 || '%'
  )
ORDER BY rank DESC, display_name ASC
LIMIT $3;
`

const searchMessagesSQL = `
SELECT
    c.id,
    c.task_id,
    c.content,
    c.created_at,
    similarity(c.content, $1) AS rank
FROM comments c
JOIN tasks t ON c.task_id = t.id
WHERE t.org_id = $2
  AND c.content ILIKE '%' || $1 || '%'
ORDER BY rank DESC, c.created_at DESC
LIMIT $3;
`

// Fallback queries without pg_trgm similarity (uses ILIKE only)
const searchProjectsFallbackSQL = `
SELECT
    id,
    name,
    description,
    status,
    CASE
        WHEN name ILIKE $1 || '%' THEN 1.0
        WHEN name ILIKE '%' || $1 || '%' THEN 0.8
        WHEN description ILIKE '%' || $1 || '%' THEN 0.5
        ELSE 0.3
    END AS rank
FROM projects
WHERE org_id = $2
  AND (
    name ILIKE '%' || $1 || '%'
    OR description ILIKE '%' || $1 || '%'
  )
ORDER BY rank DESC, name ASC
LIMIT $3;
`

const searchAgentsFallbackSQL = `
SELECT
    id,
    slug,
    display_name,
    status,
    CASE
        WHEN display_name ILIKE $1 || '%' THEN 1.0
        WHEN display_name ILIKE '%' || $1 || '%' THEN 0.8
        WHEN slug ILIKE '%' || $1 || '%' THEN 0.6
        ELSE 0.3
    END AS rank
FROM agents
WHERE org_id = $2
  AND (
    display_name ILIKE '%' || $1 || '%'
    OR slug ILIKE '%' || $1 || '%'
  )
ORDER BY rank DESC, display_name ASC
LIMIT $3;
`

const searchMessagesFallbackSQL = `
SELECT
    c.id,
    c.task_id,
    c.content,
    c.created_at,
    0.5 AS rank
FROM comments c
JOIN tasks t ON c.task_id = t.id
WHERE t.org_id = $2
  AND c.content ILIKE '%' || $1 || '%'
ORDER BY c.created_at DESC
LIMIT $3;
`

// SearchHandler handles GET /api/search?q=<query>&org_id=<uuid>
// Returns unified search results across tasks, projects, agents, and messages
// Supports demo mode with ?demo=true for testing without org_id
func SearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	isDemo := r.URL.Query().Get("demo") == "true"

	if q == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: q"})
		return
	}

	// Demo mode: return sample results without DB
	if isDemo || orgID == "" {
		demoResults := getDemoSearchResults(q)
		sendJSON(w, http.StatusOK, GlobalSearchResponse{
			Query:   q,
			OrgID:   "demo",
			Results: demoResults,
		})
		return
	}

	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), defaultSearchLimit)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	db, err := getSearchDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	ctx := r.Context()
	results := GlobalSearchResults{
		Tasks:    make([]SearchTaskResult, 0),
		Projects: make([]SearchProjectResult, 0),
		Agents:   make([]SearchAgentResult, 0),
		Messages: make([]SearchMessageResult, 0),
	}

	// Search tasks (uses full-text search)
	taskRows, err := db.QueryContext(ctx, searchTasksSQL, q, orgID, limit)
	if err == nil {
		defer taskRows.Close()
		for taskRows.Next() {
			var result SearchTaskResult
			var projectID, description, assignedAgentID, parentTaskID sql.NullString
			var contextBytes []byte

			if err := taskRows.Scan(
				&result.ID, &result.OrgID, &projectID, &result.Number, &result.Title,
				&description, &result.Status, &result.Priority, &contextBytes,
				&assignedAgentID, &parentTaskID, &result.CreatedAt, &result.UpdatedAt,
				&result.Rank, &result.TitleHighlight, &result.DescriptionHighlight,
			); err == nil {
				if projectID.Valid {
					result.ProjectID = &projectID.String
				}
				if description.Valid {
					result.Description = &description.String
				}
				if assignedAgentID.Valid {
					result.AssignedAgentID = &assignedAgentID.String
				}
				if parentTaskID.Valid {
					result.ParentTaskID = &parentTaskID.String
				}
				if len(contextBytes) == 0 {
					result.Context = json.RawMessage("{}")
				} else {
					result.Context = json.RawMessage(contextBytes)
				}
				results.Tasks = append(results.Tasks, result)
			}
		}
	}

	// Search projects (try with similarity, fallback to ILIKE)
	projectRows, err := db.QueryContext(ctx, searchProjectsFallbackSQL, q, orgID, limit)
	if err == nil {
		defer projectRows.Close()
		for projectRows.Next() {
			var result SearchProjectResult
			var description sql.NullString

			if err := projectRows.Scan(
				&result.ID, &result.Name, &description, &result.Status, &result.Rank,
			); err == nil {
				if description.Valid {
					result.Description = &description.String
				}
				result.NameHighlight = highlightMatches(result.Name, q)
				if result.Description != nil {
					result.DescriptionHighlight = highlightMatches(*result.Description, q)
				}
				results.Projects = append(results.Projects, result)
			}
		}
	}

	// Search agents
	agentRows, err := db.QueryContext(ctx, searchAgentsFallbackSQL, q, orgID, limit)
	if err == nil {
		defer agentRows.Close()
		for agentRows.Next() {
			var result SearchAgentResult

			if err := agentRows.Scan(
				&result.ID, &result.Slug, &result.DisplayName, &result.Status, &result.Rank,
			); err == nil {
				result.DisplayNameHighlight = highlightMatches(result.DisplayName, q)
				results.Agents = append(results.Agents, result)
			}
		}
	}

	// Search messages (comments)
	msgRows, err := db.QueryContext(ctx, searchMessagesFallbackSQL, q, orgID, limit)
	if err == nil {
		defer msgRows.Close()
		for msgRows.Next() {
			var result SearchMessageResult

			if err := msgRows.Scan(
				&result.ID, &result.TaskID, &result.Content, &result.CreatedAt, &result.Rank,
			); err == nil {
				result.ContentHighlight = highlightMatches(result.Content, q)
				results.Messages = append(results.Messages, result)
			}
		}
	}

	sendJSON(w, http.StatusOK, GlobalSearchResponse{
		Query:   q,
		OrgID:   orgID,
		Results: results,
	})
}

// highlightMatches wraps query matches in <mark> tags for frontend rendering
func highlightMatches(text, query string) string {
	if query == "" || text == "" {
		return text
	}

	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)

	idx := strings.Index(lowerText, lowerQuery)
	if idx == -1 {
		return text
	}

	// Build highlighted string preserving original case
	return text[:idx] + "<mark>" + text[idx:idx+len(query)] + "</mark>" + text[idx+len(query):]
}

func getSearchDB() (*sql.DB, error) {
	searchDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			searchDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			searchDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			searchDBErr = err
			return
		}

		searchDB = db
	})

	return searchDB, searchDBErr
}

func parsePositiveInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, errors.New("invalid")
	}

	return value, nil
}

func parseNonNegativeInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("invalid")
	}

	return value, nil
}

// getDemoSearchResults returns sample search results for demo mode
func getDemoSearchResults(query string) GlobalSearchResults {
	q := strings.ToLower(query)
	results := GlobalSearchResults{
		Tasks:    make([]SearchTaskResult, 0),
		Projects: make([]SearchProjectResult, 0),
		Agents:   make([]SearchAgentResult, 0),
		Messages: make([]SearchMessageResult, 0),
	}

	// Demo tasks
	demoTasks := []struct {
		id, title, desc, status, priority string
		number                            int32
	}{
		{"demo-1", "Deploy OtterCamp v1.0", "Final deployment and testing", "in_progress", "P1", 1},
		{"demo-2", "Review blog post draft", "Content review for publication", "review", "P2", 2},
		{"demo-3", "Schedule social media posts", "Queue up Twitter and LinkedIn", "done", "P3", 3},
		{"demo-4", "API documentation update", "Update OpenAPI specs", "queued", "P2", 4},
		{"demo-5", "Fix authentication flow", "Token refresh not working", "in_progress", "P1", 5},
	}

	for _, t := range demoTasks {
		if strings.Contains(strings.ToLower(t.title), q) || strings.Contains(strings.ToLower(t.desc), q) {
			desc := t.desc
			results.Tasks = append(results.Tasks, SearchTaskResult{
				ID:                   t.id,
				OrgID:                "demo",
				Number:               t.number,
				Title:                t.title,
				Description:          &desc,
				Status:               t.status,
				Priority:             t.priority,
				Context:              json.RawMessage("{}"),
				Rank:                 1.0,
				TitleHighlight:       highlightMatches(t.title, query),
				DescriptionHighlight: highlightMatches(t.desc, query),
			})
		}
	}

	// Demo projects
	demoProjects := []struct {
		id, name, desc, status string
	}{
		{"proj-1", "OtterCamp", "AI agent work management platform", "active"},
		{"proj-2", "Marketing", "Social media and content campaigns", "active"},
		{"proj-3", "Infrastructure", "DevOps and deployment automation", "active"},
	}

	for _, p := range demoProjects {
		if strings.Contains(strings.ToLower(p.name), q) || strings.Contains(strings.ToLower(p.desc), q) {
			desc := p.desc
			results.Projects = append(results.Projects, SearchProjectResult{
				ID:                   p.id,
				Name:                 p.name,
				NameHighlight:        highlightMatches(p.name, query),
				Description:          &desc,
				DescriptionHighlight: highlightMatches(p.desc, query),
				Status:               p.status,
				Rank:                 1.0,
			})
		}
	}

	// Demo agents
	demoAgents := []struct {
		id, slug, displayName, status string
	}{
		{"agent-frank", "frank", "Frank", "online"},
		{"agent-derek", "derek", "Derek", "online"},
		{"agent-nova", "nova", "Nova", "online"},
		{"agent-stone", "stone", "Stone", "busy"},
		{"agent-ivy", "ivy", "Ivy", "online"},
		{"agent-jeff", "jeff-g", "Jeff G", "online"},
	}

	for _, a := range demoAgents {
		if strings.Contains(strings.ToLower(a.displayName), q) || strings.Contains(strings.ToLower(a.slug), q) {
			results.Agents = append(results.Agents, SearchAgentResult{
				ID:                   a.id,
				Slug:                 a.slug,
				DisplayName:          a.displayName,
				DisplayNameHighlight: highlightMatches(a.displayName, query),
				Status:               a.status,
				Rank:                 1.0,
			})
		}
	}

	return results
}
