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
	t.Setenv("CONVERSATION_EMBEDDING_WORKER_ENABLED", "")
	t.Setenv("CONVERSATION_EMBEDDING_POLL_INTERVAL", "")
	t.Setenv("CONVERSATION_EMBEDDING_BATCH_SIZE", "")
	t.Setenv("CONVERSATION_EMBEDDER_PROVIDER", "")
	t.Setenv("CONVERSATION_EMBEDDER_MODEL", "")
	t.Setenv("CONVERSATION_EMBEDDER_DIMENSION", "")
	t.Setenv("CONVERSATION_EMBEDDER_OLLAMA_URL", "")
	t.Setenv("CONVERSATION_EMBEDDER_OPENAI_BASE_URL", "")
	t.Setenv("CONVERSATION_EMBEDDER_OPENAI_API_KEY", "")
	t.Setenv("CONVERSATION_SEGMENTATION_WORKER_ENABLED", "")
	t.Setenv("CONVERSATION_SEGMENTATION_POLL_INTERVAL", "")
	t.Setenv("CONVERSATION_SEGMENTATION_BATCH_SIZE", "")
	t.Setenv("CONVERSATION_SEGMENTATION_GAP_THRESHOLD", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_WORKER_ENABLED", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_POLL_INTERVAL", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_BATCH_SIZE", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_THRESHOLD", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_COOLDOWN_MESSAGES", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_MAX_ITEMS", "")

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

	if !cfg.ConversationEmbedding.Enabled {
		t.Fatalf("expected conversation embedding worker enabled by default")
	}
	if cfg.ConversationEmbedding.PollInterval != defaultConversationEmbeddingPollInterval {
		t.Fatalf("expected default conversation embedding poll interval %v, got %v", defaultConversationEmbeddingPollInterval, cfg.ConversationEmbedding.PollInterval)
	}
	if cfg.ConversationEmbedding.BatchSize != defaultConversationEmbeddingBatchSize {
		t.Fatalf("expected default conversation embedding batch size %d, got %d", defaultConversationEmbeddingBatchSize, cfg.ConversationEmbedding.BatchSize)
	}
	if cfg.ConversationEmbedding.Provider != defaultConversationEmbeddingProvider {
		t.Fatalf("expected default conversation embedding provider %q, got %q", defaultConversationEmbeddingProvider, cfg.ConversationEmbedding.Provider)
	}
	if cfg.ConversationEmbedding.Model != defaultConversationEmbeddingModel {
		t.Fatalf("expected default conversation embedding model %q, got %q", defaultConversationEmbeddingModel, cfg.ConversationEmbedding.Model)
	}
	if cfg.ConversationEmbedding.Dimension != defaultConversationEmbeddingDimension {
		t.Fatalf("expected default conversation embedding dimension %d, got %d", defaultConversationEmbeddingDimension, cfg.ConversationEmbedding.Dimension)
	}

	if !cfg.ConversationSegmentation.Enabled {
		t.Fatalf("expected conversation segmentation worker enabled by default")
	}
	if cfg.ConversationSegmentation.PollInterval != defaultConversationSegmentationPollInterval {
		t.Fatalf("expected default conversation segmentation poll interval %v, got %v", defaultConversationSegmentationPollInterval, cfg.ConversationSegmentation.PollInterval)
	}
	if cfg.ConversationSegmentation.BatchSize != defaultConversationSegmentationBatchSize {
		t.Fatalf("expected default conversation segmentation batch size %d, got %d", defaultConversationSegmentationBatchSize, cfg.ConversationSegmentation.BatchSize)
	}
	if cfg.ConversationSegmentation.GapThreshold != defaultConversationSegmentationGapThreshold {
		t.Fatalf("expected default conversation segmentation gap threshold %v, got %v", defaultConversationSegmentationGapThreshold, cfg.ConversationSegmentation.GapThreshold)
	}

	if !cfg.EllieContextInjection.Enabled {
		t.Fatalf("expected ellie context injection worker enabled by default")
	}
	if cfg.EllieContextInjection.PollInterval != defaultEllieContextInjectionPollInterval {
		t.Fatalf("expected default ellie context injection interval %v, got %v", defaultEllieContextInjectionPollInterval, cfg.EllieContextInjection.PollInterval)
	}
	if cfg.EllieContextInjection.BatchSize != defaultEllieContextInjectionBatchSize {
		t.Fatalf("expected default ellie context injection batch size %d, got %d", defaultEllieContextInjectionBatchSize, cfg.EllieContextInjection.BatchSize)
	}
	if cfg.EllieContextInjection.Threshold != defaultEllieContextInjectionThreshold {
		t.Fatalf("expected default ellie context injection threshold %.2f, got %.2f", defaultEllieContextInjectionThreshold, cfg.EllieContextInjection.Threshold)
	}
	if cfg.EllieContextInjection.CooldownMessages != defaultEllieContextInjectionCooldownMessages {
		t.Fatalf("expected default ellie context injection cooldown %d, got %d", defaultEllieContextInjectionCooldownMessages, cfg.EllieContextInjection.CooldownMessages)
	}
	if cfg.EllieContextInjection.MaxItems != defaultEllieContextInjectionMaxItems {
		t.Fatalf("expected default ellie context injection max items %d, got %d", defaultEllieContextInjectionMaxItems, cfg.EllieContextInjection.MaxItems)
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

func TestLoadParsesConversationEmbeddingSettings(t *testing.T) {
	t.Setenv("CONVERSATION_EMBEDDING_WORKER_ENABLED", "true")
	t.Setenv("CONVERSATION_EMBEDDING_POLL_INTERVAL", "7s")
	t.Setenv("CONVERSATION_EMBEDDING_BATCH_SIZE", "12")
	t.Setenv("CONVERSATION_EMBEDDER_PROVIDER", "openai")
	t.Setenv("CONVERSATION_EMBEDDER_MODEL", "text-embedding-3-small")
	t.Setenv("CONVERSATION_EMBEDDER_DIMENSION", "1536")
	t.Setenv("CONVERSATION_EMBEDDER_OLLAMA_URL", "http://localhost:11435")
	t.Setenv("CONVERSATION_EMBEDDER_OPENAI_BASE_URL", "https://api.openai.com")
	t.Setenv("CONVERSATION_EMBEDDER_OPENAI_API_KEY", "test-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !cfg.ConversationEmbedding.Enabled {
		t.Fatalf("expected conversation embedding worker enabled")
	}
	if cfg.ConversationEmbedding.PollInterval != 7*time.Second {
		t.Fatalf("expected conversation embedding poll interval 7s, got %v", cfg.ConversationEmbedding.PollInterval)
	}
	if cfg.ConversationEmbedding.BatchSize != 12 {
		t.Fatalf("expected conversation embedding batch size 12, got %d", cfg.ConversationEmbedding.BatchSize)
	}
	if cfg.ConversationEmbedding.Provider != "openai" {
		t.Fatalf("expected conversation embedding provider openai, got %q", cfg.ConversationEmbedding.Provider)
	}
	if cfg.ConversationEmbedding.Model != "text-embedding-3-small" {
		t.Fatalf("expected conversation embedding model text-embedding-3-small, got %q", cfg.ConversationEmbedding.Model)
	}
	if cfg.ConversationEmbedding.Dimension != 1536 {
		t.Fatalf("expected conversation embedding dimension 1536, got %d", cfg.ConversationEmbedding.Dimension)
	}
}

func TestLoadRejectsInvalidConversationEmbeddingBatchSize(t *testing.T) {
	t.Setenv("CONVERSATION_EMBEDDING_BATCH_SIZE", "abc")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for invalid CONVERSATION_EMBEDDING_BATCH_SIZE")
	}

	if !strings.Contains(err.Error(), "CONVERSATION_EMBEDDING_BATCH_SIZE") {
		t.Fatalf("expected error to mention CONVERSATION_EMBEDDING_BATCH_SIZE, got %v", err)
	}
}

func TestLoadParsesConversationSegmentationSettings(t *testing.T) {
	t.Setenv("CONVERSATION_SEGMENTATION_WORKER_ENABLED", "true")
	t.Setenv("CONVERSATION_SEGMENTATION_POLL_INTERVAL", "9s")
	t.Setenv("CONVERSATION_SEGMENTATION_BATCH_SIZE", "64")
	t.Setenv("CONVERSATION_SEGMENTATION_GAP_THRESHOLD", "45m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !cfg.ConversationSegmentation.Enabled {
		t.Fatalf("expected conversation segmentation worker enabled")
	}
	if cfg.ConversationSegmentation.PollInterval != 9*time.Second {
		t.Fatalf("expected conversation segmentation poll interval 9s, got %v", cfg.ConversationSegmentation.PollInterval)
	}
	if cfg.ConversationSegmentation.BatchSize != 64 {
		t.Fatalf("expected conversation segmentation batch size 64, got %d", cfg.ConversationSegmentation.BatchSize)
	}
	if cfg.ConversationSegmentation.GapThreshold != 45*time.Minute {
		t.Fatalf("expected conversation segmentation gap threshold 45m, got %v", cfg.ConversationSegmentation.GapThreshold)
	}
}

func TestLoadRejectsInvalidConversationSegmentationBatchSize(t *testing.T) {
	t.Setenv("CONVERSATION_SEGMENTATION_BATCH_SIZE", "bad")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for invalid CONVERSATION_SEGMENTATION_BATCH_SIZE")
	}

	if !strings.Contains(err.Error(), "CONVERSATION_SEGMENTATION_BATCH_SIZE") {
		t.Fatalf("expected error to mention CONVERSATION_SEGMENTATION_BATCH_SIZE, got %v", err)
	}
}

func TestLoadIncludesEllieContextInjectionDefaults(t *testing.T) {
	t.Setenv("ELLIE_CONTEXT_INJECTION_WORKER_ENABLED", "true")
	t.Setenv("ELLIE_CONTEXT_INJECTION_POLL_INTERVAL", "3s")
	t.Setenv("ELLIE_CONTEXT_INJECTION_BATCH_SIZE", "40")
	t.Setenv("ELLIE_CONTEXT_INJECTION_THRESHOLD", "0.72")
	t.Setenv("ELLIE_CONTEXT_INJECTION_COOLDOWN_MESSAGES", "7")
	t.Setenv("ELLIE_CONTEXT_INJECTION_MAX_ITEMS", "4")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !cfg.EllieContextInjection.Enabled {
		t.Fatalf("expected ellie context injection worker enabled")
	}
	if cfg.EllieContextInjection.PollInterval != 3*time.Second {
		t.Fatalf("expected ellie context injection interval 3s, got %v", cfg.EllieContextInjection.PollInterval)
	}
	if cfg.EllieContextInjection.BatchSize != 40 {
		t.Fatalf("expected ellie context injection batch size 40, got %d", cfg.EllieContextInjection.BatchSize)
	}
	if cfg.EllieContextInjection.Threshold != 0.72 {
		t.Fatalf("expected ellie context injection threshold 0.72, got %.2f", cfg.EllieContextInjection.Threshold)
	}
	if cfg.EllieContextInjection.CooldownMessages != 7 {
		t.Fatalf("expected ellie context injection cooldown 7, got %d", cfg.EllieContextInjection.CooldownMessages)
	}
	if cfg.EllieContextInjection.MaxItems != 4 {
		t.Fatalf("expected ellie context injection max items 4, got %d", cfg.EllieContextInjection.MaxItems)
	}
}
