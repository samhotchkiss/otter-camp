package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

// gatewaySummarizationModel adapts a turn.ModelGateway to implement chat.SummarizationModel.
// It formats a SummarizationRequest into a simple transcript and calls Complete.
type gatewaySummarizationModel struct {
	gateway turn.ModelGateway
}

func (m *gatewaySummarizationModel) Summarize(ctx context.Context, req chat.SummarizationRequest) (chat.SummarizationResponse, error) {
	if m == nil || m.gateway == nil {
		return chat.SummarizationResponse{}, fmt.Errorf("summarization gateway is not configured")
	}

	var sb strings.Builder
	for _, msg := range req.Messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	for _, s := range req.Summaries {
		text := strings.TrimSpace(s.SummaryText)
		if text == "" {
			continue
		}
		sb.WriteString("[previous summary]\n")
		sb.WriteString(text)
		sb.WriteString("\n\n")
	}

	transcript := strings.TrimSpace(sb.String())
	if transcript == "" {
		return chat.SummarizationResponse{SummaryText: ""}, nil
	}

	systemPrompt := strings.TrimSpace(req.Prompt)
	if systemPrompt == "" {
		systemPrompt = "Summarize the provided conversation."
	}
	totalTokens := estimateSummarizationPromptTokens(systemPrompt, transcript)

	modelReq := turn.ModelRequest{
		OrganizationID: req.OrganizationID,
		SessionID:      req.SessionID,
		TurnID:         uuid.New(),
		Purpose:        req.InvocationPurpose,
		Profile:        req.Profile,
		Prompt: &prompt.AssembledPrompt{
			SystemPrompt: systemPrompt,
			Messages: []prompt.PromptMessage{
				{Role: "user", Content: transcript},
			},
			TotalTokens: totalTokens,
		},
	}

	response, err := m.gateway.Complete(ctx, modelReq)
	if err != nil {
		return chat.SummarizationResponse{}, err
	}

	return chat.SummarizationResponse{SummaryText: strings.TrimSpace(response.Content)}, nil
}

func estimateSummarizationPromptTokens(systemPrompt, transcript string) int {
	total := 0
	if strings.TrimSpace(systemPrompt) != "" {
		total += len(systemPrompt) / 3
		total += 64
	}
	if strings.TrimSpace(transcript) != "" {
		total += len(transcript) / 3
		total += 64
	}
	return total
}
