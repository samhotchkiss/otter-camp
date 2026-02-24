package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type tokenVector struct {
	Text        string `json:"text"`
	KnownTokens int    `json:"known_tokens"`
}

func TestEstimateAnthropicTokensWithinFivePercent(t *testing.T) {
	path := filepath.Join("testdata", "token_vectors", "anthropic_vector.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token vector: %v", err)
	}

	var vector tokenVector
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatalf("decode token vector: %v", err)
	}
	if vector.KnownTokens <= 0 {
		t.Fatalf("invalid known token count: %d", vector.KnownTokens)
	}

	actual := estimateAnthropicTokens(vector.Text)
	diff := actual - vector.KnownTokens
	if diff < 0 {
		diff = -diff
	}

	allowed := int(float64(vector.KnownTokens) * 0.05)
	if diff > allowed {
		t.Fatalf("token estimate diff = %d, allowed <= %d (actual=%d known=%d)", diff, allowed, actual, vector.KnownTokens)
	}
}
