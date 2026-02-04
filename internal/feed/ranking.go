// Package feed provides feed item processing, summarization, and ranking.
package feed

import (
	"encoding/json"
	"sort"
	"time"
)

// Priority constants for feed items.
const (
	PriorityCritical = "P0"
	PriorityHigh     = "P1"
	PriorityMedium   = "P2"
	PriorityLow      = "P3"
)

// TypeWeights define base scores for different feed item types.
// Higher scores = higher importance.
var TypeWeights = map[string]float64{
	// Insights - highest priority
	"insight":      100.0,
	"anomaly":      95.0,
	"alert":        90.0,
	"dispatch":     85.0,
	"assignment":   80.0,
	"task_created": 75.0,

	// Updates - medium priority
	"task_status_changed": 60.0,
	"task_update":         55.0,
	"task_updated":        55.0,
	"commit":              50.0,
	"comment":             45.0,

	// Messages - lower priority
	"message": 30.0,
	"mention": 70.0, // Mentions get boosted
}

// PriorityWeights define multipliers for task priorities.
var PriorityWeights = map[string]float64{
	"P0": 4.0, // Critical
	"P1": 2.5, // High
	"P2": 1.5, // Medium
	"P3": 1.0, // Low
	"":   1.0, // Default
}

// DefaultTypeWeight is used for unknown item types.
const DefaultTypeWeight = 25.0

// DecayHalfLife is the time it takes for an item's recency score to halve.
const DecayHalfLife = 6 * time.Hour

// AgentBoostMultiplier is applied when the item involves a preferred agent.
const AgentBoostMultiplier = 1.5

// UserInvolvementBoost is added when the user is mentioned or assigned.
const UserInvolvementBoost = 50.0

// RankedItem extends Item with ranking metadata.
type RankedItem struct {
	Item
	Score    float64 `json:"score"`
	Priority string  `json:"priority,omitempty"`
}

// Ranker scores and sorts feed items by importance.
type Ranker struct {
	// PreferredAgents is a set of agent IDs that get boosted.
	PreferredAgents map[string]bool

	// CurrentUserID is the ID of the viewing user (for involvement scoring).
	CurrentUserID string

	// Now is the reference time for decay calculations. Defaults to time.Now().
	Now func() time.Time
}

// NewRanker creates a new Ranker with default settings.
func NewRanker() *Ranker {
	return &Ranker{
		PreferredAgents: make(map[string]bool),
		Now:             time.Now,
	}
}

// SetPreferredAgents sets the list of agent IDs that should be boosted.
func (r *Ranker) SetPreferredAgents(agentIDs []string) {
	r.PreferredAgents = make(map[string]bool)
	for _, id := range agentIDs {
		r.PreferredAgents[id] = true
	}
}

// SetCurrentUser sets the user ID for involvement scoring.
func (r *Ranker) SetCurrentUser(userID string) {
	r.CurrentUserID = userID
}

// Score calculates the importance score for a single feed item.
func (r *Ranker) Score(item *Item) float64 {
	now := r.Now()

	// Base score from item type
	score := TypeWeights[item.Type]
	if score == 0 {
		score = DefaultTypeWeight
	}

	// Extract priority from metadata if available
	priority := extractMetadataString(item.Metadata, "priority")
	if priority == "" {
		priority = extractMetadataString(item.Metadata, "task_priority")
	}

	// Apply priority multiplier
	if mult, ok := PriorityWeights[priority]; ok {
		score *= mult
	}

	// Boost for preferred agents
	if item.AgentID != nil && r.PreferredAgents[*item.AgentID] {
		score *= AgentBoostMultiplier
	}

	// Boost for user involvement (mentions, assignments)
	if r.isUserInvolved(item) {
		score += UserInvolvementBoost
	}

	// Apply time decay
	age := now.Sub(item.CreatedAt)
	score *= calculateDecay(age, DecayHalfLife)

	return score
}

// RankItems scores and sorts a slice of feed items by importance.
// Returns a new slice of RankedItems, highest score first.
func (r *Ranker) RankItems(items []*Item) []*RankedItem {
	ranked := make([]*RankedItem, len(items))

	for i, item := range items {
		score := r.Score(item)
		priority := extractMetadataString(item.Metadata, "priority")
		if priority == "" {
			priority = extractMetadataString(item.Metadata, "task_priority")
		}

		ranked[i] = &RankedItem{
			Item:     *item,
			Score:    score,
			Priority: priority,
		}
	}

	// Sort by score descending
	sort.Slice(ranked, func(i, j int) bool {
		// Primary sort: score (descending)
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		// Tie-breaker: recency (descending)
		return ranked[i].CreatedAt.After(ranked[j].CreatedAt)
	})

	return ranked
}

// ForYouFeed returns items personalized for the current user.
// This includes items they're involved in and items from preferred agents.
func (r *Ranker) ForYouFeed(items []*Item) []*RankedItem {
	// Filter to items relevant to the user
	relevant := make([]*Item, 0, len(items))
	for _, item := range items {
		if r.isRelevantToUser(item) {
			relevant = append(relevant, item)
		}
	}

	return r.RankItems(relevant)
}

// AllActivityFeed returns all items ranked by importance.
func (r *Ranker) AllActivityFeed(items []*Item) []*RankedItem {
	return r.RankItems(items)
}

// isUserInvolved checks if the current user is mentioned or assigned.
func (r *Ranker) isUserInvolved(item *Item) bool {
	if r.CurrentUserID == "" {
		return false
	}

	// Check if item mentions the user
	mentions := extractMetadataStringSlice(item.Metadata, "mentions")
	for _, m := range mentions {
		if m == r.CurrentUserID {
			return true
		}
	}

	// Check if item is assigned to user
	assignee := extractMetadataString(item.Metadata, "assignee")
	if assignee == r.CurrentUserID {
		return true
	}

	// Check assignee_id field
	assigneeID := extractMetadataString(item.Metadata, "assignee_id")
	if assigneeID == r.CurrentUserID {
		return true
	}

	return false
}

// isRelevantToUser checks if an item should appear in the "For You" feed.
func (r *Ranker) isRelevantToUser(item *Item) bool {
	// Always include items user is directly involved in
	if r.isUserInvolved(item) {
		return true
	}

	// Include items from preferred agents
	if item.AgentID != nil && r.PreferredAgents[*item.AgentID] {
		return true
	}

	// Include high-priority items (P0, P1)
	priority := extractMetadataString(item.Metadata, "priority")
	if priority == "" {
		priority = extractMetadataString(item.Metadata, "task_priority")
	}
	if priority == PriorityCritical || priority == PriorityHigh {
		return true
	}

	// Include certain high-importance types
	switch item.Type {
	case "insight", "anomaly", "alert", "dispatch", "assignment":
		return true
	}

	return false
}

// calculateDecay computes the decay factor based on age and half-life.
// Returns a value between 0 and 1, where 1 is no decay.
func calculateDecay(age time.Duration, halfLife time.Duration) float64 {
	if age <= 0 {
		return 1.0
	}
	// Exponential decay: factor = 0.5^(age/halfLife)
	// Using natural log: factor = e^(-ln(2) * age / halfLife)
	halfLives := float64(age) / float64(halfLife)
	return pow(0.5, halfLives)
}

// pow calculates x^y for float64.
func pow(x, y float64) float64 {
	if y == 0 {
		return 1.0
	}
	if y == 1 {
		return x
	}
	// Use repeated squaring for integer powers, else fall back to approx
	if y == 2 {
		return x * x
	}
	// For exponential decay, we need proper math
	// exp(y * ln(x))
	return exp(y * ln(x))
}

// ln computes natural logarithm using Taylor series approximation.
func ln(x float64) float64 {
	if x <= 0 {
		return -1e10 // Approximate -infinity
	}
	if x == 1 {
		return 0
	}

	// Normalize x to [0.5, 2) for better convergence
	exp := 0
	for x >= 2 {
		x /= 2
		exp++
	}
	for x < 0.5 {
		x *= 2
		exp--
	}

	// Taylor series for ln(1+y) where y = x-1
	y := x - 1
	result := 0.0
	term := y
	for i := 1; i <= 20; i++ {
		if i%2 == 1 {
			result += term / float64(i)
		} else {
			result -= term / float64(i)
		}
		term *= y
	}

	// Add back the exponent: ln(x * 2^exp) = ln(x) + exp*ln(2)
	ln2 := 0.6931471805599453
	return result + float64(exp)*ln2
}

// exp computes e^x using Taylor series.
func exp(x float64) float64 {
	if x > 700 {
		return 1e308 // Prevent overflow
	}
	if x < -700 {
		return 0
	}

	// e^x = sum(x^n / n!)
	result := 1.0
	term := 1.0
	for i := 1; i <= 30; i++ {
		term *= x / float64(i)
		result += term
		if term < 1e-15 && term > -1e-15 {
			break
		}
	}
	return result
}

// extractMetadataStringSlice extracts a string slice from metadata JSON.
func extractMetadataStringSlice(metadata json.RawMessage, key string) []string {
	if len(metadata) == 0 {
		return nil
	}

	var m map[string]interface{}
	if err := json.Unmarshal(metadata, &m); err != nil {
		return nil
	}

	v, ok := m[key]
	if !ok {
		return nil
	}

	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}

	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
