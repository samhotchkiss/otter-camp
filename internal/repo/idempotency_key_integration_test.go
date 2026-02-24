//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestIdempotencyKeyStoreReplayAndConflict(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	keyRepo := NewIdempotencyKeyRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{Slug: "idem-org", DisplayName: "Idem Org"})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	first, err := keyRepo.Store(context.Background(), IdempotencyKey{
		OrganizationID: org.ID,
		KeyHash:        "key-hash-1",
		RequestHash:    "request-hash-1",
		ResponseStatus: 201,
		ResponseBody:   []byte(`{"id":"abc"}`),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("first Store failed: %v", err)
	}

	replayed, err := keyRepo.Store(context.Background(), IdempotencyKey{
		OrganizationID: org.ID,
		KeyHash:        "key-hash-1",
		RequestHash:    "request-hash-1",
		ResponseStatus: 500,
		ResponseBody:   []byte(`{"id":"ignored"}`),
	})
	if err != nil {
		t.Fatalf("replay Store failed: %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("replayed id = %s, want %s", replayed.ID, first.ID)
	}
	if replayed.ResponseStatus != first.ResponseStatus {
		t.Fatalf("replayed response status = %d, want %d", replayed.ResponseStatus, first.ResponseStatus)
	}

	_, err = keyRepo.Store(context.Background(), IdempotencyKey{
		OrganizationID: org.ID,
		KeyHash:        "key-hash-1",
		RequestHash:    "request-hash-2",
		ResponseStatus: 200,
		ResponseBody:   []byte(`{"different":true}`),
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("Store conflict error = %v, want ErrIdempotencyConflict", err)
	}

	if err := keyRepo.CheckConflict(context.Background(), org.ID, "key-hash-1", "request-hash-1"); err != nil {
		t.Fatalf("CheckConflict same hash failed: %v", err)
	}
	if err := keyRepo.CheckConflict(context.Background(), org.ID, "key-hash-1", "request-hash-2"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("CheckConflict different hash error = %v, want ErrIdempotencyConflict", err)
	}
}
