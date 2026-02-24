package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	defaultFixtureDir         = "testdata/responses"
	deterministicChunkSize    = 20
	deterministicChunkDelay   = time.Millisecond
	deterministicModeEnv      = "OTTERCAMP_MODE"
	deterministicToggleEnv    = "GATEWAY_DETERMINISTIC"
	deterministicModeValue    = "test"
	deterministicEnabledValue = "true"
)

type providerByIDLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ModelProvider, error)
}

type deterministicFixture struct {
	Response json.RawMessage `json:"response"`
	Metadata struct {
		InputTokens     int `json:"input_tokens"`
		OutputTokens    int `json:"output_tokens"`
		CacheReadTokens int `json:"cache_read_tokens"`
	} `json:"metadata"`
}

func deterministicModeEnabled() bool {
	mode := strings.TrimSpace(os.Getenv(deterministicModeEnv))
	toggle := strings.TrimSpace(os.Getenv(deterministicToggleEnv))
	return strings.EqualFold(mode, deterministicModeValue) && strings.EqualFold(toggle, deterministicEnabledValue)
}

func fixtureDirectory(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultFixtureDir
	}
	return trimmed
}

func loadDeterministicFixture(ctx context.Context, providers providerByIDLookup, fixtureDir string, invocation repo.ModelInvocation) (deterministicFixture, error) {
	if providers == nil {
		return deterministicFixture{}, fmt.Errorf("provider lookup is required for deterministic mode")
	}
	provider, err := providers.GetByID(ctx, invocation.ModelProviderID)
	if err != nil {
		return deterministicFixture{}, err
	}

	base := fixtureDirectory(fixtureDir)
	primary := filepath.Join(base, provider.Slug, invocation.ModelName+".json")
	paths := []string{primary}
	if !filepath.IsAbs(base) {
		paths = append(paths, filepath.Join("internal", "gateway", primary))
	}

	var (
		raw      []byte
		readErr  error
		lastPath string
	)
	for _, path := range paths {
		lastPath = path
		raw, readErr = os.ReadFile(path)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		return deterministicFixture{}, fmt.Errorf("read fixture %q: %w", lastPath, readErr)
	}

	var fixture deterministicFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return deterministicFixture{}, fmt.Errorf("decode fixture %q: %w", lastPath, err)
	}
	if len(fixture.Response) == 0 {
		return deterministicFixture{}, fmt.Errorf("fixture %q missing response payload", lastPath)
	}
	return fixture, nil
}

func streamDeterministicFixture(ctx context.Context, sleepFn func(context.Context, time.Duration) error, payload []byte) <-chan []byte {
	out := make(chan []byte)
	if sleepFn == nil {
		sleepFn = defaultSleep
	}

	go func() {
		defer close(out)
		if len(payload) == 0 {
			return
		}

		for offset := 0; offset < len(payload); offset += deterministicChunkSize {
			end := offset + deterministicChunkSize
			if end > len(payload) {
				end = len(payload)
			}

			chunk := append([]byte(nil), payload[offset:end]...)
			select {
			case <-ctx.Done():
				return
			case out <- chunk:
			}

			if end < len(payload) {
				if err := sleepFn(ctx, deterministicChunkDelay); err != nil {
					return
				}
			}
		}
	}()

	return out
}
