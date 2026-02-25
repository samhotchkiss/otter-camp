package main

import (
	"context"
	"io"
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
