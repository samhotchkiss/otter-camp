package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

type OpenClawMigrationResetInput struct {
	OrgID              string
	OpenClawPhaseTypes []string
}

type OpenClawMigrationResetResult struct {
	PausedPhases        int
	ProgressRowsDeleted int
	Deleted             map[string]int
	TotalDeleted        int
}

type OpenClawMigrationResetStore struct {
	db *sql.DB
}

func NewOpenClawMigrationResetStore(db *sql.DB) *OpenClawMigrationResetStore {
	return &OpenClawMigrationResetStore{db: db}
}

func (s *OpenClawMigrationResetStore) Reset(
	ctx context.Context,
	input OpenClawMigrationResetInput,
) (OpenClawMigrationResetResult, error) {
	if s == nil || s.db == nil {
		return OpenClawMigrationResetResult{}, fmt.Errorf("openclaw migration reset store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return OpenClawMigrationResetResult{}, fmt.Errorf("invalid org_id")
	}

	phaseTypes := normalizeOpenClawPhaseTypes(input.OpenClawPhaseTypes)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OpenClawMigrationResetResult{}, fmt.Errorf("failed to begin openclaw migration reset transaction: %w", err)
	}
	defer tx.Rollback()

	pausedPhases := 0
	progressRowsDeleted := 0
	if len(phaseTypes) > 0 {
		pausedPhases, err = executeOpenClawMigrationResetCount(
			ctx,
			tx,
			`UPDATE migration_progress
			    SET status = 'paused'
			  WHERE org_id = $1
			    AND status = 'running'
			    AND migration_type = ANY($2)`,
			orgID,
			pq.Array(phaseTypes),
		)
		if err != nil {
			return OpenClawMigrationResetResult{}, err
		}

		progressRowsDeleted, err = executeOpenClawMigrationResetCount(
			ctx,
			tx,
			`DELETE FROM migration_progress
			  WHERE org_id = $1
			    AND migration_type = ANY($2)`,
			orgID,
			pq.Array(phaseTypes),
		)
		if err != nil {
			return OpenClawMigrationResetResult{}, err
		}
	}

	deleted := map[string]int{
		"chat_messages":         0,
		"conversations":         0,
		"room_participants":     0,
		"rooms":                 0,
		"memories":              0,
		"ellie_memory_taxonomy": 0,
		"ellie_taxonomy_nodes":  0,
		"ellie_project_docs":    0,
	}

	if deleted["chat_messages"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM chat_messages WHERE org_id = $1`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	if deleted["conversations"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM conversations WHERE org_id = $1`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	if deleted["room_participants"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM room_participants WHERE org_id = $1`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	if deleted["rooms"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM rooms WHERE org_id = $1`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	if deleted["ellie_memory_taxonomy"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM ellie_memory_taxonomy emt
		  WHERE EXISTS (
		          SELECT 1
		            FROM memories m
		           WHERE m.id = emt.memory_id
		             AND m.org_id = $1
		        )
		     OR EXISTS (
		          SELECT 1
		            FROM ellie_taxonomy_nodes n
		           WHERE n.id = emt.node_id
		             AND n.org_id = $1
		        )`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	if deleted["memories"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM memories WHERE org_id = $1`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	if deleted["ellie_taxonomy_nodes"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM ellie_taxonomy_nodes WHERE org_id = $1`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	if deleted["ellie_project_docs"], err = executeOpenClawMigrationResetCount(
		ctx,
		tx,
		`DELETE FROM ellie_project_docs WHERE org_id = $1`,
		orgID,
	); err != nil {
		return OpenClawMigrationResetResult{}, err
	}

	totalDeleted := 0
	for _, count := range deleted {
		totalDeleted += count
	}

	if err := tx.Commit(); err != nil {
		return OpenClawMigrationResetResult{}, fmt.Errorf("failed to commit openclaw migration reset transaction: %w", err)
	}

	return OpenClawMigrationResetResult{
		PausedPhases:        pausedPhases,
		ProgressRowsDeleted: progressRowsDeleted,
		Deleted:             deleted,
		TotalDeleted:        totalDeleted,
	}, nil
}

func normalizeOpenClawPhaseTypes(phaseTypes []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(phaseTypes))
	for _, phaseType := range phaseTypes {
		trimmed := strings.TrimSpace(phaseType)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func executeOpenClawMigrationResetCount(
	ctx context.Context,
	tx *sql.Tx,
	query string,
	args ...interface{},
) (int, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed openclaw migration reset query: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed reading openclaw migration reset row count: %w", err)
	}
	return int(rowsAffected), nil
}
