package migrate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	migrationsfs "github.com/samhotchkiss/otter-camp/migrations"
)

const MigrationsPathEnv = "OTTERCAMP_MIGRATIONS_PATH"

func ResolveMigrationsFSFromEnv() (fs.FS, error) {
	path := strings.TrimSpace(os.Getenv(MigrationsPathEnv))
	if path == "" {
		return migrationsfs.Files, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", MigrationsPathEnv, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s must point to a directory", MigrationsPathEnv)
	}
	return os.DirFS(abs), nil
}

func LoadMigrationsFromEnv() ([]Migration, error) {
	fsys, err := ResolveMigrationsFSFromEnv()
	if err != nil {
		return nil, err
	}
	return LoadMigrations(fsys)
}

func PendingMigrations(all []Migration, applied map[int]struct{}) []Migration {
	return pendingMigrations(all, applied)
}
