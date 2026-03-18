package gateway

import (
	"encoding/json"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

func TestBuildProviderBodyOpenAIIncludesTools(t *testing.T) {
	req := turn.ModelRequest{
		Profile: repo.ModelProfile{ModelName: "gpt-4o-mini"},
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{{Role: "user", Content: "hello"}},
			ToolDescriptors: []tools.ToolDescriptor{{
				Name:        "browser.open",
				Description: "Open a URL in the browser",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
			}},
		},
	}

	body, err := buildProviderBody("openai", req, true)
	if err != nil {
		t.Fatalf("buildProviderBody() error = %v", err)
	}

	payload := decodeBody(t, body)
	if payload["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v, want auto", payload["tool_choice"])
	}

	toolsPayload := expectArray(t, payload, "tools")
	if len(toolsPayload) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(toolsPayload))
	}
	tool := expectMapValue(t, toolsPayload[0], "tools[0]")
	if tool["type"] != "function" {
		t.Fatalf("tools[0].type = %v, want function", tool["type"])
	}

	function := expectMap(t, tool, "function")
	if function["name"] != "browser_open" {
		t.Fatalf("function.name = %v, want browser_open", function["name"])
	}
	if function["description"] != "Open a URL in the browser" {
		t.Fatalf("function.description = %v, want expected description", function["description"])
	}

	parameters := expectMap(t, function, "parameters")
	if parameters["type"] != "object" {
		t.Fatalf("parameters.type = %v, want object", parameters["type"])
	}
}

func TestBuildProviderBodyAnthropicIncludesTools(t *testing.T) {
	req := turn.ModelRequest{
		Profile: repo.ModelProfile{ModelName: "claude-3-5-sonnet"},
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{{Role: "user", Content: "hello"}},
			ToolDescriptors: []tools.ToolDescriptor{{
				Name:        "web.search",
				Description: "Search the web",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
			}},
		},
	}

	body, err := buildProviderBody("anthropic", req, false)
	if err != nil {
		t.Fatalf("buildProviderBody() error = %v", err)
	}

	payload := decodeBody(t, body)
	toolsPayload := expectArray(t, payload, "tools")
	if len(toolsPayload) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(toolsPayload))
	}
	tool := expectMapValue(t, toolsPayload[0], "tools[0]")
	if tool["name"] != "web_search" {
		t.Fatalf("tools[0].name = %v, want web_search", tool["name"])
	}
	if tool["description"] != "Search the web" {
		t.Fatalf("tools[0].description = %v, want expected description", tool["description"])
	}
	inputSchema := expectMap(t, tool, "input_schema")
	if inputSchema["type"] != "object" {
		t.Fatalf("tools[0].input_schema.type = %v, want object", inputSchema["type"])
	}
}

func TestBuildProviderBodyAnthropicCoalescesConsecutiveUserMessages(t *testing.T) {
	req := turn.ModelRequest{
		Profile: repo.ModelProfile{ModelName: "claude-3-5-sonnet"},
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{
				{Role: "system", Content: "sys-1"},
				{Role: "user", Content: "first"},
				{Role: "system", Content: "sys-2"},
				{Role: "user", Content: "second"},
				{Role: "user", Content: "third"},
			},
		},
	}

	body, err := buildProviderBody("anthropic", req, false)
	if err != nil {
		t.Fatalf("buildProviderBody() error = %v", err)
	}

	payload := decodeBody(t, body)
	if payload["system"] != "sys-1\n\nsys-2" {
		t.Fatalf("system = %v, want merged system prompt", payload["system"])
	}
	messagesPayload := expectArray(t, payload, "messages")
	if len(messagesPayload) != 1 {
		t.Fatalf("len(messages) = %d, want 1 coalesced user message", len(messagesPayload))
	}
	msg := expectMapValue(t, messagesPayload[0], "messages[0]")
	if msg["role"] != "user" {
		t.Fatalf("messages[0].role = %v, want user", msg["role"])
	}
	if msg["content"] != "first\n\nsecond\n\nthird" {
		t.Fatalf("messages[0].content = %v, want merged user content", msg["content"])
	}
}

func TestBuildProviderBodyOmitsToolsWhenNoDescriptors(t *testing.T) {
	testCases := []struct {
		name         string
		providerType string
		model        string
	}{
		{name: "openai", providerType: "openai", model: "gpt-4o-mini"},
		{name: "anthropic", providerType: "anthropic", model: "claude-3-5-sonnet"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := turn.ModelRequest{
				Profile: repo.ModelProfile{ModelName: tc.model},
				Prompt: &prompt.AssembledPrompt{
					Messages: []prompt.PromptMessage{{Role: "user", Content: "hello"}},
				},
			}

			body, err := buildProviderBody(tc.providerType, req, false)
			if err != nil {
				t.Fatalf("buildProviderBody() error = %v", err)
			}

			payload := decodeBody(t, body)
			if _, exists := payload["tools"]; exists {
				t.Fatalf("payload.tools exists; expected omitted")
			}
			if _, exists := payload["tool_choice"]; exists {
				t.Fatalf("payload.tool_choice exists; expected omitted")
			}
		})
	}
}

func TestNormalizeToolSchemaAddsPropertiesWhenMissing(t *testing.T) {
	cases := []struct {
		name   string
		input  json.RawMessage
		hasKey string
	}{
		{
			name:   "empty schema gets properties",
			input:  json.RawMessage(``),
			hasKey: "properties",
		},
		{
			name:   "object without properties gets properties injected",
			input:  json.RawMessage(`{"type":"object"}`),
			hasKey: "properties",
		},
		{
			name:   "object with properties unchanged",
			input:  json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
			hasKey: "properties",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeToolSchema(tc.input)
			obj, ok := result.(map[string]any)
			if !ok {
				t.Fatalf("result type = %T, want map[string]any", result)
			}
			if _, exists := obj[tc.hasKey]; !exists {
				t.Fatalf("result missing %q key: %v", tc.hasKey, obj)
			}
		})
	}
}

func TestOpenAIToolsSanitizesNames(t *testing.T) {
	cases := []struct {
		name    string
		apiName string
		want    string
	}{
		{name: "file.read", want: "file_read"},
		{name: "git.status", want: "git_status"},
		{name: "memory.query", want: "memory_query"},
		{name: "already_valid", want: "already_valid"},
		{name: "with-dashes", want: "with-dashes"},
		// APIName takes precedence over sanitizing Name
		{name: "file.read", apiName: "file_read_override", want: "file_read_override"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := turn.ModelRequest{
				Profile: repo.ModelProfile{ModelName: "gpt-4o-mini"},
				Prompt: &prompt.AssembledPrompt{
					Messages: []prompt.PromptMessage{{Role: "user", Content: "hi"}},
					ToolDescriptors: []tools.ToolDescriptor{{
						Name:    tc.name,
						APIName: tc.apiName,
					}},
				},
			}
			body, err := buildProviderBody("openai", req, false)
			if err != nil {
				t.Fatalf("buildProviderBody() error = %v", err)
			}
			payload := decodeBody(t, body)
			toolsPayload := expectArray(t, payload, "tools")
			tool := expectMapValue(t, toolsPayload[0], "tools[0]")
			function := expectMap(t, tool, "function")
			if function["name"] != tc.want {
				t.Fatalf("function.name = %v, want %v", function["name"], tc.want)
			}
		})
	}
}

func TestAnthropicToolsSanitizesNames(t *testing.T) {
	req := turn.ModelRequest{
		Profile: repo.ModelProfile{ModelName: "claude-3-5-sonnet"},
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{{Role: "user", Content: "hi"}},
			ToolDescriptors: []tools.ToolDescriptor{{
				Name:        "task.create",
				Description: "Create a task",
			}},
		},
	}
	body, err := buildProviderBody("anthropic", req, false)
	if err != nil {
		t.Fatalf("buildProviderBody() error = %v", err)
	}
	payload := decodeBody(t, body)
	toolsPayload := expectArray(t, payload, "tools")
	tool := expectMapValue(t, toolsPayload[0], "tools[0]")
	if tool["name"] != "task_create" {
		t.Fatalf("tools[0].name = %v, want task_create", tool["name"])
	}
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return payload
}

func expectArray(t *testing.T, payload map[string]any, key string) []any {
	t.Helper()
	value, ok := payload[key]
	if !ok {
		t.Fatalf("payload[%q] missing", key)
	}
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("payload[%q] type = %T, want []any", key, value)
	}
	return array
}

func expectMap(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := payload[key]
	if !ok {
		t.Fatalf("payload[%q] missing", key)
	}
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("payload[%q] type = %T, want map[string]any", key, value)
	}
	return out
}

func expectMapValue(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s type = %T, want map[string]any", name, value)
	}
	return out
}

// TestParseOpenAIStreamEventAccumulatesToolCallDeltas verifies that
// parseOpenAIStreamEvent correctly returns tool_call deltas from the SSE data.
func TestParseOpenAIStreamEventAccumulatesToolCallDeltas(t *testing.T) {
	// First delta: id + name arrive on index 0.
	data1 := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"file_read","arguments":""}}]}}]}`
	chunk, deltas, usage, done, err := parseOpenAIStreamEvent(data1)
	if err != nil {
		t.Fatalf("parseOpenAIStreamEvent() error = %v", err)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	if chunk != "" {
		t.Fatalf("chunk = %q, want empty", chunk)
	}
	if usage != nil {
		t.Fatalf("usage = %v, want nil", usage)
	}
	if len(deltas) != 1 {
		t.Fatalf("len(deltas) = %d, want 1", len(deltas))
	}
	if deltas[0].Index != 0 || deltas[0].ID != "call_abc" || deltas[0].Function.Name != "file_read" {
		t.Fatalf("delta[0] unexpected: %+v", deltas[0])
	}

	// Second delta: arguments chunk arrives.
	data2 := `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"/tmp\""}}]}}]}`
	_, deltas2, _, _, err := parseOpenAIStreamEvent(data2)
	if err != nil {
		t.Fatalf("parseOpenAIStreamEvent() error = %v", err)
	}
	if len(deltas2) != 1 || deltas2[0].Function.Arguments != `{"path":"/tmp"` {
		t.Fatalf("delta[0].arguments = %q, want partial JSON", deltas2[0].Function.Arguments)
	}

	// [DONE] signals completion.
	_, _, _, done2, err := parseOpenAIStreamEvent("[DONE]")
	if err != nil {
		t.Fatalf("parseOpenAIStreamEvent([DONE]) error = %v", err)
	}
	if !done2 {
		t.Fatal("done = false, want true on [DONE]")
	}
}

// TestParseOpenAICompletionExtractsToolCalls verifies non-streaming tool call parsing.
func TestParseOpenAICompletionExtractsToolCalls(t *testing.T) {
	raw := []byte(`{
		"choices":[{
			"message":{
				"content":null,
				"tool_calls":[{
					"id":"call_xyz",
					"type":"function",
					"function":{"name":"memory_query","arguments":"{\"q\":\"hello\"}"}
				}]
			}
		}],
		"usage":{"prompt_tokens":10,"completion_tokens":19}
	}`)

	result, err := parseOpenAICompletion(raw)
	if err != nil {
		t.Fatalf("parseOpenAICompletion() error = %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call_xyz" {
		t.Fatalf("ToolCall.ID = %q, want call_xyz", tc.ID)
	}
	if tc.Name != "memory_query" {
		t.Fatalf("ToolCall.Name = %q, want memory_query", tc.Name)
	}
	if q, _ := tc.Arguments["q"].(string); q != "hello" {
		t.Fatalf("ToolCall.Arguments[q] = %v, want hello", tc.Arguments["q"])
	}
	if result.Usage == nil || result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 19 {
		t.Fatalf("Usage = %+v, want {10 19 0}", result.Usage)
	}
}

// TestParseAnthropicStreamEventAccumulatesToolUseBlocks verifies that
// parseAnthropicStreamEvent surfaces tool_use content_block_start and
// input_json_delta events correctly.
func TestParseAnthropicStreamEventAccumulatesToolUseBlocks(t *testing.T) {
	// content_block_start for a tool_use block.
	startData := `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_001","name":"task_create"}}`
	chunk, toolStart, toolDelta, usage, done, err := parseAnthropicStreamEvent("content_block_start", startData)
	if err != nil {
		t.Fatalf("parseAnthropicStreamEvent() error = %v", err)
	}
	if done || chunk != "" || toolDelta != nil || usage != nil {
		t.Fatalf("unexpected non-nil fields: chunk=%q done=%v toolDelta=%v usage=%v", chunk, done, toolDelta, usage)
	}
	if toolStart == nil {
		t.Fatal("toolStart = nil, want non-nil")
	}
	if toolStart.Index != 0 || toolStart.ID != "toolu_001" || toolStart.Name != "task_create" {
		t.Fatalf("toolStart = %+v, unexpected", toolStart)
	}

	// input_json_delta with partial JSON.
	deltaData := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"title\":\"Fix"}}`
	_, toolStart2, toolDelta2, _, _, err := parseAnthropicStreamEvent("content_block_delta", deltaData)
	if err != nil {
		t.Fatalf("parseAnthropicStreamEvent() error = %v", err)
	}
	if toolStart2 != nil {
		t.Fatalf("toolStart2 non-nil on delta event: %+v", toolStart2)
	}
	if toolDelta2 == nil {
		t.Fatal("toolDelta2 = nil, want non-nil")
	}
	if toolDelta2.Index != 0 || toolDelta2.PartialJSON != `{"title":"Fix` {
		t.Fatalf("toolDelta2 = %+v, unexpected", toolDelta2)
	}

	// message_stop signals done.
	_, _, _, _, done2, err := parseAnthropicStreamEvent("message_stop", `{"type":"message_stop"}`)
	if err != nil {
		t.Fatalf("parseAnthropicStreamEvent(message_stop) error = %v", err)
	}
	if !done2 {
		t.Fatal("done = false, want true on message_stop")
	}
}

// TestParseAnthropicCompletionExtractsToolCalls verifies non-streaming tool_use parsing.
func TestParseAnthropicCompletionExtractsToolCalls(t *testing.T) {
	raw := []byte(`{
		"content":[
			{"type":"tool_use","id":"toolu_002","name":"file_read","input":{"path":"/etc/hosts"}}
		],
		"usage":{"input_tokens":5,"output_tokens":12,"cache_read_input_tokens":0}
	}`)

	result, err := parseAnthropicCompletion(raw)
	if err != nil {
		t.Fatalf("parseAnthropicCompletion() error = %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "toolu_002" || tc.Name != "file_read" {
		t.Fatalf("ToolCall = %+v, unexpected", tc)
	}
	if p, _ := tc.Arguments["path"].(string); p != "/etc/hosts" {
		t.Fatalf("ToolCall.Arguments[path] = %v, want /etc/hosts", tc.Arguments["path"])
	}
}

// TestToolNameSanitizedNamePreservedInDispatch verifies the sanitized name is
// stored in ModelToolCall.Name (reverse-mapping is done by dispatchTools in engine.go).
func TestToolNameSanitizedNamePreservedInModelToolCall(t *testing.T) {
	raw := []byte(`{
		"choices":[{
			"message":{
				"tool_calls":[{
					"id":"call_san",
					"type":"function",
					"function":{"name":"file_read","arguments":"{}"}
				}]
			}
		}]
	}`)
	result, err := parseOpenAICompletion(raw)
	if err != nil {
		t.Fatalf("parseOpenAICompletion() error = %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	// The sanitized name (file_read) should be preserved as-is in ModelToolCall.Name.
	// dispatchTools in engine.go will reverse-map it to file.read before broker dispatch.
	if result.ToolCalls[0].Name != "file_read" {
		t.Fatalf("Name = %q, want file_read", result.ToolCalls[0].Name)
	}
}
