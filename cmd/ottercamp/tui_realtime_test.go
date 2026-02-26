package main

import (
	"strings"
	"testing"

	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
)

func TestResolveTUIRealtimeCredentialsFlagOverrides(t *testing.T) {
	restore := setTUIRealtimeGlobalsForTest(t, "https://global.example", "global-key", clitools.CredentialStore{
		LookupEnv: func(string) string { return "" },
	})
	defer restore()

	serverURL, apiKey, err := resolveTUIRealtimeCredentials("https://flag.example", "flag-key")
	if err != nil {
		t.Fatalf("resolveTUIRealtimeCredentials: %v", err)
	}
	if serverURL != "https://flag.example" {
		t.Fatalf("serverURL = %q, want %q", serverURL, "https://flag.example")
	}
	if apiKey != "flag-key" {
		t.Fatalf("apiKey = %q, want %q", apiKey, "flag-key")
	}
}

func TestResolveTUIRealtimeCredentialsFallsBackToGlobal(t *testing.T) {
	restore := setTUIRealtimeGlobalsForTest(t, "https://global.example", "global-key", clitools.CredentialStore{
		LookupEnv: func(string) string { return "" },
	})
	defer restore()

	serverURL, apiKey, err := resolveTUIRealtimeCredentials("", "")
	if err != nil {
		t.Fatalf("resolveTUIRealtimeCredentials: %v", err)
	}
	if serverURL != "https://global.example" {
		t.Fatalf("serverURL = %q, want %q", serverURL, "https://global.example")
	}
	if apiKey != "global-key" {
		t.Fatalf("apiKey = %q, want %q", apiKey, "global-key")
	}
}

func TestResolveTUIRealtimeCredentialsFallsBackToEnvCredentials(t *testing.T) {
	restore := setTUIRealtimeGlobalsForTest(t, "", "", clitools.CredentialStore{
		LookupEnv: func(key string) string {
			switch key {
			case "OTTERCAMP_SERVER_URL":
				return "https://env.example"
			case "OTTERCAMP_API_KEY":
				return "env-key"
			default:
				return ""
			}
		},
	})
	defer restore()

	serverURL, apiKey, err := resolveTUIRealtimeCredentials("", "")
	if err != nil {
		t.Fatalf("resolveTUIRealtimeCredentials: %v", err)
	}
	if serverURL != "https://env.example" {
		t.Fatalf("serverURL = %q, want %q", serverURL, "https://env.example")
	}
	if apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want %q", apiKey, "env-key")
	}
}

func TestResolveTUIRealtimeCredentialsRequiresAPIKey(t *testing.T) {
	restore := setTUIRealtimeGlobalsForTest(t, "", "", clitools.CredentialStore{
		LookupEnv: func(string) string { return "" },
	})
	defer restore()

	_, _, err := resolveTUIRealtimeCredentials("", "")
	if err == nil {
		t.Fatal("expected error when no api key is available")
	}
	if !strings.Contains(err.Error(), "api key required") {
		t.Fatalf("error = %q, want api key guidance", err)
	}
}

func setTUIRealtimeGlobalsForTest(t *testing.T, serverURL, apiKey string, store clitools.CredentialStore) func() {
	t.Helper()
	origServerURL := globalServerURL
	origAPIKey := globalAPIKey
	origStore := credentialStore

	globalServerURL = strings.TrimSpace(serverURL)
	globalAPIKey = strings.TrimSpace(apiKey)
	credentialStore = store

	return func() {
		globalServerURL = origServerURL
		globalAPIKey = origAPIKey
		credentialStore = origStore
	}
}
