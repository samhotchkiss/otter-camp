package memory

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type EllieFileJSONLScanner struct {
	RootDir string
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

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	results := make([]EllieRetrievedItem, 0, limit)
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if len(results) >= limit {
			return fs.SkipAll
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNumber := 0
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			lineNumber += 1
			line := scanner.Text()
			if !strings.Contains(strings.ToLower(line), query) {
				continue
			}
			results = append(results, EllieRetrievedItem{
				Tier:    4,
				Source:  "jsonl",
				ID:      fmt.Sprintf("%s:%d", path, lineNumber),
				Snippet: line,
			})
			if len(results) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return results, nil
}
