//go:build integration

package storage

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
)

func TestFSAdapterIntegrationCRUDLargeObject(t *testing.T) {
	adapter := NewFS(t.TempDir())
	ctx := context.Background()
	key := "orgs/int/runs/run-1/large.bin"
	payload := bytes.Repeat([]byte("z"), 2*1024*1024)

	if err := adapter.Put(ctx, key, bytes.NewReader(payload), PutOptions{ContentLength: int64(len(payload))}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	exists, err := adapter.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if !exists {
		t.Fatal("Exists = false, want true")
	}

	r, err := adapter.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}

	metas, err := adapter.List(ctx, "orgs/int/")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(metas))
	}

	if err := adapter.Delete(ctx, key); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	exists, err = adapter.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists {
		t.Fatal("Exists after Delete = true, want false")
	}
}

func TestFSAdapterIntegrationConcurrentPuts(t *testing.T) {
	adapter := NewFS(t.TempDir())
	ctx := context.Background()
	key := "orgs/int/chat/session/artifact.bin"

	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 1024*1024),
		bytes.Repeat([]byte("b"), 1024*1024),
		bytes.Repeat([]byte("c"), 1024*1024),
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(payloads))

	for _, payload := range payloads {
		wg.Add(1)
		payload := payload
		go func() {
			defer wg.Done()
			errCh <- adapter.Put(ctx, key, bytes.NewReader(payload), PutOptions{ContentLength: int64(len(payload))})
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent Put returned error: %v", err)
		}
	}

	r, err := adapter.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	defer r.Close()

	finalPayload, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	matched := false
	for _, payload := range payloads {
		if bytes.Equal(finalPayload, payload) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("final payload did not match any writer; size=%d", len(finalPayload))
	}
}
