package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUnknownTool     = errors.New("unknown tool")
	ErrInvalidToolCall = errors.New("invalid tool call")
)

type ToolHandler func(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error)

type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     ToolHandler
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Data any    `json:"data,omitempty"`
}

type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolRegistry struct {
	order []string
	tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		order: make([]string, 0),
		tools: make(map[string]Tool),
	}
}

func (r *ToolRegistry) Register(tool Tool) error {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return fmt.Errorf("%w: missing name", ErrInvalidToolCall)
	}
	if tool.Handler == nil {
		return fmt.Errorf("%w: missing handler for %s", ErrInvalidToolCall, name)
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("%w: duplicate tool %s", ErrInvalidToolCall, name)
	}
	if tool.InputSchema == nil {
		tool.InputSchema = map[string]any{"type": "object"}
	}
	tool.Name = name
	r.order = append(r.order, name)
	r.tools[name] = tool
	return nil
}

func (r *ToolRegistry) List() []toolDescriptor {
	out := make([]toolDescriptor, 0, len(r.order))
	for _, name := range r.order {
		tool := r.tools[name]
		out = append(out, toolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	return out
}

func (r *ToolRegistry) Call(ctx context.Context, identity Identity, name string, args map[string]any) (ToolCallResult, error) {
	toolName := strings.TrimSpace(name)
	tool, ok := r.tools[toolName]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: %s", ErrUnknownTool, toolName)
	}
	if args == nil {
		args = map[string]any{}
	}
	return tool.Handler(ctx, identity, args)
}
