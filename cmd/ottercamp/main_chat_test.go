package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
)

func TestChatStartProjectScopeRequiresScopeID(t *testing.T) {
	restore := overrideChatTestGlobals(t, &fakeChatCommandClient{})
	defer restore()

	code, _, stderr := captureCommandOutput(t, func() int {
		return runChatStart([]string{"--scope-type=project"})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "scope-id is required when --scope-type=project") {
		t.Fatalf("stderr = %q, want scope-id validation error", stderr)
	}
}

func TestChatStartInteractiveRequiresSyncMode(t *testing.T) {
	restore := overrideChatTestGlobals(t, &fakeChatCommandClient{})
	defer restore()

	code, _, stderr := captureCommandOutput(t, func() int {
		return runChatStart([]string{"--mode=async", "--interactive"})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Interactive mode requires --mode=sync") {
		t.Fatalf("stderr = %q, want interactive guard error", stderr)
	}
}

func TestChatStartMapsActiveSyncConflictError(t *testing.T) {
	client := &fakeChatCommandClient{
		createSessionErr: &cliAPIError{
			Method:     "POST",
			Path:       "/v1/chat-sessions",
			StatusCode: 409,
			Code:       "active_sync_session_exists",
			Message:    "active sync session already exists",
		},
	}
	restore := overrideChatTestGlobals(t, client)
	defer restore()

	code, _, stderr := captureCommandOutput(t, func() int {
		return runChatStart([]string{
			"--scope-type=project",
			"--scope-id=" + uuid.NewString(),
		})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	want := "Error: An active sync session already exists for this scope."
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestChatHistoryRedactedMessagesAreDisplayedAsRedacted(t *testing.T) {
	sessionID := uuid.New()
	client := &fakeChatCommandClient{
		listMessagesResult: chatMessageListEnvelope{
			Data: []cliChatMessage{
				{
					ID:             uuid.New(),
					SessionID:      sessionID,
					SequenceNumber: 1,
					Role:           "assistant",
					Content:        "top secret",
					Status:         "redacted",
					IsRedacted:     true,
				},
			},
		},
	}
	restore := overrideChatTestGlobals(t, client)
	defer restore()

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runChatHistory([]string{sessionID.String()})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "[redacted]") {
		t.Fatalf("stdout = %q, want [redacted]", stdout)
	}
}

func TestChatListShowsRelativeLastActivity(t *testing.T) {
	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	sessionID := uuid.New()
	projectID := uuid.New()
	lastActivity := now.Add(-2 * time.Minute)

	client := &fakeChatCommandClient{
		listSessionsResult: chatSessionListEnvelope{
			Data: []cliChatSession{
				{
					ID:            sessionID,
					ScopeType:     "project",
					ScopeID:       projectID,
					Mode:          "sync",
					Status:        "active",
					MessageCount:  7,
					LastMessageAt: &lastActivity,
					CreatedAt:     now.Add(-time.Hour),
				},
			},
		},
	}
	restore := overrideChatTestGlobals(t, client)
	defer restore()

	originalNow := chatNow
	chatNow = func() time.Time { return now }
	defer func() { chatNow = originalNow }()

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runChatList([]string{})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "2 minutes ago") {
		t.Fatalf("stdout = %q, want relative time", stdout)
	}
}

func TestCLIAPIClientListChatMessagesRetriesTransientRateLimit(t *testing.T) {
	t.Parallel()

	sessionID := uuid.New()
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"too many requests"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"` + uuid.NewString() + `","session_id":"` + sessionID.String() + `","sequence_number":1,"role":"assistant","content":"ok","status":"finalized","is_redacted":false,"metadata":{},"created_at":"2026-03-11T00:00:00Z"}]}`))
	}))
	defer server.Close()

	client := &cliAPIClient{
		baseURL: server.URL,
		apiKey:  "test-key",
		client:  server.Client(),
	}

	result, err := client.ListChatMessages(context.Background(), sessionID, chatListMessagesFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListChatMessages: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("request attempts = %d, want 2", attempts)
	}
	if len(result.Data) != 1 || strings.TrimSpace(result.Data[0].Content) != "ok" {
		t.Fatalf("result = %+v, want one successful message payload", result.Data)
	}
}

func TestChatSendWaitJSONOutputShape(t *testing.T) {
	sessionID := uuid.New()
	sentMessageID := uuid.New()
	assistantMessageID := uuid.New()

	streamPayload := `event: chat.turn.completed
data: {"payload":{"session_id":"` + sessionID.String() + `","turn_id":"` + uuid.NewString() + `"}}

`
	client := &fakeChatCommandClient{
		sendResult: chatMessageEnvelope{
			Data: cliChatMessage{
				ID:             sentMessageID,
				SessionID:      sessionID,
				SequenceNumber: 1,
				Role:           "user",
				Content:        "hello",
				Status:         "pending",
			},
		},
		listMessagesResult: chatMessageListEnvelope{
			Data: []cliChatMessage{
				{
					ID:             assistantMessageID,
					SessionID:      sessionID,
					SequenceNumber: 2,
					Role:           "assistant",
					Content:        "hi there",
					Status:         "final",
				},
			},
		},
		streamReader: io.NopCloser(strings.NewReader(streamPayload)),
	}

	restore := overrideChatTestGlobals(t, client)
	defer restore()
	defaultOutputMode = clitools.OutputModeJSON

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runChatSend([]string{sessionID.String(), "hello", "--wait", "--timeout=2s"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json unmarshal stdout: %v stdout=%q", err, stdout)
	}

	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("payload data type = %T, want map[string]any", payload["data"])
	}
	if gotID, _ := data["id"].(string); gotID != sentMessageID.String() {
		t.Fatalf("data.id = %q, want %q", gotID, sentMessageID)
	}
	if _, exists := data["sent_message"]; exists {
		t.Fatalf("data.sent_message exists, expected flat data shape")
	}
	if _, exists := data["response_message"]; !exists {
		t.Fatal("data.response_message missing")
	}
}

func overrideChatTestGlobals(t *testing.T, client chatCommandClient) func() {
	t.Helper()

	originalFactory := newChatCommandClient
	originalOutput := defaultOutputMode
	originalNoColor := defaultNoColor

	newChatCommandClient = func() (chatCommandClient, error) { return client, nil }
	defaultOutputMode = clitools.OutputModeTable
	defaultNoColor = true

	return func() {
		newChatCommandClient = originalFactory
		defaultOutputMode = originalOutput
		defaultNoColor = originalNoColor
	}
}

type fakeChatCommandClient struct {
	createSessionResult chatSessionEnvelope
	createSessionErr    error

	sendResult chatMessageEnvelope
	sendErr    error

	listSessionsResult chatSessionListEnvelope
	listSessionsErr    error

	listMessagesResult chatMessageListEnvelope
	listMessagesErr    error

	listParticipantsResult []cliChatParticipant
	listParticipantsErr    error

	cancelErr error

	streamReader io.ReadCloser
	streamErr    error
}

func (f *fakeChatCommandClient) CreateChatSession(context.Context, chatCreateSessionRequest) (chatSessionEnvelope, error) {
	if f.createSessionErr != nil {
		return chatSessionEnvelope{}, f.createSessionErr
	}
	if f.createSessionResult.Data.ID == uuid.Nil {
		f.createSessionResult.Data.ID = uuid.New()
		f.createSessionResult.Data.ScopeType = "organization"
		f.createSessionResult.Data.ScopeID = uuid.New()
		f.createSessionResult.Data.Mode = "sync"
	}
	return f.createSessionResult, nil
}

func (f *fakeChatCommandClient) SendChatMessage(context.Context, uuid.UUID, string) (chatMessageEnvelope, error) {
	if f.sendErr != nil {
		return chatMessageEnvelope{}, f.sendErr
	}
	if f.sendResult.Data.ID == uuid.Nil {
		f.sendResult.Data.ID = uuid.New()
		f.sendResult.Data.SequenceNumber = 1
		f.sendResult.Data.Status = "pending"
	}
	return f.sendResult, nil
}

func (f *fakeChatCommandClient) ListChatSessions(context.Context, chatListSessionsFilter) (chatSessionListEnvelope, error) {
	return f.listSessionsResult, f.listSessionsErr
}

func (f *fakeChatCommandClient) ListChatMessages(context.Context, uuid.UUID, chatListMessagesFilter) (chatMessageListEnvelope, error) {
	return f.listMessagesResult, f.listMessagesErr
}

func (f *fakeChatCommandClient) ListChatParticipants(context.Context, uuid.UUID) ([]cliChatParticipant, error) {
	return f.listParticipantsResult, f.listParticipantsErr
}

func (f *fakeChatCommandClient) CancelChatTurn(context.Context, uuid.UUID) error {
	return f.cancelErr
}

func (f *fakeChatCommandClient) OpenEventsStream(context.Context, string) (io.ReadCloser, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	if f.streamReader != nil {
		return f.streamReader, nil
	}
	return io.NopCloser(strings.NewReader("")), nil
}
