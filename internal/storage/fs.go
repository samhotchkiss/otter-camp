package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FSStore struct {
	root string
}

func NewFS(root string) (*FSStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, &ConfigError{Field: "OTTERCAMP_STORAGE_ROOT", Message: "must not be empty"}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}

	return &FSStore{root: absRoot}, nil
}

func (s *FSStore) Put(_ context.Context, key string, r io.Reader, _ PutOptions) error {
	path, err := s.fullPath(key)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".ottercamp-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, r); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}
	cleanup = false
	return nil
}

func (s *FSStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := s.fullPath(key)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}
	return file, nil
}

func (s *FSStore) Delete(_ context.Context, key string) error {
	path, err := s.fullPath(key)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (s *FSStore) Exists(_ context.Context, key string) (bool, error) {
	path, err := s.fullPath(key)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("stat object: %w", err)
	}
}

func (s *FSStore) List(_ context.Context, prefix string) ([]ObjectMeta, error) {
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}

	startDir := s.root
	if strings.TrimSpace(prefix) != "" {
		var err error
		startDir, err = s.fullPath(prefix)
		if err != nil {
			return nil, err
		}
	}

	entries := make([]ObjectMeta, 0, 16)
	err := filepath.WalkDir(startDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relPath)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, ObjectMeta{
			Key:          key,
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ObjectMeta{}, nil
		}
		return nil, fmt.Errorf("walk objects: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})

	return entries, nil
}

func (s *FSStore) fullPath(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}

	path := filepath.Join(s.root, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return "", fmt.Errorf("resolve key path: %w", err)
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", &InvalidKeyError{Key: key, Reason: "path escapes storage root"}
	}

	return path, nil
}
