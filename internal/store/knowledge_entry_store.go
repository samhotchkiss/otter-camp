package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type KnowledgeEntry struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ReplaceKnowledgeEntryInput struct {
	Title     string
	Content   string
	Tags      []string
	CreatedBy string
}

type KnowledgeEntryStore struct {
	db *sql.DB
}

func NewKnowledgeEntryStore(db *sql.DB) *KnowledgeEntryStore {
	return &KnowledgeEntryStore{db: db}
}

func (s *KnowledgeEntryStore) ListEntries(ctx context.Context, limit int) ([]KnowledgeEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, title, content, tags, created_by, created_at, updated_at
			FROM knowledge_entries
			ORDER BY updated_at DESC, created_at DESC
			LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list knowledge entries: %w", err)
	}
	defer rows.Close()

	entries := make([]KnowledgeEntry, 0)
	for rows.Next() {
		entry, err := scanKnowledgeEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge entry: %w", err)
		}
		if entry.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read knowledge entry rows: %w", err)
	}
	return entries, nil
}

func (s *KnowledgeEntryStore) ReplaceEntries(
	ctx context.Context,
	entries []ReplaceKnowledgeEntryInput,
) (int, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return 0, ErrNoWorkspace
	}

	normalized := make([]ReplaceKnowledgeEntryInput, 0, len(entries))
	for _, entry := range entries {
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			return 0, fmt.Errorf("title is required")
		}
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			return 0, fmt.Errorf("content is required")
		}
		createdBy := strings.TrimSpace(entry.CreatedBy)
		if createdBy == "" {
			createdBy = "unknown"
		}

		tags := normalizeKnowledgeEntryTags(entry.Tags)
		normalized = append(normalized, ReplaceKnowledgeEntryInput{
			Title:     title,
			Content:   content,
			Tags:      tags,
			CreatedBy: createdBy,
		})
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE org_id = $1`, workspaceID); err != nil {
		return 0, fmt.Errorf("failed to clear knowledge entries: %w", err)
	}

	inserted := 0
	for _, entry := range normalized {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO knowledge_entries (org_id, title, content, tags, created_by)
				VALUES ($1, $2, $3, $4, $5)`,
			workspaceID,
			entry.Title,
			entry.Content,
			pq.Array(entry.Tags),
			entry.CreatedBy,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert knowledge entry: %w", err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit knowledge entry replace: %w", err)
	}
	return inserted, nil
}

func normalizeKnowledgeEntryTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}

	set := make(map[string]struct{}, len(tags))
	for _, raw := range tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		set[tag] = struct{}{}
	}

	out := make([]string, 0, len(set))
	for tag := range set {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func scanKnowledgeEntry(scanner interface{ Scan(...any) error }) (KnowledgeEntry, error) {
	var entry KnowledgeEntry
	var tags []string
	err := scanner.Scan(
		&entry.ID,
		&entry.OrgID,
		&entry.Title,
		&entry.Content,
		pq.Array(&tags),
		&entry.CreatedBy,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entry, ErrNotFound
		}
		return entry, err
	}
	entry.Tags = tags
	return entry, nil
}
