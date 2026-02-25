package delivery

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
)

type EventConsumerOptions struct {
	Bus     eventbus.EventBus
	Updater deployCompletionHandler
	Logger  *slog.Logger
}

type deployCompletionHandler interface {
	OnDeployCompleted(ctx context.Context, deployTaskID uuid.UUID) error
}

type EventConsumer struct {
	bus     eventbus.EventBus
	updater deployCompletionHandler
	logger  *slog.Logger
}

func NewEventConsumer(opts EventConsumerOptions) *EventConsumer {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EventConsumer{
		bus:     opts.Bus,
		updater: opts.Updater,
		logger:  logger,
	}
}

func (c *EventConsumer) Start(ctx context.Context) func() {
	if c == nil || c.bus == nil || c.updater == nil {
		return func() {}
	}

	sub := c.bus.Subscribe(deliveryConsumerName, nil, c.handle)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			c.bus.Unsubscribe(sub)
		})
	}
	go func() {
		<-ctx.Done()
		stop()
	}()
	return stop
}

func (c *EventConsumer) handle(ctx context.Context, event eventbus.DomainEvent) error {
	switch event.EventType {
	case "task.completed":
		taskID, taskType := parseTaskCompletionPayload(event.Payload)
		if taskID == uuid.Nil || taskType != deliveryTaskTypeDeploy {
			return nil
		}
		return c.updater.OnDeployCompleted(ctx, taskID)
	case "task.status_changed":
		taskID, toStatus := parseTaskStatusPayload(event.Payload)
		if taskID == uuid.Nil || toStatus != "done" {
			return nil
		}
		return c.updater.OnDeployCompleted(ctx, taskID)
	case "run.completed":
		// Reserved for push-run completion integration.
		return nil
	default:
		return nil
	}
}

func parseTaskCompletionPayload(payload json.RawMessage) (uuid.UUID, string) {
	if len(payload) == 0 {
		return uuid.Nil, ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return uuid.Nil, ""
	}
	taskID := parseUUIDFromMap(parsed, "task_id")
	taskType, _ := parsed["task_type"].(string)
	return taskID, strings.TrimSpace(taskType)
}

func parseTaskStatusPayload(payload json.RawMessage) (uuid.UUID, string) {
	if len(payload) == 0 {
		return uuid.Nil, ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return uuid.Nil, ""
	}
	taskID := parseUUIDFromMap(parsed, "task_id")
	toStatus, _ := parsed["to_status"].(string)
	return taskID, strings.TrimSpace(toStatus)
}

func parseUUIDFromMap(values map[string]any, key string) uuid.UUID {
	raw, _ := values[key].(string)
	if raw == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil
	}
	return id
}
