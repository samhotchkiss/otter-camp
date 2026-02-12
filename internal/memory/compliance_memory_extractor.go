package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type ComplianceMemoryExtractionInput struct {
	Issue      store.ProjectIssue
	Report     ComplianceReport
	OccurredAt time.Time
}

type ComplianceMemoryCandidate struct {
	Fingerprint string
	Memory      store.CreateEllieExtractedMemoryInput
}

func BuildComplianceMemoryCandidates(input ComplianceMemoryExtractionInput) []ComplianceMemoryCandidate {
	issue := input.Issue
	if strings.TrimSpace(issue.OrgID) == "" {
		return nil
	}

	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	candidates := make([]ComplianceMemoryCandidate, 0, len(input.Report.PassedFindings)+len(input.Report.FailedRequiredFindings)+len(input.Report.FlaggedRecommendedFindings)+len(input.Report.InformationalFindings))
	seen := make(map[string]struct{})

	appendFinding := func(finding ComplianceRuleFinding) {
		fingerprint := complianceFindingFingerprint(finding)
		if _, ok := seen[fingerprint]; ok {
			return
		}
		seen[fingerprint] = struct{}{}

		kind := complianceFindingMemoryKind(finding)
		title := strings.TrimSpace(finding.Title)
		if title == "" {
			title = "Compliance finding"
		}
		content := strings.TrimSpace(finding.Details)
		if content == "" {
			content = fmt.Sprintf("%s compliance finding without explicit details.", strings.ReplaceAll(kind, "_", " "))
		}
		if action := strings.TrimSpace(finding.ActionLabel); action != "" {
			content = content + "\nAction: " + action
		}

		metadata := map[string]any{
			"source":                 "compliance_review",
			"issue_id":               strings.TrimSpace(issue.ID),
			"issue_number":           issue.IssueNumber,
			"issue_title":            strings.TrimSpace(issue.Title),
			"rule_id":                strings.TrimSpace(finding.RuleID),
			"rule_title":             strings.TrimSpace(finding.Title),
			"category":               strings.TrimSpace(finding.Category),
			"severity":               strings.TrimSpace(finding.Severity),
			"passed":                 finding.Passed,
			"details":                strings.TrimSpace(finding.Details),
			"action_label":           strings.TrimSpace(finding.ActionLabel),
			"compliance_fingerprint": fingerprint,
		}
		metadataRaw, err := json.Marshal(metadata)
		if err != nil {
			metadataRaw = json.RawMessage(`{}`)
		}

		var sourceProjectID *string
		if trimmedProjectID := strings.TrimSpace(issue.ProjectID); trimmedProjectID != "" {
			sourceProjectID = &trimmedProjectID
		}

		candidates = append(candidates, ComplianceMemoryCandidate{
			Fingerprint: fingerprint,
			Memory: store.CreateEllieExtractedMemoryInput{
				OrgID:           strings.TrimSpace(issue.OrgID),
				Kind:            kind,
				Title:           title,
				Content:         content,
				Metadata:        metadataRaw,
				Importance:      complianceFindingImportance(kind),
				Confidence:      complianceFindingConfidence(finding),
				Status:          "active",
				OccurredAt:      occurredAt,
				SourceProjectID: sourceProjectID,
			},
		})
	}

	for _, finding := range input.Report.PassedFindings {
		appendFinding(finding)
	}
	for _, finding := range input.Report.FailedRequiredFindings {
		appendFinding(finding)
	}
	for _, finding := range input.Report.FlaggedRecommendedFindings {
		appendFinding(finding)
	}
	for _, finding := range input.Report.InformationalFindings {
		appendFinding(finding)
	}

	return candidates
}

func complianceFindingMemoryKind(finding ComplianceRuleFinding) string {
	if finding.Passed {
		return "pattern"
	}
	if strings.EqualFold(strings.TrimSpace(finding.Severity), store.ComplianceRuleSeverityRequired) {
		return "anti_pattern"
	}
	return "lesson"
}

func complianceFindingImportance(kind string) int {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "anti_pattern":
		return 5
	case "lesson":
		return 4
	default:
		return 3
	}
}

func complianceFindingConfidence(finding ComplianceRuleFinding) float64 {
	if finding.Passed {
		return 0.82
	}
	return 0.88
}

func complianceFindingFingerprint(finding ComplianceRuleFinding) string {
	normalized := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(finding.RuleID)),
		strings.ToLower(strings.TrimSpace(finding.Title)),
		strings.ToLower(strings.TrimSpace(finding.Category)),
		strings.ToLower(strings.TrimSpace(finding.Severity)),
		strings.ToLower(strings.TrimSpace(finding.Details)),
		strings.ToLower(strings.TrimSpace(finding.ActionLabel)),
		fmt.Sprintf("%t", finding.Passed),
	}, "|")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:16])
}
