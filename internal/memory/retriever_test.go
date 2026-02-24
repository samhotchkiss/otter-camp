package memory

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestBuildScopeFilterSQLIncludesProjectAndSensitivity(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	req := RetrievalRequest{
		OrganizationID:  orgID,
		ProjectID:       &projectID,
		SensitivityGate: true,
	}

	where, args, err := buildScopeFilterSQL(req, []string{"org", "project"})
	if err != nil {
		t.Fatalf("buildScopeFilterSQL: %v", err)
	}
	if !strings.Contains(where, "scope = 'org'") {
		t.Fatalf("where clause missing org scope: %s", where)
	}
	if !strings.Contains(where, "scope = 'project'") {
		t.Fatalf("where clause missing project scope: %s", where)
	}
	if !strings.Contains(where, "AND sensitivity = 'normal'") {
		t.Fatalf("where clause missing sensitivity gate: %s", where)
	}
	if len(args) != 2 {
		t.Fatalf("args len = %d, want 2", len(args))
	}
	if got := args[0].(uuid.UUID); got != orgID {
		t.Fatalf("org arg = %s, want %s", got, orgID)
	}
	if got := args[1].(uuid.UUID); got != projectID {
		t.Fatalf("project arg = %s, want %s", got, projectID)
	}
}

func TestBuildScopeFilterSQLAgentPrivateRequiresScopeAndAgentID(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	req := RetrievalRequest{
		OrganizationID: orgID,
		AgentID:        &agentID,
	}

	where, args, err := buildScopeFilterSQL(req, []string{"agent_private"})
	if err != nil {
		t.Fatalf("buildScopeFilterSQL: %v", err)
	}
	if !strings.Contains(where, "scope = 'agent_private'") {
		t.Fatalf("where clause missing agent_private scope guard: %s", where)
	}
	if !strings.Contains(where, "agent_id = $2") {
		t.Fatalf("where clause missing agent_id binding: %s", where)
	}
	if len(args) != 2 {
		t.Fatalf("args len = %d, want 2", len(args))
	}
	if got := args[1].(uuid.UUID); got != agentID {
		t.Fatalf("agent arg = %s, want %s", got, agentID)
	}
}

func TestRelevanceScoreFormula(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	createdAt := now.AddDate(0, 0, -30) // episodic half-life boundary => recency score 0.5

	got := relevanceScore(0.8, createdAt, "episodic", 0.6, now)
	want := (0.5 * 0.8) + (0.3 * 0.5) + (0.2 * 0.6)
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("relevance score = %.6f, want %.6f", got, want)
	}
}

func TestSupersessionResolveAndMaxHops(t *testing.T) {
	aID := uuid.New()
	bID := uuid.New()
	cID := uuid.New()

	store := &stubMemoryLookup{
		items: map[uuid.UUID]repo.Memory{
			aID: {ID: aID, SupersededBy: &bID},
			bID: {ID: bID, SupersededBy: &cID},
			cID: {ID: cID},
		},
	}
	chain, err := NewSupersessionChain(nil, store)
	if err != nil {
		t.Fatalf("NewSupersessionChain: %v", err)
	}
	resolved, err := chain.Resolve(context.Background(), aID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved == nil || resolved.ID != cID {
		t.Fatalf("resolved id = %v, want %s", resolved, cID)
	}

	deep := make(map[uuid.UUID]repo.Memory)
	first := uuid.New()
	current := first
	for i := 0; i < 11; i++ {
		next := uuid.New()
		if i == 10 {
			deep[current] = repo.Memory{ID: current}
			break
		}
		deep[current] = repo.Memory{ID: current, SupersededBy: &next}
		current = next
	}
	deepStore := &stubMemoryLookup{items: deep}
	deepChain, err := NewSupersessionChain(nil, deepStore)
	if err != nil {
		t.Fatalf("NewSupersessionChain deep: %v", err)
	}
	_, err = deepChain.Resolve(context.Background(), first)
	if !errors.Is(err, ErrSupersessionChainTooDeep) {
		t.Fatalf("Resolve deep err = %v, want ErrSupersessionChainTooDeep", err)
	}
}

func TestCooldownTrackingAcrossTurns(t *testing.T) {
	sessionID := uuid.New()
	memoryID := uuid.New()

	retriever := &Retriever{cooldown: newCooldownTracker()}
	ranked := []RankedMemory{{Memory: repo.Memory{ID: memoryID}, Score: 0.9}}

	for turn := 1; turn <= 6; turn++ {
		req := RetrievalRequest{
			Mode:             RetrievalModePassive,
			SessionID:        &sessionID,
			SessionTurnIndex: turn,
		}
		filtered := retriever.applyPassiveCooldown(req, ranked, turn)
		retriever.recordPassiveCooldown(req, filtered, turn)

		switch turn {
		case 1, 6:
			if len(filtered) != 1 {
				t.Fatalf("turn %d filtered len = %d, want 1", turn, len(filtered))
			}
		default:
			if len(filtered) != 0 {
				t.Fatalf("turn %d filtered len = %d, want 0", turn, len(filtered))
			}
		}
	}
}

type stubMemoryLookup struct {
	items map[uuid.UUID]repo.Memory
}

func (s *stubMemoryLookup) GetByID(_ context.Context, id uuid.UUID) (repo.Memory, error) {
	item, ok := s.items[id]
	if !ok {
		return repo.Memory{}, repo.ErrNotFound
	}
	return item, nil
}
