// Package automigrate runs pending database migrations on startup.
package automigrate

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Run applies all pending up migrations from the given directory.
func Run(db *sql.DB, migrationsDir string) error {
	// Ensure migrations table exists (use INTEGER version to match existing tool)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Get applied migrations
	applied := make(map[int]bool)
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
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

	type migration struct {
		name    string
		version int
	}
	var pending []migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		// Extract numeric prefix (e.g., "008" from "008_rls_policies.up.sql")
		parts := strings.SplitN(name, "_", 2)
		if len(parts) == 0 {
			continue
		}
		ver, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		if !applied[ver] {
			pending = append(pending, migration{name: name, version: ver})
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].version < pending[j].version })

	if len(pending) == 0 {
		log.Printf("✅ Database up to date (%d migrations applied)", len(applied))
		return nil
	}

	log.Printf("📦 Applying %d pending migration(s)...", len(pending))
	for _, m := range pending {
		name := m.name
		version := m.version
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
