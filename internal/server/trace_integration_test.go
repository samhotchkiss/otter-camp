//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestTraceSpansHTTPListAndFilters(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	testServer, adminUser := newTraceTestServer(t)
	defer testServer.Close()

	runA := uuid.New()
	taskA := uuid.New()
	agentA := uuid.New()
	runB := uuid.New()
	taskB := uuid.New()
	agentB := uuid.New()

	spanRepo := repo.NewTraceSpanRepo(testServer.Pool)
	spanA := createTraceSpanForTest(t, spanRepo, adminUser.OrganizationID, "run-a.operation", runA, taskA, agentA)
	spanB := createTraceSpanForTest(t, spanRepo, adminUser.OrganizationID, "run-b.operation", runB, taskB, agentB)

	token := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	byRun := mustJSON(t, http.MethodGet, testServer.URL+"/v1/trace/spans?run_id="+runA.String(), nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if byRun.StatusCode != http.StatusOK {
		t.Fatalf("run filter status = %d, want %d body=%s", byRun.StatusCode, http.StatusOK, string(byRun.Body))
	}
	if got := jsonPathString(t, byRun.Body, "data", "0", "span_id"); got != spanA.ID.String() {
		t.Fatalf("run filter span_id = %q, want %q body=%s", got, spanA.ID.String(), string(byRun.Body))
	}
	if got := jsonPathString(t, byRun.Body, "data", "0", "operation"); got != "run-a.operation" {
		t.Fatalf("run filter operation = %q, want %q body=%s", got, "run-a.operation", string(byRun.Body))
	}
	if got := jsonPathString(t, byRun.Body, "data", "0", "metadata", "run_id"); got != runA.String() {
		t.Fatalf("run filter metadata.run_id = %q, want %q body=%s", got, runA.String(), string(byRun.Body))
	}
	if !hasJSONPath(byRun.Body, "data", "0", "duration_ms") {
		t.Fatalf("run filter missing duration_ms body=%s", string(byRun.Body))
	}

	byTask := mustJSON(t, http.MethodGet, testServer.URL+"/v1/trace/spans?task_id="+taskB.String(), nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if byTask.StatusCode != http.StatusOK {
		t.Fatalf("task filter status = %d, want %d body=%s", byTask.StatusCode, http.StatusOK, string(byTask.Body))
	}
	if got := jsonPathString(t, byTask.Body, "data", "0", "span_id"); got != spanB.ID.String() {
		t.Fatalf("task filter span_id = %q, want %q body=%s", got, spanB.ID.String(), string(byTask.Body))
	}
	if got := jsonPathString(t, byTask.Body, "data", "0", "metadata", "task_id"); got != taskB.String() {
		t.Fatalf("task filter metadata.task_id = %q, want %q body=%s", got, taskB.String(), string(byTask.Body))
	}
}

func newTraceTestServer(t *testing.T) (*authIntegrationServer, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	org, adminUser := createOrgAndUser(t, pool, "trace-http-org", "trace-admin@example.com", "Trace Admin", "admin", "admin-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewTraceRouteRegistrar(pool),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, adminUser
}

func createTraceSpanForTest(t *testing.T, spanRepo *repo.TraceSpanRepo, organizationID uuid.UUID, operation string, runID, taskID, agentID uuid.UUID) repo.TraceSpan {
	t.Helper()

	now := time.Now().UTC()
	duration := 25
	attrs, err := json.Marshal(map[string]any{
		"run_id":   runID.String(),
		"task_id":  taskID.String(),
		"agent_id": agentID.String(),
	})
	if err != nil {
		t.Fatalf("marshal attrs: %v", err)
	}

	span, err := spanRepo.Create(context.Background(), repo.TraceSpan{
		TraceID:        uuid.New(),
		OrganizationID: &organizationID,
		SpanName:       operation,
		Service:        "ottercamp",
		Kind:           "internal",
		Status:         "ok",
		Attributes:     attrs,
		StartedAt:      now,
		EndedAt:        ptrTime(now.Add(25 * time.Millisecond)),
		DurationMS:     &duration,
	})
	if err != nil {
		t.Fatalf("create span: %v", err)
	}
	return span
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
