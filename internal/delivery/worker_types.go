package delivery

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	MergeExecuteJobType      = "merge_execute"
	DeployTaskCreateJobType  = "deploy_task_create"
	deliveryConsumerName     = "delivery_worker"
	deliveryDefaultPriority  = 100
	deliverySystemActorType  = "system"
	deliverySystemAlertType  = "system_alert"
	deliveryUrgencyUrgent    = "urgent"
	deliveryModeContinuous   = "continuous"
	deliveryModeGated        = "gated"
	deliveryModeScheduled    = "scheduled"
	deliveryTaskTypeDeploy   = "deploy"
	maxPushAttempts          = 3
	pushRetryBase            = 5 * time.Second
	mergeRetryDelay          = 5 * time.Second
	scheduledWindowTolerance = 5 * time.Minute
)

type mergeExecutePayload struct {
	MergeQueueEntryID uuid.UUID `json:"merge_queue_entry_id"`
	ProjectID         uuid.UUID `json:"project_id"`
}

type pushJobPayload struct {
	ProjectID         uuid.UUID  `json:"project_id"`
	RemoteID          uuid.UUID  `json:"remote_id"`
	EnvironmentID     uuid.UUID  `json:"environment_id"`
	CommitSHA         string     `json:"commit_sha"`
	Attempt           int        `json:"attempt"`
	TriggeredByTaskID uuid.UUID  `json:"triggered_by_task_id"`
	DeliveryMode      string     `json:"delivery_mode"`
	MergeQueueEntryID uuid.UUID  `json:"merge_queue_entry_id"`
	DeployTaskID      uuid.UUID  `json:"deploy_task_id"`
	RunAfter          *time.Time `json:"-"`
}

type deployTaskCreatePayload struct {
	ProjectID         uuid.UUID `json:"project_id"`
	CommitSHA         string    `json:"commit_sha"`
	TriggeredByTaskID uuid.UUID `json:"triggered_by_task_id"`
	DeliveryMode      string    `json:"delivery_mode"`
	MergeQueueEntryID uuid.UUID `json:"merge_queue_entry_id"`
	RemoteID          uuid.UUID `json:"remote_id"`
}

type rollbackTaskMetadata struct {
	TaskType        string `json:"task_type"`
	Rollback        bool   `json:"rollback"`
	TargetCommitSHA string `json:"target_commit_sha"`
}

func decodePayload[T any](raw json.RawMessage, into *T) error {
	if len(raw) == 0 {
		return fmt.Errorf("job payload is required")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}

func trimSHA(value string) string {
	return strings.TrimSpace(value)
}

func shortSHA(value string) string {
	trimmed := trimSHA(value)
	if len(trimmed) <= 8 {
		return trimmed
	}
	return trimmed[:8]
}

func computePushRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// attempt 1 => 5s, attempt 2 => 10s, attempt 3 => 20s
	base := float64(pushRetryBase) * math.Pow(2, float64(attempt-1))
	return time.Duration(base)
}

func addJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	min := 0.75
	max := 1.25
	factor := min + (rand.Float64() * (max - min))
	return time.Duration(float64(delay) * factor)
}

func advisoryLockKey(projectID uuid.UUID) string {
	return projectID.String()
}

func isWithinWindow(now time.Time, scheduled *time.Time, tolerance time.Duration) bool {
	if scheduled == nil {
		return false
	}
	delta := now.UTC().Sub(scheduled.UTC())
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}
