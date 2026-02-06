package api

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	errInvalidContentPath = errors.New("invalid content path")

	allowedContentWriteRoots = []string{"/notes/", "/posts/", "/assets/"}
	postPathPattern          = regexp.MustCompile(`^/posts/(\d{4}-\d{2}-\d{2})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
)

type contentBootstrapResult struct {
	Created []string `json:"created"`
}

func contentRootPath() string {
	root := strings.TrimSpace(os.Getenv("OTTER_CONTENT_ROOT"))
	if root == "" {
		root = filepath.Join("data", "content")
	}
	return root
}

func normalizeContentPath(input string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(input, "\\", "/"))
	if value == "" {
		return "", fmt.Errorf("%w: empty path", errInvalidContentPath)
	}
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%w: invalid characters", errInvalidContentPath)
	}

	parts := strings.Split(value, "/")
	for _, segment := range parts {
		if segment == ".." {
			return "", fmt.Errorf("%w: traversal is not allowed", errInvalidContentPath)
		}
	}

	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	cleaned := path.Clean(value)
	if cleaned == "/" || cleaned == "." {
		return "", fmt.Errorf("%w: root path is not allowed", errInvalidContentPath)
	}
	return cleaned, nil
}

func validateContentWritePath(input string) (string, error) {
	normalized, err := normalizeContentPath(input)
	if err != nil {
		return "", err
	}

	allowedRoot := false
	for _, root := range allowedContentWriteRoots {
		if strings.HasPrefix(normalized, root) {
			allowedRoot = true
			break
		}
	}
	if !allowedRoot {
		return "", fmt.Errorf("%w: path must be under /notes, /posts, or /assets", errInvalidContentPath)
	}

	if strings.HasPrefix(normalized, "/posts/") {
		if err := validatePostPathConvention(normalized); err != nil {
			return "", err
		}
	}

	return normalized, nil
}

func validatePostPathConvention(normalizedPath string) error {
	match := postPathPattern.FindStringSubmatch(normalizedPath)
	if len(match) != 3 {
		return fmt.Errorf("%w: post path must match /posts/YYYY-MM-DD-title.md", errInvalidContentPath)
	}

	if _, err := time.Parse("2006-01-02", match[1]); err != nil {
		return fmt.Errorf("%w: invalid post date prefix", errInvalidContentPath)
	}
	return nil
}

func resolveProjectContentWritePath(projectID, relativePath string) (string, string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", "", fmt.Errorf("%w: project id is required", errInvalidContentPath)
	}

	normalized, err := validateContentWritePath(relativePath)
	if err != nil {
		return "", "", err
	}

	absolute := filepath.Join(contentRootPath(), projectID, filepath.FromSlash(strings.TrimPrefix(normalized, "/")))
	return normalized, absolute, nil
}

func bootstrapProjectContentLayout(projectID string) (*contentBootstrapResult, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("%w: project id is required", errInvalidContentPath)
	}

	projectRoot := filepath.Join(contentRootPath(), projectID)
	dirs := []string{"notes", "posts", "assets"}
	created := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		target := filepath.Join(projectRoot, dir)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return nil, err
		}
		created = append(created, "/"+dir)
	}

	return &contentBootstrapResult{Created: created}, nil
}
