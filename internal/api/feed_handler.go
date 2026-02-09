package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/feed"
	"github.com/samhotchkiss/otter-camp/internal/store"
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
	TaskTitle   *string `json:"task_title,omitempty"`
	AgentName   *string `json:"agent_name,omitempty"`
	ProjectName *string `json:"project_name,omitempty"`

	// Computed summary
	Summary string `json:"summary,omitempty"`

	// Ranking fields
	Score    float64 `json:"score,omitempty"`
	Priority string  `json:"priority,omitempty"`
}

// FeedMode constants for feed type selection.
const (
	FeedModeAll    = "all_activity"
	FeedModeForYou = "for_you"
)

// PaginatedFeedResponse includes total count for pagination.
type PaginatedFeedResponse struct {
	OrgID    string             `json:"org_id"`
	FeedMode string             `json:"feed_mode,omitempty"`
	Types    []string           `json:"types,omitempty"`
	From     *time.Time         `json:"from,omitempty"`
	To       *time.Time         `json:"to,omitempty"`
	Limit    int                `json:"limit"`
	Offset   int                `json:"offset"`
	Total    int                `json:"total"`
	Items    []EnrichedFeedItem `json:"items"`
}

// FeedHandlerV2 handles GET /api/feed with enhanced features:
// - Multiple types filtering (comma-separated)
// - Date range filtering (from, to)
// - Total count for pagination
// - Related entities (task title, agent name)
// - Generated summaries
// - Feed mode: "for_you" (personalized) or "all_activity" (everything)
// - Priority-based ranking with time decay
// - Agent boost for preferred agents
// DemoFeedResponse is the response format for demo mode
type DemoFeedResponse struct {
	ActionItems []map[string]interface{} `json:"actionItems"`
	FeedItems   []map[string]interface{} `json:"feedItems"`
}

func FeedHandlerV2(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Demo mode: return sample data only when explicitly requested.
	if r.URL.Query().Get("demo") == "true" {
		sendJSON(w, http.StatusOK, DemoFeedResponse{
			ActionItems: []map[string]interface{}{
				{
					"id":              "1",
					"icon":            "🚀",
					"project":         "ItsAlive",
					"time":            "5 min ago",
					"agent":           "Ivy",
					"message":         "is waiting on your approval to deploy v2.1.0 with the new onboarding flow.",
					"primaryAction":   "Approve Deploy",
					"secondaryAction": "View Details",
				},
				{
					"id":              "2",
					"icon":            "✍️",
					"project":         "Content",
					"time":            "1 hour ago",
					"agent":           "Stone",
					"message":         "finished a blog post for you to review: \"Why I Run 12 AI Agents\"",
					"primaryAction":   "Review Post",
					"secondaryAction": "Later",
				},
			},
			FeedItems: []map[string]interface{}{
				{
					"id":       "summary",
					"avatar":   "✓",
					"avatarBg": "var(--green)",
					"title":    "4 projects active",
					"text":     "Derek pushed 4 commits to Pearl, Jeff G finished mockups, Nova scheduled tweets",
					"meta":     "Last 6 hours • 14 updates total",
					"type":     nil,
				},
				{
					"id":       "email",
					"avatar":   "P",
					"avatarBg": "var(--blue)",
					"title":    "Important email",
					"text":     "from investor@example.com — \"Follow up on our conversation\"",
					"meta":     "30 min ago",
					"type":     map[string]string{"label": "Penny • Email", "className": "insight"},
				},
				{
					"id":       "markets",
					"avatar":   "B",
					"avatarBg": "var(--orange)",
					"title":    "Market Summary",
					"text":     "S&P up 0.8%, your watchlist +1.2%. No alerts triggered.",
					"meta":     "2 hours ago",
					"type":     map[string]string{"label": "Beau H • Markets", "className": "progress"},
				},
			},
		})
		return
	}

	// Parse org_id (required for non-demo mode)
	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	// Parse feed_mode (optional, defaults to "all_activity")
	feedMode := strings.TrimSpace(r.URL.Query().Get("feed_mode"))
	if feedMode == "" {
		feedMode = FeedModeAll
	}
	if feedMode != FeedModeAll && feedMode != FeedModeForYou {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid feed_mode: must be 'all_activity' or 'for_you'"})
		return
	}

	// Parse user_id (optional, used for personalization)
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))

	// Parse preferred_agents (comma-separated, optional)
	var preferredAgents []string
	if agentsParam := strings.TrimSpace(r.URL.Query().Get("preferred_agents")); agentsParam != "" {
		for _, a := range strings.Split(agentsParam, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				preferredAgents = append(preferredAgents, a)
			}
		}
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

	// Initialize ranker for scoring
	ranker := feed.NewRanker()
	if userID != "" {
		ranker.SetCurrentUser(userID)
	}
	if len(preferredAgents) > 0 {
		ranker.SetPreferredAgents(preferredAgents)
	}

	// Get database connection
	db, err := getFeedDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	conn, err := store.WithWorkspaceID(r.Context(), db, orgID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNoWorkspace) || errors.Is(err, store.ErrInvalidWorkspace) {
			status = http.StatusBadRequest
		}
		sendJSON(w, status, errorResponse{Error: err.Error()})
		return
	}
	defer conn.Close()

	// Get total count
	countQuery, countArgs := buildFeedCountQuery(orgID, types, from, to)
	var total int
	if err := conn.QueryRowContext(r.Context(), countQuery, countArgs...).Scan(&total); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to count feed items"})
		return
	}

	// For ranking, we need to fetch more items than requested, then rank and slice
	// This is because ranking may filter items (for_you mode) or reorder them
	fetchLimit := limit * 3 // Fetch 3x to have enough for filtering/ranking
	if fetchLimit > 500 {
		fetchLimit = 500
	}

	// Get feed items with related entities
	query, args := buildEnrichedFeedQuery(orgID, types, from, to, fetchLimit, 0)
	rows, err := conn.QueryContext(r.Context(), query, args...)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load feed"})
		return
	}
	defer rows.Close()

	// Convert to feed.Item for ranking
	feedItems := make([]*feed.Item, 0)
	itemMap := make(map[string]EnrichedFeedItem)
	summarizer := feed.NewSummarizer()

	for rows.Next() {
		item, err := scanEnrichedFeedItem(rows)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read feed"})
			return
		}

		metadata := item.Metadata
		if len(metadata) == 0 {
			metadata = json.RawMessage("{}")
		}
		if item.Type == "git.push" {
			projectID := extractMetadataString(metadata, "project_id")
			if projectID != "" {
				metadata = mergeMetadata(metadata, map[string]string{
					"project_url": buildProjectURL(r, projectID),
				})
			}
			if item.ProjectName != nil && *item.ProjectName != "" {
				metadata = mergeMetadata(metadata, map[string]string{
					"project_name": *item.ProjectName,
				})
			}
		}

		feedItem := &feed.Item{
			ID:        item.ID,
			OrgID:     item.OrgID,
			TaskID:    item.TaskID,
			AgentID:   item.AgentID,
			Type:      item.Type,
			Metadata:  metadata,
			CreatedAt: item.CreatedAt,
			TaskTitle: item.TaskTitle,
			AgentName: item.AgentName,
		}
		feedItems = append(feedItems, feedItem)

		// Generate summary
		item.Summary = summarizer.Summarize(feedItem)
		itemMap[item.ID] = item
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read feed"})
		return
	}

	// Apply ranking based on feed mode
	var rankedItems []*feed.RankedItem
	if feedMode == FeedModeForYou {
		rankedItems = ranker.ForYouFeed(feedItems)
	} else {
		rankedItems = ranker.AllActivityFeed(feedItems)
	}

	// Apply pagination to ranked results
	start := offset
	if start > len(rankedItems) {
		start = len(rankedItems)
	}
	end := start + limit
	if end > len(rankedItems) {
		end = len(rankedItems)
	}
	paginatedRanked := rankedItems[start:end]

	// Convert back to EnrichedFeedItem with scores
	items := make([]EnrichedFeedItem, len(paginatedRanked))
	for i, ranked := range paginatedRanked {
		item := itemMap[ranked.ID]
		item.Score = ranked.Score
		item.Priority = ranked.Priority
		items[i] = item
	}

	// Update total for "for_you" mode (filtered count)
	responseTotal := total
	if feedMode == FeedModeForYou {
		responseTotal = len(rankedItems)
	}

	sendJSON(w, http.StatusOK, PaginatedFeedResponse{
		OrgID:    orgID,
		FeedMode: feedMode,
		Types:    types,
		From:     from,
		To:       to,
		Limit:    limit,
		Offset:   offset,
		Total:    responseTotal,
		Items:    items,
	})
}

// buildFeedCountQuery builds a COUNT query for pagination.
func buildFeedCountQuery(orgID string, types []string, from, to *time.Time) (string, []interface{}) {
	conditions := []string{
		"org_id = $1",
		"action NOT LIKE 'project_chat.%'",
	}
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
	conditions := []string{
		"a.org_id = $1",
		"a.action NOT LIKE 'project_chat.%'",
	}
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
			COALESCE(
				NULLIF(ag.display_name, ''),
				NULLIF(u.display_name, ''),
				NULLIF(a.metadata->>'pusher_name', ''),
				NULLIF(a.metadata->>'pusher', ''),
				NULLIF(a.metadata->>'sender_login', ''),
				NULLIF(a.metadata->>'sender_name', ''),
				NULLIF(a.metadata->>'sender', ''),
				NULLIF(a.metadata->>'author_name', ''),
				NULLIF(a.metadata->>'author', ''),
				NULLIF(a.metadata->>'committer_name', ''),
				NULLIF(a.metadata->>'committer', ''),
				NULLIF(a.metadata->>'user_name', ''),
				'System'
			) AS agent_name,
			p.name AS project_name
		FROM activity_log a
		LEFT JOIN tasks t ON a.task_id = t.id
		LEFT JOIN agents ag ON a.agent_id = ag.id
		LEFT JOIN users u ON u.org_id = a.org_id AND u.id::text = (a.metadata->>'user_id')
		LEFT JOIN projects p ON p.org_id = a.org_id AND p.id::text = (a.metadata->>'project_id')
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
	var projectName sql.NullString

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
		&projectName,
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
	normalizedAgentName := "System"
	if agentName.Valid {
		normalizedAgentName = normalizeFeedActorName(agentName.String)
	}
	item.AgentName = &normalizedAgentName
	if projectName.Valid {
		item.ProjectName = &projectName.String
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

func buildProjectURL(r *http.Request, projectID string) string {
	if projectID == "" {
		return ""
	}
	base := getPublicBaseURL(r)
	if strings.Contains(base, "api.otter.camp") {
		base = "https://sam.otter.camp"
	}
	return strings.TrimRight(base, "/") + "/projects/" + projectID
}

func mergeMetadata(existing json.RawMessage, extra map[string]string) json.RawMessage {
	merged := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &merged)
	}
	for k, v := range extra {
		if v == "" {
			continue
		}
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return existing
	}
	return out
}

func extractMetadataString(metadata json.RawMessage, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(metadata, &m); err != nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
