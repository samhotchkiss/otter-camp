package ws

import (
	"testing"
	"time"
)

func mustReceiveMessage(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for websocket payload")
		return nil
	}
}

func mustNotReceiveMessage(t *testing.T, ch <-chan []byte, timeout time.Duration) {
	t.Helper()
	select {
	case payload := <-ch:
		t.Fatalf("expected no payload, got %q", string(payload))
	case <-time.After(timeout):
	}
}

func TestHubBroadcastTopicFiltersBySubscription(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	orgID := "550e8400-e29b-41d4-a716-446655440000"
	otherOrgID := "660e8400-e29b-41d4-a716-446655440000"
	topicA := "project:11111111-1111-1111-1111-111111111111:chat"
	topicB := "project:22222222-2222-2222-2222-222222222222:chat"

	clientA := NewClient(hub, nil)
	clientA.SetOrgID(orgID)
	clientA.SubscribeTopic(topicA)

	clientB := NewClient(hub, nil)
	clientB.SetOrgID(orgID)
	clientB.SubscribeTopic(topicB)

	clientOtherOrg := NewClient(hub, nil)
	clientOtherOrg.SetOrgID(otherOrgID)
	clientOtherOrg.SubscribeTopic(topicA)

	hub.Register(clientA)
	hub.Register(clientB)
	hub.Register(clientOtherOrg)

	t.Cleanup(func() {
		hub.Unregister(clientA)
		hub.Unregister(clientB)
		hub.Unregister(clientOtherOrg)
	})

	time.Sleep(25 * time.Millisecond)

	hub.BroadcastTopic(orgID, topicA, []byte("topic-a"))
	received := mustReceiveMessage(t, clientA.Send, 200*time.Millisecond)
	if string(received) != "topic-a" {
		t.Fatalf("expected topic-a payload, got %q", string(received))
	}

	mustNotReceiveMessage(t, clientB.Send, 80*time.Millisecond)
	mustNotReceiveMessage(t, clientOtherOrg.Send, 80*time.Millisecond)

	hub.Broadcast(orgID, []byte("org-wide"))
	received = mustReceiveMessage(t, clientA.Send, 200*time.Millisecond)
	if string(received) != "org-wide" {
		t.Fatalf("expected org-wide payload for clientA, got %q", string(received))
	}
	received = mustReceiveMessage(t, clientB.Send, 200*time.Millisecond)
	if string(received) != "org-wide" {
		t.Fatalf("expected org-wide payload for clientB, got %q", string(received))
	}
	mustNotReceiveMessage(t, clientOtherOrg.Send, 80*time.Millisecond)
}
