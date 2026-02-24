//go:build integration

package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestImporterIntegrationProcessImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, ctx, pool)
	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	archive := buildImportArchive(t, 5)
	fileKey := fmt.Sprintf("imports/%s/%s/import.zip", org.ID, uuid.New())
	if err := store.Put(ctx, fileKey, bytes.NewReader(archive), storage.PutOptions{ContentType: "application/zip", ContentLength: int64(len(archive))}); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	extractor := &recordingExtractor{memoryRepo: repo.NewMemoryRepo(pool)}
	enqueuer := &stubEnqueuer{}
	imp, err := NewImporter(ImporterOptions{
		Pool:      pool,
		Store:     store,
		Extractor: extractor,
		Enqueuer:  enqueuer,
	})
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}

	importID, err := imp.StartImport(ctx, org.ID, uuid.Nil, fileKey)
	if err != nil {
		t.Fatalf("StartImport: %v", err)
	}
	item, err := repo.NewMemoryImportRepo(pool).GetByID(ctx, importID)
	if err != nil {
		t.Fatalf("GetByID after StartImport: %v", err)
	}
	if item.Status != "pending" {
		t.Fatalf("status after StartImport = %s, want pending", item.Status)
	}

	if err := imp.ProcessImport(ctx, importID); err != nil {
		t.Fatalf("ProcessImport: %v", err)
	}

	final, err := repo.NewMemoryImportRepo(pool).GetByID(ctx, importID)
	if err != nil {
		t.Fatalf("GetByID after ProcessImport: %v", err)
	}
	if final.Status != "completed" {
		t.Fatalf("final status = %s, want completed", final.Status)
	}
	if final.TotalRecords == nil || *final.TotalRecords != 5 {
		t.Fatalf("total_records = %v, want 5", final.TotalRecords)
	}
	if final.ImportedRecords < 1 {
		t.Fatalf("imported_records = %d, want >= 1", final.ImportedRecords)
	}
	if final.StartedAt == nil || final.CompletedAt == nil {
		t.Fatalf("expected started_at and completed_at to be populated")
	}

	var memoryCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memory WHERE organization_id = $1`, org.ID).Scan(&memoryCount); err != nil {
		t.Fatalf("count memory rows: %v", err)
	}
	if memoryCount < 1 {
		t.Fatal("expected at least one memory row to be created")
	}
}

type recordingExtractor struct {
	memoryRepo *repo.MemoryRepo
}

func (e *recordingExtractor) ExtractFromImport(ctx context.Context, orgID, _ uuid.UUID, records []memory.ImportRecord) error {
	for _, record := range records {
		content := strings.TrimSpace(record.Content)
		if content == "" {
			continue
		}
		memoryType := strings.TrimSpace(record.MemoryType)
		if memoryType == "" {
			memoryType = "semantic"
		}
		_, err := e.memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: orgID,
			MemoryType:     memoryType,
			Scope:          "org",
			Content:        content,
			ContentHash:    uuid.NewString(),
			Confidence:     0.6,
			UtilityScore:   0.5,
			Status:         "candidate",
			TrustTier:      0.6,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type stubEnqueuer struct{}

func (s *stubEnqueuer) Enqueue(context.Context, pgx.Tx, string, int, any, *time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}

func buildImportArchive(t *testing.T, count int) []byte {
	t.Helper()
	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	w, err := zw.Create("memory.jsonl")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	for idx := 0; idx < count; idx++ {
		line := fmt.Sprintf(`{"content":"record-%d","memory_type":"semantic"}`, idx+1)
		if idx > 0 {
			_, _ = w.Write([]byte("\n"))
		}
		_, _ = w.Write([]byte(line))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func mustCreateOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "importer-org-" + uuid.NewString()[:8], DisplayName: "Importer Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}
