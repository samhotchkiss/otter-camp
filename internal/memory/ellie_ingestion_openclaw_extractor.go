package memory

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultEllieIngestionOpenClawBinary             = "openclaw"
	defaultEllieIngestionOpenClawGatewayURL         = "ws://127.0.0.1:18791"
	defaultEllieIngestionOpenClawAgentID            = "elephant"
	defaultEllieIngestionOpenClawSessionNamespace   = "ellie-ingestion"
	defaultEllieIngestionOpenClawGatewayCallTimeout = 90 * time.Second
	defaultEllieIngestionOpenClawMaxCandidateChars  = 800
	defaultEllieIngestionOpenClawMaxPromptChars     = 18000
	defaultEllieIngestionOpenClawMaxMessageChars    = 1200
	ellieIngestionOpenClawBridgeRequestEventType    = "memory.extract.request"
)

var ellieIngestionOpenClawSessionTokenPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

type EllieIngestionOpenClawRunner interface {
	Run(ctx context.Context, args []string) ([]byte, error)
}

type EllieIngestionOpenClawBridgeRunner interface {
	Run(ctx context.Context, orgID string, args []string) ([]byte, error)
}

type EllieIngestionOpenClawBridgeRequester interface {
	Request(ctx context.Context, eventType, orgID string, data map[string]any) (json.RawMessage, error)
}

type EllieIngestionOpenClawExtractorConfig struct {
	Runner                     EllieIngestionOpenClawRunner
	BridgeRunner               EllieIngestionOpenClawBridgeRunner
	OpenClawBinary             string
	GatewayURL                 string
	GatewayToken               string
	AgentID                    string
	SessionNamespace           string
	ExpectedModelContains      string
	GatewayCallTimeout         time.Duration
	Now                        func() time.Time
	MaxResponseCandidateLength int
	MaxPromptChars             int
	MaxMessageChars            int
}

type EllieIngestionOpenClawExtractor struct {
	runner                     EllieIngestionOpenClawRunner
	bridgeRunner               EllieIngestionOpenClawBridgeRunner
	openClawBinary             string
	gatewayURL                 string
	gatewayToken               string
	agentID                    string
	sessionNamespace           string
	expectedModelContains      string
	gatewayCallTimeout         time.Duration
	now                        func() time.Time
	maxResponseCandidateLength int
	maxPromptChars             int
	maxMessageChars            int
}

// EllieIngestionLLMBudgeter is an optional interface Ellie ingestion extractors
// can implement to expose their prompt/message size constraints. The ingestion
// worker uses this to split windows deterministically instead of silently
// dropping messages when prompts exceed transport limits.
type EllieIngestionLLMBudgeter interface {
	PromptBudget() (maxPromptChars int, maxMessageChars int)
}

type ellieIngestionOpenClawExecRunner struct {
	binary string
}

func (r ellieIngestionOpenClawExecRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	return cmd.CombinedOutput()
}

type ellieIngestionOpenClawBridgeRunner struct {
	requester EllieIngestionOpenClawBridgeRequester
}

func NewEllieIngestionOpenClawBridgeRunner(
	requester EllieIngestionOpenClawBridgeRequester,
) (EllieIngestionOpenClawBridgeRunner, error) {
	if requester == nil {
		return nil, errors.New("bridge requester is required")
	}
	return &ellieIngestionOpenClawBridgeRunner{requester: requester}, nil
}

func (r *ellieIngestionOpenClawBridgeRunner) Run(ctx context.Context, orgID string, args []string) ([]byte, error) {
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

	responseRaw, err := r.requester.Request(ctx, ellieIngestionOpenClawBridgeRequestEventType, trimmedOrgID, map[string]any{
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

func NewEllieIngestionOpenClawExtractorFromEnv() (*EllieIngestionOpenClawExtractor, error) {
	timeout := defaultEllieIngestionOpenClawGatewayCallTimeout
	if raw := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("ELLIE_INGESTION_OPENCLAW_TIMEOUT: %w", err)
		}
		timeout = parsed
	}

	maxCandidateChars := defaultEllieIngestionOpenClawMaxCandidateChars
	if raw := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_MAX_CANDIDATE_CHARS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("ELLIE_INGESTION_OPENCLAW_MAX_CANDIDATE_CHARS: %w", err)
		}
		maxCandidateChars = parsed
	}

	maxPromptChars := defaultEllieIngestionOpenClawMaxPromptChars
	if raw := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_MAX_PROMPT_CHARS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("ELLIE_INGESTION_OPENCLAW_MAX_PROMPT_CHARS: %w", err)
		}
		maxPromptChars = parsed
	}

	maxMessageChars := defaultEllieIngestionOpenClawMaxMessageChars
	if raw := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_MAX_MESSAGE_CHARS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("ELLIE_INGESTION_OPENCLAW_MAX_MESSAGE_CHARS: %w", err)
		}
		maxMessageChars = parsed
	}

	return NewEllieIngestionOpenClawExtractor(EllieIngestionOpenClawExtractorConfig{
		OpenClawBinary:             strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_BINARY")),
		GatewayURL:                 strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_URL")),
		GatewayToken:               firstNonEmpty(strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_TOKEN")), strings.TrimSpace(os.Getenv("OPENCLAW_TOKEN"))),
		AgentID:                    strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_AGENT")),
		SessionNamespace:           strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_SESSION_NAMESPACE")),
		ExpectedModelContains:      strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_EXPECT_MODEL_CONTAINS")),
		GatewayCallTimeout:         timeout,
		MaxResponseCandidateLength: maxCandidateChars,
		MaxPromptChars:             maxPromptChars,
		MaxMessageChars:            maxMessageChars,
	})
}

func NewEllieIngestionOpenClawExtractor(cfg EllieIngestionOpenClawExtractorConfig) (*EllieIngestionOpenClawExtractor, error) {
	openClawBinary := strings.TrimSpace(cfg.OpenClawBinary)
	if openClawBinary == "" {
		openClawBinary = defaultEllieIngestionOpenClawBinary
	}

	gatewayURL := strings.TrimSpace(cfg.GatewayURL)
	if gatewayURL == "" {
		gatewayURL = defaultEllieIngestionOpenClawGatewayURL
	}

	agentID := normalizeEllieIngestionOpenClawSessionToken(cfg.AgentID, defaultEllieIngestionOpenClawAgentID)
	sessionNamespace := normalizeEllieIngestionOpenClawSessionToken(cfg.SessionNamespace, defaultEllieIngestionOpenClawSessionNamespace)

	// If configured, enforce that the OpenClaw agent model contains this token.
	// Default is no enforcement; OtterCamp should never hard-fail extraction just because
	// the user picked a different model in OpenClaw.
	expectedModel := strings.TrimSpace(strings.ToLower(cfg.ExpectedModelContains))

	callTimeout := cfg.GatewayCallTimeout
	if callTimeout <= 0 {
		callTimeout = defaultEllieIngestionOpenClawGatewayCallTimeout
	}

	maxCandidateChars := cfg.MaxResponseCandidateLength
	if maxCandidateChars <= 0 {
		maxCandidateChars = defaultEllieIngestionOpenClawMaxCandidateChars
	}

	maxPromptChars := cfg.MaxPromptChars
	if maxPromptChars <= 0 {
		maxPromptChars = defaultEllieIngestionOpenClawMaxPromptChars
	}

	maxMessageChars := cfg.MaxMessageChars
	if maxMessageChars <= 0 {
		maxMessageChars = defaultEllieIngestionOpenClawMaxMessageChars
	}

	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	runner := cfg.Runner
	if runner == nil {
		runner = ellieIngestionOpenClawExecRunner{binary: openClawBinary}
	}

	return &EllieIngestionOpenClawExtractor{
		runner:                     runner,
		bridgeRunner:               cfg.BridgeRunner,
		openClawBinary:             openClawBinary,
		gatewayURL:                 gatewayURL,
		gatewayToken:               strings.TrimSpace(cfg.GatewayToken),
		agentID:                    agentID,
		sessionNamespace:           sessionNamespace,
		expectedModelContains:      expectedModel,
		gatewayCallTimeout:         callTimeout,
		now:                        now,
		maxResponseCandidateLength: maxCandidateChars,
		maxPromptChars:             maxPromptChars,
		maxMessageChars:            maxMessageChars,
	}, nil
}

func (e *EllieIngestionOpenClawExtractor) SetBridgeRunner(runner EllieIngestionOpenClawBridgeRunner) {
	if e == nil {
		return
	}
	e.bridgeRunner = runner
}

func (e *EllieIngestionOpenClawExtractor) PromptBudget() (int, int) {
	if e == nil {
		return 0, 0
	}
	return e.maxPromptChars, e.maxMessageChars
}

func (e *EllieIngestionOpenClawExtractor) Extract(
	ctx context.Context,
	input EllieIngestionLLMExtractionInput,
) (EllieIngestionLLMExtractionResult, error) {
	if e == nil {
		return EllieIngestionLLMExtractionResult{}, errors.New("openclaw extractor is nil")
	}
	if e.runner == nil {
		return EllieIngestionLLMExtractionResult{}, errors.New("openclaw extractor runner is required")
	}
	orgID := strings.TrimSpace(input.OrgID)
	roomID := strings.TrimSpace(input.RoomID)
	if orgID == "" || roomID == "" {
		return EllieIngestionLLMExtractionResult{}, errors.New("org_id and room_id are required")
	}
	if len(input.Messages) == 0 {
		return EllieIngestionLLMExtractionResult{}, nil
	}

	paramsRaw, err := json.Marshal(map[string]any{
		"idempotencyKey": e.idempotencyKey(orgID, roomID, input.Messages),
		"sessionKey":     e.sessionKey(orgID),
		"message":        buildEllieIngestionOpenClawPrompt(input, e.maxPromptChars, e.maxMessageChars),
		"deliver":        false,
	})
	if err != nil {
		return EllieIngestionLLMExtractionResult{}, fmt.Errorf("encode openclaw params: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, e.gatewayCallTimeout)
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
		strconv.FormatInt(e.gatewayCallTimeout.Milliseconds(), 10),
		"--url",
		e.gatewayURL,
	}
	if token := strings.TrimSpace(e.gatewayToken); token != "" {
		args = append(args, "--token", token)
	}

	output, err := e.runGatewayAgentCall(timeoutCtx, orgID, args)
	if err != nil {
		return EllieIngestionLLMExtractionResult{}, err
	}

	response, err := parseEllieIngestionOpenClawGatewayResponse(output)
	if err != nil {
		return EllieIngestionLLMExtractionResult{}, err
	}

	model := strings.TrimSpace(response.Result.Meta.AgentMeta.Model)
	if expected := strings.TrimSpace(e.expectedModelContains); expected != "" && !strings.Contains(strings.ToLower(model), expected) {
		return EllieIngestionLLMExtractionResult{}, fmt.Errorf(
			"openclaw extraction model %q does not include required token %q",
			model,
			expected,
		)
	}

	payloadText := firstEllieIngestionOpenClawPayloadText(response.Result.Payloads)
	if payloadText == "" {
		return EllieIngestionLLMExtractionResult{}, errors.New("openclaw extraction payload is empty")
	}

	rawJSON, err := extractEllieIngestionOpenClawJSON(payloadText)
	if err != nil {
		return EllieIngestionLLMExtractionResult{}, fmt.Errorf("decode openclaw extraction payload: %w", err)
	}
	candidates, err := parseEllieIngestionOpenClawCandidates(rawJSON, e.maxResponseCandidateLength)
	if err != nil {
		return EllieIngestionLLMExtractionResult{}, fmt.Errorf("decode openclaw extraction payload: %w", err)
	}

	return EllieIngestionLLMExtractionResult{
		Model:      model,
		TraceID:    strings.TrimSpace(response.RunID),
		Candidates: candidates,
	}, nil
}

func (e *EllieIngestionOpenClawExtractor) sessionKey(orgID string) string {
	return fmt.Sprintf(
		"agent:%s:main:%s:%s",
		e.agentID,
		e.sessionNamespace,
		normalizeEllieIngestionOpenClawSessionToken(orgID, "org"),
	)
}

func (e *EllieIngestionOpenClawExtractor) runGatewayAgentCall(
	ctx context.Context,
	orgID string,
	args []string,
) ([]byte, error) {
	if e == nil {
		return nil, errors.New("openclaw extractor is nil")
	}
	trimmedOrgID := strings.TrimSpace(orgID)
	if trimmedOrgID == "" {
		return nil, errors.New("org_id is required")
	}
	if e.runner == nil {
		return nil, errors.New("openclaw extractor runner is required")
	}

	if e.bridgeRunner != nil {
		output, err := e.bridgeRunner.Run(ctx, trimmedOrgID, args)
		if err == nil {
			return output, nil
		}

		fallbackOutput, fallbackErr := e.runner.Run(ctx, args)
		if fallbackErr != nil {
			return nil, fmt.Errorf(
				"openclaw bridge call failed: %w; openclaw gateway agent call failed: %w (%s)",
				err,
				fallbackErr,
				ellieIngestionOpenClawOutputSnippet(fallbackOutput),
			)
		}
		return fallbackOutput, nil
	}

	output, err := e.runner.Run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf(
			"openclaw gateway agent call failed: %w (%s)",
			err,
			ellieIngestionOpenClawOutputSnippet(output),
		)
	}
	return output, nil
}

func (e *EllieIngestionOpenClawExtractor) idempotencyKey(orgID, roomID string, messages []store.EllieIngestionMessage) string {
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(orgID))
	builder.WriteString("|")
	builder.WriteString(strings.TrimSpace(roomID))
	builder.WriteString("|")
	builder.WriteString(e.now().UTC().Format(time.RFC3339Nano))
	for _, message := range messages {
		builder.WriteString("|")
		builder.WriteString(strings.TrimSpace(message.ID))
		builder.WriteString("|")
		builder.WriteString(message.CreatedAt.UTC().Format(time.RFC3339Nano))
	}
	sum := sha1.Sum([]byte(builder.String()))
	return fmt.Sprintf("ellie-ingestion-%x", sum[:8])
}

func buildEllieIngestionOpenClawPrompt(
	input EllieIngestionLLMExtractionInput,
	maxPromptChars int,
	maxMessageChars int,
) string {
	var builder strings.Builder
	builder.WriteString("You extract durable engineering memories from chat messages.\n")
	builder.WriteString("Return strict JSON only. Do not include markdown or commentary.\n")
	builder.WriteString("Output schema: {\"candidates\":[{\"kind\":\"...\",\"title\":\"...\",\"content\":\"...\",\"importance\":1-5,\"confidence\":0-1,\"source_conversation_id\":\"uuid|null\",\"metadata\":{}}]}\n")
	builder.WriteString("Allowed kinds: technical_decision, process_decision, preference, fact, lesson, pattern, anti_pattern, correction, process_outcome, context.\n")
	builder.WriteString("Goal: high recall. Prefer producing multiple small memories over one vague summary.\n")
	builder.WriteString("When there is enough signal, aim for 8-20 candidates per window.\n")
	builder.WriteString("Extract concrete facts, decisions, constraints, invariants, warnings, commands, file paths, API routes, table names, and \"this is how it works\" explanations.\n")
	builder.WriteString("Each candidate should be 1-3 sentences, specific, and standalone. Avoid generic statements.\n")
	builder.WriteString("If nothing is durable, return {\"candidates\":[]}.\n\n")
	builder.WriteString("Context:\n")
	builder.WriteString(fmt.Sprintf("- org_id: %s\n", strings.TrimSpace(input.OrgID)))
	builder.WriteString(fmt.Sprintf("- room_id: %s\n\n", strings.TrimSpace(input.RoomID)))
	builder.WriteString("Messages:\n")

	baseLen := builder.Len()
	entries := make([]string, 0, len(input.Messages))
	for _, message := range input.Messages {
		entries = append(entries, formatEllieIngestionPromptMessage(message, maxMessageChars))
	}

	// Cap prompt size to avoid websocket close code 1009 (message too large).
	if maxPromptChars > 0 && baseLen < maxPromptChars && len(entries) > 0 {
		allowed := make([]string, 0, len(entries))
		used := baseLen
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			nextUsed := used + len(entry)
			if nextUsed > maxPromptChars {
				// Keep a contiguous suffix of messages so we don't silently skip
				// items in the middle of a window. The ingestion worker should
				// split windows ahead of time using the same budget.
				break
			}
			allowed = append(allowed, entry)
			used = nextUsed
		}
		if len(allowed) == 0 && len(input.Messages) > 0 {
			// Ensure forward progress even if a single formatted message cannot
			// fit (e.g. misconfigured maxMessageChars vs maxPromptChars).
			// Shrink the body for the last message until the entry fits.
			last := input.Messages[len(input.Messages)-1]
			available := maxPromptChars - baseLen
			if available > 0 {
				lo, hi := 1, maxMessageChars
				best := 0
				for lo <= hi {
					mid := (lo + hi) / 2
					candidate := formatEllieIngestionPromptMessage(last, mid)
					if len(candidate) <= available {
						best = mid
						lo = mid + 1
					} else {
						hi = mid - 1
					}
				}
				if best <= 0 {
					best = 1
				}
				builder.WriteString(formatEllieIngestionPromptMessage(last, best))
				return builder.String()
			}
		}
		for i := len(allowed) - 1; i >= 0; i-- {
			builder.WriteString(allowed[i])
		}
		return builder.String()
	}

	for _, entry := range entries {
		builder.WriteString(entry)
	}
	return builder.String()
}

func formatEllieIngestionPromptMessage(message store.EllieIngestionMessage, maxBodyChars int) string {
	var builder strings.Builder
	builder.WriteString("- id=")
	builder.WriteString(strings.TrimSpace(message.ID))
	builder.WriteString(" created_at=")
	builder.WriteString(message.CreatedAt.UTC().Format(time.RFC3339))
	if message.ConversationID != nil {
		conversationID := strings.TrimSpace(*message.ConversationID)
		if conversationID != "" {
			builder.WriteString(" conversation_id=")
			builder.WriteString(conversationID)
		}
	}
	builder.WriteString("\n  ")
	body := strings.TrimSpace(message.Body)
	if maxBodyChars > 0 && len([]rune(body)) > maxBodyChars {
		body = string([]rune(body)[:maxBodyChars]) + "..."
	}
	builder.WriteString(body)
	builder.WriteString("\n")
	return builder.String()
}

type ellieIngestionOpenClawGatewayResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
	Result struct {
		Payloads []struct {
			Text string `json:"text"`
		} `json:"payloads"`
		Meta struct {
			AgentMeta struct {
				Model string `json:"model"`
			} `json:"agentMeta"`
		} `json:"meta"`
	} `json:"result"`
}

func parseEllieIngestionOpenClawGatewayResponse(raw []byte) (ellieIngestionOpenClawGatewayResponse, error) {
	var response ellieIngestionOpenClawGatewayResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ellieIngestionOpenClawGatewayResponse{}, fmt.Errorf("decode openclaw gateway response: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(response.Status)) != "ok" {
		return ellieIngestionOpenClawGatewayResponse{}, fmt.Errorf("openclaw gateway returned status %q", strings.TrimSpace(response.Status))
	}
	return response, nil
}

func firstEllieIngestionOpenClawPayloadText(payloads []struct {
	Text string `json:"text"`
}) string {
	for _, payload := range payloads {
		text := strings.TrimSpace(payload.Text)
		if text != "" {
			return text
		}
	}
	return ""
}

func extractEllieIngestionOpenClawJSON(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("empty output")
	}

	if strings.HasPrefix(trimmed, "```") {
		firstBreak := strings.Index(trimmed, "\n")
		if firstBreak < 0 {
			return "", errors.New("invalid fenced output")
		}
		trimmed = strings.TrimSpace(trimmed[firstBreak+1:])
		if endFence := strings.LastIndex(trimmed, "```"); endFence >= 0 {
			trimmed = strings.TrimSpace(trimmed[:endFence])
		}
	}

	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return trimmed, nil
	}

	start := strings.IndexAny(trimmed, "[{")
	if start < 0 {
		return "", errors.New("no json object found")
	}
	if trimmed[start] == '{' {
		end := strings.LastIndex(trimmed, "}")
		if end <= start {
			return "", errors.New("incomplete json object")
		}
		return strings.TrimSpace(trimmed[start : end+1]), nil
	}
	end := strings.LastIndex(trimmed, "]")
	if end <= start {
		return "", errors.New("incomplete json array")
	}
	return strings.TrimSpace(trimmed[start : end+1]), nil
}

type ellieIngestionOpenClawCandidateJSON struct {
	Kind                      string         `json:"kind"`
	Title                     string         `json:"title"`
	Content                   string         `json:"content"`
	Importance                *int           `json:"importance"`
	Confidence                *float64       `json:"confidence"`
	SourceConversationID      *string        `json:"source_conversation_id"`
	SourceConversationIDCamel *string        `json:"sourceConversationId"`
	Metadata                  map[string]any `json:"metadata"`
}

type ellieIngestionOpenClawCandidatesEnvelope struct {
	Candidates []ellieIngestionOpenClawCandidateJSON `json:"candidates"`
	Memories   []ellieIngestionOpenClawCandidateJSON `json:"memories"`
}

func parseEllieIngestionOpenClawCandidates(raw string, maxCandidateChars int) ([]EllieIngestionLLMCandidate, error) {
	if maxCandidateChars <= 0 {
		maxCandidateChars = defaultEllieIngestionOpenClawMaxCandidateChars
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	rows := make([]ellieIngestionOpenClawCandidateJSON, 0)
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
			return nil, err
		}
	} else {
		var envelope ellieIngestionOpenClawCandidatesEnvelope
		if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
			return nil, err
		}
		rows = envelope.Candidates
		if len(rows) == 0 {
			rows = envelope.Memories
		}
	}

	out := make([]EllieIngestionLLMCandidate, 0, len(rows))
	for _, row := range rows {
		content := strings.TrimSpace(row.Content)
		if content == "" {
			continue
		}
		if len([]rune(content)) > maxCandidateChars {
			content = string([]rune(content)[:maxCandidateChars])
		}

		importance := 3
		if row.Importance != nil && *row.Importance > 0 {
			importance = *row.Importance
		}
		confidence := 0.7
		if row.Confidence != nil {
			confidence = *row.Confidence
		}

		sourceConversationID := row.SourceConversationID
		if sourceConversationID == nil {
			sourceConversationID = row.SourceConversationIDCamel
		}

		out = append(out, EllieIngestionLLMCandidate{
			Kind:                 strings.TrimSpace(row.Kind),
			Title:                strings.TrimSpace(row.Title),
			Content:              content,
			Importance:           importance,
			Confidence:           confidence,
			SourceConversationID: sourceConversationID,
			Metadata:             row.Metadata,
		})
	}
	return out, nil
}

func ellieIngestionOpenClawOutputSnippet(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "no output"
	}
	runes := []rune(trimmed)
	if len(runes) > 280 {
		return string(runes[:280]) + "..."
	}
	return trimmed
}

func normalizeEllieIngestionOpenClawSessionToken(value, fallback string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if normalized == "" {
		normalized = fallback
	}
	normalized = ellieIngestionOpenClawSessionTokenPattern.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")
	if normalized == "" {
		return fallback
	}
	return normalized
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
