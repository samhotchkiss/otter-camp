package native

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionWorkDir wraps the rooted workspace path for a chat session.
type SessionWorkDir struct {
	root string
}

func NewSessionWorkDir(root string) (SessionWorkDir, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return SessionWorkDir{}, fmt.Errorf("workspace root is required")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return SessionWorkDir{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return SessionWorkDir{}, fmt.Errorf("create workspace root: %w", err)
	}
	canonical := abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		canonical = resolved
	}
	return SessionWorkDir{root: canonical}, nil
}

func (w SessionWorkDir) Root() string {
	return w.root
}

func (w SessionWorkDir) ResolvePath(relOrAbsPath string) (string, error) {
	candidate := strings.TrimSpace(relOrAbsPath)
	if candidate == "" {
		return w.root, nil
	}
	if hasDotDotComponent(candidate) {
		return "", ErrPathTraversal
	}

	cleaned := filepath.Clean(candidate)
	if hasDotDotComponent(cleaned) {
		return "", ErrPathTraversal
	}

	var absolute string
	if filepath.IsAbs(cleaned) {
		absolute = cleaned
	} else {
		absolute = filepath.Join(w.root, cleaned)
	}
	absolute = filepath.Clean(absolute)

	if !isWithinRoot(w.root, absolute) {
		return "", ErrPathTraversal
	}
	return absolute, nil
}

func hasDotDotComponent(p string) bool {
	if strings.TrimSpace(p) == ".." {
		return true
	}
	parts := strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, part := range parts {
		if part == ".." {
			return true
		}
	}
	return false
}

func isWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
