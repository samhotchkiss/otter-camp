package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAllowedSubscriptionOrgID(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	if !isAllowedSubscriptionOrgID(validUUID) {
		t.Fatalf("expected UUID org id to be allowed")
	}
	if !isAllowedSubscriptionOrgID("demo") {
		t.Fatalf("expected demo org id to be allowed")
	}
	if !isAllowedSubscriptionOrgID("default") {
		t.Fatalf("expected default org id to be allowed")
	}
	if isAllowedSubscriptionOrgID("not-a-uuid") {
		t.Fatalf("expected invalid org id to be rejected")
	}
}

func TestIsAllowedSubscriptionTopic(t *testing.T) {
	if !isAllowedSubscriptionTopic("project:550e8400-e29b-41d4-a716-446655440000:chat") {
		t.Fatalf("expected project topic to be allowed")
	}
	if !isAllowedSubscriptionTopic("issue:12345") {
		t.Fatalf("expected issue topic to be allowed")
	}
	if isAllowedSubscriptionTopic("") {
		t.Fatalf("expected empty topic to be rejected")
	}
	if isAllowedSubscriptionTopic("project:bad topic") {
		t.Fatalf("expected topic with spaces to be rejected")
	}
	if isAllowedSubscriptionTopic("project/<script>") {
		t.Fatalf("expected topic with disallowed chars to be rejected")
	}
}

type stubIssueSubscriptionAuthorizer struct {
	allowed map[string]bool
}

func (s stubIssueSubscriptionAuthorizer) CanSubscribeIssue(
	_ context.Context,
	orgID, issueID string,
) (bool, error) {
	return s.allowed[orgID+":"+issueID], nil
}

func TestProcessClientMessageSubscribeIssueTopicAuthorized(t *testing.T) {
	orgID := "550e8400-e29b-41d4-a716-446655440000"
	issueID := "11111111-1111-1111-1111-111111111111"
	topic := "issue:" + issueID
	client := NewClient(nil, nil)

	processClientMessage(client, clientMessage{
		Type:  "subscribe",
		OrgID: orgID,
		Topic: topic,
	}, stubIssueSubscriptionAuthorizer{
		allowed: map[string]bool{orgID + ":" + issueID: true},
	})

	if client.OrgID() != orgID {
		t.Fatalf("expected client org to be set to %q, got %q", orgID, client.OrgID())
	}
	if !client.IsSubscribedToTopic(topic) {
		t.Fatalf("expected client to be subscribed to %q", topic)
	}
}

func TestProcessClientMessageSubscribeIssueTopicUnauthorized(t *testing.T) {
	orgID := "550e8400-e29b-41d4-a716-446655440000"
	issueID := "22222222-2222-2222-2222-222222222222"
	topic := "issue:" + issueID
	client := NewClient(nil, nil)

	processClientMessage(client, clientMessage{
		Type:  "subscribe",
		OrgID: orgID,
		Topic: topic,
	}, stubIssueSubscriptionAuthorizer{
		allowed: map[string]bool{},
	})

	if client.IsSubscribedToTopic(topic) {
		t.Fatalf("expected issue topic subscription to be rejected")
	}
}

func TestProcessClientMessageSubscribeProjectTopicWithoutIssueAuthorizer(t *testing.T) {
	orgID := "550e8400-e29b-41d4-a716-446655440000"
	topic := "project:11111111-1111-1111-1111-111111111111:chat"
	client := NewClient(nil, nil)

	processClientMessage(client, clientMessage{
		Type:  "subscribe",
		OrgID: orgID,
		Topic: topic,
	}, nil)

	if !client.IsSubscribedToTopic(topic) {
		t.Fatalf("expected non-issue topic subscription to continue working")
	}
}

func TestIsWebSocketOriginAllowed_NoOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected empty origin to be allowed")
	}
}

func TestIsWebSocketOriginAllowed_SameOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"
	req.Header.Set("Origin", "http://api.otter.camp")

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected same-origin websocket to be allowed")
	}
}

func TestIsWebSocketOriginAllowed_CrossOriginDeniedByDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"
	req.Header.Set("Origin", "https://evil.example")

	if isWebSocketOriginAllowed(req) {
		t.Fatalf("expected cross-origin websocket to be denied by default")
	}
}

func TestIsWebSocketOriginAllowed_AllowListOverride(t *testing.T) {
	t.Setenv("WS_ALLOWED_ORIGINS", "https://app.otter.camp")

	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"
	req.Header.Set("Origin", "https://app.otter.camp")

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected allow-listed origin to be allowed")
	}
}

func TestIsWebSocketOriginAllowed_LoopbackAliasAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/ws", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://localhost:8080")

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected loopback alias origin to be allowed")
	}
}
