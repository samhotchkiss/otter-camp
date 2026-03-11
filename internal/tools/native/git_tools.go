package native

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	branchAheadBehindRegex = regexp.MustCompile(`^([^\.\s]+)(?:\.\.\.[^\s]+)?(?: \[(.*)\])?$`)
	unbornBranchRegex      = regexp.MustCompile(`^No commits yet on ([^\s]+)$`)
)

func (e *NativeToolExecutor) handleGitStatus(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, dir, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}
	if !isWithinRoot(wd.Root(), dir) {
		return map[string]any{"error": "path_traversal"}, nil
	}

	out, err := e.runCommand(ctx, dir, "git", "status", "--porcelain=v1", "--branch")
	if err != nil {
		if isNotGitRepoError(out, err) {
			return map[string]any{"error": "not_a_git_repo"}, nil
		}
		return nil, err
	}

	lines := splitGitLines(out)
	branch := ""
	ahead := 0
	behind := 0
	files := make([]map[string]any, 0)
	for idx, line := range lines {
		if idx == 0 && strings.HasPrefix(line, "## ") {
			branch, ahead, behind = parseBranchState(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			continue
		}
		if strings.TrimSpace(line) == "" || len(line) < 3 {
			continue
		}
		statusCode := line[:2]
		filePath := strings.TrimSpace(line[3:])
		if strings.Contains(filePath, " -> ") {
			parts := strings.Split(filePath, " -> ")
			filePath = strings.TrimSpace(parts[len(parts)-1])
		}
		files = append(files, map[string]any{
			"path":   filepath.ToSlash(filePath),
			"status": parsePorcelainStatus(statusCode),
		})
	}

	return map[string]any{
		"branch":   branch,
		"ahead":    ahead,
		"behind":   behind,
		"files":    files,
		"is_dirty": len(files) > 0,
	}, nil
}

func (e *NativeToolExecutor) handleGitDiff(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, dir, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}

	common := make([]string, 0)
	if readBool(input, "staged", false) {
		common = append(common, "--cached")
	}
	if ref, ok := readString(input, "ref"); ok && ref != "" {
		common = append(common, ref)
	}

	fileArg := ""
	if raw, ok := readString(input, "file_path"); ok && raw != "" {
		resolved, resolveErr := wd.ResolvePath(raw)
		if resolveErr != nil {
			if errors.Is(resolveErr, ErrPathTraversal) {
				return map[string]any{"error": "path_traversal"}, nil
			}
			return nil, resolveErr
		}
		rel, relErr := filepath.Rel(dir, resolved)
		if relErr != nil {
			return nil, relErr
		}
		fileArg = filepath.ToSlash(rel)
	}

	diffArgs := append([]string{"diff"}, common...)
	if fileArg != "" {
		diffArgs = append(diffArgs, "--", fileArg)
	}
	diffOutput, err := e.runCommand(ctx, dir, "git", diffArgs...)
	if err != nil {
		if isNotGitRepoError(diffOutput, err) {
			return map[string]any{"error": "not_a_git_repo"}, nil
		}
		return nil, err
	}

	truncated := false
	if len(diffOutput) > diffOutputMaxBytes {
		diffOutput = diffOutput[:diffOutputMaxBytes]
		truncated = true
	}

	numstatArgs := append([]string{"diff", "--numstat"}, common...)
	if fileArg != "" {
		numstatArgs = append(numstatArgs, "--", fileArg)
	}
	numstatOutput, numErr := e.runCommand(ctx, dir, "git", numstatArgs...)
	if numErr != nil && !isNotGitRepoError(numstatOutput, numErr) {
		return nil, numErr
	}
	filesChanged, insertions, deletions := parseNumstat(numstatOutput)

	result := map[string]any{
		"diff":          diffOutput,
		"files_changed": filesChanged,
		"insertions":    insertions,
		"deletions":     deletions,
	}
	if truncated {
		result["truncated"] = true
	}
	return result, nil
}

func (e *NativeToolExecutor) handleGitLog(ctx context.Context, input map[string]any) (map[string]any, error) {
	wd, _, dir, err := e.resolveInputPath(ctx, input, "path")
	if err != nil {
		if errors.Is(err, ErrPathTraversal) {
			return map[string]any{"error": "path_traversal"}, nil
		}
		return nil, err
	}
	limit := clamp(readInt(input, "limit", defaultGitLogLimit), 1, maxGitLogLimit)

	args := []string{
		"log",
		fmt.Sprintf("-n%d", limit),
		"--date=iso-strict",
		"--pretty=format:%H%x1f%an%x1f%ae%x1f%aI%x1f%s",
	}
	if since, ok := readString(input, "since"); ok && since != "" {
		args = append(args, "--since="+since)
	}
	if author, ok := readString(input, "author"); ok && author != "" {
		args = append(args, "--author="+author)
	}
	if raw, ok := readString(input, "file_path"); ok && raw != "" {
		resolved, resolveErr := wd.ResolvePath(raw)
		if resolveErr != nil {
			if errors.Is(resolveErr, ErrPathTraversal) {
				return map[string]any{"error": "path_traversal"}, nil
			}
			return nil, resolveErr
		}
		rel, relErr := filepath.Rel(dir, resolved)
		if relErr != nil {
			return nil, relErr
		}
		args = append(args, "--", filepath.ToSlash(rel))
	}

	out, err := e.runCommand(ctx, dir, "git", args...)
	if err != nil {
		if isNotGitRepoError(out, err) {
			return map[string]any{"error": "not_a_git_repo"}, nil
		}
		return nil, err
	}

	commits := make([]map[string]any, 0)
	for _, line := range splitGitLines(out) {
		if len(commits) >= limit {
			break
		}
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) != 5 {
			continue
		}
		sha := strings.TrimSpace(parts[0])
		if sha == "" {
			continue
		}
		short := sha
		if len(short) > 7 {
			short = short[:7]
		}
		commits = append(commits, map[string]any{
			"sha":       sha,
			"short_sha": short,
			"author":    strings.TrimSpace(parts[1]),
			"email":     strings.TrimSpace(parts[2]),
			"date":      strings.TrimSpace(parts[3]),
			"message":   strings.TrimSpace(parts[4]),
		})
	}

	return map[string]any{"commits": commits}, nil
}

func (e *NativeToolExecutor) runCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := e.command(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func isNotGitRepoError(output string, err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(output))
	return strings.Contains(text, "not a git repository") || strings.Contains(text, "fatal: not a git repository")
}

func splitGitLines(output string) []string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func parsePorcelainStatus(code string) string {
	trimmed := strings.TrimSpace(code)
	if len(trimmed) == 0 {
		return "?"
	}
	if len(trimmed) == 1 {
		return string(trimmed[0])
	}
	if code[0] != ' ' {
		return string(code[0])
	}
	return string(code[1])
}

func parseBranchState(raw string) (branch string, ahead int, behind int) {
	branch = strings.TrimSpace(raw)
	if match := unbornBranchRegex.FindStringSubmatch(branch); len(match) == 2 {
		return strings.TrimSpace(match[1]), 0, 0
	}
	matches := branchAheadBehindRegex.FindStringSubmatch(raw)
	if len(matches) == 3 {
		branch = strings.TrimSpace(matches[1])
		meta := strings.TrimSpace(matches[2])
		if meta != "" {
			for _, token := range strings.Split(meta, ",") {
				part := strings.TrimSpace(token)
				if strings.HasPrefix(part, "ahead ") {
					ahead, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(part, "ahead ")))
				}
				if strings.HasPrefix(part, "behind ") {
					behind, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(part, "behind ")))
				}
			}
		}
	}
	return branch, ahead, behind
}

func parseNumstat(output string) (filesChanged, insertions, deletions int) {
	for _, line := range splitGitLines(output) {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		filesChanged++
		if parts[0] != "-" {
			if value, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
				insertions += value
			}
		}
		if parts[1] != "-" {
			if value, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				deletions += value
			}
		}
	}
	return filesChanged, insertions, deletions
}
