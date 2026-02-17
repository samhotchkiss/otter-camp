package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	newInitClient  = func(apiBase string) (initBootstrapClient, error) {
		client, err := ottercli.NewClient(ottercli.Config{APIBaseURL: strings.TrimSpace(apiBase)}, "")
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
			return "", fmt.Errorf("invalid or expired token: %w", err)
		}
		if !resp.Valid {
			return "", errors.New("invalid or expired token")
		}
		return initFirstNonEmpty(strings.TrimSpace(resp.OrgID), strings.TrimSpace(resp.OrgSlug)), nil
	}

	detectInitOpenClaw = func() (*importer.OpenClawInstallation, error) {
		return importer.DetectOpenClawInstallation(importer.DetectOpenClawOptions{})
	}
	ensureInitOpenClawRequiredAgents = importer.EnsureOpenClawRequiredAgents
	importInitOpenClawIdentities     = importer.ImportOpenClawAgentIdentities
	inferInitOpenClawProjects        = importer.InferOpenClawProjectCandidates
	resolveInitRepoRoot              = gitRepoRoot
	writeInitBridgeEnv               = writeBridgeEnvFile
	startInitBridge                  = startBridgeProcess
)

const (
	initLocalDefaultAPIBaseURL = "http://localhost:4200"
	initDefaultAgentModel      = "gpt-5.2-codex"
	initDefaultBridgeHost      = "127.0.0.1"
	initDefaultBridgePort      = 18791
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
			return runHostedInit(opts, out)
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

	return initOptions{
		Mode:    normalizedMode,
		Name:    strings.TrimSpace(*name),
		Email:   strings.TrimSpace(*email),
		OrgName: strings.TrimSpace(*orgName),
		APIBase: strings.TrimSpace(*apiBase),
		Token:   strings.TrimSpace(*token),
		URL:     strings.TrimSpace(*hostedURL),
	}, nil
}

func runHostedInit(opts initOptions, out io.Writer) error {
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

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/api"
	} else if parsed.Path != "/api" && !strings.HasPrefix(parsed.Path, "/api/") {
		parsed.Path += "/api"
	}
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
	client, err := newInitClient(apiBase)
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
	ensureResult, ensureErr := ensureInitOpenClawRequiredAgents(installation, importer.EnsureOpenClawRequiredAgentsOptions{
		IncludeChameleon: true,
	})
	switch {
	case ensureErr != nil:
		fmt.Fprintf(out, "WARNING: OpenClaw config update failed: %v\n", ensureErr)
	case ensureResult.Updated:
		if ensureResult.AddedElephant {
			fmt.Fprintln(out, "Added Ellie (Elephant) to OpenClaw config. Restart OpenClaw when ready to activate.")
		}
		if ensureResult.AddedChameleon {
			fmt.Fprintln(out, "Added Chameleon to OpenClaw config. Restart OpenClaw when ready to activate.")
		}
	default:
		fmt.Fprintln(out, "Required OpenClaw agents already present. No config changes made.")
	}

	if promptYesNo(reader, out, "Import agents and projects from OpenClaw? (Y/n): ", true) {
		agentsImported, projectsImported, issuesImported := importOpenClawData(out, client, installation)
		fmt.Fprintf(out, "Imported %d agents, %d projects, %d issues from OpenClaw.\n", agentsImported, projectsImported, issuesImported)
	}

	repoRoot, err := resolveInitRepoRoot()
	if err != nil || strings.TrimSpace(repoRoot) == "" {
		repoRoot, _ = os.Getwd()
	}

	bridgePath, err := writeInitBridgeEnv(repoRoot, buildBridgeEnvValues(installation, cfg))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Bridge config written: %s\n", bridgePath)

	if promptYesNo(reader, out, "Start the bridge now? (y/N): ", false) {
		if err := startInitBridge(repoRoot, out); err != nil {
			fmt.Fprintf(out, "Unable to start bridge automatically: %v\n", err)
		}
	}

	return nil
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
		project, err := client.CreateProject(map[string]interface{}{
			"name":        candidate.Name,
			"description": "Imported from OpenClaw",
			"status":      "active",
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
			if _, err := client.CreateIssue(project.ID, map[string]interface{}{
				"title": issue.Title,
			}); err == nil {
				issuesImported++
			}
		}
	}

	return agentsImported, projectsImported, issuesImported
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

	return map[string]string{
		"OPENCLAW_HOST":      host,
		"OPENCLAW_PORT":      fmt.Sprintf("%d", port),
		"OPENCLAW_TOKEN":     token,
		"OTTERCAMP_URL":      initFirstNonEmpty(strings.TrimSpace(cfg.APIBaseURL), initLocalDefaultAPIBaseURL),
		"OTTERCAMP_TOKEN":    strings.TrimSpace(cfg.Token),
		"OPENCLAW_WS_SECRET": strings.TrimSpace(os.Getenv("OPENCLAW_WS_SECRET")),
	}
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
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func startBridgeProcess(repoRoot string, out io.Writer) error {
	cmd := exec.Command("npx", "tsx", "bridge/openclaw-bridge.ts", "--continuous")
	cmd.Dir = repoRoot
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
