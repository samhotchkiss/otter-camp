package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type MigrationProgressStatus string

const (
	MigrationProgressStatusPending   MigrationProgressStatus = "pending"
	MigrationProgressStatusRunning   MigrationProgressStatus = "running"
	MigrationProgressStatusPaused    MigrationProgressStatus = "paused"
	MigrationProgressStatusCompleted MigrationProgressStatus = "completed"
	MigrationProgressStatusFailed    MigrationProgressStatus = "failed"
)

var validMigrationProgressStatuses = map[MigrationProgressStatus]struct{}{
	MigrationProgressStatusPending:   {},
	MigrationProgressStatusRunning:   {},
	MigrationProgressStatusPaused:    {},
	MigrationProgressStatusCompleted: {},
	MigrationProgressStatusFailed:    {},
}

type MigrationProgress struct {
	ID             string
	OrgID          string
	MigrationType  string
	Status         MigrationProgressStatus
	TotalItems     *int
	ProcessedItems int
	FailedItems    int
	CurrentLabel   string
	StartedAt      *time.Time
	CompletedAt    *time.Time
	Error          *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type StartMigrationProgressInput struct {
	OrgID         string
	MigrationType string
	TotalItems    *int
	CurrentLabel  *string
}

type AdvanceMigrationProgressInput struct {
	OrgID          string
	MigrationType  string
	ProcessedDelta int
	FailedDelta    int
	CurrentLabel   *string
}

type SetMigrationProgressStatusInput struct {
	OrgID         string
	MigrationType string
	Status        MigrationProgressStatus
	Error         *string
	CurrentLabel  *string
}

type MigrationProgressStore struct {
	db *sql.DB
}

func NewMigrationProgressStore(db *sql.DB) *MigrationProgressStore {
	return &MigrationProgressStore{db: db}
}

func (s *MigrationProgressStore) StartPhase(ctx context.Context, input StartMigrationProgressInput) (*MigrationProgress, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("migration progress store is not configured")
	}

	orgID, migrationType, err := normalizeMigrationProgressScope(input.OrgID, input.MigrationType)
	if err != nil {
		return nil, err
	}

	totalItems, err := normalizeMigrationProgressTotal(input.TotalItems)
	if err != nil {
		return nil, err
	}

	progress, err := scanMigrationProgressRow(s.db.QueryRowContext(
		ctx,
		`INSERT INTO migration_progress (
			org_id,
			migration_type,
			status,
			total_items,
			processed_items,
			failed_items,
			current_label,
			started_at,
			completed_at,
			error
		) VALUES (
			$1,
			$2,
			$3,
			$4,
			0,
			0,
			$5,
			NOW(),
			NULL,
			NULL
		)
		ON CONFLICT (org_id, migration_type) DO UPDATE
		SET status = EXCLUDED.status,
		    total_items = EXCLUDED.total_items,
		    processed_items = 0,
		    failed_items = 0,
		    current_label = EXCLUDED.current_label,
		    started_at = NOW(),
		    completed_at = NULL,
		    error = NULL
		RETURNING id, org_id, migration_type, status, total_items, processed_items, failed_items, current_label, started_at, completed_at, error, created_at, updated_at`,
		orgID,
		migrationType,
		MigrationProgressStatusRunning,
		totalItems,
		nullableString(input.CurrentLabel),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to start migration progress phase: %w", err)
	}

	return progress, nil
}

func (s *MigrationProgressStore) GetByType(ctx context.Context, orgID, migrationType string) (*MigrationProgress, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("migration progress store is not configured")
	}

	normalizedOrgID, normalizedType, err := normalizeMigrationProgressScope(orgID, migrationType)
	if err != nil {
		return nil, err
	}

	progress, err := scanMigrationProgressRow(s.db.QueryRowContext(
		ctx,
		`SELECT id, org_id, migration_type, status, total_items, processed_items, failed_items, current_label, started_at, completed_at, error, created_at, updated_at
		 FROM migration_progress
		 WHERE org_id = $1
		   AND migration_type = $2`,
		normalizedOrgID,
		normalizedType,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get migration progress by type: %w", err)
	}

	return progress, nil
}

func (s *MigrationProgressStore) ListByOrg(ctx context.Context, orgID string) ([]MigrationProgress, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("migration progress store is not configured")
	}

	normalizedOrgID := strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(normalizedOrgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, org_id, migration_type, status, total_items, processed_items, failed_items, current_label, started_at, completed_at, error, created_at, updated_at
		 FROM migration_progress
		 WHERE org_id = $1
		 ORDER BY migration_type ASC, created_at ASC`,
		normalizedOrgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list migration progress rows: %w", err)
	}
	defer rows.Close()

	progressRows := make([]MigrationProgress, 0)
	for rows.Next() {
		row, scanErr := scanMigrationProgressRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan migration progress row: %w", scanErr)
		}
		progressRows = append(progressRows, *row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading migration progress rows: %w", err)
	}

	return progressRows, nil
}

func (s *MigrationProgressStore) Advance(ctx context.Context, input AdvanceMigrationProgressInput) (*MigrationProgress, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("migration progress store is not configured")
	}

	orgID, migrationType, err := normalizeMigrationProgressScope(input.OrgID, input.MigrationType)
	if err != nil {
		return nil, err
	}
	if input.ProcessedDelta < 0 {
		return nil, fmt.Errorf("processed_delta must be non-negative")
	}
	if input.FailedDelta < 0 {
		return nil, fmt.Errorf("failed_delta must be non-negative")
	}

	progress, err := scanMigrationProgressRow(s.db.QueryRowContext(
		ctx,
		`UPDATE migration_progress
		    SET processed_items = processed_items + $3,
		        failed_items = failed_items + $4,
		        current_label = COALESCE($5, current_label),
		        status = CASE
		            WHEN status = 'pending' THEN 'running'
		            ELSE status
		        END
		  WHERE org_id = $1
		    AND migration_type = $2
		RETURNING id, org_id, migration_type, status, total_items, processed_items, failed_items, current_label, started_at, completed_at, error, created_at, updated_at`,
		orgID,
		migrationType,
		input.ProcessedDelta,
		input.FailedDelta,
		nullableString(input.CurrentLabel),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("migration progress phase not found: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("failed to advance migration progress: %w", err)
	}

	return progress, nil
}

func (s *MigrationProgressStore) SetStatus(ctx context.Context, input SetMigrationProgressStatusInput) (*MigrationProgress, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("migration progress store is not configured")
	}

	orgID, migrationType, err := normalizeMigrationProgressScope(input.OrgID, input.MigrationType)
	if err != nil {
		return nil, err
	}
	if _, ok := validMigrationProgressStatuses[input.Status]; !ok {
		return nil, fmt.Errorf("invalid status")
	}

	progress, err := scanMigrationProgressRow(s.db.QueryRowContext(
		ctx,
		`UPDATE migration_progress
		    SET status = $3,
		        current_label = COALESCE($4, current_label),
		        error = CASE
		            WHEN $3 = 'failed' THEN COALESCE($5, error)
		            WHEN $3 IN ('running', 'completed') THEN NULL
		            ELSE error
		        END,
		        started_at = CASE
		            WHEN $3 = 'running' AND started_at IS NULL THEN NOW()
		            ELSE started_at
		        END,
		        completed_at = CASE
		            WHEN $3 IN ('completed', 'failed') THEN NOW()
		            ELSE NULL
		        END
		  WHERE org_id = $1
		    AND migration_type = $2
		RETURNING id, org_id, migration_type, status, total_items, processed_items, failed_items, current_label, started_at, completed_at, error, created_at, updated_at`,
		orgID,
		migrationType,
		input.Status,
		nullableString(input.CurrentLabel),
		nullableString(input.Error),
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("migration progress phase not found: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("failed to set migration progress status: %w", err)
	}

	return progress, nil
}

func (s *MigrationProgressStore) UpdateStatusByOrg(
	ctx context.Context,
	orgID string,
	fromStatus MigrationProgressStatus,
	toStatus MigrationProgressStatus,
) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("migration progress store is not configured")
	}

	normalizedOrgID := strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(normalizedOrgID) {
		return 0, fmt.Errorf("invalid org_id")
	}
	if _, ok := validMigrationProgressStatuses[fromStatus]; !ok {
		return 0, fmt.Errorf("invalid from_status")
	}
	if _, ok := validMigrationProgressStatuses[toStatus]; !ok {
		return 0, fmt.Errorf("invalid to_status")
	}

	updateResult, err := s.db.ExecContext(
		ctx,
		`UPDATE migration_progress
		    SET status = $3,
		        error = CASE
		            WHEN $3 = 'running' THEN NULL
		            ELSE error
		        END,
		        started_at = CASE
		            WHEN $3 = 'running' AND started_at IS NULL THEN NOW()
		            ELSE started_at
		        END,
		        completed_at = CASE
		            WHEN $3 IN ('completed', 'failed') THEN NOW()
		            ELSE NULL
		        END
		  WHERE org_id = $1
		    AND status = $2`,
		normalizedOrgID,
		fromStatus,
		toStatus,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to update migration progress status by org: %w", err)
	}

	rowsAffected, err := updateResult.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read updated migration progress row count: %w", err)
	}

	return int(rowsAffected), nil
}

func (s *MigrationProgressStore) DeleteByOrgAndTypes(
	ctx context.Context,
	orgID string,
	migrationTypes []string,
) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("migration progress store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return 0, fmt.Errorf("invalid org_id")
	}
	if len(migrationTypes) == 0 {
		return 0, nil
	}

	placeholders := make([]string, 0, len(migrationTypes))
	args := make([]any, 0, len(migrationTypes)+1)
	args = append(args, orgID)
	for i, phaseType := range migrationTypes {
		trimmed := strings.TrimSpace(phaseType)
		if trimmed == "" {
			continue
		}
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+2))
		args = append(args, trimmed)
	}
	if len(placeholders) == 0 {
		return 0, nil
	}

	query := fmt.Sprintf(
		`DELETE FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type IN (%s)`,
		strings.Join(placeholders, ","),
	)
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to delete migration progress rows: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read deleted migration progress row count: %w", err)
	}
	return int(rowsAffected), nil
}

type migrationProgressRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanMigrationProgressRow(scanner migrationProgressRowScanner) (*MigrationProgress, error) {
	var (
		progress     MigrationProgress
		totalItems   sql.NullInt64
		currentLabel sql.NullString
		startedAt    sql.NullTime
		completedAt  sql.NullTime
		lastError    sql.NullString
		status       string
	)

	if err := scanner.Scan(
		&progress.ID,
		&progress.OrgID,
		&progress.MigrationType,
		&status,
		&totalItems,
		&progress.ProcessedItems,
		&progress.FailedItems,
		&currentLabel,
		&startedAt,
		&completedAt,
		&lastError,
		&progress.CreatedAt,
		&progress.UpdatedAt,
	); err != nil {
		return nil, err
	}

	progress.Status = MigrationProgressStatus(status)
	if totalItems.Valid {
		value := int(totalItems.Int64)
		progress.TotalItems = &value
	}
	if currentLabel.Valid {
		progress.CurrentLabel = currentLabel.String
	}
	if startedAt.Valid {
		utc := startedAt.Time.UTC()
		progress.StartedAt = &utc
	}
	if completedAt.Valid {
		utc := completedAt.Time.UTC()
		progress.CompletedAt = &utc
	}
	if lastError.Valid {
		errValue := lastError.String
		progress.Error = &errValue
	}
	progress.CreatedAt = progress.CreatedAt.UTC()
	progress.UpdatedAt = progress.UpdatedAt.UTC()

	return &progress, nil
}

func normalizeMigrationProgressScope(orgID, migrationType string) (string, string, error) {
	normalizedOrgID := strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(normalizedOrgID) {
		return "", "", fmt.Errorf("invalid org_id")
	}

	normalizedType := strings.TrimSpace(strings.ToLower(migrationType))
	if normalizedType == "" {
		return "", "", fmt.Errorf("migration_type is required")
	}

	return normalizedOrgID, normalizedType, nil
}

func normalizeMigrationProgressTotal(totalItems *int) (interface{}, error) {
	if totalItems == nil {
		return nil, nil
	}
	if *totalItems < 0 {
		return nil, fmt.Errorf("total_items must be non-negative")
	}
	return *totalItems, nil
}
