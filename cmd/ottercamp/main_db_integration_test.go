//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

func TestDBMigrateDryRunAndStatus(t *testing.T) {
	pool := testdb.New(t)
	t.Setenv("OTTERCAMP_DATABASE_URL", pool.Config().ConnString())

	migrationsDir := t.TempDir()
	migrationPath := filepath.Join(migrationsDir, "9999_cli_dry_run_probe.sql")
	if err := os.WriteFile(migrationPath, []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatalf("WriteFile migration: %v", err)
	}
	t.Setenv("OTTERCAMP_MIGRATIONS_PATH", migrationsDir)

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runDBMigrate([]string{"--dry-run"})
	})
	if code != 0 {
		t.Fatalf("db migrate --dry-run exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "pending 9999_cli_dry_run_probe") {
		t.Fatalf("db migrate --dry-run output = %q", stdout)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE version = 9999`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected dry-run not to apply migration, count=%d", count)
	}

	applyCode, applyOut, applyErr := captureCommandOutput(t, func() int {
		return runDBMigrate([]string{})
	})
	if applyCode != 0 {
		t.Fatalf("db migrate apply exit=%d stderr=%q", applyCode, applyErr)
	}
	if !strings.Contains(applyOut, "Applying 9999_cli_dry_run_probe... done (") {
		t.Fatalf("db migrate apply output = %q", applyOut)
	}

	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE version = 9999`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations after apply: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected apply to record migration, count=%d", count)
	}

	statusCode, statusOut, statusErr := captureCommandOutput(t, func() int {
		return runDBStatus([]string{"--output", "json"})
	})
	if statusCode != 0 {
		t.Fatalf("db status exit=%d stderr=%q", statusCode, statusErr)
	}
	if !strings.Contains(statusOut, `"version": 9999`) {
		t.Fatalf("db status output missing target migration: %q", statusOut)
	}
	if !strings.Contains(statusOut, `"applied": true`) {
		t.Fatalf("db status output missing applied flag: %q", statusOut)
	}
}

func TestDBTokenUsageJSONIncludesCacheReadsAndAttribution(t *testing.T) {
	pool := testdb.New(t)
	t.Setenv("OTTERCAMP_DATABASE_URL", pool.Config().ConnString())

	ctx := context.Background()
	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	sessionRepo := repo.NewChatSessionRepo(pool)
	messageRepo := repo.NewChatMessageRepo(pool)
	turnRepo := repo.NewChatTurnRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "cli-token-usage-org",
		DisplayName: "CLI Token Usage Org",
	})
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "cli-token-usage-provider",
		DisplayName: "Anthropic CLI Test",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	createdAt := time.Now().UTC()
	retryUntil := createdAt.Add(30 * time.Minute).Format(time.RFC3339Nano)
	connection, err := connectionRepo.Create(ctx, repo.ProviderConnection{
		OrganizationID:   org.ID,
		ProviderID:       provider.ID,
		DisplayName:      "Anthropic Primary",
		APIKeyRef:        "anthropic/cli-token-usage",
		FailoverPriority: 10,
		MaxConcurrent:    8,
		HealthStatus:     "rate_limited",
		IsEnabled:        true,
		Metadata:         json.RawMessage(`{"health_rate_limited_until":"` + retryUntil + `"}`),
	})
	if err != nil {
		t.Fatalf("create provider connection: %v", err)
	}
	agent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "CLI Token Usage Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "test",
		OperatorInstructions: "test",
		AgentType:            "pm",
		CreatedByType:        "system",
		CreatedByID:          uuid.New(),
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project := testutil.MakeProject(t, pool, org.ID)
	task := testutil.MakeTask(t, pool, project.ID, testutil.MakeTaskOptions{})
	runRecord, err := controlplane.NewRunRepository(pool).Create(ctx, controlplane.Run{
		OrganizationID: org.ID,
		ProjectID:      &project.ID,
		TaskID:         &task.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "in_progress",
		TriggerType:    "api",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	step, err := controlplane.NewRunStepRepository(pool).Create(ctx, controlplane.RunStep{
		RunID:      runRecord.ID,
		StepNumber: 1,
		Status:     "in_progress",
		Metadata:   []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run step: %v", err)
	}
	session, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	stopReason := "validation_loop_blocked"
	startedAt := createdAt.Add(-2 * time.Minute)
	completedAt := createdAt.Add(-1 * time.Minute)
	turn, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "completed",
		StartedAt:      &startedAt,
		CompletedAt:    &completedAt,
		StopReason:     &stopReason,
	})
	if err != nil {
		t.Fatalf("create chat turn: %v", err)
	}
	synthMessage1, err := messageRepo.Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now.",
		Status:    "pending",
		CreatedAt: createdAt.Add(90 * time.Second),
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	})
	if err != nil {
		t.Fatalf("create first synthetic message: %v", err)
	}
	synthStopReason := "validation_loop_blocked"
	synthStartedAt := createdAt.Add(95 * time.Second)
	synthCompletedAt := createdAt.Add(100 * time.Second)
	if _, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       2,
		TriggerMessageID: &synthMessage1.ID,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		StartedAt:        &synthStartedAt,
		CompletedAt:      &synthCompletedAt,
		StopReason:       &synthStopReason,
	}); err != nil {
		t.Fatalf("create synthetic continuation turn: %v", err)
	}
	if _, err := messageRepo.Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the active project execution now.",
		Status:    "pending",
		CreatedAt: createdAt.Add(110 * time.Second),
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","synthetic_user_message":true}`),
	}); err != nil {
		t.Fatalf("create second synthetic message: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (
			organization_id, model_provider_id, invocation_purpose, status, model_name,
			input_tokens, output_tokens, cache_read_tokens, session_id, turn_id, created_at
		) VALUES ($1, $2, 'agent_turn', 'completed', 'claude-opus-4-6', $3, $4, $5, $6, $7, $8)
	`, org.ID, provider.ID, 100, 25, 50, session.ID, turn.ID, createdAt); err != nil {
		t.Fatalf("insert completed invocation: %v", err)
	}
	inFlightInvocationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (
			id, organization_id, model_provider_id, provider_connection_id, invocation_purpose, status, model_name,
			is_streaming, input_tokens, output_tokens, cache_read_tokens, session_id, turn_id, created_at
		) VALUES ($1, $2, $3, $4, 'agent_turn', 'in_flight', 'claude-opus-4-6', true, $5, $6, $7, $8, $9, $10)
	`, inFlightInvocationID, org.ID, provider.ID, connection.ID, 11, 0, 0, session.ID, turn.ID, createdAt.Add(30*time.Second)); err != nil {
		t.Fatalf("insert in-flight invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (
			organization_id, model_provider_id, invocation_purpose, status, model_name,
			input_tokens, output_tokens, cache_read_tokens, session_id, turn_id, created_at, error_code, error_message
		) VALUES ($1, $2, 'summarization', 'failed', 'claude-haiku-4-5-20251001', $3, $4, $5, $6, $7, $8, 'provider_rate_limited', '429 rate limit')
	`, org.ID, provider.ID, 10, 5, 15, session.ID, turn.ID, createdAt.Add(time.Minute)); err != nil {
		t.Fatalf("insert pre-routing failed invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (
			organization_id, model_provider_id, provider_connection_id, invocation_purpose, status, model_name,
			input_tokens, output_tokens, cache_read_tokens, session_id, turn_id, created_at, error_code, error_message
		) VALUES ($1, $2, $3, 'agent_turn', 'failed', 'claude-opus-4-6', $4, $5, $6, $7, $8, $9, 'provider_rate_limited', '429 rate limit')
	`, org.ID, provider.ID, connection.ID, 7, 3, 5, session.ID, turn.ID, createdAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("insert post-routing failed invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO job_queue (
			job_type, priority, payload, status, attempts, max_attempts, run_after, created_at, updated_at
		) VALUES ('agent_turn', 50, $1::jsonb, 'pending', 0, 5, $2, $3, $3)
	`, `{"session_id":"`+session.ID.String()+`"}`, createdAt.Add(5*time.Minute), createdAt); err != nil {
		t.Fatalf("insert pending job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cli_execution (
			run_id, run_step_id, task_id, project_id, agent_id, command, working_directory,
			risk_level, policy_decision, env_vars_used, started_at, completed_at, duration_ms, metadata, created_at
		) VALUES
			($1, $2, $3, $4, $5, 'cat scripts/demo.sh', '/tmp/task-worktrees/demo/task-1', 'low', 'allowed', '{}'::jsonb, $6, $6, 10, '{}'::jsonb, $6),
			($1, $2, $3, $4, $5, 'cat scripts/demo.sh', '/tmp/workspaces/demo', 'low', 'allowed', '{}'::jsonb, $7, $7, 10, '{}'::jsonb, $7)
	`, runRecord.ID, step.ID, task.ID, project.ID, agent.ID, createdAt.Add(3*time.Minute), createdAt.Add(4*time.Minute)); err != nil {
		t.Fatalf("insert cli executions: %v", err)
	}
	for idx, message := range []repo.ChatMessage{
		{
			SessionID: session.ID,
			TurnID:    &turn.ID,
			Role:      "system",
			Status:    "final",
			Content:   "[Repeated identical file.list validation failure in this turn (2/3): recovery_target_focus_required. Ending the turn early so the next continuation can take a narrower step.]",
		},
		{
			SessionID: session.ID,
			TurnID:    &turn.ID,
			Role:      "assistant",
			Status:    "final",
			Content:   "Trying pip directly.",
			Metadata:  json.RawMessage(`{"tool_calls":[{"id":"pkg-1","name":"cli_execute","arguments":{"command":"pip install pyyaml 2>&1 | tail -3"}}]}`),
		},
		{
			SessionID: session.ID,
			TurnID:    &turn.ID,
			Role:      "assistant",
			Status:    "final",
			Content:   "Trying python -m pip.",
			Metadata:  json.RawMessage(`{"tool_calls":[{"id":"pkg-2","name":"cli_execute","arguments":{"command":"python3 -m pip install --user pyyaml 2>&1 | tail -5"}}]}`),
		},
		{
			SessionID: session.ID,
			TurnID:    &turn.ID,
			Role:      "assistant",
			Status:    "final",
			Content:   "Building the helper script.",
			Metadata:  json.RawMessage(`{"tool_calls":[{"id":"build-1","name":"cli_execute","arguments":{"command":"cat > scripts/demo.sh <<'EOF'\necho ok\nEOF"}}]}`),
		},
		{
			SessionID: session.ID,
			TurnID:    &turn.ID,
			Role:      "assistant",
			Status:    "final",
			Content:   "Reading the helper script back.",
			Metadata:  json.RawMessage(`{"tool_calls":[{"id":"read-1","name":"cli_execute","arguments":{"command":"cat scripts/demo.sh"}}]}`),
		},
	} {
		created, err := messageRepo.Create(ctx, message)
		if err != nil {
			t.Fatalf("create chat message %d: %v", idx, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE chat_message SET created_at = $2, updated_at = $2 WHERE id = $1`, created.ID, createdAt.Add(time.Duration(idx+1)*time.Second)); err != nil {
			t.Fatalf("stamp chat message %d: %v", idx, err)
		}
	}

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runDBTokenUsage([]string{"--output", "json", "--hours", "24", "--limit", "5", "--org", org.ID.String()})
	})
	if code != 0 {
		t.Fatalf("db token-usage exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"total_tokens": 231`) {
		t.Fatalf("db token-usage output missing total tokens: %q", stdout)
	}
	if !strings.Contains(stdout, `"cache_read_tokens": 70`) {
		t.Fatalf("db token-usage output missing cache read tokens: %q", stdout)
	}
	if !strings.Contains(stdout, `"rate_limited_failures": 2`) {
		t.Fatalf("db token-usage output missing rate-limited failures: %q", stdout)
	}
	if !strings.Contains(stdout, session.ID.String()) {
		t.Fatalf("db token-usage output missing session id: %q", stdout)
	}
	if !strings.Contains(stdout, turn.ID.String()) {
		t.Fatalf("db token-usage output missing turn id: %q", stdout)
	}
	if !strings.Contains(stdout, `"in_flight_agent_turns"`) || !strings.Contains(stdout, inFlightInvocationID.String()) {
		t.Fatalf("db token-usage output missing in-flight agent turn section: %q", stdout)
	}
	if !strings.Contains(stdout, `"completed_by_stop_reason"`) || !strings.Contains(stdout, stopReason) {
		t.Fatalf("db token-usage output missing completed-by-stop-reason section: %q", stdout)
	}
	if !strings.Contains(stdout, `"provider_health"`) || !strings.Contains(stdout, connection.DisplayName) {
		t.Fatalf("db token-usage output missing provider health section: %q", stdout)
	}
	if !strings.Contains(stdout, `"effective_health_status": "rate_limited"`) {
		t.Fatalf("db token-usage output missing effective provider health: %q", stdout)
	}
	if !strings.Contains(stdout, `"recovery_ready_at": "`+retryUntil+`"`) {
		t.Fatalf("db token-usage output missing provider recovery_ready_at: %q", stdout)
	}
	if !strings.Contains(stdout, `"rate_limit_routing_split"`) || !strings.Contains(stdout, `pre_routing`) || !strings.Contains(stdout, `post_routing`) {
		t.Fatalf("db token-usage output missing routing split section: %q", stdout)
	}
	if !strings.Contains(stdout, `"pending_agent_turn_backlog"`) || !strings.Contains(stdout, `"backlog_state": "ready"`) {
		t.Fatalf("db token-usage output missing pending backlog section: %q", stdout)
	}
	if !strings.Contains(stdout, `"repeated_package_installs"`) || !strings.Contains(stdout, `pyyaml`) {
		t.Fatalf("db token-usage output missing repeated package installs section: %q", stdout)
	}
	if !strings.Contains(stdout, `"shell_file_build_readback_churn"`) || !strings.Contains(stdout, `scripts/demo.sh`) {
		t.Fatalf("db token-usage output missing shell build/readback section: %q", stdout)
	}
	if !strings.Contains(stdout, `"task_cli_working_directory_roots"`) || !strings.Contains(stdout, `task_worktree`) || !strings.Contains(stdout, `project_workspace`) {
		t.Fatalf("db token-usage output missing cli working directory root section: %q", stdout)
	}
	if !strings.Contains(stdout, `"recent_validation_loop_blocks"`) || !strings.Contains(stdout, `recovery_target_focus_required`) {
		t.Fatalf("db token-usage output missing recent validation-loop blocks section: %q", stdout)
	}
	if !strings.Contains(stdout, `"repeated_synthetic_prompts"`) || !strings.Contains(stdout, `"synthetic_prompts": 2`) {
		t.Fatalf("db token-usage output missing repeated synthetic prompt section: %q", stdout)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("unmarshal db token-usage output: %v", err)
	}
	repeatedPackageInstalls, ok := payload["repeated_package_installs"].([]any)
	if !ok || len(repeatedPackageInstalls) != 1 {
		t.Fatalf("unexpected repeated_package_installs payload: %#v", payload["repeated_package_installs"])
	}
	row, ok := repeatedPackageInstalls[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected repeated_package_installs row: %#v", repeatedPackageInstalls[0])
	}
	repeatedSyntheticPrompts, ok := payload["repeated_synthetic_prompts"].([]any)
	if !ok || len(repeatedSyntheticPrompts) != 1 {
		t.Fatalf("unexpected repeated_synthetic_prompts payload: %#v", payload["repeated_synthetic_prompts"])
	}
	synthRow, ok := repeatedSyntheticPrompts[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected repeated_synthetic_prompts row: %#v", repeatedSyntheticPrompts[0])
	}
	if synthRow["source"] != "project_execution_continuation" {
		t.Fatalf("unexpected repeated_synthetic_prompts source: %#v", synthRow["source"])
	}
	if synthRow["validation_blocked_turns"] != float64(1) {
		t.Fatalf("unexpected repeated_synthetic_prompts validation_blocked_turns: %#v", synthRow["validation_blocked_turns"])
	}
	if got, _ := row["attempted_specs"].(string); got != "pyyaml" {
		t.Fatalf("attempted_specs = %q, want %q", got, "pyyaml")
	}
	shellChurnRows, ok := payload["shell_file_build_readback_churn"].([]any)
	if !ok || len(shellChurnRows) != 1 {
		t.Fatalf("unexpected shell_file_build_readback_churn payload: %#v", payload["shell_file_build_readback_churn"])
	}
	shellRow, ok := shellChurnRows[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected shell_file_build_readback_churn row: %#v", shellChurnRows[0])
	}
	if got, _ := shellRow["path_hints"].(string); got != "scripts/demo.sh" {
		t.Fatalf("path_hints = %q, want %q", got, "scripts/demo.sh")
	}
	cliRootRows, ok := payload["task_cli_working_directory_roots"].([]any)
	if !ok || len(cliRootRows) < 2 {
		t.Fatalf("unexpected task_cli_working_directory_roots payload: %#v", payload["task_cli_working_directory_roots"])
	}
	rootKinds := map[string]bool{}
	for _, raw := range cliRootRows {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("unexpected cli root row: %#v", raw)
		}
		if kind, _ := row["root_kind"].(string); kind != "" {
			rootKinds[kind] = true
		}
	}
	if !rootKinds["task_worktree"] || !rootKinds["project_workspace"] {
		t.Fatalf("unexpected cli root kinds: %#v", rootKinds)
	}
	validationLoopBlocks, ok := payload["recent_validation_loop_blocks"].([]any)
	if !ok || len(validationLoopBlocks) == 0 {
		t.Fatalf("unexpected recent_validation_loop_blocks payload: %#v", payload["recent_validation_loop_blocks"])
	}
	foundRecoveryFocus := false
	for _, raw := range validationLoopBlocks {
		blockRow, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("unexpected validation-loop block row: %#v", raw)
		}
		if got, _ := blockRow["block_excerpt"].(string); strings.Contains(got, "recovery_target_focus_required") {
			foundRecoveryFocus = true
			break
		}
	}
	if !foundRecoveryFocus {
		t.Fatalf("recent_validation_loop_blocks missing recovery_target_focus_required row: %#v", validationLoopBlocks)
	}
	inFlightAgentTurns, ok := payload["in_flight_agent_turns"].([]any)
	if !ok || len(inFlightAgentTurns) != 1 {
		t.Fatalf("unexpected in_flight_agent_turns payload: %#v", payload["in_flight_agent_turns"])
	}
	inFlightRow, ok := inFlightAgentTurns[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected in_flight_agent_turns row: %#v", inFlightAgentTurns[0])
	}
	if got, _ := inFlightRow["invocation_id"].(string); got != inFlightInvocationID.String() {
		t.Fatalf("invocation_id = %q, want %q", got, inFlightInvocationID.String())
	}
}
