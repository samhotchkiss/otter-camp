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
)

var detectMigrateOpenClawInstallation = importer.DetectOpenClawInstallation
var importMigrateOpenClawAgentIdentities = importer.ImportOpenClawAgentIdentities
var importMigrateOpenClawAgents = importer.ImportOpenClawAgents
var parseMigrateOpenClawSessionEvents = importer.ParseOpenClawSessionEvents
var backfillMigrateOpenClawHistory = importer.BackfillOpenClawHistory

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
		fmt.Println("usage: otter migrate from-openclaw [--openclaw-dir <path>] [--agents-only|--history-only] [--dry-run] [--since <date>] [--org <org-id>]")
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
	default:
		fmt.Println("usage: otter migrate from-openclaw [--openclaw-dir <path>] [--agents-only|--history-only] [--dry-run] [--since <date>] [--org <org-id>]")
		os.Exit(1)
	}
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

	db, err := openMigrateDatabaseFromEnv()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()

	if !opts.HistoryOnly {
		if _, err := importMigrateOpenClawAgents(ctx, db, importer.OpenClawAgentImportOptions{
			OrgID:         orgID,
			Installation:  install,
			SummaryWriter: out,
		}); err != nil {
			return err
		}
	}

	if !opts.AgentsOnly {
		userID, err := resolveMigrateBackfillUserID(ctx, db, orgID)
		if err != nil {
			return err
		}
		if _, err := backfillMigrateOpenClawHistory(ctx, db, importer.OpenClawHistoryBackfillOptions{
			OrgID:         orgID,
			UserID:        userID,
			ParsedEvents:  events,
			SummaryWriter: out,
		}); err != nil {
			return err
		}
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
