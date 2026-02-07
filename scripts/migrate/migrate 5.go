// Package main provides a data migration CLI tool for Otter Camp.
// It supports up/down migrations, tracking applied migrations, dry-run mode, and status reporting.
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// Migration represents a single migration file.
type Migration struct {
	Version   int
	Name      string
	UpFile    string
	DownFile  string
	AppliedAt *time.Time
}

// MigrationRecord represents a row in schema_migrations.
type MigrationRecord struct {
	Version   int
	AppliedAt time.Time
}

var (
	dryRun       bool
	migrationsDir string
	verbose      bool
)

func main() {
	flag.BoolVar(&dryRun, "dry-run", false, "Print SQL statements without executing")
	flag.BoolVar(&dryRun, "n", false, "Print SQL statements without executing (shorthand)")
	flag.StringVar(&migrationsDir, "dir", "migrations", "Directory containing migration files")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	command := args[0]
	cmdArgs := args[1:]

	var err error
	switch command {
	case "up":
		err = runUp(cmdArgs)
	case "down":
		err = runDown(cmdArgs)
	case "status":
		err = runStatus()
	case "version":
		err = runVersion()
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: migrate [options] <command> [args]\n\n")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  up [n]     Apply all pending migrations or the next n migrations")
	fmt.Fprintln(os.Stderr, "  down [n]   Roll back the last migration or the last n migrations")
	fmt.Fprintln(os.Stderr, "  status     Show migration status")
	fmt.Fprintln(os.Stderr, "  version    Show current migration version")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Options:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Environment:")
	fmt.Fprintln(os.Stderr, "  DATABASE_URL  PostgreSQL connection string (required)")
}

// connectDB establishes a database connection.
func connectDB() (*sql.DB, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL environment variable is not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// ensureMigrationsTable creates the schema_migrations table if it doesn't exist.
func ensureMigrationsTable(db *sql.DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		);
	`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}
	return nil
}

// getAppliedMigrations returns all applied migration versions.
func getAppliedMigrations(db *sql.DB) (map[int]time.Time, error) {
	rows, err := db.Query("SELECT version, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("failed to query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]time.Time)
	for rows.Next() {
		var version int
		var appliedAt time.Time
		if err := rows.Scan(&version, &appliedAt); err != nil {
			return nil, fmt.Errorf("failed to scan migration record: %w", err)
		}
		applied[version] = appliedAt
	}

	return applied, rows.Err()
}

// getCurrentVersion returns the highest applied migration version.
func getCurrentVersion(db *sql.DB) (int, error) {
	var version sql.NullInt64
	err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get current version: %w", err)
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

// discoverMigrations finds all migration files in the migrations directory.
func discoverMigrations() ([]Migration, error) {
	dir, err := filepath.Abs(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve migrations directory: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Pattern: NNN_name.up.sql or NNN_name.down.sql
	pattern := regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)

	migrationsMap := make(map[int]*Migration)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := pattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}

		version, _ := strconv.Atoi(matches[1])
		name := matches[2]
		direction := matches[3]
		fullPath := filepath.Join(dir, entry.Name())

		m, exists := migrationsMap[version]
		if !exists {
			m = &Migration{Version: version, Name: name}
			migrationsMap[version] = m
		}

		if direction == "up" {
			m.UpFile = fullPath
		} else {
			m.DownFile = fullPath
		}
	}

	// Convert map to sorted slice
	migrations := make([]Migration, 0, len(migrationsMap))
	for _, m := range migrationsMap {
		migrations = append(migrations, *m)
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// runUp applies pending migrations.
func runUp(args []string) error {
	limit := -1 // -1 means apply all
	if len(args) > 0 {
		var err error
		limit, err = strconv.Atoi(args[0])
		if err != nil || limit <= 0 {
			return fmt.Errorf("invalid step count: %s", args[0])
		}
	}

	migrations, err := discoverMigrations()
	if err != nil {
		return err
	}

	if dryRun {
		return dryRunUp(migrations, limit)
	}

	db, err := connectDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	applied, err := getAppliedMigrations(db)
	if err != nil {
		return err
	}

	// Find pending migrations
	var pending []Migration
	for _, m := range migrations {
		if _, ok := applied[m.Version]; !ok {
			pending = append(pending, m)
		}
	}

	if len(pending) == 0 {
		fmt.Println("No pending migrations")
		return nil
	}

	if limit > 0 && limit < len(pending) {
		pending = pending[:limit]
	}

	fmt.Printf("Applying %d migration(s)...\n", len(pending))

	for _, m := range pending {
		if m.UpFile == "" {
			return fmt.Errorf("migration %03d has no up file", m.Version)
		}

		content, err := os.ReadFile(m.UpFile)
		if err != nil {
			return fmt.Errorf("failed to read migration %03d: %w", m.Version, err)
		}

		if verbose {
			fmt.Printf("==> %03d_%s (up)\n", m.Version, m.Name)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to apply migration %03d: %w", m.Version, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES ($1)", m.Version); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %03d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %03d: %w", m.Version, err)
		}

		fmt.Printf("  ✓ %03d_%s\n", m.Version, m.Name)
	}

	fmt.Println("Done!")
	return nil
}

// runDown rolls back migrations.
func runDown(args []string) error {
	limit := 1 // default to rolling back 1 migration
	if len(args) > 0 {
		var err error
		limit, err = strconv.Atoi(args[0])
		if err != nil || limit <= 0 {
			return fmt.Errorf("invalid step count: %s", args[0])
		}
	}

	migrations, err := discoverMigrations()
	if err != nil {
		return err
	}

	if dryRun {
		return dryRunDown(migrations, limit)
	}

	db, err := connectDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	applied, err := getAppliedMigrations(db)
	if err != nil {
		return err
	}

	// Find applied migrations in reverse order
	var toRollback []Migration
	for i := len(migrations) - 1; i >= 0; i-- {
		m := migrations[i]
		if _, ok := applied[m.Version]; ok {
			toRollback = append(toRollback, m)
			if len(toRollback) >= limit {
				break
			}
		}
	}

	if len(toRollback) == 0 {
		fmt.Println("No migrations to roll back")
		return nil
	}

	fmt.Printf("Rolling back %d migration(s)...\n", len(toRollback))

	for _, m := range toRollback {
		if m.DownFile == "" {
			return fmt.Errorf("migration %03d has no down file", m.Version)
		}

		content, err := os.ReadFile(m.DownFile)
		if err != nil {
			return fmt.Errorf("failed to read migration %03d down: %w", m.Version, err)
		}

		if verbose {
			fmt.Printf("==> %03d_%s (down)\n", m.Version, m.Name)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to roll back migration %03d: %w", m.Version, err)
		}

		if _, err := tx.Exec("DELETE FROM schema_migrations WHERE version = $1", m.Version); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to remove migration record %03d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit rollback %03d: %w", m.Version, err)
		}

		fmt.Printf("  ✓ %03d_%s (rolled back)\n", m.Version, m.Name)
	}

	fmt.Println("Done!")
	return nil
}

// runStatus shows the status of all migrations.
func runStatus() error {
	migrations, err := discoverMigrations()
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Println("Migration files discovered:")
		for _, m := range migrations {
			hasUp := "✓"
			hasDown := "✓"
			if m.UpFile == "" {
				hasUp = "✗"
			}
			if m.DownFile == "" {
				hasDown = "✗"
			}
			fmt.Printf("  %03d_%s  [up: %s, down: %s]\n", m.Version, m.Name, hasUp, hasDown)
		}
		return nil
	}

	db, err := connectDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	applied, err := getAppliedMigrations(db)
	if err != nil {
		return err
	}

	fmt.Println("Migration Status:")
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("%-6s %-35s %s\n", "VER", "NAME", "STATUS")
	fmt.Println(strings.Repeat("-", 60))

	pendingCount := 0
	appliedCount := 0

	for _, m := range migrations {
		status := "pending"
		if appliedAt, ok := applied[m.Version]; ok {
			status = fmt.Sprintf("applied %s", appliedAt.Format("2006-01-02 15:04"))
			appliedCount++
		} else {
			pendingCount++
		}
		fmt.Printf("%03d    %-35s %s\n", m.Version, truncate(m.Name, 35), status)
	}

	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("Applied: %d, Pending: %d, Total: %d\n", appliedCount, pendingCount, len(migrations))

	return nil
}

// runVersion shows the current migration version.
func runVersion() error {
	if dryRun {
		fmt.Println("Would query current migration version from database")
		return nil
	}

	db, err := connectDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	version, err := getCurrentVersion(db)
	if err != nil {
		return err
	}

	if version == 0 {
		fmt.Println("No migrations applied")
	} else {
		fmt.Printf("Current version: %d\n", version)
	}

	return nil
}

// dryRunUp shows what migrations would be applied without executing them.
func dryRunUp(migrations []Migration, limit int) error {
	fmt.Println("[DRY RUN] Would apply the following migrations:")
	fmt.Println()

	count := 0
	for _, m := range migrations {
		if limit > 0 && count >= limit {
			break
		}
		if m.UpFile == "" {
			fmt.Printf("==> %03d_%s (MISSING UP FILE)\n", m.Version, m.Name)
			continue
		}

		content, err := os.ReadFile(m.UpFile)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", m.UpFile, err)
		}

		fmt.Printf("==> %03d_%s (up)\n", m.Version, m.Name)
		fmt.Println(strings.Repeat("-", 60))
		fmt.Println(strings.TrimSpace(string(content)))
		fmt.Println()
		count++
	}

	if count == 0 {
		fmt.Println("No migrations to apply")
	} else {
		fmt.Printf("[DRY RUN] Would apply %d migration(s)\n", count)
	}

	return nil
}

// dryRunDown shows what migrations would be rolled back without executing them.
func dryRunDown(migrations []Migration, limit int) error {
	fmt.Println("[DRY RUN] Would roll back the following migrations:")
	fmt.Println()

	count := 0
	for i := len(migrations) - 1; i >= 0 && count < limit; i-- {
		m := migrations[i]
		if m.DownFile == "" {
			fmt.Printf("==> %03d_%s (MISSING DOWN FILE)\n", m.Version, m.Name)
			count++
			continue
		}

		content, err := os.ReadFile(m.DownFile)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", m.DownFile, err)
		}

		fmt.Printf("==> %03d_%s (down)\n", m.Version, m.Name)
		fmt.Println(strings.Repeat("-", 60))
		fmt.Println(strings.TrimSpace(string(content)))
		fmt.Println()
		count++
	}

	if count == 0 {
		fmt.Println("No migrations to roll back")
	} else {
		fmt.Printf("[DRY RUN] Would roll back %d migration(s)\n", count)
	}

	return nil
}

// truncate shortens a string to the given length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
