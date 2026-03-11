package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const projectOperationalStateTerminalStalled = "terminal_stalled"

type projectOperationalBlockerResponse struct {
	ID         uuid.UUID `json:"id"`
	TaskNumber int       `json:"task_number"`
	Label      string    `json:"label"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
}

type projectTerminalStallRecord struct {
	ProjectID        uuid.UUID
	ProjectLabel     string
	Summary          string
	BlockingTasks    []projectOperationalBlockerResponse
	BlockedTaskCount int
	ReviewTaskCount  int
	UpdatedAt        time.Time
}

type projectTerminalStallCandidate struct {
	ProjectID        uuid.UUID
	ProjectLabel     string
	BlockedTaskCount int
	ReviewTaskCount  int
	UpdatedAt        time.Time
}

type projectTerminalStallTaskRow struct {
	ProjectID               uuid.UUID
	TaskID                  uuid.UUID
	TaskNumber              int
	TaskTitle               string
	WorkStatus              string
	FlowBacked              bool
	CurrentFlowNodeMissing  bool
	RuntimeStatus           string
	RuntimeFailureReason    string
	ValidationToolName      string
	ValidationFailureReason string
	ValidationFailureCode   string
	ValidationBlocked       bool
	BlockerReason           string
}

type projectStallDetector struct {
	pool *pgxpool.Pool
}

func (d projectStallDetector) CountTerminalProjectStalls(ctx context.Context, organizationID uuid.UUID) (int, error) {
	records, err := d.LoadTerminalProjectStalls(ctx, organizationID, nil, 0)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

func (d projectStallDetector) LoadTerminalProjectStalls(ctx context.Context, organizationID uuid.UUID, projectIDs []uuid.UUID, limit int) ([]projectTerminalStallRecord, error) {
	if d.pool == nil || organizationID == uuid.Nil {
		return nil, nil
	}

	filterIDs := normalizedProjectIDFilter(projectIDs)
	candidates, err := d.loadTerminalProjectStallCandidates(ctx, organizationID, filterIDs, limit)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	candidateIDs := make([]uuid.UUID, 0, len(candidates))
	candidateByID := make(map[uuid.UUID]projectTerminalStallCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateIDs = append(candidateIDs, candidate.ProjectID)
		candidateByID[candidate.ProjectID] = candidate
	}

	blockingTasks, err := d.loadTerminalProjectBlockingTasks(ctx, candidateIDs)
	if err != nil {
		return nil, err
	}

	records := make([]projectTerminalStallRecord, 0, len(candidates))
	for _, candidate := range candidates {
		record := projectTerminalStallRecord{
			ProjectID:        candidate.ProjectID,
			ProjectLabel:     candidate.ProjectLabel,
			BlockedTaskCount: candidate.BlockedTaskCount,
			ReviewTaskCount:  candidate.ReviewTaskCount,
			UpdatedAt:        candidate.UpdatedAt.UTC(),
			BlockingTasks:    blockingTasks[candidate.ProjectID],
		}
		record.Summary = buildTerminalProjectStallSummary(record.BlockedTaskCount, record.ReviewTaskCount)
		records = append(records, record)
	}

	return records, nil
}

func (d projectStallDetector) loadTerminalProjectStallCandidates(ctx context.Context, organizationID uuid.UUID, projectIDs []uuid.UUID, limit int) ([]projectTerminalStallCandidate, error) {
	limitClause := ""
	args := []any{organizationID, projectIDs}
	if limit > 0 {
		limitClause = "\n\tLIMIT $3"
		args = append(args, limit)
	}

	rows, err := d.pool.Query(ctx, `
		WITH filtered_projects AS (
			SELECT id, display_name, settings, updated_at
			FROM project
			WHERE organization_id = $1
			  AND status = 'active'
			  AND (cardinality($2::uuid[]) = 0 OR id = ANY($2::uuid[]))
		),
		task_map AS (
			SELECT
				t.id,
				t.id::text AS task_id_text,
				t.project_id,
				t.work_status,
				t.requires_human_review,
				t.updated_at,
				t.blocks_scope
			FROM project_task t
			JOIN filtered_projects fp ON fp.id = t.project_id
		),
		active_runs AS (
			SELECT
				COALESCE(r.project_id, tm.project_id) AS project_id,
				COUNT(*) AS active_run_count
			FROM runtime_state rs
			JOIN run r ON r.id = rs.active_run_id
			LEFT JOIN task_map tm ON tm.id = r.task_id
			WHERE rs.active_run_id IS NOT NULL
			  AND r.organization_id = $1
			  AND r.status IN ('created', 'in_progress', 'cancelling', 'paused')
			  AND COALESCE(r.project_id, tm.project_id) IS NOT NULL
			GROUP BY COALESCE(r.project_id, tm.project_id)
		),
		unresolved_dependencies AS (
			SELECT DISTINCT d.source_id AS task_id
			FROM project_task_dependency d
			JOIN task_map tm ON tm.id = d.source_id
			LEFT JOIN project_task dep_task
			  ON d.depends_on_type = 'project_task'
			 AND dep_task.id = d.depends_on_id
			LEFT JOIN project_subtask dep_subtask
			  ON d.depends_on_type = 'project_subtask'
			 AND dep_subtask.id = d.depends_on_id
			WHERE d.source_type = 'project_task'
			  AND (
				(d.depends_on_type = 'project_task' AND COALESCE(dep_task.work_status, '') NOT IN ('done', 'cancelled'))
				OR
				(d.depends_on_type = 'project_subtask' AND COALESCE(dep_subtask.work_status, '') <> 'done')
			  )
		),
		gate_blocked AS (
			SELECT DISTINCT t.id AS task_id
			FROM task_map t
			JOIN task_map gate ON gate.project_id = t.project_id
			WHERE t.work_status = 'queued'
			  AND gate.blocks_scope = 'all'
			  AND gate.id <> t.id
			  AND gate.work_status NOT IN ('done', 'cancelled')
		),
		eligible_queued AS (
			SELECT t.project_id, COUNT(*) AS eligible_queued_count
			FROM task_map t
			LEFT JOIN unresolved_dependencies ud ON ud.task_id = t.id
			LEFT JOIN gate_blocked gb ON gb.task_id = t.id
			WHERE t.work_status = 'queued'
			  AND ud.task_id IS NULL
			  AND gb.task_id IS NULL
			GROUP BY t.project_id
		),
		session_map AS (
			SELECT
				s.id::text AS session_id_text,
				CASE
					WHEN s.scope_type = 'project' THEN s.scope_id
					WHEN s.scope_type = 'project_task' THEN tm.project_id
					ELSE NULL
				END AS project_id
			FROM chat_session s
			LEFT JOIN task_map tm
			  ON s.scope_type = 'project_task'
			 AND s.scope_id = tm.id
			WHERE s.organization_id = $1
			  AND s.scope_type IN ('project', 'project_task')
		),
		project_jobs AS (
			SELECT project_id, COUNT(DISTINCT job_id) AS pending_job_count
			FROM (
				SELECT jq.id AS job_id, fp.id AS project_id
				FROM job_queue jq
				JOIN filtered_projects fp ON jq.payload->>'project_id' = fp.id::text
				WHERE jq.status IN ('pending', 'claimed')
				UNION
				SELECT jq.id AS job_id, tm.project_id
				FROM job_queue jq
				JOIN task_map tm ON jq.payload->>'task_id' = tm.task_id_text
				WHERE jq.status IN ('pending', 'claimed')
				UNION
				SELECT jq.id AS job_id, sm.project_id
				FROM job_queue jq
				JOIN session_map sm ON jq.payload->>'session_id' = sm.session_id_text
				WHERE jq.status IN ('pending', 'claimed')
				  AND sm.project_id IS NOT NULL
			) jobs
			GROUP BY project_id
		)
		SELECT
			fp.id,
			fp.display_name,
			COUNT(*) FILTER (WHERE tm.work_status = 'blocked') AS blocked_task_count,
			COUNT(*) FILTER (WHERE tm.work_status = 'review') AS review_task_count,
			COUNT(*) FILTER (WHERE tm.work_status IN ('in_progress', 'on_hold')) AS live_task_count,
			COALESCE(ar.active_run_count, 0) AS active_run_count,
			COALESCE(eq.eligible_queued_count, 0) AS eligible_queued_count,
			COALESCE(pj.pending_job_count, 0) AS pending_job_count,
			COALESCE(MAX(tm.updated_at), fp.updated_at) AS updated_at
		FROM filtered_projects fp
		LEFT JOIN task_map tm ON tm.project_id = fp.id
		LEFT JOIN active_runs ar ON ar.project_id = fp.id
		LEFT JOIN eligible_queued eq ON eq.project_id = fp.id
		LEFT JOIN project_jobs pj ON pj.project_id = fp.id
		WHERE NOT COALESCE((fp.settings->'pause'->>'is_paused')::boolean, false)
		GROUP BY
			fp.id,
			fp.display_name,
			fp.updated_at,
			ar.active_run_count,
			eq.eligible_queued_count,
			pj.pending_job_count
		HAVING COUNT(*) FILTER (WHERE tm.work_status = 'blocked') > 0
		   AND COUNT(*) FILTER (WHERE tm.work_status IN ('in_progress', 'on_hold')) = 0
		   AND COALESCE(ar.active_run_count, 0) = 0
		   AND COALESCE(eq.eligible_queued_count, 0) = 0
		   AND COALESCE(pj.pending_job_count, 0) = 0
		ORDER BY updated_at DESC, fp.id DESC`+limitClause,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]projectTerminalStallCandidate, 0)
	for rows.Next() {
		var (
			candidate           projectTerminalStallCandidate
			liveTaskCount       int
			activeRunCount      int
			eligibleQueuedCount int
			pendingJobCount     int
		)
		if scanErr := rows.Scan(
			&candidate.ProjectID,
			&candidate.ProjectLabel,
			&candidate.BlockedTaskCount,
			&candidate.ReviewTaskCount,
			&liveTaskCount,
			&activeRunCount,
			&eligibleQueuedCount,
			&pendingJobCount,
			&candidate.UpdatedAt,
		); scanErr != nil {
			return nil, scanErr
		}
		candidate.UpdatedAt = candidate.UpdatedAt.UTC()
		candidates = append(candidates, candidate)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return candidates, nil
}

func (d projectStallDetector) loadTerminalProjectBlockingTasks(ctx context.Context, projectIDs []uuid.UUID) (map[uuid.UUID][]projectOperationalBlockerResponse, error) {
	projectIDs = normalizedProjectIDFilter(projectIDs)
	if len(projectIDs) == 0 {
		return map[uuid.UUID][]projectOperationalBlockerResponse{}, nil
	}

	rows, err := d.pool.Query(ctx, `
		SELECT
			t.project_id,
			t.id,
			t.task_number,
			t.title,
			t.work_status,
			(t.flow_template_id IS NOT NULL) AS flow_backed,
			(t.current_flow_node_id IS NULL) AS current_flow_node_missing,
			COALESCE(NULLIF(rs.metadata->>'status', ''), ''),
			COALESCE(NULLIF(rs.metadata->>'failure_reason', ''), ''),
			COALESCE(NULLIF(t.metadata->'agent_turn_validation_guard'->>'tool_name', ''), ''),
			COALESCE(NULLIF(t.metadata->'agent_turn_validation_guard'->>'failure_reason', ''), ''),
			COALESCE(NULLIF(t.metadata->'agent_turn_validation_guard'->>'failure_code', ''), ''),
			COALESCE((t.metadata->'agent_turn_validation_guard'->>'blocked')::boolean, false),
			COALESCE(
				NULLIF(inbox_reason.reason, ''),
				NULLIF(task_event_reason.reason, ''),
				''
			) AS blocker_reason
		FROM project_task t
		LEFT JOIN runtime_state rs
		  ON rs.scope_type = 'task'
		 AND rs.scope_id = t.id
		LEFT JOIN LATERAL (
			SELECT action_payload->>'reason' AS reason
			FROM inbox_item i
			WHERE i.source_task_id = t.id
			  AND i.item_type IN ('blocker', 'blocker_filed')
			ORDER BY i.created_at DESC, i.id DESC
			LIMIT 1
		) inbox_reason ON true
		LEFT JOIN LATERAL (
			SELECT payload->>'blocker_reason' AS reason
			FROM project_task_event e
			WHERE e.task_id = t.id
			  AND e.event_type = 'status.changed'
			  AND COALESCE(e.payload->>'to_status', '') = 'blocked'
			ORDER BY e.created_at DESC, e.id DESC
			LIMIT 1
		) task_event_reason ON true
		WHERE t.project_id = ANY($1::uuid[])
		  AND t.work_status IN ('blocked', 'review')
		ORDER BY
			t.project_id,
			CASE WHEN t.work_status = 'blocked' THEN 0 ELSE 1 END,
			t.updated_at DESC,
			t.id DESC
	`, projectIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[uuid.UUID][]projectOperationalBlockerResponse, len(projectIDs))
	for rows.Next() {
		var row projectTerminalStallTaskRow
		if scanErr := rows.Scan(
			&row.ProjectID,
			&row.TaskID,
			&row.TaskNumber,
			&row.TaskTitle,
			&row.WorkStatus,
			&row.FlowBacked,
			&row.CurrentFlowNodeMissing,
			&row.RuntimeStatus,
			&row.RuntimeFailureReason,
			&row.ValidationToolName,
			&row.ValidationFailureReason,
			&row.ValidationFailureCode,
			&row.ValidationBlocked,
			&row.BlockerReason,
		); scanErr != nil {
			return nil, scanErr
		}
		result[row.ProjectID] = append(result[row.ProjectID], projectOperationalBlockerResponse{
			ID:         row.TaskID,
			TaskNumber: row.TaskNumber,
			Label:      formatTaskLabel(row.TaskNumber, row.TaskTitle),
			Status:     strings.TrimSpace(row.WorkStatus),
			Reason:     terminalProjectTaskReason(row),
		})
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return result, nil
}

func buildTerminalProjectStallSummary(blockedTaskCount, reviewTaskCount int) string {
	parts := make([]string, 0, 2)
	if blockedTaskCount > 0 {
		parts = append(parts, countNoun(blockedTaskCount, "blocked task"))
	}
	if reviewTaskCount > 0 {
		parts = append(parts, countNoun(reviewTaskCount, "review wait"))
	}
	if len(parts) == 0 {
		return "terminal stall: no runnable work remains"
	}
	return fmt.Sprintf("terminal stall: %s; no active runs or pending jobs remain", strings.Join(parts, ", "))
}

func terminalProjectTaskReason(row projectTerminalStallTaskRow) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(row.WorkStatus), "review"):
		return "waiting for review"
	case strings.EqualFold(strings.TrimSpace(row.RuntimeStatus), "stranded"):
		reason := strings.TrimSpace(row.RuntimeFailureReason)
		if reason == "" {
			reason = "automatic recovery failed"
		}
		return "execution stranded: " + reason
	case row.ValidationBlocked:
		return buildValidationBlockedSummary(row.ValidationToolName, row.ValidationFailureReason, row.ValidationFailureCode)
	case strings.TrimSpace(row.RuntimeFailureReason) != "":
		return strings.TrimSpace(row.RuntimeFailureReason)
	case strings.TrimSpace(row.BlockerReason) != "":
		return strings.TrimSpace(row.BlockerReason)
	case strings.EqualFold(strings.TrimSpace(row.WorkStatus), "blocked") && row.FlowBacked && row.CurrentFlowNodeMissing && strings.TrimSpace(row.RuntimeStatus) == "":
		return "automatic repair blocked impossible live task state: flow-backed task lost runtime owner and active execution"
	default:
		return "blocked without recorded reason"
	}
}

func countNoun(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func normalizedProjectIDFilter(projectIDs []uuid.UUID) []uuid.UUID {
	if len(projectIDs) == 0 {
		return []uuid.UUID{}
	}
	result := make([]uuid.UUID, 0, len(projectIDs))
	seen := make(map[uuid.UUID]struct{}, len(projectIDs))
	for _, projectID := range projectIDs {
		if projectID == uuid.Nil {
			continue
		}
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		result = append(result, projectID)
	}
	return result
}
