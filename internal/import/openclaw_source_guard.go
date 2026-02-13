package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type OpenClawSourceSnapshot struct {
	RootDir    string
	FileHashes map[string]string
}

type OpenClawSourceGuard struct {
	rootDir string
}

func NewOpenClawSourceGuard(rootDir string) (*OpenClawSourceGuard, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(rootDir))
	if cleanRoot == "" {
		return nil, fmt.Errorf("openclaw root dir is required")
	}

	info, err := os.Stat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("openclaw root dir %s: %w", cleanRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("openclaw root path is not a directory: %s", cleanRoot)
	}

	return &OpenClawSourceGuard{rootDir: cleanRoot}, nil
}

func (g *OpenClawSourceGuard) ValidateReadPath(path string) error {
	if g == nil {
		return fmt.Errorf("openclaw source guard is not configured")
	}
	target := filepath.Clean(strings.TrimSpace(path))
	if target == "" {
		return fmt.Errorf("path is required")
	}

	if !filepath.IsAbs(target) {
		target = filepath.Clean(filepath.Join(g.rootDir, target))
	}
	if !isWithinDir(g.rootDir, target) {
		return fmt.Errorf("path %s is outside openclaw root %s", target, g.rootDir)
	}
	return nil
}

func (g *OpenClawSourceGuard) RejectWritePath(path string) error {
	if err := g.ValidateReadPath(path); err != nil {
		return err
	}
	return fmt.Errorf("openclaw source is read-only: write rejected for %s", filepath.Clean(path))
}

func (g *OpenClawSourceGuard) CaptureSnapshot() (*OpenClawSourceSnapshot, error) {
	if g == nil {
		return nil, fmt.Errorf("openclaw source guard is not configured")
	}

	hashes := map[string]string{}
	err := filepath.WalkDir(g.rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d == nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if err := g.ValidateReadPath(path); err != nil {
			return err
		}
		relPath, err := filepath.Rel(g.rootDir, path)
		if err != nil {
			return err
		}
		hash, err := hashOpenClawFile(path)
		if err != nil {
			return err
		}
		hashes[filepath.ToSlash(relPath)] = hash
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("capture openclaw source snapshot: %w", err)
	}

	return &OpenClawSourceSnapshot{
		RootDir:    g.rootDir,
		FileHashes: hashes,
	}, nil
}

func (g *OpenClawSourceGuard) VerifyUnchanged(before *OpenClawSourceSnapshot) error {
	if g == nil {
		return fmt.Errorf("openclaw source guard is not configured")
	}
	if before == nil {
		return fmt.Errorf("baseline snapshot is required")
	}
	if filepath.Clean(before.RootDir) != g.rootDir {
		return fmt.Errorf("snapshot root mismatch: %s != %s", before.RootDir, g.rootDir)
	}

	after, err := g.CaptureSnapshot()
	if err != nil {
		return err
	}

	changes := make([]string, 0)
	for path, beforeHash := range before.FileHashes {
		afterHash, exists := after.FileHashes[path]
		if !exists {
			changes = append(changes, "removed:"+path)
			continue
		}
		if afterHash != beforeHash {
			changes = append(changes, "changed:"+path)
		}
	}
	for path := range after.FileHashes {
		if _, exists := before.FileHashes[path]; !exists {
			changes = append(changes, "added:"+path)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	sort.Strings(changes)
	return fmt.Errorf("openclaw source mutation detected (%d): %s", len(changes), strings.Join(changes, ", "))
}

func hashOpenClawFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
