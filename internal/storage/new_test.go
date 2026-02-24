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

func TestNewS3AllowsOptionalEndpoint(t *testing.T) {
	_, err := New(Config{
		Backend:           BackendS3,
		S3Bucket:          "bucket-1",
		S3Region:          "us-east-1",
		S3AccessKeyID:     "access-key",
		S3SecretAccessKey: "secret-key",
		// S3Endpoint intentionally omitted; it is optional.
	})
	if err != nil {
		t.Fatalf("New with optional endpoint omitted: %v", err)
	}
}

func TestNewS3MissingRequiredFieldReportsSpecificField(t *testing.T) {
	base := Config{
		Backend:           BackendS3,
		S3Bucket:          "bucket-1",
		S3Region:          "us-east-1",
		S3AccessKeyID:     "access-key",
		S3SecretAccessKey: "secret-key",
	}

	tests := []struct {
		name      string
		fieldName string
		mutate    func(cfg *Config)
	}{
		{
			name:      "missing bucket",
			fieldName: "OTTERCAMP_S3_BUCKET",
			mutate: func(cfg *Config) {
				cfg.S3Bucket = ""
			},
		},
		{
			name:      "missing region",
			fieldName: "OTTERCAMP_S3_REGION",
			mutate: func(cfg *Config) {
				cfg.S3Region = ""
			},
		},
		{
			name:      "missing access key id",
			fieldName: "OTTERCAMP_S3_ACCESS_KEY_ID",
			mutate: func(cfg *Config) {
				cfg.S3AccessKeyID = ""
			},
		},
		{
			name:      "missing secret access key",
			fieldName: "OTTERCAMP_S3_SECRET_ACCESS_KEY",
			mutate: func(cfg *Config) {
				cfg.S3SecretAccessKey = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)

			_, err := New(cfg)
			if err == nil {
				t.Fatal("expected error")
			}

			var cfgErr *ConfigError
			if !errors.As(err, &cfgErr) {
				t.Fatalf("error type = %T, want *ConfigError", err)
			}
			if cfgErr.Field != tt.fieldName {
				t.Fatalf("ConfigError.Field = %q, want %q", cfgErr.Field, tt.fieldName)
			}
		})
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
