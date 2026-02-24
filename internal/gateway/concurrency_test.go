package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConcurrencyManagerReserveBlocksUntilRelease(t *testing.T) {
	connectionID := uuid.New()
	manager := NewConcurrencyManager(1, map[uuid.UUID]int{
		connectionID: 1,
	})

	firstRelease, err := manager.Reserve(context.Background(), connectionID)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release, reserveErr := manager.Reserve(context.Background(), connectionID)
		if reserveErr != nil {
			return
		}
		release()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second reserve completed before release")
	case <-time.After(50 * time.Millisecond):
	}

	firstRelease()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second reserve did not unblock after release")
	}
}

func TestConcurrencyManagerReserveUnblocksOnContextCancel(t *testing.T) {
	connectionID := uuid.New()
	manager := NewConcurrencyManager(1, map[uuid.UUID]int{
		connectionID: 1,
	})

	firstRelease, err := manager.Reserve(context.Background(), connectionID)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	defer firstRelease()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, reserveErr := manager.Reserve(ctx, connectionID)
	if !errors.Is(reserveErr, context.DeadlineExceeded) {
		t.Fatalf("reserve error = %v, want context deadline exceeded", reserveErr)
	}
}
