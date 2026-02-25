//go:build integration

package memory_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	mem "github.com/samhotchkiss/otter-camp/internal/memory"
	memoryimporter "github.com/samhotchkiss/otter-camp/internal/memory/importer"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestImport_JSONL_ValidFile(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := createImportTestOrg(t, ctx, pool, "import-valid")

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	archive := mustReadFixture(t, "testdata/import/valid_sample.jsonl.zip")
	fileKey := fmt.Sprintf("imports/%s/%s/valid.zip", org.ID, uuid.New())
	if err := store.Put(ctx, fileKey, bytes.NewReader(archive), storage.PutOptions{
		ContentType:   "application/zip",
		ContentLength: int64(len(archive)),
	}); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	imp, err := memoryimporter.NewImporter(memoryimporter.ImporterOptions{
		Pool:      pool,
		Store:     store,
		Extractor: importMemoryExtractor{memoryRepo: repo.NewMemoryRepo(pool)},
		Enqueuer:  noopImportEnqueuer{},
	})
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	importID, err := imp.StartImport(ctx, org.ID, uuid.Nil, fileKey)
	if err != nil {
		t.Fatalf("StartImport: %v", err)
	}
	if err := imp.ProcessImport(ctx, importID); err != nil {
		t.Fatalf("ProcessImport: %v", err)
	}

	importRow, err := repo.NewMemoryImportRepo(pool).GetByID(ctx, importID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if importRow.Status != "completed" {
		t.Fatalf("status = %q, want %q", importRow.Status, "completed")
	}
	if importRow.ProcessedRecords != 10 {
		t.Fatalf("processed_records = %d, want 10", importRow.ProcessedRecords)
	}
	if importRow.ImportedRecords != 10 {
		t.Fatalf("imported_records = %d, want 10", importRow.ImportedRecords)
	}

	var (
		memoryCount int
		minTrust    float64
		maxTrust    float64
	)
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*), MIN(trust_tier), MAX(trust_tier)
		FROM memory
		WHERE organization_id = $1
	`, org.ID).Scan(&memoryCount, &minTrust, &maxTrust); err != nil {
		t.Fatalf("query imported memories: %v", err)
	}
	if memoryCount != 10 {
		t.Fatalf("memory count = %d, want 10", memoryCount)
	}
	if minTrust != 0.6 || maxTrust != 0.6 {
		t.Fatalf("trust tier min/max = %f/%f, want 0.6/0.6", minTrust, maxTrust)
	}
}

func TestImport_JSONL_InvalidRecord(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := createImportTestOrg(t, ctx, pool, "import-invalid")

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	archive := mustReadFixture(t, "testdata/import/invalid_record.jsonl.zip")
	fileKey := fmt.Sprintf("imports/%s/%s/invalid.zip", org.ID, uuid.New())
	if err := store.Put(ctx, fileKey, bytes.NewReader(archive), storage.PutOptions{
		ContentType:   "application/zip",
		ContentLength: int64(len(archive)),
	}); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	imp, err := memoryimporter.NewImporter(memoryimporter.ImporterOptions{
		Pool:      pool,
		Store:     store,
		Extractor: importMemoryExtractor{memoryRepo: repo.NewMemoryRepo(pool)},
		Enqueuer:  noopImportEnqueuer{},
	})
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	importID, err := imp.StartImport(ctx, org.ID, uuid.Nil, fileKey)
	if err != nil {
		t.Fatalf("StartImport: %v", err)
	}
	if err := imp.ProcessImport(ctx, importID); err != nil {
		t.Fatalf("ProcessImport: %v", err)
	}

	importRow, err := repo.NewMemoryImportRepo(pool).GetByID(ctx, importID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if importRow.Status != "completed" {
		t.Fatalf("status = %q, want %q", importRow.Status, "completed")
	}
	if importRow.RejectedRecords != 1 {
		t.Fatalf("rejected_records = %d, want 1", importRow.RejectedRecords)
	}
	if importRow.ImportedRecords != 3 {
		t.Fatalf("imported_records = %d, want 3", importRow.ImportedRecords)
	}
}

func TestImport_JSONL_StatusTracking(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := createImportTestOrg(t, ctx, pool, "import-status")

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	archive := buildImportArchive(t, 250)
	fileKey := fmt.Sprintf("imports/%s/%s/status.zip", org.ID, uuid.New())
	if err := store.Put(ctx, fileKey, bytes.NewReader(archive), storage.PutOptions{
		ContentType:   "application/zip",
		ContentLength: int64(len(archive)),
	}); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	imp, err := memoryimporter.NewImporter(memoryimporter.ImporterOptions{
		Pool:      pool,
		Store:     store,
		Extractor: importMemoryExtractor{memoryRepo: repo.NewMemoryRepo(pool), sleepPerBatch: 40 * time.Millisecond},
		Enqueuer:  noopImportEnqueuer{},
	})
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	importID, err := imp.StartImport(ctx, org.ID, uuid.Nil, fileKey)
	if err != nil {
		t.Fatalf("StartImport: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- imp.ProcessImport(context.Background(), importID)
	}()

	importRepo := repo.NewMemoryImportRepo(pool)
	var sawProcessing bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		item, getErr := importRepo.GetByID(ctx, importID)
		if getErr != nil {
			t.Fatalf("GetByID during processing: %v", getErr)
		}
		if item.Status == "processing" {
			sawProcessing = true
		}
		if item.Status == "completed" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("ProcessImport: %v", err)
	}

	final, err := importRepo.GetByID(ctx, importID)
	if err != nil {
		t.Fatalf("GetByID final: %v", err)
	}
	if !sawProcessing {
		t.Fatal("did not observe status=processing during import")
	}
	if final.Status != "completed" {
		t.Fatalf("final status = %q, want %q", final.Status, "completed")
	}
	if final.ProcessedRecords != 250 {
		t.Fatalf("final processed_records = %d, want 250", final.ProcessedRecords)
	}
}

type importMemoryExtractor struct {
	memoryRepo    *repo.MemoryRepo
	sleepPerBatch time.Duration
}

func (e importMemoryExtractor) ExtractFromImport(ctx context.Context, orgID, _ uuid.UUID, records []mem.ImportRecord) error {
	for _, record := range records {
		content := strings.TrimSpace(record.Content)
		if content == "" {
			continue
		}
		memoryType := strings.TrimSpace(record.MemoryType)
		if memoryType == "" {
			memoryType = "semantic"
		}
		if _, err := e.memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: orgID,
			MemoryType:     memoryType,
			Scope:          "org",
			Content:        content,
			ContentHash:    uuid.NewString(),
			Status:         "candidate",
			Confidence:     0.6,
			UtilityScore:   0.6,
			TrustTier:      0.6,
			Sensitivity:    "normal",
		}); err != nil {
			return err
		}
	}
	if e.sleepPerBatch > 0 {
		time.Sleep(e.sleepPerBatch)
	}
	return nil
}

type noopImportEnqueuer struct{}

func (noopImportEnqueuer) Enqueue(context.Context, pgx.Tx, string, int, any, *time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}

func mustReadFixture(t *testing.T, relativePath string) []byte {
	t.Helper()
	fullPath := filepath.Join("testdata", strings.TrimPrefix(relativePath, "testdata/"))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fullPath, err)
	}
	return data
}

func buildImportArchive(t *testing.T, count int) []byte {
	t.Helper()
	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	w, err := zw.Create("memory.jsonl")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	for i := 0; i < count; i++ {
		record := fmt.Sprintf(`{"content":"record-%03d","memory_type":"semantic"}`, i+1)
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				t.Fatalf("zip newline write: %v", err)
			}
		}
		if _, err := io.WriteString(w, record); err != nil {
			t.Fatalf("zip record write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func createImportTestOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slugPrefix string) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        slugPrefix + "-" + uuid.NewString()[:8],
		DisplayName: "Memory Import Test Org " + slugPrefix,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}
