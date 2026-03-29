package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
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

func TestLatestRecoveryTargetPathForSessionPrefersMetadataBatchOutputOverDependencyArtifactHistory(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	description := "Read content/technonymous-index.json. For each of the first 12 URLs in the post_urls array, save the article text as clean markdown files under content/posts/. Deliverable: markdown files in content/posts/."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: "content/posts/stop-preparing-your-kids-for-jobs.md"},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 1-12 from technonymous index and save as markdown",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"content/technonymous-index.json"}}`,
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &taskID})
	if got != "content/posts/stop-preparing-your-kids-for-jobs.md" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "content/posts/stop-preparing-your-kids-for-jobs.md")
	}
}

func TestLatestRecoveryTargetPathForSessionPrefersFirstMissingMetadataBatchOutputOverCompletedOutput(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	description := "Read content/technonymous-index.json. For each of the first 12 URLs in the post_urls array, save the article text as clean markdown files under content/posts/."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: "content/posts/stop-preparing-your-kids-for-jobs.md"},
				{Path: "content/posts/let-kids-be-kids.md"},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "posts", "stop-preparing-your-kids-for-jobs.md"), []byte("# Stop Preparing Your Kids for Jobs\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(completed output): %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 1-12 from technonymous index and save as markdown",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/stop-preparing-your-kids-for-jobs.md\n",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &taskID})
	if got != "content/posts/let-kids-be-kids.md" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "content/posts/let-kids-be-kids.md")
	}
}

func TestLatestRecoveryTargetPathForSessionIgnoresDependencyArtifactHistoryForMarkdownBatchWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	description := "Read content/technonymous-index.json. For each of the first 12 URLs in the post_urls array, save the article text as clean markdown files under content/posts/."

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 1-12 from technonymous index and save as markdown",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"content/technonymous-index.json"}}`,
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &taskID})
	if got != "" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want empty because dependency artifact reads should not poison markdown batch recovery", got)
	}
}

func TestTaskExpectsMarkdownDeliverablesRecognizesSeparateMDFileWording(t *testing.T) {
	description := "Read the first 12 entries from content/technonymous-index.json. For each entry, use web_fetch to retrieve the full post HTML, convert the post body to clean markdown, and write each post as a separate .md file under content/posts/."
	taskRecord := repo.ProjectTask{Description: &description}
	if !taskExpectsMarkdownDeliverables(taskRecord) {
		t.Fatal("expected exact .md-file wording to qualify as markdown deliverables")
	}
}

func TestFileListAllowsReviewDeliverableRootInspectionWithinRecoveryTargetRoot(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/practice-eliminating-streaming-video.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte("# Practice eliminating streaming video\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "posts", "practice-listen-to-albums.md"), []byte("# Practice listen to albums\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(sibling): %v", err)
	}

	description := "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/",
			Description:    &description,
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/practice-eliminating-streaming-video.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.list", map[string]any{"path": "content/posts"})
	if err != nil {
		t.Fatalf("executor.Execute(file.list): %v", err)
	}
	if got := out["error"]; got != nil {
		t.Fatalf("error = %v, want nil", got)
	}
	if got := out["total"]; got != 2 {
		t.Fatalf("total = %v, want 2", got)
	}
}

func TestFileListAllowsBlockedReviewLaneDeliverableRootInspectionWithinRecoveryTargetRoot(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/practice-eliminating-streaming-video.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte("# Practice eliminating streaming video\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "posts", "practice-listen-to-albums.md"), []byte("# Practice listen to albums\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(sibling): %v", err)
	}

	description := "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/",
			Description:    &description,
			WorkStatus:     "blocked",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/practice-eliminating-streaming-video.md\n",
			},
			{
				SessionID: sessionID,
				Role:      "user",
				Content: "Review only.\n" +
					"Start with the preferred deliverable root `content/posts`. Inspect that output root directly before broader workspace, git, or task rediscovery.\n" +
					"Use flow.review_decision with the active flow_node_execution_id to approve or reject this review step.",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.list", map[string]any{"path": "content/posts"})
	if err != nil {
		t.Fatalf("executor.Execute(file.list): %v", err)
	}
	if got := out["error"]; got != nil {
		t.Fatalf("error = %v, want nil", got)
	}
	if got := out["total"]; got != 2 {
		t.Fatalf("total = %v, want 2", got)
	}
}

func TestFileListRejectsExecutionDeliverableRootInspectionWithinRecoveryTargetRootForBatchTask(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/practice-one-screen-at-a-time.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte("# Practice one screen at a time\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "posts", "discomfort-is-growth.md"), []byte("# Discomfort Is Growth\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(sibling): %v", err)
	}

	description := "Read the first 12 entries (indices 0-11) from content/technonymous-index.json. For each entry, use web_fetch to retrieve the full post HTML from its URL, convert the post body to clean markdown, and write each post as a separate .md file under content/posts/. Use the URL slug as the filename."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: targetPath},
				{Path: "content/posts/discomfort-is-growth.md"},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Replacement: Fetch posts 13-24 from technonymous-index.json and save as markdown under content/posts/",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/practice-one-screen-at-a-time.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.list", map[string]any{"path": "content/posts"})
	if err != nil {
		t.Fatalf("executor.Execute(file.list): %v", err)
	}
	if got := out["error"]; got != "recovery_target_focus_required" {
		t.Fatalf("error = %v, want recovery_target_focus_required", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsExecutionSiblingDeliverableInspectionWithinRecoveryTargetRootForBatchTask(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/practice-one-screen-at-a-time.md"
	siblingPath := "content/posts/discomfort-is-growth.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte("# Practice one screen at a time\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(siblingPath)), []byte("# Discomfort Is Growth\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(sibling): %v", err)
	}

	description := "Read the first 12 entries (indices 0-11) from content/technonymous-index.json. For each entry, use web_fetch to retrieve the full post HTML from its URL, convert the post body to clean markdown, and write each post as a separate .md file under content/posts/. Use the URL slug as the filename."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: targetPath},
				{Path: siblingPath},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Replacement: Fetch posts 13-24 from technonymous-index.json and save as markdown under content/posts/",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/practice-one-screen-at-a-time.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	if got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &taskID}); got != targetPath {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, targetPath)
	}
	if !taskAllowsBatchRecoveryRootInspection(executor.tasks.(*mockTaskRepo).task, siblingPath, targetPath) {
		t.Fatal("expected batch recovery sibling inspection to be allowed for execution task")
	}
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": siblingPath})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != "recovery_target_focus_required" {
		t.Fatalf("error = %v, out=%v, want recovery_target_focus_required", got, out)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadAllowsCheckpointArtifactInspectionWhenTrackedBatchOutputsComplete(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/i-cant-picture-my-kids.md"
	artifactPath := "content/technonymous-index.json"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte("# I Can't Picture My Kids\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "posts", "the-year-the-phone-started-talking.md"), []byte("# The Year the Phone Started Talking\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(tracked sibling): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(artifactPath)), []byte("{\"post_urls\":[]}"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(artifact): %v", err)
	}

	description := "Read the first 12 entries (indices 0-11) from content/technonymous-index.json. For each entry, use web_fetch to retrieve the full post HTML from its URL, convert the post body to clean markdown, and write each post as a separate .md file under content/posts/. Use the URL slug as the filename."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Artifacts: []taskcheckpoint.WorkspaceFile{
				{Path: artifactPath},
			},
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: targetPath},
				{Path: "content/posts/the-year-the-phone-started-talking.md"},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 1-12 from content/technonymous-index.json and save as markdown in content/posts/",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "blocked",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/i-cant-picture-my-kids.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": artifactPath})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != nil {
		t.Fatalf("error = %v, out=%v, want nil", got, out)
	}
	if got := out["path"]; got != artifactPath {
		t.Fatalf("path = %v, want %q", got, artifactPath)
	}
}

func TestFileListAllowsBlockedReviewNodeDeliverableRootInspectionWithinRecoveryTargetRoot(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	reviewNodeID := uuid.New()
	targetPath := "content/posts/practice-eliminating-streaming-video.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte("# Practice eliminating streaming video\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "posts", "practice-listen-to-albums.md"), []byte("# Practice listen to albums\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(sibling): %v", err)
	}

	description := "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:                taskID,
			OrganizationID:    orgID,
			ProjectID:         projectID,
			Title:             "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/",
			Description:       &description,
			WorkStatus:        "blocked",
			CurrentFlowNodeID: &reviewNodeID,
		},
	}
	executor.flowNodes = &mockFlowNodeRepo{
		nodes: map[uuid.UUID]repo.FlowNode{
			reviewNodeID: {
				ID:       reviewNodeID,
				NodeType: "review",
			},
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/practice-eliminating-streaming-video.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.list", map[string]any{"path": "content/posts"})
	if err != nil {
		t.Fatalf("executor.Execute(file.list): %v", err)
	}
	if got := out["error"]; got != nil {
		t.Fatalf("error = %v, want nil", got)
	}
	if got := out["total"]; got != 2 {
		t.Fatalf("total = %v, want 2", got)
	}
}

func TestFileListStillRejectsReviewInspectionOutsideDeliverableRoot(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/practice-eliminating-streaming-video.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte("# Practice eliminating streaming video\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 25-35 from technonymous-index.json via web_fetch and save the markdown files under content/posts/",
			Description:    &description,
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: content/posts/practice-eliminating-streaming-video.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.list", map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("executor.Execute(file.list): %v", err)
	}
	if got := out["error"]; got != "recovery_target_focus_required" {
		t.Fatalf("error = %v, want recovery_target_focus_required", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %s", got, targetPath)
	}
}

func TestFileReadAllowsBatchOutputWhenMetadataRecoveryTargetOverridesDependencyArtifactHistory(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "technonymous-index.json"), []byte(`{"post_urls":["https://example.com/p/one"]}`), 0o644); err != nil {
		t.Fatalf("os.WriteFile(index): %v", err)
	}
	targetPath := "content/posts/stop-preparing-your-kids-for-jobs.md"
	targetBody := "---\ntitle: Stop Preparing Your Kids for Jobs\nsource_url: https://example.com/p/one\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(targetBody), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Read content/technonymous-index.json. For each of the first 12 URLs in the post_urls array, save the article text as clean markdown files under content/posts/. Deliverable: markdown files in content/posts/."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: targetPath},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Fetch posts 1-12 from technonymous index and save as markdown",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"content/technonymous-index.json"}}`,
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
	if got := out["error"]; got != nil {
		t.Fatalf("error = %v, want nil", got)
	}
	if got := out["path"]; got != targetPath {
		t.Fatalf("path = %v, want %s", got, targetPath)
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
	if got := out["error"]; got != "mismatched_deliverable_context" {
		t.Fatalf("error = %v, want mismatched_deliverable_context", got)
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
	if got := fmt.Sprintf("%v", out["error"]); got != "placeholder_deliverable" && got != "mismatched_deliverable_context" {
		t.Fatalf("error = %v, want placeholder_deliverable or mismatched_deliverable_context", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %s", got, targetPath)
	}
}

func TestFileReadRejectsMarkdownReviewAssessmentPlaceholderAtPreferredTarget(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/stop-preparing-your-kids-for-jobs.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "Now I have all 12 existing files read. Let me assess their quality. Looking at the file sizes and content:\n\n- `jenny-can-i-have-my-privacy-back.md` (2339 bytes)\n- `stop-preparing-your-kids-for-jobs.md` contains garbage content\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Fetch posts 1-12 from content/technonymous-index.json and save as markdown in content/posts/."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Final replacement: scrape technonymous posts 1-12 to markdown in content/posts/",
			Description:    &description,
			Metadata:       json.RawMessage(`{"bootstrap_first_wave_selected":true}`),
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content: "Review only.\n" +
					"Start with the preferred deliverable target `content/posts/stop-preparing-your-kids-for-jobs.md`. Inspect that target directly before broad workspace discovery, and do not begin by listing the repository root unless `content/posts/stop-preparing-your-kids-for-jobs.md` is missing.\n" +
					"If reading `content/posts/stop-preparing-your-kids-for-jobs.md` returns `not_found`, `placeholder_deliverable`, or `mismatched_deliverable_context`, stop broad inspection and call flow.review_decision reject using that tool result as evidence.",
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
	if got := fmt.Sprintf("%v", out["error"]); got != "placeholder_deliverable" && got != "mismatched_deliverable_context" {
		t.Fatalf("error = %v, want placeholder_deliverable or mismatched_deliverable_context", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %s", got, targetPath)
	}
}

func TestFileReadRejectsReviewSummaryPlaceholderAtCheckpointOwnedReviewOutput(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/README.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "The workspace is blocking further reads under `content/posts` since enough evidence has been gathered across this review session. I have sufficient evidence from prior turns to render the decision now.\n\n" +
		"**Evidence already gathered across this review session:**\n\n" +
		"1. **`content/posts/` listing**: 35 markdown files confirmed present (>=35 requirement met).\n" +
		"2. **`content/posts/README.md`**: Exists, summarizes the imported posts collection.\n\n" +
		"**Assessment against acceptance criteria:**\n\n" +
		"The deliverables satisfy the task's acceptance criteria. Approving.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Close-out verification: confirm all 35 posts in content/posts/ and mark scrape-import workstream done."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: "content/posts/README.md"},
				{Path: "content/posts/VERIFICATION.md"},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Close-out verification for technonymous migration",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content: "Review only.\n" +
					"Start with the preferred deliverable target `content/posts/README.md`.\n" +
					"If reading `content/posts/README.md` returns `not_found`, `placeholder_deliverable`, or `mismatched_deliverable_context`, stop broad inspection and call flow.review_decision reject using that tool result as evidence.",
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
	if got := fmt.Sprintf("%v", out["error"]); got != "placeholder_deliverable" && got != "mismatched_deliverable_context" {
		t.Fatalf("error = %v, want placeholder_deliverable or mismatched_deliverable_context", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %s", got, targetPath)
	}
}

func TestFileReadRejectsMarkdownReviewAssessmentPlaceholderAtInProgressDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/stop-preparing-your-kids-for-jobs.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "Now I have all 12 existing files read. Let me assess their quality. Looking at the file sizes and content:\n\n- `jenny-can-i-have-my-privacy-back.md` (2339 bytes) — looks complete ✓\n- `stop-preparing-your-kids-for-jobs.md` (1353 bytes) — seems truncated, cuts off mid-thought\n\n11 of 12 need re-scraping. Let me fetch the first 4 posts now:\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Final replacement for batch 1-12. Previous attempts all terminally blocked. Read content/technonymous-index.json and save each post as markdown under content/posts/. Deliverable: content/posts/"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Final replacement: scrape technonymous posts 1-12 to markdown in content/posts/",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: Final replacement: scrape technonymous posts 1-12 to markdown in content/posts/",
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
	if got := out["deliverable_path"]; got != "content/posts" {
		t.Fatalf("deliverable_path = %v, want content/posts", got)
	}
}

func TestFileReadRejectsRuntimeOwnedCommitHandoffPlaceholderAtInProgressDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/stop-preparing-your-kids-for-jobs.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "The posts are all committed. The only uncommitted changes are:\n" +
		"1. A modified checkpoint file\n" +
		"2. A deleted `{slug}.md` placeholder (which should be removed)\n\n" +
		"Let me clean up by staging the deletion of the placeholder and committing:\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Read content/technonymous-index.json for the full list of post URLs. For each URL, fetch the page, extract the post title and body, and save markdown under content/posts/. Deliverable: content/posts/"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Scrape all technonymous.org posts to markdown in content/posts/",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: Scrape all technonymous.org posts to markdown in content/posts/",
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
	if got := out["deliverable_path"]; got != "content/posts" {
		t.Fatalf("deliverable_path = %v, want content/posts", got)
	}
}

func TestFileReadRejectsContentMigrationStatusPlaceholderAtInProgressDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/stop-preparing-your-kids-for-jobs.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "So `stop-preparing-your-kids-for-jobs.md` is the only real post file missing valid frontmatter. " +
		"I need to fetch the actual post and write it. Also `{slug}.md` is a bad placeholder file I should remove."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Read content/technonymous-index.json for the full list of post URLs. For each URL, fetch the page, extract the post title and body, and save markdown under content/posts/. Deliverable: content/posts/"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Scrape all technonymous.org posts to markdown in content/posts/",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: Scrape all technonymous.org posts to markdown in content/posts/",
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
	if got := out["deliverable_path"]; got != "content/posts" {
		t.Fatalf("deliverable_path = %v, want content/posts", got)
	}
}

func TestFileReadRejectsDeliverableReviewMetaPlaceholderAtInProgressDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/stop-preparing-your-kids-for-jobs.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "I've already read the preferred target and it is clearly a mismatched deliverable. " +
		"The content of `content/posts/stop-preparing-your-kids-for-jobs.md` is not a scraped blog post — " +
		"it is a fabricated \"review summary\" that pretends the review has already been completed and approved. " +
		"This is not YAML frontmatter + markdown post body; it is meta-commentary about the review process itself."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Read content/technonymous-index.json for the full list of post URLs. For each URL, fetch the page, extract the post title and body, and save markdown under content/posts/. Deliverable: content/posts/"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Scrape all technonymous.org posts to markdown in content/posts/",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: Scrape all technonymous.org posts to markdown in content/posts/",
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
	if got := out["deliverable_path"]; got != "content/posts" {
		t.Fatalf("deliverable_path = %v, want content/posts", got)
	}
}

func TestFileReadRejectsCheckpointOwnedCombinedReviewSummaryPlaceholder(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/README.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "The two task-owned deliverables (`content/posts/README.md` and `content/posts/VERIFICATION.md`) both returned errors indicating they are rejected placeholders — they don't contain actual content. Additionally, the listing of `content/posts/` itself failed to return a valid directory listing.\n\n" +
		"These are the core deliverables for this task per the checkpoint. The task's acceptance criteria require: \"all 35 posts confirmed present with valid markdown content; README exists.\" The README is a placeholder, and the verification document is likewise a placeholder. I cannot confirm the deliverables are satisfied.\n\n" +
		"Rejecting this review.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Close-out verification: confirm all 35 posts in content/posts/ and mark scrape-import workstream done."
	metadata, err := json.Marshal(map[string]any{
		"content_migration_checkpoint": taskcheckpoint.ContentMigrationCheckpoint{
			Outputs: []taskcheckpoint.WorkspaceFile{
				{Path: "content/posts/README.md"},
				{Path: "content/posts/VERIFICATION.md"},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(metadata): %v", err)
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Close-out verification for technonymous migration",
			Description:    &description,
			Metadata:       metadata,
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content: "Review only.\n" +
					"Start with the preferred deliverable target `content/posts/README.md`.\n" +
					"If reading `content/posts/README.md` returns `not_found`, `placeholder_deliverable`, or `mismatched_deliverable_context`, stop broad inspection and call flow.review_decision reject using that tool result as evidence.",
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
	if got := fmt.Sprintf("%v", out["error"]); got != "mismatched_deliverable_context" {
		t.Fatalf("error = %v, want mismatched_deliverable_context", got)
	}
	if got := out["deliverable_path"]; got != targetPath {
		t.Fatalf("deliverable_path = %v, want %s", got, targetPath)
	}
}

func TestFileReadRejectsBatchInventoryPlaceholderAtInProgressDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "content/posts/stop-preparing-your-kids-for-jobs.md"

	if err := os.MkdirAll(filepath.Join(root, "content", "posts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(content/posts): %v", err)
	}
	placeholder := "All 35 files exist. Posts 1-12 (0-indexed entries 0-11) are:\n1. stop-preparing-your-kids-for-jobs\n2. the-end-of-commercial-software\n3. he-has-risen-the-return-of-piracy\n4. happy-new-yeartechnonymous-isnt-dead\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Final replacement for batch 1-12. Previous attempts all terminally blocked. Read content/technonymous-index.json and save each post as markdown under content/posts/. Deliverable: content/posts/"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Final replacement: scrape technonymous posts 1-12 to markdown in content/posts/",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: Final replacement: scrape technonymous posts 1-12 to markdown in content/posts/",
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
	if got := out["deliverable_path"]; got != "content/posts" {
		t.Fatalf("deliverable_path = %v, want content/posts", got)
	}
}
