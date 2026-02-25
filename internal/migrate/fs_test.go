package migrate

import (
	"os"
	"testing"
)

func TestLoadMigrationsFromEnvUsesEmbeddedByDefault(t *testing.T) {
	t.Setenv(MigrationsPathEnv, "")
	migrations, err := LoadMigrationsFromEnv()
	if err != nil {
		t.Fatalf("LoadMigrationsFromEnv returned error: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("expected embedded migrations")
	}
}

func TestResolveMigrationsFSFromEnvInvalidPath(t *testing.T) {
	t.Setenv(MigrationsPathEnv, "/definitely/missing/path")
	_, err := ResolveMigrationsFSFromEnv()
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestResolveMigrationsFSFromEnvDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/0001_test.sql", []byte("select 1;"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv(MigrationsPathEnv, dir)
	migrations, err := LoadMigrationsFromEnv()
	if err != nil {
		t.Fatalf("LoadMigrationsFromEnv returned error: %v", err)
	}
	if len(migrations) != 1 || migrations[0].Version != 1 {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
}
