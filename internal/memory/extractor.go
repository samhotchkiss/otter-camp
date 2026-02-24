package memory

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	defaultBehavioralOverridePatternFile = "config/behavioral_override_patterns.txt"

	stage1InvocationPurpose   = "memory_extraction"
	stage4InvocationPurpose   = "memory_retrieval"
	stage2MinimumScore        = 40
	nearDuplicateCosineCutoff = 0.88
	maxExtractionBatchSize    = 10
	maxEmbeddingBatchSize     = 100
)

var validMemoryTypes = map[string]struct{}{
	"episodic":          {},
	"semantic":          {},
	"procedural":        {},
	"preference":        {},
	"entity_profile":    {},
	"execution_summary": {},
}

var statusUpdatePattern = regexp.MustCompile(`(?i)\b(task|project)\s+status\s+changed\s+to\b|\bdomain\s+event\b|\bstatus:\s*(draft|queued|in_progress|done|blocked|cancelled)\b`)
var standingInstructionPattern = regexp.MustCompile(`(?i)\b(update|modify|override|change|rewrite)\b.{0,48}\b(instruction|system prompt|persona|personality)\b`)

//go:embed prompts/extraction.txt
var extractionPromptText string

type ChatMessage struct {
	Role       string
	AuthorType string
	Content    string
}

type ExtractionSourceContext struct {
	SessionID    *uuid.UUID
	AgentID      *uuid.UUID
	ProjectID    *uuid.UUID
	TaskID       *uuid.UUID
	TrustTierCap float64
}

type ImportRecord struct {
	Content      string
	MemoryType   string
	Confidence   *float64
	UtilityScore *float64
	Entities     []ExtractedEntity
	TaxonomyTags []string
	TrustTier    *float64
	Embedding    []float32
}

type ExtractedEntity struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ExtractedMemory struct {
	Content         string            `json:"content"`
	MemoryType      string            `json:"memory_type"`
	Entities        []ExtractedEntity `json:"entities"`
	Confidence      float64           `json:"confidence"`
	UtilityEstimate float64           `json:"utility_estimate"`
}

type ExtractionRequest struct {
	InvocationPurpose string
	Prompt            string
	Messages          []ChatMessage
}

type EmbeddingRequest struct {
	InvocationPurpose string
	Inputs            []string
}

type extractionModel interface {
	Extract(ctx context.Context, req ExtractionRequest) ([]ExtractedMemory, error)
}

type embeddingModel interface {
	Embed(ctx context.Context, req EmbeddingRequest) ([][]float32, error)
}

type memoryRepository interface {
	Create(ctx context.Context, memory repo.Memory) (repo.Memory, error)
	HasActiveByContentHash(ctx context.Context, orgID uuid.UUID, contentHash string) (bool, error)
	FindNearDuplicates(ctx context.Context, orgID uuid.UUID, embedding []float32, minCosine float64, limit int) ([]repo.NearDuplicateMatch, error)
}

type memoryEntityRepository interface {
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repo.MemoryEntity, error)
	Create(ctx context.Context, entity repo.MemoryEntity) (repo.MemoryEntity, error)
	UpdateCanonicalName(ctx context.Context, id uuid.UUID, canonicalName string) (repo.MemoryEntity, error)
}

type memoryTaxonomyNodeRepository interface {
	ListByOrganization(ctx context.Context, organizationID uuid.UUID) ([]repo.MemoryTaxonomyNode, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.MemoryTaxonomyNode, error)
}

type memoryTaxonomyTagRepository interface {
	Create(ctx context.Context, tag repo.MemoryTaxonomyTag) (repo.MemoryTaxonomyTag, error)
}

type memoryEntityMentionRepository interface {
	Create(ctx context.Context, mention repo.MemoryEntityMention) (repo.MemoryEntityMention, error)
}

type memoryDedupReviewedRepository interface {
	Create(ctx context.Context, item repo.MemoryDedupReviewed) (repo.MemoryDedupReviewed, error)
}

type domainEventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type ExtractorOptions struct {
	Pool *pgxpool.Pool

	Memories       memoryRepository
	Entities       memoryEntityRepository
	TaxonomyNodes  memoryTaxonomyNodeRepository
	TaxonomyTags   memoryTaxonomyTagRepository
	EntityMentions memoryEntityMentionRepository
	DedupReviews   memoryDedupReviewedRepository
	Events         domainEventPublisher

	Model    extractionModel
	Embedder embeddingModel

	Prompt                        string
	BehavioralOverridePatterns    []string
	BehavioralOverridePatternFile string
	Logger                        *slog.Logger
}

type Extractor struct {
	memories       memoryRepository
	entities       memoryEntityRepository
	taxonomyNodes  memoryTaxonomyNodeRepository
	taxonomyTags   memoryTaxonomyTagRepository
	entityMentions memoryEntityMentionRepository
	dedupReviews   memoryDedupReviewedRepository
	events         domainEventPublisher
	model          extractionModel
	embedder       embeddingModel
	prompt         string
	patterns       []*regexp.Regexp
	logger         *slog.Logger
}

func NewExtractor(opts ExtractorOptions) (*Extractor, error) {
	if opts.Pool == nil && (opts.Memories == nil || opts.Entities == nil || opts.TaxonomyNodes == nil || opts.TaxonomyTags == nil || opts.EntityMentions == nil || opts.DedupReviews == nil) {
		return nil, fmt.Errorf("extractor requires database pool or explicit repositories")
	}
	if opts.Model == nil {
		return nil, fmt.Errorf("extractor model is required")
	}
	if opts.Embedder == nil {
		return nil, fmt.Errorf("embedding model is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	patterns, err := loadBehavioralOverridePatterns(opts.BehavioralOverridePatterns, opts.BehavioralOverridePatternFile)
	if err != nil {
		return nil, err
	}

	extractor := &Extractor{
		memories:       opts.Memories,
		entities:       opts.Entities,
		taxonomyNodes:  opts.TaxonomyNodes,
		taxonomyTags:   opts.TaxonomyTags,
		entityMentions: opts.EntityMentions,
		dedupReviews:   opts.DedupReviews,
		events:         opts.Events,
		model:          opts.Model,
		embedder:       opts.Embedder,
		prompt:         strings.TrimSpace(opts.Prompt),
		patterns:       patterns,
		logger:         opts.Logger,
	}
	if extractor.prompt == "" {
		extractor.prompt = strings.TrimSpace(extractionPromptText)
	}
	if extractor.memories == nil {
		extractor.memories = repo.NewMemoryRepo(opts.Pool)
	}
	if extractor.entities == nil {
		extractor.entities = repo.NewMemoryEntityRepo(opts.Pool)
	}
	if extractor.taxonomyNodes == nil {
		extractor.taxonomyNodes = repo.NewMemoryTaxonomyNodeRepo(opts.Pool)
	}
	if extractor.taxonomyTags == nil {
		extractor.taxonomyTags = repo.NewMemoryTaxonomyTagRepo(opts.Pool)
	}
	if extractor.entityMentions == nil {
		extractor.entityMentions = repo.NewMemoryEntityMentionRepo(opts.Pool)
	}
	if extractor.dedupReviews == nil {
		extractor.dedupReviews = repo.NewMemoryDedupReviewedRepo(opts.Pool)
	}
	return extractor, nil
}

func (e *Extractor) ExtractFromMessages(ctx context.Context, orgID uuid.UUID, msgs []ChatMessage, sourceContext ExtractionSourceContext) error {
	if orgID == uuid.Nil {
		return fmt.Errorf("organization id is required")
	}
	trustCap := clampFloat(sourceContext.TrustTierCap, 0, 1)
	if trustCap == 0 {
		authorType := ""
		if len(msgs) > 0 {
			authorType = msgs[0].AuthorType
		}
		trustCap = TrustTierCapForSource("chat_message", authorType)
	}
	sourceContext.TrustTierCap = trustCap
	return e.extract(ctx, orgID, msgs, sourceContext, "session_id", sourceContext.SessionID, nil)
}

func (e *Extractor) ExtractFromImport(ctx context.Context, orgID, importID uuid.UUID, records []ImportRecord) error {
	if orgID == uuid.Nil {
		return fmt.Errorf("organization id is required")
	}
	if importID == uuid.Nil {
		return fmt.Errorf("import id is required")
	}

	messages := make([]ChatMessage, 0, len(records))
	for _, record := range records {
		content := normalizeWhitespace(record.Content)
		if content == "" {
			continue
		}
		messages = append(messages, ChatMessage{
			Role:       "user",
			AuthorType: "human_user",
			Content:    content,
		})
	}

	return e.extract(ctx, orgID, messages, ExtractionSourceContext{
		TrustTierCap: TrustTierCapForSource("memory_import", ""),
	}, "import_id", nil, &importID)
}

type stagedCandidate struct {
	Content         string
	MemoryType      string
	Entities        []ExtractedEntity
	Confidence      float64
	UtilityEstimate float64
	ExtractionScore int
	TrustTier       float64
	EntityIDs       []uuid.UUID
	TaxonomyNodeIDs []uuid.UUID
	Embedding       []float32
}

func (e *Extractor) extract(
	ctx context.Context,
	orgID uuid.UUID,
	messages []ChatMessage,
	sourceContext ExtractionSourceContext,
	batchSource string,
	sessionID *uuid.UUID,
	importID *uuid.UUID,
) error {
	filtered := e.filterMessages(messages)
	if len(filtered) == 0 {
		return nil
	}

	stage1, err := e.runStage1(ctx, filtered)
	if err != nil {
		return err
	}
	if len(stage1) == 0 {
		return nil
	}

	stage2 := make([]stagedCandidate, 0, len(stage1))
	for _, candidate := range stage1 {
		breakdown := ScoreCandidate(ScoreInput{
			Content:         candidate.Content,
			MemoryType:      candidate.MemoryType,
			Entities:        candidate.Entities,
			UtilityEstimate: candidate.UtilityEstimate,
		})
		if !MeetsExtractionThreshold(breakdown.Total) {
			continue
		}
		stage2 = append(stage2, stagedCandidate{
			Content:         candidate.Content,
			MemoryType:      candidate.MemoryType,
			Entities:        candidate.Entities,
			Confidence:      candidate.Confidence,
			UtilityEstimate: candidate.UtilityEstimate,
			ExtractionScore: breakdown.Total,
			TrustTier:       ApplyTrustTierCap(candidate.Confidence, sourceContext.TrustTierCap),
		})
	}
	if len(stage2) == 0 {
		return nil
	}

	stage3, err := e.runStage3(ctx, orgID, stage2)
	if err != nil {
		return err
	}
	if len(stage3) == 0 {
		return nil
	}

	if err := e.embedStage4(ctx, stage3); err != nil {
		return err
	}

	createdCount := 0
	for _, candidate := range stage3 {
		contentHash := contentHash(candidate.Content)
		exactDup, err := e.memories.HasActiveByContentHash(ctx, orgID, contentHash)
		if err != nil {
			return err
		}
		if exactDup {
			continue
		}

		nearMatches, err := e.memories.FindNearDuplicates(ctx, orgID, candidate.Embedding, nearDuplicateCosineCutoff, 5)
		if err != nil {
			return err
		}

		created, err := e.memories.Create(ctx, repo.Memory{
			OrganizationID:  orgID,
			ProjectID:       sourceContext.ProjectID,
			ProjectTaskID:   sourceContext.TaskID,
			AgentID:         sourceContext.AgentID,
			MemoryType:      candidate.MemoryType,
			Scope:           scopeFromSource(sourceContext),
			Content:         candidate.Content,
			ContentHash:     contentHash,
			Embedding:       candidate.Embedding,
			Confidence:      clampFloat(candidate.Confidence, 0, 1),
			UtilityScore:    clampFloat(candidate.UtilityEstimate, 0, 1),
			ExtractionScore: ptrInt(candidate.ExtractionScore),
			Status:          "candidate",
			IsHardened:      false,
			TrustTier:       candidate.TrustTier,
		})
		if err != nil {
			return err
		}

		for _, taxonomyNodeID := range candidate.TaxonomyNodeIDs {
			if _, tagErr := e.taxonomyTags.Create(ctx, repo.MemoryTaxonomyTag{
				MemoryID:       created.ID,
				TaxonomyNodeID: taxonomyNodeID,
				AssignedBy:     "extraction",
				Confidence:     1.0,
			}); tagErr != nil {
				return tagErr
			}
		}
		for idx, entityID := range candidate.EntityIDs {
			if _, mentionErr := e.entityMentions.Create(ctx, repo.MemoryEntityMention{
				MemoryID:    created.ID,
				EntityID:    entityID,
				MentionText: normalizeWhitespace(candidate.Entities[idx].Name),
				Confidence:  1.0,
			}); mentionErr != nil {
				return mentionErr
			}
		}

		for _, match := range nearMatches {
			if match.MemoryID == created.ID {
				continue
			}
			_, dedupErr := e.dedupReviews.Create(ctx, repo.MemoryDedupReviewed{
				MemoryIDA:        match.MemoryID,
				MemoryIDB:        created.ID,
				CosineSimilarity: match.CosineSimilarity,
				Decision:         "deferred",
				ReviewedBy:       "llm",
			})
			if dedupErr != nil && !isConflictError(dedupErr) {
				return dedupErr
			}
		}

		createdCount++
	}

	if createdCount > 0 {
		e.emitExtractedEvent(ctx, orgID, createdCount, batchSource, sessionID, importID)
	}

	return nil
}

func (e *Extractor) runStage1(ctx context.Context, messages []ChatMessage) ([]ExtractedMemory, error) {
	batches := chunkMessages(messages, maxExtractionBatchSize)
	extracted := make([]ExtractedMemory, 0)
	for _, batch := range batches {
		items, err := e.model.Extract(ctx, ExtractionRequest{
			InvocationPurpose: stage1InvocationPurpose,
			Prompt:            e.prompt,
			Messages:          batch,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			item.Content = normalizeWhitespace(item.Content)
			item.MemoryType = strings.TrimSpace(item.MemoryType)
			if item.Content == "" {
				continue
			}
			if _, ok := validMemoryTypes[item.MemoryType]; !ok {
				continue
			}
			item.Confidence = clampFloat(item.Confidence, 0, 1)
			if item.Confidence == 0 {
				item.Confidence = 0.5
			}
			item.UtilityEstimate = clampFloat(item.UtilityEstimate, 0, 1)
			if item.UtilityEstimate == 0 {
				item.UtilityEstimate = 0.5
			}
			item.Entities = sanitizeEntities(item.Entities)
			extracted = append(extracted, item)
		}
	}
	return extracted, nil
}

func (e *Extractor) runStage3(ctx context.Context, orgID uuid.UUID, candidates []stagedCandidate) ([]stagedCandidate, error) {
	entities, err := e.entities.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}
	taxonomyNodes, err := e.taxonomyNodes.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}
	nodesByID := make(map[uuid.UUID]repo.MemoryTaxonomyNode, len(taxonomyNodes))
	for _, node := range taxonomyNodes {
		nodesByID[node.ID] = node
	}

	for idx := range candidates {
		candidate := &candidates[idx]
		for _, entity := range candidate.Entities {
			matchIndex := findMatchingEntityIndex(entities, entity.Name)
			if matchIndex == -1 {
				created, createErr := e.entities.Create(ctx, repo.MemoryEntity{
					OrganizationID: orgID,
					CanonicalName:  normalizeEntityName(entity.Name),
					EntityType:     strings.TrimSpace(entity.Type),
				})
				if createErr != nil {
					return nil, createErr
				}
				entities = append(entities, created)
				candidate.EntityIDs = append(candidate.EntityIDs, created.ID)
				continue
			}

			current := entities[matchIndex]
			canonical := chooseMoreSpecificCanonical(current.CanonicalName, normalizeEntityName(entity.Name))
			if canonical != current.CanonicalName {
				updated, updateErr := e.entities.UpdateCanonicalName(ctx, current.ID, canonical)
				if updateErr != nil {
					return nil, updateErr
				}
				entities[matchIndex] = updated
			}
			candidate.EntityIDs = append(candidate.EntityIDs, entities[matchIndex].ID)
		}

		if node := classifyTaxonomyNode(taxonomyNodes, candidate.MemoryType, candidate.Content); node != nil {
			path := taxonomyPath(nodesByID, *node)
			for _, pathNode := range path {
				candidate.TaxonomyNodeIDs = append(candidate.TaxonomyNodeIDs, pathNode.ID)
			}
		}
	}
	return candidates, nil
}

func (e *Extractor) embedStage4(ctx context.Context, candidates []stagedCandidate) error {
	for start := 0; start < len(candidates); start += maxEmbeddingBatchSize {
		end := start + maxEmbeddingBatchSize
		if end > len(candidates) {
			end = len(candidates)
		}

		inputs := make([]string, 0, end-start)
		for _, candidate := range candidates[start:end] {
			inputs = append(inputs, candidate.Content)
		}
		vectors, err := e.embedder.Embed(ctx, EmbeddingRequest{
			InvocationPurpose: stage4InvocationPurpose,
			Inputs:            inputs,
		})
		if err != nil {
			return err
		}
		if len(vectors) != len(inputs) {
			return fmt.Errorf("embedding count mismatch: got %d, want %d", len(vectors), len(inputs))
		}
		for offset := range vectors {
			candidates[start+offset].Embedding = vectors[offset]
		}
	}
	return nil
}

func (e *Extractor) filterMessages(messages []ChatMessage) []ChatMessage {
	filtered := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		if !e.passesStage0(message) {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func (e *Extractor) passesStage0(message ChatMessage) bool {
	role := strings.TrimSpace(message.Role)
	if role == "tool_call" || role == "tool_result" {
		return false
	}

	content := normalizeWhitespace(message.Content)
	if tokenCount(content) < 20 {
		return false
	}

	lowered := strings.ToLower(content)
	if statusUpdatePattern.MatchString(content) {
		return false
	}
	if standingInstructionPattern.MatchString(content) {
		return false
	}
	for _, pattern := range e.patterns {
		if pattern.MatchString(lowered) {
			return false
		}
	}
	return true
}

func (e *Extractor) emitExtractedEvent(
	ctx context.Context,
	orgID uuid.UUID,
	count int,
	batchSource string,
	sessionID *uuid.UUID,
	importID *uuid.UUID,
) {
	if e.events == nil {
		return
	}

	payload := map[string]any{
		"org_id":       orgID,
		"count":        count,
		"batch_source": batchSource,
	}
	if sessionID != nil {
		payload["session_id"] = *sessionID
	}
	if importID != nil {
		payload["import_id"] = *importID
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		e.logger.Warn("failed to marshal memory.extracted payload", "error", err)
		return
	}

	if err := e.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      "memory.extracted",
		ActorType:      "system",
		Payload:        encoded,
	}); err != nil {
		e.logger.Warn("failed to publish memory.extracted", "organization_id", orgID, "error", err)
	}
}

func findMatchingEntityIndex(entities []repo.MemoryEntity, rawName string) int {
	needle := normalizeEntityName(rawName)
	if needle == "" {
		return -1
	}
	loweredNeedle := strings.ToLower(needle)
	best := -1
	bestDistance := 999
	for index, entity := range entities {
		candidate := normalizeEntityName(entity.CanonicalName)
		if candidate == "" {
			continue
		}
		loweredCandidate := strings.ToLower(candidate)
		if loweredCandidate == loweredNeedle {
			return index
		}

		distance := levenshteinDistance(loweredCandidate, loweredNeedle)
		if distance <= 2 && distance < bestDistance {
			best = index
			bestDistance = distance
		}
	}
	return best
}

func classifyTaxonomyNode(nodes []repo.MemoryTaxonomyNode, memoryType, content string) *repo.MemoryTaxonomyNode {
	if len(nodes) == 0 {
		return nil
	}
	type scoredNode struct {
		node  repo.MemoryTaxonomyNode
		score int
	}

	loweredContent := strings.ToLower(content)
	loweredType := strings.ToLower(strings.TrimSpace(memoryType))
	best := scoredNode{score: 0}

	for _, node := range nodes {
		score := 0
		nodeSlug := strings.ToLower(node.Slug)
		nodeDisplay := strings.ToLower(node.DisplayName)
		if nodeSlug == loweredType || strings.Contains(nodeSlug, loweredType) || strings.Contains(nodeDisplay, loweredType) {
			score += 5
		}
		for _, part := range splitSlugTokens(nodeSlug) {
			if len(part) < 3 {
				continue
			}
			if strings.Contains(loweredContent, part) {
				score++
			}
		}
		if score > best.score {
			best = scoredNode{node: node, score: score}
		}
	}

	if best.score == 0 {
		return nil
	}
	return &best.node
}

func taxonomyPath(nodesByID map[uuid.UUID]repo.MemoryTaxonomyNode, leaf repo.MemoryTaxonomyNode) []repo.MemoryTaxonomyNode {
	path := []repo.MemoryTaxonomyNode{leaf}
	current := leaf
	for current.ParentID != nil {
		parent, ok := nodesByID[*current.ParentID]
		if !ok {
			break
		}
		path = append(path, parent)
		current = parent
	}
	return path
}

func chunkMessages(messages []ChatMessage, size int) [][]ChatMessage {
	if size <= 0 || len(messages) == 0 {
		return nil
	}
	chunks := make([][]ChatMessage, 0, (len(messages)+size-1)/size)
	for start := 0; start < len(messages); start += size {
		end := start + size
		if end > len(messages) {
			end = len(messages)
		}
		chunks = append(chunks, slices.Clone(messages[start:end]))
	}
	return chunks
}

func sanitizeEntities(entities []ExtractedEntity) []ExtractedEntity {
	clean := make([]ExtractedEntity, 0, len(entities))
	seen := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		name := normalizeEntityName(entity.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		clean = append(clean, ExtractedEntity{
			Name: name,
			Type: strings.TrimSpace(entity.Type),
		})
	}
	return clean
}

func splitSlugTokens(slug string) []string {
	slug = strings.ReplaceAll(slug, ".", " ")
	slug = strings.ReplaceAll(slug, "_", " ")
	slug = strings.ReplaceAll(slug, "-", " ")
	return strings.Fields(slug)
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

func scopeFromSource(sourceContext ExtractionSourceContext) string {
	if sourceContext.TaskID != nil && *sourceContext.TaskID != uuid.Nil {
		return "task"
	}
	if sourceContext.ProjectID != nil && *sourceContext.ProjectID != uuid.Nil {
		return "project"
	}
	if sourceContext.AgentID != nil && *sourceContext.AgentID != uuid.Nil {
		return "agent"
	}
	return "org"
}

func ptrInt(value int) *int {
	copy := value
	return &copy
}

func isConflictError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "conflict")
}

func loadBehavioralOverridePatterns(patterns []string, patternFile string) ([]*regexp.Regexp, error) {
	lines := patterns
	if len(lines) == 0 {
		path := strings.TrimSpace(patternFile)
		if path == "" {
			path = defaultBehavioralOverridePatternFile
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read behavioral override patterns: %w", err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			lines = append(lines, trimmed)
		}
	}

	if len(lines) == 0 {
		return nil, fmt.Errorf("behavioral override patterns cannot be empty")
	}

	compiled := make([]*regexp.Regexp, 0, len(lines))
	for _, line := range lines {
		pattern := strings.TrimSpace(line)
		if pattern == "" {
			continue
		}
		if !strings.HasPrefix(pattern, "(?i)") {
			pattern = "(?i)" + pattern
		}
		compiledPattern, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile behavioral override pattern %q: %w", line, err)
		}
		compiled = append(compiled, compiledPattern)
	}
	return compiled, nil
}
