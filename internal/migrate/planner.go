package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

var migrationFilePattern = regexp.MustCompile(`^(\d{4})_([a-zA-Z0-9_\-]+)\.sql$`)

type Migration struct {
	Version int
	Name    string
	File    string
	SQL     string
}

type AppliedVersionProvider interface {
	AppliedVersions(ctx context.Context) (map[int]struct{}, error)
}

func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migration dir: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))
	seen := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		matches := migrationFilePattern.FindStringSubmatch(filename)
		if len(matches) != 3 {
			continue
		}

		version, err := strconv.Atoi(matches[1])
		if err != nil {
			return nil, fmt.Errorf("parse version from %q: %w", filename, err)
		}

		if prior, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %04d: %q and %q", version, prior, filename)
		}
		seen[version] = filename

		sqlBytes, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", filename, err)
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    matches[2],
			File:    filepath.Clean(filename),
			SQL:     string(sqlBytes),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version == migrations[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %04d", migrations[i].Version)
		}
	}

	return migrations, nil
}

func PlanPending(ctx context.Context, provider AppliedVersionProvider, all []Migration) ([]Migration, error) {
	applied, err := provider.AppliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	return pendingMigrations(all, applied), nil
}

func pendingMigrations(all []Migration, applied map[int]struct{}) []Migration {
	pending := make([]Migration, 0, len(all))
	for _, m := range all {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		pending = append(pending, m)
	}
	return pending
}
