package repo

import (
	"testing"

	"github.com/google/uuid"
)

func TestFlowNodeJSONMappingRoundTrip(t *testing.T) {
	expectedTools := []FlowNodeMCPTool{
		{ConnectionID: uuid.New(), ToolName: "tool.alpha"},
		{ConnectionID: uuid.New(), ToolName: "tool.beta"},
	}
	expectedDomains := []string{"file", "cli", "mcp"}

	toolsRaw, err := normalizeFlowNodeMCPTools(expectedTools)
	if err != nil {
		t.Fatalf("normalizeFlowNodeMCPTools: %v", err)
	}
	domainsRaw, err := normalizeFlowNodeToolDomains(expectedDomains)
	if err != nil {
		t.Fatalf("normalizeFlowNodeToolDomains: %v", err)
	}

	decodedTools, err := decodeFlowNodeMCPTools(toolsRaw)
	if err != nil {
		t.Fatalf("decodeFlowNodeMCPTools: %v", err)
	}
	decodedDomains, err := decodeFlowNodeToolDomains(domainsRaw)
	if err != nil {
		t.Fatalf("decodeFlowNodeToolDomains: %v", err)
	}

	if len(decodedTools) != len(expectedTools) {
		t.Fatalf("decoded tools len = %d, want %d", len(decodedTools), len(expectedTools))
	}
	for i := range expectedTools {
		if decodedTools[i].ConnectionID != expectedTools[i].ConnectionID || decodedTools[i].ToolName != expectedTools[i].ToolName {
			t.Fatalf("decoded tool[%d] = %+v, want %+v", i, decodedTools[i], expectedTools[i])
		}
	}

	if len(decodedDomains) != len(expectedDomains) {
		t.Fatalf("decoded domains len = %d, want %d", len(decodedDomains), len(expectedDomains))
	}
	for i := range expectedDomains {
		if decodedDomains[i] != expectedDomains[i] {
			t.Fatalf("decoded domain[%d] = %q, want %q", i, decodedDomains[i], expectedDomains[i])
		}
	}
}

func TestFlowNodeJSONMappingEmptyArraysSerializeAsEmptyArray(t *testing.T) {
	toolsRaw, err := normalizeFlowNodeMCPTools(nil)
	if err != nil {
		t.Fatalf("normalizeFlowNodeMCPTools: %v", err)
	}
	domainsRaw, err := normalizeFlowNodeToolDomains(nil)
	if err != nil {
		t.Fatalf("normalizeFlowNodeToolDomains: %v", err)
	}

	if got := string(toolsRaw); got != "[]" {
		t.Fatalf("tools raw = %q, want []", got)
	}
	if got := string(domainsRaw); got != "[]" {
		t.Fatalf("domains raw = %q, want []", got)
	}

	decodedTools, err := decodeFlowNodeMCPTools(toolsRaw)
	if err != nil {
		t.Fatalf("decodeFlowNodeMCPTools: %v", err)
	}
	decodedDomains, err := decodeFlowNodeToolDomains(domainsRaw)
	if err != nil {
		t.Fatalf("decodeFlowNodeToolDomains: %v", err)
	}

	if decodedTools == nil || len(decodedTools) != 0 {
		t.Fatalf("decoded tools = %#v, want empty non-nil slice", decodedTools)
	}
	if decodedDomains == nil || len(decodedDomains) != 0 {
		t.Fatalf("decoded domains = %#v, want empty non-nil slice", decodedDomains)
	}
}
