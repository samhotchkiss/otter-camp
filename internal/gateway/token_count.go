package gateway

import (
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	tiktoken "github.com/pkoukk/tiktoken-go"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type TokenUsage struct {
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
}

type TokenCounter interface {
	Count(invocation repo.ModelInvocation, response []byte) TokenUsage
}

type DefaultTokenCounter struct {
	mu         sync.Mutex
	encByModel map[string]*tiktoken.Tiktoken
}

func NewDefaultTokenCounter() *DefaultTokenCounter {
	return &DefaultTokenCounter{
		encByModel: make(map[string]*tiktoken.Tiktoken),
	}
}

func (c *DefaultTokenCounter) Count(invocation repo.ModelInvocation, response []byte) TokenUsage {
	prompt := string(invocation.Metadata)
	completion := string(response)
	if isAnthropicModel(invocation.ModelName) {
		return TokenUsage{
			InputTokens:  estimateAnthropicTokens(prompt),
			OutputTokens: estimateAnthropicTokens(completion),
		}
	}

	return TokenUsage{
		InputTokens:  c.countOpenAITokens(invocation.ModelName, prompt),
		OutputTokens: c.countOpenAITokens(invocation.ModelName, completion),
	}
}

func (c *DefaultTokenCounter) countOpenAITokens(modelName, text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}

	modelKey := strings.TrimSpace(modelName)
	if modelKey == "" {
		modelKey = "cl100k_base"
	}

	enc := c.encodingForModel(modelKey)
	if enc == nil {
		return estimateAnthropicTokens(text)
	}

	return len(enc.Encode(text, nil, nil))
}

func (c *DefaultTokenCounter) encodingForModel(model string) *tiktoken.Tiktoken {
	c.mu.Lock()
	defer c.mu.Unlock()

	if enc, ok := c.encByModel[model]; ok {
		return enc
	}

	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return nil
		}
	}

	c.encByModel[model] = enc
	return enc
}

func estimateAnthropicTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}

	runes := utf8.RuneCountInString(trimmed)
	return int(math.Ceil(float64(runes) / 4.0))
}

func isAnthropicModel(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	return strings.Contains(lower, "claude") || strings.Contains(lower, "anthropic")
}
