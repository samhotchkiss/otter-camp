package storage

import (
	"os"
	"strings"
)

const (
	BackendFS = "fs"
	BackendS3 = "s3"

	defaultBackend = BackendFS
	defaultFSRoot  = "./data/objects"
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
	if lookup == nil {
		lookup = os.LookupEnv
	}

	cfg := Config{
		Backend: defaultBackend,
		FSRoot:  defaultFSRoot,
	}

	if backend, ok := lookup("OTTERCAMP_STORAGE_BACKEND"); ok && strings.TrimSpace(backend) != "" {
		cfg.Backend = strings.ToLower(strings.TrimSpace(backend))
	}
	if root, ok := lookup("OTTERCAMP_STORAGE_ROOT"); ok && strings.TrimSpace(root) != "" {
		cfg.FSRoot = strings.TrimSpace(root)
	}

	cfg.S3Bucket = lookupTrimmed(lookup, "OTTERCAMP_S3_BUCKET")
	cfg.S3Endpoint = lookupTrimmed(lookup, "OTTERCAMP_S3_ENDPOINT")
	cfg.S3Region = lookupTrimmed(lookup, "OTTERCAMP_S3_REGION")
	cfg.S3AccessKeyID = lookupTrimmed(lookup, "OTTERCAMP_S3_ACCESS_KEY_ID")
	cfg.S3SecretAccessKey = lookupTrimmed(lookup, "OTTERCAMP_S3_SECRET_ACCESS_KEY")

	return cfg
}

func lookupTrimmed(lookup func(string) (string, bool), key string) string {
	v, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}
