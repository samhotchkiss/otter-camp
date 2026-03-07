package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeChatExecutor struct {
	execFn     func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
}

func (f *fakeChatExecutor) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if f.execFn == nil {
		return pgconn.CommandTag{}, nil
	}
	return f.execFn(ctx, sql, arguments...)
}

func (f *fakeChatExecutor) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if f.queryFn == nil {
		return &fakeRows{}, nil
	}
	return f.queryFn(ctx, sql, args...)
}

func (f *fakeChatExecutor) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if f.queryRowFn == nil {
		return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
	}
	return f.queryRowFn(ctx, sql, args...)
}

type fakeRows struct {
	scanFns []func(dest ...any) error
	idx     int
	err     error
}

func (r *fakeRows) Close() {}

func (r *fakeRows) Err() error { return r.err }

func (r *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.scanFns) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.scanFns) {
		return errors.New("scan called out of sequence")
	}
	return r.scanFns[r.idx-1](dest...)
}

func (r *fakeRows) Values() ([]any, error) { return nil, nil }

func (r *fakeRows) RawValues() [][]byte { return nil }

func (r *fakeRows) Conn() *pgx.Conn { return nil }

func TestCanonicalLiveTurn(t *testing.T) {
	base := time.Date(2026, time.March, 7, 9, 0, 0, 0, time.UTC)
	startedOld := base.Add(-3 * time.Minute)
	startedNew := base.Add(-1 * time.Minute)
	startedTie := base.Add(-30 * time.Second)

	turn := func(status string, turnNumber int, createdAt time.Time, startedAt *time.Time) ChatTurn {
		return ChatTurn{
			ID:         uuid.New(),
			Status:     status,
			TurnNumber: turnNumber,
			CreatedAt:  createdAt,
			StartedAt:  startedAt,
		}
	}

	pendingOne := turn("pending", 1, base.Add(1*time.Minute), nil)
	pendingTwo := turn("pending", 2, base.Add(2*time.Minute), nil)
	pendingThree := turn("pending", 3, base.Add(3*time.Minute), nil)
	inProgressOne := turn("in_progress", 4, base.Add(4*time.Minute), &startedOld)
	inProgressTwo := turn("in_progress", 5, base.Add(5*time.Minute), &startedNew)
	inProgressThree := turn("in_progress", 5, base.Add(6*time.Minute), &startedTie)
	inProgressFour := turn("in_progress", 5, base.Add(7*time.Minute), &startedTie)

	tests := []struct {
		name             string
		turns            []ChatTurn
		wantCurrentID    uuid.UUID
		wantCurrentNil   bool
		wantDuplicateIDs []uuid.UUID
	}{
		{
			name:           "empty input returns nil",
			wantCurrentNil: true,
		},
		{
			name:          "single pending is canonical",
			turns:         []ChatTurn{pendingOne},
			wantCurrentID: pendingOne.ID,
		},
		{
			name:          "single in progress is canonical",
			turns:         []ChatTurn{inProgressOne},
			wantCurrentID: inProgressOne.ID,
		},
		{
			name:             "in progress wins over pending",
			turns:            []ChatTurn{pendingOne, inProgressTwo},
			wantCurrentID:    inProgressTwo.ID,
			wantDuplicateIDs: []uuid.UUID{pendingOne.ID},
		},
		{
			name:             "multiple pending keeps oldest and duplicates extras",
			turns:            []ChatTurn{pendingThree, pendingOne, pendingTwo},
			wantCurrentID:    pendingOne.ID,
			wantDuplicateIDs: []uuid.UUID{pendingTwo.ID, pendingThree.ID},
		},
		{
			name:             "multiple in progress keeps newest and duplicates losers",
			turns:            []ChatTurn{inProgressOne, inProgressTwo, inProgressThree, inProgressFour},
			wantCurrentID:    inProgressFour.ID,
			wantDuplicateIDs: []uuid.UUID{inProgressThree.ID, inProgressTwo.ID, inProgressOne.ID},
		},
		{
			name:             "mixed live turns keep in progress and cancel all pending extras",
			turns:            []ChatTurn{pendingTwo, inProgressOne, pendingOne},
			wantCurrentID:    inProgressOne.ID,
			wantDuplicateIDs: []uuid.UUID{pendingOne.ID, pendingTwo.ID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, duplicates := CanonicalLiveTurn(tt.turns)
			if tt.wantCurrentNil {
				if current != nil {
					t.Fatalf("current = %+v, want nil", *current)
				}
			} else {
				if current == nil {
					t.Fatal("current = nil, want live turn")
				}
				if current.ID != tt.wantCurrentID {
					t.Fatalf("current id = %s, want %s", current.ID, tt.wantCurrentID)
				}
			}

			gotDuplicateIDs := make([]uuid.UUID, 0, len(duplicates))
			for _, duplicate := range duplicates {
				gotDuplicateIDs = append(gotDuplicateIDs, duplicate.ID)
			}
			if len(gotDuplicateIDs) != len(tt.wantDuplicateIDs) {
				t.Fatalf("duplicate count = %d, want %d (%v)", len(gotDuplicateIDs), len(tt.wantDuplicateIDs), tt.wantDuplicateIDs)
			}
			for i := range gotDuplicateIDs {
				if gotDuplicateIDs[i] != tt.wantDuplicateIDs[i] {
					t.Fatalf("duplicate ids = %v, want %v", gotDuplicateIDs, tt.wantDuplicateIDs)
				}
			}
		})
	}
}

func TestChatMessageRepoUpdateContentFinalMessageReturnsError(t *testing.T) {
	messageID := uuid.New()
	content := "do not change"

	exec := &fakeChatExecutor{}
	exec.queryRowFn = func(_ context.Context, sql string, args ...any) pgx.Row {
		switch {
		case strings.Contains(sql, "SELECT status FROM chat_message"):
			return fakeRow{scanFn: func(dest ...any) error {
				*dest[0].(*string) = "final"
				return nil
			}}
		case strings.Contains(sql, "UPDATE chat_message"):
			newContent := args[1].(string)
			content = newContent
			return fakeRow{scanFn: func(dest ...any) error { return nil }}
		default:
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}
	}

	repo := &ChatMessageRepo{db: exec}
	_, err := repo.UpdateContent(context.Background(), messageID, "new content")
	if !errors.Is(err, ErrMessageContentImmutable) {
		t.Fatalf("UpdateContent error = %v, want ErrMessageContentImmutable", err)
	}
	if content != "do not change" {
		t.Fatalf("content changed to %q, want unchanged", content)
	}
}

func TestChatMessageRepoRedactSetsRedactionFields(t *testing.T) {
	now := time.Now().UTC()
	sessionID := uuid.New()
	messageID := uuid.New()

	exec := &fakeChatExecutor{}
	exec.queryRowFn = func(_ context.Context, sql string, _ ...any) pgx.Row {
		if !strings.Contains(sql, "UPDATE chat_message") {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}
		return fakeRow{scanFn: func(dest ...any) error {
			*dest[0].(*uuid.UUID) = messageID
			*dest[1].(*uuid.UUID) = sessionID
			*dest[2].(**uuid.UUID) = nil
			*dest[3].(*int64) = 7
			*dest[4].(**string) = nil
			*dest[5].(**uuid.UUID) = nil
			*dest[6].(*string) = "assistant"
			*dest[7].(*string) = ""
			*dest[8].(*string) = "text"
			*dest[9].(*string) = "redacted"
			*dest[10].(*bool) = true
			*dest[11].(**time.Time) = &now
			*dest[12].(**string) = nil
			*dest[13].(**string) = nil
			*dest[14].(*json.RawMessage) = json.RawMessage(`{"reason":"policy"}`)
			*dest[15].(*time.Time) = now
			*dest[16].(*time.Time) = now
			return nil
		}}
	}

	repo := &ChatMessageRepo{db: exec}
	updated, err := repo.Redact(context.Background(), messageID)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if updated.Content != "" {
		t.Fatalf("content = %q, want empty string", updated.Content)
	}
	if !updated.IsRedacted {
		t.Fatal("is_redacted = false, want true")
	}
	if updated.Status != "redacted" {
		t.Fatalf("status = %q, want redacted", updated.Status)
	}
	if updated.RedactedAt == nil {
		t.Fatal("redacted_at is nil")
	}
}

func TestChatMessageRepoGetBySequenceReturnsRequestedMessage(t *testing.T) {
	sessionID := uuid.New()
	messages := []ChatMessage{
		{ID: uuid.New(), SessionID: sessionID, SequenceNumber: 1, Content: "one", Status: "final", Role: "assistant", ContentFormat: "text"},
		{ID: uuid.New(), SessionID: sessionID, SequenceNumber: 2, Content: "two", Status: "final", Role: "assistant", ContentFormat: "text"},
		{ID: uuid.New(), SessionID: sessionID, SequenceNumber: 3, Content: "three", Status: "final", Role: "assistant", ContentFormat: "text"},
	}

	exec := &fakeChatExecutor{}
	exec.queryRowFn = func(_ context.Context, sql string, args ...any) pgx.Row {
		if !strings.Contains(sql, "FROM chat_message") {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}
		requestedSeq := args[1].(int64)
		for _, m := range messages {
			if m.SequenceNumber == requestedSeq {
				msg := m
				now := time.Now().UTC()
				return fakeRow{scanFn: func(dest ...any) error {
					*dest[0].(*uuid.UUID) = msg.ID
					*dest[1].(*uuid.UUID) = msg.SessionID
					*dest[2].(**uuid.UUID) = nil
					*dest[3].(*int64) = msg.SequenceNumber
					*dest[4].(**string) = nil
					*dest[5].(**uuid.UUID) = nil
					*dest[6].(*string) = msg.Role
					*dest[7].(*string) = msg.Content
					*dest[8].(*string) = msg.ContentFormat
					*dest[9].(*string) = msg.Status
					*dest[10].(*bool) = false
					*dest[11].(**time.Time) = nil
					*dest[12].(**string) = nil
					*dest[13].(**string) = nil
					*dest[14].(*json.RawMessage) = json.RawMessage(`{}`)
					*dest[15].(*time.Time) = now
					*dest[16].(*time.Time) = now
					return nil
				}}
			}
		}
		return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
	}

	repo := &ChatMessageRepo{db: exec}
	found, err := repo.GetBySequence(context.Background(), sessionID, 2)
	if err != nil {
		t.Fatalf("GetBySequence: %v", err)
	}
	if found.ID != messages[1].ID {
		t.Fatalf("id = %s, want %s", found.ID, messages[1].ID)
	}
	if found.Content != "two" {
		t.Fatalf("content = %q, want two", found.Content)
	}
}

func TestMemorySourceRepoListBySessionFiltersBySessionID(t *testing.T) {
	sessionA := uuid.New()
	sessionB := uuid.New()
	rowsData := []MemorySource{
		{ID: uuid.New(), MemoryID: uuid.New(), SourceType: "chat_message", SessionID: &sessionA},
		{ID: uuid.New(), MemoryID: uuid.New(), SourceType: "event", SessionID: &sessionA},
		{ID: uuid.New(), MemoryID: uuid.New(), SourceType: "file", SessionID: &sessionB},
	}

	exec := &fakeChatExecutor{}
	exec.queryFn = func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
		if !strings.Contains(sql, "FROM memory_source") {
			return &fakeRows{}, nil
		}
		sessionID := args[0].(uuid.UUID)
		scanFns := make([]func(dest ...any) error, 0)
		for _, item := range rowsData {
			if item.SessionID == nil || *item.SessionID != sessionID {
				continue
			}
			current := item
			now := time.Now().UTC()
			scanFns = append(scanFns, func(dest ...any) error {
				*dest[0].(*uuid.UUID) = current.ID
				*dest[1].(*uuid.UUID) = current.MemoryID
				*dest[2].(*string) = current.SourceType
				*dest[3].(**uuid.UUID) = nil
				*dest[4].(**uuid.UUID) = nil
				*dest[5].(**uuid.UUID) = current.SessionID
				*dest[6].(*time.Time) = now
				return nil
			})
		}
		return &fakeRows{scanFns: scanFns}, nil
	}

	repo := &MemorySourceRepo{db: exec}
	items, err := repo.ListBySession(context.Background(), sessionA)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("ListBySession count = %d, want 2", len(items))
	}
	for _, item := range items {
		if item.SessionID == nil || *item.SessionID != sessionA {
			t.Fatalf("unexpected session_id in result: %+v", item)
		}
	}
}

func TestChatSessionRepoDeleteProjectScopedRemovesProjectAndTaskScopedSessions(t *testing.T) {
	projectID := uuid.New()

	var (
		capturedSQL  string
		capturedArgs []any
	)

	repo := &ChatSessionRepo{
		db: &fakeChatExecutor{
			execFn: func(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
				capturedSQL = sql
				capturedArgs = arguments
				return pgconn.CommandTag{}, nil
			},
		},
	}

	if err := repo.DeleteProjectScoped(context.Background(), projectID); err != nil {
		t.Fatalf("DeleteProjectScoped: %v", err)
	}

	if !strings.Contains(capturedSQL, "DELETE FROM chat_session") {
		t.Fatalf("sql = %q, want DELETE FROM chat_session", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "scope_type = 'project'") {
		t.Fatalf("sql = %q, want project scope clause", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "scope_type = 'project_task'") {
		t.Fatalf("sql = %q, want project_task scope clause", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "FROM project_task") {
		t.Fatalf("sql = %q, want project_task subquery", capturedSQL)
	}
	if len(capturedArgs) != 1 {
		t.Fatalf("args len = %d, want 1", len(capturedArgs))
	}
	if arg, ok := capturedArgs[0].(uuid.UUID); !ok || arg != projectID {
		t.Fatalf("project id arg = %#v, want %s", capturedArgs[0], projectID)
	}
}

func TestChatSessionRepoCloseProjectScopedClosesProjectAndTaskScopedSessions(t *testing.T) {
	projectID := uuid.New()

	var (
		capturedSQL  string
		capturedArgs []any
	)

	repo := &ChatSessionRepo{
		db: &fakeChatExecutor{
			execFn: func(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
				capturedSQL = sql
				capturedArgs = arguments
				return pgconn.CommandTag{}, nil
			},
		},
	}

	if err := repo.CloseProjectScoped(context.Background(), projectID); err != nil {
		t.Fatalf("CloseProjectScoped: %v", err)
	}

	if !strings.Contains(capturedSQL, "UPDATE chat_session") {
		t.Fatalf("sql = %q, want UPDATE chat_session", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "SET status = 'closed'") {
		t.Fatalf("sql = %q, want closed status update", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "scope_type = 'project'") {
		t.Fatalf("sql = %q, want project scope clause", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "scope_type = 'project_task'") {
		t.Fatalf("sql = %q, want project_task scope clause", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "FROM project_task") {
		t.Fatalf("sql = %q, want project_task subquery", capturedSQL)
	}
	if len(capturedArgs) != 1 {
		t.Fatalf("args len = %d, want 1", len(capturedArgs))
	}
	if arg, ok := capturedArgs[0].(uuid.UUID); !ok || arg != projectID {
		t.Fatalf("project id arg = %#v, want %s", capturedArgs[0], projectID)
	}
}
