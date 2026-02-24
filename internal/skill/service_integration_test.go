//go:build integration

package skill

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestServiceDeleteSoftDeletes(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	skillRepo := repo.NewSkillRepo(pool)
	svc := NewService(skillRepo)

	org, err := orgRepo.Create(context.Background(), repo.Organization{Slug: "service-org", DisplayName: "Service Org"})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	created, err := svc.Create(context.Background(), org.ID, CreateRequest{
		Slug:          "to-delete",
		DisplayName:   "To Delete",
		Description:   "To Delete",
		FilePath:      "skills/to-delete.md",
		CreatedByType: "system",
		CreatedByID:   uuid.Nil,
	})
	if err != nil {
		t.Fatalf("service Create failed: %v", err)
	}

	if err := svc.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("service Delete failed: %v", err)
	}

	listed, err := svc.List(context.Background(), org.ID, nil)
	if err != nil {
		t.Fatalf("service List failed: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("service List length = %d, want 0", len(listed))
	}

	got, err := svc.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("service Get failed: %v", err)
	}
	if got.IsActive {
		t.Fatal("expected soft-deleted skill to have is_active=false")
	}
}
