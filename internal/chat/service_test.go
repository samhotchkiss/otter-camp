package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestCreateSessionSyncUniquenessReturnsErrActiveSyncSessionExists(t *testing.T) {
	orgID := uuid.New()
	scopeID := uuid.New()
	sessions := &fakeSessionRepo{
		getByScopeAndModeFn: func(_ context.Context, scopeType string, gotScopeID uuid.UUID, mode string) (*repo.ChatSession, error) {
			if scopeType != "project" || gotScopeID != scopeID || mode != "sync" {
				t.Fatalf("unexpected GetByScopeAndMode args: %s %s %s", scopeType, gotScopeID, mode)
			}
			return &repo.ChatSession{ID: uuid.New(), OrganizationID: orgID, Status: "active", Mode: "sync"}, nil
		},
	}

	svc := newUnitService(t, unitDeps{sessions: sessions})
	_, err := svc.CreateSession(context.Background(), CreateSessionInput{
		OrganizationID: orgID,
		ScopeType:      "project",
		ScopeID:        scopeID,
		Mode:           "sync",
	})
	if !errors.Is(err, ErrActiveSyncSessionExists) {
		t.Fatalf("CreateSession error = %v, want ErrActiveSyncSessionExists", err)
	}
}

func TestCreateSessionPublishesSessionCreatedEvent(t *testing.T) {
	orgID := uuid.New()
	scopeID := uuid.New()
	bus := &fakeEventBus{}

	svc := newUnitService(t, unitDeps{
		events: bus,
		sessions: &fakeSessionRepo{
			createFn: func(_ context.Context, session repo.ChatSession) (repo.ChatSession, error) {
				session.ID = uuid.New()
				return session, nil
			},
		},
	})

	created, err := svc.CreateSession(context.Background(), CreateSessionInput{
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        scopeID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(bus.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(bus.events))
	}
	event := bus.events[0]
	if event.EventType != "chat.session.created" {
		t.Fatalf("event type = %s, want chat.session.created", event.EventType)
	}

	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["session_id"] != created.ID.String() {
		t.Fatalf("payload session_id = %v, want %s", payload["session_id"], created.ID)
	}
}

func TestSwitchModeAsyncToSyncBlockedWhenTurnInProgress(t *testing.T) {
	sessionID := uuid.New()
	orgID := uuid.New()

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Mode: "async", Status: "active"}, nil
			},
		},
		turns: &fakeTurnRepo{
			listBySessionFn: func(_ context.Context, id uuid.UUID) ([]repo.ChatTurn, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return []repo.ChatTurn{{ID: uuid.New(), SessionID: sessionID, Status: "in_progress", TurnNumber: 4}}, nil
			},
		},
	})

	err := svc.SwitchMode(context.Background(), sessionID, "sync")
	if !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("SwitchMode error = %v, want ErrTurnInProgress", err)
	}
}

func TestUpdateMessageStatusInvalidTransitionFinalToStreaming(t *testing.T) {
	messageID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID}, nil
			},
		},
		messages: &fakeMessageRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				if id != messageID {
					t.Fatalf("unexpected message id %s", id)
				}
				return repo.ChatMessage{ID: messageID, SessionID: sessionID, Status: "final"}, nil
			},
		},
	})

	err := svc.UpdateMessageStatus(context.Background(), messageID, "streaming", "")
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("UpdateMessageStatus error = %v, want ErrInvalidStatusTransition", err)
	}
}

func TestUpdateMessageStatusFailedPersistsErrorMessage(t *testing.T) {
	messageID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()
	const failureMessage = "model timeout"

	var gotStatus string
	var gotErrorMessage string
	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID}, nil
			},
		},
		messages: &fakeMessageRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				if id != messageID {
					t.Fatalf("unexpected message id %s", id)
				}
				return repo.ChatMessage{ID: messageID, SessionID: sessionID, Status: "streaming"}, nil
			},
			updateStatusFn: func(_ context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error) {
				if id != messageID {
					t.Fatalf("unexpected message id for UpdateStatus: %s", id)
				}
				gotStatus = status
				gotErrorMessage = errorMessage
				return repo.ChatMessage{ID: id, SessionID: sessionID, Status: status}, nil
			},
		},
	})

	if err := svc.UpdateMessageStatus(context.Background(), messageID, "failed", failureMessage); err != nil {
		t.Fatalf("UpdateMessageStatus: %v", err)
	}
	if gotStatus != "failed" {
		t.Fatalf("update status = %s, want failed", gotStatus)
	}
	if gotErrorMessage != failureMessage {
		t.Fatalf("update error message = %q, want %q", gotErrorMessage, failureMessage)
	}
}

func TestRedactMessageAllowsSessionOwner(t *testing.T) {
	messageID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()
	ownerID := uuid.New()
	authorID := uuid.New()
	authorType := "human_user"
	redactCalls := 0

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{
					ID:             sessionID,
					OrganizationID: orgID,
					CreatedByType:  "human_user",
					CreatedByID:    ownerID,
				}, nil
			},
		},
		messages: &fakeMessageRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				if id != messageID {
					t.Fatalf("unexpected message id %s", id)
				}
				return repo.ChatMessage{
					ID:         messageID,
					SessionID:  sessionID,
					Status:     "final",
					AuthorType: &authorType,
					AuthorID:   &authorID,
				}, nil
			},
			redactFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				redactCalls++
				if id != messageID {
					t.Fatalf("unexpected redaction message id %s", id)
				}
				return repo.ChatMessage{ID: id, SessionID: sessionID, Status: "redacted", IsRedacted: true}, nil
			},
		},
	})

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{
		UserID:         ownerID,
		OrganizationID: orgID,
		Role:           "member",
	})
	if err := svc.RedactMessage(ctx, messageID); err != nil {
		t.Fatalf("RedactMessage error = %v, want nil", err)
	}
	if redactCalls != 1 {
		t.Fatalf("redact calls = %d, want 1", redactCalls)
	}
}

func TestRedactMessageRejectsNonOwnerNonAuthor(t *testing.T) {
	messageID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()
	ownerID := uuid.New()
	authorID := uuid.New()
	callerID := uuid.New()
	authorType := "human_user"
	redactCalls := 0

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{
					ID:             sessionID,
					OrganizationID: orgID,
					CreatedByType:  "human_user",
					CreatedByID:    ownerID,
				}, nil
			},
		},
		messages: &fakeMessageRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				if id != messageID {
					t.Fatalf("unexpected message id %s", id)
				}
				return repo.ChatMessage{
					ID:         messageID,
					SessionID:  sessionID,
					Status:     "final",
					AuthorType: &authorType,
					AuthorID:   &authorID,
				}, nil
			},
			redactFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				redactCalls++
				return repo.ChatMessage{ID: id}, nil
			},
		},
	})

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{
		UserID:         callerID,
		OrganizationID: orgID,
		Role:           "member",
	})
	err := svc.RedactMessage(ctx, messageID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("RedactMessage error = %v, want ErrForbidden", err)
	}
	if redactCalls != 0 {
		t.Fatalf("redact calls = %d, want 0", redactCalls)
	}
}

func TestCancelCurrentTurnWithoutActiveTurnReturnsErrNoActiveTurn(t *testing.T) {
	sessionID := uuid.New()
	orgID := uuid.New()

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Status: "active"}, nil
			},
		},
		turns: &fakeTurnRepo{
			listBySessionFn: func(_ context.Context, id uuid.UUID) ([]repo.ChatTurn, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return []repo.ChatTurn{{ID: uuid.New(), SessionID: sessionID, Status: "pending", TurnNumber: 1}}, nil
			},
		},
	})

	err := svc.CancelCurrentTurn(context.Background(), sessionID)
	if !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("CancelCurrentTurn error = %v, want ErrNoActiveTurn", err)
	}
}

func TestCancelCurrentTurnPublishesTurnCancelledEvent(t *testing.T) {
	sessionID := uuid.New()
	turnID := uuid.New()
	orgID := uuid.New()
	bus := &fakeEventBus{}

	svc := newUnitService(t, unitDeps{
		events: bus,
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Status: "active"}, nil
			},
			updateCurrentTurnFn: func(_ context.Context, id uuid.UUID, _ *uuid.UUID) (repo.ChatSession, error) {
				return repo.ChatSession{ID: id, OrganizationID: orgID, Status: "active"}, nil
			},
		},
		turns: &fakeTurnRepo{
			listBySessionFn: func(_ context.Context, id uuid.UUID) ([]repo.ChatTurn, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return []repo.ChatTurn{{ID: turnID, SessionID: sessionID, Status: "in_progress", TurnNumber: 1}}, nil
			},
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatTurn, error) {
				if id != turnID {
					t.Fatalf("unexpected turn id %s", id)
				}
				return repo.ChatTurn{ID: turnID, SessionID: sessionID, Status: "in_progress", TurnNumber: 1}, nil
			},
		},
	})

	if err := svc.CancelCurrentTurn(context.Background(), sessionID); err != nil {
		t.Fatalf("CancelCurrentTurn: %v", err)
	}

	found := false
	for _, event := range bus.events {
		if event.EventType != "chat.turn.cancelled" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["turn_id"] != turnID.String() {
			t.Fatalf("payload turn_id = %v, want %s", payload["turn_id"], turnID)
		}
		found = true
	}
	if !found {
		t.Fatal("chat.turn.cancelled event was not published")
	}
}

func TestCancelTurnSetsCompletedAt(t *testing.T) {
	sessionID := uuid.New()
	turnID := uuid.New()
	orgID := uuid.New()

	var gotCancelRequestedAt time.Time
	var gotCompletedAt time.Time
	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Status: "active"}, nil
			},
			updateCurrentTurnFn: func(_ context.Context, id uuid.UUID, _ *uuid.UUID) (repo.ChatSession, error) {
				return repo.ChatSession{ID: id, OrganizationID: orgID, Status: "active"}, nil
			},
		},
		turns: &fakeTurnRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatTurn, error) {
				if id != turnID {
					t.Fatalf("unexpected turn id %s", id)
				}
				return repo.ChatTurn{ID: turnID, SessionID: sessionID, Status: "in_progress", TurnNumber: 1}, nil
			},
			setCancelledFn: func(_ context.Context, id uuid.UUID, cancelRequestedAt time.Time, completedAt time.Time) (repo.ChatTurn, error) {
				if id != turnID {
					t.Fatalf("unexpected turn id for SetCancelled: %s", id)
				}
				gotCancelRequestedAt = cancelRequestedAt
				gotCompletedAt = completedAt
				return repo.ChatTurn{
					ID:                id,
					SessionID:         sessionID,
					Status:            "cancelled",
					CancelRequestedAt: &cancelRequestedAt,
					CompletedAt:       &completedAt,
				}, nil
			},
		},
	})

	if err := svc.CancelTurn(context.Background(), turnID, "unit-test"); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	if gotCancelRequestedAt.IsZero() {
		t.Fatal("cancel_requested_at was not set")
	}
	if gotCompletedAt.IsZero() {
		t.Fatal("completed_at was not set")
	}
	if !gotCompletedAt.Equal(gotCancelRequestedAt) {
		t.Fatalf("completed_at %s, want equal to cancel_requested_at %s", gotCompletedAt, gotCancelRequestedAt)
	}
}

func TestEditQueuedMessageFinalMessageReturnsErrMessageNotEditable(t *testing.T) {
	messageID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID}, nil
			},
		},
		messages: &fakeMessageRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				if id != messageID {
					t.Fatalf("unexpected message id %s", id)
				}
				authorType := "human_user"
				authorID := uuid.New()
				return repo.ChatMessage{ID: messageID, SessionID: sessionID, Status: "final", AuthorType: &authorType, AuthorID: &authorID}, nil
			},
		},
	})

	err := svc.EditQueuedMessage(context.Background(), messageID, "new content")
	if !errors.Is(err, ErrMessageNotEditable) {
		t.Fatalf("EditQueuedMessage error = %v, want ErrMessageNotEditable", err)
	}
}

func TestCreateTurnAssignsNextTurnNumber(t *testing.T) {
	sessionID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()
	var createdTurn repo.ChatTurn

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Status: "active"}, nil
			},
			updateCurrentTurnFn: func(_ context.Context, id uuid.UUID, _ *uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Status: "active"}, nil
			},
			incrementCountsFn: func(_ context.Context, id uuid.UUID, turnDelta, messageDelta int) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				if turnDelta != 1 || messageDelta != 0 {
					t.Fatalf("unexpected deltas: turn=%d message=%d", turnDelta, messageDelta)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Status: "active"}, nil
			},
		},
		agents: &fakeAgentRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
				if id != agentID {
					t.Fatalf("unexpected agent id %s", id)
				}
				return repo.Agent{ID: agentID, OrganizationID: orgID}, nil
			},
		},
		turns: &fakeTurnRepo{
			listBySessionFn: func(_ context.Context, id uuid.UUID) ([]repo.ChatTurn, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return []repo.ChatTurn{{TurnNumber: 1}, {TurnNumber: 3}, {TurnNumber: 2}}, nil
			},
			createFn: func(_ context.Context, turn repo.ChatTurn) (repo.ChatTurn, error) {
				createdTurn = turn
				turn.ID = uuid.New()
				turn.CreatedAt = time.Now().UTC()
				return turn, nil
			},
		},
	})

	created, err := svc.CreateTurn(context.Background(), sessionID, agentID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if created.TurnNumber != 4 {
		t.Fatalf("CreateTurn turn_number = %d, want 4", created.TurnNumber)
	}
	if createdTurn.TurnNumber != 4 {
		t.Fatalf("turn passed to repo create turn_number = %d, want 4", createdTurn.TurnNumber)
	}
}

func TestAddReactionDedupMapsRepoConflictToErrDuplicateReaction(t *testing.T) {
	messageID := uuid.New()
	sessionID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()

	svc := newUnitService(t, unitDeps{
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{ID: sessionID, OrganizationID: orgID, Status: "active"}, nil
			},
		},
		messages: &fakeMessageRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatMessage, error) {
				if id != messageID {
					t.Fatalf("unexpected message id %s", id)
				}
				return repo.ChatMessage{ID: messageID, SessionID: sessionID, Status: "final"}, nil
			},
		},
		users: &fakeUserRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.HumanUser, error) {
				if id != userID {
					t.Fatalf("unexpected user id %s", id)
				}
				return repo.HumanUser{ID: userID, OrganizationID: orgID}, nil
			},
		},
		reactions: &fakeReactionRepo{
			createFn: func(_ context.Context, _ repo.ChatMessageReaction) (repo.ChatMessageReaction, error) {
				return repo.ChatMessageReaction{}, repo.ErrConflict
			},
		},
	})

	_, err := svc.AddReaction(context.Background(), messageID, "human_user", userID, "thumbs_up")
	if !errors.Is(err, ErrDuplicateReaction) {
		t.Fatalf("AddReaction error = %v, want ErrDuplicateReaction", err)
	}
}

func TestMapDBErrorForeignKeyIsNotDuplicateReaction(t *testing.T) {
	err := mapDBError(&pgconn.PgError{Code: "23503", Message: "foreign key violation"})
	if errors.Is(err, ErrDuplicateReaction) {
		t.Fatalf("mapDBError returned duplicate-reaction mapped error: %v", err)
	}
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("mapDBError = %v, want repo.ErrNotFound", err)
	}
}

func TestCloseSessionEnqueuesSummarizationAndCleanupJobs(t *testing.T) {
	sessionID := uuid.New()
	orgID := uuid.New()
	now := time.Date(2026, time.January, 15, 22, 30, 0, 0, time.UTC)
	enqueuer := &fakeEnqueuer{}

	svc := newUnitService(t, unitDeps{
		enqueuer: enqueuer,
		sessions: &fakeSessionRepo{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return repo.ChatSession{
					ID:             sessionID,
					OrganizationID: orgID,
					ScopeType:      "organization",
					ScopeID:        orgID,
					Mode:           "async",
					Status:         "active",
				}, nil
			},
			closeFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
				return repo.ChatSession{ID: id, OrganizationID: orgID, Status: "closed"}, nil
			},
		},
		turns: &fakeTurnRepo{listBySessionFn: func(context.Context, uuid.UUID) ([]repo.ChatTurn, error) {
			return []repo.ChatTurn{}, nil
		}},
	})
	svc.clock = staticClock{now: now}

	if err := svc.CloseSession(context.Background(), sessionID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	if len(enqueuer.calls) != 4 {
		t.Fatalf("enqueued jobs = %d, want 4", len(enqueuer.calls))
	}
	if enqueuer.calls[0].jobType != ChatSummarizeJobType {
		t.Fatalf("first job type = %s, want %s", enqueuer.calls[0].jobType, ChatSummarizeJobType)
	}

	cleanupRunAfter := nextCleanupRunAfter(now)
	seen := map[string]bool{}
	for _, call := range enqueuer.calls[1:] {
		if call.jobType != ChatSessionCleanupJobType {
			t.Fatalf("cleanup job type = %s, want %s", call.jobType, ChatSessionCleanupJobType)
		}
		payload, ok := call.payload.(ChatSessionCleanupPayload)
		if !ok {
			t.Fatalf("cleanup payload type = %T, want ChatSessionCleanupPayload", call.payload)
		}
		seen[payload.CleanupType] = true
		if call.runAfter == nil || !call.runAfter.Equal(cleanupRunAfter) {
			t.Fatalf("cleanup run_after = %v, want %s", call.runAfter, cleanupRunAfter)
		}
	}

	for _, cleanupType := range []string{CleanupTypeEphemeralPurge, CleanupTypeToolCompaction, CleanupTypeSummaryConsolidate} {
		if !seen[cleanupType] {
			t.Fatalf("missing cleanup job for %s", cleanupType)
		}
	}
}

type unitDeps struct {
	sessions     *fakeSessionRepo
	participants *fakeParticipantRepo
	messages     *fakeMessageRepo
	turns        *fakeTurnRepo
	reactions    *fakeReactionRepo
	tasks        *fakeTaskRepo
	users        *fakeUserRepo
	agents       *fakeAgentRepo
	events       *fakeEventBus
	enqueuer     *fakeEnqueuer
}

func newUnitService(t *testing.T, deps unitDeps) *service {
	t.Helper()
	if deps.sessions == nil {
		deps.sessions = &fakeSessionRepo{}
	}
	if deps.participants == nil {
		deps.participants = &fakeParticipantRepo{}
	}
	if deps.messages == nil {
		deps.messages = &fakeMessageRepo{}
	}
	if deps.turns == nil {
		deps.turns = &fakeTurnRepo{}
	}
	if deps.reactions == nil {
		deps.reactions = &fakeReactionRepo{}
	}
	if deps.tasks == nil {
		deps.tasks = &fakeTaskRepo{}
	}
	if deps.users == nil {
		deps.users = &fakeUserRepo{}
	}
	if deps.agents == nil {
		deps.agents = &fakeAgentRepo{}
	}
	if deps.events == nil {
		deps.events = &fakeEventBus{}
	}
	if deps.enqueuer == nil {
		deps.enqueuer = &fakeEnqueuer{}
	}

	svc, err := NewService(Options{
		Pool:         new(pgxpool.Pool),
		Sessions:     deps.sessions,
		Participants: deps.participants,
		Messages:     deps.messages,
		Turns:        deps.turns,
		Reactions:    deps.reactions,
		Tasks:        deps.tasks,
		Users:        deps.users,
		Agents:       deps.agents,
		Events:       deps.events,
		Enqueuer:     deps.enqueuer,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc.(*service)
}

type fakeSessionRepo struct {
	createFn            func(ctx context.Context, session repo.ChatSession) (repo.ChatSession, error)
	getByIDFn           func(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
	getByScopeAndModeFn func(ctx context.Context, scopeType string, scopeID uuid.UUID, mode string) (*repo.ChatSession, error)
	listByOrgFn         func(ctx context.Context, organizationID uuid.UUID) ([]repo.ChatSession, error)
	updateModeFn        func(ctx context.Context, id uuid.UUID, mode string) (repo.ChatSession, error)
	closeFn             func(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
	updateCurrentTurnFn func(ctx context.Context, id uuid.UUID, currentTurnID *uuid.UUID) (repo.ChatSession, error)
	incrementCountsFn   func(ctx context.Context, id uuid.UUID, turnDelta, messageDelta int) (repo.ChatSession, error)
}

func (f *fakeSessionRepo) Create(ctx context.Context, session repo.ChatSession) (repo.ChatSession, error) {
	if f.createFn != nil {
		return f.createFn(ctx, session)
	}
	session.ID = uuid.New()
	return session, nil
}

func (f *fakeSessionRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.ChatSession, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.ChatSession{}, repo.ErrNotFound
}

func (f *fakeSessionRepo) GetByScopeAndMode(ctx context.Context, scopeType string, scopeID uuid.UUID, mode string) (*repo.ChatSession, error) {
	if f.getByScopeAndModeFn != nil {
		return f.getByScopeAndModeFn(ctx, scopeType, scopeID, mode)
	}
	return nil, nil
}

func (f *fakeSessionRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]repo.ChatSession, error) {
	if f.listByOrgFn != nil {
		return f.listByOrgFn(ctx, organizationID)
	}
	return []repo.ChatSession{}, nil
}

func (f *fakeSessionRepo) UpdateMode(ctx context.Context, id uuid.UUID, mode string) (repo.ChatSession, error) {
	if f.updateModeFn != nil {
		return f.updateModeFn(ctx, id, mode)
	}
	return repo.ChatSession{ID: id, Mode: mode}, nil
}

func (f *fakeSessionRepo) Close(ctx context.Context, id uuid.UUID) (repo.ChatSession, error) {
	if f.closeFn != nil {
		return f.closeFn(ctx, id)
	}
	now := time.Now().UTC()
	return repo.ChatSession{ID: id, Status: "closed", ClosedAt: &now}, nil
}

func (f *fakeSessionRepo) UpdateCurrentTurn(ctx context.Context, id uuid.UUID, currentTurnID *uuid.UUID) (repo.ChatSession, error) {
	if f.updateCurrentTurnFn != nil {
		return f.updateCurrentTurnFn(ctx, id, currentTurnID)
	}
	return repo.ChatSession{ID: id, CurrentTurnID: currentTurnID}, nil
}

func (f *fakeSessionRepo) IncrementCounts(ctx context.Context, id uuid.UUID, turnDelta, messageDelta int) (repo.ChatSession, error) {
	if f.incrementCountsFn != nil {
		return f.incrementCountsFn(ctx, id, turnDelta, messageDelta)
	}
	return repo.ChatSession{ID: id}, nil
}

type fakeParticipantRepo struct {
	createFn                       func(ctx context.Context, participant repo.ChatParticipant) (repo.ChatParticipant, error)
	listBySessionFn                func(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatParticipant, error)
	updateNotificationPreferenceFn func(ctx context.Context, id uuid.UUID, preference string) (repo.ChatParticipant, error)
	removeFn                       func(ctx context.Context, id uuid.UUID) (repo.ChatParticipant, error)
}

func (f *fakeParticipantRepo) Create(ctx context.Context, participant repo.ChatParticipant) (repo.ChatParticipant, error) {
	if f.createFn != nil {
		return f.createFn(ctx, participant)
	}
	participant.ID = uuid.New()
	return participant, nil
}

func (f *fakeParticipantRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatParticipant, error) {
	if f.listBySessionFn != nil {
		return f.listBySessionFn(ctx, sessionID)
	}
	return []repo.ChatParticipant{}, nil
}

func (f *fakeParticipantRepo) UpdateNotificationPreference(ctx context.Context, id uuid.UUID, preference string) (repo.ChatParticipant, error) {
	if f.updateNotificationPreferenceFn != nil {
		return f.updateNotificationPreferenceFn(ctx, id, preference)
	}
	return repo.ChatParticipant{ID: id, NotificationPreference: preference}, nil
}

func (f *fakeParticipantRepo) Remove(ctx context.Context, id uuid.UUID) (repo.ChatParticipant, error) {
	if f.removeFn != nil {
		return f.removeFn(ctx, id)
	}
	now := time.Now().UTC()
	return repo.ChatParticipant{ID: id, RemovedAt: &now}, nil
}

type fakeMessageRepo struct {
	getByIDFn       func(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error)
	listBySessionFn func(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error)
	updateStatusFn  func(ctx context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error)
	updateContentFn func(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error)
	redactFn        func(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error)
}

func (f *fakeMessageRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.ChatMessage{}, repo.ErrNotFound
}

func (f *fakeMessageRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error) {
	if f.listBySessionFn != nil {
		return f.listBySessionFn(ctx, sessionID)
	}
	return []repo.ChatMessage{}, nil
}

func (f *fakeMessageRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error) {
	if f.updateStatusFn != nil {
		return f.updateStatusFn(ctx, id, status, errorMessage)
	}
	errCopy := errorMessage
	return repo.ChatMessage{ID: id, Status: status, ErrorMessage: &errCopy}, nil
}

func (f *fakeMessageRepo) UpdateContent(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error) {
	if f.updateContentFn != nil {
		return f.updateContentFn(ctx, id, content)
	}
	return repo.ChatMessage{ID: id, Content: content}, nil
}

func (f *fakeMessageRepo) Redact(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error) {
	if f.redactFn != nil {
		return f.redactFn(ctx, id)
	}
	return repo.ChatMessage{ID: id, Status: "redacted", IsRedacted: true}, nil
}

type fakeTurnRepo struct {
	createFn        func(ctx context.Context, turn repo.ChatTurn) (repo.ChatTurn, error)
	getByIDFn       func(ctx context.Context, id uuid.UUID) (repo.ChatTurn, error)
	listBySessionFn func(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatTurn, error)
	setStartedFn    func(ctx context.Context, id uuid.UUID, startedAt time.Time) (repo.ChatTurn, error)
	setCompletedFn  func(ctx context.Context, id uuid.UUID, completedAt time.Time, durationMS int) (repo.ChatTurn, error)
	setCancelledFn  func(ctx context.Context, id uuid.UUID, cancelRequestedAt time.Time, completedAt time.Time) (repo.ChatTurn, error)
	setFailedFn     func(ctx context.Context, id uuid.UUID, errorMessage string, completedAt time.Time) (repo.ChatTurn, error)
}

func (f *fakeTurnRepo) Create(ctx context.Context, turn repo.ChatTurn) (repo.ChatTurn, error) {
	if f.createFn != nil {
		return f.createFn(ctx, turn)
	}
	turn.ID = uuid.New()
	return turn, nil
}

func (f *fakeTurnRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.ChatTurn, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.ChatTurn{}, repo.ErrNotFound
}

func (f *fakeTurnRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatTurn, error) {
	if f.listBySessionFn != nil {
		return f.listBySessionFn(ctx, sessionID)
	}
	return []repo.ChatTurn{}, nil
}

func (f *fakeTurnRepo) SetStarted(ctx context.Context, id uuid.UUID, startedAt time.Time) (repo.ChatTurn, error) {
	if f.setStartedFn != nil {
		return f.setStartedFn(ctx, id, startedAt)
	}
	return repo.ChatTurn{ID: id, Status: "in_progress", StartedAt: &startedAt}, nil
}

func (f *fakeTurnRepo) SetCompleted(ctx context.Context, id uuid.UUID, completedAt time.Time, durationMS int) (repo.ChatTurn, error) {
	if f.setCompletedFn != nil {
		return f.setCompletedFn(ctx, id, completedAt, durationMS)
	}
	return repo.ChatTurn{ID: id, Status: "completed", CompletedAt: &completedAt, DurationMS: &durationMS}, nil
}

func (f *fakeTurnRepo) SetCancelled(ctx context.Context, id uuid.UUID, cancelRequestedAt time.Time, completedAt time.Time) (repo.ChatTurn, error) {
	if f.setCancelledFn != nil {
		return f.setCancelledFn(ctx, id, cancelRequestedAt, completedAt)
	}
	return repo.ChatTurn{ID: id, Status: "cancelled", CancelRequestedAt: &cancelRequestedAt, CompletedAt: &completedAt}, nil
}

func (f *fakeTurnRepo) SetFailed(ctx context.Context, id uuid.UUID, errorMessage string, completedAt time.Time) (repo.ChatTurn, error) {
	if f.setFailedFn != nil {
		return f.setFailedFn(ctx, id, errorMessage, completedAt)
	}
	errCopy := errorMessage
	return repo.ChatTurn{ID: id, Status: "failed", ErrorMessage: &errCopy, CompletedAt: &completedAt}, nil
}

type fakeReactionRepo struct {
	createFn        func(ctx context.Context, reaction repo.ChatMessageReaction) (repo.ChatMessageReaction, error)
	getByIDFn       func(ctx context.Context, id uuid.UUID) (repo.ChatMessageReaction, error)
	listByMessageFn func(ctx context.Context, messageID uuid.UUID) ([]repo.ChatMessageReaction, error)
	deleteByIDFn    func(ctx context.Context, id uuid.UUID) error
}

func (f *fakeReactionRepo) Create(ctx context.Context, reaction repo.ChatMessageReaction) (repo.ChatMessageReaction, error) {
	if f.createFn != nil {
		return f.createFn(ctx, reaction)
	}
	reaction.ID = uuid.New()
	return reaction, nil
}

func (f *fakeReactionRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessageReaction, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.ChatMessageReaction{}, repo.ErrNotFound
}

func (f *fakeReactionRepo) ListByMessage(ctx context.Context, messageID uuid.UUID) ([]repo.ChatMessageReaction, error) {
	if f.listByMessageFn != nil {
		return f.listByMessageFn(ctx, messageID)
	}
	return []repo.ChatMessageReaction{}, nil
}

func (f *fakeReactionRepo) DeleteByID(ctx context.Context, id uuid.UUID) error {
	if f.deleteByIDFn != nil {
		return f.deleteByIDFn(ctx, id)
	}
	return nil
}

type fakeTaskRepo struct {
	getByIDFn func(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

func (f *fakeTaskRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.ProjectTask{}, repo.ErrNotFound
}

type fakeUserRepo struct {
	getByIDFn func(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
}

func (f *fakeUserRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.HumanUser{}, repo.ErrNotFound
}

type fakeAgentRepo struct {
	getByIDFn func(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

func (f *fakeAgentRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.Agent{}, repo.ErrNotFound
}

type fakeEventBus struct {
	events []eventbus.DomainEvent
}

func (f *fakeEventBus) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	f.events = append(f.events, event)
	return nil
}

type fakeEnqueuer struct {
	calls []enqueuedJob
}

type enqueuedJob struct {
	jobType  string
	priority int
	payload  any
	runAfter *time.Time
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, _ pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error) {
	call := enqueuedJob{
		jobType:  jobType,
		priority: priority,
		payload:  payload,
	}
	if runAfter != nil {
		value := runAfter.UTC()
		call.runAfter = &value
	}
	f.calls = append(f.calls, call)
	return uuid.New(), nil
}

type staticClock struct {
	now time.Time
}

func (c staticClock) Now() time.Time { return c.now }

var _ json.Marshaler
