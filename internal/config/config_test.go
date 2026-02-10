package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("APP_ENV", "")
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("GO_ENV", "")
	t.Setenv("RAILWAY_ENVIRONMENT", "")
	t.Setenv("GITHUB_INTEGRATION_ENABLED", "")
	t.Setenv("GITHUB_POLL_INTERVAL", "")
	t.Setenv("GITHUB_REPO_ROOT", "")
	t.Setenv("GITHUB_API_BASE_URL", "")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PEM", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Port != defaultPort {
		t.Fatalf("expected default port %q, got %q", defaultPort, cfg.Port)
	}
	if cfg.Port != "4200" {
		t.Fatalf("expected local default port 4200, got %q", cfg.Port)
	}

	if cfg.Environment != defaultEnvironment {
		t.Fatalf("expected default environment %q, got %q", defaultEnvironment, cfg.Environment)
	}

	if cfg.GitHub.Enabled {
		t.Fatalf("expected GitHub integration disabled by default")
	}

	if cfg.GitHub.RepoRoot != defaultGitHubRepoRoot {
		t.Fatalf("expected default repo root %q, got %q", defaultGitHubRepoRoot, cfg.GitHub.RepoRoot)
	}

	if cfg.GitHub.PollInterval != defaultGitHubPollInterval {
		t.Fatalf("expected default poll interval %v, got %v", defaultGitHubPollInterval, cfg.GitHub.PollInterval)
	}

	if cfg.GitHub.APIBaseURL != defaultGitHubAPIBaseURL {
		t.Fatalf("expected default API base URL %q, got %q", defaultGitHubAPIBaseURL, cfg.GitHub.APIBaseURL)
	}
}

func TestLoadParsesGitHubSettings(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("GITHUB_INTEGRATION_ENABLED", "true")
	t.Setenv("GITHUB_APP_ID", "123456")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "/tmp/github.pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("GITHUB_REPO_ROOT", "/srv/repos")
	t.Setenv("GITHUB_POLL_INTERVAL", "90m")
	t.Setenv("GITHUB_API_BASE_URL", "https://ghe.example.com/api/v3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !cfg.GitHub.Enabled {
		t.Fatalf("expected GitHub integration enabled")
	}

	if cfg.GitHub.AppID != "123456" {
		t.Fatalf("expected app id 123456, got %q", cfg.GitHub.AppID)
	}

	if cfg.GitHub.AppPrivateKeyPath != "/tmp/github.pem" {
		t.Fatalf("expected key path /tmp/github.pem, got %q", cfg.GitHub.AppPrivateKeyPath)
	}

	if cfg.GitHub.PollInterval != 90*time.Minute {
		t.Fatalf("expected poll interval 90m, got %v", cfg.GitHub.PollInterval)
	}

	if cfg.GitHub.RepoRoot != "/srv/repos" {
		t.Fatalf("expected repo root /srv/repos, got %q", cfg.GitHub.RepoRoot)
	}

	if cfg.GitHub.APIBaseURL != "https://ghe.example.com/api/v3" {
		t.Fatalf("unexpected API base URL: %q", cfg.GitHub.APIBaseURL)
	}
}

func TestLoadRejectsInvalidGitHubPollInterval(t *testing.T) {
	t.Setenv("GITHUB_POLL_INTERVAL", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for invalid poll interval")
	}

	if !strings.Contains(err.Error(), "GITHUB_POLL_INTERVAL") {
		t.Fatalf("expected error to mention GITHUB_POLL_INTERVAL, got %v", err)
	}
}

func TestLoadRequiresGitHubCredentialsInNonDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("GITHUB_INTEGRATION_ENABLED", "true")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PEM", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error when required GitHub credentials are missing")
	}

	if !strings.Contains(err.Error(), "GITHUB_APP_ID") {
		t.Fatalf("expected missing app id error, got %v", err)
	}
}

func TestLoadAllowsDevModeWithoutGitHubCredentials(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("GITHUB_INTEGRATION_ENABLED", "true")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PEM", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error in development mode, got %v", err)
	}

	if !cfg.GitHub.Enabled {
		t.Fatalf("expected GitHub integration enabled")
	}
}

func TestLoadRejectsInvalidGitHubEnabledFlag(t *testing.T) {
	t.Setenv("GITHUB_INTEGRATION_ENABLED", "definitely")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for invalid GITHUB_INTEGRATION_ENABLED value")
	}

	if !strings.Contains(err.Error(), "GITHUB_INTEGRATION_ENABLED") {
		t.Fatalf("expected error to mention GITHUB_INTEGRATION_ENABLED, got %v", err)
	}
}
