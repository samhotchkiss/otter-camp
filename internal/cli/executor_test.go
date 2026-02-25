package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
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

type secretResolverFunc func(ctx context.Context, orgID uuid.UUID, ref string) (string, error)

func (fn secretResolverFunc) ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error) {
	return fn(ctx, orgID, ref)
}
