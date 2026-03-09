package native

import (
	"context"
	"strings"

	"github.com/google/uuid"

	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func (e *NativeToolExecutor) ensureProjectRepoBinding(ctx context.Context, projectID uuid.UUID) (repo.Project, error) {
	if e.projects == nil || projectID == uuid.Nil {
		return repo.Project{}, nil
	}

	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		return repo.Project{}, err
	}
	if e.environments == nil {
		return projectRecord, nil
	}

	if explicitRoot := strings.TrimSpace(e.explicitRoot); explicitRoot != "" {
		if _, _, err := projectsvc.EnsureRepoBindingAtPath(ctx, e.environments, projectRecord, explicitRoot); err != nil {
			return repo.Project{}, err
		}
		return projectRecord, nil
	}
	if _, _, err := projectsvc.EnsureCanonicalRepoBinding(ctx, e.environments, e.dataDir, projectRecord); err != nil {
		return repo.Project{}, err
	}
	return projectRecord, nil
}
