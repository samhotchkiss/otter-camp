package native

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var utf8ReplacementBytes = []byte("\uFFFD")

type listedEntry struct {
	name       string
	path       string
	entryType  string
	sizeBytes  int64
	modifiedAt string
}

func (e *NativeToolExecutor) handleFileRead(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	pathInput, ok := readString(input, "path")
	if !ok || pathInput == "" {
		return map[string]any{"error": "not_found"}, nil
	}

	realPath, err := e.resolveExistingPath(wd, pathInput)
	if err != nil {
		switch {
		case errors.Is(err, ErrPathTraversal):
			return map[string]any{"error": "path_traversal"}, nil
		case errors.Is(err, fs.ErrNotExist):
			return map[string]any{"error": "not_found"}, nil
		default:
			return nil, err
		}
	}
	if !isWithinRoot(wd.Root(), realPath) {
		return map[string]any{"error": "path_traversal"}, nil
	}

	maxBytes := clamp(readInt(input, "max_bytes", defaultReadMaxBytes), 1, hardReadMaxBytes)
	encoding := "utf8"
	if raw, ok := readString(input, "encoding"); ok && strings.EqualFold(raw, "base64") {
		encoding = "base64"
	}

	file, err := os.Open(realPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	limited := io.LimitReader(file, int64(maxBytes)+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	truncated := false
	if len(payload) > maxBytes {
		payload = payload[:maxBytes]
		truncated = true
	}

	content := sanitizeUTF8TextBytes(payload)
	if encoding == "base64" {
		content = base64.StdEncoding.EncodeToString(payload)
	}

	return map[string]any{
		"content":   content,
		"encoding":  encoding,
		"byte_size": stat.Size(),
		"truncated": truncated,
		"path":      renderPath(wd.Root(), resolved),
	}, nil
}

func (e *NativeToolExecutor) handleFileList(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	pattern := "*"
	if raw, ok := readString(input, "pattern"); ok && raw != "" {
		pattern = raw
	}
	recursive := readBool(input, "recursive", false)

	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}

	entries := make([]listedEntry, 0)
	truncated := false
	baseHidden := strings.HasPrefix(filepath.Base(resolved), ".")
	collect := func(entryPath string, d fs.DirEntry) error {
		if d.IsDir() && recursive && entryPath != resolved {
			if strings.HasPrefix(d.Name(), ".") && !baseHidden {
				return filepath.SkipDir
			}
		}
		if entryPath == resolved && d.IsDir() {
			return nil
		}
		relFromInput, relErr := filepath.Rel(resolved, entryPath)
		if relErr != nil {
			return relErr
		}
		relFromInput = filepath.ToSlash(relFromInput)
		if relFromInput == "." {
			relFromInput = d.Name()
		}
		if !globMatch(pattern, relFromInput) {
			return nil
		}

		entryInfo, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		entryType := "file"
		size := entryInfo.Size()
		if d.IsDir() {
			entryType = "directory"
			size = 0
		}
		entries = append(entries, listedEntry{
			name:       d.Name(),
			path:       renderPath(wd.Root(), entryPath),
			entryType:  entryType,
			sizeBytes:  size,
			modifiedAt: formatTime(entryInfo.ModTime()),
		})
		if len(entries) >= defaultListMaxEntries {
			truncated = true
			return io.EOF
		}
		return nil
	}

	if !info.IsDir() {
		d := fs.FileInfoToDirEntry(info)
		if err := collect(resolved, d); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	} else if recursive {
		err := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			return collect(path, d)
		})
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	} else {
		dirs, err := os.ReadDir(resolved)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			if err := collect(filepath.Join(resolved, d.Name()), d); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				if errors.Is(err, filepath.SkipDir) {
					continue
				}
				return nil, err
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})
	payload := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, map[string]any{
			"name":        entry.name,
			"path":        entry.path,
			"type":        entry.entryType,
			"size_bytes":  entry.sizeBytes,
			"modified_at": entry.modifiedAt,
		})
	}

	result := map[string]any{
		"entries": payload,
		"total":   len(payload),
	}
	if truncated {
		result["truncated"] = true
	}
	return result, nil
}

func (e *NativeToolExecutor) handleFileSearch(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, resolved, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	pattern, ok := readString(input, "pattern")
	if !ok || pattern == "" {
		return map[string]any{"matches": []any{}, "total_matches": 0, "truncated": false}, nil
	}
	if readBool(input, "case_insensitive", false) {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	filePattern := "*"
	if raw, ok := readString(input, "file_pattern"); ok && raw != "" {
		filePattern = raw
	}
	recursive := readBool(input, "recursive", true)
	maxResults := clamp(readInt(input, "max_results", defaultSearchMaxResult), 1, hardSearchMaxResult)

	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}

	matches := make([]map[string]any, 0)
	totalMatches := 0
	truncated := false

	searchFile := func(filePath string) error {
		realPath, err := filepath.EvalSymlinks(filePath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !isWithinRoot(wd.Root(), realPath) {
			return nil
		}

		relPath, relErr := filepath.Rel(resolved, filePath)
		if relErr != nil {
			return relErr
		}
		relPath = filepath.ToSlash(relPath)
		if !globMatch(filePattern, relPath) {
			return nil
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil
		}
		lines := splitLines(data)
		for idx, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			totalMatches++
			if len(matches) >= maxResults {
				truncated = true
				continue
			}
			beforeStart := idx - 2
			if beforeStart < 0 {
				beforeStart = 0
			}
			afterEnd := idx + 3
			if afterEnd > len(lines) {
				afterEnd = len(lines)
			}
			before := append([]string(nil), lines[beforeStart:idx]...)
			after := append([]string(nil), lines[idx+1:afterEnd]...)
			matches = append(matches, map[string]any{
				"file":           renderPath(wd.Root(), filePath),
				"line_number":    idx + 1,
				"line_content":   line,
				"context_before": before,
				"context_after":  after,
			})
		}
		return nil
	}

	if !info.IsDir() {
		if err := searchFile(resolved); err != nil {
			return nil, err
		}
	} else if recursive {
		err := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if path != resolved && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			return searchFile(path)
		})
		if err != nil {
			return nil, err
		}
	} else {
		dirs, err := os.ReadDir(resolved)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			if d.IsDir() {
				continue
			}
			if err := searchFile(filepath.Join(resolved, d.Name())); err != nil {
				return nil, err
			}
		}
	}

	return map[string]any{
		"matches":       matches,
		"total_matches": totalMatches,
		"truncated":     truncated,
	}, nil
}

func globMatch(pattern, candidate string) bool {
	pat := filepath.ToSlash(strings.TrimSpace(pattern))
	cand := filepath.ToSlash(strings.TrimSpace(candidate))
	if cand == "" || cand == "." {
		cand = path.Base(cand)
	}
	if pat == "" || pat == "*" {
		return true
	}
	if strings.Contains(pat, "**") {
		escaped := regexp.QuoteMeta(pat)
		escaped = strings.ReplaceAll(escaped, `\*\*`, ".*")
		escaped = strings.ReplaceAll(escaped, `\*`, "[^/]*")
		escaped = strings.ReplaceAll(escaped, `\?`, ".")
		re := regexp.MustCompile("^" + escaped + "$")
		return re.MatchString(cand)
	}
	matched, err := path.Match(pat, cand)
	if err == nil && matched {
		return true
	}
	matched, err = path.Match(pat, path.Base(cand))
	return err == nil && matched
}

func splitLines(data []byte) []string {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ToValidUTF8(normalized, utf8ReplacementBytes)
	scanner := bufio.NewScanner(bytes.NewReader(normalized))
	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func sanitizeUTF8TextBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return string(bytes.ToValidUTF8(data, utf8ReplacementBytes))
}
