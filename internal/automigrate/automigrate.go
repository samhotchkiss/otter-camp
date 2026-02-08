// Package automigrate runs pending database migrations on startup.
package automigrate

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Run applies all pending up migrations from the given directory.
func Run(db *sql.DB, migrationsDir string) error {
	// Ensure migrations table exists
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Get applied migrations
	applied := make(map[string]bool)
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("scan migration version: %w", err)
		}
		applied[v] = true
	}

	// Find pending up migrations
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var pending []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		version := strings.TrimSuffix(name, ".up.sql")
		if !applied[version] {
			pending = append(pending, name)
		}
	}
	sort.Strings(pending)

	if len(pending) == 0 {
		log.Printf("✅ Database up to date (%d migrations applied)", len(applied))
		return nil
	}

	log.Printf("📦 Applying %d pending migration(s)...", len(pending))
	for _, name := range pending {
		version := strings.TrimSuffix(name, ".up.sql")
		path := filepath.Join(migrationsDir, name)
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			// If migration fails (e.g. "already exists"), mark it as applied and continue
			errStr := err.Error()
			if strings.Contains(errStr, "already exists") || strings.Contains(errStr, "duplicate key") {
				log.Printf("  ⏭️  Skipped (already applied): %s", version)
				// Record it so we don't retry
				db.Exec("INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING", version)
				continue
			}
			return fmt.Errorf("apply %s: %w", name, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}

		log.Printf("  ✅ Applied: %s", version)
	}

	log.Printf("✅ All migrations applied (%d new, %d total)", len(pending), len(applied)+len(pending))
	return nil
}
