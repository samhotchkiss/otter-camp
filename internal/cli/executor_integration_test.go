//go:build integration

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	repopkg "github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

type integrationFixture struct {
	executor     *Executor
	execRepo     *Repository
	artifactRepo *controlplane.RunArtifactRepository
	eventRepo    *controlplane.RunEventRepository
	orgID        uuid.UUID
	projectID    uuid.UUID
	taskID       uuid.UUID
	agentID      uuid.UUID
	runID        uuid.UUID
	runStepID    uuid.UUID
}

func TestExecutorIntegrationRoundTripInlineOutput(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)

	result, err := fixture.executor.ExecuteCommand(context.Background(), CLIExecuteInput{
		RunID:          fixture.runID,
		RunStepID:      fixture.runStepID,
		TaskID:         fixture.taskID,
		ProjectID:      fixture.projectID,
		AgentID:        fixture.agentID,
		OrganizationID: &fixture.orgID,
		Command:        `echo "hello"`,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", result.ExitCode)
	}
	if result.StdoutInline == nil || *result.StdoutInline != "hello\n" {
		t.Fatalf("stdout_inline = %v, want hello\\n", result.StdoutInline)
	}
	if result.StdoutArtifactID != nil {
		t.Fatalf("stdout_artifact_id = %v, want nil", result.StdoutArtifactID)
	}

	rows, err := fixture.execRepo.ListByRun(context.Background(), fixture.runID)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("cli_execution rows = %d, want 1", len(rows))
	}
	if rows[0].ExitCode == nil || *rows[0].ExitCode != 0 {
		t.Fatalf("stored exit_code = %v, want 0", rows[0].ExitCode)
	}
	if !strings.Contains(string(rows[0].Metadata), "hello\\n") {
		t.Fatalf("stored metadata missing inline output: %s", string(rows[0].Metadata))
	}
}

func TestExecutorIntegrationLargeOutputCreatesArtifact(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)

	result, err := fixture.executor.ExecuteCommand(context.Background(), CLIExecuteInput{
		RunID:          fixture.runID,
		RunStepID:      fixture.runStepID,
		TaskID:         fixture.taskID,
		ProjectID:      fixture.projectID,
		AgentID:        fixture.agentID,
		OrganizationID: &fixture.orgID,
		Command:        `yes X | head -c 102400`,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand large output: %v", err)
	}
	if result.StdoutArtifactID == nil {
		t.Fatal("stdout_artifact_id = nil, want artifact ID")
	}
	if result.StdoutInline != nil {
		t.Fatalf("stdout_inline = %q, want nil for large output", *result.StdoutInline)
	}

	artifact, err := fixture.artifactRepo.Get(context.Background(), *result.StdoutArtifactID)
	if err != nil {
		t.Fatalf("Get run_artifact: %v", err)
	}
	if artifact.ArtifactType != "stdout" {
		t.Fatalf("artifact_type = %q, want stdout", artifact.ArtifactType)
	}
}

func TestExecutorIntegrationTimeoutKillsProcess(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	timeoutSeconds := 2

	result, err := fixture.executor.ExecuteCommand(context.Background(), CLIExecuteInput{
		RunID:          fixture.runID,
		RunStepID:      fixture.runStepID,
		TaskID:         fixture.taskID,
		ProjectID:      fixture.projectID,
		AgentID:        fixture.agentID,
		OrganizationID: &fixture.orgID,
		Command:        `sleep 60`,
		TimeoutSeconds: &timeoutSeconds,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand timeout: %v", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit_code = %d, want -1", result.ExitCode)
	}
	if result.DurationMS > 8000 {
		t.Fatalf("duration_ms = %d, want <= 8000", result.DurationMS)
	}
}

func TestExecutorIntegrationRunEventsAndConstructedEnv(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]any{
		"env_vars": map[string]any{"PROJECT_ONLY": "project-value"},
	})
	t.Setenv("OPENAI_API_KEY", "should-not-leak")

	result, err := fixture.executor.ExecuteCommand(context.Background(), CLIExecuteInput{
		RunID:          fixture.runID,
		RunStepID:      fixture.runStepID,
		TaskID:         fixture.taskID,
		ProjectID:      fixture.projectID,
		AgentID:        fixture.agentID,
		OrganizationID: &fixture.orgID,
		Command:        `printf '%s\n%s\n%s\n%s\n' "$OTTERCAMP_TASK_ID" "$OPENAI_API_KEY" "$PROJECT_ONLY" "$CUSTOM_VAR"`,
		EnvOverrides: map[string]string{
			"CUSTOM_VAR":        "override-value",
			"OPENAI_API_KEY":    "blocked-override",
			"ANTHROPIC_API_KEY": "blocked-override",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCommand env/chunk: %v", err)
	}
	if result.StdoutInline == nil {
		t.Fatal("stdout_inline is nil")
	}

	lines := strings.Split(strings.TrimSuffix(*result.StdoutInline, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("stdout lines = %d, want 4; output=%q", len(lines), *result.StdoutInline)
	}
	if lines[0] != fixture.taskID.String() {
		t.Fatalf("OTTERCAMP_TASK_ID = %q, want %q", lines[0], fixture.taskID)
	}
	if lines[1] != "" {
		t.Fatalf("OPENAI_API_KEY = %q, want empty", lines[1])
	}
	if lines[2] != "project-value" {
		t.Fatalf("PROJECT_ONLY = %q, want project-value", lines[2])
	}
	if lines[3] != "override-value" {
		t.Fatalf("CUSTOM_VAR = %q, want override-value", lines[3])
	}

	events, err := fixture.eventRepo.ListByRun(context.Background(), fixture.runID, 0)
	if err != nil {
		t.Fatalf("ListByRun run_event: %v", err)
	}
	chunks := 0
	for _, event := range events {
		if event.EventType == "output_chunk" {
			chunks++
		}
	}
	if chunks == 0 {
		t.Fatal("expected at least one output_chunk run_event")
	}
}

func TestExecutorIntegrationPathTraversalRejectedBeforeExecution(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	workingDirectory := "../../etc"

	_, err := fixture.executor.ExecuteCommand(context.Background(), CLIExecuteInput{
		RunID:            fixture.runID,
		RunStepID:        fixture.runStepID,
		TaskID:           fixture.taskID,
		ProjectID:        fixture.projectID,
		AgentID:          fixture.agentID,
		OrganizationID:   &fixture.orgID,
		Command:          `echo should-not-run`,
		WorkingDirectory: &workingDirectory,
	})
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("error = %v, want ErrPathTraversal", err)
	}

	rows, listErr := fixture.execRepo.ListByRun(context.Background(), fixture.runID)
	if listErr != nil {
		t.Fatalf("ListByRun: %v", listErr)
	}
	if len(rows) != 0 {
		t.Fatalf("cli_execution rows = %d, want 0 when traversal is rejected", len(rows))
	}
}

func newIntegrationFixture(t *testing.T, projectSettings map[string]any) integrationFixture {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)

	orgID := testutil.MakeOrg(t, pool)
	settingsRaw := json.RawMessage(`{}`)
	if len(projectSettings) > 0 {
		encoded, err := json.Marshal(projectSettings)
		if err != nil {
			t.Fatalf("marshal project settings: %v", err)
		}
		settingsRaw = encoded
	}

	project, err := repopkg.NewProjectRepo(pool).Create(ctx, repopkg.Project{
		OrganizationID: orgID,
		Slug:           "project-" + strings.ToLower(uuid.NewString()[:8]),
		DisplayName:    "CLI Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       settingsRaw,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	task, err := repopkg.NewProjectTaskRepo(pool).Create(ctx, repopkg.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      project.ID,
		Title:          "CLI Task",
		WorkStatus:     "in_progress",
		CreatedByType:  "system",
		CreatedByID:    ptrUUID(uuid.Nil),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	agent := testutil.MakeAgent(t, pool, orgID)

	runRecord, err := controlplane.NewRunRepository(pool).Create(ctx, controlplane.Run{
		OrganizationID: orgID,
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
		t.Fatalf("create run_step: %v", err)
	}

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}

	execRepo := NewRepository(pool)
	executor := NewExecutor(ExecutorOptions{
		Executions: execRepo,
		Artifacts:  controlplane.NewRunArtifactRepository(pool),
		Events:     controlplane.NewRunEventRepository(pool),
		Projects:   repopkg.NewProjectRepo(pool),
		Store:      store,
		DataDir:    t.TempDir(),
	})

	return integrationFixture{
		executor:     executor,
		execRepo:     execRepo,
		artifactRepo: controlplane.NewRunArtifactRepository(pool),
		eventRepo:    controlplane.NewRunEventRepository(pool),
		orgID:        orgID,
		projectID:    project.ID,
		taskID:       task.ID,
		agentID:      agent.ID,
		runID:        runRecord.ID,
		runStepID:    step.ID,
	}
}

func ptrUUID(value uuid.UUID) *uuid.UUID {
	return &value
}
