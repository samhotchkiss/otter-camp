package turn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
)

func TestRecoveryPersistedDraftContentSkipsPlaceholderWorkspaceDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "sam-blog"
	orgSlug := "default"
	targetPath := "docs/migration-plan/oc-15-content-migration-plan.md"
	placeholder := "Good. I have the context. OC-15 (content migration) is in the Work node and needs execution. Now write the migration plan:"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	metadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    targetPath,
		FailureReason: "recovery halted after cli.execute was retried without command",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}

	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "in_progress",
				Metadata:       metadata,
			},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	workspaceFile := filepath.Join(dataDir, "workspaces", projectSlug, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(workspaceFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(workspaceFile, []byte(placeholder), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	if draft, rejectReason := recoveryResumeDraftForPrompt("recovery halted after cli.execute was retried without command", targetPath, placeholder); draft != "" || !strings.Contains(rejectReason, "intent to write the deliverable") {
		t.Fatalf("recoveryResumeDraftForPrompt draft=%q rejectReason=%q, want rejected placeholder", draft, rejectReason)
	}
	state, ok := fixture.engine.loadRecoveryResumeState(context.Background(), rt)
	if !ok {
		t.Fatal("loadRecoveryResumeState = false, want true")
	}
	if state.targetDraft != "" {
		t.Fatalf("state.targetDraft = %q, want empty after placeholder rejection", state.targetDraft)
	}
	if !strings.Contains(state.targetDraftRejectedReason, "intent to write the deliverable") {
		t.Fatalf("state.targetDraftRejectedReason = %q, want placeholder rejection", state.targetDraftRejectedReason)
	}

	draft, rejectReason, ok := fixture.engine.recoveryPersistedDraftContent(context.Background(), rt, targetPath)
	if ok {
		t.Fatalf("ok = true, want false with poisoned persisted draft %q", draft)
	}
	if rejectReason != "" {
		t.Fatalf("rejectReason = %q, want empty when persisted draft is skipped", rejectReason)
	}
}
