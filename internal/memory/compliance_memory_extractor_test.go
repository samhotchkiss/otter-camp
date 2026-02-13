package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestComplianceFindingsProduceMemoryCandidates(t *testing.T) {
	issue := store.ProjectIssue{
		ID:          "11111111-1111-1111-1111-111111111111",
		OrgID:       "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		ProjectID:   "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		IssueNumber: 847,
		Title:       "Compliance extraction test issue",
	}
	occurredAt := time.Date(2026, 2, 12, 22, 0, 0, 0, time.UTC)

	report := BuildComplianceReport(ComplianceReportInput{
		IssueNumber: int(issue.IssueNumber),
		Findings: []ComplianceRuleFinding{
			{
				RuleID:   "rule-pass",
				Title:    "Tests included",
				Category: store.ComplianceRuleCategoryCodeQuality,
				Severity: store.ComplianceRuleSeverityRequired,
				Passed:   true,
				Details:  "Test updates detected.",
			},
			{
				RuleID:      "rule-required-fail",
				Title:       "No secrets in code",
				Category:    store.ComplianceRuleCategorySecurity,
				Severity:    store.ComplianceRuleSeverityRequired,
				Passed:      false,
				Details:     "Secret-like token found in commit metadata.",
				ActionLabel: "Remove secret and rotate credentials.",
			},
			{
				RuleID:      "rule-recommended-fail",
				Title:       "Documentation updated",
				Category:    store.ComplianceRuleCategoryStyle,
				Severity:    store.ComplianceRuleSeverityRecommended,
				Passed:      false,
				Details:     "README update missing.",
				ActionLabel: "Update README with endpoint behavior.",
			},
		},
	})

	candidates := BuildComplianceMemoryCandidates(ComplianceMemoryExtractionInput{
		Issue:      issue,
		Report:     report,
		OccurredAt: occurredAt,
	})
	require.Len(t, candidates, 3)

	kinds := map[string]bool{}
	for _, candidate := range candidates {
		kinds[candidate.Memory.Kind] = true
		require.Equal(t, issue.OrgID, candidate.Memory.OrgID)
		require.NotNil(t, candidate.Memory.SourceProjectID)
		require.Equal(t, issue.ProjectID, *candidate.Memory.SourceProjectID)
		require.Equal(t, occurredAt, candidate.Memory.OccurredAt)
		require.NotEmpty(t, candidate.Fingerprint)
		require.NotEmpty(t, candidate.Memory.Metadata)
	}
	require.True(t, kinds["pattern"])
	require.True(t, kinds["anti_pattern"])
	require.True(t, kinds["lesson"])

	var metadata map[string]any
	err := json.Unmarshal(candidates[0].Memory.Metadata, &metadata)
	require.NoError(t, err)
	require.Equal(t, issue.ID, metadata["issue_id"])
	require.Equal(t, float64(issue.IssueNumber), metadata["issue_number"])
	require.Equal(t, "compliance_review", metadata["source"])
	require.NotEmpty(t, metadata["compliance_fingerprint"])
}

func TestComplianceMemoryExtractionDedupesRepeatedFindings(t *testing.T) {
	issue := store.ProjectIssue{
		ID:          "22222222-2222-2222-2222-222222222222",
		OrgID:       "cccccccc-cccc-cccc-cccc-cccccccccccc",
		ProjectID:   "dddddddd-dddd-dddd-dddd-dddddddddddd",
		IssueNumber: 9001,
		Title:       "Compliance dedupe issue",
	}

	report := BuildComplianceReport(ComplianceReportInput{
		IssueNumber: int(issue.IssueNumber),
		Findings: []ComplianceRuleFinding{
			{
				RuleID:      "rule-repeat",
				Title:       "Tests required",
				Category:    store.ComplianceRuleCategoryCodeQuality,
				Severity:    store.ComplianceRuleSeverityRequired,
				Passed:      false,
				Details:     "No tests found.",
				ActionLabel: "Add tests.",
			},
			{
				RuleID:      "rule-repeat",
				Title:       "tests required",
				Category:    store.ComplianceRuleCategoryCodeQuality,
				Severity:    store.ComplianceRuleSeverityRequired,
				Passed:      false,
				Details:     "  no tests found.  ",
				ActionLabel: "add tests.",
			},
			{
				RuleID:      "rule-repeat",
				Title:       "Tests required",
				Category:    store.ComplianceRuleCategoryCodeQuality,
				Severity:    store.ComplianceRuleSeverityRequired,
				Passed:      false,
				Details:     "No tests found.",
				ActionLabel: "Add tests.",
			},
		},
	})

	candidates := BuildComplianceMemoryCandidates(ComplianceMemoryExtractionInput{
		Issue:  issue,
		Report: report,
	})
	require.Len(t, candidates, 1)
	require.Equal(t, "anti_pattern", candidates[0].Memory.Kind)
}
