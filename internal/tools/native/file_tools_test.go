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

func TestParseRecentDeliverableTargetFromToolResultStripsWrappedDeliverablePath(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"tool_name": "file.read",
		"output": map[string]any{
			"deliverable_path": "`planning/sambot-feature-spec.md`",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(payload): %v", err)
	}

	got := parseRecentDeliverableTargetFromToolResult(string(payload))
	if got != "planning/sambot-feature-spec.md" {
		t.Fatalf("parseRecentDeliverableTargetFromToolResult(...) = %q, want %q", got, "planning/sambot-feature-spec.md")
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

func TestLatestRecoveryTargetPathForSessionStripsBackticksFromReviewPromptTarget(t *testing.T) {
	sessionID := uuid.New()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Review only.\nStart with the preferred deliverable target `planning/sambot-feature-spec.md`.\nUse flow.review_decision.",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID})
	if got != "planning/sambot-feature-spec.md" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "planning/sambot-feature-spec.md")
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

func TestDeliverableTargetMatchesTaskContractRejectsDependencyArtifactForBackendTask(t *testing.T) {
	description := "Backend API wiring — implement POST /api/sambot/chat. Use the architecture and feature spec already at planning/sambot-feature-spec.md for context on personality, tone, and knowledge base approach."
	taskRecord := repo.ProjectTask{
		TaskNumber:  174,
		Title:       "Backend API wiring",
		Description: &description,
	}

	if deliverableTargetMatchesTaskContract(taskRecord, "planning/sambot-feature-spec.md") {
		t.Fatal("expected dependency planning artifact to be rejected as backend deliverable target")
	}
}

func TestDeliverableTargetMatchesTaskContractRejectsFrontendArtifactForBackendTask(t *testing.T) {
	description := "Backend API wiring — implement POST /api/sambot/chat that returns { response, session_id }."
	taskRecord := repo.ProjectTask{
		TaskNumber:  174,
		Title:       "Backend API wiring",
		Description: &description,
	}

	if deliverableTargetMatchesTaskContract(taskRecord, "sambot/widget.html") {
		t.Fatal("expected frontend widget artifact to be rejected for backend task")
	}
	if !deliverableTargetMatchesTaskContract(taskRecord, "sambot/api.js") {
		t.Fatal("expected backend api.js artifact to remain valid for backend task")
	}
}

func TestDeliverableTargetMatchesTaskContractRejectsAuxiliaryTestArtifactForBackendTask(t *testing.T) {
	description := "Backend API wiring — implement POST /api/sambot/chat that accepts { message, session_id } and returns { response, session_id }. Use the architecture and feature spec already at planning/sambot-feature-spec.md for context on personality, tone, and knowledge base approach."
	taskRecord := repo.ProjectTask{
		TaskNumber:  174,
		Title:       "Backend API wiring",
		Description: &description,
	}

	if deliverableTargetMatchesTaskContract(taskRecord, "sambot/test-api.js") {
		t.Fatal("expected companion test artifact to be rejected for backend task")
	}
	if deliverableTargetMatchesTaskContract(taskRecord, "sambot/system-prompt-spec.md") {
		t.Fatal("expected auxiliary spec artifact to be rejected for backend task")
	}
}

func TestLatestRecoveryTargetPathForSessionIgnoresConflictingParentFrontendDeliverableForBackendChild(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	parentID := uuid.New()
	childID := uuid.New()
	sessionID := uuid.New()

	parentDescription := "Frontend chat widget — a self-contained HTML/CSS/JS component (sambot/widget.html or similar) that renders a floating chat bubble."
	childDescription := "Backend API wiring — implement POST /api/sambot/chat that accepts { message, session_id } and returns { response, session_id }."

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	childMetadata, err := json.Marshal(map[string]any{"decomposition_parent_task_id": parentID.String()})
	if err != nil {
		t.Fatalf("json.Marshal(childMetadata): %v", err)
	}
	childTask := repo.ProjectTask{
		ID:             childID,
		OrganizationID: orgID,
		ProjectID:      projectID,
		TaskNumber:     174,
		Title:          "Backend API wiring",
		Description:    &childDescription,
		Metadata:       childMetadata,
	}
	executor.tasks = &mockTaskRepo{
		task: childTask,
		listByProjectTasks: []repo.ProjectTask{
			{
				ID:             parentID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				TaskNumber:     172,
				Title:          "Build SamBot Chat MVP: frontend widget + backend API wiring",
				Description:    &parentDescription,
			},
			childTask,
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Review only.\nStart with the preferred deliverable target `sambot/api.js`.\nUse flow.review_decision.",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &childID})
	if got != "sambot/api.js" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "sambot/api.js")
	}
}

func TestLatestRecoveryTargetPathForSessionPrefersExplicitFileLabelOverHistoricalPlanningTarget(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	description := "## Deliverable\n**File:** `sambot/api.js`\n\nWrite the complete Express.js backend API for SamBot. Reference planning/sambot-personality-spec.md for tone guidance."
	taskRecord := repo.ProjectTask{
		ID:             taskID,
		OrganizationID: orgID,
		ProjectID:      projectID,
		TaskNumber:     199,
		Title:          "Build SamBot Express.js API — sambot/api.js",
		Description:    &description,
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{task: taskRecord}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"planning/sambot-architecture.md","deliverable_path":"planning/sambot-architecture.md","error":"recovery_target_focus_required","message":"Recovery already identified \u0060planning/sambot-architecture.md\u0060 as the target deliverable."}}`,
				Status:    "final",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &taskID})
	if got != "sambot/api.js" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "sambot/api.js")
	}
}

func TestNormalizeRecoveryCheckpointTargetForTaskFallsBackToExplicitFileLabel(t *testing.T) {
	description := "## Deliverable\n**File:** `planning/sambot-tech-architecture.md`\n\nWrite the complete SamBot technical architecture specification."
	taskRecord := repo.ProjectTask{
		TaskNumber:  198,
		Title:       "Write SamBot technical architecture spec — planning/sambot-tech-architecture.md",
		Description: &description,
	}

	if got := normalizeRecoveryCheckpointTargetForTask(taskRecord, "SamBot"); got != "planning/sambot-tech-architecture.md" {
		t.Fatalf("normalizeRecoveryCheckpointTargetForTask(...) = %q, want %q", got, "planning/sambot-tech-architecture.md")
	}
}

func TestLatestRecoveryTargetPathForSessionIgnoresBareDirectoryCheckpointAndUsesParentMarkdownDeliverable(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	parentID := uuid.New()
	childID := uuid.New()
	sessionID := uuid.New()

	parentDescription := "Write the file planning/sambot-tech-architecture.md — the SamBot technical architecture specification."
	childDescription := "Generic 500 errors — no internal details leaked"
	childMetadata := json.RawMessage(fmt.Sprintf(`{"decomposition_parent_task_id":"%s","recovery_file_write_checkpoint":{"version":1,"target_path":"SamBot"}}`, parentID))

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &stubTaskRepo{
		byID: map[uuid.UUID]repo.ProjectTask{
			parentID: {
				ID:             parentID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				TaskNumber:     200,
				Title:          "Write SamBot technical architecture spec — planning/sambot-tech-architecture.md",
				Description:    &parentDescription,
			},
			childID: {
				ID:             childID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				TaskNumber:     206,
				Title:          "Generic 500 errors — no internal details leaked",
				Description:    &childDescription,
				Metadata:       childMetadata,
			},
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "tool_result",
				Content:   `{"tool_name":"file.read","output":{"path":"SamBot/api.js","deliverable_path":"SamBot","error":"mismatched_deliverable_context"}}`,
				Status:    "final",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &childID})
	if got != "planning/sambot-tech-architecture.md" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "planning/sambot-tech-architecture.md")
	}
}

func TestTaskExplicitDeliverablePathFindsMatchingParentDecompositionDeliverableForBackendChild(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	parentID := uuid.New()
	childID := uuid.New()
	parentDescription := "Build the SamBot Chat MVP with two deliverables:\n- sambot/widget.html (or sambot/index.html) — the frontend chat widget\n- sambot/api.js (or sambot/server.js) — the backend API endpoint\nRead planning/sambot-feature-spec.md first for full context."
	childDescription := "Backend API wiring — implement POST /api/sambot/chat that accepts { message, session_id } and returns { response, session_id }. Use the architecture and feature spec already at planning/sambot-feature-spec.md for context on personality, tone, and knowledge base approach."
	childMetadata := json.RawMessage(fmt.Sprintf(`{"decomposition_parent_task_id":"%s"}`, parentID))
	parentMetadata := json.RawMessage(`{"decomposition":{"source_description":"Build the SamBot Chat MVP with two deliverables:\n- sambot/widget.html (or sambot/index.html) — the frontend chat widget\n- sambot/api.js (or sambot/server.js) — the backend API endpoint\nRead planning/sambot-feature-spec.md first for full context."}}`)

	executor := &NativeToolExecutor{
		tasks: &stubTaskRepo{
			byID: map[uuid.UUID]repo.ProjectTask{
				parentID: {
					ID:             parentID,
					OrganizationID: orgID,
					ProjectID:      projectID,
					TaskNumber:     172,
					Title:          "Build SamBot Chat MVP: frontend widget + backend API wiring",
					Description:    &parentDescription,
					Metadata:       parentMetadata,
				},
				childID: {
					ID:             childID,
					OrganizationID: orgID,
					ProjectID:      projectID,
					TaskNumber:     174,
					Title:          "Backend API wiring",
					Description:    &childDescription,
					Metadata:       childMetadata,
				},
			},
		},
	}

	got := executor.taskExplicitDeliverablePath(context.Background(), executor.tasks.(*stubTaskRepo).byID[childID])
	if got != "sambot/api.js" {
		t.Fatalf("taskExplicitDeliverablePath(...) = %q, want %q", got, "sambot/api.js")
	}
}

func TestContentMigrationCheckpointPreferredOutputPathSkipsExplicitSingleFileDeliverable(t *testing.T) {
	description := "Deliverable: Append these sections to the existing planning/sambot-feature-spec.md file. Do not overwrite existing content."
	taskRecord := repo.ProjectTask{
		TaskNumber:  109,
		Title:       "Append sections to planning/sambot-feature-spec.md",
		Description: &description,
	}
	checkpoint := taskcheckpoint.ContentMigrationCheckpoint{
		Outputs: []taskcheckpoint.WorkspaceFile{
			{Path: "content/posts/stop-preparing-your-kids-for-jobs.md"},
		},
	}

	if got := contentMigrationCheckpointPreferredOutputPath(taskRecord, checkpoint); got != "" {
		t.Fatalf("contentMigrationCheckpointPreferredOutputPath(...) = %q, want empty for explicit single-file deliverable", got)
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

func TestLatestRecoveryTargetPathForSessionInheritsParentExplicitDeliverableForDecomposedChild(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	parentID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	parentDescription := "Produce planning/sambot-feature-spec.md — the comprehensive SamBot chat feature specification for Sam.blog."
	childDescription := "Deliverable: Append these sections to the existing planning/sambot-feature-spec.md file. Do not overwrite existing content — read the file first, then append the new sections after the existing content."
	childMetadata, err := json.Marshal(map[string]any{
		"decomposition_parent_task_id": parentID.String(),
		"recovery_file_write_checkpoint": map[string]any{
			"target_path": "Append",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(childMetadata): %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Append technical architecture, data sources, and UI/UX sections",
			Description:    &childDescription,
			Metadata:       childMetadata,
			WorkStatus:     "in_progress",
		},
		listByProjectTasks: []repo.ProjectTask{
			{
				ID:             parentID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "SamBot spec part 2",
				Description:    &parentDescription,
			},
			{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Append technical architecture, data sources, and UI/UX sections",
				Description:    &childDescription,
				Metadata:       childMetadata,
				WorkStatus:     "in_progress",
			},
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: Append\n",
			},
		},
	}

	got := executor.latestRecoveryTargetPathForSession(context.Background(), workspaceScope{sessionID: &sessionID, taskID: &taskID})
	if got != "planning/sambot-feature-spec.md" {
		t.Fatalf("latestRecoveryTargetPathForSession(...) = %q, want %q", got, "planning/sambot-feature-spec.md")
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

func TestFileReadAllowsReadOnlyVerificationSourcePathDespiteStaleRecoveryTarget(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	sourcePath := "planning/sambot-prompts/test-conversations-level3.md"
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}
	body := "# SamBot Test Conversations — Level 3\n\n## Conversation 1\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(sourcePath)), []byte(body), 0o644); err != nil {
		t.Fatalf("os.WriteFile(source): %v", err)
	}

	description := "The deliverable file planning/sambot-prompts/test-conversations-level3.md already exists in the workspace. This is a read-only verification task for `planning/sambot-prompts/test-conversations-level3.md`. Do not use file.write or file.edit to rewrite `planning/sambot-prompts/test-conversations-level3.md`. Read `planning/sambot-prompts/test-conversations-level3.md`, keep the deliverable unchanged, and summarize the verification findings instead of mutating the file."
	metadata, err := json.Marshal(map[string]any{
		"recovery_file_write_checkpoint": map[string]any{
			"target_path": targetPath,
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
			Title:          "Verify planning/sambot-prompts/test-conversations-level3.md exists and advance",
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
				Content:   "[Recovery resume state]\nTarget file: " + targetPath + "\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": sourcePath})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != nil {
		t.Fatalf("error = %v, out=%v, want nil", got, out)
	}
	if got := out["path"]; got != sourcePath {
		t.Fatalf("path = %v, want %q", got, sourcePath)
	}
	if got := out["content"]; got != body {
		t.Fatalf("content = %q, want %q", got, body)
	}
}

func TestFileReadRejectsRecoveryDirectoryRereadOutsideTarget(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-prompts/test-conversations-level3.md"
	directoryPath := "planning/sambot-prompts"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}

	description := "Author three level-3 complexity SamBot test conversations in Markdown and save to planning/sambot-prompts/test-conversations-level3.md."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Author level-3 SamBot test dialogues file",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: planning/sambot-prompts/test-conversations-level3.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": directoryPath})
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

func TestFileReadRejectsRecoveryNotFoundRereadOutsideTarget(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-prompts/test-conversations-level3.md"
	missingPath := "planning/prd-spec/oc-246-prd.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}

	description := "Author three level-3 complexity SamBot test conversations in Markdown and save to planning/sambot-prompts/test-conversations-level3.md."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Author level-3 SamBot test dialogues file",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: planning/sambot-prompts/test-conversations-level3.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": missingPath})
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

func TestFileReadAllowsRecoveryDependencyArtifactReadNamedInTaskContract(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	dependencyPath := "planning/prd-spec/oc-246-prd.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "prd-spec"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/prd-spec): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(dependencyPath)), []byte("# PRD\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(dependency): %v", err)
	}

	description := "Write planning/sambot-prompts/test-conversations-level3.md containing the final deeply technical SamBot conversation set."
	metadata := json.RawMessage(`{"decomposition":{"source_description":"Write planning/sambot-prompts/test-conversations-level3.md containing the final deeply technical SamBot conversation set. Reference planning/prd-spec/oc-246-prd.md for source requirements."}}`)
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Author level-3 SamBot test dialogues file",
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
				Content:   "[Recovery resume state]\nTarget file: planning/sambot-prompts/test-conversations-level3.md\n",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": dependencyPath})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != nil {
		t.Fatalf("error = %v, out=%v, want nil", got, out)
	}
	if got := out["path"]; got != dependencyPath {
		t.Fatalf("path = %v, want %q", got, dependencyPath)
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

func TestFileReadRejectsRecoveryRereadUsingParentExplicitDeliverableForDecomposedChild(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	parentID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	parentDescription := "Produce planning/sambot-feature-spec.md — the comprehensive SamBot chat feature specification for Sam.blog."
	childDescription := "Deliverable: Append these sections to the existing planning/sambot-feature-spec.md file. Do not overwrite existing content — read the file first, then append the new sections after the existing content."
	childMetadata, err := json.Marshal(map[string]any{
		"decomposition_parent_task_id": parentID.String(),
		"recovery_file_write_checkpoint": map[string]any{
			"target_path": "Append",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal(childMetadata): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "planning", "sambot-feature-spec.md"), []byte("# SamBot Feature Spec\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(spec): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("workspace overview\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(readme): %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Append technical architecture, data sources, and UI/UX sections",
			Description:    &childDescription,
			Metadata:       childMetadata,
			WorkStatus:     "review",
		},
		listByProjectTasks: []repo.ProjectTask{
			{
				ID:             parentID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "SamBot spec part 2",
				Description:    &parentDescription,
			},
			{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Append technical architecture, data sources, and UI/UX sections",
				Description:    &childDescription,
				Metadata:       childMetadata,
				WorkStatus:     "review",
			},
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: Append\n",
			},
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Review only.\nStart with the preferred deliverable target `planning/sambot-feature-spec.md`.\nUse flow.review_decision.",
			},
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		SessionID:      &sessionID,
		TaskID:         &taskID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{"path": "README.md"})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != "recovery_target_focus_required" {
		t.Fatalf("error = %v, out=%v, want recovery_target_focus_required", got, out)
	}
	if got := out["deliverable_path"]; got != "planning/sambot-feature-spec.md" {
		t.Fatalf("deliverable_path = %v, want %q", got, "planning/sambot-feature-spec.md")
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

func TestFileReadAllowsExplicitReviewDeliverableWhenRecoveryTargetPointsAtSiblingSharedFile(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()

	targetPath := "planning/sambot-prompts/test-conversations-level3.md"
	staleRecoveryTarget := "planning/sambot-prompts/test-conversations-technical.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}
	targetBody := "# Deep technical conversations\n\nConversation 1.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(targetBody), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(staleRecoveryTarget)), []byte("# Shared technical conversations\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(stale recovery target): %v", err)
	}

	description := "Write the file planning/sambot-prompts/test-conversations-level3.md containing 3 deeply technical test conversations demonstrating SamBot's adaptive complexity."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write the file planning/sambot-prompts/test-conversations-level3.md containing 3 deeply technical test conversations demonstrating SamBot's adaptive complexity.",
			Description:    &description,
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "system",
				Content:   "[Recovery resume state]\nTarget file: planning/sambot-prompts/test-conversations-technical.md\n",
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
		t.Fatalf("error = %v, out=%v, want nil", got, out)
	}
	if got := out["path"]; got != targetPath {
		t.Fatalf("path = %v, want %s", got, targetPath)
	}
	if got := out["content"]; got != targetBody {
		t.Fatalf("content = %q, want %q", got, targetBody)
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

func TestFileReadTreatsNotDirectoryPreferredTargetAsNotFound(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "sambot/api.js"

	if err := os.WriteFile(filepath.Join(root, "sambot"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(sambot): %v", err)
	}

	description := "Write a Node.js/Express backend API file at sambot/api.js."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write sambot/api.js — Express.js backend API endpoint for SamBot chat",
			Description:    &description,
			WorkStatus:     "review",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content: "Review only.\n" +
					"Start with the preferred deliverable target `sambot/api.js`. Inspect that target directly before broad workspace discovery, and do not begin by listing the repository root unless `sambot/api.js` is missing.\n" +
					"If reading `sambot/api.js` returns `not_found`, `placeholder_deliverable`, or `mismatched_deliverable_context`, stop broad inspection and call flow.review_decision reject using that tool result as evidence.",
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
	if got := fmt.Sprintf("%v", out["error"]); got != "not_found" {
		t.Fatalf("error = %v, want not_found", got)
	}
	if got := out["path"]; got != targetPath {
		t.Fatalf("path = %v, want %s", got, targetPath)
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

func TestFileReadRejectsRuntimeAdvanceCompletionSummaryPlaceholderAtInProgressDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-feature-spec.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := "The deliverable is complete. The runtime will advance the flow.\n\n" +
		"Here's a summary of what was delivered:\n" +
		"## ✅ OC-111\n" +
		"**File:** planning/sambot-feature-spec.md\n" +
		"**Action:** Appended the missing SamBot feature sections.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: Append these sections to the existing planning/sambot-feature-spec.md file. Do not overwrite existing content — read the file first, then append the new sections after the existing content."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Append missing SamBot sections to planning/sambot-feature-spec.md",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: Append missing SamBot sections to planning/sambot-feature-spec.md",
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
	if got := out["deliverable_path"]; got != "planning/sambot-feature-spec.md" {
		t.Fatalf("deliverable_path = %v, want planning/sambot-feature-spec.md", got)
	}
}

func TestFileReadRejectsRuntimeOwnedCommitHandoffPreviousTurnMergeMemoAtPlanningDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-example-conversations.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := "The target file was written successfully in the previous turn (9,144 bytes at `planning/sambot-example-conversations.md`). " +
		"Now I need to commit it and advance the flow execution which is at the Merge step."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-example-conversations.md\n\nWrite the expert SamBot example conversations in markdown."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write expert SamBot example conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Continue the active task recovery now.",
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

func TestFileReadRejectsVerificationReportStubPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The source file exists and contains all 3 deeply technical conversations (14,904 bytes). " +
		"The `results/verify-test-conversations-level3.md` currently contains a stub summary note rather than a real verification report. " +
		"Writing the full verification report now:"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Continue the active task recovery now.",
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
	if got := out["deliverable_path"]; got != "results/verify-test-conversations-level3.md" {
		t.Fatalf("deliverable_path = %v, want results/verify-test-conversations-level3.md", got)
	}
}

func TestFileReadRejectsVerificationReportPlaceholderProseAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The source file exists and is substantive. The `results/verify-test-conversations-level3.md` currently contains placeholder prose rather than a real verification report. Writing it now:"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportSubstantiveSourceStubPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The source file is substantive and complete at 14,904 bytes. The target file `results/verify-test-conversations-level3.md` currently contains a placeholder stub (457 bytes). Writing the full verification report now."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Continue the active task recovery now.",
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
}

func TestFileReadRejectsVerificationReportPathBearingSourceStubPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The source file `planning/sambot-prompts/test-conversations-level3.md` exists and is substantive. The target verification report at `results/verify-test-conversations-level3.md` contains only a stub. Writing the full verification report now:"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportRealWriteStubPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The source file `planning/sambot-prompts/test-conversations-level3.md` exists and is substantive (14,904 bytes, 3 deeply technical conversations). The current `results/verify-test-conversations-level3.md` contains only a placeholder stub (303 bytes). Writing the real verification report now."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportCompletionSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "Both files exist and are complete. The target file `results/verify-test-conversations-level3.md` already contains the full verification report (4,402 bytes, PASS status) and `planning/sambot-prompts/test-conversations-level3.md` contains all 3 deeply technical conversations (14,904 bytes). The deliverable is already in place — no write action is needed.\n\n" +
		"The verification report confirms:\n" +
		"- PASS. The target file satisfies the verification criteria.\n\n" +
		"The recovery target `results/verify-test-conversations-level3.md` is durable and complete. OC-6645 and OC-297 can advance."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Continue the active task recovery now.",
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
	if got := out["deliverable_path"]; got != "results/verify-test-conversations-level3.md" {
		t.Fatalf("deliverable_path = %v, want results/verify-test-conversations-level3.md", got)
	}
}

func TestFileReadRejectsVerificationReportRuntimeAdvanceSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "Both deliverable files are already complete and verified. The flow execution `9dbc7b27-8ab1-4689-b291-b7c58e1d374e` is in `draft` status and cannot advance to review directly — this requires the runtime to activate the work node first. The target file `results/verify-test-conversations-level3.md` is fully written with a ✅ PASS verification report; no further file work is needed."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsRecoveryRetryNarrationPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-prompts/test-conversations-technical.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}
	placeholder := "There's already a solid start — 2 Level 2 conversations and 2 Level 3 conversations are in place. Let me also check for Sam's voice/bio materials before finishing the deliverable."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-prompts/test-conversations-technical.md\n\nWrite level-2 SamBot conversations in authentic voice."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write level-2 SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsPromptConversationMismatchedDeliverableContextReviewMemo(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-prompts/test-conversations-technical.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}
	placeholder := "This file does **not** contain any test conversations. It contains what appears to be an agent's internal reasoning/recovery checkpoint text — not the deliverable content (6 multi-turn technical test conversations covering AI orchestration, ethics/technology, and consulting topics).\n\nThis is a clear case of **mismatched deliverable context**. The file exists but its content is an agent's internal monologue about task management operations, not the required test conversations.\n\nRejecting this review.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-prompts/test-conversations-technical.md\n\nWrite level-2 SamBot conversations in authentic voice."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write level-2 SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsPromptConversationRequiredDeliverableRejectionMemo(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-prompts/test-conversations-technical.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}
	placeholder := "The file at `planning/sambot-prompts/test-conversations-technical.md` does **not** contain the required deliverable. Instead, it contains what appears to be an internal reasoning/checkpoint note from a previous agent session — essentially scratchpad text about task orchestration. This is not a test-conversations document; it's a mismatched deliverable context.\n\nRejecting."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-prompts/test-conversations-technical.md\n\nWrite level-2 SamBot conversations in authentic voice."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write level-2 SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsPromptConversationRecoveryCheckpointNarrationMemo(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-prompts/test-conversations-technical.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}
	placeholder := "The file contains only 2 lines of recovery checkpoint text — not actual test conversations. This is the recovery system intercepting file writes from a previous attempt. The file needs to be overwritten with proper content: three distinct technical test conversations (numbered 4, 5, and 6) with multi-turn exchanges about SamBot's AI/orchestration domain expertise.\n\nLet me also check the level-3 file for reference on the expected format.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-prompts/test-conversations-technical.md\n\nWrite level-2 SamBot conversations in authentic voice."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write level-2 SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsRecoveryCheckpointTaskUpdateMemoAtPromptConversationDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-prompts/test-conversations-technical.md"

	if err := os.MkdirAll(filepath.Join(root, "planning", "sambot-prompts"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning/sambot-prompts): %v", err)
	}
	placeholder := "The recovery checkpoint says: \"Issue one narrow task.update on the current task with child_output_verifications for the concrete completed child or superseding outputs that satisfy the deliverable, set integration_check.status=passed, and set outcome_assessment.satisfied=true. Keep the task in_progress, then call flow.advance.\"\n\nThe child tasks are listed as OC-12663 and OC-12664, both marked \"(verified)\" in the context. I don't have their UUIDs directly, but I can reference them by constructing the child_output_verifications with the parent task ID as the superseding output reference since the target file already exists with substantive content. Let me issue the exact task_update the checkpoint demands, then flow_advance.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-prompts/test-conversations-technical.md\n\nWrite level-2 SamBot conversations in authentic voice."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write level-2 SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsRecoveryCommentaryConfessionPlaceholderAtPlanningDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-example-conversations.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := "I wrote review commentary into the target file instead of the actual deliverable content. Let me read the existing file and the SamBot feature spec for grounding, then write the real expert conversation.\n\n" +
		"This is not the deliverable body."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-example-conversations.md\n\nWrite the paired SamBot example conversations."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write paired example conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsReviewerSummaryPlaceholderAtPlanningDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-example-conversations.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := "The file contains only 362 bytes of what is clearly a worker note or placeholder — not the actual deliverable. The task description requires a complete markdown document with paired conversations and adaptation notes. None of this content is present. The file is a self-referential worker note about needing to write the conversations. Rejecting."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-example-conversations.md\n\nWrite the paired SamBot example conversations."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write paired example conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsPlanningDeliverableReviewAssessmentMemo(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-example-conversations.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := "The full file was returned (6,860 bytes, not truncated). Now let me review this deliverable against the task's requirements.\n\n" +
		"**Task context:**\n- **Task title:** \"Expert version\"\n\n" +
		"**Review assessment:**\nThe deliverable file `planning/sambot-example-conversations.md` is a single consolidated document.\n\n" +
		"**Strengths:**\n1. Strong expert register.\n\n" +
		"**Minor observations (not blocking):**\n- Shared deliverable for both sibling tasks.\n\n" +
		"**Verdict:** The deliverable is complete and ready."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: planning/sambot-example-conversations.md\n\nWrite the paired SamBot example conversations."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write paired example conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportFlowAdvanceDraftStatusPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The flow node execution `9dbc7b27-8ab1-4689-b291-b7c58e1d374e` is in `draft` status and cannot be advanced to `review` — the task work step must be marked active/complete before the flow can transition; this requires the task's work status to be updated or a git commit to trigger the flow advancement."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportFlowAdvanceRuntimeActivationPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The flow node is in `draft` status and cannot be advanced to `review` directly — the work node must first be moved to `in_progress` or `complete` by the runtime before flow.advance is valid. The concrete blocker is: **flow_node_execution `9dbc7b27-8ab1-4689-b291-b7c58e1d374e` is in `draft` status and the runtime must transition it to active before `flow.advance` can succeed.**"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportAlreadyCompleteSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The target file `results/verify-test-conversations-level3.md` is already complete, substantive (6,574 bytes), and passes all verification criteria. The source file `planning/sambot-prompts/test-conversations-level3.md` (14,904 bytes) also exists and is confirmed present. The deliverable is done — no further action required from this recovery turn."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportFinalOutputSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "Both files already exist and are complete. The verification report at `results/verify-test-conversations-level3.md` (4,402 bytes) is fully written with a ✅ PASS status covering all acceptance criteria, and the source deliverable at `planning/sambot-prompts/test-conversations-level3.md` (14,904 bytes) contains all 3 deeply technical conversations. No file mutation is needed — the target file is already the final durable output for this recovery turn."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportPassVerdictSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "Both files already exist and are complete. The verification report at `results/verify-test-conversations-level3.md` correctly assesses `planning/sambot-prompts/test-conversations-level3.md` and returns a **PASS** verdict. The target file is already durable and accurate — no write or repair is needed."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportNoRepairNeededSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "Both files exist and are complete. The source file `planning/sambot-prompts/test-conversations-level3.md` is 14,904 bytes with 3 deeply technical conversations, and `results/verify-test-conversations-level3.md` already contains a thorough verification report that passes all acceptance criteria. The verification report is accurate and complete — no repair needed."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportCompleteAndSubstantiveStubPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The source file `planning/sambot-prompts/test-conversations-level3.md` is complete and substantive. The `results/verify-test-conversations-level3.md` file exists but contains only a stub. Writing the full verification report now:"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportSourceAndReportCompleteSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "Both the source file (`planning/sambot-prompts/test-conversations-level3.md`) and the verification report (`results/verify-test-conversations-level3.md`) already exist and are complete. The verification report is substantive and accurate. The task is already in a fully complete state — no file mutation is needed."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportParentOrchestrationStatusMemoAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The evidence is clear. The parent task OC-297 has **two key child tasks still blocked** (OC-6646 and OC-6649) and **five more child tasks still in draft**. Only two of nine actionable children are `done` (OC-328 and OC-6645). The parent's completion gate requires all child tasks to complete before the parent can finish integration, and that gate is not met.\n\nThe verification report at `results/verify-test-conversations-level3.md` exists and is well-written, but the parent orchestration is incomplete: blocked and draft children remain unresolved."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{SessionID: sessionID, Role: "user", Content: "Continue the active task recovery now."}},
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
}

func TestFileReadRejectsVerificationReportSourceAndReportCompleteSummaryPlaceholderWithTaskIDOnlyInInput(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "Both the source file (`planning/sambot-prompts/test-conversations-level3.md`) and the verification report (`results/verify-test-conversations-level3.md`) already exist and are complete. The verification report is substantive and accurate. The task is already in a fully complete state — no file mutation is needed."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
	})
	out, err := executor.Execute(ctx, "file.read", map[string]any{
		"path":    targetPath,
		"task_id": taskID.String(),
	})
	if err != nil {
		t.Fatalf("executor.Execute(file.read): %v", err)
	}
	if got := out["error"]; got != "placeholder_deliverable" {
		t.Fatalf("error = %v, want placeholder_deliverable", got)
	}
}

func TestFileReadRejectsVerificationReportIntentNarrationPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The file already exists and is fully substantive — 3 deeply technical multi-turn conversations covering distributed consensus (Raft vs Paxos), ML inference (KV cache + VRAM math for LLaMA-3), and event sourcing/CQRS. Now writing the verification report."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Continue the active task recovery now.",
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
}

func TestFileReadRejectsReviewerSummaryPlaceholderAtInProgressPlanningDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-feature-spec.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := "The file content is clearly not a valid deliverable. It contains 988 bytes of meta-commentary about what would be delivered rather than actual spec content. There is no `## UI/UX Design` section, no chat widget placement details, no conversation flow patterns, no mobile responsiveness guidance, no conversation starters, and no error state definitions. The file is a self-referential summary describing what sections \"would\" contain.\n\n" +
		"This is a `mismatched_deliverable_context` — rejecting.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: Append these sections to the existing planning/sambot-feature-spec.md file. Do not overwrite existing content — read the file first, then append the new sections after the existing content."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Append missing SamBot sections to planning/sambot-feature-spec.md",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: Append missing SamBot sections to planning/sambot-feature-spec.md",
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
	if got := out["deliverable_path"]; got != "planning/sambot-feature-spec.md" {
		t.Fatalf("deliverable_path = %v, want planning/sambot-feature-spec.md", got)
	}
}

func TestFileReadRejectsReviewEvidenceSummaryPlaceholderAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "sambot/widget.html"

	if err := os.MkdirAll(filepath.Join(root, "sambot"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(sambot): %v", err)
	}
	placeholder := "The file at `sambot/widget.html` returned `mismatched_deliverable_context`. Per the review protocol, this is a disqualifying result — the deliverable does not match the scope of this task (OC-175). Rejecting now with this evidence.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "sambot/widget.html (or sambot/index.html) — the frontend chat widget"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			TaskNumber:     175,
			Title:          "sambot/widget.html (or sambot/index.html) — the frontend chat widget",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: sambot/widget.html (or sambot/index.html) — the frontend chat widget",
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsRecoveryGroundingNarrationPlaceholderAtPlanningDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-example-conversations.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := "I need to ground myself on what this task requires and what SamBot context exists before writing the expert conversation. Let me check the key artifacts quickly."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: Write expert-level SamBot example conversations to planning/sambot-example-conversations.md. Produce the actual conversation corpus instead of planning or recovery narration."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write expert-level SamBot example conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.chatSessions = &fakeChatSessionRepo{
		sessions: []repo.ChatSession{{
			ID:             sessionID,
			OrganizationID: orgID,
			ScopeType:      "project_task",
			ScopeID:        taskID,
			Mode:           "async",
			Status:         "active",
		}},
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsReviewEvidenceSummaryPlaceholderWithReviewInstructionsAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The file read returned `mismatched_deliverable_context`. Per my review instructions, this is a terminal rejection condition — I should not proceed with broad inspection and should reject immediately using this tool result as evidence."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Continue the active task recovery now.",
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsReviewEvidenceSummaryPlaceholderWithTheReviewInstructionsAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "results/verify-test-conversations-level3.md"

	if err := os.MkdirAll(filepath.Join(root, "results"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(results): %v", err)
	}
	placeholder := "The file content is a self-referential placeholder stating `mismatched_deliverable_context` — it is not an actual verification report. Per the review instructions, this is a terminal rejection condition."
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: results/verify-test-conversations-level3.md\n\nRead planning/sambot-prompts/test-conversations-level3.md and write a Markdown verification report that confirms the target conversations meet the depth and voice criteria."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write results/verify-test-conversations-level3.md — verification report confirming 3 deeply technical SamBot test conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Continue the active task recovery now.",
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsPlaceholderEvidenceSummaryAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "sambot/widget.html"

	if err := os.MkdirAll(filepath.Join(root, "sambot"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(sambot): %v", err)
	}
	placeholder := "The file returned `placeholder_deliverable` — it contains only placeholder narration, not an actual frontend chat widget implementation. Per the review protocol, this is dispositive. Rejecting now.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "sambot/widget.html (or sambot/index.html) — the frontend chat widget"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			TaskNumber:     175,
			Title:          "sambot/widget.html (or sambot/index.html) — the frontend chat widget",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: sambot/widget.html (or sambot/index.html) — the frontend chat widget",
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsPlaceholderNarrationSummaryAtExplicitDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "sambot/widget.html"

	if err := os.MkdirAll(filepath.Join(root, "sambot"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(sambot): %v", err)
	}
	placeholder := "The file at `sambot/widget.html` is a placeholder — it contains only narrative text, not an actual frontend chat widget implementation. Per the review protocol, a `placeholder_deliverable` result is dispositive grounds for rejection.\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "sambot/widget.html (or sambot/index.html) — the frontend chat widget"
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			TaskNumber:     175,
			Title:          "sambot/widget.html (or sambot/index.html) — the frontend chat widget",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{
			{
				SessionID: sessionID,
				Role:      "user",
				Content:   "Start work on task: sambot/widget.html (or sambot/index.html) — the frontend chat widget",
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsTaskBriefEchoPlaceholderAtInProgressPlanningDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-mvp-spec.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := `# Write SamBot MVP implementation plan to planning/sambot-mvp-spec.md (replaces blocked OC-170)

## Objective
## Deliverable
Write a complete SamBot MVP implementation plan to ` + "`planning/sambot-mvp-spec.md`" + `.

## What to produce
A Markdown document covering:

1. **Build Order** — Numbered, sequential steps to implement SamBot MVP.

## Context
- The feature spec exists at ` + "`planning/sambot-feature-spec.md`" + ` — read it first
- The architecture spec exists at ` + "`planning/sambot-architecture.md`" + ` — read it first
- This is a WRITE task — produce the actual .md file, do not describe intent to write it

## DO NOT
- Write the file to ` + "`planning/sambot-mvp-spec.md`" + ` using file_write or python3 via cli_execute.

## Validation Criteria
- Define explicit pass/fail checks for each relevant stage.

## Evidence Expectations
- Reference the concrete files, logs, screenshots, or outputs that should exist when the work is complete.
`
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: Write a complete SamBot MVP implementation plan to planning/sambot-mvp-spec.md. Reference planning/sambot-feature-spec.md and planning/sambot-architecture.md, but produce the actual spec file rather than task instructions."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write SamBot MVP implementation plan to planning/sambot-mvp-spec.md",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{
			SessionID: sessionID,
			Role:      "user",
			Content:   "Start work on task: Write SamBot MVP implementation plan to planning/sambot-mvp-spec.md",
		}},
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
	if got := out["deliverable_path"]; got != "planning/sambot-mvp-spec.md" {
		t.Fatalf("deliverable_path = %v, want planning/sambot-mvp-spec.md", got)
	}
}

func TestFileReadRejectsPromptConversationPlaceholderAtRootConversationCorpusPath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "test-conversations-level3.md"

	placeholder := `# Append deeply technical state machines conversation to test-conversations-level3.md

## Objective
**Deliverable:** Append one deeply technical (Level 3) test conversation to ` + "`planning/sambot-prompts/test-conversations-level3.md`" + `.

**Topic:** State machines in distributed orchestration systems.

**Format:** Multi-turn conversation (6-10 exchanges) between a User and SamBot. The user is a senior engineer asking deeply technical questions about state machine design patterns in distributed systems.

## Validation Criteria
- Define explicit pass/fail checks for each relevant stage.

## Evidence Expectations
- Reference the concrete files, logs, screenshots, or outputs that should exist when the work is complete.
`
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "**Deliverable:** Append one deeply technical (Level 3) test conversation to `planning/sambot-prompts/test-conversations-level3.md`. **Format:** Multi-turn conversation (6-10 exchanges) between a User and SamBot."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Append deeply technical state machines conversation to test-conversations-level3.md",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.messages = &fakeMessageRepo{
		messages: []repo.ChatMessage{{
			SessionID: sessionID,
			Role:      "user",
			Content:   "Start work on task: Append deeply technical state machines conversation to test-conversations-level3.md",
		}},
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
	}
}

func TestFileReadRejectsCompactValidationScaffoldPlaceholderAtPlanningDeliverablePath(t *testing.T) {
	root := t.TempDir()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	sessionID := uuid.New()
	targetPath := "planning/sambot-example-conversations.md"

	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(planning): %v", err)
	}
	placeholder := `# Expert version — the same or similar topic handled at professional/technical depth, with SamBot demonstrating Sam's deep expertise

## Objective
Expert version — the same or similar topic handled at professional/technical depth, with SamBot demonstrating Sam's deep expertise.

## Validation Criteria
- Define explicit pass/fail checks for each relevant stage.
- Note the required evidence or observable outputs for each check.
- Call out key failure conditions or edge cases reviewers should expect to verify.

## Evidence Expectations
- Reference the concrete files, logs, screenshots, or outputs that should exist when the work is complete.
`
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(targetPath)), []byte(placeholder), 0o644); err != nil {
		t.Fatalf("os.WriteFile(target): %v", err)
	}

	description := "Deliverable: Write expert-level SamBot example conversations to planning/sambot-example-conversations.md. Produce the actual conversation corpus instead of a task brief or validation scaffold."
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	executor.tasks = &mockTaskRepo{
		task: repo.ProjectTask{
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Title:          "Write expert-level SamBot example conversations",
			Description:    &description,
			WorkStatus:     "in_progress",
		},
	}
	executor.chatSessions = &fakeChatSessionRepo{
		sessions: []repo.ChatSession{{
			ID:             sessionID,
			OrganizationID: orgID,
			ScopeType:      "project_task",
			ScopeID:        taskID,
			Mode:           "async",
			Status:         "active",
		}},
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
		t.Fatalf("deliverable_path = %v, want %q", got, targetPath)
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
