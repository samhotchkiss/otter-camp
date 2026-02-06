package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultPort               = "8080"
	defaultEnvironment        = "development"
	defaultGitHubRepoRoot     = "./data/repos"
	defaultGitHubPollInterval = time.Hour
	defaultGitHubAPIBaseURL   = "https://api.github.com"
)

type GitHubConfig struct {
	Enabled           bool
	AppID             string
	AppPrivateKeyPEM  string
	AppPrivateKeyPath string
	WebhookSecret     string
	RepoRoot          string
	PollInterval      time.Duration
	APIBaseURL        string
}

type Config struct {
	Port        string
	DatabaseURL string
	Environment string
	GitHub      GitHubConfig
}

func Load() (Config, error) {
	cfg := Config{
		Port:        firstNonEmpty(strings.TrimSpace(os.Getenv("PORT")), defaultPort),
		DatabaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
		Environment: resolveEnvironment(),
		GitHub: GitHubConfig{
			AppID:             strings.TrimSpace(os.Getenv("GITHUB_APP_ID")),
			AppPrivateKeyPEM:  strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PEM")),
			AppPrivateKeyPath: strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH")),
			WebhookSecret:     strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET")),
			RepoRoot: firstNonEmpty(
				strings.TrimSpace(os.Getenv("GITHUB_REPO_ROOT")),
				defaultGitHubRepoRoot,
			),
			APIBaseURL: firstNonEmpty(
				strings.TrimSpace(os.Getenv("GITHUB_API_BASE_URL")),
				defaultGitHubAPIBaseURL,
			),
		},
	}

	githubEnabled, err := parseBool("GITHUB_INTEGRATION_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	cfg.GitHub.Enabled = githubEnabled

	pollInterval, err := parseDuration("GITHUB_POLL_INTERVAL", defaultGitHubPollInterval)
	if err != nil {
		return Config{}, err
	}
	cfg.GitHub.PollInterval = pollInterval

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if !c.GitHub.Enabled {
		return nil
	}

	if c.GitHub.PollInterval <= 0 {
		return fmt.Errorf("GITHUB_POLL_INTERVAL must be greater than zero")
	}

	if c.GitHub.RepoRoot == "" {
		return fmt.Errorf("GITHUB_REPO_ROOT must not be empty when GitHub integration is enabled")
	}

	if c.GitHub.APIBaseURL == "" {
		return fmt.Errorf("GITHUB_API_BASE_URL must not be empty when GitHub integration is enabled")
	}

	if !isNonDevelopment(c.Environment) {
		return nil
	}

	if c.GitHub.AppID == "" {
		return fmt.Errorf("GITHUB_APP_ID is required when GitHub integration is enabled in non-development environments")
	}

	if c.GitHub.AppPrivateKeyPEM == "" && c.GitHub.AppPrivateKeyPath == "" {
		return fmt.Errorf("GITHUB_APP_PRIVATE_KEY_PEM or GITHUB_APP_PRIVATE_KEY_PATH is required when GitHub integration is enabled in non-development environments")
	}

	if c.GitHub.WebhookSecret == "" {
		return fmt.Errorf("GITHUB_WEBHOOK_SECRET is required when GitHub integration is enabled in non-development environments")
	}

	return nil
}

func resolveEnvironment() string {
	return strings.ToLower(firstNonEmpty(
		strings.TrimSpace(os.Getenv("APP_ENV")),
		strings.TrimSpace(os.Getenv("ENVIRONMENT")),
		strings.TrimSpace(os.Getenv("GO_ENV")),
		strings.TrimSpace(os.Getenv("RAILWAY_ENVIRONMENT")),
		defaultEnvironment,
	))
}

func isNonDevelopment(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", "dev", "development", "local", "test":
		return false
	default:
		return true
	}
}

func parseBool(name string, defaultValue bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}

	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean value", name)
	}
}

func parseDuration(name string, defaultValue time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", name, err)
	}

	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}

	return parsed, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
