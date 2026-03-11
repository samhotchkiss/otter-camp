package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *runService) GetExecutionRuntimeState(ctx context.Context, taskID, sessionID uuid.UUID) (RuntimeState, error) {
	if s.runtime == nil {
		return RuntimeState{}, ErrNotFound
	}
	scope, ok := executionScopeFromIDs(taskID, sessionID)
	if !ok {
		return RuntimeState{}, ErrNotFound
	}
	return s.runtime.GetByScope(ctx, scope.Type, scope.ID)
}

func (s *runService) RetireRuntimeStateForTask(ctx context.Context, taskID uuid.UUID, reason string) error {
	if s.runtime == nil || taskID == uuid.Nil {
		return nil
	}
	states, err := s.runtime.ListByTask(ctx, taskID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	for _, state := range states {
		if err := s.retireRuntimeState(ctx, state, now, reason); err != nil {
			return err
		}
	}
	return nil
}

func (s *runService) RetireRuntimeStateForProject(ctx context.Context, projectID uuid.UUID, reason string) error {
	if s.runtime == nil || projectID == uuid.Nil {
		return nil
	}
	states, err := s.runtime.ListByProject(ctx, projectID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	for _, state := range states {
		if err := s.retireRuntimeState(ctx, state, now, reason); err != nil {
			return err
		}
	}
	return nil
}

func (s *runService) runtimeStateForRun(ctx context.Context, runRecord Run) (RuntimeState, bool, error) {
	if s.runtime == nil {
		return RuntimeState{}, false, nil
	}
	scope, ok := executionScopeFromIDs(uuidPointerValue(runRecord.TaskID), uuidPointerValue(runRecord.SessionID))
	if !ok {
		return RuntimeState{}, false, nil
	}
	state, err := s.runtime.GetByScope(ctx, scope.Type, scope.ID)
	if errors.Is(err, ErrNotFound) {
		return RuntimeState{}, false, nil
	}
	if err != nil {
		return RuntimeState{}, false, err
	}
	return state, true, nil
}

func (s *runService) syncRuntimeStateActive(ctx context.Context, state RuntimeState, runRecord Run, event string) error {
	contract := runtimeContractFromStateAndRun(state, runRecord)
	now := s.clock.Now().UTC()
	contract.Status = "active"
	contract.LastProgressAt = &now
	contract.LastProgressEvent = strings.TrimSpace(event)
	contract.PendingWakeReason = ""
	contract.DeferredRunID = nil
	contract.ResumeDisposition = ""
	contract.FailureClass = ""
	contract.FailureReason = ""
	contract.RetiredAt = nil
	contract.RetireReason = ""
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func (s *runService) syncRuntimeStateDeferred(ctx context.Context, state RuntimeState, blockingRun, deferredRun Run, reason string) error {
	contract := runtimeContractFromStateAndRun(state, blockingRun)
	now := s.clock.Now().UTC()
	contract.Status = "active"
	contract.LastProgressAt = &now
	contract.LastProgressEvent = "wakeup_deferred"
	contract.PendingWakeReason = strings.TrimSpace(reason)
	contract.ResumeDisposition = "resumable"
	deferredRunID := deferredRun.ID
	contract.DeferredRunID = &deferredRunID
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func (s *runService) syncRuntimeStateHeldDeferred(ctx context.Context, state RuntimeState, deferredRun Run, reason string) error {
	contract := runtimeContractFromStateAndRun(state, deferredRun)
	now := s.clock.Now().UTC()
	contract.Status = "resumable"
	contract.LastProgressAt = &now
	contract.LastProgressEvent = "wakeup_held"
	contract.PendingWakeReason = strings.TrimSpace(reason)
	deferredRunID := deferredRun.ID
	contract.DeferredRunID = &deferredRunID
	contract.ResumeDisposition = "resumable"
	contract.FailureClass = ""
	contract.FailureReason = ""
	contract.RetiredAt = nil
	contract.RetireReason = ""
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func (s *runService) syncRuntimeStateStale(ctx context.Context, state RuntimeState, runRecord Run, reason string) error {
	contract := runtimeContractFromStateAndRun(state, runRecord)
	now := s.clock.Now().UTC()
	contract.Status = "stale"
	contract.LastProgressAt = &now
	contract.LastProgressEvent = "stale_owner"
	contract.PendingWakeReason = strings.TrimSpace(reason)
	contract.ResumeDisposition = "resumable"
	contract.FailureClass = "timeout"
	contract.FailureReason = "execution owner stale handoff"
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func (s *runService) syncRuntimeStateResumable(ctx context.Context, state RuntimeState, runRecord Run, event, reason, failureClass string) error {
	contract := runtimeContractFromStateAndRun(state, runRecord)
	now := s.clock.Now().UTC()
	contract.Status = "resumable"
	contract.LastProgressAt = &now
	contract.LastProgressEvent = strings.TrimSpace(event)
	contract.PendingWakeReason = strings.TrimSpace(reason)
	contract.DeferredRunID = nil
	contract.ResumeDisposition = "resumable"
	contract.FailureClass = normalizeFailureClass(failureClass)
	if contract.FailureClass == "" {
		contract.FailureReason = ""
	} else {
		contract.FailureReason = strings.TrimSpace(reason)
	}
	contract.RetiredAt = nil
	contract.RetireReason = ""
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func (s *runService) syncRuntimeStateTerminal(ctx context.Context, state RuntimeState, runRecord Run, event, reason, failureClass string) error {
	contract := runtimeContractFromStateAndRun(state, runRecord)
	now := s.clock.Now().UTC()
	contract.Status = "terminal"
	contract.LastProgressAt = &now
	contract.LastProgressEvent = strings.TrimSpace(event)
	contract.PendingWakeReason = strings.TrimSpace(reason)
	contract.DeferredRunID = nil
	contract.ResumeDisposition = "terminal"
	contract.FailureClass = normalizeFailureClass(failureClass)
	contract.FailureReason = strings.TrimSpace(reason)
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func (s *runService) syncRuntimeProgress(ctx context.Context, state RuntimeState, runRecord Run, event string) error {
	contract := runtimeContractFromStateAndRun(state, runRecord)
	now := s.clock.Now().UTC()
	contract.LastProgressAt = &now
	contract.LastProgressEvent = strings.TrimSpace(event)
	if contract.Status == "" {
		contract.Status = "active"
	}
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func (s *runService) retireRuntimeState(ctx context.Context, state RuntimeState, retiredAt time.Time, reason string) error {
	if state.ActiveRunID != nil && *state.ActiveRunID != uuid.Nil {
		if _, err := s.runtime.ClearActive(ctx, state.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	contract := state.Contract()
	contract.Status = "retired"
	contract.ResumeDisposition = "terminal"
	contract.PendingWakeReason = strings.TrimSpace(reason)
	contract.DeferredRunID = nil
	contract.RetireReason = strings.TrimSpace(reason)
	retiredAt = retiredAt.UTC()
	contract.RetiredAt = &retiredAt
	if _, err := s.runtime.UpdateMetadata(ctx, state.ID, contract.JSON()); err != nil {
		return err
	}
	return nil
}

func runtimeContractFromStateAndRun(state RuntimeState, runRecord Run) RuntimeStateContract {
	contract := state.Contract()
	if runRecord.TaskID != nil && *runRecord.TaskID != uuid.Nil {
		id := *runRecord.TaskID
		contract.TaskID = &id
	}
	if runRecord.SessionID != nil && *runRecord.SessionID != uuid.Nil {
		id := *runRecord.SessionID
		contract.SessionID = &id
	}
	if runRecord.FlowNodeID != nil && *runRecord.FlowNodeID != uuid.Nil {
		id := *runRecord.FlowNodeID
		contract.FlowNodeID = &id
	}
	if id, ok := runtimeMetadataUUIDValue(runRecord.Metadata, "flow_node_execution_id"); ok {
		contract.FlowNodeExecutionID = &id
	}
	if providerSessionID := runtimeMetadataStringValue(runRecord.Metadata, "provider_session_id"); providerSessionID != "" {
		contract.ProviderSessionID = providerSessionID
	}
	if wakeupSource := executionWakeupSource(runRecord.Metadata); wakeupSource != "" {
		contract.WakeupSource = wakeupSource
	}
	if wakeupKind := executionWakeupKind(runRecord.Metadata); wakeupKind != "" {
		contract.WakeupKind = wakeupKind
	}
	return contract.normalize()
}

func runtimeMetadataUUIDValue(metadata json.RawMessage, key string) (uuid.UUID, bool) {
	value := runtimeMetadataStringValue(metadata, key)
	if value == "" {
		return uuid.UUID{}, false
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, false
	}
	return parsed, true
}

func runtimeMetadataStringValue(metadata json.RawMessage, key string) string {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		return ""
	}
	return strings.TrimSpace(valueAsString(decoded[strings.TrimSpace(key)]))
}
