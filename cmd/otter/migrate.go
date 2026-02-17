package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/url"
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
var loadMigrateConfig = ottercli.LoadConfig
var newMigrateClient = ottercli.NewClient
var openMigrateDatabase = openMigrateDatabaseFromEnv
var resolveMigrateBackfillUserIDFn = resolveMigrateBackfillUserID
var migrateWhoAmIUserID = func(client *ottercli.Client) (string, error) {
	whoami, err := client.WhoAmI()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(whoami.User.ID), nil
}
var importMigrateOpenClawAgentsAPI = func(
	client *ottercli.Client,
	input ottercli.OpenClawMigrationImportAgentsInput,
) (ottercli.OpenClawMigrationImportAgentsResult, error) {
	return client.ImportOpenClawMigrationAgents(input)
}
var importMigrateOpenClawHistoryBatchAPI = func(
	client *ottercli.Client,
	input ottercli.OpenClawMigrationImportHistoryBatchInput,
) (ottercli.OpenClawMigrationImportHistoryBatchResult, error) {
	return client.ImportOpenClawMigrationHistoryBatch(input)
}
var runMigrateOpenClawAPI = func(
	client *ottercli.Client,
	input ottercli.OpenClawMigrationRunRequest,
) (ottercli.OpenClawMigrationRunResponse, error) {
	return client.RunOpenClawMigration(input)
}
var statusMigrateOpenClawAPI = func(
	client *ottercli.Client,
) (ottercli.OpenClawMigrationStatus, error) {
	return client.GetOpenClawMigrationStatus()
}
var reportMigrateOpenClawAPI = func(
	client *ottercli.Client,
) (ottercli.OpenClawMigrationReport, error) {
	return client.GetOpenClawMigrationReport()
}
var pauseMigrateOpenClawAPI = func(
	client *ottercli.Client,
) (ottercli.OpenClawMigrationMutationResponse, error) {
	return client.PauseOpenClawMigration()
}
var resumeMigrateOpenClawAPI = func(
	client *ottercli.Client,
) (ottercli.OpenClawMigrationMutationResponse, error) {
	return client.ResumeOpenClawMigration()
}
var resetMigrateOpenClawAPI = func(
	client *ottercli.Client,
	input ottercli.OpenClawMigrationResetRequest,
) (ottercli.OpenClawMigrationResetResponse, error) {
	return client.ResetOpenClawMigration(input)
}
var runMigrateRunner = func(
	ctx context.Context,
	runner *importer.OpenClawMigrationRunner,
	input importer.RunOpenClawMigrationInput,
) (importer.RunOpenClawMigrationResult, error) {
	return runner.Run(ctx, input)
}
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

var (
	migrateExecutionTimeout         = 30 * time.Minute
	migrateQueryTimeout             = 30 * time.Second
	migrateStatusPollInterval       = 500 * time.Millisecond
	migrateStatusPollAttempts       = 60
	migrateOpenClawHistoryBatchSize = 1000
	sleepMigrateStatusPoll          = time.Sleep
)

type migrateFromOpenClawOptions struct {
	OpenClawDir string
	AgentsOnly  bool
	HistoryOnly bool
	DryRun      bool
	Since       *time.Time
	OrgID       string
	Transport   migrateTransport
}

type migrateTransport string

const (
	migrateTransportAuto migrateTransport = "auto"
	migrateTransportAPI  migrateTransport = "api"
	migrateTransportDB   migrateTransport = "db"

	migrateOpenClawResetConfirmToken = "RESET_OPENCLAW_MIGRATION"
)

type migrateOrgOnlyOptions struct {
	OrgID     string
	Transport migrateTransport
}

type migrateFromOpenClawResetOptions struct {
	Confirm   bool
	OrgID     string
	Transport migrateTransport
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
		if len(args) > 1 {
			switch args[1] {
			case "cron":
				// Sub-subcommand: "from-openclaw cron" imports cron jobs into agent_jobs
				handleMigrateFromOpenClawCron(args[2:])
				return
			case "reset":
				resetOpts, err := parseMigrateFromOpenClawResetOptions(args[2:])
				if err != nil {
					die(err.Error())
				}
				dieIf(runMigrateFromOpenClawReset(os.Stdout, resetOpts))
				return
			}
		}
		opts, err := parseMigrateFromOpenClawOptions(args[1:])
		if err != nil {
			die(err.Error())
		}
		if err := runMigrateFromOpenClaw(os.Stdout, opts); err != nil {
			dieIf(err)
		}
	case "status":
		opts, err := parseMigrateOrgOnlyOptions("migrate status", args[1:])
		if err != nil {
			die(err.Error())
		}
		dieIf(runMigrateStatusWithTransport(os.Stdout, opts.OrgID, opts.Transport))
	case "pause":
		opts, err := parseMigrateOrgOnlyOptions("migrate pause", args[1:])
		if err != nil {
			die(err.Error())
		}
		dieIf(runMigratePauseWithTransport(os.Stdout, opts.OrgID, opts.Transport))
	case "resume":
		opts, err := parseMigrateOrgOnlyOptions("migrate resume", args[1:])
		if err != nil {
			die(err.Error())
		}
		dieIf(runMigrateResumeWithTransport(os.Stdout, opts.OrgID, opts.Transport))
	default:
		fmt.Println("usage: otter migrate <from-openclaw|status|pause|resume> ...")
		os.Exit(1)
	}
}

func parseMigrateOrgOnlyOptions(name string, args []string) (migrateOrgOnlyOptions, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	orgID := flags.String("org", "", "org id override")
	transportRaw := flags.String("transport", string(migrateTransportAuto), "migration transport (auto|api|db)")
	if err := flags.Parse(args); err != nil {
		return migrateOrgOnlyOptions{}, err
	}
	if len(flags.Args()) > 0 {
		return migrateOrgOnlyOptions{}, fmt.Errorf("unexpected positional argument(s): %s", strings.Join(flags.Args(), " "))
	}
	transport, err := parseMigrateTransport(*transportRaw)
	if err != nil {
		return migrateOrgOnlyOptions{}, err
	}
	return migrateOrgOnlyOptions{
		OrgID:     strings.TrimSpace(*orgID),
		Transport: transport,
	}, nil
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
	transportRaw := flags.String("transport", string(migrateTransportAuto), "migration transport (auto|api|db)")

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
	transport, err := parseMigrateTransport(*transportRaw)
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
		Transport:   transport,
	}, nil
}

func parseMigrateFromOpenClawResetOptions(args []string) (migrateFromOpenClawResetOptions, error) {
	flags := flag.NewFlagSet("migrate from-openclaw reset", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	confirm := flags.Bool("confirm", false, "confirm destructive OpenClaw migration reset")
	orgID := flags.String("org", "", "org id override")
	transportRaw := flags.String("transport", string(migrateTransportAuto), "migration transport (auto|api|db)")

	if err := flags.Parse(args); err != nil {
		return migrateFromOpenClawResetOptions{}, err
	}
	if len(flags.Args()) > 0 {
		return migrateFromOpenClawResetOptions{}, fmt.Errorf("unexpected positional argument(s): %s", strings.Join(flags.Args(), " "))
	}

	transport, err := parseMigrateTransport(*transportRaw)
	if err != nil {
		return migrateFromOpenClawResetOptions{}, err
	}

	return migrateFromOpenClawResetOptions{
		Confirm:   *confirm,
		OrgID:     strings.TrimSpace(*orgID),
		Transport: transport,
	}, nil
}

func parseMigrateTransport(raw string) (migrateTransport, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return migrateTransportAuto, nil
	}
	switch migrateTransport(value) {
	case migrateTransportAuto, migrateTransportAPI, migrateTransportDB:
		return migrateTransport(value), nil
	default:
		return "", fmt.Errorf("invalid --transport value %q (expected auto|api|db)", strings.TrimSpace(raw))
	}
}

func resolveMigrateTransport(raw migrateTransport, apiBaseURL string) migrateTransport {
	switch raw {
	case migrateTransportAPI:
		return migrateTransportAPI
	case migrateTransportDB:
		return migrateTransportDB
	default:
		if isLocalMigrateAPIBaseURL(apiBaseURL) {
			return migrateTransportDB
		}
		return migrateTransportAPI
	}
}

func isLocalMigrateAPIBaseURL(apiBaseURL string) bool {
	value := strings.TrimSpace(apiBaseURL)
	if value == "" {
		return true
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return true
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		host = strings.ToLower(strings.TrimSpace(parsed.Host))
	}
	if host == "" {
		return true
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" {
		return true
	}
	if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	return false
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

	transport, err := resolveMigrateTransportForExecution(opts.Transport)
	if err != nil {
		return err
	}
	if transport == migrateTransportAPI {
		return runMigrateFromOpenClawAPI(out, opts, orgID, install, identities, events)
	}

	return runMigrateFromOpenClawDB(out, opts, orgID, install, events)
}

func runMigrateFromOpenClawReset(out io.Writer, opts migrateFromOpenClawResetOptions) error {
	if !opts.Confirm {
		return fmt.Errorf("--confirm is required to reset OpenClaw migration artifacts")
	}

	orgID, err := resolveMigrateOrgID(opts.OrgID)
	if err != nil {
		return err
	}

	transport, err := resolveMigrateTransportForExecution(opts.Transport)
	if err != nil {
		return err
	}
	if transport != migrateTransportAPI {
		return fmt.Errorf("openclaw reset is only available with api transport")
	}

	cfg, err := loadMigrateConfig()
	if err != nil {
		return err
	}
	client, err := newMigrateClient(cfg, orgID)
	if err != nil {
		return err
	}

	response, err := resetMigrateOpenClawAPI(client, ottercli.OpenClawMigrationResetRequest{
		Confirm: migrateOpenClawResetConfirmToken,
	})
	if err != nil {
		return err
	}

	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = "reset"
	}
	fmt.Fprintf(out, "OpenClaw migration %s complete.\n", status)
	fmt.Fprintf(out, "  paused_phases=%d\n", response.PausedPhases)
	fmt.Fprintf(out, "  progress_rows_deleted=%d\n", response.ProgressRowsDeleted)
	if len(response.Deleted) > 0 {
		keys := make([]string, 0, len(response.Deleted))
		for key := range response.Deleted {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(out, "  deleted.%s=%d\n", key, response.Deleted[key])
		}
	}
	fmt.Fprintf(out, "  total_deleted=%d\n", response.TotalDeleted)
	return nil
}

func resolveMigrateTransportForExecution(requested migrateTransport) (migrateTransport, error) {
	switch requested {
	case migrateTransportAPI:
		return migrateTransportAPI, nil
	case migrateTransportDB:
		return migrateTransportDB, nil
	case "", migrateTransportAuto:
		cfg, err := loadMigrateConfig()
		if err != nil {
			return "", err
		}
		return resolveMigrateTransport(migrateTransportAuto, cfg.APIBaseURL), nil
	default:
		return "", fmt.Errorf("invalid migrate transport %q", requested)
	}
}

func runMigrateFromOpenClawDB(
	out io.Writer,
	opts migrateFromOpenClawOptions,
	orgID string,
	install *importer.OpenClawInstallation,
	events []importer.OpenClawSessionEvent,
) error {

	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrateExecutionTimeout)
	defer cancel()
	userID := ""

	if !opts.AgentsOnly {
		userID, err = resolveMigrateBackfillUserIDFn(ctx, db, orgID)
		if err != nil {
			return err
		}
	}

	runner := newOpenClawMigrationRunner(db)
	if !opts.AgentsOnly && out != nil {
		startedAt := time.Now()
		runner.OnHistoryCheckpoint = func(processed, total int) {
			renderMigrateHistoryProgress(out, processed, total, startedAt)
		}
	}
	runResult, err := runMigrateRunner(ctx, runner, importer.RunOpenClawMigrationInput{
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
	renderMigrateOpenClawSummary(out, runResult.Summary)
	if runResult.Paused {
		fmt.Fprintln(out, "OpenClaw migration paused at checkpoint.")
		return nil
	}
	fmt.Fprintln(out, "OpenClaw migration complete.")
	return nil
}

type migrateOpenClawAPISummary struct {
	agentsImported   bool
	agentsProcessed  int
	agentsInserted   int
	agentsUpdated    int
	agentsSkipped    int
	historyImported  bool
	eventsReceived   int
	eventsProcessed  int
	messagesInserted int
	eventsSkipped    int
	failedItems      int
	report           *ottercli.OpenClawMigrationReport
	status           *ottercli.OpenClawMigrationStatus
	warnings         []string
}

func runMigrateFromOpenClawAPI(
	out io.Writer,
	opts migrateFromOpenClawOptions,
	orgID string,
	_ *importer.OpenClawInstallation,
	identities []importer.ImportedAgentIdentity,
	events []importer.OpenClawSessionEvent,
) error {
	cfg, err := loadMigrateConfig()
	if err != nil {
		return err
	}
	client, err := newMigrateClient(cfg, orgID)
	if err != nil {
		return err
	}

	summary := migrateOpenClawAPISummary{}

	if !opts.HistoryOnly {
		agentResult, importErr := importMigrateOpenClawAgentsAPI(
			client,
			ottercli.OpenClawMigrationImportAgentsInput{
				Identities: mapMigrateOpenClawIdentitiesForAPI(identities),
			},
		)
		if importErr != nil {
			return importErr
		}
		summary.agentsImported = true
		summary.agentsProcessed = agentResult.Processed
		summary.agentsInserted = agentResult.Inserted
		summary.agentsUpdated = agentResult.Updated
		summary.agentsSkipped = agentResult.Skipped
		summary.warnings = append(summary.warnings, agentResult.Warnings...)
	}

	if !opts.AgentsOnly {
		userID, userErr := migrateWhoAmIUserID(client)
		if userErr != nil {
			return userErr
		}
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return fmt.Errorf("failed to resolve user id for OpenClaw history import")
		}

		batches := chunkMigrateOpenClawEvents(events, migrateOpenClawHistoryBatchSize)
		totalBatches := len(batches)
		batchID := fmt.Sprintf("cli-%d", time.Now().UTC().UnixNano())
		for idx, batchEvents := range batches {
			historyResult, importErr := importMigrateOpenClawHistoryBatchAPI(
				client,
				ottercli.OpenClawMigrationImportHistoryBatchInput{
					UserID: userID,
					Batch: ottercli.OpenClawMigrationImportBatch{
						ID:    batchID,
						Index: idx + 1,
						Total: totalBatches,
					},
					Events: mapMigrateOpenClawEventsForAPI(batchEvents),
				},
			)
			if importErr != nil {
				return importErr
			}
			summary.historyImported = true
			summary.eventsReceived += historyResult.EventsReceived
			summary.eventsProcessed += historyResult.EventsProcessed
			summary.messagesInserted += historyResult.MessagesInserted
			summary.eventsSkipped += historyResult.EventsSkippedUnknownAgent
			summary.failedItems += historyResult.FailedItems
			summary.warnings = append(summary.warnings, historyResult.Warnings...)
		}
	}

	if !opts.AgentsOnly && !opts.HistoryOnly {
		_, err = runMigrateOpenClawAPI(
			client,
			ottercli.OpenClawMigrationRunRequest{StartPhase: "memory_extraction"},
		)
		if err != nil {
			return err
		}
		status, statusErr := pollMigrateOpenClawAPIStatus(client)
		if statusErr != nil {
			return statusErr
		}
		summary.status = status
	}
	if summary.historyImported {
		report, reportErr := reportMigrateOpenClawAPI(client)
		if reportErr != nil {
			summary.warnings = append(summary.warnings, "failed to fetch migration reconciliation report")
		} else {
			summary.report = &report
		}
	}

	renderMigrateOpenClawAPISummary(out, summary)
	if summary.status != nil && summary.status.Active {
		fmt.Fprintln(out, "OpenClaw migration still active. Re-run `otter migrate status --transport api` to monitor progress.")
		return nil
	}
	fmt.Fprintln(out, "OpenClaw migration complete.")
	return nil
}

func chunkMigrateOpenClawEvents(events []importer.OpenClawSessionEvent, chunkSize int) [][]importer.OpenClawSessionEvent {
	if len(events) == 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = len(events)
	}
	chunks := make([][]importer.OpenClawSessionEvent, 0, (len(events)+chunkSize-1)/chunkSize)
	for start := 0; start < len(events); start += chunkSize {
		end := start + chunkSize
		if end > len(events) {
			end = len(events)
		}
		chunks = append(chunks, events[start:end])
	}
	return chunks
}

func mapMigrateOpenClawIdentitiesForAPI(
	identities []importer.ImportedAgentIdentity,
) []ottercli.OpenClawMigrationImportAgentIdentity {
	out := make([]ottercli.OpenClawMigrationImportAgentIdentity, 0, len(identities))
	for _, identity := range identities {
		out = append(out, ottercli.OpenClawMigrationImportAgentIdentity{
			ID:          strings.TrimSpace(identity.ID),
			Name:        strings.TrimSpace(identity.Name),
			Workspace:   strings.TrimSpace(identity.WorkspaceDir),
			Soul:        identity.Soul,
			Identity:    identity.Identity,
			Memory:      identity.Memory,
			Tools:       identity.Tools,
			SourceFiles: identity.SourceFiles,
		})
	}
	return out
}

func mapMigrateOpenClawEventsForAPI(
	events []importer.OpenClawSessionEvent,
) []ottercli.OpenClawMigrationImportHistoryEvent {
	out := make([]ottercli.OpenClawMigrationImportHistoryEvent, 0, len(events))
	for _, event := range events {
		out = append(out, ottercli.OpenClawMigrationImportHistoryEvent{
			AgentSlug:   strings.TrimSpace(event.AgentSlug),
			SessionID:   strings.TrimSpace(event.SessionID),
			SessionPath: strings.TrimSpace(event.SessionPath),
			EventID:     strings.TrimSpace(event.EventID),
			ParentID:    strings.TrimSpace(event.ParentID),
			Role:        strings.TrimSpace(event.Role),
			Body:        event.Body,
			CreatedAt:   event.CreatedAt.UTC(),
			Line:        event.Line,
		})
	}
	return out
}

func pollMigrateOpenClawAPIStatus(client *ottercli.Client) (*ottercli.OpenClawMigrationStatus, error) {
	attempts := migrateStatusPollAttempts
	if attempts < 1 {
		attempts = 1
	}
	interval := migrateStatusPollInterval
	if interval < 0 {
		interval = 0
	}

	var latest ottercli.OpenClawMigrationStatus
	for attempt := 0; attempt < attempts; attempt++ {
		status, err := statusMigrateOpenClawAPI(client)
		if err != nil {
			return nil, err
		}
		latest = status
		if !status.Active {
			return &latest, nil
		}
		if attempt+1 < attempts && interval > 0 {
			sleepMigrateStatusPoll(interval)
		}
	}
	return &latest, nil
}

func renderMigrateOpenClawAPISummary(out io.Writer, summary migrateOpenClawAPISummary) {
	fmt.Fprintln(out, "OpenClaw migration summary:")
	if summary.agentsImported {
		fmt.Fprintf(out, "  agent_import.processed=%d\n", summary.agentsProcessed)
		fmt.Fprintf(out, "  agent_import.inserted=%d\n", summary.agentsInserted)
		fmt.Fprintf(out, "  agent_import.updated=%d\n", summary.agentsUpdated)
		fmt.Fprintf(out, "  agent_import.failed_items=%d\n", summary.agentsSkipped)
	}
	if summary.historyImported {
		expected := summary.eventsProcessed + summary.eventsSkipped + summary.failedItems
		processed := summary.eventsProcessed
		inserted := summary.messagesInserted
		skipped := summary.eventsSkipped
		failed := summary.failedItems
		completenessRatio := 1.0
		isComplete := true
		if summary.report != nil {
			expected = summary.report.EventsExpected
			processed = summary.report.EventsProcessed
			inserted = summary.report.MessagesInserted
			skipped = summary.report.EventsSkippedUnknownAgent
			failed = summary.report.FailedItems
			completenessRatio = summary.report.CompletenessRatio
			isComplete = summary.report.IsComplete
		} else {
			accounted := inserted + skipped + failed
			if expected > 0 {
				completenessRatio = float64(accounted) / float64(expected)
			}
			isComplete = accounted == expected
		}

		fmt.Fprintf(out, "  history_backfill.events_received=%d\n", summary.eventsReceived)
		fmt.Fprintf(out, "  history_backfill.events_expected=%d\n", expected)
		fmt.Fprintf(out, "  history_backfill.events_processed=%d\n", processed)
		fmt.Fprintf(out, "  history_backfill.messages_inserted=%d\n", inserted)
		fmt.Fprintf(out, "  history_backfill.events_skipped=%d\n", skipped)
		fmt.Fprintf(out, "  history_backfill.failed_items=%d\n", failed)
		fmt.Fprintf(out, "  history_backfill.completeness_ratio=%.3f\n", completenessRatio)
		fmt.Fprintf(out, "  history_backfill.is_complete=%t\n", isComplete)
		if !isComplete {
			fmt.Fprintln(out, "  history_backfill.warning=incomplete_reconciliation")
		}
	}
	if summary.status != nil {
		for _, phase := range summary.status.Phases {
			phaseType := strings.TrimSpace(phase.MigrationType)
			if phaseType == "" {
				continue
			}
			fmt.Fprintf(out, "  %s.processed=%d\n", phaseType, phase.ProcessedItems)
			fmt.Fprintf(out, "  %s.failed_items=%d\n", phaseType, phase.FailedItems)
		}
	}
	if len(summary.warnings) == 0 {
		return
	}
	fmt.Fprintln(out, "  warnings:")
	for _, warning := range summary.warnings {
		trimmed := strings.TrimSpace(warning)
		if trimmed == "" {
			continue
		}
		fmt.Fprintf(out, "    - %s\n", trimmed)
	}
}

func renderMigrateOpenClawSummary(out io.Writer, summary importer.OpenClawMigrationSummaryReport) {
	fmt.Fprintln(out, "OpenClaw migration summary:")
	fmt.Fprintf(out, "  agent_import.processed=%d\n", summary.AgentImportProcessed)
	fmt.Fprintf(out, "  history_backfill.events_processed=%d\n", summary.HistoryEventsProcessed)
	fmt.Fprintf(out, "  history_backfill.events_skipped=%d\n", summary.HistoryEventsSkipped)
	fmt.Fprintf(out, "  history_backfill.messages_inserted=%d\n", summary.HistoryMessagesInserted)
	eventsExpected := summary.HistoryEventsProcessed + summary.HistoryEventsSkipped + summary.FailedItems
	if eventsExpected < 0 {
		eventsExpected = 0
	}
	accounted := summary.HistoryMessagesInserted + summary.HistoryEventsSkipped + summary.FailedItems
	completenessRatio := 1.0
	if eventsExpected > 0 {
		completenessRatio = float64(accounted) / float64(eventsExpected)
	}
	isComplete := accounted == eventsExpected
	fmt.Fprintf(out, "  history_backfill.events_expected=%d\n", eventsExpected)
	fmt.Fprintf(out, "  history_backfill.completeness_ratio=%.3f\n", completenessRatio)
	fmt.Fprintf(out, "  history_backfill.is_complete=%t\n", isComplete)
	if !isComplete {
		fmt.Fprintln(out, "  history_backfill.warning=incomplete_reconciliation")
	}
	if len(summary.HistorySkippedUnknownAgentCounts) > 0 {
		slugs := make([]string, 0, len(summary.HistorySkippedUnknownAgentCounts))
		for slug, count := range summary.HistorySkippedUnknownAgentCounts {
			if strings.TrimSpace(slug) == "" || count <= 0 {
				continue
			}
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			fmt.Fprintf(out, "  history_backfill.skipped_unknown_agent.%s=%d\n", slug, summary.HistorySkippedUnknownAgentCounts[slug])
		}
	}
	fmt.Fprintf(out, "  memory_extraction.processed=%d\n", summary.MemoryExtractionProcessed)
	fmt.Fprintf(out, "  entity_synthesis.processed=%d\n", summary.EntitySynthesisProcessed)
	fmt.Fprintf(out, "  memory_dedup.processed=%d\n", summary.MemoryDedupProcessed)
	fmt.Fprintf(out, "  taxonomy_classification.processed=%d\n", summary.TaxonomyClassificationProcessed)
	fmt.Fprintf(out, "  project_discovery.processed=%d\n", summary.ProjectDiscoveryProcessed)
	fmt.Fprintf(out, "  failed_items=%d\n", summary.FailedItems)
	if len(summary.Warnings) == 0 {
		return
	}
	fmt.Fprintln(out, "  warnings:")
	for _, warning := range summary.Warnings {
		fmt.Fprintf(out, "    - %s\n", strings.TrimSpace(warning))
	}
}

func renderMigrateHistoryProgress(out io.Writer, processed, total int, startedAt time.Time) {
	if out == nil || total <= 0 {
		return
	}
	if processed < 0 {
		processed = 0
	}
	if processed > total {
		processed = total
	}
	percent := (float64(processed) / float64(total)) * 100
	eta := "unknown"
	if processed >= total {
		eta = "0s"
	} else if processed > 0 {
		elapsed := time.Since(startedAt)
		remaining := total - processed
		etaDuration := time.Duration(float64(elapsed) * float64(remaining) / float64(processed))
		eta = formatMigrateETA(etaDuration)
	}

	fmt.Fprintf(out, "\rHistory progress: %d/%d (%.1f%%) ETA %s", processed, total, percent, eta)
	if processed >= total {
		fmt.Fprintln(out)
	}
}

func formatMigrateETA(duration time.Duration) string {
	if duration <= 0 {
		return "0s"
	}
	duration = duration.Round(time.Second)
	seconds := int(duration / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh%02dm%02ds", hours, minutes, seconds)
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
	return runMigrateStatusWithTransport(out, orgOverride, migrateTransportDB)
}

func runMigrateStatusWithTransport(out io.Writer, orgOverride string, requested migrateTransport) error {
	orgID, err := resolveMigrateOrgID(orgOverride)
	if err != nil {
		return err
	}
	transport, err := resolveMigrateTransportForExecution(requested)
	if err != nil {
		return err
	}
	if transport == migrateTransportAPI {
		return runMigrateStatusAPI(out, orgID)
	}
	return runMigrateStatusDB(out, orgID)
}

func runMigrateStatusDB(out io.Writer, orgID string) error {
	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrateQueryTimeout)
	defer cancel()

	progressRows, err := listMigrateProgressByOrg(ctx, db, orgID)
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
	return runMigratePauseWithTransport(out, orgOverride, migrateTransportDB)
}

func runMigratePauseWithTransport(out io.Writer, orgOverride string, requested migrateTransport) error {
	orgID, err := resolveMigrateOrgID(orgOverride)
	if err != nil {
		return err
	}
	transport, err := resolveMigrateTransportForExecution(requested)
	if err != nil {
		return err
	}
	if transport == migrateTransportAPI {
		return runMigratePauseAPI(out, orgID)
	}
	return runMigratePauseDB(out, orgID)
}

func runMigratePauseDB(out io.Writer, orgID string) error {
	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrateQueryTimeout)
	defer cancel()

	updated, err := updateMigrateProgressByOrgStatus(
		ctx,
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
	return runMigrateResumeWithTransport(out, orgOverride, migrateTransportDB)
}

func runMigrateResumeWithTransport(out io.Writer, orgOverride string, requested migrateTransport) error {
	orgID, err := resolveMigrateOrgID(orgOverride)
	if err != nil {
		return err
	}
	transport, err := resolveMigrateTransportForExecution(requested)
	if err != nil {
		return err
	}
	if transport == migrateTransportAPI {
		return runMigrateResumeAPI(out, orgID)
	}
	return runMigrateResumeDB(out, orgID)
}

func runMigrateResumeDB(out io.Writer, orgID string) error {
	db, err := openMigrateDatabase()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrateQueryTimeout)
	defer cancel()

	updated, err := updateMigrateProgressByOrgStatus(
		ctx,
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

func runMigrateStatusAPI(out io.Writer, orgID string) error {
	cfg, err := loadMigrateConfig()
	if err != nil {
		return err
	}
	client, err := newMigrateClient(cfg, orgID)
	if err != nil {
		return err
	}
	status, err := statusMigrateOpenClawAPI(client)
	if err != nil {
		return err
	}
	if len(status.Phases) == 0 {
		fmt.Fprintln(out, "No migration progress found.")
		return nil
	}
	fmt.Fprintln(out, "Migration Status:")
	for _, row := range status.Phases {
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

func runMigratePauseAPI(out io.Writer, orgID string) error {
	cfg, err := loadMigrateConfig()
	if err != nil {
		return err
	}
	client, err := newMigrateClient(cfg, orgID)
	if err != nil {
		return err
	}
	response, err := pauseMigrateOpenClawAPI(client)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Paused %d running migration phase(s).\n", response.UpdatedPhases)
	return nil
}

func runMigrateResumeAPI(out io.Writer, orgID string) error {
	cfg, err := loadMigrateConfig()
	if err != nil {
		return err
	}
	client, err := newMigrateClient(cfg, orgID)
	if err != nil {
		return err
	}
	response, err := resumeMigrateOpenClawAPI(client)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Resumed %d paused migration phase(s).\n", response.UpdatedPhases)
	return nil
}

func openMigrateDatabaseFromEnv() (*sql.DB, error) {
	databaseURL, err := resolveMigrateDatabaseURL()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), migrateQueryTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func resolveMigrateDatabaseURL() (string, error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL != "" {
		return databaseURL, nil
	}

	cfg, err := loadMigrateConfig()
	if err != nil {
		return "", err
	}
	databaseURL = strings.TrimSpace(cfg.DatabaseURL)
	if databaseURL != "" {
		return databaseURL, nil
	}

	return "", fmt.Errorf("DATABASE_URL is required for otter migrate from-openclaw")
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
