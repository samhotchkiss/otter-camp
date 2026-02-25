package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrPermanent    = errors.New("mcp permanent failure")
	ErrTimeout      = errors.New("mcp execution timed out")
	ErrAccessDenied = errors.New("mcp access denied")
)

var http4xxRegex = regexp.MustCompile(`(?i)http status 4\d\d`)

type ExecutionContext struct {
	OrganizationID  uuid.UUID
	ToolExecutionID *uuid.UUID
	RunID           *uuid.UUID
	AgentID         *uuid.UUID
}

type executionContextKey struct{}

func WithExecutionContext(ctx context.Context, execution ExecutionContext) context.Context {
	return context.WithValue(ctx, executionContextKey{}, execution)
}

func executionContextFromContext(ctx context.Context) ExecutionContext {
	if ctx == nil {
		return ExecutionContext{}
	}
	stored, ok := ctx.Value(executionContextKey{}).(ExecutionContext)
	if !ok {
		return ExecutionContext{}
	}
	return stored
}

type MCPExecutionLogWriter interface {
	Create(ctx context.Context, entry repo.MCPExecutionLog) (repo.MCPExecutionLog, error)
	Complete(ctx context.Context, id uuid.UUID, status string, responsePayload json.RawMessage, errorMessage *string, latencyMS *int) (repo.MCPExecutionLog, error)
}

type MCPConnectionReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.MCPConnection, error)
}

type MCPConnectionStatusWriter interface {
	SetStatus(ctx context.Context, id uuid.UUID, status string) (repo.MCPConnection, error)
}

type MCPToolCatalogReader interface {
	ListByConnection(ctx context.Context, connectionID uuid.UUID) ([]repo.MCPToolCatalogEntry, error)
}

type AgentProjectAssignmentReader interface {
	GetByAgentAndProject(ctx context.Context, agentID, projectID uuid.UUID) (repo.AgentProjectAssignment, error)
}

type MCPCaller interface {
	CallTool(ctx context.Context, req CallToolRequest) (json.RawMessage, error)
	CircuitState(connID uuid.UUID) CBState
}

type MCPResourceReader interface {
	ReadResource(ctx context.Context, connectionID uuid.UUID, resourceURI string) (json.RawMessage, error)
}

type ExecutorOptions struct {
	Connections      MCPConnectionReader
	ConnectionStatus MCPConnectionStatusWriter
	Catalog          MCPToolCatalogReader
	Assignments      AgentProjectAssignmentReader
	Caller           MCPCaller
	Resources        MCPResourceReader
	Logs             MCPExecutionLogWriter
	Now              func() time.Time
}

type Executor struct {
	connections      MCPConnectionReader
	connectionStatus MCPConnectionStatusWriter
	catalog          MCPToolCatalogReader
	assignments      AgentProjectAssignmentReader
	caller           MCPCaller
	resources        MCPResourceReader
	logs             MCPExecutionLogWriter
	now              func() time.Time
}

func NewExecutor(opts ExecutorOptions) (*Executor, error) {
	if opts.Connections == nil {
		return nil, fmt.Errorf("mcp executor requires a connection repository")
	}
	if opts.Caller == nil {
		return nil, fmt.Errorf("mcp executor requires a caller")
	}
	if opts.Logs == nil {
		return nil, fmt.Errorf("mcp executor requires an execution log repository")
	}

	exec := &Executor{
		connections:      opts.Connections,
		connectionStatus: opts.ConnectionStatus,
		catalog:          opts.Catalog,
		assignments:      opts.Assignments,
		caller:           opts.Caller,
		resources:        opts.Resources,
		logs:             opts.Logs,
		now:              opts.Now,
	}
	if exec.now == nil {
		exec.now = func() time.Time { return time.Now().UTC() }
	}
	return exec, nil
}

func (e *Executor) Execute(ctx context.Context, toolName string, input map[string]any, mcpConnectionID uuid.UUID) (map[string]any, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if mcpConnectionID == uuid.Nil {
		return nil, fmt.Errorf("mcp connection id is required")
	}

	connection, err := e.connections.GetByID(ctx, mcpConnectionID)
	if err != nil {
		return nil, err
	}
	execCtx := executionContextFromContext(ctx)

	requestPayload := mustJSON(input)
	catalogID := e.lookupCatalogEntryID(ctx, connection.ID, toolName)
	createdLog, err := e.logs.Create(ctx, repo.MCPExecutionLog{
		OrganizationID:   connection.OrganizationID,
		MCPConnectionID:  connection.ID,
		MCPToolCatalogID: catalogID,
		ToolExecutionID:  execCtx.ToolExecutionID,
		RunID:            execCtx.RunID,
		AgentID:          execCtx.AgentID,
		Method:           "tools/call",
		ToolName:         &toolName,
		RequestPayload:   requestPayload,
		Status:           "pending",
	})
	if err != nil {
		return nil, err
	}

	if e.caller.CircuitState(connection.ID) == CBOpen {
		errMessage := ErrCircuitOpen.Error()
		if _, completeErr := e.logs.Complete(ctx, createdLog.ID, "circuit_open", nil, &errMessage, intPtr(0)); completeErr != nil {
			return nil, completeErr
		}
		return nil, ErrCircuitOpen
	}

	safeToRetry := false
	start := e.now()
	result, callErr := e.caller.CallTool(ctx, CallToolRequest{
		ConnectionID: connection.ID,
		Tool:         toolName,
		Input:        requestPayload,
		Intent:       ToolIntentWrite,
		SafeToRetry:  &safeToRetry,
	})
	latencyMS := int(time.Since(start).Milliseconds())
	if callErr != nil {
		status := "error"
		resultErr := callErr
		if isTimeoutError(callErr) {
			status = "timeout"
			resultErr = fmt.Errorf("%w: %v", ErrTimeout, callErr)
		} else if isPermanentError(callErr) {
			resultErr = fmt.Errorf("%w: %v", ErrPermanent, callErr)
			if e.connectionStatus != nil {
				_, _ = e.connectionStatus.SetStatus(ctx, connection.ID, "failed")
			}
		}
		errMessage := callErr.Error()
		if _, completeErr := e.logs.Complete(ctx, createdLog.ID, status, nil, &errMessage, &latencyMS); completeErr != nil {
			return nil, completeErr
		}
		return nil, resultErr
	}

	if _, completeErr := e.logs.Complete(ctx, createdLog.ID, "success", normalizeRawJSON(result), nil, &latencyMS); completeErr != nil {
		return nil, completeErr
	}
	decoded, err := toMap(result)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func (e *Executor) ReadResource(ctx context.Context, resourceURI string, mcpConnectionID uuid.UUID) (map[string]any, error) {
	resourceURI = strings.TrimSpace(resourceURI)
	if resourceURI == "" {
		return nil, fmt.Errorf("resource uri is required")
	}
	if mcpConnectionID == uuid.Nil {
		return nil, fmt.Errorf("mcp connection id is required")
	}

	connection, err := e.connections.GetByID(ctx, mcpConnectionID)
	if err != nil {
		return nil, err
	}
	execCtx := executionContextFromContext(ctx)
	createdLog, err := e.logs.Create(ctx, repo.MCPExecutionLog{
		OrganizationID:  connection.OrganizationID,
		MCPConnectionID: connection.ID,
		ToolExecutionID: execCtx.ToolExecutionID,
		RunID:           execCtx.RunID,
		AgentID:         execCtx.AgentID,
		Method:          "resources/read",
		ResourceURI:     &resourceURI,
		RequestPayload:  mustJSON(map[string]any{"resource_uri": resourceURI}),
		Status:          "pending",
	})
	if err != nil {
		return nil, err
	}

	if connection.ProjectID != nil {
		if execCtx.AgentID == nil || *execCtx.AgentID == uuid.Nil || e.assignments == nil {
			errMessage := ErrAccessDenied.Error()
			_, _ = e.logs.Complete(ctx, createdLog.ID, "error", nil, &errMessage, intPtr(0))
			return nil, ErrAccessDenied
		}
		assignment, assignmentErr := e.assignments.GetByAgentAndProject(ctx, *execCtx.AgentID, *connection.ProjectID)
		if assignmentErr != nil || !assignment.IsActive {
			errMessage := ErrAccessDenied.Error()
			_, _ = e.logs.Complete(ctx, createdLog.ID, "error", nil, &errMessage, intPtr(0))
			return nil, ErrAccessDenied
		}
	}

	if e.resources == nil {
		errMessage := "resource reader is not configured"
		_, _ = e.logs.Complete(ctx, createdLog.ID, "error", nil, &errMessage, intPtr(0))
		return nil, fmt.Errorf("resource reader is not configured")
	}

	start := e.now()
	resourcePayload, readErr := e.resources.ReadResource(ctx, connection.ID, resourceURI)
	latencyMS := int(time.Since(start).Milliseconds())
	if readErr != nil {
		status := "error"
		if isTimeoutError(readErr) {
			status = "timeout"
		}
		errMessage := readErr.Error()
		if _, completeErr := e.logs.Complete(ctx, createdLog.ID, status, nil, &errMessage, &latencyMS); completeErr != nil {
			return nil, completeErr
		}
		if status == "timeout" {
			return nil, fmt.Errorf("%w: %v", ErrTimeout, readErr)
		}
		return nil, readErr
	}

	if _, completeErr := e.logs.Complete(ctx, createdLog.ID, "success", normalizeRawJSON(resourcePayload), nil, &latencyMS); completeErr != nil {
		return nil, completeErr
	}
	decoded, err := toMap(resourcePayload)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func (e *Executor) lookupCatalogEntryID(ctx context.Context, connectionID uuid.UUID, toolName string) *uuid.UUID {
	if e.catalog == nil {
		return nil
	}
	entries, err := e.catalog.ListByConnection(ctx, connectionID)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.ToolName == toolName {
			id := entry.ID
			return &id
		}
	}
	return nil
}

func toMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode mcp payload: %w", err)
	}
	if decoded == nil {
		return map[string]any{}, nil
	}
	return decoded, nil
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func intPtr(value int) *int {
	return &value
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded")
}

func isPermanentError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if http4xxRegex.MatchString(message) {
		return true
	}
	return strings.Contains(message, "schema") || strings.Contains(message, "validation")
}
