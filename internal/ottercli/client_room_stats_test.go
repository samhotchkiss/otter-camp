package ottercli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientGetRoomStats(t *testing.T) {
	t.Run("uses expected endpoint and decodes payload", func(t *testing.T) {
		var gotMethod string
		var gotPath string
		var gotOrg string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.String()
			gotOrg = r.Header.Get("X-Org-ID")

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"room_id":"room-123","room_name":"Sam & Frank","total_tokens":1245000,"conversation_count":87,"avg_tokens_per_conversation":14310,"last_7_days_tokens":42000,"tokens_by_sender":[{"sender_id":"agent-1","sender_type":"agent","total_tokens":780000}]}`))
		}))
		defer srv.Close()

		client := &Client{
			BaseURL: srv.URL,
			Token:   "token-1",
			OrgID:   "org-1",
			HTTP:    srv.Client(),
		}

		stats, err := client.GetRoomStats("room-123")
		if err != nil {
			t.Fatalf("GetRoomStats() error = %v", err)
		}

		if gotMethod != http.MethodGet || gotPath != "/api/v1/rooms/room-123/stats" {
			t.Fatalf("GetRoomStats request = %s %s", gotMethod, gotPath)
		}
		if gotOrg != "org-1" {
			t.Fatalf("X-Org-ID = %q, want org-1", gotOrg)
		}
		if stats.RoomID != "room-123" || stats.RoomName != "Sam & Frank" {
			t.Fatalf("GetRoomStats room = %#v", stats)
		}
		if stats.TotalTokens != 1245000 || stats.ConversationCount != 87 {
			t.Fatalf("GetRoomStats totals = %#v", stats)
		}
		if len(stats.TokensBySender) != 1 || stats.TokensBySender[0].SenderID != "agent-1" {
			t.Fatalf("GetRoomStats sender stats = %#v", stats.TokensBySender)
		}
	})

	t.Run("rejects empty room id", func(t *testing.T) {
		client := &Client{
			BaseURL: "https://api.otter.camp",
			Token:   "token-1",
			OrgID:   "org-1",
			HTTP:    http.DefaultClient,
		}

		_, err := client.GetRoomStats("   ")
		if err == nil || !strings.Contains(err.Error(), "room id is required") {
			t.Fatalf("expected room id validation error, got %v", err)
		}
	})
}
