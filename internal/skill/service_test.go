package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestServiceCreateValidatesSlug(t *testing.T) {
	mock := &mockRepository{
		createFn: func(_ context.Context, in repo.Skill) (repo.Skill, error) {
			in.ID = uuid.New()
			in.CreatedAt = time.Now()
			in.UpdatedAt = in.CreatedAt
			return in, nil
		},
	}
	svc := NewService(mock)
	orgID := uuid.New()

	if _, err := svc.Create(context.Background(), orgID, CreateRequest{
		Slug:        "invalid slug",
		DisplayName: "Invalid",
		Description: "Invalid",
		FilePath:    "skills/invalid.md",
	}); !errors.Is(err, ErrInvalidSlug) {
		t.Fatalf("Create error = %v, want ErrInvalidSlug", err)
	}

	if _, err := svc.Create(context.Background(), orgID, CreateRequest{
		Slug:        "Invalid",
		DisplayName: "Invalid",
		Description: "Invalid",
		FilePath:    "skills/invalid.md",
	}); !errors.Is(err, ErrInvalidSlug) {
		t.Fatalf("Create error = %v, want ErrInvalidSlug", err)
	}

	created, err := svc.Create(context.Background(), orgID, CreateRequest{
		Slug:          "valid-skill",
		DisplayName:   "Valid Skill",
		Description:   "Valid",
		FilePath:      "skills/valid-skill.md",
		CreatedByType: "system",
		CreatedByID:   uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Create valid slug failed: %v", err)
	}
	if created.Slug != "valid-skill" {
		t.Fatalf("created slug = %q, want %q", created.Slug, "valid-skill")
	}
}

func TestCheckConsistencyReportsAllCategories(t *testing.T) {
	orgID := uuid.New()
	skillsDir := t.TempDir()

	mkdirFile(t, filepath.Join(skillsDir, "alpha.md"))
	mkdirFile(t, filepath.Join(skillsDir, "not-matching.md"))
	mkdirFile(t, filepath.Join(skillsDir, "orphan.md"))

	mock := &mockRepository{
		listByOrgFn: func(_ context.Context, gotOrgID uuid.UUID, includeInactive bool) ([]repo.Skill, error) {
			if gotOrgID != orgID {
				t.Fatalf("org id = %s, want %s", gotOrgID, orgID)
			}
			if includeInactive {
				t.Fatal("CheckConsistency must only load active skills")
			}
			return []repo.Skill{
				{
					ID:             uuid.New(),
					OrganizationID: orgID,
					Slug:           "alpha",
					DisplayName:    "Alpha",
					Description:    "Alpha skill",
					FilePath:       "skills/alpha.md",
					IsActive:       true,
				},
				{
					ID:             uuid.New(),
					OrganizationID: orgID,
					Slug:           "beta",
					DisplayName:    "Beta",
					Description:    "Beta skill",
					FilePath:       "skills/beta.md",
					IsActive:       true,
				},
				{
					ID:             uuid.New(),
					OrganizationID: orgID,
					Slug:           "wrong-slug",
					DisplayName:    "Mismatch",
					Description:    "Mismatch skill",
					FilePath:       "skills/not-matching.md",
					IsActive:       true,
				},
			}, nil
		},
	}
	svc := NewService(mock)

	report, err := svc.CheckConsistency(context.Background(), orgID, skillsDir)
	if err != nil {
		t.Fatalf("CheckConsistency failed: %v", err)
	}

	if !slices.Equal(report.MissingFiles, []string{"skills/beta.md"}) {
		t.Fatalf("MissingFiles = %#v, want %#v", report.MissingFiles, []string{"skills/beta.md"})
	}
	if !slices.Equal(report.UnregisteredFiles, []string{"orphan.md"}) {
		t.Fatalf("UnregisteredFiles = %#v, want %#v", report.UnregisteredFiles, []string{"orphan.md"})
	}
	wantMismatch := []string{"skills/not-matching.md (slug=wrong-slug)"}
	if !slices.Equal(report.Mismatches, wantMismatch) {
		t.Fatalf("Mismatches = %#v, want %#v", report.Mismatches, wantMismatch)
	}
}

func mkdirFile(t *testing.T, path string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte("# skill"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type mockRepository struct {
	createFn           func(ctx context.Context, skill repo.Skill) (repo.Skill, error)
	getByIDFn          func(ctx context.Context, id uuid.UUID) (repo.Skill, error)
	getBySlugFn        func(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, slug string) (repo.Skill, error)
	listByOrgFn        func(ctx context.Context, organizationID uuid.UUID, includeInactive bool) ([]repo.Skill, error)
	listByProjectFn    func(ctx context.Context, projectID uuid.UUID, includeInactive bool) ([]repo.Skill, error)
	updateFn           func(ctx context.Context, skill repo.Skill) (repo.Skill, error)
	setActiveFn        func(ctx context.Context, id uuid.UUID, active bool) (repo.Skill, error)
	bulkUpsertBySlugFn func(ctx context.Context, skills []repo.Skill) ([]repo.Skill, error)
}

func (m *mockRepository) Create(ctx context.Context, skill repo.Skill) (repo.Skill, error) {
	return m.createFn(ctx, skill)
}

func (m *mockRepository) GetByID(ctx context.Context, id uuid.UUID) (repo.Skill, error) {
	if m.getByIDFn == nil {
		return repo.Skill{}, repo.ErrNotFound
	}
	return m.getByIDFn(ctx, id)
}

func (m *mockRepository) GetBySlug(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, slug string) (repo.Skill, error) {
	if m.getBySlugFn == nil {
		return repo.Skill{}, repo.ErrNotFound
	}
	return m.getBySlugFn(ctx, organizationID, projectID, slug)
}

func (m *mockRepository) ListByOrg(ctx context.Context, organizationID uuid.UUID, includeInactive bool) ([]repo.Skill, error) {
	if m.listByOrgFn == nil {
		return nil, nil
	}
	return m.listByOrgFn(ctx, organizationID, includeInactive)
}

func (m *mockRepository) ListByProject(ctx context.Context, projectID uuid.UUID, includeInactive bool) ([]repo.Skill, error) {
	if m.listByProjectFn == nil {
		return nil, nil
	}
	return m.listByProjectFn(ctx, projectID, includeInactive)
}

func (m *mockRepository) Update(ctx context.Context, skill repo.Skill) (repo.Skill, error) {
	if m.updateFn == nil {
		return repo.Skill{}, repo.ErrNotFound
	}
	return m.updateFn(ctx, skill)
}

func (m *mockRepository) SetActive(ctx context.Context, id uuid.UUID, active bool) (repo.Skill, error) {
	if m.setActiveFn == nil {
		return repo.Skill{}, repo.ErrNotFound
	}
	return m.setActiveFn(ctx, id, active)
}

func (m *mockRepository) BulkUpsertBySlug(ctx context.Context, skills []repo.Skill) ([]repo.Skill, error) {
	if m.bulkUpsertBySlugFn == nil {
		return nil, nil
	}
	return m.bulkUpsertBySlugFn(ctx, skills)
}
