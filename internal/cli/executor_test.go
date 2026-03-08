package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type projectReaderStub struct {
	project repo.Project
	err     error
}

func (s projectReaderStub) GetByID(_ context.Context, _ uuid.UUID) (repo.Project, error) {
	if s.err != nil {
		return repo.Project{}, s.err
	}
	return s.project, nil
}

func TestBuildEnvironmentDropsBlockedOverrides(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	t.Setenv("OPENAI_API_KEY", "host-secret")
	t.Setenv("SAFE_FROM_HOST", "ok")

	settings := json.RawMessage(`{"env_vars":{"PROJECT_TOKEN":"ref:project-token","SERVICE_PASSWORD":"allowed-from-project"}}`)
	exec := NewExecutor(ExecutorOptions{
		Projects: projectReaderStub{project: repo.Project{ID: projectID, OrganizationID: orgID, Settings: settings}},
		SecretService: secretResolverFunc(func(_ context.Context, _ uuid.UUID, ref string) (string, error) {
			if ref == "ref:project-token" {
				return "resolved-value", nil
			}
			return ref, nil
		}),
	})

	env, used, err := exec.buildEnvironment(context.Background(), orgID, CLIExecuteInput{
		ProjectID: projectID,
		EnvOverrides: map[string]string{
			"ANTHROPIC_API_KEY": "blocked",
			"CUSTOM_VAR":        "custom-value",
		},
	})
	if err != nil {
		t.Fatalf("buildEnvironment: %v", err)
	}

	joined := "\n" + strings.Join(env, "\n") + "\n"
	if strings.Contains(joined, "\nOPENAI_API_KEY=") {
		t.Fatalf("constructed env unexpectedly contains OPENAI_API_KEY: %s", joined)
	}
	if strings.Contains(joined, "\nANTHROPIC_API_KEY=") {
		t.Fatalf("constructed env unexpectedly contains ANTHROPIC_API_KEY: %s", joined)
	}
	if !strings.Contains(joined, "\nSAFE_FROM_HOST=ok\n") {
		t.Fatalf("constructed env missing inherited safe env var: %s", joined)
	}
	if !strings.Contains(joined, "\nCUSTOM_VAR=custom-value\n") {
		t.Fatalf("constructed env missing override key: %s", joined)
	}
	if !strings.Contains(joined, "\nPROJECT_TOKEN=resolved-value\n") {
		t.Fatalf("constructed env missing resolved project secret ref: %s", joined)
	}
	if !strings.Contains(joined, "\nSERVICE_PASSWORD=allowed-from-project\n") {
		t.Fatalf("project *_PASSWORD should be allowed: %s", joined)
	}

	if _, ok := used["ANTHROPIC_API_KEY"]; ok {
		t.Fatal("blocked override key should not be recorded in env_vars_used")
	}
	if used["CUSTOM_VAR"] != "override" {
		t.Fatalf("env_vars_used[CUSTOM_VAR] = %v, want override", used["CUSTOM_VAR"])
	}
}

func TestBuildEnvironmentDropsBlockedProjectKeys(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	settings := json.RawMessage(`{"env_vars":{"ANTHROPIC_API_KEY":"leaked","SERVICE_PASSWORD":"allowed-from-project"}}`)
	exec := NewExecutor(ExecutorOptions{
		Projects: projectReaderStub{project: repo.Project{ID: projectID, OrganizationID: orgID, Settings: settings}},
	})

	env, used, err := exec.buildEnvironment(context.Background(), orgID, CLIExecuteInput{ProjectID: projectID})
	if err != nil {
		t.Fatalf("buildEnvironment: %v", err)
	}

	joined := "\n" + strings.Join(env, "\n") + "\n"
	if strings.Contains(joined, "\nANTHROPIC_API_KEY=") {
		t.Fatalf("constructed env unexpectedly contains blocked project key: %s", joined)
	}
	if !strings.Contains(joined, "\nSERVICE_PASSWORD=allowed-from-project\n") {
		t.Fatalf("project *_PASSWORD key should remain allowed when sourced from project config: %s", joined)
	}
	if _, ok := used["ANTHROPIC_API_KEY"]; ok {
		t.Fatal("blocked project key should not be recorded in env_vars_used")
	}
}

func TestDecodeMapInput(t *testing.T) {
	runID := uuid.New()
	runStepID := uuid.New()
	taskID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()

	input, err := decodeMapInput(map[string]any{
		"run_id":            runID.String(),
		"run_step_id":       runStepID.String(),
		"task_id":           taskID.String(),
		"project_id":        projectID.String(),
		"agent_id":          agentID.String(),
		"organization_id":   orgID.String(),
		"command":           "echo hello",
		"working_directory": "subdir",
		"timeout_seconds":   5.0,
		"env_overrides":     map[string]any{"A": "1", "B": 2},
	})
	if err != nil {
		t.Fatalf("decodeMapInput: %v", err)
	}

	if input.RunID != runID || input.RunStepID != runStepID || input.TaskID != taskID || input.ProjectID != projectID || input.AgentID != agentID {
		t.Fatalf("decoded IDs mismatch: %+v", input)
	}
	if input.OrganizationID == nil || *input.OrganizationID != orgID {
		t.Fatalf("organization_id = %v, want %s", input.OrganizationID, orgID)
	}
	if input.WorkingDirectory == nil || *input.WorkingDirectory != "subdir" {
		t.Fatalf("working_directory = %v, want subdir", input.WorkingDirectory)
	}
	if input.TimeoutSeconds == nil || *input.TimeoutSeconds != 5 {
		t.Fatalf("timeout_seconds = %v, want 5", input.TimeoutSeconds)
	}
	if input.EnvOverrides["A"] != "1" || input.EnvOverrides["B"] != "2" {
		t.Fatalf("env_overrides = %+v, want A=1 B=2", input.EnvOverrides)
	}
}

func TestDecodeMapInputRecoversCLIExecuteAliases(t *testing.T) {
	runID := uuid.New()
	runStepID := uuid.New()
	taskID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()

	input, err := decodeMapInput(map[string]any{
		"run_id":          runID.String(),
		"run_step_id":     runStepID.String(),
		"task_id":         taskID.String(),
		"project_id":      projectID.String(),
		"agent_id":        agentID.String(),
		"organization_id": orgID.String(),
		"_raw":            `{"cmd":"cat <<'EOF' > docs/strategy.md\n# Strategy\nEOF","working_dir":"docs","timeout_ms":1200,"env":{"MODE":"test"}}`,
	})
	if err != nil {
		t.Fatalf("decodeMapInput: %v", err)
	}

	if input.Command != "cat <<'EOF' > docs/strategy.md\n# Strategy\nEOF" {
		t.Fatalf("command = %q, want recovered heredoc command", input.Command)
	}
	if input.WorkingDirectory == nil || *input.WorkingDirectory != "docs" {
		t.Fatalf("working_directory = %v, want docs", input.WorkingDirectory)
	}
	if input.TimeoutSeconds == nil || *input.TimeoutSeconds != 2 {
		t.Fatalf("timeout_seconds = %v, want 2", input.TimeoutSeconds)
	}
	if input.EnvOverrides["MODE"] != "test" {
		t.Fatalf("env_overrides = %+v, want MODE=test", input.EnvOverrides)
	}
}

func TestResolveWorkingDirectoryUsesProjectSlugWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("resolve data dir symlink: %v", err)
	}
	orgID := uuid.New()
	projectID := uuid.New()

	exec := NewExecutor(ExecutorOptions{
		DataDir:  dataDir,
		Projects: projectReaderStub{project: repo.Project{ID: projectID, OrganizationID: orgID, Slug: "site-redesign"}},
	})

	got, err := exec.resolveWorkingDirectory(context.Background(), orgID, CLIExecuteInput{
		TaskID:    uuid.New(),
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("resolveWorkingDirectory: %v", err)
	}

	want := filepath.Join(resolvedDataDir, "workspaces", "site-redesign")
	if got != want {
		t.Fatalf("working directory = %q, want %q", got, want)
	}
}

func TestResolveWorkingDirectoryRejectsProjectOrganizationMismatch(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	exec := NewExecutor(ExecutorOptions{
		DataDir:  t.TempDir(),
		Projects: projectReaderStub{project: repo.Project{ID: projectID, OrganizationID: uuid.New(), Slug: "site-redesign"}},
	})

	_, err := exec.resolveWorkingDirectory(context.Background(), orgID, CLIExecuteInput{
		TaskID:    uuid.New(),
		ProjectID: projectID,
	})
	if err == nil {
		t.Fatal("resolveWorkingDirectory error = nil, want organization mismatch")
	}
	if !strings.Contains(err.Error(), "project organization mismatch") {
		t.Fatalf("resolveWorkingDirectory error = %v, want project organization mismatch", err)
	}
}

func TestExecuteCommandIgnoresEventAppendErrors(t *testing.T) {
	queryStub := &executionQueryStub{}
	exec := NewExecutor(ExecutorOptions{
		Executions: &Repository{db: queryStub},
		Events: runEventWriterFunc(func(_ context.Context, _ controlplane.RunEvent) (controlplane.RunEvent, error) {
			return controlplane.RunEvent{}, errors.New("event append failed")
		}),
		WorkspaceRoot: t.TempDir(),
		DataDir:       t.TempDir(),
	})

	orgID := uuid.New()
	output, err := exec.ExecuteCommand(context.Background(), CLIExecuteInput{
		RunID:          uuid.New(),
		RunStepID:      uuid.New(),
		TaskID:         uuid.New(),
		ProjectID:      uuid.New(),
		AgentID:        uuid.New(),
		OrganizationID: &orgID,
		Command:        "printf ok",
	})
	if err != nil {
		t.Fatalf("ExecuteCommand returned error for non-fatal event append failure: %v", err)
	}
	if output.ExitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", output.ExitCode)
	}
	if output.StdoutInline == nil || *output.StdoutInline != "ok" {
		t.Fatalf("stdout_inline = %v, want ok", output.StdoutInline)
	}
	if queryStub.createCalls != 1 || queryStub.updateCalls != 1 {
		t.Fatalf("repository calls create=%d update=%d, want create=1 update=1", queryStub.createCalls, queryStub.updateCalls)
	}
}

type secretResolverFunc func(ctx context.Context, orgID uuid.UUID, ref string) (string, error)

func (fn secretResolverFunc) ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error) {
	return fn(ctx, orgID, ref)
}

type runEventWriterFunc func(ctx context.Context, event controlplane.RunEvent) (controlplane.RunEvent, error)

func (fn runEventWriterFunc) Append(ctx context.Context, event controlplane.RunEvent) (controlplane.RunEvent, error) {
	return fn(ctx, event)
}

type executionQueryStub struct {
	created     Execution
	updated     Execution
	createCalls int
	updateCalls int
}

func (s *executionQueryStub) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}

func (s *executionQueryStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO cli_execution"):
		s.createCalls++
		now := time.Now().UTC()
		s.created = Execution{
			ID:               uuid.New(),
			RunID:            args[0].(uuid.UUID),
			RunStepID:        args[1].(uuid.UUID),
			TaskID:           args[2].(uuid.UUID),
			ProjectID:        args[3].(uuid.UUID),
			AgentID:          args[4].(uuid.UUID),
			Command:          args[5].(string),
			WorkingDirectory: args[6].(string),
			RiskLevel:        args[7].(RiskLevel),
			PolicyDecision:   args[8].(string),
			ExitCode:         copyIntPointer(args[9]),
			StdoutArtifactID: copyUUIDPointer(args[10]),
			StderrArtifactID: copyUUIDPointer(args[11]),
			EnvVarsUsed:      copyRawMessage(args[12]),
			StartedAt:        copyTimePointer(args[13]),
			CompletedAt:      copyTimePointer(args[14]),
			DurationMS:       copyIntPointer(args[15]),
			Metadata:         copyRawMessage(args[16]),
			CreatedAt:        now,
		}
		return fakeCLIExecutionRow{execution: s.created}
	case strings.Contains(sql, "UPDATE cli_execution"):
		s.updateCalls++
		s.updated = s.created
		if decision, ok := args[1].(string); ok && strings.TrimSpace(decision) != "" {
			s.updated.PolicyDecision = decision
		}
		s.updated.ExitCode = copyIntPointer(args[2])
		s.updated.StdoutArtifactID = copyUUIDPointer(args[3])
		s.updated.StderrArtifactID = copyUUIDPointer(args[4])
		s.updated.CompletedAt = copyTimePointer(args[5])
		s.updated.DurationMS = copyIntPointer(args[6])
		s.updated.Metadata = copyRawMessage(args[7])
		return fakeCLIExecutionRow{execution: s.updated}
	default:
		return fakeCLIExecutionRow{err: pgx.ErrNoRows}
	}
}

type fakeCLIExecutionRow struct {
	execution Execution
	err       error
}

func (r fakeCLIExecutionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*uuid.UUID) = r.execution.ID
	*dest[1].(*uuid.UUID) = r.execution.RunID
	*dest[2].(*uuid.UUID) = r.execution.RunStepID
	*dest[3].(*uuid.UUID) = r.execution.TaskID
	*dest[4].(*uuid.UUID) = r.execution.ProjectID
	*dest[5].(*uuid.UUID) = r.execution.AgentID
	*dest[6].(*string) = r.execution.Command
	*dest[7].(*string) = r.execution.WorkingDirectory
	*dest[8].(*RiskLevel) = r.execution.RiskLevel
	*dest[9].(*string) = r.execution.PolicyDecision
	*dest[10].(**int) = copyIntValue(r.execution.ExitCode)
	*dest[11].(**uuid.UUID) = copyUUIDValue(r.execution.StdoutArtifactID)
	*dest[12].(**uuid.UUID) = copyUUIDValue(r.execution.StderrArtifactID)
	*dest[13].(*json.RawMessage) = append(json.RawMessage(nil), r.execution.EnvVarsUsed...)
	*dest[14].(**time.Time) = copyTimeValue(r.execution.StartedAt)
	*dest[15].(**time.Time) = copyTimeValue(r.execution.CompletedAt)
	*dest[16].(**int) = copyIntValue(r.execution.DurationMS)
	*dest[17].(*json.RawMessage) = append(json.RawMessage(nil), r.execution.Metadata...)
	*dest[18].(*time.Time) = r.execution.CreatedAt
	return nil
}

func copyRawMessage(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	raw, ok := value.(json.RawMessage)
	if !ok {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func copyIntPointer(value any) *int {
	if value == nil {
		return nil
	}
	ptr, ok := value.(*int)
	if !ok || ptr == nil {
		return nil
	}
	copied := *ptr
	return &copied
}

func copyUUIDPointer(value any) *uuid.UUID {
	if value == nil {
		return nil
	}
	ptr, ok := value.(*uuid.UUID)
	if !ok || ptr == nil {
		return nil
	}
	copied := *ptr
	return &copied
}

func copyTimePointer(value any) *time.Time {
	if value == nil {
		return nil
	}
	ptr, ok := value.(*time.Time)
	if !ok || ptr == nil {
		return nil
	}
	copied := *ptr
	return &copied
}

func copyIntValue(ptr *int) *int {
	if ptr == nil {
		return nil
	}
	copied := *ptr
	return &copied
}

func copyUUIDValue(ptr *uuid.UUID) *uuid.UUID {
	if ptr == nil {
		return nil
	}
	copied := *ptr
	return &copied
}

func copyTimeValue(ptr *time.Time) *time.Time {
	if ptr == nil {
		return nil
	}
	copied := *ptr
	return &copied
}
