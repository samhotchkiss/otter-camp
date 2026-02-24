package repo

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestFlowNodeJSONMappingRoundTrip(t *testing.T) {
	tools := []FlowNodeMCPTool{
		{
			ConnectionID: uuid.New(),
			ToolName:     "browser.navigate",
		},
		{
			ConnectionID: uuid.New(),
			ToolName:     "cli.execute",
		},
	}
	toolDomains := []string{"browser", "cli"}

	toolsJSON := marshalFlowNodeMCPTools(tools)
	domainsJSON := marshalFlowNodeToolDomains(toolDomains)

	var decodedTools []FlowNodeMCPTool
	if err := json.Unmarshal(toolsJSON, &decodedTools); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if len(decodedTools) != len(tools) {
		t.Fatalf("decoded tool count = %d, want %d", len(decodedTools), len(tools))
	}
	if decodedTools[0].ConnectionID != tools[0].ConnectionID || decodedTools[0].ToolName != tools[0].ToolName {
		t.Fatalf("decoded first tool = %+v, want %+v", decodedTools[0], tools[0])
	}

	var decodedDomains []string
	if err := json.Unmarshal(domainsJSON, &decodedDomains); err != nil {
		t.Fatalf("unmarshal tool domains: %v", err)
	}
	if len(decodedDomains) != len(toolDomains) {
		t.Fatalf("decoded domain count = %d, want %d", len(decodedDomains), len(toolDomains))
	}
	if decodedDomains[1] != "cli" {
		t.Fatalf("decoded domain[1] = %q, want %q", decodedDomains[1], "cli")
	}
}

func TestFlowNodeJSONMappingEmptyArraysSerializeAsEmptyArray(t *testing.T) {
	var (
		nilTools   []FlowNodeMCPTool
		nilDomains []string
	)

	toolsJSON := marshalFlowNodeMCPTools(nilTools)
	if string(toolsJSON) != "[]" {
		t.Fatalf("nil tools json = %s, want []", string(toolsJSON))
	}

	domainsJSON := marshalFlowNodeToolDomains(nilDomains)
	if string(domainsJSON) != "[]" {
		t.Fatalf("nil tool domains json = %s, want []", string(domainsJSON))
	}
}
