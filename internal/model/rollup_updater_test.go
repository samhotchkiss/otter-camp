package model

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRollupUpdaterUpdateRunTokenCountsUpdatesAllLevelsInSingleTransaction(t *testing.T) {
	runID := uuid.New()
	runStepID := uuid.New()
	runAttemptID := uuid.New()
	input := 120
	output := 45

	tx := &fakeRunTokenTx{}
	updater := &RollupUpdater{txBeginner: &fakeRunTokenTxBeginner{tx: tx}}

	err := updater.UpdateRunTokenCounts(context.Background(), repo.ModelInvocation{
		RunID:        &runID,
		RunStepID:    &runStepID,
		RunAttemptID: &runAttemptID,
		InputTokens:  &input,
		OutputTokens: &output,
	})
	if err != nil {
		t.Fatalf("UpdateRunTokenCounts: %v", err)
	}

	if tx.beginCount != 1 {
		t.Fatalf("begin count = %d, want 1", tx.beginCount)
	}
	if !tx.committed {
		t.Fatal("transaction commit not called")
	}
	if tx.rolledBack {
		t.Fatal("transaction should not rollback on success")
	}
	if len(tx.execs) != 3 {
		t.Fatalf("exec calls = %d, want 3", len(tx.execs))
	}
	if !strings.Contains(tx.execs[0].sql, "UPDATE run") {
		t.Fatalf("first statement = %q, want UPDATE run", tx.execs[0].sql)
	}
	if !strings.Contains(tx.execs[1].sql, "UPDATE run_step") {
		t.Fatalf("second statement = %q, want UPDATE run_step", tx.execs[1].sql)
	}
	if !strings.Contains(tx.execs[2].sql, "UPDATE run_attempt") {
		t.Fatalf("third statement = %q, want UPDATE run_attempt", tx.execs[2].sql)
	}
}

func TestRollupUpdaterUpdateRunTokenCountsSkipsRunAttemptWhenNil(t *testing.T) {
	runID := uuid.New()
	runStepID := uuid.New()
	input := 10
	output := 5

	tx := &fakeRunTokenTx{}
	updater := &RollupUpdater{txBeginner: &fakeRunTokenTxBeginner{tx: tx}}

	err := updater.UpdateRunTokenCounts(context.Background(), repo.ModelInvocation{
		RunID:        &runID,
		RunStepID:    &runStepID,
		InputTokens:  &input,
		OutputTokens: &output,
	})
	if err != nil {
		t.Fatalf("UpdateRunTokenCounts: %v", err)
	}

	if len(tx.execs) != 2 {
		t.Fatalf("exec calls = %d, want 2", len(tx.execs))
	}
	for _, call := range tx.execs {
		if strings.Contains(call.sql, "run_attempt") {
			t.Fatalf("unexpected run_attempt update: %q", call.sql)
		}
	}
}

type fakeRunTokenTxBeginner struct {
	tx *fakeRunTokenTx
}

func (f *fakeRunTokenTxBeginner) BeginTx(context.Context, pgx.TxOptions) (runTokenTx, error) {
	f.tx.beginCount++
	return f.tx, nil
}

type fakeRunTokenTx struct {
	beginCount int
	committed  bool
	rolledBack bool
	execs      []fakeRunTokenExec
}

type fakeRunTokenExec struct {
	sql  string
	args []any
}

func (f *fakeRunTokenTx) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	f.execs = append(f.execs, fakeRunTokenExec{sql: sql, args: arguments})
	return pgconn.CommandTag{}, nil
}

func (f *fakeRunTokenTx) Commit(context.Context) error {
	f.committed = true
	return nil
}

func (f *fakeRunTokenTx) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}
