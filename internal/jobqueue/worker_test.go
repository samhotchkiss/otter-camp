package jobqueue

import (
	"strings"
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: 1 * time.Second},
		{attempts: 1, want: 1 * time.Second},
		{attempts: 2, want: 2 * time.Second},
		{attempts: 3, want: 4 * time.Second},
		{attempts: 20, want: 5 * time.Minute},
	}

	for _, tc := range tests {
		if got := backoffDelay(tc.attempts); got != tc.want {
			t.Fatalf("backoffDelay(%d) = %s, want %s", tc.attempts, got, tc.want)
		}
	}
}

func TestBuildWorkerIDFormat(t *testing.T) {
	id := buildWorkerID()
	parts := strings.Split(id, "-")
	if len(parts) < 3 {
		t.Fatalf("worker id %q should contain hostname, pid, uuid", id)
	}
}
