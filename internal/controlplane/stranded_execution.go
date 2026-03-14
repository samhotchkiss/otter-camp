package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
)

const (
	strandedExecutionPendingWakeReason = "no_live_task_turn"
	strandedExecutionRecoveryReason    = "active execution lost live task turn"
	strandedExecutionFailureReason     = "active execution lost live task turn and automatic recovery failed"
	errMissingTaskTransitionServiceForStrandedExecution = "supervisor requires task transition service to block stranded executions"
)

type strandedExecutionCandidate struct {
	ExecutionID       uuid.UUID
	TaskID            uuid.UUID
	ProjectID         uuid.UUID
	OrganizationID    uuid.UUID
	FlowNodeID        uuid.UUID
	SessionID         uuid.UUID
	SessionStatus     string
	CurrentTurnID     uuid.UUID
	CurrentTurnStatus string
	ActiveRunID       uuid.UUID
	ActiveRunStatus   string
	RecoveryAgentID   uuid.UUID
	AssignedAgentID   uuid.UUID
	LastActivityAt    time.Time
}

type errStrandedExecutionUnrecoverable struct {
	reason string
}

func (e errStrandedExecutionUnrecoverable) Error() string {
	if trimmed := strings.TrimSpace(e.reason); trimmed != "" {
		return trimmed
	}
	return strandedExecutionFailureReason
}

func (s *Supervisor) detectStrandedActiveExecutions(ctx context.Context) error {
	if s.pool == nil || s.chatService == nil {
		return nil
	}

	cutoff := s.clock.Now().UTC().Add(-strandedExecutionGraceLimit)
	candidates, err := s.listStrandedActiveExecutions(ctx, cutoff, nil)
	if err != nil {
		return err
	}

	for _, candidate := range candidates {
		refreshed, found, refreshErr := s.loadStrandedActiveExecution(ctx, candidate.ExecutionID, cutoff)
		if refreshErr != nil {
			return refreshErr
		}
		if !found {
			continue
		}

		if recoverErr := s.recoverStrandedActiveExecution(ctx, refreshed); recoverErr == nil {
			continue
		} else {
			var unrecoverable errStrandedExecutionUnrecoverable
			if !errors.As(recoverErr, &unrecoverable) {
				return recoverErr
			}
			if markErr := s.markExecutionStranded(ctx, refreshed, unrecoverable.Error()); markErr != nil {
				return markErr
			}
		}
	}
	return nil
}

func (s *Supervisor) loadStrandedActiveExecution(ctx context.Context, executionID uuid.UUID, cutoff time.Time) (strandedExecutionCandidate, bool, error) {
	if executionID == uuid.Nil {
		return strandedExecutionCandidate{}, false, nil
	}
	rows, err := s.listStrandedActiveExecutions(ctx, cutoff, &executionID)
	if err != nil {
		return strandedExecutionCandidate{}, false, err
	}
	if len(rows) == 0 {
		return strandedExecutionCandidate{}, false, nil
	}
	return rows[0], true, nil
}

func (s *Supervisor) listStrandedActiveExecutions(ctx context.Context, cutoff time.Time, executionID *uuid.UUID) ([]strandedExecutionCandidate, error) {
	if s.pool == nil {
		return nil, nil
	}

	query := `
		SELECT
			e.id,
			e.task_id,
			t.project_id,
			t.organization_id,
			e.flow_node_id,
			COALESCE(e.session_id, '00000000-0000-0000-0000-000000000000'::uuid) AS session_id,
			COALESCE(s.status, '') AS session_status,
			COALESCE(s.current_turn_id, '00000000-0000-0000-0000-000000000000'::uuid) AS current_turn_id,
			COALESCE(turn_row.status, '') AS current_turn_status,
			COALESCE(rs.active_run_id, '00000000-0000-0000-0000-000000000000'::uuid) AS active_run_id,
			COALESCE(r.status, '') AS active_run_status,
			COALESCE(recovery_agent.agent_id, '00000000-0000-0000-0000-000000000000'::uuid) AS recovery_agent_id,
			COALESCE(t.assigned_agent_id, '00000000-0000-0000-0000-000000000000'::uuid) AS assigned_agent_id,
			COALESCE(s.last_message_at, s.updated_at, t.updated_at, e.started_at) AS last_activity_at
		FROM flow_node_execution e
		JOIN project_task t ON t.id = e.task_id
		LEFT JOIN chat_session s ON s.id = e.session_id
		LEFT JOIN chat_turn turn_row ON turn_row.id = s.current_turn_id
		LEFT JOIN LATERAL (
			SELECT ct.id, ct.status
			FROM chat_turn ct
			WHERE ct.session_id = s.id
			ORDER BY ct.turn_number DESC, ct.created_at DESC, ct.id DESC
			LIMIT 1
		) latest_turn ON true
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		LEFT JOIN run r ON r.id = rs.active_run_id
		LEFT JOIN LATERAL (
			SELECT cp.participant_id AS agent_id
			FROM chat_participant cp
			WHERE cp.session_id = s.id
			  AND cp.participant_type = 'agent'
			  AND cp.removed_at IS NULL
			ORDER BY
				CASE WHEN cp.role = 'responder' THEN 0 ELSE 1 END,
				cp.joined_at ASC,
				cp.id ASC
			LIMIT 1
		) recovery_agent ON true
		WHERE e.status = 'active'
		  AND t.work_status = 'in_progress'
		  AND (
			s.id IS NULL
			OR s.status <> 'active'
			OR s.current_turn_id IS NULL
			OR COALESCE(turn_row.status, '') NOT IN ('pending', 'in_progress')
		  )
		  AND COALESCE(s.last_message_at, s.updated_at, t.updated_at, e.started_at) < $1
	`
	query += `
		  AND NOT (
			latest_turn.id IS NOT NULL
			AND COALESCE(latest_turn.status, '') = 'completed'
			AND COALESCE(t.metadata->'` + taskcheckpoint.RecoveryFileWriteMetadataKey + `'->>'halt_turn_id', '') = latest_turn.id::text
		  )
	`

	args := []any{cutoff.UTC()}
	if executionID != nil && *executionID != uuid.Nil {
		query += ` AND e.id = $2`
		args = append(args, *executionID)
	}
	query += `
		ORDER BY
			COALESCE(s.last_message_at, s.updated_at, t.updated_at, e.started_at) ASC,
			e.started_at ASC,
			e.id ASC
	`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]strandedExecutionCandidate, 0)
	for rows.Next() {
		var item strandedExecutionCandidate
		if scanErr := rows.Scan(
			&item.ExecutionID,
			&item.TaskID,
			&item.ProjectID,
			&item.OrganizationID,
			&item.FlowNodeID,
			&item.SessionID,
			&item.SessionStatus,
			&item.CurrentTurnID,
			&item.CurrentTurnStatus,
			&item.ActiveRunID,
			&item.ActiveRunStatus,
			&item.RecoveryAgentID,
			&item.AssignedAgentID,
			&item.LastActivityAt,
		); scanErr != nil {
			return nil, scanErr
		}
		item.SessionStatus = strings.TrimSpace(item.SessionStatus)
		item.CurrentTurnStatus = strings.TrimSpace(item.CurrentTurnStatus)
		item.ActiveRunStatus = strings.TrimSpace(item.ActiveRunStatus)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Supervisor) recoverStrandedActiveExecution(ctx context.Context, candidate strandedExecutionCandidate) error {
	if candidate.ExecutionID == uuid.Nil || candidate.TaskID == uuid.Nil || candidate.FlowNodeID == uuid.Nil {
		return errStrandedExecutionUnrecoverable{reason: "active execution record is incomplete"}
	}
	if candidate.SessionID == uuid.Nil {
		return errStrandedExecutionUnrecoverable{reason: "active execution has no execution session"}
	}
	if !strings.EqualFold(candidate.SessionStatus, "active") {
		return errStrandedExecutionUnrecoverable{reason: fmt.Sprintf("execution session is %q", candidate.SessionStatus)}
	}

	agentID, ok, err := s.resolveStrandedExecutionAgent(ctx, candidate)
	if err != nil {
		return err
	}
	if !ok || agentID == uuid.Nil {
		return errStrandedExecutionUnrecoverable{reason: "no recovery agent could be resolved for stranded execution"}
	}
	if err := s.releaseStrandedExecutionOwner(ctx, candidate); err != nil {
		return err
	}

	coordinator, ok := s.runService.(interface {
		CreateExecutionWakeup(context.Context, executionWakeupInput) (executionWakeupResult, error)
	})
	if !ok {
		return errStrandedExecutionUnrecoverable{reason: "run service does not support execution wakeup recovery"}
	}

	metadata, err := json.Marshal(map[string]any{
		"source":                     "supervisor",
		"flow_node_execution_id":     candidate.ExecutionID.String(),
		"run_mode":                   "async",
		"stranded_execution":         true,
		"supervisor_recovery_reason": strandedExecutionRecoveryReason,
	})
	if err != nil {
		return err
	}

	result, err := coordinator.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: candidate.OrganizationID,
			PrincipalType:  "agent",
			PrincipalID:    agentID,
			TriggerType:    "supervisor",
			ProjectID:      &candidate.ProjectID,
			TaskID:         &candidate.TaskID,
			FlowNodeID:     &candidate.FlowNodeID,
			SessionID:      uuidPtr(candidate.SessionID),
			Metadata:       metadata,
		},
		WakeupSource: "supervisor",
		WakeupKind:   "stranded_execution",
		WakeupPayload: map[string]any{
			"source":                 "supervisor",
			"flow_node_execution_id": candidate.ExecutionID.String(),
			"stranded_execution":     true,
			"run_mode":               "async",
		},
	})
	if err != nil {
		return err
	}
	if !result.shouldDispatch() {
		if session, getErr := s.chatService.GetSession(ctx, candidate.SessionID); getErr == nil && session != nil && session.CurrentTurnID != nil && *session.CurrentTurnID != uuid.Nil {
			return nil
		}
		return errStrandedExecutionUnrecoverable{reason: "supervisor wakeup could not claim execution ownership"}
	}

	if _, err := s.chatService.AddParticipant(ctx, candidate.SessionID, "agent", agentID, "responder"); err != nil && !errors.Is(err, chat.ErrAlreadyParticipant) {
		return err
	}

	messageMetadata, err := json.Marshal(map[string]any{
		"source":                 "supervisor",
		"run_id":                 result.Run.ID.String(),
		"reason":                 strandedExecutionRecoveryReason,
		"flow_node_execution_id": candidate.ExecutionID.String(),
		"stranded_execution":     true,
	})
	if err != nil {
		return err
	}
	message, err := s.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: candidate.SessionID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Metadata:  messageMetadata,
	})
	if err != nil {
		return err
	}

	turn, _, err := repo.NewChatTurnRepo(s.pool).CreateForMessageAttempt(ctx, candidate.SessionID, agentID, message.ID, 0)
	if err != nil {
		return err
	}
	if !isLiveTurnStatus(turn.Status) {
		return errStrandedExecutionUnrecoverable{reason: fmt.Sprintf("recovery turn entered non-live status %q", turn.Status)}
	}
	return nil
}

func (s *Supervisor) resolveStrandedExecutionAgent(ctx context.Context, candidate strandedExecutionCandidate) (uuid.UUID, bool, error) {
	if candidate.RecoveryAgentID != uuid.Nil {
		return candidate.RecoveryAgentID, true, nil
	}
	if s.pool == nil || candidate.FlowNodeID == uuid.Nil {
		if candidate.AssignedAgentID != uuid.Nil {
			return candidate.AssignedAgentID, true, nil
		}
		return uuid.Nil, false, nil
	}

	node, err := repo.NewFlowNodeRepo(s.pool).GetByID(ctx, candidate.FlowNodeID)
	if errors.Is(err, repo.ErrNotFound) {
		if candidate.AssignedAgentID != uuid.Nil {
			return candidate.AssignedAgentID, true, nil
		}
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}

	actorType := ""
	if node.ActorType != nil {
		actorType = strings.ToLower(strings.TrimSpace(*node.ActorType))
	}
	switch actorType {
	case "agent":
		if node.ActorID != nil && *node.ActorID != uuid.Nil {
			return *node.ActorID, true, nil
		}
		if candidate.AssignedAgentID != uuid.Nil {
			return candidate.AssignedAgentID, true, nil
		}
		return uuid.Nil, false, nil
	case "project_manager":
		pm, err := repo.NewAgentProjectAssignmentRepo(s.pool).GetPM(ctx, candidate.ProjectID)
		if errors.Is(err, repo.ErrNotFound) {
			return uuid.Nil, false, nil
		}
		if err != nil {
			return uuid.Nil, false, err
		}
		return pm.AgentID, true, nil
	case "role":
		assignments, err := repo.NewAgentProjectAssignmentRepo(s.pool).ListByProject(ctx, candidate.ProjectID)
		if err != nil {
			return uuid.Nil, false, err
		}
		role := resolveFlowNodeRole(node)
		for _, assignment := range assignments {
			if strings.EqualFold(strings.TrimSpace(assignment.Role), role) {
				return assignment.AgentID, true, nil
			}
		}
		return uuid.Nil, false, nil
	default:
		assignments, err := repo.NewAgentProjectAssignmentRepo(s.pool).ListByProject(ctx, candidate.ProjectID)
		if err != nil {
			return uuid.Nil, false, err
		}
		role := resolveFlowNodeRole(node)
		for _, assignment := range assignments {
			if strings.EqualFold(strings.TrimSpace(assignment.Role), role) {
				return assignment.AgentID, true, nil
			}
		}
		if candidate.AssignedAgentID != uuid.Nil {
			return candidate.AssignedAgentID, true, nil
		}
		return uuid.Nil, false, nil
	}
}

func (s *Supervisor) releaseStrandedExecutionOwner(ctx context.Context, candidate strandedExecutionCandidate) error {
	if candidate.ActiveRunID == uuid.Nil {
		return nil
	}

	runRecord, err := s.runService.GetRun(ctx, candidate.ActiveRunID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, repo.ErrNotFound) {
			return s.clearTaskRuntimeActiveRunIfMatches(ctx, candidate.TaskID, candidate.ActiveRunID)
		}
		return err
	}

	switch strings.ToLower(strings.TrimSpace(runRecord.Status)) {
	case "created":
		cancelErr := s.runService.RequestCancel(ctx, runRecord.ID, CancelRequestActor{Type: "supervisor"})
		if cancelErr != nil && !errors.Is(cancelErr, ErrInvalidTransition) && !errors.Is(cancelErr, ErrTerminalState) {
			return cancelErr
		}
	case "in_progress", "paused":
		failErr := s.runService.FailRun(ctx, runRecord.ID, strandedExecutionRecoveryReason, "transient")
		if failErr != nil && !errors.Is(failErr, ErrInvalidTransition) && !errors.Is(failErr, ErrTerminalState) {
			return failErr
		}
	case "cancelling":
		cancelErr := s.runService.ConfirmCancelled(ctx, runRecord.ID)
		if cancelErr != nil && !errors.Is(cancelErr, ErrInvalidTransition) && !errors.Is(cancelErr, ErrTerminalState) {
			return cancelErr
		}
	}

	return s.clearTaskRuntimeActiveRunIfMatches(ctx, candidate.TaskID, candidate.ActiveRunID)
}

func (s *Supervisor) clearTaskRuntimeActiveRunIfMatches(ctx context.Context, taskID, runID uuid.UUID) error {
	if s.pool == nil || taskID == uuid.Nil || runID == uuid.Nil {
		return nil
	}

	runtimeRepo := NewRuntimeStateRepository(s.pool)
	state, err := runtimeRepo.GetByScope(ctx, "task", taskID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.ActiveRunID == nil || *state.ActiveRunID != runID {
		return nil
	}
	if _, err := runtimeRepo.ClearActive(ctx, state.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func (s *Supervisor) markExecutionStranded(ctx context.Context, candidate strandedExecutionCandidate, reason string) error {
	if err := s.releaseStrandedExecutionOwner(ctx, candidate); err != nil {
		return err
	}
	if err := s.transitionTaskToBlocked(ctx, candidate.TaskID); err != nil {
		return err
	}
	if err := s.abandonActiveExecution(ctx, candidate.ExecutionID); err != nil {
		return err
	}
	return s.recordStrandedRuntimeState(ctx, candidate, reason)
}

func (s *Supervisor) transitionTaskToBlocked(ctx context.Context, taskID uuid.UUID) error {
	if taskID == uuid.Nil || s.pool == nil {
		return nil
	}

	taskRepo := repo.NewProjectTaskRepo(s.pool)
	taskRecord, err := taskRepo.GetByID(ctx, taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	status := strings.ToLower(strings.TrimSpace(taskRecord.WorkStatus))
	if status == "blocked" || status == "done" || status == "cancelled" {
		return nil
	}
	if status != "in_progress" {
		return nil
	}

	if s.taskService == nil {
		return fmt.Errorf(errMissingTaskTransitionServiceForStrandedExecution)
	}
	if _, err := s.taskService.TransitionStatus(ctx, taskID, "blocked", tasksvc.Actor{Type: "system"}); err != nil {
		var transitionErr tasksvc.ErrInvalidStatusTransition
		if !errors.As(err, &transitionErr) {
			return err
		}

		refreshed, refreshErr := taskRepo.GetByID(ctx, taskID)
		if refreshErr == nil {
			nextStatus := strings.ToLower(strings.TrimSpace(refreshed.WorkStatus))
			if nextStatus == "blocked" || nextStatus == "done" || nextStatus == "cancelled" {
				return nil
			}
		}
		return nil
	}
	return nil
}

func (s *Supervisor) abandonActiveExecution(ctx context.Context, executionID uuid.UUID) error {
	if executionID == uuid.Nil || s.pool == nil {
		return nil
	}

	executionRepo := repo.NewFlowNodeExecutionRepo(s.pool)
	execution, err := executionRepo.GetByID(ctx, executionID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(execution.Status), "active") {
		return nil
	}
	_, err = executionRepo.Abandon(ctx, executionID)
	return err
}

func (s *Supervisor) recordStrandedRuntimeState(ctx context.Context, candidate strandedExecutionCandidate, reason string) error {
	if s.pool == nil || candidate.TaskID == uuid.Nil || candidate.OrganizationID == uuid.Nil {
		return nil
	}

	runtimeRepo := NewRuntimeStateRepository(s.pool)
	state, err := runtimeRepo.Ensure(ctx, candidate.OrganizationID, "task", candidate.TaskID)
	if err != nil {
		return err
	}
	if state.ActiveRunID != nil && *state.ActiveRunID != uuid.Nil {
		if _, err := runtimeRepo.ClearActive(ctx, state.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}

	now := s.clock.Now().UTC()
	contract := state.Contract()
	contract.Status = "stranded"
	contract.TaskID = uuidPtr(candidate.TaskID)
	if candidate.SessionID != uuid.Nil {
		contract.SessionID = uuidPtr(candidate.SessionID)
	}
	if candidate.FlowNodeID != uuid.Nil {
		contract.FlowNodeID = uuidPtr(candidate.FlowNodeID)
	}
	if candidate.ExecutionID != uuid.Nil {
		contract.FlowNodeExecutionID = uuidPtr(candidate.ExecutionID)
	}
	contract.LastProgressAt = &now
	contract.LastProgressEvent = "execution_stranded"
	contract.PendingWakeReason = strandedExecutionPendingWakeReason
	contract.DeferredRunID = nil
	contract.ResumeDisposition = "terminal"
	contract.FailureClass = "permanent"
	contract.FailureReason = strings.TrimSpace(reason)
	contract.RetiredAt = nil
	contract.RetireReason = ""
	contract.WakeupSource = "supervisor"
	contract.WakeupKind = "stranded_execution"
	_, err = runtimeRepo.UpdateMetadata(ctx, state.ID, contract.JSON())
	return err
}

func isLiveTurnStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "in_progress":
		return true
	default:
		return false
	}
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	value := id
	return &value
}
