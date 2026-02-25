//go:build integration

package project

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestFlowSessionBridgeEnsureNodeSessionIntegrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)
	template, nodes := createLinearTemplate073(t, ctx, fx, 1, false, 5)
	taskRecord := createFlowTask073(t, ctx, fx, "bridge-session-roundtrip", "in_progress", &template.ID)

	execution, err := fx.executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  nodes[0].ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("create flow_node_execution: %v", err)
	}

	bus := eventbus.New(fx.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	chatService, err := chat.NewService(chat.Options{
		Pool:   fx.pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("new chat service: %v", err)
	}
	bridge, err := NewFlowSessionBridge(FlowSessionBridgeOptions{
		Pool:  fx.pool,
		Chats: chatService,
	})
	if err != nil {
		t.Fatalf("new flow session bridge: %v", err)
	}

	session, err := bridge.EnsureNodeSession(ctx, execution)
	if err != nil {
		t.Fatalf("EnsureNodeSession first call: %v", err)
	}
	if session.ScopeType != "project_task" {
		t.Fatalf("scope_type = %q, want project_task", session.ScopeType)
	}
	if session.ScopeID != taskRecord.ID {
		t.Fatalf("scope_id = %s, want %s", session.ScopeID, taskRecord.ID)
	}
	if session.Mode != "async" {
		t.Fatalf("mode = %q, want async", session.Mode)
	}

	updatedExecution, err := fx.executionRepo.GetByID(ctx, execution.ID)
	if err != nil {
		t.Fatalf("GetByID updated flow_node_execution: %v", err)
	}
	if updatedExecution.SessionID == nil || *updatedExecution.SessionID != session.ID {
		t.Fatalf("flow_node_execution.session_id = %v, want %s", updatedExecution.SessionID, session.ID)
	}

	second, err := bridge.EnsureNodeSession(ctx, updatedExecution)
	if err != nil {
		t.Fatalf("EnsureNodeSession second call: %v", err)
	}
	if second.ID != session.ID {
		t.Fatalf("second session id = %s, want %s", second.ID, session.ID)
	}

	var count int
	if err := fx.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE scope_type = 'project_task'
		  AND scope_id = $1
		  AND mode = 'async'
		  AND metadata->>'flow_node_execution_id' = $2
	`, taskRecord.ID, execution.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count chat_session rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("chat_session count = %d, want 1", count)
	}

	chatRepo := repo.NewChatSessionRepo(fx.pool)
	storedSession, err := chatRepo.GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetByID chat_session: %v", err)
	}
	if storedSession.CreatedByType != "system" || storedSession.CreatedByID != uuid.Nil {
		t.Fatalf("created_by = (%q,%s), want (system,%s)", storedSession.CreatedByType, storedSession.CreatedByID, uuid.Nil)
	}
}
