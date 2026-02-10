package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestEmbedder(t *testing.T) {
	t.Run("ollama provider request shape", func(t *testing.T) {
		requests := make([]map[string]any, 0)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/embeddings", r.URL.Path)
			require.Equal(t, http.MethodPost, r.Method)

			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			requests = append(requests, payload)

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"embedding":[0.1,0.2]}`))
		}))
		defer server.Close()

		embedder, err := NewEmbedder(EmbedderConfig{
			Provider:  ProviderOllama,
			Model:     "nomic-embed-text",
			OllamaURL: server.URL,
			Dimension: 2,
		}, server.Client())
		require.NoError(t, err)

		vectors, err := embedder.Embed(context.Background(), []string{"alpha", "beta"})
		require.NoError(t, err)
		require.Len(t, vectors, 2)
		require.Len(t, requests, 2)
		require.Equal(t, "nomic-embed-text", requests[0]["model"])
		require.Equal(t, "alpha", requests[0]["prompt"])
		require.Equal(t, "beta", requests[1]["prompt"])
	})

	t.Run("openai provider request shape", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/embeddings", r.URL.Path)
			require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "text-embedding-3-small", payload["model"])
			require.Equal(t, []any{"x", "y"}, payload["input"])

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"embedding":[0.5,0.6]},{"embedding":[0.7,0.8]}]}`))
		}))
		defer server.Close()

		embedder, err := NewEmbedder(EmbedderConfig{
			Provider:      ProviderOpenAI,
			Model:         "text-embedding-3-small",
			OpenAIBaseURL: server.URL,
			OpenAIAPIKey:  "test-key",
			Dimension:     2,
		}, server.Client())
		require.NoError(t, err)

		vectors, err := embedder.Embed(context.Background(), []string{"x", "y"})
		require.NoError(t, err)
		require.Len(t, vectors, 2)
		require.Len(t, vectors[0], 2)
		require.Len(t, vectors[1], 2)
	})

	t.Run("openai inputs are trimmed before request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, []any{"x", "y"}, payload["input"])

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"embedding":[0.5,0.6]},{"embedding":[0.7,0.8]}]}`))
		}))
		defer server.Close()

		embedder, err := NewEmbedder(EmbedderConfig{
			Provider:      ProviderOpenAI,
			Model:         "text-embedding-3-small",
			OpenAIBaseURL: server.URL,
			OpenAIAPIKey:  "test-key",
			Dimension:     2,
		}, server.Client())
		require.NoError(t, err)

		_, err = embedder.Embed(context.Background(), []string{"  x ", "\n y\t"})
		require.NoError(t, err)
	})

	t.Run("dimension mismatch returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"embedding":[0.1,0.2,0.3]}`))
		}))
		defer server.Close()

		embedder, err := NewEmbedder(EmbedderConfig{
			Provider:  ProviderOllama,
			Model:     "nomic-embed-text",
			OllamaURL: server.URL,
			Dimension: 2,
		}, server.Client())
		require.NoError(t, err)

		_, err = embedder.Embed(context.Background(), []string{"alpha"})
		require.Error(t, err)
	})
}

func TestChunkTextForEmbedding(t *testing.T) {
	short := ChunkTextForEmbedding("short text", 10, 2)
	require.Equal(t, []string{"short text"}, short)

	chunks := ChunkTextForEmbedding("abcdefghijklmnopqrstuvwxyz", 10, 2)
	require.Equal(t, []string{
		"abcdefghij",
		"ijklmnopqr",
		"qrstuvwxyz",
	}, chunks)

	defaulted := ChunkTextForEmbedding("abcdef", 0, -1)
	require.Equal(t, []string{"abcdef"}, defaulted)
}

func TestChunkUTF8(t *testing.T) {
	text := "你好🙂世界🙂abc"
	chunks := ChunkTextForEmbedding(text, 4, 1)
	require.NotEmpty(t, chunks)

	for _, chunk := range chunks {
		require.True(t, utf8.ValidString(chunk))
		require.LessOrEqual(t, utf8.RuneCountInString(chunk), 4)
	}
}

func TestEmbedderTimeout(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding":[0.1,0.2]}`))
	}))
	defer server.Close()

	embedder, err := NewEmbedder(EmbedderConfig{
		Provider:      ProviderOllama,
		Model:         "nomic-embed-text",
		OllamaURL:     server.URL,
		Dimension:     2,
		Timeout:       5 * time.Millisecond,
		RetryAttempts: 3,
		RetryBackoff:  1 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	_, err = embedder.Embed(context.Background(), []string{"slow request"})
	require.Error(t, err)
	require.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestEmbedderTrimming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, []any{"trim-me"}, payload["input"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.3,0.4]}]}`))
	}))
	defer server.Close()

	embedder, err := NewEmbedder(EmbedderConfig{
		Provider:      ProviderOpenAI,
		Model:         "text-embedding-3-small",
		OpenAIBaseURL: server.URL,
		OpenAIAPIKey:  "test-key",
		Dimension:     2,
	}, server.Client())
	require.NoError(t, err)

	_, err = embedder.Embed(context.Background(), []string{" trim-me "})
	require.NoError(t, err)
}

func TestNormalizeRetryAttemptsCapsUpperBound(t *testing.T) {
	require.Equal(t, defaultEmbedderRetryAttempts, normalizeRetryAttempts(0))
	require.Equal(t, maxEmbedderRetryAttempts, normalizeRetryAttempts(100))
	require.Equal(t, maxEmbedderRetryDelay, normalizeRetryBackoff(45*time.Second))
	require.Equal(t, maxEmbedderRetryDelay, retryDelay(20*time.Second, 3))
}
