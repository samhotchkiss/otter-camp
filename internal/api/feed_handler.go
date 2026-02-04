package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/feed"
)

// EnrichedFeedItem represents a feed item with related entity data.
type EnrichedFeedItem struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	TaskID    *string         `json:"task_id,omitempty"`
	AgentID   *string         `json:"agent_id,omitempty"`
	Type      string          `json:"type"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`

	// Related entities
	TaskTitle *string `json:"task_title,omitempty"`
	AgentName *string `json:"agent_name,omitempty"`

	// Computed summary
	Summary string `json:"summary,omitempty"`
}

// PaginatedFeedResponse includes total count for pagination.
type PaginatedFeedResponse struct {
	OrgID  string             `json:"org_id"`
	Types  []string           `json:"types,omitempty"`
	From   *time.Time         `json:"from,omitempty"`
	To     *time.Time         `json:"to,omitempty"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
	Total  int                `json:"total"`
	Items  []EnrichedFeedItem `json:"items"`
}

// FeedHandlerV2 handles GET /api/feed with enhanced features:
// - Multiple types filtering (comma-separated)
// - Date range filtering (from, to)
// - Total count for pagination
// - Related entities (task title, agent name)
// - Generated summaries
func FeedHandlerV2(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Parse org_id (required)
	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	// Parse types (comma-separated, optional)
	typesParam := strings.TrimSpace(r.URL.Query().Get("types"))
	if typesParam == "" {
		typesParam = strings.TrimSpace(r.URL.Query().Get("type"))
	}
	var types []string
	if typesParam != "" {
		for _, t := range strings.Split(typesParam, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				types = append(types, t)
			}
		}
	}

	// Parse date range (optional)
	var from, to *time.Time
	if fromStr := strings.TrimSpace(r.URL.Query().Get("from")); fromStr != "" {
		t, err := parseDateTime(fromStr)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid from date"})
			return
		}
		from = &t
	}
	if toStr := strings.TrimSpace(r.URL.Query().Get("to")); toStr != "" {
		t, err := parseDateTime(toStr)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid to date"})
			return
		}
		to = &t
	}

	// Parse pagination
	limit, err := parsePositiveInt(r.URL.Query().Get("limit"), defaultFeedLimit)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
		return
	}
	if limit > maxFeedLimit {
		limit = maxFeedLimit
	}

	offset, err := parseNonNegativeInt(r.URL.Query().Get("offset"), 0)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid offset"})
		return
	}

	// Get database connection
	db, err := getFeedDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	// Get total count
	countQuery, countArgs := buildFeedCountQuery(orgID, types, from, to)
	var total int
	if err := db.QueryRowContext(r.Context(), countQuery, countArgs...).Scan(&total); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to count feed items"})
		return
	}

	// Get feed items with related entities
	query, args := buildEnrichedFeedQuery(orgID, types, from, to, limit, offset)
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load feed"})
		return
	}
	defer rows.Close()

	items := make([]EnrichedFeedItem, 0)
	summarizer := feed.NewSummarizer()

	for rows.Next() {
		item, err := scanEnrichedFeedItem(rows)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read feed"})
			return
		}

		// Generate summary
		feedItem := &feed.Item{
			ID:        item.ID,
			OrgID:     item.OrgID,
			TaskID:    item.TaskID,
			AgentID:   item.AgentID,
			Type:      item.Type,
			Metadata:  item.Metadata,
			CreatedAt: item.CreatedAt,
			TaskTitle: item.TaskTitle,
			AgentName: item.AgentName,
		}
		item.Summary = summarizer.Summarize(feedItem)

		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read feed"})
		return
	}

	sendJSON(w, http.StatusOK, PaginatedFeedResponse{
		OrgID:  orgID,
		Types:  types,
		From:   from,
		To:     to,
		Limit:  limit,
		Offset: offset,
		Total:  total,
		Items:  items,
	})
}

// buildFeedCountQuery builds a COUNT query for pagination.
func buildFeedCountQuery(orgID string, types []string, from, to *time.Time) (string, []interface{}) {
	conditions := []string{"org_id = $1"}
	args := []interface{}{orgID}

	if len(types) == 1 {
		args = append(args, types[0])
		conditions = append(conditions, fmt.Sprintf("action = $%d", len(args)))
	} else if len(types) > 1 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			args = append(args, t)
			placeholders[i] = fmt.Sprintf("$%d", len(args))
		}
		conditions = append(conditions, fmt.Sprintf("action IN (%s)", strings.Join(placeholders, ", ")))
	}

	if from != nil {
		args = append(args, *from)
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if to != nil {
		args = append(args, *to)
		conditions = append(conditions, fmt.Sprintf("created_at <= $%d", len(args)))
	}

	query := "SELECT COUNT(*) FROM activity_log WHERE " + strings.Join(conditions, " AND ")
	return query, args
}

// buildEnrichedFeedQuery builds a query that joins with tasks and agents tables.
func buildEnrichedFeedQuery(orgID string, types []string, from, to *time.Time, limit, offset int) (string, []interface{}) {
	conditions := []string{"a.org_id = $1"}
	args := []interface{}{orgID}

	if len(types) == 1 {
		args = append(args, types[0])
		conditions = append(conditions, fmt.Sprintf("a.action = $%d", len(args)))
	} else if len(types) > 1 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			args = append(args, t)
			placeholders[i] = fmt.Sprintf("$%d", len(args))
		}
		conditions = append(conditions, fmt.Sprintf("a.action IN (%s)", strings.Join(placeholders, ", ")))
	}

	if from != nil {
		args = append(args, *from)
		conditions = append(conditions, fmt.Sprintf("a.created_at >= $%d", len(args)))
	}
	if to != nil {
		args = append(args, *to)
		conditions = append(conditions, fmt.Sprintf("a.created_at <= $%d", len(args)))
	}

	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	args = append(args, limit, offset)

	query := `
		SELECT 
			a.id,
			a.org_id,
			a.task_id,
			a.agent_id,
			a.action,
			a.metadata,
			a.created_at,
			t.title AS task_title,
			ag.display_name AS agent_name
		FROM activity_log a
		LEFT JOIN tasks t ON a.task_id = t.id
		LEFT JOIN agents ag ON a.agent_id = ag.id
		WHERE ` + strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY a.created_at DESC LIMIT $%d OFFSET $%d", limitPos, offsetPos)

	return query, args
}

// scanEnrichedFeedItem scans a row into an EnrichedFeedItem.
func scanEnrichedFeedItem(scanner interface{ Scan(...any) error }) (EnrichedFeedItem, error) {
	var item EnrichedFeedItem
	var taskID sql.NullString
	var agentID sql.NullString
	var metadataBytes []byte
	var taskTitle sql.NullString
	var agentName sql.NullString

	err := scanner.Scan(
		&item.ID,
		&item.OrgID,
		&taskID,
		&agentID,
		&item.Type,
		&metadataBytes,
		&item.CreatedAt,
		&taskTitle,
		&agentName,
	)
	if err != nil {
		return item, err
	}

	if taskID.Valid {
		item.TaskID = &taskID.String
	}
	if agentID.Valid {
		item.AgentID = &agentID.String
	}
	if taskTitle.Valid {
		item.TaskTitle = &taskTitle.String
	}
	if agentName.Valid {
		item.AgentName = &agentName.String
	}
	if len(metadataBytes) == 0 {
		item.Metadata = json.RawMessage("{}")
	} else {
		item.Metadata = json.RawMessage(metadataBytes)
	}

	return item, nil
}

// parseDateTime parses a date/time string in various formats.
func parseDateTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date format: %s", s)
}
