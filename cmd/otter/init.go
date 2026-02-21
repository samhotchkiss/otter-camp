package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

type initOptions struct {
	Mode    string
	Name    string
	Email   string
	OrgName string
	APIBase string
	Token   string
	URL     string
	Data    string
}

type initImportFlowOptions struct {
	ForceImport      bool
	ForceStartBridge bool
	ForceMigrate     bool
	ProgressPrefix   string
	MigrateTransport migrateTransport
	DataMode         string
}

type initBootstrapClient interface {
	OnboardingBootstrap(input ottercli.OnboardingBootstrapRequest) (ottercli.OnboardingBootstrapResponse, error)
	CreateAgent(input map[string]any) (map[string]any, error)
	CreateProject(input map[string]interface{}) (ottercli.Project, error)
	CreateIssue(projectID string, input map[string]interface{}) (ottercli.Issue, error)
}

var (
	loadInitConfig = ottercli.LoadConfig
	saveInitConfig = ottercli.SaveConfig
	newInitClient  = func(cfg ottercli.Config) (initBootstrapClient, error) {
		client, err := ottercli.NewClient(ottercli.Config{
			APIBaseURL: strings.TrimSpace(cfg.APIBaseURL),
			Token:      strings.TrimSpace(cfg.Token),
			DefaultOrg: strings.TrimSpace(cfg.DefaultOrg),
		}, strings.TrimSpace(cfg.DefaultOrg))
		if err != nil {
			return nil, err
		}
		return client, nil
	}
	validateHostedInitToken = func(apiBaseURL, token string) (string, error) {
		client, err := ottercli.NewClient(ottercli.Config{
			APIBaseURL: strings.TrimSpace(apiBaseURL),
			Token:      strings.TrimSpace(token),
		}, "")
		if err != nil {
			return "", err
		}
		resp, err := client.WhoAmI()
		if err != nil {
			return "", mapHostedInitTokenValidationError(err)
		}
		if !resp.Valid {
			return "", errors.New("invalid or expired token")
		}
		return initFirstNonEmpty(strings.TrimSpace(resp.OrgID), strings.TrimSpace(resp.OrgSlug)), nil
	}

	detectInitOpenClaw = func() (*importer.OpenClawInstallation, error) {
		return importer.DetectOpenClawInstallation(importer.DetectOpenClawOptions{})
	}
	parseInitOpenClawSessionEvents   = importer.ParseOpenClawSessionEvents
	ensureInitOpenClawRequiredAgents = importer.EnsureOpenClawRequiredAgents
	importInitOpenClawIdentities     = importer.ImportOpenClawAgentIdentities
	inferInitOpenClawProjects        = importer.InferOpenClawProjectCandidates
	restartInitOpenClawGateway       = restartOpenClawGateway
	resolveInitRepoRoot              = gitRepoRoot
	resolveInitBridgeScriptPath      = resolveBridgeScriptPath
	writeInitBridgeEnv               = writeBridgeEnvFile
	startInitBridge                  = startBridgeProcess
	resolveInitMigrateDatabaseURL    = resolveMigrateDatabaseURL
	runInitOpenClawMigration         = runMigrateFromOpenClaw
)

func mapHostedInitTokenValidationError(err error) error {
	if err == nil {
		return nil
	}
	if status, ok := ottercli.HTTPStatusCode(err); ok {
		switch {
		case status == 401 || status == 403:
			return errors.New("invalid or expired token")
		case status >= 500:
			return fmt.Errorf("API server returned an error (%d); the service may be down", status)
		default:
			return fmt.Errorf("API server returned an invalid response (%d); verify --url and try again", status)
		}
	}
	return fmt.Errorf("unable to validate token with API server: %w", err)
}

const (
	initLocalDefaultAPIBaseURL      = "http://localhost:4200"
	initDefaultAgentModel           = "gpt-5.2-codex"
	initDefaultBridgeHost           = "127.0.0.1"
	initDefaultBridgePort           = 18791
	initDefaultBackfillWindowSize   = 30
	initDefaultBackfillWindowStride = 30
)

func handleInit(args []string) {
	if err := runInitCommand(args, os.Stdin, os.Stdout); err != nil {
		die(err.Error())
	}
}

func runInitCommand(args []string, in io.Reader, out io.Writer) error {
	opts, err := parseInitOptions(args)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(in)
	mode := opts.Mode
	if mode == "" {
		mode, err = promptInitMode(reader, out)
		if err != nil {
			return err
		}
	}

	switch mode {
	case "local":
		return runLocalInit(opts, reader, out)
	case "hosted":
		if strings.TrimSpace(opts.Token) != "" || strings.TrimSpace(opts.URL) != "" {
			return runHostedInit(opts, reader, out)
		}
		fmt.Fprintln(out, "Visit otter.camp/setup to get started.")
		return nil
	default:
		return errors.New("--mode must be local or hosted")
	}
}

func parseInitOptions(args []string) (initOptions, error) {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := flags.String("mode", "", "onboarding mode (local|hosted)")
	name := flags.String("name", "", "display name for local bootstrap")
	email := flags.String("email", "", "email for local bootstrap")
	orgName := flags.String("org-name", "", "organization name for local bootstrap")
	apiBase := flags.String("api", "", "API base URL override")
	token := flags.String("token", "", "hosted session token")
	hostedURL := flags.String("url", "", "hosted workspace URL")
	dataMode := flags.String("data", "", "data mode (fresh|import)")
	if err := flags.Parse(args); err != nil {
		return initOptions{}, err
	}
	if len(flags.Args()) != 0 {
		return initOptions{}, errors.New("usage: otter init [--mode <local|hosted>]")
	}

	normalizedMode := strings.ToLower(strings.TrimSpace(*mode))
	if normalizedMode != "" && normalizedMode != "local" && normalizedMode != "hosted" {
		return initOptions{}, errors.New("--mode must be local or hosted")
	}

	normalizedData := strings.ToLower(strings.TrimSpace(*dataMode))
	if normalizedData != "" && normalizedData != "fresh" && normalizedData != "import" {
		return initOptions{}, errors.New("--data must be fresh or import")
	}

	return initOptions{
		Mode:    normalizedMode,
		Name:    strings.TrimSpace(*name),
		Email:   strings.TrimSpace(*email),
		OrgName: strings.TrimSpace(*orgName),
		APIBase: strings.TrimSpace(*apiBase),
		Token:   strings.TrimSpace(*token),
		URL:     strings.TrimSpace(*hostedURL),
		Data:    normalizedData,
	}, nil
}

func runHostedInit(opts initOptions, reader *bufio.Reader, out io.Writer) error {
	token := strings.TrimSpace(opts.Token)
	hostedURL := strings.TrimSpace(opts.URL)
	if token == "" || hostedURL == "" {
		return errors.New("--mode hosted requires both --token and --url")
	}

	apiBaseURL, err := deriveHostedAPIBaseURL(hostedURL)
	if err != nil {
		return err
	}

	defaultOrg, err := validateHostedInitToken(apiBaseURL, token)
	if err != nil {
		return err
	}

	cfg, err := loadInitConfig()
	if err != nil {
		return err
	}
	cfg.APIBaseURL = apiBaseURL
	cfg.Token = token
	if defaultOrg != "" {
		cfg.DefaultOrg = defaultOrg
	}
	if err := saveInitConfig(cfg); err != nil {
		return err
	}

	fmt.Fprintln(out, "Hosted setup configured.")
	fmt.Fprintln(out, "Next step: otter whoami")

	client, err := newInitClient(cfg)
	if err != nil {
		return err
	}
	if err := runInitImportAndBridgeWithOptions(reader, out, client, cfg, initImportFlowOptions{
		ForceStartBridge: true,
		ProgressPrefix:   "Hosted",
		MigrateTransport: migrateTransportAPI,
		DataMode:         opts.Data,
	}); err != nil {
		return err
	}
	fmt.Fprintln(out, "Hosted setup complete.")
	return nil
}

func deriveHostedAPIBaseURL(rawURL string) (string, error) {
	value := strings.TrimSpace(rawURL)
	if value == "" {
		return "", errors.New("--url is required")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("--url must be a valid absolute URL")
	}

	// Do NOT append /api — ottercli.Client already prefixes /api/ on all request paths.
	// BaseURL should be the bare origin (e.g., https://swh.otter.camp).
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return strings.TrimRight(parsed.String(), "/"), nil
}

func promptInitMode(reader *bufio.Reader, out io.Writer) (string, error) {
	if reader == nil {
		return "", errors.New("input reader is required")
	}

	for {
		fmt.Fprintln(out, "Welcome to Otter Camp!")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "How are you setting up?")
		fmt.Fprintln(out, "  [1] Local install (run everything on this machine)")
		fmt.Fprintln(out, "  [2] Hosted (connect to otter.camp)")
		fmt.Fprint(out, "> ")

		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}

		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "", "1", "local":
			return "local", nil
		case "2", "hosted":
			return "hosted", nil
		}

		if errors.Is(err, io.EOF) {
			return "", errors.New("init mode selection required")
		}
		fmt.Fprintln(out, "Please enter 1 for local or 2 for hosted.")
	}
}

func runLocalInit(opts initOptions, reader *bufio.Reader, out io.Writer) error {
	if reader == nil {
		return errors.New("input reader is required")
	}

	cfg, err := loadInitConfig()
	if err != nil {
		return err
	}

	req, err := collectLocalInitBootstrapInput(opts, reader, out)
	if err != nil {
		return err
	}

	apiBase := resolveInitAPIBaseURL(cfg.APIBaseURL, opts.APIBase)
	client, err := newInitClient(ottercli.Config{APIBaseURL: apiBase})
	if err != nil {
		return err
	}

	resp, err := client.OnboardingBootstrap(req)
	if err != nil {
		return err
	}

	cfg.APIBaseURL = apiBase
	cfg.Token = strings.TrimSpace(resp.Token)
	cfg.DefaultOrg = strings.TrimSpace(resp.OrgID)
	if err := saveInitConfig(cfg); err != nil {
		return err
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Account setup complete.")
	fmt.Fprintf(out, "Your auth token (saved to CLI config): %s\n", cfg.Token)
	fmt.Fprintf(out, "Organization: %s\n", initFirstNonEmpty(req.OrganizationName, resp.OrgSlug, resp.OrgID))
	if agentNames := initOnboardingAgentNames(resp.Agents); len(agentNames) > 0 {
		fmt.Fprintf(out, "Created agents: %s\n", strings.Join(agentNames, ", "))
	}
	fmt.Fprintf(out, "Dashboard: %s\n", initLocalDefaultAPIBaseURL)
	fmt.Fprintln(out, "Next step: otter whoami")

	if err := runInitImportAndBridge(reader, out, client, cfg); err != nil {
		return err
	}
	return nil
}

func collectLocalInitBootstrapInput(opts initOptions, reader *bufio.Reader, out io.Writer) (ottercli.OnboardingBootstrapRequest, error) {
	name := strings.TrimSpace(opts.Name)
	email := strings.TrimSpace(opts.Email)
	orgName := strings.TrimSpace(opts.OrgName)

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Let's set up your account.")
	fmt.Fprintln(out, "")

	if name == "" {
		name = promptRequiredField(reader, out, "Your name: ")
	}
	if email == "" {
		email = promptRequiredField(reader, out, "Email: ")
	}
	if orgName == "" {
		orgName = promptRequiredField(reader, out, "Organization name: ")
	}

	if name == "" {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("name is required")
	}
	if email == "" {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("email is required")
	}
	if !strings.Contains(email, "@") {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("invalid email")
	}
	if orgName == "" {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("organization name is required")
	}

	return ottercli.OnboardingBootstrapRequest{
		Name:             name,
		Email:            email,
		OrganizationName: orgName,
	}, nil
}

func runInitImportAndBridge(reader *bufio.Reader, out io.Writer, client initBootstrapClient, cfg ottercli.Config) error {
	return runInitImportAndBridgeWithOptions(reader, out, client, cfg, initImportFlowOptions{})
}

func runInitImportAndBridgeWithOptions(
	reader *bufio.Reader,
	out io.Writer,
	client initBootstrapClient,
	cfg ottercli.Config,
	opts initImportFlowOptions,
) error {
	phase := func(msg string) {
		if strings.TrimSpace(opts.ProgressPrefix) == "" {
			return
		}
		fmt.Fprintf(out, "%s phase: %s\n", strings.TrimSpace(opts.ProgressPrefix), msg)
	}

	phase("Detect OpenClaw")
	installation, err := detectInitOpenClaw()
	if err != nil {
		if errors.Is(err, importer.ErrOpenClawNotFound) {
			fmt.Fprintln(out, "OpenClaw installation not detected. Skipping import and bridge setup.")
			return nil
		}
		fmt.Fprintf(out, "OpenClaw detection failed: %v\n", err)
		return nil
	}

	fmt.Fprintf(out, "Found OpenClaw at %s with %d agent workspaces.\n", installation.RootDir, len(installation.Agents))
	phase("Ensure required agents")
	ensureResult, ensureErr := ensureInitOpenClawRequiredAgents(installation, importer.EnsureOpenClawRequiredAgentsOptions{
		IncludeChameleon: true,
	})
	switch {
	case ensureErr != nil:
		fmt.Fprintf(out, "WARNING: OpenClaw config update failed: %v\n", ensureErr)
	default:
		if len(ensureResult.DroppedUnknownKeys) > 0 {
			fmt.Fprintf(
				out,
				"WARNING: OpenClaw config skipped unsupported agent keys: %s\n",
				strings.Join(ensureResult.DroppedUnknownKeys, ", "),
			)
		}

		if ensureResult.Updated {
			if ensureResult.AddedElephant {
				fmt.Fprintln(out, "Added Ellie (Elephant) to OpenClaw config.")
			}
			if ensureResult.AddedChameleon {
				fmt.Fprintln(out, "Added Chameleon to OpenClaw config.")
			}
			if err := restartInitOpenClawGateway(out); err != nil {
				fmt.Fprintf(out, "WARNING: OpenClaw restart failed: %v\n", err)
			} else {
				fmt.Fprintln(out, "OpenClaw restarted to activate new agents.")
			}
		} else {
			fmt.Fprintln(out, "Required OpenClaw agents already present. No config changes made.")
		}
	}

	dataMode := strings.ToLower(strings.TrimSpace(opts.DataMode))
	if dataMode == "" {
		dataMode = promptInitDataMode(reader, out)
	}
	switch dataMode {
	case "fresh":
		fmt.Fprintln(out, "Data mode: start fresh (skipping OpenClaw history import).")
	case "import":
		fmt.Fprintln(out, "Data mode: import OpenClaw history (this may take hours).")
	default:
		return fmt.Errorf("invalid data mode %q (expected fresh|import)", strings.TrimSpace(dataMode))
	}

	if dataMode == "import" {
		phase("Import agents/projects from OpenClaw")
		agentsImported, projectsImported, issuesImported := importOpenClawData(out, client, installation)
		fmt.Fprintf(out, "Imported %d agents, %d projects, %d issues from OpenClaw.\n", agentsImported, projectsImported, issuesImported)
	}
	if err := ensureInitGatewayPortConfigured(installation); err != nil {
		return err
	}

	bridgeRoot := strings.TrimSpace(os.Getenv("OTTERCAMP_REPO"))
	if bridgeRoot == "" {
		resolved, err := resolveInitRepoRoot()
		if err == nil && strings.TrimSpace(resolved) != "" {
			bridgeRoot = resolved
		}
	}
	if bridgeRoot == "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidate := filepath.Join(home, "Documents", "Dev", "otter-camp")
			if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
				bridgeRoot = candidate
			}
		}
	}
	if bridgeRoot == "" {
		bridgeRoot, _ = os.Getwd()
	}
	bridgeScriptPath, err := resolveInitBridgeScriptPath(bridgeRoot)
	if err != nil {
		return err
	}

	phase("Write bridge config")
	bridgeValues := buildBridgeEnvValues(installation, cfg)
	bridgeValues["BRIDGE_SCRIPT"] = bridgeScriptPath
	bridgePath, err := writeInitBridgeEnv(bridgeRoot, bridgeValues)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Bridge config written: %s\n", bridgePath)

	shouldStartBridge := opts.ForceStartBridge
	if !opts.ForceStartBridge {
		shouldStartBridge = promptYesNo(reader, out, "Start the bridge now? (y/N): ", false)
	}
	if shouldStartBridge {
		phase("Start bridge")
		if err := startInitBridge(bridgeRoot, out); err != nil {
			fmt.Fprintf(out, "Unable to start bridge automatically: %v\n", err)
		}
	}

	transportHint := opts.MigrateTransport
	if transportHint == "" {
		transportHint = migrateTransportDB
	}

	databaseURL, dbErr := resolveInitMigrateDatabaseURL()
	shouldMigrate := dataMode == "import" || opts.ForceMigrate
	if shouldMigrate && dataMode == "import" {
		renderInitImportEstimate(out, installation)
		if reader != nil && !opts.ForceMigrate {
			if !promptYesNo(reader, out, "Proceed with OpenClaw history import? (Y/n): ", true) {
				fmt.Fprintln(out, "OpenClaw history import skipped.")
				return nil
			}
		}
	}

	if shouldMigrate {
		if dbErr != nil {
			fmt.Fprintf(out, "Skipping automatic migration: %v\n", dbErr)
			fmt.Fprintf(out, "Run this command later to migrate OpenClaw data:\n  %s\n", renderInitMigrationCommand("", cfg.DefaultOrg, installation.RootDir, transportHint))
			return nil
		}
		fmt.Fprintln(out, "Migration progress: preparing OpenClaw migration input.")
		fmt.Fprintln(out, "Migration progress: running otter migrate from-openclaw.")
		if err := runInitOpenClawMigration(out, migrateFromOpenClawOptions{
			OpenClawDir: installation.RootDir,
			OrgID:       strings.TrimSpace(cfg.DefaultOrg),
			Transport:   transportHint,
		}); err != nil {
			fmt.Fprintf(out, "Migration progress: failed (%v)\n", err)
			fmt.Fprintf(out, "Retry with:\n  %s\n", renderInitMigrationCommand(databaseURL, cfg.DefaultOrg, installation.RootDir, transportHint))
			return nil
		}
		fmt.Fprintln(out, "Migration progress: complete.")
		return nil
	}

	fmt.Fprintf(out, "OpenClaw migration skipped. Run later with:\n  %s\n", renderInitMigrationCommand(databaseURL, cfg.DefaultOrg, installation.RootDir, transportHint))
	return nil
}

func promptInitDataMode(reader *bufio.Reader, out io.Writer) string {
	if reader == nil {
		// Preserve prior behavior for non-interactive runs.
		return "import"
	}
	for {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "How should Otter Camp initialize your data?")
		fmt.Fprintln(out, "  [1] Start fresh (skip importing past OpenClaw activity)")
		fmt.Fprintln(out, "  [2] Import everything (import all OpenClaw history and bootstrap memories)")
		fmt.Fprint(out, "> ")

		line, err := reader.ReadString('\n')
		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "", "2", "import":
			return "import"
		case "1", "fresh":
			return "fresh"
		}
		if errors.Is(err, io.EOF) {
			return "import"
		}
		fmt.Fprintln(out, "Please enter 1 for fresh or 2 for import.")
	}
}

type initImportEstimate struct {
	Agents                int
	Events                int
	TotalChars            int
	ApproxTokens          int
	EstimatedExtractCalls int
	WindowSize            int
	WindowStride          int
}

func renderInitImportEstimate(out io.Writer, installation *importer.OpenClawInstallation) {
	if out == nil || installation == nil {
		return
	}

	events, err := parseInitOpenClawSessionEvents(installation)
	if err != nil {
		fmt.Fprintf(out, "Estimate: unable to parse OpenClaw sessions (%v)\n", err)
		return
	}

	windowSize := initDefaultBackfillWindowSize
	if raw := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_BACKFILL_WINDOW_SIZE")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			windowSize = parsed
		}
	}
	windowStride := initDefaultBackfillWindowStride
	if raw := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_BACKFILL_WINDOW_STRIDE")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			windowStride = parsed
		}
	}
	if windowStride <= 0 {
		windowStride = windowSize
	}

	estimate := buildInitImportEstimate(installation, events, windowSize, windowStride)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Import estimate (rough):")
	fmt.Fprintf(out, "  agents=%d\n", estimate.Agents)
	fmt.Fprintf(out, "  events=%d\n", estimate.Events)
	fmt.Fprintf(out, "  total_chars=%d\n", estimate.TotalChars)
	fmt.Fprintf(out, "  approx_tokens=%d\n", estimate.ApproxTokens)
	fmt.Fprintf(out, "  ellie_extract_calls~=%d (window_size=%d stride=%d)\n", estimate.EstimatedExtractCalls, estimate.WindowSize, estimate.WindowStride)
	fmt.Fprintln(out, "Notes:")
	fmt.Fprintln(out, "  - Embeddings will run on all imported chat messages (and later on extracted memories).")
	fmt.Fprintln(out, "  - Ellie extraction/dedup/taxonomy costs depend on your OpenClaw model and rate limits.")
	fmt.Fprintln(out, "  - Large histories can take hours to fully backfill.")
}

func buildInitImportEstimate(
	installation *importer.OpenClawInstallation,
	events []importer.OpenClawSessionEvent,
	windowSize int,
	windowStride int,
) initImportEstimate {
	agents := 0
	if installation != nil {
		agents = len(installation.Agents)
	}
	totalChars := 0
	byAgent := make(map[string]int, 16)
	for _, event := range events {
		body := event.Body
		totalChars += len(body)
		slug := strings.TrimSpace(event.AgentSlug)
		if slug == "" {
			slug = "unknown"
		}
		byAgent[slug]++
	}

	extractCalls := 0
	if windowSize > 0 && windowStride > 0 {
		for _, count := range byAgent {
			extractCalls += estimateSlidingWindows(count, windowSize, windowStride)
		}
	}

	approxTokens := totalChars / 4
	if approxTokens < 0 {
		approxTokens = 0
	}

	return initImportEstimate{
		Agents:                agents,
		Events:                len(events),
		TotalChars:            totalChars,
		ApproxTokens:          approxTokens,
		EstimatedExtractCalls: extractCalls,
		WindowSize:            windowSize,
		WindowStride:          windowStride,
	}
}

func estimateSlidingWindows(count, windowSize, windowStride int) int {
	if count <= 0 {
		return 0
	}
	if windowSize <= 0 || windowStride <= 0 {
		return 0
	}
	if count <= windowSize {
		return 1
	}
	remaining := count - windowSize
	return 1 + int(math.Ceil(float64(remaining)/float64(windowStride)))
}

func importOpenClawData(out io.Writer, client initBootstrapClient, installation *importer.OpenClawInstallation) (int, int, int) {
	identities, err := importInitOpenClawIdentities(installation)
	if err != nil {
		fmt.Fprintf(out, "OpenClaw identity import failed: %v\n", err)
		return 0, 0, 0
	}

	agentsImported := 0
	identitiesByID := make(map[string]importer.ImportedAgentIdentity, len(identities))
	for _, identity := range identities {
		identitiesByID[identity.ID] = identity
		payload := map[string]any{
			"slot":         normalizeInitAgentSlot(initFirstNonEmpty(identity.ID, identity.Name)),
			"display_name": initFirstNonEmpty(identity.Name, identity.ID),
			"model":        initDefaultAgentModel,
		}
		if role := extractInitRole(identity.Soul); role != "" {
			payload["role"] = role
		}
		if identity.Soul != "" {
			payload["soul"] = identity.Soul
		}
		if identity.Identity != "" {
			payload["identity"] = identity.Identity
		}
		if _, err := client.CreateAgent(payload); err == nil {
			agentsImported++
		}
	}

	projectSignals := make([]importer.OpenClawWorkspaceSignal, 0, len(installation.Agents))
	for _, agent := range installation.Agents {
		repoPath := detectWorkspaceRepoRoot(agent.WorkspaceDir)
		signal := importer.OpenClawWorkspaceSignal{
			AgentID:      agent.ID,
			WorkspaceDir: agent.WorkspaceDir,
			RepoPath:     repoPath,
		}
		if identity, ok := identitiesByID[agent.ID]; ok {
			signal.IssueHints = extractInitIssueHints(identity.Memory)
		}
		projectSignals = append(projectSignals, signal)
	}
	candidates := inferInitOpenClawProjects(importer.OpenClawProjectImportInput{
		Workspaces: projectSignals,
	})

	projectsImported := 0
	issuesImported := 0
	for _, candidate := range candidates {
		description := importer.BuildOpenClawProjectDescription(candidate)
		status := strings.TrimSpace(strings.ToLower(candidate.Status))
		if status == "" {
			status = "active"
		}
		project, err := client.CreateProject(map[string]interface{}{
			"name":        candidate.Name,
			"description": description,
			"status":      status,
		})
		if err != nil {
			continue
		}
		projectsImported++

		if len(candidate.Issues) == 0 {
			if _, err := client.CreateIssue(project.ID, map[string]interface{}{
				"title": "Review imported OpenClaw context",
				"body":  "Confirm imported context and create follow-up issues.",
			}); err == nil {
				issuesImported++
			}
			continue
		}

		for _, issue := range candidate.Issues {
			payload := map[string]interface{}{
				"title": issue.Title,
			}
			state, workStatus := inferInitImportedIssueLifecycle(issue)
			if strings.TrimSpace(state) != "" {
				payload["state"] = state
			}
			if strings.TrimSpace(workStatus) != "" {
				payload["work_status"] = workStatus
			}
			if _, err := client.CreateIssue(project.ID, payload); err == nil {
				issuesImported++
			}
		}
	}

	return agentsImported, projectsImported, issuesImported
}

func inferInitImportedIssueLifecycle(issue importer.OpenClawIssueCandidate) (state string, workStatus string) {
	workStatus = strings.TrimSpace(strings.ToLower(issue.Status))
	if workStatus == "" {
		// Fall back to conservative defaults if discovery did not infer a work status.
		return "open", "queued"
	}
	switch workStatus {
	case "done", "cancelled":
		return "closed", workStatus
	case "queued", "planning", "ready", "ready_for_work", "in_progress", "blocked", "review", "flagged":
		return "open", workStatus
	default:
		return "open", "queued"
	}
}

func ensureInitGatewayPortConfigured(installation *importer.OpenClawInstallation) error {
	if installation == nil || installation.Gateway.Port > 0 {
		return nil
	}
	configPath := strings.TrimSpace(installation.ConfigPath)
	if configPath == "" {
		configPath = "~/.openclaw/openclaw.json"
	}
	return fmt.Errorf("unable to determine OpenClaw gateway port from %s; set gateway.port (or port) in config", configPath)
}

func buildBridgeEnvValues(installation *importer.OpenClawInstallation, cfg ottercli.Config) map[string]string {
	host := initDefaultBridgeHost
	port := initDefaultBridgePort
	token := "your-openclaw-gateway-token"
	if installation != nil {
		host = initFirstNonEmpty(installation.Gateway.Host, host)
		if installation.Gateway.Port > 0 {
			port = installation.Gateway.Port
		}
		token = initFirstNonEmpty(installation.Gateway.Token, token)
	}

	ottercampURL := deriveBridgeOttercampURL(cfg.APIBaseURL)

	return map[string]string{
		"OPENCLAW_HOST":      host,
		"OPENCLAW_PORT":      fmt.Sprintf("%d", port),
		"OPENCLAW_TOKEN":     token,
		"OTTERCAMP_URL":      initFirstNonEmpty(ottercampURL, initLocalDefaultAPIBaseURL),
		"OTTERCAMP_TOKEN":    strings.TrimSpace(cfg.Token),
		"OPENCLAW_WS_SECRET": strings.TrimSpace(os.Getenv("OPENCLAW_WS_SECRET")),
	}
}

func deriveBridgeOttercampURL(apiBaseURL string) string {
	value := strings.TrimSpace(apiBaseURL)
	if value == "" {
		return ""
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(value, "/")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "/api" {
		parsed.Path = ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return strings.TrimRight(parsed.String(), "/")
}

func writeBridgeEnvFile(repoRoot string, values map[string]string) (string, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		repoRoot = "."
	}

	path := filepath.Join(repoRoot, "bridge", ".env")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	content := strings.Join([]string{
		"OPENCLAW_HOST=" + strings.TrimSpace(values["OPENCLAW_HOST"]),
		"OPENCLAW_PORT=" + strings.TrimSpace(values["OPENCLAW_PORT"]),
		"OPENCLAW_TOKEN=" + strings.TrimSpace(values["OPENCLAW_TOKEN"]),
		"OTTERCAMP_URL=" + strings.TrimSpace(values["OTTERCAMP_URL"]),
		"OTTERCAMP_TOKEN=" + strings.TrimSpace(values["OTTERCAMP_TOKEN"]),
		"OPENCLAW_WS_SECRET=" + strings.TrimSpace(values["OPENCLAW_WS_SECRET"]),
		"BRIDGE_SCRIPT=" + strings.TrimSpace(values["BRIDGE_SCRIPT"]),
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func restartOpenClawGateway(out io.Writer) error {
	cmd := exec.Command("openclaw", "gateway", "restart")
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

func startBridgeProcess(repoRoot string, out io.Writer) error {
	if _, err := resolveBridgeScriptPath(repoRoot); err != nil {
		return err
	}

	cmd := exec.Command("bash", "-lc", buildInitBridgeStartCommand(repoRoot))
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return err
	}
	if out != nil {
		fmt.Fprintf(out, "Bridge started in background (pid %d).\n", cmd.Process.Pid)
	}
	return nil
}

func resolveBridgeScriptPath(repoRoot string) (string, error) {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}

	candidate := filepath.Join(root, "bridge", "openclaw-bridge.ts")
	info, err := os.Stat(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("bridge script not found at %s", candidate)
		}
		return "", fmt.Errorf("resolve bridge script: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("bridge script is not a file: %s", candidate)
	}

	absPath, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve bridge script: %w", err)
	}
	return absPath, nil
}

func buildInitBridgeStartCommand(repoRoot string) string {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}
	return fmt.Sprintf(
		"cd %s && set -a && . bridge/.env && set +a && npx tsx \"${BRIDGE_SCRIPT:-bridge/openclaw-bridge.ts}\" --continuous",
		shellSingleQuote(root),
	)
}

func promptRequiredField(reader *bufio.Reader, out io.Writer, label string) string {
	for {
		fmt.Fprint(out, label)
		line, err := reader.ReadString('\n')
		value := strings.TrimSpace(line)
		if value != "" {
			return value
		}
		if errors.Is(err, io.EOF) {
			return ""
		}
		fmt.Fprintln(out, "This field is required.")
	}
}

func promptYesNo(reader *bufio.Reader, out io.Writer, label string, defaultYes bool) bool {
	if reader == nil {
		return defaultYes
	}

	for {
		fmt.Fprint(out, label)
		line, err := reader.ReadString('\n')
		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "":
			return defaultYes
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}

		if errors.Is(err, io.EOF) {
			return defaultYes
		}
		fmt.Fprintln(out, "Please answer y or n.")
	}
}

func resolveInitAPIBaseURL(existing, override string) string {
	override = strings.TrimSpace(override)
	if override != "" {
		return strings.TrimRight(override, "/")
	}

	existing = strings.TrimSpace(existing)
	if existing == "" || strings.EqualFold(existing, "https://api.otter.camp") {
		return initLocalDefaultAPIBaseURL
	}
	return strings.TrimRight(existing, "/")
}

func detectWorkspaceRepoRoot(startDir string) string {
	current := strings.TrimSpace(startDir)
	if current == "" {
		return ""
	}
	current = filepath.Clean(current)
	for {
		gitPath := filepath.Join(current, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func extractInitRole(soul string) string {
	for _, line := range strings.Split(soul, "\n") {
		candidate := strings.TrimSpace(strings.TrimLeft(line, "#*- "))
		if candidate == "" {
			continue
		}
		if len(candidate) > 80 {
			candidate = candidate[:80]
		}
		return candidate
	}
	return ""
}

func extractInitIssueHints(memory string) []string {
	lines := strings.Split(memory, "\n")
	hints := make([]string, 0, 3)
	seen := map[string]struct{}{}
	for _, line := range lines {
		candidate := strings.TrimSpace(strings.TrimLeft(line, "-*# "))
		if len(candidate) < 8 {
			continue
		}
		key := strings.ToLower(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		hints = append(hints, candidate)
		if len(hints) >= 3 {
			break
		}
	}
	return hints
}

func initFirstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func initOnboardingAgentNames(agents []ottercli.OnboardingAgent) []string {
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		name := initFirstNonEmpty(agent.DisplayName, agent.Slug)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func normalizeInitAgentSlot(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "agent"
	}
	replacer := strings.NewReplacer(" ", "-", "_", "-", ".", "-")
	value = replacer.Replace(value)
	var b strings.Builder
	lastDash := false
	for _, ch := range value {
		isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if isAlnum {
			b.WriteRune(ch)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	normalized := strings.Trim(b.String(), "-")
	if len(normalized) < 2 {
		normalized = normalized + "x"
	}
	if len(normalized) > 63 {
		normalized = normalized[:63]
	}
	return normalized
}

func renderInitMigrationCommand(databaseURL, orgID, openClawDir string, transport migrateTransport) string {
	databaseURL = strings.TrimSpace(databaseURL)
	command := "otter migrate from-openclaw"
	switch transport {
	case migrateTransportAPI:
		command += " --transport api"
	default:
		if databaseURL == "" {
			databaseURL = "<database-url>"
		}
		command = fmt.Sprintf("DATABASE_URL=%q %s", databaseURL, command)
	}
	if trimmedOrg := strings.TrimSpace(orgID); trimmedOrg != "" {
		command += fmt.Sprintf(" --org %q", trimmedOrg)
	}
	if trimmedOpenClawDir := strings.TrimSpace(openClawDir); trimmedOpenClawDir != "" {
		command += fmt.Sprintf(" --openclaw-dir %q", trimmedOpenClawDir)
	}
	return command
}
