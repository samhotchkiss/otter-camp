//go:build integration

package storage

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
)

func TestFSStoreIntegrationCRUDAndLargeObject(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	key := "orgs/int/chat/session-1/artifacts/large.bin"
	large := bytes.Repeat([]byte("z"), 2*1024*1024)

	exists, err := store.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists before Put: %v", err)
	}
	if exists {
		t.Fatal("Exists before Put = true, want false")
	}

	if err := store.Put(context.Background(), key, bytes.NewReader(large), PutOptions{ContentLength: int64(len(large))}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	body, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, large) {
		t.Fatal("round-trip mismatch for large object")
	}

	items, err := store.List(context.Background(), "orgs/int/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List len = %d, want 1", len(items))
	}

	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	exists, err = store.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists after Delete: %v", err)
	}
	if exists {
		t.Fatal("Exists after Delete = true, want false")
	}
}

func TestFSStoreIntegrationConcurrentPuts(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	const writers = 16
	var wg sync.WaitGroup
	wg.Add(writers)
	errs := make(chan error, writers)

	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := "orgs/int/runs/run-1/artifacts/object-" + string(rune('a'+i)) + ".txt"
			value := []byte{byte('a' + i)}
			errs <- store.Put(context.Background(), key, bytes.NewReader(value), PutOptions{ContentLength: int64(len(value))})
		}()
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Put returned error: %v", err)
		}
	}

	items, err := store.List(context.Background(), "orgs/int/runs/run-1/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != writers {
		t.Fatalf("List len = %d, want %d", len(items), writers)
	}
}
