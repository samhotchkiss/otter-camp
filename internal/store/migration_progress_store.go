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
