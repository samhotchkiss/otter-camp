package worker

import (
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/db"
)

func TestWorkerDBMaxConnsDefaultsAboveGlobalFloor(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "")

	got, err := workerDBMaxConns()
	if err != nil {
		t.Fatalf("workerDBMaxConns returned error: %v", err)
	}
	if want := maxInt32(db.DefaultMaxConns, defaultWorkerMaxConns); got != want {
		t.Fatalf("worker max conns = %d, want %d", got, want)
	}
}

func TestWorkerDBMaxConnsPrefersWorkerOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "41")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "24")

	got, err := workerDBMaxConns()
	if err != nil {
		t.Fatalf("workerDBMaxConns returned error: %v", err)
	}
	if got != 41 {
		t.Fatalf("worker max conns = %d, want 41", got)
	}
}

func TestWorkerDBMaxConnsFallsBackToGlobalOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "24")

	got, err := workerDBMaxConns()
	if err != nil {
		t.Fatalf("workerDBMaxConns returned error: %v", err)
	}
	if got != 24 {
		t.Fatalf("worker max conns = %d, want 24", got)
	}
}

func TestWorkerDBMaxConnsRejectsInvalidOverride(t *testing.T) {
	t.Setenv("OTTERCAMP_WORKER_DB_MAX_CONNS", "bad")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "")

	if _, err := workerDBMaxConns(); err == nil {
		t.Fatal("expected error for invalid worker override")
	}
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
