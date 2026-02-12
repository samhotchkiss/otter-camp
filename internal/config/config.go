package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func init() {
	// Auto-load .env file if present (don't override existing env vars)
	loadDotEnv(".env")
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Remove surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		// Don't override existing env vars
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

const (
	defaultPort               = "4200"
	defaultEnvironment        = "development"
	defaultGitHubRepoRoot     = "./data/repos"
	defaultGitHubPollInterval = time.Hour
	defaultGitHubAPIBaseURL   = "https://api.github.com"

	defaultConversationEmbeddingEnabled      = true
	defaultConversationEmbeddingPollInterval = 5 * time.Second
	defaultConversationEmbeddingBatchSize    = 50
	defaultConversationEmbeddingProvider     = "ollama"
	defaultConversationEmbeddingModel        = "nomic-embed-text"
	defaultConversationEmbeddingDimension    = 384
	defaultConversationEmbeddingOllamaURL    = "http://localhost:11434"
	defaultConversationEmbeddingOpenAIBase   = "https://api.openai.com"
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
	Port                  string
	DatabaseURL           string
	Environment           string
	GitHub                GitHubConfig
	ConversationEmbedding ConversationEmbeddingConfig
}

type ConversationEmbeddingConfig struct {
	Enabled       bool
	PollInterval  time.Duration
	BatchSize     int
	Provider      string
	Model         string
	Dimension     int
	OllamaURL     string
	OpenAIBaseURL string
	OpenAIAPIKey  string
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
		ConversationEmbedding: ConversationEmbeddingConfig{
			Provider: firstNonEmpty(
				strings.TrimSpace(os.Getenv("CONVERSATION_EMBEDDER_PROVIDER")),
				defaultConversationEmbeddingProvider,
			),
			Model: firstNonEmpty(
				strings.TrimSpace(os.Getenv("CONVERSATION_EMBEDDER_MODEL")),
				defaultConversationEmbeddingModel,
			),
			OllamaURL: firstNonEmpty(
				strings.TrimSpace(os.Getenv("CONVERSATION_EMBEDDER_OLLAMA_URL")),
				defaultConversationEmbeddingOllamaURL,
			),
			OpenAIBaseURL: firstNonEmpty(
				strings.TrimSpace(os.Getenv("CONVERSATION_EMBEDDER_OPENAI_BASE_URL")),
				defaultConversationEmbeddingOpenAIBase,
			),
			OpenAIAPIKey: strings.TrimSpace(os.Getenv("CONVERSATION_EMBEDDER_OPENAI_API_KEY")),
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

	conversationEmbeddingEnabled, err := parseBool("CONVERSATION_EMBEDDING_WORKER_ENABLED", defaultConversationEmbeddingEnabled)
	if err != nil {
		return Config{}, err
	}
	cfg.ConversationEmbedding.Enabled = conversationEmbeddingEnabled

	conversationPollInterval, err := parseDuration("CONVERSATION_EMBEDDING_POLL_INTERVAL", defaultConversationEmbeddingPollInterval)
	if err != nil {
		return Config{}, err
	}
	cfg.ConversationEmbedding.PollInterval = conversationPollInterval

	conversationBatchSize, err := parseInt("CONVERSATION_EMBEDDING_BATCH_SIZE", defaultConversationEmbeddingBatchSize)
	if err != nil {
		return Config{}, err
	}
	cfg.ConversationEmbedding.BatchSize = conversationBatchSize

	conversationDimension, err := parseInt("CONVERSATION_EMBEDDER_DIMENSION", defaultConversationEmbeddingDimension)
	if err != nil {
		return Config{}, err
	}
	cfg.ConversationEmbedding.Dimension = conversationDimension

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.ConversationEmbedding.Enabled {
		if c.ConversationEmbedding.PollInterval <= 0 {
			return fmt.Errorf("CONVERSATION_EMBEDDING_POLL_INTERVAL must be greater than zero")
		}
		if c.ConversationEmbedding.BatchSize <= 0 {
			return fmt.Errorf("CONVERSATION_EMBEDDING_BATCH_SIZE must be greater than zero")
		}
		if c.ConversationEmbedding.Provider == "" {
			return fmt.Errorf("CONVERSATION_EMBEDDER_PROVIDER must not be empty when worker is enabled")
		}
		if c.ConversationEmbedding.Model == "" {
			return fmt.Errorf("CONVERSATION_EMBEDDER_MODEL must not be empty when worker is enabled")
		}
		if c.ConversationEmbedding.Dimension <= 0 {
			return fmt.Errorf("CONVERSATION_EMBEDDER_DIMENSION must be greater than zero when worker is enabled")
		}
	}

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

func parseInt(name string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid integer: %w", name, err)
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
