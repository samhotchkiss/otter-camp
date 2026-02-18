package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

// EllieDedupStoreAdapter bridges internal/store's EllieDedupStore to the
// internal/memory worker interfaces (which use memory-local cursor types).
type EllieDedupStoreAdapter struct {
	Store *store.EllieDedupStore
}

func (a *EllieDedupStoreAdapter) ListCandidatePairs(
	ctx context.Context,
	orgID string,
	threshold float64,
	limit int,
) ([]EllieDedupPair, error) {
	if a == nil || a.Store == nil {
		return nil, fmt.Errorf("dedup store adapter is not configured")
	}
	pairs, err := a.Store.ListCandidatePairs(ctx, orgID, threshold, limit)
	if err != nil {
		return nil, err
	}
	out := make([]EllieDedupPair, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, EllieDedupPair{
			MemoryID1:  strings.TrimSpace(pair.MemoryID1),
			MemoryID2:  strings.TrimSpace(pair.MemoryID2),
			Similarity: pair.Similarity,
		})
	}
	return out, nil
}

func (a *EllieDedupStoreAdapter) ListMemoriesByIDs(
	ctx context.Context,
	orgID string,
	memoryIDs []string,
) ([]EllieDedupReviewMemory, error) {
	if a == nil || a.Store == nil {
		return nil, fmt.Errorf("dedup store adapter is not configured")
	}
	rows, err := a.Store.ListMemoriesByIDs(ctx, orgID, memoryIDs)
	if err != nil {
		return nil, err
	}
	out := make([]EllieDedupReviewMemory, 0, len(rows))
	for _, row := range rows {
		out = append(out, EllieDedupReviewMemory{
			MemoryID: strings.TrimSpace(row.MemoryID),
			Title:    strings.TrimSpace(row.Title),
			Content:  strings.TrimSpace(row.Content),
		})
	}
	return out, nil
}

func (a *EllieDedupStoreAdapter) IsPairReviewed(ctx context.Context, orgID, memoryID1, memoryID2 string) (bool, error) {
	if a == nil || a.Store == nil {
		return false, fmt.Errorf("dedup store adapter is not configured")
	}
	return a.Store.IsPairReviewed(ctx, orgID, memoryID1, memoryID2)
}

func (a *EllieDedupStoreAdapter) RecordReviewedPair(ctx context.Context, orgID, memoryID1, memoryID2, decision string) error {
	if a == nil || a.Store == nil {
		return fmt.Errorf("dedup store adapter is not configured")
	}
	return a.Store.RecordReviewedPair(ctx, store.RecordEllieDedupReviewedPairInput{
		OrgID:     orgID,
		MemoryID1: memoryID1,
		MemoryID2: memoryID2,
		Decision:  decision,
	})
}

func (a *EllieDedupStoreAdapter) DeprecateMemories(ctx context.Context, orgID string, memoryIDs []string, supersededBy *string) error {
	if a == nil || a.Store == nil {
		return fmt.Errorf("dedup store adapter is not configured")
	}
	return a.Store.DeprecateMemories(ctx, orgID, memoryIDs, supersededBy)
}

func (a *EllieDedupStoreAdapter) CreateMergedMemory(
	ctx context.Context,
	orgID, title, content string,
	sourceMemoryIDs []string,
) (string, error) {
	if a == nil || a.Store == nil {
		return "", fmt.Errorf("dedup store adapter is not configured")
	}
	return a.Store.CreateMergedMemory(ctx, orgID, title, content, sourceMemoryIDs)
}

func (a *EllieDedupStoreAdapter) GetCursor(ctx context.Context, orgID string) (*EllieDedupCursorState, error) {
	if a == nil || a.Store == nil {
		return nil, fmt.Errorf("dedup store adapter is not configured")
	}
	cursor, err := a.Store.GetCursor(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if cursor == nil {
		return nil, nil
	}
	return &EllieDedupCursorState{
		LastClusterKey:    cursor.LastClusterKey,
		ProcessedClusters: cursor.ProcessedClusters,
		TotalClusters:     cursor.TotalClusters,
	}, nil
}

func (a *EllieDedupStoreAdapter) UpsertCursor(ctx context.Context, orgID string, lastClusterKey *string, processedClusters, totalClusters int) error {
	if a == nil || a.Store == nil {
		return fmt.Errorf("dedup store adapter is not configured")
	}
	return a.Store.UpsertCursor(ctx, store.UpsertEllieDedupCursorInput{
		OrgID:             orgID,
		LastClusterKey:    lastClusterKey,
		ProcessedClusters: processedClusters,
		TotalClusters:     totalClusters,
	})
}

