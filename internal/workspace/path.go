package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultDataDir = "~/otter-data/"

func ResolveDataDir(raw string) string {
	dataDir := strings.TrimSpace(raw)
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv("OTTERCAMP_DATA_DIR"))
	}
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	expanded, err := ExpandDataDir(dataDir)
	if err == nil {
		return expanded
	}
	return dataDir
}

func ExpandDataDir(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("data directory path is required")
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if trimmed == "~" {
			return filepath.Clean(home), nil
		}
		return filepath.Clean(filepath.Join(home, trimmed[2:])), nil
	}
	return filepath.Clean(trimmed), nil
}

func ProjectRoot(dataDir, projectSlug string) (string, error) {
	slug := strings.TrimSpace(projectSlug)
	if slug == "" {
		return "", fmt.Errorf("project slug is required")
	}
	return filepath.Join(ResolveDataDir(dataDir), "workspaces", slug), nil
}
