package testutil

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type MCPConnectionOptions struct {
	ProjectID       *uuid.UUID
	DisplayName     string
	Slug            string
	Transport       string
	TransportConfig json.RawMessage
	Status          string
	IsEnabled       *bool
	CreatedByType   string
	CreatedByID     uuid.UUID
}

func MakeMCPConnection(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, opts MCPConnectionOptions) uuid.UUID {
	t.Helper()

	displayName := strings.TrimSpace(opts.DisplayName)
	if displayName == "" {
		displayName = "MCP " + uuid.NewString()[:8]
	}
	slug := strings.TrimSpace(opts.Slug)
	if slug == "" {
		slug = "mcp-" + strings.ToLower(uuid.NewString()[:8])
	}
	transport := strings.TrimSpace(opts.Transport)
	if transport == "" {
		transport = "http"
	}
	createdByType := strings.TrimSpace(opts.CreatedByType)
	if createdByType == "" {
		createdByType = "system"
	}
	isEnabled := true
	if opts.IsEnabled != nil {
		isEnabled = *opts.IsEnabled
	}

	created, err := repo.NewMCPConnectionRepo(db).Create(context.Background(), repo.MCPConnection{
		OrganizationID:  orgID,
		ProjectID:       opts.ProjectID,
		DisplayName:     displayName,
		Slug:            slug,
		Transport:       transport,
		TransportConfig: opts.TransportConfig,
		Status:          opts.Status,
		IsEnabled:       isEnabled,
		CreatedByType:   createdByType,
		CreatedByID:     opts.CreatedByID,
	})
	if err != nil {
		t.Fatalf("create mcp connection: %v", err)
	}
	return created.ID
}

type MCPTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Metadata    json.RawMessage
}

type MockMCPServerOptions struct {
	Tools           []MCPTool
	CallResult      json.RawMessage
	CallStatusCode  int
	CallErrorBody   string
	HealthStatus    int
	HealthBody      string
	PerRequestDelay time.Duration
}

type ScriptedMCPHandler func(w http.ResponseWriter, r *http.Request)

type MCPServer struct {
	URL string

	server *httptest.Server

	mu          sync.Mutex
	pathCalls   map[string]int
	totalCalls  int
	lastRequest map[string]json.RawMessage
}

func (s *MCPServer) Close() {
	if s == nil || s.server == nil {
		return
	}
	s.server.Close()
}

func (s *MCPServer) TotalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalCalls
}

func (s *MCPServer) CallsForPath(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pathCalls[path]
}

func (s *MCPServer) LastRequest(path string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	body := s.lastRequest[path]
	if len(body) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(body))
	copy(out, body)
	return out
}

func MockMCPServer(t testing.TB, opts MockMCPServerOptions) *MCPServer {
	t.Helper()

	tools := opts.Tools
	if len(tools) == 0 {
		tools = []MCPTool{{
			Name:        "tool.echo",
			Description: "Echo tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Metadata:    json.RawMessage(`{}`),
		}}
	}
	callResult := opts.CallResult
	if len(callResult) == 0 {
		callResult = json.RawMessage(`{"ok":true}`)
	}
	healthStatus := opts.HealthStatus
	if healthStatus == 0 {
		healthStatus = http.StatusOK
	}

	srv := &MCPServer{
		pathCalls:   make(map[string]int),
		lastRequest: make(map[string]json.RawMessage),
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.recordCall(r)
		if opts.PerRequestDelay > 0 {
			time.Sleep(opts.PerRequestDelay)
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			writeText(w, healthStatus, opts.HealthBody)
			return
		case r.Method == http.MethodPost && r.URL.Path == "/tools/list":
			list := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				list = append(list, map[string]any{
					"name":         tool.Name,
					"description":  tool.Description,
					"input_schema": normalizeJSON(tool.InputSchema, json.RawMessage(`{"type":"object"}`)),
					"metadata":     normalizeJSON(tool.Metadata, json.RawMessage(`{}`)),
				})
			}
			writeJSON(w, http.StatusOK, map[string]any{"tools": list})
			return
		case r.Method == http.MethodPost && r.URL.Path == "/tools/call":
			if opts.CallStatusCode >= 400 {
				writeText(w, opts.CallStatusCode, opts.CallErrorBody)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"result": json.RawMessage(callResult)})
			return
		default:
			writeText(w, http.StatusNotFound, "not found")
			return
		}
	})

	srv.server = httptest.NewServer(handler)
	srv.URL = srv.server.URL
	t.Cleanup(srv.Close)
	return srv
}

func ScriptedMCPServer(t testing.TB, handlers []ScriptedMCPHandler) *MCPServer {
	t.Helper()

	srv := &MCPServer{
		pathCalls:   make(map[string]int),
		lastRequest: make(map[string]json.RawMessage),
	}

	var (
		mu    sync.Mutex
		index int
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.recordCall(r)

		mu.Lock()
		defer mu.Unlock()
		if index >= len(handlers) {
			writeText(w, http.StatusInternalServerError, "script exhausted")
			return
		}
		current := handlers[index]
		index++
		current(w, r)
	})

	srv.server = httptest.NewServer(handler)
	srv.URL = srv.server.URL
	t.Cleanup(srv.Close)
	return srv
}

func (s *MCPServer) recordCall(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalCalls++
	s.pathCalls[r.URL.Path]++
	if len(body) > 0 {
		s.lastRequest[r.URL.Path] = append(json.RawMessage(nil), body...)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	if strings.TrimSpace(body) == "" {
		return
	}
	_, _ = w.Write([]byte(body))
}

func normalizeJSON(raw, fallback json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return fallback
	}
	return raw
}
