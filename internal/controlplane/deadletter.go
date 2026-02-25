package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type DeadLetterHandler struct {
	service *runService
}

func NewDeadLetterHandler(service *runService) *DeadLetterHandler {
	return &DeadLetterHandler{service: service}
}

func (h *DeadLetterHandler) Handle(ctx context.Context, runID uuid.UUID, reason string) error {
	if h == nil || h.service == nil || runID == uuid.Nil {
		return nil
	}

	runRecord, err := h.service.runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	attemptCount := h.attemptCount(ctx, runRecord.ID)
	failureClass := strings.TrimSpace(stringPointerValue(runRecord.FailureClass))
	lastError := strings.TrimSpace(reason)
	if lastError == "" {
		lastError = strings.TrimSpace(stringPointerValue(runRecord.FailureReason))
	}
	if lastError == "" {
		lastError = "dead lettered"
	}

	eventPayload := map[string]any{
		"dead_lettered": true,
		"reason":        lastError,
	}
	if failureClass != "" {
		eventPayload["failure_class"] = failureClass
	}
	if _, err := h.service.appendRunEvent(ctx, runRecord.ID, nil, nil, "run_failed", "system", nil, eventPayload); err != nil {
		return err
	}

	if err := h.service.publishDomainEvent(ctx, runRecord.OrganizationID, "run.dead_lettered", "system", nil, map[string]any{
		"run_id":        runRecord.ID.String(),
		"task_id":       uuidToString(runRecord.TaskID),
		"failure_class": failureClass,
		"attempt_count": attemptCount,
		"last_error":    lastError,
	}); err != nil {
		return err
	}

	if err := h.service.recordRunDeadLetteredTaskEvent(ctx, runRecord, "system"); err != nil {
		return err
	}
	h.createEscalationInboxItem(ctx, runRecord, attemptCount, failureClass, lastError)
	return nil
}

func (h *DeadLetterHandler) attemptCount(ctx context.Context, runID uuid.UUID) int {
	if h == nil || h.service == nil || h.service.runSteps == nil || h.service.attempts == nil {
		return 0
	}
	steps, err := h.service.runSteps.ListByRun(ctx, runID)
	if err != nil {
		return 0
	}
	total := 0
	for _, step := range steps {
		latest, latestErr := h.service.attempts.GetLatestByStep(ctx, step.ID)
		if latestErr != nil {
			continue
		}
		if latest.AttemptNumber > 0 {
			total += latest.AttemptNumber
		}
	}
	if total == 0 {
		return 1
	}
	return total
}

func (h *DeadLetterHandler) createEscalationInboxItem(ctx context.Context, runRecord Run, attemptCount int, failureClass, lastError string) {
	if h == nil || h.service == nil || h.service.inbox == nil || runRecord.ProjectID == nil {
		return
	}

	targetUserID, err := h.service.resolvePMTargetUser(ctx, *runRecord.ProjectID)
	if err != nil {
		h.service.logger.Warn("dead letter resolve PM failed", "run_id", runRecord.ID, "error", err)
		return
	}
	summary := fmt.Sprintf("Run dead-lettered after %d attempts", attemptCount)
	payload, err := marshalPayload(map[string]any{
		"run_id":         runRecord.ID.String(),
		"failure_class":  failureClass,
		"last_error":     lastError,
		"attempt_count":  attemptCount,
		"requested_type": "escalation",
	})
	if err != nil {
		h.service.logger.Warn("dead letter action payload marshal failed", "run_id", runRecord.ID, "error", err)
		return
	}

	item := repo.InboxItem{
		OrganizationID:  runRecord.OrganizationID,
		TargetUserID:    targetUserID,
		ItemType:        "escalation",
		SourceProjectID: runRecord.ProjectID,
		SourceTaskID:    runRecord.TaskID,
		CreatedByType:   "system",
		CreatedByID:     nil,
		Title:           summary,
		Body:            &summary,
		ActionPayload:   payload,
	}
	if _, err := h.service.inbox.Create(ctx, item); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			item.ItemType = "system_alert"
			if _, fallbackErr := h.service.inbox.Create(ctx, item); fallbackErr != nil {
				h.service.logger.Warn("dead letter inbox fallback failed", "run_id", runRecord.ID, "error", fallbackErr)
				return
			}
			return
		}
		h.service.logger.Warn("dead letter inbox create failed", "run_id", runRecord.ID, "error", err)
	}
}
