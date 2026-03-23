package planningasset

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

type fakeArtifactRepo struct {
	upserts []repo.PlanningArtifactUpsert
}

func (f *fakeArtifactRepo) UpsertVersion(_ context.Context, artifact repo.PlanningArtifactUpsert) (repo.PlanningArtifact, bool, error) {
	f.upserts = append(f.upserts, artifact)
	return repo.PlanningArtifact{
		ID:                  uuid.New(),
		OrganizationID:      artifact.OrganizationID,
		ProjectID:           artifact.ProjectID,
		SourceTaskID:        artifact.SourceTaskID,
		ArtifactKind:        artifact.ArtifactKind,
		ArtifactSlug:        artifact.ArtifactSlug,
		Title:               artifact.Title,
		RepoPath:            artifact.RepoPath,
		CurrentVersion:      1,
		LatestContentSHA256: artifact.LatestContentSHA256,
	}, true, nil
}

func (*fakeArtifactRepo) ListByProject(context.Context, uuid.UUID) ([]repo.PlanningArtifact, error) {
	return nil, nil
}

func (*fakeArtifactRepo) ListBySourceTask(context.Context, uuid.UUID) ([]repo.PlanningArtifact, error) {
	return nil, nil
}

func (*fakeArtifactRepo) ListVersions(context.Context, uuid.UUID) ([]repo.PlanningArtifactVersion, error) {
	return nil, nil
}

type fakeEnvironmentRepo struct {
	repoRoot string
}

func (f *fakeEnvironmentRepo) ListByProject(_ context.Context, _ uuid.UUID) ([]repo.ProjectEnvironment, error) {
	return []repo.ProjectEnvironment{{
		ProjectID: uuid.New(),
		RepoPath:  &f.repoRoot,
		IsActive:  true,
	}}, nil
}

func TestSyncTaskRepairsArtifactWhenSourceTaskTitleMismatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	artifactPath := filepath.Join(repoRoot, "planning/strategy-artifact/oc-24-strategy-brief.md")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bad := `# OC-24: Listing Pages Strategy Brief

- Kind: strategy_artifact
- Playbook: strategy
- Source task: OC-24 (Define listing pages)
- Generated at: 2026-03-30T15:45:00Z

## Purpose
Wrong task body.`
	if err := os.WriteFile(artifactPath, []byte(bad), 0o644); err != nil {
		t.Fatalf("write bad artifact: %v", err)
	}

	now := time.Date(2026, 3, 22, 22, 45, 0, 0, time.UTC)
	artifacts := &fakeArtifactRepo{}
	service := New(Options{
		Artifacts:    artifacts,
		Environments: &fakeEnvironmentRepo{repoRoot: repoRoot},
		Clock:        func() time.Time { return now },
	})

	task := repo.ProjectTask{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		ProjectID:      uuid.New(),
		TaskNumber:     24,
		Title:          "Plan hosting and infrastructure",
		Metadata: taskplan.ApplyMetadata(nil, taskplan.Plan{
			Mode:             taskplan.ModeExecutionFirst,
			Playbook:         taskplan.PlaybookStrategy,
			WorkType:         "strategy",
			ProjectStage:     "definition",
			EvidenceMaturity: "directional",
			RiskLevel:        "low",
			ProcessEnforced:  true,
			Artifacts: []taskplan.PlannedArtifact{{
				Slug:     "strategy-brief",
				Title:    "Strategy brief",
				Kind:     "strategy_artifact",
				RepoPath: "planning/strategy-artifact/oc-24-strategy-brief.md",
			}},
		}),
	}

	if _, err := service.SyncTask(ctx, task, Actor{Type: "agent"}); err != nil {
		t.Fatalf("SyncTask: %v", err)
	}
	rewritten, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read repaired artifact: %v", err)
	}
	content := string(rewritten)
	if strings.Contains(content, "Define listing pages") {
		t.Fatalf("repaired artifact still references wrong task: %s", content)
	}
	if !strings.Contains(content, "- Source task: OC-24") {
		t.Fatalf("repaired artifact missing expected source task marker: %s", content)
	}
	if !strings.Contains(content, "Replace this scaffold with the durable planning output for this artifact.") {
		t.Fatalf("repaired artifact did not revert to scaffold: %s", content)
	}
	if len(artifacts.upserts) != 1 {
		t.Fatalf("upsert count = %d, want 1", len(artifacts.upserts))
	}
}
