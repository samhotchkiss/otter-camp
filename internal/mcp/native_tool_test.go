package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRegisterNativeToolDefinitions(t *testing.T) {
	writer := &fakeToolDefinitionWriter{}
	if err := RegisterNativeToolDefinitions(context.Background(), writer); err != nil {
		t.Fatalf("RegisterNativeToolDefinitions: %v", err)
	}
	if len(writer.tools) != 1 {
		t.Fatalf("upserted tool count = %d, want 1", len(writer.tools))
	}
	tool := writer.tools[0]
	if tool.Name != DiscoverToolName {
		t.Fatalf("tool name = %q, want %q", tool.Name, DiscoverToolName)
	}
	if tool.ToolTier != "tier1" {
		t.Fatalf("tool tier = %q, want tier1", tool.ToolTier)
	}
	if tool.ToolDomain != "mcp" {
		t.Fatalf("tool domain = %q, want mcp", tool.ToolDomain)
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}
	propertiesRaw, ok := schema["properties"]
	if !ok {
		t.Fatal("discover tool schema missing properties")
	}
	properties, ok := propertiesRaw.(map[string]any)
	if !ok {
		t.Fatal("discover tool schema properties should be an object")
	}
	if _, ok := properties["connection_id"]; !ok {
		t.Fatal("discover tool schema missing connection_id property")
	}
}

type fakeToolDefinitionWriter struct {
	tools []repo.ToolDefinition
}

func (f *fakeToolDefinitionWriter) BulkUpsert(_ context.Context, tools []repo.ToolDefinition) ([]repo.ToolDefinition, error) {
	f.tools = append(f.tools, tools...)
	return tools, nil
}
