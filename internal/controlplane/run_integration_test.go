//go:build integration

package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRun_Lifecycle_FullSuccess(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)

	now := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	svc := newRunServiceIntegrationWithClock(t, pool, fakeClock)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := svc.EmitHeartbeat(ctx, runRecord.ID, nil); err != nil {
			t.Fatalf("EmitHeartbeat #%d: %v", i+1, err)
		}
		fakeClock.Advance(30 * time.Second)
	}

	step1, err := svc.CreateStep(ctx, runRecord.ID, "tool.one", "tier2")
	if err != nil {
		t.Fatalf("CreateStep #1: %v", err)
	}
	if err := svc.StartStep(ctx, step1.ID); err != nil {
		t.Fatalf("StartStep #1: %v", err)
	}
	if err := svc.CompleteStep(ctx, step1.ID); err != nil {
		t.Fatalf("CompleteStep #1: %v", err)
	}

	step2, err := svc.CreateStep(ctx, runRecord.ID, "tool.two", "tier2")
	if err != nil {
		t.Fatalf("CreateStep #2: %v", err)
	}
	if err := svc.StartStep(ctx, step2.ID); err != nil {
		t.Fatalf("StartStep #2: %v", err)
	}
	if err := svc.CompleteStep(ctx, step2.ID); err != nil {
		t.Fatalf("CompleteStep #2: %v", err)
	}

	if err := svc.CompleteRun(ctx, runRecord.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}

	storedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if storedRun.Status != "completed" {
		t.Fatalf("run status = %s, want completed", storedRun.Status)
	}
	if storedRun.CompletedAt == nil {
		t.Fatal("run.completed_at is nil, want non-nil")
	}

	steps, err := NewRunStepRepository(pool).ListByRun(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("ListByRun steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
	if steps[0].StepNumber != 1 || steps[1].StepNumber != 2 {
		t.Fatalf("step numbers = [%d,%d], want [1,2]", steps[0].StepNumber, steps[1].StepNumber)
	}

	events, err := NewRunEventRepository(pool).ListByRun(ctx, runRecord.ID, 0)
	if err != nil {
		t.Fatalf("ListByRun events: %v", err)
	}
	required := map[string]bool{
		"run_started":    false,
		"step_started":   false,
		"step_completed": false,
		"run_completed":  false,
	}
	heartbeatCount := 0
	for _, event := range events {
		if _, ok := required[event.EventType]; ok {
			required[event.EventType] = true
		}
		if event.EventType == "heartbeat" {
			heartbeatCount++
		}
	}
	for eventType, found := range required {
		if !found {
			t.Fatalf("missing run_event type %q", eventType)
		}
	}
	if heartbeatCount < 3 {
		t.Fatalf("heartbeat count = %d, want >= 3", heartbeatCount)
	}
}

func TestRun_Lifecycle_AllStates(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	seenStatuses := map[string]bool{}

	cancelRun, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun cancel chain: %v", err)
	}
	seenStatuses[cancelRun.Status] = true
	if err := svc.StartRun(ctx, cancelRun.ID); err != nil {
		t.Fatalf("StartRun cancel chain: %v", err)
	}
	seenStatuses["in_progress"] = true
	if err := svc.PauseRun(ctx, cancelRun.ID); err != nil {
		t.Fatalf("PauseRun cancel chain: %v", err)
	}
	seenStatuses["paused"] = true
	if err := svc.ResumeRun(ctx, cancelRun.ID); err != nil {
		t.Fatalf("ResumeRun cancel chain: %v", err)
	}
	seenStatuses["in_progress"] = true
	if err := svc.RequestCancel(ctx, cancelRun.ID, CancelRequestActor{Type: "system"}); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	cancellingRun, err := NewRunRepository(pool).Get(ctx, cancelRun.ID)
	if err != nil {
		t.Fatalf("Get cancelling run: %v", err)
	}
	seenStatuses[cancellingRun.Status] = true
	if cancellingRun.Status != "cancelling" {
		t.Fatalf("status after cancel request = %s, want cancelling", cancellingRun.Status)
	}
	if err := svc.ConfirmCancelled(ctx, cancelRun.ID); err != nil {
		t.Fatalf("ConfirmCancelled: %v", err)
	}
	cancelledRun, err := NewRunRepository(pool).Get(ctx, cancelRun.ID)
	if err != nil {
		t.Fatalf("Get cancelled run: %v", err)
	}
	seenStatuses[cancelledRun.Status] = true

	completeRun, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun completed chain: %v", err)
	}
	seenStatuses[completeRun.Status] = true
	if err := svc.StartRun(ctx, completeRun.ID); err != nil {
		t.Fatalf("StartRun completed chain: %v", err)
	}
	if err := svc.CompleteRun(ctx, completeRun.ID, json.RawMessage(`{"done":true}`)); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}
	completedRun, err := NewRunRepository(pool).Get(ctx, completeRun.ID)
	if err != nil {
		t.Fatalf("Get completed run: %v", err)
	}
	seenStatuses[completedRun.Status] = true

	timedOutRun, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun timed_out chain: %v", err)
	}
	seenStatuses[timedOutRun.Status] = true
	if err := svc.StartRun(ctx, timedOutRun.ID); err != nil {
		t.Fatalf("StartRun timed_out chain: %v", err)
	}
	if err := svc.FailRun(ctx, timedOutRun.ID, "heartbeat silence", "timeout"); err != nil {
		t.Fatalf("FailRun timeout: %v", err)
	}
	updatedTimedOut, err := NewRunRepository(pool).Get(ctx, timedOutRun.ID)
	if err != nil {
		t.Fatalf("Get timed_out run: %v", err)
	}
	seenStatuses[updatedTimedOut.Status] = true

	deadLetterRun, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun dead_letter chain: %v", err)
	}
	seenStatuses[deadLetterRun.Status] = true
	if err := svc.StartRun(ctx, deadLetterRun.ID); err != nil {
		t.Fatalf("StartRun dead_letter chain: %v", err)
	}
	if err := svc.FailRun(ctx, deadLetterRun.ID, "step failed", "transient"); err != nil {
		t.Fatalf("FailRun transient: %v", err)
	}
	failedRun, err := NewRunRepository(pool).Get(ctx, deadLetterRun.ID)
	if err != nil {
		t.Fatalf("Get failed run: %v", err)
	}
	seenStatuses[failedRun.Status] = true
	if err := svc.DeadLetter(ctx, deadLetterRun.ID); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	deadLetteredRun, err := NewRunRepository(pool).Get(ctx, deadLetterRun.ID)
	if err != nil {
		t.Fatalf("Get dead_letter run: %v", err)
	}
	seenStatuses[deadLetteredRun.Status] = true

	for _, status := range []string{
		"created", "in_progress", "paused", "cancelling", "cancelled", "completed", "failed", "timed_out", "dead_letter",
	} {
		if !seenStatuses[status] {
			t.Fatalf("missing observed run status %q", status)
		}
	}

	cancelEvents, err := NewRunEventRepository(pool).ListByRun(ctx, cancelRun.ID, 0)
	if err != nil {
		t.Fatalf("List cancel run events: %v", err)
	}
	seenEventTypes := map[string]bool{}
	for _, event := range cancelEvents {
		seenEventTypes[event.EventType] = true
	}
	for _, eventType := range []string{"run_started", "run_paused", "run_cancelled"} {
		if !seenEventTypes[eventType] {
			t.Fatalf("missing cancel-chain run_event type %q", eventType)
		}
	}

	assertRunEventTypes(t, pool, completeRun.ID, "run_started", "run_completed")
	assertRunEventTypes(t, pool, timedOutRun.ID, "run_started", "run_timed_out")
	assertRunEventTypes(t, pool, deadLetterRun.ID, "run_started", "run_failed")
}

func TestRun_Lifecycle_TimedOut(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)

	now := time.Date(2026, 2, 25, 13, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	svc := newRunServiceIntegrationWithClock(t, pool, fakeClock)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := svc.PauseRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("PauseRun: %v", err)
	}
	if err := svc.FailRun(ctx, runRecord.ID, "paused timeout exceeded", "timeout"); err != nil {
		t.Fatalf("FailRun timeout: %v", err)
	}

	updatedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "timed_out" {
		t.Fatalf("run status = %s, want timed_out", updatedRun.Status)
	}
}

func TestRun_Lifecycle_DeadLetter(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	projectRecord, taskRecord := seedRunProjectTaskWithPM(t, ctx, pool, org.ID)
	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		ProjectID:      &projectRecord.ID,
		TaskID:         &taskRecord.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	step, err := svc.CreateStep(ctx, runRecord.ID, "mcp.execute", "tier2")
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	attemptRepo := NewRunAttemptRepository(pool)
	worker := "mcp"
	for attemptNumber := 1; attemptNumber <= 3; attemptNumber++ {
		failureClass := "transient"
		failureReason := "failure"
		if _, err := attemptRepo.Create(ctx, RunAttempt{
			RunStepID:     step.ID,
			AttemptNumber: attemptNumber,
			Trigger:       "retry_transient",
			Status:        "failed",
			FailureClass:  &failureClass,
			FailureReason: &failureReason,
			WorkerType:    &worker,
			Metadata:      json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("seed run_attempt #%d: %v", attemptNumber, err)
		}
	}

	if _, err := svc.CreateRetryAttempt(ctx, step.ID, "retry_transient"); !errors.Is(err, ErrMaxAttemptsExceeded) {
		t.Fatalf("CreateRetryAttempt error = %v, want ErrMaxAttemptsExceeded", err)
	}

	updatedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "dead_letter" {
		t.Fatalf("run status = %s, want dead_letter", updatedRun.Status)
	}

	var taskEventCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'run_dead_lettered'
	`, taskRecord.ID).Scan(&taskEventCount); err != nil {
		t.Fatalf("count project_task_event: %v", err)
	}
	if taskEventCount == 0 {
		t.Fatal("expected project_task_event run_dead_lettered row")
	}

	var inboxCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM inbox_item
		WHERE source_task_id = $1
		  AND (
		    item_type = 'escalation'
		    OR (item_type = 'system_alert' AND action_payload->>'requested_type' = 'escalation')
		  )
	`, taskRecord.ID).Scan(&inboxCount); err != nil {
		t.Fatalf("count escalation inbox items: %v", err)
	}
	if inboxCount == 0 {
		t.Fatal("expected escalation inbox item for dead-letter run")
	}
}

func TestRun_Retry_NewAttempt(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	step, err := svc.CreateStep(ctx, runRecord.ID, "mcp.execute", "tier2")
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	if err := svc.StartStep(ctx, step.ID); err != nil {
		t.Fatalf("StartStep: %v", err)
	}

	attemptRepo := NewRunAttemptRepository(pool)
	workerType := "mcp"
	failureClass := "transient"
	failureReason := "network timeout"
	attempt1, err := attemptRepo.Create(ctx, RunAttempt{
		RunStepID:     step.ID,
		AttemptNumber: 1,
		Trigger:       "initial",
		Status:        "failed",
		WorkerType:    &workerType,
		FailureClass:  &failureClass,
		FailureReason: &failureReason,
		Metadata:      json.RawMessage(`{"attempt":"one"}`),
	})
	if err != nil {
		t.Fatalf("seed attempt 1: %v", err)
	}

	attempt2, err := svc.CreateRetryAttempt(ctx, step.ID, "retry_transient")
	if err != nil {
		t.Fatalf("CreateRetryAttempt: %v", err)
	}
	if attempt2.AttemptNumber != 2 {
		t.Fatalf("attempt number = %d, want 2", attempt2.AttemptNumber)
	}

	allAttempts, err := attemptRepo.ListByStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("ListByStep: %v", err)
	}
	if len(allAttempts) != 2 {
		t.Fatalf("attempt rows = %d, want 2", len(allAttempts))
	}

	preservedAttempt1, err := attemptRepo.Get(ctx, attempt1.ID)
	if err != nil {
		t.Fatalf("Get attempt 1: %v", err)
	}
	if preservedAttempt1.Status != "failed" {
		t.Fatalf("attempt 1 status = %s, want failed", preservedAttempt1.Status)
	}
	if preservedAttempt1.FailureClass == nil || *preservedAttempt1.FailureClass != "transient" {
		t.Fatalf("attempt 1 failure_class = %v, want transient", preservedAttempt1.FailureClass)
	}
	if !preservedAttempt1.CreatedAt.Equal(attempt1.CreatedAt) {
		t.Fatalf("attempt 1 created_at changed from %s to %s", attempt1.CreatedAt, preservedAttempt1.CreatedAt)
	}

	if _, err := attemptRepo.UpdateStatus(ctx, attempt2.ID, "completed", nil, nil, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatalf("UpdateStatus attempt 2: %v", err)
	}
	if err := svc.CompleteStep(ctx, step.ID); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}
	if err := svc.CompleteRun(ctx, runRecord.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}

	finalRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if finalRun.Status != "completed" {
		t.Fatalf("run status = %s, want completed", finalRun.Status)
	}
}

func TestRun_Retry_PermanentFailure_NoRetry(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	step, err := svc.CreateStep(ctx, runRecord.ID, "mcp.execute", "tier2")
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	attemptRepo := NewRunAttemptRepository(pool)
	workerType := "mcp"
	failureClass := "permanent"
	failureReason := "invalid request"
	attempt1, err := attemptRepo.Create(ctx, RunAttempt{
		RunStepID:     step.ID,
		AttemptNumber: 1,
		Trigger:       "initial",
		Status:        "failed",
		WorkerType:    &workerType,
		FailureClass:  &failureClass,
		FailureReason: &failureReason,
		Metadata:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("seed attempt 1: %v", err)
	}

	decider := RetryDecider{}
	shouldRetry, trigger := decider.ShouldRetry(ToolExecution{ToolDomain: "mcp"}, attempt1)
	if shouldRetry {
		t.Fatalf("ShouldRetry = true, want false (trigger=%q)", trigger)
	}

	attempts, err := attemptRepo.ListByStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("ListByStep: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempt rows = %d, want 1", len(attempts))
	}

	if err := svc.FailRun(ctx, runRecord.ID, "permanent failure", "permanent"); err != nil {
		t.Fatalf("FailRun permanent: %v", err)
	}
	updatedRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "failed" {
		t.Fatalf("run status = %s, want failed", updatedRun.Status)
	}

	storedAttempt, err := attemptRepo.Get(ctx, attempt1.ID)
	if err != nil {
		t.Fatalf("Get attempt 1: %v", err)
	}
	if storedAttempt.FailureClass == nil || *storedAttempt.FailureClass != "permanent" {
		t.Fatalf("attempt failure_class = %v, want permanent", storedAttempt.FailureClass)
	}
}

func TestRun_Idempotency(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(api.WithOrganizationID(r.Context(), org.ID)))
		})
	})
	router.Use(middleware.Idempotency(middleware.IdempotencyOptions{
		Repository: repo.NewIdempotencyKeyRepo(pool),
	}))
	router.Post("/v1/control/runs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IdempotencyKey *string `json:"idempotency_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		created, err := svc.CreateRun(r.Context(), CreateRunInput{
			OrganizationID: org.ID,
			PrincipalType:  "system",
			PrincipalID:    uuid.Nil,
			TriggerType:    "api",
			IdempotencyKey: req.IdempotencyKey,
			Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
		})
		if err != nil {
			http.Error(w, "create run failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id": created.ID.String(),
			},
		})
	})

	server := httptest.NewServer(router)
	defer server.Close()

	key := "run-idempotency-key"
	requestBody := map[string]any{"idempotency_key": key}
	first := postJSON(t, http.DefaultClient, server.URL+"/v1/control/runs", requestBody, map[string]string{
		"Idempotency-Key": key,
	})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want %d body=%s", first.StatusCode, http.StatusCreated, string(first.Body))
	}
	firstID := jsonStringField(t, first.Body, "data", "id")
	if firstID == "" {
		t.Fatalf("missing first run id body=%s", string(first.Body))
	}

	second := postJSON(t, http.DefaultClient, server.URL+"/v1/control/runs", requestBody, map[string]string{
		"Idempotency-Key": key,
	})
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second status = %d, want %d body=%s", second.StatusCode, http.StatusCreated, string(second.Body))
	}
	secondID := jsonStringField(t, second.Body, "data", "id")
	if secondID != firstID {
		t.Fatalf("second run id = %s, want existing %s", secondID, firstID)
	}

	var runCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND idempotency_key = $2
	`, org.ID, key).Scan(&runCount); err != nil {
		t.Fatalf("count runs by idempotency key: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("run count = %d, want 1", runCount)
	}

	var idempotencyRows int
	keyHash := hashIdempotencyKey(key)
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM idempotency_key
		WHERE organization_id = $1
		  AND key_hash = $2
	`, org.ID, keyHash).Scan(&idempotencyRows); err != nil {
		t.Fatalf("count idempotency rows: %v", err)
	}
	if idempotencyRows != 1 {
		t.Fatalf("idempotency_key rows = %d, want 1", idempotencyRows)
	}
}

func TestRun_OptimisticConcurrency(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	runRepo := NewRunRepository(pool)

	runRecord, err := runRepo.Create(ctx, Run{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "created",
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Create run: %v", err)
	}

	start := make(chan struct{})
	var (
		wg        sync.WaitGroup
		errs      = make(chan error, 2)
		conflicts int
		successes int
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, updateErr := runRepo.UpdateStatus(ctx, runRecord.ID, runRecord.Version, "in_progress", nil, nil)
			errs <- updateErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for updateErr := range errs {
		if updateErr == nil {
			successes++
			continue
		}
		if errors.Is(updateErr, ErrConflict) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected update error: %v", updateErr)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes/conflicts = %d/%d, want 1/1", successes, conflicts)
	}

	updated, err := runRepo.Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updated.Status != "in_progress" {
		t.Fatalf("final run status = %s, want in_progress", updated.Status)
	}
}

func TestRun_GracefulCancellation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedControlPlaneOrg(t, ctx, pool)
	svc := newRunServiceIntegration(t, pool)

	runRecord, err := svc.CreateRun(ctx, CreateRunInput{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Metadata:       json.RawMessage(`{"run_mode":"sync"}`),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := svc.StartRun(ctx, runRecord.ID); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	cancelSignals := make(chan uuid.UUID, 1)
	router := chi.NewRouter()
	router.Post("/v1/control/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		runID, parseErr := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
		if parseErr != nil {
			http.Error(w, "invalid run id", http.StatusBadRequest)
			return
		}
		actorID := uuid.New()
		if err := svc.RequestCancel(r.Context(), runID, CancelRequestActor{Type: "human_user", ID: actorID}); err != nil {
			http.Error(w, "cancel failed", http.StatusInternalServerError)
			return
		}
		cancelSignals <- runID
		w.WriteHeader(http.StatusAccepted)
	})
	server := httptest.NewServer(router)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/control/runs/"+runRecord.ID.String()+"/cancel", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new cancel request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	afterCancel, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run after cancel request: %v", err)
	}
	if afterCancel.Status != "cancelling" {
		t.Fatalf("run status = %s, want cancelling", afterCancel.Status)
	}

	select {
	case <-cancelSignals:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not receive cancel signal")
	}

	if err := svc.ConfirmCancelled(ctx, runRecord.ID); err != nil {
		t.Fatalf("ConfirmCancelled: %v", err)
	}
	finalRun, err := NewRunRepository(pool).Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run after confirm cancel: %v", err)
	}
	if finalRun.Status != "cancelled" {
		t.Fatalf("final run status = %s, want cancelled", finalRun.Status)
	}

	events, err := NewRunEventRepository(pool).ListByRun(ctx, runRecord.ID, 0)
	if err != nil {
		t.Fatalf("List run events: %v", err)
	}
	cancelEventCount := 0
	for _, event := range events {
		if event.EventType == "run_cancelled" {
			cancelEventCount++
		}
	}
	if cancelEventCount < 2 {
		t.Fatalf("run_cancelled event count = %d, want >= 2", cancelEventCount)
	}
}

func newRunServiceIntegrationWithClock(t *testing.T, pool *pgxpool.Pool, c clock.Clock) RunService {
	t.Helper()
	bus := eventbus.New(pool, newDiscardLogger(), eventbus.Config{})
	svc, err := NewRunService(RunServiceOptions{
		Pool:     pool,
		EventBus: bus,
		Policy:   allowRunCreationPolicy{},
		Clock:    c,
		Logger:   newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
	return svc
}

func seedRunProjectTaskWithPM(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (repo.Project, repo.ProjectTask) {
	t.Helper()

	projectRecord := seedRunProject(t, ctx, pool, orgID)
	taskRecord := seedRunTask(t, ctx, pool, orgID, projectRecord.ID)

	userRepo := repo.NewHumanUserRepo(pool)
	passwordHash := "integration-password-hash"
	pmUser, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: orgID,
		Email:          "pm+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "PM User",
		PasswordHash:   &passwordHash,
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create PM user: %v", err)
	}

	pmAgent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "PM Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "pm",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "human_user",
		CreatedByID:          pmUser.ID,
	})
	if err != nil {
		t.Fatalf("create PM agent: %v", err)
	}

	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        pmAgent.ID,
		ProjectID:      projectRecord.ID,
		Role:           "pm",
		AssignedByType: "human_user",
		AssignedByID:   &pmUser.ID,
	}); err != nil {
		t.Fatalf("assign PM agent: %v", err)
	}

	return projectRecord, taskRecord
}

type jsonHTTPResponse struct {
	StatusCode int
	Body       []byte
}

func postJSON(t *testing.T, client *http.Client, endpoint string, payload any, headers map[string]string) jsonHTTPResponse {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return jsonHTTPResponse{
		StatusCode: resp.StatusCode,
		Body:       body,
	}
}

func assertRunEventTypes(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID, expected ...string) {
	t.Helper()
	events, err := NewRunEventRepository(pool).ListByRun(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("ListByRun events for %s: %v", runID, err)
	}

	seen := make(map[string]bool, len(events))
	for _, event := range events {
		seen[event.EventType] = true
	}
	for _, eventType := range expected {
		if !seen[eventType] {
			t.Fatalf("run %s missing run_event type %q", runID, eventType)
		}
	}
}

func jsonStringField(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	for _, part := range path {
		typed, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("invalid json path %v in body: %s", path, string(body))
		}
		current = typed[part]
	}
	out, _ := current.(string)
	return out
}

func hashIdempotencyKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key) + "\n"))
	return hex.EncodeToString(sum[:])
}
