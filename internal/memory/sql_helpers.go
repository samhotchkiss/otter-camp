package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func scanMemory(row pgx.Row) (repo.Memory, error) {
	var (
		item          repo.Memory
		embeddingText *string
	)
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ProjectID,
		&item.ProjectTaskID,
		&item.AgentID,
		&item.MemoryType,
		&item.Scope,
		&item.Content,
		&item.ContentHash,
		&embeddingText,
		&item.Confidence,
		&item.UtilityScore,
		&item.ExtractionScore,
		&item.Status,
		&item.IsHardened,
		&item.Sensitivity,
		&item.TrustTier,
		&item.FileBacked,
		&item.FilePath,
		&item.FileLastScannedAt,
		&item.SupersededBy,
		&item.SupersededAt,
		&item.ArchivedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return repo.Memory{}, err
	}

	if embeddingText != nil && strings.TrimSpace(*embeddingText) != "" {
		vector, err := parseEmbedding(*embeddingText)
		if err != nil {
			return repo.Memory{}, err
		}
		item.Embedding = vector
	}
	return item, nil
}

func scanMemoryRows(rows pgx.Rows) ([]repo.Memory, error) {
	items := make([]repo.Memory, 0)
	for rows.Next() {
		item, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func parseEmbedding(value string) ([]float32, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	if strings.TrimSpace(trimmed) == "" {
		return nil, nil
	}

	parts := strings.Split(trimmed, ",")
	embedding := make([]float32, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("parse embedding component %q: %w", part, err)
		}
		embedding = append(embedding, float32(number))
	}
	return embedding, nil
}

func embeddingToLiteral(vector []float32) string {
	if len(vector) == 0 {
		return ""
	}

	parts := make([]string, len(vector))
	for index, value := range vector {
		parts[index] = strconv.FormatFloat(float64(value), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func hashContent(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

func derefUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
