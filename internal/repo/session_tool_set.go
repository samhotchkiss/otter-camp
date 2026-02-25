package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SessionToolSet struct {
	ID            uuid.UUID
	SessionID     uuid.UUID
	AgentID       uuid.UUID
	ResolvedAt    time.Time
	ToolSet       json.RawMessage
	InvalidatedAt *time.Time
}

type SessionToolSetRepo struct {
	pool *pgxpool.Pool
}

func NewSessionToolSetRepo(pool *pgxpool.Pool) *SessionToolSetRepo {
	return &SessionToolSetRepo{pool: pool}
}

func (r *SessionToolSetRepo) Create(ctx context.Context, sessionID, agentID uuid.UUID, toolSet json.RawMessage) (*SessionToolSet, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO session_tool_set (
			session_id,
			agent_id,
			tool_set
		)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, session_id, agent_id, resolved_at, tool_set, invalidated_at
	`, sessionID, agentID, normalizeSessionToolSetJSON(toolSet))

	item, err := scanSessionToolSet(row)
	if err != nil {
		return nil, mapDBError(err)
	}
	return &item, nil
}

func (r *SessionToolSetRepo) GetActive(ctx context.Context, sessionID, agentID uuid.UUID) (*SessionToolSet, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, session_id, agent_id, resolved_at, tool_set, invalidated_at
		FROM session_tool_set
		WHERE session_id = $1
		  AND agent_id = $2
		  AND invalidated_at IS NULL
		ORDER BY resolved_at DESC, id DESC
		LIMIT 1
	`, sessionID, agentID)

	item, err := scanSessionToolSet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, mapDBError(err)
	}
	return &item, nil
}

func (r *SessionToolSetRepo) Invalidate(ctx context.Context, sessionID, agentID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE session_tool_set
		SET invalidated_at = now()
		WHERE session_id = $1
		  AND agent_id = $2
		  AND invalidated_at IS NULL
	`, sessionID, agentID)
	return mapDBError(err)
}

func (r *SessionToolSetRepo) ReplaceToolSet(ctx context.Context, sessionID, agentID uuid.UUID, newToolSet json.RawMessage) (*SessionToolSet, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
		UPDATE session_tool_set
		SET invalidated_at = now()
		WHERE session_id = $1
		  AND agent_id = $2
		  AND invalidated_at IS NULL
	`, sessionID, agentID); err != nil {
		return nil, mapDBError(err)
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO session_tool_set (
			session_id,
			agent_id,
			tool_set
		)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, session_id, agent_id, resolved_at, tool_set, invalidated_at
	`, sessionID, agentID, normalizeSessionToolSetJSON(newToolSet))
	item, err := scanSessionToolSet(row)
	if err != nil {
		return nil, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, mapDBError(err)
	}
	return &item, nil
}

func (r *SessionToolSetRepo) ListActiveByOrganization(ctx context.Context, organizationID uuid.UUID) ([]SessionToolSet, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT sts.id, sts.session_id, sts.agent_id, sts.resolved_at, sts.tool_set, sts.invalidated_at
		FROM session_tool_set sts
		JOIN chat_session s ON s.id = sts.session_id
		WHERE s.organization_id = $1
		  AND sts.invalidated_at IS NULL
		ORDER BY sts.resolved_at ASC, sts.id ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	sets := make([]SessionToolSet, 0)
	for rows.Next() {
		item, scanErr := scanSessionToolSet(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		sets = append(sets, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return sets, nil
}

func normalizeSessionToolSetJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`[]`)
	}
	if !json.Valid(value) {
		return json.RawMessage(`[]`)
	}
	return value
}

func scanSessionToolSet(row pgx.Row) (SessionToolSet, error) {
	var item SessionToolSet
	if err := row.Scan(
		&item.ID,
		&item.SessionID,
		&item.AgentID,
		&item.ResolvedAt,
		&item.ToolSet,
		&item.InvalidatedAt,
	); err != nil {
		return SessionToolSet{}, err
	}
	if len(item.ToolSet) == 0 {
		item.ToolSet = json.RawMessage(`[]`)
	}
	return item, nil
}
