package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRunRepositoryUpdateStatusVersionConflict(t *testing.T) {
	runID := uuid.New()
	orgID := uuid.New()

	repo := &RunRepository{db: &fakeQueryExecutor{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "UPDATE run"):
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			case strings.Contains(sql, "FROM run"):
				return fakeRunRow(runID, orgID, 3, "created")
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
	}}

	_, err := repo.UpdateStatus(context.Background(), runID, 2, "completed", nil, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateStatus error = %v, want ErrConflict", err)
	}
}

func TestRunRepositoryUpdateStatusVersionSuccess(t *testing.T) {
	runID := uuid.New()
	orgID := uuid.New()

	repo := &RunRepository{db: &fakeQueryExecutor{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "UPDATE run") {
				return fakeRunRow(runID, orgID, 2, "completed")
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}}

	updated, err := repo.UpdateStatus(context.Background(), runID, 1, "completed", nil, nil)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}
	if updated.Status != "completed" {
		t.Fatalf("status = %q, want completed", updated.Status)
	}
}

func TestRunArtifactRepositoryCreateRejectsOversizedInlineContent(t *testing.T) {
	repo := &RunArtifactRepository{db: &fakeQueryExecutor{}}
	inline := "too large"
	_, err := repo.Create(context.Background(), RunArtifact{
		RunID:         uuid.New(),
		ArtifactType:  "stdout",
		StorageKey:    "runs/x/stdout.log",
		ContentType:   "text/plain",
		ByteSize:      60000,
		InlineContent: &inline,
	})
	if !errors.Is(err, ErrInlineContentTooLarge) {
		t.Fatalf("Create error = %v, want ErrInlineContentTooLarge", err)
	}
}

func TestRunAttemptRepositoryCreateRejectsAttemptNumberZero(t *testing.T) {
	repo := &RunAttemptRepository{db: &fakeQueryExecutor{}}
	_, err := repo.Create(context.Background(), RunAttempt{
		RunStepID:     uuid.New(),
		AttemptNumber: 0,
		Trigger:       "initial",
	})
	if err == nil {
		t.Fatal("Create expected error for attempt_number=0")
	}
}

type fakeQueryExecutor struct {
	execFn     func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
}

func (f *fakeQueryExecutor) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if f.execFn != nil {
		return f.execFn(ctx, sql, arguments...)
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeQueryExecutor) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, sql, args...)
	}
	return &fakeRows{}, nil
}

func (f *fakeQueryExecutor) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if f.queryRowFn != nil {
		return f.queryRowFn(ctx, sql, args...)
	}
	return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}

type fakeRow struct {
	scanFn func(dest ...any) error
}

func (f fakeRow) Scan(dest ...any) error {
	if f.scanFn != nil {
		return f.scanFn(dest...)
	}
	return pgx.ErrNoRows
}

type fakeRows struct{}

func (f *fakeRows) Close()                                       {}
func (f *fakeRows) Err() error                                   { return nil }
func (f *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (f *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakeRows) Next() bool                                   { return false }
func (f *fakeRows) Scan(dest ...any) error                       { return pgx.ErrNoRows }
func (f *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (f *fakeRows) RawValues() [][]byte                          { return nil }
func (f *fakeRows) Conn() *pgx.Conn                              { return nil }

func fakeRunRow(id, orgID uuid.UUID, version int, status string) fakeRow {
	return fakeRow{scanFn: func(dest ...any) error {
		now := time.Now().UTC()
		*dest[0].(*uuid.UUID) = id
		*dest[1].(*uuid.UUID) = orgID
		*dest[2].(**uuid.UUID) = nil
		*dest[3].(**uuid.UUID) = nil
		*dest[4].(**uuid.UUID) = nil
		*dest[5].(**uuid.UUID) = nil
		*dest[6].(**uuid.UUID) = nil
		*dest[7].(*string) = "system"
		*dest[8].(*uuid.UUID) = uuid.Nil
		*dest[9].(*string) = status
		*dest[10].(**string) = nil
		*dest[11].(*string) = "api"
		*dest[12].(*int) = version
		*dest[13].(**string) = nil
		*dest[14].(**string) = nil
		*dest[15].(*int) = 0
		*dest[16].(*int) = 0
		*dest[17].(*json.RawMessage) = json.RawMessage(`{}`)
		*dest[18].(*time.Time) = now
		*dest[19].(*time.Time) = now
		*dest[20].(**time.Time) = nil
		*dest[21].(**time.Time) = nil
		return nil
	}}
}
