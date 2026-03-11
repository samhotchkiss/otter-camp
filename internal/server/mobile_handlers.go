package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"golang.org/x/sync/errgroup"
)

const (
	defaultDashboardInboxLimit = 10
	maxDashboardInboxLimit     = 50
	defaultRecentSessionLimit  = 5
)

type mobileHandlers struct {
	pool *pgxpool.Pool
}

type mobileDashboardInboxResponse struct {
	UnreadCount int                        `json:"unread_count"`
	Items       []mobileDashboardInboxItem `json:"items"`
}

type mobileDashboardInboxItem struct {
	ID              uuid.UUID       `json:"id"`
	ItemType        string          `json:"item_type"`
	Title           string          `json:"title"`
	Body            *string         `json:"body,omitempty"`
	IsRead          bool            `json:"is_read"`
	SourceProjectID *uuid.UUID      `json:"source_project_id,omitempty"`
	SourceTaskID    *uuid.UUID      `json:"source_task_id,omitempty"`
	ActionPayload   json.RawMessage `json:"action_payload"`
	CreatedAt       time.Time       `json:"created_at"`
}

type mobileDashboardProjectResponse struct {
	ID               uuid.UUID                           `json:"id"`
	Name             string                              `json:"name"`
	Slug             string                              `json:"slug"`
	OperationalState string                              `json:"operational_state"`
	StateSummary     string                              `json:"state_summary,omitempty"`
	BlockingTasks    []projectOperationalBlockerResponse `json:"blocking_tasks,omitempty"`
	ActiveTaskCount  int                                 `json:"active_task_count"`
	BlockedTaskCount int                                 `json:"blocked_task_count"`
	LatestDeploy     *mobileDashboardLatestDeployState   `json:"latest_deploy,omitempty"`
}

type mobileDashboardLatestDeployState struct {
	DeployedAt time.Time `json:"deployed_at"`
	CommitSHA  string    `json:"commit_sha"`
	Status     string    `json:"status"`
}

type mobileDashboardSessionResponse struct {
	ID                 uuid.UUID  `json:"id"`
	ScopeType          string     `json:"scope_type"`
	ScopeID            uuid.UUID  `json:"scope_id"`
	Label              string     `json:"label,omitempty"`
	ProjectID          *uuid.UUID `json:"project_id,omitempty"`
	ProjectName        string     `json:"project_name,omitempty"`
	ProjectSlug        string     `json:"project_slug,omitempty"`
	TaskTitle          string     `json:"task_title,omitempty"`
	LastMessageAt      *time.Time `json:"last_message_at,omitempty"`
	UnreadMessageCount int        `json:"unread_message_count"`
}

func newMobileHandlers(pool *pgxpool.Pool) mobileHandlers {
	return mobileHandlers{pool: pool}
}

func (h mobileHandlers) dashboard(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "mobile dashboard unavailable")
		return
	}

	projectIDs, err := parseDashboardProjectIDs(r.URL.Query().Get("project_ids"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project_ids")
		return
	}
	inboxLimit, err := parseDashboardInboxLimit(r.URL.Query().Get("inbox_limit"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid inbox_limit")
		return
	}

	var (
		inbox    mobileDashboardInboxResponse
		projects []mobileDashboardProjectResponse
		sessions []mobileDashboardSessionResponse
	)

	if err := runDashboardQueriesParallel(r.Context(),
		func(ctx context.Context) error {
			value, loadErr := h.loadDashboardInbox(ctx, principal.UserID, inboxLimit)
			if loadErr != nil {
				return loadErr
			}
			inbox = value
			return nil
		},
		func(ctx context.Context) error {
			value, loadErr := h.loadDashboardProjects(ctx, principal.OrganizationID, projectIDs)
			if loadErr != nil {
				return loadErr
			}
			projects = value
			return nil
		},
		func(ctx context.Context) error {
			value, loadErr := h.loadRecentSessions(ctx, principal.OrganizationID, principal.UserID)
			if loadErr != nil {
				return loadErr
			}
			sessions = value
			return nil
		},
	); err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load mobile dashboard")
		return
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"inbox":           inbox,
		"projects":        projects,
		"recent_sessions": sessions,
		"server_time":     time.Now().UTC().Format(time.RFC3339),
	})
}

func runDashboardQueriesParallel(ctx context.Context, queries ...func(context.Context) error) error {
	group, groupCtx := errgroup.WithContext(ctx)
	for _, query := range queries {
		if query == nil {
			continue
		}
		q := query
		group.Go(func() error {
			return q(groupCtx)
		})
	}
	return group.Wait()
}

func (h mobileHandlers) loadDashboardInbox(ctx context.Context, userID uuid.UUID, limit int) (mobileDashboardInboxResponse, error) {
	var unreadCount int
	if err := h.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM inbox_item
		WHERE target_user_id = $1
		  AND is_acted = false
		  AND is_read = false
	`, userID).Scan(&unreadCount); err != nil {
		return mobileDashboardInboxResponse{}, err
	}

	// The current schema does not include urgency/actioned_at; recency ordering is used for startup summaries.
	rows, err := h.pool.Query(ctx, `
		SELECT id, item_type, title, body, is_read, source_project_id, source_task_id, action_payload, created_at
		FROM inbox_item
		WHERE target_user_id = $1
		  AND is_acted = false
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return mobileDashboardInboxResponse{}, err
	}
	defer rows.Close()

	items := make([]mobileDashboardInboxItem, 0, limit)
	for rows.Next() {
		var item mobileDashboardInboxItem
		if scanErr := rows.Scan(
			&item.ID,
			&item.ItemType,
			&item.Title,
			&item.Body,
			&item.IsRead,
			&item.SourceProjectID,
			&item.SourceTaskID,
			&item.ActionPayload,
			&item.CreatedAt,
		); scanErr != nil {
			return mobileDashboardInboxResponse{}, scanErr
		}
		if len(item.ActionPayload) == 0 {
			item.ActionPayload = json.RawMessage(`{}`)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return mobileDashboardInboxResponse{}, rows.Err()
	}

	return mobileDashboardInboxResponse{
		UnreadCount: unreadCount,
		Items:       items,
	}, nil
}

func (h mobileHandlers) loadDashboardProjects(ctx context.Context, organizationID uuid.UUID, projectIDs []uuid.UUID) ([]mobileDashboardProjectResponse, error) {
	if len(projectIDs) == 0 {
		return []mobileDashboardProjectResponse{}, nil
	}

	rows, err := h.pool.Query(ctx, `
		SELECT
			p.id,
			p.display_name,
			p.slug,
			p.status,
			COALESCE((p.settings->'pause'->>'is_paused')::boolean, false) AS is_paused,
			COALESCE(p.settings->'pause'->>'reason', '') AS pause_reason,
			COALESCE(SUM(CASE WHEN t.work_status IN ('queued', 'in_progress', 'review', 'on_hold') THEN 1 ELSE 0 END), 0) AS active_task_count,
			COALESCE(SUM(CASE WHEN t.work_status = 'blocked' THEN 1 ELSE 0 END), 0) AS blocked_task_count
		FROM project p
		LEFT JOIN project_task t ON t.project_id = p.id
		WHERE p.organization_id = $1
		  AND p.id = ANY($2::uuid[])
		GROUP BY
			p.id,
			p.display_name,
			p.slug,
			p.status,
			COALESCE((p.settings->'pause'->>'is_paused')::boolean, false),
			COALESCE(p.settings->'pause'->>'reason', '')
	`, organizationID, projectIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := make(map[uuid.UUID]mobileDashboardProjectResponse, len(projectIDs))
	for rows.Next() {
		var (
			item        mobileDashboardProjectResponse
			status      string
			isPaused    bool
			pauseReason string
		)
		if scanErr := rows.Scan(&item.ID, &item.Name, &item.Slug, &status, &isPaused, &pauseReason, &item.ActiveTaskCount, &item.BlockedTaskCount); scanErr != nil {
			return nil, scanErr
		}
		item.OperationalState = "active"
		if strings.TrimSpace(status) != "" && !strings.EqualFold(strings.TrimSpace(status), "active") {
			item.OperationalState = strings.TrimSpace(status)
		}
		if isPaused {
			item.OperationalState = "paused"
			item.StateSummary = "project paused"
			if trimmed := strings.TrimSpace(pauseReason); trimmed != "" {
				item.StateSummary = "project paused: " + trimmed
			}
		}
		byID[item.ID] = item
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	stalls, err := projectStallDetector{pool: h.pool}.LoadTerminalProjectStalls(ctx, organizationID, projectIDs, 0)
	if err != nil {
		return nil, err
	}
	for _, stall := range stalls {
		record, ok := byID[stall.ProjectID]
		if !ok {
			continue
		}
		record.OperationalState = projectOperationalStateTerminalStalled
		record.StateSummary = stall.Summary
		record.BlockingTasks = stall.BlockingTasks
		byID[stall.ProjectID] = record
	}

	deployRows, err := h.pool.Query(ctx, `
		SELECT DISTINCT ON (project_id) project_id, last_deployed_at, last_deployed_commit
		FROM project_environment
		WHERE project_id = ANY($1::uuid[])
		  AND last_deployed_at IS NOT NULL
		ORDER BY project_id, last_deployed_at DESC
	`, projectIDs)
	if err != nil {
		return nil, err
	}
	defer deployRows.Close()

	for deployRows.Next() {
		var (
			projectID  uuid.UUID
			deployedAt time.Time
			commitSHA  *string
		)
		if scanErr := deployRows.Scan(&projectID, &deployedAt, &commitSHA); scanErr != nil {
			return nil, scanErr
		}
		record, ok := byID[projectID]
		if !ok {
			continue
		}
		record.LatestDeploy = &mobileDashboardLatestDeployState{
			DeployedAt: deployedAt.UTC(),
			CommitSHA:  strings.TrimSpace(valueOrEmpty(commitSHA)),
			Status:     "deployed",
		}
		byID[projectID] = record
	}
	if deployRows.Err() != nil {
		return nil, deployRows.Err()
	}

	ordered := make([]mobileDashboardProjectResponse, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		record, ok := byID[projectID]
		if !ok {
			continue
		}
		ordered = append(ordered, record)
	}
	return ordered, nil
}

func (h mobileHandlers) loadRecentSessions(ctx context.Context, organizationID, userID uuid.UUID) ([]mobileDashboardSessionResponse, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT
			s.id,
			s.scope_type,
			s.scope_id,
			COALESCE(task_project.id, project_scope.id) AS project_id,
			COALESCE(task_project.display_name, project_scope.display_name, '') AS project_name,
			COALESCE(task_project.slug, project_scope.slug, '') AS project_slug,
			COALESCE(task_scope.title, '') AS task_title,
			s.last_message_at,
			GREATEST(
				COALESCE(MAX(m.sequence_number), 0) - COALESCE(rc.last_read_sequence, 0),
				0
			) AS unread_message_count
		FROM chat_session s
		LEFT JOIN project project_scope
			ON s.scope_type = 'project'
		   AND project_scope.id = s.scope_id
		LEFT JOIN project_task task_scope
			ON s.scope_type = 'project_task'
		   AND task_scope.id = s.scope_id
		LEFT JOIN project task_project
			ON task_scope.project_id = task_project.id
		LEFT JOIN chat_message m ON m.session_id = s.id
		LEFT JOIN chat_read_cursor rc ON rc.session_id = s.id AND rc.user_id = $2
		WHERE s.organization_id = $1
		  AND s.status = 'active'
		  AND (
			(s.scope_type = 'project' AND EXISTS (
				SELECT 1 FROM project p WHERE p.id = s.scope_id AND p.organization_id = $1
			))
			OR
			(s.scope_type = 'project_task' AND EXISTS (
				SELECT 1 FROM project_task t WHERE t.id = s.scope_id AND t.organization_id = $1
			))
		  )
		GROUP BY
			s.id, s.scope_type, s.scope_id, s.last_message_at, s.created_at, rc.last_read_sequence,
			project_scope.id, project_scope.display_name, project_scope.slug,
			task_scope.title, task_project.id, task_project.display_name, task_project.slug
		ORDER BY COALESCE(s.last_message_at, s.created_at) DESC, s.created_at DESC
		LIMIT $3
	`, organizationID, userID, defaultRecentSessionLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]mobileDashboardSessionResponse, 0, defaultRecentSessionLimit)
	for rows.Next() {
		var (
			item        mobileDashboardSessionResponse
			unreadCount int64
		)
		if scanErr := rows.Scan(
			&item.ID,
			&item.ScopeType,
			&item.ScopeID,
			&item.ProjectID,
			&item.ProjectName,
			&item.ProjectSlug,
			&item.TaskTitle,
			&item.LastMessageAt,
			&unreadCount,
		); scanErr != nil {
			return nil, scanErr
		}
		item.Label = resolveMobileDashboardSessionLabel(item)
		item.UnreadMessageCount = int(unreadCount)
		sessions = append(sessions, item)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return sessions, nil
}

func resolveMobileDashboardSessionLabel(item mobileDashboardSessionResponse) string {
	if strings.EqualFold(strings.TrimSpace(item.ScopeType), "project_task") {
		if label := strings.TrimSpace(item.TaskTitle); label != "" {
			return label
		}
	}
	if label := strings.TrimSpace(item.ProjectName); label != "" {
		return label
	}
	if label := strings.TrimSpace(item.ProjectSlug); label != "" {
		return label
	}
	return ""
}

func parseDashboardProjectIDs(raw string) ([]uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []uuid.UUID{}, nil
	}

	parts := strings.Split(raw, ",")
	seen := make(map[uuid.UUID]struct{}, len(parts))
	ids := make([]uuid.UUID, 0, len(parts))
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		id, err := uuid.Parse(candidate)
		if err != nil || id == uuid.Nil {
			return nil, errors.New("invalid project id")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseDashboardInboxLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultDashboardInboxLimit, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, errors.New("limit must be positive")
	}
	if limit > maxDashboardInboxLimit {
		limit = maxDashboardInboxLimit
	}
	return limit, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
