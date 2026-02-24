//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestSecretRepoCRUDUniqueAndDelete(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	secretRepo := NewSecretRepo(pool)

	orgA, err := orgRepo.Create(context.Background(), Organization{Slug: "secret-org-a", DisplayName: "Secret Org A"})
	if err != nil {
		t.Fatalf("create org A failed: %v", err)
	}
	orgB, err := orgRepo.Create(context.Background(), Organization{Slug: "secret-org-b", DisplayName: "Secret Org B"})
	if err != nil {
		t.Fatalf("create org B failed: %v", err)
	}

	created, err := secretRepo.Create(context.Background(), Secret{
		OrganizationID: orgA.ID,
		Slug:           "build-token",
		DisplayName:    "Build Token",
		Description:    "token for builds",
		Ciphertext:     []byte{1, 2, 3, 4, 5},
		Nonce:          []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		KeyVersion:     1,
		CreatedByType:  "human",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := secretRepo.GetBySlug(context.Background(), orgA.ID, "build-token")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetBySlug ID = %s, want %s", got.ID, created.ID)
	}
	if got.DisplayName != "Build Token" {
		t.Fatalf("display name = %q, want %q", got.DisplayName, "Build Token")
	}

	_, err = secretRepo.Create(context.Background(), Secret{
		OrganizationID: orgA.ID,
		Slug:           "build-token",
		DisplayName:    "Duplicate",
		Ciphertext:     []byte{9, 9, 9},
		Nonce:          []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		KeyVersion:     1,
		CreatedByType:  "human",
		CreatedByID:    uuid.Nil,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug error = %v, want ErrConflict", err)
	}

	if _, err := secretRepo.Create(context.Background(), Secret{
		OrganizationID: orgB.ID,
		Slug:           "build-token",
		DisplayName:    "Same Slug Different Org",
		Ciphertext:     []byte{7, 7, 7},
		Nonce:          []byte{2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		KeyVersion:     1,
		CreatedByType:  "human",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("same slug in different org should succeed, got %v", err)
	}

	updated, err := secretRepo.Update(context.Background(), Secret{
		ID:             created.ID,
		OrganizationID: orgA.ID,
		DisplayName:    "Build Token Updated",
		Description:    "updated",
		Ciphertext:     []byte{9, 8, 7, 6},
		Nonce:          []byte{9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2},
		KeyVersion:     1,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updated.DisplayName != "Build Token Updated" {
		t.Fatalf("updated display name = %q, want %q", updated.DisplayName, "Build Token Updated")
	}

	listed, err := secretRepo.List(context.Background(), orgA.ID)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List length = %d, want 1", len(listed))
	}

	if err := secretRepo.Delete(context.Background(), orgA.ID, "build-token"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := secretRepo.GetBySlug(context.Background(), orgA.ID, "build-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBySlug after delete error = %v, want ErrNotFound", err)
	}
}
