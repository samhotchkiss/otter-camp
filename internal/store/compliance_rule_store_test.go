package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComplianceRuleStoreCreateAndDisable(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "compliance-rule-create-org")
	projectID := createTestProject(t, db, orgID, "Compliance Rule Create Project")

	store := NewComplianceRuleStore(db)
	created, err := store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgID,
		ProjectID:        &projectID,
		Title:            "All PRs require tests",
		Description:      "Every PR must include tests",
		CheckInstruction: "Verify added functionality has tests.",
		Category:         ComplianceRuleCategoryCodeQuality,
		Severity:         ComplianceRuleSeverityRequired,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, orgID, created.OrgID)
	require.NotNil(t, created.ProjectID)
	require.Equal(t, projectID, *created.ProjectID)
	require.True(t, created.Enabled)

	err = store.SetEnabled(context.Background(), orgID, created.ID, false)
	require.NoError(t, err)

	var enabled bool
	err = db.QueryRow(`SELECT enabled FROM compliance_rules WHERE id = $1`, created.ID).Scan(&enabled)
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestComplianceRuleStoreListApplicableRules(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "compliance-rule-scope-org-a")
	orgB := createTestOrganization(t, db, "compliance-rule-scope-org-b")
	projectA := createTestProject(t, db, orgA, "Scope Project A")
	projectB := createTestProject(t, db, orgA, "Scope Project B")
	projectOtherOrg := createTestProject(t, db, orgB, "Scope Project Other Org")

	store := NewComplianceRuleStore(db)

	_, err := store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgA,
		Title:            "Org-wide required rule",
		Description:      "Applies to all projects",
		CheckInstruction: "check org-wide",
		Category:         ComplianceRuleCategoryProcess,
		Severity:         ComplianceRuleSeverityRequired,
	})
	require.NoError(t, err)

	projectRule, err := store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgA,
		ProjectID:        &projectA,
		Title:            "Project A rule",
		Description:      "Project-specific rule",
		CheckInstruction: "check project-a",
		Category:         ComplianceRuleCategoryScope,
		Severity:         ComplianceRuleSeverityRecommended,
	})
	require.NoError(t, err)

	projectBRule, err := store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgA,
		ProjectID:        &projectB,
		Title:            "Project B disabled rule",
		Description:      "Should not apply to Project A",
		CheckInstruction: "check project-b",
		Category:         ComplianceRuleCategoryScope,
		Severity:         ComplianceRuleSeverityRequired,
	})
	require.NoError(t, err)
	require.NoError(t, store.SetEnabled(context.Background(), orgA, projectBRule.ID, false))

	_, err = store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgB,
		ProjectID:        &projectOtherOrg,
		Title:            "Other org rule",
		Description:      "Must not leak cross-org",
		CheckInstruction: "check other-org",
		Category:         ComplianceRuleCategoryTechnical,
		Severity:         ComplianceRuleSeverityRequired,
	})
	require.NoError(t, err)

	applicable, err := store.ListApplicableRules(context.Background(), orgA, &projectA)
	require.NoError(t, err)
	require.Len(t, applicable, 2)

	titles := map[string]bool{}
	for _, rule := range applicable {
		titles[rule.Title] = true
	}
	require.True(t, titles["Org-wide required rule"])
	require.True(t, titles["Project A rule"])
	require.False(t, titles["Project B disabled rule"])

	err = store.SetEnabled(context.Background(), orgA, projectRule.ID, false)
	require.NoError(t, err)

	applicable, err = store.ListApplicableRules(context.Background(), orgA, &projectA)
	require.NoError(t, err)
	require.Len(t, applicable, 1)
	require.Equal(t, "Org-wide required rule", applicable[0].Title)
}

func TestComplianceRuleStoreRejectsInvalidCategoryAndSeverity(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "compliance-rule-invalid-org")
	store := NewComplianceRuleStore(db)

	_, err := store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgID,
		Title:            "Invalid category rule",
		Description:      "invalid category",
		CheckInstruction: "invalid category",
		Category:         "invalid",
		Severity:         ComplianceRuleSeverityRequired,
	})
	require.ErrorContains(t, err, "invalid category")

	_, err = store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgID,
		Title:            "Invalid severity rule",
		Description:      "invalid severity",
		CheckInstruction: "invalid severity",
		Category:         ComplianceRuleCategoryProcess,
		Severity:         "invalid",
	})
	require.ErrorContains(t, err, "invalid severity")
}

func TestComplianceRuleStoreUpdateAndGetByID(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "compliance-rule-update-org")
	projectID := createTestProject(t, db, orgID, "Compliance Rule Update Project")
	store := NewComplianceRuleStore(db)

	created, err := store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgID,
		ProjectID:        &projectID,
		Title:            "Original title",
		Description:      "Original description",
		CheckInstruction: "Original instruction",
		Category:         ComplianceRuleCategoryProcess,
		Severity:         ComplianceRuleSeverityRequired,
	})
	require.NoError(t, err)

	updated, err := store.Update(context.Background(), orgID, created.ID, UpdateComplianceRuleInput{
		Description: complianceRuleStringPtr("Updated description"),
		Severity:    complianceRuleStringPtr(ComplianceRuleSeverityRecommended),
	})
	require.NoError(t, err)
	require.Equal(t, "Updated description", updated.Description)
	require.Equal(t, ComplianceRuleSeverityRecommended, updated.Severity)

	loaded, err := store.GetByID(context.Background(), orgID, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, loaded.ID)
	require.Equal(t, "Updated description", loaded.Description)
	require.Equal(t, ComplianceRuleSeverityRecommended, loaded.Severity)
}

func TestComplianceRuleStoreCreateRejectsCrossOrgProject(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "compliance-rule-cross-project-org-a")
	orgB := createTestOrganization(t, db, "compliance-rule-cross-project-org-b")
	projectB := createTestProject(t, db, orgB, "Compliance Rule Cross Project B")
	store := NewComplianceRuleStore(db)

	_, err := store.Create(context.Background(), CreateComplianceRuleInput{
		OrgID:            orgA,
		ProjectID:        &projectB,
		Title:            "Cross project scope",
		Description:      "Should fail",
		CheckInstruction: "Should fail",
		Category:         ComplianceRuleCategoryScope,
		Severity:         ComplianceRuleSeverityRequired,
	})
	require.ErrorContains(t, err, "project_id does not belong to org")
}

func complianceRuleStringPtr(value string) *string {
	return &value
}
