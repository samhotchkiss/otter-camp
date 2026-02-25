package jobs

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuildTraceSpanPartitionDDLUsesLiteralTimestamps(t *testing.T) {
	start := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	partitionName := "trace_span_p_20260304"

	sql := buildTraceSpanPartitionDDL(partitionName, start, end)

	if strings.Contains(sql, "$1") || strings.Contains(sql, "$2") {
		t.Fatalf("partition SQL still contains parameter placeholders: %q", sql)
	}

	expectedBounds := fmt.Sprintf(
		"FOR VALUES FROM ('%s') TO ('%s')",
		start.Format(tracePartitionTimestampLayout),
		end.Format(tracePartitionTimestampLayout),
	)
	if !strings.Contains(sql, expectedBounds) {
		t.Fatalf("partition SQL = %q, want bounds %q", sql, expectedBounds)
	}
}
