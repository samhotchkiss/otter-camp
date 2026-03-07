//go:build integration

package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestSession_Create_OrgScope(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)

	created, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if created.OrganizationID != org.ID {
		t.Fatalf("organization_id = %s, want %s", created.OrganizationID, org.ID)
	}
	if created.ScopeType != "organization" {
		t.Fatalf("scope_type = %q, want organization", created.ScopeType)
	}
	if created.ScopeID != org.ID {
		t.Fatalf("scope_id = %s, want %s", created.ScopeID, org.ID)
	}

	got, err := svc.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetSession id = %s, want %s", got.ID, created.ID)
	}
}

func TestSession_Create_ProjectScope(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)

	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "chat-project-" + uuid.NewString()[:8],
		DisplayName:    "Chat Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	first, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession first: %v", err)
	}

	_, err = svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "sync",
	})
	if !errors.Is(err, ErrActiveSyncSessionExists) {
		t.Fatalf("CreateSession second error = %v, want ErrActiveSyncSessionExists", err)
	}

	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE scope_type = 'project'
		  AND scope_id = $1
		  AND mode = 'sync'
		  AND status = 'active'
	`, project.ID).Scan(&activeCount); err != nil {
		t.Fatalf("count active project sessions: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active sync sessions = %d, want 1", activeCount)
	}

	if err := svc.CloseSession(ctx, first.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	second, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession after close: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("new session id = %s, want different from %s", second.ID, first.ID)
	}
}

func TestSession_Create_ProjectScopeAsyncReusesCanonicalSession(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)

	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "chat-project-async-" + uuid.NewString()[:8],
		DisplayName:    "Chat Project Async",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	first, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession first async: %v", err)
	}
	second, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession second async: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("session ids = %s and %s, want canonical reuse", first.ID, second.ID)
	}

	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE scope_type = 'project'
		  AND scope_id = $1
		  AND mode = 'async'
		  AND status = 'active'
	`, project.ID).Scan(&activeCount); err != nil {
		t.Fatalf("count active async project sessions: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active async sessions = %d, want 1", activeCount)
	}
}

func TestSession_Create_TaskScope(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)

	execution := seedChatServiceFlowNodeExecution(t, ctx, pool, org.ID)
	session, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := svc.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ScopeType != "project_task" {
		t.Fatalf("scope_type = %q, want project_task", got.ScopeType)
	}
	if got.ScopeID != execution.TaskID {
		t.Fatalf("scope_id = %s, want %s", got.ScopeID, execution.TaskID)
	}
}

func TestSession_Create_TaskScopeAsyncClosesDuplicateBlankSessions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)
	sessionRepo := repo.NewChatSessionRepo(pool)

	execution := seedChatServiceFlowNodeExecution(t, ctx, pool, org.ID)
	first, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"chat-session-test","attempt":1}`),
	})
	if err != nil {
		t.Fatalf("create first duplicate blank task session: %v", err)
	}
	second, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"chat-session-test","attempt":2}`),
	})
	if err != nil {
		t.Fatalf("create second duplicate blank task session: %v", err)
	}

	reused, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession async task scope: %v", err)
	}
	if reused.ID != second.ID {
		t.Fatalf("reused session id = %s, want newest blank %s", reused.ID, second.ID)
	}

	firstStored, err := sessionRepo.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetByID first duplicate: %v", err)
	}
	if firstStored.Status != "closed" || firstStored.ClosedAt == nil {
		t.Fatalf("first duplicate session = %+v, want closed with closed_at", firstStored)
	}

	secondStored, err := sessionRepo.GetByID(ctx, second.ID)
	if err != nil {
		t.Fatalf("GetByID canonical blank: %v", err)
	}
	if secondStored.Status != "active" {
		t.Fatalf("canonical blank session status = %q, want active", secondStored.Status)
	}

	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE organization_id = $1
		  AND scope_type = 'project_task'
		  AND scope_id = $2
		  AND mode = 'async'
		  AND status = 'active'
	`, org.ID, execution.TaskID).Scan(&activeCount); err != nil {
		t.Fatalf("count active async task sessions: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active async task sessions = %d, want 1", activeCount)
	}
}

func TestSession_Create_TaskScopeAsyncReusesCanonicalNonBlankSessionEX294(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)
	sessionRepo := repo.NewChatSessionRepo(pool)

	execution := seedChatServiceFlowNodeExecution(t, ctx, pool, org.ID)
	canonical, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession canonical: %v", err)
	}
	if _, err := sessionRepo.IncrementCounts(ctx, canonical.ID, 0, 1); err != nil {
		t.Fatalf("IncrementCounts canonical: %v", err)
	}

	duplicate, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"chat-session-test","attempt":"duplicate-blank"}`),
	})
	if err != nil {
		t.Fatalf("create blank duplicate task session: %v", err)
	}

	reused, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession async task scope: %v", err)
	}
	if reused.ID != canonical.ID {
		t.Fatalf("reused session id = %s, want canonical nonblank %s", reused.ID, canonical.ID)
	}

	duplicateStored, err := sessionRepo.GetByID(ctx, duplicate.ID)
	if err != nil {
		t.Fatalf("GetByID blank duplicate: %v", err)
	}
	if duplicateStored.Status != "closed" || duplicateStored.ClosedAt == nil {
		t.Fatalf("blank duplicate session = %+v, want closed with closed_at", duplicateStored)
	}

	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE organization_id = $1
		  AND scope_type = 'project_task'
		  AND scope_id = $2
		  AND mode = 'async'
		  AND status = 'active'
	`, org.ID, execution.TaskID).Scan(&activeCount); err != nil {
		t.Fatalf("count active async task sessions: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active async task sessions = %d, want 1", activeCount)
	}
}

func TestSession_PerNodeAsync_AutoCreated(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	execution := seedChatServiceFlowNodeExecution(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session, err := svc.GetOrCreateNodeSession(ctx, execution.ID, agent.ID)
	if err != nil {
		t.Fatalf("GetOrCreateNodeSession: %v", err)
	}
	if session.ScopeType != "project_task" {
		t.Fatalf("scope_type = %q, want project_task", session.ScopeType)
	}
	if session.Mode != "async" {
		t.Fatalf("mode = %q, want async", session.Mode)
	}

	var linkedSessionID *uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT session_id
		FROM flow_node_execution
		WHERE id = $1
	`, execution.ID).Scan(&linkedSessionID); err != nil {
		t.Fatalf("query flow_node_execution.session_id: %v", err)
	}
	if linkedSessionID == nil || *linkedSessionID == uuid.Nil {
		t.Fatal("flow_node_execution.session_id is nil, want non-nil")
	}
}

func TestSession_ParticipantManagement(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "participant-human", "member")
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	if _, err := svc.AddParticipant(ctx, session.ID, "human_user", user.ID, "member"); err != nil {
		t.Fatalf("AddParticipant human: %v", err)
	}
	if _, err := svc.AddParticipant(ctx, session.ID, "human_user", user.ID, "member"); !errors.Is(err, ErrAlreadyParticipant) {
		t.Fatalf("duplicate AddParticipant error = %v, want ErrAlreadyParticipant", err)
	}
	if _, err := svc.AddParticipant(ctx, session.ID, "agent", agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant agent: %v", err)
	}

	participants, err := svc.ListParticipants(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(participants) != 2 {
		t.Fatalf("participant count = %d, want 2", len(participants))
	}
}

func TestSession_ModeSwitch(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	if err := svc.SwitchMode(ctx, session.ID, "async"); err != nil {
		t.Fatalf("SwitchMode to async: %v", err)
	}

	turn, err := svc.CreateTurn(ctx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := svc.StartTurn(ctx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	if err := svc.SwitchMode(ctx, session.ID, "sync"); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("SwitchMode async->sync error = %v, want ErrTurnInProgress", err)
	}

	if err := svc.CancelTurn(ctx, turn.ID, "mode-switch"); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	if err := svc.SwitchMode(ctx, session.ID, "sync"); err != nil {
		t.Fatalf("SwitchMode async->sync after cancel: %v", err)
	}

	got, err := svc.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Mode != "sync" {
		t.Fatalf("mode = %q, want sync", got.Mode)
	}
}

func TestSession_CompleteTurnPromotesQueuedRetryCurrentTurnEX305(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	turnRepo := repo.NewChatTurnRepo(pool)
	sessionRepo := repo.NewChatSessionRepo(pool)
	startedAt := time.Now().UTC().Add(-30 * time.Second)
	activeTurn, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
		StartedAt:      &startedAt,
	})
	if err != nil {
		t.Fatalf("create active turn: %v", err)
	}
	pendingRetry, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "pending",
	})
	if err != nil {
		t.Fatalf("create pending retry: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, session.ID, &activeTurn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}

	if err := svc.CompleteTurn(ctx, activeTurn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	refreshedSession, err := sessionRepo.GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID != pendingRetry.ID {
		t.Fatalf("current_turn_id = %v, want pending retry %s", refreshedSession.CurrentTurnID, pendingRetry.ID)
	}
	refreshedRetry, err := turnRepo.GetByID(ctx, pendingRetry.ID)
	if err != nil {
		t.Fatalf("GetByID retry turn: %v", err)
	}
	if refreshedRetry.Status != "pending" {
		t.Fatalf("retry turn status = %q, want pending", refreshedRetry.Status)
	}
}

func TestSession_CreateTurnBlockedWhileProjectPausedUntilResume(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	svc := newIntegrationService(t, pool, nil)
	projectRepo := repo.NewProjectRepo(pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "chat-pause-" + uuid.NewString()[:8],
		DisplayName:    "Chat Pause",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	firstTurn, err := svc.CreateTurn(ctx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn first: %v", err)
	}
	if err := svc.StartTurn(ctx, firstTurn.ID); err != nil {
		t.Fatalf("StartTurn first: %v", err)
	}

	project.Settings, err = projectpause.ApplyPause(project.Settings, "operator pause", json.RawMessage(`{"source":"test"}`), mustParseTime(t, "2026-03-05T18:00:00Z"), "human_user", uuid.New())
	if err != nil {
		t.Fatalf("ApplyPause: %v", err)
	}
	if _, err := projectRepo.Update(ctx, project); err != nil {
		t.Fatalf("update paused project: %v", err)
	}

	if err := svc.CompleteTurn(ctx, firstTurn.ID); err != nil {
		t.Fatalf("CompleteTurn first: %v", err)
	}
	if _, err := svc.CreateTurn(ctx, session.ID, agent.ID); !errors.Is(err, projectpause.ErrProjectPaused) {
		t.Fatalf("CreateTurn while paused err = %v, want ErrProjectPaused", err)
	}

	project.Settings, err = projectpause.ClearPause(project.Settings)
	if err != nil {
		t.Fatalf("ClearPause: %v", err)
	}
	if _, err := projectRepo.Update(ctx, project); err != nil {
		t.Fatalf("update resumed project: %v", err)
	}

	secondTurn, err := svc.CreateTurn(ctx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn after resume: %v", err)
	}
	if secondTurn.ID == uuid.Nil {
		t.Fatal("second turn id is nil")
	}
}

func TestSession_ReadCursor(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "read-cursor", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	authorType := "human_user"
	for i := 0; i < 5; i++ {
		if _, err := svc.AppendMessage(ctx, AppendMessageInput{
			SessionID:  session.ID,
			AuthorType: &authorType,
			AuthorID:   &user.ID,
			Role:       "user",
			Content:    "message",
		}); err != nil {
			t.Fatalf("AppendMessage %d: %v", i+1, err)
		}
	}

	cursorRepo := repo.NewChatReadCursorRepo(pool)
	if _, err := cursorRepo.Upsert(ctx, repo.ChatReadCursor{
		SessionID:        session.ID,
		UserID:           user.ID,
		LastReadSequence: 5,
	}); err != nil {
		t.Fatalf("Upsert first cursor: %v", err)
	}
	first, err := cursorRepo.GetBySessionAndUser(ctx, session.ID, user.ID)
	if err != nil {
		t.Fatalf("GetBySessionAndUser first: %v", err)
	}
	if first.LastReadSequence != 5 {
		t.Fatalf("last_read_sequence = %d, want 5", first.LastReadSequence)
	}

	if _, err := cursorRepo.Upsert(ctx, repo.ChatReadCursor{
		SessionID:        session.ID,
		UserID:           user.ID,
		LastReadSequence: 3,
	}); err != nil {
		t.Fatalf("Upsert second cursor: %v", err)
	}
	second, err := cursorRepo.GetBySessionAndUser(ctx, session.ID, user.ID)
	if err != nil {
		t.Fatalf("GetBySessionAndUser second: %v", err)
	}
	if second.LastReadSequence != 3 {
		t.Fatalf("last_read_sequence = %d, want 3", second.LastReadSequence)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed.UTC()
}
