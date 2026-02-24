package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const DiscoverToolName = "mcp.discover"

type ToolDefinitionWriter interface {
	BulkUpsert(ctx context.Context, tools []repo.ToolDefinition) ([]repo.ToolDefinition, error)
}

func RegisterNativeToolDefinitions(ctx context.Context, writer ToolDefinitionWriter) error {
	if writer == nil {
		return fmt.Errorf("tool definition writer is required")
	}

	_, err := writer.BulkUpsert(ctx, []repo.ToolDefinition{discoverToolDefinition()})
	return err
}

func discoverToolDefinition() repo.ToolDefinition {
	return repo.ToolDefinition{
		Name:        DiscoverToolName,
		DisplayName: "MCP Discover",
		Description: "Discover available MCP tools and their descriptions for a given connection",
		ToolTier:    "tier1",
		ToolDomain:  "mcp",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"required":["connection_id"],
			"properties":{
				"connection_id":{"type":"string","format":"uuid"},
				"filter":{"type":"string"}
			}
		}`),
		IsEnabled: true,
	}
}
