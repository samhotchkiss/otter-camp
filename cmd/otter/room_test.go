package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

func TestHandleRoomStats(t *testing.T) {
	t.Run("renders human output", func(t *testing.T) {
		client := &fakeRoomCommandClient{
			stats: ottercli.RoomTokenStats{
				RoomID:                   "room-123",
				RoomName:                 "Sam & Frank",
				TotalTokens:              1245000,
				ConversationCount:        87,
				AvgTokensPerConversation: 14310,
				Last7DaysTokens:          42000,
			},
		}

		var out bytes.Buffer
		err := runRoomCommand(
			[]string{"stats", "room-123", "--org", "org-9"},
			func(org string) (roomCommandClient, error) {
				if org != "org-9" {
					t.Fatalf("org override = %q, want org-9", org)
				}
				return client, nil
			},
			&out,
		)
		if err != nil {
			t.Fatalf("runRoomCommand() error = %v", err)
		}
		if client.gotRoomID != "room-123" {
			t.Fatalf("GetRoomStats room id = %q, want room-123", client.gotRoomID)
		}

		output := out.String()
		if !strings.Contains(output, "Room: Sam & Frank") {
			t.Fatalf("expected room label in output, got %q", output)
		}
		if !strings.Contains(output, "Total tokens: 1,245,000") {
			t.Fatalf("expected total tokens in output, got %q", output)
		}
		if !strings.Contains(output, "Conversations: 87") {
			t.Fatalf("expected conversations in output, got %q", output)
		}
		if !strings.Contains(output, "Avg tokens/conversation: 14,310") {
			t.Fatalf("expected avg tokens in output, got %q", output)
		}
		if !strings.Contains(output, "Last 7 days: 42,000 tokens") {
			t.Fatalf("expected recent tokens in output, got %q", output)
		}
	})

	t.Run("renders json output", func(t *testing.T) {
		client := &fakeRoomCommandClient{
			stats: ottercli.RoomTokenStats{
				RoomID:                   "room-123",
				RoomName:                 "Sam & Frank",
				TotalTokens:              123,
				ConversationCount:        2,
				AvgTokensPerConversation: 61,
				Last7DaysTokens:          60,
			},
		}

		var out bytes.Buffer
		err := runRoomCommand(
			[]string{"stats", "room-123", "--json"},
			func(org string) (roomCommandClient, error) {
				return client, nil
			},
			&out,
		)
		if err != nil {
			t.Fatalf("runRoomCommand() error = %v", err)
		}
		output := out.String()
		if !strings.Contains(output, `"room_id": "room-123"`) {
			t.Fatalf("expected room_id in json output, got %q", output)
		}
		if !strings.Contains(output, `"total_tokens": 123`) {
			t.Fatalf("expected total_tokens in json output, got %q", output)
		}
	})

	t.Run("rejects missing room id", func(t *testing.T) {
		factoryCalled := false
		err := runRoomCommand(
			[]string{"stats"},
			func(org string) (roomCommandClient, error) {
				factoryCalled = true
				return nil, nil
			},
			&bytes.Buffer{},
		)
		if err == nil || !strings.Contains(err.Error(), "usage: otter room stats <room-id>") {
			t.Fatalf("expected usage error, got %v", err)
		}
		if factoryCalled {
			t.Fatalf("factory should not be called on parse/validation failure")
		}
	})

	t.Run("surfaces client errors", func(t *testing.T) {
		client := &fakeRoomCommandClient{err: errors.New("boom")}
		err := runRoomCommand(
			[]string{"stats", "room-123"},
			func(org string) (roomCommandClient, error) {
				return client, nil
			},
			&bytes.Buffer{},
		)
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected client error to surface, got %v", err)
		}
	})
}

type fakeRoomCommandClient struct {
	gotRoomID string
	stats     ottercli.RoomTokenStats
	err       error
}

func (f *fakeRoomCommandClient) GetRoomStats(roomID string) (ottercli.RoomTokenStats, error) {
	f.gotRoomID = roomID
	if f.err != nil {
		return ottercli.RoomTokenStats{}, f.err
	}
	return f.stats, nil
}
