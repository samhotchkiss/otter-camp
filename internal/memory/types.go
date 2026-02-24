package memory

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var ErrSupersessionChainTooDeep = errors.New("supersession chain exceeded max hops")

var ErrFileNotFound = errors.New("file not found")

type RetrievalMode string

const (
	RetrievalModePassive    RetrievalMode = "passive"
	RetrievalModeMention    RetrievalMode = "mention"
	RetrievalModeAgentQuery RetrievalMode = "agent_query"
)

type RetrievalRequest struct {
	OrganizationID   uuid.UUID
	ProjectID        *uuid.UUID
	AgentID          *uuid.UUID
	TaskID           *uuid.UUID
	Query            string
	Mode             RetrievalMode
	MaxResults       int
	SensitivityGate  bool
	SessionID        *uuid.UUID
	SessionTurnIndex int
}

type RetrievalResult struct {
	Memories       []RankedMemory
	FallbackUsed   bool
	EntityProfiles []EntityProfile
}

type RankedMemory struct {
	Memory    repo.Memory
	Score     float64
	CosineSim float64
}

type EntityProfile struct {
	EntityID uuid.UUID
	Memory   repo.Memory
}

type DedupPair struct {
	MemoryA          repo.Memory
	MemoryB          repo.Memory
	CosineSimilarity float64
}

type DedupDecision string

const (
	DedupDecisionKeepBoth   DedupDecision = "keep_both"
	DedupDecisionMerge      DedupDecision = "merge"
	DedupDecisionSupersedeA DedupDecision = "supersede_a"
	DedupDecisionSupersedeB DedupDecision = "supersede_b"
)

type DedupReview struct {
	Pair     DedupPair
	Decision DedupDecision
}

type ContradictionLabel string

const (
	ContradictionLabelContradictory ContradictionLabel = "contradictory"
	ContradictionLabelRedundant     ContradictionLabel = "redundant"
	ContradictionLabelIndependent   ContradictionLabel = "independent"
)

type TaxonomyClassifier interface {
	ClassifyTaxonomy(ctx context.Context, orgID uuid.UUID, query string, nodes []repo.MemoryTaxonomyNode) ([]uuid.UUID, error)
}

type QueryEmbedder interface {
	Embed(ctx context.Context, orgID uuid.UUID, invocationPurpose string, inputs []string) ([][]float32, error)
}

type SynthesisModel interface {
	SynthesizeEntityProfile(ctx context.Context, orgID uuid.UUID, entity repo.MemoryEntity, memories []repo.Memory) (string, error)
}

type DedupReviewer interface {
	ReviewDedup(ctx context.Context, orgID uuid.UUID, pairs []DedupPair) ([]DedupReview, error)
}

type DedupMerger interface {
	Merge(ctx context.Context, orgID uuid.UUID, memoryA, memoryB repo.Memory) (repo.Memory, error)
}

type ContradictionClassifier interface {
	ClassifyContradictions(ctx context.Context, orgID uuid.UUID, newMemory repo.Memory, candidates []repo.Memory) (map[uuid.UUID]ContradictionLabel, error)
}

type FileReader interface {
	ReadFile(ctx context.Context, projectID uuid.UUID, path string) ([]byte, error)
}

type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time {
	return time.Now().UTC()
}
