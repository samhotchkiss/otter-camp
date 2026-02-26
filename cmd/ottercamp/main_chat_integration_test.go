//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestChatStartRoundTripAndListIntegration(t *testing.T) {
	fixture := newChatCLIIntegrationFixture(t)
	defer fixture.Close()

	restore := setChatCLIEnvForTest(t, fixture.ServerURL, fixture.APIKey, clitools.OutputModeTable)
	defer restore()

	scopeID := uuid.New()
	code, startOut, startErr := captureCommandOutput(t, func() int {
		return runChatStart([]string{
			"--scope-type=project",
			"--scope-id=" + scopeID.String(),
		})
	})
	if code != 0 {
		t.Fatalf("chat start exit=%d stderr=%q", code, startErr)
	}
	if !strings.Contains(startOut, "Session created") {
		t.Fatalf("chat start output missing success header: %q", startOut)
	}

	sessionID := extractSessionIDFromStartOutput(t, startOut)
	client, err := newCLIAPIClient(fixture.ServerURL, fixture.APIKey)
	if err != nil {
		t.Fatalf("newCLIAPIClient: %v", err)
	}
	sessions, err := client.ListChatSessions(context.Background(), chatListSessionsFilter{Status: "active", Limit: 50})
	if err != nil {
		t.Fatalf("ListChatSessions: %v", err)
	}
	found := false
	for _, item := range sessions.Data {
		if item.ID == sessionID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created session %s not present in GET /v1/chat-sessions", sessionID)
	}

	code, listOut, listErr := captureCommandOutput(t, func() int {
		return runChatList([]string{})
	})
	if code != 0 {
		t.Fatalf("chat list exit=%d stderr=%q", code, listErr)
	}
	if !strings.Contains(listOut, sessionID.String()) {
		t.Fatalf("chat list output missing session id %s: %q", sessionID, listOut)
	}
}

func TestChatSendWaitUsesSSEIntegration(t *testing.T) {
	fixture := newChatCLIIntegrationFixture(t)
	defer fixture.Close()

	restore := setChatCLIEnvForTest(t, fixture.ServerURL, fixture.APIKey, clitools.OutputModeTable)
	defer restore()

	session, err := fixture.ChatService.CreateSession(context.Background(), chat.CreateSessionInput{
		OrganizationID: fixture.Org.ID,
		ScopeType:      "organization",
		ScopeID:        fixture.Org.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		payloadChunk, _ := json.Marshal(map[string]any{
			"session_id": session.ID.String(),
			"delta":      "hello from agent",
		})
		_ = fixture.Bus.Publish(context.Background(), nil, eventbus.DomainEvent{
			OrganizationID: fixture.Org.ID,
			EventType:      "chat.message.chunk",
			ActorType:      "agent",
			ActorID:        &fixture.User.ID,
			Payload:        payloadChunk,
		})

		payloadDone, _ := json.Marshal(map[string]any{
			"session_id": session.ID.String(),
			"turn_id":    uuid.NewString(),
		})
		_ = fixture.Bus.Publish(context.Background(), nil, eventbus.DomainEvent{
			OrganizationID: fixture.Org.ID,
			EventType:      "chat.turn.completed",
			ActorType:      "agent",
			ActorID:        &fixture.User.ID,
			Payload:        payloadDone,
		})
	}()

	code, out, errOut := captureCommandOutput(t, func() int {
		return runChatSend([]string{session.ID.String(), "hello", "--wait", "--timeout=3s"})
	})
	if code != 0 {
		t.Fatalf("chat send --wait exit=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "Agent: hello from agent") {
		t.Fatalf("chat send --wait output missing streamed response: %q", out)
	}
	if !strings.Contains(out, "Turn completed in") {
		t.Fatalf("chat send --wait output missing completion line: %q", out)
	}
}

func TestChatHistoryPaginationIntegration(t *testing.T) {
	fixture := newChatCLIIntegrationFixture(t)
	defer fixture.Close()

	restore := setChatCLIEnvForTest(t, fixture.ServerURL, fixture.APIKey, clitools.OutputModeJSON)
	defer restore()

	session, err := fixture.ChatService.CreateSession(context.Background(), chat.CreateSessionInput{
		OrganizationID: fixture.Org.ID,
		ScopeType:      "organization",
		ScopeID:        fixture.Org.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	authorType := "human_user"
	for i := 0; i < 75; i++ {
		if _, err := fixture.ChatService.AppendMessage(context.Background(), chat.AppendMessageInput{
			SessionID:  session.ID,
			AuthorType: &authorType,
			AuthorID:   &fixture.User.ID,
			Role:       "user",
			Content:    "message-" + strconv.Itoa(i+1),
		}); err != nil {
			t.Fatalf("AppendMessage %d: %v", i+1, err)
		}
	}

	code, out, errOut := captureCommandOutput(t, func() int {
		return runChatHistory([]string{session.ID.String(), "--limit=50"})
	})
	if code != 0 {
		t.Fatalf("chat history first page exit=%d stderr=%q", code, errOut)
	}
	count := jsonArrayCountAtPath(t, out, "data")
	if count != 50 {
		t.Fatalf("chat history first page count=%d, want 50", count)
	}

	code, out, errOut = captureCommandOutput(t, func() int {
		return runChatHistory([]string{session.ID.String(), "--limit=50", "--before-sequence=50"})
	})
	if code != 0 {
		t.Fatalf("chat history second page exit=%d stderr=%q", code, errOut)
	}
	count = jsonArrayCountAtPath(t, out, "data")
	if count != 49 {
		t.Fatalf("chat history second page count=%d, want 49", count)
	}
}

type chatCLIIntegrationFixture struct {
	ServerURL   string
	Pool        *pgxpool.Pool
	Server      *httptest.Server
	Realtime    *server.RealtimeRouteRegistrar
	Org         repo.Organization
	User        repo.HumanUser
	APIKey      string
	Bus         *eventbus.Bus
	ChatService chat.ChatService
}

func (f *chatCLIIntegrationFixture) Close() {
	if f == nil {
		return
	}
	if f.Realtime != nil {
		_ = f.Realtime.Close()
	}
	f.Server.Close()
}

func newChatCLIIntegrationFixture(t *testing.T) *chatCLIIntegrationFixture {
	t.Helper()

	pool := testdb.New(t)
	org, user := createCLIOrgAndUser(t, pool)

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("NewService auth: %v", err)
	}

	issued, err := authService.IssueAPIKey(context.Background(), user.ID, "chat-cli-test", []string{"read:chat", "write:chat"}, nil)
	if err != nil {
		t.Fatalf("IssueAPIKey: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService chat: %v", err)
	}

	realtimeRegistrar := server.NewRealtimeRouteRegistrar(server.RealtimeRouteOptions{
		Pool:   pool,
		Events: bus,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []server.RouteRegistrar{
			server.NewChatRouteRegistrar(chatService, pool),
			realtimeRegistrar,
		},
	})

	ts := httptest.NewServer(handler)
	return &chatCLIIntegrationFixture{
		ServerURL:   ts.URL,
		Pool:        pool,
		Server:      ts,
		Realtime:    realtimeRegistrar,
		Org:         org,
		User:        user,
		APIKey:      issued.RawKey,
		Bus:         bus,
		ChatService: chatService,
	}
}

func createCLIOrgAndUser(t *testing.T, pool *pgxpool.Pool) (repo.Organization, repo.HumanUser) {
	t.Helper()
	ctx := context.Background()

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "chat-cli-" + uuid.NewString()[:8],
		DisplayName: "Chat CLI Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	hash := string(hashed)
	user, err := repo.NewHumanUserRepo(pool).Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "chat-cli-" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "Chat CLI Admin",
		PasswordHash:   &hash,
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return org, user
}

func setChatCLIEnvForTest(t *testing.T, serverURL, apiKey, outputMode string) func() {
	t.Helper()
	origServerURL := globalServerURL
	origAPIKey := globalAPIKey
	origOutput := defaultOutputMode
	origNoColor := defaultNoColor

	globalServerURL = strings.TrimSpace(serverURL)
	globalAPIKey = strings.TrimSpace(apiKey)
	defaultOutputMode = outputMode
	defaultNoColor = true

	return func() {
		globalServerURL = origServerURL
		globalAPIKey = origAPIKey
		defaultOutputMode = origOutput
		defaultNoColor = origNoColor
	}
}

func extractSessionIDFromStartOutput(t *testing.T, out string) uuid.UUID {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ID:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "ID:"))
		parsed, err := uuid.Parse(raw)
		if err != nil {
			t.Fatalf("parse created session id from %q: %v", line, err)
		}
		return parsed
	}
	t.Fatalf("session ID line not found in output: %q", out)
	return uuid.Nil
}

func jsonArrayCountAtPath(t *testing.T, raw string, key string) int {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal json output: %v\nraw=%s", err, raw)
	}
	items, ok := payload[key].([]any)
	if !ok {
		t.Fatalf("json key %q is %T, want []any", key, payload[key])
	}
	return len(items)
}
