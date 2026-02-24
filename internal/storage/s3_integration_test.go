//go:build integration

package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestS3AdapterIntegrationRoundTrip(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_S3_ENDPOINT"))
	if endpoint == "" {
		t.Skip("OTTERCAMP_TEST_S3_ENDPOINT not set")
	}

	bucket := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_S3_BUCKET"))
	region := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_S3_REGION"))
	accessKey := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_S3_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_S3_SECRET_ACCESS_KEY"))
	if bucket == "" || region == "" || accessKey == "" || secretKey == "" {
		t.Skip("s3 integration env incomplete; set OTTERCAMP_TEST_S3_BUCKET/REGION/ACCESS_KEY_ID/SECRET_ACCESS_KEY")
	}

	store, err := New(Config{
		Backend:           BackendS3,
		S3Endpoint:        endpoint,
		S3Bucket:          bucket,
		S3Region:          region,
		S3AccessKeyID:     accessKey,
		S3SecretAccessKey: secretKey,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx := context.Background()
	key := "integration/" + uuid.NewString() + ".txt"
	payload := []byte("hello integration s3")

	if err := store.Put(ctx, key, bytes.NewReader(payload), PutOptions{ContentLength: int64(len(payload)), ContentType: "text/plain"}); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	exists, err := store.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if !exists {
		t.Fatal("Exists = false, want true")
	}

	r, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get returned %q, want %q", string(got), string(payload))
	}

	metas, err := store.List(ctx, "integration/")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	found := false
	for _, meta := range metas {
		if meta.Key == key {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("List did not return key %q", key)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	exists, err = store.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists {
		t.Fatal("Exists after Delete = true, want false")
	}
}
