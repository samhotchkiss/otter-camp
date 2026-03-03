package compaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const MemoryTaskConsolidationJobType = "memory_task_consolidation"

type TaskConsolidationPayload struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	ProjectID      uuid.UUID `json:"project_id"`
	TaskID         uuid.UUID `json:"task_id"`
}

type TaskSummaryModel interface {
	SynthesizeTaskSummary(ctx context.Context, orgID, projectID, taskID uuid.UUID, episodic []repo.Memory) (string, error)
}

type EntityProfileSynthesizer interface {
	SynthesizeProfile(ctx context.Context, entityID uuid.UUID) (any, error)
}

type TaskConsolidatorOptions struct {
	Pool              *pgxpool.Pool
	Runs              *repo.MemoryCompactionRunRepo
	Memories          *repo.MemoryRepo
	SummaryModel      TaskSummaryModel
	EntitySynthesizer EntityProfileSynthesizer
	Now               func() time.Time
}

type TaskConsolidator struct {
	pool              *pgxpool.Pool
	runs              *repo.MemoryCompactionRunRepo
	memories          *repo.MemoryRepo
	summaryModel      TaskSummaryModel
	entitySynthesizer EntityProfileSynthesizer
	now               func() time.Time
}

func NewTaskConsolidator(opts TaskConsolidatorOptions) (*TaskConsolidator, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("task consolidator requires database pool")
	}
	if opts.SummaryModel == nil {
		return nil, fmt.Errorf("task consolidator summary model is required")
	}

	c := &TaskConsolidator{
		pool:              opts.Pool,
		runs:              opts.Runs,
		memories:          opts.Memories,
		summaryModel:      opts.SummaryModel,
		entitySynthesizer: opts.EntitySynthesizer,
		now:               opts.Now,
	}
	if c.runs == nil {
		c.runs = repo.NewMemoryCompactionRunRepo(opts.Pool)
	}
	if c.memories == nil {
		c.memories = repo.NewMemoryRepo(opts.Pool)
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

func (c *TaskConsolidator) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if c == nil || registrar == nil {
		return
	}
	registrar.Register(MemoryTaskConsolidationJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload TaskConsolidationPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode memory task consolidation payload: %w", err)
		}
		if payload.OrganizationID == uuid.Nil || payload.ProjectID == uuid.Nil || payload.TaskID == uuid.Nil {
			return fmt.Errorf("memory task consolidation payload missing required ids")
		}
		return c.Consolidate(ctx, payload.OrganizationID, payload.ProjectID, payload.TaskID)
	})
}

func (c *TaskConsolidator) Consolidate(ctx context.Context, orgID, projectID, taskID uuid.UUID) (err error) {
	if c == nil || c.pool == nil || c.runs == nil || c.memories == nil || c.summaryModel == nil {
		return fmt.Errorf("task consolidator is not configured")
	}
	if orgID == uuid.Nil || projectID == uuid.Nil || taskID == uuid.Nil {
		return fmt.Errorf("organization_id, project_id, and task_id are required")
	}
	if _, err := c.runs.FailStalePending(ctx, c.now().UTC().Add(-time.Hour), repo.MemoryCompactionPendingTimeoutReason); err != nil {
		return err
	}

	exists, err := c.completedRunExists(ctx, orgID, taskID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	scopeContext, _ := json.Marshal(map[string]any{"project_id": projectID, "task_id": taskID})
	run, err := c.runs.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: orgID,
		RunType:        "task_consolidation",
		ScopeContext:   string(scopeContext),
		Status:         "pending",
	})
	if err != nil {
		return err
	}

	startedAt := c.now().UTC()
	if _, err := c.runs.UpdateStatus(ctx, run.ID, "running", nil, &startedAt, nil); err != nil {
		return err
	}

	var (
		examined int
		updated  int
		archived int
		created  int
	)
	defer func() {
		if err != nil {
			message := strings.TrimSpace(err.Error())
			if message == "" {
				message = repo.MemoryCompactionDefaultFailedMessage
			}
			completedAt := c.now().UTC()
			_, _ = c.runs.UpdateStatus(ctx, run.ID, "failed", &message, &startedAt, &completedAt)
			return
		}
		_, _ = c.runs.UpdateCounts(ctx, run.ID, examined, updated, archived, created)
		completedAt := c.now().UTC()
		_, _ = c.runs.UpdateStatus(ctx, run.ID, "completed", nil, &startedAt, &completedAt)
	}()

	taskMemories, err := c.listActiveTaskMemories(ctx, orgID, taskID)
	if err != nil {
		return err
	}
	examined = len(taskMemories)

	episodicMemories := make([]repo.Memory, 0)
	for _, memoryItem := range taskMemories {
		action, actionErr := c.scopePromotionAction(ctx, memoryItem, taskID)
		if actionErr != nil {
			return actionErr
		}

		switch action {
		case ScopeActionArchive:
			archivedAt := c.now().UTC()
			if _, execErr := c.pool.Exec(ctx, `
				UPDATE memory
				SET status = 'archived',
				    archived_at = COALESCE(archived_at, $2)
				WHERE id = $1
			`, memoryItem.ID, archivedAt); execErr != nil {
				return execErr
			}
			archived++
		case ScopeActionPromoteProject:
			if _, execErr := c.pool.Exec(ctx, `
				UPDATE memory
				SET scope = 'project',
				    project_id = COALESCE(project_id, $2),
				    project_task_id = NULL
				WHERE id = $1
			`, memoryItem.ID, projectID); execErr != nil {
				return execErr
			}
			updated++
		}

		if memoryItem.MemoryType == "episodic" {
			episodicMemories = append(episodicMemories, memoryItem)
		}
	}

	if len(episodicMemories) > 0 {
		summaryText, synthErr := c.summaryModel.SynthesizeTaskSummary(ctx, orgID, projectID, taskID, episodicMemories)
		if synthErr != nil {
			return synthErr
		}
		summaryText = strings.TrimSpace(summaryText)
		if summaryText != "" {
			_, createErr := c.memories.Create(ctx, repo.Memory{
				OrganizationID: orgID,
				ProjectID:      &projectID,
				ProjectTaskID:  &taskID,
				MemoryType:     "execution_summary",
				Scope:          "project",
				Content:        summaryText,
				ContentHash:    hashContent(summaryText),
				Confidence:     0.8,
				UtilityScore:   0.8,
				Status:         "active",
				TrustTier:      0.8,
			})
			if createErr != nil {
				return createErr
			}
			created++
		}
	}

	if c.entitySynthesizer != nil {
		entityIDs, listErr := c.listTaskEntitiesAtLeastFiveMentions(ctx, orgID, taskID)
		if listErr != nil {
			return listErr
		}
		for _, entityID := range entityIDs {
			if _, synthErr := c.entitySynthesizer.SynthesizeProfile(ctx, entityID); synthErr != nil {
				return synthErr
			}
		}
	}

	return nil
}

const (
	ScopeActionArchive        = "archive"
	ScopeActionPromoteProject = "promote_project"
)

func ScopePromotionAction(memoryType string, entityTaskCount int) string {
	switch strings.TrimSpace(memoryType) {
	case "episodic":
		return ScopeActionArchive
	case "semantic", "procedural", "preference":
		return ScopeActionPromoteProject
	case "entity_profile":
		if entityTaskCount >= 3 {
			return ScopeActionPromoteProject
		}
		return ScopeActionArchive
	default:
		return ScopeActionArchive
	}
}

func (c *TaskConsolidator) scopePromotionAction(ctx context.Context, memoryItem repo.Memory, taskID uuid.UUID) (string, error) {
	if memoryItem.MemoryType != "entity_profile" {
		return ScopePromotionAction(memoryItem.MemoryType, 0), nil
	}

	entityID, found, err := c.lookupEntityForProfileMemory(ctx, memoryItem.ID)
	if err != nil {
		return "", err
	}
	if !found {
		return ScopeActionArchive, nil
	}
	count, err := c.countOtherTasksForEntity(ctx, entityID, taskID)
	if err != nil {
		return "", err
	}
	return ScopePromotionAction(memoryItem.MemoryType, count), nil
}

func (c *TaskConsolidator) completedRunExists(ctx context.Context, orgID, taskID uuid.UUID) (bool, error) {
	var exists bool
	err := c.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM memory_compaction_run
			WHERE organization_id = $1
			  AND run_type = 'task_consolidation'
			  AND status = 'completed'
			  AND scope_context->>'task_id' = $2
		)
	`, orgID, taskID.String()).Scan(&exists)
	return exists, err
}

func (c *TaskConsolidator) listActiveTaskMemories(ctx context.Context, orgID, taskID uuid.UUID) ([]repo.Memory, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       confidence::float8, utility_score::float8, status, created_at, updated_at
		FROM memory
		WHERE organization_id = $1
		  AND project_task_id = $2
		  AND scope = 'task'
		  AND status = 'active'
		ORDER BY created_at ASC
	`, orgID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]repo.Memory, 0)
	for rows.Next() {
		var item repo.Memory
		if err := rows.Scan(
			&item.ID,
			&item.OrganizationID,
			&item.ProjectID,
			&item.ProjectTaskID,
			&item.AgentID,
			&item.MemoryType,
			&item.Scope,
			&item.Content,
			&item.ContentHash,
			&item.Confidence,
			&item.UtilityScore,
			&item.Status,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (c *TaskConsolidator) listTaskEntitiesAtLeastFiveMentions(ctx context.Context, orgID, taskID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT mem.entity_id
		FROM memory_entity_mention mem
		JOIN memory m ON m.id = mem.memory_id
		WHERE m.organization_id = $1
		  AND m.project_task_id = $2
		GROUP BY mem.entity_id
		HAVING COUNT(*) >= 5
	`, orgID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entityIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var entityID uuid.UUID
		if err := rows.Scan(&entityID); err != nil {
			return nil, err
		}
		entityIDs = append(entityIDs, entityID)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return entityIDs, nil
}

func (c *TaskConsolidator) lookupEntityForProfileMemory(ctx context.Context, memoryID uuid.UUID) (uuid.UUID, bool, error) {
	var entityID uuid.UUID
	err := c.pool.QueryRow(ctx, `
		SELECT id
		FROM memory_entity
		WHERE synthesis_memory_id = $1
		LIMIT 1
	`, memoryID).Scan(&entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return entityID, true, nil
}

func (c *TaskConsolidator) countOtherTasksForEntity(ctx context.Context, entityID, taskID uuid.UUID) (int, error) {
	var count int
	err := c.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT m.project_task_id)::integer
		FROM memory_entity_mention mem
		JOIN memory m ON m.id = mem.memory_id
		WHERE mem.entity_id = $1
		  AND m.project_task_id IS NOT NULL
		  AND m.project_task_id <> $2
	`, entityID, taskID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}
