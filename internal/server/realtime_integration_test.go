//go:build integration

package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestRealtimeSSERequiresAuth(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	req, err := http.NewRequest(http.MethodGet, fixture.URL+"/v1/events/stream?scopes=org", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusUnauthorized, string(body))
	}
}

func TestRealtimeSSEFanoutFromChatAppendMessage(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	sessionID := createChatSessionForTest(t, fixture.URL, token)
	lastSeq := fixture.currentMaxSeq(t)

	stream := openSSEStream(t, fixture.URL, token, fmt.Sprintf("session:%s", sessionID), lastSeq)
	defer stream.close()
	stream.expectEvent(t, "connected", 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{
		UserID:         fixture.Admin.ID,
		OrganizationID: fixture.Org.ID,
		DisplayName:    fixture.Admin.DisplayName,
		Email:          fixture.Admin.Email,
		Role:           fixture.Admin.Role,
	})
	if _, err := fixture.ChatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: mustUUID(t, sessionID),
		Role:      "user",
		Content:   "realtime integration message",
	}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	var count int
	if err := fixture.Pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'chat.message.created'
	`, fixture.Org.ID).Scan(&count); err != nil {
		t.Fatalf("count domain events: %v", err)
	}
	if count == 0 {
		t.Fatal("expected chat.message.created domain event row")
	}

	evt := stream.expectEvent(t, "chat.message.created", 500*time.Millisecond)
	if evt.id == "" {
		t.Fatal("expected SSE event id")
	}
}

func TestRealtimeSSEReconnectReplay(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	sessionID := createChatSessionForTest(t, fixture.URL, token)
	sessionUUID := mustUUID(t, sessionID)
	lastSeq := fixture.currentMaxSeq(t)

	stream := openSSEStream(t, fixture.URL, token, fmt.Sprintf("session:%s", sessionID), lastSeq)
	stream.expectEvent(t, "connected", 2*time.Second)

	for i := 0; i < 5; i++ {
		fixture.publishSessionEvent(t, "chat.message.created", sessionUUID)
	}

	lastID := ""
	for i := 0; i < 5; i++ {
		evt := stream.expectEvent(t, "chat.message.created", 500*time.Millisecond)
		lastID = evt.id
	}
	if lastID == "" {
		t.Fatal("expected last event id after initial stream")
	}
	stream.close()

	for i := 0; i < 3; i++ {
		fixture.publishSessionEvent(t, "chat.message.created", sessionUUID)
	}

	replay := openSSEStream(t, fixture.URL, token, fmt.Sprintf("session:%s", sessionID), mustInt64(t, lastID))
	defer replay.close()
	replay.expectEvent(t, "connected", 2*time.Second)

	ids := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		evt := replay.expectEvent(t, "chat.message.created", 500*time.Millisecond)
		ids = append(ids, mustInt64(t, evt.id))
	}
	if !(ids[0] < ids[1] && ids[1] < ids[2]) {
		t.Fatalf("replayed event ids not strictly increasing: %v", ids)
	}
}

func TestRealtimeSSEMultiScopeRouting(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	sessionA := createChatSessionForTest(t, fixture.URL, token)
	sessionC := createChatSessionForTest(t, fixture.URL, token)
	projectB := fixture.createProject(t)
	lastSeq := fixture.currentMaxSeq(t)

	scopes := fmt.Sprintf("session:%s,project:%s", sessionA, projectB.ID)
	stream := openSSEStream(t, fixture.URL, token, scopes, lastSeq)
	defer stream.close()
	stream.expectEvent(t, "connected", 2*time.Second)

	fixture.publishSessionEvent(t, "chat.message.created", mustUUID(t, sessionA))
	fixture.publishProjectTaskEvent(t, projectB.ID)
	fixture.publishSessionEvent(t, "chat.message.created", mustUUID(t, sessionC))

	first := stream.expectAnyEvent(t, 500*time.Millisecond)
	second := stream.expectAnyEvent(t, 500*time.Millisecond)
	if first.eventName == "chat.message.created" {
		if second.eventName != "task.status_changed" {
			t.Fatalf("expected second event task.status_changed, got %s", second.eventName)
		}
	} else if first.eventName == "task.status_changed" {
		if second.eventName != "chat.message.created" {
			t.Fatalf("expected second event chat.message.created, got %s", second.eventName)
		}
	} else {
		t.Fatalf("unexpected first event %s", first.eventName)
	}

	stream.expectNoEvent(t, 300*time.Millisecond)
}

func TestRealtimeWebSocketTypingBroadcast(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	sessionID := createChatSessionForTest(t, fixture.URL, token)
	lastSeq := fixture.currentMaxSeq(t)

	ws1 := openWebSocket(t, fixture.URL, token, fmt.Sprintf("session:%s", sessionID), lastSeq)
	defer ws1.Close()
	ws2 := openWebSocket(t, fixture.URL, token, fmt.Sprintf("session:%s", sessionID), lastSeq)
	defer ws2.Close()

	readWSMessage(t, ws1, 2*time.Second) // connected
	readWSMessage(t, ws2, 2*time.Second) // connected

	if err := ws1.WriteJSON(map[string]any{"type": "typing", "session_id": sessionID}); err != nil {
		t.Fatalf("write typing frame: %v", err)
	}

	message := readWSMessage(t, ws2, 200*time.Millisecond)
	if got, _ := message["type"].(string); got != "typing" {
		t.Fatalf("frame type = %q, want typing", got)
	}
	if got, _ := message["session_id"].(string); got != sessionID {
		t.Fatalf("session_id = %q, want %q", got, sessionID)
	}
}

func TestRealtimeScopeValidation(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	otherOrg := fixture.createForeignSession(t)

	req, err := http.NewRequest(http.MethodGet, fixture.URL+"/v1/events/stream?scopes=session:"+otherOrg.String(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusBadRequest, string(body))
	}

	agentKey := fixture.issueAgentLikeAPIKey(t)
	req2, err := http.NewRequest(http.MethodGet, fixture.URL+"/v1/events/stream?scopes=org", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req2.Header.Set("X-API-Key", agentKey)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("status = %d, want %d body=%s", resp2.StatusCode, http.StatusForbidden, string(body))
	}
}

func TestRealtimeWebSocketNegotiateByUserAgent(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")

	mobileResp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/ws/negotiate", nil, map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    "OtterCamp-iOS/1.0",
	})
	if mobileResp.StatusCode != http.StatusOK {
		t.Fatalf("mobile negotiate status = %d, want %d body=%s", mobileResp.StatusCode, http.StatusOK, string(mobileResp.Body))
	}
	if got := jsonPathString(t, mobileResp.Body, "data", "preferred"); got != "websocket" {
		t.Fatalf("mobile preferred = %q, want %q body=%s", got, "websocket", string(mobileResp.Body))
	}

	webResp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/ws/negotiate", nil, map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    "Mozilla/5.0",
	})
	if webResp.StatusCode != http.StatusOK {
		t.Fatalf("web negotiate status = %d, want %d body=%s", webResp.StatusCode, http.StatusOK, string(webResp.Body))
	}
	if got := jsonPathString(t, webResp.Body, "data", "preferred"); got != "sse" {
		t.Fatalf("web preferred = %q, want %q body=%s", got, "sse", string(webResp.Body))
	}
}

func TestRealtimeSSEMalformedLastEventIDReturnsBadRequest(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")

	req, err := http.NewRequest(http.MethodGet, fixture.URL+"/v1/events/stream?scopes=org", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Last-Event-ID", "not-a-number")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusBadRequest, string(body))
	}
}

func TestRealtimeSSEReconnectFromHeaderReplaysExpectedEvents(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newRealtimeTestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	sessionID := createChatSessionForTest(t, fixture.URL, token)
	sessionUUID := mustUUID(t, sessionID)

	baseSeq := fixture.currentMaxSeq(t)
	for i := 0; i < 10; i++ {
		fixture.publishSessionEvent(t, "chat.message.created", sessionUUID)
	}

	req, err := http.NewRequest(http.MethodGet, fixture.URL+"/v1/events/stream?scopes=session:"+sessionID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Last-Event-ID", strconv.FormatInt(baseSeq+7, 10))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open sse stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("open sse status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	stream := &sseStream{resp: resp, reader: bufio.NewReader(resp.Body)}
	defer stream.close()

	stream.expectEvent(t, "connected", 2*time.Second)
	first := stream.expectEvent(t, "chat.message.created", time.Second)
	second := stream.expectEvent(t, "chat.message.created", time.Second)
	third := stream.expectEvent(t, "chat.message.created", time.Second)

	want := []int64{baseSeq + 8, baseSeq + 9, baseSeq + 10}
	got := []int64{mustInt64(t, first.id), mustInt64(t, second.id), mustInt64(t, third.id)}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replayed seq[%d] = %d, want %d (all=%v)", i, got[i], want[i], got)
		}
	}
}

type realtimeFixture struct {
	URL         string
	Pool        *pgxpool.Pool
	Org         repo.Organization
	Admin       repo.HumanUser
	Member      repo.HumanUser
	Bus         *eventbus.Bus
	ChatService chat.ChatService
	AuthService authsvc.Service
	Realtime    *RealtimeRouteRegistrar

	ts *httptest.Server
}

func newRealtimeTestServer(t *testing.T) *realtimeFixture {
	t.Helper()
	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "realtime-http-org", "rt-admin@example.com", "RT Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, "realtime-http-org", "rt-member@example.com", "RT Member", "member", "member-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{PollInterval: 10 * time.Millisecond})
	chatService, err := chat.NewService(chat.Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("new chat service: %v", err)
	}

	realtimeRegistrar := NewRealtimeRouteRegistrar(RealtimeRouteOptions{Pool: pool, Events: bus})
	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewChatRouteRegistrar(chatService, pool),
			realtimeRegistrar,
		},
	})

	ts := httptest.NewServer(handler)
	return &realtimeFixture{
		URL:         ts.URL,
		Pool:        pool,
		Org:         org,
		Admin:       adminUser,
		Member:      memberUser,
		Bus:         bus,
		ChatService: chatService,
		AuthService: authService,
		Realtime:    realtimeRegistrar,
		ts:          ts,
	}
}

func (f *realtimeFixture) Close() {
	if f == nil || f.ts == nil {
		return
	}
	if f.Realtime != nil {
		_ = f.Realtime.Close()
	}
	f.ts.Close()
}

func (f *realtimeFixture) currentMaxSeq(t *testing.T) int64 {
	t.Helper()
	var seq int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT COALESCE(MAX(seq), 0) FROM domain_event WHERE organization_id = $1`, f.Org.ID).Scan(&seq); err != nil {
		t.Fatalf("query max seq: %v", err)
	}
	return seq
}

func (f *realtimeFixture) createProject(t *testing.T) repo.Project {
	t.Helper()
	project, err := repo.NewProjectRepo(f.Pool).Create(context.Background(), repo.Project{
		OrganizationID: f.Org.ID,
		Slug:           "realtime-project-" + uuid.NewString()[:8],
		DisplayName:    "Realtime Project",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    f.Admin.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func (f *realtimeFixture) publishSessionEvent(t *testing.T, eventType string, sessionID uuid.UUID) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"session_id": sessionID.String(), "message_id": uuid.NewString()})
	if err := f.Bus.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: f.Org.ID,
		EventType:      eventType,
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish session event: %v", err)
	}
}

func (f *realtimeFixture) publishProjectTaskEvent(t *testing.T, projectID uuid.UUID) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"project_id": projectID.String(), "task_id": uuid.NewString()})
	if err := f.Bus.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: f.Org.ID,
		EventType:      "task.status_changed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish project task event: %v", err)
	}
}

func (f *realtimeFixture) createForeignSession(t *testing.T) uuid.UUID {
	t.Helper()
	otherOrg, _ := createOrgAndUser(t, f.Pool, "realtime-other-org", "other-admin@example.com", "Other Admin", "admin", "admin-password")
	session, err := repo.NewChatSessionRepo(f.Pool).Create(context.Background(), repo.ChatSession{
		OrganizationID: otherOrg.ID,
		ScopeType:      "project",
		ScopeID:        uuid.New(),
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create foreign session: %v", err)
	}
	return session.ID
}

func (f *realtimeFixture) issueAgentLikeAPIKey(t *testing.T) string {
	t.Helper()
	issued, err := f.AuthService.IssueAPIKey(context.Background(), f.Admin.ID, "agent-like", []string{"agent:chat"}, nil)
	if err != nil {
		t.Fatalf("issue api key: %v", err)
	}
	return issued.RawKey
}

type sseEvent struct {
	eventName string
	id        string
	data      string
}

type sseStream struct {
	resp   *http.Response
	reader *bufio.Reader
}

func openSSEStream(t *testing.T, baseURL, token, scopes string, lastEventID int64) *sseStream {
	t.Helper()
	query := url.Values{}
	query.Set("scopes", scopes)
	if lastEventID > 0 {
		query.Set("last-event-id", fmt.Sprintf("%d", lastEventID))
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/events/stream?"+query.Encode(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open sse stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("open sse status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(body))
	}

	return &sseStream{resp: resp, reader: bufio.NewReader(resp.Body)}
}

func (s *sseStream) close() {
	if s == nil || s.resp == nil || s.resp.Body == nil {
		return
	}
	_ = s.resp.Body.Close()
}

func (s *sseStream) expectEvent(t *testing.T, name string, timeout time.Duration) sseEvent {
	t.Helper()
	evt := s.expectAnyEvent(t, timeout)
	if evt.eventName != name {
		t.Fatalf("event name = %q, want %q", evt.eventName, name)
	}
	return evt
}

func (s *sseStream) expectAnyEvent(t *testing.T, timeout time.Duration) sseEvent {
	t.Helper()
	ch := make(chan sseEvent, 1)
	errCh := make(chan error, 1)
	go func() {
		for {
			eventName, id, data, err := readSSEFrame(s.reader)
			if err != nil {
				errCh <- err
				return
			}
			if strings.TrimSpace(eventName) == "" {
				continue
			}
			ch <- sseEvent{eventName: eventName, id: id, data: data}
			return
		}
	}()

	select {
	case evt := <-ch:
		return evt
	case err := <-errCh:
		t.Fatalf("read sse event: %v", err)
		return sseEvent{}
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for sse event")
		return sseEvent{}
	}
}

func (s *sseStream) expectNoEvent(t *testing.T, timeout time.Duration) {
	t.Helper()
	ch := make(chan struct{}, 1)
	go func() {
		_, _, _, err := readSSEFrame(s.reader)
		if err == nil {
			ch <- struct{}{}
		}
	}()

	select {
	case <-ch:
		t.Fatal("unexpected SSE event")
	case <-time.After(timeout):
	}
}

func openWebSocket(t *testing.T, baseURL, token, scopes string, lastEventID int64) *websocket.Conn {
	t.Helper()
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1)
	query := url.Values{}
	query.Set("scopes", scopes)
	query.Set("last-event-id", fmt.Sprintf("%d", lastEventID))
	query.Set("token", token)

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL+"/v1/ws?"+query.Encode(), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readWSMessage(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	decoded := make(map[string]any)
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal websocket message: %v", err)
	}
	return decoded
}

func mustUUID(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("parse uuid %q: %v", raw, err)
	}
	return parsed
}

func mustInt64(t *testing.T, raw string) int64 {
	t.Helper()
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		t.Fatalf("parse int64 %q: %v", raw, err)
	}
	return value
}
