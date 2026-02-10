package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

type fakeInitClient struct {
	gotRequest ottercli.OnboardingBootstrapRequest
	response   ottercli.OnboardingBootstrapResponse
	err        error
}

func (f *fakeInitClient) OnboardingBootstrap(input ottercli.OnboardingBootstrapRequest) (ottercli.OnboardingBootstrapResponse, error) {
	f.gotRequest = input
	if f.err != nil {
		return ottercli.OnboardingBootstrapResponse{}, f.err
	}
	return f.response, nil
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

func TestHandleInitPromptDefaultsToLocalSelection(t *testing.T) {
	stubInitDeps(t, ottercli.Config{}, &fakeInitClient{
		response: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-local",
			Token: "oc_sess_local",
		},
	}, nil)

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
		response: ottercli.OnboardingBootstrapResponse{
			OrgID: "org-bootstrap",
			Token: "oc_sess_bootstrap",
		},
	}
	saveState := stubInitDeps(t, ottercli.Config{}, client, nil)

	var out bytes.Buffer
	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader(""),
		&out,
	)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	if client.gotRequest.Name != "Sam" || client.gotRequest.Email != "sam@example.com" || client.gotRequest.OrganizationName != "My Team" {
		t.Fatalf("bootstrap request = %#v", client.gotRequest)
	}
	if saveState.gotAPIBase != "http://localhost:4200" {
		t.Fatalf("api base = %q, want http://localhost:4200", saveState.gotAPIBase)
	}
	if !saveState.saveCalled {
		t.Fatalf("expected save config to be called")
	}
	if saveState.savedCfg.APIBaseURL != "http://localhost:4200" {
		t.Fatalf("saved api base = %q, want http://localhost:4200", saveState.savedCfg.APIBaseURL)
	}
	if saveState.savedCfg.Token != "oc_sess_bootstrap" {
		t.Fatalf("saved token = %q, want oc_sess_bootstrap", saveState.savedCfg.Token)
	}
	if saveState.savedCfg.DefaultOrg != "org-bootstrap" {
		t.Fatalf("saved org = %q, want org-bootstrap", saveState.savedCfg.DefaultOrg)
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
	client := &fakeInitClient{err: errors.New("bootstrap failed")}
	saveState := stubInitDeps(t, ottercli.Config{}, client, nil)

	err := runInitCommand(
		[]string{"--mode", "local", "--name", "Sam", "--email", "sam@example.com", "--org-name", "My Team"},
		strings.NewReader(""),
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "bootstrap failed") {
		t.Fatalf("expected bootstrap error, got %v", err)
	}
	if saveState.saveCalled {
		t.Fatalf("save config should not be called when bootstrap fails")
	}
}

func TestInitBootstrapLocalConfigSaveFailureReturnsError(t *testing.T) {
	client := &fakeInitClient{
		response: ottercli.OnboardingBootstrapResponse{
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

type initStubState struct {
	gotAPIBase string
	savedCfg   ottercli.Config
	saveCalled bool
}

func stubInitDeps(t *testing.T, loadCfg ottercli.Config, client *fakeInitClient, saveErr error) *initStubState {
	t.Helper()

	state := &initStubState{}
	origLoad := loadInitConfig
	origSave := saveInitConfig
	origNewClient := newInitClient

	loadInitConfig = func() (ottercli.Config, error) {
		return loadCfg, nil
	}
	saveInitConfig = func(cfg ottercli.Config) error {
		state.saveCalled = true
		state.savedCfg = cfg
		return saveErr
	}
	newInitClient = func(apiBase string) (initBootstrapClient, error) {
		state.gotAPIBase = apiBase
		return client, nil
	}

	t.Cleanup(func() {
		loadInitConfig = origLoad
		saveInitConfig = origSave
		newInitClient = origNewClient
	})
	return state
}
