package api

import (
	"net/http"
	"strings"
)

const commandSearchLimit = 10

type CommandSearchResult struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Label     string  `json:"label"`
	ActionURL string  `json:"action_url"`
	Rank      float64 `json:"rank"`
}

type CommandSearchResponse struct {
	Query   string                `json:"query"`
	OrgID   string                `json:"org_id"`
	Results []CommandSearchResult `json:"results"`
}

const commandSearchSQL = `
WITH query AS (
    SELECT plainto_tsquery('english', $1) AS q
)
SELECT type, id, label, action_url, rank
FROM (
    SELECT
        'task' AS type,
        id::text AS id,
        title AS label,
        format('/tasks/%s', number) AS action_url,
        ts_rank(search_vector, query.q) AS rank,
        updated_at AS sort_time
    FROM tasks, query
    WHERE org_id = $2
      AND search_vector @@ query.q

    UNION ALL

    SELECT
        'project' AS type,
        id::text AS id,
        name AS label,
        format('/projects/%s', id) AS action_url,
        ts_rank(
            to_tsvector('english', COALESCE(name, '') || ' ' || COALESCE(description, '')),
            query.q
        ) AS rank,
        updated_at AS sort_time
    FROM projects, query
    WHERE org_id = $2
      AND to_tsvector('english', COALESCE(name, '') || ' ' || COALESCE(description, '')) @@ query.q

    UNION ALL

    SELECT
        'agent' AS type,
        id::text AS id,
        display_name AS label,
        format('/agents/%s', slug) AS action_url,
        ts_rank(
            to_tsvector('english', COALESCE(display_name, '') || ' ' || COALESCE(slug, '')),
            query.q
        ) AS rank,
        updated_at AS sort_time
    FROM agents, query
    WHERE org_id = $2
      AND to_tsvector('english', COALESCE(display_name, '') || ' ' || COALESCE(slug, '')) @@ query.q

    UNION ALL

    SELECT
        'activity' AS type,
        id::text AS id,
        action AS label,
        format('/activity/%s', id) AS action_url,
        ts_rank(
            to_tsvector('english', COALESCE(action, '') || ' ' || COALESCE(metadata::text, '')),
            query.q
        ) AS rank,
        created_at AS sort_time
    FROM activity_log, query
    WHERE org_id = $2
      AND to_tsvector('english', COALESCE(action, '') || ' ' || COALESCE(metadata::text, '')) @@ query.q
) results
ORDER BY rank DESC, sort_time DESC
LIMIT $3;
`

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

	rows, err := db.QueryContext(r.Context(), commandSearchSQL, q, orgID, commandSearchLimit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search query failed"})
		return
	}
	defer rows.Close()

	results := make([]CommandSearchResult, 0)
	for rows.Next() {
		var result CommandSearchResult
		if err := rows.Scan(&result.Type, &result.ID, &result.Label, &result.ActionURL, &result.Rank); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read search results"})
			return
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "search results error"})
		return
	}

	sendJSON(w, http.StatusOK, CommandSearchResponse{
		Query:   q,
		OrgID:   orgID,
		Results: results,
	})
}
