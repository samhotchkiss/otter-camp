package storage

import (
	"context"
	"io"
	"time"
)

// Store abstracts object storage operations used across OtterCamp domains.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	List(ctx context.Context, prefix string) ([]ObjectMeta, error)
}

type PutOptions struct {
	ContentType   string
	ContentLength int64 // -1 means unknown
}

type ObjectMeta struct {
	Key          string
	Size         int64
	LastModified time.Time
}
