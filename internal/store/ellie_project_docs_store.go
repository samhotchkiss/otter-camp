package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type EllieProjectDoc struct {
	ID            string
	OrgID         string
	ProjectID     string
	FilePath      string
	Title         string
	Summary       string
	ContentHash   string
	LastScannedAt *time.Time
	DeletedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type UpsertEllieProjectDocInput struct {
	OrgID            string
	ProjectID        string
	FilePath         string
	Title            string
	Summary          string
	SummaryEmbedding []float64
	ContentHash      string
}

type EllieProjectDocsStore struct {
	db *sql.DB
}

func NewEllieProjectDocsStore(db *sql.DB) *EllieProjectDocsStore {
	return &EllieProjectDocsStore{db: db}
}

func (s *EllieProjectDocsStore) UpsertProjectDoc(ctx context.Context, input UpsertEllieProjectDocInput) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("ellie project docs store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return "", fmt.Errorf("invalid project_id")
	}
	filePath := strings.TrimSpace(input.FilePath)
	if filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	contentHash := strings.TrimSpace(input.ContentHash)
	if contentHash == "" {
		return "", fmt.Errorf("content_hash is required")
	}

	var (
		embeddingLiteral interface{}
		err              error
	)
	if len(input.SummaryEmbedding) > 0 {
		if len(input.SummaryEmbedding) != openAIEmbeddingDimension {
			return "", fmt.Errorf("summary embedding must have %d dimensions", openAIEmbeddingDimension)
		}
		embeddingLiteral, err = formatVectorLiteral(input.SummaryEmbedding)
		if err != nil {
			return "", fmt.Errorf("format summary embedding: %w", err)
		}
	}

	title := nullableTrimmedString(input.Title)
	summary := nullableTrimmedString(input.Summary)

	var id string
	err = s.db.QueryRowContext(
		ctx,
		`INSERT INTO ellie_project_docs (
			org_id,
			project_id,
			file_path,
			title,
			summary,
			summary_embedding,
			content_hash,
			last_scanned_at,
			is_active,
			deleted_at
		) VALUES ($1, $2, $3, $4, $5, $6::vector, $7, NOW(), true, NULL)
		ON CONFLICT (org_id, project_id, file_path) DO UPDATE
		SET title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			summary_embedding = EXCLUDED.summary_embedding,
			content_hash = EXCLUDED.content_hash,
			last_scanned_at = NOW(),
			is_active = true,
			deleted_at = NULL
		RETURNING id`,
		orgID,
		projectID,
		filePath,
		title,
		summary,
		embeddingLiteral,
		contentHash,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert project doc: %w", err)
	}

	return id, nil
}

func (s *EllieProjectDocsStore) ListActiveProjectDocs(ctx context.Context, orgID, projectID string) ([]EllieProjectDoc, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie project docs store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			id,
			org_id,
			project_id,
			file_path,
			COALESCE(title, ''),
			COALESCE(summary, ''),
			content_hash,
			last_scanned_at,
			deleted_at,
			created_at,
			updated_at
		FROM ellie_project_docs
		WHERE org_id = $1
		  AND project_id = $2
		  AND is_active = true
		ORDER BY file_path ASC`,
		orgID,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list active project docs: %w", err)
	}
	defer rows.Close()

	docs := make([]EllieProjectDoc, 0)
	for rows.Next() {
		var (
			doc           EllieProjectDoc
			lastScannedAt sql.NullTime
			deletedAt     sql.NullTime
		)
		if err := rows.Scan(
			&doc.ID,
			&doc.OrgID,
			&doc.ProjectID,
			&doc.FilePath,
			&doc.Title,
			&doc.Summary,
			&doc.ContentHash,
			&lastScannedAt,
			&deletedAt,
			&doc.CreatedAt,
			&doc.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active project doc: %w", err)
		}
		if lastScannedAt.Valid {
			scanned := lastScannedAt.Time
			doc.LastScannedAt = &scanned
		}
		if deletedAt.Valid {
			deleted := deletedAt.Time
			doc.DeletedAt = &deleted
		}
		docs = append(docs, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active project docs: %w", err)
	}

	return docs, nil
}

func (s *EllieProjectDocsStore) MarkProjectDocsInactiveExcept(
	ctx context.Context,
	orgID, projectID string,
	keepPaths []string,
) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("ellie project docs store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return 0, fmt.Errorf("invalid org_id")
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return 0, fmt.Errorf("invalid project_id")
	}

	normalizedKeepPaths := make([]string, 0, len(keepPaths))
	seen := make(map[string]struct{}, len(keepPaths))
	for _, raw := range keepPaths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalizedKeepPaths = append(normalizedKeepPaths, trimmed)
	}

	var (
		result sql.Result
		err    error
	)
	if len(normalizedKeepPaths) == 0 {
		result, err = s.db.ExecContext(
			ctx,
			`UPDATE ellie_project_docs
			 SET is_active = false,
			     deleted_at = NOW()
			 WHERE org_id = $1
			   AND project_id = $2
			   AND is_active = true`,
			orgID,
			projectID,
		)
	} else {
		result, err = s.db.ExecContext(
			ctx,
			`UPDATE ellie_project_docs
			 SET is_active = false,
			     deleted_at = NOW()
			 WHERE org_id = $1
			   AND project_id = $2
			   AND is_active = true
			   AND NOT (file_path = ANY($3::text[]))`,
			orgID,
			projectID,
			pq.Array(normalizedKeepPaths),
		)
	}
	if err != nil {
		return 0, fmt.Errorf("mark project docs inactive: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read inactivated row count: %w", err)
	}

	return int(rowsAffected), nil
}

func nullableTrimmedString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
