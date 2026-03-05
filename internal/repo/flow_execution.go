package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FlowNodeExecution struct {
	ID          uuid.UUID
	TaskID      uuid.UUID
	FlowNodeID  uuid.UUID
	VisitNumber int
	Status      string
	SessionID   *uuid.UUID
	CommitSHA   *string
	StartedAt   time.Time
	CompletedAt *time.Time
	Metadata    json.RawMessage
}

type ProjectSubtask struct {
	ID                  uuid.UUID
	TaskID              uuid.UUID
	FlowNodeExecutionID uuid.UUID
	Title               string
	Description         *string
	WorkStatus          string
	SequenceNumber      int
	AssigneeType        *string
	AssigneeID          *uuid.UUID
	CreatedByType       string
	CreatedByID         *uuid.UUID
	CompletedAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ProjectTaskDependency struct {
	ID            uuid.UUID
	SourceType    string
	SourceID      uuid.UUID
	DependsOnType string
	DependsOnID   uuid.UUID
	CreatedByType string
	CreatedByID   *uuid.UUID
	CreatedAt     time.Time
}

type ProjectTaskParticipant struct {
	ID              uuid.UUID
	TaskID          uuid.UUID
	ParticipantType string
	ParticipantID   uuid.UUID
	Role            string
	JoinedAt        time.Time
	LeftAt          *time.Time
}

type FlowNodeExecutionRepo struct {
	pool *pgxpool.Pool
}

func NewFlowNodeExecutionRepo(pool *pgxpool.Pool) *FlowNodeExecutionRepo {
	return &FlowNodeExecutionRepo{pool: pool}
}

func (r *FlowNodeExecutionRepo) Create(ctx context.Context, execution FlowNodeExecution) (FlowNodeExecution, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO flow_node_execution (
			task_id,
			flow_node_id,
			visit_number,
			status,
			session_id,
			commit_sha,
			started_at,
			completed_at,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, now()), $8, $9::jsonb)
		RETURNING id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
	`,
		execution.TaskID,
		execution.FlowNodeID,
		defaultVisitNumber(execution.VisitNumber),
		defaultFlowNodeExecutionStatus(execution.Status),
		execution.SessionID,
		trimStringPointer(execution.CommitSHA),
		zeroTimeToNil(execution.StartedAt),
		execution.CompletedAt,
		normalizeFlowExecutionJSON(execution.Metadata),
	)

	created, err := scanFlowNodeExecution(row)
	if err != nil {
		return FlowNodeExecution{}, mapDBError(err)
	}
	return created, nil
}

func (r *FlowNodeExecutionRepo) GetByID(ctx context.Context, id uuid.UUID) (FlowNodeExecution, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
		FROM flow_node_execution
		WHERE id = $1
	`, id)

	execution, err := scanFlowNodeExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeExecution{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeExecution{}, mapDBError(err)
	}
	return execution, nil
}

func (r *FlowNodeExecutionRepo) GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (FlowNodeExecution, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
		FROM flow_node_execution
		WHERE task_id = $1
		  AND flow_node_id = $2
		  AND status = 'active'
		ORDER BY started_at DESC, id DESC
		LIMIT 1
	`, taskID, flowNodeID)

	execution, err := scanFlowNodeExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeExecution{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeExecution{}, mapDBError(err)
	}
	return execution, nil
}

func (r *FlowNodeExecutionRepo) ListByTask(ctx context.Context, taskID uuid.UUID) ([]FlowNodeExecution, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
		FROM flow_node_execution
		WHERE task_id = $1
		ORDER BY started_at ASC, id ASC
	`, taskID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	executions := make([]FlowNodeExecution, 0)
	for rows.Next() {
		execution, scanErr := scanFlowNodeExecution(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		executions = append(executions, execution)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return executions, nil
}

func (r *FlowNodeExecutionRepo) Complete(ctx context.Context, id uuid.UUID) (FlowNodeExecution, error) {
	return r.updateStatus(ctx, id, "completed")
}

func (r *FlowNodeExecutionRepo) Reject(ctx context.Context, id uuid.UUID) (FlowNodeExecution, error) {
	return r.updateStatus(ctx, id, "rejected")
}

func (r *FlowNodeExecutionRepo) Abandon(ctx context.Context, id uuid.UUID) (FlowNodeExecution, error) {
	return r.updateStatus(ctx, id, "abandoned")
}

func (r *FlowNodeExecutionRepo) updateStatus(ctx context.Context, id uuid.UUID, status string) (FlowNodeExecution, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node_execution
		SET
			status = $2,
			completed_at = COALESCE(completed_at, now())
		WHERE id = $1
		RETURNING id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
	`, id, strings.TrimSpace(status))

	updated, err := scanFlowNodeExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeExecution{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeExecution{}, mapDBError(err)
	}
	return updated, nil
}

func (r *FlowNodeExecutionRepo) RecordCommitSHA(ctx context.Context, id uuid.UUID, commitSHA string) (FlowNodeExecution, error) {
	trimmedCommit := strings.TrimSpace(commitSHA)
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node_execution
		SET commit_sha = NULLIF($2, '')
		WHERE id = $1
		RETURNING id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
	`, id, trimmedCommit)

	updated, err := scanFlowNodeExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeExecution{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeExecution{}, mapDBError(err)
	}
	return updated, nil
}

func (r *FlowNodeExecutionRepo) UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (FlowNodeExecution, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node_execution
		SET metadata = $2::jsonb
		WHERE id = $1
		RETURNING id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
	`, id, normalizeFlowExecutionJSON(metadata))

	updated, err := scanFlowNodeExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeExecution{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeExecution{}, mapDBError(err)
	}
	return updated, nil
}

func (r *FlowNodeExecutionRepo) SetSessionID(ctx context.Context, id, sessionID uuid.UUID) (FlowNodeExecution, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node_execution
		SET session_id = $2
		WHERE id = $1
		RETURNING id, task_id, flow_node_id, visit_number, status, session_id, commit_sha, started_at, completed_at, metadata
	`, id, sessionID)

	updated, err := scanFlowNodeExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeExecution{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeExecution{}, mapDBError(err)
	}
	return updated, nil
}

type ProjectSubtaskRepo struct {
	pool *pgxpool.Pool
}

func NewProjectSubtaskRepo(pool *pgxpool.Pool) *ProjectSubtaskRepo {
	return &ProjectSubtaskRepo{pool: pool}
}

func (r *ProjectSubtaskRepo) Create(ctx context.Context, subtask ProjectSubtask) (ProjectSubtask, error) {
	sequence := subtask.SequenceNumber
	if sequence <= 0 {
		nextSequence, err := r.NextSequenceNumber(ctx, subtask.FlowNodeExecutionID)
		if err != nil {
			return ProjectSubtask{}, err
		}
		sequence = nextSequence
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO project_subtask (
			task_id,
			flow_node_execution_id,
			title,
			description,
			work_status,
			sequence_number,
			assignee_type,
			assignee_id,
			created_by_type,
			created_by_id,
			completed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, task_id, flow_node_execution_id, title, description, work_status, sequence_number, assignee_type, assignee_id, created_by_type, created_by_id, completed_at, created_at, updated_at
	`,
		subtask.TaskID,
		subtask.FlowNodeExecutionID,
		strings.TrimSpace(subtask.Title),
		subtask.Description,
		defaultSubtaskStatus(subtask.WorkStatus),
		sequence,
		trimStringPointer(subtask.AssigneeType),
		subtask.AssigneeID,
		strings.TrimSpace(subtask.CreatedByType),
		subtask.CreatedByID,
		subtask.CompletedAt,
	)

	created, err := scanProjectSubtask(row)
	if err != nil {
		return ProjectSubtask{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProjectSubtaskRepo) GetByID(ctx context.Context, id uuid.UUID) (ProjectSubtask, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, task_id, flow_node_execution_id, title, description, work_status, sequence_number, assignee_type, assignee_id, created_by_type, created_by_id, completed_at, created_at, updated_at
		FROM project_subtask
		WHERE id = $1
	`, id)

	subtask, err := scanProjectSubtask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectSubtask{}, ErrNotFound
	}
	if err != nil {
		return ProjectSubtask{}, mapDBError(err)
	}
	return subtask, nil
}

func (r *ProjectSubtaskRepo) ListByExecution(ctx context.Context, flowNodeExecutionID uuid.UUID) ([]ProjectSubtask, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, flow_node_execution_id, title, description, work_status, sequence_number, assignee_type, assignee_id, created_by_type, created_by_id, completed_at, created_at, updated_at
		FROM project_subtask
		WHERE flow_node_execution_id = $1
		ORDER BY sequence_number ASC, id ASC
	`, flowNodeExecutionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	subtasks := make([]ProjectSubtask, 0)
	for rows.Next() {
		subtask, scanErr := scanProjectSubtask(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		subtasks = append(subtasks, subtask)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return subtasks, nil
}

func (r *ProjectSubtaskRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (ProjectSubtask, error) {
	trimmedStatus := strings.TrimSpace(status)
	row := r.pool.QueryRow(ctx, `
		UPDATE project_subtask
		SET
			work_status = $2,
			completed_at = CASE WHEN $2 IN ('done', 'cancelled') THEN now() ELSE NULL END
		WHERE id = $1
		RETURNING id, task_id, flow_node_execution_id, title, description, work_status, sequence_number, assignee_type, assignee_id, created_by_type, created_by_id, completed_at, created_at, updated_at
	`, id, trimmedStatus)

	updated, err := scanProjectSubtask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectSubtask{}, ErrNotFound
	}
	if err != nil {
		return ProjectSubtask{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectSubtaskRepo) NextSequenceNumber(ctx context.Context, flowNodeExecutionID uuid.UUID) (int, error) {
	var next int
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence_number), 0) + 1
		FROM project_subtask
		WHERE flow_node_execution_id = $1
	`, flowNodeExecutionID).Scan(&next); err != nil {
		return 0, mapDBError(err)
	}
	return next, nil
}

type ProjectTaskDependencyRepo struct {
	pool *pgxpool.Pool
}

func NewProjectTaskDependencyRepo(pool *pgxpool.Pool) *ProjectTaskDependencyRepo {
	return &ProjectTaskDependencyRepo{pool: pool}
}

func (r *ProjectTaskDependencyRepo) Add(ctx context.Context, dependency ProjectTaskDependency) (ProjectTaskDependency, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO project_task_dependency (
			source_type,
			source_id,
			depends_on_type,
			depends_on_id,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, source_type, source_id, depends_on_type, depends_on_id, created_by_type, created_by_id, created_at
	`,
		strings.TrimSpace(dependency.SourceType),
		dependency.SourceID,
		strings.TrimSpace(dependency.DependsOnType),
		dependency.DependsOnID,
		strings.TrimSpace(dependency.CreatedByType),
		dependency.CreatedByID,
	)

	created, err := scanProjectTaskDependency(row)
	if err != nil {
		return ProjectTaskDependency{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProjectTaskDependencyRepo) Remove(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM project_task_dependency
		WHERE id = $1
	`, id)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ProjectTaskDependencyRepo) ListOutbound(ctx context.Context, sourceType string, sourceID uuid.UUID) ([]ProjectTaskDependency, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, source_type, source_id, depends_on_type, depends_on_id, created_by_type, created_by_id, created_at
		FROM project_task_dependency
		WHERE source_type = $1
		  AND source_id = $2
		ORDER BY created_at DESC, id DESC
	`, strings.TrimSpace(sourceType), sourceID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	dependencies := make([]ProjectTaskDependency, 0)
	for rows.Next() {
		dependency, scanErr := scanProjectTaskDependency(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		dependencies = append(dependencies, dependency)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return dependencies, nil
}

func (r *ProjectTaskDependencyRepo) ListInbound(ctx context.Context, dependsOnType string, dependsOnID uuid.UUID) ([]ProjectTaskDependency, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, source_type, source_id, depends_on_type, depends_on_id, created_by_type, created_by_id, created_at
		FROM project_task_dependency
		WHERE depends_on_type = $1
		  AND depends_on_id = $2
		ORDER BY created_at DESC, id DESC
	`, strings.TrimSpace(dependsOnType), dependsOnID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	dependencies := make([]ProjectTaskDependency, 0)
	for rows.Next() {
		dependency, scanErr := scanProjectTaskDependency(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		dependencies = append(dependencies, dependency)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return dependencies, nil
}

func (r *ProjectTaskDependencyRepo) CheckCycle(ctx context.Context, sourceType string, sourceID, dependsOnID uuid.UUID) (bool, error) {
	var hasCycle bool
	if err := r.pool.QueryRow(ctx, `
		WITH RECURSIVE walk(node_id) AS (
			SELECT $3::uuid
			UNION
			SELECT d.depends_on_id
			FROM project_task_dependency d
			JOIN walk w ON d.source_id = w.node_id
			WHERE d.source_type = $1
			  AND d.depends_on_type = $1
		)
		SELECT EXISTS (
			SELECT 1
			FROM walk
			WHERE node_id = $2
		)
	`, strings.TrimSpace(sourceType), sourceID, dependsOnID).Scan(&hasCycle); err != nil {
		return false, mapDBError(err)
	}
	return hasCycle, nil
}

type ProjectTaskParticipantRepo struct {
	pool *pgxpool.Pool
}

func NewProjectTaskParticipantRepo(pool *pgxpool.Pool) *ProjectTaskParticipantRepo {
	return &ProjectTaskParticipantRepo{pool: pool}
}

func (r *ProjectTaskParticipantRepo) Add(ctx context.Context, participant ProjectTaskParticipant) (ProjectTaskParticipant, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO project_task_participant (
			task_id,
			participant_type,
			participant_id,
			role,
			joined_at,
			left_at
		)
		VALUES ($1, $2, $3, $4, COALESCE($5, now()), $6)
		RETURNING id, task_id, participant_type, participant_id, role, joined_at, left_at
	`,
		participant.TaskID,
		strings.TrimSpace(participant.ParticipantType),
		participant.ParticipantID,
		strings.TrimSpace(participant.Role),
		zeroTimeToNil(participant.JoinedAt),
		participant.LeftAt,
	)

	created, err := scanProjectTaskParticipant(row)
	if err != nil {
		return ProjectTaskParticipant{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProjectTaskParticipantRepo) Remove(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) (ProjectTaskParticipant, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project_task_participant
		SET left_at = now()
		WHERE task_id = $1
		  AND participant_type = $2
		  AND participant_id = $3
		  AND left_at IS NULL
		RETURNING id, task_id, participant_type, participant_id, role, joined_at, left_at
	`, taskID, strings.TrimSpace(participantType), participantID)

	removed, err := scanProjectTaskParticipant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTaskParticipant{}, ErrNotFound
	}
	if err != nil {
		return ProjectTaskParticipant{}, mapDBError(err)
	}
	return removed, nil
}

func (r *ProjectTaskParticipantRepo) ListActive(ctx context.Context, taskID uuid.UUID) ([]ProjectTaskParticipant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, participant_type, participant_id, role, joined_at, left_at
		FROM project_task_participant
		WHERE task_id = $1
		  AND left_at IS NULL
		ORDER BY joined_at ASC, id ASC
	`, taskID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	participants := make([]ProjectTaskParticipant, 0)
	for rows.Next() {
		participant, scanErr := scanProjectTaskParticipant(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		participants = append(participants, participant)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return participants, nil
}

func (r *ProjectTaskParticipantRepo) GetByParticipant(ctx context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) (ProjectTaskParticipant, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, task_id, participant_type, participant_id, role, joined_at, left_at
		FROM project_task_participant
		WHERE task_id = $1
		  AND participant_type = $2
		  AND participant_id = $3
		ORDER BY joined_at DESC, id DESC
		LIMIT 1
	`, taskID, strings.TrimSpace(participantType), participantID)

	participant, err := scanProjectTaskParticipant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTaskParticipant{}, ErrNotFound
	}
	if err != nil {
		return ProjectTaskParticipant{}, mapDBError(err)
	}
	return participant, nil
}

func scanFlowNodeExecution(row pgx.Row) (FlowNodeExecution, error) {
	var execution FlowNodeExecution
	if err := row.Scan(
		&execution.ID,
		&execution.TaskID,
		&execution.FlowNodeID,
		&execution.VisitNumber,
		&execution.Status,
		&execution.SessionID,
		&execution.CommitSHA,
		&execution.StartedAt,
		&execution.CompletedAt,
		&execution.Metadata,
	); err != nil {
		return FlowNodeExecution{}, err
	}
	return execution, nil
}

func scanProjectSubtask(row pgx.Row) (ProjectSubtask, error) {
	var subtask ProjectSubtask
	if err := row.Scan(
		&subtask.ID,
		&subtask.TaskID,
		&subtask.FlowNodeExecutionID,
		&subtask.Title,
		&subtask.Description,
		&subtask.WorkStatus,
		&subtask.SequenceNumber,
		&subtask.AssigneeType,
		&subtask.AssigneeID,
		&subtask.CreatedByType,
		&subtask.CreatedByID,
		&subtask.CompletedAt,
		&subtask.CreatedAt,
		&subtask.UpdatedAt,
	); err != nil {
		return ProjectSubtask{}, err
	}
	return subtask, nil
}

func scanProjectTaskDependency(row pgx.Row) (ProjectTaskDependency, error) {
	var dependency ProjectTaskDependency
	if err := row.Scan(
		&dependency.ID,
		&dependency.SourceType,
		&dependency.SourceID,
		&dependency.DependsOnType,
		&dependency.DependsOnID,
		&dependency.CreatedByType,
		&dependency.CreatedByID,
		&dependency.CreatedAt,
	); err != nil {
		return ProjectTaskDependency{}, err
	}
	return dependency, nil
}

func scanProjectTaskParticipant(row pgx.Row) (ProjectTaskParticipant, error) {
	var participant ProjectTaskParticipant
	if err := row.Scan(
		&participant.ID,
		&participant.TaskID,
		&participant.ParticipantType,
		&participant.ParticipantID,
		&participant.Role,
		&participant.JoinedAt,
		&participant.LeftAt,
	); err != nil {
		return ProjectTaskParticipant{}, err
	}
	return participant, nil
}

func defaultVisitNumber(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func defaultFlowNodeExecutionStatus(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "active"
	}
	return trimmed
}

func defaultSubtaskStatus(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "pending"
	}
	return trimmed
}

func normalizeFlowExecutionJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}

func zeroTimeToNil(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}
