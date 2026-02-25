package worker

import (
	"context"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/turn"
)

// deterministicTurnModelGateway provides stable, non-network model responses for test mode workers.
type deterministicTurnModelGateway struct{}

func (deterministicTurnModelGateway) StreamComplete(_ context.Context, req turn.ModelRequest, onChunk func(token string) error) (turn.ModelResponse, error) {
	content := deterministicModelContent(req)
	for _, token := range strings.Fields(content) {
		if err := onChunk(token + " "); err != nil {
			return turn.ModelResponse{}, err
		}
	}
	return turn.ModelResponse{
		Content: content,
		Usage: &turn.ModelUsage{
			InputTokens:  deterministicInputTokens(req),
			OutputTokens: maxInt(1, len(strings.Fields(content))),
		},
	}, nil
}

func (deterministicTurnModelGateway) Complete(_ context.Context, req turn.ModelRequest) (turn.ModelResponse, error) {
	content := deterministicModelContent(req)
	return turn.ModelResponse{
		Content: content,
		Usage: &turn.ModelUsage{
			InputTokens:  deterministicInputTokens(req),
			OutputTokens: maxInt(1, len(strings.Fields(content))),
		},
	}, nil
}

func deterministicModelContent(req turn.ModelRequest) string {
	switch strings.TrimSpace(strings.ToLower(req.Purpose)) {
	case "listening_eval":
		return "respond now"
	case "continuation_summary":
		return "Continuation summarized."
	case "agent_turn":
		if req.Prompt != nil && req.Prompt.MemoryManifest.TotalMemories > 0 {
			return "I recall prior infrastructure details from memory."
		}
		return "Acknowledged."
	default:
		return "ok"
	}
}

func deterministicInputTokens(req turn.ModelRequest) int {
	if req.Prompt != nil && req.Prompt.TotalTokens > 0 {
		return req.Prompt.TotalTokens
	}
	total := 0
	for _, item := range req.HumanMessages {
		total += len(strings.Fields(item))
	}
	if total <= 0 {
		return 1
	}
	return total
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
