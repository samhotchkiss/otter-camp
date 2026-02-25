package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultServerURL = "http://localhost:4110"
	serverURLEnvKey  = "OTTERCAMP_SERVER_URL"
	apiKeyEnvKey     = "OTTERCAMP_API_KEY"
)

type Credentials struct {
	ServerURL string
	APIKey    string
}

type CredentialStore struct {
	Path      string
	LookupEnv func(string) string
}

func NewCredentialStore() CredentialStore {
	home, _ := os.UserHomeDir()
	path := ""
	if strings.TrimSpace(home) != "" {
		path = filepath.Join(home, ".ottercamp", "credentials")
	}
	return CredentialStore{
		Path: path,
		LookupEnv: func(key string) string {
			return os.Getenv(key)
		},
	}
}

func (s CredentialStore) Load() (Credentials, error) {
	creds := Credentials{}
	if strings.TrimSpace(s.Path) != "" {
		raw, err := os.ReadFile(s.Path)
		switch {
		case err == nil:
			parsed := parseCredentials(raw)
			creds.ServerURL = parsed.ServerURL
			creds.APIKey = parsed.APIKey
		case errors.Is(err, os.ErrNotExist):
			// Missing credentials file is not an error.
		case err != nil:
			return Credentials{}, fmt.Errorf("read credentials: %w", err)
		}
	}

	lookup := s.LookupEnv
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	if value := strings.TrimSpace(lookup(serverURLEnvKey)); value != "" {
		creds.ServerURL = value
	}
	if value := strings.TrimSpace(lookup(apiKeyEnvKey)); value != "" {
		creds.APIKey = value
	}

	return creds, nil
}

func (s CredentialStore) Save(creds Credentials) error {
	if strings.TrimSpace(s.Path) == "" {
		return fmt.Errorf("credentials path is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create credentials dir: %w", err)
	}

	content := strings.TrimSpace(fmt.Sprintf("[default]\nserver_url = %s\napi_key = %s\n", strings.TrimSpace(creds.ServerURL), strings.TrimSpace(creds.APIKey))) + "\n"
	if err := os.WriteFile(s.Path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Chmod(s.Path, 0o600); err != nil {
		return fmt.Errorf("set credentials mode: %w", err)
	}
	return nil
}

func (s CredentialStore) Clear() error {
	if strings.TrimSpace(s.Path) == "" {
		return nil
	}
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove credentials: %w", err)
	}
	return nil
}

func parseCredentials(raw []byte) Credentials {
	creds := Credentials{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "server_url", "api_url":
			if creds.ServerURL == "" {
				creds.ServerURL = value
			}
		case "api_key":
			if creds.APIKey == "" {
				creds.APIKey = value
			}
		}
	}
	return creds
}

func ResolveServerURL(explicit string, creds Credentials) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	if value := strings.TrimSpace(creds.ServerURL); value != "" {
		return value
	}
	return defaultServerURL
}
