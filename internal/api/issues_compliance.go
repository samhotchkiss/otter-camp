package api

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type issueComplianceReviewer interface {
	ReviewIssueClose(ctx context.Context, issue store.ProjectIssue) (memory.ComplianceReport, error)
}

type issueComplianceRuleEvaluator interface {
	Evaluate(ctx context.Context, input issueComplianceEvaluationInput) (memory.ComplianceRuleFinding, error)
}

type issueComplianceEvaluationInput struct {
	Rule              store.ComplianceRule
	Issue             store.ProjectIssue
	AcceptanceContext string
	ChangedArtifacts  []string
}

type defaultIssueComplianceReviewer struct {
	RuleStore   *store.ComplianceRuleStore
	CommitStore *store.ProjectCommitStore
	Evaluator   issueComplianceRuleEvaluator
}

type heuristicIssueComplianceRuleEvaluator struct{}

func newDefaultIssueComplianceReviewer(
	ruleStore *store.ComplianceRuleStore,
	commitStore *store.ProjectCommitStore,
) *defaultIssueComplianceReviewer {
	return &defaultIssueComplianceReviewer{
		RuleStore:   ruleStore,
		CommitStore: commitStore,
		Evaluator:   heuristicIssueComplianceRuleEvaluator{},
	}
}

func (r *defaultIssueComplianceReviewer) ReviewIssueClose(
	ctx context.Context,
	issue store.ProjectIssue,
) (memory.ComplianceReport, error) {
	if r == nil || r.RuleStore == nil {
		return memory.BuildComplianceReport(memory.ComplianceReportInput{
			IssueNumber: int(issue.IssueNumber),
			Findings:    nil,
		}), nil
	}

	var projectID *string
	if trimmedProjectID := strings.TrimSpace(issue.ProjectID); trimmedProjectID != "" {
		projectID = &trimmedProjectID
	}

	rules, err := r.RuleStore.ListApplicableRules(ctx, issue.OrgID, projectID)
	if err != nil {
		return memory.ComplianceReport{}, err
	}

	acceptanceContext := buildIssueAcceptanceContext(issue)
	changedArtifacts := collectIssueChangedArtifacts(ctx, r.CommitStore, issue.ProjectID)

	findings := make([]memory.ComplianceRuleFinding, 0, len(rules))
	for _, rule := range rules {
		finding, evalErr := r.Evaluator.Evaluate(ctx, issueComplianceEvaluationInput{
			Rule:              rule,
			Issue:             issue,
			AcceptanceContext: acceptanceContext,
			ChangedArtifacts:  changedArtifacts,
		})
		if evalErr != nil {
			return memory.ComplianceReport{}, evalErr
		}
		findings = append(findings, finding)
	}

	return memory.BuildComplianceReport(memory.ComplianceReportInput{
		IssueNumber: int(issue.IssueNumber),
		Findings:    findings,
	}), nil
}

func (heuristicIssueComplianceRuleEvaluator) Evaluate(
	_ context.Context,
	input issueComplianceEvaluationInput,
) (memory.ComplianceRuleFinding, error) {
	rule := input.Rule
	finding := memory.ComplianceRuleFinding{
		RuleID:   strings.TrimSpace(rule.ID),
		Title:    strings.TrimSpace(rule.Title),
		Category: strings.TrimSpace(rule.Category),
		Severity: strings.TrimSpace(rule.Severity),
		Passed:   true,
		Details:  "No compliance issues detected from issue context and recent artifact metadata.",
	}

	ruleText := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(rule.Title),
		strings.TrimSpace(rule.Description),
		strings.TrimSpace(rule.CheckInstruction),
	}, " "))
	evidence := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(input.Issue.Title),
		optionalIssueBodyText(input.Issue.Body),
		strings.TrimSpace(input.AcceptanceContext),
		strings.Join(input.ChangedArtifacts, " "),
	}, " "))

	if strings.Contains(ruleText, "test") && !strings.Contains(evidence, "test") {
		finding.Passed = false
		finding.Details = "No test coverage evidence found in issue context or recent commit metadata."
		finding.ActionLabel = "Add or reference tests that cover the implemented behavior."
		return finding, nil
	}

	if strings.Contains(ruleText, "secret") && containsCredentialSignal(evidence) {
		finding.Passed = false
		finding.Details = "Potential credential-like token detected in issue or commit context."
		finding.ActionLabel = "Remove secrets and rotate any exposed credentials."
		return finding, nil
	}

	return finding, nil
}

func (h *IssuesHandler) runIssueCloseComplianceReviewBestEffort(
	ctx context.Context,
	issue *store.ProjectIssue,
) *store.ProjectIssue {
	if h == nil || issue == nil || h.ComplianceReviewer == nil {
		return issue
	}

	report, err := h.ComplianceReviewer.ReviewIssueClose(ctx, *issue)
	if err != nil {
		log.Printf("issues: compliance review failed for issue %s: %v", issue.ID, err)
		return issue
	}

	h.attachComplianceReportCommentBestEffort(ctx, issue.ID, report)
	h.ingestComplianceReviewMemoriesBestEffort(ctx, *issue, report)
	if !report.Blocking {
		return issue
	}

	reopened, err := h.IssueStore.UpdateIssueWorkTracking(ctx, store.UpdateProjectIssueWorkTrackingInput{
		IssueID:  issue.ID,
		SetState: true,
		State:    "open",
	})
	if err != nil {
		log.Printf("issues: failed to reopen issue %s after blocking compliance review: %v", issue.ID, err)
		return issue
	}

	return reopened
}

func (h *IssuesHandler) ingestComplianceReviewMemoriesBestEffort(
	ctx context.Context,
	issue store.ProjectIssue,
	report memory.ComplianceReport,
) {
	if h == nil || h.EllieIngestionStore == nil {
		return
	}

	candidates := memory.BuildComplianceMemoryCandidates(memory.ComplianceMemoryExtractionInput{
		Issue:      issue,
		Report:     report,
		OccurredAt: time.Now().UTC(),
	})
	for _, candidate := range candidates {
		exists, err := h.EllieIngestionStore.HasComplianceFingerprint(ctx, issue.OrgID, candidate.Fingerprint)
		if err != nil {
			log.Printf("issues: compliance memory dedupe check failed for issue %s: %v", issue.ID, err)
			continue
		}
		if exists {
			continue
		}

		if _, err := h.EllieIngestionStore.CreateEllieExtractedMemory(ctx, candidate.Memory); err != nil {
			log.Printf("issues: compliance memory ingestion failed for issue %s: %v", issue.ID, err)
		}
	}
}

func (h *IssuesHandler) attachComplianceReportCommentBestEffort(
	ctx context.Context,
	issueID string,
	report memory.ComplianceReport,
) {
	if h == nil || h.IssueStore == nil {
		return
	}

	reportMarkdown := strings.TrimSpace(report.Markdown)
	if reportMarkdown == "" {
		return
	}

	authorAgentID, err := h.ensureEllieComplianceAuthorID(ctx)
	if err != nil {
		log.Printf("issues: compliance report comment skipped for issue %s: %v", issueID, err)
		return
	}

	if _, err := h.IssueStore.CreateComment(ctx, store.CreateProjectIssueCommentInput{
		IssueID:       strings.TrimSpace(issueID),
		AuthorAgentID: authorAgentID,
		Body:          reportMarkdown,
	}); err != nil {
		log.Printf("issues: failed to persist compliance report comment for issue %s: %v", issueID, err)
	}
}

func (h *IssuesHandler) ensureEllieComplianceAuthorID(ctx context.Context) (string, error) {
	if h == nil {
		return "", errors.New("issues handler is nil")
	}

	agentStore := h.AgentStore
	if agentStore == nil && h.DB != nil {
		agentStore = store.NewAgentStore(h.DB)
	}
	if agentStore == nil {
		return "", errors.New("agent store unavailable")
	}

	agent, err := agentStore.GetBySlug(ctx, "ellie")
	if err == nil {
		return agent.ID, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}

	created, createErr := agentStore.Create(ctx, store.CreateAgentInput{
		Slug:        "ellie",
		DisplayName: "Ellie",
		Status:      "active",
	})
	if createErr == nil {
		return created.ID, nil
	}

	agent, getErr := agentStore.GetBySlug(ctx, "ellie")
	if getErr == nil {
		return agent.ID, nil
	}
	return "", createErr
}

func buildIssueAcceptanceContext(issue store.ProjectIssue) string {
	parts := make([]string, 0, 2)
	if issue.Body != nil {
		if body := strings.TrimSpace(*issue.Body); body != "" {
			parts = append(parts, body)
		}
	}
	if issue.DocumentPath != nil {
		if documentPath := strings.TrimSpace(*issue.DocumentPath); documentPath != "" {
			parts = append(parts, "Linked document: "+documentPath)
		}
	}
	return strings.Join(parts, "\n")
}

func collectIssueChangedArtifacts(
	ctx context.Context,
	commitStore *store.ProjectCommitStore,
	projectID string,
) []string {
	if commitStore == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}

	commits, err := commitStore.ListCommits(ctx, store.ProjectCommitFilter{
		ProjectID: strings.TrimSpace(projectID),
		Limit:     20,
	})
	if err != nil {
		return nil
	}

	artifacts := make([]string, 0, len(commits)*2)
	seen := make(map[string]struct{}, len(commits)*2)
	for _, commit := range commits {
		for _, raw := range []string{
			strings.TrimSpace(commit.Subject),
			strings.TrimSpace(commit.Message),
		} {
			if raw == "" {
				continue
			}
			key := strings.ToLower(raw)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			artifacts = append(artifacts, raw)
		}
	}
	return artifacts
}

func containsCredentialSignal(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	credentialSignals := []string{
		"api_key",
		"api key",
		"secret=",
		"token=",
		"password=",
	}
	for _, signal := range credentialSignals {
		if strings.Contains(value, signal) {
			return true
		}
	}
	return false
}

func optionalIssueBodyText(body *string) string {
	if body == nil {
		return ""
	}
	return strings.TrimSpace(*body)
}
