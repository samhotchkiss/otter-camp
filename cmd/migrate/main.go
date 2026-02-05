package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "up":
		runUp(args)
	case "down":
		runDown(args)
	case "create":
		runCreate(args)
	case "force":
		runForce(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [args]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  up [n]        Apply all migrations or the next n migrations")
	fmt.Fprintln(os.Stderr, "  down [n]      Roll back all migrations or the last n migrations")
	fmt.Fprintln(os.Stderr, "  create <name> Create new migration files")
	fmt.Fprintln(os.Stderr, "  force <ver>   Force set the migration version (fixes dirty state)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Environment:")
	fmt.Fprintln(os.Stderr, "  DATABASE_URL  PostgreSQL connection string")
}

func runUp(args []string) {
	m, err := newMigrator()
	if err != nil {
		exitWithError(err)
	}
	defer closeMigrator(m)

	if len(args) == 0 {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			exitWithError(err)
		}
		return
	}

	steps, err := parseSteps(args[0])
	if err != nil {
		exitWithError(err)
	}
	if err := m.Steps(steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		exitWithError(err)
	}
}

func runDown(args []string) {
	m, err := newMigrator()
	if err != nil {
		exitWithError(err)
	}
	defer closeMigrator(m)

	if len(args) == 0 {
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			exitWithError(err)
		}
		return
	}

	steps, err := parseSteps(args[0])
	if err != nil {
		exitWithError(err)
	}
	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		exitWithError(err)
	}
}

func runForce(args []string) {
	if len(args) == 0 {
		exitWithError(errors.New("version number is required"))
	}

	version, err := strconv.Atoi(args[0])
	if err != nil {
		exitWithError(fmt.Errorf("invalid version: %s", args[0]))
	}

	m, err := newMigrator()
	if err != nil {
		exitWithError(err)
	}
	defer closeMigrator(m)

	if err := m.Force(version); err != nil {
		exitWithError(err)
	}
	fmt.Printf("Forced version to %d\n", version)
}

func runCreate(args []string) {
	if len(args) == 0 {
		exitWithError(errors.New("migration name is required"))
	}

	name := sanitizeName(args[0])
	if name == "" {
		exitWithError(errors.New("migration name must include at least one alphanumeric character"))
	}

	dir, err := migrationsDir()
	if err != nil {
		exitWithError(err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		exitWithError(err)
	}

	timestamp := time.Now().UTC().Format("20060102150405")
	base := fmt.Sprintf("%s_%s", timestamp, name)
	upPath := filepath.Join(dir, base+".up.sql")
	downPath := filepath.Join(dir, base+".down.sql")

	if err := writeMigrationFile(upPath, "-- migrate up\n"); err != nil {
		exitWithError(err)
	}
	if err := writeMigrationFile(downPath, "-- migrate down\n"); err != nil {
		exitWithError(err)
	}

	fmt.Printf("Created %s and %s\n", upPath, downPath)
}

func newMigrator() (*migrate.Migrate, error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}

	dir, err := migrationsDir()
	if err != nil {
		return nil, err
	}

	sourceURL := "file://" + dir
	m, err := migrate.New(sourceURL, databaseURL)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func migrationsDir() (string, error) {
	return filepath.Abs("migrations")
}

func parseSteps(value string) (int, error) {
	steps, err := strconv.Atoi(value)
	if err != nil || steps <= 0 {
		return 0, fmt.Errorf("invalid steps: %s", value)
	}
	return steps, nil
}

func sanitizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")

	re := regexp.MustCompile(`[^a-z0-9_]+`)
	name = re.ReplaceAllString(name, "")
	name = strings.Trim(name, "_")
	return name
}

func writeMigrationFile(path string, contents string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.WriteString(contents); err != nil {
		return err
	}
	return nil
}

func closeMigrator(m *migrate.Migrate) {
	sourceErr, dbErr := m.Close()
	if sourceErr != nil {
		fmt.Fprintf(os.Stderr, "source close error: %v\n", sourceErr)
	}
	if dbErr != nil {
		fmt.Fprintf(os.Stderr, "db close error: %v\n", dbErr)
	}
}

func exitWithError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
	usage()
	os.Exit(1)
}
