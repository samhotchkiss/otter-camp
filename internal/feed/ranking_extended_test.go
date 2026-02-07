package feed

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRanker_Score_AllTypes(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	// Test all known types get expected weights
	for itemType, expectedWeight := range TypeWeights {
		t.Run(itemType, func(t *testing.T) {
			item := &Item{
				ID:        "item-1",
				Type:      itemType,
				CreatedAt: now, // No decay
			}

			score := r.Score(item)
			assert.Equal(t, expectedWeight, score)
		})
	}
}

func TestRanker_Score_UnknownType(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	item := &Item{
		ID:        "item-1",
		Type:      "unknown_type",
		CreatedAt: now,
	}

	score := r.Score(item)
	assert.Equal(t, DefaultTypeWeight, score)
}

func TestRanker_Score_PriorityMultiplier(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	baseType := "task_created"
	baseWeight := TypeWeights[baseType]

	tests := []struct {
		priority   string
		multiplier float64
	}{
		{"P0", 4.0},
		{"P1", 2.5},
		{"P2", 1.5},
		{"P3", 1.0},
		{"", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.priority, func(t *testing.T) {
			item := &Item{
				ID:        "item-1",
				Type:      baseType,
				Metadata:  json.RawMessage(`{"priority":"` + tt.priority + `"}`),
				CreatedAt: now,
			}

			score := r.Score(item)
			assert.Equal(t, baseWeight*tt.multiplier, score)
		})
	}
}

func TestRanker_Score_TaskPriorityFallback(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	// Test task_priority field as fallback
	item := &Item{
		ID:        "item-1",
		Type:      "task_created",
		Metadata:  json.RawMessage(`{"task_priority":"P0"}`),
		CreatedAt: now,
	}

	score := r.Score(item)
	expected := TypeWeights["task_created"] * PriorityWeights["P0"]
	assert.Equal(t, expected, score)
}

func TestRanker_Score_PreferredAgentBoost(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	r.SetPreferredAgents([]string{"agent-1", "agent-2"})
	now := time.Now()
	r.Now = func() time.Time { return now }

	agentID := "agent-1"
	item := &Item{
		ID:        "item-1",
		Type:      "message",
		AgentID:   &agentID,
		CreatedAt: now,
	}

	score := r.Score(item)
	expected := TypeWeights["message"] * AgentBoostMultiplier
	assert.Equal(t, expected, score)
}

func TestRanker_Score_NoBoostForNonPreferred(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	r.SetPreferredAgents([]string{"agent-1"})
	now := time.Now()
	r.Now = func() time.Time { return now }

	agentID := "agent-other"
	item := &Item{
		ID:        "item-1",
		Type:      "message",
		AgentID:   &agentID,
		CreatedAt: now,
	}

	score := r.Score(item)
	assert.Equal(t, TypeWeights["message"], score)
}

func TestRanker_Score_UserInvolvementBoost(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	r.SetCurrentUser("user-123")
	now := time.Now()
	r.Now = func() time.Time { return now }

	tests := []struct {
		name     string
		metadata json.RawMessage
	}{
		{
			name:     "mentioned",
			metadata: json.RawMessage(`{"mentions":["user-123"]}`),
		},
		{
			name:     "assignee",
			metadata: json.RawMessage(`{"assignee":"user-123"}`),
		},
		{
			name:     "assignee_id",
			metadata: json.RawMessage(`{"assignee_id":"user-123"}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &Item{
				ID:        "item-1",
				Type:      "task_created",
				Metadata:  tt.metadata,
				CreatedAt: now,
			}

			score := r.Score(item)
			expected := TypeWeights["task_created"] + UserInvolvementBoost
			assert.Equal(t, expected, score)
		})
	}
}

func TestRanker_Score_TimeDecay(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	// Item at current time - no decay
	currentItem := &Item{
		ID:        "item-1",
		Type:      "message",
		CreatedAt: now,
	}

	// Item at half-life - should be ~50% score
	halfLifeItem := &Item{
		ID:        "item-2",
		Type:      "message",
		CreatedAt: now.Add(-DecayHalfLife),
	}

	// Very old item - should be much lower
	oldItem := &Item{
		ID:        "item-3",
		Type:      "message",
		CreatedAt: now.Add(-24 * time.Hour),
	}

	currentScore := r.Score(currentItem)
	halfLifeScore := r.Score(halfLifeItem)
	oldScore := r.Score(oldItem)

	assert.Greater(t, currentScore, halfLifeScore)
	assert.Greater(t, halfLifeScore, oldScore)

	// Half-life score should be approximately half
	ratio := halfLifeScore / currentScore
	assert.InDelta(t, 0.5, ratio, 0.1)
}

func TestRanker_RankItems(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	items := []*Item{
		{ID: "low", Type: "message", CreatedAt: now},                              // Low weight
		{ID: "high", Type: "insight", CreatedAt: now},                             // High weight
		{ID: "medium", Type: "task_status_changed", CreatedAt: now},               // Medium weight
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 3)
	assert.Equal(t, "high", ranked[0].ID)    // insight = 100
	assert.Equal(t, "medium", ranked[1].ID)  // task_status_changed = 60
	assert.Equal(t, "low", ranked[2].ID)     // message = 30
}

func TestRanker_RankItems_TiebreakByRecency(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	// Same type, same score - should sort by recency
	items := []*Item{
		{ID: "older", Type: "message", CreatedAt: now.Add(-time.Hour)},
		{ID: "newer", Type: "message", CreatedAt: now},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 2)
	assert.Equal(t, "newer", ranked[0].ID)
	assert.Equal(t, "older", ranked[1].ID)
}

func TestRanker_RankItems_EmptySlice(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	ranked := r.RankItems([]*Item{})
	assert.Empty(t, ranked)
}

func TestRanker_ForYouFeed(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	r.SetCurrentUser("user-123")
	r.SetPreferredAgents([]string{"agent-1"})
	now := time.Now()
	r.Now = func() time.Time { return now }

	agentID := "agent-1"
	items := []*Item{
		// Should be included - user is mentioned
		{ID: "mentioned", Type: "message", Metadata: json.RawMessage(`{"mentions":["user-123"]}`), CreatedAt: now},
		// Should be included - preferred agent
		{ID: "preferred", Type: "message", AgentID: &agentID, CreatedAt: now},
		// Should be included - high priority
		{ID: "critical", Type: "task_created", Metadata: json.RawMessage(`{"priority":"P0"}`), CreatedAt: now},
		// Should be included - insight type
		{ID: "insight", Type: "insight", CreatedAt: now},
		// Should NOT be included - low priority, unknown agent, no involvement
		{ID: "low", Type: "task_created", Metadata: json.RawMessage(`{"priority":"P3"}`), CreatedAt: now},
	}

	ranked := r.ForYouFeed(items)

	// Low priority item should be filtered out
	ids := make([]string, len(ranked))
	for i, r := range ranked {
		ids[i] = r.ID
	}

	assert.Contains(t, ids, "mentioned")
	assert.Contains(t, ids, "preferred")
	assert.Contains(t, ids, "critical")
	assert.Contains(t, ids, "insight")
	assert.NotContains(t, ids, "low")
}

func TestRanker_AllActivityFeed(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	now := time.Now()
	r.Now = func() time.Time { return now }

	items := []*Item{
		{ID: "1", Type: "message", CreatedAt: now},
		{ID: "2", Type: "insight", CreatedAt: now},
		{ID: "3", Type: "task_created", CreatedAt: now},
	}

	ranked := r.AllActivityFeed(items)

	// Should return all items, ranked
	require.Len(t, ranked, 3)
}

func TestRanker_SetPreferredAgents(t *testing.T) {
	t.Parallel()

	r := NewRanker()

	r.SetPreferredAgents([]string{"agent-1", "agent-2", "agent-3"})

	assert.True(t, r.PreferredAgents["agent-1"])
	assert.True(t, r.PreferredAgents["agent-2"])
	assert.True(t, r.PreferredAgents["agent-3"])
	assert.False(t, r.PreferredAgents["agent-4"])
}

func TestRanker_SetCurrentUser(t *testing.T) {
	t.Parallel()

	r := NewRanker()
	r.SetCurrentUser("user-123")

	assert.Equal(t, "user-123", r.CurrentUserID)
}

func TestCalculateDecay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		age      time.Duration
		halfLife time.Duration
		wantMin  float64
		wantMax  float64
	}{
		{
			name:     "zero age",
			age:      0,
			halfLife: time.Hour,
			wantMin:  0.99,
			wantMax:  1.01,
		},
		{
			name:     "negative age",
			age:      -time.Hour,
			halfLife: time.Hour,
			wantMin:  0.99,
			wantMax:  1.01,
		},
		{
			name:     "one half-life",
			age:      DecayHalfLife,
			halfLife: DecayHalfLife,
			wantMin:  0.45,
			wantMax:  0.55,
		},
		{
			name:     "two half-lives",
			age:      2 * DecayHalfLife,
			halfLife: DecayHalfLife,
			wantMin:  0.20,
			wantMax:  0.30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateDecay(tt.age, tt.halfLife)
			assert.GreaterOrEqual(t, result, tt.wantMin)
			assert.LessOrEqual(t, result, tt.wantMax)
		})
	}
}

func TestExtractMetadataStringSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata json.RawMessage
		key      string
		want     []string
	}{
		{
			name:     "valid array",
			metadata: json.RawMessage(`{"mentions":["user-1","user-2"]}`),
			key:      "mentions",
			want:     []string{"user-1", "user-2"},
		},
		{
			name:     "empty array",
			metadata: json.RawMessage(`{"mentions":[]}`),
			key:      "mentions",
			want:     []string{},
		},
		{
			name:     "missing key",
			metadata: json.RawMessage(`{"other":"value"}`),
			key:      "mentions",
			want:     nil,
		},
		{
			name:     "nil metadata",
			metadata: nil,
			key:      "mentions",
			want:     nil,
		},
		{
			name:     "invalid JSON",
			metadata: json.RawMessage(`{invalid}`),
			key:      "mentions",
			want:     nil,
		},
		{
			name:     "not an array",
			metadata: json.RawMessage(`{"mentions":"not-an-array"}`),
			key:      "mentions",
			want:     nil,
		},
		{
			name:     "mixed array",
			metadata: json.RawMessage(`{"mentions":["user-1", 123, "user-2"]}`),
			key:      "mentions",
			want:     []string{"user-1", "user-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMetadataStringSlice(tt.metadata, tt.key)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestRankedItem_JSONSerialization(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	item := RankedItem{
		Item: Item{
			ID:        "item-1",
			OrgID:     "org-1",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		Score:    85.5,
		Priority: "P1",
	}

	data, err := json.Marshal(item)
	require.NoError(t, err)

	var parsed RankedItem
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, item.ID, parsed.ID)
	assert.Equal(t, item.Score, parsed.Score)
	assert.Equal(t, item.Priority, parsed.Priority)
}

func TestTypeWeightsExist(t *testing.T) {
	t.Parallel()

	expectedTypes := []string{
		"insight", "anomaly", "alert", "dispatch", "assignment", "task_created",
		"task_status_changed", "task_update", "task_updated", "commit", "comment",
		"message", "mention",
	}

	for _, itemType := range expectedTypes {
		weight, exists := TypeWeights[itemType]
		assert.True(t, exists, "TypeWeights should contain %q", itemType)
		assert.Greater(t, weight, 0.0, "Weight for %q should be positive", itemType)
	}
}

func TestPriorityWeightsExist(t *testing.T) {
	t.Parallel()

	expectedPriorities := []string{"P0", "P1", "P2", "P3", ""}

	for _, priority := range expectedPriorities {
		weight, exists := PriorityWeights[priority]
		assert.True(t, exists, "PriorityWeights should contain %q", priority)
		assert.Greater(t, weight, 0.0, "Weight for %q should be positive", priority)
	}
}

func TestPriorityConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "P0", PriorityCritical)
	assert.Equal(t, "P1", PriorityHigh)
	assert.Equal(t, "P2", PriorityMedium)
	assert.Equal(t, "P3", PriorityLow)
}

func TestMathFunctions(t *testing.T) {
	t.Parallel()

	// Test pow
	assert.InDelta(t, 1.0, pow(5.0, 0), 0.001)
	assert.InDelta(t, 5.0, pow(5.0, 1), 0.001)
	assert.InDelta(t, 25.0, pow(5.0, 2), 0.001)
	assert.InDelta(t, 8.0, pow(2.0, 3), 0.1)

	// Test ln
	assert.InDelta(t, 0.0, ln(1.0), 0.001)
	assert.InDelta(t, 0.693, ln(2.0), 0.01)

	// Test exp
	assert.InDelta(t, 1.0, exp(0), 0.001)
	assert.InDelta(t, 2.718, exp(1), 0.01)
}
