package memory

import "github.com/jackc/pgx/v5/pgxpool"

// NewDefaultExtractor creates an Extractor with deterministic (non-LLM) model defaults.
// Suitable for wiring in workers where the real LLM extraction model is not configured.
// Uses the same deterministic implementations as the EventConsumer default path.
func NewDefaultExtractor(pool *pgxpool.Pool) (*Extractor, error) {
	return NewExtractor(ExtractorOptions{
		Pool:                       pool,
		Model:                      deterministicTurnExtractionModel{},
		Embedder:                   deterministicTurnEmbeddingModel{},
		BehavioralOverridePatterns: []string{`ignore\s+all\s+previous\s+instructions`},
	})
}
