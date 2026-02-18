package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type EllieIngestionResetResult struct {
	DeletedMemories         int `json:"deleted_memories"`
	DeletedIngestionCursors int `json:"deleted_ingestion_cursors"`
	DeletedDedupReviewed    int `json:"deleted_dedup_reviewed"`
	DeletedDedupCursors     int `json:"deleted_dedup_cursors"`
}

type EllieIngestionResetStore struct {
	db *sql.DB
}

func NewEllieIngestionResetStore(db *sql.DB) *EllieIngestionResetStore {
	return &EllieIngestionResetStore{db: db}
}

// ResetMemoryExtractionState clears Ellie ingestion state so a backfill can be rerun
// without deleting the underlying chat history.
//
// It deletes:
// - `memories` rows created from `chat_messages` ingestion (metadata.source_table='chat_messages')
// - `ellie_ingestion_cursors` rows for the org
// - Ellie dedup cursor/review state (memory ids change after a re-extract)
func (s *EllieIngestionResetStore) ResetMemoryExtractionState(
	ctx context.Context,
	orgID string,
) (EllieIngestionResetResult, error) {
	if s == nil || s.db == nil {
		return EllieIngestionResetResult{}, fmt.Errorf("ellie ingestion reset store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return EllieIngestionResetResult{}, fmt.Errorf("invalid org_id")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EllieIngestionResetResult{}, fmt.Errorf("begin ellie ingestion reset transaction: %w", err)
	}
	defer tx.Rollback()

	result := EllieIngestionResetResult{}

	if result.DeletedDedupReviewed, err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM ellie_dedup_reviewed WHERE org_id = $1`,
		orgID,
	); err != nil {
		return EllieIngestionResetResult{}, err
	}

	if result.DeletedDedupCursors, err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM ellie_dedup_cursors WHERE org_id = $1`,
		orgID,
	); err != nil {
		return EllieIngestionResetResult{}, err
	}

	if result.DeletedIngestionCursors, err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM ellie_ingestion_cursors WHERE org_id = $1`,
		orgID,
	); err != nil {
		return EllieIngestionResetResult{}, err
	}

	// Only delete memories that were extracted from chat history ingestion.
	if result.DeletedMemories, err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM memories
		  WHERE org_id = $1
		    AND metadata->>'source_table' = 'chat_messages'`,
		orgID,
	); err != nil {
		return EllieIngestionResetResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return EllieIngestionResetResult{}, fmt.Errorf("commit ellie ingestion reset transaction: %w", err)
	}

	return result, nil
}

