package feed

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSummarizerTaskCreated(t *testing.T) {
	s := NewSummarizer()

	agentName := "Frank"
	taskTitle := "Build the API"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "task_created",
		AgentName: &agentName,
		TaskTitle: &taskTitle,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Frank")
	require.Contains(t, summary, "created")
	require.Contains(t, summary, "Build the API")
}

func TestSummarizerTaskUpdate(t *testing.T) {
	s := NewSummarizer()

	agentName := "Nova"
	taskTitle := "Design homepage"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "task_update",
		AgentName: &agentName,
		TaskTitle: &taskTitle,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Nova")
	require.Contains(t, summary, "updated")
	require.Contains(t, summary, "Design homepage")
}

func TestSummarizerTaskStatusChanged(t *testing.T) {
	s := NewSummarizer()

	agentName := "Derek"
	taskTitle := "Fix bug"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "task_status_changed",
		Metadata:  json.RawMessage(`{"new_status":"done"}`),
		AgentName: &agentName,
		TaskTitle: &taskTitle,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Derek")
	require.Contains(t, summary, "Fix bug")
	require.Contains(t, summary, "done")
}

func TestSummarizerMessage(t *testing.T) {
	s := NewSummarizer()

	agentName := "Stone"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "message",
		Metadata:  json.RawMessage(`{"preview":"Hello team, let's discuss the roadmap"}`),
		AgentName: &agentName,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Stone")
	require.Contains(t, summary, "Hello team")
}

func TestSummarizerMessageLongPreview(t *testing.T) {
	s := NewSummarizer()

	agentName := "Stone"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "message",
		Metadata:  json.RawMessage(`{"preview":"This is a very long message that should be truncated because it exceeds the maximum length allowed for previews"}`),
		AgentName: &agentName,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "...")
	require.LessOrEqual(t, len(summary), 100) // Should be truncated
}

func TestSummarizerCommit(t *testing.T) {
	s := NewSummarizer()

	agentName := "Josh"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "commit",
		Metadata:  json.RawMessage(`{"repo":"pearl","message":"fix: handle edge case"}`),
		AgentName: &agentName,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Josh")
	require.Contains(t, summary, "pearl")
	require.Contains(t, summary, "fix: handle edge case")
}

func TestSummarizerComment(t *testing.T) {
	s := NewSummarizer()

	agentName := "Jeremy"
	taskTitle := "Review PR #42"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "comment",
		AgentName: &agentName,
		TaskTitle: &taskTitle,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Jeremy")
	require.Contains(t, summary, "commented")
	require.Contains(t, summary, "Review PR #42")
}

func TestSummarizerDispatch(t *testing.T) {
	s := NewSummarizer()

	agentName := "Frank"
	taskTitle := "Deploy to production"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "dispatch",
		AgentName: &agentName,
		TaskTitle: &taskTitle,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Frank")
	require.Contains(t, summary, "dispatched")
	require.Contains(t, summary, "Deploy to production")
}

func TestSummarizerNoAgentName(t *testing.T) {
	s := NewSummarizer()

	taskTitle := "Some task"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "task_created",
		TaskTitle: &taskTitle,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Someone")
	require.Contains(t, summary, "created")
}

func TestSummarizerUnknownType(t *testing.T) {
	s := NewSummarizer()

	agentName := "Max"
	taskTitle := "Mystery task"

	item := &Item{
		ID:        "test-id",
		OrgID:     "org-id",
		Type:      "unknown_action",
		AgentName: &agentName,
		TaskTitle: &taskTitle,
		CreatedAt: time.Now(),
	}

	summary := s.Summarize(item)
	require.Contains(t, summary, "Max")
	require.Contains(t, summary, "unknown_action")
	require.Contains(t, summary, "Mystery task")
}

func TestSummarizeItems(t *testing.T) {
	s := NewSummarizer()

	agentName := "Frank"
	taskTitle := "Task A"

	items := []*Item{
		{
			ID:        "1",
			OrgID:     "org",
			Type:      "task_created",
			AgentName: &agentName,
			TaskTitle: &taskTitle,
		},
		{
			ID:        "2",
			OrgID:     "org",
			Type:      "task_update",
			AgentName: &agentName,
			TaskTitle: &taskTitle,
		},
	}

	s.SummarizeItems(items)

	require.NotEmpty(t, items[0].Summary)
	require.NotEmpty(t, items[1].Summary)
	require.Contains(t, items[0].Summary, "created")
	require.Contains(t, items[1].Summary, "updated")
}
