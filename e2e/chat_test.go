//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/e2e/testutil"
)

func TestChat_CreateSessionAndSendMessage(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	bootstrapOut, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, bootstrapOut)
	}
	token := testutil.AdminToken(t, baseURL)
	orgID := currentOrgID(t, baseURL, token)

	sessionBody, sessionStatus := testutil.POST(t, baseURL, "/v1/chat-sessions", token, map[string]any{
		"scope_type": "organization",
		"scope_id":   orgID,
		"mode":       "sync",
	})
	if sessionStatus != http.StatusCreated {
		t.Fatalf("POST /v1/chat-sessions status=%d want=%d body=%s", sessionStatus, http.StatusCreated, string(sessionBody))
	}
	sessionID := strings.TrimSpace(asString(testutil.JSONPath(t, sessionBody, "data", "id")))
	assertUUID(t, sessionID, "session_id")
	if mode := strings.ToLower(asString(testutil.JSONPath(t, sessionBody, "data", "mode"))); mode != "sync" {
		t.Fatalf("session mode=%q want=sync body=%s", mode, string(sessionBody))
	}
	if scopeType := strings.ToLower(asString(testutil.JSONPath(t, sessionBody, "data", "scope_type"))); scopeType != "organization" {
		t.Fatalf("session scope_type=%q want=organization body=%s", scopeType, string(sessionBody))
	}

	frankID := frankAgentID(t, baseURL, token)
	participantBody, participantStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/participants", token, map[string]any{
		"participant_type": "agent",
		"participant_id":   frankID,
	})
	if participantStatus != http.StatusCreated {
		t.Fatalf("POST participants status=%d want=%d body=%s", participantStatus, http.StatusCreated, string(participantBody))
	}

	sseEvents, closeSSE := testutil.SSEClient(t, baseURL, "/v1/events/stream?scopes=session:"+sessionID, token)
	defer closeSSE()
	connected := testutil.WaitForSSEEvent(t, sseEvents, "connected", 10*time.Second)
	cursor := connectedHighWatermark(t, connected)

	messageBody, messageStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token, map[string]any{
		"content": "Hello, what can you help me with today?",
		"role":    "human",
	})
	if messageStatus != http.StatusCreated && messageStatus != http.StatusAccepted {
		t.Fatalf("POST messages status=%d want=%d|%d body=%s", messageStatus, http.StatusCreated, http.StatusAccepted, string(messageBody))
	}
	messageID := strings.TrimSpace(asString(testutil.JSONPath(t, messageBody, "data", "id")))
	assertUUID(t, messageID, "message_id")
	messageState := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, messageBody, "data", "status"))))
	if messageState != "pending" && messageState != "final" {
		t.Fatalf("message status=%q want=pending|final body=%s", messageState, string(messageBody))
	}

	turnsBody, turnsStatus := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/turns", token)
	if turnsStatus != http.StatusOK {
		t.Fatalf("GET turns status=%d want=%d body=%s", turnsStatus, http.StatusOK, string(turnsBody))
	}
	turns := asArray(t, testutil.JSONPath(t, turnsBody, "data"), "turns")
	if len(turns) < 1 {
		t.Fatalf("turns len=%d want>=1 body=%s", len(turns), string(turnsBody))
	}
	firstTurn := asObject(t, turns[0], "first turn")
	firstTurnID := strings.TrimSpace(asString(firstTurn["id"]))
	assertUUID(t, firstTurnID, "turn_id")
	firstStatus := strings.ToLower(strings.TrimSpace(asString(firstTurn["status"])))
	if firstStatus != "active" && firstStatus != "completed" && firstStatus != "pending" {
		t.Fatalf("turn status=%q want=active|completed|pending body=%s", firstStatus, string(turnsBody))
	}
	_ = waitForCompletedTurnCount(t, baseURL, token, sessionID, 1, 30*time.Second)

	completedEvent, _ := waitForReplayEvent(t, baseURL, token, sessionID, cursor, "chat.turn.completed", 30*time.Second)
	if eventSessionID := ssePayloadString(t, completedEvent, "session_id"); eventSessionID != sessionID {
		t.Fatalf("turn.completed payload.session_id=%q want=%q data=%s", eventSessionID, sessionID, completedEvent.Data)
	}

	turnsBody, turnsStatus = testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/turns", token)
	if turnsStatus != http.StatusOK {
		t.Fatalf("GET turns after completion status=%d want=%d body=%s", turnsStatus, http.StatusOK, string(turnsBody))
	}
	turns = asArray(t, testutil.JSONPath(t, turnsBody, "data"), "turns after completion")
	if !turnStatusExists(turns, "completed") {
		t.Fatalf("expected completed turn body=%s", string(turnsBody))
	}

	messagesBody, messagesStatus := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token)
	if messagesStatus != http.StatusOK {
		t.Fatalf("GET messages status=%d want=%d body=%s", messagesStatus, http.StatusOK, string(messagesBody))
	}
	messages := asArray(t, testutil.JSONPath(t, messagesBody, "data"), "messages")
	if len(messages) < 2 {
		t.Fatalf("messages len=%d want>=2 body=%s", len(messages), string(messagesBody))
	}
	agentFound := false
	for _, item := range messages {
		msg := asObject(t, item, "message")
		if strings.ToLower(asString(msg["author_type"])) != "agent" {
			continue
		}
		if strings.ToLower(asString(msg["status"])) != "final" {
			t.Fatalf("agent message status=%q want=final body=%s", asString(msg["status"]), string(messagesBody))
		}
		if strings.TrimSpace(asString(msg["content"])) == "" {
			t.Fatalf("agent message content is empty body=%s", string(messagesBody))
		}
		agentFound = true
		break
	}
	if !agentFound {
		t.Fatalf("expected at least one agent message body=%s", string(messagesBody))
	}
}

func TestChat_ConversationHistory(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	bootstrapOut, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, bootstrapOut)
	}
	token := testutil.AdminToken(t, baseURL)

	sessionID := createSessionWithFrankParticipant(t, baseURL, token)
	sseEvents, closeSSE := testutil.SSEClient(t, baseURL, "/v1/events/stream?scopes=session:"+sessionID, token)
	defer closeSSE()
	connected := testutil.WaitForSSEEvent(t, sseEvents, "connected", 10*time.Second)
	cursor := connectedHighWatermark(t, connected)

	sendHumanMessage(t, baseURL, token, sessionID, "Hello there.")
	_ = waitForCompletedTurnCount(t, baseURL, token, sessionID, 1, 30*time.Second)
	_, cursor = waitForReplayEvent(t, baseURL, token, sessionID, cursor, "chat.turn.completed", 30*time.Second)

	sendHumanMessage(t, baseURL, token, sessionID, "Can you elaborate on that?")
	_ = waitForCompletedTurnCount(t, baseURL, token, sessionID, 2, 30*time.Second)
	_, _ = waitForReplayEvent(t, baseURL, token, sessionID, cursor, "chat.turn.completed", 30*time.Second)

	messagesBody, messagesStatus := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token)
	if messagesStatus != http.StatusOK {
		t.Fatalf("GET messages status=%d want=%d body=%s", messagesStatus, http.StatusOK, string(messagesBody))
	}
	messages := asArray(t, testutil.JSONPath(t, messagesBody, "data"), "messages")
	if len(messages) < 4 {
		t.Fatalf("messages len=%d want>=4 body=%s", len(messages), string(messagesBody))
	}
	var previous time.Time
	for i, item := range messages {
		message := asObject(t, item, "message")
		createdRaw := strings.TrimSpace(asString(message["created_at"]))
		createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
		if err != nil {
			t.Fatalf("parse created_at[%d]=%q: %v body=%s", i, createdRaw, err, string(messagesBody))
		}
		if i > 0 && createdAt.Before(previous) {
			t.Fatalf("messages not chronological: index %d (%s) before previous %s body=%s", i, createdAt, previous, string(messagesBody))
		}
		previous = createdAt
	}

	total, ok := testutil.JSONPath(t, messagesBody, "meta", "pagination", "total").(float64)
	if !ok {
		t.Fatalf("meta.pagination.total type=%T want=float64 body=%s", testutil.JSONPath(t, messagesBody, "meta", "pagination", "total"), string(messagesBody))
	}
	if int(total) < 4 {
		t.Fatalf("meta.pagination.total=%v want>=4 body=%s", total, string(messagesBody))
	}
}

func TestChat_Reaction(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	bootstrapOut, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, bootstrapOut)
	}
	token := testutil.AdminToken(t, baseURL)

	sessionID := createSessionWithFrankParticipant(t, baseURL, token)
	sseEvents, closeSSE := testutil.SSEClient(t, baseURL, "/v1/events/stream?scopes=session:"+sessionID, token)
	defer closeSSE()
	connected := testutil.WaitForSSEEvent(t, sseEvents, "connected", 10*time.Second)
	cursor := connectedHighWatermark(t, connected)

	sendHumanMessage(t, baseURL, token, sessionID, "Please answer with one concise sentence.")
	_ = waitForCompletedTurnCount(t, baseURL, token, sessionID, 1, 30*time.Second)
	_, _ = waitForReplayEvent(t, baseURL, token, sessionID, cursor, "chat.turn.completed", 30*time.Second)

	agentMessageID := firstAgentMessageID(t, baseURL, token, sessionID)
	reactionBody, reactionStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages/"+agentMessageID+"/reactions", token, map[string]any{
		"reaction": "thumbs_up",
	})
	if reactionStatus != http.StatusCreated {
		t.Fatalf("POST reaction status=%d want=%d body=%s", reactionStatus, http.StatusCreated, string(reactionBody))
	}
	reactionID := strings.TrimSpace(asString(testutil.JSONPath(t, reactionBody, "data", "id")))
	assertUUID(t, reactionID, "reaction_id")
	if reaction := strings.TrimSpace(asString(testutil.JSONPath(t, reactionBody, "data", "reaction"))); reaction != "thumbs_up" {
		t.Fatalf("reaction=%q want=thumbs_up body=%s", reaction, string(reactionBody))
	}
	if reactorType := strings.TrimSpace(asString(testutil.JSONPath(t, reactionBody, "data", "reactor_type"))); reactorType != "human_user" {
		t.Fatalf("reactor_type=%q want=human_user body=%s", reactorType, string(reactionBody))
	}

	messageBody, messageStatus := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages/"+agentMessageID, token)
	if messageStatus != http.StatusOK {
		t.Fatalf("GET message status=%d want=%d body=%s", messageStatus, http.StatusOK, string(messageBody))
	}
	reactions := asArray(t, testutil.JSONPath(t, messageBody, "data", "reactions"), "message reactions")
	if len(reactions) < 1 {
		t.Fatalf("reactions len=%d want>=1 body=%s", len(reactions), string(messageBody))
	}
	firstReaction := asObject(t, reactions[0], "first reaction")
	if reaction := strings.TrimSpace(asString(firstReaction["reaction"])); reaction != "thumbs_up" {
		t.Fatalf("stored reaction=%q want=thumbs_up body=%s", reaction, string(messageBody))
	}

	deleteBody, deleteStatus := testutil.DELETE(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages/"+agentMessageID+"/reactions/"+reactionID, token)
	if deleteStatus != http.StatusNoContent {
		t.Fatalf("DELETE reaction status=%d want=%d body=%s", deleteStatus, http.StatusNoContent, string(deleteBody))
	}

	messageBody, messageStatus = testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages/"+agentMessageID, token)
	if messageStatus != http.StatusOK {
		t.Fatalf("GET message after delete status=%d want=%d body=%s", messageStatus, http.StatusOK, string(messageBody))
	}
	reactions = asArray(t, testutil.JSONPath(t, messageBody, "data", "reactions"), "message reactions after delete")
	if len(reactions) != 0 {
		t.Fatalf("reactions len=%d want=0 body=%s", len(reactions), string(messageBody))
	}
}

func TestChat_CancelInFlightTurn(t *testing.T) {
	server, baseURL := testutil.StartServer(t)
	defer server.Stop(t)

	testutil.ResetState(t, baseURL)
	bootstrapOut, code := server.RunCLI(t, "--server-url", baseURL, "bootstrap")
	if code != 0 {
		t.Fatalf("bootstrap exit=%d want=0 output=%s", code, bootstrapOut)
	}
	token := testutil.AdminToken(t, baseURL)

	sessionID := createSessionWithFrankParticipant(t, baseURL, token)
	sseEvents, closeSSE := testutil.SSEClient(t, baseURL, "/v1/events/stream?scopes=session:"+sessionID, token)
	defer closeSSE()
	connected := testutil.WaitForSSEEvent(t, sseEvents, "connected", 10*time.Second)
	cursor := connectedHighWatermark(t, connected)

	sendHumanMessage(t, baseURL, token, sessionID, "[slow-response] Analyze everything carefully.")

	_ = waitForTurnStatus(t, baseURL, token, sessionID, "active", 10*time.Second)
	started, cursor := waitForReplayEvent(t, baseURL, token, sessionID, cursor, "chat.turn.started", 10*time.Second)
	turnID := ssePayloadString(t, started, "turn_id")
	assertUUID(t, turnID, "turn_id")

	cancelBody, cancelStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/cancel-turn", token, map[string]any{
		"turn_id": turnID,
	})
	if cancelStatus != http.StatusOK && cancelStatus != http.StatusNoContent {
		t.Fatalf("POST cancel-turn status=%d want=%d|%d body=%s", cancelStatus, http.StatusOK, http.StatusNoContent, string(cancelBody))
	}

	_ = waitForTurnStatus(t, baseURL, token, sessionID, "cancelled", 10*time.Second)
	cancelled, _ := waitForReplayEvent(t, baseURL, token, sessionID, cursor, "chat.turn.cancelled", 10*time.Second)
	cancelledTurnID := ssePayloadString(t, cancelled, "turn_id")
	if cancelledTurnID != turnID {
		t.Fatalf("turn.cancelled payload.turn_id=%q want=%q data=%s", cancelledTurnID, turnID, cancelled.Data)
	}

	turnsBody, turnsStatus := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/turns", token)
	if turnsStatus != http.StatusOK {
		t.Fatalf("GET turns status=%d want=%d body=%s", turnsStatus, http.StatusOK, string(turnsBody))
	}
	turns := asArray(t, testutil.JSONPath(t, turnsBody, "data"), "turns")
	cancelledFound := false
	for _, item := range turns {
		turn := asObject(t, item, "turn")
		if strings.TrimSpace(asString(turn["id"])) != turnID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(asString(turn["status"]))) != "cancelled" {
			t.Fatalf("turn status=%q want=cancelled body=%s", asString(turn["status"]), string(turnsBody))
		}
		cancelledFound = true
		break
	}
	if !cancelledFound {
		t.Fatalf("cancelled turn %q not found body=%s", turnID, string(turnsBody))
	}

	sendHumanMessage(t, baseURL, token, sessionID, "Still there?")
}

func createSessionWithFrankParticipant(t *testing.T, baseURL, token string) string {
	t.Helper()
	orgID := currentOrgID(t, baseURL, token)
	sessionBody, sessionStatus := testutil.POST(t, baseURL, "/v1/chat-sessions", token, map[string]any{
		"scope_type": "organization",
		"scope_id":   orgID,
		"mode":       "sync",
	})
	if sessionStatus != http.StatusCreated {
		t.Fatalf("POST /v1/chat-sessions status=%d want=%d body=%s", sessionStatus, http.StatusCreated, string(sessionBody))
	}
	sessionID := strings.TrimSpace(asString(testutil.JSONPath(t, sessionBody, "data", "id")))
	assertUUID(t, sessionID, "session_id")

	frankID := frankAgentID(t, baseURL, token)
	participantBody, participantStatus := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/participants", token, map[string]any{
		"participant_type": "agent",
		"participant_id":   frankID,
	})
	if participantStatus != http.StatusCreated {
		t.Fatalf("POST participants status=%d want=%d body=%s", participantStatus, http.StatusCreated, string(participantBody))
	}
	return sessionID
}

func frankAgentID(t *testing.T, baseURL, token string) string {
	t.Helper()
	body, status := testutil.GET(t, baseURL, "/v1/agents?name=Frank", token)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/agents?name=Frank status=%d want=%d body=%s", status, http.StatusOK, string(body))
	}
	agents := asArray(t, testutil.JSONPath(t, body, "data"), "Frank agents")
	if len(agents) == 0 {
		t.Fatalf("expected Frank agent body=%s", string(body))
	}
	frank := asObject(t, agents[0], "Frank agent")
	frankID := strings.TrimSpace(asString(frank["id"]))
	assertUUID(t, frankID, "frank_id")
	return frankID
}

func sendHumanMessage(t *testing.T, baseURL, token, sessionID, content string) string {
	t.Helper()
	body, status := testutil.POST(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token, map[string]any{
		"content": content,
		"role":    "human",
	})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("POST /messages status=%d want=%d|%d body=%s", status, http.StatusCreated, http.StatusAccepted, string(body))
	}
	messageID := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "id")))
	assertUUID(t, messageID, "message_id")
	state := strings.ToLower(strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "status"))))
	if state != "pending" && state != "final" {
		t.Fatalf("message status=%q want=pending|final body=%s", state, string(body))
	}
	return messageID
}

func firstAgentMessageID(t *testing.T, baseURL, token, sessionID string) string {
	t.Helper()
	body, status := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/messages", token)
	if status != http.StatusOK {
		t.Fatalf("GET messages status=%d want=%d body=%s", status, http.StatusOK, string(body))
	}
	messages := asArray(t, testutil.JSONPath(t, body, "data"), "messages")
	for _, item := range messages {
		message := asObject(t, item, "message")
		if strings.ToLower(strings.TrimSpace(asString(message["author_type"]))) != "agent" {
			continue
		}
		id := strings.TrimSpace(asString(message["id"]))
		assertUUID(t, id, "agent_message_id")
		return id
	}
	t.Fatalf("agent response message not found body=%s", string(body))
	return ""
}

func currentOrgID(t *testing.T, baseURL, token string) string {
	t.Helper()
	body, status := testutil.GET(t, baseURL, "/v1/orgs/current", token)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/orgs/current status=%d want=%d body=%s", status, http.StatusOK, string(body))
	}
	orgID := strings.TrimSpace(asString(testutil.JSONPath(t, body, "data", "id")))
	assertUUID(t, orgID, "org_id")
	return orgID
}

func turnStatusExists(turns []any, expected string) bool {
	for _, item := range turns {
		turn, ok := item.(map[string]any)
		if !ok {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(asString(turn["status"])))
		if status == strings.ToLower(strings.TrimSpace(expected)) {
			return true
		}
	}
	return false
}

func ssePayloadString(t *testing.T, event testutil.SSEEvent, key string) string {
	t.Helper()

	var envelope map[string]any
	if err := json.Unmarshal([]byte(event.Data), &envelope); err != nil {
		t.Fatalf("unmarshal sse data: %v data=%s", err, event.Data)
	}
	payload := asObject(t, envelope["payload"], "sse payload")
	value := strings.TrimSpace(asString(payload[key]))
	if value == "" {
		t.Fatalf("missing sse payload key %q event=%+v", key, event)
	}
	return value
}

func waitForReplayEvent(
	t *testing.T,
	baseURL, token, sessionID string,
	lastEventID int64,
	eventType string,
	timeout time.Duration,
) (testutil.SSEEvent, int64) {
	t.Helper()

	path := fmt.Sprintf("/v1/events/stream?scopes=session:%s&last-event-id=%d", sessionID, lastEventID)
	ch, closeFn := testutil.SSEClient(t, baseURL, path, token)
	defer closeFn()

	_ = testutil.WaitForSSEEvent(t, ch, "connected", 10*time.Second)
	event := testutil.WaitForSSEEvent(t, ch, eventType, timeout)
	return event, parseEventID(t, event.ID)
}

func connectedHighWatermark(t *testing.T, event testutil.SSEEvent) int64 {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
		t.Fatalf("unmarshal connected event: %v data=%s", err, event.Data)
	}
	value, ok := payload["high_watermark"].(float64)
	if !ok {
		t.Fatalf("connected high_watermark type=%T want=float64 data=%s", payload["high_watermark"], event.Data)
	}
	return int64(value)
}

func parseEventID(t *testing.T, raw string) int64 {
	t.Helper()
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		t.Fatalf("parse event id %q: %v", raw, err)
	}
	return value
}

func waitForTurnStatus(t *testing.T, baseURL, token, sessionID, status string, timeout time.Duration) map[string]any {
	t.Helper()

	deadline := time.Now().Add(timeout)
	want := strings.ToLower(strings.TrimSpace(status))
	for time.Now().Before(deadline) {
		body, code := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/turns", token)
		if code != http.StatusOK {
			t.Fatalf("GET turns status=%d want=%d body=%s", code, http.StatusOK, string(body))
		}
		turns := asArray(t, testutil.JSONPath(t, body, "data"), "turns")
		for _, item := range turns {
			turn := asObject(t, item, "turn")
			current := strings.ToLower(strings.TrimSpace(asString(turn["status"])))
			if current == want {
				return turn
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn status %q", want)
	return nil
}

func waitForCompletedTurnCount(t *testing.T, baseURL, token, sessionID string, minCompleted int, timeout time.Duration) []any {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body, code := testutil.GET(t, baseURL, "/v1/chat-sessions/"+sessionID+"/turns", token)
		if code != http.StatusOK {
			t.Fatalf("GET turns status=%d want=%d body=%s", code, http.StatusOK, string(body))
		}
		turns := asArray(t, testutil.JSONPath(t, body, "data"), "turns")
		completed := 0
		for _, item := range turns {
			turn := asObject(t, item, "turn")
			current := strings.ToLower(strings.TrimSpace(asString(turn["status"])))
			if current == "completed" {
				completed++
			}
		}
		if completed >= minCompleted {
			return turns
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for at least %d completed turns", minCompleted)
	return nil
}

func assertUUID(t *testing.T, raw, label string) {
	t.Helper()
	if strings.TrimSpace(raw) == "" {
		t.Fatalf("%s is empty", label)
	}
	if _, err := uuid.Parse(strings.TrimSpace(raw)); err != nil {
		t.Fatalf("%s=%q is not a UUID: %v", label, raw, err)
	}
}
