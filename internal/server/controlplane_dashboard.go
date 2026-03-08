package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	operatorDashboardDefaultLimit            = 6
	operatorDashboardMaxLimit                = 12
	operatorDashboardStaleExecutionThreshold = 5 * time.Minute
	operatorDashboardStaleTaskThreshold      = 15 * time.Minute
	operatorDashboardRecentWindow            = 24 * time.Hour
)

type operatorDashboardResponse struct {
	Summary        operatorDashboardSummaryResponse `json:"summary"`
	Active         operatorDashboardSectionResponse `json:"active"`
	Stale          operatorDashboardSectionResponse `json:"stale"`
	Blocked        operatorDashboardSectionResponse `json:"blocked"`
	RecentFailures operatorDashboardSectionResponse `json:"recent_failures"`
	RecentActivity operatorDashboardSectionResponse `json:"recent_activity"`
	Thresholds     operatorDashboardThresholds      `json:"thresholds"`
	ServerTime     time.Time                        `json:"server_time"`
}

type operatorDashboardSummaryResponse struct {
	Health          string `json:"health"`
	QuietHealthy    bool   `json:"quiet_healthy"`
	ActiveProjects  int    `json:"active_projects"`
	ActiveTasks     int    `json:"active_tasks"`
	ActiveRuns      int    `json:"active_runs"`
	StaleTasks      int    `json:"stale_tasks"`
	StaleExecutions int    `json:"stale_executions"`
	BlockedItems    int    `json:"blocked_items"`
	RecentFailures  int    `json:"recent_failures"`
}

type operatorDashboardThresholds struct {
	StaleExecutionSeconds int `json:"stale_execution_seconds"`
	StaleTaskSeconds      int `json:"stale_task_seconds"`
}

type operatorDashboardSectionResponse struct {
	Count      int                             `json:"count"`
	TotalCount int                             `json:"total_count"`
	Items      []operatorDashboardItemResponse `json:"items"`
}

type operatorDashboardItemResponse struct {
	Kind            string                              `json:"kind"`
	Title           string                              `json:"title"`
	Summary         string                              `json:"summary,omitempty"`
	Status          string                              `json:"status"`
	Project         *operatorDashboardRefResponse       `json:"project,omitempty"`
	Task            *operatorDashboardTaskRef           `json:"task,omitempty"`
	Run             *operatorDashboardRefResponse       `json:"run,omitempty"`
	BlockingTasks   []projectOperationalBlockerResponse `json:"blocking_tasks,omitempty"`
	UpdatedAt       time.Time                           `json:"updated_at"`
	AgeSeconds      int                                 `json:"age_seconds"`
	StaleForSeconds int                                 `json:"stale_for_seconds,omitempty"`
	Links           operatorDashboardLinks              `json:"links"`
}

type operatorDashboardRefResponse struct {
	ID    uuid.UUID `json:"id"`
	Label string    `json:"label"`
}

type operatorDashboardTaskRef struct {
	ID         uuid.UUID `json:"id"`
	TaskNumber int       `json:"task_number"`
	Label      string    `json:"label"`
}

type operatorDashboardLinks struct {
	Project string `json:"project,omitempty"`
	Task    string `json:"task,omitempty"`
	Run     string `json:"run,omitempty"`
}

type operatorDashboardLoader struct {
	pool *pgxpool.Pool
	now  time.Time
}

func (h controlPlaneHandlers) getOperatorDashboard(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "operator dashboard unavailable")
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"), operatorDashboardDefaultLimit, operatorDashboardMaxLimit)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid limit")
		return
	}

	now := time.Now().UTC()
	if h.clock != nil {
		now = h.clock.Now().UTC()
	}
	loader := operatorDashboardLoader{pool: h.pool, now: now}
	data, err := loader.Load(r.Context(), principal.OrganizationID, principal.UserID, limit)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load operator dashboard")
		return
	}

	responder.JSON(w, http.StatusOK, data)
}

func (l operatorDashboardLoader) Load(ctx context.Context, organizationID, userID uuid.UUID, limit int) (operatorDashboardResponse, error) {
	activeProjectCount, err := l.countActiveProjects(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	activeTaskCount, err := l.countActiveTasks(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	activeRunCount, err := l.countActiveRuns(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	staleTaskCount, err := l.countStaleTasks(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	staleExecutionCount, err := l.countStaleExecutions(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	blockedCount, err := l.countBlockedItems(ctx, organizationID, userID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	recentFailureCount, err := l.countRecentFailures(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	activeSectionCount, err := l.countActiveSectionItems(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	recentActivityCount, err := l.countRecentActivity(ctx, organizationID)
	if err != nil {
		return operatorDashboardResponse{}, err
	}

	activeItems, err := l.loadActiveItems(ctx, organizationID, limit)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	staleItems, err := l.loadStaleItems(ctx, organizationID, limit)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	blockedItems, err := l.loadBlockedItems(ctx, organizationID, userID, limit)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	recentFailureItems, err := l.loadRecentFailureItems(ctx, organizationID, limit)
	if err != nil {
		return operatorDashboardResponse{}, err
	}
	recentActivityItems, err := l.loadRecentActivityItems(ctx, organizationID, limit)
	if err != nil {
		return operatorDashboardResponse{}, err
	}

	quietHealthy := activeTaskCount == 0 &&
		activeRunCount == 0 &&
		staleTaskCount == 0 &&
		staleExecutionCount == 0 &&
		blockedCount == 0 &&
		recentFailureCount == 0

	health := "active_healthy"
	if quietHealthy {
		health = "quiet_healthy"
	} else if staleTaskCount > 0 || staleExecutionCount > 0 || blockedCount > 0 || recentFailureCount > 0 {
		health = "attention_required"
	}

	return operatorDashboardResponse{
		Summary: operatorDashboardSummaryResponse{
			Health:          health,
			QuietHealthy:    quietHealthy,
			ActiveProjects:  activeProjectCount,
			ActiveTasks:     activeTaskCount,
			ActiveRuns:      activeRunCount,
			StaleTasks:      staleTaskCount,
			StaleExecutions: staleExecutionCount,
			BlockedItems:    blockedCount,
			RecentFailures:  recentFailureCount,
		},
		Active: operatorDashboardSectionResponse{
			Count:      len(activeItems),
			TotalCount: activeSectionCount,
			Items:      activeItems,
		},
		Stale: operatorDashboardSectionResponse{
			Count:      len(staleItems),
			TotalCount: staleTaskCount + staleExecutionCount,
			Items:      staleItems,
		},
		Blocked: operatorDashboardSectionResponse{
			Count:      len(blockedItems),
			TotalCount: blockedCount,
			Items:      blockedItems,
		},
		RecentFailures: operatorDashboardSectionResponse{
			Count:      len(recentFailureItems),
			TotalCount: recentFailureCount,
			Items:      recentFailureItems,
		},
		RecentActivity: operatorDashboardSectionResponse{
			Count:      len(recentActivityItems),
			TotalCount: recentActivityCount,
			Items:      recentActivityItems,
		},
		Thresholds: operatorDashboardThresholds{
			StaleExecutionSeconds: int(operatorDashboardStaleExecutionThreshold / time.Second),
			StaleTaskSeconds:      int(operatorDashboardStaleTaskThreshold / time.Second),
		},
		ServerTime: l.now,
	}, nil
}

func (l operatorDashboardLoader) countActiveProjects(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT project_id)
		FROM (
			SELECT project_id
			FROM project_task
			WHERE organization_id = $1
			  AND work_status IN ('queued', 'in_progress', 'on_hold')
			UNION
			SELECT COALESCE(r.project_id, t.project_id) AS project_id
			FROM runtime_state rs
			JOIN run r ON r.id = rs.active_run_id
			LEFT JOIN project_task t ON t.id = r.task_id
			WHERE r.organization_id = $1
			  AND rs.active_run_id IS NOT NULL
			  AND COALESCE(r.project_id, t.project_id) IS NOT NULL
		) active_projects
	`, organizationID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countActiveTasks(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE organization_id = $1
		  AND work_status IN ('queued', 'in_progress', 'on_hold')
	`, organizationID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countActiveRuns(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM runtime_state rs
		JOIN run r ON r.id = rs.active_run_id
		WHERE rs.active_run_id IS NOT NULL
		  AND r.organization_id = $1
		  AND r.status IN ('created', 'in_progress', 'cancelling', 'paused')
	`, organizationID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countStaleTasks(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task t
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		 AND rs.active_run_id IS NOT NULL
		WHERE t.organization_id = $1
		  AND t.work_status IN ('queued', 'in_progress', 'on_hold')
		  AND rs.id IS NULL
		  AND t.updated_at < $2
	`, organizationID, l.now.Add(-operatorDashboardStaleTaskThreshold)).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countStaleExecutions(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM runtime_state rs
		JOIN run r ON r.id = rs.active_run_id
		LEFT JOIN LATERAL (
			SELECT created_at
			FROM run_event re
			WHERE re.run_id = r.id
			  AND re.event_type = 'heartbeat'
			ORDER BY re.sequence DESC
			LIMIT 1
		) hb ON true
		WHERE rs.active_run_id IS NOT NULL
		  AND r.organization_id = $1
		  AND r.status IN ('created', 'in_progress', 'cancelling', 'paused')
		  AND COALESCE(hb.created_at, rs.last_wakeup_at, r.updated_at, r.created_at) < $2
	`, organizationID, l.now.Add(-operatorDashboardStaleExecutionThreshold)).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countBlockedItems(ctx context.Context, organizationID, userID uuid.UUID) (int, error) {
	terminalStallCount, err := l.countTerminalProjectStalls(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	reviewCount, err := l.countReviewBlockedTasks(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	strandedCount, err := l.countStrandedExecutions(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	validationBlockedCount, err := l.countValidationBlockedTasks(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	pausedProjects, err := l.countPausedProjects(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	humanInputCount, err := l.countHumanInputItems(ctx, organizationID, userID)
	if err != nil {
		return 0, err
	}
	return terminalStallCount + reviewCount + strandedCount + validationBlockedCount + pausedProjects + humanInputCount, nil
}

func (l operatorDashboardLoader) countTerminalProjectStalls(ctx context.Context, organizationID uuid.UUID) (int, error) {
	return projectStallDetector{pool: l.pool}.CountTerminalProjectStalls(ctx, organizationID)
}

func (l operatorDashboardLoader) countReviewBlockedTasks(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE organization_id = $1
		  AND (requires_human_review = true OR work_status = 'review')
	`, organizationID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countStrandedExecutions(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM runtime_state rs
		JOIN project_task t
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		WHERE t.organization_id = $1
		  AND t.work_status = 'blocked'
		  AND rs.metadata->>'status' = 'stranded'
	`, organizationID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countValidationBlockedTasks(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task t
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		WHERE t.organization_id = $1
		  AND t.work_status = 'blocked'
		  AND COALESCE((t.metadata->'agent_turn_validation_guard'->>'blocked')::boolean, false)
		  AND COALESCE(NULLIF(rs.metadata->>'status', ''), '') <> 'stranded'
	`, organizationID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countPausedProjects(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project
		WHERE organization_id = $1
		  AND COALESCE((settings->'pause'->>'is_paused')::boolean, false)
	`, organizationID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countHumanInputItems(ctx context.Context, organizationID, userID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM inbox_item
		WHERE organization_id = $1
		  AND target_user_id = $2
		  AND is_acted = false
		  AND item_type IN ('human_approval_required', 'draft_action_review', 'browser_handoff', 'blocker_filed', 'system_alert')
		  AND NOT (
			item_type = 'blocker_filed'
			AND EXISTS (
				SELECT 1
				FROM project_task t
				WHERE t.id = inbox_item.source_task_id
				  AND COALESCE((t.metadata->'agent_turn_validation_guard'->>'blocked')::boolean, false)
			)
		  )
	`, organizationID, userID).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countRecentFailures(ctx context.Context, organizationID uuid.UUID) (int, error) {
	return l.countRecentEvents(ctx, organizationID, []string{
		"run_failed",
		"attempt_failed",
		"run_timed_out",
		"run_cancelled",
		"wakeup_deferred",
		"wakeup_promoted",
		"supervisor_recovery",
	})
}

func (l operatorDashboardLoader) countRecentActivity(ctx context.Context, organizationID uuid.UUID) (int, error) {
	return l.countRecentEvents(ctx, organizationID, []string{
		"run_started",
		"run_completed",
		"run_failed",
		"run_cancelled",
		"run_timed_out",
		"run_paused",
		"attempt_failed",
		"supervisor_recovery",
		"wakeup_coalesced",
		"wakeup_deferred",
		"wakeup_promoted",
	})
}

func (l operatorDashboardLoader) countRecentEvents(ctx context.Context, organizationID uuid.UUID, eventTypes []string) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run_event re
		JOIN run r ON r.id = re.run_id
		WHERE r.organization_id = $1
		  AND re.created_at >= $2
		  AND re.event_type = ANY($3::text[])
	`, organizationID, l.now.Add(-operatorDashboardRecentWindow), eventTypes).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countActiveSectionItems(ctx context.Context, organizationID uuid.UUID) (int, error) {
	runCount, err := l.countActiveSectionRuns(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	taskCount, err := l.countActiveSectionTasks(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	return runCount + taskCount, nil
}

func (l operatorDashboardLoader) countActiveSectionRuns(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM runtime_state rs
		JOIN run r ON r.id = rs.active_run_id
		LEFT JOIN LATERAL (
			SELECT created_at
			FROM run_event re
			WHERE re.run_id = r.id
			  AND re.event_type = 'heartbeat'
			ORDER BY re.sequence DESC
			LIMIT 1
		) hb ON true
		WHERE rs.active_run_id IS NOT NULL
		  AND r.organization_id = $1
		  AND r.status IN ('created', 'in_progress', 'cancelling', 'paused')
		  AND COALESCE(hb.created_at, rs.last_wakeup_at, r.updated_at, r.created_at) >= $2
	`, organizationID, l.now.Add(-operatorDashboardStaleExecutionThreshold)).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) countActiveSectionTasks(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	err := l.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task t
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		 AND rs.active_run_id IS NOT NULL
		WHERE t.organization_id = $1
		  AND t.work_status IN ('queued', 'in_progress', 'on_hold')
		  AND rs.id IS NULL
		  AND t.updated_at >= $2
	`, organizationID, l.now.Add(-operatorDashboardStaleTaskThreshold)).Scan(&count)
	return count, err
}

func (l operatorDashboardLoader) loadActiveItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	items, err := l.loadActiveRunItems(ctx, organizationID, limit)
	if err != nil {
		return nil, err
	}
	if len(items) >= limit {
		return items[:limit], nil
	}

	taskItems, err := l.loadActiveTaskItems(ctx, organizationID, limit-len(items))
	if err != nil {
		return nil, err
	}
	return append(items, taskItems...), nil
}

func (l operatorDashboardLoader) loadActiveRunItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			r.id,
			r.status,
			COALESCE(hb.created_at, rs.last_wakeup_at, r.updated_at, r.created_at) AS activity_at
		FROM runtime_state rs
		JOIN run r ON r.id = rs.active_run_id
		LEFT JOIN project_task t ON t.id = r.task_id
		LEFT JOIN project p ON p.id = COALESCE(r.project_id, t.project_id)
		LEFT JOIN LATERAL (
			SELECT created_at
			FROM run_event re
			WHERE re.run_id = r.id
			  AND re.event_type = 'heartbeat'
			ORDER BY re.sequence DESC
			LIMIT 1
		) hb ON true
		WHERE rs.active_run_id IS NOT NULL
		  AND r.organization_id = $1
		  AND r.status IN ('created', 'in_progress', 'cancelling', 'paused')
		  AND COALESCE(hb.created_at, rs.last_wakeup_at, r.updated_at, r.created_at) >= $2
		ORDER BY activity_at DESC, r.id DESC
		LIMIT $3
	`, organizationID, l.now.Add(-operatorDashboardStaleExecutionThreshold), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID    *uuid.UUID
			projectLabel *string
			taskID       *uuid.UUID
			taskNumber   *int
			taskTitle    *string
			runID        uuid.UUID
			status       string
			activityAt   time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &runID, &status, &activityAt); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, l.newDashboardItem(
			"active_execution",
			taskOrProjectTitle(taskID, taskNumber, taskTitle, projectLabel, runID),
			fmt.Sprintf("run %s", strings.ReplaceAll(status, "_", " ")),
			status,
			activityAt,
			0,
			projectID,
			projectLabel,
			taskID,
			taskNumber,
			taskTitle,
			&runID,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadActiveTaskItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			t.work_status,
			t.updated_at
		FROM project_task t
		JOIN project p ON p.id = t.project_id
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		 AND rs.active_run_id IS NOT NULL
		WHERE t.organization_id = $1
		  AND t.work_status IN ('queued', 'in_progress', 'on_hold')
		  AND rs.id IS NULL
		  AND t.updated_at >= $2
		ORDER BY t.updated_at DESC, t.id DESC
		LIMIT $3
	`, organizationID, l.now.Add(-operatorDashboardStaleTaskThreshold), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID    uuid.UUID
			projectLabel string
			taskID       uuid.UUID
			taskNumber   int
			taskTitle    string
			status       string
			updatedAt    time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &status, &updatedAt); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, l.newDashboardItem(
			"active_task",
			formatTaskLabel(taskNumber, taskTitle),
			fmt.Sprintf("task %s", strings.ReplaceAll(status, "_", " ")),
			status,
			updatedAt,
			0,
			&projectID,
			&projectLabel,
			&taskID,
			&taskNumber,
			&taskTitle,
			nil,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadStaleItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	items, err := l.loadStaleExecutionItems(ctx, organizationID, limit)
	if err != nil {
		return nil, err
	}
	if len(items) >= limit {
		return items[:limit], nil
	}
	taskItems, err := l.loadStaleTaskItems(ctx, organizationID, limit-len(items))
	if err != nil {
		return nil, err
	}
	return append(items, taskItems...), nil
}

func (l operatorDashboardLoader) loadStaleExecutionItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			r.id,
			r.status,
			COALESCE(hb.created_at, rs.last_wakeup_at, r.updated_at, r.created_at) AS activity_at
		FROM runtime_state rs
		JOIN run r ON r.id = rs.active_run_id
		LEFT JOIN project_task t ON t.id = r.task_id
		LEFT JOIN project p ON p.id = COALESCE(r.project_id, t.project_id)
		LEFT JOIN LATERAL (
			SELECT created_at
			FROM run_event re
			WHERE re.run_id = r.id
			  AND re.event_type = 'heartbeat'
			ORDER BY re.sequence DESC
			LIMIT 1
		) hb ON true
		WHERE rs.active_run_id IS NOT NULL
		  AND r.organization_id = $1
		  AND r.status IN ('created', 'in_progress', 'cancelling', 'paused')
		  AND COALESCE(hb.created_at, rs.last_wakeup_at, r.updated_at, r.created_at) < $2
		ORDER BY activity_at ASC, r.id ASC
		LIMIT $3
	`, organizationID, l.now.Add(-operatorDashboardStaleExecutionThreshold), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID    *uuid.UUID
			projectLabel *string
			taskID       *uuid.UUID
			taskNumber   *int
			taskTitle    *string
			runID        uuid.UUID
			status       string
			activityAt   time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &runID, &status, &activityAt); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, l.newDashboardItem(
			"stale_execution",
			taskOrProjectTitle(taskID, taskNumber, taskTitle, projectLabel, runID),
			"execution quiet past stale threshold",
			status,
			activityAt,
			int(operatorDashboardStaleExecutionThreshold/time.Second),
			projectID,
			projectLabel,
			taskID,
			taskNumber,
			taskTitle,
			&runID,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadStaleTaskItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			t.work_status,
			t.updated_at
		FROM project_task t
		JOIN project p ON p.id = t.project_id
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		 AND rs.active_run_id IS NOT NULL
		WHERE t.organization_id = $1
		  AND t.work_status IN ('queued', 'in_progress', 'on_hold')
		  AND rs.id IS NULL
		  AND t.updated_at < $2
		ORDER BY t.updated_at ASC, t.id ASC
		LIMIT $3
	`, organizationID, l.now.Add(-operatorDashboardStaleTaskThreshold), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID    uuid.UUID
			projectLabel string
			taskID       uuid.UUID
			taskNumber   int
			taskTitle    string
			status       string
			updatedAt    time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &status, &updatedAt); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, l.newDashboardItem(
			"stale_task",
			formatTaskLabel(taskNumber, taskTitle),
			"task idle past stale threshold",
			status,
			updatedAt,
			int(operatorDashboardStaleTaskThreshold/time.Second),
			&projectID,
			&projectLabel,
			&taskID,
			&taskNumber,
			&taskTitle,
			nil,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadBlockedItems(ctx context.Context, organizationID, userID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	items := make([]operatorDashboardItemResponse, 0, limit)

	appendItems := func(next []operatorDashboardItemResponse) {
		for _, item := range next {
			if len(items) >= limit {
				return
			}
			items = append(items, item)
		}
	}

	terminalStallItems, err := l.loadTerminalProjectStallItems(ctx, organizationID, limit)
	if err != nil {
		return nil, err
	}
	appendItems(terminalStallItems)
	if len(items) >= limit {
		return items, nil
	}

	strandedItems, err := l.loadStrandedExecutionItems(ctx, organizationID, limit-len(items))
	if err != nil {
		return nil, err
	}
	appendItems(strandedItems)
	if len(items) >= limit {
		return items, nil
	}

	validationItems, err := l.loadValidationBlockedItems(ctx, organizationID, limit-len(items))
	if err != nil {
		return nil, err
	}
	appendItems(validationItems)
	if len(items) >= limit {
		return items, nil
	}

	reviewItems, err := l.loadReviewBlockedItems(ctx, organizationID, limit)
	if err != nil {
		return nil, err
	}
	appendItems(reviewItems)
	if len(items) >= limit {
		return items, nil
	}

	pausedItems, err := l.loadPausedProjectItems(ctx, organizationID, limit-len(items))
	if err != nil {
		return nil, err
	}
	appendItems(pausedItems)
	if len(items) >= limit {
		return items, nil
	}

	humanInputItems, err := l.loadHumanInputItems(ctx, organizationID, userID, limit-len(items))
	if err != nil {
		return nil, err
	}
	appendItems(humanInputItems)
	return items, nil
}

func (l operatorDashboardLoader) loadTerminalProjectStallItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	records, err := projectStallDetector{pool: l.pool}.LoadTerminalProjectStalls(ctx, organizationID, nil, limit)
	if err != nil {
		return nil, err
	}
	items := make([]operatorDashboardItemResponse, 0, len(records))
	for _, record := range records {
		projectID := record.ProjectID
		projectLabel := record.ProjectLabel
		item := l.newDashboardItem(
			"terminal_project_stall",
			projectLabel,
			record.Summary,
			projectOperationalStateTerminalStalled,
			record.UpdatedAt,
			0,
			&projectID,
			&projectLabel,
			nil,
			nil,
			nil,
			nil,
		)
		item.BlockingTasks = record.BlockingTasks
		items = append(items, item)
	}
	return items, nil
}

func (l operatorDashboardLoader) loadStrandedExecutionItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			COALESCE(NULLIF(rs.metadata->>'failure_reason', ''), ''),
			COALESCE(NULLIF(rs.metadata->>'status', ''), 'stranded'),
			COALESCE(NULLIF(rs.metadata->>'last_progress_at', '')::timestamptz, rs.updated_at, t.updated_at)
		FROM runtime_state rs
		JOIN project_task t
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		JOIN project p ON p.id = t.project_id
		WHERE t.organization_id = $1
		  AND t.work_status = 'blocked'
		  AND rs.metadata->>'status' = 'stranded'
		ORDER BY
			COALESCE(NULLIF(rs.metadata->>'last_progress_at', '')::timestamptz, rs.updated_at, t.updated_at) DESC,
			t.id DESC
		LIMIT $2
	`, organizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID     uuid.UUID
			projectLabel  string
			taskID        uuid.UUID
			taskNumber    int
			taskTitle     string
			failureReason string
			status        string
			updatedAt     time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &failureReason, &status, &updatedAt); scanErr != nil {
			return nil, scanErr
		}

		summary := "execution stranded: automatic recovery failed"
		if trimmed := strings.TrimSpace(failureReason); trimmed != "" {
			summary = "execution stranded: " + trimmed
		}
		items = append(items, l.newDashboardItem(
			"stranded_execution",
			formatTaskLabel(taskNumber, taskTitle),
			summary,
			status,
			updatedAt,
			0,
			&projectID,
			&projectLabel,
			&taskID,
			&taskNumber,
			&taskTitle,
			nil,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadValidationBlockedItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			COALESCE(NULLIF(t.metadata->'agent_turn_validation_guard'->>'tool_name', ''), ''),
			COALESCE(NULLIF(t.metadata->'agent_turn_validation_guard'->>'failure_reason', ''), ''),
			COALESCE(NULLIF(t.metadata->'agent_turn_validation_guard'->>'failure_code', ''), ''),
			t.updated_at
		FROM project_task t
		JOIN project p ON p.id = t.project_id
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		WHERE t.organization_id = $1
		  AND t.work_status = 'blocked'
		  AND COALESCE((t.metadata->'agent_turn_validation_guard'->>'blocked')::boolean, false)
		  AND COALESCE(NULLIF(rs.metadata->>'status', ''), '') <> 'stranded'
		ORDER BY t.updated_at DESC, t.id DESC
		LIMIT $2
	`, organizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID     uuid.UUID
			projectLabel  string
			taskID        uuid.UUID
			taskNumber    int
			taskTitle     string
			toolName      string
			failureReason string
			failureCode   string
			updatedAt     time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &toolName, &failureReason, &failureCode, &updatedAt); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, l.newDashboardItem(
			"validation_blocked",
			formatTaskLabel(taskNumber, taskTitle),
			buildValidationBlockedSummary(toolName, failureReason, failureCode),
			"blocked",
			updatedAt,
			0,
			&projectID,
			&projectLabel,
			&taskID,
			&taskNumber,
			&taskTitle,
			nil,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadReviewBlockedItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			t.work_status,
			t.updated_at
		FROM project_task t
		JOIN project p ON p.id = t.project_id
		WHERE t.organization_id = $1
		  AND (t.requires_human_review = true OR t.work_status = 'review')
		ORDER BY t.updated_at DESC, t.id DESC
		LIMIT $2
	`, organizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID    uuid.UUID
			projectLabel string
			taskID       uuid.UUID
			taskNumber   int
			taskTitle    string
			status       string
			updatedAt    time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &status, &updatedAt); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, l.newDashboardItem(
			"review_blocked",
			formatTaskLabel(taskNumber, taskTitle),
			"waiting for review",
			status,
			updatedAt,
			0,
			&projectID,
			&projectLabel,
			&taskID,
			&taskNumber,
			&taskTitle,
			nil,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadPausedProjectItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			id,
			display_name,
			COALESCE(settings->'pause'->>'reason', ''),
			updated_at
		FROM project
		WHERE organization_id = $1
		  AND COALESCE((settings->'pause'->>'is_paused')::boolean, false)
		ORDER BY updated_at DESC, id DESC
		LIMIT $2
	`, organizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			projectID    uuid.UUID
			projectLabel string
			reason       string
			updatedAt    time.Time
		)
		if scanErr := rows.Scan(&projectID, &projectLabel, &reason, &updatedAt); scanErr != nil {
			return nil, scanErr
		}
		summary := "project paused"
		if trimmed := strings.TrimSpace(reason); trimmed != "" {
			summary = "project paused: " + trimmed
		}
		items = append(items, l.newDashboardItem(
			"paused_project",
			projectLabel,
			summary,
			"paused",
			updatedAt,
			0,
			&projectID,
			&projectLabel,
			nil,
			nil,
			nil,
			nil,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadHumanInputItems(ctx context.Context, organizationID, userID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			i.title,
			i.item_type,
			i.created_at,
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title
		FROM inbox_item i
		LEFT JOIN project_task t ON t.id = i.source_task_id
		LEFT JOIN project p ON p.id = COALESCE(i.source_project_id, t.project_id)
		WHERE i.organization_id = $1
		  AND i.target_user_id = $2
		  AND i.is_acted = false
		  AND i.item_type IN ('human_approval_required', 'draft_action_review', 'browser_handoff', 'blocker_filed', 'system_alert')
		  AND NOT (
			i.item_type = 'blocker_filed'
			AND EXISTS (
				SELECT 1
				FROM project_task blocked_task
				WHERE blocked_task.id = i.source_task_id
				  AND COALESCE((blocked_task.metadata->'agent_turn_validation_guard'->>'blocked')::boolean, false)
			)
		  )
		ORDER BY i.created_at DESC, i.id DESC
		LIMIT $3
	`, organizationID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			inboxTitle   string
			itemType     string
			createdAt    time.Time
			projectID    *uuid.UUID
			projectLabel *string
			taskID       *uuid.UUID
			taskNumber   *int
			taskTitle    *string
		)
		if scanErr := rows.Scan(&inboxTitle, &itemType, &createdAt, &projectID, &projectLabel, &taskID, &taskNumber, &taskTitle); scanErr != nil {
			return nil, scanErr
		}
		title := strings.TrimSpace(inboxTitle)
		if taskID != nil && taskNumber != nil && taskTitle != nil {
			title = formatTaskLabel(*taskNumber, *taskTitle)
		}
		summary := "waiting for human input"
		if trimmed := strings.TrimSpace(inboxTitle); trimmed != "" && title != trimmed {
			summary = fmt.Sprintf("waiting for human input: %s", trimmed)
		}
		items = append(items, l.newDashboardItem(
			"human_input",
			title,
			summary,
			itemType,
			createdAt,
			0,
			projectID,
			projectLabel,
			taskID,
			taskNumber,
			taskTitle,
			nil,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) loadRecentFailureItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	return l.loadRunEventItems(ctx, organizationID, limit, []string{
		"run_failed",
		"attempt_failed",
		"run_timed_out",
		"run_cancelled",
		"wakeup_deferred",
		"wakeup_promoted",
		"supervisor_recovery",
	})
}

func (l operatorDashboardLoader) loadRecentActivityItems(ctx context.Context, organizationID uuid.UUID, limit int) ([]operatorDashboardItemResponse, error) {
	return l.loadRunEventItems(ctx, organizationID, limit, []string{
		"run_started",
		"run_completed",
		"run_failed",
		"run_cancelled",
		"run_timed_out",
		"run_paused",
		"attempt_failed",
		"supervisor_recovery",
		"wakeup_coalesced",
		"wakeup_deferred",
		"wakeup_promoted",
	})
}

func (l operatorDashboardLoader) loadRunEventItems(ctx context.Context, organizationID uuid.UUID, limit int, eventTypes []string) ([]operatorDashboardItemResponse, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT
			re.event_type,
			re.created_at,
			COALESCE(NULLIF(r.failure_reason, ''), NULLIF(re.payload->>'reason', ''), NULLIF(re.payload->>'failure_reason', ''), NULLIF(re.payload->>'message', '')),
			p.id,
			p.display_name,
			t.id,
			t.task_number,
			t.title,
			r.id
		FROM run_event re
		JOIN run r ON r.id = re.run_id
		LEFT JOIN project_task t ON t.id = r.task_id
		LEFT JOIN project p ON p.id = COALESCE(r.project_id, t.project_id)
		WHERE r.organization_id = $1
		  AND re.created_at >= $2
		  AND re.event_type = ANY($3::text[])
		ORDER BY re.created_at DESC, re.run_id DESC
		LIMIT $4
	`, organizationID, l.now.Add(-operatorDashboardRecentWindow), eventTypes, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]operatorDashboardItemResponse, 0, limit)
	for rows.Next() {
		var (
			eventType    string
			createdAt    time.Time
			detail       *string
			projectID    *uuid.UUID
			projectLabel *string
			taskID       *uuid.UUID
			taskNumber   *int
			taskTitle    *string
			runID        uuid.UUID
		)
		if scanErr := rows.Scan(&eventType, &createdAt, &detail, &projectID, &projectLabel, &taskID, &taskNumber, &taskTitle, &runID); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, l.newDashboardItem(
			eventType,
			taskOrProjectTitle(taskID, taskNumber, taskTitle, projectLabel, runID),
			runEventSummary(eventType, detail),
			eventType,
			createdAt,
			0,
			projectID,
			projectLabel,
			taskID,
			taskNumber,
			taskTitle,
			&runID,
		))
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (l operatorDashboardLoader) newDashboardItem(
	kind string,
	title string,
	summary string,
	status string,
	updatedAt time.Time,
	staleThresholdSeconds int,
	projectID *uuid.UUID,
	projectLabel *string,
	taskID *uuid.UUID,
	taskNumber *int,
	taskTitle *string,
	runID *uuid.UUID,
) operatorDashboardItemResponse {
	ageSeconds := 0
	if !updatedAt.IsZero() {
		ageSeconds = int(maxDuration(l.now.Sub(updatedAt), 0) / time.Second)
	}
	staleForSeconds := 0
	if staleThresholdSeconds > 0 && ageSeconds > staleThresholdSeconds {
		staleForSeconds = ageSeconds - staleThresholdSeconds
	}
	item := operatorDashboardItemResponse{
		Kind:            strings.TrimSpace(kind),
		Title:           strings.TrimSpace(title),
		Summary:         strings.TrimSpace(summary),
		Status:          strings.TrimSpace(status),
		UpdatedAt:       updatedAt,
		AgeSeconds:      ageSeconds,
		StaleForSeconds: staleForSeconds,
		Links:           operatorDashboardLinksFor(projectID, taskID, runID),
	}
	if projectID != nil && *projectID != uuid.Nil {
		item.Project = &operatorDashboardRefResponse{
			ID:    *projectID,
			Label: operatorDashboardStringValue(projectLabel),
		}
	}
	if taskID != nil && *taskID != uuid.Nil {
		item.Task = &operatorDashboardTaskRef{
			ID:         *taskID,
			TaskNumber: operatorDashboardIntValue(taskNumber),
			Label:      formatTaskLabel(operatorDashboardIntValue(taskNumber), operatorDashboardStringValue(taskTitle)),
		}
	}
	if runID != nil && *runID != uuid.Nil {
		item.Run = &operatorDashboardRefResponse{
			ID:    *runID,
			Label: "Run " + shortUUID(*runID),
		}
	}
	return item
}

func operatorDashboardLinksFor(projectID, taskID, runID *uuid.UUID) operatorDashboardLinks {
	links := operatorDashboardLinks{}
	if projectID != nil && *projectID != uuid.Nil {
		links.Project = "/v1/projects/" + projectID.String()
	}
	if taskID != nil && *taskID != uuid.Nil {
		links.Task = "/v1/tasks/" + taskID.String()
	}
	if runID != nil && *runID != uuid.Nil {
		links.Run = "/v1/control/runs/" + runID.String()
	}
	return links
}

func taskOrProjectTitle(taskID *uuid.UUID, taskNumber *int, taskTitle *string, projectLabel *string, runID uuid.UUID) string {
	if taskID != nil && *taskID != uuid.Nil {
		return formatTaskLabel(operatorDashboardIntValue(taskNumber), operatorDashboardStringValue(taskTitle))
	}
	if trimmed := strings.TrimSpace(operatorDashboardStringValue(projectLabel)); trimmed != "" {
		return trimmed
	}
	return "Run " + shortUUID(runID)
}

func runEventSummary(eventType string, detail *string) string {
	label := map[string]string{
		"run_started":         "run started",
		"run_completed":       "run completed",
		"run_failed":          "run failed",
		"run_cancelled":       "run cancelled",
		"run_timed_out":       "run timed out",
		"run_paused":          "run paused",
		"attempt_failed":      "retry failed",
		"supervisor_recovery": "supervisor recovery",
		"wakeup_coalesced":    "duplicate wakeup coalesced",
		"wakeup_deferred":     "wakeup deferred",
		"wakeup_promoted":     "deferred wakeup promoted",
	}[strings.TrimSpace(eventType)]
	if label == "" {
		label = strings.ReplaceAll(strings.TrimSpace(eventType), "_", " ")
	}
	if trimmed := strings.TrimSpace(operatorDashboardStringValue(detail)); trimmed != "" {
		return label + ": " + trimmed
	}
	return label
}

func buildValidationBlockedSummary(toolName, failureReason, failureCode string) string {
	reason := strings.TrimSpace(failureReason)
	if reason == "" {
		reason = strings.TrimSpace(failureCode)
	}
	toolName = strings.TrimSpace(toolName)

	summary := "validation loop blocked"
	switch {
	case toolName != "" && reason != "":
		summary = fmt.Sprintf("validation loop blocked: %s (%s)", toolName, reason)
	case toolName != "":
		summary = fmt.Sprintf("validation loop blocked: %s", toolName)
	case reason != "":
		summary = fmt.Sprintf("validation loop blocked: %s", reason)
	}
	return summary + ". Resume task to retry."
}

func formatTaskLabel(taskNumber int, title string) string {
	trimmedTitle := strings.TrimSpace(title)
	if taskNumber > 0 && trimmedTitle != "" {
		return fmt.Sprintf("OC-%d: %s", taskNumber, trimmedTitle)
	}
	if taskNumber > 0 {
		return fmt.Sprintf("OC-%d", taskNumber)
	}
	if trimmedTitle == "" {
		return "Task"
	}
	return trimmedTitle
}

func operatorDashboardStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func operatorDashboardIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func shortUUID(id uuid.UUID) string {
	value := id.String()
	if len(value) < 8 {
		return value
	}
	return value[:8]
}

func maxDuration(value, floor time.Duration) time.Duration {
	if value < floor {
		return floor
	}
	return value
}
