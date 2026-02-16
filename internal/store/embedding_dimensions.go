package store

const (
	legacyEmbeddingDimension = 768
	openAIEmbeddingDimension = 1536
)

func normalizeEmbeddingDimension(dimension int) int {
	if dimension == openAIEmbeddingDimension {
		return openAIEmbeddingDimension
	}
	return legacyEmbeddingDimension
}

func embeddingColumnForDimension(dimension int) string {
	if normalizeEmbeddingDimension(dimension) == openAIEmbeddingDimension {
		return "embedding_1536"
	}
	return "embedding"
}
