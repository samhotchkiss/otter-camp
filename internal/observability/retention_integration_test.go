//go:build integration

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/jobs"
	"github.com/samhotchkiss/otter-camp/internal/logging"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

func TestRetention_ChatMessages_90Days(t *testing.T) {
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)
	agent := testutil.MakeAgent(t, pool, orgID)

	sessionID := insertChatSession(t, pool, orgID, project.ID)
	turnID := insertChatTurn(t, pool, sessionID, agent.ID)
	oldID := insertChatMessage(t, pool, sessionID, &turnID, agent.ID, 1)
	newID := insertChatMessage(t, pool, sessionID, &turnID, agent.ID, 2)
	setCreatedAt(t, pool, "chat_message", oldID, now.AddDate(0, 0, -91))
	setCreatedAt(t, pool, "chat_message", newID, now.AddDate(0, 0, -10))

	runRetention(t, pool, now)

	assertRowMissing(t, pool, "chat_message", oldID)
	assertRowExists(t, pool, "chat_message", newID)
}

func TestRetention_RunRecords_90Days(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	project := testutil.MakeProject(t, pool, orgID)

	run, err := controlplane.NewRunRepository(pool).Create(ctx, controlplane.Run{
		OrganizationID: orgID,
		ProjectID:      &project.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "in_progress",
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	step, err := controlplane.NewRunStepRepository(pool).Create(ctx, controlplane.RunStep{
		RunID:       run.ID,
		StepNumber:  1,
		Status:      "completed",
		ToolName:    strPtr("retention.tool"),
		ToolTier:    strPtr("tier2"),
		Metadata:    json.RawMessage(`{}`),
		StartedAt:   timePtr(now.Add(-2 * time.Hour)),
		CompletedAt: timePtr(now.Add(-time.Hour)),
	})
	if err != nil {
		t.Fatalf("create run_step: %v", err)
	}
	attempt, err := controlplane.NewRunAttemptRepository(pool).Create(ctx, controlplane.RunAttempt{
		RunStepID:     step.ID,
		AttemptNumber: 1,
		Trigger:       "initial",
		Status:        "completed",
		Metadata:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create run_attempt: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO run_event (run_id, run_step_id, run_attempt_id, sequence, event_type, actor_type, payload, created_at)
		VALUES ($1, $2, $3, 1, 'run_started', 'system', '{}'::jsonb, $4)
	`, run.ID, step.ID, attempt.ID, now.AddDate(0, 0, -5)); err != nil {
		t.Fatalf("insert run_event: %v", err)
	}
	var artifactID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO run_artifact (
			run_id, run_step_id, run_attempt_id, artifact_type, storage_key, content_type, byte_size, inline_content, created_at
		)
		VALUES ($1, $2, $3, 'stdout', $4, 'text/plain', 12, 'artifact', $5)
		RETURNING id
	`, run.ID, step.ID, attempt.ID, "retention-artifact-"+uuid.NewString(), now.AddDate(0, 0, -1)).Scan(&artifactID); err != nil {
		t.Fatalf("insert run_artifact: %v", err)
	}

	setCreatedAt(t, pool, "run", run.ID, now.AddDate(0, 0, -91))
	setCreatedAt(t, pool, "run_step", step.ID, now.AddDate(0, 0, -91))
	setCreatedAt(t, pool, "run_attempt", attempt.ID, now.AddDate(0, 0, -91))

	runRetention(t, pool, now)

	assertRowMissing(t, pool, "run", run.ID)
	assertRowMissing(t, pool, "run_step", step.ID)
	assertRowMissing(t, pool, "run_attempt", attempt.ID)
	assertZeroCount(t, pool, `SELECT COUNT(*) FROM run_event WHERE run_id = $1`, run.ID)
	assertRowMissing(t, pool, "run_artifact", artifactID)
}

func TestRetention_ModelInvocations_90Days(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "retention-provider-" + uuid.NewString()[:8],
		DisplayName: "Retention Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}

	oldInvocation := insertModelInvocation(t, pool, orgID, provider.ID)
	newInvocation := insertModelInvocation(t, pool, orgID, provider.ID)
	setCreatedAt(t, pool, "model_invocation", oldInvocation, now.AddDate(0, 0, -91))
	setCreatedAt(t, pool, "model_invocation", newInvocation, now.AddDate(0, 0, -10))

	runRetention(t, pool, now)

	assertRowMissing(t, pool, "model_invocation", oldInvocation)
	assertRowExists(t, pool, "model_invocation", newInvocation)
}

func TestRetention_DomainEvents_90Days(t *testing.T) {
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)

	oldID := insertDomainEvent(t, pool, orgID, "domain.old")
	newID := insertDomainEvent(t, pool, orgID, "domain.new")
	setCreatedAt(t, pool, "domain_event", oldID, now.AddDate(0, 0, -91))
	setCreatedAt(t, pool, "domain_event", newID, now.AddDate(0, 0, -5))

	runRetention(t, pool, now)

	assertRowMissing(t, pool, "domain_event", oldID)
	assertRowExists(t, pool, "domain_event", newID)
}

func TestRetention_AuditEvents_1Year(t *testing.T) {
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "admin")

	oldID := testutil.MakeAuditEvent(t, pool, orgID, "audit.old", "human", user.ID)
	newID := testutil.MakeAuditEvent(t, pool, orgID, "audit.new", "human", user.ID)
	setCreatedAt(t, pool, "audit_event", oldID, now.AddDate(-1, -1, 0))
	setCreatedAt(t, pool, "audit_event", newID, now.AddDate(0, -2, 0))

	runRetention(t, pool, now)

	assertRowMissing(t, pool, "audit_event", oldID)
	assertRowExists(t, pool, "audit_event", newID)
}

func TestRetention_ArchivedMemories_1Year(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	memRepo := repo.NewMemoryRepo(pool)

	active, err := memRepo.Create(ctx, repo.Memory{
		OrganizationID: orgID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "active memory",
		ContentHash:    testutil.HashContent("active memory"),
		Status:         "active",
	})
	if err != nil {
		t.Fatalf("create active memory: %v", err)
	}
	archived, err := memRepo.Create(ctx, repo.Memory{
		OrganizationID: orgID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "archived memory",
		ContentHash:    testutil.HashContent("archived memory"),
		Status:         "archived",
	})
	if err != nil {
		t.Fatalf("create archived memory: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE memory
		SET superseded_by = $2, superseded_at = $3
		WHERE id = $1
	`, archived.ID, active.ID, now.AddDate(-1, -1, 0)); err != nil {
		t.Fatalf("mark archived memory superseded: %v", err)
	}
	setCreatedAt(t, pool, "memory", archived.ID, now.AddDate(-1, -1, 0))
	setCreatedAt(t, pool, "memory", active.ID, now.AddDate(-2, 0, 0))

	runRetention(t, pool, now)

	assertRowMissing(t, pool, "memory", archived.ID)
	assertRowExists(t, pool, "memory", active.ID)
}

func TestRetention_TraceSpans_7Days(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)

	traceService, err := NewTraceSpanService(pool)
	if err != nil {
		t.Fatalf("NewTraceSpanService: %v", err)
	}
	newSpan, err := traceService.Create(ctx, repo.TraceSpan{
		TraceID:        uuid.New(),
		OrganizationID: &orgID,
		SpanName:       "recent.span",
		Kind:           "server",
		Status:         "ok",
		StartedAt:      now.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create new trace span: %v", err)
	}

	oldStart := now.AddDate(0, 0, -14).Truncate(24 * time.Hour)
	oldEnd := oldStart.Add(24 * time.Hour)
	partitionDDL := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS trace_span_p_old_retention PARTITION OF trace_span FOR VALUES FROM ('%s') TO ('%s')",
		oldStart.Format(time.RFC3339),
		oldEnd.Format(time.RFC3339),
	)
	if _, err := pool.Exec(ctx, partitionDDL); err != nil {
		t.Fatalf("create old trace partition: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trace_span (
			trace_id, organization_id, span_name, service, kind, status, attributes, events, started_at, created_at
		)
		VALUES ($1, $2, 'old.span', 'ottercamp', 'internal', 'ok', '{}'::jsonb, '[]'::jsonb, $3, $3)
	`, uuid.New(), orgID, oldStart.Add(2*time.Hour)); err != nil {
		t.Fatalf("insert old trace span: %v", err)
	}

	runRetention(t, pool, now)

	assertRowExists(t, pool, "trace_span", newSpan.ID)
	assertZeroCount(t, pool, `SELECT COUNT(*) FROM trace_span WHERE span_name = 'old.span'`)
	assertZeroCount(t, pool, `
		SELECT COUNT(*)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema()
		  AND c.relname = 'trace_span_p_old_retention'
	`)
}

func TestRetention_Idempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)

	oldID := insertDomainEvent(t, pool, orgID, "retention.idempotent.old")
	setCreatedAt(t, pool, "domain_event", oldID, now.AddDate(0, 0, -91))

	runRetention(t, pool, now)
	var afterFirst int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM domain_event WHERE organization_id = $1`, orgID).Scan(&afterFirst); err != nil {
		t.Fatalf("count domain events after first run: %v", err)
	}

	runRetention(t, pool, now)
	var afterSecond int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM domain_event WHERE organization_id = $1`, orgID).Scan(&afterSecond); err != nil {
		t.Fatalf("count domain events after second run: %v", err)
	}
	if afterSecond != afterFirst {
		t.Fatalf("idempotency mismatch: afterFirst=%d afterSecond=%d", afterFirst, afterSecond)
	}
}

func TestRequestID_Propagation(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	previousDefault := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(previousDefault)
	downstreamSeen := make(chan string, 1)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamSeen <- strings.TrimSpace(r.Header.Get(api.HeaderRequestID))
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()

	serverURL, token := newRequestIDHarness(t, logger, downstream.URL)

	req, err := http.NewRequest(http.MethodGet, serverURL+"/v1/request-id/check", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	requestID := strings.TrimSpace(resp.Header.Get(api.HeaderRequestID))
	if requestID == "" {
		t.Fatal("expected generated X-Request-ID response header")
	}

	select {
	case forwarded := <-downstreamSeen:
		if forwarded != requestID {
			t.Fatalf("downstream request_id=%q want=%q", forwarded, requestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("downstream request not observed")
	}

	logText := logBuf.String()
	count := strings.Count(logText, "request_id="+requestID)
	if count < 3 {
		t.Fatalf("request_id appeared in %d log lines, want >= 3 logs=%s", count, logText)
	}
}

func TestRequestID_ClientProvided(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	previousDefault := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(previousDefault)
	downstreamSeen := make(chan string, 1)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamSeen <- strings.TrimSpace(r.Header.Get(api.HeaderRequestID))
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()

	serverURL, token := newRequestIDHarness(t, logger, downstream.URL)
	clientRequestID := "my-test-id-123"

	req, err := http.NewRequest(http.MethodGet, serverURL+"/v1/request-id/check", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(api.HeaderRequestID, clientRequestID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if got := strings.TrimSpace(resp.Header.Get(api.HeaderRequestID)); got != clientRequestID {
		t.Fatalf("response request_id=%q want=%q", got, clientRequestID)
	}
	select {
	case forwarded := <-downstreamSeen:
		if forwarded != clientRequestID {
			t.Fatalf("downstream request_id=%q want=%q", forwarded, clientRequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("downstream request not observed")
	}

	logText := logBuf.String()
	if strings.Count(logText, "request_id="+clientRequestID) < 3 {
		t.Fatalf("client request_id missing from expected log lines: %s", logText)
	}
}

type requestIDRouteRegistrar struct {
	downstreamURL string
}

func (r requestIDRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.Get("/request-id/check", func(w http.ResponseWriter, req *http.Request) {
		requestID, _ := api.RequestIDFromContext(req.Context())
		logger := logging.FromContext(req.Context())
		logger.Info("request_midpoint", "path", req.URL.Path)

		downstreamReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, r.downstreamURL, nil)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "downstream request build failed")
			return
		}
		downstreamReq.Header.Set(api.HeaderRequestID, requestID)
		downstreamResp, err := http.DefaultClient.Do(downstreamReq)
		if err != nil {
			api.Error(w, http.StatusBadGateway, api.ErrCodeServiceUnavailable, "downstream request failed")
			return
		}
		_ = downstreamResp.Body.Close()
		logger.Info("request_downstream_complete", "status_code", downstreamResp.StatusCode)
		api.JSON(w, http.StatusOK, map[string]any{"request_id": requestID})
	})
}

func newRequestIDHarness(t *testing.T, logger *slog.Logger, downstreamURL string) (string, string) {
	t.Helper()
	pool := testdb.New(t)
	orgID := testutil.MakeOrg(t, pool)
	user := testutil.MakeUser(t, pool, orgID, "admin")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: orgID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:     "request-id-integration",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []server.RouteRegistrar{
			requestIDRouteRegistrar{downstreamURL: downstreamURL},
		},
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	token := testutil.LoginUser(t, srv, user.Email, testutil.DefaultUserPassword)
	return srv.URL, token
}

func runRetention(t *testing.T, pool *pgxpool.Pool, now time.Time) {
	t.Helper()
	job, err := jobs.NewRetentionJob(jobs.RetentionJobOptions{
		Pool: pool,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRetentionJob: %v", err)
	}
	if err := job.Run(context.Background()); err != nil {
		t.Fatalf("RetentionJob.Run: %v", err)
	}
}

func insertChatSession(t *testing.T, pool *pgxpool.Pool, orgID, projectID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO chat_session (organization_id, scope_type, scope_id, mode, status, created_by_type, created_by_id)
		VALUES ($1, 'project', $2, 'sync', 'active', 'system', $3)
		RETURNING id
	`, orgID, projectID, uuid.Nil).Scan(&id); err != nil {
		t.Fatalf("insert chat_session: %v", err)
	}
	return id
}

func insertChatTurn(t *testing.T, pool *pgxpool.Pool, sessionID, agentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO chat_turn (session_id, turn_number, responding_type, responding_id, status)
		VALUES ($1, 1, 'agent', $2, 'completed')
		RETURNING id
	`, sessionID, agentID).Scan(&id); err != nil {
		t.Fatalf("insert chat_turn: %v", err)
	}
	return id
}

func insertChatMessage(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID, turnID *uuid.UUID, agentID uuid.UUID, seq int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO chat_message (session_id, turn_id, sequence_number, author_type, author_id, role, content, status)
		VALUES ($1, $2, $3, 'agent', $4, 'assistant', 'retention message', 'final')
		RETURNING id
	`, sessionID, turnID, seq, agentID).Scan(&id); err != nil {
		t.Fatalf("insert chat_message: %v", err)
	}
	return id
}

func insertModelInvocation(t *testing.T, pool *pgxpool.Pool, orgID, providerID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO model_invocation (
			organization_id, model_provider_id, invocation_purpose, status, model_name, metadata
		)
		VALUES ($1, $2, 'agent_turn', 'completed', 'retention-model', '{}'::jsonb)
		RETURNING id
	`, orgID, providerID).Scan(&id); err != nil {
		t.Fatalf("insert model_invocation: %v", err)
	}
	return id
}

func insertDomainEvent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, eventType string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO domain_event (organization_id, event_type, actor_type, payload)
		VALUES ($1, $2, 'system', '{}'::jsonb)
		RETURNING id
	`, orgID, eventType).Scan(&id); err != nil {
		t.Fatalf("insert domain_event: %v", err)
	}
	return id
}

func setCreatedAt(t *testing.T, pool *pgxpool.Pool, table string, id uuid.UUID, createdAt time.Time) {
	t.Helper()
	query := "UPDATE " + table + " SET created_at = $2 WHERE id = $1"
	if _, err := pool.Exec(context.Background(), query, id, createdAt.UTC()); err != nil {
		t.Fatalf("set created_at on %s: %v", table, err)
	}
}

func assertRowMissing(t *testing.T, pool *pgxpool.Pool, table string, id uuid.UUID) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table+" WHERE id = $1", id).Scan(&count); err != nil {
		t.Fatalf("count missing row %s: %v", table, err)
	}
	if count != 0 {
		t.Fatalf("expected row missing table=%s id=%s count=%d", table, id, count)
	}
}

func assertRowExists(t *testing.T, pool *pgxpool.Pool, table string, id uuid.UUID) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table+" WHERE id = $1", id).Scan(&count); err != nil {
		t.Fatalf("count existing row %s: %v", table, err)
	}
	if count != 1 {
		t.Fatalf("expected row exists table=%s id=%s count=%d", table, id, count)
	}
}

func assertZeroCount(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 query=%s", count, query)
	}
}

func strPtr(v string) *string {
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}
