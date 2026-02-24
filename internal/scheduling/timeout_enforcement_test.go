package scheduling

import (
	"context"
	"testing"
	"time"
)

func TestMaxDurationCheckerInterface(t *testing.T) {
	var checker MaxDurationChecker = NoopMaxDurationChecker{}

	items, err := checker.ListTimedOutScheduledTasks(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("ListTimedOutScheduledTasks: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("timed out task count = %d, want 0", len(items))
	}
}
