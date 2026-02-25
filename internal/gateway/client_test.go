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
