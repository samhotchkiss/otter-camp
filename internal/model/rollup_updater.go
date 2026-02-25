package model

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type runTokenTx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type runTokenTxBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (runTokenTx, error)
}

type pgxRunTokenTxBeginner struct {
	pool *pgxpool.Pool
}

func (b pgxRunTokenTxBeginner) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (runTokenTx, error) {
	tx, err := b.pool.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

type RollupUpdater struct {
	txBeginner runTokenTxBeginner
}

func NewRollupUpdater(pool *pgxpool.Pool) *RollupUpdater {
	if pool == nil {
		return &RollupUpdater{}
	}
	return &RollupUpdater{txBeginner: pgxRunTokenTxBeginner{pool: pool}}
}

func (u *RollupUpdater) UpdateRunTokenCounts(ctx context.Context, invocation repo.ModelInvocation) error {
	if u == nil || u.txBeginner == nil {
		return fmt.Errorf("rollup updater is not configured")
	}
	if invocation.RunID == nil && invocation.RunStepID == nil && invocation.RunAttemptID == nil {
		return nil
	}

	inputTokens := intPointerValue(invocation.InputTokens)
	outputTokens := intPointerValue(invocation.OutputTokens)

	tx, err := u.txBeginner.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if invocation.RunID != nil {
		if err := incrementRunTokens(ctx, tx, "run", *invocation.RunID, inputTokens, outputTokens); err != nil {
			return err
		}
	}
	if invocation.RunStepID != nil {
		if err := incrementRunTokens(ctx, tx, "run_step", *invocation.RunStepID, inputTokens, outputTokens); err != nil {
			return err
		}
	}
	if invocation.RunAttemptID != nil {
		if err := incrementRunTokens(ctx, tx, "run_attempt", *invocation.RunAttemptID, inputTokens, outputTokens); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

func incrementRunTokens(ctx context.Context, tx runTokenTx, table string, id uuid.UUID, inputTokens, outputTokens int) error {
	if id == uuid.Nil {
		return nil
	}
	if table != "run" && table != "run_step" && table != "run_attempt" {
		return fmt.Errorf("unsupported rollup table %q", table)
	}
	_, err := tx.Exec(ctx, `
		UPDATE `+table+`
		SET input_tokens = input_tokens + $1,
		    output_tokens = output_tokens + $2
		WHERE id = $3
	`, inputTokens, outputTokens, id)
	return err
}

func intPointerValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
