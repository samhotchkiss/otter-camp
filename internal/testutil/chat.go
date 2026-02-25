package testutil

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func MakeSession(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, scopeType string, scopeID uuid.UUID) *chat.ChatSession {
	t.Helper()

	if orgID == uuid.Nil {
		t.Fatal("MakeSession requires non-nil org id")
	}
	if scopeID == uuid.Nil {
		scopeID = orgID
	}

	normalizedScope := strings.TrimSpace(scopeType)
	if normalizedScope == "" {
		normalizedScope = "organization"
	}

	created, err := repo.NewChatSessionRepo(db).Create(context.Background(), repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      normalizedScope,
		ScopeID:        scopeID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	out := chat.ChatSession(created)
	return &out
}

func MakeMessage(t testing.TB, db *pgxpool.Pool, sessionID uuid.UUID, authorType string, authorID uuid.UUID, content string) *chat.ChatMessage {
	t.Helper()

	if sessionID == uuid.Nil {
		t.Fatal("MakeMessage requires non-nil session id")
	}

	var authorTypePtr *string
	var authorIDPtr *uuid.UUID
	if strings.TrimSpace(authorType) != "" {
		normalized := strings.TrimSpace(authorType)
		authorTypePtr = &normalized
		if authorID != uuid.Nil {
			authorIDCopy := authorID
			authorIDPtr = &authorIDCopy
		}
	}

	created, err := repo.NewChatMessageRepo(db).Create(context.Background(), repo.ChatMessage{
		SessionID:  sessionID,
		AuthorType: authorTypePtr,
		AuthorID:   authorIDPtr,
		Role:       "user",
		Content:    strings.TrimSpace(content),
		Status:     "final",
		Metadata:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create chat message: %v", err)
	}

	out := chat.ChatMessage(created)
	return &out
}

func MakeTurn(t testing.TB, db *pgxpool.Pool, sessionID uuid.UUID) *chat.ChatTurn {
	t.Helper()

	if sessionID == uuid.Nil {
		t.Fatal("MakeTurn requires non-nil session id")
	}

	created, err := repo.NewChatTurnRepo(db).Create(context.Background(), repo.ChatTurn{
		SessionID:      sessionID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   uuid.Nil,
		Status:         "pending",
	})
	if err != nil {
		t.Fatalf("create chat turn: %v", err)
	}

	out := chat.ChatTurn(created)
	return &out
}
