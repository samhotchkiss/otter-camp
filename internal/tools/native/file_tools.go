package native

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
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
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
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

	renderedPath := renderPath(wd.Root(), resolved)
	if reject, blocked, rejectErr := e.rejectRecoveryTargetReread(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectExecutionFirstDeliverableReread(ctx, wd, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectPlaceholderDeliverableRead(ctx, scope, renderedPath, content); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectMismatchedTaskDeliverableRead(ctx, scope, renderedPath, content); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}

	return map[string]any{
		"content":   content,
		"encoding":  encoding,
		"byte_size": stat.Size(),
		"truncated": truncated,
		"path":      renderedPath,
	}, nil
}

func (e *NativeToolExecutor) rejectPlaceholderDeliverableRead(ctx context.Context, scope workspaceScope, relativePath, content string) (map[string]any, bool, error) {
	if e == nil || e.tasks == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok || !strings.EqualFold(strings.TrimSpace(plan.Mode), taskplan.ModeExecutionFirst) {
		return nil, false, nil
	}
	deliverablePath := parseExplicitDeliverablePath(taskRecord)
	if deliverablePath == "" {
		deliverablePath = e.latestRecoveryTargetPathForSession(ctx, scope)
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" {
		return nil, false, nil
	}
	if !looksLikeRejectedDeliverablePlaceholder(content) && !looksLikeDeliverableCompletionSummaryWithoutBody(normalizedPath, content) {
		return nil, false, nil
	}
	if deliverablePath == "" {
		deliverablePath = parsePlaceholderDeliverableTarget(content)
	}
	if deliverablePath == "" || !sameOrNestedWorkspacePath(normalizedPath, deliverablePath) {
		return nil, false, nil
	}
	message := fmt.Sprintf("The explicit deliverable `%s` currently contains rejected placeholder or status narration. Do not reread `%s` for context. Overwrite it directly with the real deliverable body instead.", deliverablePath, normalizedPath)
	if workspacePathLooksDirectory(deliverablePath) {
		message = fmt.Sprintf("A file under the explicit deliverable directory `%s/` currently contains rejected placeholder or status narration. Do not reread `%s` for context. Overwrite it directly with the real deliverable body instead.", strings.TrimSuffix(deliverablePath, "/"), normalizedPath)
	}
	return map[string]any{
		"error":            "placeholder_deliverable",
		"deliverable_path": deliverablePath,
		"message":          message,
	}, true, nil
}

func (e *NativeToolExecutor) rejectMismatchedTaskDeliverableRead(ctx context.Context, scope workspaceScope, relativePath, content string) (map[string]any, bool, error) {
	if e == nil || e.tasks == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	targetPath := parseExplicitDeliverablePath(taskRecord)
	if targetPath == "" {
		if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok {
			targetPath = normalizeWorkspacePath(checkpoint.TargetPath)
		}
	}
	if targetPath == "" {
		targetPath = e.latestRecoveryTargetPathForSession(ctx, scope)
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" || targetPath == "" || !sameOrNestedWorkspacePath(normalizedPath, targetPath) {
		return nil, false, nil
	}
	if !taskDraftSemanticallyMismatchesScope(taskRecord, content) {
		return nil, false, nil
	}
	return map[string]any{
		"error":            "mismatched_deliverable_context",
		"deliverable_path": targetPath,
		"message": fmt.Sprintf(
			"The current deliverable `%s` contains content that does not match this task's scope. Do not reuse `%s` as context; overwrite it with content that matches the current task title and description.",
			targetPath,
			normalizedPath,
		),
	}, true, nil
}

func (e *NativeToolExecutor) rejectExecutionFirstDeliverableReread(ctx context.Context, wd SessionWorkDir, scope workspaceScope, relativePath string) (map[string]any, bool, error) {
	if e == nil || e.tasks == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	taskRecord, err := e.tasks.GetByID(ctx, *scope.taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
		return nil, false, nil
	}
	plan, ok := taskplan.Parse(taskRecord.Metadata)
	if !ok || !strings.EqualFold(strings.TrimSpace(plan.Mode), taskplan.ModeExecutionFirst) {
		return nil, false, nil
	}
	deliverablePath := parseExplicitDeliverablePath(taskRecord)
	if deliverablePath == "" {
		return nil, false, nil
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" || sameOrNestedWorkspacePath(normalizedPath, deliverablePath) {
		return nil, false, nil
	}
	exists, err := substantiveExplicitDeliverableExists(wd.Root(), deliverablePath)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	return map[string]any{
		"error":            "explicit_deliverable_focus_required",
		"deliverable_path": deliverablePath,
		"message": fmt.Sprintf(
			"This execution-first task already has a substantive explicit deliverable `%s`. Do not reread `%s` now. Revise the deliverable directly if needed, or stop and let the runtime advance the flow.",
			deliverablePath,
			normalizedPath,
		),
	}, true, nil
}

func (e *NativeToolExecutor) rejectRecoveryTargetReread(ctx context.Context, scope workspaceScope, relativePath string) (map[string]any, bool, error) {
	if e == nil || scope.taskID == nil || *scope.taskID == uuid.Nil {
		return nil, false, nil
	}
	targetPath := e.latestRecoveryTargetPathForSession(ctx, scope)
	if targetPath == "" {
		return nil, false, nil
	}
	normalizedPath := normalizeWorkspacePath(relativePath)
	if normalizedPath == "" || sameOrNestedWorkspacePath(normalizedPath, targetPath) {
		return nil, false, nil
	}
	return map[string]any{
		"error":            "recovery_target_focus_required",
		"deliverable_path": targetPath,
		"message": fmt.Sprintf(
			"Recovery already identified `%s` as the target deliverable. Do not reread `%s` now. Continue from the recovery target and write the deliverable body directly.",
			targetPath,
			normalizedPath,
		),
	}, true, nil
}

func substantiveExplicitDeliverableExists(workspaceRoot, deliverablePath string) (bool, error) {
	normalized := normalizeWorkspacePath(deliverablePath)
	if normalized == "" {
		return false, nil
	}
	absPath := filepath.Join(workspaceRoot, filepath.FromSlash(normalized))
	if workspacePathLooksDirectory(normalized) {
		found := false
		walkErr := filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return filepath.SkipDir
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				return statErr
			}
			if info.Size() > 0 {
				found = true
				return io.EOF
			}
			return nil
		})
		if errors.Is(walkErr, io.EOF) {
			return true, nil
		}
		if errors.Is(walkErr, fs.ErrNotExist) {
			return false, nil
		}
		if walkErr != nil {
			return false, walkErr
		}
		return found, nil
	}
	info, err := os.Stat(absPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size() > 0, nil
}

func (e *NativeToolExecutor) latestRecoveryTargetPathForSession(ctx context.Context, scope workspaceScope) string {
	if e == nil || e.messages == nil || scope.sessionID == nil || *scope.sessionID == uuid.Nil {
		return ""
	}
	messages, err := e.messages.ListBySession(ctx, *scope.sessionID)
	if err != nil {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "system") {
			continue
		}
		if target := parseRecoveryTargetPath(messages[i].Content); target != "" {
			return target
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "tool_result") {
			continue
		}
		if target := parseRecentDeliverableTargetFromToolResult(messages[i].Content); target != "" {
			return target
		}
	}
	return ""
}

func parseRecentDeliverableTargetFromToolResult(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return ""
	}
	if payload == nil {
		return ""
	}
	output, _ := payload["output"].(map[string]any)
	if output != nil {
		if target := normalizeWorkspacePath(readStringValue(output["deliverable_path"])); target != "" && looksLikeExplicitDeliverablePath(target, readStringValue(output["deliverable_path"])) {
			return target
		}
	}
	toolName := strings.ToLower(strings.TrimSpace(readStringValue(payload["tool_name"])))
	switch toolName {
	case "file.read", "file.write":
		if output != nil {
			if target := normalizeWorkspacePath(readStringValue(output["path"])); target != "" && looksLikeExplicitDeliverablePath(target, readStringValue(output["path"])) {
				return target
			}
		}
	}
	return ""
}

func parseRecoveryTargetPath(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Target file:") {
			continue
		}
		target := strings.TrimSpace(strings.TrimPrefix(trimmed, "Target file:"))
		normalized := normalizeWorkspacePath(target)
		if !looksLikeExplicitDeliverablePath(normalized, target) {
			return ""
		}
		return normalized
	}
	return ""
}

func parsePlaceholderDeliverableTarget(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "**Deliverable Target**:") && !strings.HasPrefix(trimmed, "Deliverable Target:") {
			continue
		}
		target := strings.TrimSpace(strings.TrimPrefix(trimmed, "**Deliverable Target**:"))
		target = strings.TrimSpace(strings.TrimPrefix(target, "Deliverable Target:"))
		return normalizeWorkspacePath(target)
	}
	return ""
}

func looksLikeRejectedDeliverablePlaceholder(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || len(trimmed) > 4000 {
		return false
	}
	lower := strings.ToLower(trimmed)
	if looksLikeNarratedTaskFileWritePlaceholder(trimmed) {
		return true
	}
	if strings.Contains(lower, "# ready to continue oc-") &&
		(strings.Contains(lower, "deliverable target:") || strings.Contains(lower, "**deliverable target**:")) &&
		(strings.Contains(lower, "what i need from you:") || strings.Contains(lower, "**what i need from you**:")) {
		return true
	}
	if strings.Contains(lower, "**status**: task oc-") &&
		strings.Contains(lower, "currently **in_progress**") &&
		strings.Contains(lower, "target deliverable file is just a placeholder") {
		return true
	}
	if strings.Contains(lower, "task execution is already underway. reuse the existing workspace files") {
		return true
	}
	if strings.Contains(lower, "i don't see a durable draft") &&
		(strings.Contains(lower, "please provide the substantive draft") || strings.Contains(lower, "please provide the recovery artifact")) {
		return true
	}
	if (strings.Contains(lower, "what i need from you:") || strings.Contains(lower, "**what i need from you**:")) &&
		(strings.Contains(lower, "should i proceed") || strings.Contains(lower, "do you want me to")) {
		return true
	}
	return false
}

func (e *NativeToolExecutor) handleFileList(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
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
	renderedPath := renderPath(wd.Root(), resolved)
	if reject, blocked, rejectErr := e.rejectRecoveryTargetReread(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
	}
	if reject, blocked, rejectErr := e.rejectExecutionFirstDeliverableReread(ctx, wd, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
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
	wd, scope, resolved, err := e.resolveInputPath(ctx, input, "path")
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
	renderedPath := renderPath(wd.Root(), resolved)
	if reject, blocked, rejectErr := e.rejectRecoveryTargetReread(ctx, scope, renderedPath); rejectErr != nil {
		return nil, rejectErr
	} else if blocked {
		reject["path"] = renderedPath
		return reject, nil
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
		renderedPath := renderPath(wd.Root(), filePath)
		if _, blocked, rejectErr := e.rejectPlaceholderDeliverableRead(ctx, scope, renderedPath, sanitizeUTF8TextBytes(data)); rejectErr != nil {
			return rejectErr
		} else if blocked {
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
				"file":           renderedPath,
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
