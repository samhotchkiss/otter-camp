package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// Workflow represents an ongoing agent process.
type Workflow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Agent       string    `json:"agent"`
	AgentEmoji  string    `json:"agent_emoji"`
	Icon        string    `json:"icon"`
	IconType    string    `json:"icon_type"` // twitter, email, markets, content
	Status      string    `json:"status"`    // active, paused
	Schedule    string    `json:"schedule"`
	DisplayMode string    `json:"display_mode"` // replace, stack
	LatestItem  *WorkflowItem `json:"latest_item,omitempty"`
	StackedItems []WorkflowItem `json:"stacked_items,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// WorkflowItem represents a single item/message in a workflow.
type WorkflowItem struct {
	ID             string    `json:"id"`
	Content        string    `json:"content"`
	Priority       string    `json:"priority"` // low, normal, high, urgent
	ActionRequired bool      `json:"action_required"`
	DismissedAt    *time.Time `json:"dismissed_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// WorkflowsHandler handles workflow-related requests.
type WorkflowsHandler struct{}

// List returns all workflows.
func (h *WorkflowsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Check for demo mode
	demo := r.URL.Query().Get("demo") == "true"
	
	var workflows []Workflow
	
	if demo {
		workflows = getDemoWorkflows()
	} else {
		// TODO: Fetch from database
		workflows = getDemoWorkflows()
	}
	
	json.NewEncoder(w).Encode(workflows)
}

func getDemoWorkflows() []Workflow {
	now := time.Now()
	
	return []Workflow{
		{
			ID:          "nova-twitter",
			Name:        "Twitter Engagement",
			Agent:       "Nova",
			AgentEmoji:  "✨",
			Icon:        "🐦",
			IconType:    "twitter",
			Status:      "active",
			Schedule:    "Every 5 minutes",
			DisplayMode: "replace",
			LatestItem: &WorkflowItem{
				ID:        "tw-1",
				Content:   "Posted update about OtterCamp launch. 12 likes, 3 retweets so far. Engagement looking good!",
				Priority:  "normal",
				CreatedAt: now.Add(-15 * time.Minute),
			},
			CreatedAt: now.Add(-7 * 24 * time.Hour),
		},
		{
			ID:          "penny-email",
			Name:        "Email Monitoring",
			Agent:       "Penny",
			AgentEmoji:  "📬",
			Icon:        "✉️",
			IconType:    "email",
			Status:      "active",
			Schedule:    "Every 2 minutes",
			DisplayMode: "stack",
			LatestItem: &WorkflowItem{
				ID:             "em-1",
				Content:        "Important email from investor@example.com — wants to schedule a call about Series A",
				Priority:       "high",
				ActionRequired: true,
				CreatedAt:      now.Add(-30 * time.Minute),
			},
			StackedItems: []WorkflowItem{
				{
					ID:        "em-2",
					Content:   "Newsletter from TechCrunch — AI funding roundup",
					Priority:  "low",
					CreatedAt: now.Add(-2 * time.Hour),
				},
				{
					ID:        "em-3",
					Content:   "GitHub notification: PR #94 merged",
					Priority:  "normal",
					CreatedAt: now.Add(-3 * time.Hour),
				},
			},
			CreatedAt: now.Add(-14 * 24 * time.Hour),
		},
		{
			ID:          "beau-markets",
			Name:        "Market Watch",
			Agent:       "Beau H",
			AgentEmoji:  "📈",
			Icon:        "💹",
			IconType:    "markets",
			Status:      "active",
			Schedule:    "Every 15 minutes",
			DisplayMode: "replace",
			LatestItem: &WorkflowItem{
				ID:        "mk-1",
				Content:   "Markets closed up 0.8%. Tech sector led gains. Your watchlist: NVDA +2.3%, MSFT +1.1%, AAPL +0.5%",
				Priority:  "normal",
				CreatedAt: now.Add(-45 * time.Minute),
			},
			CreatedAt: now.Add(-30 * 24 * time.Hour),
		},
		{
			ID:          "stone-content",
			Name:        "Content Pipeline",
			Agent:       "Stone",
			AgentEmoji:  "✍️",
			Icon:        "📝",
			IconType:    "content",
			Status:      "paused",
			Schedule:    "Daily at 9am",
			DisplayMode: "stack",
			LatestItem: &WorkflowItem{
				ID:             "ct-1",
				Content:        "Blog draft ready for review: 'Why I Run 12 AI Agents'",
				Priority:       "normal",
				ActionRequired: true,
				CreatedAt:      now.Add(-4 * time.Hour),
			},
			CreatedAt: now.Add(-21 * 24 * time.Hour),
		},
	}
}
