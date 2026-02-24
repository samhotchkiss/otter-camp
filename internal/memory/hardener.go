package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	MemoryCandidateReviewJobType = "memory_candidate_review"

	candidateHoldDuration = 7 * 24 * time.Hour
	hardeningDuration     = 30 * 24 * time.Hour
)

type candidateReviewPayload struct {
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
}

type candidateMemoryRepository interface {
	ListCandidates(ctx context.Context, orgID uuid.UUID, olderThan time.Time) ([]repo.Memory, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (repo.Memory, error)
	ListActiveUnhardened(ctx context.Context, orgID uuid.UUID, olderThan time.Time) ([]repo.Memory, error)
	UpdateHardened(ctx context.Context, id uuid.UUID, hardened bool) (repo.Memory, error)
	UpdateConfidence(ctx context.Context, id uuid.UUID, confidence float64) (repo.Memory, error)
}

type organizationRepository interface {
	List(ctx context.Context) ([]repo.Organization, error)
}

type memoryMentionRepository interface {
	ListByMemory(ctx context.Context, memoryID uuid.UUID) ([]repo.MemoryEntityMention, error)
}

type memoryEntityLookup interface {
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repo.MemoryEntity, error)
}

type HardenerOptions struct {
	Pool *pgxpool.Pool

	Memories candidateMemoryRepository
	Orgs     organizationRepository
	Mentions memoryMentionRepository
	Entities memoryEntityLookup
	Logger   *slog.Logger
	Now      func() time.Time
}

type Hardener struct {
	pool     *pgxpool.Pool
	memories candidateMemoryRepository
	orgs     organizationRepository
	mentions memoryMentionRepository
	entities memoryEntityLookup
	logger   *slog.Logger
	now      func() time.Time
}

func NewHardener(opts HardenerOptions) (*Hardener, error) {
	if opts.Pool == nil && (opts.Memories == nil || opts.Orgs == nil || opts.Mentions == nil || opts.Entities == nil) {
		return nil, fmt.Errorf("hardener requires database pool or explicit repositories")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	h := &Hardener{
		pool:     opts.Pool,
		memories: opts.Memories,
		orgs:     opts.Orgs,
		mentions: opts.Mentions,
		entities: opts.Entities,
		logger:   opts.Logger,
		now:      opts.Now,
	}
	if h.memories == nil {
		h.memories = repo.NewMemoryRepo(opts.Pool)
	}
	if h.orgs == nil {
		h.orgs = repo.NewOrgRepo(opts.Pool)
	}
	if h.mentions == nil {
		h.mentions = repo.NewMemoryEntityMentionRepo(opts.Pool)
	}
	if h.entities == nil {
		h.entities = repo.NewMemoryEntityRepo(opts.Pool)
	}
	return h, nil
}

func (h *Hardener) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if h == nil || registrar == nil {
		return
	}

	registrar.Register(MemoryCandidateReviewJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload candidateReviewPayload
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return fmt.Errorf("decode memory candidate review payload: %w", err)
			}
		}
		return h.RunCandidateReview(ctx, payload.OrganizationID)
	})
}

func (h *Hardener) RunCandidateReview(ctx context.Context, orgID *uuid.UUID) error {
	if h == nil || h.memories == nil || h.orgs == nil {
		return fmt.Errorf("hardener is not configured")
	}

	if orgID != nil && *orgID != uuid.Nil {
		return h.reviewOrganization(ctx, *orgID)
	}

	orgs, err := h.orgs.List(ctx)
	if err != nil {
		return err
	}
	for _, org := range orgs {
		if reviewErr := h.reviewOrganization(ctx, org.ID); reviewErr != nil {
			return reviewErr
		}
	}
	return nil
}

func (h *Hardener) reviewOrganization(ctx context.Context, orgID uuid.UUID) error {
	candidates, err := h.memories.ListCandidates(ctx, orgID, h.now().UTC().Add(-candidateHoldDuration))
	if err != nil {
		return err
	}

	for _, candidate := range candidates {
		if candidate.ExtractionScore == nil || *candidate.ExtractionScore < stage2MinimumScore {
			continue
		}
		contradiction, contradictionErr := h.hasContradiction(ctx, candidate)
		if contradictionErr != nil {
			return contradictionErr
		}
		if contradiction {
			continue
		}
		if _, promoteErr := h.memories.UpdateStatus(ctx, candidate.ID, "active"); promoteErr != nil {
			return promoteErr
		}
	}

	eligible, err := h.memories.ListActiveUnhardened(ctx, orgID, h.now().UTC().Add(-hardeningDuration))
	if err != nil {
		return err
	}
	if len(eligible) == 0 {
		return nil
	}

	entityLookup, err := h.loadEntityLookup(ctx, orgID)
	if err != nil {
		return err
	}

	for _, memoryItem := range eligible {
		positive, checkErr := h.hasPositiveFrictionSignal(ctx, memoryItem, entityLookup)
		if checkErr != nil {
			return checkErr
		}
		if !positive {
			continue
		}
		if _, confErr := h.memories.UpdateConfidence(ctx, memoryItem.ID, clampFloat(memoryItem.Confidence+0.05, 0, 1)); confErr != nil {
			return confErr
		}
		if _, hardenErr := h.memories.UpdateHardened(ctx, memoryItem.ID, true); hardenErr != nil {
			return hardenErr
		}
	}
	return nil
}

func (h *Hardener) loadEntityLookup(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]repo.MemoryEntity, error) {
	entities, err := h.entities.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}
	lookup := make(map[uuid.UUID]repo.MemoryEntity, len(entities))
	for _, entity := range entities {
		lookup[entity.ID] = entity
	}
	return lookup, nil
}

func (h *Hardener) hasPositiveFrictionSignal(ctx context.Context, memoryItem repo.Memory, entityLookup map[uuid.UUID]repo.MemoryEntity) (bool, error) {
	if h.pool == nil || h.mentions == nil {
		return false, nil
	}

	mentions, err := h.mentions.ListByMemory(ctx, memoryItem.ID)
	if err != nil {
		return false, err
	}
	if len(mentions) == 0 {
		return false, nil
	}

	if !h.chatReactionTablesExist(ctx) {
		return false, nil
	}

	names := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		entity, ok := entityLookup[mention.EntityID]
		if !ok {
			continue
		}
		name := normalizeWhitespace(entity.CanonicalName)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return false, nil
	}

	var (
		args       []any
		conditions []string
	)
	for idx, name := range names {
		args = append(args, "%"+name+"%")
		conditions = append(conditions, fmt.Sprintf("m.content ILIKE $%d", idx+1))
	}

	query := `
		SELECT COUNT(*)::integer
		FROM chat_message_reaction r
		JOIN chat_message m ON m.id = r.message_id
		WHERE lower(r.emoji) IN ('confirm', 'accurate')
		  AND (` + strings.Join(conditions, " OR ") + `)
	`
	var count int
	if err := h.pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, err
	}
	return count > 0, nil
}

func (h *Hardener) chatReactionTablesExist(ctx context.Context) bool {
	if h.pool == nil {
		return false
	}
	var exists bool
	err := h.pool.QueryRow(ctx, `
		SELECT to_regclass('chat_message_reaction') IS NOT NULL
		   AND to_regclass('chat_message') IS NOT NULL
	`).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

func (h *Hardener) hasContradiction(context.Context, repo.Memory) (bool, error) {
	// Contradiction detection is implemented by task 040.
	return false, nil
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
