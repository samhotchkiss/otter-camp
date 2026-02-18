package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieIngestionOpenClawRunner struct {
	args   []string
	output []byte
	err    error
}

func (f *fakeEllieIngestionOpenClawRunner) Run(_ context.Context, args []string) ([]byte, error) {
	f.args = append([]string(nil), args...)
	if f.err != nil {
		return f.output, f.err
	}
	return append([]byte(nil), f.output...), nil
}

type fakeEllieIngestionOpenClawBridgeRequester struct {
	eventType string
	orgID     string
	data      map[string]any
	response  json.RawMessage
	err       error
}

func (f *fakeEllieIngestionOpenClawBridgeRequester) Request(
	_ context.Context,
	eventType, orgID string,
	data map[string]any,
) (json.RawMessage, error) {
	f.eventType = eventType
	f.orgID = orgID
	f.data = data
	if f.err != nil {
		return nil, f.err
	}
	return append(json.RawMessage(nil), f.response...), nil
}

func TestEllieIngestionOpenClawExtractorBuildsGatewayAgentCall(t *testing.T) {
	runner := &fakeEllieIngestionOpenClawRunner{
		output: []byte(`{"runId":"run-1","status":"ok","result":{"payloads":[{"text":"{\"candidates\":[]}"}],"meta":{"agentMeta":{"model":"anthropic/claude-3-5-haiku-latest"}}}}`),
	}
	extractor, err := NewEllieIngestionOpenClawExtractor(EllieIngestionOpenClawExtractorConfig{
		Runner:                     runner,
		GatewayURL:                 "ws://127.0.0.1:18791",
		GatewayToken:               "gateway-token",
		AgentID:                    "elephant",
		SessionNamespace:           "ellie-ingestion",
		ExpectedModelContains:      "haiku",
		GatewayCallTimeout:         42 * time.Second,
		Now:                        func() time.Time { return time.Date(2026, 2, 17, 6, 0, 0, 0, time.UTC) },
		OpenClawBinary:             "openclaw",
		MaxResponseCandidateLength: 400,
	})
	require.NoError(t, err)

	_, err = extractor.Extract(context.Background(), EllieIngestionLLMExtractionInput{
		OrgID:  "0d86d9e4-b8a1-46cf-aed1-c666123c2d1f",
		RoomID: "ab86d9e4-b8a1-46cf-aed1-c666123c2d1a",
		Messages: []store.EllieIngestionMessage{
			{
				ID:        "bb86d9e4-b8a1-46cf-aed1-c666123c2d1a",
				OrgID:     "0d86d9e4-b8a1-46cf-aed1-c666123c2d1f",
				RoomID:    "ab86d9e4-b8a1-46cf-aed1-c666123c2d1a",
				Body:      "We decided to keep explicit SQL migrations.",
				CreatedAt: time.Date(2026, 2, 17, 5, 58, 0, 0, time.UTC),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"gateway",
		"call",
		"agent",
		"--expect-final",
		"--json",
		"--params",
		runner.args[6],
		"--timeout",
		"42000",
		"--url",
		"ws://127.0.0.1:18791",
		"--token",
		"gateway-token",
	}, runner.args)

	var params map[string]any
	require.NoError(t, json.Unmarshal([]byte(runner.args[6]), &params))
	require.Equal(t, false, params["deliver"])
	require.Equal(t, "agent:elephant:main:ellie-ingestion:0d86d9e4-b8a1-46cf-aed1-c666123c2d1f", params["sessionKey"])
	require.NotEmpty(t, params["idempotencyKey"])
	require.Contains(t, params["message"], "Return strict JSON only")
	require.Contains(t, params["message"], "We decided to keep explicit SQL migrations.")
}

func TestEllieIngestionOpenClawExtractorParsesFencedJSONCandidates(t *testing.T) {
	runner := &fakeEllieIngestionOpenClawRunner{
		output: []byte("{\"runId\":\"trace-159\",\"status\":\"ok\",\"result\":{\"payloads\":[{\"text\":\"```json\\n{\\\"candidates\\\":[{\\\"kind\\\":\\\"lesson\\\",\\\"title\\\":\\\"Migration lesson\\\",\\\"content\\\":\\\"Keep migrations explicit and reversible.\\\",\\\"importance\\\":4,\\\"confidence\\\":0.88,\\\"metadata\\\":{\\\"evidence\\\":\\\"window-1\\\"}}]}\\n```\"}],\"meta\":{\"agentMeta\":{\"model\":\"anthropic/claude-3-5-haiku-latest\"}}}}"),
	}
	extractor, err := NewEllieIngestionOpenClawExtractor(EllieIngestionOpenClawExtractorConfig{
		Runner:                runner,
		ExpectedModelContains: "haiku",
	})
	require.NoError(t, err)

	result, err := extractor.Extract(context.Background(), EllieIngestionLLMExtractionInput{
		OrgID:  "0d86d9e4-b8a1-46cf-aed1-c666123c2d1f",
		RoomID: "ab86d9e4-b8a1-46cf-aed1-c666123c2d1a",
		Messages: []store.EllieIngestionMessage{
			{
				ID:        "bb86d9e4-b8a1-46cf-aed1-c666123c2d1a",
				OrgID:     "0d86d9e4-b8a1-46cf-aed1-c666123c2d1f",
				RoomID:    "ab86d9e4-b8a1-46cf-aed1-c666123c2d1a",
				Body:      "Lesson learned: keep SQL explicit.",
				CreatedAt: time.Date(2026, 2, 17, 5, 58, 0, 0, time.UTC),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "trace-159", result.TraceID)
	require.Equal(t, "anthropic/claude-3-5-haiku-latest", result.Model)
	require.Len(t, result.Candidates, 1)
	require.Equal(t, "lesson", result.Candidates[0].Kind)
	require.Equal(t, "Migration lesson", result.Candidates[0].Title)
	require.Equal(t, "Keep migrations explicit and reversible.", result.Candidates[0].Content)
	require.Equal(t, 4, result.Candidates[0].Importance)
	require.Equal(t, 0.88, result.Candidates[0].Confidence)
	require.Equal(t, "window-1", result.Candidates[0].Metadata["evidence"])
}

func TestEllieIngestionOpenClawExtractorReturnsErrorOnMalformedOutput(t *testing.T) {
	runner := &fakeEllieIngestionOpenClawRunner{
		output: []byte(`{"runId":"trace-160","status":"ok","result":{"payloads":[{"text":"not-json"}],"meta":{"agentMeta":{"model":"anthropic/claude-3-5-haiku-latest"}}}}`),
	}
	extractor, err := NewEllieIngestionOpenClawExtractor(EllieIngestionOpenClawExtractorConfig{
		Runner:                runner,
		ExpectedModelContains: "haiku",
	})
	require.NoError(t, err)

	_, err = extractor.Extract(context.Background(), EllieIngestionLLMExtractionInput{
		OrgID:  "0d86d9e4-b8a1-46cf-aed1-c666123c2d1f",
		RoomID: "ab86d9e4-b8a1-46cf-aed1-c666123c2d1a",
		Messages: []store.EllieIngestionMessage{
			{
				ID:        "bb86d9e4-b8a1-46cf-aed1-c666123c2d1a",
				OrgID:     "0d86d9e4-b8a1-46cf-aed1-c666123c2d1f",
				RoomID:    "ab86d9e4-b8a1-46cf-aed1-c666123c2d1a",
				Body:      "Any content.",
				CreatedAt: time.Date(2026, 2, 17, 5, 58, 0, 0, time.UTC),
			},
		},
	})
	require.ErrorContains(t, err, "decode openclaw extraction payload")
}

func TestEllieIngestionOpenClawExtractorReturnsErrorWhenGatewayCallFails(t *testing.T) {
	runner := &fakeEllieIngestionOpenClawRunner{
		output: []byte("Gateway call failed: Error: invalid agent params"),
		err:    errors.New("exit status 1"),
	}
	extractor, err := NewEllieIngestionOpenClawExtractor(EllieIngestionOpenClawExtractorConfig{
		Runner:                runner,
		ExpectedModelContains: "haiku",
	})
	require.NoError(t, err)

	_, err = extractor.Extract(context.Background(), EllieIngestionLLMExtractionInput{
		OrgID:    "0d86d9e4-b8a1-46cf-aed1-c666123c2d1f",
		RoomID:   "ab86d9e4-b8a1-46cf-aed1-c666123c2d1a",
		Messages: []store.EllieIngestionMessage{{ID: "bb86d9e4-b8a1-46cf-aed1-c666123c2d1a", Body: "Any content."}},
	})
	require.ErrorContains(t, err, "openclaw gateway agent call failed")
}

func TestNewEllieIngestionOpenClawExtractorFromEnv(t *testing.T) {
	t.Setenv("ELLIE_INGESTION_OPENCLAW_BINARY", "openclaw-custom")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_URL", "ws://127.0.0.1:19001")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_TOKEN", "")
	t.Setenv("OPENCLAW_TOKEN", "fallback-token")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_AGENT", "Elephant")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_SESSION_NAMESPACE", "Ellie Ingestion")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_EXPECT_MODEL_CONTAINS", "haiku")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_TIMEOUT", "45s")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_MAX_CANDIDATE_CHARS", "512")

	extractor, err := NewEllieIngestionOpenClawExtractorFromEnv()
	require.NoError(t, err)
	require.Equal(t, "openclaw-custom", extractor.openClawBinary)
	require.Equal(t, "ws://127.0.0.1:19001", extractor.gatewayURL)
	require.Equal(t, "fallback-token", extractor.gatewayToken)
	require.Equal(t, "elephant", extractor.agentID)
	require.Equal(t, "ellie-ingestion", extractor.sessionNamespace)
	require.Equal(t, "haiku", extractor.expectedModelContains)
	require.Equal(t, 45*time.Second, extractor.gatewayCallTimeout)
	require.Equal(t, 512, extractor.maxResponseCandidateLength)
}

func TestNewEllieIngestionOpenClawExtractorFromEnvRejectsInvalidTimeout(t *testing.T) {
	t.Setenv("ELLIE_INGESTION_OPENCLAW_TIMEOUT", "not-a-duration")
	_, err := NewEllieIngestionOpenClawExtractorFromEnv()
	require.ErrorContains(t, err, "ELLIE_INGESTION_OPENCLAW_TIMEOUT")
}

func TestEllieIngestionOpenClawBridgeRunnerRoutesGatewayCall(t *testing.T) {
	requester := &fakeEllieIngestionOpenClawBridgeRequester{
		response: json.RawMessage(`{"request_id":"req-1","ok":true,"output":"{\"runId\":\"trace-99\",\"status\":\"ok\"}"}`),
	}
	runner, err := NewEllieIngestionOpenClawBridgeRunner(requester)
	require.NoError(t, err)

	output, err := runner.Run(context.Background(), "org-1", []string{
		"gateway",
		"call",
		"agent",
		"--json",
	})
	require.NoError(t, err)
	require.Equal(t, `{"runId":"trace-99","status":"ok"}`, string(output))
	require.Equal(t, "memory.extract.request", requester.eventType)
	require.Equal(t, "org-1", requester.orgID)
	require.Equal(t, []string{"gateway", "call", "agent", "--json"}, requester.data["args"])
}

func TestEllieIngestionOpenClawExtractorFallsBackToExecWhenBridgeFails(t *testing.T) {
	bridgeRequester := &fakeEllieIngestionOpenClawBridgeRequester{
		err: errors.New("bridge unavailable"),
	}
	bridgeRunner, err := NewEllieIngestionOpenClawBridgeRunner(bridgeRequester)
	require.NoError(t, err)

	execRunner := &fakeEllieIngestionOpenClawRunner{
		output: []byte(`{"runId":"run-1","status":"ok","result":{"payloads":[{"text":"{\"candidates\":[]}"}],"meta":{"agentMeta":{"model":"anthropic/claude-3-5-haiku-latest"}}}}`),
	}
	extractor, err := NewEllieIngestionOpenClawExtractor(EllieIngestionOpenClawExtractorConfig{
		Runner:                execRunner,
		BridgeRunner:          bridgeRunner,
		ExpectedModelContains: "haiku",
	})
	require.NoError(t, err)

	_, err = extractor.Extract(context.Background(), EllieIngestionLLMExtractionInput{
		OrgID:    "org-1",
		RoomID:   "room-1",
		Messages: []store.EllieIngestionMessage{{ID: "msg-1", Body: "Any content."}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, execRunner.args)
}
