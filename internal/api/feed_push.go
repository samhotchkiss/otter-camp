package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const (
	// MaxFeedPushBatchSize is the maximum number of items that can be pushed at once.
	MaxFeedPushBatchSize = 100

	// MaxFeedPushBodySize is the maximum request body size (1MB).
	MaxFeedPushBodySize = 1 << 20

	// RateLimitWindow is the time window for rate limiting.
	RateLimitWindow = time.Minute

	// RateLimitMaxRequests is the maximum requests per agent per window.
	RateLimitMaxRequests = 60
)

var (
	feedPushDB     *sql.DB
	feedPushDBErr  error
	feedPushDBOnce sync.Once
)

// AgentRateLimiter tracks request counts per agent.
type AgentRateLimiter struct {
	mu      sync.Mutex
	counts  map[string]int
	resets  map[string]time.Time
	window  time.Duration
	maxReqs int
	nowFunc func() time.Time // for testing
}

// NewAgentRateLimiter creates a new rate limiter.
func NewAgentRateLimiter(window time.Duration, maxReqs int) *AgentRateLimiter {
	return &AgentRateLimiter{
		counts:  make(map[string]int),
		resets:  make(map[string]time.Time),
		window:  window,
		maxReqs: maxReqs,
		nowFunc: time.Now,
	}
}

// Allow checks if a request is allowed for the given agent ID.
// Returns true if allowed, false if rate limited.
func (rl *AgentRateLimiter) Allow(agentID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc()
	resetTime, exists := rl.resets[agentID]

	// Reset counter if window has passed
	if !exists || now.After(resetTime) {
		rl.counts[agentID] = 0
		rl.resets[agentID] = now.Add(rl.window)
	}

	// Check if under limit
	if rl.counts[agentID] >= rl.maxReqs {
		return false
	}

	rl.counts[agentID]++
	return true
}

// Default rate limiter instance
var defaultRateLimiter = NewAgentRateLimiter(RateLimitWindow, RateLimitMaxRequests)

// FeedPushItem represents a single activity item to push.
type FeedPushItem struct {
	TaskID   *string         `json:"task_id,omitempty"`
	Type     string          `json:"type"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Priority bool            `json:"priority,omitempty"`
}

// FeedPushRequest is the request body for POST /api/feed/push.
type FeedPushRequest struct {
	OrgID string         `json:"org_id"`
	Mode  string         `json:"mode,omitempty"` // "augment" (default) or "replace"
	Items []FeedPushItem `json:"items"`
}

// FeedPushResponse is the response for POST /api/feed.
type FeedPushResponse struct {
	OK       bool     `json:"ok"`
	Inserted int      `json:"inserted"`
	IDs      []string `json:"ids"`
}

// FeedPushEvent is broadcast to WebSocket subscribers.
type FeedPushEvent struct {
	Type  ws.MessageType `json:"type"`
	Items []FeedItem     `json:"items"`
}

// FeedPushHandler handles agent feed push requests with WebSocket broadcasting.
type FeedPushHandler struct {
	Hub         *ws.Hub
	RateLimiter *AgentRateLimiter
}

// NewFeedPushHandler creates a new feed push handler.
func NewFeedPushHandler(hub *ws.Hub) *FeedPushHandler {
	return &FeedPushHandler{
		Hub:         hub,
		RateLimiter: defaultRateLimiter,
	}
}

// Handle handles POST /api/feed/push for agent activity pushes.
func (h *FeedPushHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Validate API key auth
	apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if apiKey == "" {
		// Also check Authorization header for Bearer token
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(authHeader, "Bearer ") {
			apiKey = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if apiKey == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing authentication"})
		return
	}

	// Get database connection
	db, err := getFeedPushDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	// Validate API key and get agent info
	agentID, orgID, err := validateAgentAPIKey(r.Context(), db, apiKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "authentication error"})
		return
	}

	// Check rate limit
	rateLimiter := h.RateLimiter
	if rateLimiter == nil {
		rateLimiter = defaultRateLimiter
	}
	if !rateLimiter.Allow(agentID) {
		sendJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded"})
		return
	}

	// Read and validate body
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxFeedPushBodySize+1))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unable to read request body"})
		return
	}
	if len(body) > MaxFeedPushBodySize {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
		return
	}

	var req FeedPushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "augment"
	}
	if mode != "augment" && mode != "replace" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid mode: must be 'augment' or 'replace'"})
		return
	}

	// Validate org_id matches agent's org
	if req.OrgID != "" && req.OrgID != orgID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "org_id mismatch"})
		return
	}

	// Validate items
	if len(req.Items) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "no items provided"})
		return
	}
	if len(req.Items) > MaxFeedPushBatchSize {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "too many items (max 100)"})
		return
	}

	// Validate and normalize each item
	normalized := make([]FeedPushItem, 0, len(req.Items))
	for i, item := range req.Items {
		if strings.TrimSpace(item.Type) == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("item %d: missing type", i)})
			return
		}
		if item.TaskID != nil && !uuidRegex.MatchString(*item.TaskID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("item %d: invalid task_id", i)})
			return
		}
		metadata, err := normalizeFeedPushMetadata(item.Metadata, item.Priority)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("item %d: invalid metadata", i)})
			return
		}
		item.Metadata = metadata
		normalized = append(normalized, item)
	}

	if mode == "replace" {
		if err := clearAgentFeedItems(r.Context(), db, orgID, agentID); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to replace feed items"})
			return
		}
	}

	// Insert items
	ids, err := insertFeedItems(r.Context(), db, orgID, agentID, normalized)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to insert items"})
		return
	}

	// Broadcast to WebSocket subscribers
	if h.Hub != nil {
		items := make([]FeedItem, len(normalized))
		now := time.Now()
		for i, item := range normalized {
			items[i] = FeedItem{
				ID:        ids[i],
				OrgID:     orgID,
				TaskID:    item.TaskID,
				AgentID:   &agentID,
				Type:      item.Type,
				Metadata:  item.Metadata,
				CreatedAt: now,
			}
			if items[i].Metadata == nil {
				items[i].Metadata = json.RawMessage("{}")
			}
		}
		broadcastFeedItems(h.Hub, orgID, items)
	}

	sendJSON(w, http.StatusOK, FeedPushResponse{
		OK:       true,
		Inserted: len(ids),
		IDs:      ids,
	})
}

func normalizeFeedPushMetadata(raw json.RawMessage, priority bool) (json.RawMessage, error) {
	var payload interface{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
	}

	var obj map[string]interface{}
	switch typed := payload.(type) {
	case nil:
		obj = map[string]interface{}{}
	case map[string]interface{}:
		obj = typed
	default:
		obj = map[string]interface{}{
			"payload": typed,
		}
	}

	obj["source"] = "agent_push"
	if priority {
		if _, exists := obj["priority"]; !exists {
			obj["priority"] = true
		}
	}

	normalized, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

// validateAgentAPIKey validates an API key and returns the agent ID and org ID.
func validateAgentAPIKey(ctx context.Context, db *sql.DB, apiKey string) (string, string, error) {
	var agentID, orgID string
	err := db.QueryRowContext(
		ctx,
		"SELECT id, org_id FROM agents WHERE api_key = $1 AND status = 'active'",
		apiKey,
	).Scan(&agentID, &orgID)
	return agentID, orgID, err
}

// insertFeedItems inserts multiple feed items and returns their IDs.
func insertFeedItems(ctx context.Context, db *sql.DB, orgID, agentID string, items []FeedPushItem) ([]string, error) {
	ids := make([]string, 0, len(items))

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(
		"INSERT INTO activity_log (org_id, task_id, agent_id, action, metadata) VALUES ($1, $2, $3, $4, $5) RETURNING id",
	)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	for _, item := range items {
		var taskArg interface{}
		if item.TaskID != nil {
			taskArg = *item.TaskID
		}

		metadata := item.Metadata
		if metadata == nil {
			metadata = json.RawMessage("{}")
		}

		var id string
		err := stmt.QueryRow(orgID, taskArg, agentID, item.Type, metadata).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return ids, nil
}

func clearAgentFeedItems(ctx context.Context, db *sql.DB, orgID, agentID string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM activity_log WHERE org_id = $1 AND agent_id = $2 AND (metadata->>'source') = 'agent_push'", orgID, agentID)
	return err
}

// broadcastFeedItems sends feed items to WebSocket subscribers.
func broadcastFeedItems(hub *ws.Hub, orgID string, items []FeedItem) {
	if hub == nil {
		return
	}

	payload, err := json.Marshal(FeedPushEvent{
		Type:  "FeedItemsAdded",
		Items: items,
	})
	if err != nil {
		return
	}

	hub.Broadcast(orgID, payload)
}

func getFeedPushDB() (*sql.DB, error) {
	feedPushDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			feedPushDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			feedPushDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			feedPushDBErr = err
			return
		}

		feedPushDB = db
	})

	return feedPushDB, feedPushDBErr
}
