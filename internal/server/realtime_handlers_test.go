package server

import (
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

func TestRouteEventToScopes(t *testing.T) {
	sessionA := uuid.New()
	sessionB := uuid.New()

	scopes := map[realtimeScope]struct{}{
		{Kind: "session", ID: sessionA}: {},
		{Kind: "org"}:                   {},
	}
	if !routeEventToScopes("chat.message.created", map[string]any{"session_id": sessionA.String()}, scopes) {
		t.Fatal("expected session scope to receive matching chat event")
	}

	scopes = map[realtimeScope]struct{}{{Kind: "session", ID: sessionA}: {}}
	if routeEventToScopes("chat.message.created", map[string]any{"session_id": sessionB.String()}, scopes) {
		t.Fatal("unexpected routing to non-matching session scope")
	}
}

func TestMentionPreferencePasses(t *testing.T) {
	userID := uuid.New()
	otherID := uuid.New()

	if mentionPreferencePasses("anything", &userID, userID, []string{"sam"}) == false {
		t.Fatal("expected author self-match to pass mention filter")
	}
	if mentionPreferencePasses("hello world", &otherID, userID, []string{"sam"}) {
		t.Fatal("expected non-mention to be filtered")
	}
	if !mentionPreferencePasses("hey @sam can you review", &otherID, userID, []string{"sam"}) {
		t.Fatal("expected explicit mention to pass")
	}
}

func TestDetectEventGap(t *testing.T) {
	if !detectEventGap(50, 60) {
		t.Fatal("expected gap when last-event-id is older than retained window")
	}
	if detectEventGap(59, 60) {
		t.Fatal("did not expect gap when reconnect cursor is immediately before min seq")
	}
	if detectEventGap(0, 60) {
		t.Fatal("did not expect gap without reconnect cursor")
	}
}

func TestDetectReplayGap(t *testing.T) {
	historicalGap := []eventbus.DomainEvent{{Seq: 1}, {Seq: 3}}
	if detectReplayGap(0, historicalGap) {
		t.Fatal("did not expect gap on fresh connect with historical sequence gaps")
	}

	continuousReplay := []eventbus.DomainEvent{{Seq: 101}, {Seq: 102}, {Seq: 103}}
	if detectReplayGap(100, continuousReplay) {
		t.Fatal("did not expect gap on reconnect with continuous replay")
	}

	gappedReplay := []eventbus.DomainEvent{{Seq: 101}, {Seq: 103}}
	if !detectReplayGap(100, gappedReplay) {
		t.Fatal("expected gap on reconnect when replay skips sequence")
	}
}

func TestBufferOverflowClosesConnection(t *testing.T) {
	conn := newRealtimeConnection(middleware.Principal{UserID: uuid.New(), OrganizationID: uuid.New()}, []realtimeScope{{Kind: "org"}}, "sse")
	hub := &realtimeHub{}

	for i := 0; i < realtimeEventBufferSize; i++ {
		hub.enqueue(conn, realtimeFrame{Type: realtimeFrameEvent})
	}
	hub.enqueue(conn, realtimeFrame{Type: realtimeFrameEvent})

	select {
	case <-conn.closed:
		if got := conn.reason(); got != string(realtimeFrameBufferOverflow) {
			t.Fatalf("close reason = %q, want %q", got, string(realtimeFrameBufferOverflow))
		}
	default:
		t.Fatal("expected connection to close on buffer overflow")
	}
}

func TestMentionTokensForPrincipal(t *testing.T) {
	principal := middleware.Principal{DisplayName: "Sam Hotchkiss", Email: "sam@example.com"}
	tokens := mentionTokensForPrincipal(principal)
	if len(tokens) == 0 {
		t.Fatal("expected mention tokens")
	}
}
