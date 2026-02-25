package main

import (
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/migrate"
)

func TestMigrationAppliedLine(t *testing.T) {
	line := migrationAppliedLine(migrate.Migration{
		Version: 42,
		Name:    "memory_schema",
	}, 12*time.Millisecond)

	if !strings.Contains(line, "Applying 0042_memory_schema... done (12ms)") {
		t.Fatalf("migrationAppliedLine output = %q", line)
	}
}
