package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultInboxLimit = 50
	maxInboxLimit     = 200
)

type projectTaskExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ProjectTask struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
	ProjectID           uuid.UUID
	TaskNumber          int
	Title               string
	Description         *string
	WorkStatus          string
	CurrentFlowNodeID   *uuid.UUID
	FlowTemplateID      *uuid.UUID
	ScheduleID          *uuid.UUID
	BranchName          *string
	RequiresHumanReview bool
	CreatedByType       string
	CreatedByID         *uuid.UUID
	AssignedAgentID     *uuid.UUID
	Metadata            json.RawMessage
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time
}

type ProjectTaskEvent struct {
	ID         uuid.UUID
	TaskID     uuid.UUID
	ProjectID  uuid.UUID
	EventType  string
	ActorType  string
	ActorID    *uuid.UUID
	FlowNodeID *uuid.UUID
	Payload    json.RawMessage
	CreatedAt  time.Time
}

type InboxItem struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	TargetUserID    *uuid.UUID
	ItemType        string
	SourceProjectID *uuid.UUID
	SourceTaskID    *uuid.UUID
	CreatedByType   string
	CreatedByID     *uuid.UUID
	Title           string
	Body            *string
	ActionPayload   json.RawMessage
	IsRead          bool
	IsActed         bool
	ActedAt         *time.Time
	ActedByID       *uuid.UUID
	ExpiresAt       *time.Time
	CreatedAt       time.Time
}

type MergeQueueEntry struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	TaskID        uuid.UUID
	BranchName    string
	TargetBranch  string
	CommitSHA     *string
	Position      int
	Status        string
	EnqueuedAt    time.Time
	MergedAt      *time.Time
	ArchivedAt    *time.Time
	FailureReason *string
	Metadata      json.RawMessage
}

type InboxListOptions struct {
	IncludeActed bool
	ItemType     string
	Limit        int
	CursorAt     *time.Time
	CursorID     *uuid.UUID
}

type ProjectTaskRepo struct {
	pool *pgxpool.Pool
}

func NewProjectTaskRepo(pool *pgxpool.Pool) *ProjectTaskRepo {
	return &ProjectTaskRepo{pool: pool}
}

func (r *ProjectTaskRepo) Create(ctx context.Context, task ProjectTask) (ProjectTask, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, task.ProjectID.String()); err != nil {
		return ProjectTask{}, mapDBError(err)
	}

	var nextNumber int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(task_number), 0) + 1
		FROM project_task
		WHERE project_id = $1
	`, task.ProjectID).Scan(&nextNumber); err != nil {
		return ProjectTask{}, mapDBError(err)
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO project_task (
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			completed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16)
		RETURNING
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
	`,
		task.OrganizationID,
		task.ProjectID,
		nextNumber,
		strings.TrimSpace(task.Title),
		task.Description,
		defaultProjectTaskStatus(task.WorkStatus),
		task.CurrentFlowNodeID,
		task.FlowTemplateID,
		task.ScheduleID,
		task.BranchName,
		task.RequiresHumanReview,
		strings.TrimSpace(task.CreatedByType),
		task.CreatedByID,
		task.AssignedAgentID,
		normalizeProjectTaskJSON(task.Metadata, json.RawMessage(`{}`)),
		task.CompletedAt,
	)

	created, err := scanProjectTask(row)
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	return created, nil
}

func (r *ProjectTaskRepo) GetByID(ctx context.Context, id uuid.UUID) (ProjectTask, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
		FROM project_task
		WHERE id = $1
	`, id)

	task, err := scanProjectTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTask{}, ErrNotFound
	}
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	return task, nil
}

func (r *ProjectTaskRepo) GetByProjectAndNumber(ctx context.Context, projectID uuid.UUID, taskNumber int) (ProjectTask, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
		FROM project_task
		WHERE project_id = $1
		  AND task_number = $2
	`, projectID, taskNumber)

	task, err := scanProjectTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTask{}, ErrNotFound
	}
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	return task, nil
}

func (r *ProjectTaskRepo) ListByProject(ctx context.Context, projectID uuid.UUID, statuses ...string) ([]ProjectTask, error) {
	normalizedStatuses := make([]string, 0, len(statuses))
	for _, status := range statuses {
		trimmed := strings.TrimSpace(status)
		if trimmed == "" {
			continue
		}
		normalizedStatuses = append(normalizedStatuses, trimmed)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
		FROM project_task
		WHERE project_id = $1
		  AND (cardinality($2::text[]) = 0 OR work_status = ANY($2::text[]))
		ORDER BY created_at DESC, id DESC
	`, projectID, normalizedStatuses)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	tasks := make([]ProjectTask, 0)
	for rows.Next() {
		task, scanErr := scanProjectTask(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		tasks = append(tasks, task)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return tasks, nil
}

func (r *ProjectTaskRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (ProjectTask, error) {
	normalizedStatus := strings.TrimSpace(status)
	row := r.pool.QueryRow(ctx, `
		UPDATE project_task
		SET
			work_status = $2,
			completed_at = CASE WHEN $2 IN ('done', 'cancelled') THEN now() ELSE NULL END
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
	`, id, normalizedStatus)

	updated, err := scanProjectTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTask{}, ErrNotFound
	}
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectTaskRepo) Update(ctx context.Context, task ProjectTask) (ProjectTask, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project_task
		SET
			title = $2,
			description = $3,
			work_status = $4,
			current_flow_node_id = $5,
			flow_template_id = $6,
			schedule_id = $7,
			branch_name = $8,
			requires_human_review = $9,
			assigned_agent_id = $10,
			metadata = $11::jsonb,
			completed_at = $12
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
	`,
		task.ID,
		strings.TrimSpace(task.Title),
		task.Description,
		strings.TrimSpace(task.WorkStatus),
		task.CurrentFlowNodeID,
		task.FlowTemplateID,
		task.ScheduleID,
		task.BranchName,
		task.RequiresHumanReview,
		task.AssignedAgentID,
		normalizeProjectTaskJSON(task.Metadata, json.RawMessage(`{}`)),
		task.CompletedAt,
	)

	updated, err := scanProjectTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTask{}, ErrNotFound
	}
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectTaskRepo) SetFlowNode(ctx context.Context, id uuid.UUID, flowNodeID *uuid.UUID) (ProjectTask, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE project_task
		SET current_flow_node_id = $2
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
	`, id, flowNodeID)

	updated, err := scanProjectTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTask{}, ErrNotFound
	}
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ProjectTaskRepo) SetBranch(ctx context.Context, id uuid.UUID, branchName *string) (ProjectTask, error) {
	var normalized *string
	if branchName != nil {
		trimmed := strings.TrimSpace(*branchName)
		normalized = &trimmed
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE project_task
		SET branch_name = $2
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			project_id,
			task_number,
			title,
			description,
			work_status,
			current_flow_node_id,
			flow_template_id,
			schedule_id,
			branch_name,
			requires_human_review,
			created_by_type,
			created_by_id,
			assigned_agent_id,
			metadata,
			created_at,
			updated_at,
			completed_at
	`, id, normalized)

	updated, err := scanProjectTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectTask{}, ErrNotFound
	}
	if err != nil {
		return ProjectTask{}, mapDBError(err)
	}
	return updated, nil
}

type ProjectTaskEventRepo struct {
	pool *pgxpool.Pool
}

func NewProjectTaskEventRepo(pool *pgxpool.Pool) *ProjectTaskEventRepo {
	return &ProjectTaskEventRepo{pool: pool}
}

func (r *ProjectTaskEventRepo) Record(ctx context.Context, event ProjectTaskEvent) (ProjectTaskEvent, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO project_task_event (
			task_id,
			project_id,
			event_type,
			actor_type,
			actor_id,
			flow_node_id,
			payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		RETURNING id, task_id, project_id, event_type, actor_type, actor_id, flow_node_id, payload, created_at
	`,
		event.TaskID,
		event.ProjectID,
		strings.TrimSpace(event.EventType),
		strings.TrimSpace(event.ActorType),
		event.ActorID,
		event.FlowNodeID,
		normalizeProjectTaskJSON(event.Payload, json.RawMessage(`{}`)),
	)

	recorded, err := scanProjectTaskEvent(row)
	if err != nil {
		return ProjectTaskEvent{}, mapDBError(err)
	}
	return recorded, nil
}

func (r *ProjectTaskEventRepo) ListByTask(ctx context.Context, taskID uuid.UUID) ([]ProjectTaskEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, project_id, event_type, actor_type, actor_id, flow_node_id, payload, created_at
		FROM project_task_event
		WHERE task_id = $1
		ORDER BY created_at DESC, id DESC
	`, taskID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	events := make([]ProjectTaskEvent, 0)
	for rows.Next() {
		event, scanErr := scanProjectTaskEvent(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		events = append(events, event)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return events, nil
}

func (r *ProjectTaskEventRepo) ListByProject(ctx context.Context, projectID uuid.UUID, eventType string) ([]ProjectTaskEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, project_id, event_type, actor_type, actor_id, flow_node_id, payload, created_at
		FROM project_task_event
		WHERE project_id = $1
		  AND ($2 = '' OR event_type = $2)
		ORDER BY created_at DESC, id DESC
	`, projectID, strings.TrimSpace(eventType))
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	events := make([]ProjectTaskEvent, 0)
	for rows.Next() {
		event, scanErr := scanProjectTaskEvent(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		events = append(events, event)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return events, nil
}

type InboxItemRepo struct {
	pool *pgxpool.Pool
}

func NewInboxItemRepo(pool *pgxpool.Pool) *InboxItemRepo {
	return &InboxItemRepo{pool: pool}
}

func (r *InboxItemRepo) Create(ctx context.Context, item InboxItem) (InboxItem, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO inbox_item (
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12, $13, $14, $15)
		RETURNING
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at
	`,
		item.OrganizationID,
		item.TargetUserID,
		strings.TrimSpace(item.ItemType),
		item.SourceProjectID,
		item.SourceTaskID,
		strings.TrimSpace(item.CreatedByType),
		item.CreatedByID,
		strings.TrimSpace(item.Title),
		item.Body,
		normalizeProjectTaskJSON(item.ActionPayload, json.RawMessage(`{}`)),
		item.IsRead,
		item.IsActed,
		item.ActedAt,
		item.ActedByID,
		item.ExpiresAt,
	)

	created, err := scanInboxItem(row)
	if err != nil {
		return InboxItem{}, mapDBError(err)
	}
	return created, nil
}

func (r *InboxItemRepo) GetByID(ctx context.Context, id uuid.UUID) (InboxItem, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at
		FROM inbox_item
		WHERE id = $1
	`, id)

	item, err := scanInboxItem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return InboxItem{}, ErrNotFound
	}
	if err != nil {
		return InboxItem{}, mapDBError(err)
	}
	return item, nil
}

func (r *InboxItemRepo) ListForUser(ctx context.Context, organizationID, userID uuid.UUID, options InboxListOptions) ([]InboxItem, error) {
	limit := clampInboxLimit(options.Limit)
	cursorAt, cursorID := resolveInboxCursor(options)

	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at
		FROM inbox_item
		WHERE organization_id = $1
		  AND (target_user_id = $2 OR target_user_id IS NULL)
		  AND ($3 OR is_acted = false)
		  AND ($4 = '' OR item_type = $4)
		  AND (
		  	$5::timestamptz IS NULL
			OR created_at < $5
			OR (created_at = $5 AND id < $6)
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $7
	`, organizationID, userID, options.IncludeActed, strings.TrimSpace(options.ItemType), cursorAt, cursorID, limit)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]InboxItem, 0)
	for rows.Next() {
		item, scanErr := scanInboxItem(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *InboxItemRepo) ListBroadcast(ctx context.Context, organizationID uuid.UUID, options InboxListOptions) ([]InboxItem, error) {
	limit := clampInboxLimit(options.Limit)
	cursorAt, cursorID := resolveInboxCursor(options)

	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at
		FROM inbox_item
		WHERE organization_id = $1
		  AND target_user_id IS NULL
		  AND ($2 OR is_acted = false)
		  AND ($3 = '' OR item_type = $3)
		  AND (
		  	$4::timestamptz IS NULL
			OR created_at < $4
			OR (created_at = $4 AND id < $5)
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $6
	`, organizationID, options.IncludeActed, strings.TrimSpace(options.ItemType), cursorAt, cursorID, limit)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]InboxItem, 0)
	for rows.Next() {
		item, scanErr := scanInboxItem(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *InboxItemRepo) MarkRead(ctx context.Context, id uuid.UUID) (InboxItem, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE inbox_item
		SET is_read = true
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at
	`, id)

	item, err := scanInboxItem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return InboxItem{}, ErrNotFound
	}
	if err != nil {
		return InboxItem{}, mapDBError(err)
	}
	return item, nil
}

func (r *InboxItemRepo) MarkActed(ctx context.Context, id, actedByID uuid.UUID) (InboxItem, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE inbox_item
		SET
			is_acted = true,
			acted_at = COALESCE(acted_at, now()),
			acted_by_id = $2
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			target_user_id,
			item_type,
			source_project_id,
			source_task_id,
			created_by_type,
			created_by_id,
			title,
			body,
			action_payload,
			is_read,
			is_acted,
			acted_at,
			acted_by_id,
			expires_at,
			created_at
	`, id, actedByID)

	item, err := scanInboxItem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return InboxItem{}, ErrNotFound
	}
	if err != nil {
		return InboxItem{}, mapDBError(err)
	}
	return item, nil
}

func (r *InboxItemRepo) ExpireDue(ctx context.Context, now time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE inbox_item
		SET
			is_acted = true,
			acted_at = COALESCE(acted_at, now())
		WHERE expires_at IS NOT NULL
		  AND expires_at <= $1
		  AND is_acted = false
	`, now.UTC())
	if err != nil {
		return 0, mapDBError(err)
	}
	return tag.RowsAffected(), nil
}

type MergeQueueEntryRepo struct {
	pool *pgxpool.Pool
}

func NewMergeQueueEntryRepo(pool *pgxpool.Pool) *MergeQueueEntryRepo {
	return &MergeQueueEntryRepo{pool: pool}
}

func (r *MergeQueueEntryRepo) Enqueue(ctx context.Context, entry MergeQueueEntry) (MergeQueueEntry, error) {
	if entry.EnqueuedAt.IsZero() {
		entry.EnqueuedAt = time.Now().UTC()
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, entry.ProjectID.String()); err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}

	var nextPosition int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(position), 0) + 1
		FROM merge_queue_entry
		WHERE project_id = $1
		  AND archived_at IS NULL
	`, entry.ProjectID).Scan(&nextPosition); err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO merge_queue_entry (
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, now()), $9, $10, $11, $12::jsonb)
		RETURNING
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
	`,
		entry.ProjectID,
		entry.TaskID,
		strings.TrimSpace(entry.BranchName),
		defaultTargetBranch(entry.TargetBranch),
		entry.CommitSHA,
		nextPosition,
		defaultMergeQueueStatus(entry.Status),
		entry.EnqueuedAt,
		entry.MergedAt,
		entry.ArchivedAt,
		entry.FailureReason,
		normalizeProjectTaskJSON(entry.Metadata, json.RawMessage(`{}`)),
	)

	enqueued, err := scanMergeQueueEntry(row)
	if err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}
	return enqueued, nil
}

func (r *MergeQueueEntryRepo) GetByTask(ctx context.Context, taskID uuid.UUID) (MergeQueueEntry, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
		FROM merge_queue_entry
		WHERE task_id = $1
	`, taskID)

	entry, err := scanMergeQueueEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MergeQueueEntry{}, ErrNotFound
	}
	if err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}
	return entry, nil
}

func (r *MergeQueueEntryRepo) GetByID(ctx context.Context, id uuid.UUID) (MergeQueueEntry, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
		FROM merge_queue_entry
		WHERE id = $1
	`, id)

	entry, err := scanMergeQueueEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MergeQueueEntry{}, ErrNotFound
	}
	if err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}
	return entry, nil
}

func (r *MergeQueueEntryRepo) ListByProject(ctx context.Context, projectID uuid.UUID) ([]MergeQueueEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
		FROM merge_queue_entry
		WHERE project_id = $1
		ORDER BY position ASC, id ASC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	entries := make([]MergeQueueEntry, 0)
	for rows.Next() {
		entry, scanErr := scanMergeQueueEntry(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return entries, nil
}

func (r *MergeQueueEntryRepo) ListActive(ctx context.Context, projectID uuid.UUID) ([]MergeQueueEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
		FROM merge_queue_entry
		WHERE project_id = $1
		  AND archived_at IS NULL
		ORDER BY position ASC, id ASC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	entries := make([]MergeQueueEntry, 0)
	for rows.Next() {
		entry, scanErr := scanMergeQueueEntry(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return entries, nil
}

func (r *MergeQueueEntryRepo) SetCommitSHA(ctx context.Context, id uuid.UUID, commitSHA string) (MergeQueueEntry, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE merge_queue_entry
		SET commit_sha = NULLIF($2, '')
		WHERE id = $1
		RETURNING
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
	`, id, strings.TrimSpace(commitSHA))

	updated, err := scanMergeQueueEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MergeQueueEntry{}, ErrNotFound
	}
	if err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MergeQueueEntryRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, failureReason *string, mergedAt *time.Time) (MergeQueueEntry, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE merge_queue_entry
		SET
			status = $2,
			failure_reason = $3,
			merged_at = $4
		WHERE id = $1
		RETURNING
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
	`, id, strings.TrimSpace(status), failureReason, mergedAt)

	updated, err := scanMergeQueueEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MergeQueueEntry{}, ErrNotFound
	}
	if err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MergeQueueEntryRepo) Archive(ctx context.Context, id uuid.UUID, archivedAt time.Time) (MergeQueueEntry, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE merge_queue_entry
		SET archived_at = $2
		WHERE id = $1
		RETURNING
			id,
			project_id,
			task_id,
			branch_name,
			target_branch,
			commit_sha,
			position,
			status,
			enqueued_at,
			merged_at,
			archived_at,
			failure_reason,
			metadata
	`, id, archivedAt.UTC())

	updated, err := scanMergeQueueEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MergeQueueEntry{}, ErrNotFound
	}
	if err != nil {
		return MergeQueueEntry{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MergeQueueEntryRepo) GetPosition(ctx context.Context, projectID, taskID uuid.UUID) (int, error) {
	var position int
	if err := r.pool.QueryRow(ctx, `
		SELECT position
		FROM merge_queue_entry
		WHERE project_id = $1
		  AND task_id = $2
		  AND archived_at IS NULL
		LIMIT 1
	`, projectID, taskID).Scan(&position); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, mapDBError(err)
	}
	return position, nil
}

func scanProjectTask(row pgx.Row) (ProjectTask, error) {
	var task ProjectTask
	if err := row.Scan(
		&task.ID,
		&task.OrganizationID,
		&task.ProjectID,
		&task.TaskNumber,
		&task.Title,
		&task.Description,
		&task.WorkStatus,
		&task.CurrentFlowNodeID,
		&task.FlowTemplateID,
		&task.ScheduleID,
		&task.BranchName,
		&task.RequiresHumanReview,
		&task.CreatedByType,
		&task.CreatedByID,
		&task.AssignedAgentID,
		&task.Metadata,
		&task.CreatedAt,
		&task.UpdatedAt,
		&task.CompletedAt,
	); err != nil {
		return ProjectTask{}, err
	}
	return task, nil
}

func scanProjectTaskEvent(row pgx.Row) (ProjectTaskEvent, error) {
	var event ProjectTaskEvent
	if err := row.Scan(
		&event.ID,
		&event.TaskID,
		&event.ProjectID,
		&event.EventType,
		&event.ActorType,
		&event.ActorID,
		&event.FlowNodeID,
		&event.Payload,
		&event.CreatedAt,
	); err != nil {
		return ProjectTaskEvent{}, err
	}
	return event, nil
}

func scanInboxItem(row pgx.Row) (InboxItem, error) {
	var item InboxItem
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.TargetUserID,
		&item.ItemType,
		&item.SourceProjectID,
		&item.SourceTaskID,
		&item.CreatedByType,
		&item.CreatedByID,
		&item.Title,
		&item.Body,
		&item.ActionPayload,
		&item.IsRead,
		&item.IsActed,
		&item.ActedAt,
		&item.ActedByID,
		&item.ExpiresAt,
		&item.CreatedAt,
	); err != nil {
		return InboxItem{}, err
	}
	return item, nil
}

func scanMergeQueueEntry(row pgx.Row) (MergeQueueEntry, error) {
	var entry MergeQueueEntry
	if err := row.Scan(
		&entry.ID,
		&entry.ProjectID,
		&entry.TaskID,
		&entry.BranchName,
		&entry.TargetBranch,
		&entry.CommitSHA,
		&entry.Position,
		&entry.Status,
		&entry.EnqueuedAt,
		&entry.MergedAt,
		&entry.ArchivedAt,
		&entry.FailureReason,
		&entry.Metadata,
	); err != nil {
		return MergeQueueEntry{}, err
	}
	return entry, nil
}

func defaultProjectTaskStatus(status string) string {
	trimmed := strings.TrimSpace(status)
	if trimmed == "" {
		return "draft"
	}
	return trimmed
}

func defaultMergeQueueStatus(status string) string {
	trimmed := strings.TrimSpace(status)
	if trimmed == "" {
		return "queued"
	}
	return trimmed
}

func defaultTargetBranch(branch string) string {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" {
		return "main"
	}
	return trimmed
}

func normalizeProjectTaskJSON(raw, fallback json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return fallback
	}
	return raw
}

func clampInboxLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultInboxLimit
	case limit > maxInboxLimit:
		return maxInboxLimit
	default:
		return limit
	}
}

func resolveInboxCursor(options InboxListOptions) (*time.Time, uuid.UUID) {
	cursorID := uuid.Nil
	if options.CursorID != nil {
		cursorID = *options.CursorID
	}
	return options.CursorAt, cursorID
}
