package native

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

type fakeMessageRepo struct {
	messages []repo.ChatMessage
}

func (f *fakeMessageRepo) Create(_ context.Context, message repo.ChatMessage) (repo.ChatMessage, error) {
	if message.ID == uuid.Nil {
		message.ID = uuid.New()
	}
	f.messages = append(f.messages, message)
	return message, nil
}

func (f *fakeMessageRepo) ListBySession(_ context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error) {
	items := make([]repo.ChatMessage, 0, len(f.messages))
	for _, message := range f.messages {
		if message.SessionID == sessionID {
			items = append(items, message)
		}
	}
	return items, nil
}

func TestParseRecentDeliverableTargetFromToolResultPrefersDeliverablePath(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"tool_name": "file.write",
		"output": map[string]any{
			"deliverable_path": "Work/OC-13-report.md",
			"path":             "planning/ignored.md",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(payload): %v", err)
	}

	got := parseRecentDeliverableTargetFromToolResult(string(payload))
	if got != "Work/OC-13-report.md" {
		t.Fatalf("parseRecentDeliverableTargetFromToolResult(...) = %q, want %q", got, "Work/OC-13-report.md")
	}
}

func TestLatestRecoveryTargetPathForSessionFallsBackToRecentToolResult(t *testing.T) {
	sessionID := uuid.New()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"src/pipeline_logger.py"}}`,
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID})
	if got != "src/pipeline_logger.py" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "src/pipeline_logger.py")
	}
}

func TestLatestRecoveryTargetPathForSessionFallsBackToReviewPromptTarget(t *testing.T) {
	sessionID := uuid.New()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content: "Review only.\n" +
					"Start with the preferred deliverable target `src/pipeline_logger.py`. Inspect that target directly before broad workspace discovery, and do not begin by listing the repository root unless `src/pipeline_logger.py` is missing.\n" +
					"Do not inspect planning artifacts or list the full repository tree while `src/pipeline_logger.py` is present and readable.",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID})
	if got != "src/pipeline_logger.py" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "src/pipeline_logger.py")
	}
}

func TestLatestRecoveryTargetPathForSessionPrefersSystemRecoveryTarget(t *testing.T) {
	sessionID := uuid.New()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"src/pipeline_logger.py"}}`,
			},
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: Work/OC-13-report.md\n",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID})
	if got != "Work/OC-13-report.md" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "Work/OC-13-report.md")
	}
}

func TestFileReadRejectsPlaceholderRecentReadTargetWithoutExplicitDeliverable(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "src/pipeline_logger.py"

	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(src): %v", err)
	}
	placeholder := "Active task request: Start review on task: Validate pipeline logging output and formats " +
		"Task description: Verify pipeline produces structured logs at each stage, log levels are correct, sensitive data is redacted, and log output matches expected format. ~20 min. " +
		"Review instruction: Inspect the current deliverables and use flow.review_decision to approve or reject this review step. Approval closes with an empty review commit. Rejection may add review-scoped CriticMarkup notes. " +
		"Flow node execution: 419cef3e-6b5d-4b8e-a2f7-95e7b0b64c4a"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Validate pipeline logging output and formats."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Validate pipeline logging output and formats",
			Description:    &description,
			Metadata:       json.RawMessage(`{"bootstrap_first_wave_selected":true}`),
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"src/pipeline_logger.py"}}`,
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": targetPath})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != "placeholder_deliverable" {
		t.Fatalf("error = %v, want placeholder_deliverable", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %s", got, targetPath)
	}
	if _, ok := out["content"]; ok {
		t.Fatalf("content should be omitted for placeholder deliverable reads: %#v", out["content"])
	}
}

func TestFileReadRejectsPlaceholderReviewPromptTargetWithoutExplicitDeliverable(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "src/pipeline_logger.py"

	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(src): %v", err)
	}
	placeholder := "Task execution is already underway. Reuse the existing workspace files and continue the deliverable directly."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Validate pipeline logging output and formats."
	plan := taskplan.Analyze("Validate pipeline logging output and formats", &description)
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Validate pipeline logging output and formats",
			Description:    &description,
			Metadata:       taskplan.ApplyMetadata(nil, plan),
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content: "Review only.\n" +
					"Start with the preferred deliverable target `src/pipeline_logger.py`. Inspect that target directly before broad workspace discovery, and do not begin by listing the repository root unless `src/pipeline_logger.py` is missing.\n" +
					"Do not inspect planning artifacts or list the full repository tree while `src/pipeline_logger.py` is present and readable.",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": targetPath})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != "placeholder_deliverable" {
		t.Fatalf("error = %v, want placeholder_deliverable", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %s", got, targetPath)
	}
}
