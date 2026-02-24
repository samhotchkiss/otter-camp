//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTokenBudgetRepoUniqueConstraintForOrgScope(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	budgetRepo := NewTokenBudgetRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "budget-unique-org",
		DisplayName: "Budget Unique Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	soft := int64(100)
	hard := int64(1000)
	first, err := budgetRepo.Create(ctx, TokenBudget{
		OrganizationID:  org.ID,
		Period:          "daily",
		SoftLimitTokens: &soft,
		HardLimitTokens: &hard,
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create first budget: %v", err)
	}
	if first.ID == uuid.Nil {
		t.Fatal("first budget id must be set")
	}

	_, err = budgetRepo.Create(ctx, TokenBudget{
		OrganizationID:  org.ID,
		Period:          "daily",
		SoftLimitTokens: &soft,
		HardLimitTokens: &hard,
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate org scope create error = %v, want ErrConflict", err)
	}
}

func TestTokenBudgetRepoGetByScopeForOrgAndProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	budgetRepo := NewTokenBudgetRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "budget-scope-org",
		DisplayName: "Budget Scope Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "scope-project",
		DisplayName:    "Scope Project",
		Description:    "Project budget test",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	orgSoft := int64(200)
	orgHard := int64(2000)
	orgBudget, err := budgetRepo.Create(ctx, TokenBudget{
		OrganizationID:  org.ID,
		Period:          "daily",
		SoftLimitTokens: &orgSoft,
		HardLimitTokens: &orgHard,
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create org budget: %v", err)
	}

	projectSoft := int64(80)
	projectHard := int64(500)
	projectBudget, err := budgetRepo.Create(ctx, TokenBudget{
		OrganizationID:  org.ID,
		ProjectID:       &project.ID,
		Period:          "daily",
		SoftLimitTokens: &projectSoft,
		HardLimitTokens: &projectHard,
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project budget: %v", err)
	}

	gotOrg, err := budgetRepo.GetByScope(ctx, org.ID, nil, "daily")
	if err != nil {
		t.Fatalf("GetByScope org: %v", err)
	}
	if gotOrg.ID != orgBudget.ID {
		t.Fatalf("org budget id = %s, want %s", gotOrg.ID, orgBudget.ID)
	}
	if gotOrg.ProjectID != nil {
		t.Fatalf("org budget project_id = %v, want nil", gotOrg.ProjectID)
	}

	gotProject, err := budgetRepo.GetByScope(ctx, org.ID, &project.ID, "daily")
	if err != nil {
		t.Fatalf("GetByScope project: %v", err)
	}
	if gotProject.ID != projectBudget.ID {
		t.Fatalf("project budget id = %s, want %s", gotProject.ID, projectBudget.ID)
	}
	if gotProject.ProjectID == nil || *gotProject.ProjectID != project.ID {
		t.Fatalf("project budget project_id = %v, want %s", gotProject.ProjectID, project.ID)
	}
}
