//go:build integration

package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
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
