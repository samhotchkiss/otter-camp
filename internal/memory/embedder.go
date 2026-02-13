package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type Provider string

const (
	ProviderOllama Provider = "ollama"
	ProviderOpenAI Provider = "openai"
)

var (
	ErrEmbedderProviderUnsupported = errors.New("embedder provider is unsupported")
	ErrEmbedderModelRequired       = errors.New("embedder model is required")
	ErrEmbedderInputRequired       = errors.New("embedder input is required")
	ErrEmbedderDimensionMismatch   = errors.New("embedding dimension mismatch")
	ErrEmbedderAPIKeyRequired      = errors.New("openai api key is required")
)

const (
	defaultEmbedderTimeout       = 30 * time.Second
	defaultEmbedderRetryAttempts = 3
	defaultEmbedderRetryBackoff  = 200 * time.Millisecond
	maxEmbedderRetryAttempts     = 10
	maxEmbedderRetryDelay        = 30 * time.Second
	maxEmbedderSuccessBodyBytes  = 1 << 20
)

type EmbedderConfig struct {
	Provider      Provider
	Model         string
	Dimension     int
	OllamaURL     string
	OpenAIBaseURL string
	OpenAIAPIKey  string
	Timeout       time.Duration
	RetryAttempts int
	RetryBackoff  time.Duration
}

type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float64, error)
	Dimension() int
}

func NewEmbedder(cfg EmbedderConfig, client *http.Client) (Embedder, error) {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, ErrEmbedderModelRequired
	}
	if cfg.Dimension <= 0 {
		cfg.Dimension = 768
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultEmbedderTimeout
	}
	client = ensureHTTPClientTimeout(client, timeout)
	retryAttempts := normalizeRetryAttempts(cfg.RetryAttempts)
	retryBackoff := normalizeRetryBackoff(cfg.RetryBackoff)

	switch cfg.Provider {
	case ProviderOllama:
		url := strings.TrimSpace(cfg.OllamaURL)
		if url == "" {
			url = "http://localhost:11434"
		}
		return &ollamaEmbedder{
			model:         model,
			baseURL:       strings.TrimRight(url, "/"),
			dimension:     cfg.Dimension,
			client:        client,
			retryAttempts: retryAttempts,
			retryBackoff:  retryBackoff,
		}, nil
	case ProviderOpenAI:
		url := strings.TrimSpace(cfg.OpenAIBaseURL)
		if url == "" {
			url = "https://api.openai.com"
		}
		apiKey := strings.TrimSpace(cfg.OpenAIAPIKey)
		if apiKey == "" {
			return nil, ErrEmbedderAPIKeyRequired
		}
		return &openAIEmbedder{
			model:         model,
			baseURL:       strings.TrimRight(url, "/"),
			apiKey:        apiKey,
			dimension:     cfg.Dimension,
			client:        client,
			retryAttempts: retryAttempts,
			retryBackoff:  retryBackoff,
		}, nil
	default:
		return nil, ErrEmbedderProviderUnsupported
	}
}

func ChunkTextForEmbedding(text string, maxChars, overlapChars int) []string {
	if text == "" {
		return []string{}
	}
	if maxChars <= 0 {
		maxChars = 1000
	}
	if overlapChars < 0 {
		overlapChars = 0
	}
	if overlapChars >= maxChars {
		overlapChars = maxChars / 5
	}

	if utf8.RuneCountInString(text) <= maxChars {
		return []string{text}
	}
	runes := []rune(text)

	step := maxChars - overlapChars
	if step <= 0 {
		step = maxChars
	}

	chunks := make([]string, 0)
	for start := 0; start < len(runes); start += step {
		end := start + maxChars
		if end >= len(runes) {
			chunks = append(chunks, string(runes[start:]))
			break
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

type ollamaEmbedder struct {
	model         string
	baseURL       string
	dimension     int
	client        *http.Client
	retryAttempts int
	retryBackoff  time.Duration
}

func (e *ollamaEmbedder) Dimension() int {
	return e.dimension
}

func (e *ollamaEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if len(inputs) == 0 {
		return nil, ErrEmbedderInputRequired
	}

	out := make([][]float64, 0, len(inputs))
	for _, input := range inputs {
		// Truncate to stay within model context window.
		// nomic-embed-text context varies by quantization; ~2048 tokens safe limit.
		truncated := truncateForEmbedding(input, 8000)
		vector, err := e.embedOne(ctx, truncated)
		if err != nil {
			return nil, err
		}
		out = append(out, vector)
	}
	return out, nil
}

func (e *ollamaEmbedder) embedOne(ctx context.Context, input string) ([]float64, error) {
	if strings.TrimSpace(input) == "" {
		return nil, ErrEmbedderInputRequired
	}
	for attempt := 1; attempt <= e.retryAttempts; attempt += 1 {
		vector, retry, err := e.embedOneAttempt(ctx, input)
		if err == nil {
			return vector, nil
		}
		if !retry || attempt == e.retryAttempts {
			return nil, err
		}
		if err := sleepWithContext(ctx, retryDelay(e.retryBackoff, attempt)); err != nil {
			return nil, fmt.Errorf("ollama retry canceled: %w", err)
		}
	}
	return nil, errors.New("ollama request failed")
}

func (e *ollamaEmbedder) embedOneAttempt(ctx context.Context, input string) ([]float64, bool, error) {
	body, err := json.Marshal(map[string]any{
		"model":  e.model,
		"prompt": input,
	})
	if err != nil {
		return nil, false, fmt.Errorf("marshal ollama payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("build ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return nil, retry, fmt.Errorf("ollama request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxEmbedderSuccessBodyBytes)).Decode(&payload); err != nil {
		return nil, false, fmt.Errorf("decode ollama response: %w", err)
	}
	if len(payload.Embedding) != e.dimension {
		return nil, false, fmt.Errorf("%w: expected %d got %d", ErrEmbedderDimensionMismatch, e.dimension, len(payload.Embedding))
	}
	return payload.Embedding, false, nil
}

type openAIEmbedder struct {
	model         string
	baseURL       string
	apiKey        string
	dimension     int
	client        *http.Client
	retryAttempts int
	retryBackoff  time.Duration
}

func (e *openAIEmbedder) Dimension() int {
	return e.dimension
}

func (e *openAIEmbedder) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if len(inputs) == 0 {
		return nil, ErrEmbedderInputRequired
	}
	normalized := make([]string, 0, len(inputs))
	for _, input := range inputs {
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			return nil, ErrEmbedderInputRequired
		}
		// Truncate to stay within model context limits
		normalized = append(normalized, truncateForEmbedding(trimmed, 8000))
	}
	return e.embedBatchWithRetry(ctx, normalized)
}

func (e *openAIEmbedder) embedBatchWithRetry(ctx context.Context, inputs []string) ([][]float64, error) {
	for attempt := 1; attempt <= e.retryAttempts; attempt += 1 {
		vectors, retry, err := e.embedBatchAttempt(ctx, inputs)
		if err == nil {
			return vectors, nil
		}
		if !retry || attempt == e.retryAttempts {
			return nil, err
		}
		if err := sleepWithContext(ctx, retryDelay(e.retryBackoff, attempt)); err != nil {
			return nil, fmt.Errorf("openai retry canceled: %w", err)
		}
	}
	return nil, errors.New("openai request failed")
}

func (e *openAIEmbedder) embedBatchAttempt(ctx context.Context, inputs []string) ([][]float64, bool, error) {
	body, err := json.Marshal(map[string]any{
		"model": e.model,
		"input": inputs,
	})
	if err != nil {
		return nil, false, fmt.Errorf("marshal openai payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("build openai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return nil, retry, fmt.Errorf("openai request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxEmbedderSuccessBodyBytes)).Decode(&payload); err != nil {
		return nil, false, fmt.Errorf("decode openai response: %w", err)
	}
	if len(payload.Data) != len(inputs) {
		return nil, false, fmt.Errorf("openai response vector count mismatch: expected %d got %d", len(inputs), len(payload.Data))
	}

	vectors := make([][]float64, 0, len(payload.Data))
	for _, row := range payload.Data {
		if len(row.Embedding) != e.dimension {
			return nil, false, fmt.Errorf("%w: expected %d got %d", ErrEmbedderDimensionMismatch, e.dimension, len(row.Embedding))
		}
		vectors = append(vectors, row.Embedding)
	}
	return vectors, false, nil
}

func ensureHTTPClientTimeout(client *http.Client, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultEmbedderTimeout
	}
	if client == nil {
		return &http.Client{Timeout: timeout}
	}
	if client.Timeout > 0 {
		return client
	}
	clone := *client
	clone.Timeout = timeout
	return &clone
}

func normalizeRetryAttempts(value int) int {
	if value <= 0 {
		return defaultEmbedderRetryAttempts
	}
	if value > maxEmbedderRetryAttempts {
		return maxEmbedderRetryAttempts
	}
	return value
}

func normalizeRetryBackoff(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultEmbedderRetryBackoff
	}
	if value > maxEmbedderRetryDelay {
		return maxEmbedderRetryDelay
	}
	return value
}

func retryDelay(base time.Duration, attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	delay := base * time.Duration(attempt)
	if delay > maxEmbedderRetryDelay {
		return maxEmbedderRetryDelay
	}
	return delay
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncateForEmbedding truncates text to maxChars to stay within embedding
// model context windows. Truncates at the last space boundary to avoid
// splitting words.
func truncateForEmbedding(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	truncated := text[:maxChars]
	// Try to break at last space
	if idx := strings.LastIndex(truncated, " "); idx > maxChars/2 {
		truncated = truncated[:idx]
	}
	return truncated
}
