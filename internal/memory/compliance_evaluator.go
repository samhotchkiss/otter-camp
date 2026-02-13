package memory

import (
	"fmt"
	"strings"
)

type ComplianceRuleFinding struct {
	RuleID      string
	Title       string
	Category    string
	Severity    string
	Passed      bool
	Details     string
	ActionLabel string
}

type ComplianceReportInput struct {
	IssueNumber int
	Findings    []ComplianceRuleFinding
}

type ComplianceReport struct {
	IssueNumber                 int
	Blocking                    bool
	PassedFindings              []ComplianceRuleFinding
	FailedRequiredFindings      []ComplianceRuleFinding
	FlaggedRecommendedFindings  []ComplianceRuleFinding
	InformationalFindings       []ComplianceRuleFinding
	Markdown                    string
}

func BuildComplianceReport(input ComplianceReportInput) ComplianceReport {
	report := ComplianceReport{
		IssueNumber: input.IssueNumber,
	}

	for _, finding := range input.Findings {
		severity := strings.TrimSpace(strings.ToLower(finding.Severity))
		switch {
		case finding.Passed:
			report.PassedFindings = append(report.PassedFindings, finding)
		case severity == "required":
			report.FailedRequiredFindings = append(report.FailedRequiredFindings, finding)
		case severity == "recommended":
			report.FlaggedRecommendedFindings = append(report.FlaggedRecommendedFindings, finding)
		default:
			report.InformationalFindings = append(report.InformationalFindings, finding)
		}
	}

	report.Blocking = len(report.FailedRequiredFindings) > 0
	report.Markdown = formatComplianceReportMarkdown(report)
	return report
}

func formatComplianceReportMarkdown(report ComplianceReport) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("## Compliance Review - Issue #%d\n", report.IssueNumber))

	appendComplianceSection(&builder, "Passed", report.PassedFindings)
	appendComplianceSection(&builder, "Failed (required)", report.FailedRequiredFindings)
	appendComplianceSection(&builder, "Flagged (recommended)", report.FlaggedRecommendedFindings)
	appendComplianceSection(&builder, "Notes (informational)", report.InformationalFindings)

	return strings.TrimSpace(builder.String())
}

func appendComplianceSection(builder *strings.Builder, title string, findings []ComplianceRuleFinding) {
	if len(findings) == 0 {
		return
	}
	builder.WriteString("\n\n")
	builder.WriteString("### ")
	builder.WriteString(title)
	builder.WriteString("\n")

	for _, finding := range findings {
		builder.WriteString(fmt.Sprintf("- [%s] %s - %s\n", strings.TrimSpace(finding.Category), strings.TrimSpace(finding.Title), strings.TrimSpace(finding.Details)))
		if strings.TrimSpace(finding.ActionLabel) != "" {
			builder.WriteString(fmt.Sprintf("  - Action: %s\n", strings.TrimSpace(finding.ActionLabel)))
		}
	}
}
