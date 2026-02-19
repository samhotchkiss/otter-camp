package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeOpenClawGatewayRunner struct {
	output []byte
	err    error
}

func (f fakeOpenClawGatewayRunner) Run(_ context.Context, _ []string) ([]byte, error) {
	return f.output, f.err
}

func TestOpenClawGatewayCallerFromEnvSupportsAgentAliasAndModelExpectation(t *testing.T) {
	t.Setenv("ELLIE_LLM_OPENCLAW_AGENT_ID", "")
	t.Setenv("ELLIE_LLM_OPENCLAW_AGENT", "")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_AGENT_ID", "")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_AGENT", "ellie-extractor")
	t.Setenv("ELLIE_LLM_OPENCLAW_EXPECT_MODEL_CONTAINS", "")
	t.Setenv("ELLIE_INGESTION_OPENCLAW_EXPECT_MODEL_CONTAINS", "haiku")

	caller := NewOpenClawGatewayCallerFromEnv()
	require.NotNil(t, caller)
	require.Equal(t, "ellie-extractor", caller.agentID)
	require.Equal(t, "haiku", caller.expectedModel)
}

func TestOpenClawGatewayCallerRejectsUnexpectedModel(t *testing.T) {
	caller := &OpenClawGatewayCaller{
		runner:        fakeOpenClawGatewayRunner{output: []byte(`{"runId":"run-1","status":"ok","result":{"payloads":[{"text":"{\"ok\":true}"}],"meta":{"agentMeta":{"model":"anthropic/claude-opus-4-6"}}}}`)},
		gatewayURL:    "ws://127.0.0.1:18791",
		agentID:       "ellie-extractor",
		expectedModel: "haiku",
	}

	_, err := caller.Call(context.Background(), "2b6b783e-6af0-4020-9b1e-9c63e9f6df52", "hello")
	require.Error(t, err)
	require.ErrorContains(t, err, "does not include required token")
}

func TestOpenClawGatewayCallerAcceptsExpectedModel(t *testing.T) {
	caller := &OpenClawGatewayCaller{
		runner:        fakeOpenClawGatewayRunner{output: []byte(`{"runId":"run-1","status":"ok","result":{"payloads":[{"text":"{\"ok\":true}"}],"meta":{"agentMeta":{"model":"anthropic/claude-3-5-haiku-latest"}}}}`)},
		gatewayURL:    "ws://127.0.0.1:18791",
		agentID:       "ellie-extractor",
		expectedModel: "haiku",
	}

	result, err := caller.Call(context.Background(), "2b6b783e-6af0-4020-9b1e-9c63e9f6df52", "hello")
	require.NoError(t, err)
	require.Equal(t, "anthropic/claude-3-5-haiku-latest", result.Model)
	require.Equal(t, "{\"ok\":true}", result.Text)
}
