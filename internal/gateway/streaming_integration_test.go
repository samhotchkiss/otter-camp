//go:build integration

package gateway

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

type collectingWriter struct {
	chunks [][]byte
}

func (w *collectingWriter) WriteChunk(chunk []byte) error {
	w.chunks = append(w.chunks, append([]byte(nil), chunk...))
	return nil
}

func TestStreamResponseDeterministicUpdatesInvocationAndRollup(t *testing.T) {
	t.Setenv("OTTERCAMP_MODE", "test")
	t.Setenv("GATEWAY_DETERMINISTIC", "true")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	rollupRepo := repo.NewModelUsageRollupRepo(pool)
	enqueuer := jobqueue.New(pool, nil, jobqueue.Config{})

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "stream-deterministic-org",
		DisplayName: "Stream Deterministic Org",
	})
	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "test-provider",
		DisplayName: "Test Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	connection := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID: org.ID,
		ProviderID:     provider.ID,
		DisplayName:    "Primary",
		APIKeyRef:      "ref:test-provider",
		IsEnabled:      true,
	})

	profileID := "high-capability"
	invocation := mustCreateInvocation(t, ctx, invocationRepo, repo.ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelProfileID:       &profileID,
		InvocationPurpose:    "agent_turn",
		ModelName:            "gpt-4o-mini",
		IsStreaming:          true,
		Metadata:             []byte(`{"prompt":"hello world"}`),
	})

	processor, err := NewStreamProcessor(StreamProcessorOptions{
		Invocations: invocationRepo,
		Providers:   providerRepo,
		JobEnqueuer: enqueuer,
	})
	if err != nil {
		t.Fatalf("NewStreamProcessor: %v", err)
	}

	writer := &collectingWriter{}
	if err := processor.StreamResponse(ctx, invocation.ID, nil, writer); err != nil {
		t.Fatalf("StreamResponse: %v", err)
	}
	if len(writer.chunks) < 2 {
		t.Fatalf("chunk count = %d, want >= 2", len(writer.chunks))
	}

	stored, err := invocationRepo.GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.InputTokens == nil || *stored.InputTokens != 37 {
		t.Fatalf("input_tokens = %v, want 37", stored.InputTokens)
	}
	if stored.OutputTokens == nil || *stored.OutputTokens != 21 {
		t.Fatalf("output_tokens = %v, want 21", stored.OutputTokens)
	}

	var payloadJSON []byte
	if err := pool.QueryRow(ctx, `
		SELECT payload
		FROM job_queue
		WHERE job_type = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, rollupUpdateJobType).Scan(&payloadJSON); err != nil {
		t.Fatalf("select rollup_update job: %v", err)
	}

	var payload RollupUpdatePayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("decode rollup payload: %v", err)
	}

	rollupDate, err := time.Parse(defaultRollupQueueDate, payload.RollupDate)
	if err != nil {
		t.Fatalf("parse rollup date: %v", err)
	}
	worker := NewRollupWorker(pool, rollupRepo, nil)
	if err := worker.RunRollupForDate(ctx, payload.OrgID, rollupDate); err != nil {
		t.Fatalf("RunRollupForDate: %v", err)
	}

	rollups, err := rollupRepo.GetForDate(ctx, org.ID, rollupDate)
	if err != nil {
		t.Fatalf("GetForDate: %v", err)
	}
	row := findRollupRow(rollups, "model_provider", &provider.ID, "gpt-4o-mini", "agent_turn")
	if row == nil {
		t.Fatal("provider rollup row missing")
	}
	if row.TotalInputTokens != 37 {
		t.Fatalf("rollup total_input_tokens = %d, want 37", row.TotalInputTokens)
	}
}

func TestRunRollupForDateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	rollupRepo := repo.NewModelUsageRollupRepo(pool)
	worker := NewRollupWorker(pool, rollupRepo, nil)

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "rollup-idempotent-org",
		DisplayName: "Rollup Idempotent Org",
	})
	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "rollup-provider",
		DisplayName: "Rollup Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})

	modelName := "gpt-4o-mini"
	purpose := "agent_turn"
	invA := mustCreateInvocation(t, ctx, invocationRepo, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: purpose,
		ModelName:         modelName,
		Metadata:          []byte(`{"prompt":"a"}`),
	})
	invB := mustCreateInvocation(t, ctx, invocationRepo, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: purpose,
		ModelName:         modelName,
		Metadata:          []byte(`{"prompt":"b"}`),
	})

	if err := invocationRepo.UpdateCompletion(ctx, invA.ID, 100, 10, 0, 5, 30, nil, nil); err != nil {
		t.Fatalf("UpdateCompletion invA: %v", err)
	}
	if err := invocationRepo.UpdateCompletion(ctx, invB.ID, 200, 20, 0, 7, 40, nil, nil); err != nil {
		t.Fatalf("UpdateCompletion invB: %v", err)
	}

	day := invA.CreatedAt.UTC()
	if err := worker.RunRollupForDate(ctx, org.ID, day); err != nil {
		t.Fatalf("RunRollupForDate first: %v", err)
	}
	firstRows, err := rollupRepo.GetForDate(ctx, org.ID, day)
	if err != nil {
		t.Fatalf("GetForDate first: %v", err)
	}
	firstRow := findRollupRow(firstRows, "model_provider", &provider.ID, modelName, purpose)
	if firstRow == nil {
		t.Fatal("first rollup row missing")
	}

	time.Sleep(10 * time.Millisecond)
	if err := worker.RunRollupForDate(ctx, org.ID, day); err != nil {
		t.Fatalf("RunRollupForDate second: %v", err)
	}
	secondRows, err := rollupRepo.GetForDate(ctx, org.ID, day)
	if err != nil {
		t.Fatalf("GetForDate second: %v", err)
	}
	secondRow := findRollupRow(secondRows, "model_provider", &provider.ID, modelName, purpose)
	if secondRow == nil {
		t.Fatal("second rollup row missing")
	}

	if len(firstRows) != len(secondRows) {
		t.Fatalf("row count changed: first=%d second=%d", len(firstRows), len(secondRows))
	}
	if secondRow.TotalInputTokens != 300 {
		t.Fatalf("total_input_tokens = %d, want 300", secondRow.TotalInputTokens)
	}
	if !secondRow.UpdatedAt.After(firstRow.UpdatedAt) {
		t.Fatalf("updated_at did not advance: first=%s second=%s", firstRow.UpdatedAt, secondRow.UpdatedAt)
	}
}

func TestCaptureStoresGzippedJSONInFilesystemAdapter(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "capture-integration-org",
		DisplayName: "Capture Integration Org",
		Settings: repo.OrganizationSettings{
			ModelCapture: repo.OrganizationModelCaptureSettings{
				CapturePrompts:   true,
				CaptureResponses: true,
			},
		},
	})
	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "capture-provider",
		DisplayName: "Capture Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})

	invocation := mustCreateInvocation(t, ctx, invocationRepo, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		ModelName:         "gpt-4o-mini",
		Metadata:          []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	})

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	capture := NewCaptureService(orgRepo, store, nil)

	promptKey, responseKey := capture.Capture(ctx, invocation, []byte(`{"message":"ok"}`))
	if promptKey == nil || responseKey == nil {
		t.Fatalf("capture keys = (%v,%v), want both non-nil", promptKey, responseKey)
	}

	assertGzipJSONReadable(t, store, *promptKey)
	assertGzipJSONReadable(t, store, *responseKey)
}

func assertGzipJSONReadable(t *testing.T, store storage.Store, key string) {
	t.Helper()
	body, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer body.Close()

	reader, err := gzip.NewReader(body)
	if err != nil {
		t.Fatalf("gzip.NewReader(%q): %v", key, err)
	}
	defer reader.Close()

	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(%q): %v", key, err)
	}
	if !json.Valid(raw) {
		t.Fatalf("payload at %q is not valid json: %s", key, string(raw))
	}
}

func findRollupRow(rows []repo.ModelUsageRollup, rollupType string, rollupID *uuid.UUID, modelName string, purpose string) *repo.ModelUsageRollup {
	for i := range rows {
		row := rows[i]
		if row.RollupType != rollupType {
			continue
		}
		if rollupID == nil {
			if row.RollupID != nil {
				continue
			}
		} else {
			if row.RollupID == nil || *row.RollupID != *rollupID {
				continue
			}
		}
		if modelName != "" {
			if row.ModelName == nil || *row.ModelName != modelName {
				continue
			}
		}
		if purpose != "" {
			if row.InvocationPurpose == nil || *row.InvocationPurpose != purpose {
				continue
			}
		}
		return &rows[i]
	}
	return nil
}

func mustCreateOrg(t *testing.T, ctx context.Context, orgRepo *repo.OrgRepo, org repo.Organization) repo.Organization {
	t.Helper()
	created, err := orgRepo.Create(ctx, org)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return created
}

func mustCreateProvider(t *testing.T, ctx context.Context, providerRepo *repo.ModelProviderRepo, provider repo.ModelProvider) repo.ModelProvider {
	t.Helper()
	created, err := providerRepo.Create(ctx, provider)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return created
}

func mustCreateConnection(t *testing.T, ctx context.Context, connectionRepo *repo.ProviderConnectionRepo, connection repo.ProviderConnection) repo.ProviderConnection {
	t.Helper()
	created, err := connectionRepo.Create(ctx, connection)
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return created
}

func mustCreateInvocation(t *testing.T, ctx context.Context, invocationRepo *repo.ModelInvocationRepo, invocation repo.ModelInvocation) repo.ModelInvocation {
	t.Helper()
	created, err := invocationRepo.Create(ctx, invocation)
	if err != nil {
		t.Fatalf("create invocation: %v", err)
	}
	return created
}
