package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type ToolManifest struct {
	ToolName    string
	Description string
	InputSchema json.RawMessage
	Metadata    json.RawMessage
}

type MCPTransport interface {
	Call(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error)
	HealthCheck(ctx context.Context) error
	DiscoverTools(ctx context.Context) ([]ToolManifest, error)
	Close() error
}

type TransportFactory interface {
	New(ctx context.Context, connection repo.MCPConnection, resolvedConfig map[string]any, env map[string]string) (MCPTransport, error)
}

type DefaultTransportFactory struct {
	HTTPClient *http.Client
}

func NewDefaultTransportFactory(client *http.Client) *DefaultTransportFactory {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &DefaultTransportFactory{HTTPClient: client}
}

func (f *DefaultTransportFactory) New(_ context.Context, connection repo.MCPConnection, resolvedConfig map[string]any, env map[string]string) (MCPTransport, error) {
	switch strings.TrimSpace(connection.Transport) {
	case "stdio":
		command, ok := stringConfig(resolvedConfig, "command")
		if !ok || command == "" {
			return nil, fmt.Errorf("stdio transport requires command in transport_config")
		}
		args := sliceConfig(resolvedConfig, "args")
		workingDir, _ := stringConfig(resolvedConfig, "working_dir")
		processEnv := stringMapConfig(resolvedConfig, "env")
		for key, value := range env {
			processEnv[key] = value
		}
		return NewStdioTransport(command, args, workingDir, processEnv)
	case "http":
		baseURL, ok := stringConfig(resolvedConfig, "base_url")
		if !ok || baseURL == "" {
			return nil, fmt.Errorf("http transport requires base_url in transport_config")
		}
		return NewHTTPTransport(baseURL, f.HTTPClient), nil
	case "sse":
		sseURL, ok := stringConfig(resolvedConfig, "sse_url", "endpoint")
		if !ok || sseURL == "" {
			return nil, fmt.Errorf("sse transport requires sse_url or endpoint in transport_config")
		}
		var rpcTransport *HTTPTransport
		if rpcURL, ok := stringConfig(resolvedConfig, "rpc_url"); ok && rpcURL != "" {
			rpcTransport = NewHTTPTransport(rpcURL, f.HTTPClient)
		}
		return NewSSETransport(sseURL, rpcTransport, f.HTTPClient), nil
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q", connection.Transport)
	}
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorObject `json:"error,omitempty"`
}

type rpcErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type StdioTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	stderr    bytes.Buffer
	nextID    int64
	closeOnce sync.Once
	waitDone  chan struct{}
	waitErr   error
	mu        sync.Mutex
}

func NewStdioTransport(command string, args []string, workingDir string, env map[string]string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	cmd.Stderr = &bytes.Buffer{}
	if strings.TrimSpace(workingDir) != "" {
		cmd.Dir = strings.TrimSpace(workingDir)
	}
	mergedEnv := os.Environ()
	for key, value := range env {
		mergedEnv = append(mergedEnv, fmt.Sprintf("%s=%s", key, value))
	}
	cmd.Env = mergedEnv

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdio stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdio stdout pipe: %w", err)
	}

	stderrBuf := &bytes.Buffer{}
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start stdio transport process: %w", err)
	}

	transport := &StdioTransport{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewReader(stdoutPipe),
		stderr:   *stderrBuf,
		waitDone: make(chan struct{}),
	}
	go func() {
		transport.waitErr = cmd.Wait()
		close(transport.waitDone)
	}()
	return transport, nil
}

func (t *StdioTransport) Call(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error) {
	params, err := json.Marshal(map[string]any{
		"name":  strings.TrimSpace(tool),
		"input": normalizeRawJSON(input),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tools/call params: %w", err)
	}
	response, err := t.sendRPC(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}

	var resultEnvelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(response, &resultEnvelope); err == nil && len(resultEnvelope.Result) > 0 {
		return resultEnvelope.Result, nil
	}
	return response, nil
}

func (t *StdioTransport) HealthCheck(ctx context.Context) error {
	if err := t.processHealth(); err != nil {
		return err
	}
	_, err := t.sendRPC(ctx, "ping", json.RawMessage(`{}`))
	return err
}

func (t *StdioTransport) DiscoverTools(ctx context.Context) ([]ToolManifest, error) {
	response, err := t.sendRPC(ctx, "tools/list", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
			Metadata    json.RawMessage `json:"metadata"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, fmt.Errorf("decode stdio tools/list result: %w", err)
	}

	manifest := make([]ToolManifest, 0, len(result.Tools))
	for _, tool := range result.Tools {
		manifest = append(manifest, ToolManifest{
			ToolName:    strings.TrimSpace(tool.Name),
			Description: tool.Description,
			InputSchema: normalizeRawJSON(tool.InputSchema),
			Metadata:    normalizeRawJSON(tool.Metadata),
		})
	}
	return manifest, nil
}

func (t *StdioTransport) Close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		if t.cmd == nil || t.cmd.Process == nil {
			return
		}

		_ = t.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-t.waitDone:
			closeErr = t.waitErr
		case <-time.After(5 * time.Second):
			_ = t.cmd.Process.Kill()
			<-t.waitDone
			closeErr = t.waitErr
		}
		_ = t.stdin.Close()
	})
	return closeErr
}

func (t *StdioTransport) processHealth() error {
	select {
	case <-t.waitDone:
		exitCode := -1
		if t.cmd.ProcessState != nil {
			exitCode = t.cmd.ProcessState.ExitCode()
		}
		return fmt.Errorf("stdio process is not running (exit_code=%d)", exitCode)
	default:
		return nil
	}
}

func (t *StdioTransport) sendRPC(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.processHealth(); err != nil {
		return nil, err
	}

	t.nextID++
	envelope := rpcEnvelope{
		JSONRPC: "2.0",
		ID:      t.nextID,
		Method:  method,
		Params:  normalizeRawJSON(params),
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}
	body = append(body, '\n')

	if _, err := t.stdin.Write(body); err != nil {
		return nil, fmt.Errorf("write rpc request: %w", err)
	}

	line, err := readLineContext(ctx, t.stdout)
	if err != nil {
		return nil, fmt.Errorf("read rpc response: %w", err)
	}
	var response rpcEnvelope
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w", err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("rpc %s failed: %s", method, strings.TrimSpace(response.Error.Message))
	}
	return normalizeRawJSON(response.Result), nil
}

type HTTPTransport struct {
	baseURL string
	client  *http.Client
}

func NewHTTPTransport(baseURL string, client *http.Client) *HTTPTransport {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPTransport{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  client,
	}
}

func (t *HTTPTransport) Call(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error) {
	var inputValue any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &inputValue); err != nil {
			return nil, fmt.Errorf("decode tool input json: %w", err)
		}
	}
	return t.doJSON(ctx, http.MethodPost, "/tools/call", map[string]any{
		"name":  strings.TrimSpace(tool),
		"input": inputValue,
	})
}

func (t *HTTPTransport) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	resp, err := t.client.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return nil
		}
	}

	_, discoverErr := t.doJSON(ctx, http.MethodPost, "/tools/list", map[string]any{})
	return discoverErr
}

func (t *HTTPTransport) DiscoverTools(ctx context.Context) ([]ToolManifest, error) {
	raw, err := t.doJSON(ctx, http.MethodPost, "/tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
			Metadata    json.RawMessage `json:"metadata"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode tools/list response: %w", err)
	}

	manifest := make([]ToolManifest, 0, len(result.Tools))
	for _, tool := range result.Tools {
		manifest = append(manifest, ToolManifest{
			ToolName:    strings.TrimSpace(tool.Name),
			Description: tool.Description,
			InputSchema: normalizeRawJSON(tool.InputSchema),
			Metadata:    normalizeRawJSON(tool.Metadata),
		})
	}
	return manifest, nil
}

func (t *HTTPTransport) Close() error {
	return nil
}

func (t *HTTPTransport) doJSON(ctx context.Context, method, path string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal http payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read http response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var parsed struct {
		Result json.RawMessage `json:"result"`
		Tools  json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil {
		if len(parsed.Result) > 0 {
			return parsed.Result, nil
		}
		if len(parsed.Tools) > 0 {
			return json.RawMessage(`{"tools":` + string(parsed.Tools) + `}`), nil
		}
	}
	return normalizeRawJSON(data), nil
}

type SSETransport struct {
	sseURL string
	rpc    *HTTPTransport
	client *http.Client
}

func NewSSETransport(sseURL string, rpc *HTTPTransport, client *http.Client) *SSETransport {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &SSETransport{
		sseURL: strings.TrimSpace(sseURL),
		rpc:    rpc,
		client: client,
	}
}

func (t *SSETransport) Call(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error) {
	if t.rpc == nil {
		return nil, errors.New("sse transport has no rpc_url configured")
	}
	return t.rpc.Call(ctx, tool, input)
}

func (t *SSETransport) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.sseURL, nil)
	if err != nil {
		return fmt.Errorf("build sse health request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("sse health status %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	line, err := readLineContext(ctx, reader)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(string(line))
	if strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, ":") {
		return nil
	}
	return fmt.Errorf("unexpected sse payload: %s", trimmed)
}

func (t *SSETransport) DiscoverTools(ctx context.Context) ([]ToolManifest, error) {
	if t.rpc == nil {
		return nil, errors.New("sse transport has no rpc_url configured")
	}
	return t.rpc.DiscoverTools(ctx)
}

func (t *SSETransport) Close() error {
	return nil
}

type MockTransport struct {
	Tools             []ToolManifest
	CallResult        json.RawMessage
	CallErr           error
	HealthCheckErr    error
	DiscoverToolsErr  error
	CloseErr          error
	CallCount         int
	HealthCheckCount  int
	DiscoverToolCount int
	CloseCount        int
	CallFn            func(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error)
	HealthCheckFn     func(ctx context.Context) error
	DiscoverToolsFn   func(ctx context.Context) ([]ToolManifest, error)
	CloseFn           func() error
	mu                sync.Mutex
}

func (m *MockTransport) Call(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error) {
	m.mu.Lock()
	m.CallCount++
	m.mu.Unlock()
	if m.CallFn != nil {
		return m.CallFn(ctx, tool, input)
	}
	if m.CallErr != nil {
		return nil, m.CallErr
	}
	return normalizeRawJSON(m.CallResult), nil
}

func (m *MockTransport) HealthCheck(ctx context.Context) error {
	m.mu.Lock()
	m.HealthCheckCount++
	m.mu.Unlock()
	if m.HealthCheckFn != nil {
		return m.HealthCheckFn(ctx)
	}
	return m.HealthCheckErr
}

func (m *MockTransport) DiscoverTools(ctx context.Context) ([]ToolManifest, error) {
	m.mu.Lock()
	m.DiscoverToolCount++
	m.mu.Unlock()
	if m.DiscoverToolsFn != nil {
		return m.DiscoverToolsFn(ctx)
	}
	if m.DiscoverToolsErr != nil {
		return nil, m.DiscoverToolsErr
	}
	out := make([]ToolManifest, len(m.Tools))
	copy(out, m.Tools)
	return out, nil
}

func (m *MockTransport) Close() error {
	m.mu.Lock()
	m.CloseCount++
	m.mu.Unlock()
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return m.CloseErr
}

type StaticTransportFactory struct {
	Transport MCPTransport
	Err       error
}

func (f StaticTransportFactory) New(_ context.Context, _ repo.MCPConnection, _ map[string]any, _ map[string]string) (MCPTransport, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Transport, nil
}

func stringConfig(config map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := config[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed), true
		}
	}
	return "", false
}

func sliceConfig(config map[string]any, key string) []string {
	raw, ok := config[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

func stringMapConfig(config map[string]any, key string) map[string]string {
	raw, ok := config[key]
	if !ok {
		return map[string]string{}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, value := range m {
		text, ok := value.(string)
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = text
	}
	return out
}

func normalizeRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func readLineContext(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := reader.ReadBytes('\n')
		done <- result{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-done:
		return bytes.TrimSpace(out.line), out.err
	}
}
