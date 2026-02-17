package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

type fakeInitClient struct {
	gotBootstrapRequest ottercli.OnboardingBootstrapRequest
	bootstrapResponse   ottercli.OnboardingBootstrapResponse
	bootstrapErr        error

	createAgentInputs   []map[string]any
	createAgentErr      error
	createProjectInputs []map[string]interface{}
	createProjectErr    error
	createIssueCalls    []fakeIssueCreateCall
	createIssueErr      error
}

type fakeIssueCreateCall struct {
	ProjectID string
	Input     map[string]interface{}
}

func (f *fakeInitClient) OnboardingBootstrap(input ottercli.OnboardingBootstrapRequest) (ottercli.OnboardingBootstrapResponse, error) {
	f.gotBootstrapRequest = input
	if f.bootstrapErr != nil {
		return ottercli.OnboardingBootstrapResponse{}, f.bootstrapErr
	}
	return f.bootstrapResponse, nil
}

func (f *fakeInitClient) CreateAgent(input map[string]any) (map[string]any, error) {
	f.createAgentInputs = append(f.createAgentInputs, input)
	if f.createAgentErr != nil {
		return nil, f.createAgentErr
	}
	return map[string]any{"id": "agent-1"}, nil
}

func (f *fakeInitClient) CreateProject(input map[string]interface{}) (ottercli.Project, error) {
	f.createProjectInputs = append(f.createProjectInputs, input)
	if f.createProjectErr != nil {
		return ottercli.Project{}, f.createProjectErr
	}
	return ottercli.Project{
		ID:   "project-1",
		Name: "Imported Project",
	}, nil
}

func (f *fakeInitClient) CreateIssue(projectID string, input map[string]interface{}) (ottercli.Issue, error) {
	f.createIssueCalls = append(f.createIssueCalls, fakeIssueCreateCall{
		ProjectID: projectID,
		Input:     input,
	})
	if f.createIssueErr != nil {
		return ottercli.Issue{}, f.createIssueErr
	}
	return ottercli.Issue{
		ID:          "issue-1",
		Title:       toString(input["title"]),
		IssueNumber: 1,
	}, nil
}

func TestHandleInitHostedModeFlagPrintsHandoff(t *testing.T) {
	var out bytes.Buffer
	err := runInitCommand([]string{"--mode", "hosted"}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !strings.Contains(out.String(), "Visit otter.camp/setup to get started.") {
		t.Fatalf("expected hosted handoff message, got %q", out.String())
	}
}

func TestHandleInitPromptRoutesHostedSelection(t *testing.T) {
	var out bytes.Buffer
	err := runInitCommand(nil, strings.NewReader("2\n"), &out)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Welcome to Otter Camp!") {
		t.Fatalf("expected welcome prompt, got %q", output)
	}
	if !strings.Contains(output, "[2] Hosted") {
		t.Fatalf("expected hosted option in prompt, got %q", output)
	}
	if !strings.Contains(output, "Visit otter.camp/setup to get started.") {
		t.Fatalf("expected hosted handoff message, got %q", output)
	}
}

func TestParseInitOptionsHostedFlags(t *testing.T) {
	opts, err := parseInitOptions([]string{"--mode", "hosted", "--token", "  oc_sess_123  ", "--url", " https://swh.otter.camp "})
	if err != nil {
		t.Fatalf("parseInitOptions() error = %v", err)
	}
	if opts.Mode != "hosted" {
		t.Fatalf("mode = %q, want hosted", opts.Mode)
	}
	if opts.Token != "oc_sess_123" {
		t.Fatalf("token = %q, want oc_sess_123", opts.Token)
	}
	if opts.URL != "https://swh.otter.camp" {
		t.Fatalf("url = %q, want https://swh.otter.camp", opts.URL)
	}
}

func TestDeriveHostedAPIBaseURLReturnsOrigin(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "origin", input: "https://swh.otter.camp", want: "https://swh.otter.camp"},
		{name: "api-path", input: "https://swh.otter.camp/api", want: "https://swh.otter.camp"},
		{name: "api-path-trailing-slash", input: "https://swh.otter.camp/api/", want: "https://swh.otter.camp"},
		{name: "extra-path", input: "https://swh.otter.camp/workspace/setup", want: "https://swh.otter.camp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := deriveHostedAPIBaseURL(tt.input)
			if err != nil {
				t.Fatalf("deriveHostedAPIBaseURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("deriveHostedAPIBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInitHostedWithFlagsPersistsConfig(t *testing.T) {
	state := stubInitDeps(t, ottercli.Config{}, &fakeInitClient{}, nil)
	state.hostedValidateOrg = "org-hosted"

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted", "--url", "https://swh.otter.camp"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !state.saveCalled {
		t.Fatalf("expected save config to be called")
	}
	if state.savedCfg.Token != "oc_sess_hosted" {
		t.Fatalf("saved token = %q, want oc_sess_hosted", state.savedCfg.Token)
	}
	if state.savedCfg.APIBaseURL != "https://swh.otter.camp" {
		t.Fatalf("saved api base = %q, want https://swh.otter.camp", state.savedCfg.APIBaseURL)
	}
	if state.gotAPIBase != "https://swh.otter.camp" {
		t.Fatalf("client api base = %q, want https://swh.otter.camp", state.gotAPIBase)
	}
	if state.savedCfg.DefaultOrg != "org-hosted" {
		t.Fatalf("saved default org = %q, want org-hosted", state.savedCfg.DefaultOrg)
	}
	if !strings.Contains(out.String(), "Hosted setup configured.") {
		t.Fatalf("expected hosted setup output, got %q", out.String())
	}
}

func TestInitHostedRequiresTokenAndURLPair(t *testing.T) {
	stubInitDeps(t, ottercli.Config{}, &fakeInitClient{}, nil)

	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted"},
		strings.NewReader(""),
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatalf("expected hosted argument validation error")
	}
	if !strings.Contains(err.Error(), "--mode hosted requires both --token and --url") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitHostedInvalidTokenReturnsClearError(t *testing.T) {
	state := stubInitDeps(t, ottercli.Config{}, &fakeInitClient{}, nil)
	state.hostedValidateErr = errors.New("401 unauthorized")

	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted", "--url", "https://swh.otter.camp"},
		strings.NewReader(""),
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatalf("expected hosted token validation error")
	}
	if !strings.Contains(err.Error(), "401 unauthorized") {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.saveCalled {
		t.Fatalf("config should not be saved when token validation fails")
	}
}

func TestInitHostedWhoamiPersistsOrgContext(t *testing.T) {
	state := stubInitDeps(t, ottercli.Config{}, &fakeInitClient{}, nil)
	state.hostedValidateOrg = "org-from-whoami"

	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted", "--url", "https://swh.otter.camp"},
		strings.NewReader(""),
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if state.savedCfg.DefaultOrg != "org-from-whoami" {
		t.Fatalf("saved default org = %q, want org-from-whoami", state.savedCfg.DefaultOrg)
	}
}

func TestInitHostedRunsImportAndStartsBridge(t *testing.T) {
	client := &fakeInitClient{}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.hostedValidateOrg = "org-hosted"
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir: "/Users/sam/.openclaw",
		Gateway: importer.OpenClawGatewayConfig{
			Host:  "127.0.0.1",
			Port:  18791,
			Token: "openclaw-token",
		},
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", Name: "Frank", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.identities = []importer.ImportedAgentIdentity{
		{ID: "main", Name: "Frank", Soul: "Chief of Staff"},
	}
	state.projects = []importer.OpenClawProjectCandidate{
		{
			Key:      "otter-camp",
			Name:     "Otter Camp",
			RepoPath: "/Users/sam/dev/otter-camp",
			Issues: []importer.OpenClawIssueCandidate{
				{Title: "Review imported context"},
			},
		},
	}

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted", "--url", "https://swh.otter.camp"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	if len(client.createAgentInputs) != 1 {
		t.Fatalf("expected one imported agent call, got %d", len(client.createAgentInputs))
	}
	if len(client.createProjectInputs) != 1 {
		t.Fatalf("expected one imported project call, got %d", len(client.createProjectInputs))
	}
	if len(client.createIssueCalls) != 1 {
		t.Fatalf("expected one imported issue call, got %d", len(client.createIssueCalls))
	}
	if !state.bridgeWriteCalled {
		t.Fatalf("expected bridge env write call")
	}
	if !state.bridgeStarted {
		t.Fatalf("expected hosted init to start bridge non-interactively")
	}
	if state.bridgeValues["OTTERCAMP_URL"] != "https://swh.otter.camp" {
		t.Fatalf("bridge otter URL = %q, want https://swh.otter.camp", state.bridgeValues["OTTERCAMP_URL"])
	}

	output := out.String()
	if !strings.Contains(output, "Hosted phase: Detect OpenClaw") {
		t.Fatalf("expected hosted progress output, got %q", output)
	}
	if !strings.Contains(output, "Hosted phase: Start bridge") {
		t.Fatalf("expected hosted bridge phase output, got %q", output)
	}
}

func TestInitHostedAutomaticallyRunsMigrationWhenDatabaseConfigured(t *testing.T) {
	client := &fakeInitClient{}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.hostedValidateOrg = "org-hosted"
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir: "/Users/sam/.openclaw",
		Gateway: importer.OpenClawGatewayConfig{
			Host:  "127.0.0.1",
			Port:  18791,
			Token: "openclaw-token",
		},
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", Name: "Frank", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.migrateDatabaseURL = "postgres://cfg-user:cfg-pass@localhost:5432/otter?sslmode=disable"

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted", "--url", "https://swh.otter.camp"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !state.migrateCalled {
		t.Fatalf("expected hosted init to run migration automatically")
	}
	if state.migrateOpts.OpenClawDir != "/Users/sam/.openclaw" {
		t.Fatalf("migration openclaw dir = %q", state.migrateOpts.OpenClawDir)
	}
	if state.migrateOpts.OrgID != "org-hosted" {
		t.Fatalf("migration org id = %q, want org-hosted", state.migrateOpts.OrgID)
	}
	output := out.String()
	if !strings.Contains(output, "Migration progress: preparing OpenClaw migration input.") {
		t.Fatalf("expected migration prep progress output, got %q", output)
	}
	if !strings.Contains(output, "Migration progress: running otter migrate from-openclaw.") {
		t.Fatalf("expected migration run progress output, got %q", output)
	}
	if !strings.Contains(output, "Migration progress: complete.") {
		t.Fatalf("expected migration completion output, got %q", output)
	}
}

func TestInitHostedPrintsMigrationCommandWhenDatabaseUnavailable(t *testing.T) {
	client := &fakeInitClient{}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.hostedValidateOrg = "org-hosted"
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir: "/Users/sam/.openclaw",
		Gateway: importer.OpenClawGatewayConfig{
			Host:  "127.0.0.1",
			Port:  18791,
			Token: "openclaw-token",
		},
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", Name: "Frank", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.migrateResolveErr = errors.New("DATABASE_URL is required for otter migrate from-openclaw")

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted", "--url", "https://swh.otter.camp"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if state.migrateCalled {
		t.Fatalf("expected hosted init not to run migration when DATABASE_URL is unavailable")
	}
	output := out.String()
	if !strings.Contains(output, "Run this command later to migrate OpenClaw data:") {
		t.Fatalf("expected migration follow-up command output, got %q", output)
	}
	if !strings.Contains(output, "DATABASE_URL=\"<database-url>\" otter migrate from-openclaw --org \"org-hosted\" --openclaw-dir \"/Users/sam/.openclaw\"") {
		t.Fatalf("expected explicit migration command with env vars, got %q", output)
	}
}

func TestInitLocalRunsMigrationWhenUserAccepts(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir: "/Users/sam/.openclaw",
		Gateway: importer.OpenClawGatewayConfig{
			Host:  "127.0.0.1",
			Port:  18791,
			Token: "openclaw-token",
		},
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", Name: "Frank", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.migrateDatabaseURL = "postgres://cfg-user:cfg-pass@localhost:5432/otter?sslmode=disable"

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader("n\nn\ny\n"),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !state.migrateCalled {
		t.Fatalf("expected migration call when user accepts prompt")
	}
	if !strings.Contains(out.String(), "Migrate OpenClaw history now? (y/N): ") {
		t.Fatalf("expected migration prompt in output, got %q", out.String())
	}
}

func TestInitHostedReportsMigrationFailureAndRetryCommand(t *testing.T) {
	client := &fakeInitClient{}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.hostedValidateOrg = "org-hosted"
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir: "/Users/sam/.openclaw",
		Gateway: importer.OpenClawGatewayConfig{
			Host:  "127.0.0.1",
			Port:  18791,
			Token: "openclaw-token",
		},
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", Name: "Frank", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.migrateDatabaseURL = "postgres://cfg-user:cfg-pass@localhost:5432/otter?sslmode=disable"
	state.migrateErr = errors.New("network timeout")

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "hosted", "--token", "oc_sess_hosted", "--url", "https://swh.otter.camp"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "Migration progress: failed (network timeout)") {
		t.Fatalf("expected migration failure progress output, got %q", output)
	}
	if !strings.Contains(output, "Retry with:") {
		t.Fatalf("expected retry command output, got %q", output)
	}
	if !strings.Contains(output, "DATABASE_URL=\"postgres://cfg-user:cfg-pass@localhost:5432/otter?sslmode=disable\" otter migrate from-openclaw --org \"org-hosted\" --openclaw-dir \"/Users/sam/.openclaw\"") {
		t.Fatalf("expected explicit retry command output, got %q", output)
	}
}

func TestBuildBridgeEnvValuesNormalizesHostedAPIPath(t *testing.T) {
	values := buildBridgeEnvValues(nil, ottercli.Config{
		APIBaseURL: "https://swh.otter.camp/api",
		Token:      "oc_sess_hosted",
	})

	if values["OTTERCAMP_URL"] != "https://swh.otter.camp" {
		t.Fatalf("OTTERCAMP_URL = %q, want https://swh.otter.camp", values["OTTERCAMP_URL"])
	}
}

func TestResolveBridgeScriptPath(t *testing.T) {
	t.Run("returns validated bridge script path", func(t *testing.T) {
		root := t.TempDir()
		scriptPath := filepath.Join(root, "bridge", "openclaw-bridge.ts")
		if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
			t.Fatalf("mkdir bridge dir: %v", err)
		}
		if err := os.WriteFile(scriptPath, []byte("console.log('ok')"), 0o644); err != nil {
			t.Fatalf("write bridge script: %v", err)
		}

		resolved, err := resolveBridgeScriptPath(root)
		if err != nil {
			t.Fatalf("resolveBridgeScriptPath() error = %v", err)
		}
		if resolved != scriptPath {
			t.Fatalf("resolved bridge script = %q, want %q", resolved, scriptPath)
		}
	})

	t.Run("returns clear error when script is missing", func(t *testing.T) {
		root := t.TempDir()
		_, err := resolveBridgeScriptPath(root)
		if err == nil {
			t.Fatalf("expected missing bridge script error")
		}
		if !strings.Contains(err.Error(), "bridge script not found") {
			t.Fatalf("expected clear missing-script error, got %v", err)
		}
	})
}

func TestWriteBridgeEnvFileIncludesBridgeScript(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "bridge", "openclaw-bridge.ts")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir bridge dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write bridge script: %v", err)
	}

	path, err := writeBridgeEnvFile(root, map[string]string{
		"OPENCLAW_HOST":      "127.0.0.1",
		"OPENCLAW_PORT":      "18791",
		"OPENCLAW_TOKEN":     "token",
		"OTTERCAMP_URL":      "https://swh.otter.camp",
		"OTTERCAMP_TOKEN":    "otter-token",
		"OPENCLAW_WS_SECRET": "secret",
		"BRIDGE_SCRIPT":      scriptPath,
	})
	if err != nil {
		t.Fatalf("writeBridgeEnvFile() error = %v", err)
	}
	if path == "" {
		t.Fatalf("expected bridge env path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bridge env: %v", err)
	}
	if !strings.Contains(string(raw), "BRIDGE_SCRIPT="+scriptPath) {
		t.Fatalf("expected bridge script line in env file, got %q", string(raw))
	}
}

func TestStartBridgeProcessReportsMissingScript(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer

	err := startBridgeProcess(root, &out)
	if err == nil {
		t.Fatalf("expected missing bridge script error")
	}
	if !strings.Contains(err.Error(), "bridge script not found") {
		t.Fatalf("expected clear missing-script message, got %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "err_module_not_found") {
		t.Fatalf("expected pre-spawn validation error, got node module error: %v", err)
	}
}

func TestHandleInitPromptDefaultsToLocalSelection(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-local",
			Token: "oc_sess_local",
		},
	}
	stubInitDeps(t, ottercli.Config{}, client, nil)

	var out bytes.Buffer
	err := runInitCommand(nil, strings.NewReader("1\nSam\nsam@example.com\nMy Team\n"), &out)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !strings.Contains(out.String(), "Account setup complete.") {
		t.Fatalf("expected local bootstrap completion message, got %q", out.String())
	}
}

func TestHandleInitRejectsInvalidModeFlag(t *testing.T) {
	err := runInitCommand([]string{"--mode", "cloud"}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil {
		t.Fatalf("expected invalid mode error")
	}
	if !strings.Contains(err.Error(), "--mode must be local or hosted") {
		t.Fatalf("expected mode validation error, got %v", err)
	}
}

func TestInitBootstrapLocalSuccessPersistsConfig(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	if client.gotBootstrapRequest.Name != "Sam" || client.gotBootstrapRequest.Email != "sam@example.com" || client.gotBootstrapRequest.OrganizationName != "My Team" {
		t.Fatalf("bootstrap request = %#v", client.gotBootstrapRequest)
	}
	if state.gotAPIBase != "http://localhost:4200" {
		t.Fatalf("api base = %q, want http://localhost:4200", state.gotAPIBase)
	}
	if !state.saveCalled {
		t.Fatalf("expected save config to be called")
	}
	if state.savedCfg.APIBaseURL != "http://localhost:4200" {
		t.Fatalf("saved api base = %q, want http://localhost:4200", state.savedCfg.APIBaseURL)
	}
	if state.savedCfg.Token != "oc_sess_bootstrap" {
		t.Fatalf("saved token = %q, want oc_sess_bootstrap", state.savedCfg.Token)
	}
	if state.savedCfg.DefaultOrg != "org-bootstrap" {
		t.Fatalf("saved org = %q, want org-bootstrap", state.savedCfg.DefaultOrg)
	}

	output := out.String()
	if strings.Count(output, "oc_sess_bootstrap") != 1 {
		t.Fatalf("expected token to appear once in output, got %q", output)
	}
	if !strings.Contains(output, "Next step: otter whoami") {
		t.Fatalf("expected follow-up instruction, got %q", output)
	}
}

func TestInitBootstrapLocalAPIFailureSkipsConfigSave(t *testing.T) {
	client := &fakeInitClient{bootstrapErr: errors.New("bootstrap failed")}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)

	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader(""),
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "bootstrap failed") {
		t.Fatalf("expected bootstrap error, got %v", err)
	}
	if state.saveCalled {
		t.Fatalf("save config should not be called when bootstrap fails")
	}
}

func TestInitBootstrapLocalConfigSaveFailureReturnsError(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	stubInitDeps(t, ottercli.Config{}, client, errors.New("save failed"))

	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader(""),
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "save failed") {
		t.Fatalf("expected save failure error, got %v", err)
	}
}

func TestInitBootstrapLocalPrintsSeededStarterAgents(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
			Agents: []ottercli.OnboardingAgent{
				{ID: "agent-frank", Slug: "frank", DisplayName: "Frank"},
				{ID: "agent-lori", Slug: "lori", DisplayName: "Lori"},
				{ID: "agent-ellie", Slug: "ellie", DisplayName: "Ellie"},
			},
		},
	}
	stubInitDeps(t, ottercli.Config{}, client, nil)

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	if !strings.Contains(out.String(), "Created agents: Frank, Lori, Ellie") {
		t.Fatalf("expected seeded agents confirmation, got %q", out.String())
	}
}

func TestInitImportAndBridgeApprovedFlowImportsAndStartsBridge(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir: "/Users/sam/.openclaw",
		Gateway: importer.OpenClawGatewayConfig{
			Host:  "127.0.0.1",
			Port:  18791,
			Token: "openclaw-token",
		},
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", Name: "Frank", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.identities = []importer.ImportedAgentIdentity{
		{ID: "main", Name: "Frank", Soul: "Chief of Staff"},
	}
	state.projects = []importer.OpenClawProjectCandidate{
		{
			Key:      "otter-camp",
			Name:     "Otter Camp",
			RepoPath: "/Users/sam/dev/otter-camp",
			Issues: []importer.OpenClawIssueCandidate{
				{Title: "Review imported context"},
			},
		},
	}

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader("y\ny\n"),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	if len(client.createAgentInputs) != 1 {
		t.Fatalf("expected one imported agent call, got %d", len(client.createAgentInputs))
	}
	if len(client.createProjectInputs) != 1 {
		t.Fatalf("expected one imported project call, got %d", len(client.createProjectInputs))
	}
	if len(client.createIssueCalls) != 1 {
		t.Fatalf("expected one imported issue call, got %d", len(client.createIssueCalls))
	}
	if !state.bridgeWriteCalled {
		t.Fatalf("expected bridge env write call")
	}
	if state.bridgeValues["OPENCLAW_TOKEN"] != "openclaw-token" {
		t.Fatalf("bridge token = %q", state.bridgeValues["OPENCLAW_TOKEN"])
	}
	if state.bridgeValues["OTTERCAMP_TOKEN"] != "oc_sess_bootstrap" {
		t.Fatalf("bridge otter token = %q", state.bridgeValues["OTTERCAMP_TOKEN"])
	}
	if !state.bridgeStarted {
		t.Fatalf("expected bridge start call")
	}

	output := out.String()
	if !strings.Contains(output, "Imported 1 agents, 1 projects, 1 issues") {
		t.Fatalf("expected import summary, got %q", output)
	}
	if !strings.Contains(output, "Bridge config written: /repo/bridge/.env") {
		t.Fatalf("expected bridge config output, got %q", output)
	}
}

func TestInitImportAndBridgeSkipsWhenOpenClawMissing(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.detectErr = importer.ErrOpenClawNotFound

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if state.bridgeWriteCalled {
		t.Fatalf("bridge config should not be written when OpenClaw is missing")
	}
	if state.bridgeStarted {
		t.Fatalf("bridge should not start when OpenClaw is missing")
	}
	if !strings.Contains(out.String(), "OpenClaw installation not detected") {
		t.Fatalf("expected missing-openclaw message, got %q", out.String())
	}
}

func TestInitAddsRequiredOpenClawAgentsToConfig(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir:    "/Users/sam/.openclaw",
		ConfigPath: "/Users/sam/.openclaw/openclaw.json",
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.ensureResult = importer.EnsureOpenClawRequiredAgentsResult{
		Updated:        true,
		AddedElephant:  true,
		AddedChameleon: true,
	}

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader("n\nn\n"),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	if !state.ensureCalled {
		t.Fatalf("expected OpenClaw required-agent ensure call")
	}
	if !state.ensureOpts.IncludeChameleon {
		t.Fatalf("expected IncludeChameleon=true")
	}

	output := out.String()
	if !strings.Contains(output, "Added Ellie (Elephant) to OpenClaw config.") {
		t.Fatalf("expected elephant added message, got %q", output)
	}
	if !strings.Contains(output, "Added Chameleon to OpenClaw config.") {
		t.Fatalf("expected chameleon added message, got %q", output)
	}
	if !strings.Contains(output, "OpenClaw restarted to activate new agents.") {
		t.Fatalf("expected restart message, got %q", output)
	}
	if !state.restartCalled {
		t.Fatalf("expected OpenClaw gateway restart to be attempted")
	}
}

func TestInitWarnsWhenUnsupportedOpenClawKeysAreSkipped(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir:    "/Users/sam/.openclaw",
		ConfigPath: "/Users/sam/.openclaw/openclaw.json",
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.ensureResult = importer.EnsureOpenClawRequiredAgentsResult{
		Updated:            true,
		AddedElephant:      true,
		AddedChameleon:     true,
		DroppedUnknownKeys: []string{"channels", "thinking"},
	}

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader("n\nn\n"),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "WARNING: OpenClaw config skipped unsupported agent keys: channels, thinking") {
		t.Fatalf("expected skipped-key warning, got %q", output)
	}
}

func TestInitSkipsOpenClawAgentConfigUpdateWhenAlreadyPresent(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir:    "/Users/sam/.openclaw",
		ConfigPath: "/Users/sam/.openclaw/openclaw.json",
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.ensureResult = importer.EnsureOpenClawRequiredAgentsResult{
		Updated: false,
	}

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader("n\nn\n"),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !state.ensureCalled {
		t.Fatalf("expected OpenClaw required-agent ensure call")
	}
	if !strings.Contains(out.String(), "Required OpenClaw agents already present. No config changes made.") {
		t.Fatalf("expected no-op message, got %q", out.String())
	}
}

func TestInitHandlesEnsureOpenClawAgentsError(t *testing.T) {
	client := &fakeInitClient{
		bootstrapResponse: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	state := stubInitDeps(t, ottercli.Config{}, client, nil)
	state.detectInstall = &importer.OpenClawInstallation{
		RootDir:    "/Users/sam/.openclaw",
		ConfigPath: "/Users/sam/.openclaw/openclaw.json",
		Agents: []importer.OpenClawAgentWorkspace{
			{ID: "main", WorkspaceDir: "/Users/sam/.openclaw/workspaces/main"},
		},
	}
	state.detectErr = nil
	state.ensureErr = errors.New("write denied")

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader("n\nn\n"),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "WARNING: OpenClaw config update failed: write denied") {
		t.Fatalf("expected warning message, got %q", output)
	}
	if !strings.Contains(output, "Bridge config written: /repo/bridge/.env") {
		t.Fatalf("expected init flow to continue and write bridge config, got %q", output)
	}
}

type initStubState struct {
	gotAPIBase string
	savedCfg   ottercli.Config
	saveCalled bool

	hostedValidateOrg string
	hostedValidateErr error

	detectInstall   *importer.OpenClawInstallation
	detectErr       error
	ensureCalled    bool
	ensureOpts      importer.EnsureOpenClawRequiredAgentsOptions
	ensureResult    importer.EnsureOpenClawRequiredAgentsResult
	ensureErr       error
	identities      []importer.ImportedAgentIdentity
	projects        []importer.OpenClawProjectCandidate
	repoRoot        string
	bridgeScript    string
	bridgeScriptErr error

	bridgeWriteCalled bool
	bridgeValues      map[string]string
	bridgePath        string
	bridgeWriteErr    error

	bridgeStarted  bool
	bridgeStartErr error

	restartCalled bool
	restartErr    error

	migrateDatabaseURL string
	migrateResolveErr  error
	migrateCalled      bool
	migrateOpts        migrateFromOpenClawOptions
	migrateErr         error
}

func stubInitDeps(t *testing.T, loadCfg ottercli.Config, client *fakeInitClient, saveErr error) *initStubState {
	t.Helper()

	state := &initStubState{
		detectErr:    importer.ErrOpenClawNotFound,
		repoRoot:     "/repo",
		bridgeScript: "/repo/bridge/openclaw-bridge.ts",
		bridgePath:   "/repo/bridge/.env",
	}

	origLoad := loadInitConfig
	origSave := saveInitConfig
	origNewClient := newInitClient
	origHostedValidate := validateHostedInitToken
	origDetect := detectInitOpenClaw
	origEnsure := ensureInitOpenClawRequiredAgents
	origImport := importInitOpenClawIdentities
	origInfer := inferInitOpenClawProjects
	origRestartGateway := restartInitOpenClawGateway
	origRepoRoot := resolveInitRepoRoot
	origResolveBridgeScript := resolveInitBridgeScriptPath
	origWriteBridge := writeInitBridgeEnv
	origStartBridge := startInitBridge
	origResolveMigrateDatabaseURL := resolveInitMigrateDatabaseURL
	origRunMigrate := runInitOpenClawMigration

	loadInitConfig = func() (ottercli.Config, error) {
		return loadCfg, nil
	}
	saveInitConfig = func(cfg ottercli.Config) error {
		state.saveCalled = true
		state.savedCfg = cfg
		return saveErr
	}
	newInitClient = func(cfg ottercli.Config) (initBootstrapClient, error) {
		state.gotAPIBase = cfg.APIBaseURL
		return client, nil
	}
	validateHostedInitToken = func(apiBaseURL, token string) (string, error) {
		if state.hostedValidateErr != nil {
			return "", state.hostedValidateErr
		}
		return state.hostedValidateOrg, nil
	}
	detectInitOpenClaw = func() (*importer.OpenClawInstallation, error) {
		if state.detectErr != nil {
			return nil, state.detectErr
		}
		return state.detectInstall, nil
	}
	ensureInitOpenClawRequiredAgents = func(
		install *importer.OpenClawInstallation,
		opts importer.EnsureOpenClawRequiredAgentsOptions,
	) (importer.EnsureOpenClawRequiredAgentsResult, error) {
		state.ensureCalled = true
		state.ensureOpts = opts
		if state.ensureErr != nil {
			return importer.EnsureOpenClawRequiredAgentsResult{}, state.ensureErr
		}
		return state.ensureResult, nil
	}
	importInitOpenClawIdentities = func(install *importer.OpenClawInstallation) ([]importer.ImportedAgentIdentity, error) {
		return state.identities, nil
	}
	inferInitOpenClawProjects = func(input importer.OpenClawProjectImportInput) []importer.OpenClawProjectCandidate {
		return state.projects
	}
	restartInitOpenClawGateway = func(out io.Writer) error {
		state.restartCalled = true
		return state.restartErr
	}
	resolveInitRepoRoot = func() (string, error) {
		return state.repoRoot, nil
	}
	resolveInitBridgeScriptPath = func(repoRoot string) (string, error) {
		if state.bridgeScriptErr != nil {
			return "", state.bridgeScriptErr
		}
		return state.bridgeScript, nil
	}
	writeInitBridgeEnv = func(repoRoot string, values map[string]string) (string, error) {
		state.bridgeWriteCalled = true
		state.bridgeValues = values
		if state.bridgeWriteErr != nil {
			return "", state.bridgeWriteErr
		}
		return state.bridgePath, nil
	}
	startInitBridge = func(repoRoot string, out io.Writer) error {
		state.bridgeStarted = true
		return state.bridgeStartErr
	}
	resolveInitMigrateDatabaseURL = func() (string, error) {
		if state.migrateResolveErr != nil {
			return "", state.migrateResolveErr
		}
		return state.migrateDatabaseURL, nil
	}
	runInitOpenClawMigration = func(out io.Writer, opts migrateFromOpenClawOptions) error {
		state.migrateCalled = true
		state.migrateOpts = opts
		return state.migrateErr
	}

	t.Cleanup(func() {
		loadInitConfig = origLoad
		saveInitConfig = origSave
		newInitClient = origNewClient
		validateHostedInitToken = origHostedValidate
		detectInitOpenClaw = origDetect
		ensureInitOpenClawRequiredAgents = origEnsure
		importInitOpenClawIdentities = origImport
		inferInitOpenClawProjects = origInfer
		restartInitOpenClawGateway = origRestartGateway
		resolveInitRepoRoot = origRepoRoot
		resolveInitBridgeScriptPath = origResolveBridgeScript
		writeInitBridgeEnv = origWriteBridge
		startInitBridge = origStartBridge
		resolveInitMigrateDatabaseURL = origResolveMigrateDatabaseURL
		runInitOpenClawMigration = origRunMigrate
	})

	return state
}
