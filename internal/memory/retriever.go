package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	retrievalInvocationPurpose = "memory_retrieval"
	stage1CandidateLimit       = 500
	defaultMaxResults          = 20
	maxTaxonomyClassifications = 3
)

type agentLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type taxonomyNodeLookup interface {
	ListByOrganization(ctx context.Context, organizationID uuid.UUID) ([]repo.MemoryTaxonomyNode, error)
	ListSubtree(ctx context.Context, rootID uuid.UUID) ([]repo.MemoryTaxonomyNode, error)
}

type RetrieverOptions struct {
	Pool          *pgxpool.Pool
	Agents        agentLookup
	TaxonomyNodes taxonomyNodeLookup
	Embedder      QueryEmbedder
	Classifier    TaxonomyClassifier
	Clock         Clock
}

type Retriever struct {
	pool       *pgxpool.Pool
	agents     agentLookup
	taxonomy   taxonomyNodeLookup
	embedder   QueryEmbedder
	classifier TaxonomyClassifier
	clock      Clock
	cooldown   *cooldownTracker
}

func NewRetriever(opts RetrieverOptions) (*Retriever, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("retriever pool is required")
	}
	if opts.Agents == nil {
		opts.Agents = repo.NewAgentRepo(opts.Pool)
	}
	if opts.TaxonomyNodes == nil {
		opts.TaxonomyNodes = repo.NewMemoryTaxonomyNodeRepo(opts.Pool)
	}
	if opts.Clock == nil {
		opts.Clock = wallClock{}
	}

	return &Retriever{
		pool:       opts.Pool,
		agents:     opts.Agents,
		taxonomy:   opts.TaxonomyNodes,
		embedder:   opts.Embedder,
		classifier: opts.Classifier,
		clock:      opts.Clock,
		cooldown:   newCooldownTracker(),
	}, nil
}

func (r *Retriever) Query(ctx context.Context, req RetrievalRequest) (RetrievalResult, error) {
	if req.OrganizationID == uuid.Nil {
		return RetrievalResult{}, fmt.Errorf("organization id is required")
	}
	if strings.TrimSpace(req.Query) == "" {
		return RetrievalResult{}, fmt.Errorf("query is required")
	}
	if r.embedder == nil {
		return RetrievalResult{}, fmt.Errorf("retriever embedder is required")
	}

	stage1, err := r.stage1ScopeFilter(ctx, req)
	if err != nil {
		return RetrievalResult{}, err
	}
	if len(stage1) == 0 {
		return RetrievalResult{}, nil
	}

	nodes, err := r.taxonomy.ListByOrganization(ctx, req.OrganizationID)
	if err != nil {
		return RetrievalResult{}, err
	}

	classified, err := r.classify(ctx, req, nodes)
	if err != nil {
		return RetrievalResult{}, err
	}

	filtered := stage1
	fallbackUsed := false
	taxonomyApplied := false
	if len(classified) > 0 {
		filtered, fallbackUsed, err = r.stage3SubtreeFilter(ctx, stage1, classified)
		if err != nil {
			return RetrievalResult{}, err
		}
		taxonomyApplied = !fallbackUsed
	}

	queryEmbedding, err := r.queryEmbedding(ctx, req)
	if err != nil {
		return RetrievalResult{}, err
	}

	ranked := rankCandidates(queryEmbedding, filtered, r.clock.Now())
	if len(ranked) > 0 && ranked[0].Score < 0.35 && taxonomyApplied {
		ranked = rankCandidates(queryEmbedding, stage1, r.clock.Now())
		fallbackUsed = true
	}

	turnIndex := 0
	if req.Mode == RetrievalModePassive && req.SessionID != nil && *req.SessionID != uuid.Nil {
		turnIndex = r.cooldown.turn(*req.SessionID, req.SessionTurnIndex)
	}
	ranked = r.applyPassiveCooldown(req, ranked, turnIndex)
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	if maxResults > len(ranked) {
		maxResults = len(ranked)
	}
	selected := append([]RankedMemory(nil), ranked[:maxResults]...)
	// Injection ordering is least-relevant first so the strongest memory appears nearest the turn boundary.
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Score < selected[j].Score
	})

	r.recordPassiveCooldown(req, selected, turnIndex)
	profiles, err := r.loadEntityProfiles(ctx, selected)
	if err != nil {
		return RetrievalResult{}, err
	}

	return RetrievalResult{
		Memories:       selected,
		FallbackUsed:   fallbackUsed,
		EntityProfiles: profiles,
	}, nil
}

func (r *Retriever) stage1ScopeFilter(ctx context.Context, req RetrievalRequest) ([]repo.Memory, error) {
	scopes, err := r.resolveReadScopes(ctx, req)
	if err != nil {
		return nil, err
	}
	where, args, err := buildScopeFilterSQL(req, scopes)
	if err != nil {
		return nil, err
	}
	query := `
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		       file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
		FROM memory
		WHERE ` + where + `
		ORDER BY created_at DESC
		LIMIT ` + fmt.Sprintf("%d", stage1CandidateLimit)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMemoryRows(rows)
}

func (r *Retriever) resolveReadScopes(ctx context.Context, req RetrievalRequest) ([]string, error) {
	if req.AgentID == nil || *req.AgentID == uuid.Nil {
		return []string{"org"}, nil
	}

	agent, err := r.agents.GetByID(ctx, *req.AgentID)
	if err != nil {
		return nil, err
	}
	if agent.OrganizationID != req.OrganizationID {
		return nil, fmt.Errorf("agent does not belong to organization")
	}

	scopes := make([]string, 0, len(agent.MemoryReadScopes))
	for _, scope := range agent.MemoryReadScopes {
		normalized := strings.TrimSpace(strings.ToLower(scope))
		if normalized == "" {
			continue
		}
		scopes = append(scopes, normalized)
	}
	if len(scopes) == 0 {
		return []string{"org"}, nil
	}
	return scopes, nil
}

func buildScopeFilterSQL(req RetrievalRequest, readScopes []string) (string, []any, error) {
	if req.OrganizationID == uuid.Nil {
		return "", nil, fmt.Errorf("organization id is required")
	}
	if len(readScopes) == 0 {
		readScopes = []string{"org"}
	}

	args := []any{req.OrganizationID}
	nextArg := 2
	scopeClauses := make([]string, 0, len(readScopes))
	seen := make(map[string]struct{}, len(readScopes))

	for _, raw := range readScopes {
		scope := strings.TrimSpace(strings.ToLower(raw))
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}

		switch scope {
		case "org":
			scopeClauses = append(scopeClauses, "scope = 'org'")
		case "project":
			if req.ProjectID == nil || *req.ProjectID == uuid.Nil {
				continue
			}
			scopeClauses = append(scopeClauses, fmt.Sprintf("(scope = 'project' AND project_id = $%d)", nextArg))
			args = append(args, *req.ProjectID)
			nextArg++
		case "task":
			if req.TaskID == nil || *req.TaskID == uuid.Nil {
				continue
			}
			scopeClauses = append(scopeClauses, fmt.Sprintf("(scope = 'task' AND project_task_id = $%d)", nextArg))
			args = append(args, *req.TaskID)
			nextArg++
		case "agent_private":
			if req.AgentID == nil || *req.AgentID == uuid.Nil {
				continue
			}
			scopeClauses = append(scopeClauses, fmt.Sprintf("(agent_id = $%d)", nextArg))
			args = append(args, *req.AgentID)
			nextArg++
		}
	}

	if len(scopeClauses) == 0 {
		return "organization_id = $1 AND status = 'active' AND false", args, nil
	}

	where := "organization_id = $1 AND status = 'active' AND (" + strings.Join(scopeClauses, " OR ") + ")"
	if req.SensitivityGate {
		where += " AND sensitivity = 'normal'"
	}
	return where, args, nil
}

func (r *Retriever) classify(ctx context.Context, req RetrievalRequest, nodes []repo.MemoryTaxonomyNode) ([]uuid.UUID, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	if req.Mode == RetrievalModeMention || req.Mode == RetrievalModeAgentQuery {
		if r.classifier != nil {
			classified, err := r.classifier.ClassifyTaxonomy(ctx, req.OrganizationID, req.Query, nodes)
			if err == nil && len(classified) > 0 {
				return dedupeUUIDs(classified), nil
			}
		}
	}
	return classifyTaxonomyRuleBased(req.Query, nodes), nil
}

func classifyTaxonomyRuleBased(query string, nodes []repo.MemoryTaxonomyNode) []uuid.UUID {
	normalized := strings.ToLower(strings.TrimSpace(query))
	if normalized == "" || len(nodes) == 0 {
		return nil
	}

	type scoredNode struct {
		id    uuid.UUID
		slug  string
		score int
	}
	scored := make([]scoredNode, 0, len(nodes))
	for _, node := range nodes {
		nodeTokens := tokenize(node.Slug + " " + node.DisplayName)
		if len(nodeTokens) == 0 {
			continue
		}
		score := 0
		for _, token := range nodeTokens {
			if strings.Contains(normalized, token) {
				score++
			}
		}
		if strings.Contains(normalized, strings.ToLower(strings.TrimSpace(node.DisplayName))) {
			score += 2
		}
		if score > 0 {
			scored = append(scored, scoredNode{id: node.ID, slug: node.Slug, score: score})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].slug < scored[j].slug
		}
		return scored[i].score > scored[j].score
	})

	limit := maxTaxonomyClassifications
	if len(scored) < limit {
		limit = len(scored)
	}
	out := make([]uuid.UUID, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, scored[i].id)
	}
	return out
}

func tokenize(input string) []string {
	normalized := strings.ToLower(strings.TrimSpace(input))
	normalized = strings.ReplaceAll(normalized, ".", " ")
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.ReplaceAll(normalized, "_", " ")
	parts := strings.Fields(normalized)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		out = append(out, part)
	}
	return out
}

func (r *Retriever) stage3SubtreeFilter(ctx context.Context, stage1 []repo.Memory, classified []uuid.UUID) ([]repo.Memory, bool, error) {
	nodeSet := make(map[uuid.UUID]struct{}, len(classified))
	for _, root := range classified {
		nodeSet[root] = struct{}{}
		children, err := r.taxonomy.ListSubtree(ctx, root)
		if err != nil {
			return nil, false, err
		}
		for _, child := range children {
			nodeSet[child.ID] = struct{}{}
		}
	}
	if len(nodeSet) == 0 {
		return stage1, false, nil
	}

	memoryIDs := make([]uuid.UUID, 0, len(stage1))
	for _, item := range stage1 {
		memoryIDs = append(memoryIDs, item.ID)
	}
	nodeIDs := make([]uuid.UUID, 0, len(nodeSet))
	for id := range nodeSet {
		nodeIDs = append(nodeIDs, id)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT memory_id
		FROM memory_taxonomy_tag
		WHERE memory_id = ANY($1::uuid[])
		  AND taxonomy_node_id = ANY($2::uuid[])
	`, memoryIDs, nodeIDs)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	allowed := make(map[uuid.UUID]struct{}, len(stage1))
	for rows.Next() {
		var memoryID uuid.UUID
		if scanErr := rows.Scan(&memoryID); scanErr != nil {
			return nil, false, scanErr
		}
		allowed[memoryID] = struct{}{}
	}
	if rows.Err() != nil {
		return nil, false, rows.Err()
	}

	filtered := make([]repo.Memory, 0, len(allowed))
	for _, item := range stage1 {
		if _, ok := allowed[item.ID]; ok {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) < 3 {
		return stage1, true, nil
	}
	return filtered, false, nil
}

func (r *Retriever) queryEmbedding(ctx context.Context, req RetrievalRequest) ([]float32, error) {
	vectors, err := r.embedder.Embed(ctx, req.OrganizationID, retrievalInvocationPurpose, []string{req.Query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("expected one query embedding, got %d", len(vectors))
	}
	return vectors[0], nil
}

func rankCandidates(queryEmbedding []float32, candidates []repo.Memory, now time.Time) []RankedMemory {
	ranked := make([]RankedMemory, 0, len(candidates))
	for _, candidate := range candidates {
		cosine := cosineSimilarity(queryEmbedding, candidate.Embedding)
		score := relevanceScore(cosine, candidate.CreatedAt, candidate.MemoryType, candidate.Confidence, now)
		ranked = append(ranked, RankedMemory{
			Memory:    candidate,
			Score:     score,
			CosineSim: cosine,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Memory.CreatedAt.After(ranked[j].Memory.CreatedAt)
		}
		return ranked[i].Score > ranked[j].Score
	})
	return ranked
}

func (r *Retriever) applyPassiveCooldown(req RetrievalRequest, ranked []RankedMemory, turn int) []RankedMemory {
	if req.Mode != RetrievalModePassive || req.SessionID == nil || *req.SessionID == uuid.Nil {
		return ranked
	}
	if turn <= 0 {
		turn = r.cooldown.turn(*req.SessionID, req.SessionTurnIndex)
	}
	filtered := make([]RankedMemory, 0, len(ranked))
	for _, item := range ranked {
		if r.cooldown.shouldInclude(*req.SessionID, item.Memory.ID, turn) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (r *Retriever) recordPassiveCooldown(req RetrievalRequest, returned []RankedMemory, turn int) {
	if req.Mode != RetrievalModePassive || req.SessionID == nil || *req.SessionID == uuid.Nil {
		return
	}
	if turn <= 0 {
		turn = r.cooldown.turn(*req.SessionID, req.SessionTurnIndex)
	}
	ids := make([]uuid.UUID, 0, len(returned))
	for _, item := range returned {
		ids = append(ids, item.Memory.ID)
	}
	r.cooldown.markInjected(*req.SessionID, ids, turn)
}

func (r *Retriever) loadEntityProfiles(ctx context.Context, memories []RankedMemory) ([]EntityProfile, error) {
	if len(memories) == 0 {
		return nil, nil
	}

	memoryIDs := make([]uuid.UUID, 0, len(memories))
	for _, memory := range memories {
		memoryIDs = append(memoryIDs, memory.Memory.ID)
	}

	entityRows, err := r.pool.Query(ctx, `
		SELECT DISTINCT entity_id
		FROM memory_entity_mention
		WHERE memory_id = ANY($1::uuid[])
	`, memoryIDs)
	if err != nil {
		return nil, err
	}
	defer entityRows.Close()

	entityIDs := make([]uuid.UUID, 0)
	for entityRows.Next() {
		var entityID uuid.UUID
		if scanErr := entityRows.Scan(&entityID); scanErr != nil {
			return nil, scanErr
		}
		entityIDs = append(entityIDs, entityID)
	}
	if entityRows.Err() != nil {
		return nil, entityRows.Err()
	}
	if len(entityIDs) == 0 {
		return nil, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT e.id,
		       m.id, m.organization_id, m.project_id, m.project_task_id, m.agent_id, m.memory_type, m.scope, m.content, m.content_hash,
		       m.embedding::text, m.confidence, m.utility_score, m.extraction_score, m.status, m.is_hardened, m.sensitivity, m.trust_tier,
		       m.file_backed, m.file_path, m.file_last_scanned_at, m.superseded_by, m.superseded_at, m.archived_at, m.created_at, m.updated_at
		FROM memory_entity e
		JOIN memory m ON m.id = e.synthesis_memory_id
		WHERE e.id = ANY($1::uuid[])
	`, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	profiles := make([]EntityProfile, 0)
	for rows.Next() {
		var (
			entityID      uuid.UUID
			item          repo.Memory
			embeddingText *string
		)
		if err := rows.Scan(&entityID,
			&item.ID,
			&item.OrganizationID,
			&item.ProjectID,
			&item.ProjectTaskID,
			&item.AgentID,
			&item.MemoryType,
			&item.Scope,
			&item.Content,
			&item.ContentHash,
			&embeddingText,
			&item.Confidence,
			&item.UtilityScore,
			&item.ExtractionScore,
			&item.Status,
			&item.IsHardened,
			&item.Sensitivity,
			&item.TrustTier,
			&item.FileBacked,
			&item.FilePath,
			&item.FileLastScannedAt,
			&item.SupersededBy,
			&item.SupersededAt,
			&item.ArchivedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if embeddingText != nil && strings.TrimSpace(*embeddingText) != "" {
			vector, parseErr := parseEmbedding(*embeddingText)
			if parseErr != nil {
				return nil, parseErr
			}
			item.Embedding = vector
		}
		profiles = append(profiles, EntityProfile{
			EntityID: entityID,
			Memory:   item,
		})
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return profiles, nil
}

func dedupeUUIDs(input []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(input))
	out := make([]uuid.UUID, 0, len(input))
	for _, id := range input {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
