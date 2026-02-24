package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestFSStorePutGetRoundTrip(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	key := "orgs/acme/chat/session-1/artifacts/a.txt"
	want := []byte("hello ottercamp")
	if err := store.Put(context.Background(), key, bytes.NewReader(want), PutOptions{ContentLength: int64(len(want))}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	body, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()

	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: got %q want %q", got, want)
	}
}

func TestFSStorePutRejectsPathTraversal(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	err = store.Put(context.Background(), "../../etc/passwd", bytes.NewReader([]byte("x")), PutOptions{})
	if err == nil {
		t.Fatal("expected invalid key error")
	}

	var keyErr *InvalidKeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("error = %T, want *InvalidKeyError", err)
	}
}

func TestFSStoreRejectsPathTraversalForGetDeleteAndExists(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	key := "../../etc/passwd"
	checkInvalidKey := func(err error, method string) {
		if err == nil {
			t.Fatalf("%s expected invalid key error", method)
		}
		var keyErr *InvalidKeyError
		if !errors.As(err, &keyErr) {
			t.Fatalf("%s error = %T, want *InvalidKeyError", method, err)
		}
	}

	_, err = store.Get(context.Background(), key)
	checkInvalidKey(err, "Get")

	err = store.Delete(context.Background(), key)
	checkInvalidKey(err, "Delete")

	_, err = store.Exists(context.Background(), key)
	checkInvalidKey(err, "Exists")
}

func TestFSStoreExistsAndDeleteLifecycle(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	key := "orgs/abc/runs/run-1/artifacts/out.log"
	exists, err := store.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists before put: %v", err)
	}
	if exists {
		t.Fatal("Exists before put = true, want false")
	}

	if err := store.Put(context.Background(), key, bytes.NewReader([]byte("data")), PutOptions{}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	exists, err = store.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists after put: %v", err)
	}
	if !exists {
		t.Fatal("Exists after put = false, want true")
	}

	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	exists, err = store.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists after delete: %v", err)
	}
	if exists {
		t.Fatal("Exists after delete = true, want false")
	}
}

func TestFSStoreListByPrefix(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	input := map[string]string{
		"orgs/abc/chat/a/file-1.txt": "a",
		"orgs/abc/chat/b/file-2.txt": "b",
		"orgs/def/chat/c/file-3.txt": "c",
	}
	for key, value := range input {
		if err := store.Put(context.Background(), key, bytes.NewReader([]byte(value)), PutOptions{}); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
	}

	items, err := store.List(context.Background(), "orgs/abc/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("List len = %d, want 2", len(items))
	}
	for _, item := range items {
		if len(item.Key) < len("orgs/abc/") || item.Key[:len("orgs/abc/")] != "orgs/abc/" {
			t.Fatalf("key %q does not match prefix", item.Key)
		}
	}
}

func TestFSStorePutIsAtomicForConcurrentReaders(t *testing.T) {
	store, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	key := "orgs/abc/runs/run-atomic/artifacts/blob.bin"
	oldValue := bytes.Repeat([]byte("a"), 2*1024*1024)
	newValue := bytes.Repeat([]byte("b"), 2*1024*1024)
	if err := store.Put(context.Background(), key, bytes.NewReader(oldValue), PutOptions{ContentLength: int64(len(oldValue))}); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- store.Put(
			context.Background(),
			key,
			&slowReader{data: newValue, chunkSize: 4 * 1024, delay: 2 * time.Millisecond},
			PutOptions{ContentLength: int64(len(newValue))},
		)
	}()

	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Put while reading: %v", err)
			}
			body, err := store.Get(context.Background(), key)
			if err != nil {
				t.Fatalf("Get final: %v", err)
			}
			finalData, err := io.ReadAll(body)
			body.Close()
			if err != nil {
				t.Fatalf("ReadAll final: %v", err)
			}
			if !bytes.Equal(finalData, newValue) {
				t.Fatal("final read does not match new content")
			}
			return
		default:
			body, err := store.Get(context.Background(), key)
			if err != nil {
				t.Fatalf("Get during write: %v", err)
			}
			data, err := io.ReadAll(body)
			body.Close()
			if err != nil {
				t.Fatalf("ReadAll during write: %v", err)
			}
			if !bytes.Equal(data, oldValue) && !bytes.Equal(data, newValue) {
				t.Fatalf("reader observed partial write of %d bytes", len(data))
			}
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

	n := r.chunkSize
	if n <= 0 {
		n = len(p)
	}
	if n > len(p) {
		n = len(p)
	}
	remaining := len(r.data) - r.offset
	if n > remaining {
		n = remaining
	}

	copy(p[:n], r.data[r.offset:r.offset+n])
	r.offset += n
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	return n, nil
}
