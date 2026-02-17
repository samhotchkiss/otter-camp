package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	defaultOpenClawHistoryFailureListLimit = 100
	maxOpenClawHistoryFailureListLimit     = 1000
)

type OpenClawHistoryImportFailure struct {
	ID                 string
	OrgID              string
	MigrationType      string
	BatchID            string
	AgentSlug          string
	SessionID          string
	EventID            string
	SessionPath        string
	Line               int
	MessageIDCandidate string
	ErrorReason        string
	ErrorMessage       string
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	AttemptCount       int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type UpsertOpenClawHistoryFailureInput struct {
	OrgID              string
	MigrationType      string
	BatchID            string
	AgentSlug          string
	SessionID          string
	EventID            string
	SessionPath        string
	Line               int
	MessageIDCandidate string
	ErrorReason        string
	ErrorMessage       string
}

type ListOpenClawHistoryFailureOptions struct {
	MigrationType string
	Limit         int
}

type OpenClawHistoryFailureLedgerStore struct {
	db *sql.DB
}

func NewOpenClawHistoryFailureLedgerStore(db *sql.DB) *OpenClawHistoryFailureLedgerStore {
	return &OpenClawHistoryFailureLedgerStore{db: db}
}

func (s *OpenClawHistoryFailureLedgerStore) Upsert(
	ctx context.Context,
	input UpsertOpenClawHistoryFailureInput,
) (*OpenClawHistoryImportFailure, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("openclaw history failure ledger store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	migrationType := strings.TrimSpace(input.MigrationType)
	if migrationType == "" {
		return nil, fmt.Errorf("migration_type is required")
	}
	agentSlug := strings.TrimSpace(input.AgentSlug)
	if agentSlug == "" {
		return nil, fmt.Errorf("agent_slug is required")
	}
	eventID := strings.TrimSpace(input.EventID)
	if eventID == "" {
		return nil, fmt.Errorf("event_id is required")
	}
	if input.Line < 0 {
		return nil, fmt.Errorf("line must be >= 0")
	}
	errorReason := strings.TrimSpace(input.ErrorReason)
	if errorReason == "" {
		return nil, fmt.Errorf("error_reason is required")
	}
	errorMessage := strings.TrimSpace(input.ErrorMessage)
	if errorMessage == "" {
		return nil, fmt.Errorf("error_message is required")
	}

	row, err := scanOpenClawHistoryImportFailureRow(
		s.db.QueryRowContext(
			ctx,
			`INSERT INTO openclaw_history_import_failures (
				org_id,
				migration_type,
				batch_id,
				agent_slug,
				session_id,
				event_id,
				session_path,
				line,
				message_id_candidate,
				error_reason,
				error_message
			) VALUES (
				$1,
				$2,
				$3,
				$4,
				$5,
				$6,
				$7,
				$8,
				$9,
				$10,
				$11
			)
			ON CONFLICT (
				org_id,
				migration_type,
				agent_slug,
				session_id,
				event_id,
				session_path,
				line
			) DO UPDATE
			SET batch_id = EXCLUDED.batch_id,
			    message_id_candidate = EXCLUDED.message_id_candidate,
			    error_reason = EXCLUDED.error_reason,
			    error_message = EXCLUDED.error_message,
			    attempt_count = openclaw_history_import_failures.attempt_count + 1,
			    last_seen_at = NOW(),
			    updated_at = NOW()
			RETURNING
				id::text,
				org_id::text,
				migration_type,
				batch_id,
				agent_slug,
				session_id,
				event_id,
				session_path,
				line,
				message_id_candidate,
				error_reason,
				error_message,
				first_seen_at,
				last_seen_at,
				attempt_count,
				created_at,
				updated_at`,
			orgID,
			migrationType,
			strings.TrimSpace(input.BatchID),
			agentSlug,
			strings.TrimSpace(input.SessionID),
			eventID,
			strings.TrimSpace(input.SessionPath),
			input.Line,
			strings.TrimSpace(input.MessageIDCandidate),
			errorReason,
			errorMessage,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert openclaw history import failure: %w", err)
	}
	return row, nil
}

func (s *OpenClawHistoryFailureLedgerStore) ListByOrg(
	ctx context.Context,
	orgID string,
	opts ListOpenClawHistoryFailureOptions,
) ([]OpenClawHistoryImportFailure, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("openclaw history failure ledger store is not configured")
	}

	normalizedOrgID := strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(normalizedOrgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultOpenClawHistoryFailureListLimit
	}
	if limit > maxOpenClawHistoryFailureListLimit {
		limit = maxOpenClawHistoryFailureListLimit
	}
	migrationType := strings.TrimSpace(opts.MigrationType)
	args := []interface{}{normalizedOrgID}

	query := `SELECT
		id::text,
		org_id::text,
		migration_type,
		batch_id,
		agent_slug,
		session_id,
		event_id,
		session_path,
		line,
		message_id_candidate,
		error_reason,
		error_message,
		first_seen_at,
		last_seen_at,
		attempt_count,
		created_at,
		updated_at
	FROM openclaw_history_import_failures
	WHERE org_id = $1`
	if migrationType != "" {
		query += " AND migration_type = $2"
		args = append(args, migrationType)
		query += " ORDER BY last_seen_at DESC, first_seen_at DESC, id ASC"
		query += " LIMIT $3"
		args = append(args, limit)
	} else {
		query += " ORDER BY last_seen_at DESC, first_seen_at DESC, id ASC"
		query += " LIMIT $2"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list openclaw history import failures: %w", err)
	}
	defer rows.Close()

	out := make([]OpenClawHistoryImportFailure, 0)
	for rows.Next() {
		row, scanErr := scanOpenClawHistoryImportFailureRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan openclaw history import failure row: %w", scanErr)
		}
		out = append(out, *row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading openclaw history import failures: %w", err)
	}
	return out, nil
}

type openClawHistoryImportFailureRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanOpenClawHistoryImportFailureRow(
	scanner openClawHistoryImportFailureRowScanner,
) (*OpenClawHistoryImportFailure, error) {
	var row OpenClawHistoryImportFailure
	if err := scanner.Scan(
		&row.ID,
		&row.OrgID,
		&row.MigrationType,
		&row.BatchID,
		&row.AgentSlug,
		&row.SessionID,
		&row.EventID,
		&row.SessionPath,
		&row.Line,
		&row.MessageIDCandidate,
		&row.ErrorReason,
		&row.ErrorMessage,
		&row.FirstSeenAt,
		&row.LastSeenAt,
		&row.AttemptCount,
		&row.CreatedAt,
		&row.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &row, nil
}
