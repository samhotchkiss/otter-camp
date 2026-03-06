package planningasset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

type artifactRepository interface {
	UpsertVersion(ctx context.Context, artifact repo.PlanningArtifactUpsert) (repo.PlanningArtifact, bool, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.PlanningArtifact, error)
	ListBySourceTask(ctx context.Context, taskID uuid.UUID) ([]repo.PlanningArtifact, error)
	ListVersions(ctx context.Context, artifactID uuid.UUID) ([]repo.PlanningArtifactVersion, error)
}

type environmentRepository interface {
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error)
}

type Actor struct {
	Type string
	ID   *uuid.UUID
}

type Options struct {
	Artifacts    artifactRepository
	Environments environmentRepository
	Clock        func() time.Time
}

type Service struct {
	artifacts    artifactRepository
	environments environmentRepository
	clock        func() time.Time
}

func New(opts Options) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Service{
		artifacts:    opts.Artifacts,
		environments: opts.Environments,
		clock:        clock,
	}
}

func (s *Service) SyncTask(ctx context.Context, task repo.ProjectTask, actor Actor) (taskplan.Plan, error) {
	plan, ok := taskplan.Parse(task.Metadata)
	if !ok || !plan.HasSelection() || len(plan.Artifacts) == 0 {
		return plan, nil
	}
	if s.artifacts == nil || s.environments == nil {
		return plan, nil
	}

	repoRoot, err := s.projectRepoRoot(ctx, task.ProjectID)
	if err != nil || repoRoot == "" {
		return plan, err
	}

	synced := make([]taskplan.PlannedArtifact, 0, len(plan.Artifacts))
	for _, artifact := range plan.Artifacts {
		updated, syncErr := s.syncArtifact(ctx, repoRoot, task, plan, artifact, actor)
		if syncErr != nil {
			return plan, syncErr
		}
		synced = append(synced, updated)
	}
	plan.Artifacts = synced
	return plan, nil
}

func (s *Service) ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.PlanningArtifact, error) {
	if s == nil || s.artifacts == nil {
		return nil, nil
	}
	return s.artifacts.ListByProject(ctx, projectID)
}

func (s *Service) ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.PlanningArtifact, error) {
	if s == nil || s.artifacts == nil {
		return nil, nil
	}
	return s.artifacts.ListBySourceTask(ctx, taskID)
}

func (s *Service) ListVersions(ctx context.Context, artifactID uuid.UUID) ([]repo.PlanningArtifactVersion, error) {
	if s == nil || s.artifacts == nil {
		return nil, nil
	}
	return s.artifacts.ListVersions(ctx, artifactID)
}

func OverlayPlanArtifacts(plan taskplan.Plan, records []repo.PlanningArtifact) taskplan.Plan {
	if len(records) == 0 {
		return plan
	}

	bySlug := make(map[string]repo.PlanningArtifact, len(records))
	byPath := make(map[string]repo.PlanningArtifact, len(records))
	for _, record := range records {
		if slug := strings.TrimSpace(record.ArtifactSlug); slug != "" {
			bySlug[slug] = record
		}
		if repoPath := strings.TrimSpace(record.RepoPath); repoPath != "" {
			byPath[repoPath] = record
		}
	}

	out := plan
	if len(out.Artifacts) == 0 {
		out.Artifacts = PlannedArtifactsFromRecords(records)
		return out
	}

	merged := make([]taskplan.PlannedArtifact, 0, len(out.Artifacts))
	for _, artifact := range out.Artifacts {
		record, ok := byPath[strings.TrimSpace(artifact.RepoPath)]
		if !ok {
			record, ok = bySlug[strings.TrimSpace(artifact.Slug)]
		}
		if !ok {
			merged = append(merged, artifact)
			continue
		}
		artifact.Kind = strings.TrimSpace(record.ArtifactKind)
		artifact.ArtifactID = record.ID.String()
		artifact.RepoPath = strings.TrimSpace(record.RepoPath)
		artifact.Version = record.CurrentVersion
		artifact.ContentSHA256 = strings.TrimSpace(record.LatestContentSHA256)
		merged = append(merged, artifact)
	}
	out.Artifacts = merged
	return out
}

func PlannedArtifactsFromRecords(records []repo.PlanningArtifact) []taskplan.PlannedArtifact {
	out := make([]taskplan.PlannedArtifact, 0, len(records))
	for _, record := range records {
		out = append(out, taskplan.PlannedArtifact{
			Slug:          strings.TrimSpace(record.ArtifactSlug),
			Title:         strings.TrimSpace(record.Title),
			Kind:          strings.TrimSpace(record.ArtifactKind),
			ArtifactID:    record.ID.String(),
			RepoPath:      strings.TrimSpace(record.RepoPath),
			Version:       record.CurrentVersion,
			ContentSHA256: strings.TrimSpace(record.LatestContentSHA256),
		})
	}
	return out
}

func (s *Service) projectRepoRoot(ctx context.Context, projectID uuid.UUID) (string, error) {
	environments, err := s.environments.ListByProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, environment := range environments {
		if !environment.IsActive {
			continue
		}
		if root := strings.TrimSpace(derefString(environment.RepoPath)); root != "" {
			return filepath.Abs(root)
		}
	}
	for _, environment := range environments {
		if root := strings.TrimSpace(derefString(environment.RepoPath)); root != "" {
			return filepath.Abs(root)
		}
	}
	return "", nil
}

func (s *Service) syncArtifact(
	ctx context.Context,
	repoRoot string,
	task repo.ProjectTask,
	plan taskplan.Plan,
	artifact taskplan.PlannedArtifact,
	actor Actor,
) (taskplan.PlannedArtifact, error) {
	artifact.Kind = taskplan.NormalizeArtifactKind(artifact.Kind)
	if artifact.Kind == "" {
		artifact.Kind = taskplan.DefaultArtifactKindForPlaybook(plan.Playbook)
	}
	relPath := sanitizeRepoPath(artifact.RepoPath)
	if relPath == "" {
		relPath = defaultRepoPath(task, artifact)
	}
	absPath, err := resolveArtifactPath(repoRoot, relPath)
	if err != nil {
		return artifact, err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return artifact, err
	}
	if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
		if err := os.WriteFile(absPath, []byte(scaffoldContent(s.clock(), task, plan, artifact)), 0o644); err != nil {
			return artifact, err
		}
	} else if statErr != nil {
		return artifact, statErr
	}

	payload, err := os.ReadFile(absPath)
	if err != nil {
		return artifact, err
	}
	contentHash := sha256Hex(payload)
	record, _, err := s.artifacts.UpsertVersion(ctx, repo.PlanningArtifactUpsert{
		OrganizationID:      task.OrganizationID,
		ProjectID:           task.ProjectID,
		SourceTaskID:        task.ID,
		ArtifactKind:        artifact.Kind,
		ArtifactSlug:        strings.TrimSpace(artifact.Slug),
		Title:               strings.TrimSpace(artifact.Title),
		RepoPath:            filepath.ToSlash(relPath),
		LatestContentSHA256: contentHash,
		ByteSize:            len(payload),
		CreatedByType:       normalizeActorType(actor.Type),
		CreatedByID:         actor.ID,
	})
	if err != nil {
		return artifact, err
	}

	artifact.ArtifactID = record.ID.String()
	artifact.RepoPath = strings.TrimSpace(record.RepoPath)
	artifact.Version = record.CurrentVersion
	artifact.ContentSHA256 = strings.TrimSpace(record.LatestContentSHA256)
	return artifact, nil
}

func sanitizeRepoPath(repoPath string) string {
	trimmed := strings.TrimSpace(repoPath)
	if trimmed == "" || path.IsAbs(trimmed) {
		return ""
	}
	cleaned := path.Clean(strings.TrimPrefix(trimmed, "./"))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ""
	}
	return cleaned
}

func defaultRepoPath(task repo.ProjectTask, artifact taskplan.PlannedArtifact) string {
	kindDir := strings.ReplaceAll(strings.TrimSpace(artifact.Kind), "_", "-")
	if kindDir == "" {
		kindDir = "planning-artifact"
	}
	base := normalizeSlug(strings.TrimSpace(artifact.Slug))
	if base == "" {
		base = normalizeSlug(strings.TrimSpace(artifact.Title))
	}
	if base == "" {
		base = "artifact"
	}
	prefix := fmt.Sprintf("oc-%d", task.TaskNumber)
	if task.TaskNumber <= 0 {
		prefix = "task-" + strings.ToLower(task.ID.String()[:8])
	}
	return path.Join("planning", kindDir, prefix+"-"+base+".md")
}

func resolveArtifactPath(repoRoot, repoPath string) (string, error) {
	root, err := filepath.Abs(strings.TrimSpace(repoRoot))
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(repoPath))
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path escapes repo root")
	}
	return target, nil
}

func scaffoldContent(now time.Time, task repo.ProjectTask, plan taskplan.Plan, artifact taskplan.PlannedArtifact) string {
	taskLabel := task.ID.String()
	if task.TaskNumber > 0 {
		taskLabel = fmt.Sprintf("OC-%d", task.TaskNumber)
	}
	return strings.TrimSpace(fmt.Sprintf(`
# %s

- Kind: %s
- Playbook: %s
- Source task: %s
- Generated at: %s

## Purpose
Replace this scaffold with the durable planning output for this artifact.

## Context
- Project stage: %s
- Evidence maturity: %s
- Risk level: %s

## Notes
- Keep decisions, trade-offs, and unresolved questions in this file so downstream work can link to it directly.
`, strings.TrimSpace(artifact.Title), strings.TrimSpace(artifact.Kind), strings.TrimSpace(plan.Playbook), taskLabel, now.UTC().Format(time.RFC3339), strings.TrimSpace(plan.ProjectStage), strings.TrimSpace(plan.EvidenceMaturity), strings.TrimSpace(plan.RiskLevel)))
}

func normalizeSlug(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	var builder strings.Builder
	lastDash := false
	for _, r := range trimmed {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeActorType(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "system"
	}
	return trimmed
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
