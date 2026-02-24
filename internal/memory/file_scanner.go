package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type fileScanMemoryStore interface {
	ListFileBacked(ctx context.Context, orgID uuid.UUID) ([]repo.Memory, error)
	Create(ctx context.Context, memory repo.Memory) (repo.Memory, error)
	Supersede(ctx context.Context, id, supersededBy uuid.UUID) (repo.Memory, error)
	Archive(ctx context.Context, id uuid.UUID) (repo.Memory, error)
}

type FileScannerOptions struct {
	Pool     *pgxpool.Pool
	Reader   FileReader
	Memories fileScanMemoryStore
	Clock    Clock
}

type FileScanner struct {
	pool     *pgxpool.Pool
	reader   FileReader
	memories fileScanMemoryStore
	clock    Clock
}

func NewFileScanner(opts FileScannerOptions) (*FileScanner, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("file scanner pool is required")
	}
	if opts.Reader == nil {
		return nil, fmt.Errorf("file scanner reader is required")
	}
	if opts.Memories == nil {
		opts.Memories = repo.NewMemoryRepo(opts.Pool)
	}
	if opts.Clock == nil {
		opts.Clock = wallClock{}
	}

	return &FileScanner{
		pool:     opts.Pool,
		reader:   opts.Reader,
		memories: opts.Memories,
		clock:    opts.Clock,
	}, nil
}

func (s *FileScanner) Scan(ctx context.Context, orgID uuid.UUID) error {
	if orgID == uuid.Nil {
		return fmt.Errorf("organization id is required")
	}

	fileBacked, err := s.memories.ListFileBacked(ctx, orgID)
	if err != nil {
		return err
	}

	for _, memory := range fileBacked {
		if memory.Status != "active" {
			continue
		}
		if err := s.scanOne(ctx, memory); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileScanner) scanOne(ctx context.Context, source repo.Memory) error {
	scanTime := s.clock.Now()
	defer func() {
		_ = s.markScanned(ctx, source.ID, scanTime)
	}()

	if source.ProjectID == nil || *source.ProjectID == uuid.Nil || source.FilePath == nil || *source.FilePath == "" {
		return nil
	}

	content, err := s.reader.ReadFile(ctx, *source.ProjectID, *source.FilePath)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			_, archiveErr := s.memories.Archive(ctx, source.ID)
			return archiveErr
		}
		return err
	}

	newContent := string(content)
	newHash := hashContent(newContent)
	if newHash == source.ContentHash {
		return nil
	}

	created, err := s.memories.Create(ctx, repo.Memory{
		OrganizationID:    source.OrganizationID,
		ProjectID:         source.ProjectID,
		ProjectTaskID:     source.ProjectTaskID,
		AgentID:           source.AgentID,
		MemoryType:        source.MemoryType,
		Scope:             source.Scope,
		Content:           newContent,
		ContentHash:       newHash,
		Confidence:        source.Confidence,
		UtilityScore:      source.UtilityScore,
		Status:            "active",
		Sensitivity:       source.Sensitivity,
		TrustTier:         source.TrustTier,
		FileBacked:        true,
		FilePath:          source.FilePath,
		FileLastScannedAt: &scanTime,
	})
	if err != nil {
		return err
	}

	_, err = s.memories.Supersede(ctx, source.ID, created.ID)
	return err
}

func (s *FileScanner) markScanned(ctx context.Context, memoryID uuid.UUID, scannedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE memory
		SET file_last_scanned_at = $2
		WHERE id = $1
	`, memoryID, scannedAt.UTC())
	return err
}
