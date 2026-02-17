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
	defaultEllieIngestionOpenClawExpectedModelToken = "haiku"
	defaultEllieIngestionOpenClawGatewayCallTimeout = 90 * time.Second
	defaultEllieIngestionOpenClawMaxCandidateChars  = 800
)

var ellieIngestionOpenClawSessionTokenPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

type EllieIngestionOpenClawRunner interface {
	Run(ctx context.Context, args []string) ([]byte, error)
}

type EllieIngestionOpenClawExtractorConfig struct {
	Runner                     EllieIngestionOpenClawRunner
	OpenClawBinary             string
	GatewayURL                 string
	GatewayToken               string
	AgentID                    string
	SessionNamespace           string
	ExpectedModelContains      string
	GatewayCallTimeout         time.Duration
	Now                        func() time.Time
	MaxResponseCandidateLength int
}

type EllieIngestionOpenClawExtractor struct {
	runner                     EllieIngestionOpenClawRunner
	openClawBinary             string
	gatewayURL                 string
	gatewayToken               string
	agentID                    string
	sessionNamespace           string
	expectedModelContains      string
	gatewayCallTimeout         time.Duration
	now                        func() time.Time
	maxResponseCandidateLength int
}

type ellieIngestionOpenClawExecRunner struct {
	binary string
}

func (r ellieIngestionOpenClawExecRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	return cmd.CombinedOutput()
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

	return NewEllieIngestionOpenClawExtractor(EllieIngestionOpenClawExtractorConfig{
		OpenClawBinary:             strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_BINARY")),
		GatewayURL:                 strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_URL")),
		GatewayToken:               firstNonEmpty(strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_GATEWAY_TOKEN")), strings.TrimSpace(os.Getenv("OPENCLAW_TOKEN"))),
		AgentID:                    strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_AGENT")),
		SessionNamespace:           strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_SESSION_NAMESPACE")),
		ExpectedModelContains:      strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_EXPECT_MODEL_CONTAINS")),
		GatewayCallTimeout:         timeout,
		MaxResponseCandidateLength: maxCandidateChars,
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

	expectedModel := strings.TrimSpace(strings.ToLower(cfg.ExpectedModelContains))
	if expectedModel == "" {
		expectedModel = defaultEllieIngestionOpenClawExpectedModelToken
	}

	callTimeout := cfg.GatewayCallTimeout
	if callTimeout <= 0 {
		callTimeout = defaultEllieIngestionOpenClawGatewayCallTimeout
	}

	maxCandidateChars := cfg.MaxResponseCandidateLength
	if maxCandidateChars <= 0 {
		maxCandidateChars = defaultEllieIngestionOpenClawMaxCandidateChars
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
		openClawBinary:             openClawBinary,
		gatewayURL:                 gatewayURL,
		gatewayToken:               strings.TrimSpace(cfg.GatewayToken),
		agentID:                    agentID,
		sessionNamespace:           sessionNamespace,
		expectedModelContains:      expectedModel,
		gatewayCallTimeout:         callTimeout,
		now:                        now,
		maxResponseCandidateLength: maxCandidateChars,
	}, nil
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
		"message":        buildEllieIngestionOpenClawPrompt(input),
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

	output, err := e.runner.Run(timeoutCtx, args)
	if err != nil {
		return EllieIngestionLLMExtractionResult{}, fmt.Errorf(
			"openclaw gateway agent call failed: %w (%s)",
			err,
			ellieIngestionOpenClawOutputSnippet(output),
		)
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

func buildEllieIngestionOpenClawPrompt(input EllieIngestionLLMExtractionInput) string {
	var builder strings.Builder
	builder.WriteString("You extract durable engineering memories from chat messages.\n")
	builder.WriteString("Return strict JSON only. Do not include markdown or commentary.\n")
	builder.WriteString("Output schema: {\"candidates\":[{\"kind\":\"...\",\"title\":\"...\",\"content\":\"...\",\"importance\":1-5,\"confidence\":0-1,\"source_conversation_id\":\"uuid|null\",\"metadata\":{}}]}\n")
	builder.WriteString("Allowed kinds: technical_decision, process_decision, preference, fact, lesson, pattern, anti_pattern, correction, process_outcome, context.\n")
	builder.WriteString("Only include concrete, durable facts/decisions/preferences. Skip low-signal chatter.\n")
	builder.WriteString("If nothing is durable, return {\"candidates\":[]}.\n\n")
	builder.WriteString("Context:\n")
	builder.WriteString(fmt.Sprintf("- org_id: %s\n", strings.TrimSpace(input.OrgID)))
	builder.WriteString(fmt.Sprintf("- room_id: %s\n\n", strings.TrimSpace(input.RoomID)))
	builder.WriteString("Messages:\n")
	for _, message := range input.Messages {
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
		builder.WriteString(strings.TrimSpace(message.Body))
		builder.WriteString("\n")
	}
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
