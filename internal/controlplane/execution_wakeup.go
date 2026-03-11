package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type executionWakeupDecision string

const (
	executionWakeupStarted   executionWakeupDecision = "started"
	executionWakeupCoalesced executionWakeupDecision = "coalesced"
	executionWakeupDeferred  executionWakeupDecision = "deferred"
	executionWakeupPromoted  executionWakeupDecision = "promoted"
)

type executionWakeupInput struct {
	CreateRunInput
	WakeupSource  string
	WakeupKind    string
	WakeupPayload map[string]any
}

type executionWakeupResult struct {
	Run         Run
	Decision    executionWakeupDecision
	BlockingRun *Run
}

type ExecutionWakeupInput = executionWakeupInput
type ExecutionWakeupResult = executionWakeupResult

type executionScope struct {
	Type string
	ID   uuid.UUID
}

func (r executionWakeupResult) shouldDispatch() bool {
	return r.Decision == executionWakeupStarted || r.Decision == executionWakeupPromoted
}

func (s *runService) CreateExecutionWakeup(ctx context.Context, input executionWakeupInput) (executionWakeupResult, error) {
	if s.runtime == nil {
		runRecord, err := s.CreateRun(ctx, input.CreateRunInput)
		if err != nil {
			return executionWakeupResult{}, err
		}
		if err := s.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
			return executionWakeupResult{}, err
		}
		updated, _ := s.GetRun(ctx, runRecord.ID)
		if updated.ID != uuid.Nil {
			runRecord = updated
		}
		return executionWakeupResult{Run: runRecord, Decision: executionWakeupStarted}, nil
	}

	scope, ok := executionScopeFromInput(input.CreateRunInput)
	if !ok {
		runRecord, err := s.CreateRun(ctx, input.CreateRunInput)
		if err != nil {
			return executionWakeupResult{}, err
		}
		if err := s.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
			return executionWakeupResult{}, err
		}
		updated, _ := s.GetRun(ctx, runRecord.ID)
		if updated.ID != uuid.Nil {
			runRecord = updated
		}
		return executionWakeupResult{Run: runRecord, Decision: executionWakeupStarted}, nil
	}

	state, err := s.runtime.Ensure(ctx, input.OrganizationID, scope.Type, scope.ID)
	if err != nil {
		return executionWakeupResult{}, err
	}

	activeRun, stale, err := s.resolveActiveWakeupRun(ctx, state)
	if err != nil {
		return executionWakeupResult{}, err
	}
	if stale {
		if activeRun != nil && activeRun.ID != uuid.Nil {
			if err := s.syncRuntimeStateStale(ctx, state, *activeRun, "stale_owner"); err != nil {
				return executionWakeupResult{}, err
			}
		}
		if err := s.clearActiveWakeupState(ctx, state, activeRun); err != nil {
			return executionWakeupResult{}, err
		}
		activeRun = nil
	}

	if activeRun != nil {
		if sameExecutionOwner(activeRun, input.PrincipalType, input.PrincipalID) {
			now := s.clock.Now().UTC()
			if _, err := s.runtime.SetActive(ctx, state.ID, activeRun.ID, activeRun.PrincipalType, principalIDPointer(activeRun.PrincipalType, activeRun.PrincipalID), now, now); err != nil {
				return executionWakeupResult{}, err
			}
			if err := s.syncRuntimeProgress(ctx, state, *activeRun, "wakeup_coalesced"); err != nil {
				return executionWakeupResult{}, err
			}
			if err := s.recordWakeupEvent(ctx, *activeRun, "wakeup_coalesced", map[string]any{
				"scope_type":  scope.Type,
				"scope_id":    scope.ID.String(),
				"wake_source": strings.TrimSpace(input.WakeupSource),
				"wake_kind":   strings.TrimSpace(input.WakeupKind),
			}); err != nil {
				return executionWakeupResult{}, err
			}
			return executionWakeupResult{Run: *activeRun, Decision: executionWakeupCoalesced}, nil
		}

		deferred, err := s.ensureDeferredWakeup(ctx, input, scope, activeRun.ID)
		if err != nil {
			return executionWakeupResult{}, err
		}
		if err := s.syncRuntimeStateDeferred(ctx, state, *activeRun, deferred, strings.TrimSpace(input.WakeupSource)+":"+strings.TrimSpace(input.WakeupKind)); err != nil {
			return executionWakeupResult{}, err
		}
		if err := s.recordWakeupEvent(ctx, deferred, "wakeup_deferred", map[string]any{
			"scope_type":      scope.Type,
			"scope_id":        scope.ID.String(),
			"wake_source":     strings.TrimSpace(input.WakeupSource),
			"wake_kind":       strings.TrimSpace(input.WakeupKind),
			"blocking_run_id": activeRun.ID.String(),
		}); err != nil {
			return executionWakeupResult{}, err
		}
		return executionWakeupResult{Run: deferred, Decision: executionWakeupDeferred, BlockingRun: activeRun}, nil
	}

	if existing, found, err := s.findDeferredWakeup(ctx, input.OrganizationID, scope, normalizePrincipalType(input.PrincipalType), principalIDPointer(input.PrincipalType, input.PrincipalID)); err != nil {
		return executionWakeupResult{}, err
	} else if found {
		promoted, promoteErr := s.promoteDeferredWakeup(ctx, state, existing, "stale_or_released_owner")
		if promoteErr != nil {
			return executionWakeupResult{}, promoteErr
		}
		return executionWakeupResult{Run: promoted, Decision: executionWakeupPromoted}, nil
	}

	started, err := s.startFreshWakeup(ctx, state, input, scope)
	if err != nil {
		return executionWakeupResult{}, err
	}
	return executionWakeupResult{Run: started, Decision: executionWakeupStarted}, nil
}

func (s *runService) ReleaseExecutionOwner(ctx context.Context, taskID, sessionID uuid.UUID, reason string) (executionWakeupResult, error) {
	return s.releaseExecutionOwner(ctx, taskID, sessionID, nil, reason, true)
}

func (s *runService) ReleaseExecutionOwnerForRun(ctx context.Context, taskID, sessionID, runID uuid.UUID, reason string) (executionWakeupResult, error) {
	return s.releaseExecutionOwner(ctx, taskID, sessionID, &runID, reason, true)
}

func (s *runService) ReleaseExecutionOwnerHoldingDeferred(ctx context.Context, taskID, sessionID uuid.UUID, reason string) (executionWakeupResult, error) {
	return s.releaseExecutionOwner(ctx, taskID, sessionID, nil, reason, false)
}

func (s *runService) ReleaseExecutionOwnerForRunHoldingDeferred(ctx context.Context, taskID, sessionID, runID uuid.UUID, reason string) (executionWakeupResult, error) {
	return s.releaseExecutionOwner(ctx, taskID, sessionID, &runID, reason, false)
}

func (s *runService) releaseExecutionOwner(ctx context.Context, taskID, sessionID uuid.UUID, expectedRunID *uuid.UUID, reason string, promoteDeferred bool) (executionWakeupResult, error) {
	if s.runtime == nil {
		return executionWakeupResult{}, nil
	}

	scope, ok := executionScopeFromIDs(taskID, sessionID)
	if !ok {
		return executionWakeupResult{}, nil
	}

	state, err := s.runtime.GetByScope(ctx, scope.Type, scope.ID)
	if errors.Is(err, ErrNotFound) {
		return executionWakeupResult{}, nil
	}
	if err != nil {
		return executionWakeupResult{}, err
	}
	if state.ActiveRunID == nil || *state.ActiveRunID == uuid.Nil {
		deferred, found, err := s.findDeferredWakeup(ctx, state.OrganizationID, scope, "", nil)
		if err != nil || !found {
			return executionWakeupResult{}, err
		}
		if !promoteDeferred {
			if syncErr := s.syncRuntimeStateHeldDeferred(ctx, state, deferred, strings.TrimSpace(reason)); syncErr != nil {
				return executionWakeupResult{}, syncErr
			}
			return executionWakeupResult{Run: deferred, Decision: executionWakeupDeferred}, nil
		}
		promoted, err := s.promoteDeferredWakeup(ctx, state, deferred, strings.TrimSpace(reason))
		if err != nil {
			return executionWakeupResult{}, err
		}
		return executionWakeupResult{Run: promoted, Decision: executionWakeupPromoted}, nil
	}
	var releasedRun Run
	if state.ActiveRunID != nil && *state.ActiveRunID != uuid.Nil {
		if runRecord, getErr := s.runs.Get(ctx, *state.ActiveRunID); getErr == nil {
			releasedRun = runRecord
		}
	}
	if releasedRun.ID == uuid.Nil {
		return executionWakeupResult{}, nil
	}
	if expectedRunID != nil && *expectedRunID != uuid.Nil && releasedRun.ID != *expectedRunID {
		return executionWakeupResult{}, nil
	}
	if releasedRun.SessionID == nil || *releasedRun.SessionID == uuid.Nil || *releasedRun.SessionID != sessionID {
		return executionWakeupResult{}, nil
	}
	updatedState, err := s.runtime.ClearActive(ctx, state.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return executionWakeupResult{}, err
	}
	if updatedState.ID != uuid.Nil {
		state = updatedState
	}

	deferred, found, err := s.findDeferredWakeup(ctx, state.OrganizationID, scope, "", nil)
	if err != nil || !found {
		if releasedRun.ID != uuid.Nil {
			if syncErr := s.syncRuntimeStateResumable(ctx, state, releasedRun, strings.TrimSpace(reason), strings.TrimSpace(reason), ""); syncErr != nil {
				return executionWakeupResult{}, syncErr
			}
		}
		return executionWakeupResult{}, err
	}
	if !promoteDeferred {
		if syncErr := s.syncRuntimeStateHeldDeferred(ctx, state, deferred, strings.TrimSpace(reason)); syncErr != nil {
			return executionWakeupResult{}, syncErr
		}
		return executionWakeupResult{Run: deferred, Decision: executionWakeupDeferred}, nil
	}
	promoted, err := s.promoteDeferredWakeup(ctx, state, deferred, strings.TrimSpace(reason))
	if err != nil {
		return executionWakeupResult{}, err
	}
	return executionWakeupResult{Run: promoted, Decision: executionWakeupPromoted}, nil
}

func (s *runService) PromoteDeferredWakeupsForProject(ctx context.Context, projectID uuid.UUID) ([]Run, error) {
	if s.runtime == nil || projectID == uuid.Nil {
		return nil, nil
	}
	states, err := s.runtime.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	promoted := make([]Run, 0, len(states))
	for _, state := range states {
		if state.ActiveRunID != nil && *state.ActiveRunID != uuid.Nil {
			continue
		}
		contract := state.Contract()
		if contract.DeferredRunID == nil || *contract.DeferredRunID == uuid.Nil {
			continue
		}
		deferred, err := s.runs.Get(ctx, *contract.DeferredRunID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scope := executionScope{Type: state.ScopeType, ID: state.ScopeID}
		if !isDeferredWakeupRun(deferred, scope) || !strings.EqualFold(strings.TrimSpace(deferred.Status), "created") {
			continue
		}
		runRecord, err := s.promoteDeferredWakeup(ctx, state, deferred, "project_resumed")
		if err != nil {
			return nil, err
		}
		promoted = append(promoted, runRecord)
	}
	sort.Slice(promoted, func(i, j int) bool {
		if promoted[i].CreatedAt.Equal(promoted[j].CreatedAt) {
			return promoted[i].ID.String() < promoted[j].ID.String()
		}
		return promoted[i].CreatedAt.Before(promoted[j].CreatedAt)
	})
	return promoted, nil
}

func (s *runService) startFreshWakeup(ctx context.Context, state RuntimeState, input executionWakeupInput, scope executionScope) (Run, error) {
	now := s.clock.Now().UTC()
	runRecord, err := s.CreateRun(ctx, CreateRunInput{
		OrganizationID: input.OrganizationID,
		PrincipalType:  input.PrincipalType,
		PrincipalID:    input.PrincipalID,
		TriggerType:    input.TriggerType,
		ProjectID:      input.ProjectID,
		TaskID:         input.TaskID,
		FlowNodeID:     input.FlowNodeID,
		SessionID:      input.SessionID,
		TurnID:         input.TurnID,
		IdempotencyKey: input.IdempotencyKey,
		Metadata:       buildExecutionWakeupMetadata(input.Metadata, scope, input.WakeupSource, input.WakeupKind, input.WakeupPayload, "started", nil),
	})
	if err != nil {
		return Run{}, err
	}
	if _, err := s.runtime.SetActive(ctx, state.ID, runRecord.ID, runRecord.PrincipalType, principalIDPointer(runRecord.PrincipalType, runRecord.PrincipalID), now, now); err != nil {
		return Run{}, err
	}
	if err := s.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		_, _ = s.runtime.ClearActive(ctx, state.ID)
		return Run{}, err
	}
	updated, err := s.GetRun(ctx, runRecord.ID)
	if err == nil {
		runRecord = updated
	}
	if err := s.syncRuntimeStateActive(ctx, state, runRecord, "wakeup_started"); err != nil {
		return Run{}, err
	}
	return runRecord, nil
}

func (s *runService) ensureDeferredWakeup(ctx context.Context, input executionWakeupInput, scope executionScope, activeRunID uuid.UUID) (Run, error) {
	metadata := buildExecutionWakeupMetadata(input.Metadata, scope, input.WakeupSource, input.WakeupKind, input.WakeupPayload, "deferred", &activeRunID)
	runRecord, err := s.CreateRun(ctx, CreateRunInput{
		OrganizationID: input.OrganizationID,
		PrincipalType:  input.PrincipalType,
		PrincipalID:    input.PrincipalID,
		TriggerType:    input.TriggerType,
		ProjectID:      input.ProjectID,
		TaskID:         input.TaskID,
		FlowNodeID:     input.FlowNodeID,
		SessionID:      input.SessionID,
		TurnID:         input.TurnID,
		IdempotencyKey: input.IdempotencyKey,
		Metadata:       metadata,
	})
	if err != nil {
		return Run{}, err
	}
	return runRecord, nil
}

func (s *runService) promoteDeferredWakeup(ctx context.Context, state RuntimeState, deferred Run, reason string) (Run, error) {
	now := s.clock.Now().UTC()
	if _, err := s.runtime.SetActive(ctx, state.ID, deferred.ID, deferred.PrincipalType, principalIDPointer(deferred.PrincipalType, deferred.PrincipalID), now, now); err != nil {
		return Run{}, err
	}
	if err := s.StartRun(ctx, deferred.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		_, _ = s.runtime.ClearActive(ctx, state.ID)
		return Run{}, err
	}
	updated, err := s.GetRun(ctx, deferred.ID)
	if err == nil {
		deferred = updated
	}
	if err := s.syncRuntimeStateActive(ctx, state, deferred, "wakeup_promoted"); err != nil {
		return Run{}, err
	}
	if err := s.recordWakeupEvent(ctx, deferred, "wakeup_promoted", map[string]any{
		"scope_type":  state.ScopeType,
		"scope_id":    state.ScopeID.String(),
		"reason":      strings.TrimSpace(reason),
		"wake_source": executionWakeupSource(deferred.Metadata),
		"wake_kind":   executionWakeupKind(deferred.Metadata),
	}); err != nil {
		return Run{}, err
	}
	return deferred, nil
}

func (s *runService) resolveActiveWakeupRun(ctx context.Context, state RuntimeState) (*Run, bool, error) {
	if state.ActiveRunID == nil || *state.ActiveRunID == uuid.Nil {
		return nil, false, nil
	}
	runRecord, err := s.runs.Get(ctx, *state.ActiveRunID)
	if errors.Is(err, ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, terminal := runTerminalStatuses[runRecord.Status]; terminal {
		return &runRecord, true, nil
	}
	stale, staleErr := s.activeWakeupRunStale(ctx, runRecord)
	if staleErr != nil {
		return nil, false, staleErr
	}
	return &runRecord, stale, nil
}

func (s *runService) activeWakeupRunStale(ctx context.Context, runRecord Run) (bool, error) {
	now := s.clock.Now().UTC()
	threshold := heartbeatSilenceThreshold(runRecord)
	if now.Sub(runRecord.UpdatedAt) <= threshold {
		return false, nil
	}
	latestHeartbeat, err := s.events.GetLatestHeartbeat(ctx, runRecord.ID)
	if err == nil {
		return now.Sub(latestHeartbeat.CreatedAt) > threshold, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return true, nil
}

func (s *runService) clearActiveWakeupState(ctx context.Context, state RuntimeState, activeRun *Run) error {
	if activeRun != nil && activeRun.ID != uuid.Nil {
		if _, terminal := runTerminalStatuses[activeRun.Status]; !terminal {
			if err := s.FailRun(ctx, activeRun.ID, "execution owner stale handoff", "transient"); err != nil && !errors.Is(err, ErrInvalidTransition) {
				return err
			}
		}
	}
	_, err := s.runtime.ClearActive(ctx, state.ID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (s *runService) findDeferredWakeup(
	ctx context.Context,
	organizationID uuid.UUID,
	scope executionScope,
	principalType string,
	principalID *uuid.UUID,
) (Run, bool, error) {
	type deferredWakeupFinder interface {
		FindOldestDeferredWakeup(context.Context, uuid.UUID, executionScope, string, *uuid.UUID) (Run, bool, error)
	}
	if finder, ok := s.runs.(deferredWakeupFinder); ok {
		runRecord, found, err := finder.FindOldestDeferredWakeup(ctx, organizationID, scope, principalType, principalID)
		if err != nil || !found {
			return runRecord, found, err
		}
		if isDeferredWakeupRun(runRecord, scope) && (principalType == "" || sameExecutionOwner(&runRecord, principalType, uuidPointerValue(principalID))) {
			return runRecord, true, nil
		}
	}

	filter := RunListFilter{
		OrganizationID: organizationID,
		Status:         "created",
		Limit:          maxListLimit,
	}
	if scope.Type == "task" {
		filter.TaskID = &scope.ID
	}
	if scope.Type == "session" {
		filter.SessionID = &scope.ID
	}
	runs, err := s.runs.List(ctx, filter)
	if err != nil {
		return Run{}, false, err
	}
	deferred := make([]Run, 0, len(runs))
	for _, candidate := range runs {
		if !isDeferredWakeupRun(candidate, scope) {
			continue
		}
		if principalType != "" && !sameExecutionOwner(&candidate, principalType, uuidPointerValue(principalID)) {
			continue
		}
		deferred = append(deferred, candidate)
	}
	if len(deferred) == 0 {
		return Run{}, false, nil
	}
	sort.Slice(deferred, func(i, j int) bool {
		if deferred[i].CreatedAt.Equal(deferred[j].CreatedAt) {
			return deferred[i].ID.String() < deferred[j].ID.String()
		}
		return deferred[i].CreatedAt.Before(deferred[j].CreatedAt)
	})
	return deferred[0], true, nil
}

func (s *runService) recordWakeupEvent(ctx context.Context, runRecord Run, eventType string, payload map[string]any) error {
	if _, err := s.appendRunEvent(ctx, runRecord.ID, nil, nil, eventType, "system", nil, payload); err != nil {
		return err
	}
	return s.publishDomainEvent(ctx, runRecord.OrganizationID, "run."+eventType, "system", nil, payload)
}

func executionScopeFromInput(input CreateRunInput) (executionScope, bool) {
	return executionScopeFromIDs(uuidPointerValue(input.TaskID), uuidPointerValue(input.SessionID))
}

func executionScopeFromIDs(taskID, sessionID uuid.UUID) (executionScope, bool) {
	if taskID != uuid.Nil {
		return executionScope{Type: "task", ID: taskID}, true
	}
	if sessionID != uuid.Nil {
		return executionScope{Type: "session", ID: sessionID}, true
	}
	return executionScope{}, false
}

func buildExecutionWakeupMetadata(
	base json.RawMessage,
	scope executionScope,
	source, kind string,
	payload map[string]any,
	state string,
	deferredByRunID *uuid.UUID,
) json.RawMessage {
	metadata := map[string]any{}
	if len(base) > 0 && json.Valid(base) {
		_ = json.Unmarshal(base, &metadata)
	}
	for key, value := range payload {
		metadata[strings.TrimSpace(key)] = value
	}
	wakeup := map[string]any{
		"scope_type": scope.Type,
		"scope_id":   scope.ID.String(),
		"source":     strings.TrimSpace(source),
		"kind":       strings.TrimSpace(kind),
		"state":      strings.TrimSpace(state),
	}
	if deferredByRunID != nil && *deferredByRunID != uuid.Nil {
		wakeup["deferred_by_run_id"] = deferredByRunID.String()
	}
	metadata["execution_wakeup"] = wakeup
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return normalizeJSON(base, json.RawMessage(`{}`))
	}
	return encoded
}

func parseExecutionWakeupMetadata(metadata json.RawMessage) map[string]any {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		return nil
	}
	wakeup, _ := decoded["execution_wakeup"].(map[string]any)
	return wakeup
}

func executionWakeupSource(metadata json.RawMessage) string {
	wakeup := parseExecutionWakeupMetadata(metadata)
	if wakeup == nil {
		return ""
	}
	return strings.TrimSpace(valueAsString(wakeup["source"]))
}

func executionWakeupKind(metadata json.RawMessage) string {
	wakeup := parseExecutionWakeupMetadata(metadata)
	if wakeup == nil {
		return ""
	}
	return strings.TrimSpace(valueAsString(wakeup["kind"]))
}

func isDeferredWakeupRun(runRecord Run, scope executionScope) bool {
	wakeup := parseExecutionWakeupMetadata(runRecord.Metadata)
	if wakeup == nil {
		return false
	}
	if strings.TrimSpace(valueAsString(wakeup["state"])) != "deferred" {
		return false
	}
	if strings.TrimSpace(valueAsString(wakeup["scope_type"])) != scope.Type {
		return false
	}
	return strings.TrimSpace(valueAsString(wakeup["scope_id"])) == scope.ID.String()
}

func sameExecutionOwner(runRecord *Run, principalType string, principalID uuid.UUID) bool {
	if runRecord == nil {
		return false
	}
	if normalizePrincipalType(runRecord.PrincipalType) != normalizePrincipalType(principalType) {
		return false
	}
	if normalizePrincipalType(principalType) == "system" {
		return true
	}
	return runRecord.PrincipalID == principalID
}

func principalIDPointer(principalType string, principalID uuid.UUID) *uuid.UUID {
	if normalizePrincipalType(principalType) == "system" || principalID == uuid.Nil {
		return nil
	}
	id := principalID
	return &id
}
