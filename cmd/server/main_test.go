package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/api"
)

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	router := api.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected content-type application/json, got %q", ct)
	}
}

func TestHealthEndpointFields(t *testing.T) {
	t.Parallel()

	router := api.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	for _, field := range []string{"status", "uptime", "version", "timestamp"} {
		if payload[field] == "" {
			t.Fatalf("expected %s to be set, got empty", field)
		}
	}

	if _, err := time.Parse(time.RFC3339, payload["timestamp"]); err != nil {
		t.Fatalf("expected timestamp to be RFC3339, got %q", payload["timestamp"])
	}
}

func TestRootEndpoint(t *testing.T) {
	t.Parallel()

	router := api.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if payload["name"] == "" || payload["tagline"] == "" {
		t.Fatalf("expected name and tagline to be set, got %#v", payload)
	}
	if payload["docs"] == "" || payload["health"] == "" {
		t.Fatalf("expected docs and health to be set, got %#v", payload)
	}
}

func TestLocalRuntimeDefaults(t *testing.T) {
	t.Parallel()

	composeBytes, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatalf("failed to read docker-compose.yml: %v", err)
	}
	compose := string(composeBytes)
	for _, snippet := range []string{
		"image: pgvector/pgvector:pg16",
		"\"4200:4200\"",
		"PORT: 4200",
		"VITE_API_URL: \"\"",
	} {
		if !strings.Contains(compose, snippet) {
			t.Fatalf("expected docker-compose.yml to contain %q", snippet)
		}
	}

	envBytes, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("failed to read .env.example: %v", err)
	}
	env := string(envBytes)
	if !strings.Contains(env, "PORT=4200") {
		t.Fatalf("expected .env.example to set PORT=4200")
	}

	setupBytes, err := os.ReadFile("../../scripts/setup.sh")
	if err != nil {
		t.Fatalf("failed to read scripts/setup.sh: %v", err)
	}
	setup := string(setupBytes)
	for _, snippet := range []string{
		"PORT=4200",
		"OTTERCAMP_URL=http://localhost:${PORT:-4200}",
		"Dashboard:         http://localhost:${PORT:-4200}",
	} {
		if !strings.Contains(setup, snippet) {
			t.Fatalf("expected scripts/setup.sh to contain %q", snippet)
		}
	}

	viteBytes, err := os.ReadFile("../../web/vite.config.ts")
	if err != nil {
		t.Fatalf("failed to read web/vite.config.ts: %v", err)
	}
	vite := string(viteBytes)
	for _, snippet := range []string{
		`target: "http://localhost:4200"`,
		`target: "ws://localhost:4200"`,
	} {
		if !strings.Contains(vite, snippet) {
			t.Fatalf("expected web/vite.config.ts to contain %q", snippet)
		}
	}

	e2eBytes, err := os.ReadFile("../../web/e2e/app.spec.ts")
	if err != nil {
		t.Fatalf("failed to read web/e2e/app.spec.ts: %v", err)
	}
	e2eSpec := string(e2eBytes)
	for _, snippet := range []string{
		"resolveApiHealthUrl",
		"request.get(resolveApiHealthUrl())",
	} {
		if !strings.Contains(e2eSpec, snippet) {
			t.Fatalf("expected web/e2e/app.spec.ts to contain %q", snippet)
		}
	}

	e2eApiURLBytes, err := os.ReadFile("../../web/e2e/api-base-url.ts")
	if err != nil {
		t.Fatalf("failed to read web/e2e/api-base-url.ts: %v", err)
	}
	e2eApiURL := string(e2eApiURLBytes)
	for _, snippet := range []string{
		"http://127.0.0.1:${port}",
		"'4200'",
		"E2E_API_BASE_URL",
		"E2E_API_PORT",
	} {
		if !strings.Contains(e2eApiURL, snippet) {
			t.Fatalf("expected web/e2e/api-base-url.ts to contain %q", snippet)
		}
	}

	bridgeEnvBytes, err := os.ReadFile("../../bridge/.env.example")
	if err != nil {
		t.Fatalf("failed to read bridge/.env.example: %v", err)
	}
	if !strings.Contains(string(bridgeEnvBytes), "OTTERCAMP_URL=http://localhost:4200") {
		t.Fatalf("expected bridge/.env.example to set OTTERCAMP_URL to localhost:4200")
	}

	docsBytes, err := os.ReadFile("../../docs/START-HERE.md")
	if err != nil {
		t.Fatalf("failed to read docs/START-HERE.md: %v", err)
	}
	docs := string(docsBytes)
	for _, snippet := range []string{
		"http://localhost:4200",
		"Hosted + Bridge Mode (`{site}.otter.camp`)",
	} {
		if !strings.Contains(docs, snippet) {
			t.Fatalf("expected docs/START-HERE.md to contain %q", snippet)
		}
	}

	ciBytes, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("failed to read .github/workflows/ci.yml: %v", err)
	}
	ci := string(ciBytes)
	for _, snippet := range []string{
		"PORT: '4200'",
		"VITE_API_URL: http://localhost:4200",
		"curl -s http://localhost:4200/health",
		"CONVERSATION_EMBEDDING_WORKER_ENABLED: 'false'",
		"ELLIE_CONTEXT_INJECTION_WORKER_ENABLED: 'false'",
	} {
		if !strings.Contains(ci, snippet) {
			t.Fatalf("expected .github/workflows/ci.yml to contain %q", snippet)
		}
	}

	dockerfileBytes, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("failed to read Dockerfile: %v", err)
	}
	dockerfile := string(dockerfileBytes)
	for _, snippet := range []string{
		"docker run -p 4200:4200 --env-file .env otter-camp",
		"EXPOSE 4200",
		"http://localhost:4200/health",
	} {
		if !strings.Contains(dockerfile, snippet) {
			t.Fatalf("expected Dockerfile to contain %q", snippet)
		}
	}
}

func TestMainStartsConversationEmbeddingWorkerWhenConfigured(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	for _, snippet := range []string{
		"cfg.ConversationEmbedding.Enabled",
		"memory.NewConversationEmbeddingWorker",
		"Conversation embedding worker started",
		"store.NewConversationEmbeddingStore",
	} {
		if !strings.Contains(mainContent, snippet) {
			t.Fatalf("expected cmd/server/main.go to contain %q", snippet)
		}
	}
}

func TestMainStartsConversationSegmentationWorkerWhenConfigured(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	for _, snippet := range []string{
		"cfg.ConversationSegmentation.Enabled",
		"memory.NewConversationSegmentationWorker",
		"Conversation segmentation worker started",
		"store.NewConversationSegmentationStore",
	} {
		if !strings.Contains(mainContent, snippet) {
			t.Fatalf("expected cmd/server/main.go to contain %q", snippet)
		}
	}
}

func TestRunServerAutoMigrationLogsOutcomes(t *testing.T) {
	origOpen := openServerDB
	origRun := runServerAutoMigrate
	t.Cleanup(func() {
		openServerDB = origOpen
		runServerAutoMigrate = origRun
	})

	logs := make([]string, 0, 3)
	logf := func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	openServerDB = func() (*sql.DB, error) {
		return nil, errors.New("db unavailable")
	}
	runServerAutoMigration(logf)

	openServerDB = func() (*sql.DB, error) {
		return &sql.DB{}, nil
	}
	runServerAutoMigrate = func(_ *sql.DB, _ string) error {
		return errors.New("migration failed")
	}
	runServerAutoMigration(logf)

	runServerAutoMigrate = func(_ *sql.DB, _ string) error {
		return nil
	}
	runServerAutoMigration(logf)

	joined := strings.Join(logs, "\n")
	for _, snippet := range []string{
		"Auto-migration skipped; database unavailable",
		"Auto-migration failed",
		"Auto-migration complete",
	} {
		if !strings.Contains(joined, snippet) {
			t.Fatalf("expected log output to contain %q, got %q", snippet, joined)
		}
	}
}

func TestStartWorkerWithRecoveryRecoversPanic(t *testing.T) {
	var wg sync.WaitGroup
	logs := make([]string, 0, 1)
	logf := func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	startWorkerWithRecovery(context.Background(), &wg, "panic-worker", logf, func(context.Context) {
		panic("boom")
	})
	wg.Wait()

	if len(logs) == 0 {
		t.Fatalf("expected panic log output")
	}
	if !strings.Contains(logs[0], "worker panic") || !strings.Contains(logs[0], "panic-worker") || !strings.Contains(logs[0], "boom") {
		t.Fatalf("unexpected panic log output: %q", logs[0])
	}
}

func TestStartWorkerWithRecoveryRunsAsynchronously(t *testing.T) {
	var wg sync.WaitGroup
	started := make(chan struct{})
	release := make(chan struct{})

	startWorkerWithRecovery(context.Background(), &wg, "async-worker", nil, func(context.Context) {
		close(started)
		<-release
	})

	select {
	case <-started:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected worker goroutine to start asynchronously")
	}

	close(release)
	wg.Wait()
}

func TestMainStartsEllieIngestionWorkerWhenConfigured(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	for _, snippet := range []string{
		"cfg.EllieIngestion.Enabled",
		"memory.NewEllieIngestionWorker",
		"Ellie ingestion worker started",
		"store.NewEllieIngestionStore",
	} {
		if !strings.Contains(mainContent, snippet) {
			t.Fatalf("expected cmd/server/main.go to contain %q", snippet)
		}
	}
}

func TestMainStartsJobSchedulerWorkerWhenConfigured(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	for _, snippet := range []string{
		"cfg.JobScheduler.Enabled",
		"scheduler.NewAgentJobWorker",
		"store.NewAgentJobStore",
		"WorkspaceID:",
		"cfg.OrgID",
		"Agent job scheduler worker started",
		"Agent job scheduler worker disabled; database unavailable",
		"startWorker(worker.Start)",
	} {
		if !strings.Contains(mainContent, snippet) {
			t.Fatalf("expected cmd/server/main.go to contain %q", snippet)
		}
	}
}

func TestMainWorkersStopOnContextCancel(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	for _, snippet := range []string{
		"signal.NotifyContext",
		"context.WithCancel",
		"sync.WaitGroup",
		"workerWG.Wait()",
		"cancelWorkers()",
		"startWorker(worker.Start)",
	} {
		if !strings.Contains(mainContent, snippet) {
			t.Fatalf("expected cmd/server/main.go to contain %q", snippet)
		}
	}

	if strings.Contains(mainContent, "go worker.Start(context.Background())") {
		t.Fatalf("expected cmd/server/main.go to start workers through startWorker, found direct background startup")
	}
}

func TestMainStartsEllieContextInjectionWorkerWhenConfigured(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	for _, snippet := range []string{
		"cfg.EllieContextInjection.Enabled",
		"memory.NewEllieContextInjectionWorker",
		"Ellie context injection worker started",
		"store.NewEllieContextInjectionStore",
	} {
		if !strings.Contains(mainContent, snippet) {
			t.Fatalf("expected cmd/server/main.go to contain %q", snippet)
		}
	}
}

func TestMainStartsConversationTokenBackfillWorkerWhenConfigured(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	for _, snippet := range []string{
		"cfg.ConversationTokenBackfill.Enabled",
		"memory.NewConversationTokenBackfillWorker",
		"Conversation token backfill worker started",
		"store.NewConversationTokenStore",
	} {
		if !strings.Contains(mainContent, snippet) {
			t.Fatalf("expected cmd/server/main.go to contain %q", snippet)
		}
	}
}

func TestMainConstructsSingleSharedEmbedderForEmbeddingWorkers(t *testing.T) {
	t.Parallel()

	mainBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read cmd/server/main.go: %v", err)
	}
	mainContent := string(mainBytes)

	if strings.Count(mainContent, "memory.NewEmbedder(") != 1 {
		t.Fatalf("expected exactly one memory.NewEmbedder construction in cmd/server/main.go")
	}
	if strings.Count(mainContent, "getConversationEmbedder()") < 2 {
		t.Fatalf("expected both worker startup paths to reuse getConversationEmbedder()")
	}
}
