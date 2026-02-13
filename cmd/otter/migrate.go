package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/ottercli"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

var detectMigrateOpenClawInstallation = importer.DetectOpenClawInstallation
var importMigrateOpenClawAgentIdentities = importer.ImportOpenClawAgentIdentities
var parseMigrateOpenClawSessionEvents = importer.ParseOpenClawSessionEvents
var newOpenClawMigrationRunner = importer.NewOpenClawMigrationRunner
var openMigrateDatabase = openMigrateDatabaseFromEnv
var listMigrateProgressByOrg = func(ctx context.Context, db *sql.DB, orgID string) ([]store.MigrationProgress, error) {
	progressStore := store.NewMigrationProgressStore(db)
	return progressStore.ListByOrg(ctx, orgID)
}
var updateMigrateProgressByOrgStatus = func(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	fromStatus store.MigrationProgressStatus,
	toStatus store.MigrationProgressStatus,
) (int, error) {
	progressStore := store.NewMigrationProgressStore(db)
	return progressStore.UpdateStatusByOrg(ctx, orgID, fromStatus, toStatus)
}

type migrateFromOpenClawOptions struct {
	OpenClawDir string
	AgentsOnly  bool
	HistoryOnly bool
	DryRun      bool
	Since       *time.Time
	OrgID       string
}

type migrateOpenClawDryRunSummary struct {
	Mode             string
	Since            *time.Time
	AgentCount       int
	ConversationRows int
	AgentSlugs       []string
}

func handleMigrate(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter migrate <from-openclaw|status|pause|resume> ...")
		os.Exit(1)
	}

	switch args[0] {
	case "from-openclaw":
		opts, err := parseMigrateFromOpenClawOptions(args[1:])
		if err != nil {
			die(err.Error())
		}
		if err := runMigrateFromOpenClaw(os.Stdout, opts); err != nil {
			dieIf(err)
		}
	case "status":
		orgID, err := parseMigrateOrgOnlyOptions("migrate status", args[1:])
		if err != nil {
			die(err.Error())
		}
		dieIf(runMigrateStatus(os.Stdout, orgID))
	case "pause":
		orgID, err := parseMigrateOrgOnlyOptions("migrate pause", args[1:])
		if err != nil {
			die(err.Error())
		}
		dieIf(runMigratePause(os.Stdout, orgID))
	case "resume":
		orgID, err := parseMigrateOrgOnlyOptions("migrate resume", args[1:])
		if err != nil {
			die(err.Error())
		}
		dieIf(runMigrateResume(os.Stdout, orgID))
	default:
		fmt.Println("usage: otter migrate <from-openclaw|status|pause|resume> ...")
		os.Exit(1)
	}
}

func parseMigrateOrgOnlyOptions(name string, args []string) (string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	orgID := flags.String("org", "", "org id override")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if len(flags.Args()) > 0 {
		return "", fmt.Errorf("unexpected positional argument(s): %s", strings.Join(flags.Args(), " "))
	}
	return strings.TrimSpace(*orgID), nil
}

func parseMigrateFromOpenClawOptions(args []string) (migrateFromOpenClawOptions, error) {
	flags := flag.NewFlagSet("migrate from-openclaw", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	openClawDir := flags.String("openclaw-dir", "", "path to OpenClaw root directory")
	agentsOnly := flags.Bool("agents-only", false, "import agents only")
	historyOnly := flags.Bool("history-only", false, "import history only")
	dryRun := flags.Bool("dry-run", false, "show migration plan without writing")
	sinceRaw := flags.String("since", "", "only import session events at/after this timestamp (RFC3339 or YYYY-MM-DD)")
	orgID := flags.String("org", "", "org id override")

	if err := flags.Parse(args); err != nil {
		return migrateFromOpenClawOptions{}, err
	}
	if len(flags.Args()) > 0 {
		return migrateFromOpenClawOptions{}, fmt.Errorf("unexpected positional argument(s): %s", strings.Join(flags.Args(), " "))
	}
	if *agentsOnly && *historyOnly {
		return migrateFromOpenClawOptions{}, fmt.Errorf("--agents-only and --history-only are mutually exclusive")
	}

	since, err := parseMigrateSince(*sinceRaw)
	if err != nil {
		return migrateFromOpenClawOptions{}, err
	}

	return migrateFromOpenClawOptions{
		OpenClawDir: strings.TrimSpace(*openClawDir),
		AgentsOnly:  *agentsOnly,
		HistoryOnly: *historyOnly,
		DryRun:      *dryRun,
		Since:       since,
		OrgID:       strings.TrimSpace(*orgID),
	}, nil
}

func parseMigrateSince(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse("2006-01-02", raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	return nil, fmt.Errorf("invalid --since value %q (expected RFC3339 or YYYY-MM-DD)", raw)
}

func runMigrateFromOpenClaw(out io.Writer, opts migrateFromOpenClawOptions) error {
	install, err := detectMigrateOpenClawInstallation(importer.DetectOpenClawOptions{
		HomeDir: opts.OpenClawDir,
	})
	if err != nil {
		return err
	}

	identities := make([]importer.ImportedAgentIdentity, 0)
	if !opts.HistoryOnly || opts.DryRun {
		identities, err = importMigrateOpenClawAgentIdentities(install)
		if err != nil {
			return err
		}
	}

	events := make([]importer.OpenClawSessionEvent, 0)
	if !opts.AgentsOnly || opts.DryRun {
		events, err = parseMigrateOpenClawSessionEvents(install)
		if err != nil {
			return err
		}
		if opts.Since != nil {
			events = filterMigrateOpenClawEventsSince(events, *opts.Since)
		}
	}

	if opts.DryRun {
		summary := buildMigrateOpenClawDryRunSummary(opts, identities, events)
		renderMigrateOpenClawDryRun(out, summary)
		return nil
	}

	orgID, err := resolveMigrateOrgID(opts.OrgID)
	if err != nil {
		return err
	}

	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	ctx := context.Background()
	userID := ""

	if !opts.AgentsOnly {
		userID, err = resolveMigrateBackfillUserID(ctx, db, orgID)
		if err != nil {
			return err
		}
	}

	runner := newOpenClawMigrationRunner(db)
	runResult, err := runner.Run(ctx, importer.RunOpenClawMigrationInput{
		OrgID:        orgID,
		UserID:       userID,
		Installation: install,
		ParsedEvents: events,
		AgentsOnly:   opts.AgentsOnly,
		HistoryOnly:  opts.HistoryOnly,
	})
	if err != nil {
		return err
	}
	if runResult.Paused {
		fmt.Fprintln(out, "OpenClaw migration paused at checkpoint.")
		return nil
	}
	fmt.Fprintln(out, "OpenClaw migration complete.")
	return nil
}

func resolveMigrateOrgID(flagOrgID string) (string, error) {
	flagOrgID = strings.TrimSpace(flagOrgID)
	if flagOrgID != "" {
		return flagOrgID, nil
	}

	cfg, err := ottercli.LoadConfig()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.DefaultOrg) == "" {
		return "", fmt.Errorf("org id is required (pass --org or set default org with otter auth login)")
	}
	return strings.TrimSpace(cfg.DefaultOrg), nil
}

func runMigrateStatus(out io.Writer, orgOverride string) error {
	orgID, err := resolveMigrateOrgID(orgOverride)
	if err != nil {
		return err
	}
	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	progressRows, err := listMigrateProgressByOrg(context.Background(), db, orgID)
	if err != nil {
		return err
	}
	if len(progressRows) == 0 {
		fmt.Fprintln(out, "No migration progress found.")
		return nil
	}

	fmt.Fprintln(out, "Migration Status:")
	for _, row := range progressRows {
		progress := fmt.Sprintf("%d", row.ProcessedItems)
		if row.TotalItems != nil && *row.TotalItems > 0 {
			progress = fmt.Sprintf("%d / %d", row.ProcessedItems, *row.TotalItems)
		}
		line := fmt.Sprintf("  %s: %s (%s)", row.MigrationType, row.Status, progress)
		if strings.TrimSpace(row.CurrentLabel) != "" {
			line += " - " + strings.TrimSpace(row.CurrentLabel)
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

func runMigratePause(out io.Writer, orgOverride string) error {
	orgID, err := resolveMigrateOrgID(orgOverride)
	if err != nil {
		return err
	}
	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	updated, err := updateMigrateProgressByOrgStatus(
		context.Background(),
		db,
		orgID,
		store.MigrationProgressStatusRunning,
		store.MigrationProgressStatusPaused,
	)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Paused %d running migration phase(s).\n", updated)
	return nil
}

func runMigrateResume(out io.Writer, orgOverride string) error {
	orgID, err := resolveMigrateOrgID(orgOverride)
	if err != nil {
		return err
	}
	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	updated, err := updateMigrateProgressByOrgStatus(
		context.Background(),
		db,
		orgID,
		store.MigrationProgressStatusPaused,
		store.MigrationProgressStatusRunning,
	)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Resumed %d paused migration phase(s).\n", updated)
	return nil
}

func openMigrateDatabaseFromEnv() (*sql.DB, error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required for otter migrate from-openclaw")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func resolveMigrateBackfillUserID(ctx context.Context, db *sql.DB, orgID string) (string, error) {
	var userID string
	err := db.QueryRowContext(
		ctx,
		`SELECT id::text
		   FROM users
		  WHERE org_id = $1
		  ORDER BY created_at ASC, id ASC
		  LIMIT 1`,
		orgID,
	).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no users found in org %s for history backfill participant mapping", orgID)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(userID), nil
}

func filterMigrateOpenClawEventsSince(events []importer.OpenClawSessionEvent, since time.Time) []importer.OpenClawSessionEvent {
	filtered := make([]importer.OpenClawSessionEvent, 0, len(events))
	sinceUTC := since.UTC()
	for _, event := range events {
		if event.CreatedAt.Before(sinceUTC) {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func buildMigrateOpenClawDryRunSummary(
	opts migrateFromOpenClawOptions,
	identities []importer.ImportedAgentIdentity,
	events []importer.OpenClawSessionEvent,
) migrateOpenClawDryRunSummary {
	mode := "full"
	switch {
	case opts.AgentsOnly:
		mode = "agents-only"
	case opts.HistoryOnly:
		mode = "history-only"
	}

	slugSet := map[string]struct{}{}
	for _, identity := range identities {
		slug := strings.TrimSpace(identity.ID)
		if slug != "" {
			slugSet[slug] = struct{}{}
		}
	}
	for _, event := range events {
		slug := strings.TrimSpace(event.AgentSlug)
		if slug != "" {
			slugSet[slug] = struct{}{}
		}
	}
	agentSlugs := make([]string, 0, len(slugSet))
	for slug := range slugSet {
		agentSlugs = append(agentSlugs, slug)
	}
	sort.Strings(agentSlugs)

	return migrateOpenClawDryRunSummary{
		Mode:             mode,
		Since:            opts.Since,
		AgentCount:       len(uniqueMigrateAgentIdentityIDs(identities)),
		ConversationRows: len(events),
		AgentSlugs:       agentSlugs,
	}
}

func renderMigrateOpenClawDryRun(out io.Writer, summary migrateOpenClawDryRunSummary) {
	fmt.Fprintln(out, "Dry run: otter migrate from-openclaw")
	fmt.Fprintf(out, "Mode: %s\n", summary.Mode)
	if summary.Since != nil {
		fmt.Fprintf(out, "Since: %s\n", summary.Since.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(out, "Agents to import: %d\n", summary.AgentCount)
	fmt.Fprintf(out, "Conversation events to import: %d\n", summary.ConversationRows)
	if len(summary.AgentSlugs) > 0 {
		fmt.Fprintf(out, "Agent slugs: %s\n", strings.Join(summary.AgentSlugs, ", "))
	}
}

func uniqueMigrateAgentIdentityIDs(identities []importer.ImportedAgentIdentity) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(identities))
	for _, identity := range identities {
		slug := strings.TrimSpace(identity.ID)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}
