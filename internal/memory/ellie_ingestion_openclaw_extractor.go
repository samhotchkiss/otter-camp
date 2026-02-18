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

// EllieIngestionLLMBudgeter is an optional interface Ellie ingestion extractors
// can implement to expose their prompt/message size constraints. The ingestion
// worker uses this to split windows deterministically instead of silently
// dropping messages when prompts exceed transport limits.
type EllieIngestionLLMBudgeter interface {
	PromptBudget() (maxPromptChars int, maxMessageChars int)
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
		// When the bridge runner is configured, ALL calls must route through OpenClaw via
		// the bridge (hosted parity with local OpenClaw execution). If the bridge is down,
		// fail and let the worker retry later without advancing cursors.
		output, err := e.bridgeRunner.Run(ctx, trimmedOrgID, args)
		if err != nil {
			return nil, fmt.Errorf("openclaw bridge call failed: %w", err)
		}
		return output, nil
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
	builder.WriteString("You are a knowledge extraction tool. You produce CANDIDATE extractions from conversation logs.\n")
	builder.WriteString("Your goal is HIGH RECALL. A later filtering stage will remove noise.\n")
	builder.WriteString("Return strict JSON only. Do not include markdown or commentary.\n\n")
	builder.WriteString("OUTPUT FORMAT:\n")
	builder.WriteString("{\"summary\":\"...\",\"candidates\":{\"memories\":[...],\"projects\":[...],\"issues\":[...]}}\n\n")
	builder.WriteString("CANDIDATE MEMORIES:\n")
	builder.WriteString("- Extract potential durable facts, preferences, decisions, and lessons.\n")
	builder.WriteString("- Each memory MUST include: kind, title, content, importance(1-5), confidence(0-1), source_message_ids, source_quotes(<=25 words), origin_hint, pii_flags, sensitivity.\n")
	builder.WriteString("- Allowed kinds: preference, technical_decision, process_decision, fact, lesson.\n")
	builder.WriteString("- Each memory should be atomic (one fact per memory) and 1-2 sentences.\n")
	builder.WriteString("- NEVER include raw secrets (tokens/passwords/API keys) in content or quotes. If present, redact the value.\n\n")
	builder.WriteString("CANDIDATE PROJECTS:\n")
	builder.WriteString("- Real ongoing products/books/software. Include: name, description, status, source_message_ids, confidence.\n\n")
	builder.WriteString("CANDIDATE ISSUES/TASKS:\n")
	builder.WriteString("- Extract explicit and implicit tasks aggressively. Include: title, description, status(open|in_progress|blocked|completed), project (name or null), source_message_ids, source_quotes, confidence.\n\n")
	builder.WriteString("CONTEXT:\n")
	builder.WriteString(fmt.Sprintf("- org_id: %s\n", strings.TrimSpace(input.OrgID)))
	builder.WriteString(fmt.Sprintf("- room_id: %s\n\n", strings.TrimSpace(input.RoomID)))
	builder.WriteString("CONVERSATION:\n")

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
				continue
			}
			allowed = append(allowed, entry)
			used = nextUsed
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
	id := strings.TrimSpace(message.ID)
	if id == "" {
		id = "unknown"
	}

	body := strings.TrimSpace(message.Body)
	body = ellieIngestionPreRedact(body)

	senderType := strings.TrimSpace(strings.ToLower(message.SenderType))
	label := "[AGENT]"
	switch senderType {
	case "user":
		label = "[USER]"
	case "system":
		label = "[SYSTEM]"
	case "agent":
		label = "[AGENT]"
	}
	if strings.HasPrefix(body, "[Queued") || strings.Contains(body, "Queued #") {
		label = "[QUEUED]"
	}

	builder.WriteString("[")
	builder.WriteString(message.CreatedAt.UTC().Format("2006-01-02 15:04:05"))
	builder.WriteString("] (")
	builder.WriteString(id)
	builder.WriteString(") ")
	builder.WriteString(label)
	builder.WriteString(": ")

	if maxBodyChars > 0 && len([]rune(body)) > maxBodyChars {
		body = string([]rune(body)[:maxBodyChars]) + "..."
	}
	builder.WriteString(body)
	builder.WriteString("\n")
	return builder.String()
}

var (
	ellieIngestionSecretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\b(ghp_[A-Za-z0-9]{20,})\b`),
		regexp.MustCompile(`\b(sk-[A-Za-z0-9_\-]{20,})\b`),
		regexp.MustCompile(`\b(xox[bpsr]-[A-Za-z0-9\-]{10,})\b`),
		regexp.MustCompile(`\b(AKIA[A-Z0-9]{16})\b`),
		regexp.MustCompile(`\b(ops_[A-Za-z0-9+/=]{20,})\b`),
		regexp.MustCompile(`\b(eyJ[A-Za-z0-9+/=_\\-]{40,})\b`),
		regexp.MustCompile(`(?i)password:\\s*\\S+`),
		regexp.MustCompile(`(?i)token:\\s*\\S{20,}`),
		regexp.MustCompile(`(?i)api[_-]?key:\\s*\\S{10,}`),
		regexp.MustCompile(`(?i)secret:\\s*\\S{10,}`),
		regexp.MustCompile(`(?i)pairing code:\\s*\\S+`),
	}
	ellieIngestionPIIPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\\b\\d{1,5}\\s+[A-Z][a-z]+\\s+(Street|St|Avenue|Ave|Road|Rd|Drive|Dr|Lane|Ln|Trail|Way|Boulevard|Blvd|Circle|Cir|Court|Ct|Place|Pl)\\b`),
		regexp.MustCompile(`\\b(\\+?1[-.\\s]?)?[(]?\\d{3}[)]?[-.\\s]?\\d{3}[-.\\s]?\\d{4}\\b`),
		regexp.MustCompile(`\\b\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}\\b`),
		regexp.MustCompile(`(?i)\\b(dob|born|birthday|birth date)[:\\s]*\\d{1,2}[\\/\\-\\.]\\d{1,2}[\\/\\-\\.]\\d{2,4}\\b`),
		regexp.MustCompile(`\\b(January|February|March|April|May|June|July|August|September|October|November|December)\\s+\\d{1,2},?\\s+\\d{4}\\b`),
	}
)

func ellieIngestionPreRedact(text string) string {
	out := text
	for _, pat := range ellieIngestionSecretPatterns {
		out = pat.ReplaceAllString(out, "[REDACTED_SECRET]")
	}
	for _, pat := range ellieIngestionPIIPatterns {
		out = pat.ReplaceAllString(out, "[PII_REDACTED]")
	}
	return out
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

type ellieIngestionStage1Candidate struct {
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Importance  *int     `json:"importance"`
	Confidence  *float64 `json:"confidence"`
	OriginHint  string   `json:"origin_hint"`
	OriginCamel string   `json:"originHint"`
	Sensitivity string   `json:"sensitivity"`

	SourceMessageIDs []string `json:"source_message_ids"`
	SourceIDsCamel   []string `json:"sourceMessageIds"`
	SourceQuotes     []string `json:"source_quotes"`
	QuotesCamel      []string `json:"sourceQuotes"`
	PIIFlags         []string `json:"pii_flags"`
	PIICamel         []string `json:"piiFlags"`

	Status  string `json:"status"`
	Project string `json:"project"`
}

type ellieIngestionStage1Envelope struct {
	Summary     string `json:"summary"`
	SummaryText string `json:"Summary"`
	Candidates  struct {
		Memories []ellieIngestionStage1Candidate `json:"memories"`
		Projects []struct {
			Name             string   `json:"name"`
			Description      string   `json:"description"`
			Status           string   `json:"status"`
			SourceMessageIDs []string `json:"source_message_ids"`
			SourceIDsCamel   []string `json:"sourceMessageIds"`
			Confidence       *float64 `json:"confidence"`
		} `json:"projects"`
		Issues []struct {
			Title            string   `json:"title"`
			Description      string   `json:"description"`
			Status           string   `json:"status"`
			Project          *string  `json:"project"`
			SourceMessageIDs []string `json:"source_message_ids"`
			SourceIDsCamel   []string `json:"sourceMessageIds"`
			SourceQuotes     []string `json:"source_quotes"`
			QuotesCamel      []string `json:"sourceQuotes"`
			Confidence       *float64 `json:"confidence"`
		} `json:"issues"`
	} `json:"candidates"`
}

func parseEllieIngestionOpenClawCandidates(raw string, maxCandidateChars int) ([]EllieIngestionLLMCandidate, error) {
	if maxCandidateChars <= 0 {
		maxCandidateChars = defaultEllieIngestionOpenClawMaxCandidateChars
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	// Stage 1 style envelope: {"summary":"...","candidates":{"memories":[...],"projects":[...],"issues":[...]}}
	if strings.HasPrefix(trimmed, "{") {
		var stage1 ellieIngestionStage1Envelope
		if err := json.Unmarshal([]byte(trimmed), &stage1); err == nil {
			out := make([]EllieIngestionLLMCandidate, 0, len(stage1.Candidates.Memories)+len(stage1.Candidates.Projects)+len(stage1.Candidates.Issues))

			for _, row := range stage1.Candidates.Memories {
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

				sourceIDs := row.SourceMessageIDs
				if len(sourceIDs) == 0 {
					sourceIDs = row.SourceIDsCamel
				}
				quotes := row.SourceQuotes
				if len(quotes) == 0 {
					quotes = row.QuotesCamel
				}
				originHint := strings.TrimSpace(firstNonEmpty(row.OriginHint, row.OriginCamel))
				pii := row.PIIFlags
				if len(pii) == 0 {
					pii = row.PIICamel
				}
				sensitivity := strings.TrimSpace(row.Sensitivity)

				meta := map[string]any{
					"source_message_ids": sourceIDs,
					"source_quotes":      quotes,
					"origin_hint":        originHint,
					"pii_flags":          pii,
					"sensitivity":        sensitivity,
				}
				if status := strings.TrimSpace(row.Status); status != "" {
					meta["status"] = status
				}
				if project := strings.TrimSpace(row.Project); project != "" {
					meta["project"] = project
				}

				out = append(out, EllieIngestionLLMCandidate{
					Kind:       strings.TrimSpace(row.Kind),
					Title:      strings.TrimSpace(row.Title),
					Content:    content,
					Importance: importance,
					Confidence: confidence,
					Metadata:   meta,
				})
			}

			// Flatten projects/issues as context candidates for now.
			for _, proj := range stage1.Candidates.Projects {
				title := strings.TrimSpace(proj.Name)
				content := strings.TrimSpace(proj.Description)
				if title == "" || content == "" {
					continue
				}
				if len([]rune(content)) > maxCandidateChars {
					content = string([]rune(content)[:maxCandidateChars])
				}
				conf := 0.7
				if proj.Confidence != nil {
					conf = *proj.Confidence
				}
				sourceIDs := proj.SourceMessageIDs
				if len(sourceIDs) == 0 {
					sourceIDs = proj.SourceIDsCamel
				}
				out = append(out, EllieIngestionLLMCandidate{
					Kind:       "context",
					Title:      title,
					Content:    content,
					Importance: 4,
					Confidence: conf,
					Metadata: map[string]any{
						"type":               "project",
						"status":             strings.TrimSpace(proj.Status),
						"source_message_ids": sourceIDs,
					},
				})
			}
			for _, iss := range stage1.Candidates.Issues {
				title := strings.TrimSpace(iss.Title)
				content := strings.TrimSpace(iss.Description)
				if title == "" || content == "" {
					continue
				}
				if len([]rune(content)) > maxCandidateChars {
					content = string([]rune(content)[:maxCandidateChars])
				}
				conf := 0.7
				if iss.Confidence != nil {
					conf = *iss.Confidence
				}
				sourceIDs := iss.SourceMessageIDs
				if len(sourceIDs) == 0 {
					sourceIDs = iss.SourceIDsCamel
				}
				quotes := iss.SourceQuotes
				if len(quotes) == 0 {
					quotes = iss.QuotesCamel
				}
				var project string
				if iss.Project != nil {
					project = strings.TrimSpace(*iss.Project)
				}
				out = append(out, EllieIngestionLLMCandidate{
					Kind:       "context",
					Title:      title,
					Content:    content,
					Importance: 3,
					Confidence: conf,
					Metadata: map[string]any{
						"type":               "issue",
						"status":             strings.TrimSpace(iss.Status),
						"project":            project,
						"source_message_ids": sourceIDs,
						"source_quotes":      quotes,
					},
				})
			}

			if len(out) > 0 {
				return out, nil
			}
		}
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
