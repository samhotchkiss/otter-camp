package api

import "testing"

func TestBuildDMMessageBroadcastEventRequiresThreadID(t *testing.T) {
	if _, ok := buildDMMessageBroadcastEvent(Message{Content: "hello"}); ok {
		t.Fatal("expected false without thread id")
	}

	empty := ""
	if _, ok := buildDMMessageBroadcastEvent(Message{
		ThreadID: &empty,
		Content:  "hello",
	}); ok {
		t.Fatal("expected false with empty thread id")
	}
}

func TestBuildDMMessageBroadcastEventIncludesExpectedFields(t *testing.T) {
	threadID := "dm_itsalive"
	senderName := "Ivy"
	event, ok := buildDMMessageBroadcastEvent(Message{
		ID:         "message-1",
		ThreadID:   &threadID,
		SenderName: &senderName,
		Content:    "Reply content",
	})
	if !ok {
		t.Fatal("expected event")
	}

	if event["type"] != "DMMessageReceived" {
		t.Fatalf("unexpected type: %#v", event["type"])
	}

	data, ok := event["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data payload: %#v", event["data"])
	}
	if data["threadId"] != threadID {
		t.Fatalf("unexpected threadId: %#v", data["threadId"])
	}
	if data["thread_id"] != threadID {
		t.Fatalf("unexpected thread_id: %#v", data["thread_id"])
	}
	if data["preview"] != "Reply content" {
		t.Fatalf("unexpected preview: %#v", data["preview"])
	}
	if data["from"] != senderName {
		t.Fatalf("unexpected from: %#v", data["from"])
	}

	message, ok := data["message"].(Message)
	if !ok {
		t.Fatalf("unexpected message payload type: %T", data["message"])
	}
	if message.ID != "message-1" {
		t.Fatalf("unexpected message id: %s", message.ID)
	}
}
