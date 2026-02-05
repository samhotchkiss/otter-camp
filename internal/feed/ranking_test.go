package feed

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fixedTime() time.Time {
	return time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
}

func TestFeedPriorityOrder(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	// Create items with different priorities, all same type and time
	items := []*Item{
		{
			ID:        "low",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"priority":"P3"}`),
			CreatedAt: now,
		},
		{
			ID:        "critical",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"priority":"P0"}`),
			CreatedAt: now,
		},
		{
			ID:        "medium",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"priority":"P2"}`),
			CreatedAt: now,
		},
		{
			ID:        "high",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"priority":"P1"}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 4)
	// P0 should be first, then P1, P2, P3
	require.Equal(t, "critical", ranked[0].ID)
	require.Equal(t, "high", ranked[1].ID)
	require.Equal(t, "medium", ranked[2].ID)
	require.Equal(t, "low", ranked[3].ID)

	// Verify priority is extracted into RankedItem
	require.Equal(t, "P0", ranked[0].Priority)
	require.Equal(t, "P1", ranked[1].Priority)
}

func TestFeedRecencyOrder(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	// Create items with same priority but different times
	items := []*Item{
		{
			ID:        "old",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now.Add(-24 * time.Hour),
		},
		{
			ID:        "new",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "medium",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now.Add(-6 * time.Hour),
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 3)
	// Newer items should rank higher due to time decay
	require.Equal(t, "new", ranked[0].ID)
	require.Equal(t, "medium", ranked[1].ID)
	require.Equal(t, "old", ranked[2].ID)

	// Verify decay is applied - new item should have higher score
	require.Greater(t, ranked[0].Score, ranked[1].Score)
	require.Greater(t, ranked[1].Score, ranked[2].Score)
}

func TestFeedAgentBoost(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.SetPreferredAgents([]string{"agent-frank", "agent-nova"})

	now := fixedTime()
	frankID := "agent-frank"
	randomID := "agent-random"

	// Create two identical items, one from preferred agent
	items := []*Item{
		{
			ID:        "regular",
			Type:      "commit",
			AgentID:   &randomID,
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "preferred",
			Type:      "commit",
			AgentID:   &frankID,
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 2)
	// Preferred agent should rank higher
	require.Equal(t, "preferred", ranked[0].ID)
	require.Equal(t, "regular", ranked[1].ID)

	// Score difference should be the boost multiplier
	require.InDelta(t, ranked[0].Score, ranked[1].Score*AgentBoostMultiplier, 0.01)
}

func TestFeedTypeRanking(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	// Create items of different types: insights > updates > messages
	items := []*Item{
		{
			ID:        "message",
			Type:      "message",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "insight",
			Type:      "insight",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "task_update",
			Type:      "task_update",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "commit",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 4)
	// Should be ordered: insight > task_update/commit > message
	require.Equal(t, "insight", ranked[0].ID)
	require.Equal(t, "message", ranked[len(ranked)-1].ID)

	// Verify type weights are applied
	require.Greater(t, ranked[0].Score, ranked[1].Score)
}

func TestFeedUserInvolvement(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.SetCurrentUser("user-sam")

	now := fixedTime()

	// Create items, one mentioning the current user
	items := []*Item{
		{
			ID:        "regular",
			Type:      "message",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "mentioned",
			Type:      "message",
			Metadata:  json.RawMessage(`{"mentions":["user-sam","user-other"]}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 2)
	// Mentioned item should rank higher
	require.Equal(t, "mentioned", ranked[0].ID)
	require.Greater(t, ranked[0].Score, ranked[1].Score)
}

func TestFeedTimeDecay(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	// Create identical items at different ages
	items := []*Item{
		{
			ID:        "just-now",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "half-life",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now.Add(-DecayHalfLife),
		},
		{
			ID:        "two-half-lives",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now.Add(-2 * DecayHalfLife),
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 3)

	// Scores should decay exponentially
	// After one half-life, score should be ~50%
	// After two half-lives, score should be ~25%
	require.InDelta(t, ranked[0].Score/2, ranked[1].Score, ranked[0].Score*0.1)
	require.InDelta(t, ranked[0].Score/4, ranked[2].Score, ranked[0].Score*0.1)
}

func TestForYouFeed(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.SetCurrentUser("user-sam")
	r.SetPreferredAgents([]string{"agent-frank"})

	now := fixedTime()
	frankID := "agent-frank"
	randomID := "agent-random"

	items := []*Item{
		{
			ID:        "random-low-priority",
			Type:      "message",
			AgentID:   &randomID,
			Metadata:  json.RawMessage(`{"priority":"P3"}`),
			CreatedAt: now,
		},
		{
			ID:        "mentions-me",
			Type:      "message",
			AgentID:   &randomID,
			Metadata:  json.RawMessage(`{"mentions":["user-sam"]}`),
			CreatedAt: now,
		},
		{
			ID:        "from-preferred",
			Type:      "commit",
			AgentID:   &frankID,
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "high-priority",
			Type:      "task_created",
			AgentID:   &randomID,
			Metadata:  json.RawMessage(`{"priority":"P0"}`),
			CreatedAt: now,
		},
		{
			ID:        "insight",
			Type:      "insight",
			AgentID:   &randomID,
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	forYou := r.ForYouFeed(items)

	// Should include: mentions-me, from-preferred, high-priority, insight
	// Should exclude: random-low-priority (not relevant)
	require.Len(t, forYou, 4)

	ids := make([]string, len(forYou))
	for i, item := range forYou {
		ids[i] = item.ID
	}

	require.Contains(t, ids, "mentions-me")
	require.Contains(t, ids, "from-preferred")
	require.Contains(t, ids, "high-priority")
	require.Contains(t, ids, "insight")
	require.NotContains(t, ids, "random-low-priority")
}

func TestAllActivityFeed(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	items := []*Item{
		{
			ID:        "a",
			Type:      "message",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "b",
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
		{
			ID:        "c",
			Type:      "insight",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	allActivity := r.AllActivityFeed(items)

	// Should include all items
	require.Len(t, allActivity, 3)
}

func TestRankedItemHasPriority(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	items := []*Item{
		{
			ID:        "with-priority",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"priority":"P1"}`),
			CreatedAt: now,
		},
		{
			ID:        "task-priority-field",
			Type:      "task_update",
			Metadata:  json.RawMessage(`{"task_priority":"P2"}`),
			CreatedAt: now,
		},
		{
			ID:        "no-priority",
			Type:      "message",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	// Find each item and verify priority
	for _, item := range ranked {
		switch item.ID {
		case "with-priority":
			require.Equal(t, "P1", item.Priority)
		case "task-priority-field":
			require.Equal(t, "P2", item.Priority)
		case "no-priority":
			require.Empty(t, item.Priority)
		}
	}
}

func TestAssigneeInvolvement(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.SetCurrentUser("user-sam")

	now := fixedTime()

	items := []*Item{
		{
			ID:        "assigned-to-me",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"assignee":"user-sam"}`),
			CreatedAt: now,
		},
		{
			ID:        "assigned-to-other",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"assignee":"user-other"}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 2)
	require.Equal(t, "assigned-to-me", ranked[0].ID)
	require.Greater(t, ranked[0].Score, ranked[1].Score)
}

func TestAssigneeIDInvolvement(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.SetCurrentUser("user-sam")

	now := fixedTime()

	items := []*Item{
		{
			ID:        "assigned-to-me",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"assignee_id":"user-sam"}`),
			CreatedAt: now,
		},
		{
			ID:        "not-assigned",
			Type:      "task_created",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 2)
	require.Equal(t, "assigned-to-me", ranked[0].ID)
}

func TestEmptyItems(t *testing.T) {
	r := NewRanker()

	ranked := r.RankItems([]*Item{})
	require.Empty(t, ranked)

	forYou := r.ForYouFeed([]*Item{})
	require.Empty(t, forYou)

	allActivity := r.AllActivityFeed([]*Item{})
	require.Empty(t, allActivity)
}

func TestUnknownTypeDefaultWeight(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	items := []*Item{
		{
			ID:        "unknown",
			Type:      "some_unknown_type",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 1)
	require.InDelta(t, DefaultTypeWeight, ranked[0].Score, 0.01)
}

func TestRankItemsReplaceModeOnlyLatestPerSource(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.Mode = RankingModeReplace

	now := fixedTime()
	taskID := "task-1"
	agentID := "agent-1"

	items := []*Item{
		{
			ID:        "old-high-score",
			TaskID:    &taskID,
			AgentID:   &agentID,
			Type:      "task_created",
			Metadata:  json.RawMessage(`{"priority":"P0"}`),
			CreatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:        "new-low-score",
			TaskID:    &taskID,
			AgentID:   &agentID,
			Type:      "message",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now.Add(-1 * time.Hour),
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 1)
	require.Equal(t, "new-low-score", ranked[0].ID)
}

func TestRankItemsReplaceModeFallsBackToAgentID(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.Mode = RankingModeReplace

	now := fixedTime()
	agent1 := "agent-1"
	agent2 := "agent-2"

	items := []*Item{
		{
			ID:        "agent-1-old",
			AgentID:   &agent1,
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:        "agent-1-new",
			AgentID:   &agent1,
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now.Add(-1 * time.Hour),
		},
		{
			ID:        "agent-2",
			AgentID:   &agent2,
			Type:      "commit",
			Metadata:  json.RawMessage(`{}`),
			CreatedAt: now,
		},
	}

	ranked := r.RankItems(items)

	require.Len(t, ranked, 2)

	ids := []string{ranked[0].ID, ranked[1].ID}
	require.Contains(t, ids, "agent-1-new")
	require.NotContains(t, ids, "agent-1-old")
	require.Contains(t, ids, "agent-2")
}

func TestRankItemsAugmentModeAccumulatesItems(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime
	r.Mode = RankingModeAugment

	now := fixedTime()
	taskID := "task-1"

	items := []*Item{
		{ID: "a", TaskID: &taskID, Type: "message", Metadata: json.RawMessage(`{}`), CreatedAt: now},
		{ID: "b", TaskID: &taskID, Type: "message", Metadata: json.RawMessage(`{}`), CreatedAt: now.Add(-time.Minute)},
	}

	ranked := r.RankItems(items)
	require.Len(t, ranked, 2)
}

func TestUrgencyScorePriorityFlagBoost(t *testing.T) {
	r := NewRanker()
	r.Now = fixedTime

	now := fixedTime()

	regular := &Item{
		ID:        "regular",
		Type:      "commit",
		Metadata:  json.RawMessage(`{}`),
		CreatedAt: now,
	}
	priorityFlag := &Item{
		ID:        "priority-flag",
		Type:      "commit",
		Metadata:  json.RawMessage(`{"priority":true}`),
		CreatedAt: now,
	}

	require.Greater(t, r.UrgencyScore(priorityFlag), r.UrgencyScore(regular))
}
