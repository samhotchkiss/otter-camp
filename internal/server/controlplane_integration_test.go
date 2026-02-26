//go:build integration

package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

type controlPlaneIntegrationServer struct {
	URL        string
	Pool       *pgxpool.Pool
	ts         *httptest.Server
	runService controlplane.RunService
}

func (s *controlPlaneIntegrationServer) Close() {
	if s == nil {
		return
	}
	s.ts.Close()
}

func TestControlPlaneAPIRoundTripCreateListCancel(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, _, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/control/runs", map[string]any{
		"trigger_type": "api",
	}, map[string]string{"Authorization": "Bearer " + token})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create run status=%d want=%d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	runID := jsonPathString(t, created.Body, "data", "id")
	runUUID, err := uuid.Parse(runID)
	if err != nil {
		t.Fatalf("parse run id: %v", err)
	}

	if err := testServer.runService.StartRun(context.Background(), runUUID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/runs?status=in_progress", nil, map[string]string{"Authorization": "Bearer " + token})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list runs status=%d want=%d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	rows, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("list data type=%T want=[]any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least one in-progress run body=%s", string(listed.Body))
	}

	cancel := mustJSON(t, http.MethodPost, testServer.URL+"/v1/control/runs/"+runID+"/cancel", map[string]any{}, map[string]string{"Authorization": "Bearer " + token})
	if cancel.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status=%d want=%d body=%s", cancel.StatusCode, http.StatusAccepted, string(cancel.Body))
	}
	if got := jsonPathString(t, cancel.Body, "data", "status"); got != "cancelling" {
		t.Fatalf("cancelled status=%q want=%q body=%s", got, "cancelling", string(cancel.Body))
	}
}

func TestControlPlaneAPIRunEventStreamReplayWithLastEventID(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	runRepo := controlplane.NewRunRepository(testServer.Pool)
	eventRepo := controlplane.NewRunEventRepository(testServer.Pool)

	now := time.Now().UTC()
	runRecord, err := runRepo.Create(context.Background(), controlplane.Run{
		OrganizationID: orgA.ID,
		PrincipalType:  "human_user",
		PrincipalID:    adminA.ID,
		Status:         "in_progress",
		TriggerType:    "api",
		StartedAt:      &now,
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}

	streamReq, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/control/runs/"+runRecord.ID.String()+"/events/stream", nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	streamReq.Header.Set("Authorization", "Bearer "+token)
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d want=%d", streamResp.StatusCode, http.StatusOK)
	}

	for i := 1; i <= 10; i++ {
		if _, err := eventRepo.Append(context.Background(), controlplane.RunEvent{
			RunID:     runRecord.ID,
			EventType: "heartbeat",
			ActorType: "system",
			Payload:   []byte(`{"idx":` + strconv.Itoa(i) + `}`),
		}); err != nil {
			t.Fatalf("append run event %d: %v", i, err)
		}
	}

	reader := bufio.NewReader(streamResp.Body)
	received := make([]int, 0, 10)
	deadline := time.Now().Add(5 * time.Second)
	for len(received) < 10 {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for stream events, got=%v", received)
		}
		eventName, id, _, err := readSSEFrame(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			continue
		}
		if eventName != "run_event" {
			continue
		}
		seq, err := strconv.Atoi(id)
		if err != nil {
			t.Fatalf("parse event id %q: %v", id, err)
		}
		received = append(received, seq)
	}
	if len(received) != 10 {
		t.Fatalf("received events=%v, want 10 events", received)
	}
	for i := 1; i <= 10; i++ {
		if received[i-1] != i {
			t.Fatalf("received events=%v, want [1..10]", received)
		}
	}

	if _, err := runRepo.UpdateStatus(context.Background(), runRecord.ID, runRecord.Version, "completed", nil, nil); err != nil {
		t.Fatalf("mark run completed: %v", err)
	}

	replayReq, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/control/runs/"+runRecord.ID.String()+"/events/stream", nil)
	if err != nil {
		t.Fatalf("new replay request: %v", err)
	}
	replayReq.Header.Set("Authorization", "Bearer "+token)
	replayReq.Header.Set("Last-Event-ID", "5")
	replayResp, err := http.DefaultClient.Do(replayReq)
	if err != nil {
		t.Fatalf("open replay stream: %v", err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusOK {
		t.Fatalf("replay status=%d want=%d", replayResp.StatusCode, http.StatusOK)
	}

	replayReader := bufio.NewReader(replayResp.Body)
	replayed := make([]int, 0, 5)
	for {
		eventName, id, _, err := readSSEFrame(replayReader)
		if err != nil {
			if err == io.EOF {
				break
			}
			continue
		}
		if eventName != "run_event" {
			continue
		}
		seq, err := strconv.Atoi(id)
		if err != nil {
			t.Fatalf("parse replay event id %q: %v", id, err)
		}
		replayed = append(replayed, seq)
	}
	if len(replayed) != 5 {
		t.Fatalf("replayed len=%d, want 5 replayed events (6..10), got=%v", len(replayed), replayed)
	}
	for idx, seq := range replayed {
		want := idx + 6
		if seq != want {
			t.Fatalf("replayed sequence[%d]=%d, want %d; replayed=%v", idx, seq, want, replayed)
		}
	}
}

func TestControlPlaneAPICostSummaryTotals(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, _, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	projectA := uuid.New()
	projectB := uuid.New()
	rollupDate := time.Now().UTC()
	for _, row := range []struct {
		projectID    uuid.UUID
		inputTokens  int64
		outputTokens int64
	}{
		{projectID: projectA, inputTokens: 10, outputTokens: 5},
		{projectID: projectB, inputTokens: 30, outputTokens: 5},
	} {
		if _, err := testServer.Pool.Exec(context.Background(), `
			INSERT INTO model_usage_rollup (
				organization_id,
				rollup_date,
				rollup_type,
				rollup_id,
				model_name,
				invocation_purpose,
				total_invocations,
				total_input_tokens,
				total_output_tokens
			)
			VALUES ($1, $2, 'project', $3, 'gpt-4o-mini', 'agent_turn', 1, $4, $5)
		`, orgA.ID, rollupDate, row.projectID, row.inputTokens, row.outputTokens); err != nil {
			t.Fatalf("seed model_usage_rollup row: %v", err)
		}
	}

	summary := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/cost/summary?period=30d&group_by=project", nil, map[string]string{"Authorization": "Bearer " + token})
	if summary.StatusCode != http.StatusOK {
		t.Fatalf("cost summary status=%d want=%d body=%s", summary.StatusCode, http.StatusOK, string(summary.Body))
	}
	if got := int(jsonPathFloatValue(t, summary.Body, "data", "total_tokens")); got != 50 {
		t.Fatalf("total_tokens=%d want=%d body=%s", got, 50, string(summary.Body))
	}
	byGroup, ok := jsonPathValue(t, summary.Body, "data", "by_group").([]any)
	if !ok {
		t.Fatalf("by_group type=%T want=[]any body=%s", jsonPathValue(t, summary.Body, "data", "by_group"), string(summary.Body))
	}
	if len(byGroup) != 2 {
		t.Fatalf("by_group len=%d want=2 body=%s", len(byGroup), string(summary.Body))
	}

	filtered := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/cost/summary?period=30d&group_by=project&project_id="+projectA.String(), nil, map[string]string{"Authorization": "Bearer " + token})
	if filtered.StatusCode != http.StatusOK {
		t.Fatalf("filtered cost summary status=%d want=%d body=%s", filtered.StatusCode, http.StatusOK, string(filtered.Body))
	}
	if got := int(jsonPathFloatValue(t, filtered.Body, "data", "total_tokens")); got != 15 {
		t.Fatalf("filtered total_tokens=%d want=%d body=%s", got, 15, string(filtered.Body))
	}
}

func TestControlPlaneAPIToolExecutionsDeniedFilter(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, orgA, adminA, orgB, _ := newControlPlaneTestServer(t)
	defer testServer.Close()
	tokenA := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	runRepo := controlplane.NewRunRepository(testServer.Pool)
	toolRepo := controlplane.NewToolExecutionRepository(testServer.Pool)

	runA, err := runRepo.Create(context.Background(), controlplane.Run{OrganizationID: orgA.ID, PrincipalType: "human_user", PrincipalID: adminA.ID, Status: "in_progress", TriggerType: "api"})
	if err != nil {
		t.Fatalf("seed run A: %v", err)
	}
	runB, err := runRepo.Create(context.Background(), controlplane.Run{OrganizationID: orgB.ID, PrincipalType: "human_user", PrincipalID: uuid.New(), Status: "in_progress", TriggerType: "api"})
	if err != nil {
		t.Fatalf("seed run B: %v", err)
	}

	for _, exec := range []controlplane.ToolExecution{
		{RunID: &runA.ID, ToolName: "file.read", ToolTier: "tier1", ToolDomain: "native", PolicyDecision: "denied", Status: "policy_denied", Input: json.RawMessage(`{"a":1}`)},
		{RunID: &runA.ID, ToolName: "file.list", ToolTier: "tier1", ToolDomain: "native", PolicyDecision: "allowed", Status: "completed", Input: json.RawMessage(`{"a":2}`)},
		{RunID: &runB.ID, ToolName: "file.read", ToolTier: "tier1", ToolDomain: "native", PolicyDecision: "denied", Status: "policy_denied", Input: json.RawMessage(`{"a":3}`)},
	} {
		if _, err := toolRepo.Create(context.Background(), exec); err != nil {
			t.Fatalf("seed tool execution: %v", err)
		}
	}

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/control/tool-executions?policy_decision=denied", nil, map[string]string{"Authorization": "Bearer " + tokenA})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list tool executions status=%d want=%d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	rows, ok := jsonPathValue(t, listed.Body, "data").([]any)
	if !ok {
		t.Fatalf("tool executions data type=%T want=[]any body=%s", jsonPathValue(t, listed.Body, "data"), string(listed.Body))
	}
	if len(rows) != 1 {
		t.Fatalf("denied tool executions len=%d want=1 body=%s", len(rows), string(listed.Body))
	}
	if got := jsonPathString(t, listed.Body, "data", "0", "policy_decision"); got != "denied" {
		t.Fatalf("policy_decision=%q want=denied body=%s", got, string(listed.Body))
	}
}

func newControlPlaneTestServer(t *testing.T) (*controlPlaneIntegrationServer, repo.Organization, repo.HumanUser, repo.Organization, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)
	orgA, adminA := createOrgAndUser(t, pool, "control-http-org-a", "control-admin-a@example.com", "Control Admin A", "admin", "admin-password")
	orgB, adminB := createOrgAndUser(t, pool, "control-http-org-b", "control-admin-b@example.com", "Control Admin B", "admin", "admin-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: orgA.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	runService, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewControlPlaneRouteRegistrar(ControlPlaneRouteOptions{
				Pool:       pool,
				RunService: runService,
			}),
		},
	})
	ts := httptest.NewServer(handler)
	return &controlPlaneIntegrationServer{URL: ts.URL, Pool: pool, ts: ts, runService: runService}, orgA, adminA, orgB, adminB
}
