package importer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

const (
	MemoryImportProcessJobType = "memory_import_process"

	maxUncompressedBytes = 50 * 1024 * 1024
	progressBatchSize    = 100
)

type Extractor interface {
	ExtractFromImport(ctx context.Context, orgID, importID uuid.UUID, records []memory.ImportRecord) error
}

type jobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type ImporterOptions struct {
	Pool      *pgxpool.Pool
	Imports   *repo.MemoryImportRepo
	Enqueuer  jobEnqueuer
	Store     storage.Store
	Extractor Extractor
	Now       func() time.Time
}

type Importer struct {
	imports   *repo.MemoryImportRepo
	enqueuer  jobEnqueuer
	store     storage.Store
	extractor Extractor
	now       func() time.Time
}

type ProcessPayload struct {
	ImportID uuid.UUID `json:"import_id"`
}

type jsonlRecord struct {
	Content      string                   `json:"content"`
	MemoryType   string                   `json:"memory_type"`
	Confidence   *float64                 `json:"confidence,omitempty"`
	UtilityScore *float64                 `json:"utility_score,omitempty"`
	Entities     []memory.ExtractedEntity `json:"entities,omitempty"`
	TaxonomyTags []string                 `json:"taxonomy_tags,omitempty"`
	TrustTier    *float64                 `json:"trust_tier,omitempty"`
	Embedding    []float32                `json:"embedding,omitempty"`
}

var validImportMemoryTypes = map[string]struct{}{
	"episodic":          {},
	"semantic":          {},
	"procedural":        {},
	"preference":        {},
	"entity_profile":    {},
	"execution_summary": {},
}

func NewImporter(opts ImporterOptions) (*Importer, error) {
	if opts.Pool == nil && (opts.Imports == nil || opts.Enqueuer == nil) {
		return nil, fmt.Errorf("importer requires pool or explicit repositories")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("importer requires storage backend")
	}

	imp := &Importer{
		imports:   opts.Imports,
		enqueuer:  opts.Enqueuer,
		store:     opts.Store,
		extractor: opts.Extractor,
		now:       opts.Now,
	}
	if imp.imports == nil {
		imp.imports = repo.NewMemoryImportRepo(opts.Pool)
	}
	if imp.enqueuer == nil {
		imp.enqueuer = jobqueue.New(opts.Pool, nil, jobqueue.Config{})
	}
	if imp.now == nil {
		imp.now = time.Now
	}
	return imp, nil
}

func (i *Importer) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if i == nil || registrar == nil {
		return
	}

	registrar.Register(MemoryImportProcessJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload ProcessPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode memory import payload: %w", err)
		}
		if payload.ImportID == uuid.Nil {
			return fmt.Errorf("memory import payload missing import_id")
		}
		return i.ProcessImport(ctx, payload.ImportID)
	})
}

func (i *Importer) StartImport(ctx context.Context, orgID uuid.UUID, requestedBy uuid.UUID, fileKey string) (uuid.UUID, error) {
	if i == nil || i.imports == nil || i.enqueuer == nil {
		return uuid.Nil, fmt.Errorf("importer is not configured")
	}
	if orgID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("organization id is required")
	}
	if strings.TrimSpace(fileKey) == "" {
		return uuid.Nil, fmt.Errorf("file key is required")
	}

	var requester *uuid.UUID
	if requestedBy != uuid.Nil {
		requester = &requestedBy
	}

	created, err := i.imports.Create(ctx, repo.MemoryImport{
		OrganizationID: orgID,
		RequestedBy:    requester,
		Status:         "pending",
		FileKey:        strings.TrimSpace(fileKey),
	})
	if err != nil {
		return uuid.Nil, err
	}

	if _, err := i.enqueuer.Enqueue(ctx, nil, MemoryImportProcessJobType, 75, ProcessPayload{ImportID: created.ID}, nil); err != nil {
		return uuid.Nil, err
	}
	return created.ID, nil
}

func (i *Importer) ProcessImport(ctx context.Context, importID uuid.UUID) (err error) {
	if i == nil || i.imports == nil || i.store == nil {
		return fmt.Errorf("importer is not configured")
	}
	if importID == uuid.Nil {
		return fmt.Errorf("import id is required")
	}

	item, err := i.imports.GetByID(ctx, importID)
	if err != nil {
		return err
	}

	startedAt := i.now().UTC()
	if _, err := i.imports.UpdateStatus(ctx, importID, "processing", nil, &startedAt, nil); err != nil {
		return err
	}

	defer func() {
		if err == nil {
			return
		}
		message := strings.TrimSpace(err.Error())
		completedAt := i.now().UTC()
		_, _ = i.imports.UpdateStatus(ctx, importID, "failed", &message, &startedAt, &completedAt)
	}()

	reader, err := i.store.Get(ctx, item.FileKey)
	if err != nil {
		return err
	}
	defer reader.Close()

	archiveBytes, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	validated, rejectedCount, totalCount, err := parseAndValidateArchive(archiveBytes)
	if err != nil {
		return err
	}

	total := totalCount
	processed := rejectedCount
	imported := 0
	rejected := rejectedCount
	if _, err := i.imports.UpdateProgress(ctx, importID, &total, processed, imported, rejected); err != nil {
		return err
	}

	if i.extractor == nil {
		return fmt.Errorf("importer extractor is not configured")
	}

	for start := 0; start < len(validated); start += progressBatchSize {
		end := start + progressBatchSize
		if end > len(validated) {
			end = len(validated)
		}
		batch := validated[start:end]

		if err := i.extractor.ExtractFromImport(ctx, item.OrganizationID, item.ID, batch); err != nil {
			return err
		}

		imported += len(batch)
		processed = imported + rejected
		if _, err := i.imports.UpdateProgress(ctx, importID, &total, processed, imported, rejected); err != nil {
			return err
		}
	}

	completedAt := i.now().UTC()
	if _, err := i.imports.UpdateStatus(ctx, importID, "completed", nil, &startedAt, &completedAt); err != nil {
		return err
	}
	return nil
}

func parseAndValidateArchive(archive []byte) ([]memory.ImportRecord, int, int, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("open zip: %w", err)
	}
	if len(reader.File) == 0 {
		return nil, 0, 0, fmt.Errorf("zip archive is empty")
	}

	valid := make([]memory.ImportRecord, 0)
	totalRecords := 0
	rejectedRecords := 0
	hasJSONL := false

	var uncompressedSize int64
	for _, file := range reader.File {
		if strings.HasSuffix(strings.ToLower(file.Name), "/") {
			continue
		}
		if strings.ToLower(filepath.Ext(file.Name)) != ".jsonl" {
			return nil, 0, 0, fmt.Errorf("zip contains non-JSONL file: %s", file.Name)
		}
		hasJSONL = true
		uncompressedSize += int64(file.UncompressedSize64)
		if err := validateUncompressedSize(uncompressedSize); err != nil {
			return nil, 0, 0, err
		}

		fileReader, err := file.Open()
		if err != nil {
			return nil, 0, 0, err
		}
		scanner := bufio.NewScanner(fileReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			totalRecords++

			record, err := validateRecordJSON([]byte(line))
			if err != nil {
				rejectedRecords++
				continue
			}
			valid = append(valid, *record)
		}
		closeErr := fileReader.Close()
		if err := scanner.Err(); err != nil {
			if closeErr != nil {
				return nil, 0, 0, errors.Join(err, closeErr)
			}
			return nil, 0, 0, err
		}
		if closeErr != nil {
			return nil, 0, 0, closeErr
		}
	}

	if !hasJSONL {
		return nil, 0, 0, fmt.Errorf("zip must contain at least one .jsonl file")
	}
	return valid, rejectedRecords, totalRecords, nil
}

func validateUncompressedSize(totalSize int64) error {
	if totalSize > maxUncompressedBytes {
		return fmt.Errorf("zip uncompressed size exceeds 50MB limit")
	}
	return nil
}

func validateRecordJSON(raw []byte) (*memory.ImportRecord, error) {
	var record jsonlRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode jsonl record: %w", err)
	}

	record.Content = strings.TrimSpace(record.Content)
	record.MemoryType = strings.TrimSpace(record.MemoryType)
	if record.Content == "" {
		return nil, fmt.Errorf("record content is required")
	}
	if _, ok := validImportMemoryTypes[record.MemoryType]; !ok {
		return nil, fmt.Errorf("record memory_type is invalid")
	}
	if len(record.Embedding) > 0 && len(record.Embedding) != 1536 {
		return nil, fmt.Errorf("embedding must have 1536 dimensions")
	}

	importRecord := &memory.ImportRecord{
		Content:      record.Content,
		MemoryType:   record.MemoryType,
		Confidence:   record.Confidence,
		UtilityScore: record.UtilityScore,
		Entities:     record.Entities,
		TaxonomyTags: record.TaxonomyTags,
		Embedding:    record.Embedding,
	}
	if record.TrustTier != nil {
		capped := *record.TrustTier
		if capped > 0.6 {
			capped = 0.6
		}
		if capped < 0 {
			capped = 0
		}
		importRecord.TrustTier = &capped
	}

	return importRecord, nil
}
