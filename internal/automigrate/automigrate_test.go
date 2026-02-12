package automigrate

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRunRecordsVersionWhenSchemaMigrationsHasNoDirtyColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	migrationsDir := writeTestMigration(t, "001_create_widgets.up.sql", "CREATE TABLE widgets (id INTEGER);")

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version FROM schema_migrations ORDER BY version").
		WillReturnRows(sqlmock.NewRows([]string{"version"}))
	mock.ExpectQuery("SELECT EXISTS \\(").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE widgets (id INTEGER);")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO schema_migrations (version) VALUES ($1)")).
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := Run(db, migrationsDir); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunRecordsVersionWhenSchemaMigrationsHasDirtyColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	migrationsDir := writeTestMigration(t, "001_create_widgets.up.sql", "CREATE TABLE widgets (id INTEGER);")

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version FROM schema_migrations ORDER BY version").
		WillReturnRows(sqlmock.NewRows([]string{"version"}))
	mock.ExpectQuery("SELECT EXISTS \\(").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE widgets (id INTEGER);")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO schema_migrations (version, dirty) VALUES ($1, false)")).
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := Run(db, migrationsDir); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunSkipPathRecordsVersionWithoutDirtyColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	migrationsDir := writeTestMigration(t, "001_create_widgets.up.sql", "CREATE TABLE widgets (id INTEGER);")

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT version FROM schema_migrations ORDER BY version").
		WillReturnRows(sqlmock.NewRows([]string{"version"}))
	mock.ExpectQuery("SELECT EXISTS \\(").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE widgets (id INTEGER);")).
		WillReturnError(errors.New(`relation "widgets" already exists`))
	mock.ExpectRollback()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO schema_migrations (version) VALUES ($1)")).
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := Run(db, migrationsDir); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func writeTestMigration(t *testing.T, filename, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(contents), 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}
	return dir
}
