package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFSAdapterPutGetRoundTrip(t *testing.T) {
	adapter := NewFS(t.TempDir())
	ctx := context.Background()
	key := "orgs/abc/chat/file.txt"
	want := []byte("hello from fs adapter")

	if err := adapter.Put(ctx, key, bytes.NewReader(want), PutOptions{ContentLength: int64(len(want))}); err != nil {
		t.Fatalf("Put returned error: %v", err)
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

	if !bytes.Equal(got, want) {
		t.Fatalf("Get returned %q, want %q", string(got), string(want))
	}
}

func TestFSAdapterRejectsPathTraversalWithTypedError(t *testing.T) {
	adapter := NewFS(t.TempDir())
	err := adapter.Put(context.Background(), "../../etc/passwd", strings.NewReader("x"), PutOptions{ContentLength: 1})
	if err == nil {
		t.Fatal("expected invalid key error")
	}

	var keyErr *InvalidKeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("error type = %T, want *InvalidKeyError", err)
	}
}

func TestFSAdapterExistsAndDeleteLifecycle(t *testing.T) {
	adapter := NewFS(t.TempDir())
	ctx := context.Background()
	key := "orgs/abc/memory/item.bin"

	exists, err := adapter.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists before Put returned error: %v", err)
	}
	if exists {
		t.Fatal("Exists before Put = true, want false")
	}

	if err := adapter.Put(ctx, key, strings.NewReader("payload"), PutOptions{ContentLength: int64(len("payload"))}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	exists, err = adapter.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists after Put returned error: %v", err)
	}
	if !exists {
		t.Fatal("Exists after Put = false, want true")
	}

	if err := adapter.Delete(ctx, key); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	exists, err = adapter.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists after Delete returned error: %v", err)
	}
	if exists {
		t.Fatal("Exists after Delete = true, want false")
	}
}

func TestFSAdapterListByPrefix(t *testing.T) {
	adapter := NewFS(t.TempDir())
	ctx := context.Background()

	entries := map[string]string{
		"orgs/abc/chat/a.txt": "a",
		"orgs/abc/chat/b.txt": "b",
		"orgs/xyz/chat/c.txt": "c",
	}

	for key, value := range entries {
		if err := adapter.Put(ctx, key, strings.NewReader(value), PutOptions{ContentLength: int64(len(value))}); err != nil {
			t.Fatalf("Put(%s) returned error: %v", key, err)
		}
	}

	metas, err := adapter.List(ctx, "orgs/abc/")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if len(metas) != 2 {
		t.Fatalf("List returned %d results, want 2", len(metas))
	}
	for _, meta := range metas {
		if !strings.HasPrefix(meta.Key, "orgs/abc/") {
			t.Fatalf("List returned key %q outside prefix", meta.Key)
		}
	}
}

func TestFSAdapterPutIsAtomicForReaders(t *testing.T) {
	root := t.TempDir()
	adapter := NewFS(root)
	ctx := context.Background()
	key := "orgs/abc/runs/run-1/artifact.bin"

	oldPayload := bytes.Repeat([]byte("A"), 128*1024)
	if err := adapter.Put(ctx, key, bytes.NewReader(oldPayload), PutOptions{ContentLength: int64(len(oldPayload))}); err != nil {
		t.Fatalf("seed Put returned error: %v", err)
	}

	newPayload := bytes.Repeat([]byte("B"), 3*1024*1024)
	putDone := make(chan error, 1)
	go func() {
		putDone <- adapter.Put(ctx, key, &slowReader{data: newPayload, chunkSize: 64 * 1024, delay: time.Millisecond}, PutOptions{ContentLength: int64(len(newPayload))})
	}()

	path := filepath.Join(root, filepath.FromSlash(key))
	deadline := time.After(8 * time.Second)

	for {
		select {
		case err := <-putDone:
			if err != nil {
				t.Fatalf("Put returned error: %v", err)
			}

			finalPayload, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("failed reading final object: %v", readErr)
			}
			if !bytes.Equal(finalPayload, newPayload) {
				t.Fatalf("final payload mismatch: got %d bytes", len(finalPayload))
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for Put")
		default:
			payload, readErr := os.ReadFile(path)
			if readErr != nil {
				if errors.Is(readErr, os.ErrNotExist) {
					time.Sleep(time.Millisecond)
					continue
				}
				t.Fatalf("reader error: %v", readErr)
			}

			if len(payload) != len(oldPayload) && len(payload) != len(newPayload) {
				t.Fatalf("reader observed partial write size=%d (old=%d new=%d)", len(payload), len(oldPayload), len(newPayload))
			}
			time.Sleep(time.Millisecond)
		}
	}
}

type slowReader struct {
	data      []byte
	chunkSize int
	delay     time.Duration
	offset    int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}

	if r.chunkSize <= 0 {
		r.chunkSize = len(p)
	}

	n := r.chunkSize
	remaining := len(r.data) - r.offset
	if n > remaining {
		n = remaining
	}
	if n > len(p) {
		n = len(p)
	}

	copy(p[:n], r.data[r.offset:r.offset+n])
	r.offset += n
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	return n, nil
}
