package storage

import (
	"errors"
	"testing"
)

func TestNewDefaultsToFilesystemAdapter(t *testing.T) {
	store, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, ok := store.(*FSStore); !ok {
		t.Fatalf("store type = %T, want *FSStore", store)
	}
}

func TestNewS3MissingRequiredConfigReturnsTypedError(t *testing.T) {
	_, err := New(Config{Backend: BackendS3})
	if err == nil {
		t.Fatal("expected config error for missing s3 vars")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}
}

func TestConfigFromEnvDefaultsBackendAndRoot(t *testing.T) {
	cfg := ConfigFromEnv(func(string) (string, bool) { return "", false })
	if cfg.Backend != BackendFS {
		t.Fatalf("Backend = %q, want %q", cfg.Backend, BackendFS)
	}
	if cfg.FSRoot != defaultStorageRoot {
		t.Fatalf("FSRoot = %q, want %q", cfg.FSRoot, defaultStorageRoot)
	}
}
