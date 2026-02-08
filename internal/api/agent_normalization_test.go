package api

import (
	"testing"
	"time"
)

func TestNormalizeCurrentTask(t *testing.T) {
	cases := []struct {
		input  string
		expect string
	}{
		{"", ""},
		{"   ", ""},
		{"HEARTBEAT_OK", ""},
		{"heartbeat_ok", ""},
		{"Heartbeat OK", ""},
		{"HEARTBEAT", ""},
		{"HEARTBEAT_FAIL", ""},
		{"slack:#engineering", "Active in #engineering"},
		{"slack:g-c0abhd38u05-thread-1770304196.509249", "Thread in #essie"},
		{"webchat:g-agent-three-stones-main", "Active in Three Stones webchat"},
		{"Slack thread #engineering: :building_construction: Derek: Checked deployment status", "Slack thread #engineering: Derek: Checked deployment status"},
		{"Draft launch plan", "Draft launch plan"},
	}

	for _, tc := range cases {
		if got := normalizeCurrentTask(tc.input); got != tc.expect {
			t.Fatalf("normalizeCurrentTask(%q) = %q, want %q", tc.input, got, tc.expect)
		}
	}
}

func TestAgentCurrentTaskHumanized(t *testing.T) {
	got := normalizeCurrentTask("slack:#essie")
	if got != "Active in #essie" {
		t.Fatalf("normalizeCurrentTask should humanize slack channel, got %q", got)
	}
}

func TestNormalizeLastSeenTimestamp(t *testing.T) {
	if got := normalizeLastSeenTimestamp(time.Time{}); got != "" {
		t.Fatalf("expected empty string for zero time, got %q", got)
	}

	if got := normalizeLastSeenTimestamp(time.Unix(0, 0)); got != "" {
		t.Fatalf("expected empty string for epoch, got %q", got)
	}

	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := normalizeLastSeenTimestamp(now); got != "2024-01-02T03:04:05Z" {
		t.Fatalf("expected RFC3339 timestamp, got %q", got)
	}
}
