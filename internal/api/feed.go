package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

const (
	defaultFeedLimit = 50
	maxFeedLimit     = 200
)

var (
	feedDB     *sql.DB
	feedDBErr  error
	feedDBOnce sync.Once
)

type FeedItem struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	TaskID    *string         `json:"task_id,omitempty"`
	AgentID   *string         `json:"agent_id,omitempty"`
	Type      string          `json:"type"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

type FeedResponse struct {
	OrgID  string     `json:"org_id"`
	Type   string     `json:"type,omitempty"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
	Items  []FeedItem `json:"items"`
}

// FeedHandler handles GET /api/feed
func FeedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	action := strings.TrimSpace(r.URL.Query().Get("type"))
	if action == "" {
		action = strings.TrimSpace(r.URL.Query().Get("action"))
	}

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

	db, err := getFeedDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	query, args := buildFeedQuery(orgID, action, limit, offset)
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load feed"})
		return
	}
	defer rows.Close()

	items := make([]FeedItem, 0)
	for rows.Next() {
		item, err := scanFeedItem(rows)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read feed"})
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read feed"})
		return
	}

	sendJSON(w, http.StatusOK, FeedResponse{
		OrgID:  orgID,
		Type:   action,
		Limit:  limit,
		Offset: offset,
		Items:  items,
	})
}

func getFeedDB() (*sql.DB, error) {
	feedDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			feedDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			feedDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			feedDBErr = err
			return
		}

		feedDB = db
	})

	return feedDB, feedDBErr
}

func buildFeedQuery(orgID, action string, limit, offset int) (string, []interface{}) {
<<<<<<< HEAD
	conditions := []string{
		"org_id = $1",
		"action NOT LIKE 'project_chat.%'",
	}
=======
	conditions := []string{"org_id = $1"}
>>>>>>> 9f623eb (feat(6a): add activity feed API endpoint with pagination)
	args := []interface{}{orgID}

	if action != "" {
		args = append(args, action)
		conditions = append(conditions, fmt.Sprintf("action = $%d", len(args)))
	}

	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	args = append(args, limit, offset)

	query := "SELECT id, org_id, task_id, agent_id, action, metadata, created_at FROM activity_log WHERE " +
		strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", limitPos, offsetPos)

	return query, args
}

func scanFeedItem(scanner interface{ Scan(...any) error }) (FeedItem, error) {
	var item FeedItem
	var taskID sql.NullString
	var agentID sql.NullString
	var metadataBytes []byte

	err := scanner.Scan(
		&item.ID,
		&item.OrgID,
		&taskID,
		&agentID,
		&item.Type,
		&metadataBytes,
		&item.CreatedAt,
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
	if len(metadataBytes) == 0 {
		item.Metadata = json.RawMessage("{}")
	} else {
		item.Metadata = json.RawMessage(metadataBytes)
	}

	return item, nil
}
