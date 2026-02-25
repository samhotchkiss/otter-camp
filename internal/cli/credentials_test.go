package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialStoreLoadMissingFileReturnsZeroValue(t *testing.T) {
	store := CredentialStore{
		Path: filepath.Join(t.TempDir(), "credentials"),
		LookupEnv: func(string) string {
			return ""
		},
	}

	creds, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if creds.APIKey != "" || creds.ServerURL != "" {
		t.Fatalf("unexpected credentials: %+v", creds)
	}
}

func TestCredentialStoreLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials")
	if err := os.WriteFile(path, []byte("[default]\nserver_url = http://from-file\napi_key = file-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := CredentialStore{
		Path: path,
		LookupEnv: func(key string) string {
			switch key {
			case serverURLEnvKey:
				return "http://from-env"
			case apiKeyEnvKey:
				return "env-key"
			default:
				return ""
			}
		},
	}

	creds, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if creds.ServerURL != "http://from-env" {
		t.Fatalf("ServerURL = %q, want %q", creds.ServerURL, "http://from-env")
	}
	if creds.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want %q", creds.APIKey, "env-key")
	}
}

func TestCredentialStoreSaveUses0600Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials")
	store := CredentialStore{Path: path}

	if err := store.Save(Credentials{ServerURL: "http://localhost:4110", APIKey: "test-key"}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %#o, want %#o", got, 0o600)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(raw), "server_url = http://localhost:4110") {
		t.Fatalf("credentials file missing server_url: %q", string(raw))
	}
}
