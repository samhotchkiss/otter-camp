package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type EllieOpenClawEntitySynthesizer struct {
	Caller *OpenClawGatewayCaller
}

func (s *EllieOpenClawEntitySynthesizer) Synthesize(ctx context.Context, input EllieEntitySynthesisInput) (EllieEntitySynthesisOutput, error) {
	if s == nil || s.Caller == nil {
		return EllieEntitySynthesisOutput{}, fmt.Errorf("openclaw entity synthesizer is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	if orgID == "" {
		return EllieEntitySynthesisOutput{}, fmt.Errorf("org_id is required")
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return EllieEntitySynthesisOutput{}, fmt.Errorf("prompt is required")
	}

	callResult, err := s.Caller.Call(ctx, orgID, prompt)
	if err != nil {
		return EllieEntitySynthesisOutput{}, err
	}

	rawJSON, err := extractEllieIngestionOpenClawJSON(callResult.Text)
	if err != nil {
		return EllieEntitySynthesisOutput{}, fmt.Errorf("decode entity synthesis payload: %w", err)
	}

	var parsed struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawJSON)), &parsed); err != nil {
		return EllieEntitySynthesisOutput{}, fmt.Errorf("decode entity synthesis json: %w", err)
	}

	return EllieEntitySynthesisOutput{
		Title:   strings.TrimSpace(parsed.Title),
		Content: strings.TrimSpace(parsed.Content),
		Model:   strings.TrimSpace(callResult.Model),
		TraceID: strings.TrimSpace(callResult.TraceID),
	}, nil
}

type EllieOpenClawTaxonomyClassifier struct {
	Caller *OpenClawGatewayCaller
}

func (c *EllieOpenClawTaxonomyClassifier) ClassifyMemory(ctx context.Context, input EllieTaxonomyLLMClassificationInput) (EllieTaxonomyLLMClassificationOutput, error) {
	if c == nil || c.Caller == nil {
		return EllieTaxonomyLLMClassificationOutput{}, fmt.Errorf("openclaw taxonomy classifier is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	if orgID == "" {
		return EllieTaxonomyLLMClassificationOutput{}, fmt.Errorf("org_id is required")
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return EllieTaxonomyLLMClassificationOutput{}, fmt.Errorf("prompt is required")
	}

	callResult, err := c.Caller.Call(ctx, orgID, prompt)
	if err != nil {
		return EllieTaxonomyLLMClassificationOutput{}, err
	}
	rawJSON, err := extractEllieIngestionOpenClawJSON(callResult.Text)
	if err != nil {
		return EllieTaxonomyLLMClassificationOutput{}, fmt.Errorf("decode taxonomy classification payload: %w", err)
	}
	return EllieTaxonomyLLMClassificationOutput{
		Model:   strings.TrimSpace(callResult.Model),
		TraceID: strings.TrimSpace(callResult.TraceID),
		RawJSON: strings.TrimSpace(rawJSON),
	}, nil
}

type EllieOpenClawDedupReviewer struct {
	Caller *OpenClawGatewayCaller
}

func (r *EllieOpenClawDedupReviewer) Review(ctx context.Context, input EllieDedupReviewInput) (EllieDedupDecision, error) {
	if r == nil || r.Caller == nil {
		return EllieDedupDecision{}, fmt.Errorf("openclaw dedup reviewer is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	if orgID == "" {
		return EllieDedupDecision{}, fmt.Errorf("org_id is required")
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return EllieDedupDecision{}, fmt.Errorf("prompt is required")
	}

	callResult, err := r.Caller.Call(ctx, orgID, prompt)
	if err != nil {
		return EllieDedupDecision{}, err
	}
	rawJSON, err := extractEllieIngestionOpenClawJSON(callResult.Text)
	if err != nil {
		return EllieDedupDecision{}, fmt.Errorf("decode dedup decision payload: %w", err)
	}
	clusterIDs := make([]string, 0, len(input.Cluster.MemoryIDs))
	clusterIDs = append(clusterIDs, input.Cluster.MemoryIDs...)
	return ParseAndValidateEllieDedupDecision(clusterIDs, rawJSON)
}

type EllieOpenClawProjectDocSummarizer struct {
	Caller *OpenClawGatewayCaller
}

func (s *EllieOpenClawProjectDocSummarizer) Summarize(ctx context.Context, input EllieProjectDocSummaryInput) (string, error) {
	if s == nil || s.Caller == nil {
		return "", fmt.Errorf("openclaw doc summarizer is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	if orgID == "" {
		return "", fmt.Errorf("org_id is required")
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "Untitled"
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}

	prompt := buildEllieProjectDocSummaryPrompt(input.FilePath, title, content, input.SectionIndex, input.SectionTotal)
	callResult, err := s.Caller.Call(ctx, orgID, prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(callResult.Text), nil
}

func buildEllieProjectDocSummaryPrompt(filePath, title, content string, sectionIndex, sectionTotal int) string {
	filePath = strings.TrimSpace(filePath)
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if sectionIndex < 0 {
		sectionIndex = 0
	}
	if sectionTotal <= 0 {
		sectionTotal = 1
	}

	var builder strings.Builder
	builder.WriteString("You summarize project documentation for semantic retrieval.\\n")
	builder.WriteString("Write a concise, information-dense summary (2-5 sentences).\\n")
	builder.WriteString("Preserve concrete details: commands, file paths, APIs, config keys, table names, IDs, version numbers.\\n")
	builder.WriteString("Do not add speculation. Do not use markdown headings.\\n\\n")
	if filePath != "" {
		builder.WriteString("File: " + filePath + "\\n")
	}
	builder.WriteString("Title: " + title + "\\n")
	builder.WriteString(fmt.Sprintf("Section: %d/%d\\n\\n", sectionIndex+1, sectionTotal))
	builder.WriteString(content)
	return builder.String()
}

