package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const defaultDataDir = "~/otter-data/"

type ProjectLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

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

func GeneralRoot(dataDir string) string {
	return filepath.Join(ResolveDataDir(dataDir), "workspaces", "general")
}

func ProjectRoot(dataDir, projectSlug string) (string, error) {
	slug := strings.TrimSpace(projectSlug)
	if slug == "" {
		return "", fmt.Errorf("project slug is required")
	}
	return filepath.Join(ResolveDataDir(dataDir), "workspaces", slug), nil
}

func ProjectRootByID(ctx context.Context, projects ProjectLookup, dataDir string, organizationID, projectID uuid.UUID) (string, error) {
	if projects == nil {
		return "", fmt.Errorf("project repository is required for workspace resolution")
	}
	if projectID == uuid.Nil {
		return "", fmt.Errorf("project id is required")
	}

	projectRecord, err := projects.GetByID(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("resolve project slug: %w", err)
	}
	if organizationID != uuid.Nil && projectRecord.OrganizationID != organizationID {
		return "", fmt.Errorf("project organization mismatch")
	}
	return ProjectRoot(dataDir, projectRecord.Slug)
}
