//go:build integration

package model_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	modelpkg "github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestInvocationRecorderAttributionRoundTripAndRunTokenIncrementIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, ctx, pool, "model-attribution-org")
	provider, connection := mustCreateProviderConnection(t, ctx, pool, org.ID)

	runRepo := controlplane.NewRunRepository(pool)
	stepRepo := controlplane.NewRunStepRepository(pool)
	attemptRepo := controlplane.NewRunAttemptRepository(pool)

	runRecord, err := runRepo.Create(ctx, controlplane.Run{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Status:         "created",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	step, err := stepRepo.Create(ctx, controlplane.RunStep{RunID: runRecord.ID, StepNumber: 1, Status: "pending", Metadata: []byte(`{}`)})
	if err != nil {
		t.Fatalf("create run_step: %v", err)
	}
	attempt, err := attemptRepo.Create(ctx, controlplane.RunAttempt{RunStepID: step.ID, AttemptNumber: 1, Trigger: "initial", Status: "pending", Metadata: []byte(`{}`)})
	if err != nil {
		t.Fatalf("create run_attempt: %v", err)
	}

	recorder, err := modelpkg.NewInvocationRecorder(modelpkg.InvocationRecorderOptions{
		Invocations: repo.NewModelInvocationRepo(pool),
		Attribution: modelpkg.NewAttributionMiddleware(),
		Rollup:      modelpkg.NewRollupUpdater(pool),
		AsyncSpawner: func(fn func()) {
			fn()
		},
	})
	if err != nil {
		t.Fatalf("new invocation recorder: %v", err)
	}

	runCtx := modelpkg.WithInvocationContext(ctx, modelpkg.InvocationContext{
		OrganizationID:    org.ID,
		RunID:             &runRecord.ID,
		RunStepID:         &step.ID,
		RunAttemptID:      &attempt.ID,
		InvocationPurpose: "agent_turn",
	})

	inputA := 100
	outputA := 25
	createdA, err := recorder.Create(runCtx, modelpkg.ModelInvocationInput{
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelName:            "gpt-4o-mini",
		Status:               "completed",
		InputTokens:          &inputA,
		OutputTokens:         &outputA,
	})
	if err != nil {
		t.Fatalf("create invocation A: %v", err)
	}
	if createdA.RunID == nil || *createdA.RunID != runRecord.ID {
		t.Fatalf("run_id = %v, want %s", createdA.RunID, runRecord.ID)
	}
	if createdA.RunStepID == nil || *createdA.RunStepID != step.ID {
		t.Fatalf("run_step_id = %v, want %s", createdA.RunStepID, step.ID)
	}
	if createdA.RunAttemptID == nil || *createdA.RunAttemptID != attempt.ID {
		t.Fatalf("run_attempt_id = %v, want %s", createdA.RunAttemptID, attempt.ID)
	}
	if createdA.InvocationPurpose != "agent_turn" {
		t.Fatalf("invocation_purpose = %q, want agent_turn", createdA.InvocationPurpose)
	}

	inputB := 200
	outputB := 30
	if _, err := recorder.Create(runCtx, modelpkg.ModelInvocationInput{
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelName:            "gpt-4o-mini",
		Status:               "completed",
		InputTokens:          &inputB,
		OutputTokens:         &outputB,
	}); err != nil {
		t.Fatalf("create invocation B: %v", err)
	}

	updatedRun, err := runRepo.Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updatedRun.InputTokens != 300 {
		t.Fatalf("run.input_tokens = %d, want 300", updatedRun.InputTokens)
	}
	if updatedRun.OutputTokens != 55 {
		t.Fatalf("run.output_tokens = %d, want 55", updatedRun.OutputTokens)
	}
	updatedStep, err := stepRepo.Get(ctx, step.ID)
	if err != nil {
		t.Fatalf("get run_step: %v", err)
	}
	if updatedStep.InputTokens != 300 || updatedStep.OutputTokens != 55 {
		t.Fatalf("run_step token totals = (%d,%d), want (300,55)", updatedStep.InputTokens, updatedStep.OutputTokens)
	}
	updatedAttempt, err := attemptRepo.Get(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get run_attempt: %v", err)
	}
	if updatedAttempt.InputTokens != 300 || updatedAttempt.OutputTokens != 55 {
		t.Fatalf("run_attempt token totals = (%d,%d), want (300,55)", updatedAttempt.InputTokens, updatedAttempt.OutputTokens)
	}
}

func TestInvocationRecorderMemoryExtractionPurposeIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, ctx, pool, "memory-extract-org")
	provider, connection := mustCreateProviderConnection(t, ctx, pool, org.ID)

	recorder, err := modelpkg.NewInvocationRecorder(modelpkg.InvocationRecorderOptions{
		Invocations: repo.NewModelInvocationRepo(pool),
		Attribution: modelpkg.NewAttributionMiddleware(),
	})
	if err != nil {
		t.Fatalf("new invocation recorder: %v", err)
	}

	extractCtx := modelpkg.WithInvocationContext(ctx, modelpkg.InvocationContext{
		OrganizationID:    org.ID,
		InvocationPurpose: "memory_extraction",
	})

	created, err := recorder.Create(extractCtx, modelpkg.ModelInvocationInput{
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelName:            "gpt-4o-mini",
		Status:               "completed",
	})
	if err != nil {
		t.Fatalf("create invocation: %v", err)
	}
	if created.InvocationPurpose != "memory_extraction" {
		t.Fatalf("invocation_purpose = %q, want memory_extraction", created.InvocationPurpose)
	}
	if created.RunID != nil || created.RunStepID != nil || created.RunAttemptID != nil {
		t.Fatalf("expected standalone invocation run fields to be nil, got run=%v step=%v attempt=%v", created.RunID, created.RunStepID, created.RunAttemptID)
	}
}

func TestDailyRollupJobRunIntegrationIdempotentAgentProjectGrouping(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, ctx, pool, "daily-rollup-org")
	provider, connection := mustCreateProviderConnection(t, ctx, pool, org.ID)
	agentA := mustCreateAgent(t, ctx, pool, org.ID, "Rollup Agent A")
	agentB := mustCreateAgent(t, ctx, pool, org.ID, "Rollup Agent B")
	project := mustCreateProject(t, ctx, pool, org.ID, "daily-rollup-project")

	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	yesterdayStart := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)

	mustInsertInvocationAt(t, ctx, pool, org.ID, provider.ID, connection.ID, &agentA.ID, &project.ID, 10, 5, yesterdayStart.Add(1*time.Hour))
	mustInsertInvocationAt(t, ctx, pool, org.ID, provider.ID, connection.ID, &agentA.ID, &project.ID, 20, 10, yesterdayStart.Add(2*time.Hour))
	mustInsertInvocationAt(t, ctx, pool, org.ID, provider.ID, connection.ID, &agentA.ID, &project.ID, 30, 15, yesterdayStart.Add(3*time.Hour))
	mustInsertInvocationAt(t, ctx, pool, org.ID, provider.ID, connection.ID, &agentB.ID, &project.ID, 40, 20, yesterdayStart.Add(4*time.Hour))
	mustInsertInvocationAt(t, ctx, pool, org.ID, provider.ID, connection.ID, &agentB.ID, &project.ID, 50, 25, yesterdayStart.Add(5*time.Hour))

	dailyJob, err := modelpkg.NewDailyRollupJob(modelpkg.DailyRollupJobOptions{
		Pool: pool,
		Now:  func() time.Time { return yesterdayStart.Add(30 * time.Hour) },
	})
	if err != nil {
		t.Fatalf("new daily rollup job: %v", err)
	}

	if err := dailyJob.Run(ctx, jobqueue.Job{}); err != nil {
		t.Fatalf("daily rollup first run: %v", err)
	}
	if err := dailyJob.Run(ctx, jobqueue.Job{}); err != nil {
		t.Fatalf("daily rollup second run: %v", err)
	}

	rollupRepo := repo.NewModelUsageRollupRepo(pool)
	rows, err := rollupRepo.GetForDate(ctx, org.ID, yesterdayStart)
	if err != nil {
		t.Fatalf("get rollups for date: %v", err)
	}

	agentRows := make(map[uuid.UUID]repo.ModelUsageRollup)
	projectRows := make(map[uuid.UUID]repo.ModelUsageRollup)
	for _, row := range rows {
		if row.RollupType == "agent" && row.RollupID != nil {
			agentRows[*row.RollupID] = row
		}
		if row.RollupType == "project" && row.RollupID != nil {
			projectRows[*row.RollupID] = row
		}
	}

	if len(agentRows) != 2 {
		t.Fatalf("agent rollup rows = %d, want 2", len(agentRows))
	}
	if row := agentRows[agentA.ID]; row.TotalInputTokens != 60 || row.TotalOutputTokens != 30 {
		t.Fatalf("agent A totals = (%d,%d), want (60,30)", row.TotalInputTokens, row.TotalOutputTokens)
	}
	if row := agentRows[agentB.ID]; row.TotalInputTokens != 90 || row.TotalOutputTokens != 45 {
		t.Fatalf("agent B totals = (%d,%d), want (90,45)", row.TotalInputTokens, row.TotalOutputTokens)
	}
	if len(projectRows) != 1 {
		t.Fatalf("project rollup rows = %d, want 1", len(projectRows))
	}
	if row := projectRows[project.ID]; row.TotalInputTokens != 150 || row.TotalOutputTokens != 75 {
		t.Fatalf("project totals = (%d,%d), want (150,75)", row.TotalInputTokens, row.TotalOutputTokens)
	}

	var agentRowCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM model_usage_rollup
		WHERE organization_id = $1
		  AND rollup_date = $2
		  AND rollup_type = 'agent'
	`, org.ID, yesterdayStart).Scan(&agentRowCount); err != nil {
		t.Fatalf("count agent rollup rows: %v", err)
	}
	if agentRowCount != 2 {
		t.Fatalf("agent rollup row count in table = %d, want 2", agentRowCount)
	}
}

func TestCostQuerySumForRunIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, ctx, pool, "cost-query-org")
	provider, connection := mustCreateProviderConnectionWithCosts(t, ctx, pool, org.ID, 2.0, 4.0)

	runRecord, err := controlplane.NewRunRepository(pool).Create(ctx, controlplane.Run{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Status:         "created",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	invocationRepo := repo.NewModelInvocationRepo(pool)
	for _, item := range []struct{ input, output int }{{100, 50}, {200, 100}, {300, 150}} {
		input := item.input
		output := item.output
		if _, err := invocationRepo.Create(ctx, repo.ModelInvocation{
			OrganizationID:       org.ID,
			ModelProviderID:      provider.ID,
			ProviderConnectionID: &connection.ID,
			InvocationPurpose:    "agent_turn",
			Status:               "completed",
			ModelName:            "gpt-4o-mini",
			InputTokens:          &input,
			OutputTokens:         &output,
			RunID:                &runRecord.ID,
		}); err != nil {
			t.Fatalf("create model_invocation: %v", err)
		}
	}

	query := modelpkg.NewCostQuery(pool)
	summary, err := query.SumForRun(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("SumForRun: %v", err)
	}
	if summary.InputTokens != 600 {
		t.Fatalf("input_tokens = %d, want 600", summary.InputTokens)
	}
	if summary.OutputTokens != 300 {
		t.Fatalf("output_tokens = %d, want 300", summary.OutputTokens)
	}
	if summary.TotalTokens != 900 {
		t.Fatalf("total_tokens = %d, want 900", summary.TotalTokens)
	}
	if summary.EstimatedCostCents != 2 {
		t.Fatalf("estimated_cost_cents = %d, want 2", summary.EstimatedCostCents)
	}

	emptySummary, err := query.SumForRun(ctx, uuid.New())
	if err != nil {
		t.Fatalf("SumForRun empty: %v", err)
	}
	if emptySummary != (modelpkg.TokenSummary{}) {
		t.Fatalf("empty summary = %+v, want zero value", emptySummary)
	}
}

func mustCreateOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: slug + "-" + uuid.NewString()[:8], DisplayName: "Model Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}

func mustCreateProviderConnection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (repo.ModelProvider, repo.ProviderConnection) {
	t.Helper()
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "provider-" + uuid.NewString()[:8],
		DisplayName: "Model Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model_provider: %v", err)
	}
	connection, err := repo.NewProviderConnectionRepo(pool).Create(ctx, repo.ProviderConnection{
		OrganizationID: orgID,
		ProviderID:     provider.ID,
		DisplayName:    "Default Connection",
		APIKeyRef:      "ref:provider",
		IsEnabled:      true,
	})
	if err != nil {
		t.Fatalf("create provider_connection: %v", err)
	}
	return provider, connection
}

func mustCreateProviderConnectionWithCosts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, inputCostPer1K, outputCostPer1K float64) (repo.ModelProvider, repo.ProviderConnection) {
	t.Helper()
	metadata, _ := json.Marshal(map[string]any{
		"input_cost_per_1k":  inputCostPer1K,
		"output_cost_per_1k": outputCostPer1K,
	})
	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "provider-cost-" + uuid.NewString()[:8],
		DisplayName: "Cost Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
		Metadata:    metadata,
	})
	if err != nil {
		t.Fatalf("create model_provider: %v", err)
	}
	connection, err := repo.NewProviderConnectionRepo(pool).Create(ctx, repo.ProviderConnection{
		OrganizationID: orgID,
		ProviderID:     provider.ID,
		DisplayName:    "Cost Connection",
		APIKeyRef:      "ref:provider-cost",
		IsEnabled:      true,
	})
	if err != nil {
		t.Fatalf("create provider_connection: %v", err)
	}
	return provider, connection
}

func mustCreateAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, name string) repo.Agent {
	t.Helper()
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          name,
		AgentClass:           "staff",
		LifecycleStatus:      "draft",
		SystemPrompt:         "",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func mustCreateProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, slug string) repo.Project {
	t.Helper()
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           slug + "-" + uuid.NewString()[:8],
		DisplayName:    "Rollup Project",
		Description:    "",
		DeliveryMode:   "gated",
		Settings:       []byte(`{}`),
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func mustInsertInvocationAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, providerID, connectionID uuid.UUID, agentID, projectID *uuid.UUID, inputTokens, outputTokens int, createdAt time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (
			organization_id,
			model_provider_id,
			provider_connection_id,
			invocation_purpose,
			status,
			model_name,
			input_tokens,
			output_tokens,
			agent_id,
			project_id,
			created_at
		)
		VALUES ($1, $2, $3, 'agent_turn', 'completed', 'gpt-4o-mini', $4, $5, $6, $7, $8)
	`, orgID, providerID, connectionID, inputTokens, outputTokens, agentID, projectID, createdAt)
	if err != nil {
		t.Fatalf("insert model_invocation row: %v", err)
	}
}
