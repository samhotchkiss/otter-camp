package memory

import (
	"math"
	"sort"
	"strings"
)

const defaultEllieDedupSimilarityThreshold = 0.88

type EllieDedupMemory struct {
	MemoryID  string
	Status    string
	Embedding []float64
}

type EllieDedupPair struct {
	MemoryID1  string
	MemoryID2  string
	Similarity float64
}

type EllieDedupCluster struct {
	MemoryIDs []string
}

func DetectEllieDedupCandidatePairs(memories []EllieDedupMemory, threshold float64) []EllieDedupPair {
	if threshold <= 0 {
		threshold = defaultEllieDedupSimilarityThreshold
	}
	if threshold > 1 {
		threshold = 1
	}

	active := make([]EllieDedupMemory, 0, len(memories))
	for _, memory := range memories {
		if !strings.EqualFold(strings.TrimSpace(memory.Status), "active") {
			continue
		}
		id := strings.TrimSpace(memory.MemoryID)
		if id == "" || len(memory.Embedding) == 0 {
			continue
		}
		memory.MemoryID = id
		active = append(active, memory)
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].MemoryID < active[j].MemoryID
	})

	pairs := make([]EllieDedupPair, 0)
	for i := 0; i < len(active); i += 1 {
		for j := i + 1; j < len(active); j += 1 {
			similarity, ok := ellieDedupCosineSimilarity(active[i].Embedding, active[j].Embedding)
			if !ok || similarity < threshold {
				continue
			}
			id1, id2 := ellieDedupCanonicalPair(active[i].MemoryID, active[j].MemoryID)
			pairs = append(pairs, EllieDedupPair{
				MemoryID1:  id1,
				MemoryID2:  id2,
				Similarity: similarity,
			})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].MemoryID1 == pairs[j].MemoryID1 {
			return pairs[i].MemoryID2 < pairs[j].MemoryID2
		}
		return pairs[i].MemoryID1 < pairs[j].MemoryID1
	})
	return pairs
}

func ClusterEllieDedupPairs(pairs []EllieDedupPair) []EllieDedupCluster {
	if len(pairs) == 0 {
		return []EllieDedupCluster{}
	}

	adjacency := make(map[string]map[string]struct{})
	for _, pair := range pairs {
		id1, id2 := ellieDedupCanonicalPair(pair.MemoryID1, pair.MemoryID2)
		if strings.TrimSpace(id1) == "" || strings.TrimSpace(id2) == "" || id1 == id2 {
			continue
		}
		if adjacency[id1] == nil {
			adjacency[id1] = make(map[string]struct{})
		}
		if adjacency[id2] == nil {
			adjacency[id2] = make(map[string]struct{})
		}
		adjacency[id1][id2] = struct{}{}
		adjacency[id2][id1] = struct{}{}
	}

	nodes := make([]string, 0, len(adjacency))
	for id := range adjacency {
		nodes = append(nodes, id)
	}
	sort.Strings(nodes)

	seen := make(map[string]struct{}, len(nodes))
	clusters := make([]EllieDedupCluster, 0)
	for _, node := range nodes {
		if _, ok := seen[node]; ok {
			continue
		}
		component := make([]string, 0)
		stack := []string{node}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, ok := seen[current]; ok {
				continue
			}
			seen[current] = struct{}{}
			component = append(component, current)

			neighbors := make([]string, 0, len(adjacency[current]))
			for neighbor := range adjacency[current] {
				neighbors = append(neighbors, neighbor)
			}
			sort.Sort(sort.Reverse(sort.StringSlice(neighbors)))
			for _, neighbor := range neighbors {
				if _, ok := seen[neighbor]; ok {
					continue
				}
				stack = append(stack, neighbor)
			}
		}

		sort.Strings(component)
		clusters = append(clusters, EllieDedupCluster{MemoryIDs: component})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i].MemoryIDs) == 0 {
			return false
		}
		if len(clusters[j].MemoryIDs) == 0 {
			return true
		}
		return clusters[i].MemoryIDs[0] < clusters[j].MemoryIDs[0]
	})
	return clusters
}

func ellieDedupCanonicalPair(memoryID1, memoryID2 string) (string, string) {
	memoryID1 = strings.TrimSpace(memoryID1)
	memoryID2 = strings.TrimSpace(memoryID2)
	if memoryID2 < memoryID1 {
		return memoryID2, memoryID1
	}
	return memoryID1, memoryID2
}

func ellieDedupCosineSimilarity(left, right []float64) (float64, bool) {
	if len(left) == 0 || len(right) == 0 || len(left) != len(right) {
		return 0, false
	}

	dot := 0.0
	leftMagnitude := 0.0
	rightMagnitude := 0.0
	for i := range left {
		dot += left[i] * right[i]
		leftMagnitude += left[i] * left[i]
		rightMagnitude += right[i] * right[i]
	}
	if leftMagnitude == 0 || rightMagnitude == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(leftMagnitude) * math.Sqrt(rightMagnitude)), true
}
