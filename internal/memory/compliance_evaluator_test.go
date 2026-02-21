package memory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComplianceEvaluatorAggregatesSeverityAndBlocking(t *testing.T) {
	report := BuildComplianceReport(ComplianceReportInput{
		IssueNumber: 847,
		Findings: []ComplianceRuleFinding{
			{
				RuleID:      "rule-required-pass",
				Title:       "Commit message conventions",
				Category:    "code_quality",
				Severity:    "required",
				Passed:      true,
				Details:     "all commits matched format",
				ActionLabel: "",
			},
			{
				RuleID:      "rule-required-fail",
				Title:       "All PRs require tests",
				Category:    "code_quality",
				Severity:    "required",
				Passed:      false,
				Details:     "new endpoint missing tests",
				ActionLabel: "Add endpoint tests",
			},
			{
				RuleID:      "rule-recommended-fail",
				Title:       "Documentation updated",
				Category:    "style",
				Severity:    "recommended",
				Passed:      false,
				Details:     "README not updated",
				ActionLabel: "Document endpoint",
			},
			{
				RuleID:      "rule-info",
				Title:       "Postgres metadata alignment",
				Category:    "technical",
				Severity:    "informational",
				Passed:      false,
				Details:     "metadata usage matches convention",
				ActionLabel: "",
			},
		},
	})

	require.True(t, report.Blocking)
	require.Len(t, report.PassedFindings, 1)
	require.Len(t, report.FailedRequiredFindings, 1)
	require.Len(t, report.FlaggedRecommendedFindings, 1)
	require.Len(t, report.InformationalFindings, 1)
}

func TestComplianceReportFormatting(t *testing.T) {
	report := BuildComplianceReport(ComplianceReportInput{
		IssueNumber: 847,
		Findings: []ComplianceRuleFinding{
			{
				RuleID:      "rule-required-pass",
				Title:       "Commit message conventions",
				Category:    "code_quality",
				Severity:    "required",
				Passed:      true,
				Details:     "all commits follow format",
				ActionLabel: "",
			},
			{
				RuleID:      "rule-required-fail",
				Title:       "All PRs require tests",
				Category:    "code_quality",
				Severity:    "required",
				Passed:      false,
				Details:     "new endpoint lacks test coverage",
				ActionLabel: "Add endpoint tests",
			},
			{
				RuleID:      "rule-recommended-fail",
				Title:       "Documentation updated",
				Category:    "style",
				Severity:    "recommended",
				Passed:      false,
				Details:     "README missing endpoint details",
				ActionLabel: "Update API docs",
			},
		},
	})

	markdown := report.Markdown
	require.Contains(t, markdown, "## Compliance Review - Issue #847")
	require.Contains(t, markdown, "### Passed")
	require.Contains(t, markdown, "### Failed (required)")
	require.Contains(t, markdown, "### Flagged (recommended)")
	require.Contains(t, markdown, "[code_quality] All PRs require tests")
	require.Contains(t, markdown, "Action: Add endpoint tests")
	require.Contains(t, markdown, "[style] Documentation updated")
}
