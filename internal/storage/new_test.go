package storage

import (
	"errors"
	"testing"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg := ConfigFromEnv(func(string) (string, bool) { return "", false })

	if cfg.Backend != BackendFS {
		t.Fatalf("Backend = %q, want %q", cfg.Backend, BackendFS)
	}
	if cfg.FSRoot != defaultFSRoot {
		t.Fatalf("FSRoot = %q, want %q", cfg.FSRoot, defaultFSRoot)
	}
}

func TestConfigFromEnvValues(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"OTTERCAMP_STORAGE_BACKEND":      "s3",
		"OTTERCAMP_STORAGE_ROOT":         "/tmp/objects",
		"OTTERCAMP_S3_BUCKET":            "bucket",
		"OTTERCAMP_S3_ENDPOINT":          "http://localhost:9000",
		"OTTERCAMP_S3_REGION":            "us-east-1",
		"OTTERCAMP_S3_ACCESS_KEY_ID":     "ak",
		"OTTERCAMP_S3_SECRET_ACCESS_KEY": "sk",
	})

	cfg := ConfigFromEnv(lookup)
	if cfg.Backend != BackendS3 {
		t.Fatalf("Backend = %q, want %q", cfg.Backend, BackendS3)
	}
	if cfg.FSRoot != "/tmp/objects" {
		t.Fatalf("FSRoot = %q, want %q", cfg.FSRoot, "/tmp/objects")
	}
	if cfg.S3Bucket != "bucket" || cfg.S3Endpoint != "http://localhost:9000" || cfg.S3Region != "us-east-1" {
		t.Fatalf("unexpected s3 config: %+v", cfg)
	}
}

func TestNewDefaultsToFSAdapter(t *testing.T) {
	store, err := New(Config{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if _, ok := store.(*FSAdapter); !ok {
		t.Fatalf("store type = %T, want *FSAdapter", store)
	}
}

func TestNewReturnsConfigErrorForMissingS3Vars(t *testing.T) {
	_, err := New(Config{Backend: BackendS3})
	if err == nil {
		t.Fatal("expected missing s3 config error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}
}

func TestNewBuildsS3AdapterWhenConfigComplete(t *testing.T) {
	store, err := New(Config{
		Backend:           BackendS3,
		S3Bucket:          "bucket",
		S3Region:          "us-east-1",
		S3AccessKeyID:     "access",
		S3SecretAccessKey: "secret",
		S3Endpoint:        "http://localhost:9000",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if _, ok := store.(*S3Adapter); !ok {
		t.Fatalf("store type = %T, want *S3Adapter", store)
	}
}

func TestNewRejectsUnknownBackend(t *testing.T) {
	_, err := New(Config{Backend: "blob"})
	if err == nil {
		t.Fatal("expected unknown backend error")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}
