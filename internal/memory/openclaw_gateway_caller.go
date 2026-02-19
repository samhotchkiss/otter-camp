package memory

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const openClawGatewayCallBridgeRequestEventType = "openclaw.gateway.call.request"

type openClawGatewayCallBridgeRunner struct {
	requester EllieIngestionOpenClawBridgeRequester
}

func NewOpenClawGatewayCallBridgeRunner(
	requester EllieIngestionOpenClawBridgeRequester,
) (EllieIngestionOpenClawBridgeRunner, error) {
	if requester == nil {
		return nil, errors.New("bridge requester is required")
	}
	return &openClawGatewayCallBridgeRunner{requester: requester}, nil
}

func (r *openClawGatewayCallBridgeRunner) Run(ctx context.Context, orgID string, args []string) ([]byte, error) {
	if r == nil || r.requester == nil {
		return nil, errors.New("bridge requester is required")
	}
	trimmedOrgID := strings.TrimSpace(orgID)
	if trimmedOrgID == "" {
		return nil, errors.New("org_id is required")
	}

	normalizedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		normalizedArgs = append(normalizedArgs, trimmed)
	}
	if len(normalizedArgs) == 0 {
		return nil, errors.New("openclaw args are required")
	}
	if normalizedArgs[0] != "gateway" {
		return nil, errors.New("unsupported openclaw command; expected gateway args")
	}

	responseRaw, err := r.requester.Request(ctx, openClawGatewayCallBridgeRequestEventType, trimmedOrgID, map[string]any{
		"args": normalizedArgs,
	})
	if err != nil {
		return nil, err
	}

	var response struct {
		Output string `json:"output"`
		Stdout string `json:"stdout"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(responseRaw, &response); err != nil {
		return nil, fmt.Errorf("decode openclaw bridge response: %w", err)
	}
	output := firstNonEmpty(response.Output, response.Stdout, response.Result)
	if output == "" {
		return nil, errors.New("openclaw bridge response output is empty")
	}
	return []byte(output), nil
}

type OpenClawGatewayCallResult struct {
	Model   string
	TraceID string
	Text    string
}

type OpenClawGatewayCaller struct {
	runner           EllieIngestionOpenClawRunner
	bridgeRunner     EllieIngestionOpenClawBridgeRunner
	gatewayURL       string
	gatewayToken     string
	agentID          string
	sessionNamespace string
	expectedModel    string
	callTimeout      time.Duration
	now              func() time.Time
	requireBridge    bool
}

func NewOpenClawGatewayCallerFromEnv() *OpenClawGatewayCaller {
	timeout := defaultEllieIngestionOpenClawGatewayCallTimeout
	if raw := strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}

	gatewayURL := firstNonEmpty(
		strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_GATEWAY_URL")),
		strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_URL")),
		defaultEllieIngestionOpenClawGatewayURL,
	)
	gatewayToken := firstNonEmpty(
		strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_GATEWAY_TOKEN")),
		strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_TOKEN")),
		strings.TrimSpace(os.Getenv("OPENCLAW_TOKEN")),
	)
	agentID := firstNonEmpty(
		strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_AGENT_ID")),
		strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_AGENT")),
		strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_AGENT_ID")),
		strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_AGENT")),
		defaultEllieIngestionOpenClawAgentID,
	)
	expectedModel := strings.TrimSpace(strings.ToLower(firstNonEmpty(
		strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_EXPECT_MODEL_CONTAINS")),
		strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_EXPECT_MODEL_CONTAINS")),
		"haiku",
	)))
	sessionNamespace := firstNonEmpty(
		strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_SESSION_NAMESPACE")),
		"ellie-migration",
	)
	binary := firstNonEmpty(
		strings.TrimSpace(os.Getenv("ELLIE_LLM_OPENCLAW_BINARY")),
		strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_BINARY")),
		defaultEllieIngestionOpenClawBinary,
	)
	requireBridge := parseEllieBoolEnvDefault("ELLIE_LLM_OPENCLAW_REQUIRE_BRIDGE", true)

	return &OpenClawGatewayCaller{
		runner:           ellieIngestionOpenClawExecRunner{binary: binary},
		gatewayURL:       gatewayURL,
		gatewayToken:     gatewayToken,
		agentID:          agentID,
		sessionNamespace: sessionNamespace,
		expectedModel:    expectedModel,
		callTimeout:      timeout,
		now:              func() time.Time { return time.Now().UTC() },
		requireBridge:    requireBridge,
	}
}

func (c *OpenClawGatewayCaller) SetBridgeRunner(runner EllieIngestionOpenClawBridgeRunner) {
	if c == nil {
		return
	}
	c.bridgeRunner = runner
}

func (c *OpenClawGatewayCaller) Call(ctx context.Context, orgID string, prompt string) (OpenClawGatewayCallResult, error) {
	if c == nil || c.runner == nil {
		return OpenClawGatewayCallResult{}, errors.New("openclaw gateway caller is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return OpenClawGatewayCallResult{}, errors.New("org_id is required")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return OpenClawGatewayCallResult{}, errors.New("prompt is required")
	}

	paramsRaw, err := json.Marshal(map[string]any{
		"idempotencyKey": c.idempotencyKey(orgID, prompt),
		"sessionKey":     c.sessionKey(orgID),
		"message":        prompt,
		"deliver":        false,
	})
	if err != nil {
		return OpenClawGatewayCallResult{}, fmt.Errorf("encode openclaw params: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()

	args := []string{
		"gateway",
		"call",
		"agent",
		"--expect-final",
		"--json",
		"--params",
		string(paramsRaw),
		"--timeout",
		strconv.FormatInt(c.callTimeout.Milliseconds(), 10),
		"--url",
		strings.TrimSpace(c.gatewayURL),
	}
	if token := strings.TrimSpace(c.gatewayToken); token != "" {
		args = append(args, "--token", token)
	}

	output, err := c.runGatewayAgentCall(timeoutCtx, orgID, args)
	if err != nil {
		return OpenClawGatewayCallResult{}, err
	}

	response, err := parseEllieIngestionOpenClawGatewayResponse(output)
	if err != nil {
		return OpenClawGatewayCallResult{}, err
	}

	text := strings.TrimSpace(firstEllieIngestionOpenClawPayloadText(response.Result.Payloads))
	if text == "" {
		return OpenClawGatewayCallResult{}, errors.New("openclaw gateway payload is empty")
	}

	model := strings.TrimSpace(response.Result.Meta.AgentMeta.Model)
	if expected := strings.TrimSpace(c.expectedModel); expected != "" && !strings.Contains(strings.ToLower(model), expected) {
		return OpenClawGatewayCallResult{}, fmt.Errorf(
			"openclaw model %q does not include required token %q",
			model,
			expected,
		)
	}

	return OpenClawGatewayCallResult{
		Model:   model,
		TraceID: strings.TrimSpace(response.RunID),
		Text:    text,
	}, nil
}

func (c *OpenClawGatewayCaller) sessionKey(orgID string) string {
	agentID := strings.TrimSpace(c.agentID)
	if agentID == "" {
		agentID = defaultEllieIngestionOpenClawAgentID
	}
	namespace := strings.TrimSpace(c.sessionNamespace)
	if namespace == "" {
		namespace = "ellie-migration"
	}
	return fmt.Sprintf(
		"agent:%s:main:%s:%s:%s",
		agentID,
		namespace,
		normalizeEllieIngestionOpenClawSessionToken(orgID, "org"),
		normalizeEllieIngestionOpenClawSessionToken(agentID, "agent"),
	)
}

func (c *OpenClawGatewayCaller) idempotencyKey(orgID, prompt string) string {
	now := time.Now().UTC()
	if c != nil && c.now != nil {
		now = c.now().UTC()
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(orgID) + "|" + strings.TrimSpace(prompt) + "|" + now.Format(time.RFC3339Nano)))
	return fmt.Sprintf("ellie-gateway-%x", sum[:8])
}

func (c *OpenClawGatewayCaller) runGatewayAgentCall(ctx context.Context, orgID string, args []string) ([]byte, error) {
	if c == nil {
		return nil, errors.New("openclaw gateway caller is nil")
	}
	trimmedOrgID := strings.TrimSpace(orgID)
	if trimmedOrgID == "" {
		return nil, errors.New("org_id is required")
	}

	if c.bridgeRunner != nil {
		output, err := c.bridgeRunner.Run(ctx, trimmedOrgID, args)
		if err != nil {
			return nil, fmt.Errorf("openclaw bridge call failed: %w", err)
		}
		return output, nil
	}

	if c.requireBridge {
		return nil, ws.ErrOpenClawNotConnected
	}

	output, err := c.runner.Run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("openclaw gateway agent call failed: %w (%s)", err, ellieIngestionOpenClawOutputSnippet(output))
	}
	return output, nil
}
