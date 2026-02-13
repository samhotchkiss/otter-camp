package memory

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultEllieJSONLMaxLineBytes    = 256 * 1024
	defaultEllieJSONLMaxBytesScanned = 8 * 1024 * 1024
	defaultEllieJSONLSnippetMaxBytes = 1024
)

type EllieFileJSONLScanner struct {
	RootDir         string
	MaxLineBytes    int
	MaxBytesScanned int
}

func (s *EllieFileJSONLScanner) Scan(ctx context.Context, input EllieJSONLScanInput) ([]EllieRetrievedItem, error) {
	query := strings.TrimSpace(strings.ToLower(input.Query))
	if query == "" {
		return []EllieRetrievedItem{}, nil
	}
	rootDir := strings.TrimSpace(s.RootDir)
	if rootDir == "" {
		return []EllieRetrievedItem{}, nil
	}
	rootDir, err := normalizeJSONLRoot(rootDir)
	if err != nil {
		return nil, err
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}
	maxLineBytes := s.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = defaultEllieJSONLMaxLineBytes
	}
	maxBytesScanned := s.MaxBytesScanned
	if maxBytesScanned <= 0 {
		maxBytesScanned = defaultEllieJSONLMaxBytesScanned
	}

	results := make([]EllieRetrievedItem, 0, limit)
	bytesScanned := 0
	exhaustedScanBudget := false
	err = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if exhaustedScanBudget {
			return fs.SkipAll
		}
		if len(results) >= limit {
			return fs.SkipAll
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return nil
		}

		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil
		}
		if err := ensurePathWithinRoot(rootDir, resolvedPath); err != nil {
			return fmt.Errorf("jsonl scanner path %q escapes root: %w", path, err)
		}

		file, err := os.Open(resolvedPath)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 4096), maxLineBytes)
		lineNumber := 0
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			lineBytes := len(scanner.Bytes()) + 1
			if maxBytesScanned > 0 && bytesScanned+lineBytes > maxBytesScanned {
				exhaustedScanBudget = true
				break
			}
			bytesScanned += lineBytes

			lineNumber += 1
			line := scanner.Text()
			if !strings.Contains(strings.ToLower(line), query) {
				continue
			}
			results = append(results, EllieRetrievedItem{
				Tier:    4,
				Source:  "jsonl",
				ID:      formatJSONLItemID(rootDir, resolvedPath, lineNumber),
				Snippet: truncateJSONLSnippet(line),
			})
			if len(results) >= limit {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			if errors.Is(err, bufio.ErrTooLong) {
				return nil
			}
			return err
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return results, nil
}

func normalizeJSONLRoot(rootDir string) (string, error) {
	cleanRoot := filepath.Clean(rootDir)
	absRoot, err := filepath.Abs(cleanRoot)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", err
	}
	return resolvedRoot, nil
}

func ensurePathWithinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("path %q is outside %q", path, root)
	}
	return nil
}

func formatJSONLItemID(rootDir, filePath string, lineNumber int) string {
	rel, err := filepath.Rel(rootDir, filePath)
	if err != nil {
		rel = filepath.Base(filePath)
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), lineNumber)
}

func truncateJSONLSnippet(line string) string {
	if len(line) <= defaultEllieJSONLSnippetMaxBytes {
		return line
	}
	return line[:defaultEllieJSONLSnippetMaxBytes]
}
