package memory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieDedupCandidateDetectionRespectsThreshold(t *testing.T) {
	memories := []EllieDedupMemory{
		{MemoryID: "a", Status: "active", Embedding: []float64{1, 0, 0}},
		{MemoryID: "b", Status: "active", Embedding: []float64{0.9, 0.1, 0}},
		{MemoryID: "c", Status: "active", Embedding: []float64{0, 1, 0}},
		{MemoryID: "d", Status: "deprecated", Embedding: []float64{0.95, 0.05, 0}},
	}

	pairs := DetectEllieDedupCandidatePairs(memories, 0.88)
	require.Len(t, pairs, 1)
	require.Equal(t, "a", pairs[0].MemoryID1)
	require.Equal(t, "b", pairs[0].MemoryID2)
	require.GreaterOrEqual(t, pairs[0].Similarity, 0.88)
}

func TestEllieDedupCandidateDetectionClustersConnectedPairs(t *testing.T) {
	pairs := []EllieDedupPair{
		{MemoryID1: "a", MemoryID2: "b", Similarity: 0.93},
		{MemoryID1: "b", MemoryID2: "c", Similarity: 0.91},
		{MemoryID1: "d", MemoryID2: "e", Similarity: 0.94},
	}

	clusters := ClusterEllieDedupPairs(pairs)
	require.Len(t, clusters, 2)
	require.Equal(t, []string{"a", "b", "c"}, clusters[0].MemoryIDs)
	require.Equal(t, []string{"d", "e"}, clusters[1].MemoryIDs)
}
