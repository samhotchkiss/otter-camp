//go:build integration

package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestChatHTTPSessionCreateListGetRoundTrip(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newChatTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	sessionID := createChatSessionForTest(t, testServer.URL, adminToken)

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list sessions status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	if got := jsonPathString(t, listed.Body, "data", "0", "id"); got != sessionID {
		t.Fatalf("listed session id = %q, want %q body=%s", got, sessionID, string(listed.Body))
	}
	if got := jsonPathValue(t, listed.Body, "meta", "pagination", "has_more"); got != false {
		t.Fatalf("meta.pagination.has_more = %v, want false body=%s", got, string(listed.Body))
	}

	for i := 0; i < 3; i++ {
		_ = createChatSessionForTest(t, testServer.URL, adminToken)
	}
	paged := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions?limit=2", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if paged.StatusCode != http.StatusOK {
		t.Fatalf("paged list sessions status = %d, want %d body=%s", paged.StatusCode, http.StatusOK, string(paged.Body))
	}
	if got := jsonPathValue(t, paged.Body, "meta", "pagination", "has_more"); got != true {
		t.Fatalf("paged meta.pagination.has_more = %v, want true body=%s", got, string(paged.Body))
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get session status = %d, want %d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if gotID := jsonPathString(t, got.Body, "data", "id"); gotID != sessionID {
		t.Fatalf("session id = %q, want %q body=%s", gotID, sessionID, string(got.Body))
	}
	participants, ok := jsonPathValue(t, got.Body, "data", "participants").([]any)
	if !ok {
		t.Fatalf("participants shape = %T, want []any body=%s", jsonPathValue(t, got.Body, "data", "participants"), string(got.Body))
	}
	if len(participants) == 0 {
		t.Fatalf("participants len = 0, want >=1 body=%s", string(got.Body))
	}
}

func TestChatHTTPMessageSendAndList(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newChatTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	sessionID := createChatSessionForTest(t, testServer.URL, adminToken)

	sent := mustJSON(t, http.MethodPost, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content": "hello from integration",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if sent.StatusCode != http.StatusAccepted {
		t.Fatalf("append message status = %d, want %d body=%s", sent.StatusCode, http.StatusAccepted, string(sent.Body))
	}
	if got := jsonPathString(t, sent.Body, "data", "status"); got != "pending" {
		t.Fatalf("message status = %q, want %q body=%s", got, "pending", string(sent.Body))
	}
	messageID := jsonPathString(t, sent.Body, "data", "id")

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list messages status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	if got := jsonPathString(t, listed.Body, "data", "0", "id"); got != messageID {
		t.Fatalf("listed message id = %q, want %q body=%s", got, messageID, string(listed.Body))
	}
	if got := jsonPathValue(t, listed.Body, "meta", "pagination", "has_more"); got != false {
		t.Fatalf("meta.pagination.has_more = %v, want false body=%s", got, string(listed.Body))
	}

	_ = appendChatMessageForTest(t, testServer.URL, adminToken, sessionID, "second message")
	paged := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages?limit=1", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if paged.StatusCode != http.StatusOK {
		t.Fatalf("paged list messages status = %d, want %d body=%s", paged.StatusCode, http.StatusOK, string(paged.Body))
	}
	if got := jsonPathValue(t, paged.Body, "meta", "pagination", "has_more"); got != true {
		t.Fatalf("paged meta.pagination.has_more = %v, want true body=%s", got, string(paged.Body))
	}
}

func TestChatHTTPMessageRedactionAndPermissions(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, memberUser := newChatTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")
	sessionID := createChatSessionForTest(t, testServer.URL, adminToken)

	memberMessage := mustJSON(t, http.MethodPost, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content": "member-authored content",
		"role":    "assistant",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if memberMessage.StatusCode != http.StatusAccepted {
		t.Fatalf("append member message status = %d, want %d body=%s", memberMessage.StatusCode, http.StatusAccepted, string(memberMessage.Body))
	}
	memberMessageID := jsonPathString(t, memberMessage.Body, "data", "id")

	adminMessage := mustJSON(t, http.MethodPost, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content": "admin-authored content",
		"role":    "assistant",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if adminMessage.StatusCode != http.StatusAccepted {
		t.Fatalf("append admin message status = %d, want %d body=%s", adminMessage.StatusCode, http.StatusAccepted, string(adminMessage.Body))
	}
	adminMessageID := jsonPathString(t, adminMessage.Body, "data", "id")

	ownerRedact := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+memberMessageID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if ownerRedact.StatusCode != http.StatusOK {
		t.Fatalf("owner redact status = %d, want %d body=%s", ownerRedact.StatusCode, http.StatusOK, string(ownerRedact.Body))
	}
	if got := jsonPathValue(t, ownerRedact.Body, "data", "is_redacted"); got != true {
		t.Fatalf("owner redact is_redacted = %v, want true body=%s", got, string(ownerRedact.Body))
	}
	if got := jsonPathString(t, ownerRedact.Body, "data", "content"); got != "[redacted]" {
		t.Fatalf("owner redact content = %q, want %q body=%s", got, "[redacted]", string(ownerRedact.Body))
	}

	getRedacted := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+memberMessageID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if getRedacted.StatusCode != http.StatusOK {
		t.Fatalf("get redacted message status = %d, want %d body=%s", getRedacted.StatusCode, http.StatusOK, string(getRedacted.Body))
	}
	if got := jsonPathValue(t, getRedacted.Body, "data", "is_redacted"); got != true {
		t.Fatalf("get redacted is_redacted = %v, want true body=%s", got, string(getRedacted.Body))
	}
	if got := jsonPathString(t, getRedacted.Body, "data", "content"); got != "[redacted]" {
		t.Fatalf("get redacted content = %q, want %q body=%s", got, "[redacted]", string(getRedacted.Body))
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list messages status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	items, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("list messages data shape = %T, want []any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	foundRedacted := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["id"] != memberMessageID {
			continue
		}
		foundRedacted = true
		if item["is_redacted"] != true {
			t.Fatalf("listed is_redacted = %v, want true body=%s", item["is_redacted"], string(listed.Body))
		}
		if item["content"] != "[redacted]" {
			t.Fatalf("listed content = %v, want %q body=%s", item["content"], "[redacted]", string(listed.Body))
		}
	}
	if !foundRedacted {
		t.Fatalf("redacted message %s missing from list body=%s", memberMessageID, string(listed.Body))
	}

	forbidden := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+adminMessageID, nil, map[string]string{"Authorization": "Bearer " + memberToken})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner/non-author redact status = %d, want %d body=%s", forbidden.StatusCode, http.StatusForbidden, string(forbidden.Body))
	}
}

func TestChatHTTPReactionLifecycle(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, memberUser := newChatTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberUser.Email, "member-password")
	sessionID := createChatSessionForTest(t, testServer.URL, adminToken)
	messageID := appendChatMessageForTest(t, testServer.URL, adminToken, sessionID, "react to this")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+messageID+"/reactions", map[string]any{
		"emoji": "👍",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create reaction status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	reactionID := jsonPathString(t, created.Body, "data", "id")

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+messageID+"/reactions", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list reactions status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	reactionItems, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("reaction list shape = %T, want []any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	if len(reactionItems) != 1 {
		t.Fatalf("reaction list count = %d, want 1 body=%s", len(reactionItems), string(listed.Body))
	}

	forbidden := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+messageID+"/reactions/"+reactionID, nil, map[string]string{"Authorization": "Bearer " + memberToken})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("delete reaction as non-reactor status = %d, want %d body=%s", forbidden.StatusCode, http.StatusForbidden, string(forbidden.Body))
	}

	stillListed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+messageID+"/reactions", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if stillListed.StatusCode != http.StatusOK {
		t.Fatalf("list reactions after forbidden delete status = %d, want %d body=%s", stillListed.StatusCode, http.StatusOK, string(stillListed.Body))
	}
	stillItems, ok := jsonPathValue(t, stillListed.Body, "data").([]any)
	if !ok {
		t.Fatalf("reaction list after forbidden delete shape = %T, want []any body=%s", jsonPathValue(t, stillListed.Body, "data"), string(stillListed.Body))
	}
	if len(stillItems) != 1 {
		t.Fatalf("reaction list count after forbidden delete = %d, want 1 body=%s", len(stillItems), string(stillListed.Body))
	}

	deleted := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+messageID+"/reactions/"+reactionID, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete reaction status = %d, want %d body=%s", deleted.StatusCode, http.StatusNoContent, string(deleted.Body))
	}

	empty := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/messages/"+messageID+"/reactions", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if empty.StatusCode != http.StatusOK {
		t.Fatalf("list reactions after delete status = %d, want %d body=%s", empty.StatusCode, http.StatusOK, string(empty.Body))
	}
	emptyItems, ok := jsonPathValue(t, empty.Body, "data").([]any)
	if !ok {
		t.Fatalf("reaction list after delete shape = %T, want []any body=%s", jsonPathValue(t, empty.Body, "data"), string(empty.Body))
	}
	if len(emptyItems) != 0 {
		t.Fatalf("reaction list count after delete = %d, want 0 body=%s", len(emptyItems), string(empty.Body))
	}
}

func TestChatHTTPReadCursorSupportsBackwardUpdate(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newChatTestServer(t)
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")
	sessionID := createChatSessionForTest(t, testServer.URL, adminToken)

	first := mustJSON(t, http.MethodPut, testServer.URL+"/v1/chat-sessions/"+sessionID+"/read-cursor", map[string]any{
		"last_read_sequence": 5,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("put read cursor status = %d, want %d body=%s", first.StatusCode, http.StatusOK, string(first.Body))
	}

	firstGet := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/read-cursor", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if firstGet.StatusCode != http.StatusOK {
		t.Fatalf("get read cursor status = %d, want %d body=%s", firstGet.StatusCode, http.StatusOK, string(firstGet.Body))
	}
	if got := jsonPathValue(t, firstGet.Body, "data", "last_read_sequence"); got.(float64) != 5 {
		t.Fatalf("last_read_sequence = %v, want 5 body=%s", got, string(firstGet.Body))
	}

	second := mustJSON(t, http.MethodPut, testServer.URL+"/v1/chat-sessions/"+sessionID+"/read-cursor", map[string]any{
		"last_read_sequence": 3,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second put read cursor status = %d, want %d body=%s", second.StatusCode, http.StatusOK, string(second.Body))
	}

	secondGet := mustJSON(t, http.MethodGet, testServer.URL+"/v1/chat-sessions/"+sessionID+"/read-cursor", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if secondGet.StatusCode != http.StatusOK {
		t.Fatalf("second get read cursor status = %d, want %d body=%s", secondGet.StatusCode, http.StatusOK, string(secondGet.Body))
	}
	if got := jsonPathValue(t, secondGet.Body, "data", "last_read_sequence"); got.(float64) != 3 {
		t.Fatalf("last_read_sequence = %v, want 3 body=%s", got, string(secondGet.Body))
	}
}

func createChatSessionForTest(t *testing.T, baseURL, token string) string {
	t.Helper()
	created := mustJSON(t, http.MethodPost, baseURL+"/v1/chat-sessions", map[string]any{
		"scope_type": "project",
		"scope_id":   uuid.NewString(),
		"mode":       "sync",
		"title":      "integration session",
	}, map[string]string{"Authorization": "Bearer " + token})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create session status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	return jsonPathString(t, created.Body, "data", "id")
}

func appendChatMessageForTest(t *testing.T, baseURL, token, sessionID, content string) string {
	t.Helper()
	resp := mustJSON(t, http.MethodPost, baseURL+"/v1/chat-sessions/"+sessionID+"/messages", map[string]any{
		"content": content,
	}, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("append message status = %d, want %d body=%s", resp.StatusCode, http.StatusAccepted, string(resp.Body))
	}
	return jsonPathString(t, resp.Body, "data", "id")
}

func newChatTestServer(t *testing.T) (*authIntegrationServer, repo.HumanUser, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "chat-http-org", "chat-admin@example.com", "Chat Admin", "admin", "admin-password")
	_, memberUser := createOrgAndUser(t, pool, "chat-http-org", "chat-member@example.com", "Chat Member", "member", "member-password")

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

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	chatService, err := chat.NewService(chat.Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("new chat service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewChatRouteRegistrar(chatService, pool),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, adminUser, memberUser
}
