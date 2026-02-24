package push

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type DeliveryGate interface {
	ShouldDeliver(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID, urgencyTier, eventType string) (bool, error)
}

type TokenStore interface {
	GetTokens(ctx context.Context, userID uuid.UUID) ([]PushToken, error)
}

type DeliveryConsumer struct {
	logger       *slog.Logger
	preferences  DeliveryGate
	tokens       TokenStore
	adapter      Adapter
	participants *repo.ProjectTaskParticipantRepo
	inbox        *repo.InboxItemRepo
}

type DeliveryConsumerOptions struct {
	Pool        *pgxpool.Pool
	Logger      *slog.Logger
	Preferences DeliveryGate
	Tokens      TokenStore
	Adapter     Adapter
}

func NewDeliveryConsumer(opts DeliveryConsumerOptions) (*DeliveryConsumer, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("pool is required")
	}
	if opts.Preferences == nil {
		return nil, fmt.Errorf("preferences service is required")
	}
	if opts.Tokens == nil {
		return nil, fmt.Errorf("token repository is required")
	}
	if opts.Adapter == nil {
		return nil, fmt.Errorf("push adapter is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &DeliveryConsumer{
		logger:       logger,
		preferences:  opts.Preferences,
		tokens:       opts.Tokens,
		adapter:      opts.Adapter,
		participants: repo.NewProjectTaskParticipantRepo(opts.Pool),
		inbox:        repo.NewInboxItemRepo(opts.Pool),
	}, nil
}

func (c *DeliveryConsumer) Consume(ctx context.Context, event eventbus.DomainEvent) error {
	urgency := mapUrgency(event)
	if urgency == "" {
		return nil
	}

	payload := map[string]any{}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode event payload: %w", err)
		}
	}

	targets, err := c.resolveTargetUsers(ctx, event, payload)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	projectID, _ := parseUUID(payload, "project_id")
	var projectIDPtr *uuid.UUID
	if projectID != uuid.Nil {
		projectIDPtr = &projectID
	}

	pushPayload := c.BuildPayload(event, urgency)
	for _, userID := range targets {
		shouldDeliver, err := c.preferences.ShouldDeliver(ctx, userID, projectIDPtr, urgency, event.EventType)
		if err != nil {
			return err
		}
		if !shouldDeliver {
			c.logger.Info("push delivery skipped", "reason", "preference_gate", "user_id", userID, "event_type", event.EventType)
			continue
		}

		tokens, err := c.tokens.GetTokens(ctx, userID)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if err := c.adapter.Send(ctx, token, pushPayload); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *DeliveryConsumer) BuildPayload(event eventbus.DomainEvent, urgencyTier string) PushPayload {
	payload := map[string]any{}
	if len(event.Payload) > 0 {
		_ = json.Unmarshal(event.Payload, &payload)
	}
	taskID, _ := parseUUID(payload, "task_id")
	itemID := ""
	if taskID != uuid.Nil {
		itemID = taskID.String()
	}

	switch strings.TrimSpace(event.EventType) {
	case "task.blocked":
		return PushPayload{
			Title:    "Task blocked",
			Body:     "Waiting on dependency resolution",
			Category: "task_blocked",
			DeepLink: fmt.Sprintf("ottercamp://tasks/%s", taskID),
			ItemID:   itemID,
		}
	case "run.dead_lettered", "run_dead_lettered":
		return PushPayload{
			Title:    "Run dead-lettered",
			Body:     "A run requires immediate attention",
			Category: "run_dead_lettered",
			DeepLink: "ottercamp://inbox",
			ItemID:   itemID,
		}
	case "project.push_failed":
		return PushPayload{
			Title:    "Project push failed",
			Body:     "A project push failed and needs attention",
			Category: "project_push_failed",
			DeepLink: "ottercamp://inbox",
			ItemID:   itemID,
		}
	case "task.completed":
		return PushPayload{
			Title:    "Task completed",
			Body:     "A task has been completed",
			Category: "task_completed",
			DeepLink: fmt.Sprintf("ottercamp://tasks/%s", taskID),
			ItemID:   itemID,
		}
	default:
		return PushPayload{
			Title:    "OtterCamp notification",
			Body:     "You have a new update",
			Category: urgencyTier,
			DeepLink: "ottercamp://inbox",
			ItemID:   itemID,
		}
	}
}

func (c *DeliveryConsumer) resolveTargetUsers(ctx context.Context, event eventbus.DomainEvent, payload map[string]any) ([]uuid.UUID, error) {
	seen := make(map[uuid.UUID]struct{})
	add := func(id uuid.UUID) {
		if id == uuid.Nil {
			return
		}
		seen[id] = struct{}{}
	}

	for _, key := range []string{"target_user_id", "user_id", "pm_user_id", "acted_by_user_id"} {
		if id, ok := parseUUID(payload, key); ok {
			add(id)
		}
	}

	eventType := strings.TrimSpace(event.EventType)
	taskID, hasTask := parseUUID(payload, "task_id")
	if hasTask {
		switch eventType {
		case "task.blocked", "task.review_requested", "task.completed", "task.status_changed":
			participants, err := c.participants.ListActive(ctx, taskID)
			if err != nil && err != repo.ErrNotFound {
				return nil, err
			}
			for _, participant := range participants {
				if participant.ParticipantType == "human_user" {
					add(participant.ParticipantID)
				}
			}
		}
	}

	if eventType == "inbox.item_created" {
		if inboxID, ok := parseUUID(payload, "inbox_item_id"); ok {
			item, err := c.inbox.GetByID(ctx, inboxID)
			if err == nil && item.TargetUserID != nil {
				add(*item.TargetUserID)
			}
		}
	}

	users := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		users = append(users, id)
	}
	return users, nil
}

func mapUrgency(event eventbus.DomainEvent) string {
	eventType := strings.TrimSpace(event.EventType)
	switch eventType {
	case "task.blocked", "task.review_requested":
		return TierHigh
	case "run.dead_lettered", "run_dead_lettered", "project.push_failed":
		return TierUrgent
	case "task.completed":
		return TierNormal
	case "inbox.item_created":
		payload := map[string]any{}
		if len(event.Payload) > 0 {
			_ = json.Unmarshal(event.Payload, &payload)
		}
		if value, ok := payload["urgency"].(string); ok {
			normalized := normalizeTier(value)
			if normalized != "" {
				return normalized
			}
		}
		return TierNormal
	case "task.status_changed":
		payload := map[string]any{}
		if len(event.Payload) > 0 {
			_ = json.Unmarshal(event.Payload, &payload)
		}
		if status, ok := payload["to_status"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(status)) {
			case "blocked":
				return TierHigh
			case "done":
				return TierNormal
			}
		}
		return ""
	default:
		return ""
	}
}

func parseUUID(payload map[string]any, key string) (uuid.UUID, bool) {
	value, ok := payload[key]
	if !ok {
		return uuid.Nil, false
	}
	str, ok := value.(string)
	if !ok {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(strings.TrimSpace(str))
	if err != nil {
		return uuid.Nil, false
	}
	return parsed, true
}
