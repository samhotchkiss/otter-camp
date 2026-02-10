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

type EmbedderConfig struct {
	Provider      Provider
	Model         string
	Dimension     int
	OllamaURL     string
	OpenAIBaseURL string
	OpenAIAPIKey  string
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
	if client == nil {
		client = http.DefaultClient
	}

	switch cfg.Provider {
	case ProviderOllama:
		url := strings.TrimSpace(cfg.OllamaURL)
		if url == "" {
			url = "http://localhost:11434"
		}
		return &ollamaEmbedder{
			model:     model,
			baseURL:   strings.TrimRight(url, "/"),
			dimension: cfg.Dimension,
			client:    client,
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
			model:     model,
			baseURL:   strings.TrimRight(url, "/"),
			apiKey:    apiKey,
			dimension: cfg.Dimension,
			client:    client,
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

	if len(text) <= maxChars {
		return []string{text}
	}

	step := maxChars - overlapChars
	if step <= 0 {
		step = maxChars
	}

	chunks := make([]string, 0)
	for start := 0; start < len(text); start += step {
		end := start + maxChars
		if end >= len(text) {
			chunks = append(chunks, text[start:])
			break
		}
		chunks = append(chunks, text[start:end])
	}
	return chunks
}

type ollamaEmbedder struct {
	model     string
	baseURL   string
	dimension int
	client    *http.Client
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
		vector, err := e.embedOne(ctx, input)
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

	body, err := json.Marshal(map[string]any{
		"model":  e.model,
		"prompt": input,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ollama payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	if len(payload.Embedding) != e.dimension {
		return nil, fmt.Errorf("%w: expected %d got %d", ErrEmbedderDimensionMismatch, e.dimension, len(payload.Embedding))
	}
	return payload.Embedding, nil
}

type openAIEmbedder struct {
	model     string
	baseURL   string
	apiKey    string
	dimension int
	client    *http.Client
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
		normalized = append(normalized, input)
	}

	body, err := json.Marshal(map[string]any{
		"model": e.model,
		"input": normalized,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal openai payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build openai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	if len(payload.Data) != len(inputs) {
		return nil, fmt.Errorf("openai response vector count mismatch: expected %d got %d", len(inputs), len(payload.Data))
	}

	vectors := make([][]float64, 0, len(payload.Data))
	for _, row := range payload.Data {
		if len(row.Embedding) != e.dimension {
			return nil, fmt.Errorf("%w: expected %d got %d", ErrEmbedderDimensionMismatch, e.dimension, len(row.Embedding))
		}
		vectors = append(vectors, row.Embedding)
	}
	return vectors, nil
}
