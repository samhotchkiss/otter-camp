//go:build integration

package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestS3StoreIntegrationRoundTripOptIn(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_S3_ENDPOINT")) == "" {
		t.Skip("OTTERCAMP_TEST_S3_ENDPOINT not set")
	}

	cfg := Config{
		Backend:           BackendS3,
		S3Endpoint:        envFirst("OTTERCAMP_TEST_S3_ENDPOINT", "OTTERCAMP_S3_ENDPOINT"),
		S3Bucket:          envFirst("OTTERCAMP_TEST_S3_BUCKET", "OTTERCAMP_S3_BUCKET"),
		S3Region:          envFirst("OTTERCAMP_TEST_S3_REGION", "OTTERCAMP_S3_REGION"),
		S3AccessKeyID:     envFirst("OTTERCAMP_TEST_S3_ACCESS_KEY_ID", "OTTERCAMP_S3_ACCESS_KEY_ID"),
		S3SecretAccessKey: envFirst("OTTERCAMP_TEST_S3_SECRET_ACCESS_KEY", "OTTERCAMP_S3_SECRET_ACCESS_KEY"),
	}

	if cfg.S3Bucket == "" || cfg.S3Region == "" || cfg.S3AccessKeyID == "" || cfg.S3SecretAccessKey == "" {
		t.Skip("S3 test endpoint is set but test credentials are incomplete")
	}

	store, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	key := "integration/object-storage/" + time.Now().Format("20060102150405.000000000") + ".txt"
	payload := []byte("integration test payload")

	if err := store.Put(context.Background(), key, bytes.NewReader(payload), PutOptions{
		ContentType:   "text/plain",
		ContentLength: int64(len(payload)),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	defer store.Delete(context.Background(), key)

	exists, err := store.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("Exists = false, want true")
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
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}
}

func envFirst(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
