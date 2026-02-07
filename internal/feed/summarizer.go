// Package feed provides feed item processing and summarization.
package feed

import (
	"encoding/json"
	"fmt"
	"time"
)

// Item represents a feed item with optional related entities.
type Item struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	TaskID    *string         `json:"task_id,omitempty"`
	AgentID   *string         `json:"agent_id,omitempty"`
	Type      string          `json:"type"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`

	// Related entities (populated by enrichment)
	TaskTitle *string `json:"task_title,omitempty"`
	AgentName *string `json:"agent_name,omitempty"`

	// Computed summary
	Summary string `json:"summary,omitempty"`
}

// Summarizer generates human-readable summaries for feed items.
type Summarizer struct{}

// NewSummarizer creates a new Summarizer instance.
func NewSummarizer() *Summarizer {
	return &Summarizer{}
}

// Summarize generates a human-readable summary for a feed item.
func (s *Summarizer) Summarize(item *Item) string {
	agent := "Someone"
	if item.AgentName != nil && *item.AgentName != "" {
		agent = *item.AgentName
	}

	task := ""
	if item.TaskTitle != nil && *item.TaskTitle != "" {
		task = *item.TaskTitle
	}

	switch item.Type {
	case "task_created":
		if task != "" {
			return fmt.Sprintf("%s created task \"%s\"", agent, task)
		}
		return fmt.Sprintf("%s created a task", agent)

	case "task_update", "task_updated":
		if task != "" {
			return fmt.Sprintf("%s updated task \"%s\"", agent, task)
		}
		return fmt.Sprintf("%s updated a task", agent)

	case "task_status_changed":
		status := extractMetadataString(item.Metadata, "new_status")
		if task != "" && status != "" {
			return fmt.Sprintf("%s changed task \"%s\" to %s", agent, task, status)
		}
		if task != "" {
			return fmt.Sprintf("%s changed status of task \"%s\"", agent, task)
		}
		return fmt.Sprintf("%s changed a task status", agent)

	case "message":
		preview := extractMetadataString(item.Metadata, "preview")
		if preview != "" {
			if len(preview) > 50 {
				preview = preview[:47] + "..."
			}
			return fmt.Sprintf("%s: \"%s\"", agent, preview)
		}
		return fmt.Sprintf("%s sent a message", agent)

	case "comment":
		if task != "" {
			return fmt.Sprintf("%s commented on \"%s\"", agent, task)
		}
		return fmt.Sprintf("%s added a comment", agent)

	case "commit":
		repo := extractMetadataString(item.Metadata, "repo")
		msg := extractMetadataString(item.Metadata, "message")
		if repo != "" && msg != "" {
			if len(msg) > 40 {
				msg = msg[:37] + "..."
			}
			return fmt.Sprintf("%s committed to %s: \"%s\"", agent, repo, msg)
		}
		if repo != "" {
			return fmt.Sprintf("%s committed to %s", agent, repo)
		}
		return fmt.Sprintf("%s made a commit", agent)

	case "dispatch":
		if task != "" {
			return fmt.Sprintf("Task \"%s\" dispatched to %s", task, agent)
		}
		return fmt.Sprintf("Task dispatched to %s", agent)

	case "assignment":
		if task != "" {
			return fmt.Sprintf("%s was assigned to \"%s\"", agent, task)
		}
		return fmt.Sprintf("%s received an assignment", agent)

	default:
		if task != "" {
			return fmt.Sprintf("%s: %s on \"%s\"", agent, item.Type, task)
		}
		return fmt.Sprintf("%s: %s", agent, item.Type)
	}
}

// SummarizeItems adds summaries to a slice of feed items.
func (s *Summarizer) SummarizeItems(items []*Item) {
	for _, item := range items {
		item.Summary = s.Summarize(item)
	}
}

// extractMetadataString extracts a string value from metadata JSON.
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
