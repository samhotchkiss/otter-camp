package storage

import (
	"fmt"
	"strings"
)

type Config struct {
	Backend string
	FSRoot  string

	S3Bucket          string
	S3Endpoint        string
	S3Region          string
	S3AccessKeyID     string
	S3SecretAccessKey string
}

func ConfigFromEnv(lookup func(string) (string, bool)) Config {
	cfg := Config{
		Backend: strings.TrimSpace(getEnv(lookup, "OTTERCAMP_STORAGE_BACKEND")),
		FSRoot:  strings.TrimSpace(getEnv(lookup, "OTTERCAMP_STORAGE_ROOT")),

		S3Bucket:          strings.TrimSpace(getEnv(lookup, "OTTERCAMP_S3_BUCKET")),
		S3Endpoint:        strings.TrimSpace(getEnv(lookup, "OTTERCAMP_S3_ENDPOINT")),
		S3Region:          strings.TrimSpace(getEnv(lookup, "OTTERCAMP_S3_REGION")),
		S3AccessKeyID:     strings.TrimSpace(getEnv(lookup, "OTTERCAMP_S3_ACCESS_KEY_ID")),
		S3SecretAccessKey: strings.TrimSpace(getEnv(lookup, "OTTERCAMP_S3_SECRET_ACCESS_KEY")),
	}
	return cfg.withDefaults()
}

func New(cfg Config) (Store, error) {
	cfg = cfg.withDefaults()

	switch cfg.Backend {
	case BackendFS:
		store, err := NewFS(cfg.FSRoot)
		if err != nil {
			return nil, err
		}
		return store, nil
	case BackendS3:
		if err := validateS3Config(cfg); err != nil {
			return nil, err
		}

		store, err := NewS3(cfg)
		if err != nil {
			return nil, err
		}
		return store, nil
	default:
		return nil, &ConfigError{
			Field:   "OTTERCAMP_STORAGE_BACKEND",
			Message: fmt.Sprintf("unsupported backend %q (expected fs|s3)", cfg.Backend),
		}
	}
}

func validateS3Config(cfg Config) error {
	required := []struct {
		value string
		name  string
	}{
		{cfg.S3Bucket, "OTTERCAMP_S3_BUCKET"},
		{cfg.S3Region, "OTTERCAMP_S3_REGION"},
		{cfg.S3AccessKeyID, "OTTERCAMP_S3_ACCESS_KEY_ID"},
		{cfg.S3SecretAccessKey, "OTTERCAMP_S3_SECRET_ACCESS_KEY"},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return &ConfigError{
				Field:   item.name,
				Message: "is required when OTTERCAMP_STORAGE_BACKEND=s3",
			}
		}
	}
	return nil
}

func (cfg Config) withDefaults() Config {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" {
		backend = BackendFS
	}
	cfg.Backend = backend

	if strings.TrimSpace(cfg.FSRoot) == "" {
		cfg.FSRoot = defaultStorageRoot
	}

	return cfg
}

func getEnv(lookup func(string) (string, bool), key string) string {
	if lookup == nil {
		return ""
	}
	value, ok := lookup(key)
	if !ok {
		return ""
	}
	return value
}
