package feed

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestComputeSummaryFilters(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	agentA := "agent-a"
	agentB := "agent-b"
	projectA := "project-a"
	projectB := "project-b"

	items := []*Item{
		{
			ID:        "1",
			Type:      "commit",
			AgentID:   &agentA,
			Metadata:  json.RawMessage(`{"project_id":"project-a"}`),
			CreatedAt: now.Add(-90 * time.Minute),
		},
		{
			ID:        "2",
			Type:      "task_update",
			AgentID:   &agentA,
			Metadata:  json.RawMessage(`{"project":"project-b"}`),
			CreatedAt: now.Add(-30 * time.Minute),
		},
		{
			ID:        "3",
			Type:      "comment",
			AgentID:   &agentB,
			Metadata:  json.RawMessage(`{"project_id":"project-a"}`),
			CreatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:        "4",
			Type:      "comment",
			AgentID:   &agentB,
			Metadata:  json.RawMessage(`{"project_id":"project-a"}`),
			CreatedAt: now.Add(-25 * time.Hour),
		},
	}

	from := now.Add(-3 * time.Hour)
	to := now.Add(-45 * time.Minute)

	summary := ComputeSummary(items, SummaryFilter{
		ProjectID: projectA,
		AgentID:   agentB,
		From:      &from,
		To:        &to,
	})

	require.Equal(t, 1, summary.Total)
	require.Equal(t, 1, summary.ByType["comment"])
	require.Contains(t, summary.ByAgent, agentB)
	require.Equal(t, 1, summary.ByAgent[agentB].Total)
	require.NotContains(t, summary.ByAgent, agentA)

	require.Equal(t, projectA, projectIDFromItem(items[0]))
	require.Equal(t, projectB, projectIDFromItem(items[1]))
}

func TestComputeSummaryAggregationAndUnread(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	agentA := "agent-a"
	agentB := "agent-b"
	project := "project-x"

	items := []*Item{
		{
			ID:        "a1",
			Type:      "commit",
			AgentID:   &agentA,
			Metadata:  json.RawMessage(`{"project_id":"project-x"}`),
			CreatedAt: now.Add(-30 * time.Minute),
		},
		{
			ID:        "a2",
			Type:      "commit",
			AgentID:   &agentA,
			Metadata:  json.RawMessage(`{"project_id":"project-x"}`),
			CreatedAt: now.Add(-90 * time.Minute),
		},
		{
			ID:        "b1",
			Type:      "comment",
			AgentID:   &agentB,
			Metadata:  json.RawMessage(`{"project_id":"project-x"}`),
			CreatedAt: now.Add(-80 * time.Minute),
		},
		{
			ID:        "b2",
			Type:      "task_update",
			AgentID:   &agentB,
			Metadata:  json.RawMessage(`{"project_id":"project-x"}`),
			CreatedAt: now.Add(-130 * time.Minute),
		},
	}

	lastRead := now.Add(-85 * time.Minute)

	summary := ComputeSummary(items, SummaryFilter{
		ProjectID:  project,
		BucketSize: time.Hour,
		LastReadAt: &lastRead,
	})

	require.Equal(t, 4, summary.Total)
	require.Equal(t, 2, summary.Unread)
	require.Equal(t, 2, summary.ByType["commit"])
	require.Equal(t, 1, summary.ByType["comment"])
	require.Equal(t, 1, summary.ByType["task_update"])

	require.Contains(t, summary.ByAgent, agentA)
	require.Contains(t, summary.ByAgent, agentB)
	require.Equal(t, 2, summary.ByAgent[agentA].Total)
	require.Equal(t, 1, summary.ByAgent[agentA].Unread)
	require.Equal(t, 2, summary.ByAgent[agentB].Total)
	require.Equal(t, 1, summary.ByAgent[agentB].Unread)

	require.Len(t, summary.ByTime, 3)
	require.Equal(t, now.Add(-3*time.Hour).Truncate(time.Hour), summary.ByTime[0].Start)
	require.Equal(t, 1, summary.ByTime[0].Total)
	require.Equal(t, now.Add(-2*time.Hour).Truncate(time.Hour), summary.ByTime[1].Start)
	require.Equal(t, 2, summary.ByTime[1].Total)
	require.Equal(t, 1, summary.ByTime[1].ByType["commit"])
	require.Equal(t, 1, summary.ByTime[1].ByType["comment"])
	require.Equal(t, 1, summary.ByTime[1].ByAgent[agentA])
	require.Equal(t, 1, summary.ByTime[1].ByAgent[agentB])
	require.Equal(t, now.Add(-1*time.Hour).Truncate(time.Hour), summary.ByTime[2].Start)
	require.Equal(t, 1, summary.ByTime[2].Total)

	require.Len(t, summary.ByAgent[agentA].ByTime, 2)
	require.Equal(t, now.Add(-2*time.Hour).Truncate(time.Hour), summary.ByAgent[agentA].ByTime[0].Start)
	require.Equal(t, 1, summary.ByAgent[agentA].ByTime[0].Total)
	require.Equal(t, now.Add(-1*time.Hour).Truncate(time.Hour), summary.ByAgent[agentA].ByTime[1].Start)
	require.Equal(t, 1, summary.ByAgent[agentA].ByTime[1].Total)
}

func TestComputeSummaryAgentFilterFromMetadata(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	items := []*Item{
		{
			ID:        "1",
			Type:      "message",
			Metadata:  json.RawMessage(`{"agent_id":"agent-meta"}`),
			CreatedAt: now,
		},
		{
			ID:        "2",
			Type:      "message",
			Metadata:  json.RawMessage(`{"agent_id":"agent-other"}`),
			CreatedAt: now,
		},
	}

	summary := ComputeSummary(items, SummaryFilter{AgentID: "agent-meta"})
	require.Equal(t, 1, summary.Total)
	require.Contains(t, summary.ByAgent, "agent-meta")
}
