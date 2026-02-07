package syncmetrics

import (
	"testing"
	"time"
)

func TestSnapshotCapturesLifecycleAndQuotaMetrics(t *testing.T) {
	ResetForTests()

	RecordJobPicked("issue_import")
	RecordJobFailure("issue_import", true, false)
	RecordJobFailure("issue_import", false, true)
	RecordJobCompleted("issue_import", 250*time.Millisecond)
	RecordDeadLetterReplay("issue_import")

	resetAt := time.Date(2026, 2, 6, 14, 0, 0, 0, time.UTC)
	RecordQuota("issue_import", 5000, 4900, resetAt)
	RecordThrottle("issue_import")

	snapshot := SnapshotNow()
	issueMetrics, ok := snapshot.Jobs["issue_import"]
	if !ok {
		t.Fatalf("expected issue_import metrics")
	}
	if issueMetrics.PickedTotal != 1 {
		t.Fatalf("expected picked_total=1, got %d", issueMetrics.PickedTotal)
	}
	if issueMetrics.SuccessTotal != 1 {
		t.Fatalf("expected success_total=1, got %d", issueMetrics.SuccessTotal)
	}
	if issueMetrics.FailureTotal != 2 {
		t.Fatalf("expected failure_total=2, got %d", issueMetrics.FailureTotal)
	}
	if issueMetrics.RetryTotal != 1 {
		t.Fatalf("expected retry_total=1, got %d", issueMetrics.RetryTotal)
	}
	if issueMetrics.DeadLetterTotal != 1 {
		t.Fatalf("expected dead_letter_total=1, got %d", issueMetrics.DeadLetterTotal)
	}
	if issueMetrics.ReplayTotal != 1 {
		t.Fatalf("expected replay_total=1, got %d", issueMetrics.ReplayTotal)
	}
	if issueMetrics.TotalLatencyMillis != 250 {
		t.Fatalf("expected latency=250ms, got %d", issueMetrics.TotalLatencyMillis)
	}

	quotaMetrics, ok := snapshot.Quota["issue_import"]
	if !ok {
		t.Fatalf("expected issue_import quota metrics")
	}
	if quotaMetrics.Limit != 5000 || quotaMetrics.Remaining != 4900 {
		t.Fatalf("unexpected quota metrics: %+v", quotaMetrics)
	}
	if !quotaMetrics.ResetAt.Equal(resetAt) {
		t.Fatalf("expected reset_at=%s, got %s", resetAt, quotaMetrics.ResetAt)
	}
	if quotaMetrics.ThrottleEvents != 1 {
		t.Fatalf("expected throttle_events=1, got %d", quotaMetrics.ThrottleEvents)
	}
}
