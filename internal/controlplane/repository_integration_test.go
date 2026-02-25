//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestControlPlaneCascadeDeleteRunChildren(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)

	runRepo := NewRunRepository(pool)
	stepRepo := NewRunStepRepository(pool)
	attemptRepo := NewRunAttemptRepository(pool)

	runRecord := seedControlPlaneRun(t, ctx, runRepo, org.ID)
	step, err := stepRepo.Create(ctx, RunStep{
		RunID:      runRecord.ID,
		StepNumber: 1,
		Status:     "pending",
	})
	if err != nil {
		t.Fatalf("Create run_step: %v", err)
	}
	attempt, err := attemptRepo.Create(ctx, RunAttempt{
		RunStepID:     step.ID,
		AttemptNumber: 1,
		Trigger:       "initial",
		Status:        "pending",
	})
	if err != nil {
		t.Fatalf("Create run_attempt: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM run WHERE id = $1`, runRecord.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}

	assertCountByID(t, ctx, pool, "run_step", step.ID, 0)
	assertCountByID(t, ctx, pool, "run_attempt", attempt.ID, 0)
}

func TestRunEventUniqueSequenceConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	runRecord := seedControlPlaneRun(t, ctx, NewRunRepository(pool), org.ID)

	if _, err := pool.Exec(ctx, `
		INSERT INTO run_event (run_id, sequence, event_type, actor_type, actor_id, payload)
		VALUES ($1, 1, 'run_started', 'system', NULL, '{}'::jsonb)
	`, runRecord.ID); err != nil {
		t.Fatalf("insert first run_event: %v", err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO run_event (run_id, sequence, event_type, actor_type, actor_id, payload)
		VALUES ($1, 1, 'run_completed', 'system', NULL, '{}'::jsonb)
	`, runRecord.ID)
	if err == nil {
		t.Fatal("expected unique constraint violation for duplicate run_event sequence")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate sequence error = %v, want pg code 23505", err)
	}
}

func TestRunEventRepositoryAppendConcurrentSequenceAssignment(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	runRecord := seedControlPlaneRun(t, ctx, NewRunRepository(pool), org.ID)

	repo := NewRunEventRepository(pool)
	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 10)
		seqCh = make(chan int, 10)
	)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := repo.Append(ctx, RunEvent{
				RunID:     runRecord.ID,
				EventType: "heartbeat",
				ActorType: "system",
			})
			if err != nil {
				errCh <- err
				return
			}
			seqCh <- created.Sequence
		}()
	}
	wg.Wait()
	close(errCh)
	close(seqCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("Append concurrent error: %v", err)
		}
	}

	seen := map[int]struct{}{}
	for sequence := range seqCh {
		if _, ok := seen[sequence]; ok {
			t.Fatalf("duplicate run_event sequence %d", sequence)
		}
		seen[sequence] = struct{}{}
	}
	if len(seen) != 10 {
		t.Fatalf("sequence count = %d, want 10", len(seen))
	}
	for i := 1; i <= 10; i++ {
		if _, ok := seen[i]; !ok {
			t.Fatalf("missing sequence %d", i)
		}
	}
}

func TestRunEventRepositoryAppendPublishesNotify(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	runRecord := seedControlPlaneRun(t, ctx, NewRunRepository(pool), org.ID)

	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire listener: %v", err)
	}
	defer listener.Release()

	channel := RunEventsChannel(runRecord.ID)
	listenSQL := "LISTEN " + pgx.Identifier{channel}.Sanitize()
	if _, err := listener.Exec(ctx, listenSQL); err != nil {
		t.Fatalf("listen run event channel: %v", err)
	}
	defer func() {
		_, _ = listener.Exec(context.Background(), "UNLISTEN "+pgx.Identifier{channel}.Sanitize())
	}()

	created, err := NewRunEventRepository(pool).Append(ctx, RunEvent{
		RunID:     runRecord.ID,
		EventType: "heartbeat",
		ActorType: "system",
		Payload:   []byte(`{"k":"v"}`),
	})
	if err != nil {
		t.Fatalf("append run event: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	notification, err := listener.Conn().WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("wait for notification: %v", err)
	}
	if notification.Channel != channel {
		t.Fatalf("notification channel = %q, want %q", notification.Channel, channel)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
		t.Fatalf("decode notification payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", payload["run_id"])); got != runRecord.ID.String() {
		t.Fatalf("payload.run_id = %q, want %q", got, runRecord.ID)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", payload["sequence"])); got != fmt.Sprintf("%d", created.Sequence) {
		t.Fatalf("payload.sequence = %q, want %d", got, created.Sequence)
	}
}

func TestRunFkFixupModelInvocationOnDeleteSetNull(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)

	runRepo := NewRunRepository(pool)
	stepRepo := NewRunStepRepository(pool)
	attemptRepo := NewRunAttemptRepository(pool)

	runRecord := seedControlPlaneRun(t, ctx, runRepo, org.ID)
	step, err := stepRepo.Create(ctx, RunStep{RunID: runRecord.ID, StepNumber: 1, Status: "pending"})
	if err != nil {
		t.Fatalf("Create run_step: %v", err)
	}
	attempt, err := attemptRepo.Create(ctx, RunAttempt{RunStepID: step.ID, AttemptNumber: 1, Trigger: "initial", Status: "pending"})
	if err != nil {
		t.Fatalf("Create run_attempt: %v", err)
	}

	provider, connection := seedControlPlaneProviderConnection(t, ctx, pool, org.ID)
	invRepo := repo.NewModelInvocationRepo(pool)
	invocation, err := invRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		InvocationPurpose:    "agent_turn",
		ModelName:            "gpt-4o-mini",
		RunID:                &runRecord.ID,
		RunStepID:            &step.ID,
		RunAttemptID:         &attempt.ID,
	})
	if err != nil {
		t.Fatalf("Create model_invocation: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM run WHERE id = $1`, runRecord.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}

	stored, err := invRepo.GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("GetByID model_invocation: %v", err)
	}
	if stored.RunID != nil {
		t.Fatalf("run_id = %v, want nil after ON DELETE SET NULL", stored.RunID)
	}
	if stored.RunStepID != nil {
		t.Fatalf("run_step_id = %v, want nil after ON DELETE SET NULL", stored.RunStepID)
	}
	if stored.RunAttemptID != nil {
		t.Fatalf("run_attempt_id = %v, want nil after ON DELETE SET NULL", stored.RunAttemptID)
	}
}

func seedControlPlaneOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "cp-org-" + uuid.NewString()[:8],
		DisplayName: "Control Plane Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}

func seedControlPlaneRun(t *testing.T, ctx context.Context, runRepo *RunRepository, orgID uuid.UUID) Run {
	t.Helper()
	runRecord, err := runRepo.Create(ctx, Run{
		OrganizationID: orgID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "created",
		TriggerType:    "api",
		Metadata:       jsonRaw(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return runRecord
}

func seedControlPlaneProviderConnection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (repo.ModelProvider, repo.ProviderConnection) {
	t.Helper()
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "cp-provider-" + uuid.NewString()[:8],
		DisplayName: "Control Plane Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	connection, err := repo.NewProviderConnectionRepo(pool).Create(ctx, repo.ProviderConnection{
		OrganizationID: orgID,
		ProviderID:     provider.ID,
		DisplayName:    "Control Plane Connection",
		APIKeyRef:      "ref:cp-provider",
		IsEnabled:      true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return provider, connection
}

func assertCountByID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, id uuid.UUID, want int) {
	t.Helper()
	var count int
	query := "SELECT COUNT(*) FROM " + table + " WHERE id = $1"
	if err := pool.QueryRow(ctx, query, id).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s row count = %d, want %d", table, count, want)
	}
}

func jsonRaw(value string) []byte {
	return []byte(value)
}
