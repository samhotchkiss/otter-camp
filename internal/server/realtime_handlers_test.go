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
	makeEvents := func(seqs ...int64) []eventbus.DomainEvent {
		events := make([]eventbus.DomainEvent, len(seqs))
		for i, s := range seqs {
			events[i].Seq = s
		}
		return events
	}

	// Fresh connect (lastEventID=0): no gap even if historical events have gaps.
	if detectReplayGap(0, makeEvents(1, 3, 5)) {
		t.Fatal("expected no gap on fresh connect (lastEventID=0) even with historical seq gaps")
	}
	// Fresh connect with empty events: no gap.
	if detectReplayGap(0, nil) {
		t.Fatal("expected no gap on fresh connect with no events")
	}
	// Reconnect with continuous events: no gap.
	if detectReplayGap(10, makeEvents(11, 12, 13)) {
		t.Fatal("expected no gap for continuous replay from lastEventID")
	}
	// Reconnect with gap in replay: gap.
	if !detectReplayGap(10, makeEvents(11, 13)) {
		t.Fatal("expected gap when replay events skip seq 12")
	}
	// Reconnect but no replay events (all purged): handled by detectEventGap, not here.
	if detectReplayGap(10, nil) {
		t.Fatal("expected no gap from detectReplayGap with empty events (purge handled by detectEventGap)")
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
