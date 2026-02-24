//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestSkillRepoCRUDAndScopedGetBySlug(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	skillRepo := NewSkillRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{Slug: "skill-org", DisplayName: "Skill Org"})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	created, err := skillRepo.Create(context.Background(), Skill{
		OrganizationID: org.ID,
		Slug:           "summarize",
		DisplayName:    "Summarize",
		Description:    "Summarize meetings",
		FilePath:       "skills/summarize.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create skill failed: %v", err)
	}

	byID, err := skillRepo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if byID.Slug != "summarize" {
		t.Fatalf("skill slug = %q, want %q", byID.Slug, "summarize")
	}

	byOrgSlug, err := skillRepo.GetBySlug(context.Background(), org.ID, nil, "summarize")
	if err != nil {
		t.Fatalf("GetBySlug org-scoped failed: %v", err)
	}
	if byOrgSlug.ID != created.ID {
		t.Fatalf("GetBySlug org-scoped ID = %s, want %s", byOrgSlug.ID, created.ID)
	}

	projectID := uuid.New()
	projectSkill, err := skillRepo.Create(context.Background(), Skill{
		OrganizationID: org.ID,
		ProjectID:      &projectID,
		Slug:           "summarize",
		DisplayName:    "Project Summarize",
		Description:    "Project summarize",
		FilePath:       "skills/project-summarize.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create project skill failed: %v", err)
	}

	byProjectSlug, err := skillRepo.GetBySlug(context.Background(), org.ID, &projectID, "summarize")
	if err != nil {
		t.Fatalf("GetBySlug project-scoped failed: %v", err)
	}
	if byProjectSlug.ID != projectSkill.ID {
		t.Fatalf("GetBySlug project-scoped ID = %s, want %s", byProjectSlug.ID, projectSkill.ID)
	}
}

func TestSkillRepoPartialUniqueIndexes(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	skillRepo := NewSkillRepo(pool)

	orgA, err := orgRepo.Create(context.Background(), Organization{Slug: "org-a", DisplayName: "Org A"})
	if err != nil {
		t.Fatalf("create org A failed: %v", err)
	}
	orgB, err := orgRepo.Create(context.Background(), Organization{Slug: "org-b", DisplayName: "Org B"})
	if err != nil {
		t.Fatalf("create org B failed: %v", err)
	}

	_, err = skillRepo.Create(context.Background(), Skill{
		OrganizationID: orgA.ID,
		Slug:           "shared-slug",
		DisplayName:    "Shared",
		Description:    "Shared",
		FilePath:       "skills/shared.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create org-scoped skill failed: %v", err)
	}

	_, err = skillRepo.Create(context.Background(), Skill{
		OrganizationID: orgA.ID,
		Slug:           "shared-slug",
		DisplayName:    "Duplicate",
		Description:    "Duplicate",
		FilePath:       "skills/shared-dup.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate org-scoped slug error = %v, want ErrConflict", err)
	}

	if _, err := skillRepo.Create(context.Background(), Skill{
		OrganizationID: orgB.ID,
		Slug:           "shared-slug",
		DisplayName:    "Shared Org B",
		Description:    "Shared",
		FilePath:       "skills/shared.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	}); err != nil {
		t.Fatalf("same slug in different org should succeed, got %v", err)
	}

	projectID := uuid.New()
	_, err = skillRepo.Create(context.Background(), Skill{
		OrganizationID: orgA.ID,
		ProjectID:      &projectID,
		Slug:           "project-slug",
		DisplayName:    "Project",
		Description:    "Project",
		FilePath:       "skills/project.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create project-scoped skill failed: %v", err)
	}

	_, err = skillRepo.Create(context.Background(), Skill{
		OrganizationID: orgA.ID,
		ProjectID:      &projectID,
		Slug:           "project-slug",
		DisplayName:    "Project Duplicate",
		Description:    "Project duplicate",
		FilePath:       "skills/project-dup.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate project-scoped slug error = %v, want ErrConflict", err)
	}

	anotherProjectID := uuid.New()
	if _, err := skillRepo.Create(context.Background(), Skill{
		OrganizationID: orgA.ID,
		ProjectID:      &anotherProjectID,
		Slug:           "project-slug",
		DisplayName:    "Project Other",
		Description:    "Project other",
		FilePath:       "skills/project-other.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		IsActive:       true,
	}); err != nil {
		t.Fatalf("same slug in different project should succeed, got %v", err)
	}
}

func TestSkillRepoBulkUpsertAndListByOrgIncludeInactive(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	skillRepo := NewSkillRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{Slug: "upsert-org", DisplayName: "Upsert Org"})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	initial := []Skill{
		{
			OrganizationID: org.ID,
			Slug:           "one",
			DisplayName:    "One",
			Description:    "One",
			FilePath:       "skills/one.md",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			IsActive:       true,
		},
		{
			OrganizationID: org.ID,
			Slug:           "two",
			DisplayName:    "Two",
			Description:    "Two",
			FilePath:       "skills/two.md",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			IsActive:       true,
		},
		{
			OrganizationID: org.ID,
			Slug:           "three",
			DisplayName:    "Three",
			Description:    "Three",
			FilePath:       "skills/three.md",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			IsActive:       true,
		},
	}
	if _, err := skillRepo.BulkUpsertBySlug(context.Background(), initial); err != nil {
		t.Fatalf("initial BulkUpsertBySlug failed: %v", err)
	}

	second := []Skill{
		{
			OrganizationID: org.ID,
			Slug:           "one",
			DisplayName:    "One Updated",
			Description:    "One Updated",
			FilePath:       "skills/one-updated.md",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			IsActive:       true,
		},
		{
			OrganizationID: org.ID,
			Slug:           "two",
			DisplayName:    "Two Updated",
			Description:    "Two Updated",
			FilePath:       "skills/two-updated.md",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			IsActive:       false,
		},
		{
			OrganizationID: org.ID,
			Slug:           "four",
			DisplayName:    "Four",
			Description:    "Four",
			FilePath:       "skills/four.md",
			CreatedByType:  "system",
			CreatedByID:    uuid.Nil,
			IsActive:       true,
		},
	}
	if _, err := skillRepo.BulkUpsertBySlug(context.Background(), second); err != nil {
		t.Fatalf("second BulkUpsertBySlug failed: %v", err)
	}

	var total int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM skill WHERE organization_id = $1`, org.ID).Scan(&total); err != nil {
		t.Fatalf("count skill rows failed: %v", err)
	}
	if total != 4 {
		t.Fatalf("skill count = %d, want 4", total)
	}

	activeOnly, err := skillRepo.ListByOrg(context.Background(), org.ID, false)
	if err != nil {
		t.Fatalf("ListByOrg active only failed: %v", err)
	}
	if len(activeOnly) != 3 {
		t.Fatalf("active ListByOrg length = %d, want 3", len(activeOnly))
	}

	all, err := skillRepo.ListByOrg(context.Background(), org.ID, true)
	if err != nil {
		t.Fatalf("ListByOrg include inactive failed: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("include-inactive ListByOrg length = %d, want 4", len(all))
	}
}
