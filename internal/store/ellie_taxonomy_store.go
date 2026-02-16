package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lib/pq"
)

type EllieTaxonomyNode struct {
	ID          string
	OrgID       string
	ParentID    *string
	Slug        string
	DisplayName string
	Description *string
	Depth       int
	CreatedAt   time.Time
}

type CreateEllieTaxonomyNodeInput struct {
	OrgID       string
	ParentID    *string
	Slug        string
	DisplayName string
	Description *string
}

type UpsertEllieMemoryTaxonomyInput struct {
	OrgID        string
	MemoryID     string
	NodeID       string
	Confidence   float64
	ClassifiedAt time.Time
}

type EllieMemoryTaxonomyClassification struct {
	MemoryID     string
	NodeID       string
	NodePath     string
	Confidence   float64
	ClassifiedAt time.Time
}

type ellieTaxonomyQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type EllieTaxonomyStore struct {
	db ellieTaxonomyQuerier
}

func NewEllieTaxonomyStore(db *sql.DB) *EllieTaxonomyStore {
	return &EllieTaxonomyStore{db: db}
}

func NewEllieTaxonomyStoreTx(tx *sql.Tx) *EllieTaxonomyStore {
	return &EllieTaxonomyStore{db: tx}
}

func (s *EllieTaxonomyStore) CreateNode(ctx context.Context, input CreateEllieTaxonomyNodeInput) (*EllieTaxonomyNode, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie taxonomy store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	parentID, err := normalizeOptionalEllieUUID(input.ParentID)
	if err != nil {
		return nil, fmt.Errorf("parent_id: %w", err)
	}

	slug := strings.ToLower(strings.TrimSpace(input.Slug))
	if slug == "" {
		return nil, fmt.Errorf("slug is required")
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return nil, fmt.Errorf("display_name is required")
	}
	description := sanitizeOptionalString(input.Description)

	node, err := scanEllieTaxonomyNode(s.db.QueryRowContext(
		ctx,
		`WITH parent AS (
			SELECT id, depth
			  FROM ellie_taxonomy_nodes
			 WHERE org_id = $1
			   AND id = $2
		)
		INSERT INTO ellie_taxonomy_nodes (org_id, parent_id, slug, display_name, description, depth)
		SELECT
			$1,
			$2,
			$3,
			$4,
			$5,
			CASE
				WHEN $2 IS NULL THEN 0
				ELSE (SELECT depth + 1 FROM parent)
			END
		WHERE $2 IS NULL OR EXISTS (SELECT 1 FROM parent)
		RETURNING id, org_id, parent_id::text, slug, display_name, description, depth, created_at`,
		orgID,
		parentID,
		slug,
		displayName,
		description,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if isEllieTaxonomyConflict(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("failed to create ellie taxonomy node: %w", err)
	}
	return node, nil
}

func (s *EllieTaxonomyStore) GetNodeByID(ctx context.Context, orgID, nodeID string) (*EllieTaxonomyNode, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie taxonomy store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	nodeID = strings.TrimSpace(nodeID)
	if !uuidRegex.MatchString(nodeID) {
		return nil, fmt.Errorf("invalid node_id")
	}

	node, err := scanEllieTaxonomyNode(s.db.QueryRowContext(
		ctx,
		`SELECT id, org_id, parent_id::text, slug, display_name, description, depth, created_at
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1
		    AND id = $2`,
		orgID,
		nodeID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get taxonomy node: %w", err)
	}
	return node, nil
}

func (s *EllieTaxonomyStore) ListNodesByParent(ctx context.Context, orgID string, parentID *string, limit int) ([]EllieTaxonomyNode, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie taxonomy store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}

	normalizedParentID, err := normalizeOptionalEllieUUID(parentID)
	if err != nil {
		return nil, fmt.Errorf("parent_id: %w", err)
	}

	query := `SELECT id, org_id, parent_id::text, slug, display_name, description, depth, created_at
		FROM ellie_taxonomy_nodes
		WHERE org_id = $1`
	args := []any{orgID}
	if normalizedParentID == nil {
		query += ` AND parent_id IS NULL`
	} else {
		query += ` AND parent_id = $2`
		args = append(args, normalizedParentID)
	}
	if normalizedParentID == nil {
		query += ` ORDER BY slug ASC LIMIT $2`
		args = append(args, limit)
	} else {
		query += ` ORDER BY slug ASC LIMIT $3`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list taxonomy nodes by parent: %w", err)
	}
	defer rows.Close()

	nodes := make([]EllieTaxonomyNode, 0, limit)
	for rows.Next() {
		node, scanErr := scanEllieTaxonomyNode(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan taxonomy node: %w", scanErr)
		}
		nodes = append(nodes, *node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading taxonomy nodes: %w", err)
	}

	return nodes, nil
}

func (s *EllieTaxonomyStore) UpsertMemoryClassification(ctx context.Context, input UpsertEllieMemoryTaxonomyInput) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie taxonomy store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	memoryID := strings.TrimSpace(input.MemoryID)
	if !uuidRegex.MatchString(memoryID) {
		return fmt.Errorf("invalid memory_id")
	}
	nodeID := strings.TrimSpace(input.NodeID)
	if !uuidRegex.MatchString(nodeID) {
		return fmt.Errorf("invalid node_id")
	}

	confidence := input.Confidence
	if math.IsNaN(confidence) || confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	classifiedAt := input.ClassifiedAt.UTC()
	if classifiedAt.IsZero() {
		classifiedAt = time.Now().UTC()
	}

	var insertedMemoryID string
	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO ellie_memory_taxonomy (memory_id, node_id, confidence, classified_at)
		 SELECT m.id, n.id, $4, $5
		   FROM memories m
		   JOIN ellie_taxonomy_nodes n ON n.id = $3
		  WHERE m.id = $2
		    AND m.org_id = $1
		    AND n.org_id = $1
		 ON CONFLICT (memory_id, node_id)
		 DO UPDATE
		       SET confidence = EXCLUDED.confidence,
		           classified_at = EXCLUDED.classified_at
		 RETURNING memory_id::text`,
		orgID,
		memoryID,
		nodeID,
		confidence,
		classifiedAt,
	).Scan(&insertedMemoryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to upsert memory classification: %w", err)
	}

	return nil
}

func (s *EllieTaxonomyStore) ListMemoryClassifications(ctx context.Context, orgID, memoryID string) ([]EllieMemoryTaxonomyClassification, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie taxonomy store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	memoryID = strings.TrimSpace(memoryID)
	if !uuidRegex.MatchString(memoryID) {
		return nil, fmt.Errorf("invalid memory_id")
	}

	var exists bool
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			  FROM memories
			 WHERE org_id = $1
			   AND id = $2
		)`,
		orgID,
		memoryID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("failed to check memory existence: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	taxonomyMap, err := s.listNodesForPath(ctx, orgID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT mt.memory_id::text, mt.node_id::text, mt.confidence, mt.classified_at
		   FROM ellie_memory_taxonomy mt
		   JOIN memories m ON m.id = mt.memory_id
		   JOIN ellie_taxonomy_nodes n ON n.id = mt.node_id
		  WHERE m.org_id = $1
		    AND n.org_id = $1
		    AND mt.memory_id = $2
		  ORDER BY mt.confidence DESC, mt.node_id ASC`,
		orgID,
		memoryID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list memory classifications: %w", err)
	}
	defer rows.Close()

	classifications := make([]EllieMemoryTaxonomyClassification, 0)
	for rows.Next() {
		var row EllieMemoryTaxonomyClassification
		if err := rows.Scan(&row.MemoryID, &row.NodeID, &row.Confidence, &row.ClassifiedAt); err != nil {
			return nil, fmt.Errorf("failed to scan memory classification: %w", err)
		}

		nodePath, err := ellieTaxonomyPathForNode(row.NodeID, taxonomyMap)
		if err != nil {
			return nil, err
		}
		row.NodePath = nodePath
		classifications = append(classifications, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading memory classifications: %w", err)
	}

	return classifications, nil
}

func (s *EllieTaxonomyStore) listNodesForPath(ctx context.Context, orgID string) (map[string]EllieTaxonomyNode, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, org_id, parent_id::text, slug, display_name, description, depth, created_at
		   FROM ellie_taxonomy_nodes
		  WHERE org_id = $1`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list taxonomy nodes for pathing: %w", err)
	}
	defer rows.Close()

	nodes := make(map[string]EllieTaxonomyNode)
	for rows.Next() {
		node, scanErr := scanEllieTaxonomyNode(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan taxonomy node for pathing: %w", scanErr)
		}
		nodes[node.ID] = *node
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading taxonomy nodes for pathing: %w", err)
	}
	return nodes, nil
}

func ellieTaxonomyPathForNode(nodeID string, nodeMap map[string]EllieTaxonomyNode) (string, error) {
	currentID := strings.TrimSpace(nodeID)
	if currentID == "" {
		return "", fmt.Errorf("node path requires node_id")
	}

	parts := make([]string, 0, 8)
	visited := make(map[string]struct{})
	for currentID != "" {
		if _, seen := visited[currentID]; seen {
			return "", fmt.Errorf("taxonomy path cycle detected")
		}
		visited[currentID] = struct{}{}

		node, ok := nodeMap[currentID]
		if !ok {
			return "", fmt.Errorf("taxonomy node not found while building path")
		}
		parts = append(parts, node.Slug)
		if node.ParentID == nil {
			break
		}
		currentID = strings.TrimSpace(*node.ParentID)
	}

	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/"), nil
}

func scanEllieTaxonomyNode(scanner interface{ Scan(...any) error }) (*EllieTaxonomyNode, error) {
	var (
		node        EllieTaxonomyNode
		parentID    sql.NullString
		description sql.NullString
	)

	if err := scanner.Scan(
		&node.ID,
		&node.OrgID,
		&parentID,
		&node.Slug,
		&node.DisplayName,
		&description,
		&node.Depth,
		&node.CreatedAt,
	); err != nil {
		return nil, err
	}

	if parentID.Valid {
		trimmed := strings.TrimSpace(parentID.String)
		if trimmed != "" {
			node.ParentID = &trimmed
		}
	}

	if description.Valid {
		trimmed := strings.TrimSpace(description.String)
		if trimmed != "" {
			node.Description = &trimmed
		}
	}

	return &node, nil
}

func isEllieTaxonomyConflict(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "23505"
}
