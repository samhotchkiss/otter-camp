package githubsync

import (
	"fmt"
	"strings"
	"time"
)

type PullRequestWebhookTransition struct {
	State    string     `json:"state"`
	Draft    *bool      `json:"draft,omitempty"`
	Merged   *bool      `json:"merged,omitempty"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
}

func MapPullRequestWebhookTransition(action string, merged bool, now time.Time) (PullRequestWebhookTransition, error) {
	normalized := strings.TrimSpace(strings.ToLower(action))
	transition := PullRequestWebhookTransition{}

	switch normalized {
	case "opened", "reopened", "synchronize":
		transition.State = "open"
		transition.Merged = boolRef(false)
	case "ready_for_review":
		transition.State = "open"
		transition.Draft = boolRef(false)
	case "converted_to_draft":
		transition.State = "open"
		transition.Draft = boolRef(true)
	case "closed":
		transition.State = "closed"
		transition.Merged = boolRef(merged)
		closedAt := now.UTC()
		transition.ClosedAt = &closedAt
	default:
		return PullRequestWebhookTransition{}, fmt.Errorf("unsupported pull_request action %q", action)
	}

	return transition, nil
}

func boolRef(value bool) *bool {
	return &value
}
