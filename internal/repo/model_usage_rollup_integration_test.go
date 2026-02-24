//go:build integration

package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestModelUsageRollupRepoUpsertNullDimensions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	rollupRepo := NewModelUsageRollupRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "rollup-upsert-org",
		DisplayName: "Rollup Upsert Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	day := time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)
	first, err := rollupRepo.Upsert(ctx, ModelUsageRollup{
		OrganizationID:       org.ID,
		RollupDate:           day,
		RollupType:           "model_provider",
		RollupID:             nil,
		ModelName:            nil,
		InvocationPurpose:    nil,
		TotalInvocations:     1,
		TotalInputTokens:     100,
		TotalOutputTokens:    50,
		TotalCacheReadTokens: 5,
		TotalLatencyMS:       20,
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second, err := rollupRepo.Upsert(ctx, ModelUsageRollup{
		OrganizationID:       org.ID,
		RollupDate:           day,
		RollupType:           "model_provider",
		RollupID:             nil,
		ModelName:            nil,
		InvocationPurpose:    nil,
		TotalInvocations:     3,
		TotalInputTokens:     300,
		TotalOutputTokens:    120,
		TotalCacheReadTokens: 9,
		TotalLatencyMS:       60,
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("upsert id = %s, want %s", second.ID, first.ID)
	}
	if second.TotalInputTokens != 300 {
		t.Fatalf("total_input_tokens = %d, want 300", second.TotalInputTokens)
	}

	items, err := rollupRepo.GetForDate(ctx, org.ID, day)
	if err != nil {
		t.Fatalf("GetForDate: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("GetForDate count = %d, want 1", len(items))
	}
}

func TestModelUsageRollupRepoListByRollupID(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	rollupRepo := NewModelUsageRollupRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "rollup-list-org",
		DisplayName: "Rollup List Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	rollupID := uuid.New()
	day := time.Date(2026, 2, 21, 0, 0, 0, 0, time.UTC)
	modelName := "gpt-4o-mini"
	purpose := "agent_turn"
	if _, err := rollupRepo.Upsert(ctx, ModelUsageRollup{
		OrganizationID:       org.ID,
		RollupDate:           day,
		RollupType:           "project",
		RollupID:             &rollupID,
		ModelName:            &modelName,
		InvocationPurpose:    &purpose,
		TotalInvocations:     2,
		TotalInputTokens:     210,
		TotalOutputTokens:    90,
		TotalCacheReadTokens: 7,
		TotalLatencyMS:       45,
	}); err != nil {
		t.Fatalf("upsert project rollup: %v", err)
	}

	found, err := rollupRepo.ListByRollupID(ctx, org.ID, "project", rollupID)
	if err != nil {
		t.Fatalf("ListByRollupID: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("ListByRollupID count = %d, want 1", len(found))
	}
	if found[0].TotalInvocations != 2 {
		t.Fatalf("total_invocations = %d, want 2", found[0].TotalInvocations)
	}
}
