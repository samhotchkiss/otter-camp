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

type FSAdapter struct {
	root string
}

func NewFS(root string) *FSAdapter {
	if strings.TrimSpace(root) == "" {
		root = defaultFSRoot
	}
	return &FSAdapter{root: root}
}

func (a *FSAdapter) Put(_ context.Context, key string, r io.Reader, opts PutOptions) (retErr error) {
	if err := validateKey(key); err != nil {
		return err
	}

	target := a.pathForKey(key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("storage fs put mkdir: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("storage fs put create temp: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpFile.Name())
		}
	}()

	written, err := io.Copy(tmpFile, r)
	if err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("storage fs put write temp: %w", err)
	}

	if opts.ContentLength >= 0 && written != opts.ContentLength {
		_ = tmpFile.Close()
		return fmt.Errorf("storage fs put content length mismatch: wrote %d bytes, expected %d", written, opts.ContentLength)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("storage fs put close temp: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), target); err != nil {
		return fmt.Errorf("storage fs put rename: %w", err)
	}

	return nil
}

func (a *FSAdapter) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	f, err := os.Open(a.pathForKey(key))
	if err != nil {
		return nil, fmt.Errorf("storage fs get: %w", err)
	}
	return f, nil
}

func (a *FSAdapter) Delete(_ context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	err := os.Remove(a.pathForKey(key))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("storage fs delete: %w", err)
}

func (a *FSAdapter) Exists(_ context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}

	_, err := os.Stat(a.pathForKey(key))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("storage fs exists: %w", err)
}

func (a *FSAdapter) List(_ context.Context, prefix string) ([]ObjectMeta, error) {
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}

	if _, err := os.Stat(a.root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("storage fs list root stat: %w", err)
	}

	metas := make([]ObjectMeta, 0)
	err := filepath.WalkDir(a.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(a.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		metas = append(metas, ObjectMeta{
			Key:          key,
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("storage fs list: %w", err)
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Key < metas[j].Key
	})

	return metas, nil
}

func (a *FSAdapter) pathForKey(key string) string {
	return filepath.Join(a.root, filepath.FromSlash(key))
}
