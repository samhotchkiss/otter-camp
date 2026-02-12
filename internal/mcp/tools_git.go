package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func RegisterGitTools(s *Server) error {
	if s == nil {
		return fmt.Errorf("%w: server is required", ErrInvalidToolCall)
	}

	registerErr := s.RegisterTool(Tool{
		Name:        "file_read",
		Description: "Read file content from a project repository",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
				"path",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"path":    map[string]any{"type": "string"},
				"ref":     map[string]any{"type": "string"},
			},
		},
		Handler: s.handleFileReadTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "tree_list",
		Description: "List repository tree entries",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
			},
			"properties": map[string]any{
				"project":   map[string]any{"type": "string"},
				"path":      map[string]any{"type": "string"},
				"ref":       map[string]any{"type": "string"},
				"recursive": map[string]any{"type": "boolean"},
			},
		},
		Handler: s.handleTreeListTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "commit_list",
		Description: "List recent commits for a project",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"ref":     map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "number"},
			},
		},
		Handler: s.handleCommitListTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "diff",
		Description: "Show diff summary between two refs",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
				"base",
				"head",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"base":    map[string]any{"type": "string"},
				"head":    map[string]any{"type": "string"},
			},
		},
		Handler: s.handleDiffTool,
	})
	if registerErr != nil {
		return registerErr
	}

	return s.RegisterTool(Tool{
		Name:        "branch_list",
		Description: "List branches in the project repository",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleBranchListTool,
	})
}

func (s *Server) handleFileReadTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	repoPath, err := s.resolveGitRepo(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	filePath, err := readRequiredPathArg(args, "path")
	if err != nil {
		return ToolCallResult{}, err
	}
	ref := readOptionalStringArg(args, "ref", "HEAD")

	output, err := runGitRepo(ctx, repoPath, "show", ref+":"+filePath)
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("%w: %v", ErrInvalidToolCall, err)
	}

	encoding := "base64"
	content := base64.StdEncoding.EncodeToString(output)
	if utf8.Valid(output) && !bytes.Contains(output, []byte{0}) {
		encoding = "utf-8"
		content = string(output)
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"path":     "/" + filePath,
					"ref":      ref,
					"encoding": encoding,
					"size":     len(output),
					"content":  content,
				},
			},
		},
	}, nil
}

func (s *Server) handleTreeListTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	repoPath, err := s.resolveGitRepo(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	ref := readOptionalStringArg(args, "ref", "HEAD")
	treePath := readOptionalPathArg(args, "path")
	recursive := false
	if rawRecursive, ok := args["recursive"]; ok {
		value, ok := rawRecursive.(bool)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: recursive must be a boolean", ErrInvalidToolCall)
		}
		recursive = value
	}

	object := ref
	if treePath != "" {
		object = ref + ":" + treePath
	}
	commandArgs := []string{"ls-tree"}
	if recursive {
		commandArgs = append(commandArgs, "-r")
	}
	commandArgs = append(commandArgs, object)
	output, err := runGitRepo(ctx, repoPath, commandArgs...)
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("%w: %v", ErrInvalidToolCall, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		meta := strings.Fields(parts[0])
		if len(meta) < 3 {
			continue
		}
		entryPath := strings.TrimSpace(parts[1])
		entries = append(entries, map[string]any{
			"type": meta[1],
			"sha":  meta[2],
			"path": entryPath,
			"name": path.Base(entryPath),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return fmt.Sprint(entries[i]["path"]) < fmt.Sprint(entries[j]["path"])
	})

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"ref":     ref,
					"path":    "/" + treePath,
					"entries": entries,
					"total":   len(entries),
				},
			},
		},
	}, nil
}

func (s *Server) handleCommitListTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	repoPath, err := s.resolveGitRepo(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	ref := readOptionalStringArg(args, "ref", "HEAD")
	limit := 20
	if rawLimit, ok := args["limit"]; ok {
		value, ok := rawLimit.(float64)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be a number", ErrInvalidToolCall)
		}
		limit = int(value)
		if limit <= 0 {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be positive", ErrInvalidToolCall)
		}
	}

	format := "%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%s"
	output, err := runGitRepo(ctx, repoPath, "log", "--format="+format, "-n", strconv.Itoa(limit), ref)
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("%w: %v", ErrInvalidToolCall, err)
	}

	commits := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 6 {
			continue
		}
		commits = append(commits, map[string]any{
			"sha":          parts[0],
			"parent_sha":   parts[1],
			"author_name":  parts[2],
			"author_email": parts[3],
			"timestamp":    parts[4],
			"subject":      parts[5],
		})
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"ref":     ref,
					"commits": commits,
					"total":   len(commits),
				},
			},
		},
	}, nil
}

func (s *Server) handleDiffTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	repoPath, err := s.resolveGitRepo(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	base := readOptionalStringArg(args, "base", "")
	head := readOptionalStringArg(args, "head", "")
	if base == "" || head == "" {
		return ToolCallResult{}, fmt.Errorf("%w: base and head are required", ErrInvalidToolCall)
	}

	output, err := runGitRepo(ctx, repoPath, "diff", "--name-status", base+".."+head)
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("%w: %v", ErrInvalidToolCall, err)
	}

	files := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		files = append(files, map[string]any{
			"status": parts[0],
			"path":   parts[1],
		})
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"base":  base,
					"head":  head,
					"files": files,
					"total": len(files),
				},
			},
		},
	}, nil
}

func (s *Server) handleBranchListTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	repoPath, err := s.resolveGitRepo(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	output, err := runGitRepo(ctx, repoPath, "branch", "--format=%(refname:short)")
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("%w: %v", ErrInvalidToolCall, err)
	}
	branches := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		branch := strings.TrimSpace(line)
		if branch != "" {
			branches = append(branches, branch)
		}
	}
	sort.Strings(branches)

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"branches": branches,
					"total":    len(branches),
				},
			},
		},
	}, nil
}

func (s *Server) resolveGitRepo(ctx context.Context, identity Identity, args map[string]any) (string, error) {
	project, err := s.resolveProject(ctx, identity, args)
	if err != nil {
		return "", err
	}
	if project.LocalRepoPath == nil || strings.TrimSpace(*project.LocalRepoPath) == "" {
		return "", fmt.Errorf("%w: project has no local repository path", ErrInvalidToolCall)
	}
	repoPath := strings.TrimSpace(*project.LocalRepoPath)
	if _, err := os.Stat(repoPath); err != nil {
		return "", fmt.Errorf("%w: repository path not found", ErrInvalidToolCall)
	}
	return repoPath, nil
}

func readRequiredPathArg(args map[string]any, key string) (string, error) {
	value, ok := args[key]
	if !ok {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidToolCall, key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be a string", ErrInvalidToolCall, key)
	}
	normalized := normalizeRepositoryPath(text)
	if normalized == "" {
		return "", fmt.Errorf("%w: %s must not be empty", ErrInvalidToolCall, key)
	}
	return normalized, nil
}

func readOptionalPathArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return normalizeRepositoryPath(text)
}

func readOptionalStringArg(args map[string]any, key, fallback string) string {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	text, ok := value.(string)
	if !ok {
		return fallback
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func normalizeRepositoryPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return ""
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "..") {
		return ""
	}
	return cleaned
}

func runGitRepo(ctx context.Context, repoPath string, gitArgs ...string) ([]byte, error) {
	args := make([]string, 0, len(gitArgs)+2)
	if _, err := os.Stat(path.Join(repoPath, ".git")); err == nil {
		args = append(args, "-C", repoPath)
	} else {
		args = append(args, "--git-dir", repoPath)
	}
	args = append(args, gitArgs...)

	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return output, nil
}
