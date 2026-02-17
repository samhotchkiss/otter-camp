package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Org represents an organization record.
type Org struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Tier      string    `json:"tier"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OrgStore provides organization-level read access.
type OrgStore struct {
	db *sql.DB
}

// NewOrgStore creates an OrgStore with a database connection.
func NewOrgStore(db *sql.DB) *OrgStore {
	return &OrgStore{db: db}
}

// GetBySlug retrieves one organization by slug.
func (s *OrgStore) GetBySlug(ctx context.Context, slug string) (*Org, error) {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return nil, ErrValidation
	}

	const query = `SELECT id::text, name, slug, tier, created_at, updated_at
		FROM organizations
		WHERE LOWER(slug) = $1
		LIMIT 1`

	var org Org
	if err := s.db.QueryRowContext(ctx, query, normalized).Scan(
		&org.ID,
		&org.Name,
		&org.Slug,
		&org.Tier,
		&org.CreatedAt,
		&org.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get org by slug: %w", err)
	}

	return &org, nil
}
