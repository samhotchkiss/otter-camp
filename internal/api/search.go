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
LIMIT $3 OFFSET $4;
`

// SearchHandler handles GET /api/search?q=<query>&org_id=<uuid>
func SearchHandler(w http.ResponseWriter, r *http.Request) {
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

	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), defaultSearchLimit)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	offset, err := parseNonNegativeInt(r.URL.Query().Get("offset"), 0)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid offset"})
		return
	}

	db, err := getSearchDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	rows, err := db.QueryContext(r.Context(), searchTasksSQL, q, orgID, limit, offset)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search query failed"})
		return
	}
	defer rows.Close()

	results := make([]SearchTaskResult, 0)
	for rows.Next() {
		var result SearchTaskResult
		var projectID sql.NullString
		var description sql.NullString
		var assignedAgentID sql.NullString
		var parentTaskID sql.NullString
		var contextBytes []byte

		err := rows.Scan(
			&result.ID,
			&result.OrgID,
			&projectID,
			&result.Number,
			&result.Title,
			&description,
			&result.Status,
			&result.Priority,
			&contextBytes,
			&assignedAgentID,
			&parentTaskID,
			&result.CreatedAt,
			&result.UpdatedAt,
			&result.Rank,
			&result.TitleHighlight,
			&result.DescriptionHighlight,
		)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read search results"})
			return
		}

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

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search results error"})
		return
	}

	sendJSON(w, http.StatusOK, SearchResponse{
		Query:   q,
		OrgID:   orgID,
		Limit:   limit,
		Offset:  offset,
		Results: results,
	})
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
