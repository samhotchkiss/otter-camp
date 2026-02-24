package mcp

import (
	"context"
	"encoding/json"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type ToolManifest struct {
	ToolName    string
	Description string
	InputSchema json.RawMessage
	Metadata    json.RawMessage
}

type Transport interface {
	ListTools(ctx context.Context, connection repo.MCPConnection, resolvedConfig map[string]any, env map[string]string) ([]ToolManifest, error)
}

type MockTransport struct {
	Tools []ToolManifest
	Err   error
}

func (m MockTransport) ListTools(_ context.Context, _ repo.MCPConnection, _ map[string]any, _ map[string]string) ([]ToolManifest, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	out := make([]ToolManifest, len(m.Tools))
	copy(out, m.Tools)
	return out, nil
}
