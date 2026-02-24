package memory

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const maxSupersessionHops = 10

type memoryLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Memory, error)
}

type SupersessionChain struct {
	memories memoryLookup
}

func NewSupersessionChain(pool *pgxpool.Pool, memories memoryLookup) (*SupersessionChain, error) {
	if memories == nil {
		if pool == nil {
			return nil, fmt.Errorf("supersession chain requires pool or memory repository")
		}
		memories = repo.NewMemoryRepo(pool)
	}
	return &SupersessionChain{memories: memories}, nil
}

func (s *SupersessionChain) Resolve(ctx context.Context, memoryID uuid.UUID) (*repo.Memory, error) {
	chain, err := s.GetSupersessionChain(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, nil
	}
	current := chain[len(chain)-1]
	return &current, nil
}

func (s *SupersessionChain) GetSupersessionChain(ctx context.Context, memoryID uuid.UUID) ([]repo.Memory, error) {
	if memoryID == uuid.Nil {
		return nil, fmt.Errorf("memory id is required")
	}

	chain := make([]repo.Memory, 0, 4)
	visited := make(map[uuid.UUID]struct{}, 4)
	currentID := memoryID

	for hops := 0; ; hops++ {
		if hops >= maxSupersessionHops {
			return nil, ErrSupersessionChainTooDeep
		}

		current, err := s.memories.GetByID(ctx, currentID)
		if err != nil {
			return nil, err
		}
		chain = append(chain, current)

		if current.SupersededBy == nil || *current.SupersededBy == uuid.Nil {
			return chain, nil
		}
		if _, seen := visited[current.ID]; seen {
			return nil, ErrSupersessionChainTooDeep
		}
		visited[current.ID] = struct{}{}
		currentID = *current.SupersededBy
	}
}
