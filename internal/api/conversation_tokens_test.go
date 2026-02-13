package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestRoomTokenEndpoints(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "room-token-endpoint")

	roomID := insertConversationTokenAPITestRoom(t, db, orgID, "Room Tokens")
	conversationID := insertConversationTokenAPITestConversation(t, db, orgID, roomID, "Room Tokens Conversation")

	_, err := db.Exec(
		`INSERT INTO chat_messages (
			org_id,
			room_id,
			conversation_id,
			sender_id,
			sender_type,
			body,
			type,
			attachments
		) VALUES ($1, $2, $3, gen_random_uuid(), 'user', $4, 'message', '[]'::jsonb)`,
		orgID,
		roomID,
		conversationID,
		"room token endpoint message",
	)
	require.NoError(t, err)

	handler := &ConversationTokenHandler{
		Store: store.NewConversationTokenStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rooms/"+roomID, nil)
	req = addRouteParam(req, "id", roomID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetRoom(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload roomTokenResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, roomID, payload.ID)
	require.Equal(t, "Room Tokens", payload.Name)
	require.Greater(t, payload.TotalTokens, int64(0))

	invalidReq := httptest.NewRequest(http.MethodGet, "/api/v1/rooms/invalid", nil)
	invalidReq = addRouteParam(invalidReq, "id", "invalid")
	invalidReq = invalidReq.WithContext(context.WithValue(invalidReq.Context(), middleware.WorkspaceIDKey, orgID))
	invalidRec := httptest.NewRecorder()
	handler.GetRoom(invalidRec, invalidReq)
	require.Equal(t, http.StatusBadRequest, invalidRec.Code)
}

func TestConversationTokenEndpoint(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conversation-token-endpoint")

	roomID := insertConversationTokenAPITestRoom(t, db, orgID, "Conversation Tokens")
	conversationID := insertConversationTokenAPITestConversation(t, db, orgID, roomID, "Conversation Topic")

	_, err := db.Exec(
		`INSERT INTO chat_messages (
			org_id,
			room_id,
			conversation_id,
			sender_id,
			sender_type,
			body,
			type,
			attachments
		) VALUES ($1, $2, $3, gen_random_uuid(), 'agent', $4, 'message', '[]'::jsonb)`,
		orgID,
		roomID,
		conversationID,
		"conversation token endpoint message",
	)
	require.NoError(t, err)

	handler := &ConversationTokenHandler{
		Store: store.NewConversationTokenStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+conversationID, nil)
	req = addRouteParam(req, "id", conversationID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetConversation(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload conversationTokenResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, conversationID, payload.ID)
	require.Equal(t, roomID, payload.RoomID)
	require.Equal(t, "Conversation Topic", payload.Topic)
	require.Greater(t, payload.TotalTokens, int64(0))
}

func TestRoomStatsEndpoint(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "room-stats-endpoint")

	roomID := insertConversationTokenAPITestRoom(t, db, orgID, "Room Stats")
	conversationID := insertConversationTokenAPITestConversation(t, db, orgID, roomID, "Room Stats Conversation")

	older := time.Now().UTC().Add(-10 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-2 * time.Hour)
	_, err := db.Exec(
		`INSERT INTO chat_messages (
			org_id,
			room_id,
			conversation_id,
			sender_id,
			sender_type,
			body,
			type,
			created_at,
			attachments
		) VALUES
			($1, $2, $3, gen_random_uuid(), 'user', $4, 'message', $5, '[]'::jsonb),
			($1, $2, $3, gen_random_uuid(), 'agent', $6, 'message', $7, '[]'::jsonb)`,
		orgID,
		roomID,
		conversationID,
		"older room stats message",
		older,
		"recent room stats message",
		recent,
	)
	require.NoError(t, err)

	var expectedTotal int64
	err = db.QueryRow(`SELECT total_tokens FROM rooms WHERE id = $1`, roomID).Scan(&expectedTotal)
	require.NoError(t, err)

	var expectedRecent int64
	err = db.QueryRow(
		`SELECT COALESCE(SUM(token_count), 0)
		   FROM chat_messages
		  WHERE org_id = $1
		    AND room_id = $2
		    AND created_at >= NOW() - INTERVAL '7 days'`,
		orgID,
		roomID,
	).Scan(&expectedRecent)
	require.NoError(t, err)

	handler := &ConversationTokenHandler{
		Store: store.NewConversationTokenStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rooms/"+roomID+"/stats", nil)
	req = addRouteParam(req, "id", roomID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetRoomStats(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload roomTokenStatsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, roomID, payload.RoomID)
	require.Equal(t, "Room Stats", payload.RoomName)
	require.Equal(t, expectedTotal, payload.TotalTokens)
	require.Equal(t, 1, payload.ConversationCount)
	require.Equal(t, expectedTotal, payload.AvgTokensPerConversation)
	require.Equal(t, expectedRecent, payload.Last7DaysTokens)
	require.Len(t, payload.TokensBySender, 2)
}

func insertConversationTokenAPITestRoom(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type)
		 VALUES ($1, $2, 'ad_hoc')
		 RETURNING id::text`,
		orgID,
		name,
	).Scan(&roomID)
	require.NoError(t, err)
	return roomID
}

func insertConversationTokenAPITestConversation(t *testing.T, db *sql.DB, orgID, roomID, topic string) string {
	t.Helper()
	var conversationID string
	err := db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at)
		 VALUES ($1, $2, $3, NOW())
		 RETURNING id::text`,
		orgID,
		roomID,
		topic,
	).Scan(&conversationID)
	require.NoError(t, err)
	return conversationID
}
