package mcp

import (
	"context"
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
}

type fakeToolDefinitionWriter struct {
	tools []repo.ToolDefinition
}

func (f *fakeToolDefinitionWriter) BulkUpsert(_ context.Context, tools []repo.ToolDefinition) ([]repo.ToolDefinition, error) {
	f.tools = append(f.tools, tools...)
	return tools, nil
}
