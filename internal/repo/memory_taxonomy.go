package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MemoryTaxonomyNode struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ParentID       *uuid.UUID
	Slug           string
	DisplayName    string
	Description    *string
	CreatedAt      time.Time
}

type MemoryTaxonomyNodeRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryTaxonomyNodeRepo(pool *pgxpool.Pool) *MemoryTaxonomyNodeRepo {
	return &MemoryTaxonomyNodeRepo{pool: pool}
}

func (r *MemoryTaxonomyNodeRepo) Create(ctx context.Context, node MemoryTaxonomyNode) (MemoryTaxonomyNode, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory_taxonomy_node (
			organization_id,
			parent_id,
			slug,
			display_name,
			description
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, organization_id, parent_id, slug, display_name, description, created_at
	`, node.OrganizationID, node.ParentID, node.Slug, node.DisplayName, node.Description)

	created, err := scanMemoryTaxonomyNode(row)
	if err != nil {
		return MemoryTaxonomyNode{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryTaxonomyNodeRepo) GetByID(ctx context.Context, id uuid.UUID) (MemoryTaxonomyNode, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, parent_id, slug, display_name, description, created_at
		FROM memory_taxonomy_node
		WHERE id = $1
	`, id)

	node, err := scanMemoryTaxonomyNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryTaxonomyNode{}, ErrNotFound
	}
	if err != nil {
		return MemoryTaxonomyNode{}, mapDBError(err)
	}
	return node, nil
}

func (r *MemoryTaxonomyNodeRepo) GetBySlug(ctx context.Context, organizationID uuid.UUID, parentID *uuid.UUID, slug string) (MemoryTaxonomyNode, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, parent_id, slug, display_name, description, created_at
		FROM memory_taxonomy_node
		WHERE organization_id = $1
		  AND slug = $2
		  AND (
				(parent_id IS NULL AND $3::uuid IS NULL)
				OR parent_id = $3
		  )
	`, organizationID, slug, parentID)

	node, err := scanMemoryTaxonomyNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryTaxonomyNode{}, ErrNotFound
	}
	if err != nil {
		return MemoryTaxonomyNode{}, mapDBError(err)
	}
	return node, nil
}

func (r *MemoryTaxonomyNodeRepo) ListChildren(ctx context.Context, organizationID uuid.UUID, parentID *uuid.UUID) ([]MemoryTaxonomyNode, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, parent_id, slug, display_name, description, created_at
		FROM memory_taxonomy_node
		WHERE organization_id = $1
		  AND (
				(parent_id IS NULL AND $2::uuid IS NULL)
				OR parent_id = $2
		  )
		ORDER BY created_at
	`, organizationID, parentID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	nodes := make([]MemoryTaxonomyNode, 0)
	for rows.Next() {
		node, scanErr := scanMemoryTaxonomyNode(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		nodes = append(nodes, node)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return nodes, nil
}

func (r *MemoryTaxonomyNodeRepo) ListByOrganization(ctx context.Context, organizationID uuid.UUID) ([]MemoryTaxonomyNode, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, parent_id, slug, display_name, description, created_at
		FROM memory_taxonomy_node
		WHERE organization_id = $1
		ORDER BY created_at ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	nodes := make([]MemoryTaxonomyNode, 0)
	for rows.Next() {
		node, scanErr := scanMemoryTaxonomyNode(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		nodes = append(nodes, node)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return nodes, nil
}

const listSubtreeQuery = `
WITH RECURSIVE subtree AS (
	SELECT id, organization_id, parent_id, slug, display_name, description, created_at, 0 AS depth
	FROM memory_taxonomy_node
	WHERE id = $1

	UNION ALL

	SELECT child.id, child.organization_id, child.parent_id, child.slug, child.display_name, child.description, child.created_at, subtree.depth + 1 AS depth
	FROM memory_taxonomy_node child
	JOIN subtree ON child.parent_id = subtree.id
)
SELECT id, organization_id, parent_id, slug, display_name, description, created_at
FROM subtree
WHERE depth > 0
ORDER BY depth, created_at
`

func (r *MemoryTaxonomyNodeRepo) ListSubtree(ctx context.Context, rootID uuid.UUID) ([]MemoryTaxonomyNode, error) {
	rows, err := r.pool.Query(ctx, listSubtreeQuery, rootID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	nodes := make([]MemoryTaxonomyNode, 0)
	for rows.Next() {
		node, scanErr := scanMemoryTaxonomyNode(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		nodes = append(nodes, node)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return nodes, nil
}

func (r *MemoryTaxonomyNodeRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM memory_taxonomy_node WHERE id = $1`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMemoryTaxonomyNode(row pgx.Row) (MemoryTaxonomyNode, error) {
	var node MemoryTaxonomyNode
	if err := row.Scan(
		&node.ID,
		&node.OrganizationID,
		&node.ParentID,
		&node.Slug,
		&node.DisplayName,
		&node.Description,
		&node.CreatedAt,
	); err != nil {
		return MemoryTaxonomyNode{}, err
	}
	return node, nil
}
