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
	strandedExecutionPendingWakeReason                  = "no_live_task_turn"
	strandedExecutionRecoveryReason                     = "active execution lost live task turn"
	strandedExecutionFailureReason                      = "active execution lost live task turn and automatic recovery failed"
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
		if markErr := s.markExecutionStalled(ctx, refreshed.ExecutionID); markErr != nil {
			return markErr
		}

		if recoverErr := s.recoverStrandedActiveExecution(ctx, refreshed); recoverErr == nil {
			continue
		} else {
			if errors.Is(recoverErr, chat.ErrProjectArchived) {
				if refreshed.ActiveRunID != uuid.Nil {
					_ = s.runService.FailRun(ctx, refreshed.ActiveRunID, "project archived", "permanent")
				}
				s.logger.Info("supervisor: skipping stranded execution recovery for archived project", "execution_id", refreshed.ExecutionID, "task_id", refreshed.TaskID, "project_id", refreshed.ProjectID)
				continue
			}
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
			COALESCE(
				CASE
					WHEN COALESCE(e.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
					THEN (e.metadata->>'live_turn_id')::uuid
					ELSE NULL
				END,
				s.current_turn_id,
				'00000000-0000-0000-0000-000000000000'::uuid
			) AS current_turn_id,
			COALESCE(live_turn_row.status, turn_row.status, '') AS current_turn_status,
			COALESCE(
				CASE
					WHEN COALESCE(e.metadata->>'live_run_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
					THEN (e.metadata->>'live_run_id')::uuid
					ELSE NULL
				END,
				rs.active_run_id,
				'00000000-0000-0000-0000-000000000000'::uuid
			) AS active_run_id,
			COALESCE(live_run.status, r.status, '') AS active_run_status,
			COALESCE(recovery_agent.agent_id, '00000000-0000-0000-0000-000000000000'::uuid) AS recovery_agent_id,
			COALESCE(t.assigned_agent_id, '00000000-0000-0000-0000-000000000000'::uuid) AS assigned_agent_id,
			COALESCE(s.last_message_at, s.updated_at, t.updated_at, e.started_at) AS last_activity_at
		FROM flow_node_execution e
		JOIN project_task t ON t.id = e.task_id
		JOIN project p ON p.id = t.project_id
		LEFT JOIN chat_session s ON s.id = e.session_id
		LEFT JOIN chat_turn turn_row ON turn_row.id = s.current_turn_id
		LEFT JOIN chat_turn live_turn_row ON live_turn_row.id = CASE
			WHEN COALESCE(e.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (e.metadata->>'live_turn_id')::uuid
			ELSE NULL
		END
		LEFT JOIN LATERAL (
			SELECT ct.id, ct.status, ct.stop_reason
			FROM chat_turn ct
			WHERE ct.session_id = s.id
			ORDER BY ct.turn_number DESC, ct.created_at DESC, ct.id DESC
			LIMIT 1
		) latest_turn ON true
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		LEFT JOIN run r ON r.id = rs.active_run_id
		LEFT JOIN run live_run ON live_run.id = CASE
			WHEN COALESCE(e.metadata->>'live_run_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (e.metadata->>'live_run_id')::uuid
			ELSE NULL
		END
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
		  AND p.status = 'active'
		  AND COALESCE((p.settings->'pause'->>'is_paused')::boolean, false) = false
		  AND t.work_status IN ('in_progress', 'review')
		  AND (
			s.id IS NULL
			OR s.status <> 'active'
			OR s.current_turn_id IS NULL
			OR COALESCE(turn_row.status, '') NOT IN ('pending', 'in_progress')
			OR (
				rs.active_run_id IS NULL
				AND s.current_turn_id IS NOT NULL
				AND COALESCE(turn_row.status, '') IN ('pending', 'in_progress')
				AND NOT EXISTS (
					SELECT 1
					FROM job_queue jq
					WHERE jq.job_type = 'agent_turn'
					  AND jq.status IN ('pending', 'claimed')
					  AND (jq.payload->>'session_id')::uuid = s.id
				)
			)
		  )
		  AND COALESCE(s.last_message_at, s.updated_at, t.updated_at, e.started_at) < $1
		`
	query += `
		  AND NOT (
			latest_turn.id IS NOT NULL
			AND COALESCE(latest_turn.status, '') = 'completed'
			AND (
				COALESCE(t.metadata->'` + taskcheckpoint.RecoveryFileWriteMetadataKey + `'->>'halt_turn_id', '') = latest_turn.id::text
				OR COALESCE(latest_turn.stop_reason, '') = 'recovery_content_required'
			)
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
	agentID, ok, err := s.resolveStrandedExecutionAgent(ctx, candidate)
	if err != nil {
		return err
	}
	if !ok || agentID == uuid.Nil {
		return errStrandedExecutionUnrecoverable{reason: "no recovery agent could be resolved for stranded execution"}
	}
	repairedCandidate, err := s.repairStrandedExecutionSession(ctx, candidate, agentID)
	if err != nil {
		return err
	}
	candidate = repairedCandidate
	if candidate.SessionID == uuid.Nil {
		return errStrandedExecutionUnrecoverable{reason: "active execution has no execution session"}
	}
	if !strings.EqualFold(candidate.SessionStatus, "active") {
		return errStrandedExecutionUnrecoverable{reason: fmt.Sprintf("execution session is %q", candidate.SessionStatus)}
	}
	if candidate.ActiveRunID == uuid.Nil && candidate.CurrentTurnID != uuid.Nil && isLiveTurnStatus(candidate.CurrentTurnStatus) {
		if err := s.clearStrandedExecutionLiveTurn(ctx, candidate); err != nil {
			return err
		}
		candidate.CurrentTurnID = uuid.Nil
		candidate.CurrentTurnStatus = ""
	}
	if live, err := s.hasLiveStrandedExecutionRecovery(ctx, candidate.SessionID, candidate.ExecutionID); err != nil {
		return err
	} else if live {
		return nil
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
	if err := s.markExecutionRecoveryPending(ctx, candidate.ExecutionID, result.Run.ID, turn.ID, candidate.CurrentTurnID, strandedExecutionRecoveryReason); err != nil {
		return err
	}
	return nil
}

func (s *Supervisor) clearStrandedExecutionLiveTurn(ctx context.Context, candidate strandedExecutionCandidate) error {
	if s == nil || s.pool == nil || candidate.SessionID == uuid.Nil || candidate.CurrentTurnID == uuid.Nil || !isLiveTurnStatus(candidate.CurrentTurnStatus) {
		return nil
	}
	const failureReason = "supervisor recovery cleared stale live task turn without active run ownership"
	if _, err := s.pool.Exec(ctx, `
		UPDATE model_invocation
		SET status = 'failed',
		    failure_class = 'product_runtime',
		    error_code = 'stale_turn_recovered',
		    error_message = $2,
		    completed_at = COALESCE(completed_at, now())
		WHERE turn_id = $1
		  AND status = 'in_flight'
	`, candidate.CurrentTurnID, failureReason); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE chat_message
		SET status = 'failed',
		    error_message = $2
		WHERE turn_id = $1
		  AND role = 'assistant'
		  AND status IN ('pending', 'streaming')
	`, candidate.CurrentTurnID, failureReason); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE chat_turn
		SET status = 'failed',
		    error_message = $2,
		    completed_at = COALESCE(completed_at, now())
		WHERE id = $1
		  AND status IN ('pending', 'in_progress')
	`, candidate.CurrentTurnID, failureReason); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE chat_session
		SET current_turn_id = NULL
		WHERE id = $1
		  AND current_turn_id = $2
	`, candidate.SessionID, candidate.CurrentTurnID); err != nil {
		return err
	}
	return nil
}

func (s *Supervisor) repairStrandedExecutionSession(ctx context.Context, candidate strandedExecutionCandidate, agentID uuid.UUID) (strandedExecutionCandidate, error) {
	if s.chatService == nil {
		return candidate, nil
	}
	if candidate.ExecutionID == uuid.Nil || agentID == uuid.Nil {
		return candidate, nil
	}
	if candidate.SessionID != uuid.Nil && strings.EqualFold(candidate.SessionStatus, "active") {
		return candidate, nil
	}

	session, err := s.chatService.GetOrCreateNodeSession(ctx, candidate.ExecutionID, agentID)
	if errors.Is(err, repo.ErrNotFound) {
		return candidate, errStrandedExecutionUnrecoverable{reason: "active execution has no repairable execution session"}
	}
	if err != nil {
		return candidate, err
	}
	if session == nil || session.ID == uuid.Nil {
		return candidate, errStrandedExecutionUnrecoverable{reason: "active execution session repair returned empty session"}
	}

	candidate.SessionID = session.ID
	candidate.SessionStatus = strings.TrimSpace(session.Status)
	if session.CurrentTurnID != nil {
		candidate.CurrentTurnID = *session.CurrentTurnID
	} else {
		candidate.CurrentTurnID = uuid.Nil
	}
	return candidate, nil
}

func (s *Supervisor) hasLiveStrandedExecutionRecovery(ctx context.Context, sessionID, executionID uuid.UUID) (bool, error) {
	if s.pool == nil || sessionID == uuid.Nil || executionID == uuid.Nil {
		return false, nil
	}

	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat_message cm
			WHERE cm.session_id = $1
			  AND cm.role = 'user'
			  AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = $2
			  AND (
				(
					cm.content = 'supervisor recovery: resume task'
					AND COALESCE(cm.metadata->>'source', '') = 'supervisor'
				)
				OR COALESCE(cm.metadata->>'source', '') IN ('task_review_action', 'task_recovery_resume')
			  )
			  AND (
				EXISTS (
					SELECT 1
					FROM chat_turn ct
					WHERE ct.trigger_message_id = cm.id
					  AND ct.status IN ('pending', 'in_progress')
				)
				OR EXISTS (
					SELECT 1
					FROM job_queue jq
					WHERE jq.job_type = 'agent_turn'
					  AND jq.status IN ('pending', 'claimed')
					  AND (jq.payload->>'session_id')::uuid = cm.session_id
					  AND (jq.payload->>'message_id')::uuid = cm.id
				)
			  )
		)
	`, sessionID, executionID.String()).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
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
	if err := s.recordExecutionRecoveryCheckpoint(ctx, candidate.ExecutionID, candidate.CurrentTurnID, strings.TrimSpace(reason), "await_pm_decision"); err != nil {
		return err
	}
	if err := s.notifyProjectSessionForStrandedExecution(ctx, candidate, strings.TrimSpace(reason)); err != nil {
		return err
	}
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

func (s *Supervisor) notifyProjectSessionForStrandedExecution(ctx context.Context, candidate strandedExecutionCandidate, reason string) error {
	if s == nil || s.pool == nil || s.chatService == nil || s.runService == nil || candidate.ProjectID == uuid.Nil || candidate.OrganizationID == uuid.Nil {
		return nil
	}
	projectSession, err := repo.NewChatSessionRepo(s.pool).GetByScopeAndMode(ctx, "project", candidate.ProjectID, "async")
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if projectSession == nil || projectSession.ID == uuid.Nil || !strings.EqualFold(strings.TrimSpace(projectSession.Status), "active") {
		return nil
	}
	if hasKickoff, err := s.sessionHasSupervisorPMRecoveryKickoff(ctx, projectSession.ID, candidate.ExecutionID); err != nil {
		return err
	} else if hasKickoff {
		return nil
	}

	metadata, err := json.Marshal(map[string]any{
		"source":                     "supervisor",
		"supervisor_pm_recovery":     true,
		"supervisor_recovery_from":   candidate.ExecutionID.String(),
		"supervisor_recovery_reason": strings.TrimSpace(reason),
		"task_id":                    candidate.TaskID.String(),
		"flow_node_execution_id":     candidate.ExecutionID.String(),
		"recovery_disposition":       "await_pm_decision",
	})
	if err != nil {
		return err
	}
	runRecord, err := s.runService.CreateRun(ctx, CreateRunInput{
		OrganizationID: candidate.OrganizationID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "supervisor",
		ProjectID:      &candidate.ProjectID,
		SessionID:      &projectSession.ID,
		Metadata:       metadata,
	})
	if err != nil {
		return err
	}
	if err := s.runService.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		return err
	}
	messageMetadata, err := json.Marshal(map[string]any{
		"source":                 "supervisor",
		"run_id":                 runRecord.ID.String(),
		"reason":                 strings.TrimSpace(reason),
		"task_id":                candidate.TaskID.String(),
		"flow_node_execution_id": candidate.ExecutionID.String(),
		"supervisor_pm_recovery": true,
		"recovery_disposition":   "await_pm_decision",
	})
	if err != nil {
		return err
	}
	_, err = s.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: projectSession.ID,
		Role:      "user",
		Content:   "supervisor recovery: inspect stranded execution and use flow.recovery_decision",
		Metadata:  messageMetadata,
	})
	return err
}

func (s *Supervisor) sessionHasSupervisorPMRecoveryKickoff(ctx context.Context, sessionID, executionID uuid.UUID) (bool, error) {
	if s == nil || s.pool == nil || sessionID == uuid.Nil || executionID == uuid.Nil {
		return false, nil
	}
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat_message
			WHERE session_id = $1
			  AND role = 'user'
			  AND content = 'supervisor recovery: inspect stranded execution and use flow.recovery_decision'
			  AND metadata->>'source' = 'supervisor'
			  AND metadata->>'supervisor_pm_recovery' = 'true'
			  AND metadata->>'flow_node_execution_id' = $2
		)
	`, sessionID, executionID.String()).Scan(&exists)
	return exists, err
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

func (s *Supervisor) markExecutionRecoveryPending(ctx context.Context, executionID, runID, turnID, failedTurnID uuid.UUID, reason string) error {
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
	commitSHA := ""
	if execution.CommitSHA != nil {
		commitSHA = strings.TrimSpace(*execution.CommitSHA)
	}
	liveOwner := repo.FlowExecutionLiveOwnerFromMetadata(execution.Metadata)
	if failedTurnID == uuid.Nil && liveOwner.TurnID != nil {
		failedTurnID = *liveOwner.TurnID
	}
	now := s.clock.Now().UTC()
	checkpoint := &repo.FlowExecutionRecoveryCheckpoint{
		CheckpointType: "stranded_execution",
		LastCommitSHA:  commitSHA,
		ResumeAction:   "start_new_turn",
		FailureClass:   "product_runtime",
		FailureSummary: strings.TrimSpace(reason),
		UpdatedAt:      &now,
	}
	if failedTurnID != uuid.Nil {
		checkpoint.FailedTurnID = &failedTurnID
	}
	updatedMetadata := repo.FlowExecutionMetadataWithRecoveryCheckpoint(execution.Metadata, checkpoint)
	updatedMetadata = repo.FlowExecutionMetadataWithLiveOwner(updatedMetadata, repo.FlowExecutionLiveOwner{
		RunID:  uuidPtr(runID),
		TurnID: uuidPtr(turnID),
	})
	updatedExecution, err := executionRepo.UpdateMetadata(ctx, executionID, updatedMetadata)
	if err != nil {
		return err
	}
	recoveryPending := "recovery_pending"
	_, err = executionRepo.UpdateRuntimeSubstate(ctx, updatedExecution.ID, &recoveryPending)
	return err
}

func (s *Supervisor) markExecutionStalled(ctx context.Context, executionID uuid.UUID) error {
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
	stalled := "stalled"
	_, err = executionRepo.UpdateRuntimeSubstate(ctx, executionID, &stalled)
	return err
}

func (s *Supervisor) recordExecutionRecoveryCheckpoint(ctx context.Context, executionID, failedTurnID uuid.UUID, reason, resumeAction string) error {
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
	commitSHA := ""
	if execution.CommitSHA != nil {
		commitSHA = strings.TrimSpace(*execution.CommitSHA)
	}
	liveOwner := repo.FlowExecutionLiveOwnerFromMetadata(execution.Metadata)
	if failedTurnID == uuid.Nil && liveOwner.TurnID != nil {
		failedTurnID = *liveOwner.TurnID
	}
	now := s.clock.Now().UTC()
	checkpoint := &repo.FlowExecutionRecoveryCheckpoint{
		CheckpointType: "stranded_execution",
		LastCommitSHA:  commitSHA,
		ResumeAction:   strings.TrimSpace(resumeAction),
		FailureClass:   "product_runtime",
		FailureSummary: strings.TrimSpace(reason),
		UpdatedAt:      &now,
	}
	if failedTurnID != uuid.Nil {
		checkpoint.FailedTurnID = &failedTurnID
	}
	_, err = executionRepo.UpdateMetadata(ctx, executionID, repo.FlowExecutionMetadataWithRecoveryCheckpoint(execution.Metadata, checkpoint))
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
