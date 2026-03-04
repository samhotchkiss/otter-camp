package testdb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeAdmin struct {
	dropErrs       []error
	sessionBatches [][]int32
	queryErr       error

	terminateCalls int
	dropCalls      int
	queryCalls     int
}

func (f *fakeAdmin) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "pg_terminate_backend") {
		f.terminateCalls++
		return pgconn.CommandTag{}, nil
	}
	if strings.Contains(sql, "DROP DATABASE") {
		f.dropCalls++
		if len(f.dropErrs) == 0 {
			return pgconn.CommandTag{}, nil
		}
		err := f.dropErrs[0]
		f.dropErrs = f.dropErrs[1:]
		return pgconn.CommandTag{}, err
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeAdmin) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	var pids []int32
	if f.queryCalls < len(f.sessionBatches) {
		pids = append([]int32(nil), f.sessionBatches[f.queryCalls]...)
	}
	f.queryCalls++
	return fakeRow{pids: pids, err: f.queryErr}
}

type fakeRow struct {
	pids []int32
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	target, ok := dest[0].(*[]int32)
	if !ok {
		return errors.New("expected *[]int32 scan target")
	}
	*target = append((*target)[:0], r.pids...)
	return nil
}

func TestDropDatabaseWithRetryRetriesTransientErrors(t *testing.T) {
	admin := &fakeAdmin{
		dropErrs: []error{
			&pgconn.PgError{Code: dropRetryObjectInUseSQLState, Message: "database is being accessed by other users"},
			context.DeadlineExceeded,
			nil,
		},
		sessionBatches: [][]int32{{101, 102}, {201}, {}},
	}

	cfg := dropRetryConfig{
		MaxAttempts:    5,
		AttemptTimeout: time.Second,
		InitialBackoff: 0,
		MaxBackoff:     0,
	}
	if err := dropDatabaseWithRetry(context.Background(), admin, "otter_test_retry", cfg); err != nil {
		t.Fatalf("dropDatabaseWithRetry() error = %v", err)
	}
	if admin.dropCalls != 3 {
		t.Fatalf("drop calls = %d, want 3", admin.dropCalls)
	}
	if admin.terminateCalls != 3 {
		t.Fatalf("terminate calls = %d, want 3", admin.terminateCalls)
	}
}

func TestDropDatabaseWithRetryFailsFastOnPermanentError(t *testing.T) {
	admin := &fakeAdmin{
		dropErrs:       []error{errors.New("permission denied")},
		sessionBatches: [][]int32{{88, 99}},
	}

	cfg := dropRetryConfig{
		MaxAttempts:    5,
		AttemptTimeout: time.Second,
		InitialBackoff: 0,
		MaxBackoff:     0,
	}
	err := dropDatabaseWithRetry(context.Background(), admin, "otter_test_perm", cfg)
	if err == nil {
		t.Fatal("dropDatabaseWithRetry() error = nil, want non-nil")
	}
	if admin.dropCalls != 1 {
		t.Fatalf("drop calls = %d, want 1", admin.dropCalls)
	}
	if !strings.Contains(err.Error(), "active_pids=[88 99]") {
		t.Fatalf("error missing active pid telemetry: %v", err)
	}
}

func TestDropDatabaseWithRetryExhaustedRetriesIncludesTelemetry(t *testing.T) {
	admin := &fakeAdmin{
		dropErrs: []error{
			&pgconn.PgError{Code: dropRetryObjectInUseSQLState, Message: "database is being accessed by other users"},
			&pgconn.PgError{Code: dropRetryObjectInUseSQLState, Message: "database is being accessed by other users"},
			&pgconn.PgError{Code: dropRetryObjectInUseSQLState, Message: "database is being accessed by other users"},
		},
		sessionBatches: [][]int32{{11}, {22, 33}, {44}},
	}

	cfg := dropRetryConfig{
		MaxAttempts:    3,
		AttemptTimeout: time.Second,
		InitialBackoff: 0,
		MaxBackoff:     0,
	}
	err := dropDatabaseWithRetry(context.Background(), admin, "otter_test_exhaust", cfg)
	if err == nil {
		t.Fatal("dropDatabaseWithRetry() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "failed after 3 attempt(s)") {
		t.Fatalf("error missing attempts detail: %v", err)
	}
	if !strings.Contains(err.Error(), "active_pids=[44]") {
		t.Fatalf("error missing latest active pid telemetry: %v", err)
	}
}
