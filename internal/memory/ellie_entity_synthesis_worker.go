package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultEllieEntitySynthesisMinMentions    = 5
	defaultEllieEntitySynthesisCandidateBatch = 50
	defaultEllieEntitySynthesisSourceLimit    = 250
)

type EllieEntitySynthesisStore interface {
	ListCandidates(ctx context.Context, orgID string, minMentions, limit int) ([]store.EllieEntitySynthesisCandidate, error)
	ListSourceMemories(ctx context.Context, orgID, entityKey string, limit int) ([]store.EllieEntitySynthesisSourceMemory, error)
	CreateEllieExtractedMemory(ctx context.Context, input store.CreateEllieExtractedMemoryInput) (string, error)
	UpdateSynthesisMemory(ctx context.Context, input store.UpdateEllieEntitySynthesisMemoryInput) error
}

type EllieEntitySynthesisEmbeddingStore interface {
	UpdateMemoryEmbedding(ctx context.Context, memoryID string, embedding []float64) error
}

type EllieEntitySynthesizer interface {
	Synthesize(ctx context.Context, input EllieEntitySynthesisInput) (EllieEntitySynthesisOutput, error)
}

type EllieEntitySynthesisInput struct {
	OrgID          string
	EntityKey      string
	EntityName     string
	Prompt         string
	SourceMemories []store.EllieEntitySynthesisSourceMemory
}

type EllieEntitySynthesisOutput struct {
	Title   string
	Content string
	Model   string
	TraceID string
}

type EllieEntitySynthesisWorkerConfig struct {
	MinMentions       int
	CandidateBatch    int
	SourceMemoryLimit int
	Synthesizer       EllieEntitySynthesizer
}

type EllieEntitySynthesisRunResult struct {
	CandidatesConsidered int
	CreatedCount         int
	UpdatedCount         int
	SkippedExistingCount int
}

type EllieEntitySynthesisWorker struct {
	Store             EllieEntitySynthesisStore
	Embedder          Embedder
	EmbeddingStore    EllieEntitySynthesisEmbeddingStore
	MinMentions       int
	CandidateBatch    int
	SourceMemoryLimit int
	Synthesizer       EllieEntitySynthesizer
}

func NewEllieEntitySynthesisWorker(
	store EllieEntitySynthesisStore,
	embedder Embedder,
	embeddingStore EllieEntitySynthesisEmbeddingStore,
	cfg EllieEntitySynthesisWorkerConfig,
) *EllieEntitySynthesisWorker {
	minMentions := cfg.MinMentions
	if minMentions <= 0 {
		minMentions = defaultEllieEntitySynthesisMinMentions
	}
	candidateBatch := cfg.CandidateBatch
	if candidateBatch <= 0 {
		candidateBatch = defaultEllieEntitySynthesisCandidateBatch
	}
	sourceLimit := cfg.SourceMemoryLimit
	if sourceLimit <= 0 {
		sourceLimit = defaultEllieEntitySynthesisSourceLimit
	}

	return &EllieEntitySynthesisWorker{
		Store:             store,
		Embedder:          embedder,
		EmbeddingStore:    embeddingStore,
		MinMentions:       minMentions,
		CandidateBatch:    candidateBatch,
		SourceMemoryLimit: sourceLimit,
		Synthesizer:       cfg.Synthesizer,
	}
}

func (w *EllieEntitySynthesisWorker) RunOnce(ctx context.Context, orgID string) (EllieEntitySynthesisRunResult, error) {
	if w == nil {
		return EllieEntitySynthesisRunResult{}, fmt.Errorf("ellie entity synthesis worker is nil")
	}
	if w.Store == nil {
		return EllieEntitySynthesisRunResult{}, fmt.Errorf("entity synthesis store is required")
	}
	if w.Synthesizer == nil {
		return EllieEntitySynthesisRunResult{}, fmt.Errorf("entity synthesizer is required")
	}

	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return EllieEntitySynthesisRunResult{}, fmt.Errorf("org_id is required")
	}

	candidates, err := w.Store.ListCandidates(ctx, orgID, w.MinMentions, w.CandidateBatch)
	if err != nil {
		return EllieEntitySynthesisRunResult{}, fmt.Errorf("list entity synthesis candidates: %w", err)
	}

	result := EllieEntitySynthesisRunResult{}
	for _, candidate := range candidates {
		result.CandidatesConsidered += 1
		if candidate.ExistingSynthesisMemoryID != nil && !candidate.NeedsResynthesis {
			result.SkippedExistingCount += 1
			continue
		}

		entityKey := strings.TrimSpace(candidate.EntityKey)
		if entityKey == "" {
			continue
		}
		entityName := strings.TrimSpace(candidate.EntityName)
		if entityName == "" {
			entityName = entityKey
		}

		sourceMemories, err := w.Store.ListSourceMemories(ctx, orgID, entityKey, w.SourceMemoryLimit)
		if err != nil {
			return result, fmt.Errorf("list source memories for entity %q: %w", entityKey, err)
		}
		if len(sourceMemories) == 0 {
			continue
		}

		promptSources := make([]EllieEntitySynthesisPromptSourceMemory, 0, len(sourceMemories))
		for _, source := range sourceMemories {
			promptSources = append(promptSources, EllieEntitySynthesisPromptSourceMemory{
				MemoryID: source.MemoryID,
				Title:    source.Title,
				Content:  source.Content,
			})
		}
		prompt := BuildEllieEntitySynthesisPrompt(EllieEntitySynthesisPromptInput{
			EntityName:     entityName,
			SourceMemories: promptSources,
		})

		synthesis, err := w.Synthesizer.Synthesize(ctx, EllieEntitySynthesisInput{
			OrgID:          orgID,
			EntityKey:      entityKey,
			EntityName:     entityName,
			Prompt:         prompt,
			SourceMemories: sourceMemories,
		})
		if err != nil {
			return result, fmt.Errorf("synthesize entity %q: %w", entityKey, err)
		}

		title := strings.TrimSpace(synthesis.Title)
		if title == "" {
			title = fmt.Sprintf("%s definition", entityName)
		}
		content := strings.TrimSpace(synthesis.Content)
		if content == "" {
			return result, fmt.Errorf("synthesis content is required for entity %q", entityKey)
		}

		now := time.Now().UTC()
		metadata := map[string]any{
			"source_type":         "synthesis",
			"entity_key":          entityKey,
			"entity_name":         entityName,
			"source_memory_ids":   ellieEntitySynthesisSourceMemoryIDs(sourceMemories),
			"source_memory_count": len(sourceMemories),
			"synthesized_at":      now.Format(time.RFC3339Nano),
		}
		if model := strings.TrimSpace(synthesis.Model); model != "" {
			metadata["synthesis_model"] = model
		}
		if traceID := strings.TrimSpace(synthesis.TraceID); traceID != "" {
			metadata["synthesis_trace_id"] = traceID
		}
		metadataRaw, _ := json.Marshal(metadata)

		occurredAt := ellieEntitySynthesisLatestOccurredAt(sourceMemories, now)
		sourceProjectID := ellieEntitySynthesisFirstProjectID(sourceMemories)

		if candidate.ExistingSynthesisMemoryID != nil && candidate.NeedsResynthesis {
			memoryID := strings.TrimSpace(*candidate.ExistingSynthesisMemoryID)
			if memoryID == "" {
				return result, fmt.Errorf("existing synthesis memory id is empty for entity %q", entityKey)
			}
			if err := w.Store.UpdateSynthesisMemory(ctx, store.UpdateEllieEntitySynthesisMemoryInput{
				OrgID:           orgID,
				MemoryID:        memoryID,
				Title:           title,
				Content:         content,
				Metadata:        metadataRaw,
				Importance:      5,
				Confidence:      0.95,
				OccurredAt:      occurredAt,
				SourceProjectID: sourceProjectID,
			}); err != nil {
				return result, fmt.Errorf("update synthesis memory for entity %q: %w", entityKey, err)
			}
			if err := w.embedMemory(ctx, memoryID, title, content); err != nil {
				return result, fmt.Errorf("embed synthesis memory for entity %q: %w", entityKey, err)
			}
			result.UpdatedCount += 1
			continue
		}

		memoryID, err := w.Store.CreateEllieExtractedMemory(ctx, store.CreateEllieExtractedMemoryInput{
			OrgID:           orgID,
			Kind:            "fact",
			Title:           title,
			Content:         content,
			Metadata:        metadataRaw,
			Importance:      5,
			Confidence:      0.95,
			Status:          "active",
			OccurredAt:      occurredAt,
			SourceProjectID: sourceProjectID,
		})
		if err != nil {
			return result, fmt.Errorf("create synthesis memory for entity %q: %w", entityKey, err)
		}

		if err := w.embedMemory(ctx, memoryID, title, content); err != nil {
			return result, fmt.Errorf("embed synthesis memory for entity %q: %w", entityKey, err)
		}

		result.CreatedCount += 1
	}

	return result, nil
}

func (w *EllieEntitySynthesisWorker) embedMemory(ctx context.Context, memoryID, title, content string) error {
	if w == nil || w.Embedder == nil || w.EmbeddingStore == nil {
		return nil
	}

	embeddings, err := w.Embedder.Embed(ctx, []string{strings.TrimSpace(title + "\n\n" + content)})
	if err != nil {
		return err
	}
	if len(embeddings) != 1 || len(embeddings[0]) == 0 {
		return fmt.Errorf("embedder returned empty synthesis embedding")
	}
	if len(embeddings[0]) != 1536 {
		return fmt.Errorf("synthesis embedding dimension mismatch: expected 1536, got %d", len(embeddings[0]))
	}
	if err := w.EmbeddingStore.UpdateMemoryEmbedding(ctx, strings.TrimSpace(memoryID), embeddings[0]); err != nil {
		return err
	}
	return nil
}

func ellieEntitySynthesisSourceMemoryIDs(memories []store.EllieEntitySynthesisSourceMemory) []string {
	ids := make([]string, 0, len(memories))
	for _, source := range memories {
		id := strings.TrimSpace(source.MemoryID)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func ellieEntitySynthesisFirstProjectID(memories []store.EllieEntitySynthesisSourceMemory) *string {
	for _, source := range memories {
		if source.SourceProjectID == nil {
			continue
		}
		trimmed := strings.TrimSpace(*source.SourceProjectID)
		if trimmed == "" {
			continue
		}
		return &trimmed
	}
	return nil
}

func ellieEntitySynthesisLatestOccurredAt(memories []store.EllieEntitySynthesisSourceMemory, fallback time.Time) time.Time {
	latest := fallback.UTC()
	for _, source := range memories {
		occurredAt := source.OccurredAt.UTC()
		if occurredAt.IsZero() {
			continue
		}
		if occurredAt.After(latest) {
			latest = occurredAt
		}
	}
	return latest
}
