package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func New(cfg Config) (Store, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" {
		backend = defaultBackend
	}

	switch backend {
	case BackendFS:
		return NewFS(cfg.FSRoot), nil
	case BackendS3:
		if err := validateS3Config(cfg); err != nil {
			return nil, err
		}

		client, err := newS3Client(cfg)
		if err != nil {
			return nil, err
		}

		return NewS3(client, cfg.S3Bucket), nil
	default:
		return nil, &ConfigError{Backend: backend, Reason: "unsupported backend (expected fs|s3)"}
	}
}

func validateS3Config(cfg Config) error {
	required := map[string]string{
		"OTTERCAMP_S3_BUCKET":            cfg.S3Bucket,
		"OTTERCAMP_S3_REGION":            cfg.S3Region,
		"OTTERCAMP_S3_ACCESS_KEY_ID":     cfg.S3AccessKeyID,
		"OTTERCAMP_S3_SECRET_ACCESS_KEY": cfg.S3SecretAccessKey,
	}

	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return &ConfigError{Backend: BackendS3, Field: field, Reason: "required environment variable is not set"}
		}
	}

	return nil
}

func newS3Client(cfg Config) (*s3.Client, error) {
	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.S3AccessKeyID, cfg.S3SecretAccessKey, "")),
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("storage: load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.S3Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
			o.UsePathStyle = true
		}
	})

	if client == nil {
		return nil, errors.New("storage: failed to create s3 client")
	}

	return client, nil
}
