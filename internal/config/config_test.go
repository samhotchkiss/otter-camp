package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func loadWithDefaultOpenAIKey(t *testing.T) (Config, error) {
	t.Helper()
	if _, exists := os.LookupEnv("CONVERSATION_EMBEDDER_OPENAI_API_KEY"); !exists {
		t.Setenv("CONVERSATION_EMBEDDER_OPENAI_API_KEY", "test-openai-key")
	}
	return Load()
}

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
	t.Setenv("CONVERSATION_EMBEDDER_OPENAI_API_KEY", "test-default-openai-key")
	t.Setenv("CONVERSATION_SEGMENTATION_WORKER_ENABLED", "")
	t.Setenv("CONVERSATION_SEGMENTATION_POLL_INTERVAL", "")
	t.Setenv("CONVERSATION_SEGMENTATION_BATCH_SIZE", "")
	t.Setenv("CONVERSATION_SEGMENTATION_GAP_THRESHOLD", "")
	t.Setenv("ELLIE_INGESTION_WORKER_ENABLED", "")
	t.Setenv("ELLIE_INGESTION_INTERVAL", "")
	t.Setenv("ELLIE_INGESTION_BATCH_SIZE", "")
	t.Setenv("ELLIE_INGESTION_MAX_PER_ROOM", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_WORKER_ENABLED", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_POLL_INTERVAL", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_BATCH_SIZE", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_THRESHOLD", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_COOLDOWN_MESSAGES", "")
	t.Setenv("ELLIE_CONTEXT_INJECTION_MAX_ITEMS", "")
	t.Setenv("CONVERSATION_TOKEN_BACKFILL_WORKER_ENABLED", "")
	t.Setenv("CONVERSATION_TOKEN_BACKFILL_POLL_INTERVAL", "")
	t.Setenv("CONVERSATION_TOKEN_BACKFILL_BATCH_SIZE", "")
	t.Setenv("JOB_SCHEDULER_ENABLED", "")
	t.Setenv("JOB_SCHEDULER_POLL_INTERVAL", "")
	t.Setenv("JOB_SCHEDULER_MAX_PER_POLL", "")
	t.Setenv("JOB_SCHEDULER_RUN_TIMEOUT", "")
	t.Setenv("JOB_SCHEDULER_MAX_RUN_HISTORY", "")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
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
	if cfg.ConversationEmbedding.Provider != "openai" {
		t.Fatalf("expected default conversation embedding provider %q, got %q", "openai", cfg.ConversationEmbedding.Provider)
	}
	if cfg.ConversationEmbedding.Model != "text-embedding-3-small" {
		t.Fatalf("expected default conversation embedding model %q, got %q", "text-embedding-3-small", cfg.ConversationEmbedding.Model)
	}
	if cfg.ConversationEmbedding.Dimension != 1536 {
		t.Fatalf("expected default conversation embedding dimension %d, got %d", 1536, cfg.ConversationEmbedding.Dimension)
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

	if !cfg.EllieIngestion.Enabled {
		t.Fatalf("expected ellie ingestion worker enabled by default")
	}
	if cfg.EllieIngestion.Interval != defaultEllieIngestionInterval {
		t.Fatalf("expected default ellie ingestion interval %v, got %v", defaultEllieIngestionInterval, cfg.EllieIngestion.Interval)
	}
	if cfg.EllieIngestion.BatchSize != defaultEllieIngestionBatchSize {
		t.Fatalf("expected default ellie ingestion batch size %d, got %d", defaultEllieIngestionBatchSize, cfg.EllieIngestion.BatchSize)
	}
	if cfg.EllieIngestion.MaxPerRoom != defaultEllieIngestionMaxPerRoom {
		t.Fatalf("expected default ellie ingestion max_per_room %d, got %d", defaultEllieIngestionMaxPerRoom, cfg.EllieIngestion.MaxPerRoom)
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

	if !cfg.ConversationTokenBackfill.Enabled {
		t.Fatalf("expected conversation token backfill worker enabled by default")
	}
	if cfg.ConversationTokenBackfill.PollInterval != defaultConversationTokenBackfillPollInterval {
		t.Fatalf("expected default token backfill interval %v, got %v", defaultConversationTokenBackfillPollInterval, cfg.ConversationTokenBackfill.PollInterval)
	}
	if cfg.ConversationTokenBackfill.BatchSize != defaultConversationTokenBackfillBatchSize {
		t.Fatalf("expected default token backfill batch size %d, got %d", defaultConversationTokenBackfillBatchSize, cfg.ConversationTokenBackfill.BatchSize)
	}

	if !cfg.JobScheduler.Enabled {
		t.Fatalf("expected job scheduler enabled by default")
	}
	if cfg.JobScheduler.PollInterval != defaultJobSchedulerPollInterval {
		t.Fatalf("expected default job scheduler poll interval %v, got %v", defaultJobSchedulerPollInterval, cfg.JobScheduler.PollInterval)
	}
	if cfg.JobScheduler.MaxPerPoll != defaultJobSchedulerMaxPerPoll {
		t.Fatalf("expected default job scheduler max_per_poll %d, got %d", defaultJobSchedulerMaxPerPoll, cfg.JobScheduler.MaxPerPoll)
	}
	if cfg.JobScheduler.RunTimeout != defaultJobSchedulerRunTimeout {
		t.Fatalf("expected default job scheduler run timeout %v, got %v", defaultJobSchedulerRunTimeout, cfg.JobScheduler.RunTimeout)
	}
	if cfg.JobScheduler.MaxRunHistory != defaultJobSchedulerMaxRunHistory {
		t.Fatalf("expected default job scheduler max run history %d, got %d", defaultJobSchedulerMaxRunHistory, cfg.JobScheduler.MaxRunHistory)
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

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
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

	_, err := loadWithDefaultOpenAIKey(t)
	if err == nil {
		t.Fatalf("expected error for invalid poll interval")
	}

	if !strings.Contains(err.Error(), "GITHUB_POLL_INTERVAL") {
		t.Fatalf("expected error to mention GITHUB_POLL_INTERVAL, got %v", err)
	}
}

func TestLoadDefaultsJobScheduler(t *testing.T) {
	t.Setenv("JOB_SCHEDULER_ENABLED", "")
	t.Setenv("JOB_SCHEDULER_POLL_INTERVAL", "")
	t.Setenv("JOB_SCHEDULER_MAX_PER_POLL", "")
	t.Setenv("JOB_SCHEDULER_RUN_TIMEOUT", "")
	t.Setenv("JOB_SCHEDULER_MAX_RUN_HISTORY", "")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
	}

	if !cfg.JobScheduler.Enabled {
		t.Fatalf("expected job scheduler enabled by default")
	}
	if cfg.JobScheduler.PollInterval != defaultJobSchedulerPollInterval {
		t.Fatalf("expected poll interval %v, got %v", defaultJobSchedulerPollInterval, cfg.JobScheduler.PollInterval)
	}
	if cfg.JobScheduler.MaxPerPoll != defaultJobSchedulerMaxPerPoll {
		t.Fatalf("expected max per poll %d, got %d", defaultJobSchedulerMaxPerPoll, cfg.JobScheduler.MaxPerPoll)
	}
	if cfg.JobScheduler.RunTimeout != defaultJobSchedulerRunTimeout {
		t.Fatalf("expected run timeout %v, got %v", defaultJobSchedulerRunTimeout, cfg.JobScheduler.RunTimeout)
	}
	if cfg.JobScheduler.MaxRunHistory != defaultJobSchedulerMaxRunHistory {
		t.Fatalf("expected max run history %d, got %d", defaultJobSchedulerMaxRunHistory, cfg.JobScheduler.MaxRunHistory)
	}
}

func TestLoadParsesJobSchedulerSettings(t *testing.T) {
	t.Setenv("JOB_SCHEDULER_ENABLED", "true")
	t.Setenv("JOB_SCHEDULER_POLL_INTERVAL", "7s")
	t.Setenv("JOB_SCHEDULER_MAX_PER_POLL", "25")
	t.Setenv("JOB_SCHEDULER_RUN_TIMEOUT", "4m")
	t.Setenv("JOB_SCHEDULER_MAX_RUN_HISTORY", "150")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
	}

	if !cfg.JobScheduler.Enabled {
		t.Fatalf("expected job scheduler enabled")
	}
	if cfg.JobScheduler.PollInterval != 7*time.Second {
		t.Fatalf("expected poll interval 7s, got %v", cfg.JobScheduler.PollInterval)
	}
	if cfg.JobScheduler.MaxPerPoll != 25 {
		t.Fatalf("expected max per poll 25, got %d", cfg.JobScheduler.MaxPerPoll)
	}
	if cfg.JobScheduler.RunTimeout != 4*time.Minute {
		t.Fatalf("expected run timeout 4m, got %v", cfg.JobScheduler.RunTimeout)
	}
	if cfg.JobScheduler.MaxRunHistory != 150 {
		t.Fatalf("expected max run history 150, got %d", cfg.JobScheduler.MaxRunHistory)
	}
}

func TestLoadParsesWorkerOrgID(t *testing.T) {
	t.Setenv("OTTER_ORG_ID", "11111111-2222-3333-4444-555555555555")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
	}

	if cfg.OrgID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("expected org id to be parsed from OTTER_ORG_ID, got %q", cfg.OrgID)
	}
}

func TestLoadRejectsInvalidJobSchedulerPollInterval(t *testing.T) {
	t.Setenv("JOB_SCHEDULER_POLL_INTERVAL", "not-a-duration")

	_, err := loadWithDefaultOpenAIKey(t)
	if err == nil {
		t.Fatalf("expected error for invalid JOB_SCHEDULER_POLL_INTERVAL")
	}
	if !strings.Contains(err.Error(), "JOB_SCHEDULER_POLL_INTERVAL") {
		t.Fatalf("expected error to mention JOB_SCHEDULER_POLL_INTERVAL, got %v", err)
	}
}

func TestLoadRejectsInvalidJobSchedulerMaxPerPoll(t *testing.T) {
	t.Setenv("JOB_SCHEDULER_ENABLED", "true")
	t.Setenv("JOB_SCHEDULER_MAX_PER_POLL", "0")

	_, err := loadWithDefaultOpenAIKey(t)
	if err == nil {
		t.Fatalf("expected error for non-positive JOB_SCHEDULER_MAX_PER_POLL")
	}
	if !strings.Contains(err.Error(), "JOB_SCHEDULER_MAX_PER_POLL") {
		t.Fatalf("expected error to mention JOB_SCHEDULER_MAX_PER_POLL, got %v", err)
	}
}

func TestLoadRequiresGitHubCredentialsInNonDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("GITHUB_INTEGRATION_ENABLED", "true")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PEM", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	_, err := loadWithDefaultOpenAIKey(t)
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

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("expected no error in development mode, got %v", err)
	}

	if !cfg.GitHub.Enabled {
		t.Fatalf("expected GitHub integration enabled")
	}
}

func TestLoadRejectsInvalidGitHubEnabledFlag(t *testing.T) {
	t.Setenv("GITHUB_INTEGRATION_ENABLED", "definitely")

	_, err := loadWithDefaultOpenAIKey(t)
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

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
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

func TestLoadUsesReducedDefaultConversationEmbeddingBatchSize(t *testing.T) {
	t.Setenv("CONVERSATION_EMBEDDING_BATCH_SIZE", "")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
	}

	if cfg.ConversationEmbedding.BatchSize != 20 {
		t.Fatalf("expected reduced default conversation embedding batch size 20, got %d", cfg.ConversationEmbedding.BatchSize)
	}
}

func TestLoadConversationTokenBackfill(t *testing.T) {
	t.Setenv("CONVERSATION_TOKEN_BACKFILL_WORKER_ENABLED", "true")
	t.Setenv("CONVERSATION_TOKEN_BACKFILL_POLL_INTERVAL", "11s")
	t.Setenv("CONVERSATION_TOKEN_BACKFILL_BATCH_SIZE", "321")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
	}

	if !cfg.ConversationTokenBackfill.Enabled {
		t.Fatalf("expected conversation token backfill worker enabled")
	}
	if cfg.ConversationTokenBackfill.PollInterval != 11*time.Second {
		t.Fatalf("expected token backfill poll interval 11s, got %v", cfg.ConversationTokenBackfill.PollInterval)
	}
	if cfg.ConversationTokenBackfill.BatchSize != 321 {
		t.Fatalf("expected token backfill batch size 321, got %d", cfg.ConversationTokenBackfill.BatchSize)
	}
}

func TestLoadRejectsInvalidConversationEmbeddingBatchSize(t *testing.T) {
	t.Setenv("CONVERSATION_EMBEDDING_BATCH_SIZE", "abc")

	_, err := loadWithDefaultOpenAIKey(t)
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

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
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

	_, err := loadWithDefaultOpenAIKey(t)
	if err == nil {
		t.Fatalf("expected error for invalid CONVERSATION_SEGMENTATION_BATCH_SIZE")
	}

	if !strings.Contains(err.Error(), "CONVERSATION_SEGMENTATION_BATCH_SIZE") {
		t.Fatalf("expected error to mention CONVERSATION_SEGMENTATION_BATCH_SIZE, got %v", err)
	}
}

func TestLoadParsesEllieIngestionSettings(t *testing.T) {
	t.Setenv("ELLIE_INGESTION_WORKER_ENABLED", "true")
	t.Setenv("ELLIE_INGESTION_INTERVAL", "2m")
	t.Setenv("ELLIE_INGESTION_BATCH_SIZE", "80")
	t.Setenv("ELLIE_INGESTION_MAX_PER_ROOM", "120")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
	}

	if !cfg.EllieIngestion.Enabled {
		t.Fatalf("expected ellie ingestion worker enabled")
	}
	if cfg.EllieIngestion.Interval != 2*time.Minute {
		t.Fatalf("expected ellie ingestion interval 2m, got %v", cfg.EllieIngestion.Interval)
	}
	if cfg.EllieIngestion.BatchSize != 80 {
		t.Fatalf("expected ellie ingestion batch size 80, got %d", cfg.EllieIngestion.BatchSize)
	}
	if cfg.EllieIngestion.MaxPerRoom != 120 {
		t.Fatalf("expected ellie ingestion max per room 120, got %d", cfg.EllieIngestion.MaxPerRoom)
	}
}

func TestLoadRejectsInvalidEllieIngestionBatchSize(t *testing.T) {
	t.Setenv("ELLIE_INGESTION_BATCH_SIZE", "bad")

	_, err := loadWithDefaultOpenAIKey(t)
	if err == nil {
		t.Fatalf("expected error for invalid ELLIE_INGESTION_BATCH_SIZE")
	}

	if !strings.Contains(err.Error(), "ELLIE_INGESTION_BATCH_SIZE") {
		t.Fatalf("expected error to mention ELLIE_INGESTION_BATCH_SIZE, got %v", err)
	}
}

func TestLoadIncludesEllieContextInjectionDefaults(t *testing.T) {
	t.Setenv("ELLIE_CONTEXT_INJECTION_WORKER_ENABLED", "true")
	t.Setenv("ELLIE_CONTEXT_INJECTION_POLL_INTERVAL", "3s")
	t.Setenv("ELLIE_CONTEXT_INJECTION_BATCH_SIZE", "40")
	t.Setenv("ELLIE_CONTEXT_INJECTION_THRESHOLD", "0.72")
	t.Setenv("ELLIE_CONTEXT_INJECTION_COOLDOWN_MESSAGES", "7")
	t.Setenv("ELLIE_CONTEXT_INJECTION_MAX_ITEMS", "4")

	cfg, err := loadWithDefaultOpenAIKey(t)
	if err != nil {
		t.Fatalf("loadWithDefaultOpenAIKey(t) returned error: %v", err)
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

func TestLoadRejectsNaNContextInjectionThreshold(t *testing.T) {
	t.Setenv("ELLIE_CONTEXT_INJECTION_THRESHOLD", "NaN")

	_, err := loadWithDefaultOpenAIKey(t)
	if err == nil {
		t.Fatalf("expected error for NaN ELLIE_CONTEXT_INJECTION_THRESHOLD")
	}
	if !strings.Contains(err.Error(), "ELLIE_CONTEXT_INJECTION_THRESHOLD") {
		t.Fatalf("expected error to mention ELLIE_CONTEXT_INJECTION_THRESHOLD, got %v", err)
	}
}

func TestLoadRejectsInvalidEmbedderConfigWhenInjectionEnabled(t *testing.T) {
	t.Setenv("CONVERSATION_EMBEDDING_WORKER_ENABLED", "false")
	t.Setenv("ELLIE_CONTEXT_INJECTION_WORKER_ENABLED", "true")
	t.Setenv("CONVERSATION_EMBEDDER_DIMENSION", "0")

	_, err := loadWithDefaultOpenAIKey(t)
	if err == nil {
		t.Fatalf("expected error when context injection is enabled with invalid embedder config")
	}
	if !strings.Contains(err.Error(), "CONVERSATION_EMBEDDER_DIMENSION") {
		t.Fatalf("expected error to mention CONVERSATION_EMBEDDER_DIMENSION, got %v", err)
	}
}

func TestLoadRejectsMissingOpenAIAPIKeyWhenOpenAIProviderEnabled(t *testing.T) {
	t.Setenv("CONVERSATION_EMBEDDING_WORKER_ENABLED", "true")
	t.Setenv("ELLIE_CONTEXT_INJECTION_WORKER_ENABLED", "false")
	t.Setenv("CONVERSATION_EMBEDDER_PROVIDER", "openai")
	t.Setenv("CONVERSATION_EMBEDDER_OPENAI_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error when openai provider is enabled without API key")
	}
	if !strings.Contains(err.Error(), "CONVERSATION_EMBEDDER_OPENAI_API_KEY") {
		t.Fatalf("expected error to mention CONVERSATION_EMBEDDER_OPENAI_API_KEY, got %v", err)
	}
}

func TestLoadAllowsMissingOpenAIAPIKeyWhenProviderIsNotOpenAI(t *testing.T) {
	t.Setenv("CONVERSATION_EMBEDDING_WORKER_ENABLED", "true")
	t.Setenv("ELLIE_CONTEXT_INJECTION_WORKER_ENABLED", "false")
	t.Setenv("CONVERSATION_EMBEDDER_PROVIDER", "ollama")
	t.Setenv("CONVERSATION_EMBEDDER_OPENAI_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected ollama provider to load without openai api key, got %v", err)
	}
	if cfg.ConversationEmbedding.Provider != "ollama" {
		t.Fatalf("expected provider ollama, got %q", cfg.ConversationEmbedding.Provider)
	}
}
