package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"log/slog"
)

func TestListeningEvalSkippedForSyncSinglePending(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("hello"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "hello"}, nil
	}
	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		t.Fatal("listening eval should be skipped for sync with single pending message")
		return ModelResponse{}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
	if fixture.model.listeningEvalCalls != 0 {
		t.Fatalf("listening eval calls = %d, want 0", fixture.model.listeningEvalCalls)
	}
}

func TestTurnRecoverableWorktreeRemoveError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "not a working tree",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: '/tmp/task-10' is not a working tree"),
			want: true,
		},
		{
			name: "legacy git file corruption",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' is not a .git file, error code 7"),
			want: true,
		},
		{
			name: "legacy not a git repository",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' not a git repository"),
			want: true,
		},
		{
			name: "missing dot git",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' does not exist"),
			want: true,
		},
		{
			name: "unrelated git error",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: branch is currently checked out"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := turnRecoverableWorktreeRemoveError(tc.err); got != tc.want {
				t.Fatalf("turnRecoverableWorktreeRemoveError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestTurnRecoverableWorktreeAddError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "path already exists",
			err:  errors.New("git worktree add --force -b task/15 /tmp/task-15: exit status 128: fatal: '/tmp/task-15' already exists"),
			want: true,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "unrelated",
			err:  errors.New("git worktree add --force -b task/15 /tmp/task-15: exit status 128: fatal: not a git repository"),
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := turnRecoverableWorktreeAddError(tc.err); got != tc.want {
				t.Fatalf("turnRecoverableWorktreeAddError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestEnsureTurnTaskWorktreeCreatesOrphanBranchForUnbornRepo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	worktreeRoot := filepath.Join(t.TempDir(), "task-12")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run(repoRoot, "init", "-b", "main")

	if err := ensureTurnTaskWorktree(ctx, repoRoot, worktreeRoot, "task/12", "main"); err != nil {
		t.Fatalf("ensureTurnTaskWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreeRoot, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = worktreeRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git symbolic-ref failed: %v\n%s", err, string(out))
	}
	if got := strings.TrimSpace(string(out)); got != "task/12" {
		t.Fatalf("branch = %q, want task/12", got)
	}
}

func TestSanitizeInheritedRunAttributionDropsTerminalRun(t *testing.T) {
	pool := testdb.New(t)
	engine := &TurnEngine{pool: pool}
	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "sanitize-inherited-run-attribution-drops-terminal",
		DisplayName: "Sanitize Inherited Run Attribution Drops Terminal",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	runID := uuid.New()
	runStepID := uuid.New()
	runAttemptID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id, organization_id, principal_type, principal_id, status, trigger_type, version, metadata, completed_at
		) VALUES (
			$1, $2, 'agent', $3, 'failed', 'scheduler', 1, '{}'::jsonb, now()
		)
	`, runID, org.ID, uuid.New()); err != nil {
		t.Fatalf("insert failed run: %v", err)
	}

	gotRunID, gotRunStepID, gotRunAttemptID := engine.sanitizeInheritedRunAttribution(ctx, &runID, &runStepID, &runAttemptID)
	if gotRunID != nil || gotRunStepID != nil || gotRunAttemptID != nil {
		t.Fatalf("sanitizeInheritedRunAttribution() = (%v, %v, %v), want nil ids for failed run", gotRunID, gotRunStepID, gotRunAttemptID)
	}
}

func TestSanitizeInheritedRunAttributionKeepsActiveRun(t *testing.T) {
	pool := testdb.New(t)
	engine := &TurnEngine{pool: pool}
	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "sanitize-inherited-run-attribution-keeps-active",
		DisplayName: "Sanitize Inherited Run Attribution Keeps Active",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	runID := uuid.New()
	runStepID := uuid.New()
	runAttemptID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO run (
			id, organization_id, principal_type, principal_id, status, trigger_type, version, metadata, started_at
		) VALUES (
			$1, $2, 'agent', $3, 'in_progress', 'scheduler', 1, '{}'::jsonb, now()
		)
	`, runID, org.ID, uuid.New()); err != nil {
		t.Fatalf("insert active run: %v", err)
	}

	gotRunID, gotRunStepID, gotRunAttemptID := engine.sanitizeInheritedRunAttribution(ctx, &runID, &runStepID, &runAttemptID)
	if gotRunID == nil || *gotRunID != runID {
		t.Fatalf("sanitizeInheritedRunAttribution() runID = %v, want %s", gotRunID, runID)
	}
	if gotRunStepID == nil || *gotRunStepID != runStepID {
		t.Fatalf("sanitizeInheritedRunAttribution() runStepID = %v, want %s", gotRunStepID, runStepID)
	}
	if gotRunAttemptID == nil || *gotRunAttemptID != runAttemptID {
		t.Fatalf("sanitizeInheritedRunAttribution() runAttemptID = %v, want %s", gotRunAttemptID, runAttemptID)
	}
}

func TestSyncBoundFlowExecutionTurnOwnershipClearsStaleLiveRunID(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	engine := &TurnEngine{pool: pool}

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "sync-bound-flow-execution-turn-ownership-clears-stale-run",
		DisplayName: "Sync Bound Flow Execution Turn Ownership Clears Stale Run",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "sync-bound-flow-execution-turn-ownership-clears-stale-run-project",
		DisplayName:    "Sync Bound Flow Execution Turn Ownership Clears Stale Run Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "sync-bound-flow-execution-turn-ownership-clears-stale-run-template",
		DisplayName:    "Sync Bound Flow Execution Turn Ownership Clears Stale Run Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       1,
		MaxVisits:      2,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Review task",
		WorkStatus:     "review",
		BlocksScope:    "task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    &org.ID,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sessionID := uuid.New()
	staleRunID := uuid.New()
	turnID := uuid.New()

	if _, err := pool.Exec(ctx, `
		INSERT INTO chat_session (
			id, organization_id, scope_type, scope_id, mode, status, metadata, created_by_type, created_by_id
		) VALUES (
			$1, $2, 'project_task', $3, 'async', 'active', $4::jsonb, 'system', $5
		)
	`, sessionID, org.ID, taskRecord.ID, mustJSONRaw(map[string]any{}), uuid.New()); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	createdExecution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &sessionID,
		Metadata: repo.FlowExecutionMetadataWithLiveOwner(json.RawMessage(`{}`), repo.FlowExecutionLiveOwner{
			RunID:  &staleRunID,
			TurnID: nil,
		}),
	})
	if err != nil {
		t.Fatalf("insert execution: %v", err)
	}
	executionID := createdExecution.ID
	if _, err := pool.Exec(ctx, `
		UPDATE chat_session
		SET metadata = $2::jsonb
		WHERE id = $1
	`, sessionID, mustJSONRaw(map[string]any{"flow_node_execution_id": executionID.String()})); err != nil {
		t.Fatalf("update session metadata: %v", err)
	}

	session := &chat.ChatSession{
		ID:             sessionID,
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		Metadata:       mustJSONRaw(map[string]any{"flow_node_execution_id": executionID.String()}),
	}
	if err := engine.syncBoundFlowExecutionTurnOwnership(ctx, session, &turnID, nil); err != nil {
		t.Fatalf("syncBoundFlowExecutionTurnOwnership: %v", err)
	}

	execution, err := repo.NewFlowNodeExecutionRepo(pool).GetByID(ctx, executionID)
	if err != nil {
		t.Fatalf("reload execution: %v", err)
	}
	liveOwner := repo.FlowExecutionLiveOwnerFromMetadata(execution.Metadata)
	if liveOwner.RunID != nil {
		t.Fatalf("live owner run_id = %v, want nil", liveOwner.RunID)
	}
	if liveOwner.TurnID == nil || *liveOwner.TurnID != turnID {
		t.Fatalf("live owner turn_id = %v, want %s", liveOwner.TurnID, turnID)
	}
}

func TestHasQueuedAgentTurnForSessionIgnoresCompletedBootstrapDispatch(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	engine := &TurnEngine{pool: pool}

	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "has-queued-agent-turn-ignores-completed-bootstrap",
		DisplayName: "Has Queued Agent Turn Ignores Completed Bootstrap",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "has-queued-agent-turn-ignores-completed-bootstrap-project",
		DisplayName:    "Has Queued Agent Turn Ignores Completed Bootstrap Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		Metadata:       mustJSONRaw(map[string]any{"project_bootstrap": map[string]any{"status": "completed"}}),
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue bootstrap from persisted state.",
		Status:    "pending",
		Metadata:  mustJSONRaw(map[string]any{"source": "project_bootstrap", "auto_continue": true}),
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO job_queue (id, job_type, priority, payload, status, attempts, max_attempts, run_after, created_at, updated_at)
		VALUES ($1, $2, 100, $3::jsonb, 'pending', 0, 3, now(), now(), now())
	`, uuid.New(), AgentTurnJobType, mustJSONRaw(map[string]any{
		"session_id": session.ID.String(),
		"message_id": message.ID.String(),
	})); err != nil {
		t.Fatalf("insert pending agent_turn: %v", err)
	}

	queued, err := engine.hasQueuedAgentTurnForSession(ctx, session.ID, nil)
	if err != nil {
		t.Fatalf("hasQueuedAgentTurnForSession: %v", err)
	}
	if queued {
		t.Fatal("expected completed-bootstrap dispatch to be ignored")
	}
}

func TestLogicalMessageCancelledUsesLatestAttemptForTriggerMessage(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	messageID := uuid.New()
	sessionID := fixture.session.ID
	agentID := fixture.chat.participants[0].ParticipantID

	cancelledTurnID := uuid.New()
	completedRetryTurnID := uuid.New()
	trigger := messageID
	fixture.chat.turns[cancelledTurnID] = &chat.ChatTurn{
		ID:               cancelledTurnID,
		SessionID:        sessionID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agentID,
		Status:           "cancelled",
		TriggerMessageID: &trigger,
		RetryCount:       0,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, cancelledTurnID)
	fixture.chat.turns[completedRetryTurnID] = &chat.ChatTurn{
		ID:               completedRetryTurnID,
		SessionID:        sessionID,
		TurnNumber:       2,
		RespondingType:   "agent",
		RespondingID:     agentID,
		Status:           "completed",
		TriggerMessageID: &trigger,
		RetryCount:       1,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, completedRetryTurnID)

	cancelled, err := fixture.engine.logicalMessageCancelled(context.Background(), sessionID, messageID)
	if err != nil {
		t.Fatalf("logicalMessageCancelled: %v", err)
	}
	if cancelled {
		t.Fatal("logicalMessageCancelled = true, want false when later retry is not cancelled")
	}
}

func TestListeningEvalRunsForAsyncSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "continuation"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.listeningEvalCalls != 1 {
		t.Fatalf("listening eval calls = %d, want 1", fixture.model.listeningEvalCalls)
	}
}

func TestListeningEvalSkippedForAsyncProjectTaskSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		t.Fatal("listening eval should be skipped for async project_task sessions")
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.listeningEvalCalls != 0 {
		t.Fatalf("listening eval calls = %d, want 0", fixture.model.listeningEvalCalls)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
}

func TestListeningEvalSkippedForActiveProjectBootstrapSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active","current_phase":"staffing_persisted"}}`)
	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		t.Fatal("listening eval should be skipped for active project bootstrap sessions")
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.listeningEvalCalls != 0 {
		t.Fatalf("listening eval calls = %d, want 0", fixture.model.listeningEvalCalls)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
}

func TestHandleUserMessageFailsWhenInvocationCompletionFails(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.invocations.updateCompletionErr = errors.New("update completion failed")
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "continue"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil {
		t.Fatal("HandleUserMessage err = nil, want invocation completion failure")
	}
	if !strings.Contains(err.Error(), "update completion failed") {
		t.Fatalf("HandleUserMessage err = %v, want update completion failed", err)
	}
}

func TestListeningEvalWaitReenqueuesAndSkipsPhase2(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "wait"}, nil
		}
		return ModelResponse{Content: "respond"}, nil
	}
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		t.Fatal("phase 2 model call should be skipped when listening eval returns wait")
		return ModelResponse{}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.listeningEvalCalls != 1 {
		t.Fatalf("listening eval calls = %d, want 1", fixture.model.listeningEvalCalls)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.payload == nil {
		t.Fatal("agent_turn payload missing")
	}
	if job.payload.SessionID != fixture.session.ID || job.payload.MessageID != fixture.userMessageID {
		t.Fatalf("unexpected re-enqueue payload: %+v", *job.payload)
	}
	if job.runAfter == nil {
		t.Fatal("expected run_after to be set")
	}
	wantRunAfter := base.Add(fixture.engine.listeningEvalDelay)
	if !job.runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", job.runAfter, wantRunAfter)
	}
	if !job.runAfter.After(base) {
		t.Fatalf("run_after = %s, want > %s", job.runAfter, base)
	}
}

func TestHandleUserMessageSummarizeEnqueueUsesBackgroundPriority(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.summarization = &fakeSummarizationChecker{shouldSummarize: true}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	agentJobs := fixture.enqueuer.agentTurnJobs()
	if len(agentJobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(agentJobs))
	}
	summarizeJobs := fixture.enqueuer.jobsByType(chat.ChatSummarizeJobType)
	if len(summarizeJobs) != 1 {
		t.Fatalf("chat_summarize jobs = %d, want 1", len(summarizeJobs))
	}
	if summarizeJobs[0].priority != backgroundSummarizeJobPriority {
		t.Fatalf("chat_summarize priority = %d, want %d", summarizeJobs[0].priority, backgroundSummarizeJobPriority)
	}
	if summarizeJobs[0].priority >= fixture.engine.jobPriority {
		t.Fatalf("chat_summarize priority = %d, want below agent_turn priority %d", summarizeJobs[0].priority, fixture.engine.jobPriority)
	}
}

func TestHandleUserMessagePartialStreamContextCanceledFailsTurnWithoutCancelling(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("partial"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{}, context.Canceled
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HandleUserMessage err = %v, want context.Canceled", err)
	}
	if fixture.chat.cancelCalls != 0 {
		t.Fatalf("cancel calls = %d, want 0", fixture.chat.cancelCalls)
	}
	if fixture.chat.failCalls != 1 {
		t.Fatalf("fail calls = %d, want 1", fixture.chat.failCalls)
	}
	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", turn.Status)
	}
}

func TestHandleUserMessageTaskSessionClosedDuringToolDispatchCancelsTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "task.inspect", Tier: "tier1"}}}
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{
			ToolCalls: []ModelToolCall{{
				ID:        "call_1",
				Name:      "task.inspect",
				Tier:      "tier1",
				Arguments: map[string]any{},
			}},
		}, nil
	}
	fixture.dispatcher.tier1Fn = func(context.Context, ToolCall) (ToolResult, error) {
		fixture.chat.session.Status = "closed"
		return ToolResult{}, context.Canceled
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if !errors.Is(err, errTurnCancelled) {
		t.Fatalf("HandleUserMessage err = %v, want %v", err, errTurnCancelled)
	}
	if fixture.chat.cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", fixture.chat.cancelCalls)
	}
	if fixture.chat.failCalls != 0 {
		t.Fatalf("fail calls = %d, want 0", fixture.chat.failCalls)
	}
	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.Status != "cancelled" {
		t.Fatalf("turn status = %q, want cancelled", turn.Status)
	}
}

func TestHandleUserMessageAsyncExecutionSessionIdempotentForStableMessageKey(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("first HandleUserMessage: %v", err)
	}
	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("second HandleUserMessage: %v", err)
	}

	if got := len(fixture.chat.turnOrder); got != 1 {
		t.Fatalf("turn count = %d, want 1", got)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}

	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.TriggerMessageID == nil || *turn.TriggerMessageID != fixture.userMessageID {
		t.Fatalf("trigger_message_id = %v, want %s", turn.TriggerMessageID, fixture.userMessageID)
	}
	if turn.RetryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", turn.RetryCount)
	}
}

func TestHandleTurnJobDuplicateDeliveryDoesNotCreateSecondTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
			JobType: AgentTurnJobType,
			Payload: payload,
		}); err != nil {
			t.Fatalf("HandleTurnJob[%d]: %v", i, err)
		}
	}

	if got := len(fixture.chat.turnOrder); got != 1 {
		t.Fatalf("turn count = %d, want 1", got)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
}

func TestHandleTurnJobCancelledMessageDoesNotCreateTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	metadata, err := chat.MergeAgentTurnDispatchCancelledMetadata(message.Metadata, "unit-test", time.Now().UTC())
	if err != nil {
		t.Fatalf("merge cancelled metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), fixture.userMessageID, metadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	if got := len(fixture.chat.turnOrder); got != 0 {
		t.Fatalf("turn count = %d, want 0", got)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}
}

func TestHandleTurnJobRetryAttemptCreatesDistinctTurnAndRecordsRetryState(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("retry-" + req.TurnID.String()[:8]); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	firstPayload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal first payload: %v", err)
	}
	retryPayload, err := json.Marshal(AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: 1,
	})
	if err != nil {
		t.Fatalf("marshal retry payload: %v", err)
	}

	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{JobType: AgentTurnJobType, Payload: firstPayload}); err != nil {
		t.Fatalf("HandleTurnJob first: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{JobType: AgentTurnJobType, Payload: firstPayload}); err != nil {
		t.Fatalf("HandleTurnJob duplicate: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{JobType: AgentTurnJobType, Payload: retryPayload}); err != nil {
		t.Fatalf("HandleTurnJob retry: %v", err)
	}

	if got := len(fixture.chat.turnOrder); got != 2 {
		t.Fatalf("turn count = %d, want 2", got)
	}
	if fixture.model.streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", fixture.model.streamCalls)
	}
	if !fixture.messages.containsContent("[Retry attempt 1 started.]") {
		t.Fatal("missing retry state system message")
	}

	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	secondTurn := fixture.chat.turnByID(fixture.chat.turnOrder[1])
	if firstTurn == nil || secondTurn == nil {
		t.Fatal("expected both turns to be present")
	}
	if firstTurn.RetryCount != 0 || secondTurn.RetryCount != 1 {
		t.Fatalf("retry counts = [%d %d], want [0 1]", firstTurn.RetryCount, secondTurn.RetryCount)
	}
	if firstTurn.TriggerMessageID == nil || secondTurn.TriggerMessageID == nil {
		t.Fatal("trigger_message_id should be set on both turns")
	}
	if *firstTurn.TriggerMessageID != fixture.userMessageID || *secondTurn.TriggerMessageID != fixture.userMessageID {
		t.Fatalf("trigger_message_ids = [%v %v], want %s", firstTurn.TriggerMessageID, secondTurn.TriggerMessageID, fixture.userMessageID)
	}
}

func TestHandleUserMessageEventProjectScopeFrankHandoffRoutesToExistingParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := fixture.chat.participants[0].ParticipantID
	workerID := uuid.New()
	loriID := uuid.New()
	actorID := frankID

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{
		{
			ID:              uuid.New(),
			SessionID:       fixture.session.ID,
			ParticipantType: "agent",
			ParticipantID:   frankID,
		},
		{
			ID:              uuid.New(),
			SessionID:       fixture.session.ID,
			ParticipantType: "agent",
			ParticipantID:   workerID,
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID:  {ID: frankID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Frank", AgentType: "general"},
			workerID: {ID: workerID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Builder", AgentType: "worker"},
			loriID:   {ID: loriID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Lori", AgentType: "pm"},
		},
		starter: []repo.Agent{
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
		},
	}

	err := fixture.engine.HandleUserMessageEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.message.user_sent",
		ActorType:      "agent",
		ActorID:        &actorID,
		Payload: mustRawJSON(t, map[string]any{
			"session_id": fixture.session.ID,
			"message_id": fixture.userMessageID,
		}),
	})
	if err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.AgentID == nil {
		t.Fatal("agent_turn payload missing routed agent")
	}
	if *jobs[0].payload.AgentID != workerID {
		t.Fatalf("payload.agent_id = %v, want %s", jobs[0].payload.AgentID, workerID)
	}
}

func TestHandleUserMessageEventProjectScopeFrankHandoffFallsBackToLori(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := fixture.chat.participants[0].ParticipantID
	loriID := uuid.New()
	actorID := frankID

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{
		{
			ID:              uuid.New(),
			SessionID:       fixture.session.ID,
			ParticipantType: "agent",
			ParticipantID:   frankID,
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Frank", AgentType: "general"},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Lori", AgentType: "pm"},
		},
		starter: []repo.Agent{
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
		},
	}

	err := fixture.engine.HandleUserMessageEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.message.user_sent",
		ActorType:      "agent",
		ActorID:        &actorID,
		Payload: mustRawJSON(t, map[string]any{
			"session_id": fixture.session.ID,
			"message_id": fixture.userMessageID,
		}),
	})
	if err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.AgentID == nil {
		t.Fatal("agent_turn payload missing routed agent")
	}
	if *jobs[0].payload.AgentID != loriID {
		t.Fatalf("payload.agent_id = %v, want %s", jobs[0].payload.AgentID, loriID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasLori := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == loriID {
			hasLori = true
			break
		}
	}
	if !hasLori {
		t.Fatalf("expected Lori %s to be added as a session participant", loriID)
	}
}

func TestHandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.engine.modelRetryBudget = 1

	retryAfter := 8*time.Minute + 4*time.Second
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, NewRateLimitedError(retryAfter, errors.New("http status 429"))
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn retries = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.payload == nil {
		t.Fatal("retry payload missing")
	}
	if job.payload.RetryCount != 1 {
		t.Fatalf("retry_count = %d, want 1", job.payload.RetryCount)
	}
	if !job.payload.RateLimitJitterApplied {
		t.Fatal("expected rate limit retry payload to be marked as jittered")
	}
	if job.runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantRunAfter := base.Add(jitteredRateLimitRetryDelay(retryAfter, fixture.session.ID, fixture.userMessageID, 0))
	if !job.runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *job.runAfter, wantRunAfter)
	}

	wantDelayMessage := fmt.Sprintf("[Rate limited, retrying in %s...]", formatRetryDelay(jitteredRateLimitRetryDelay(retryAfter, fixture.session.ID, fixture.userMessageID, 0)))
	if !fixture.messages.containsContent(wantDelayMessage) {
		t.Fatal("missing rate-limited retry status message")
	}
	if fixture.messages.containsContentSubstring("[Turn failed:") {
		t.Fatal("unexpected generic turn failed message for rate-limited retry")
	}
}

func TestHandleTurnJobRateLimitedUsesBackoffWhenNoRetryHint(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.engine.modelRetryBudget = 1

	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, NewRateLimitedError(0, errors.New("http status 429"))
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn retries = %d, want 1", len(jobs))
	}
	if !jobs[0].payload.RateLimitJitterApplied {
		t.Fatal("expected rate limit retry payload to be marked as jittered")
	}
	if jobs[0].runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantRunAfter := base.Add(defaultRateLimitBackoff)
	if !jobs[0].runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *jobs[0].runAfter, wantRunAfter)
	}
	if !fixture.messages.containsContent("[Rate limited, retrying in 30s...]") {
		t.Fatal("missing backoff retry status message")
	}
}

func TestHandleTurnJobRateLimitedCapsProviderHintAtMaxBackoff(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.engine.modelRetryBudget = 1

	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, NewRateLimitedError(42*time.Hour, errors.New("http status 429"))
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn retries = %d, want 1", len(jobs))
	}
	if jobs[0].runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantDelay := jitteredRateLimitRetryDelay(maxRateLimitBackoff, fixture.session.ID, fixture.userMessageID, 0)
	wantRunAfter := base.Add(wantDelay)
	if !jobs[0].runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *jobs[0].runAfter, wantRunAfter)
	}
	wantDelayMessage := fmt.Sprintf("[Rate limited, retrying in %s...]", formatRetryDelay(wantDelay))
	if !fixture.messages.containsContent(wantDelayMessage) {
		t.Fatal("missing capped rate-limited retry status message")
	}
}

func TestHandleTurnJobRateLimitedRetryCapStopsRequeue(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.modelRetryBudget = 1
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, NewRateLimitedError(45*time.Second, errors.New("http status 429"))
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: maxRateLimitRetries,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn retries = %d, want 0", len(jobs))
	}
	if !fixture.messages.containsContent("[Turn failed: model retries exhausted after 5 attempts.]") {
		t.Fatal("missing retries exhausted status message")
	}
}

func TestHandleTurnJobTransientInfrastructureEnqueuesRetry(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.engine.modelRetryBudget = 1

	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, errors.New("failed to connect to database: FATAL: remaining connection slots are reserved for roles with the SUPERUSER attribute (SQLSTATE 53300)")
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn retries = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil {
		t.Fatal("retry payload missing")
	}
	if jobs[0].payload.RetryCount != 1 {
		t.Fatalf("retry_count = %d, want 1", jobs[0].payload.RetryCount)
	}
	if jobs[0].runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantRunAfter := base.Add(defaultTransientInfraBackoff)
	if !jobs[0].runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *jobs[0].runAfter, wantRunAfter)
	}
	if !fixture.messages.containsContent("[Infrastructure temporarily unavailable, retrying in 15s...]") {
		t.Fatal("missing transient infrastructure retry status message")
	}
	if fixture.messages.containsContentSubstring("[Turn failed:") {
		t.Fatal("unexpected generic turn failed message for transient infrastructure retry")
	}
}

func TestHandleTurnJobTransientInfrastructureRetryCapStopsRequeue(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.modelRetryBudget = 1
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, errors.New("pq: sorry, too many clients already")
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: maxTransientInfraRetries,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn retries = %d, want 0", len(jobs))
	}
	if !fixture.messages.containsContent("[Turn failed: temporary infrastructure retries exhausted after 5 attempts.]") {
		t.Fatal("missing transient infrastructure retries exhausted status message")
	}
}

func TestHandleTurnCompletedEventEnqueuesAutoContinuation(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Investigating the next step.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil {
		t.Fatal("auto continuation payload missing")
	}
	if jobs[0].payload.SessionID != fixture.session.ID {
		t.Fatalf("payload.session_id = %s, want %s", jobs[0].payload.SessionID, fixture.session.ID)
	}
	if jobs[0].payload.MessageID != fixture.userMessageID {
		t.Fatalf("payload.message_id = %s, want %s", jobs[0].payload.MessageID, fixture.userMessageID)
	}
	if jobs[0].payload.AgentID == nil || *jobs[0].payload.AgentID != agentID {
		t.Fatalf("payload.agent_id = %v, want %s", jobs[0].payload.AgentID, agentID)
	}
	if jobs[0].runAfter == nil {
		t.Fatal("auto continuation run_after missing")
	}
	wantRunAfter := base.Add(defaultAutoContinueDelay)
	if !jobs[0].runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *jobs[0].runAfter, wantRunAfter)
	}
}

func TestAppendReviewActionStateRootsHistoryForReviewTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	executionID := uuid.New()
	runID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				ProjectID:  uuid.New(),
				WorkStatus: "review",
			},
		},
	}
	turn := &chat.ChatTurn{
		ID:             uuid.New(),
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   fixture.chat.participants[0].ParticipantID,
		Status:         "in_progress",
	}
	fixture.chat.turns[turn.ID] = turn
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, turn.ID)
	initialMessage, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID initial user message: %v", err)
	}
	initialMessage.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_queue_processor",
		"run_id":                 runID.String(),
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(initialMessage)
	rt := &turnRuntime{
		session:          fixture.session,
		turn:             turn,
		initialMessageID: fixture.userMessageID,
	}

	appended, err := fixture.engine.appendReviewActionState(context.Background(), rt, false)
	if err != nil {
		t.Fatalf("appendReviewActionState: %v", err)
	}
	if !appended {
		t.Fatal("appendReviewActionState = false, want true")
	}
	if rt.historyStartID == nil {
		t.Fatal("historyStartID = nil, want synthetic review action message")
	}
	message, getErr := fixture.messages.GetByID(context.Background(), *rt.historyStartID)
	if getErr != nil {
		t.Fatalf("GetByID historyStartID: %v", getErr)
	}
	if !strings.Contains(message.Content, "flow.review_decision") {
		t.Fatalf("review action prompt = %q, want flow.review_decision guidance", message.Content)
	}
	if !strings.Contains(message.Content, executionID.String()) {
		t.Fatalf("review action prompt = %q, want execution id %s", message.Content, executionID)
	}
	if promptRunID := runIDFromMetadata(message.Metadata); promptRunID == nil || *promptRunID != runID {
		t.Fatalf("review action prompt run_id = %v, want %s", promptRunID, runID)
	}
}

func TestHandleCompletedReviewTurnWithoutDecisionRewritesRecoveryResumeToReviewPrompt(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	executionID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				ProjectID:  uuid.New(),
				WorkStatus: "review",
			},
		},
	}

	latestCompleted := &repo.ChatTurn{
		ID:           uuid.New(),
		SessionID:    fixture.session.ID,
		Status:       "completed",
		RespondingID: fixture.chat.participants[0].ParticipantID,
		RetryCount:   0,
	}
	latestUser := repo.ChatMessage{
		ID:        uuid.New(),
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                 "supervisor",
			"synthetic_user_message": true,
			"flow_node_execution_id": executionID.String(),
		}),
	}
	fixture.messages.create(latestUser)

	handled, err := fixture.engine.handleCompletedReviewTurnWithoutDecision(
		context.Background(),
		fixture.session,
		repo.ProjectTask{ID: taskID, WorkStatus: "review"},
		latestCompleted,
		&latestUser,
		"I'm ready to assist as the reviewer.",
	)
	if err != nil {
		t.Fatalf("handleCompletedReviewTurnWithoutDecision: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	retryMessage, getErr := fixture.messages.GetByID(context.Background(), jobs[0].payload.MessageID)
	if getErr != nil {
		t.Fatalf("GetByID retry message: %v", getErr)
	}
	if !strings.Contains(retryMessage.Content, "flow.review_decision") {
		t.Fatalf("retry message = %q, want review-decision prompt", retryMessage.Content)
	}
	if !strings.Contains(retryMessage.Content, executionID.String()) {
		t.Fatalf("retry message = %q, want execution id %s", retryMessage.Content, executionID)
	}
}

func TestAppendRecoveryResumeStateSkipsReviewTasks(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    "test-result-state-machine.log",
		ArtifactPath:  ".ottercamp/recovery/test-result-state-machine.log",
		FailureReason: "assistant draft for test-result-state-machine.log described intent to write the deliverable instead of the file body",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				ProjectID:  uuid.New(),
				WorkStatus: "review",
				Metadata:   checkpointMetadata,
			},
		},
	}
	turn := &chat.ChatTurn{
		ID:             uuid.New(),
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   fixture.chat.participants[0].ParticipantID,
		Status:         "in_progress",
	}
	fixture.chat.turns[turn.ID] = turn
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, turn.ID)
	rt := &turnRuntime{
		session:      fixture.session,
		turn:         turn,
		recoveryTurn: true,
	}

	appended, err := fixture.engine.appendRecoveryResumeState(context.Background(), rt, false)
	if err != nil {
		t.Fatalf("appendRecoveryResumeState: %v", err)
	}
	if appended {
		t.Fatal("appendRecoveryResumeState = true, want false for review task")
	}
	if rt.historyStartID != nil {
		t.Fatalf("historyStartID = %v, want nil when review task skips recovery state", *rt.historyStartID)
	}
}

func TestHandleTurnCompletedEventRetriesReviewTurnWithoutDecision(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	runID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "review"},
		},
	}

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = fixture.engine.buildTaskReviewActionPrompt(context.Background(), fixture.session)
	message.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_review_action",
		"synthetic_user_message": true,
		"run_id":                 runID.String(),
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I reviewed the deliverables and can now make a decision.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != 1 {
		t.Fatalf("payload.retry_count = %#v, want 1", jobs[0].payload)
	}
	if jobs[0].payload.FlowNodeExecutionID == nil || *jobs[0].payload.FlowNodeExecutionID != executionID {
		t.Fatalf("payload.flow_node_execution_id = %v, want %s", jobs[0].payload.FlowNodeExecutionID, executionID)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}
	retryMessage, err := fixture.messages.GetByID(context.Background(), jobs[0].payload.MessageID)
	if err != nil {
		t.Fatalf("GetByID retry message: %v", err)
	}
	if retryRunID := runIDFromMetadata(retryMessage.Metadata); retryRunID == nil || *retryRunID != runID {
		t.Fatalf("retry message run_id = %v, want %s", retryRunID, runID)
	}
}

func TestHandleTurnCompletedEventRetriesReviewTurnWithoutDecisionRestoresRunIDFromSessionHistory(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700000001, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	runID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "review"},
		},
	}

	fixture.messages.create(repo.ChatMessage{
		ID:        uuid.New(),
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "Start review on task: historical kickoff",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                 "task_queue_processor",
			"run_id":                 runID.String(),
			"flow_node_execution_id": executionID.String(),
		}),
	})

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = fixture.engine.buildTaskReviewActionPrompt(context.Background(), fixture.session)
	message.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_review_action",
		"synthetic_user_message": true,
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I reviewed the deliverables and can now make a decision.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	retryMessage, err := fixture.messages.GetByID(context.Background(), jobs[0].payload.MessageID)
	if err != nil {
		t.Fatalf("GetByID retry message: %v", err)
	}
	if retryRunID := runIDFromMetadata(retryMessage.Metadata); retryRunID == nil || *retryRunID != runID {
		t.Fatalf("retry message run_id = %v, want restored %s", retryRunID, runID)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedReviewTurnWithoutDecision(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "review"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = fixture.engine.buildTaskReviewActionPrompt(context.Background(), fixture.session)
	message.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_review_action",
		"synthetic_user_message": true,
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I have reviewed the artifacts and am ready to decide.")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "flow.review_decision") {
		t.Fatalf("blocked reason = %q, want review decision reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
	guard, ok := tasksvc.ParseValidationGuard(updated.Metadata)
	if !ok {
		t.Fatal("expected validation guard metadata")
	}
	if !guard.Blocked {
		t.Fatal("expected blocked validation guard")
	}
	if guard.ToolName != "flow.review_decision" {
		t.Fatalf("guard.ToolName = %q, want flow.review_decision", guard.ToolName)
	}
	if guard.FailureCode != "review_decision_required" {
		t.Fatalf("guard.FailureCode = %q, want review_decision_required", guard.FailureCode)
	}
	if guard.LastTurnID != turnID.String() {
		t.Fatalf("guard.LastTurnID = %q, want %s", guard.LastTurnID, turnID)
	}
}

func TestHandleTurnCompletedEventRetriesEmptyReviewTurnWithoutDecisionAtRetryLimit(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700002000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "review"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = fixture.engine.buildTaskReviewActionPrompt(context.Background(), fixture.session)
	message.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_review_action",
		"synthetic_user_message": true,
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != maxGenericRecoveryReplyRetries+1 {
		t.Fatalf("payload.retry_count = %#v, want %d", jobs[0].payload, maxGenericRecoveryReplyRetries+1)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}
	if len(blocker.calls) != 0 {
		t.Fatalf("blocked calls = %d, want 0", len(blocker.calls))
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", updated.WorkStatus)
	}
	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawSystem bool
	var retryPrompt *chat.ChatMessage
	for i := range messages {
		msg := messages[i]
		if msg.Role == "system" && strings.Contains(msg.Content, "empty assistant output") {
			sawSystem = true
		}
		if msg.ID == jobs[0].payload.MessageID {
			retryPrompt = &msg
		}
	}
	if !sawSystem {
		t.Fatal("missing empty-output retry system message")
	}
	if retryPrompt == nil {
		t.Fatal("missing retry prompt message")
	}
	if !strings.Contains(retryPrompt.Content, "flow.review_decision") {
		t.Fatalf("retry prompt = %q, want flow.review_decision guidance", retryPrompt.Content)
	}
	if !strings.Contains(retryPrompt.Content, executionID.String()) {
		t.Fatalf("retry prompt = %q, want flow execution id %s", retryPrompt.Content, executionID)
	}
}

func TestHandleCompletedProjectBootstrapEmptyAssistantTurnRetriesWithFreshBootstrapPrompt(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700002100, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	fixture.session.ScopeType = "project"
	fixture.session.Mode = "async"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.Mode = fixture.session.Mode
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	updatedAt := base.Add(-time.Minute)
	lastProgressAt := base.Add(-2 * time.Minute)

	state := projectBootstrapState{
		Status:           projectBootstrapStatusActive,
		CurrentPhase:     projectBootstrapCheckpointStaffingPersisted,
		InitialMessageID: fixture.userMessageID.String(),
		AutoTurnCount:    1,
		AssignmentCount:  0,
		PlannedTaskCount: 0,
		LastResponderID:  fixture.chat.participants[0].ParticipantID.String(),
		UpdatedAt:        &updatedAt,
		LastProgressAt:   &lastProgressAt,
	}
	metadata, err := projectBootstrapMetadataJSON(nil, state)
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	userMessage, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	userMessage.Content = "Continue the active project bootstrap from the persisted state above."
	userMessage.Metadata = mustRawJSON(t, map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": fixture.userMessageID.String(),
		"bootstrap_auto_turn_count":    1,
	})
	fixture.messages.upsert(userMessage)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "")

	turns, err := fixture.engine.turns.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	latestCompleted := latestCompletedTurn(turns)
	if latestCompleted == nil || latestCompleted.ID != turnID {
		t.Fatalf("latestCompleted = %#v, want turn %s", latestCompleted, turnID)
	}
	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	assistant := latestAssistantFinalForTurn(messages, turnID)
	if assistant == nil {
		t.Fatal("assistant message missing")
	}
	progress := projectBootstrapProgress{ValidationStatus: projectBootstrapValidationPending}

	handled, err := fixture.engine.handleCompletedProjectBootstrapEmptyAssistantTurn(
		context.Background(),
		fixture.session,
		latestCompleted,
		&userMessage,
		assistant,
		messages,
		state,
		progress,
		base,
	)
	if err != nil {
		t.Fatalf("handleCompletedProjectBootstrapEmptyAssistantTurn: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != 1 {
		t.Fatalf("payload.retry_count = %#v, want 1", jobs[0].payload)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}

	retryPrompt, err := fixture.messages.GetByID(context.Background(), jobs[0].payload.MessageID)
	if err != nil {
		t.Fatalf("GetByID retry prompt: %v", err)
	}
	if !strings.Contains(retryPrompt.Content, "Your last bootstrap follow-on turn returned empty assistant output.") {
		t.Fatalf("retry prompt = %q, want explicit empty-output warning", retryPrompt.Content)
	}
	if !strings.Contains(retryPrompt.Content, "Do not reply with blank text") {
		t.Fatalf("retry prompt = %q, want blank-text warning", retryPrompt.Content)
	}
	retryMetadata := messageMetadataMap(retryPrompt.Metadata)
	if got := strings.TrimSpace(stringValue(retryMetadata["source"])); got != projectBootstrapSource {
		t.Fatalf("retry prompt source = %q, want %q", got, projectBootstrapSource)
	}
	if got := strings.TrimSpace(stringValue(retryMetadata["bootstrap_initial_message_id"])); got != fixture.userMessageID.String() {
		t.Fatalf("retry prompt bootstrap_initial_message_id = %q, want %s", got, fixture.userMessageID)
	}

	messages, err = fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages after retry: %v", err)
	}
	var sawSystem bool
	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "Bootstrap follow-on turn returned empty assistant output") {
			sawSystem = true
			break
		}
	}
	if !sawSystem {
		t.Fatal("missing bootstrap empty-output retry system message")
	}
}

func TestHandleTurnCompletedEventAutoRejectsExplicitReviewDecision(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "review"},
		},
	}
	fixture.flowAdvancer.tasks = taskRepo
	fixture.engine.flowAdvancer = fixture.flowAdvancer

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = fixture.engine.buildTaskReviewActionPrompt(context.Background(), fixture.session)
	message.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_review_action",
		"synthetic_user_message": true,
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "## REVIEW DECISION: REJECT FOR REWORK\n\nI am rejecting this task because the deliverables remain incomplete.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if fixture.flowAdvancer.rejectFlowCalls != 1 {
		t.Fatalf("reject flow calls = %d, want 1", fixture.flowAdvancer.rejectFlowCalls)
	}
	if fixture.flowAdvancer.lastRejectActor.Type != "agent" || fixture.flowAdvancer.lastRejectActor.ID != agentID {
		t.Fatalf("reject actor = %+v, want agent %s", fixture.flowAdvancer.lastRejectActor, agentID)
	}
}

func TestHandleTurnCompletedEventSkipsWhenCompletionMessagePresent(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Task complete and ready for review.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventAdvancesFlowFromSuccessfulGitCommit(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Task complete and ready for review.")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "git.commit",
		"output": map[string]any{
			"sha":             "fa05fa0abc123456",
			"files_committed": 1,
		},
	})

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.recordNodeCommitCalls != 1 {
		t.Fatalf("record node commit calls = %d, want 1", fixture.flowAdvancer.recordNodeCommitCalls)
	}
	if fixture.flowAdvancer.lastCommitSHA != "fa05fa0abc123456" {
		t.Fatalf("last commit sha = %q, want fa05fa0abc123456", fixture.flowAdvancer.lastCommitSHA)
	}
	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}
	if fixture.flowAdvancer.lastAdvanceActor.Type != "agent" || fixture.flowAdvancer.lastAdvanceActor.ID != agentID {
		t.Fatalf("advance actor = %+v, want agent %s", fixture.flowAdvancer.lastAdvanceActor, agentID)
	}
	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review after successful completion", updatedTask.WorkStatus)
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventCreatesCanonicalCommitFromDeliverableWrite(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	projectSlug := "task-commit-" + uuid.NewString()[:8]
	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	description := "Complete the task deliverable. Output: deliverable.md"
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				TaskNumber:        7,
				Title:             "Write Deliverable",
				Description:       &description,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
				Metadata: mustRawJSON(t, map[string]any{
					"planning": map[string]any{
						"mode": "execution_first",
					},
				}),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.flowAdvancer.activeExecution = &repo.FlowNodeExecution{
		TaskID:      taskID,
		FlowNodeID:  nodeID,
		VisitNumber: 1,
		Status:      "active",
	}
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work", DisplayName: "Work"},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: fixture.session.OrganizationID,
				Slug:           projectSlug,
			},
		},
	}

	projectRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "deliverable.md"), []byte("deliverable body\n"), 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Deliverable written.")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "file.write",
		"output": map[string]any{
			"path":      "deliverable.md",
			"byte_size": 17,
		},
	})

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.recordNodeCommitCalls != 1 {
		t.Fatalf("record node commit calls = %d, want 1", fixture.flowAdvancer.recordNodeCommitCalls)
	}
	if strings.TrimSpace(fixture.flowAdvancer.lastCommitSHA) == "" {
		t.Fatal("last commit sha = empty, want canonical runtime commit")
	}
	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}

	messageBytes, err := exec.Command("git", "-C", projectRoot, "log", "-1", "--pretty=%B").CombinedOutput()
	if err != nil {
		t.Fatalf("git log latest commit: %v: %s", err, strings.TrimSpace(string(messageBytes)))
	}
	message := strings.TrimSpace(string(messageBytes))
	if !strings.Contains(message, "flow(work:work#1): Write Deliverable") {
		t.Fatalf("latest commit message = %q, want canonical work commit header", message)
	}
}

func TestHandleTurnCompletedEventAdvancesFlowFromDurableRecoveryWrite(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	projectSlug := "recovery-commit-" + uuid.NewString()[:8]
	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.flowAdvancer.activeExecution = &repo.FlowNodeExecution{
		TaskID:      taskID,
		FlowNodeID:  nodeID,
		VisitNumber: 1,
		Status:      "active",
	}
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work", DisplayName: "Work"},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: fixture.session.OrganizationID,
				Slug:           projectSlug,
			},
		},
	}
	projectRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	if err := os.MkdirAll(filepath.Join(projectRoot, "docs", "migration-plan"), 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "docs", "migration-plan", "oc-15-content-migration-plan.md"), []byte("recovery body\n"), 0o644); err != nil {
		t.Fatalf("write recovery deliverable: %v", err)
	}

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Let me write the complete migration plan now:")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "file.write",
		"output": map[string]any{
			"path":      "docs/migration-plan/oc-15-content-migration-plan.md",
			"byte_size": 13720,
			"created":   false,
		},
	})

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}
	if fixture.flowAdvancer.recordNodeCommitCalls != 1 {
		t.Fatalf("record node commit calls = %d, want 1", fixture.flowAdvancer.recordNodeCommitCalls)
	}
	if strings.TrimSpace(fixture.flowAdvancer.lastCommitSHA) == "" {
		t.Fatal("last commit sha = empty, want canonical runtime commit before recovery advance")
	}
	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review after durable recovery write", updatedTask.WorkStatus)
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventKickoffDurableWriteRecordsCanonicalCommit(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	projectSlug := "kickoff-commit-" + uuid.NewString()[:8]
	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	description := "Execute happy path test. Output: test_execution_happy_path.md with evidence."
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				TaskNumber:        11,
				Title:             "Happy Path Test - Core Routing",
				Description:       &description,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.flowAdvancer.activeExecution = &repo.FlowNodeExecution{
		TaskID:      taskID,
		FlowNodeID:  nodeID,
		VisitNumber: 1,
		Status:      "active",
	}
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work", DisplayName: "Work"},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: fixture.session.OrganizationID,
				Slug:           projectSlug,
			},
		},
	}

	projectRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "test_execution_happy_path.md"), []byte("# execution log\n"), 0o644); err != nil {
		t.Fatalf("write kickoff deliverable: %v", err)
	}

	userMessage, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	userMessage.Content = "Start work on task: Happy Path Test - Core Routing\n\nTask description:\nExecute happy path test. Output: test_execution_happy_path.md with evidence."
	userMessage.Status = "final"
	fixture.messages.upsert(userMessage)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'll update the execution log now.")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "file.write",
		"output": map[string]any{
			"path":      "test_execution_happy_path.md",
			"byte_size": 16,
			"created":   false,
		},
	})

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.recordNodeCommitCalls != 1 {
		t.Fatalf("record node commit calls = %d, want 1", fixture.flowAdvancer.recordNodeCommitCalls)
	}
	if strings.TrimSpace(fixture.flowAdvancer.lastCommitSHA) == "" {
		t.Fatal("last commit sha = empty, want canonical runtime commit before kickoff advance")
	}
	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}
}

func TestHandleTurnCompletedEventDuplicateCompletionSignalAdvancesExactlyOnce(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Task complete and ready for review.")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "git.commit",
		"output": map[string]any{
			"sha":             "fa05fa0abc123456",
			"files_committed": 1,
		},
	})

	event := eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}
	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), event); err != nil {
		t.Fatalf("first HandleTurnCompletedEvent: %v", err)
	}
	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), event); err != nil {
		t.Fatalf("second HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}
	if fixture.flowAdvancer.recordNodeCommitCalls != 1 {
		t.Fatalf("record node commit calls = %d, want 1", fixture.flowAdvancer.recordNodeCommitCalls)
	}
}

func TestHandleTurnCompletedEventSkipsAtAutoContinuationCap(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	var latestTurnID uuid.UUID
	for i := 0; i < maxConsecutiveAutoTurns+1; i++ {
		latestTurnID = createCompletedTurnWithAssistantMessage(t, fixture, agentID, fmt.Sprintf("loop step %d", i+1))
	}

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": latestTurnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventRetriesGenericRecoveryReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700001000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to help. What do you need?")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != 1 {
		t.Fatalf("payload.retry_count = %#v, want 1", jobs[0].payload)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to assist. What would you like me to do?")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReplyHelpWithVariant(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready. What would you like me to help with for this task?")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReplyPleaseSpecifyVariant(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	reply := "I'm ready to assist with the Speaker Pipeline Ops Validation project.\n\nWhat do you need me to do?\n\nPlease specify:\n1. What action should I take?"
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, reply)
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReplyStatusInventoryVariant(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	reply := "I'm ready to assist with the Speaker Pipeline Ops Validation task.\n\n**Current Status**: Task is in the Work node of an active flow execution.\n\nI have access to:\n- Planning artifacts\n- Prior task outputs"
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, reply)
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReplyWorkspaceCheckVariant(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	reply := "I'll help you resume the validation synthesis task. Let me first check the current state of the workspace and the task."
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, reply)
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestNormalizeRecoveryCheckpointTargetForTaskRepointsExecutionFirstReportAwayFromPlanning(t *testing.T) {
	taskRecord := repo.ProjectTask{
		TaskNumber: 13,
		Title:      "Synthesize Validation Findings & Report",
		Metadata: mustRawJSON(t, map[string]any{
			"planning": map[string]any{
				"mode": "execution_first",
			},
		}),
	}
	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   "planning/discovery-plan/oc-13-validation-plan.md",
		ArtifactPath: ".ottercamp/recovery/planning/discovery-plan/oc-13-validation-plan.md",
	}

	normalized := normalizeRecoveryCheckpointTargetForTask(taskRecord, checkpoint)
	if normalized.TargetPath != "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md" {
		t.Fatalf("normalized target_path = %q, want Work report path", normalized.TargetPath)
	}
	if normalized.ArtifactPath != checkpoint.ArtifactPath {
		t.Fatalf("artifact_path = %q, want %q", normalized.ArtifactPath, checkpoint.ArtifactPath)
	}
}

func TestNormalizeRecoveryCheckpointTargetForTaskPrefersExplicitDeliverablePath(t *testing.T) {
	description := "Design the core data model for speaking opportunities in the pipeline. Output: schema-definition.md with complete field specifications."
	taskRecord := repo.ProjectTask{
		TaskNumber:  9,
		Title:       "Design Pipeline Data Schema & Fields",
		Description: &description,
		Metadata: mustRawJSON(t, map[string]any{
			"planning": map[string]any{
				"mode": "execution_first",
			},
		}),
	}
	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   "planning/strategy-artifact/oc-9-success-narrative.md",
		ArtifactPath: ".ottercamp/recovery/planning/strategy-artifact/oc-9-success-narrative.md",
	}

	normalized := normalizeRecoveryCheckpointTargetForTask(taskRecord, checkpoint)
	if normalized.TargetPath != "schema-definition.md" {
		t.Fatalf("normalized target_path = %q, want explicit deliverable path", normalized.TargetPath)
	}
	if normalized.ArtifactPath != checkpoint.ArtifactPath {
		t.Fatalf("artifact_path = %q, want %q", normalized.ArtifactPath, checkpoint.ArtifactPath)
	}
}

func TestNormalizeRecoveryCheckpointTargetForTaskKeepsPlanningTargetForNonReportTask(t *testing.T) {
	taskRecord := repo.ProjectTask{
		TaskNumber: 7,
		Title:      "Validate task sizing and dependencies",
		Metadata: mustRawJSON(t, map[string]any{
			"planning": map[string]any{
				"mode": "execution_first",
			},
		}),
	}
	checkpoint := taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath: "planning/discovery-plan/oc-7-validation-plan.md",
	}

	normalized := normalizeRecoveryCheckpointTargetForTask(taskRecord, checkpoint)
	if normalized.TargetPath != checkpoint.TargetPath {
		t.Fatalf("normalized target_path = %q, want unchanged %q", normalized.TargetPath, checkpoint.TargetPath)
	}
}

func TestPreferredTaskDeliverablePathInfersTestExecutionLogTarget(t *testing.T) {
	description := "Execute test scenario 2 (edge cases): test capacity limits, concurrent assignments, boundary conditions. Log all test cases and verify system behavior under stress."
	taskRecord := repo.ProjectTask{
		TaskNumber:  13,
		Title:       "OC-4: Execute Test Scenario 2 (Edge Cases)",
		Description: &description,
	}

	if got := preferredTaskDeliverablePath(taskRecord); got != "Test/test-execution-oc13-scenario2-edge-cases.md" {
		t.Fatalf("preferredTaskDeliverablePath = %q, want test execution log target", got)
	}
}

func TestPreferredTaskDeliverablePathInfersHappyPathExecutionLogTarget(t *testing.T) {
	description := "Execute the happy-path scenario end-to-end against the real speaker pipeline product. Capture screenshots, logs, and evidence of successful completion at each step."
	taskRecord := repo.ProjectTask{
		TaskNumber:  17,
		Title:       "Execute happy-path scenario",
		Description: &description,
	}

	if got := preferredTaskDeliverablePath(taskRecord); got != "Test/test-execution-oc17-happy-path-scenario.md" {
		t.Fatalf("preferredTaskDeliverablePath = %q, want happy-path execution log target", got)
	}
}

func TestPreferredTaskDeliverablePathInfersValidationExecutionLogTarget(t *testing.T) {
	description := "Execute capacity test: submit registrations when pipeline at 90% and 100% capacity. Verify expected responses and behaviors. Record results. ~25 min."
	taskRecord := repo.ProjectTask{
		TaskNumber:  27,
		Title:       "Validation execution: test pipeline capacity rejection",
		Description: &description,
	}

	if got := preferredTaskDeliverablePath(taskRecord); got != "Test/test-execution-oc27-test-pipeline-capacity-rejection.md" {
		t.Fatalf("preferredTaskDeliverablePath = %q, want validation execution log target", got)
	}
}

func TestPreferredTaskDeliverablePathInfersCanonicalDocumentTarget(t *testing.T) {
	taskRecord := repo.ProjectTask{
		TaskNumber: 17,
		Title:      "Design scenario: speaker registration with complete profile data",
	}

	if got := preferredTaskDeliverablePath(taskRecord); got != "Work/OC-17-DESIGN-SCENARIO-SPEAKER-REGISTRATION-WITH-COMPLETE-PROFILE-DATA.md" {
		t.Fatalf("preferredTaskDeliverablePath = %q, want canonical document target", got)
	}
}

func TestInferredTaskExecutionLogDraftBuildsConcreteStarter(t *testing.T) {
	description := "Execute test scenario 3 (error handling): malformed input, agent unavailability, timeout conditions. Verify system gracefully handles errors."
	taskRecord := repo.ProjectTask{
		TaskNumber:  14,
		Title:       "OC-5: Execute Test Scenario 3 (Error Handling)",
		Description: &description,
	}

	draft := inferredTaskExecutionLogDraft(taskRecord)
	if !strings.Contains(draft, "# OC-5: Execute Test Scenario 3 (Error Handling) Execution Log") {
		t.Fatalf("draft = %q, want execution log heading", draft)
	}
	if !strings.Contains(draft, "Malformed input") || !strings.Contains(draft, "Agent unavailability") || !strings.Contains(draft, "Timeout conditions") {
		t.Fatalf("draft = %q, want concrete scenario coverage", draft)
	}
}

func TestReconcileRecoveryCheckpointCandidateNormalizesExecutionFirstReportTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Synthesize validation findings into a single report with recommendations."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  13,
				WorkStatus:  "blocked",
				Title:       "Synthesize Validation Findings & Report",
				Description: &description,
				Metadata: mustRawJSON(t, map[string]any{
					"planning": map[string]any{
						"mode": "execution_first",
					},
				}),
			},
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
	}

	checkpoint := fixture.engine.reconcileRecoveryCheckpointCandidate(context.Background(), rt, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   "planning/discovery-plan/oc-13-validation-plan.md",
		ArtifactPath: ".ottercamp/recovery/planning/discovery-plan/oc-13-validation-plan.md",
	})
	if checkpoint.TargetPath != "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md" {
		t.Fatalf("TargetPath = %q, want normalized work report path", checkpoint.TargetPath)
	}
}

func TestHandleTurnCompletedEventRetriesGenericTaskContinuationReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700002000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = "Continue the active task now from the continuation summary above."
	message.Metadata = taskContinuationResumeMessageMetadata(nil, 1)
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to help with OC-24. What would you like me to do?")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != 1 {
		t.Fatalf("payload.retry_count = %#v, want 1", jobs[0].payload)
	}
	if jobs[0].payload.MessageID == fixture.userMessageID {
		t.Fatalf("payload.message_id = %s, want fresh retry prompt message", jobs[0].payload.MessageID)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}
	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	latest := messages[len(messages)-1]
	if latest.ID != jobs[0].payload.MessageID {
		t.Fatalf("latest message id = %s, want %s", latest.ID, jobs[0].payload.MessageID)
	}
	if latest.Role != "user" {
		t.Fatalf("latest role = %q, want user", latest.Role)
	}
	if latest.Content != message.Content {
		t.Fatalf("latest content = %q, want retry prompt copy", latest.Content)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericTaskContinuationReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = "Continue the active task now from the continuation summary above."
	message.Metadata = taskContinuationResumeMessageMetadata(nil, 1)
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to help with OC-24. What would you like me to do?")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventRetriesGenericTaskQueueKickoffReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700003000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = "Start work on task: Test speaker list endpoint"
	message.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_queue_processor",
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to assist. I'm a Data Integration Tester specializing in end-to-end validation.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != 1 {
		t.Fatalf("payload.retry_count = %#v, want 1", jobs[0].payload)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericTaskQueueKickoffReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = "Start work on task: Test speaker list endpoint"
	message.Metadata = mustRawJSON(t, map[string]any{
		"source":                 "task_queue_processor",
		"flow_node_execution_id": executionID.String(),
	})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to assist. I'm a Data Integration Tester specializing in end-to-end validation.")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventSkipsAutoContinuationForRecoveryHaltMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
				Metadata:          json.RawMessage(`{}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	authorType := "human_user"
	userMessage, err := fixture.chat.AppendMessage(context.Background(), chat.AppendMessageInput{
		SessionID:  fixture.session.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.session.OrganizationID,
		Role:       "user",
		Content:    "supervisor recovery: resume task",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	recoveryMetadata := mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	if _, err := fixture.messages.UpdateMetadata(context.Background(), userMessage.ID, recoveryMetadata); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnRecord, _, err := fixture.engine.turns.CreateForMessageAttempt(context.Background(), fixture.session.ID, agentID, userMessage.ID, 3)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := fixture.chat.CompleteTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	authorType = "agent"
	fixture.messages.create(repo.ChatMessage{
		SessionID:  fixture.session.ID,
		TurnID:     &turnRecord.ID,
		AuthorType: &authorType,
		AuthorID:   &agentID,
		Role:       "assistant",
		Status:     "final",
		Content:    "Perfect. I have the substantive draft and now I'll write it to the target file.",
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnRecord.ID,
		Role:      "system",
		Status:    "final",
		Content:   "[Recovery turn halted: recovered file.write for `docs/target.md` produced another non-substantive draft after the prior checkpoint already rejected placeholder narration.]",
	})

	metadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRepo.items[taskID].Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    "docs/target.md",
		ArtifactPath:  ".ottercamp/recovery/docs/target.md",
		FailureReason: "repeated non-substantive recovery drafts for docs/target.md across explicit resume attempts; latest assistant draft for docs/target.md described intent to write the deliverable instead of the file body",
		HaltTurnID:    turnRecord.ID.String(),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	record := taskRepo.items[taskID]
	record.Metadata = metadata
	taskRepo.items[taskID] = record

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnRecord.ID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventSkipsWhenProjectPaused(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"pause":{"is_paused":true,"reason":"operator pause","metadata":{}}}`),
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Investigating the next step.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestIsAlreadyQueuedTaskTransition(t *testing.T) {
	if !isAlreadyQueuedTaskTransition(tasksvc.ErrInvalidStatusTransition{From: "queued", To: "queued"}) {
		t.Fatal("queued -> queued transition should be treated as already queued")
	}
	if isAlreadyQueuedTaskTransition(tasksvc.ErrInvalidStatusTransition{From: "draft", To: "queued"}) {
		t.Fatal("draft -> queued transition should not be treated as already queued")
	}
	if isAlreadyQueuedTaskTransition(errors.New("other error")) {
		t.Fatal("non-transition errors should not be treated as already queued")
	}
}

func TestAppendProjectBootstrapContinuationMessageWithoutAuthorUsesSystem(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	message, err := fixture.engine.appendProjectBootstrapContinuationMessageWithContent(
		context.Background(),
		fixture.session.ID,
		uuid.Nil,
		fixture.userMessageID.String(),
		1,
		"Continue bootstrap.",
	)
	if err != nil {
		t.Fatalf("appendProjectBootstrapContinuationMessageWithContent: %v", err)
	}

	stored, err := fixture.messages.GetByID(context.Background(), message.ID)
	if err != nil {
		t.Fatalf("GetByID continuation message: %v", err)
	}
	if stored.AuthorType != nil {
		t.Fatalf("author_type = %v, want nil for system-authored continuation message", *stored.AuthorType)
	}
	if stored.AuthorID != nil {
		t.Fatalf("author_id = %v, want nil", stored.AuthorID)
	}
	if got := strings.TrimSpace(stored.Role); got != "user" {
		t.Fatalf("role = %q, want user", got)
	}
}

func TestAppendProjectBootstrapRecoveryContinuationMessageIncludesNamedTaskFromProgress(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	taskID := uuid.New()
	pmID := uuid.New()
	workerID := uuid.New()
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active","current_phase":"first_wave_executions_created"}}`)
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: pmID, Role: "project_manager"},
			{ProjectID: projectID, AgentID: workerID, Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Lori"},
			workerID: {ID: workerID, DisplayName: "Dev"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				ProjectID:       projectID,
				TaskNumber:      23,
				Title:           "Draft second blog post",
				WorkStatus:      "draft",
				AssignedAgentID: nil,
			},
		},
	}

	msg, err := fixture.engine.appendProjectBootstrapRecoveryContinuationMessage(
		context.Background(),
		fixture.session.ID,
		fixture.chat.participants[0].ParticipantID,
		fixture.userMessageID.String(),
		3,
		projectBootstrapProgress{
			ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
			ValidationFailureReason: "kickoff validation failed: first-wave task 23 (Draft second blog post) has no assigned agent, so bootstrap cannot queue runnable execution.",
		},
	)
	if err != nil {
		t.Fatalf("appendProjectBootstrapRecoveryContinuationMessage: %v", err)
	}
	if !strings.Contains(msg.Content, "Named blocked task: task 23 id="+taskID.String()) {
		t.Fatalf("message = %q, want named blocked task line with exact id", msg.Content)
	}
	if !strings.Contains(msg.Content, "workers=Dev") {
		t.Fatalf("message = %q, want assignment roster", msg.Content)
	}
}

func TestContinueTurnStopsWhenProjectPaused(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	projectID := uuid.New()
	agentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
			},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"pause":{"is_paused":true,"reason":"operator pause","metadata":{}}}`),
			},
		},
	}

	turn, err := fixture.chat.CreateTurn(context.Background(), fixture.session.ID, agentID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	rt := &turnRuntime{
		session:          fixture.session,
		agent:            repo.Agent{ID: agentID, OrganizationID: fixture.session.OrganizationID},
		turn:             turn,
		initialMessageID: fixture.userMessageID,
	}
	if err := fixture.engine.continueTurn(context.Background(), rt); !errors.Is(err, errTurnPaused) {
		t.Fatalf("continueTurn err = %v, want errTurnPaused", err)
	}

	if len(fixture.chat.turnOrder) != 1 {
		t.Fatalf("turn count = %d, want 1", len(fixture.chat.turnOrder))
	}
	currentTurn, err := fixture.chat.GetTurn(context.Background(), turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if currentTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", currentTurn.Status)
	}
	if !fixture.messages.containsContentSubstring("Project paused - continuation deferred until resume") {
		t.Fatal("missing paused continuation message")
	}
}

func TestHandleUserMessageEventSkipsClosedSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.Status = "closed"

	event := eventbus.DomainEvent{
		EventType: "chat.message.user_sent",
	}
	payload, err := json.Marshal(map[string]any{
		"session_id": fixture.session.ID.String(),
		"message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event.Payload = payload
	if err := fixture.engine.HandleUserMessageEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}
	if got := len(fixture.enqueuer.agentTurnJobs()); got != 0 {
		t.Fatalf("agent turn jobs = %d, want 0 for closed session", got)
	}
}

func TestHandleUserMessageEventSkipsLegacyFallbackForExecutionOwnedTaskEventWithoutSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	sessionID := fixture.session.ID
	executionID := uuid.New()
	fixture.chat.session.ID = uuid.New()

	event := eventbus.DomainEvent{
		EventType: "chat.message.user_sent",
	}
	payload, err := json.Marshal(map[string]any{
		"session_id":             sessionID.String(),
		"message_id":             fixture.userMessageID.String(),
		"flow_node_execution_id": executionID.String(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event.Payload = payload
	if err := fixture.engine.HandleUserMessageEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}
	if got := len(fixture.enqueuer.agentTurnJobs()); got != 0 {
		t.Fatalf("agent turn jobs = %d, want 0 when execution-owned task session is missing", got)
	}
}

func TestHandleUserMessageProjectBootstrapWatchdogCancelsBlockedChunkPersistence(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	projectID := uuid.New()
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active"}}`)
	fixture.engine.projectBootstrapTurnTimeout = 40 * time.Millisecond
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: fixture.session.OrganizationID,
				Status:         "active",
			},
		},
	}

	updateContentCalled := make(chan struct{}, 1)
	fixture.messages.updateContentFn = func(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error) {
		select {
		case updateContentCalled <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return repo.ChatMessage{}, ctx.Err()
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("blocked "); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "unexpected success"}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessageID)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("HandleUserMessage err = %v, want nil with watchdog failure handled in-turn", err)
	}
	if fixture.model.streamCalls == 0 {
		t.Fatal("expected model stream call")
	}
	select {
	case <-updateContentCalled:
	default:
		t.Fatal("expected streamed chunk persistence to be attempted")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("HandleUserMessage elapsed = %s, want watchdog cancellation before outer context timeout", elapsed)
	}
	latestTurn := fixture.chat.turns[fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1]]
	if latestTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed after bootstrap watchdog timeout", latestTurn.Status)
	}
	if latestTurn.StopReason == nil || *latestTurn.StopReason != stopReasonMaxDuration {
		t.Fatalf("turn stop_reason = %v, want %q", latestTurn.StopReason, stopReasonMaxDuration)
	}
	if !fixture.messages.containsContentSubstring("watchdog timed out") {
		t.Fatal("missing watchdog timeout message")
	}
}

func TestHandleUserMessageProjectBootstrapWatchdogReturnsWhenModelIgnoresCancellation(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	projectID := uuid.New()
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active"}}`)
	fixture.engine.projectBootstrapTurnTimeout = 40 * time.Millisecond
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: fixture.session.OrganizationID,
				Status:         "active",
			},
		},
	}

	release := make(chan struct{})
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		<-release
		return ModelResponse{}, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessageID)
	elapsed := time.Since(started)
	close(release)
	if err != nil {
		t.Fatalf("HandleUserMessage err = %v, want nil with watchdog failure handled in-turn", err)
	}
	if fixture.model.streamCalls == 0 {
		t.Fatal("expected model stream call")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("HandleUserMessage elapsed = %s, want watchdog cancellation before outer context timeout", elapsed)
	}
	latestTurn := fixture.chat.turns[fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1]]
	if latestTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed after bootstrap watchdog timeout", latestTurn.Status)
	}
	if latestTurn.StopReason == nil || *latestTurn.StopReason != stopReasonMaxDuration {
		t.Fatalf("turn stop_reason = %v, want %q", latestTurn.StopReason, stopReasonMaxDuration)
	}
	if !fixture.messages.containsContentSubstring("watchdog timed out") {
		t.Fatal("missing watchdog timeout message")
	}
}

func TestHandleUserMessageTaskScopeRoutesToAssignedAgent(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			assignedID: {ID: assignedID, OrganizationID: fixture.session.OrganizationID},
			frankID:    {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != assignedID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, assignedID)
	}
}

func TestHandleUserMessageTaskScopeSyncRoutesToProjectPMAndAddsParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	taskID := uuid.New()
	projectID := uuid.New()
	pmID := uuid.New()
	assignedID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		items: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {ProjectID: projectID, AgentID: pmID, IsActive: true},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:       {ID: pmID, OrganizationID: fixture.session.OrganizationID},
			assignedID: {ID: assignedID, OrganizationID: fixture.session.OrganizationID},
			frankID:    {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != pmID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, pmID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasPM := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == pmID {
			hasPM = true
			break
		}
	}
	if !hasPM {
		t.Fatalf("expected project PM %s to be added as a session participant", pmID)
	}
}

func TestHandleUserMessageTaskScopeRequiresAssignedAgent(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	pmID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
			},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		items: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {ProjectID: projectID, AgentID: pmID, IsActive: true},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:    {ID: pmID, OrganizationID: fixture.session.OrganizationID},
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil {
		t.Fatal("HandleUserMessage error = nil, want missing assigned agent invariant")
	}
	if !strings.Contains(err.Error(), "internal invariant: task-scoped session is missing assigned agent") {
		t.Fatalf("HandleUserMessage error = %v, want missing assigned agent invariant", err)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}
}

func TestResolveSessionAgentForSessionRecoversMissingTaskAssigneeFromSessionParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	workerID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   workerID,
	}}
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{{
			ProjectID: projectID,
			AgentID:   workerID,
			IsActive:  true,
			Role:      "worker",
		}},
	}

	agentID, err := fixture.engine.resolveSessionAgentForSession(context.Background(), fixture.session)
	if err != nil {
		t.Fatalf("resolveSessionAgentForSession: %v", err)
	}
	if agentID != workerID {
		t.Fatalf("agent_id = %s, want %s", agentID, workerID)
	}

	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updatedTask.AssignedAgentID == nil || *updatedTask.AssignedAgentID != workerID {
		t.Fatalf("assigned_agent_id = %v, want %s", updatedTask.AssignedAgentID, workerID)
	}
}

func TestResolveSessionAgentForSessionRoutesReviewTaskToDistinctReviewer(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	workerID := uuid.New()
	reviewerID := uuid.New()
	pmID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				WorkStatus:      "review",
				AssignedAgentID: &workerID,
			},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: workerID, IsActive: true, Role: "worker"},
			{ProjectID: projectID, AgentID: reviewerID, IsActive: true, Role: "reviewer"},
			{ProjectID: projectID, AgentID: pmID, IsActive: true, Role: "project_manager"},
		},
	}

	agentID, err := fixture.engine.resolveSessionAgentForSession(context.Background(), fixture.session)
	if err != nil {
		t.Fatalf("resolveSessionAgentForSession: %v", err)
	}
	if agentID != reviewerID {
		t.Fatalf("agent_id = %s, want reviewer %s", agentID, reviewerID)
	}
}

func TestShouldAppendSyntheticUserPromptSkipsDuplicatePendingSource(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Continue the active task now from the continuation summary above.",
		Metadata:  syntheticContinuationActionMessageMetadata(nil, taskContinuationResumeMessageSource),
	})

	shouldAppend, err := fixture.engine.shouldAppendSyntheticUserPrompt(context.Background(), fixture.session.ID, taskContinuationResumeMessageSource)
	if err != nil {
		t.Fatalf("shouldAppendSyntheticUserPrompt: %v", err)
	}
	if shouldAppend {
		t.Fatal("shouldAppendSyntheticUserPrompt = true, want false for duplicate pending synthetic prompt")
	}
}

func TestShouldAppendSyntheticUserPromptIgnoresDuplicateOnCompletedTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	completedTurn, err := fixture.chat.CreateTurn(context.Background(), fixture.session.ID, fixture.chat.participants[0].ParticipantID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), completedTurn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := fixture.chat.CompleteTurn(context.Background(), completedTurn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	turnID := completedTurn.ID
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "user",
		Status:    "pending",
		Content:   "Continue the active organization request now from the continuation summary above.",
		Metadata:  syntheticContinuationActionMessageMetadata(nil, "organization_continuation_resume"),
	})

	shouldAppend, err := fixture.engine.shouldAppendSyntheticUserPrompt(context.Background(), fixture.session.ID, "organization_continuation_resume")
	if err != nil {
		t.Fatalf("shouldAppendSyntheticUserPrompt: %v", err)
	}
	if !shouldAppend {
		t.Fatal("shouldAppendSyntheticUserPrompt = false, want true when duplicate synthetic prompt belongs to completed turn")
	}
}

func TestNormalizeRoutedAgentForSessionDropsStaleAsyncTaskOverride(t *testing.T) {
	agentID := uuid.New()
	session := &chat.ChatSession{ScopeType: "project_task", Mode: "async"}
	if got := normalizeRoutedAgentForSession(session, &agentID); got != nil {
		t.Fatalf("normalizeRoutedAgentForSession returned %v, want nil for async task session", *got)
	}
}

func TestHandleUserMessageProjectScopeRoutesToProjectPMAndAddsParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	pmID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{
		items: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {ProjectID: projectID, AgentID: pmID, IsActive: true},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:    {ID: pmID, OrganizationID: fixture.session.OrganizationID},
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != pmID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, pmID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasPM := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == pmID {
			hasPM = true
			break
		}
	}
	if !hasPM {
		t.Fatalf("expected project PM %s to be added as a session participant", pmID)
	}
}

func TestProjectCreateStateMachinePreventsConflictReentryAfterSuccess(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	projectSlug := "sam-blog-v2"

	dispatched := make([]string, 0, 3)
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = append(dispatched, call.ID)
		switch call.ID {
		case "create-conflict":
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "slug_conflict_archive_or_reuse_required",
			}, nil
		case "create-success":
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Output: map[string]any{
					"project": map[string]any{
						"id":   projectID,
						"slug": projectSlug,
						"name": "Sam Blog V2",
					},
				},
			}, nil
		case "create-reopen":
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "unexpected_reopen_dispatch",
			}, nil
		default:
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
		}
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-conflict", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog", "slug": "sam-blog"}}}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-success", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog V2", "slug": projectSlug}}}}, nil
		case 3:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-reopen", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog V2", "slug": projectSlug}}}}, nil
		default:
			return ModelResponse{Content: "Proceeding with the created project."}, nil
		}
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if strings.Join(dispatched, ",") != "create-conflict,create-success" {
		t.Fatalf("dispatched project.create calls = %v, want [create-conflict create-success]", dispatched)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	wantLock := fmt.Sprintf("Project identity locked: slug=%s project_id=%s", projectSlug, projectID)
	hasLock := false
	hasBlockedReopen := false
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "system") && strings.Contains(message.Content, wantLock) {
			hasLock = true
		}
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") &&
			message.ToolCallID != nil &&
			*message.ToolCallID == "create-reopen" &&
			strings.Contains(message.Content, "project already created in this flow") {
			hasBlockedReopen = true
		}
	}
	if !hasLock {
		t.Fatalf("missing project identity lock system message containing %q", wantLock)
	}
	if !hasBlockedReopen {
		t.Fatal("missing blocked project.create tool_result for create-reopen call")
	}
}

func TestHandleUserMessageProjectScopeKickoffStartsWithFrank(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != frankID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, frankID)
	}
}

func TestHandleUserMessageProjectScopeBootstrapScaffoldStartsWithLori(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_gate":true}`)},
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`)},
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"staff-project"}`)},
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"decompose-workstreams"}`)},
		},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != loriID {
		t.Fatalf("turn responding_id = %s, want Lori %s", turn.RespondingID, loriID)
	}
}

func TestShouldBlockFreshKickoffPreCreateTool(t *testing.T) {
	rt := &turnRuntime{
		freshKickoff: true,
		session: &repo.ChatSession{
			ScopeType: "organization",
		},
		agent: repo.Agent{
			ID:          uuid.New(),
			DisplayName: "Frank",
		},
	}
	if !shouldBlockFreshKickoffPreCreateTool(rt, "project.list") {
		t.Fatal("fresh kickoff should block pre-create project browsing")
	}
	if !shouldBlockFreshKickoffPreCreateTool(rt, "memory.search") {
		t.Fatal("fresh kickoff should block pre-create memory browsing")
	}
	if shouldBlockFreshKickoffPreCreateTool(rt, "project.create") {
		t.Fatal("fresh kickoff should allow project.create")
	}
	rt.projectIdentity = &projectIdentity{id: uuid.New(), slug: "sam-blog-fresh"}
	if shouldBlockFreshKickoffPreCreateTool(rt, "project.list") {
		t.Fatal("fresh kickoff pre-create block should clear after project identity is established")
	}
}

func TestShouldBlockFreshKickoffMemoryTool(t *testing.T) {
	rt := &turnRuntime{
		freshKickoff: true,
		session: &repo.ChatSession{
			ScopeType: "project",
		},
	}
	if !shouldBlockFreshKickoffMemoryTool(rt, "memory.search") {
		t.Fatal("fresh kickoff should block project-scope memory search")
	}
	if !shouldBlockFreshKickoffMemoryTool(rt, "memory.list") {
		t.Fatal("fresh kickoff should block project-scope memory list")
	}
	if shouldBlockFreshKickoffMemoryTool(rt, "project.get") {
		t.Fatal("fresh kickoff should not block non-memory tools here")
	}
	rt.freshKickoff = false
	if shouldBlockFreshKickoffMemoryTool(rt, "memory.search") {
		t.Fatal("non-fresh kickoff should not block memory tools")
	}
}

func TestHandleUserMessageRecoveryMessageWithFreshKickoffMetadataDisablesMemory(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	metadata, err := json.Marshal(map[string]any{"fresh_kickoff": true, "source": projectBootstrapSource})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	fixture.messages.items[fixture.userMessageID] = repo.ChatMessage{
		ID:             fixture.userMessageID,
		SessionID:      fixture.session.ID,
		SequenceNumber: 1,
		Role:           "user",
		Status:         "pending",
		Content:        "Continue the bootstrap recovery now.",
		Metadata:       metadata,
	}
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if !input.DisableMemory {
			t.Fatalf("assemble call %d DisableMemory = false, want true", call)
		}
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
}

func TestShouldBlockFreshKickoffAgentBrowseTool(t *testing.T) {
	rt := &turnRuntime{
		freshKickoff: true,
		session: &repo.ChatSession{
			ScopeType: "project",
		},
	}
	if !shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.list") {
		t.Fatal("fresh kickoff should block project-scope agent.list")
	}
	if !shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.get") {
		t.Fatal("fresh kickoff should block project-scope agent.get")
	}
	if shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.create_draft") {
		t.Fatal("fresh kickoff should not block creating fresh staff")
	}
	rt.session.ScopeType = "organization"
	if shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.list") {
		t.Fatal("organization-scope fresh kickoff should already be covered by pre-create tool guard, not agent browse guard")
	}
	rt.freshKickoff = false
	rt.session.ScopeType = "project"
	if shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.list") {
		t.Fatal("non-fresh kickoff should not block agent browsing")
	}
}

func TestAppendProjectBootstrapContinuationMessagePreservesFreshKickoffMetadata(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	initialID := uuid.New()
	initial := fixture.messages.create(repo.ChatMessage{
		ID:        initialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "Start Sam.blog from scratch as a fresh kickoff. Do not reuse archived work.",
	})

	msg, err := fixture.engine.appendProjectBootstrapContinuationMessage(context.Background(), fixture.session.ID, fixture.chat.participants[0].ParticipantID, initial.ID.String(), 2)
	if err != nil {
		t.Fatalf("appendProjectBootstrapContinuationMessage: %v", err)
	}
	metadata := messageMetadataMap(msg.Metadata)
	if fresh, _ := metadata["fresh_kickoff"].(bool); !fresh {
		t.Fatalf("fresh_kickoff metadata = %v, want true", metadata["fresh_kickoff"])
	}
}

func TestAppendProjectBootstrapContinuationMessageUsesPhaseAwareResumePrompt(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveSelected,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFlowTemplatesPersisted,
		AssignmentCount:          3,
		PlannedTaskCount:         13,
		PlannedFlowTemplateCount: 2,
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		ValidationStatus:         projectBootstrapValidationPassed,
		StartedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	initialID := uuid.New()
	initial := fixture.messages.create(repo.ChatMessage{
		ID:        initialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "Start the validation project.",
	})

	msg, err := fixture.engine.appendProjectBootstrapContinuationMessage(context.Background(), fixture.session.ID, fixture.chat.participants[0].ParticipantID, initial.ID.String(), 0)
	if err != nil {
		t.Fatalf("appendProjectBootstrapContinuationMessage: %v", err)
	}
	if strings.Contains(msg.Content, "Persist project assignments, scoped tasks, and flow templates if the handoff already contains enough information") {
		t.Fatalf("content = %q, want phase-aware resume prompt instead of generic bootstrap continuation", msg.Content)
	}
	if !strings.Contains(msg.Content, "Your first tool call in this resume turn should be bootstrap.setup.persist") {
		t.Fatalf("content = %q, want resume prompt to start with bootstrap.setup.persist guidance", msg.Content)
	}
}

func TestBuildSyntheticProjectKickoffHandoffPrefersFreshProjectContext(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	content := "Start a brand-new Sam.blog project from scratch. Do not reuse archived chains."
	if _, err := fixture.messages.UpdateContent(context.Background(), fixture.userMessageID, content); err != nil {
		t.Fatalf("UpdateContent user message: %v", err)
	}

	rt := &turnRuntime{
		initialMessageID: fixture.userMessageID,
		projectIdentity: &projectIdentity{
			slug: "sam-blog-fresh-test",
			id:   uuid.New(),
		},
	}

	handoff := fixture.engine.buildSyntheticProjectKickoffHandoff(context.Background(), rt)
	if !strings.Contains(handoff, "Treat this as a fresh project bootstrap.") {
		t.Fatalf("handoff = %q, want fresh bootstrap guidance", handoff)
	}
	if !strings.Contains(handoff, "Do not assume architecture, CMS choice, or workflow from archived/restart chains or org memory") {
		t.Fatalf("handoff = %q, want anti-reuse guidance", handoff)
	}
	if !strings.Contains(handoff, "Prefer the current project description and live tool results over prior-project memory.") {
		t.Fatalf("handoff = %q, want current-project preference", handoff)
	}
	if !strings.Contains(handoff, "Do not call memory.query, memory.list, or other memory tools during this bootstrap handoff") {
		t.Fatalf("handoff = %q, want explicit no-memory guidance", handoff)
	}
	if !strings.Contains(handoff, "Frank, Lori, and Ellie are starter-trio governance agents") {
		t.Fatalf("handoff = %q, want starter-trio staffing guidance", handoff)
	}
	if !strings.Contains(handoff, "Keep staffing discovery bounded.") {
		t.Fatalf("handoff = %q, want bounded staffing discovery guidance", handoff)
	}
	if !strings.Contains(handoff, "Use at most one staffing.browse_profiles pass per needed category") {
		t.Fatalf("handoff = %q, want explicit staffing discovery budget", handoff)
	}
	if !strings.Contains(handoff, "The project already has the canonical bootstrap task tree seeded for this workflow") {
		t.Fatalf("handoff = %q, want seeded bootstrap task-tree guidance", handoff)
	}
	if !strings.Contains(handoff, "Do not recreate or duplicate those seeded bootstrap tasks.") {
		t.Fatalf("handoff = %q, want no-duplicate-bootstrap-task guidance", handoff)
	}
	if !strings.Contains(handoff, "Do not spend a turn writing a staffing plan, rationale memo, or markdown table before you materialize staff.") {
		t.Fatalf("handoff = %q, want no-staffing-memo guidance", handoff)
	}
	if !strings.Contains(handoff, "Once enough candidates are known, do not emit another assistant planning summary about staffing.") {
		t.Fatalf("handoff = %q, want direct-tool-action staffing guidance", handoff)
	}
	if !strings.Contains(handoff, "Fresh bootstrap staffing must materially advance in this turn.") {
		t.Fatalf("handoff = %q, want same-turn staffing progress guidance", handoff)
	}
	if !strings.Contains(handoff, "Do not interleave extra assistant summaries between staffing lookups.") {
		t.Fatalf("handoff = %q, want no-interleaved-staffing-summary guidance", handoff)
	}
	if !strings.Contains(handoff, "If you need project docs, files, planning artifacts, or current task state during bootstrap, inspect them directly with tools.") {
		t.Fatalf("handoff = %q, want direct-doc-inspection guidance", handoff)
	}
	if !strings.Contains(handoff, "Before you pause to read scaffold artifacts or persist setup, every persisted broad workstream parent must either have bounded executable child tasks under it") {
		t.Fatalf("handoff = %q, want all-parents-decomposed guidance", handoff)
	}
	if !strings.Contains(handoff, content) {
		t.Fatalf("handoff = %q, want originating request content", handoff)
	}
}

func TestShouldStopAfterBlockedProjectKickoffSessionCreate(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ID:        uuid.New(),
			ScopeType: "organization",
		},
		projectIdentity: &projectIdentity{
			id:   uuid.New(),
			slug: "sam-blog",
		},
	}

	if !shouldStopAfterBlockedProjectKickoffSessionCreate(rt, []ToolResult{{
		Name:  "session.create",
		Error: "project kickoff is now handoff-only: project already created as slug=sam-blog project_id=11111111-1111-1111-1111-111111111111. Provide Lori the handoff summary and end the turn without additional tool use",
	}}) {
		t.Fatal("expected blocked handoff-only session.create to stop the kickoff turn")
	}

	if shouldStopAfterBlockedProjectKickoffSessionCreate(rt, []ToolResult{{
		Name:  "browser.open",
		Error: "project kickoff is now handoff-only: provide Lori the handoff summary and end the turn",
	}}) {
		t.Fatal("non-session blocked follow-on tool should not trigger the kickoff stop path")
	}

	if shouldStopAfterBlockedProjectKickoffSessionCreate(&turnRuntime{
		session: &chat.ChatSession{ID: uuid.New(), ScopeType: "project"},
		projectIdentity: &projectIdentity{
			id:   uuid.New(),
			slug: "sam-blog",
		},
	}, []ToolResult{{
		Name:  "session.create",
		Error: "project kickoff is now handoff-only",
	}}) {
		t.Fatal("project-scoped sessions should not use the org kickoff stop path")
	}
}

func TestShouldBlockProjectBootstrapSetupTaskChildCreate(t *testing.T) {
	projectID := uuid.New()
	setupTaskID := uuid.New()
	normalParentTaskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			setupTaskID: {
				ID:         setupTaskID,
				ProjectID:  projectID,
				TaskNumber: 4,
				Title:      "Decompose workstreams into bounded tasks",
				Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"decompose-workstreams"}`),
			},
			normalParentTaskID: {
				ID:         normalParentTaskID,
				ProjectID:  projectID,
				TaskNumber: 9,
				Title:      "Validation orchestration",
				Metadata:   json.RawMessage(`{}`),
			},
		},
	}
	engine := &TurnEngine{tasks: taskRepo}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ID:        uuid.New(),
			ScopeType: "project",
			ScopeID:   projectID,
			Metadata:  json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
		},
	}

	blocked, reason := engine.shouldBlockProjectBootstrapSetupTaskChildCreate(context.Background(), rt, "task.create", map[string]any{
		"project_id": uuid.NewString(),
		"title":      "Review Test Results & Coverage",
	})
	if blocked {
		t.Fatalf("top-level task.create should remain allowed during active bootstrap, got %q", reason)
	}

	blocked, reason = engine.shouldBlockProjectBootstrapSetupTaskChildCreate(context.Background(), rt, "task.create", map[string]any{
		"project_id":     uuid.NewString(),
		"title":          "Review Test Results & Coverage",
		"parent_task_id": normalParentTaskID.String(),
	})
	if blocked {
		t.Fatalf("non-bootstrap parents should remain allowed, got %q", reason)
	}

	blocked, reason = engine.shouldBlockProjectBootstrapSetupTaskChildCreate(context.Background(), rt, "task.create", map[string]any{
		"project_id":     uuid.NewString(),
		"title":          "Review Test Results & Coverage",
		"parent_task_id": setupTaskID.String(),
	})
	if !blocked {
		t.Fatal("expected bootstrap setup parent to block child task.create")
	}
	if !strings.Contains(reason, "orchestration-only") || !strings.Contains(reason, "bootstrap.setup.persist") {
		t.Fatalf("guard error = %q, want setup-task child guidance", reason)
	}
}

func TestBuildProjectBootstrapRestartPromptRequiresFinishingAllBroadParents(t *testing.T) {
	prompt := buildProjectBootstrapRestartPrompt(
		projectBootstrapRestartBundle{OperatorBrief: "Frank handoff: create the initial staffed bootstrap for this project."},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-old"},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-new"},
		false,
	)
	if !strings.Contains(prompt, "Do not answer with a standalone acknowledgement or status note.") {
		t.Fatalf("prompt = %q, want no-acknowledgement guidance", prompt)
	}
	if !strings.Contains(prompt, "This first restart turn must contain the concrete staffing/task mutation tool calls needed to recreate staffed executable work") {
		t.Fatalf("prompt = %q, want direct restart action guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with broad rereads like project.get, task.list, flow.list_templates, file.list, git.log, or memory tools.") {
		t.Fatalf("prompt = %q, want no-broad-rereads guidance", prompt)
	}
	if !strings.Contains(prompt, "Before you pause to read scaffold artifacts or persist setup, every persisted broad workstream parent must either have bounded executable child tasks under it") {
		t.Fatalf("prompt = %q, want all-parents-decomposed guidance", prompt)
	}
}

func TestBuildProjectBootstrapRestartPromptForSeededScaffold(t *testing.T) {
	prompt := buildProjectBootstrapRestartPrompt(
		projectBootstrapRestartBundle{OperatorBrief: "Frank handoff: repair the archived bootstrap scaffold."},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-old"},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-new"},
		true,
	)
	if !strings.Contains(prompt, "already been seeded with the archived project's persisted assignments and draft task scaffold") {
		t.Fatalf("prompt = %q, want seeded scaffold guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not recreate that scaffold from scratch") {
		t.Fatalf("prompt = %q, want no-recreate guidance", prompt)
	}
}

func TestProjectBootstrapRestartSeededFailureProgressFallsBackToRecoverableHistory(t *testing.T) {
	progress, ok := projectBootstrapRestartSeededFailureProgress(
		projectBootstrapProgress{},
		projectAutomaticFailureRecord{
			FailureClass:  projectBootstrapFailureStalled,
			FailureReason: `kickoff validation failed: automatic bootstrap restart replied with narrative only and never persisted staffed executable work; reply="I'll inspect the seeded scaffold first."`,
		},
		projectBootstrapRestartBundle{
			FailureHistory: []projectfailure.FailureHistoryEntry{
				{
					FailureClass:  projectBootstrapFailureFirstWaveExecution,
					FailureReason: "kickoff validation failed: first-wave task 76 (Use a topic from the editorial calendar) has no assigned agent, so bootstrap cannot queue runnable execution",
				},
			},
		},
	)
	if !ok {
		t.Fatal("projectBootstrapRestartSeededFailureProgress ok = false, want recoverable history fallback")
	}
	if got := strings.TrimSpace(progress.ValidationFailureReason); !strings.Contains(got, "Use a topic from the editorial calendar") {
		t.Fatalf("validation_failure_reason = %q, want recoverable history reason", got)
	}
	if got := strings.TrimSpace(progress.ValidationFailureClass); got != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("validation_failure_class = %q, want %q", got, projectBootstrapFailureFirstWaveExecution)
	}
}

func TestBootstrapRestartSlugCandidateCollapsesRestartChains(t *testing.T) {
	got := bootstrapRestartSlugCandidate("sam-blog-110-restart-restart-restart-13", 1)
	if got != "sam-blog-110-restart" {
		t.Fatalf("bootstrapRestartSlugCandidate collapse = %q, want %q", got, "sam-blog-110-restart")
	}
}

func TestBootstrapRestartSlugCandidateStaysWithinProjectSlugLimit(t *testing.T) {
	base := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklm-restart-12"
	got := bootstrapRestartSlugCandidate(base, 123)
	if len(got) > maxProjectSlugLength {
		t.Fatalf("bootstrapRestartSlugCandidate len = %d, want <= %d (%q)", len(got), maxProjectSlugLength, got)
	}
	if !strings.HasSuffix(got, "-restart-123") {
		t.Fatalf("bootstrapRestartSlugCandidate suffix = %q, want -restart-123", got)
	}
}

func TestShouldBlockProjectBootstrapRestaffingToolAfterTaskTreePersisted(t *testing.T) {
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          7,
		PlannedTaskCount:         12,
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRestaffingTool(rt, "staffing.browse_profiles") {
		t.Fatal("expected staffing.browse_profiles to be blocked after task tree persisted")
	}
	if !shouldBlockProjectBootstrapRestaffingTool(rt, "agent.create_staff") {
		t.Fatal("expected agent.create_staff to be blocked after task tree persisted")
	}
	if shouldBlockProjectBootstrapRestaffingTool(rt, "agent.assign_project") {
		t.Fatal("agent.assign_project should not be blocked by restaffing guard")
	}
}

func TestShouldBlockProjectBootstrapExcessStaffingDiscoveryTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
		},
		toolCallsUsed: projectBootstrapStaffingDiscoveryBudget,
	}
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{Status: projectBootstrapStatusActive})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt.session.Metadata = metadata
	if !shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.browse_profiles") {
		t.Fatal("expected staffing browse to be blocked after discovery budget is exhausted")
	}
	if !shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.get_profile") {
		t.Fatal("expected staffing get_profile to be blocked after discovery budget is exhausted")
	}
	rt.toolCallsUsed = projectBootstrapStaffingDiscoveryBudget - 1
	if shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.browse_profiles") {
		t.Fatal("unexpected staffing browse block before discovery budget is exhausted")
	}
	metadata, err = projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:          projectBootstrapStatusActive,
		AssignmentCount: 1,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON assignments: %v", err)
	}
	rt.session.Metadata = metadata
	rt.toolCallsUsed = projectBootstrapStaffingDiscoveryBudget
	if shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.browse_profiles") {
		t.Fatal("unexpected staffing browse block once assignments already exist")
	}
}

func TestBuildProjectBootstrapExcessStaffingDiscoveryGuardError(t *testing.T) {
	msg := buildProjectBootstrapExcessStaffingDiscoveryGuardError()
	if !strings.Contains(msg, "stop browsing profiles and create/assign the concrete PM, workers, and reviewers now") {
		t.Fatalf("guard error = %q, want direct staffing action guidance", msg)
	}
}

func TestShouldBlockProjectBootstrapRestaffingToolAllowsMissingPMRecovery(t *testing.T) {
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                 projectBootstrapStatusActive,
		AssignmentCount:        2,
		CurrentPhase:           projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:       projectBootstrapValidationFailed,
		ValidationFailureClass: projectBootstrapFailureMissingPM,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: "Continue bootstrap. Named blocked task: task 12 id=1234 title=\"Content Creation\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if shouldBlockProjectBootstrapRestaffingTool(rt, "agent.create_staff") {
		t.Fatal("missing-PM recovery should allow agent.create_staff")
	}
	if shouldBlockProjectBootstrapRestaffingTool(rt, "staffing.browse_profiles") {
		t.Fatal("missing-PM recovery should allow staffing.browse_profiles")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksBroadRereads(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            1,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason:  "kickoff validation failed: first-wave tasks violate the bounded task-size policy",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "project.list", nil) {
		t.Fatal("expected project.list to be blocked during late bootstrap validation recovery")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "project.get", nil) {
		t.Fatal("expected project.get to be blocked during late bootstrap validation recovery")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected first flow.list_templates pass to remain available before the turn burns context budget")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected first task.list pass to remain available for targeted recovery lookup")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("task.get should remain available for targeted recovery inspection")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksLateCompactResumeRereads(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          4,
		PlannedTaskCount:         35,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       30,
		BootstrapTaskOutstanding: true,
		BootstrapTaskID:          uuid.New().String(),
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationPassed,
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "project.list", nil) {
		t.Fatal("expected project.list to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected flow.list_templates to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.search", nil) {
		t.Fatal("expected file.search to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/spec.md"}) {
		t.Fatal("expected planning file.read to be blocked during late compact bootstrap resume")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("task.get should remain available for a single specific late-phase blocker inspection")
	}
}

func TestBuildProjectBootstrapRecoveryRereadToolGuardErrorLateCompactResume(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          4,
		PlannedTaskCount:         35,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       30,
		BootstrapTaskOutstanding: true,
		BootstrapTaskID:          uuid.New().String(),
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationPassed,
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	msg := buildProjectBootstrapRecoveryRereadToolGuardError(rt, "task.list")
	if !strings.Contains(msg, "call bootstrap.setup.persist now") {
		t.Fatalf("guard error = %q, want bootstrap.setup.persist guidance", msg)
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksLateCompactResumeWithoutOutstandingBootstrapTask(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          3,
		PlannedTaskCount:         6,
		PlannedFlowTemplateCount: 2,
		FirstWaveTaskCount:       3,
		FirstWavePromotedCount:   0,
		FirstWaveJobCount:        0,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationPassed,
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked during late compact bootstrap resume before any first-wave promotion or jobs exist")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected flow.list_templates to be blocked during late compact bootstrap resume before any first-wave promotion or jobs exist")
	}
}

func TestBuildProjectBootstrapRecoveryRereadToolGuardErrorScaffoldOnlyRecovery(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          3,
		PlannedTaskCount:         0,
		CurrentPhase:             projectBootstrapCheckpointTaskTreePersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointStaffingPersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureRuntime,
		ValidationFailureReason:  buildProjectBootstrapScaffoldOnlyFailureReason(),
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	msg := buildProjectBootstrapRecoveryRereadToolGuardError(rt, "project.list")
	if !strings.Contains(msg, "direct task.create or subtask.create calls") {
		t.Fatalf("guard error = %q, want direct task creation guidance", msg)
	}
	if !strings.Contains(msg, "Do not reread project state first") {
		t.Fatalf("guard error = %q, want no-reread guidance", msg)
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksScaffoldOnlyRecoveryRereads(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          3,
		PlannedTaskCount:         0,
		CurrentPhase:             projectBootstrapCheckpointTaskTreePersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointStaffingPersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureRuntime,
		ValidationFailureReason:  buildProjectBootstrapScaffoldOnlyFailureReason(),
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: buildProjectBootstrapScaffoldOnlyFailureReason(),
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "project.get", nil) {
		t.Fatal("expected project.get to be blocked during scaffold-only bootstrap recovery")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked during scaffold-only bootstrap recovery")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected flow.list_templates to be blocked during scaffold-only bootstrap recovery")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.search", nil) {
		t.Fatal("expected file.search to be blocked during scaffold-only bootstrap recovery")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/oc-73.md"}) {
		t.Fatal("expected planning file.read to be blocked during scaffold-only bootstrap recovery")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.create", map[string]any{"title": "Create bounded task"}) {
		t.Fatal("task.create should remain available during scaffold-only bootstrap recovery")
	}
}

func TestShouldRequireDirectBootstrapRepairActionForBoundedSizeFailure(t *testing.T) {
	state := projectBootstrapState{
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason: "kickoff validation failed: first-wave task 105 (SEO, Performance, and Analytics Integration) violates the bounded task-size policy",
	}
	snapshot := projectBootstrapResumeSnapshot{
		FailedTaskLine: `Named blocked task: task 105 id=1234 title="SEO, Performance, and Analytics Integration" work_status=draft assigned_agent_id=06ff.`,
	}

	if !shouldRequireDirectBootstrapRepairAction(state, snapshot) {
		t.Fatal("expected bounded-size failures with a named task to require direct repair action")
	}
}

func TestBuildProjectBootstrapResumeStateMessageRequiresDirectBoundedSplit(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 105 (SEO, Performance, and Analytics Integration) violates the bounded task-size policy",
		AssignmentCount:          4,
		PlannedTaskCount:         35,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       30,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
	}
	snapshot := projectBootstrapResumeSnapshot{
		ProjectID:      "028a3f39-be56-45bc-b9f8-5978ab9cf28f",
		ProjectSlug:    "sam-blog-110-restart-9",
		FailedTaskLine: `Named blocked task: task 105 id=1234 title="SEO, Performance, and Analytics Integration" work_status=draft assigned_agent_id=06ff.`,
		RepairTaskLine: "Direct repair for the named oversized task: keep task 105 orchestration-only, do not call task.get on task id=1234 first, create 2-4 bounded executable child tasks directly beneath task id=1234, keep each child to a single concrete deliverable under 60 minutes, and assign each child to an existing active project assignee before resuming bootstrap.setup.persist.",
	}

	msg := buildProjectBootstrapResumeStateMessage(state, snapshot)
	if !strings.Contains(msg, "next acceptable bootstrap action is direct bounded child-task creation") {
		t.Fatalf("resume state message = %q, want direct bounded child-task creation guidance", msg)
	}
	if !strings.Contains(msg, "Direct repair for the named oversized task") {
		t.Fatalf("resume state message = %q, want explicit oversized-task repair line", msg)
	}
	if !strings.Contains(msg, "do not call task.get on task id=1234 first") {
		t.Fatalf("resume state message = %q, want direct no-task.get oversized-task guidance", msg)
	}
}

func TestRequireTurnInProgressReturnsCancelledSentinel(t *testing.T) {
	turnID := uuid.New()
	engine := &TurnEngine{
		chat: &fakeChatService{
			turns: map[uuid.UUID]*chat.ChatTurn{
				turnID: {
					ID:     turnID,
					Status: "cancelled",
				},
			},
		},
	}
	rt := &turnRuntime{
		turn: &chat.ChatTurn{ID: turnID},
	}

	err := engine.requireTurnInProgress(context.Background(), rt)
	if !errors.Is(err, errTurnCancelled) {
		t.Fatalf("requireTurnInProgress error = %v, want errTurnCancelled", err)
	}
}

func TestRequireTurnInProgressReturnsCancelledForOtherTerminalStatuses(t *testing.T) {
	for _, status := range []string{"failed", "completed"} {
		t.Run(status, func(t *testing.T) {
			turnID := uuid.New()
			engine := &TurnEngine{
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				chat: &fakeChatService{
					turns: map[uuid.UUID]*chat.ChatTurn{
						turnID: {
							ID:     turnID,
							Status: status,
						},
					},
				},
			}
			rt := &turnRuntime{
				session: &chat.ChatSession{ID: uuid.New()},
				turn:    &chat.ChatTurn{ID: turnID},
			}

			err := engine.requireTurnInProgress(context.Background(), rt)
			if !errors.Is(err, errTurnCancelled) {
				t.Fatalf("requireTurnInProgress error = %v, want errTurnCancelled", err)
			}
		})
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksNamedTaskListImmediately(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            1,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: "Continue bootstrap. Named blocked task: task 12 id=1234 title=\"Content Creation\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked immediately when recovery already names the exact failing task")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("expected task.get to be blocked when recovery already carries the exact task id and direct task.update instructions")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected flow.list_templates to be blocked immediately when recovery already names the exact failing task")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.search", nil) {
		t.Fatal("expected file.search to be blocked immediately when recovery already names the exact failing task")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/oc-73.md"}) {
		t.Fatal("expected planning file.read to be blocked immediately when recovery already names the exact failing task")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "src/components/PostCard.tsx"}) {
		t.Fatal("non-planning file.read should remain available for targeted implementation inspection")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksNamedTaskListFromRecoveryMessage(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            3,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent.",
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked immediately when the recovery message already names the exact failing task")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("task.get should remain available for targeted recovery inspection")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksRepeatedTaskList(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            2,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 51 has not materialized execution",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		toolCallsUsed: 1,
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected repeated task.list to be blocked after recovery already spent tool budget in this turn")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected late flow.list_templates reread to be blocked after recovery already spent tool budget in this turn")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.search", nil) {
		t.Fatal("expected file.search to be blocked after recovery already spent tool budget in this turn")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/oc-73.md"}) {
		t.Fatal("expected scaffold planning file.read to be blocked after recovery already spent tool budget in this turn")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "src/components/PostCard.tsx"}) {
		t.Fatal("non-planning file.read should remain available for targeted code inspection")
	}
}

func TestBootstrapFirstWaveSelectableTasksSkipsParentsAndDeferredFinalizationTasks(t *testing.T) {
	parentID := uuid.New()
	selectableID := uuid.New()
	reportID := uuid.New()
	deferredID := uuid.New()

	tasks := []repo.ProjectTask{
		{
			ID:         parentID,
			TaskNumber: 9,
			Title:      "OC-1: Validate routing",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(`{}`),
		},
		{
			ID:         selectableID,
			TaskNumber: 12,
			Title:      "OC-2: Execute Test Scenario 1 (Happy Path)",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(fmt.Sprintf(`{"decomposition_parent_task_id":"%s"}`, parentID)),
		},
		{
			ID:         reportID,
			TaskNumber: 10,
			Title:      "Produce final validation report with pass/fail determination, risk summary, and recommendations",
			WorkStatus: "draft",
		},
		{
			ID:         deferredID,
			TaskNumber: 11,
			Title:      "Deferred task-queued after all test scenarios complete.",
			WorkStatus: "draft",
		},
	}

	selectable := bootstrapFirstWaveSelectableTasks(tasks)
	if len(selectable) != 1 {
		t.Fatalf("selectable task count = %d, want 1", len(selectable))
	}
	if selectable[0].ID != selectableID {
		t.Fatalf("selectable task id = %s, want %s", selectable[0].ID, selectableID)
	}
}

func TestShouldStopAfterBlockedProjectBootstrapRecoveryReread(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
		},
	}

	if !shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, true) {
		t.Fatal("expected blocked late bootstrap reread to stop the current turn")
	}
	if shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, false) {
		t.Fatal("unexpected stop without a blocked reread")
	}

	rt.session.ScopeType = "organization"
	if shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, true) {
		t.Fatal("unexpected stop outside project scope")
	}
}

func TestShouldBlockTaskExecutionBroadContextTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project_task",
			Mode:      "async",
		},
	}

	for _, toolName := range []string{"memory.query", "memory.list", "project.get", "project.list", "task.list", "flow.list_templates", "agent.list"} {
		if !shouldBlockTaskExecutionBroadContextTool(rt, toolName) {
			t.Fatalf("expected %s to be blocked for task-scoped async execution", toolName)
		}
	}
	if shouldBlockTaskExecutionBroadContextTool(rt, "task.get") {
		t.Fatal("task.get should remain available for task-scoped execution")
	}
	if shouldBlockTaskExecutionBroadContextTool(rt, "flow.get_execution") {
		t.Fatal("flow.get_execution should remain available for task-scoped execution")
	}
}

func TestShouldBlockTaskExecutionBroadContextToolAllowsOrchestrationValidationContextReads(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Orchestration task: Validate that first-wave executable tasks can enter execution, advance through flows, and produce outputs. Parent task for first-wave validation subtasks."

	fixture.session.ScopeType = "project_task"
	fixture.session.Mode = "async"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  12,
				Title:       "V2: Validate First-Wave Task Execution & Flow Advancement",
				Description: &description,
			},
		},
	}

	rt := &turnRuntime{session: fixture.session}
	if fixture.engine.shouldBlockTaskExecutionBroadContextTool(context.Background(), rt, "project.get") {
		t.Fatal("project.get should be allowed for orchestration validation task")
	}
	if fixture.engine.shouldBlockTaskExecutionBroadContextTool(context.Background(), rt, "task.list") {
		t.Fatal("task.list should be allowed for orchestration validation task")
	}
	if !fixture.engine.shouldBlockTaskExecutionBroadContextTool(context.Background(), rt, "memory.query") {
		t.Fatal("memory.query should remain blocked for orchestration validation task")
	}
	if !fixture.engine.shouldBlockTaskExecutionBroadContextTool(context.Background(), rt, "agent.list") {
		t.Fatal("agent.list should remain blocked for orchestration validation task")
	}
}

func TestBuildTaskExecutionBroadContextToolGuardError(t *testing.T) {
	if msg := buildTaskExecutionBroadContextToolGuardError("memory.query"); !strings.Contains(msg, "should not browse org or project memory") {
		t.Fatalf("memory.query guard = %q, want memory guidance", msg)
	}
	if msg := buildTaskExecutionBroadContextToolGuardError("task.list"); !strings.Contains(msg, "should not re-list the broader project task tree") {
		t.Fatalf("task.list guard = %q, want task-tree guidance", msg)
	}
}

func TestShouldBlockProjectExecutionPrematureDoneTool(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	targetTaskID := uuid.New()
	doneTaskID := uuid.New()

	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	metadata, err := json.Marshal(map[string]any{
		"project_bootstrap": map[string]any{
			"status": "completed",
		},
	})
	if err != nil {
		t.Fatalf("marshal bootstrap metadata: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			targetTaskID: {
				ID:         targetTaskID,
				ProjectID:  projectID,
				TaskNumber: 12,
				Title:      "Analysis: Summarize happy-path results and validate coverage",
				WorkStatus: "draft",
			},
			doneTaskID: {
				ID:         doneTaskID,
				ProjectID:  projectID,
				TaskNumber: 16,
				Title:      "Frank Sign-Off: Record validation results and project completion",
				WorkStatus: "done",
			},
		},
	}

	blocked, reason := fixture.engine.shouldBlockProjectExecutionPrematureDoneTool(context.Background(), &turnRuntime{session: fixture.session}, "task.update", map[string]any{
		"task_id":     targetTaskID.String(),
		"work_status": "done",
	})
	if !blocked {
		t.Fatal("expected project continuation to block premature done update for untouched draft task")
	}
	if !strings.Contains(reason, "still has 1 draft tasks remaining") {
		t.Fatalf("reason = %q, want remaining draft task count", reason)
	}
	if !strings.Contains(reason, "task 12") {
		t.Fatalf("reason = %q, want targeted task label", reason)
	}
}

func TestShouldNotBlockProjectExecutionPrematureDoneToolWithoutRemainingDrafts(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	targetTaskID := uuid.New()
	nodeID := uuid.New()

	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	metadata, err := json.Marshal(map[string]any{
		"project_bootstrap": map[string]any{
			"status": "completed",
		},
	})
	if err != nil {
		t.Fatalf("marshal bootstrap metadata: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			targetTaskID: {
				ID:                targetTaskID,
				ProjectID:         projectID,
				TaskNumber:        16,
				Title:             "Frank Sign-Off: Record validation results and project completion",
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}

	blocked, _ := fixture.engine.shouldBlockProjectExecutionPrematureDoneTool(context.Background(), &turnRuntime{session: fixture.session}, "task.update", map[string]any{
		"task_id":     targetTaskID.String(),
		"work_status": "done",
	})
	if blocked {
		t.Fatal("unexpected block when no draft tasks remain and target task is already in flow")
	}
}

func TestHandleCompletedProjectExecutionContinuationTurnAutoQueuesRunnableDraft(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	completedTaskID := uuid.New()
	draftTaskID := uuid.New()
	uuidPtr := func(id uuid.UUID) *uuid.UUID { return &id }

	fixture := newUnitFixture(t, "async")
	fixture.engine.pool = testdb.New(t)
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			completedTaskID: {
				ID:              completedTaskID,
				ProjectID:       projectID,
				TaskNumber:      16,
				Title:           "Frank Sign-Off: Record validation results and project completion",
				WorkStatus:      "done",
				AssignedAgentID: uuidPtr(uuid.New()),
				FlowTemplateID:  uuidPtr(uuid.New()),
			},
			draftTaskID: {
				ID:              draftTaskID,
				ProjectID:       projectID,
				TaskNumber:      19,
				Title:           "Design boundary scenario: rate limits and pipeline capacity",
				WorkStatus:      "draft",
				AssignedAgentID: uuidPtr(uuid.New()),
				FlowTemplateID:  uuidPtr(uuid.New()),
			},
		},
	}
	taskTransitions := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = taskTransitions

	userMessageID := uuid.New()
	turnID := uuid.New()
	latestUser := &repo.ChatMessage{
		ID:             userMessageID,
		SessionID:      fixture.session.ID,
		Role:           "user",
		Status:         "pending",
		SequenceNumber: 10,
		Metadata: mustJSONRaw(map[string]any{
			"source":            projectExecutionContinuationSource,
			"auto_continue":     true,
			"completed_task_id": completedTaskID.String(),
		}),
	}
	assistant := &repo.ChatMessage{
		ID:             uuid.New(),
		SessionID:      fixture.session.ID,
		TurnID:         &turnID,
		Role:           "assistant",
		Status:         "final",
		Content:        "These remaining draft tasks are auxiliary and not blockers.",
		SequenceNumber: 11,
	}
	messages := []repo.ChatMessage{*latestUser, *assistant}
	completedTurn := &repo.ChatTurn{ID: turnID, SessionID: fixture.session.ID}

	handled, err := fixture.engine.handleCompletedProjectExecutionContinuationTurn(context.Background(), fixture.session, completedTurn, latestUser, assistant, messages)
	if err != nil {
		t.Fatalf("handleCompletedProjectExecutionContinuationTurn: %v", err)
	}
	if !handled {
		t.Fatal("expected narrative-only project continuation to auto-queue runnable draft task")
	}

	updatedDraft, err := taskRepo.GetByID(context.Background(), draftTaskID)
	if err != nil {
		t.Fatalf("GetByID draft task: %v", err)
	}
	if updatedDraft.WorkStatus != "queued" {
		t.Fatalf("draft task work_status = %q, want queued", updatedDraft.WorkStatus)
	}

	var sawSystemMessage bool
	storedMessages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range storedMessages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "system") && strings.Contains(msg.Content, "auto-queued task 19") {
			sawSystemMessage = true
			break
		}
	}
	if !sawSystemMessage {
		t.Fatal("expected system message recording auto-queued runnable draft task")
	}
}

func TestHandleCompletedProjectExecutionContinuationTurnConsumesBoundedSizeQueueFailure(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	completedTaskID := uuid.New()
	draftTaskID := uuid.New()
	uuidPtr := func(id uuid.UUID) *uuid.UUID { return &id }

	fixture := newUnitFixture(t, "async")
	fixture.engine.pool = testdb.New(t)
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			completedTaskID: {
				ID:              completedTaskID,
				ProjectID:       projectID,
				TaskNumber:      16,
				Title:           "Frank Sign-Off: Record validation results and project completion",
				WorkStatus:      "done",
				AssignedAgentID: uuidPtr(uuid.New()),
				FlowTemplateID:  uuidPtr(uuid.New()),
			},
			draftTaskID: {
				ID:              draftTaskID,
				ProjectID:       projectID,
				TaskNumber:      19,
				Title:           "Design boundary scenario: rate limits and pipeline capacity",
				WorkStatus:      "draft",
				AssignedAgentID: uuidPtr(uuid.New()),
				FlowTemplateID:  uuidPtr(uuid.New()),
			},
		},
	}
	taskTransitions := &fakeTaskTransitionService{
		repo: taskRepo,
		err:  taskdecomp.QueueSizeError{EstimatedMinutes: 50, MaxMinutes: 30, Reason: "split the work into smaller reviewable tasks before queueing"},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = taskTransitions

	userMessageID := uuid.New()
	turnID := uuid.New()
	latestUser := &repo.ChatMessage{
		ID:             userMessageID,
		SessionID:      fixture.session.ID,
		Role:           "user",
		Status:         "pending",
		SequenceNumber: 10,
		Metadata: mustJSONRaw(map[string]any{
			"source":            projectExecutionContinuationSource,
			"auto_continue":     true,
			"completed_task_id": completedTaskID.String(),
		}),
	}
	assistant := &repo.ChatMessage{
		ID:             uuid.New(),
		SessionID:      fixture.session.ID,
		TurnID:         &turnID,
		Role:           "assistant",
		Status:         "final",
		Content:        "The remaining draft tasks can be wrapped up now.",
		SequenceNumber: 11,
	}
	messages := []repo.ChatMessage{*latestUser, *assistant}
	completedTurn := &repo.ChatTurn{ID: turnID, SessionID: fixture.session.ID}

	handled, err := fixture.engine.handleCompletedProjectExecutionContinuationTurn(context.Background(), fixture.session, completedTurn, latestUser, assistant, messages)
	if err != nil {
		t.Fatalf("handleCompletedProjectExecutionContinuationTurn: %v", err)
	}
	if !handled {
		t.Fatal("expected bounded-size queue failure to be consumed")
	}

	updatedDraft, err := taskRepo.GetByID(context.Background(), draftTaskID)
	if err != nil {
		t.Fatalf("GetByID draft task: %v", err)
	}
	if updatedDraft.WorkStatus != "draft" {
		t.Fatalf("draft task work_status = %q, want draft after bounded-size queue failure", updatedDraft.WorkStatus)
	}

	var sawSystemMessage bool
	storedMessages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range storedMessages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "system") &&
			strings.Contains(msg.Content, "violates the bounded size policy") &&
			strings.Contains(msg.Content, "task 19") {
			sawSystemMessage = true
			break
		}
	}
	if !sawSystemMessage {
		t.Fatal("expected system message recording bounded-size auto-queue failure")
	}
}

func TestProjectBootstrapWatchdogTimeoutForModel(t *testing.T) {
	base := 90 * time.Second
	if got := projectBootstrapWatchdogTimeoutForModel("qwen2.5:72b", base); got != 20*time.Minute {
		t.Fatalf("qwen timeout = %s, want %s", got, 20*time.Minute)
	}
	if got := projectBootstrapWatchdogTimeoutForModel("mistral-nemo:latest", base); got != 20*time.Minute {
		t.Fatalf("mistral timeout = %s, want %s", got, 20*time.Minute)
	}
	if got := projectBootstrapWatchdogTimeoutForModel("claude-haiku-4-5-20251001", base); got != 8*time.Minute {
		t.Fatalf("claude timeout = %s, want %s", got, 8*time.Minute)
	}
	if got := projectBootstrapWatchdogTimeoutForModel("gpt-4o", base); got != 8*time.Minute {
		t.Fatalf("gpt timeout = %s, want %s", got, 8*time.Minute)
	}
	longBase := 24 * time.Minute
	if got := projectBootstrapWatchdogTimeoutForModel("qwen2.5:72b", longBase); got != longBase {
		t.Fatalf("long timeout = %s, want %s", got, longBase)
	}
}

func TestRecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.pool = testdb.New(t)

	taskID := uuid.New()
	messageID := uuid.New()
	agentID := fixture.chat.participants[0].ParticipantID

	org, err := repo.NewOrgRepo(fixture.engine.pool).Create(context.Background(), repo.Organization{
		Slug:        "recover-stale-live-invocation",
		DisplayName: "Recover Stale Live Invocation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	dbAgent, err := repo.NewAgentRepo(fixture.engine.pool).Create(context.Background(), repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recover Stale Live Invocation Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Test agent",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agentID = dbAgent.ID
	fixture.session.OrganizationID = org.ID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	dbSession, err := repo.NewChatSessionRepo(fixture.engine.pool).Create(context.Background(), repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create db session: %v", err)
	}
	dbTurn, err := repo.NewChatTurnRepo(fixture.engine.pool).Create(context.Background(), repo.ChatTurn{
		SessionID:      dbSession.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   dbAgent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create db turn: %v", err)
	}

	fixture.session.ID = dbSession.ID
	fixture.session.CurrentTurnID = &dbTurn.ID
	fixture.chat.session = fixture.session
	fixture.chat.turns[dbTurn.ID] = &chat.ChatTurn{
		ID:             dbTurn.ID,
		SessionID:      dbSession.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   dbAgent.ID,
		Status:         "in_progress",
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, dbTurn.ID)

	provider, err := repo.NewModelProviderRepo(fixture.engine.pool).Create(context.Background(), repo.ModelProvider{
		Slug:        "recover-stale-inbound-turn-live-invocation-provider",
		DisplayName: "Recover Stale Inbound Turn Live Invocation Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	if _, err := repo.NewModelInvocationRepo(fixture.engine.pool).Create(context.Background(), repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		SessionID:         &dbSession.ID,
		TurnID:            &dbTurn.ID,
	}); err != nil {
		t.Fatalf("create live model invocation: %v", err)
	}

	recovered, err := fixture.engine.recoverProjectTaskStaleInboundTurnWithoutRun(
		context.Background(),
		fixture.session,
		messageID,
		agentID,
		0,
		nil,
		fixture.chat.turns[dbTurn.ID],
	)
	if err != nil {
		t.Fatalf("recoverProjectTaskStaleInboundTurnWithoutRun: %v", err)
	}
	if recovered {
		t.Fatal("recovered = true, want false when live model invocation exists")
	}

	storedTurn, err := fixture.chat.GetTurn(context.Background(), dbTurn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if storedTurn.Status != "in_progress" {
		t.Fatalf("turn status = %q, want in_progress", storedTurn.Status)
	}
}

func TestRecoverRetriedAgentTurnLeakKeepsRecentCompletedInvocation(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.pool = testdb.New(t)

	projectID := uuid.New()
	messageID := uuid.New()

	org, err := repo.NewOrgRepo(fixture.engine.pool).Create(context.Background(), repo.Organization{
		Slug:        "recover-retried-agent-turn-leak-keep-recent-completed",
		DisplayName: "Recover Retried Agent Turn Leak Keep Recent Completed",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	dbAgent, err := repo.NewAgentRepo(fixture.engine.pool).Create(context.Background(), repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recover Retried Agent Turn Leak Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "Test agent",
		AgentType:       "pm",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	dbSession, err := repo.NewChatSessionRepo(fixture.engine.pool).Create(context.Background(), repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        projectID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create db session: %v", err)
	}
	dbMessage, err := repo.NewChatMessageRepo(fixture.engine.pool).Create(context.Background(), repo.ChatMessage{
		SessionID: dbSession.ID,
		Role:      "user",
		Content:   "Continue bootstrap.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create db message: %v", err)
	}
	messageID = dbMessage.ID
	dbTurn, err := repo.NewChatTurnRepo(fixture.engine.pool).Create(context.Background(), repo.ChatTurn{
		SessionID:        dbSession.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     dbAgent.ID,
		Status:           "in_progress",
		TriggerMessageID: &messageID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create db turn: %v", err)
	}

	fixture.session.OrganizationID = org.ID
	fixture.session.ID = dbSession.ID
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.CurrentTurnID = &dbTurn.ID
	fixture.chat.session = fixture.session
	fixture.chat.turns[dbTurn.ID] = &chat.ChatTurn{
		ID:               dbTurn.ID,
		SessionID:        dbSession.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     dbAgent.ID,
		Status:           "in_progress",
		TriggerMessageID: &messageID,
		RetryCount:       0,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, dbTurn.ID)

	provider, err := repo.NewModelProviderRepo(fixture.engine.pool).Create(context.Background(), repo.ModelProvider{
		Slug:        "recover-retried-agent-turn-leak-provider",
		DisplayName: "Recover Retried Agent Turn Leak Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	completedAt := fixture.engine.now().UTC().Add(-10 * time.Second)
	if _, err := repo.NewModelInvocationRepo(fixture.engine.pool).Create(context.Background(), repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "test-model",
		SessionID:         &dbSession.ID,
		TurnID:            &dbTurn.ID,
		CompletedAt:       &completedAt,
	}); err != nil {
		t.Fatalf("create recent completed invocation: %v", err)
	}

	var jobID uuid.UUID
	if err := fixture.engine.pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, claimed_by, claimed_at, attempts, updated_at)
		VALUES ('agent_turn', 'claimed', $1::jsonb, now(), 70, 'test-worker', now(), 2, now())
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, dbSession.ID, messageID)).Scan(&jobID); err != nil {
		t.Fatalf("insert claimed agent_turn job: %v", err)
	}

	recovered, err := fixture.engine.recoverRetriedAgentTurnLeak(
		context.Background(),
		fixture.session,
		messageID,
		dbAgent.ID,
		0,
		&jobID,
		fixture.chat.turns[dbTurn.ID],
	)
	if err != nil {
		t.Fatalf("recoverRetriedAgentTurnLeak: %v", err)
	}
	if recovered {
		t.Fatal("recovered = true, want false when a recent completed invocation exists")
	}

	storedTurn, err := fixture.chat.GetTurn(context.Background(), dbTurn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if storedTurn.Status != "in_progress" {
		t.Fatalf("turn status = %q, want in_progress", storedTurn.Status)
	}
}

func TestTryActivateProjectBootstrapKickoffIsSingleWinner(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.pool = testdb.New(t)

	ctx := context.Background()
	org, err := repo.NewOrgRepo(fixture.engine.pool).Create(ctx, repo.Organization{
		Slug:        "try-activate-project-bootstrap-kickoff-single-winner",
		DisplayName: "Try Activate Project Bootstrap Kickoff Single Winner",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(fixture.engine.pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "try-activate-project-bootstrap-kickoff-single-winner-project",
		DisplayName:    "Try Activate Project Bootstrap Kickoff Single Winner Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(fixture.engine.pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	now := fixture.engine.now().UTC()
	state := projectBootstrapState{
		Status:           projectBootstrapStatusActive,
		CurrentPhase:     "kickoff_handoff",
		InitialMessageID: uuid.NewString(),
		StartedAt:        &now,
		UpdatedAt:        &now,
	}
	acquired, err := fixture.engine.tryActivateProjectBootstrapKickoff(ctx, &chat.ChatSession{
		ID:             session.ID,
		OrganizationID: session.OrganizationID,
		ScopeType:      session.ScopeType,
		ScopeID:        session.ScopeID,
		Mode:           session.Mode,
		Status:         session.Status,
		Metadata:       session.Metadata,
	}, state)
	if err != nil {
		t.Fatalf("first tryActivateProjectBootstrapKickoff: %v", err)
	}
	if !acquired {
		t.Fatal("first activation acquired = false, want true")
	}

	reloaded, err := repo.NewChatSessionRepo(fixture.engine.pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	acquired, err = fixture.engine.tryActivateProjectBootstrapKickoff(ctx, &chat.ChatSession{
		ID:             reloaded.ID,
		OrganizationID: reloaded.OrganizationID,
		ScopeType:      reloaded.ScopeType,
		ScopeID:        reloaded.ScopeID,
		Mode:           reloaded.Mode,
		Status:         reloaded.Status,
		Metadata:       reloaded.Metadata,
	}, state)
	if err != nil {
		t.Fatalf("second tryActivateProjectBootstrapKickoff: %v", err)
	}
	if acquired {
		t.Fatal("second activation acquired = true, want false")
	}
}

func TestShouldBlockTaskExecutionOffTargetEvidenceTool(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	description := "Execute test scenario 2 (edge cases): test capacity limits, concurrent assignments, boundary conditions. Log all test cases and verify system behavior under stress."
	targetPath := "Test/test-execution-oc13-scenario2-edge-cases.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				TaskNumber:     13,
				Title:          "OC-4: Execute Test Scenario 2 (Edge Cases)",
				WorkStatus:     "in_progress",
				Description:    &description,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	targetAbs := filepath.Join(dataDir, "workspaces", projectSlug, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte("# OC-4 Execution Log\n\n## Findings\nSubstantive edge-case evidence.\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	rt := &turnRuntime{session: fixture.session}

	if blocked, _ := fixture.engine.shouldBlockTaskExecutionOffTargetEvidenceTool(context.Background(), rt, "file.read", map[string]any{"path": "planning/discovery-plan/oc-9-validation-plan.md"}); !blocked {
		t.Fatal("expected off-target planning read to be blocked")
	}
	if blocked, _ := fixture.engine.shouldBlockTaskExecutionOffTargetEvidenceTool(context.Background(), rt, "git.log", nil); !blocked {
		t.Fatal("expected git.log to be blocked when substantive target exists")
	}
	if blocked, _ := fixture.engine.shouldBlockTaskExecutionOffTargetEvidenceTool(context.Background(), rt, "file.read", map[string]any{"path": targetPath}); blocked {
		t.Fatal("expected target file read to be allowed")
	}
	if blocked, _ := fixture.engine.shouldBlockTaskExecutionOffTargetEvidenceTool(context.Background(), rt, "file.list", map[string]any{"path": "Test"}); blocked {
		t.Fatal("expected target directory listing to be allowed")
	}
	if blocked, _ := fixture.engine.shouldBlockTaskExecutionOffTargetEvidenceTool(context.Background(), rt, "file.read", map[string]any{"path": "planning/discovery-plan/oc-13-validation-plan.md"}); blocked {
		t.Fatal("expected same-task planning artifact read to be allowed")
	}

	if err := os.Remove(targetAbs); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	recoveryAbs := filepath.Join(dataDir, "workspaces", projectSlug, ".ottercamp", "recovery", filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(recoveryAbs), 0o755); err != nil {
		t.Fatalf("mkdir recovery artifact: %v", err)
	}
	if err := os.WriteFile(recoveryAbs, []byte("## Draft Content\n\n# OC-4 Execution Log\n\n## Findings\nRecovered substantive edge-case evidence.\n"), 0o644); err != nil {
		t.Fatalf("write recovery artifact: %v", err)
	}
	if blocked, _ := fixture.engine.shouldBlockTaskExecutionOffTargetEvidenceTool(context.Background(), rt, "file.read", map[string]any{"path": "planning/discovery-plan/oc-9-validation-plan.md"}); !blocked {
		t.Fatal("expected recovery-artifact-backed off-target planning read to be blocked")
	}
}

func TestBuildTaskExecutionOffTargetEvidenceToolGuardError(t *testing.T) {
	t.Parallel()

	msg := buildTaskExecutionOffTargetEvidenceToolGuardError("Test/test-execution-oc13-scenario2-edge-cases.md", "planning/discovery-plan/oc-9-validation-plan.md", "file.read")
	if !strings.Contains(msg, "substantive content") || !strings.Contains(msg, "planning/discovery-plan/oc-9-validation-plan.md") {
		t.Fatalf("file.read guard = %q, want deliverable-focus guidance", msg)
	}
	msg = buildTaskExecutionOffTargetEvidenceToolGuardError("Test/test-execution-oc13-scenario2-edge-cases.md", "", "git.log")
	if !strings.Contains(msg, "Do not inspect git history now") {
		t.Fatalf("git.log guard = %q, want git-history guidance", msg)
	}
}

func TestShouldBlockTaskRecoveryStatusPathTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project_task",
			Mode:      "async",
		},
	}

	if !shouldBlockTaskRecoveryStatusPathTool(rt, "file.read", map[string]any{"path": "planning/recovery-state/OC-20-WORK-COMPLETE-SUMMARY.md"}) {
		t.Fatal("expected task recovery-state read to be blocked")
	}
	if !shouldBlockTaskRecoveryStatusPathTool(rt, "file.write", map[string]any{"path": ".ottercamp/recovery/docs/migration-plan/oc-15.md"}) {
		t.Fatal("expected task recovery artifact write to be blocked")
	}
	if !shouldBlockTaskRecoveryStatusPathTool(rt, "file.list", map[string]any{"path": "planning/checkpoint"}) {
		t.Fatal("expected task checkpoint listing to be blocked")
	}
	if shouldBlockTaskRecoveryStatusPathTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/oc-24-infrastructure-spec.md"}) {
		t.Fatal("deliverable file read should remain available")
	}

	rt.session.Mode = "sync"
	if shouldBlockTaskRecoveryStatusPathTool(rt, "file.read", map[string]any{"path": "planning/recovery-state/OC-20-WORK-COMPLETE-SUMMARY.md"}) {
		t.Fatal("expected sync task session to bypass async recovery-state guard")
	}
}

func TestShouldBlockTaskStatusMessageTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project_task",
			Mode:      "async",
			ID:        uuid.New(),
		},
	}

	if !shouldBlockTaskStatusMessageTool(rt, "message.send", map[string]any{"content": "Notifying the team that the task is complete."}) {
		t.Fatal("expected task status message without target session to be blocked")
	}
	if shouldBlockTaskStatusMessageTool(rt, "message.send", map[string]any{"session_id": uuid.NewString(), "content": "handoff"}) {
		t.Fatal("explicit target session should remain available")
	}
	if shouldBlockTaskStatusMessageTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/spec.md"}) {
		t.Fatal("non-message tool should not trigger status-message guard")
	}
}

func TestShouldBlockTaskPlaceholderDeliverableFollowOnTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project_task",
			Mode:      "async",
		},
		placeholderTargetSeen: true,
	}

	if !shouldBlockTaskPlaceholderDeliverableFollowOnTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked after placeholder deliverable read")
	}
	if !shouldBlockTaskPlaceholderDeliverableFollowOnTool(rt, "git.log", nil) {
		t.Fatal("expected git.log to be blocked after placeholder deliverable read")
	}
	if !shouldBlockTaskPlaceholderDeliverableFollowOnTool(rt, "file.list", map[string]any{"path": "Work"}) {
		t.Fatal("expected file.list to be blocked after placeholder deliverable read")
	}
	if !shouldBlockTaskPlaceholderDeliverableFollowOnTool(rt, "file.search", map[string]any{"path": "Work", "pattern": "OC-13"}) {
		t.Fatal("expected file.search to be blocked after placeholder deliverable read")
	}
	if !shouldBlockTaskPlaceholderDeliverableFollowOnTool(rt, "file.read", map[string]any{"path": "planning/discovery-plan/oc-13-validation-plan.md"}) {
		t.Fatal("expected planning file.read to be blocked after placeholder deliverable read")
	}
	if shouldBlockTaskPlaceholderDeliverableFollowOnTool(rt, "file.read", map[string]any{"path": "Work/OC-12-VALIDATION-REPORT.md"}) {
		t.Fatal("expected targeted deliverable read to remain available after placeholder deliverable read")
	}

	rt.placeholderTargetSeen = false
	if shouldBlockTaskPlaceholderDeliverableFollowOnTool(rt, "task.list", nil) {
		t.Fatal("expected no follow-on blocking before placeholder deliverable read")
	}
}

func TestShouldNotStopAfterBlockedProjectBootstrapRecoveryRereadScaffoldOnlyRecovery(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          3,
		PlannedTaskCount:         0,
		CurrentPhase:             projectBootstrapCheckpointTaskTreePersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointStaffingPersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureRuntime,
		ValidationFailureReason:  buildProjectBootstrapScaffoldOnlyFailureReason(),
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: buildProjectBootstrapScaffoldOnlyFailureReason(),
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, true) {
		t.Fatal("expected scaffold-only recovery reread block to continue the current turn")
	}
}

func TestProjectBootstrapFailureClassForReasonSelectedTaskDependencyBlock(t *testing.T) {
	reason := buildProjectBootstrapFirstWaveDependencyFailureReason(repo.ProjectTask{
		TaskNumber: 9,
		Title:      "Test: Happy Path Scenario",
	})
	if got := projectBootstrapFailureClassForReason("", reason); got != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("failure class = %q, want %q", got, projectBootstrapFailureFirstWaveExecution)
	}
}

func TestIsTransientInfrastructureError(t *testing.T) {
	if !isTransientInfrastructureError(errors.New("failed to connect to database: FATAL: remaining connection slots are reserved for roles with the SUPERUSER attribute (SQLSTATE 53300)")) {
		t.Fatal("expected SQLSTATE 53300 connection exhaustion to classify as transient infrastructure error")
	}
	if !isTransientInfrastructureError(errors.New("pq: sorry, too many clients already")) {
		t.Fatal("expected too many clients error to classify as transient infrastructure error")
	}
	if isTransientInfrastructureError(errors.New("provider auth failed")) {
		t.Fatal("unexpected infrastructure classification for unrelated error")
	}
}

func TestIsTransientModelError(t *testing.T) {
	if !isTransientModelError(errors.New("stream error: stream ID 135; INTERNAL_ERROR; received from peer")) {
		t.Fatal("expected provider stream INTERNAL_ERROR reset to classify as transient model error")
	}
	if !isTransientModelError(errors.New("request hit timeout while waiting for model response")) {
		t.Fatal("expected timeout text to classify as transient model error")
	}
	if isTransientModelError(errors.New("provider auth failed")) {
		t.Fatal("unexpected transient model classification for unrelated error")
	}
}

func TestShouldAutoClearTransientExecutionRuntimePauseForSession(t *testing.T) {
	session := &chat.ChatSession{
		ID:        uuid.New(),
		ScopeType: "project_task",
		ScopeID:   uuid.New(),
	}
	pauseState := projectpause.State{
		IsPaused: true,
		Reason:   "stream error: stream ID 135; INTERNAL_ERROR; received from peer",
		Metadata: json.RawMessage(`{"source":"execution_runtime","failure_reason":"stream error: stream ID 135; INTERNAL_ERROR; received from peer"}`),
	}

	if !shouldAutoClearTransientExecutionRuntimePauseForSession(session, pauseState) {
		t.Fatal("expected transient execution-runtime pause on a task session to auto-clear")
	}

	projectSession := &chat.ChatSession{
		ID:        uuid.New(),
		ScopeType: "project",
		ScopeID:   uuid.New(),
	}
	if shouldAutoClearTransientExecutionRuntimePauseForSession(projectSession, pauseState) {
		t.Fatal("project-scoped pause should not auto-clear through task-session recovery")
	}

	authPause := projectpause.State{
		IsPaused: true,
		Reason:   "provider auth failed",
		Metadata: json.RawMessage(`{"source":"execution_runtime","failure_reason":"provider auth failed"}`),
	}
	if shouldAutoClearTransientExecutionRuntimePauseForSession(session, authPause) {
		t.Fatal("non-transient execution-runtime pause should not auto-clear")
	}
}

func TestIsRecoverableExecutionContinuationDepthError(t *testing.T) {
	if !isRecoverableExecutionContinuationDepthError(errContextCompressionContinuationDepthExceeded) {
		t.Fatal("context compression continuation depth sentinel should be recoverable")
	}
	if !isRecoverableExecutionContinuationDepthError(errAgentTurnPromptGuardrailDepthExceeded) {
		t.Fatal("prompt guardrail continuation depth sentinel should be recoverable")
	}
	if isRecoverableExecutionContinuationDepthError(errors.New("agent turn prompt exceeded guardrail continuation depth")) {
		t.Fatal("plain string-matched errors should not be treated as recoverable continuation depth failures")
	}
	if isRecoverableExecutionContinuationDepthError(nil) {
		t.Fatal("nil error should not be treated as recoverable continuation depth failure")
	}
}

func TestIsRecoverableProjectExecutionFailure(t *testing.T) {
	if !isRecoverableProjectExecutionFailure(ErrModelTransient) {
		t.Fatal("transient model failures should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(ErrRateLimited) {
		t.Fatal("rate limit failures should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(errContextCompressionContinuationDepthExceeded) {
		t.Fatal("continuation depth failures should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(errors.New("pq: sorry, too many clients already")) {
		t.Fatal("transient infrastructure failures should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(chat.ErrInvalidStatusTransition) {
		t.Fatal("chat invalid status transition races should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(tasksvc.ErrInvalidStatusTransition{From: "review", To: "in_progress"}) {
		t.Fatal("task invalid status transition races should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(repo.ErrConflict) {
		t.Fatal("optimistic status conflicts should be recoverable for project execution")
	}
	if isRecoverableProjectExecutionFailure(errors.New("provider auth failed")) {
		t.Fatal("provider auth failures should not be treated as recoverable project execution failures")
	}
	if isRecoverableProjectExecutionFailure(nil) {
		t.Fatal("nil should not be treated as recoverable project execution failure")
	}
}

func TestHandleRecoverableProjectExecutionTurnFailureConvertsInvalidTransitionToCleanExit(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	turnID := uuid.New()
	sessionID := uuid.New()
	fixture.chat.session = &chat.ChatSession{
		ID:             sessionID,
		OrganizationID: fixture.session.OrganizationID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CurrentTurnID:  &turnID,
	}
	fixture.chat.enforceStatus = true
	fixture.chat.turns[turnID] = &chat.ChatTurn{
		ID:        turnID,
		SessionID: sessionID,
		Status:    "in_progress",
	}
	engine := fixture.engine
	runtime := &turnRuntime{
		session: fixture.chat.session,
		turn:    fixture.chat.turns[turnID],
	}

	handled, err := engine.handleRecoverableProjectExecutionTurnFailure(context.Background(), runtime, tasksvc.ErrInvalidStatusTransition{From: "review", To: "in_progress"})
	if err != nil {
		t.Fatalf("handleRecoverableProjectExecutionTurnFailure: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	current, getErr := fixture.chat.GetTurn(context.Background(), turnID)
	if getErr != nil {
		t.Fatalf("GetTurn: %v", getErr)
	}
	if current.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", current.Status)
	}
	if fixture.messages.containsContentSubstring("[Turn failed:") {
		t.Fatal("unexpected generic turn failed system message for recoverable execution race")
	}
}

func TestHandleUserMessageProjectScopeKickoffHandoffRoutesToLoriAfterFrank(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	completedAt := time.Now().UTC()
	seededTurnID := uuid.New()
	fixture.chat.turns[seededTurnID] = &chat.ChatTurn{
		ID:             seededTurnID,
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   frankID,
		Status:         "completed",
		StartedAt:      &completedAt,
		CompletedAt:    &completedAt,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, seededTurnID)
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != loriID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, loriID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasLori := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == loriID {
			hasLori = true
			break
		}
	}
	if !hasLori {
		t.Fatalf("expected Lori %s to be added as a session participant", loriID)
	}
}

func TestHandleUserMessageProjectScopeKickoffDoesNotHandoffOnInProgressFrankTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	startedAt := time.Now().UTC()
	seededTurnID := uuid.New()
	fixture.chat.turns[seededTurnID] = &chat.ChatTurn{
		ID:             seededTurnID,
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   frankID,
		Status:         "in_progress",
		StartedAt:      &startedAt,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, seededTurnID)
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != frankID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, frankID)
	}
}

func TestHandleUserMessageProjectScopeKickoffDoesNotHandoffOnFailedFrankTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	startedAt := time.Now().UTC()
	completedAt := startedAt.Add(1 * time.Second)
	seededTurnID := uuid.New()
	fixture.chat.turns[seededTurnID] = &chat.ChatTurn{
		ID:             seededTurnID,
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   frankID,
		Status:         "failed",
		StartedAt:      &startedAt,
		CompletedAt:    &completedAt,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, seededTurnID)
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != frankID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, frankID)
	}
}

func TestHandleUserMessageTaskScopeMissingTaskBindingReturnsInvariant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	otherParticipantID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   otherParticipantID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			otherParticipantID: {ID: otherParticipantID, OrganizationID: fixture.session.OrganizationID},
			frankID:            {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: uuid.New(), DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil {
		t.Fatal("HandleUserMessage error = nil, want missing task binding invariant")
	}
	if !strings.Contains(err.Error(), "internal invariant: task-scoped session task binding was not found") {
		t.Fatalf("HandleUserMessage error = %v, want missing task binding invariant", err)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}
}

func TestDispatchToolsTaskScopeInjectsBoundProjectAndTaskIDs(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}

	var dispatched ToolCall
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		dispatched = call
		runID := uuid.New()
		onRunStarted(runID)
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}, RunID: &runID}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:        "tier2",
					Name:      "cli.execute",
					Tier:      "tier2",
					Arguments: map[string]any{"command": "echo hello"},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["task_id"]; got != taskID.String() {
		t.Fatalf("task_id = %v, want %s", got, taskID)
	}
	if got := dispatched.Arguments["session_id"]; got != fixture.session.ID.String() {
		t.Fatalf("session_id = %v, want %s", got, fixture.session.ID)
	}
}

func TestDispatchToolsProjectScopeOverridesStaleBoundIDs(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	staleProjectID := uuid.New()
	staleSessionID := uuid.New()
	staleTaskID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "task.list",
					Arguments: map[string]any{
						"project_id": staleProjectID.String(),
						"session_id": staleSessionID.String(),
						"task_id":    staleTaskID.String(),
						"agent_id":   uuid.New().String(),
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["session_id"]; got != fixture.session.ID.String() {
		t.Fatalf("session_id = %v, want %s", got, fixture.session.ID)
	}
	if got := dispatched.Arguments["agent_id"]; got != fixture.chat.participants[0].ParticipantID.String() {
		t.Fatalf("agent_id = %v, want %s", got, fixture.chat.participants[0].ParticipantID)
	}
	if _, exists := dispatched.Arguments["task_id"]; exists {
		t.Fatalf("task_id = %v, want stale task binding cleared for project-scoped dispatch", dispatched.Arguments["task_id"])
	}
}

func TestDispatchToolsMessageSendPreservesExplicitTargetSessionID(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	targetSessionID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "message.send",
					Arguments: map[string]any{
						"session_id": targetSessionID.String(),
						"role":       "user",
						"content":    "handoff",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["session_id"]; got != targetSessionID.String() {
		t.Fatalf("session_id = %v, want explicit target %s", got, targetSessionID)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["agent_id"]; got != fixture.chat.participants[0].ParticipantID.String() {
		t.Fatalf("agent_id = %v, want %s", got, fixture.chat.participants[0].ParticipantID)
	}
}

func TestDispatchToolsBlocksTaskMessageSendWithoutSessionID(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}

	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		t.Fatalf("unexpected dispatched tool call: %s", call.Name)
		return ToolResult{}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "notify-team",
					Name: "message.send",
					Arguments: map[string]any{
						"role":    "user",
						"content": "Notify the team this task is complete.",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var blocked bool
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") &&
			message.ToolCallID != nil &&
			*message.ToolCallID == "notify-team" &&
			strings.Contains(message.Content, "status or notification messages without an explicit destination session") {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("missing blocked message.send tool_result for task status-message guard")
	}
}

func TestDispatchToolsBlocksTaskRecoveryStateRead(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}

	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		t.Fatalf("unexpected dispatched tool call: %s", call.Name)
		return ToolResult{}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "read-recovery",
					Name: "file.read",
					Arguments: map[string]any{
						"path": "planning/recovery-state/OC-20-WORK-COMPLETE-SUMMARY.md",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var blocked bool
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") &&
			message.ToolCallID != nil &&
			*message.ToolCallID == "read-recovery" &&
			strings.Contains(message.Content, "should not reread recovery-state or checkpoint files") {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("missing blocked recovery-state tool_result for async task session")
	}
}

func TestDispatchToolsAssignProjectPreservesExplicitTargetAgentID(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	targetAgentID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "agent.assign_project",
					Arguments: map[string]any{
						"agent_id": targetAgentID.String(),
						"role":     "worker",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["agent_id"]; got != targetAgentID.String() {
		t.Fatalf("agent_id = %v, want explicit target %s", got, targetAgentID)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["session_id"]; got != fixture.session.ID.String() {
		t.Fatalf("session_id = %v, want %s", got, fixture.session.ID)
	}
}

func TestDispatchToolsTaskGetPreservesExplicitTargetTaskIDInProjectScope(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	targetTaskID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "task.get",
					Arguments: map[string]any{
						"task_id": targetTaskID.String(),
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["task_id"]; got != targetTaskID.String() {
		t.Fatalf("task_id = %v, want explicit target %s", got, targetTaskID)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
}

func TestTierRoutingExecutesTier1ParallelThenTier2Sequential(t *testing.T) {
	fixture := newUnitFixture(t, "sync")

	var (
		mu         sync.Mutex
		tier1Start []time.Time
		tier2Start []time.Time
	)
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		mu.Lock()
		tier1Start = append(tier1Start, time.Now())
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		mu.Lock()
		tier2Start = append(tier2Start, time.Now())
		mu.Unlock()
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"done": true}, RunID: &runID}, nil
	}

	callRound := 0
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		_ = onChunk("token")
		callRound++
		if callRound == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "t1", Name: "file.read", Tier: "tier1"},
				{ID: "t2", Name: "memory.query", Tier: "tier1"},
				{ID: "t3", Name: "cli.execute", Tier: "tier2"},
			}}, nil
		}
		return ModelResponse{Content: "final"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(tier1Start) != 2 {
		t.Fatalf("tier1 calls = %d, want 2", len(tier1Start))
	}
	if len(tier2Start) != 1 {
		t.Fatalf("tier2 calls = %d, want 1", len(tier2Start))
	}
	latestTier1 := tier1Start[0]
	for _, at := range tier1Start[1:] {
		if at.After(latestTier1) {
			latestTier1 = at
		}
	}
	if tier2Start[0].Before(latestTier1) {
		t.Fatalf("tier2 started before tier1 completed: tier2=%s latest_tier1=%s", tier2Start[0], latestTier1)
	}
}

func TestTierRoutingTwoTier2ToolsAreSequential(t *testing.T) {
	fixture := newUnitFixture(t, "sync")

	var (
		mu          sync.Mutex
		startedAt   = map[string]time.Time{}
		completedAt = map[string]time.Time{}
	)
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)

		start := time.Now()
		mu.Lock()
		startedAt[call.ID] = start
		mu.Unlock()

		if call.ID == "tier2-a" {
			time.Sleep(40 * time.Millisecond)
		}

		end := time.Now()
		mu.Lock()
		completedAt[call.ID] = end
		mu.Unlock()

		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}, RunID: &runID}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "tier2-a", Name: "cli.execute", Tier: "tier2"},
				{ID: "tier2-b", Name: "run.deploy", Tier: "tier2"},
			}}, nil
		}
		return ModelResponse{Content: "final"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if len(startedAt) != 2 || len(completedAt) != 2 {
		t.Fatalf("tier2 calls recorded start=%d complete=%d, want 2 each", len(startedAt), len(completedAt))
	}
	if startedAt["tier2-b"].Before(completedAt["tier2-a"]) {
		t.Fatalf("tier2-b started before tier2-a completed: start=%s first_done=%s", startedAt["tier2-b"], completedAt["tier2-a"])
	}
}

func TestMaxToolCallsSyncStops(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.maxToolCalls = 3

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{
			{ID: "a", Name: "file.read", Tier: "tier1"},
			{ID: "b", Name: "memory.query", Tier: "tier1"},
			{ID: "c", Name: "chat.search", Tier: "tier1"},
			{ID: "d", Name: "task.list", Tier: "tier1"},
		}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if !fixture.messages.containsContent("[Max tool calls reached. Turn ended.]") {
		t.Fatal("missing max tool calls stop system message")
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) != 1 {
		t.Fatalf("turn count = %d, want 1", len(fixture.chat.turnOrder))
	}
	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if firstTurn == nil || firstTurn.StopReason == nil || *firstTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("first turn stop_reason = %v, want %q", firstTurn.StopReason, stopReasonMaxToolCalls)
	}
}

func TestMaxToolCallsAsyncContinuation(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.maxToolCalls = 3

	var (
		mu              sync.Mutex
		dispatchedTier1 []string
	)
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		mu.Lock()
		dispatchedTier1 = append(dispatchedTier1, call.ID)
		mu.Unlock()
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "a", Name: "file.read", Tier: "tier1"},
				{ID: "b", Name: "memory.query", Tier: "tier1"},
				{ID: "c", Name: "chat.search", Tier: "tier1"},
				{ID: "d", Name: "task.list", Tier: "tier1"},
			}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "e", Name: "chat.search", Tier: "tier1"},
				{ID: "f", Name: "task.list", Tier: "tier1"},
			}}, nil
		default:
			return ModelResponse{Content: "final"}, nil
		}
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), dispatchedTier1...)
	mu.Unlock()
	sort.Strings(got)
	if len(got) != 5 {
		t.Fatalf("tier1 dispatches = %v, want 5 total tool calls across continuation", got)
	}
	if strings.Join(got, ",") != "a,b,c,e,f" {
		t.Fatalf("tier1 dispatch calls = %v, want [a b c e f]", got)
	}
	if fixture.model.continuationSummaryCalls != 1 {
		t.Fatalf("continuation summary calls = %d, want 1", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) != 2 {
		t.Fatalf("turn count = %d, want 2", len(fixture.chat.turnOrder))
	}
	if !fixture.messages.containsContent("[Max tool calls reached. Turn ended.]") {
		t.Fatal("missing max tool calls stop system message")
	}
	if !fixture.messages.containsContent("[Continuation summary] respond") {
		t.Fatal("missing continuation summary message")
	}
	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if firstTurn == nil || firstTurn.StopReason == nil || *firstTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("first turn stop_reason = %v, want %q", firstTurn.StopReason, stopReasonMaxToolCalls)
	}
}

func TestMaxToolCallsAsyncContinuationRecoversLeakedInProgressTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.maxToolCalls = 3
	fixture.chat.completeNoop = true

	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{
			{ID: "a", Name: "file.read", Tier: "tier1"},
			{ID: "b", Name: "memory.query", Tier: "tier1"},
			{ID: "c", Name: "chat.search", Tier: "tier1"},
			{ID: "d", Name: "task.list", Tier: "tier1"},
		}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(fixture.chat.turnOrder) == 0 {
		t.Fatal("expected at least one turn")
	}
	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if firstTurn == nil {
		t.Fatal("missing first turn")
	}
	if firstTurn.Status != "failed" {
		t.Fatalf("first turn status = %q, want failed after leak recovery", firstTurn.Status)
	}
	if firstTurn.StopReason == nil || *firstTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("first turn stop_reason = %v, want %q", firstTurn.StopReason, stopReasonMaxToolCalls)
	}
	if fixture.chat.failCalls < 1 {
		t.Fatalf("fail calls = %d, want >= 1", fixture.chat.failCalls)
	}
	if !fixture.messages.containsContent("[Recovered leaked in-progress turn after max-tool-calls handoff - allowing queued continuation to proceed.]") {
		t.Fatal("missing leaked turn recovery system message")
	}
	for _, turnID := range fixture.chat.turnOrder {
		turn := fixture.chat.turnByID(turnID)
		if turn == nil {
			continue
		}
		if turn.Status == "in_progress" {
			t.Fatalf("turn %s still in_progress after leak recovery", turn.ID)
		}
	}
	for _, job := range fixture.enqueuer.agentTurnJobs() {
		if job.payload == nil {
			t.Fatal("expected agent_turn payload")
		}
		if job.payload.MessageID == fixture.userMessageID {
			t.Fatal("expected continuation payload to target a follow-on message, not the original user message")
		}
	}
}

func TestProjectFailureActionForProgressPausesAfterTaskTreePersistence(t *testing.T) {
	progress := projectBootstrapProgress{
		AssignmentCount:          3,
		PlannedTaskCount:         5,
		PlannedFlowTemplateCount: 1,
	}
	if got := projectFailureActionForProgress(progress, projectFailureCategoryBootstrap, projectBootstrapFailureCompoundParent); got != projectFailureActionPause {
		t.Fatalf("projectFailureActionForProgress = %q, want %q", got, projectFailureActionPause)
	}
}

func TestProjectFailureActionForProgressArchivesBeforeTaskTreePersistence(t *testing.T) {
	progress := projectBootstrapProgress{
		AssignmentCount: 1,
	}
	if got := projectFailureActionForProgress(progress, projectFailureCategoryBootstrap, projectBootstrapFailureMissingAssignments); got != projectFailureActionArchive {
		t.Fatalf("projectFailureActionForProgress = %q, want %q", got, projectFailureActionArchive)
	}
}

func TestBuildProjectBootstrapAutomaticFailureMessageUsesPauseLanguageAfterRecoverableCheckpoint(t *testing.T) {
	record := buildProjectBootstrapAutomaticFailureRecord(projectBootstrapProgress{
		AssignmentCount:          3,
		PlannedTaskCount:         5,
		PlannedFlowTemplateCount: 1,
	}, projectFailureCategoryBootstrap, projectBootstrapFailureCompoundParent, "task tree needs repair", time.Now().UTC())
	if record.Action != projectFailureActionPause {
		t.Fatalf("record.Action = %q, want %q", record.Action, projectFailureActionPause)
	}
	message := buildProjectBootstrapAutomaticFailureMessage(record)
	if !strings.Contains(message, "paused automatically because execution had already reached") {
		t.Fatalf("message = %q, want pause wording", message)
	}
}

func TestBuildProjectBootstrapAutomaticFailureRecordUsesStaffingCheckpointForPersistedDrafts(t *testing.T) {
	record := buildProjectBootstrapAutomaticFailureRecord(projectBootstrapProgress{
		StaffingDraftCount: 3,
	}, projectFailureCategoryBootstrap, projectBootstrapFailureStalled, "bootstrap stalled", time.Now().UTC())
	if record.LastCheckpoint != projectBootstrapCheckpointStaffingPersisted {
		t.Fatalf("record.LastCheckpoint = %q, want %q", record.LastCheckpoint, projectBootstrapCheckpointStaffingPersisted)
	}
	if record.LastSuccessfulCheckpoint != projectBootstrapCheckpointStaffingPersisted {
		t.Fatalf("record.LastSuccessfulCheckpoint = %q, want %q", record.LastSuccessfulCheckpoint, projectBootstrapCheckpointStaffingPersisted)
	}
	if record.SetupPersisted != true {
		t.Fatalf("record.SetupPersisted = %v, want true", record.SetupPersisted)
	}
}

func TestProjectBootstrapRecoveryEndedByRereadGuard(t *testing.T) {
	messages := []repo.ChatMessage{
		{Role: "assistant", Content: "I'll inspect the project state first."},
		{Role: "system", Content: "[Bootstrap validation recovery reread blocked - ending this turn so the next continuation can repair the named blocker directly.]"},
	}
	if !projectBootstrapRecoveryEndedByRereadGuard(messages) {
		t.Fatal("projectBootstrapRecoveryEndedByRereadGuard = false, want true")
	}
}

func TestProjectBootstrapRecoverableMaxToolCallFailure(t *testing.T) {
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass: projectBootstrapFailureCompoundParent,
	}) {
		t.Fatal("compound parent failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass: projectBootstrapFailureFirstWaveSize,
	}) {
		t.Fatal("first-wave size failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "task exceeds bounded size policy (estimated 35 minutes > 30 minute limit): split the work",
	}) {
		t.Fatal("bounded-size first-wave execution failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 39 (Write photography + brand post titles) has no assigned agent, so bootstrap cannot queue runnable execution",
	}) {
		t.Fatal("unassigned first-wave execution failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: only 12 of 20 selected first-wave child tasks created flow_node_execution rows, so bootstrap never materialized the full runnable child wave",
	}) {
		t.Fatal("partial first-wave execution materialization should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureRuntime,
		ValidationFailureReason: buildProjectBootstrapRestartScaffoldFailureReason(),
	}) {
		t.Fatal("restart scaffold runtime failure should be recoverable")
	}
	if projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: bootstrap setup persisted staffing but did not emit any executable non-bootstrap project tasks for the first wave",
	}) {
		t.Fatal("non-bounded first-wave execution failure should not be recoverable")
	}
}

func TestProjectBootstrapProgressAdvancedBeyondState(t *testing.T) {
	state := projectBootstrapState{
		AssignmentCount:          4,
		PlannedTaskCount:         20,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       18,
		FirstWavePromotedCount:   17,
		FirstWaveExecutionCount:  17,
		FirstWaveJobCount:        16,
	}
	progress := projectBootstrapProgress{
		AssignmentCount:          4,
		PlannedTaskCount:         20,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       18,
		FirstWavePromotedCount:   18,
		FirstWaveExecutionCount:  18,
		FirstWaveJobCount:        17,
	}
	if !projectBootstrapProgressAdvancedBeyondState(state, progress) {
		t.Fatal("later first-wave counts should reset recoverable continuation budget")
	}
	if projectBootstrapProgressAdvancedBeyondState(state, projectBootstrapProgress{
		AssignmentCount:          4,
		PlannedTaskCount:         20,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       18,
		FirstWavePromotedCount:   17,
		FirstWaveExecutionCount:  17,
		FirstWaveJobCount:        16,
	}) {
		t.Fatal("unchanged progress should not reset recoverable continuation budget")
	}
}

func TestProjectBootstrapPauseInvalidatedByCurrentProgressForFirstWavePromotion(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusFailed,
		FailureClass:             "first_wave_execution_missing",
		FailurePhase:             projectBootstrapCheckpointFirstWaveExecutions,
		AssignmentCount:          3,
		PlannedTaskCount:         3,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       3,
	}
	progress := projectBootstrapProgress{
		AssignmentCount:          3,
		PlannedTaskCount:         3,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       3,
		FirstWavePromotedCount:   3,
	}
	if !projectBootstrapPauseInvalidatedByCurrentProgress(state, progress) {
		t.Fatal("first-wave promotion should invalidate stale first-wave execution pause")
	}
}

func TestProjectBootstrapPauseMetadataInvalidatedByCurrentProgressForFirstWavePromotion(t *testing.T) {
	progress := projectBootstrapProgress{
		FirstWaveTaskCount:     3,
		FirstWavePromotedCount: 3,
	}
	metadata := json.RawMessage(`{"failure_class":"first_wave_execution_missing","failure_phase":"first_wave_executions_created"}`)
	if !projectBootstrapPauseMetadataInvalidatedByCurrentProgress(metadata, progress) {
		t.Fatal("pause metadata should invalidate stale first-wave execution pause after promotion")
	}
}

func TestBuildProjectBootstrapValidationRecoveryPrompt(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason: "kickoff validation failed: first-wave task 12 (WS4: Blog Post Ideation) violates the bounded task-size policy",
	})
	if !strings.Contains(prompt, "automatic follow-on bootstrap turn 2") {
		t.Fatalf("prompt = %q, want turn count", prompt)
	}
	if !strings.Contains(prompt, "WS4: Blog Post Ideation") {
		t.Fatalf("prompt = %q, want validation reason detail", prompt)
	}
	if !strings.Contains(prompt, "Do not repeat the same oversized task definitions") {
		t.Fatalf("prompt = %q, want anti-repeat guidance", prompt)
	}
	if !strings.Contains(prompt, "splitting the offending broad parent or first-wave task into narrower executable child tasks") {
		t.Fatalf("prompt = %q, want bounded child-splitting guidance", prompt)
	}
	if !strings.Contains(prompt, "Your next assistant action should be a tool call, not a narrative reply") {
		t.Fatalf("prompt = %q, want no-narrative bounded repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get on the named oversized task first") {
		t.Fatalf("prompt = %q, want direct no-task.get bounded repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not return to bootstrap.setup.persist until that structural repair is complete") {
		t.Fatalf("prompt = %q, want setup persist deferral guidance", prompt)
	}
	if !strings.Contains(prompt, "Every task.create or subtask.create call must include a concrete non-empty title") {
		t.Fatalf("prompt = %q, want title guidance", prompt)
	}
	if !strings.Contains(prompt, "The bootstrap governance gate task is system-managed") {
		t.Fatalf("prompt = %q, want governance gate guidance", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedFirstWaveTask(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(3, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	})
	if !strings.Contains(prompt, "has no assigned agent") {
		t.Fatalf("prompt = %q, want validation reason detail", prompt)
	}
	if !strings.Contains(prompt, "Do not call bootstrap.setup.persist until every selected first-wave task has an assigned active project agent") {
		t.Fatalf("prompt = %q, want first-wave assignment guidance", prompt)
	}
	if !strings.Contains(prompt, "Repair the named persisted first-wave task directly") {
		t.Fatalf("prompt = %q, want direct task repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.get, task.list, task.children, flow.list_templates, agent.list") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get with the bare task number from the validation error") {
		t.Fatalf("prompt = %q, want no bare task.get guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get on the named blocked task first") {
		t.Fatalf("prompt = %q, want direct no task.get guidance for named blocked task", prompt)
	}
	if !strings.Contains(prompt, "Your next assistant action should be a tool call, not a narrative reply") {
		t.Fatalf("prompt = %q, want direct tool-call guidance for named blocked task", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedFirstWaveTaskSkipsFlowRereadAfterRepair(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(3, projectBootstrapProgress{
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
		PlannedFlowTemplateCount: 2,
		BootstrapSetupDoneCount:  5,
		BootstrapTaskOutstanding: true,
	})
	if !strings.Contains(prompt, "do not go back to flow.list_templates") {
		t.Fatalf("prompt = %q, want no flow reread guidance after direct repair", prompt)
	}
	if !strings.Contains(prompt, "return straight to bootstrap.setup.persist with canonical step slugs") {
		t.Fatalf("prompt = %q, want persist-after-repair guidance", prompt)
	}
	if !strings.Contains(prompt, "attach-validate-flow-templates, select-first-wave, and record-frank-sign-off") {
		t.Fatalf("prompt = %q, want canonical remaining-step guidance", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedFirstWaveTaskSkipsProjectRereadAfterAssignmentFix(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(3, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	})
	if !strings.Contains(prompt, "After you fix the assignment, do not call project.list, project.get, task.list, or flow.list_templates") {
		t.Fatalf("prompt = %q, want no broad reread guidance after assignment fix", prompt)
	}
	if !strings.Contains(prompt, "If that same named task still looks broad, keep it orchestration-only and split it directly") {
		t.Fatalf("prompt = %q, want direct split guidance after assignment fix", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedWaveParent(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 11 (Wave 3: Polish, Deploy & Content) has no assigned agent",
	})
	if !strings.Contains(prompt, "keep the parent orchestration-only and immediately create bounded executable child tasks beneath it") {
		t.Fatalf("prompt = %q, want parent-splitting guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call file.read on planning artifacts just to decide the child split") {
		t.Fatalf("prompt = %q, want no planning-artifact reread guidance", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForApprovalGatedFirstWaveTask(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 12 (Generate analytics report script) requires human approval before queueing, so bootstrap cannot materialize autonomous runnable execution",
	})
	if !strings.Contains(prompt, "requires human approval before queueing") {
		t.Fatalf("prompt = %q, want validation reason detail", prompt)
	}
	if !strings.Contains(prompt, "Do not ask for manual approval") {
		t.Fatalf("prompt = %q, want no-manual-approval guidance", prompt)
	}
	if !strings.Contains(prompt, "Remove the approval gate from that exact task or replace it in the selected first wave") {
		t.Fatalf("prompt = %q, want direct repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.list, project.get, task.list, flow.list_templates, agent.list, inbox reads, or staffing discovery") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
}

func TestProjectBootstrapRecoverableMaxToolCallFailureForApprovalGatedFirstWaveTask(t *testing.T) {
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 12 (Generate analytics report script) requires human approval before queueing, so bootstrap cannot materialize autonomous runnable execution",
	}) {
		t.Fatal("approval-gated first-wave execution failure should be recoverable")
	}
}

func TestProjectBootstrapRecoverableMaxToolCallFailureForProjectGateFirstWaveTask(t *testing.T) {
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 15 (Create flow template) is a project-wide gate (blocks_scope=all), so it cannot be selected alongside other first-wave tasks because it will block the rest of the wave from entering runnable execution",
	}) {
		t.Fatal("project-gated first-wave execution failure should be recoverable")
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForPartialFirstWaveMaterialization(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: only 12 of 20 selected first-wave child tasks created flow_node_execution rows, so bootstrap never materialized the full runnable child wave",
	})
	if !strings.Contains(prompt, "Shrink the selected first wave to a smaller bounded subset of the already-created child tasks") {
		t.Fatalf("prompt = %q, want first-wave narrowing guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.list, project.get, task.list, flow.list_templates, flow.get_execution, file.read, file.write, agent.list, or staffing discovery") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not rewrite planning artifacts or regenerate the broader scaffold") {
		t.Fatalf("prompt = %q, want no planning rewrite guidance", prompt)
	}
	if !strings.Contains(prompt, "repair the selected runnable subset with direct task and flow mutations only") {
		t.Fatalf("prompt = %q, want direct repair guidance", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForProjectGateFirstWaveTask(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 15 (Create flow template with work/review/merge nodes) is a project-wide gate (blocks_scope=all), so it cannot be selected alongside other first-wave tasks because it will block the rest of the wave from entering runnable execution",
	})
	if !strings.Contains(prompt, "project-wide gate (blocks_scope=all)") {
		t.Fatalf("prompt = %q, want gate validation reason detail", prompt)
	}
	if !strings.Contains(prompt, "Either drop that exact task from the selected first wave and leave it in draft for later, or update it so it no longer blocks the entire project") {
		t.Fatalf("prompt = %q, want direct gate repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.list, project.get, task.list, flow.list_templates, flow.get_execution, file.read, file.write, agent.list, or staffing discovery") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForProjectGateFirstWaveTask(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 15 (Create flow template with work/review/merge nodes) is a project-wide gate (blocks_scope=all), so it cannot be selected alongside other first-wave tasks because it will block the rest of the wave from entering runnable execution",
	}, projectBootstrapResumeSnapshot{})
	if !strings.Contains(prompt, "Do not keep that named task as a project-wide gate while it remains in a multi-task first wave") {
		t.Fatalf("prompt = %q, want project-gate repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Your next assistant action should be a tool call, not a narrative reply") {
		t.Fatalf("prompt = %q, want direct tool-call guidance", prompt)
	}
}

func TestProjectBootstrapFirstWaveProjectGateConflict(t *testing.T) {
	tasks := []repo.ProjectTask{
		{TaskNumber: 10, Title: "Runnable slice", BlocksScope: "none"},
		{TaskNumber: 15, Title: "Gate slice", BlocksScope: "all"},
	}
	taskRecord, conflicted := projectBootstrapFirstWaveProjectGateConflict(tasks)
	if !conflicted {
		t.Fatal("expected conflict when multi-task first wave contains project gate")
	}
	if taskRecord.TaskNumber != 15 {
		t.Fatalf("conflicting task number = %d, want 15", taskRecord.TaskNumber)
	}
	if buildProjectBootstrapFirstWaveProjectGateFailureReason(taskRecord) == "" {
		t.Fatal("expected non-empty first-wave project gate failure reason")
	}
	if _, conflicted := projectBootstrapFirstWaveProjectGateConflict([]repo.ProjectTask{{TaskNumber: 15, BlocksScope: "all"}}); conflicted {
		t.Fatal("single-task first wave should allow a project gate")
	}
}

func TestBuildProjectBootstrapAdditionalRepairTaskLineListsOtherUnassignedFirstWaveTasks(t *testing.T) {
	progress := projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 23 (Build base layout with header and footer) has no assigned agent",
		FirstWaveTasks: []repo.ProjectTask{
			{ID: uuid.MustParse("268af254-cc71-487c-9852-8841a0e84238"), TaskNumber: 23, Title: "Build base layout with header and footer"},
			{ID: uuid.MustParse("04d0f869-dfe2-4e87-b52d-0f82b6b8ea5f"), TaskNumber: 22, Title: "Configure Tailwind and design tokens"},
			{ID: uuid.MustParse("c0fbed7a-d298-4efc-84ba-6102f7d20766"), TaskNumber: 31, Title: "Responsive styling for CTA banner section"},
		},
	}
	line := buildProjectBootstrapAdditionalRepairTaskLine(progress)
	if !strings.Contains(line, "task 22 id=04d0f869-dfe2-4e87-b52d-0f82b6b8ea5f") {
		t.Fatalf("line = %q, want remaining unassigned first-wave task", line)
	}
	if !strings.Contains(line, "task 31 id=c0fbed7a-d298-4efc-84ba-6102f7d20766") {
		t.Fatalf("line = %q, want second unassigned first-wave task", line)
	}
	if strings.Contains(line, "task 23 id=268af254-cc71-487c-9852-8841a0e84238") {
		t.Fatalf("line = %q, should not repeat the named blocked task", line)
	}
}

func TestBuildProjectBootstrapAdditionalRepairTaskLineListsFullRemainingRoster(t *testing.T) {
	progress := projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 50 (Blocked task) has no assigned agent",
		FirstWaveTasks: []repo.ProjectTask{
			{ID: uuid.MustParse("6d95d68c-87cc-47f2-a4f6-d74695d0c2f3"), TaskNumber: 50, Title: "Blocked task"},
			{ID: uuid.MustParse("7ec50721-96a7-4f2d-a2f1-a8ac578a8941"), TaskNumber: 49, Title: "Task A"},
			{ID: uuid.MustParse("f5a10df9-7f3e-4fd4-8ee0-a4a45fd936db"), TaskNumber: 48, Title: "Task B"},
			{ID: uuid.MustParse("5c74f97c-c856-42e2-904c-bc85eb03a9c3"), TaskNumber: 47, Title: "Task C"},
			{ID: uuid.MustParse("5c0852ca-1503-4bff-a837-bfdbd4429f02"), TaskNumber: 46, Title: "Task D"},
			{ID: uuid.MustParse("7bb2a8a4-e34e-47fa-b4d5-2ad85da9ad34"), TaskNumber: 45, Title: "Task E"},
		},
	}

	line := buildProjectBootstrapAdditionalRepairTaskLine(progress)
	for _, snippet := range []string{
		`task 49 id=7ec50721-96a7-4f2d-a2f1-a8ac578a8941 title="Task A"`,
		`task 48 id=f5a10df9-7f3e-4fd4-8ee0-a4a45fd936db title="Task B"`,
		`task 47 id=5c74f97c-c856-42e2-904c-bc85eb03a9c3 title="Task C"`,
		`task 46 id=5c0852ca-1503-4bff-a837-bfdbd4429f02 title="Task D"`,
		`task 45 id=7bb2a8a4-e34e-47fa-b4d5-2ad85da9ad34 title="Task E"`,
	} {
		if !strings.Contains(line, snippet) {
			t.Fatalf("line = %q, want %q", line, snippet)
		}
	}
	if strings.Contains(line, "plus ") {
		t.Fatalf("line = %q, should not truncate remaining repair targets", line)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForRestartScaffoldFailure(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureRuntime,
		ValidationFailureReason: buildProjectBootstrapRestartScaffoldFailureReason(),
	})
	if !strings.Contains(prompt, "did not materialize staffed executable project work") {
		t.Fatalf("prompt = %q, want scaffold recovery guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.get, task.list, flow.list_templates, file.list, git.log") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Reuse the dedicated project staff already created in this session") {
		t.Fatalf("prompt = %q, want direct staffing reuse guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not answer with a standalone acknowledgement or status note") {
		t.Fatalf("prompt = %q, want no-acknowledgement guidance", prompt)
	}
}

func TestEnsureTurnRunExitInvariantRejectsLeakedInProgressTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	turn, err := fixture.chat.CreateTurn(context.Background(), fixture.session.ID, fixture.chat.participants[0].ParticipantID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	rt := &turnRuntime{session: fixture.session, turn: turn}
	if err := fixture.engine.ensureTurnRunExitInvariant(context.Background(), rt); err == nil {
		t.Fatal("expected leaked in-progress turn invariant error")
	}

	if err := fixture.chat.CompleteTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err := fixture.engine.ensureTurnRunExitInvariant(context.Background(), rt); err != nil {
		t.Fatalf("ensureTurnRunExitInvariant completed turn: %v", err)
	}
}

func TestProjectBootstrapHasPriorMaxToolCallsContinuation(t *testing.T) {
	cycleID := uuid.New()
	previousStop := stopReasonMaxToolCalls
	current := chat.ChatTurn{
		ID:         uuid.New(),
		TurnNumber: 2,
		CycleID:    &cycleID,
	}
	turns := []repo.ChatTurn{
		{
			ID:         uuid.New(),
			TurnNumber: 1,
			CycleID:    &cycleID,
			Status:     "completed",
			StopReason: &previousStop,
		},
		{
			ID:         current.ID,
			TurnNumber: current.TurnNumber,
			CycleID:    current.CycleID,
			Status:     "pending",
		},
	}
	if !projectBootstrapHasPriorMaxToolCallsContinuation(turns, current) {
		t.Fatal("expected prior max-tool-calls continuation to be detected")
	}

	otherCycle := uuid.New()
	current.CycleID = &otherCycle
	if projectBootstrapHasPriorMaxToolCallsContinuation(turns, current) {
		t.Fatal("did not expect continuation detection across different cycles")
	}
}

func TestProjectBootstrapHasNewerLiveContinuationTurn(t *testing.T) {
	cycleID := uuid.New()
	completedStop := stopReasonMaxToolCalls
	completed := repo.ChatTurn{
		ID:         uuid.New(),
		TurnNumber: 1,
		CycleID:    &cycleID,
		Status:     "completed",
		StopReason: &completedStop,
	}
	turns := []repo.ChatTurn{
		completed,
		{
			ID:         uuid.New(),
			TurnNumber: 2,
			CycleID:    &cycleID,
			Status:     "in_progress",
		},
	}
	if !projectBootstrapHasNewerLiveContinuationTurn(turns, completed) {
		t.Fatal("expected newer live continuation turn")
	}

	turns[1].Status = "failed"
	if projectBootstrapHasNewerLiveContinuationTurn(turns, completed) {
		t.Fatal("did not expect failed turn to count as live continuation")
	}
}

func TestMaxToolCallsContinuationDepthLimit(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.maxToolCalls = 1

	var dispatched int
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched++
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{
			{ID: uuid.NewString(), Name: "file.read", Tier: "tier1"},
			{ID: uuid.NewString(), Name: "task.list", Tier: "tier1"},
		}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 3 {
		t.Fatalf("continuation summary calls = %d, want 3", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) != 4 {
		t.Fatalf("turn count = %d, want 4 (original + 3 continuations)", len(fixture.chat.turnOrder))
	}
	if dispatched != 4 {
		t.Fatalf("dispatched tool calls = %d, want 4", dispatched)
	}
	lastTurn := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if lastTurn == nil || lastTurn.StopReason == nil || *lastTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("last turn stop_reason = %v, want %q", lastTurn.StopReason, stopReasonMaxToolCalls)
	}
}

func TestMaxToolCallsBudgetAccumulatesAcrossRounds(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.maxToolCalls = 3

	var (
		mu              sync.Mutex
		dispatchedTier1 []string
	)
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		mu.Lock()
		dispatchedTier1 = append(dispatchedTier1, call.ID)
		mu.Unlock()
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "a", Name: "file.read", Tier: "tier1"},
				{ID: "b", Name: "memory.query", Tier: "tier1"},
			}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "c", Name: "chat.search", Tier: "tier1"},
				{ID: "d", Name: "task.list", Tier: "tier1"},
			}}, nil
		default:
			return ModelResponse{Content: "final"}, nil
		}
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), dispatchedTier1...)
	mu.Unlock()
	sort.Strings(got)
	if len(got) != 3 {
		t.Fatalf("tier1 dispatches = %v, want 3 total tool calls", got)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("tier1 dispatch calls = %v, want [a b c]", got)
	}
	if !fixture.messages.containsContent("[Max tool calls reached. Turn ended.]") {
		t.Fatal("missing max tool calls stop system message")
	}
	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil || turn.StopReason == nil || *turn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("turn stop_reason = %v, want %q", turn.StopReason, stopReasonMaxToolCalls)
	}
}

func TestMaxDurationStopCondition(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.syncMaxDuration = 1 * time.Millisecond
	fixture.engine.now = func() time.Time { return base.Add(5 * time.Millisecond) }
	fixture.chat.startedAt = base

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{{ID: "x", Name: "file.read", Tier: "tier1"}}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if !fixture.messages.containsContent("[Turn duration limit reached. Turn ended.]") {
		t.Fatal("missing max duration stop system message")
	}
}

func TestDispatchToolsReverseMapsSanitizedToolNames(t *testing.T) {
	fixture := newUnitFixture(t, "sync")

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			// The model returns the API-safe name; dispatch must map it back.
			return ModelResponse{
				ToolCalls: []ModelToolCall{{ID: "tool-1", Name: "file_read", Arguments: map[string]any{"path": "/tmp/data"}}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if dispatched.Name != "file.read" {
		t.Fatalf("dispatched tool name = %q, want file.read", dispatched.Name)
	}
	if dispatched.Tier != "tier1" {
		t.Fatalf("dispatched tier = %q, want tier1", dispatched.Tier)
	}
	if got, _ := dispatched.Arguments["path"].(string); got != "/tmp/data" {
		t.Fatalf("dispatched argument path = %v, want /tmp/data", dispatched.Arguments["path"])
	}
}

func TestCancellationDuringStreaming(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	streamStarted := make(chan struct{})

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("partial"); err != nil {
			return ModelResponse{}, err
		}
		close(streamStarted)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	}()

	select {
	case <-streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream to start")
	}

	turnID := fixture.chat.waitForTurnID(t)
	payload, _ := json.Marshal(map[string]any{"session_id": fixture.session.ID.String(), "turn_id": turnID.String()})
	if err := fixture.events.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "human",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish cancel event: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleUserMessage returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HandleUserMessage")
	}

	if !fixture.messages.containsStatus("assistant", "failed") {
		t.Fatal("expected assistant message in failed status")
	}
	if !fixture.messages.containsContent("[Turn cancelled by user.]") {
		t.Fatal("missing cancellation system message")
	}
	if fixture.chat.cancelCalls == 0 {
		t.Fatal("expected CancelTurn to be called")
	}
}

func TestHandleTurnCancelledEventEnqueuesBootstrapRecovery(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.session.CurrentTurnID = nil

	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:           projectBootstrapStatusActive,
		InitialMessageID: fixture.userMessageID.String(),
		StartedAt:        &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata

	cancelledTurnID := uuid.New()
	fixture.chat.turns[cancelledTurnID] = &chat.ChatTurn{
		ID:           cancelledTurnID,
		SessionID:    fixture.session.ID,
		TurnNumber:   2,
		RespondingID: uuid.New(),
		Status:       "cancelled",
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, cancelledTurnID)

	payload, err := json.Marshal(map[string]any{
		"session_id": fixture.session.ID.String(),
		"turn_id":    cancelledTurnID.String(),
	})
	if err != nil {
		t.Fatalf("marshal cancel payload: %v", err)
	}
	event := eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "system",
		Payload:        payload,
	}

	if err := fixture.engine.HandleTurnCancelledEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleTurnCancelledEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("bootstrap recovery jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.SessionID != fixture.session.ID {
		t.Fatalf("recovery payload = %+v, want session %s", jobs[0].payload, fixture.session.ID)
	}
	if jobs[0].payload.MessageID == fixture.userMessageID {
		t.Fatal("expected recovery to target a fresh bootstrap continuation message")
	}
	if !fixture.messages.containsContent("[Recovered cancelled bootstrap turn - retrying in a fresh turn.]") {
		t.Fatal("missing bootstrap cancellation recovery system message")
	}
	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(messages) < 3 {
		t.Fatal("missing bootstrap continuation message append")
	}

	if err := fixture.engine.HandleTurnCancelledEvent(context.Background(), event); err != nil {
		t.Fatalf("second HandleTurnCancelledEvent: %v", err)
	}
	if got := len(fixture.enqueuer.agentTurnJobs()); got != 1 {
		t.Fatalf("bootstrap recovery jobs after duplicate event = %d, want 1", got)
	}
}

func TestHandleTurnCancelledEventSkipsBootstrapRecoveryWhenValidationAlreadyFailed(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.session.CurrentTurnID = nil

	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                  projectBootstrapStatusActive,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureMissingAssignments,
		ValidationFailureReason: "kickoff validation failed: planned tasks were created before any active project assignments were persisted",
		InitialMessageID:        fixture.userMessageID.String(),
		StartedAt:               &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	cancelledTurnID := uuid.New()
	fixture.chat.turns[cancelledTurnID] = &chat.ChatTurn{
		ID:           cancelledTurnID,
		SessionID:    fixture.session.ID,
		TurnNumber:   2,
		RespondingID: uuid.New(),
		Status:       "cancelled",
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, cancelledTurnID)

	payload, err := json.Marshal(map[string]any{
		"session_id": fixture.session.ID.String(),
		"turn_id":    cancelledTurnID.String(),
	})
	if err != nil {
		t.Fatalf("marshal cancel payload: %v", err)
	}
	event := eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "system",
		Payload:        payload,
	}

	if err := fixture.engine.HandleTurnCancelledEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleTurnCancelledEvent: %v", err)
	}
	if got := len(fixture.enqueuer.agentTurnJobs()); got != 0 {
		t.Fatalf("bootstrap recovery jobs = %d, want 0 when validation already failed", got)
	}
	if fixture.messages.containsContent("[Recovered cancelled bootstrap turn - retrying in a fresh turn.]") {
		t.Fatal("unexpected bootstrap cancellation recovery message")
	}
}

func TestProjectBootstrapCancelledRecoveryProgressPrefersRecoverableValidation(t *testing.T) {
	progress, recoverable := projectBootstrapCancelledRecoveryProgress(
		projectBootstrapState{
			ValidationStatus:        projectBootstrapValidationFailed,
			ValidationFailureClass:  projectBootstrapFailureCompoundParent,
			ValidationFailureReason: "kickoff validation failed: task 34 is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete",
		},
		projectBootstrapProgress{},
	)
	if !recoverable {
		t.Fatal("recoverable = false, want true")
	}
	if progress.ValidationFailureClass != projectBootstrapFailureCompoundParent {
		t.Fatalf("validation failure class = %q, want %q", progress.ValidationFailureClass, projectBootstrapFailureCompoundParent)
	}
	if !strings.Contains(progress.ValidationFailureReason, "task 34 is still a broad parent workstream") {
		t.Fatalf("validation failure reason = %q, want recoverable compound parent reason", progress.ValidationFailureReason)
	}
}

func TestProjectBootstrapCancelledRecoveryProgressRejectsNonRecoverableValidation(t *testing.T) {
	progress, recoverable := projectBootstrapCancelledRecoveryProgress(
		projectBootstrapState{
			ValidationStatus:        projectBootstrapValidationFailed,
			ValidationFailureClass:  projectBootstrapFailureMissingAssignments,
			ValidationFailureReason: "kickoff validation failed: planned tasks were created before any active project assignments were persisted",
		},
		projectBootstrapProgress{},
	)
	if recoverable {
		t.Fatal("recoverable = true, want false")
	}
	if progress.ValidationFailureClass != projectBootstrapFailureMissingAssignments {
		t.Fatalf("validation failure class = %q, want %q", progress.ValidationFailureClass, projectBootstrapFailureMissingAssignments)
	}
}

func TestProjectBootstrapAutoContinueMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	autoMetadata, err := json.Marshal(map[string]any{
		"source":        projectBootstrapSource,
		"auto_continue": true,
	})
	if err != nil {
		t.Fatalf("marshal auto metadata: %v", err)
	}
	autoMsg, err := fixture.chat.AppendMessage(context.Background(), chat.AppendMessageInput{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "Continue the bounded project bootstrap setup workflow now.",
		Metadata:  autoMetadata,
	})
	if err != nil {
		t.Fatalf("AppendMessage auto: %v", err)
	}
	if !fixture.engine.projectBootstrapAutoContinueMessage(context.Background(), autoMsg.ID) {
		t.Fatal("projectBootstrapAutoContinueMessage = false, want true")
	}

	plainMsg, err := fixture.chat.AppendMessage(context.Background(), chat.AppendMessageInput{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "continue bootstrap",
	})
	if err != nil {
		t.Fatalf("AppendMessage plain: %v", err)
	}
	if fixture.engine.projectBootstrapAutoContinueMessage(context.Background(), plainMsg.ID) {
		t.Fatal("projectBootstrapAutoContinueMessage = true, want false for plain user message")
	}
}

func TestCancellationDuringTier2DispatchRequestsRunCancel(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	runStarted := make(chan uuid.UUID, 1)
	fixture.dispatcher.tier2Fn = func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		runStarted <- runID
		<-ctx.Done()
		return ToolResult{}, ctx.Err()
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{{ID: "tier2", Name: "cli.execute", Tier: "tier2"}}}, nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	}()

	var runID uuid.UUID
	select {
	case runID = <-runStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tier2 run start")
	}
	turnID := fixture.chat.waitForTurnID(t)
	payload, _ := json.Marshal(map[string]any{"session_id": fixture.session.ID.String(), "turn_id": turnID.String()})
	_ = fixture.events.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "human",
		Payload:        payload,
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleUserMessage returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HandleUserMessage")
	}

	if len(fixture.runCanceler.calls) == 0 {
		t.Fatal("expected run cancel request")
	}
	if fixture.runCanceler.calls[0] != runID {
		t.Fatalf("cancelled run id = %s, want %s", fixture.runCanceler.calls[0], runID)
	}
}

func TestCancellationWatchReusesConsumerNameAcrossTurns(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage first turn: %v", err)
	}
	second := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "second message",
	})
	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, second.ID); err != nil {
		t.Fatalf("HandleUserMessage second turn: %v", err)
	}

	cancelSubs := fixture.events.subscriptionNamesWithPrefix("turn-engine.cancel")
	if len(cancelSubs) != 2 {
		t.Fatalf("cancel subscription count = %d, want 2", len(cancelSubs))
	}
	if cancelSubs[0] != cancelSubs[1] {
		t.Fatalf("cancel consumer name differs across turns: %q != %q", cancelSubs[0], cancelSubs[1])
	}
}

func TestReactionFeedbackNoLinkedMemories(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	assistant := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "assistant",
		Status:    "final",
	})
	turnID := fixture.chat.waitForTurnIDOptional()
	if turnID == uuid.Nil {
		turnID = uuid.New()
	}
	assistant.TurnID = &turnID
	fixture.messages.upsert(assistant)

	payload, _ := json.Marshal(map[string]any{"session_id": fixture.session.ID.String(), "message_id": assistant.ID.String(), "emoji": "👍"})
	err := fixture.engine.HandleReactionEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.reaction.added",
		ActorType:      "human",
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("HandleReactionEvent: %v", err)
	}
	if fixture.memories.updateCalls != 0 {
		t.Fatalf("memory update calls = %d, want 0", fixture.memories.updateCalls)
	}
}

func TestContinuationTurnOnContextCompressed(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	var secondAssembleHistoryStart *uuid.UUID
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondAssembleHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "summary"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 1 {
		t.Fatalf("continuation summary calls = %d, want 1", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want >= 2", len(fixture.chat.turnOrder))
	}
	first := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	second := fixture.chat.turnByID(fixture.chat.turnOrder[1])
	if first == nil || second == nil || first.CycleID == nil || second.CycleID == nil {
		t.Fatal("expected two turns with cycle ids")
	}
	if *first.CycleID != *second.CycleID {
		t.Fatalf("continuation cycle id mismatch: %s != %s", *first.CycleID, *second.CycleID)
	}
	if secondAssembleHistoryStart == nil || *secondAssembleHistoryStart == uuid.Nil {
		t.Fatal("second assemble HistoryStartID is nil, want continuation summary to become history root")
	}
}

func TestContinuationTurnCapsSummaryBeforeReusingAsHistoryRoot(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: strings.Repeat("very long summary line\n", 2000)}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if len(summaryMessage.Content) > 5000 {
		t.Fatalf("continuation summary length = %d, want <= 5000 chars", len(summaryMessage.Content))
	}
}

func TestContinuationTurnUsesCurrentTriggerMessageForOrganizationContinuationSummary(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}},
	}

	oldEscalation := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "@Frank escalation: wave 1 specs blocked in review",
	})
	currentRequest := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 18 with slug speaker-pipeline-ops-validation-fresh-20260324-rerun-18.",
	})
	fixture.userMessageID = currentRequest.ID

	var continuationHumanMessages []string
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			continuationHumanMessages = append([]string(nil), req.HumanMessages...)
			return ModelResponse{Content: "Continue with the current project creation request."}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, currentRequest.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if fixture.model.continuationSummaryCalls != 1 {
		t.Fatalf("continuation summary calls = %d, want 1", fixture.model.continuationSummaryCalls)
	}
	if len(continuationHumanMessages) != 1 {
		t.Fatalf("continuation HumanMessages = %#v, want only current trigger message", continuationHumanMessages)
	}
	if continuationHumanMessages[0] != currentRequest.Content {
		t.Fatalf("continuation HumanMessages[0] = %q, want %q", continuationHumanMessages[0], currentRequest.Content)
	}
	for _, message := range continuationHumanMessages {
		if strings.Contains(message, oldEscalation.Content) {
			t.Fatalf("continuation HumanMessages unexpectedly included stale prior request: %#v", continuationHumanMessages)
		}
	}
}

func TestContinuationTurnAppendsDirectActionPromptForAsyncOrganizationSession(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}},
	}

	currentRequest := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 20 with slug speaker-pipeline-ops-validation-fresh-20260324-rerun-20.",
	})
	fixture.userMessageID = currentRequest.ID

	var secondHistoryStart *uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "Create the new project and continue the bootstrap handoff."}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, currentRequest.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessageID uuid.UUID
	var actionPromptFound bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			summaryMessageID = msg.ID
		}
		if strings.Contains(msg.Content, "Continue the active organization request now from the continuation summary above.") {
			actionPromptFound = true
			if !strings.Contains(msg.Content, "Do not say that you are ready, ask what to do next, or ask the user what they need.") {
				t.Fatalf("organization continuation action prompt = %q, want anti-generic-chat guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "If the request is to create a project, your next step should be the concrete project creation and handoff action") {
				t.Fatalf("organization continuation action prompt = %q, want direct project-creation guidance", msg.Content)
			}
		}
	}
	if summaryMessageID == uuid.Nil {
		t.Fatal("continuation summary message missing")
	}
	if !actionPromptFound {
		t.Fatal("organization continuation action prompt missing")
	}
	if secondHistoryStart == nil || *secondHistoryStart != summaryMessageID {
		t.Fatalf("second assemble HistoryStartID = %v, want continuation summary %s", secondHistoryStart, summaryMessageID)
	}
}

func TestContinuationTurnUsesDeterministicActiveRequestSummaryForAsyncOrganizationSession(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}},
	}

	currentRequest := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 22 with slug speaker-pipeline-ops-validation-fresh-20260324-rerun-22.",
	})
	fixture.userMessageID = currentRequest.ID

	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			t.Fatalf("unexpected continuation_summary model call")
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, currentRequest.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if strings.Contains(message.Content, "[Continuation summary]") {
			copyMessage := message
			summaryMessage = &copyMessage
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Active organization request:") {
		t.Fatalf("continuation summary = %q, want deterministic active-request summary", summaryMessage.Content)
	}
	if strings.Contains(strings.ToLower(summaryMessage.Content), "project created") {
		t.Fatalf("continuation summary = %q, want no fabricated completed state", summaryMessage.Content)
	}
	if !strings.Contains(summaryMessage.Content, currentRequest.Content) {
		t.Fatalf("continuation summary = %q, want original request content", summaryMessage.Content)
	}
}

func TestContinuationTurnUsesPriorRealRequestWhenTriggeredBySyntheticOrganizationContinuationPrompt(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}},
	}

	realRequest := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 30 with slug speaker-pipeline-ops-validation-fresh-20260324-rerun-30.",
	})
	summaryMessage := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "system",
		Status:    "final",
		Content:   "[Continuation summary] Active organization request: Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 30 with slug speaker-pipeline-ops-validation-fresh-20260324-rerun-30.",
	})
	continuationPrompt := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Continue the active organization request now from the continuation summary above. Your next response must take direct action instead of generic chat.",
		Metadata:  json.RawMessage(`{"source":"organization_continuation_resume","synthetic_user_message":true}`),
	})
	fixture.userMessageID = continuationPrompt.ID
	_ = summaryMessage

	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			t.Fatalf("unexpected continuation_summary model call")
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, continuationPrompt.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var continuationSummary *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if strings.Contains(message.Content, "[Continuation summary]") && message.ID != summaryMessage.ID {
			copyMessage := message
			continuationSummary = &copyMessage
		}
	}
	if continuationSummary == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(continuationSummary.Content, realRequest.Content) {
		t.Fatalf("continuation summary = %q, want original request content", continuationSummary.Content)
	}
	if strings.Contains(continuationSummary.Content, continuationPrompt.Content) {
		t.Fatalf("continuation summary = %q, should not summarize synthetic continuation prompt", continuationSummary.Content)
	}
}

func TestContinuationTurnUsesDeterministicActiveRequestSummaryForDirectTaskRecoveryPrompt(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}},
	}

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				TaskNumber:      12,
				Title:           "V2: Validate First-Wave Task Execution & Flow Advancement",
				WorkStatus:      "blocked",
				AssignedAgentID: &fixture.chat.participants[0].ParticipantID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}

	currentRequest := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Use flow.recovery_decision now. Decision: retry. flow_node_execution_id: d4e1d332-7b6b-4a15-9f84-92807a59c7fb. Stay pinned to FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md.",
	})
	fixture.userMessageID = currentRequest.ID

	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			t.Fatalf("unexpected continuation_summary model call")
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, currentRequest.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if strings.Contains(message.Content, "[Continuation summary]") {
			copyMessage := message
			summaryMessage = &copyMessage
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Active task request:") {
		t.Fatalf("continuation summary = %q, want deterministic active task request summary", summaryMessage.Content)
	}
	if !strings.Contains(summaryMessage.Content, "flow.recovery_decision") {
		t.Fatalf("continuation summary = %q, want preserved recovery tool request", summaryMessage.Content)
	}
	if !strings.Contains(summaryMessage.Content, "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md") {
		t.Fatalf("continuation summary = %q, want preserved target path", summaryMessage.Content)
	}
}

func TestAsyncProjectTaskGuardrailContinuationDepthRequeuesFromSyntheticContinuationRoot(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, ProjectID: projectID, WorkStatus: "in_progress", AssignedAgentID: &assignedAgentID},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil {
		t.Fatal("expected queued agent_turn payload")
	}
	if jobs[0].runAfter == nil {
		t.Fatal("expected delayed continuation retry")
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var actionMessage *repo.ChatMessage
	var retryNoticeCount int
	for i := range messages {
		message := messages[i]
		if strings.Contains(message.Content, "retrying from a narrowed continuation root") {
			retryNoticeCount++
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Content), buildTaskContinuationActionPrompt("")) {
			continue
		}
		if taskContinuationResumeMessageRootsHistory(message) {
			copied := message
			actionMessage = &copied
		}
	}
	if retryNoticeCount != 1 {
		t.Fatalf("retry notice count = %d, want 1", retryNoticeCount)
	}
	if actionMessage == nil {
		t.Fatal("expected synthetic continuation root message")
	}
	if jobs[0].payload.MessageID != actionMessage.ID {
		t.Fatalf("queued message id = %s, want %s", jobs[0].payload.MessageID, actionMessage.ID)
	}
}

func TestTaskContinuationRootMessageStartsAssemblyAtTriggerMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, ProjectID: projectID, WorkStatus: "in_progress", AssignedAgentID: &assignedAgentID},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}

	rootMessage := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   buildTaskContinuationActionPrompt(""),
		Metadata:  taskContinuationResumeMessageMetadata(nil, 1),
	})

	var assembledHistoryStart *uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call != 1 || input.HistoryStartID == nil {
			return
		}
		copied := *input.HistoryStartID
		assembledHistoryStart = &copied
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, rootMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if assembledHistoryStart == nil {
		t.Fatal("HistoryStartID is nil, want continuation root message id")
	}
	if *assembledHistoryStart != rootMessage.ID {
		t.Fatalf("HistoryStartID = %s, want %s", *assembledHistoryStart, rootMessage.ID)
	}
}

func TestContinuationTurnNormalizesGenericNoContextSummary(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "I don't have a continuation summary or prior context about an active task. Please provide the task details and current progress."}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Continuation summary unavailable.") {
		t.Fatalf("continuation summary = %q, want normalized unavailable message", summaryMessage.Content)
	}
}

func TestContinuationTurnNormalizesMissingDurableDraftSummary(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "I don't see a durable draft for the OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md file in the context provided above. Please provide the substantive draft or recovery artifact for this task so I can write the target file directly."}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Continuation summary unavailable.") {
		t.Fatalf("continuation summary = %q, want normalized unavailable message", summaryMessage.Content)
	}
}

func TestContinuationTurnNormalizesMissingTaskSessionHistorySummary(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "I have reviewed the instructions, but no continuation summary or task session history was included in this message. I cannot determine what task to continue or what draft deliverable exists to revise."}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Continuation summary unavailable.") {
		t.Fatalf("continuation summary = %q, want normalized unavailable message", summaryMessage.Content)
	}
}

func TestContinuationTurnUsesTaskFallbackSummaryForAsyncProjectTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "I don't have a continuation summary or prior context about an active task. Please provide the task details and current progress."}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Task execution is already underway.") {
		t.Fatalf("continuation summary = %q, want actionable task fallback summary", summaryMessage.Content)
	}
}

func TestContinuationTurnUsesTaskFallbackSummaryForSupervisorContextQuestionnaire(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	rootMessage, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID root user message: %v", err)
	}
	rootMessage.Content = "supervisor recovery: resume task"
	rootMessage.Metadata = mustRawJSON(t, map[string]any{
		"source": "supervisor",
	})
	fixture.messages.upsert(rootMessage)
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
				WorkStatus:      "review",
			},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{{
			ProjectID: projectID,
			AgentID:   assignedID,
			IsActive:  true,
			Role:      "reviewer",
		}},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "# Task Resume\n\nI'm ready to continue. However, you've sent the resume command three times without providing context about what task needs to be resumed.\n\nPlease provide:\n1. Task Description\n2. Current Status\n3. Remaining Work"}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, taskExecutionContinuationFallbackSummary()) {
		t.Fatalf("continuation summary = %q, want task fallback summary", summaryMessage.Content)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuationSummaryCalls = %d, want 0 for supervisor recovery fallback", fixture.model.continuationSummaryCalls)
	}
}

func TestContinuationTurnAppendsDirectActionPromptForAsyncProjectTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	var secondHistoryStart *uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "Task is mid-flight and should continue from the existing workspace state."}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if fixture.model.continuationSummaryCalls != 1 {
		t.Fatalf("continuation summary calls = %d, want 1", fixture.model.continuationSummaryCalls)
	}
	if secondHistoryStart == nil || *secondHistoryStart == uuid.Nil {
		t.Fatal("second assemble HistoryStartID is nil, want continuation summary to remain history root")
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessageID uuid.UUID
	var actionPromptFound bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			summaryMessageID = msg.ID
		}
		if strings.Contains(msg.Content, "Continue the active task now from the continuation summary above.") {
			actionPromptFound = true
			if !strings.Contains(msg.Content, "Your next response must take direct action on the task instead of generic chat.") {
				t.Fatalf("action prompt = %q, want direct action guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not say that you are ready, ask what to do next, or ask the user what they need.") {
				t.Fatalf("action prompt = %q, want anti-generic-chat guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not say that you lack context or ask the user to restate the task when this continuation turn already includes the task session history and continuation summary.") {
				t.Fatalf("action prompt = %q, want anti-no-context guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not start with project.list, project.get, task.list, task.get, task_get, flow.list_templates, flow.get_execution, file.read, file_list, or agent.list unless a specific blocker names that exact record.") {
				t.Fatalf("action prompt = %q, want anti-reread guidance", msg.Content)
			}
		}
	}
	if summaryMessageID == uuid.Nil {
		t.Fatal("continuation summary message missing")
	}
	if !actionPromptFound {
		t.Fatal("task continuation action prompt missing")
	}
	if *secondHistoryStart != summaryMessageID {
		t.Fatalf("second assemble HistoryStartID = %s, want continuation summary %s", *secondHistoryStart, summaryMessageID)
	}
	turns, err := fixture.chat.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turn count = %d, want 2", len(turns))
	}
	last := turns[len(turns)-1]
	if last.TriggerMessageID == nil || *last.TriggerMessageID != summaryMessageID {
		t.Fatalf("continuation turn trigger_message_id = %v, want %s", last.TriggerMessageID, summaryMessageID)
	}
}

func TestContinuationTurnUsesPriorSubstantiveDraftForAsyncProjectTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	createCompletedTurnWithAssistantMessage(t, fixture, assignedID, strings.TrimSpace(`# Deliverable

## Section
Concrete draft body that should become the continuation root.
`))
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			t.Fatalf("unexpected continuation_summary model call")
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	var actionPrompt *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if strings.Contains(message.Content, "[Continuation summary]") {
			copyMessage := message
			summaryMessage = &copyMessage
		}
		if strings.Contains(message.Content, "Treat it as the working artifact draft for this turn.") {
			copyMessage := message
			actionPrompt = &copyMessage
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Concrete draft body that should become the continuation root.") {
		t.Fatalf("continuation summary = %q, want prior substantive draft", summaryMessage.Content)
	}
	if actionPrompt == nil {
		t.Fatal("expected task continuation action prompt with draft guidance")
	}
}

func TestContinuationTurnUsesPriorRecoveryArtifactDraftForAsyncProjectTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	priorTurnID := createCompletedTurnWithAssistantMessage(t, fixture, assignedID, "Now I'll write the file body.")
	appendToolResultMessage(t, fixture, priorTurnID, map[string]any{
		"tool_name": "file.read",
		"output": map[string]any{
			"path": recoveryArtifactDir + "/design-system/03-accessibility-standards.md",
			"content": strings.TrimSpace(`# Recovery file.write artifact

Task: OC-28
Target Path: design-system/03-accessibility-standards.md

## Draft Content

# Accessibility Standards

## Contrast Requirements
- Body copy must meet WCAG contrast thresholds.
`),
		},
	})
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			t.Fatalf("unexpected continuation_summary model call")
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for i := range messages {
		message := messages[i]
		if strings.Contains(message.Content, "[Continuation summary]") {
			copyMessage := message
			summaryMessage = &copyMessage
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "# Accessibility Standards") {
		t.Fatalf("continuation summary = %q, want prior recovery artifact draft", summaryMessage.Content)
	}
}

func TestBuildTaskContinuationActionPromptTreatsDocumentSummaryAsDraft(t *testing.T) {
	summary := "# Visual Direction\n\n- Kind: strategy_artifact\n\n## Design Principles\nConcrete draft body."

	prompt := buildTaskContinuationActionPrompt(summary)

	if !strings.Contains(prompt, "The continuation summary above already contains draft deliverable content. Treat it as the working artifact draft for this turn.") {
		t.Fatalf("prompt = %q, want draft-summary guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not reopen broad workspace context or search for more source material before using that draft.") {
		t.Fatalf("prompt = %q, want anti-reread draft guidance", prompt)
	}
	if !strings.Contains(prompt, "If a target file is in scope, revise the draft directly and write the file with concrete content instead of re-deriving the document from scratch.") {
		t.Fatalf("prompt = %q, want direct-write guidance", prompt)
	}
}

func TestBuildProjectContinuationActionPrompt(t *testing.T) {
	prompt := buildProjectContinuationActionPrompt("Project execution should continue directly from the current task tree.")

	if !strings.Contains(prompt, "Continue the active project execution now from the continuation summary above.") {
		t.Fatalf("prompt = %q, want project continuation lead-in", prompt)
	}
	if !strings.Contains(prompt, "Your next response must take direct project action instead of generic chat.") {
		t.Fatalf("prompt = %q, want direct project action guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not say that you are ready, ask what to do next, or ask the user what they need.") {
		t.Fatalf("prompt = %q, want anti-ready guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not say that you lack context or ask the user to restate the project when this continuation turn already includes the project session history and continuation summary.") {
		t.Fatalf("prompt = %q, want anti-restatement guidance", prompt)
	}
	if !strings.Contains(prompt, "Use the existing task tree, workspace state, planning artifacts, and recent tool results to continue execution directly.") {
		t.Fatalf("prompt = %q, want direct execution guidance", prompt)
	}
}

func TestWaitingBoundFlowExecutionRuntimeSubstateUsesReviewForReviewTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, WorkStatus: "review"},
		},
	}

	got := fixture.engine.waitingBoundFlowExecutionRuntimeSubstate(context.Background(), fixture.session)
	if got == nil || *got != "waiting_for_review" {
		t.Fatalf("waitingBoundFlowExecutionRuntimeSubstate = %v, want waiting_for_review", got)
	}
}

func TestWaitingBoundFlowExecutionRuntimeSubstateDefaultsToWaitingForTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, WorkStatus: "in_progress"},
		},
	}

	got := fixture.engine.waitingBoundFlowExecutionRuntimeSubstate(context.Background(), fixture.session)
	if got == nil || *got != "waiting_for_turn" {
		t.Fatalf("waitingBoundFlowExecutionRuntimeSubstate = %v, want waiting_for_turn", got)
	}
}

func TestBuildProjectExecutionContinuationPrompt(t *testing.T) {
	task := repo.ProjectTask{TaskNumber: 11, Title: "Document what persisted correctly"}

	prompt := buildProjectExecutionContinuationPrompt(task, 4)

	if !strings.Contains(prompt, "Continue the active project execution now.") {
		t.Fatalf("prompt = %q, want continuation lead-in", prompt)
	}
	if !strings.Contains(prompt, "The latest completed task was task 11 (Document what persisted correctly).") {
		t.Fatalf("prompt = %q, want completed task context", prompt)
	}
	if !strings.Contains(prompt, "There are 4 remaining draft project tasks") {
		t.Fatalf("prompt = %q, want remaining draft count guidance", prompt)
	}
	if !strings.Contains(prompt, "Your next response must take direct project action instead of generic chat.") {
		t.Fatalf("prompt = %q, want direct project action guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not treat a completed gate-review or sign-off task as proof that the whole project is complete") {
		t.Fatalf("prompt = %q, want sign-off completion guard", prompt)
	}
	if !strings.Contains(prompt, "Do not use task.update to mark untouched draft tasks done") {
		t.Fatalf("prompt = %q, want draft-task done guard", prompt)
	}
}

func TestProjectExecutionContinuationFallbackSummary(t *testing.T) {
	summary := projectExecutionContinuationFallbackSummary()

	if !strings.Contains(summary, "Project execution is already underway.") {
		t.Fatalf("summary = %q, want execution-underway guidance", summary)
	}
	if !strings.Contains(summary, "Reuse the existing project task tree, workspace artifacts, planning files, and recent tool results") {
		t.Fatalf("summary = %q, want concrete project-state reuse guidance", summary)
	}
}

func TestRecoveryTurnAppendsDirectActionPromptForAsyncProjectTaskWithoutCheckpoint(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	if _, err := fixture.messages.UpdateContent(context.Background(), fixture.userMessageID, "supervisor recovery: resume task"); err != nil {
		t.Fatalf("UpdateContent recovery kickoff: %v", err)
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var actionPromptFound bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "Continue the active task recovery now.") {
			actionPromptFound = true
			if !strings.Contains(msg.Content, "Your next response must take direct recovery action on the task instead of generic chat.") {
				t.Fatalf("action prompt = %q, want direct recovery guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not say that you lack context or ask the user to restate the task when this recovery turn already includes the task session history and recovery kickoff.") {
				t.Fatalf("action prompt = %q, want anti-no-context guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not start with project.list, project.get, task.list, task.get, task_get, flow.list_templates, flow.get_execution, file.read, file_list, or agent.list unless a specific blocker names that exact record.") {
				t.Fatalf("action prompt = %q, want anti-reread guidance", msg.Content)
			}
		}
	}
	if !actionPromptFound {
		t.Fatal("task recovery action prompt missing")
	}
}

func TestBuildRecoveryResumeActionPromptHardensIntentOnlyCheckpointWithoutDraft(t *testing.T) {
	t.Parallel()

	prompt := buildRecoveryResumeActionPrompt(recoveryResumeState{
		targetPath:    "docs/sitemap-and-navigation.md",
		blockerClass:  taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint,
		failureReason: "repeated intent-only recovery drafts for docs/sitemap-and-navigation.md across explicit resume attempts; latest assistant draft for docs/sitemap-and-navigation.md described intent to write the deliverable instead of the file body",
	})

	if !strings.Contains(prompt, "must begin with the concrete file body for docs/sitemap-and-navigation.md itself") {
		t.Fatalf("prompt = %q, want direct begin-with-file-body guidance", prompt)
	}
	if !strings.Contains(prompt, "The first non-whitespace character of your next assistant message must be the first character of the deliverable body itself.") {
		t.Fatalf("prompt = %q, want first-character guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not start with phrases like 'I', 'I'll', 'I will', 'Now I'll', 'Let me', 'Here is', or 'Below is' before the file body.") {
		t.Fatalf("prompt = %q, want anti-preface guidance", prompt)
	}
	if !strings.Contains(prompt, "No substantive durable draft is available.") {
		t.Fatalf("prompt = %q, want no-draft guidance", prompt)
	}
	if !strings.Contains(prompt, "If the target is Markdown, start immediately with a heading and real section content.") {
		t.Fatalf("prompt = %q, want markdown-start guidance", prompt)
	}
	if !strings.Contains(prompt, "already hardened after repeated non-substantive drafts") {
		t.Fatalf("prompt = %q, want repeated-draft hardening guidance", prompt)
	}
}

func TestBuildRecoveryResumeActionPromptUsesAvailableDraftDirectly(t *testing.T) {
	t.Parallel()

	prompt := buildRecoveryResumeActionPrompt(recoveryResumeState{
		targetPath:    "planning/prd-spec/oc-24-infrastructure-spec.md",
		artifactDraft: "# Infrastructure Specification\n\n## Hosting\nConcrete content.",
	})

	if !strings.Contains(prompt, "A substantive durable draft is already available above. Reuse that draft body directly") {
		t.Fatalf("prompt = %q, want substantive-draft guidance", prompt)
	}
	if !strings.Contains(prompt, "your next assistant message should begin with the first line of the best available draft") {
		t.Fatalf("prompt = %q, want first-line draft guidance", prompt)
	}
	if !strings.Contains(prompt, "Your entire next assistant message must be either the concrete file body for the target deliverable or one concrete blocker sentence.") {
		t.Fatalf("prompt = %q, want body-or-blocker guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not ask 'What do you need?', 'What would you like me to do?', or any equivalent recovery question.") {
		t.Fatalf("prompt = %q, want anti-recovery-question guidance", prompt)
	}
}

func TestBuildRecoveryResumeActionPromptUsesContinuationSummaryDraftDirectly(t *testing.T) {
	t.Parallel()

	prompt := buildRecoveryResumeActionPrompt(recoveryResumeState{
		targetPath:   "planning/prd-spec/oc-24-infrastructure-spec.md",
		summaryDraft: "# Infrastructure Specification\n\n## Hosting\nConcrete content.",
	})

	if !strings.Contains(prompt, "A substantive durable draft is already available above. Reuse that draft body directly") {
		t.Fatalf("prompt = %q, want substantive-draft guidance", prompt)
	}
	if !strings.Contains(prompt, "your next assistant message should begin with the first line of the best available draft") {
		t.Fatalf("prompt = %q, want first-line draft guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not browse .ottercamp/recovery broadly or read recovery artifacts for other tasks") {
		t.Fatalf("prompt = %q, want same-task recovery scope guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not reread strategy artifacts, planning files, or workspace listings before writing") {
		t.Fatalf("prompt = %q, want anti-reread draft guidance", prompt)
	}
	if !strings.Contains(prompt, "Your entire next assistant message must be either the concrete file body for the target deliverable or one concrete blocker sentence.") {
		t.Fatalf("prompt = %q, want body-or-blocker guidance", prompt)
	}
}

func TestLooksLikeRecoveryIntentNarrationPlaceholderDetectsNowIllWritePreface(t *testing.T) {
	t.Parallel()

	if !looksLikeRecoveryIntentNarrationPlaceholder("Now I'll write the substantive blog post template design specification:") {
		t.Fatal("expected now-I'll-write preface to be rejected as intent narration")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsContextSummaryReadyReply(t *testing.T) {
	t.Parallel()

	content := "I'm ready to help. I'm Alex, Technical Lead for the SAM.blog rebuild. Based on the context, I can see the target file and current blocker."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected context-summary ready reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsIllHelpStub(t *testing.T) {
	t.Parallel()

	if !looksLikeGenericTaskRecoveryReply("I'll help") {
		t.Fatal("expected I'll-help stub to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsFocusQuestionMenu(t *testing.T) {
	t.Parallel()

	content := "I'm Alex, Technical Lead for the SAM.blog rebuild. I'm ready to work on OC-24. " +
		"I have access to the planning artifacts for this task. What would you like me to focus on first? " +
		"Or is there a specific decision or constraint I should know about before I start?"
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected focus-question menu to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsMoveForwardStatusReply(t *testing.T) {
	t.Parallel()

	content := "I'm Alex, Technical Lead for the SAM.blog rebuild. I'm ready to help you move forward on OC-24: Plan Hosting and Infrastructure.\n\n" +
		"Current Status:\n" +
		"- Strategy phase is complete\n" +
		"- Task is in progress\n" +
		"- Next deliverable is the infrastructure spec"
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected move-forward status reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsDraftingConfirmationReply(t *testing.T) {
	t.Parallel()

	content := "I'm ready to continue on OC-24: Plan hosting and infrastructure. " +
		"Before I proceed with drafting the infrastructure spec, I need to confirm the locked decisions are still valid."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected drafting confirmation reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsKickoffReadyAssistReply(t *testing.T) {
	t.Parallel()

	content := "I'm ready to assist. I'm a Data Integration Tester specializing in end-to-end data flow validation for the Speaker Pipeline Ops project.\n\n**Current Task Context:**\n- **Task ID:** OC-14 (Test speaker list endpoint)\n- **Status:** Blocked (validation work abandoned)\n- **Target Deliverable:** test-result-list.log"
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected kickoff ready-assist reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsContinuationProceedQuestionReply(t *testing.T) {
	t.Parallel()

	content := "I'm ready to continue OC-13 (Analysis: Evaluate error handling and recovery completeness).\n\n" +
		"Current situation:\n" +
		"- Task is in Work phase\n" +
		"- Dependencies show OC-10 is complete\n\n" +
		"What I need to proceed:\n" +
		"1. Should I search the workspace for existing test execution logs?\n" +
		"2. Or do you want me to focus on a specific error scenario first?\n\n" +
		"Ready to proceed."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected continuation proceed-question reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsCrossTaskClarificationReply(t *testing.T) {
	t.Parallel()

	content := "I need to clarify which task to continue. The continuation summary references multiple tasks with different statuses:\n\n" +
		"1. OC-18 in_progress\n" +
		"2. OC-19 blocked\n" +
		"3. OC-20 blocked"
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected cross-task clarification reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsMissingContinuationSummaryReply(t *testing.T) {
	t.Parallel()

	content := "I don't see a continuation summary in your message. The context block shows task OC-18 (Design Intake Framework), but no continuation summary that contains task session history or draft deliverable content."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected missing-continuation-summary reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsNoSpecificRequestReply(t *testing.T) {
	t.Parallel()

	content := "I'm ready to assist. However, I don't see a specific question or request in your message."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected no-specific-request reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsConcreteSituationClarifier(t *testing.T) {
	t.Parallel()

	content := "I'm reviewing the continuation summary you provided, but I need to clarify the concrete situation before I continue."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected concrete-situation clarification reply to be treated as generic recovery output")
	}
}

func TestTaskExecutionKickoffMessageDetectsTaskQueueKickoff(t *testing.T) {
	t.Parallel()

	message := repo.ChatMessage{
		Role:    "user",
		Content: "Start work on task: Test speaker list endpoint",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                 "task_queue_processor",
			"flow_node_execution_id": uuid.NewString(),
		}),
	}
	if !taskExecutionKickoffMessage(message) {
		t.Fatal("expected task queue kickoff message to be treated as execution kickoff")
	}
}

func TestLooksLikeRecoveryQuestionEchoDetectsClarificationMenu(t *testing.T) {
	t.Parallel()

	content := "I'm ready to help with the SAM.blog rebuild infrastructure task (OC-24).\n\n" +
		"**Quick clarification:**\n" +
		"- Continue drafting the success narrative?\n" +
		"- Move to the decision log or tradeoff matrix?\n\n" +
		"Please let me know the most useful next step."
	if !looksLikeRecoveryQuestionEcho(content) {
		t.Fatal("expected recovery clarification menu to be treated as a question echo")
	}
}

func TestContinuationTurnUsesDeterministicBootstrapResumeState(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	pmID := uuid.New()
	workerAID := uuid.New()
	workerBID := uuid.New()
	reviewerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               "bootstrap_planning",
			"assignment_count":            6,
			"planned_task_count":          18,
			"planned_flow_template_count": 1,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata
	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:       {ID: pmID, DisplayName: "Sam.blog PM", AgentClass: "staff", AgentType: "pm"},
			workerAID:  {ID: workerAID, DisplayName: "André Kowalski"},
			workerBID:  {ID: workerBID, DisplayName: "Ananya Webb"},
			reviewerID: {ID: reviewerID, DisplayName: "Vivian Cho"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerAID, Role: "worker", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerBID, Role: "worker", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: reviewerID, Role: "reviewer", IsActive: true},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	var secondHistoryStart *uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 for active bootstrap continuation", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawResume bool
	var resumeContent string
	var resumeMessageID uuid.UUID
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			sawResume = true
			resumeContent = msg.Content
			resumeMessageID = msg.ID
			break
		}
	}
	if !sawResume {
		t.Fatal("project bootstrap resume message missing")
	}
	if !strings.Contains(resumeContent, "Existing PM: Sam.blog PM") {
		t.Fatalf("resume message = %q, want existing PM line", resumeContent)
	}
	if !strings.Contains(resumeContent, "id="+pmID.String()) || !strings.Contains(resumeContent, "class=staff") || !strings.Contains(resumeContent, "type=pm") {
		t.Fatalf("resume message = %q, want PM identity details", resumeContent)
	}
	if !strings.Contains(resumeContent, "Active project id: "+fixture.session.ScopeID.String()) {
		t.Fatalf("resume message = %q, want active project id line", resumeContent)
	}
	if !strings.Contains(resumeContent, "Existing active assignments:") ||
		!strings.Contains(resumeContent, "Vivian Cho (id=") ||
		!strings.Contains(resumeContent, "Ananya Webb (id=") ||
		!strings.Contains(resumeContent, "André Kowalski (id=") {
		t.Fatalf("resume message = %q, want assignment roster", resumeContent)
	}
	if !strings.Contains(resumeContent, "Do not create duplicate agents or another PM.") {
		t.Fatalf("resume message = %q, want duplicate-agent guardrail", resumeContent)
	}
	if !strings.Contains(resumeContent, "The persisted task tree already has runnable flow templates.") {
		t.Fatalf("resume message = %q, want first-wave promotion guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "The bootstrap governance gate task is system-managed") {
		t.Fatalf("resume message = %q, want governance gate guidance", resumeContent)
	}
	if secondHistoryStart == nil {
		t.Fatal("second assemble HistoryStartID is nil, want bootstrap continuation to root at resume message")
	}
	if resumeMessageID == uuid.Nil {
		t.Fatal("resume message id missing")
	}
	if *secondHistoryStart != resumeMessageID {
		t.Fatalf("second assemble HistoryStartID = %s, want resume message %s", *secondHistoryStart, resumeMessageID)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want at least 2 after continuation", len(fixture.chat.turnOrder))
	}
}

func TestBootstrapAutoContinueTurnAppendsResumeStateBeforeFirstModelCall(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               projectBootstrapCheckpointFirstWaveExecutions,
			"last_successful_checkpoint":  projectBootstrapCheckpointFirstWaveSelected,
			"assignment_count":            2,
			"planned_task_count":          9,
			"planned_flow_template_count": 1,
			"first_wave_task_count":       4,
			"validation_status":           projectBootstrapValidationPassed,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	messageMetadata, err := json.Marshal(map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("Marshal message metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), fixture.userMessageID, messageMetadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}

	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM", AgentClass: "staff", AgentType: "pm"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz", AgentClass: "staff", AgentType: "worker"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawResume bool
	var sawAction bool
	var actionRole string
	var actionStatus string
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			sawResume = true
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
		}
		if strings.Contains(msg.Content, "Continue the active project bootstrap from the persisted state above.") {
			sawAction = true
			actionRole = msg.Role
			actionStatus = msg.Status
		}
	}
	if !sawResume {
		t.Fatal("project bootstrap resume message missing for bootstrap auto-continue turn")
	}
	if !sawAction {
		t.Fatal("bootstrap resume action prompt missing for bootstrap auto-continue turn")
	}
	if actionRole != "system" {
		t.Fatalf("bootstrap resume action prompt role = %q, want system", actionRole)
	}
	if actionStatus != "final" {
		t.Fatalf("bootstrap resume action prompt status = %q, want final", actionStatus)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}
}

func TestBootstrapAutoContinueSanitizesInheritedNonBootstrapInitialMessageID(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	badRoot := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Continue the active project execution now.",
		Metadata:  json.RawMessage(`{"source":"project_execution_continuation","auto_continue":true}`),
	})

	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               projectBootstrapCheckpointFirstWaveExecutions,
			"last_successful_checkpoint":  projectBootstrapCheckpointFirstWaveSelected,
			"assignment_count":            2,
			"planned_task_count":          9,
			"planned_flow_template_count": 1,
			"first_wave_task_count":       4,
			"validation_status":           projectBootstrapValidationPassed,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	messageMetadata, err := json.Marshal(map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": badRoot.ID.String(),
	})
	if err != nil {
		t.Fatalf("Marshal message metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), fixture.userMessageID, messageMetadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}
	if _, err := fixture.messages.UpdateContent(context.Background(), fixture.userMessageID, "Continue the active project bootstrap from the persisted state above."); err != nil {
		t.Fatalf("UpdateContent user message: %v", err)
	}

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range messages {
		if !strings.Contains(msg.Content, "Continue the active project bootstrap from the persisted state above.") {
			continue
		}
		if msg.ID == fixture.userMessageID {
			continue
		}
		meta := messageMetadataMap(msg.Metadata)
		initialMessageID := strings.TrimSpace(stringValue(meta["bootstrap_initial_message_id"]))
		if initialMessageID == "" {
			t.Fatal("bootstrap action prompt missing bootstrap_initial_message_id")
		}
		if initialMessageID == badRoot.ID.String() {
			t.Fatalf("bootstrap_initial_message_id = %s, want bootstrap root %s instead of non-bootstrap source", initialMessageID, fixture.userMessageID)
		}
		if initialMessageID != fixture.userMessageID.String() {
			t.Fatalf("bootstrap_initial_message_id = %s, want bootstrap root %s", initialMessageID, fixture.userMessageID)
		}
		return
	}
	t.Fatal("bootstrap action prompt missing after sanitizing inherited initial message id")
}

func TestBuildProjectBootstrapResumeStateMessageUsesCompactRosterForLateFirstWaveState(t *testing.T) {
	state := projectBootstrapState{
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		AssignmentCount:          6,
		PlannedTaskCount:         18,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       8,
		FirstWavePromotedCount:   0,
		FirstWaveJobCount:        0,
		ValidationStatus:         projectBootstrapValidationPassed,
	}
	snapshot := projectBootstrapResumeSnapshot{
		ProjectID:      uuid.NewString(),
		ProjectSlug:    "sam-blog-compact",
		ExistingPM:     "Sam.blog PM (id=" + uuid.NewString() + ", class=staff, type=pm)",
		AssignmentLine: "workers=Ananya Webb (id=" + uuid.NewString() + "), André Kowalski (id=" + uuid.NewString() + ")",
	}

	resumeContent := buildProjectBootstrapResumeStateMessage(state, snapshot)
	if strings.Contains(resumeContent, "Existing active assignments:") {
		if !strings.Contains(resumeContent, "Existing active assignments: workers=Ananya Webb") {
			t.Fatalf("resume message = %q, want compact assignment roster summary", resumeContent)
		}
	}
	if !strings.Contains(resumeContent, "Existing staffing is already persisted for 6 active project assignments.") {
		t.Fatalf("resume message = %q, want compact staffing summary", resumeContent)
	}
	if !strings.Contains(resumeContent, "Do not create more agents, parent tasks, or broad child-task batches") {
		t.Fatalf("resume message = %q, want late first-wave guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "Reuse the existing active project assignees, including temp agents") {
		t.Fatalf("resume message = %q, want persisted-assignee reuse guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not spend tools re-reading scaffold planning artifacts or re-listing the full task tree and template catalog before acting") {
		t.Fatalf("resume message = %q, want anti-reread guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "Prefer direct task assignment, first-wave selection, and bootstrap.setup.persist") {
		t.Fatalf("resume message = %q, want direct action guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "keep the bootstrap governance gate task untouched") {
		t.Fatalf("resume message = %q, want compact governance guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "bootstrap.setup.persist tool") {
		t.Fatalf("resume message = %q, want setup persist tool guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "not raw task.update status changes") {
		t.Fatalf("resume message = %q, want raw task.update warning", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not manually queue or start first-wave execution tasks") {
		t.Fatalf("resume message = %q, want no manual first-wave promotion guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not call task.list or flow.list_templates before trying bootstrap.setup.persist") {
		t.Fatalf("resume message = %q, want setup-persist-first guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist") {
		t.Fatalf("resume message = %q, want repo-binding anti-drift guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "completed_step_slugs for bind-repo-environment, staff-project, decompose-workstreams, validate-task-shape, attach-validate-flow-templates, select-first-wave, and record-frank-sign-off") {
		t.Fatalf("resume message = %q, want canonical bootstrap step slugs", resumeContent)
	}
	if !strings.Contains(resumeContent, "include sign_off_summary when recording Frank approval") {
		t.Fatalf("resume message = %q, want Frank sign-off guidance", resumeContent)
	}
}

func TestProjectBootstrapResumeNeedsSetupPersist(t *testing.T) {
	state := projectBootstrapState{
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		FirstWaveTaskCount:       4,
		ValidationStatus:         projectBootstrapValidationPassed,
	}
	if !projectBootstrapResumeNeedsSetupPersist(state) {
		t.Fatal("resume state should require setup persist guidance when validation passed but bootstrap gate is still outstanding")
	}

	state.FirstWavePromotedCount = 1
	if projectBootstrapResumeNeedsSetupPersist(state) {
		t.Fatal("resume state should not require setup persist guidance after first-wave promotion has started")
	}
}

func TestBuildProjectBootstrapResumeActionPromptForSetupPersist(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		FirstWaveTaskCount:       3,
		ValidationStatus:         projectBootstrapValidationPassed,
	}, projectBootstrapResumeSnapshot{})
	if !strings.Contains(prompt, "Do not call task.list, flow.list_templates, or file.read on scaffold planning artifacts") {
		t.Fatalf("prompt = %q, want tool-specific anti-reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Your first tool call in this resume turn should be bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "call bootstrap.setup.persist immediately with completed_step_slugs for staff-project, decompose-workstreams, and validate-task-shape") {
		t.Fatalf("prompt = %q, want early completed-step guidance", prompt)
	}
	if !strings.Contains(prompt, "finish first-wave selection, and then call bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want setup persist sequencing", prompt)
	}
	if !strings.Contains(prompt, "Before any task.list or flow.list_templates call, try bootstrap.setup.persist first") {
		t.Fatalf("prompt = %q, want setup-persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist") {
		t.Fatalf("prompt = %q, want repo-binding anti-drift guidance", prompt)
	}
	if !strings.Contains(prompt, "do not use raw task.update to queue or start first-wave execution tasks") {
		t.Fatalf("prompt = %q, want no manual first-wave queue guidance", prompt)
	}
	if !strings.Contains(prompt, "do not edit, assign, queue, or complete the gate task manually") {
		t.Fatalf("prompt = %q, want governance gate protection", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForValidationFailure(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFlowTemplatesPersisted,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason: "kickoff validation failed: first-wave task 35 (List blog personality traits) violates the bounded task-size policy: split it into smaller reviewable tasks before queueing",
	}, projectBootstrapResumeSnapshot{})
	if !strings.Contains(prompt, "Bootstrap is currently blocked on this validation failure: kickoff validation failed: first-wave task 35") {
		t.Fatalf("prompt = %q, want concrete validation failure", prompt)
	}
	if !strings.Contains(prompt, "Do not start with bootstrap.setup.persist on this turn") {
		t.Fatalf("prompt = %q, want no-persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "split that exact persisted task into narrower executable child tasks") {
		t.Fatalf("prompt = %q, want exact-task split guidance", prompt)
	}
	if !strings.Contains(prompt, "Only after the named blocker is repaired should you call bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want repair-before-persist guidance", prompt)
	}
	if !strings.Contains(prompt, "Current phase names like first_wave_executions_created are not valid completed_step_slugs") {
		t.Fatalf("prompt = %q, want canonical step slug guidance", prompt)
	}
	if !strings.Contains(prompt, "do not use raw task.update to force draft first-wave tasks into queued or in_progress") {
		t.Fatalf("prompt = %q, want no-manual-promotion guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForUnresolvedFailedTaskStartsWithPersist(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFlowTemplatesPersisted,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureCompoundParent,
		ValidationFailureReason: "kickoff validation failed: task 20 (Define cross-field validation rules) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete",
	}, projectBootstrapResumeSnapshot{
		RepairTaskLine: buildProjectBootstrapUnresolvedFailureRepairTaskLine(20, "Define cross-field validation rules"),
	})
	if !strings.Contains(prompt, "The named blocker no longer resolves to one exact persisted task id. Start with bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want unresolved-task persist-first guidance", prompt)
	}
	if strings.Contains(prompt, "Do not start with bootstrap.setup.persist on this turn unless you have already repaired the named blocker") {
		t.Fatalf("prompt = %q, want no conflicting no-persist guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call project.get, project.list, task.list, flow.list_templates, agent.list, or scaffold file reads before that bootstrap.setup.persist call") {
		t.Fatalf("prompt = %q, want no-reread-before-persist guidance", prompt)
	}
	if !strings.Contains(prompt, "If that bootstrap.setup.persist call returns a newly resolved exact task id, repair that one task directly on the next step") {
		t.Fatalf("prompt = %q, want resolved-id follow-up guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForPartialFirstWaveMaterialization(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveExecutions,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: only 12 of 20 selected first-wave child tasks created flow_node_execution rows, so bootstrap never materialized the full runnable child wave",
	}, projectBootstrapResumeSnapshot{
		SelectedFirstWaveLine: "Currently selected first-wave tasks: task 10 id=abc title=\"Build report generator\" assigned_agent_id=worker-1; task 11 id=def title=\"Write dashboard spec\" assigned_agent_id=worker-2",
	})
	if !strings.Contains(prompt, "selected first wave is too large or too broad to materialize cleanly in one pass") {
		t.Fatalf("prompt = %q, want first-wave narrowing guidance", prompt)
	}
	if !strings.Contains(prompt, "Reduce the first wave to a smaller bounded subset of the already-created child tasks") {
		t.Fatalf("prompt = %q, want smaller first-wave guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not start that repair with project.list, project.get, task.list, flow.list_templates, flow.get_execution, file.read, file.write, agent.list, or staffing discovery") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not rewrite planning artifacts or restaff the project") {
		t.Fatalf("prompt = %q, want no planning rewrite guidance", prompt)
	}
	if !strings.Contains(prompt, "repair the runnable subset with direct task and flow mutations") {
		t.Fatalf("prompt = %q, want direct repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Use the currently selected first-wave task ids already listed") {
		t.Fatalf("prompt = %q, want selected-wave guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForExplicitFirstWaveSelection(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: buildProjectBootstrapExplicitFirstWaveSelectionFailureReason(),
	}, projectBootstrapResumeSnapshot{
		SelectableFirstWaveLine: "Selectable first-wave tasks: task 10 id=abc title=\"Build report generator\" assigned_agent_id=worker-1; task 11 id=def title=\"Write dashboard spec\" assigned_agent_id=worker-2",
	})
	if !strings.Contains(prompt, "Start with bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "first_wave_task_ids or first_wave_task_numbers") {
		t.Fatalf("prompt = %q, want explicit first-wave selection guidance", prompt)
	}
	if strings.Contains(prompt, "Do not start with bootstrap.setup.persist on this turn unless you have already repaired the named blocker") {
		t.Fatalf("prompt = %q, want no conflicting no-persist guidance", prompt)
	}
	if !strings.Contains(prompt, "Use the selectable task ids already listed") {
		t.Fatalf("prompt = %q, want selectable-task guidance", prompt)
	}
}

func TestProjectBootstrapResumeNeedsExplicitFirstWaveSelectionWhenPhasePending(t *testing.T) {
	state := projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveSelected,
		PlannedTaskCount:        12,
		FirstWaveTaskCount:      0,
		FirstWavePromotedCount:  0,
		FirstWaveExecutionCount: 0,
		FirstWaveJobCount:       0,
	}
	if !projectBootstrapResumeNeedsExplicitFirstWaveSelection(state) {
		t.Fatal("expected first-wave-selected phase with zero selected/promoted/executed work to require explicit first-wave selection guidance")
	}
}

func TestBuildProjectBootstrapResumeActionPromptForPendingFirstWaveSelectionWithoutFailure(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveSelected,
		PlannedTaskCount:        12,
		FirstWaveTaskCount:      0,
		FirstWavePromotedCount:  0,
		FirstWaveExecutionCount: 0,
		FirstWaveJobCount:       0,
	}, projectBootstrapResumeSnapshot{
		SelectableFirstWaveLine: "Selectable first-wave tasks: task 14 id=abc title=\"Build report generator\" assigned_agent_id=worker-1; task 15 id=def title=\"Write dashboard spec\" assigned_agent_id=worker-2",
	})
	if !strings.Contains(prompt, "Bootstrap is still waiting on an explicit selected first-wave subset.") {
		t.Fatalf("prompt = %q, want explicit first-wave pending guidance", prompt)
	}
	if !strings.Contains(prompt, "Your first tool call in this resume turn should be bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "Use the selectable task ids already listed") {
		t.Fatalf("prompt = %q, want selectable-task guidance", prompt)
	}
	if strings.Contains(prompt, "Do not start with bootstrap.setup.persist on this turn unless you have already repaired the named blocker") {
		t.Fatalf("prompt = %q, want no conflicting blocker-repair guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForUnassignedFirstWaveTask(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveExecutions,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	}, projectBootstrapResumeSnapshot{})
	if !strings.Contains(prompt, "already names the exact unassigned first-wave task") {
		t.Fatalf("prompt = %q, want exact-task repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not start with project.get, task.list, task.children, flow.list_templates, or agent.list") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get with the bare task number from the validation error") {
		t.Fatalf("prompt = %q, want direct task id guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForUnassignedWaveParent(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveExecutions,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 11 (Wave 3: Polish, Deploy & Content) has no assigned agent",
	}, projectBootstrapResumeSnapshot{})
	if !strings.Contains(prompt, "If the named task is a broad wave/workstream parent, do not read planning artifacts first") {
		t.Fatalf("prompt = %q, want no planning reread guidance", prompt)
	}
	if !strings.Contains(prompt, "create bounded executable child tasks directly beneath it") {
		t.Fatalf("prompt = %q, want child-task guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeStateMessageIncludesValidationFailure(t *testing.T) {
	message := buildProjectBootstrapResumeStateMessage(projectBootstrapState{
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 35 (List blog personality traits) violates the bounded task-size policy",
	}, projectBootstrapResumeSnapshot{
		ProjectID:   uuid.NewString(),
		ProjectSlug: "sam-blog-test",
	})
	if !strings.Contains(message, "Current validation failure: kickoff validation failed: first-wave task 35") {
		t.Fatalf("message = %q, want validation failure line", message)
	}
}

func TestBuildProjectBootstrapResumeStateMessageIncludesSelectableFirstWaveTasks(t *testing.T) {
	message := buildProjectBootstrapResumeStateMessage(projectBootstrapState{
		CurrentPhase:             projectBootstrapCheckpointFirstWaveSelected,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFlowTemplatesPersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  buildProjectBootstrapExplicitFirstWaveSelectionFailureReason(),
	}, projectBootstrapResumeSnapshot{
		ProjectID:               uuid.NewString(),
		ProjectSlug:             "speaker-pipeline-ops-validation-fresh",
		SelectableFirstWaveLine: "Selectable first-wave tasks: task 10 id=abc title=\"Build report generator\" assigned_agent_id=worker-1; task 11 id=def title=\"Write dashboard spec\" assigned_agent_id=worker-2",
	})
	if !strings.Contains(message, "Selectable first-wave tasks: task 10 id=abc") {
		t.Fatalf("message = %q, want selectable first-wave line", message)
	}
}

func TestBuildProjectBootstrapResumeStateMessageIncludesSelectedFirstWaveTasks(t *testing.T) {
	message := buildProjectBootstrapResumeStateMessage(projectBootstrapState{
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  "kickoff validation failed: only 11 of 12 selected first-wave child tasks produced runnable agent_turn jobs, so bootstrap never claimed the full runnable child wave",
	}, projectBootstrapResumeSnapshot{
		ProjectID:             uuid.NewString(),
		ProjectSlug:           "speaker-pipeline-ops-validation-fresh",
		SelectedFirstWaveLine: "Currently selected first-wave tasks: task 10 id=abc title=\"Build report generator\" assigned_agent_id=worker-1; task 11 id=def title=\"Write dashboard spec\" assigned_agent_id=worker-2",
	})
	if !strings.Contains(message, "Currently selected first-wave tasks: task 10 id=abc") {
		t.Fatalf("message = %q, want selected first-wave line", message)
	}
}

func TestBuildProjectBootstrapResumeStateMessageIncludesNamedBlockedTask(t *testing.T) {
	message := buildProjectBootstrapResumeStateMessage(projectBootstrapState{
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	}, projectBootstrapResumeSnapshot{
		ProjectID:      uuid.NewString(),
		ProjectSlug:    "sam-blog-test",
		FailedTaskLine: "Named blocked task: task 19 id=1234 title=\"Draft homepage hero\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
	})
	if !strings.Contains(message, "Named blocked task: task 19 id=1234") {
		t.Fatalf("message = %q, want blocked task line", message)
	}
	if !strings.Contains(message, "Use task.update directly on this task id instead of task.get with the bare task number") {
		t.Fatalf("message = %q, want direct update guidance", message)
	}
}

func TestBuildProjectBootstrapResumeStateMessageRejectsChecklistOnlyCompletion(t *testing.T) {
	message := buildProjectBootstrapResumeStateMessage(projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		CurrentPhase:             projectBootstrapCheckpointStaffingPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointProjectCreated,
	}, projectBootstrapResumeSnapshot{
		ProjectID:   uuid.NewString(),
		ProjectSlug: "speaker-pipeline-test",
	})
	if !strings.Contains(message, "Bootstrap is not complete just because the governance gate or checklist tasks are marked done.") {
		t.Fatalf("message = %q, want checklist-only completion warning", message)
	}
	if !strings.Contains(message, "task 1-8 alone as a finished bootstrap") {
		t.Fatalf("message = %q, want explicit checklist-only warning", message)
	}
}

func TestProjectBootstrapFailureTaskNumber(t *testing.T) {
	if got := projectBootstrapFailureTaskNumber("kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent"); got != 19 {
		t.Fatalf("task number = %d, want 19", got)
	}
	if got := projectBootstrapFailureTaskNumber("kickoff validation failed: task 21 (Build the individual blog post template) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete"); got != 21 {
		t.Fatalf("task number = %d, want 21", got)
	}
	if got := projectBootstrapFailureTaskNumber("kickoff validation failed: bootstrap setup persisted staffing but did not emit any executable tasks"); got != 0 {
		t.Fatalf("task number = %d, want 0", got)
	}
}

func TestProjectBootstrapFailureTaskTitlePreservesParentheticalTaskTitle(t *testing.T) {
	reason := "kickoff validation failed: task 15 (Document scope boundaries: in-scope (routing decisions, load balancing, speaker assignment, error recovery), out-of-scope (UI design, external integrations)) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete"
	if got := projectBootstrapFailureTaskTitle(reason); got != "Document scope boundaries: in-scope (routing decisions, load balancing, speaker assignment, error recovery), out-of-scope (UI design, external integrations)" {
		t.Fatalf("task title = %q", got)
	}
}

func TestProjectBootstrapRecoveryTargetFromMessage(t *testing.T) {
	message := "Continue the bounded project bootstrap setup workflow now. This is automatic follow-on bootstrap turn 3. Recovery target: kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent, so bootstrap cannot queue runnable execution. Do not repeat the same oversized task definitions."
	if got := projectBootstrapRecoveryTargetFromMessage(message); got != "kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent, so bootstrap cannot queue runnable execution." {
		t.Fatalf("recovery target = %q", got)
	}
	if got := projectBootstrapRecoveryTargetFromMessage("Continue bootstrap."); got != "" {
		t.Fatalf("recovery target = %q, want empty", got)
	}
}

func TestProjectBootstrapBlockedRecoveryFailureFallsBackToRecentRecoveryMessage(t *testing.T) {
	reason, class := projectBootstrapBlockedRecoveryFailure([]repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution. Do not stop at acknowledgement."},
		{Role: "assistant", Content: "I'll first reread the state."},
	}, projectBootstrapState{})
	if reason != "kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution." {
		t.Fatalf("reason = %q", reason)
	}
	if class != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("class = %q, want %q", class, projectBootstrapFailureFirstWaveExecution)
	}
}

func TestProjectBootstrapBlockedRecoveryFailureFallsBackToBootstrapPersistSelectionError(t *testing.T) {
	reason, class := projectBootstrapBlockedRecoveryFailure([]repo.ChatMessage{
		{Role: "tool_result", Content: `{"tool_name":"bootstrap.setup.persist","output":{"error":"first_wave_task_selection_required","message":"When persisting select-first-wave with multiple executable project tasks, include the exact selected first-wave tasks via first_wave_task_ids or first_wave_task_numbers so later-wave work stays draft."}}`},
	}, projectBootstrapState{})
	if reason != buildProjectBootstrapExplicitFirstWaveSelectionFailureReason() {
		t.Fatalf("reason = %q", reason)
	}
	if class != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("class = %q, want %q", class, projectBootstrapFailureFirstWaveExecution)
	}
}

func TestBuildBootstrapFirstWaveSelectionInstruction(t *testing.T) {
	instruction, ok := buildBootstrapFirstWaveSelectionInstruction(ToolResult{
		Name: "bootstrap.setup.persist",
		Output: map[string]any{
			"remaining_step_slugs": []any{"select-first-wave", "record-frank-sign-off"},
			"selectable_first_wave_tasks": []any{
				map[string]any{"task_id": "task-1", "task_number": 9, "title": "Validate API endpoint specs"},
				map[string]any{"task_id": "task-2", "task_number": 10, "title": "Test invalid input validation"},
			},
		},
	})
	if !ok {
		t.Fatal("expected first-wave instruction to be generated")
	}
	if !strings.Contains(instruction, "select-first-wave") {
		t.Fatalf("instruction = %q, want select-first-wave guidance", instruction)
	}
	if !strings.Contains(instruction, "task 9 id=task-1") {
		t.Fatalf("instruction = %q, want selectable task ids", instruction)
	}
}

func TestProjectBootstrapAckOnlyRecoveryReply(t *testing.T) {
	if !projectBootstrapAckOnlyReply(&repo.ChatMessage{Role: "assistant", Content: "Acknowledged."}) {
		t.Fatal("expected bare acknowledgement to be detected")
	}
	if projectBootstrapAckOnlyReply(&repo.ChatMessage{Role: "assistant", Content: "Acknowledged. I will assign the task now."}) {
		t.Fatal("did not expect substantive acknowledgement to be treated as ack-only")
	}
	if !projectBootstrapAckOnlyRecoveryReply([]repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution."},
	}, &repo.ChatMessage{Role: "assistant", Content: "Acknowledged."}) {
		t.Fatal("expected ack-only recovery reply to be detected")
	}
	if projectBootstrapAckOnlyRecoveryReply([]repo.ChatMessage{
		{Role: "user", Content: "Continue bootstrap."},
	}, &repo.ChatMessage{Role: "assistant", Content: "Acknowledged."}) {
		t.Fatal("did not expect non-recovery ack to be detected")
	}
	if projectBootstrapAckOnlyRecoveryReply([]repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution."},
	}, &repo.ChatMessage{Role: "assistant", Content: "Acknowledged. I will assign the task now."}) {
		t.Fatal("did not expect substantive reply to be treated as ack-only")
	}
}

func TestProjectBootstrapNarrativeOnlyRecoveryReply(t *testing.T) {
	messages := []repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution."},
	}
	if !projectBootstrapNarrativeOnlyRecoveryReply(messages, &repo.ChatMessage{Role: "assistant", Content: "I recall prior infrastructure details from memory."}) {
		t.Fatal("expected memory-only recovery reply to be detected")
	}
	if projectBootstrapNarrativeOnlyRecoveryReply(messages, &repo.ChatMessage{Role: "assistant", Content: "I cannot continue because the repo credential is missing."}) {
		t.Fatal("did not expect a concrete blocker to be treated as narrative-only")
	}
	if projectBootstrapNarrativeOnlyRecoveryReply(append(messages, repo.ChatMessage{Role: "tool", Content: "{}"}), &repo.ChatMessage{Role: "assistant", Content: "I recall prior infrastructure details from memory."}) {
		t.Fatal("did not expect tool-backed recovery reply to be treated as narrative-only")
	}
}

func TestBuildProjectBootstrapAckOnlyRecoveryFailureReason(t *testing.T) {
	got := buildProjectBootstrapAckOnlyRecoveryFailureReason("kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent")
	if !strings.Contains(got, "acknowledgement only") {
		t.Fatalf("expected acknowledgement-only wording, got %q", got)
	}
	if !strings.Contains(got, "first-wave task 11 (Site Build) has no assigned agent") {
		t.Fatalf("expected target in failure reason, got %q", got)
	}
}

func TestBuildProjectBootstrapAckOnlyRestartFailureReason(t *testing.T) {
	got := buildProjectBootstrapAckOnlyRestartFailureReason()
	if !strings.Contains(got, "automatic bootstrap restart") {
		t.Fatalf("expected restart wording, got %q", got)
	}
	if !strings.Contains(got, "acknowledgement only") {
		t.Fatalf("expected acknowledgement-only wording, got %q", got)
	}
}

func TestBuildProjectBootstrapNarrativeOnlyRecoveryFailureReason(t *testing.T) {
	got := buildProjectBootstrapNarrativeOnlyRecoveryFailureReason(
		"kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent",
		&repo.ChatMessage{Role: "assistant", Content: "I recall prior infrastructure details from memory."},
	)
	if !strings.Contains(got, "narrative only") {
		t.Fatalf("expected narrative-only wording, got %q", got)
	}
	if !strings.Contains(got, "I recall prior infrastructure details from memory.") {
		t.Fatalf("expected reply content in failure reason, got %q", got)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForUnassignedTaskRequiresImmediateToolAction(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution",
	}
	got := buildProjectBootstrapResumeActionPrompt(state, projectBootstrapResumeSnapshot{})
	if !strings.Contains(got, "Your next assistant action should be a tool call, not a narrative reply.") {
		t.Fatalf("expected immediate tool action guidance, got %q", got)
	}
	if !strings.Contains(got, "call task.update on that task now") {
		t.Fatalf("expected direct task.update guidance, got %q", got)
	}
}

func TestBuildProjectBootstrapResumeActionPromptRejectsChecklistOnlyCompletion(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		CurrentPhase:             projectBootstrapCheckpointStaffingPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointProjectCreated,
	}
	got := buildProjectBootstrapResumeActionPrompt(state, projectBootstrapResumeSnapshot{})
	if !strings.Contains(got, "Do not claim bootstrap is complete just because the governance gate or checklist tasks are done.") {
		t.Fatalf("expected checklist-only completion rejection, got %q", got)
	}
	if !strings.Contains(got, "Your next action must create real project assignments, non-bootstrap tasks, and runnable flow templates") {
		t.Fatalf("expected staffed-work creation guidance, got %q", got)
	}
}

func TestBuildProjectBootstrapResumeStateMessageRequiresDirectRepairAction(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		AssignmentCount:          3,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution",
	}
	snapshot := projectBootstrapResumeSnapshot{
		ProjectID:      uuid.New().String(),
		ProjectSlug:    "sam-blog-test",
		FailedTaskLine: "Named blocked task: task 11 id=abc title=\"Site Build\" work_status=draft assigned_agent_id=unassigned.",
		AssignmentLine: "workers=Dev (id=worker-1), Rina (id=worker-2)",
	}
	got := buildProjectBootstrapResumeStateMessage(state, snapshot)
	if !strings.Contains(got, "The next acceptable bootstrap action is a direct task.update") {
		t.Fatalf("expected direct repair contract, got %q", got)
	}
}

func TestLoadProjectBootstrapResumeSnapshotIncludesBroadParentBlockedTaskLine(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	taskID := uuid.New()
	workerID := uuid.New()
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: workerID, Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			workerID: {ID: workerID, DisplayName: "Dev"},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				ProjectID:  projectID,
				TaskNumber: 21,
				Title:      "Build the individual blog post template",
				WorkStatus: "draft",
			},
		},
	}

	snapshot, err := fixture.engine.loadProjectBootstrapResumeSnapshot(context.Background(), projectID, projectBootstrapState{
		ValidationFailureReason: "kickoff validation failed: task 21 (Build the individual blog post template) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete",
	})
	if err != nil {
		t.Fatalf("loadProjectBootstrapResumeSnapshot: %v", err)
	}
	if !strings.Contains(snapshot.FailedTaskLine, "Named blocked task: task 21 id="+taskID.String()) {
		t.Fatalf("FailedTaskLine = %q, want task id", snapshot.FailedTaskLine)
	}
}

func TestLoadProjectBootstrapResumeSnapshotWarnsWhenBlockedTaskIDCannotBeResolved(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	workerID := uuid.New()
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: workerID, Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			workerID: {ID: workerID, DisplayName: "Dev"},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			uuid.New(): {
				ID:         uuid.New(),
				ProjectID:  projectID,
				TaskNumber: 92,
				Title:      "Draft first blog post from editorial calendar topic",
				WorkStatus: "draft",
			},
		},
	}

	snapshot, err := fixture.engine.loadProjectBootstrapResumeSnapshot(context.Background(), projectID, projectBootstrapState{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 28 (Use a topic from the editorial calendar) has no assigned agent",
	})
	if err != nil {
		t.Fatalf("loadProjectBootstrapResumeSnapshot: %v", err)
	}
	if snapshot.FailedTaskLine != "" {
		t.Fatalf("FailedTaskLine = %q, want empty when stale task id cannot be resolved", snapshot.FailedTaskLine)
	}
	if !strings.Contains(snapshot.RepairTaskLine, "Do not fabricate a UUID from the bare task number.") {
		t.Fatalf("RepairTaskLine = %q, want anti-fabrication guidance", snapshot.RepairTaskLine)
	}
	if !strings.Contains(snapshot.RepairTaskLine, "call bootstrap.setup.persist with canonical completed_step_slugs") {
		t.Fatalf("RepairTaskLine = %q, want setup.persist fallback guidance", snapshot.RepairTaskLine)
	}
}

func TestProjectBootstrapRestartScaffoldFailureReasonMatch(t *testing.T) {
	if !projectBootstrapRestartScaffoldFailureReason(buildProjectBootstrapRestartScaffoldFailureReason()) {
		t.Fatal("expected canonical restart scaffold reason to match")
	}
	if projectBootstrapRestartScaffoldFailureReason("kickoff validation failed: first-wave task 19 has no assigned agent") {
		t.Fatal("unexpected scaffold reason match")
	}
}

func TestBuildProjectBootstrapRecoveryContinuationContext(t *testing.T) {
	context := buildProjectBootstrapRecoveryContinuationContext(projectBootstrapResumeSnapshot{
		FailedTaskLine: "Named blocked task: task 19 id=1234 title=\"Draft homepage hero\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
		RepairTaskLine: "Other still-unassigned first-wave tasks you can repair in this same turn without rereading the task tree: task 22 id=5678 title=\"Configure Tailwind and design tokens\".",
		AssignmentLine: "workers=Ananya Webb (id=worker-1), Naomi Baptiste (id=worker-2)",
	})
	if !strings.Contains(context, "Named blocked task: task 19 id=1234") {
		t.Fatalf("context = %q, want blocked task line", context)
	}
	if !strings.Contains(context, "task 22 id=5678") {
		t.Fatalf("context = %q, want additional repair targets", context)
	}
	if !strings.Contains(context, "Existing active assignments: workers=Ananya Webb") {
		t.Fatalf("context = %q, want assignment roster", context)
	}
	if !strings.Contains(context, "do not call agent.list unless the persisted roster itself is inconsistent") {
		t.Fatalf("context = %q, want no-agent-list guidance", context)
	}
}

func TestBuildProjectBootstrapCompoundParentRepairTaskLineListsTopLevelDrafts(t *testing.T) {
	blockedID := uuid.New()
	childParentID := uuid.New()
	line := buildProjectBootstrapCompoundParentRepairTaskLine([]repo.ProjectTask{
		{
			ID:         blockedID,
			TaskNumber: 11,
			Title:      "Page Build",
			WorkStatus: "draft",
		},
		{
			ID:         uuid.New(),
			TaskNumber: 13,
			Title:      "Homepage layout",
			WorkStatus: "draft",
		},
		{
			ID:         uuid.New(),
			TaskNumber: 14,
			Title:      "Blog index layout",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(`{"decomposition_parent_task_id":"` + childParentID.String() + `"}`),
		},
		{
			ID:         uuid.New(),
			TaskNumber: 15,
			Title:      "Post template",
			WorkStatus: "queued",
		},
		{
			ID:         uuid.New(),
			TaskNumber: 16,
			Title:      "Navigation shell",
			WorkStatus: "draft",
		},
	}, repo.ProjectTask{
		ID:         blockedID,
		TaskNumber: 11,
		Title:      "Page Build",
		WorkStatus: "draft",
	})
	if !strings.Contains(line, "task 13") || !strings.Contains(line, "Homepage layout") {
		t.Fatalf("line = %q, want top-level draft task", line)
	}
	if !strings.Contains(line, "task 16") || !strings.Contains(line, "Navigation shell") {
		t.Fatalf("line = %q, want second top-level draft task", line)
	}
	if strings.Contains(line, "task 14") {
		t.Fatalf("line = %q, should skip existing child task", line)
	}
	if strings.Contains(line, "task 15") {
		t.Fatalf("line = %q, should skip non-draft task", line)
	}
}

func TestCanonicalProjectBootstrapSetupTasksPrefersCompletedCanonicalSet(t *testing.T) {
	projectID := uuid.New()
	doneBindID := uuid.New()
	draftBindID := uuid.New()
	doneSignoffID := uuid.New()
	draftSignoffID := uuid.New()

	canonical, byID := canonicalProjectBootstrapSetupTasks([]repo.ProjectTask{
		{
			ID:         draftBindID,
			ProjectID:  projectID,
			TaskNumber: 10,
			Title:      "Bind repo and environment",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`),
		},
		{
			ID:         doneBindID,
			ProjectID:  projectID,
			TaskNumber: 2,
			Title:      "Bind repo and environment",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`),
		},
		{
			ID:         draftSignoffID,
			ProjectID:  projectID,
			TaskNumber: 16,
			Title:      "Request and record Frank sign-off",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"record-frank-sign-off"}`),
		},
		{
			ID:         doneSignoffID,
			ProjectID:  projectID,
			TaskNumber: 8,
			Title:      "Request and record Frank sign-off",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"record-frank-sign-off"}`),
		},
	})

	if len(canonical) != 2 {
		t.Fatalf("canonical setup task count = %d, want 2", len(canonical))
	}
	if canonical[0].ID != doneBindID || canonical[1].ID != doneSignoffID {
		t.Fatalf("canonical setup task ids = [%s %s], want [%s %s]", canonical[0].ID, canonical[1].ID, doneBindID, doneSignoffID)
	}
	if _, ok := byID[doneBindID]; !ok {
		t.Fatal("done bind task missing from canonical byID map")
	}
	if _, ok := byID[draftBindID]; ok {
		t.Fatal("draft duplicate bind task should not remain in canonical byID map")
	}
}

func TestCanonicalProjectBootstrapGateTaskMatchesCompletedSetupBatch(t *testing.T) {
	projectID := uuid.New()
	earlyGateID := uuid.New()
	lateGateID := uuid.New()

	setupTasks := []repo.ProjectTask{
		{
			ID:         uuid.New(),
			ProjectID:  projectID,
			TaskNumber: 10,
			Title:      "Bind repo and environment",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`),
		},
		{
			ID:         uuid.New(),
			ProjectID:  projectID,
			TaskNumber: 16,
			Title:      "Request and record Frank sign-off",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"record-frank-sign-off"}`),
		},
	}

	gate, ok := canonicalProjectBootstrapGateTask([]repo.ProjectTask{
		{ID: earlyGateID, ProjectID: projectID, TaskNumber: 1, Title: "Bootstrap governance gate", WorkStatus: "draft", Metadata: json.RawMessage(`{"bootstrap_gate":true}`)},
		{ID: lateGateID, ProjectID: projectID, TaskNumber: 9, Title: "Bootstrap governance gate", WorkStatus: "draft", Metadata: json.RawMessage(`{"bootstrap_gate":true}`)},
	}, setupTasks)
	if !ok {
		t.Fatal("canonicalProjectBootstrapGateTask ok = false, want true")
	}
	if gate.ID != lateGateID {
		t.Fatalf("canonical gate id = %s, want %s", gate.ID, lateGateID)
	}
}

func TestLoadProjectBootstrapResumeSnapshotAddsCompoundParentRepairTargets(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	taskID := uuid.New()
	task13ID := uuid.New()
	task14ID := uuid.New()
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: uuid.New(), Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				ProjectID:  projectID,
				TaskNumber: 11,
				Title:      "Page Build",
				WorkStatus: "draft",
			},
			task13ID: {
				ID:         task13ID,
				ProjectID:  projectID,
				TaskNumber: 13,
				Title:      "Homepage layout",
				WorkStatus: "draft",
			},
			task14ID: {
				ID:         task14ID,
				ProjectID:  projectID,
				TaskNumber: 14,
				Title:      "Blog index layout",
				WorkStatus: "draft",
			},
		},
	}

	snapshot, err := fixture.engine.loadProjectBootstrapResumeSnapshot(context.Background(), projectID, projectBootstrapState{
		ValidationFailureClass:  projectBootstrapFailureCompoundParent,
		ValidationFailureReason: "kickoff validation failed: task 11 (Page Build) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete",
	})
	if err != nil {
		t.Fatalf("loadProjectBootstrapResumeSnapshot: %v", err)
	}
	if !strings.Contains(snapshot.RepairTaskLine, "task 13") || !strings.Contains(snapshot.RepairTaskLine, "task 14") {
		t.Fatalf("RepairTaskLine = %q, want top-level repair targets", snapshot.RepairTaskLine)
	}
}

func TestBuildProjectBootstrapResumeActionPromptStartsWithPersistAfterTaskTree(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		AssignmentCount:          4,
		PlannedTaskCount:         18,
		CurrentPhase:             projectBootstrapCheckpointTaskTreePersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
	}, projectBootstrapResumeSnapshot{})
	if !strings.Contains(prompt, "Your first tool call in this resume turn should be bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want persist-first guidance after task tree", prompt)
	}
	if !strings.Contains(prompt, "Record any already-complete checklist steps before reading more tasks, templates, or scaffold artifacts") {
		t.Fatalf("prompt = %q, want no-reread-first guidance after task tree", prompt)
	}
}

func TestShouldStopAfterBootstrapPersistWhenToolResultCompletesChecklist(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status": projectBootstrapStatusActive,
		},
	})
	if err != nil {
		t.Fatalf("marshal session metadata: %v", err)
	}
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = metadata
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
			},
		},
	}

	stop, err := fixture.engine.shouldStopAfterBootstrapPersist(context.Background(), &turnRuntime{
		session: fixture.session,
	}, []ToolResult{{
		Name: "bootstrap.setup.persist",
		Output: map[string]any{
			"setup_checklist_complete": true,
		},
	}})
	if err != nil {
		t.Fatalf("shouldStopAfterBootstrapPersist err = %v, want nil", err)
	}
	if !stop {
		t.Fatal("shouldStopAfterBootstrapPersist stop = false, want true")
	}
}

func TestContinuationTurnReloadsSessionBeforeBootstrapResumeState(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               "task_tree_persisted",
			"assignment_count":            2,
			"planned_task_count":          9,
			"planned_flow_template_count": 1,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}

	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 1 {
			fixture.chat.session.Metadata = metadata
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 after session reload", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
			if !strings.Contains(msg.Content, "Existing PM: Sam.blog PM") {
				t.Fatalf("resume message = %q, want refreshed PM line", msg.Content)
			}
			return
		}
	}
	t.Fatal("project bootstrap resume message missing after session reload")
}

func TestContinuationTurnAppendsCompactBootstrapActionPromptAfterCompression(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               "first_wave_executions_created",
			"last_successful_checkpoint":  projectBootstrapCheckpointFirstWaveSelected,
			"assignment_count":            2,
			"planned_task_count":          9,
			"planned_flow_template_count": 1,
			"first_wave_task_count":       4,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}

	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM", AgentClass: "staff", AgentType: "pm"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz", AgentClass: "temp", AgentType: "general"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	messageMetadata, err := json.Marshal(map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("Marshal message metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), fixture.userMessageID, messageMetadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}
	fixture.chat.session.Metadata = metadata
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	var secondHistoryStart *uuid.UUID
	var actionMessageID uuid.UUID
	var resumeMessageID uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if secondHistoryStart == nil {
		t.Fatal("second assemble HistoryStartID is nil, want resume-rooted bootstrap continuation")
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			resumeMessageID = msg.ID
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
			if !strings.Contains(msg.Content, "Existing PM: Sam.blog PM") {
				t.Fatalf("resume message = %q, want PM line", msg.Content)
			}
			continue
		}
		if strings.Contains(msg.Content, "Continue the active project bootstrap from the persisted state above.") {
			actionMessageID = msg.ID
			if !strings.Contains(msg.Content, "Do not restate the project state or re-read scaffold artifacts") {
				t.Fatalf("action prompt = %q, want anti-reread guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not call task.list, flow.list_templates, or file.read on scaffold planning artifacts") {
				t.Fatalf("action prompt = %q, want tool-specific anti-reread guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not ask the user what they want. Continue the bootstrap workflow now.") {
				t.Fatalf("action prompt = %q, want direct bootstrap action guidance", msg.Content)
			}
		}
	}
	if actionMessageID == uuid.Nil {
		t.Fatal("compact bootstrap action prompt missing after compressed auto-continuation")
	}
	if resumeMessageID == uuid.Nil {
		t.Fatal("bootstrap resume message missing after compressed auto-continuation")
	}
	if *secondHistoryStart != resumeMessageID && *secondHistoryStart != actionMessageID {
		t.Fatalf("second assemble HistoryStartID = %s, want bootstrap resume %s or action prompt %s", *secondHistoryStart, resumeMessageID, actionMessageID)
	}
}

func TestContinuationTurnSynthesizesBootstrapResumeStateWhenMetadataIsStale(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 with synthesized bootstrap resume", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
			if !strings.Contains(msg.Content, "Existing PM: Sam.blog PM") {
				t.Fatalf("resume message = %q, want synthesized PM line", msg.Content)
			}
			return
		}
	}
	t.Fatal("project bootstrap resume message missing with stale metadata")
}

func TestBootstrapAutoContinueRedirectsAfterBootstrapCompletion(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.Mode = "async"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.Mode = fixture.session.Mode
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	state := projectBootstrapState{
		Status:                   projectBootstrapStatusCompleted,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveJobsClaimed,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveJobsClaimed,
		ValidationStatus:         projectBootstrapValidationPassed,
		AssignmentCount:          4,
		PlannedTaskCount:         12,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       4,
		FirstWavePromotedCount:   4,
		FirstWaveExecutionCount:  4,
		FirstWaveJobCount:        4,
	}
	metadata, err := projectBootstrapMetadataJSON(nil, state)
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	userMessage, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	userMetadata, err := json.Marshal(map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("Marshal user metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), userMessage.ID, userMetadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}
	if _, err := fixture.messages.UpdateContent(context.Background(), userMessage.ID, "Continue the active project bootstrap from the persisted state above."); err != nil {
		t.Fatalf("UpdateContent user message: %v", err)
	}

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawRedirect bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Bootstrap is already complete.") {
			sawRedirect = true
		}
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			t.Fatalf("resume message = %q, want none after bootstrap completion", msg.Content)
		}
	}
	if !sawRedirect {
		t.Fatal("completed bootstrap redirect message missing")
	}
}

func TestBootstrapAutoContinueUsesResumeStateForRecoverableFailedBootstrap(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.Mode = "async"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.Mode = fixture.session.Mode
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	state := projectBootstrapState{
		Status:                   projectBootstrapStatusFailed,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveSelected,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFlowTemplatesPersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureMissingReviewer,
		ValidationFailureReason:  "kickoff validation failed: staffed project persisted executable work but did not assign an active reviewer",
		AssignmentCount:          3,
		PlannedTaskCount:         16,
		PlannedFlowTemplateCount: 1,
		BootstrapTaskOutstanding: true,
		BootstrapTaskID:          uuid.NewString(),
	}
	metadata, err := projectBootstrapMetadataJSON(nil, state)
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	userMessage, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	userMetadata, err := json.Marshal(map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("Marshal user metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), userMessage.ID, userMetadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}
	if _, err := fixture.messages.UpdateContent(context.Background(), userMessage.ID, "Continue the active project bootstrap from the persisted state above."); err != nil {
		t.Fatalf("UpdateContent user message: %v", err)
	}

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawResume bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Bootstrap is already complete.") {
			t.Fatalf("unexpected completed-bootstrap redirect: %q", msg.Content)
		}
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			sawResume = true
		}
	}
	if !sawResume {
		t.Fatal("recoverable failed bootstrap resume message missing")
	}
}

func TestFinalizePendingProjectBootstrapMessages(t *testing.T) {
	fixture := newUnitFixture(t, "sync")

	bootstrapMsg := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Continue the active project bootstrap from the persisted state above.",
		Metadata:  json.RawMessage(`{"source":"project_bootstrap","auto_continue":true}`),
	})
	plainMsg := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Normal user message",
	})

	if err := fixture.engine.finalizePendingProjectBootstrapMessages(context.Background(), fixture.session.ID); err != nil {
		t.Fatalf("finalizePendingProjectBootstrapMessages: %v", err)
	}

	updatedBootstrap, err := fixture.messages.GetByID(context.Background(), bootstrapMsg.ID)
	if err != nil {
		t.Fatalf("GetByID bootstrap message: %v", err)
	}
	if updatedBootstrap.Status != "final" {
		t.Fatalf("bootstrap message status = %q, want final", updatedBootstrap.Status)
	}

	updatedPlain, err := fixture.messages.GetByID(context.Background(), plainMsg.ID)
	if err != nil {
		t.Fatalf("GetByID plain message: %v", err)
	}
	if updatedPlain.Status != "pending" {
		t.Fatalf("plain message status = %q, want pending", updatedPlain.Status)
	}
}

func TestResolveModelProfileWorkerDefaultsToStandardWithoutOverrides(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.resolver = nil
	agentRepo := fixture.engine.agents.(*fakeAgentRepo)
	agentRepo.agent.AgentType = "worker"

	profile, err := fixture.engine.resolveModelProfile(context.Background(), fixture.session, agentRepo.agent, "agent_turn", 0, false)
	if err != nil {
		t.Fatalf("resolveModelProfile: %v", err)
	}
	if profile.LogicalProfileID != "standard" {
		t.Fatalf("logical_profile_id = %q, want %q", profile.LogicalProfileID, "standard")
	}
}

func TestWorkerModelEscalatesToHighCapabilityAfterTransientRetry(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.resolver = nil
	agentRepo := fixture.engine.agents.(*fakeAgentRepo)
	agentRepo.agent.AgentType = "worker"

	streamProfiles := make([]string, 0, 2)
	attempt := 0
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		streamProfiles = append(streamProfiles, strings.TrimSpace(req.Profile.LogicalProfileID))
		attempt++
		if attempt == 1 {
			return ModelResponse{}, errors.New("rate limit exceeded")
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(streamProfiles) < 2 {
		t.Fatalf("stream profile calls = %v, want at least 2 calls", streamProfiles)
	}
	if streamProfiles[0] != "standard" {
		t.Fatalf("first stream profile = %q, want %q", streamProfiles[0], "standard")
	}
	if streamProfiles[1] != "high-capability" {
		t.Fatalf("second stream profile = %q, want %q", streamProfiles[1], "high-capability")
	}
}

func TestTaskWorkspaceRootFailsClosedWhenMainWorktreeOwnsTaskBranch(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	ctx := context.Background()
	repoRoot := t.TempDir()
	projectID := uuid.New()
	taskID := uuid.New()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run(repoRoot, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	run(repoRoot, "add", "README.md")
	run(repoRoot, "commit", "-m", "base")
	run(repoRoot, "checkout", "-b", "task/10")
	if err := os.WriteFile(filepath.Join(repoRoot, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	projects := fixture.engine.projects.(*fakeProjectRepo)
	projects.items = map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, Slug: "turn-fail-closed"},
	}
	tasks := fixture.engine.tasks.(*fakeTaskRepo)
	branchName := "task/10"
	tasks.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:         taskID,
			ProjectID:  projectID,
			TaskNumber: 10,
			BranchName: &branchName,
		},
	}
	fixture.engine.environments = &fakeTurnProjectEnvironmentRepo{
		items: map[uuid.UUID][]repo.ProjectEnvironment{
			projectID: {{
				Name: "workspace",
				RepoPath: func() *string {
					path := repoRoot
					return &path
				}(),
				IsActive: true,
			}},
		},
	}

	root, err := fixture.engine.taskWorkspaceRoot(ctx, tasks.items[taskID])
	if err == nil {
		t.Fatal("taskWorkspaceRoot error = nil, want fail-closed worktree acquisition error")
	}
	if strings.TrimSpace(root) != "" {
		t.Fatalf("taskWorkspaceRoot root = %q, want empty root on fail-closed worktree acquisition", root)
	}
}

func TestContinuationTurnRecoversWhenTurnAlreadyCompleted(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	var completeErr error
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call != 1 || completeErr != nil {
			return
		}
		completeErr = fixture.chat.CompleteTurn(context.Background(), input.TurnID)
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "summary"}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if completeErr != nil {
		t.Fatalf("external completion err = %v, want nil", completeErr)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want >= 2", len(fixture.chat.turnOrder))
	}
	last := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if last == nil || last.Status != "completed" {
		t.Fatalf("last turn status = %v, want completed", last)
	}
}

func TestContinuationTurnRecoversWhenTurnAlreadyCancelled(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	var cancelErr error
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call != 1 || cancelErr != nil {
			return
		}
		cancelErr = fixture.chat.CancelTurn(context.Background(), input.TurnID, "steer_turn")
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "summary"}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if cancelErr != nil {
		t.Fatalf("external cancellation err = %v, want nil", cancelErr)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want >= 2", len(fixture.chat.turnOrder))
	}
	first := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if first == nil || first.Status != "cancelled" {
		t.Fatalf("first turn status = %v, want cancelled", first)
	}
	last := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if last == nil || last.Status != "completed" {
		t.Fatalf("last turn status = %v, want completed", last)
	}
}

func TestHandleUserMessageIgnoresImmutableAssistantWriteAfterTurnCancelled(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.messages.updateContentFn = func(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error) {
		turnID := fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1]
		if err := fixture.chat.CancelTurn(context.Background(), turnID, "steer_turn"); err != nil {
			t.Fatalf("CancelTurn: %v", err)
		}
		return repo.ChatMessage{}, repo.ErrMessageContentImmutable
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("#"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "# draft"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(fixture.chat.turnOrder) == 0 {
		t.Fatal("expected at least one turn")
	}
	last := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if last == nil || last.Status != "cancelled" {
		t.Fatalf("last turn status = %v, want cancelled", last)
	}
}

func TestHandleUserMessageCarriesRunAttributionIntoInvocationAndModelRequest(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	runID := uuid.New()
	runStepID := uuid.New()
	runAttemptID := uuid.New()

	metadata, err := json.Marshal(map[string]any{
		"run_id":         runID.String(),
		"run_step_id":    runStepID.String(),
		"run_attempt_id": runAttemptID.String(),
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	message.Metadata = metadata
	fixture.messages.upsert(message)

	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		return ModelResponse{Content: "respond"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if req.InvocationID == nil || *req.InvocationID == uuid.Nil {
			t.Fatalf("req.InvocationID = %v, want non-nil", req.InvocationID)
		}
		if req.RunID == nil || *req.RunID != runID {
			t.Fatalf("req.RunID = %v, want %s", req.RunID, runID)
		}
		if req.RunStepID == nil || *req.RunStepID != runStepID {
			t.Fatalf("req.RunStepID = %v, want %s", req.RunStepID, runStepID)
		}
		if req.RunAttemptID == nil || *req.RunAttemptID != runAttemptID {
			t.Fatalf("req.RunAttemptID = %v, want %s", req.RunAttemptID, runAttemptID)
		}
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok", Usage: &ModelUsage{InputTokens: 12, OutputTokens: 2}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if len(fixture.invocations.creates) == 0 {
		t.Fatal("expected at least one invocation create")
	}
	created := fixture.invocations.creates[0]
	if created.RunID == nil || *created.RunID != runID {
		t.Fatalf("created.RunID = %v, want %s", created.RunID, runID)
	}
	if created.RunStepID == nil || *created.RunStepID != runStepID {
		t.Fatalf("created.RunStepID = %v, want %s", created.RunStepID, runStepID)
	}
	if created.RunAttemptID == nil || *created.RunAttemptID != runAttemptID {
		t.Fatalf("created.RunAttemptID = %v, want %s", created.RunAttemptID, runAttemptID)
	}
}

func TestHandleUserMessageBlocksRepeatedToolValidationFailures(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Mode = "async"

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				Title:           "Write file",
				WorkStatus:      "in_progress",
				AssignedAgentID: &assignedAgentID,
				Metadata:        json.RawMessage(`{"existing":"value"}`),
			},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = blocker
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("write"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{
			ToolCalls: []ModelToolCall{{
				ID:   "write-1",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"_raw": `{"content":"hello"}`,
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output:     map[string]any{"error": "path_required"},
			RunID:      &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	taskRecord, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if taskRecord.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", taskRecord.WorkStatus)
	}

	guard, ok := parseTaskValidationGuard(taskRecord.Metadata)
	if !ok {
		t.Fatal("expected validation guard metadata")
	}
	if !guard.Blocked {
		t.Fatal("expected blocked validation guard")
	}
	if guard.Count != validationLoopBlockThreshold {
		t.Fatalf("guard count = %d, want %d", guard.Count, validationLoopBlockThreshold)
	}
	if guard.FailureClass != "tool_validation" {
		t.Fatalf("guard failure_class = %q, want tool_validation", guard.FailureClass)
	}
	if guard.FailureCode != "path_required" {
		t.Fatalf("guard failure_code = %q, want path_required", guard.FailureCode)
	}
	if !strings.Contains(guard.FailureReason, "path_required") {
		t.Fatalf("guard failure_reason = %q, want contains path_required", guard.FailureReason)
	}
	if strings.TrimSpace(guard.AttemptFingerprint) == "" {
		t.Fatal("expected attempt fingerprint on validation guard")
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocker calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "file.write") {
		t.Fatalf("block reason = %q, want contains file.write", blocker.calls[0].reason)
	}
	if fixture.chat.failCalls != 0 {
		t.Fatalf("failCalls = %d, want 0", fixture.chat.failCalls)
	}
	if fixture.chat.completeCalls != 1 {
		t.Fatalf("completeCalls = %d, want 1", fixture.chat.completeCalls)
	}
	if !fixture.messages.containsContentSubstring("validation loop blocked") {
		t.Fatal("expected validation loop system message")
	}
}

func TestClassifyToolValidationFailureRecognizesNonSubstantiveContent(t *testing.T) {
	failure, ok := classifyToolValidationFailure(ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "validation_scope.md",
			"content": "Let me create the substantive content for validation_scope.md:",
		},
	}, ToolResult{
		ToolCallID: "write-1",
		Name:       "file.write",
		Output: map[string]any{
			"error": "non_substantive_content",
		},
	})
	if !ok {
		t.Fatal("expected non_substantive_content to classify as a validation failure")
	}
	if failure.FailureCode != "non_substantive_content" {
		t.Fatalf("failure code = %q, want non_substantive_content", failure.FailureCode)
	}
	if failure.ToolName != "file.write" {
		t.Fatalf("tool name = %q, want file.write", failure.ToolName)
	}
}

func TestHandleUserMessageFailsWhenValidationLoopBlockLacksTaskTransitions(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Mode = "async"

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				Title:           "Write file",
				WorkStatus:      "in_progress",
				AssignedAgentID: &assignedAgentID,
				Metadata:        json.RawMessage(`{"existing":"value"}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = nil
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("write"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{
			ToolCalls: []ModelToolCall{{
				ID:   "write-1",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"_raw": `{"content":"hello"}`,
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output:     map[string]any{"error": "path_required"},
			RunID:      &runID,
		}, nil
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil || !strings.Contains(err.Error(), errMissingTaskTransitionServiceForValidationBlock) {
		t.Fatalf("HandleUserMessage error = %v, want contains %q", err, errMissingTaskTransitionServiceForValidationBlock)
	}

	taskRecord, getErr := taskRepo.GetByID(context.Background(), taskID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
}

func TestCollectToolValidationFailuresSuppressesFocusReadFailuresAfterSuccessfulFileWrite(t *testing.T) {
	t.Parallel()

	calls := []ToolCall{
		{ID: "read-1", Name: "file.read"},
		{ID: "write-1", Name: "file.write"},
	}
	results := []ToolResult{
		{
			ToolCallID: "read-1",
			Name:       "file.read",
			Output: map[string]any{
				"error":            "recovery_target_focus_required",
				"deliverable_path": "schema-definition.md",
			},
		},
		{
			ToolCallID: "write-1",
			Name:       "file.write",
			Output: map[string]any{
				"path":      "schema-definition.md",
				"byte_size": 4096,
			},
		},
	}

	failures := collectToolValidationFailures(calls, results)
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want suppressed focus-read failure after successful file.write", failures)
	}
}

func TestCollectToolValidationFailuresPreservesDeliverablePath(t *testing.T) {
	t.Parallel()

	calls := []ToolCall{{ID: "read-1", Name: "file.read"}}
	results := []ToolResult{{
		ToolCallID: "read-1",
		Name:       "file.read",
		Output: map[string]any{
			"error":            "recovery_target_focus_required",
			"deliverable_path": "schemas/pipeline-schema-v1.0.md",
		},
	}}

	failures := collectToolValidationFailures(calls, results)
	if len(failures) != 1 {
		t.Fatalf("failure count = %d, want 1", len(failures))
	}
	if failures[0].DeliverablePath != "schemas/pipeline-schema-v1.0.md" {
		t.Fatalf("DeliverablePath = %q, want schemas/pipeline-schema-v1.0.md", failures[0].DeliverablePath)
	}
}

func TestCollectToolValidationFailuresKeepsNonFocusFailuresAfterSuccessfulFileWrite(t *testing.T) {
	t.Parallel()

	calls := []ToolCall{
		{ID: "read-1", Name: "file.read"},
		{ID: "write-1", Name: "file.write"},
	}
	results := []ToolResult{
		{
			ToolCallID: "read-1",
			Name:       "file.read",
			Output: map[string]any{
				"error": "path_required",
			},
		},
		{
			ToolCallID: "write-1",
			Name:       "file.write",
			Output: map[string]any{
				"path":      "schema-definition.md",
				"byte_size": 4096,
			},
		},
	}

	failures := collectToolValidationFailures(calls, results)
	if len(failures) != 1 {
		t.Fatalf("failure count = %d, want 1", len(failures))
	}
	if failures[0].FailureCode != "path_required" {
		t.Fatalf("failure code = %q, want path_required", failures[0].FailureCode)
	}
}

func TestHandleUserMessageSkipsBlockedValidationLoop(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Mode = "async"

	guard := taskValidationGuardState{
		InitialMessageID:   fixture.userMessageID.String(),
		Fingerprint:        "file.write:path_required",
		AttemptFingerprint: "file.write:attempt",
		ToolName:           "file.write",
		FailureClass:       "tool_validation",
		FailureCode:        "path_required",
		FailureReason:      "path_required",
		Count:              validationLoopBlockThreshold,
		BlockThreshold:     validationLoopBlockThreshold,
		Blocked:            true,
		FirstSeenAt:        time.Now().UTC().Format(time.RFC3339Nano),
		LastSeenAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
	metadata, err := mergeTaskValidationGuardMetadata(json.RawMessage(`{"existing":"value"}`), guard)
	if err != nil {
		t.Fatalf("mergeTaskValidationGuardMetadata: %v", err)
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      uuid.New(),
				Title:          "Blocked task",
				WorkStatus:     "blocked",
				Metadata:       metadata,
			},
		},
	}

	if err := fixture.engine.handleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID, nil, 0, nil); err != nil {
		t.Fatalf("handleUserMessage: %v", err)
	}
	if got := len(fixture.chat.turnOrder); got != 0 {
		t.Fatalf("created turns = %d, want 0", got)
	}

	enqueued, err := fixture.engine.enqueueAgentTurnIfActive(context.Background(), fixture.session, AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive: %v", err)
	}
	if enqueued {
		t.Fatal("expected enqueue to be suppressed")
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleUserMessageValidationLoopBlockPersistsDeliverableCheckpoint(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Mode = "async"

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				Title:           "Write schema",
				WorkStatus:      "in_progress",
				AssignedAgentID: &assignedAgentID,
				Metadata:        json.RawMessage(`{"existing":"value"}`),
			},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = blocker
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.read", Tier: "tier2"}}}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("read"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{
			ToolCalls: []ModelToolCall{{
				ID:   "read-1",
				Name: "file.read",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": "planning/strategy-artifact/oc-20-success-narrative.md",
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"error":            "recovery_target_focus_required",
				"deliverable_path": "schemas/pipeline-schema-v1.0.md",
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	taskRecord, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != "schemas/pipeline-schema-v1.0.md" {
		t.Fatalf("TargetPath = %q, want schemas/pipeline-schema-v1.0.md", checkpoint.TargetPath)
	}
	if checkpoint.FailureReason != "recovery_target_focus_required" {
		t.Fatalf("FailureReason = %q, want recovery_target_focus_required", checkpoint.FailureReason)
	}
}

func TestHandleRecoveryPopulatedFileWriteOutcomePrefersDeliverablePathFromToolResult(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline-ops"
	orgSlug := "default"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.dataDir = dataDir
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				TaskNumber:     20,
				WorkStatus:     "in_progress",
			},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
		recoveryFileWrites: map[string]recoveryPopulatedFileWriteState{
			"write-1": {
				TargetPath: "planning/strategy-artifact/oc-20-success-narrative.md",
				Draft:      "# Pipeline Schema\n\nSubstantive schema body.\n",
			},
		},
	}

	handled, err := fixture.engine.handleRecoveryPopulatedFileWriteOutcome(context.Background(), rt, ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path": "planning/strategy-artifact/oc-20-success-narrative.md",
		},
	}, ToolResult{
		ToolCallID: "write-1",
		Name:       "file.write",
		Output: map[string]any{
			"error":            "deliverable_path_required",
			"deliverable_path": "schemas/pipeline-schema-v1.0.md",
			"message":          "This execution-first task already has an explicit deliverable path `schemas/pipeline-schema-v1.0.md`. Do not write `planning/strategy-artifact/oc-20-success-narrative.md` during task execution. Continue the concrete deliverable instead.",
		},
	})
	if err != nil {
		t.Fatalf("handleRecoveryPopulatedFileWriteOutcome: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}

	taskRecord, err := fixture.engine.tasks.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != "schemas/pipeline-schema-v1.0.md" {
		t.Fatalf("TargetPath = %q, want schemas/pipeline-schema-v1.0.md", checkpoint.TargetPath)
	}
	if !strings.Contains(checkpoint.ArtifactPath, "schemas/pipeline-schema-v1.0.md") {
		t.Fatalf("ArtifactPath = %q, want schema artifact path", checkpoint.ArtifactPath)
	}
	if !fixture.messages.containsContentSubstring("schemas/pipeline-schema-v1.0.md") {
		t.Fatal("expected recovery halt message to mention deliverable path")
	}
}

func TestMaybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatchBlocksReadOnlyRecoveryNarration(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	description := "Write the deliverable. Output: docs/content-strategy.md with the full content strategy."
	plan := taskplan.Analyze("Write content strategy", &description)

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		Title:          "Write content strategy",
		WorkStatus:     "in_progress",
		Description:    &description,
		Metadata:       taskplan.ApplyMetadata(nil, plan),
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	handled, err := fixture.engine.maybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatch(
		context.Background(),
		rt,
		"Let me first check the current state of the target file and recovery artifacts, then write the substantive content strategy document.",
		[]ModelToolCall{{
			ID:   "read-1",
			Name: "file.list",
			Tier: "tier1",
			Arguments: map[string]any{
				"path": ".",
			},
		}},
	)
	if err != nil {
		t.Fatalf("maybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatch: %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if strings.TrimSpace(rt.recoveryBlockReason) == "" {
		t.Fatal("expected recoveryBlockReason to be set")
	}

	taskRecord, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != "docs/content-strategy.md" {
		t.Fatalf("TargetPath = %q, want docs/content-strategy.md", checkpoint.TargetPath)
	}
	if !strings.Contains(checkpoint.FailureReason, "instead of the file body") {
		t.Fatalf("FailureReason = %q, want file-body rejection", checkpoint.FailureReason)
	}
	if !fixture.messages.containsContentSubstring("Recovery turn halted: recovered file.write") {
		t.Fatal("expected recovery halt system message")
	}
}

func TestMaybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatchAllowsMutationRepairPath(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	description := "Write the deliverable. Output: docs/content-strategy.md with the full content strategy."
	plan := taskplan.Analyze("Write content strategy", &description)

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		Title:          "Write content strategy",
		WorkStatus:     "in_progress",
		Description:    &description,
		Metadata:       taskplan.ApplyMetadata(nil, plan),
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	handled, err := fixture.engine.maybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatch(
		context.Background(),
		rt,
		"Let me first check the current state of the target file and recovery artifacts, then write the substantive content strategy document.",
		[]ModelToolCall{{
			ID:   "write-1",
			Name: "file.write",
			Tier: "tier2",
			Arguments: map[string]any{
				"path": "docs/content-strategy.md",
			},
		}},
	)
	if err != nil {
		t.Fatalf("maybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatch: %v", err)
	}
	if handled {
		t.Fatal("handled = true, want false when a deliverable mutation is present")
	}

	taskRecord, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(taskRecord.Metadata); ok {
		t.Fatal("unexpected recovery checkpoint")
	}
	if fixture.messages.containsContentSubstring("Recovery turn halted: recovered file.write") {
		t.Fatal("unexpected recovery halt system message")
	}
}

func TestMaybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatchSkipsReviewLane(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:         taskID,
		TaskNumber: 13,
		Title:      "Document success criteria",
		WorkStatus: "review",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "durable_recovery_checkpoint",
				"target_path":    "OC-13-PASS-FAIL-CHECKLIST.md",
				"failure_reason": "review lane recovery",
			},
		}),
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	handled, err := fixture.engine.maybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatch(
		context.Background(),
		rt,
		"I'm the reviewer and I will inspect the deliverable before deciding.",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatch: %v", err)
	}
	if handled {
		t.Fatal("handled = true, want false for review-lane recovery")
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsUsesContinuationSummaryDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Execute test scenario 2 (edge cases): test capacity limits, concurrent assignments, boundary conditions. Log all test cases and verify system behavior under stress."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		OrganizationID: fixture.session.OrganizationID,
		Title:          "OC-4: Execute Test Scenario 2 (Edge Cases)",
		TaskNumber:     13,
		WorkStatus:     "in_progress",
		Description:    &description,
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "repeated_non_substantive_recovery_checkpoint",
				"target_path":    "Test/test-execution-oc13-scenario2-edge-cases.md",
				"failure_reason": "repeated non-substantive recovery drafts",
			},
		}),
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		"I'll write the test execution deliverable now using the continuation summary draft above.",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != "Test/test-execution-oc13-scenario2-edge-cases.md" {
		t.Fatalf("path = %q, want inferred target", got)
	}
	if got := stringValue(toolCalls[0].Arguments["content"]); !strings.Contains(got, "## Test Cases") {
		t.Fatalf("content = %q, want synthesized continuation draft", got)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsSkipsWhenMutationAlreadyPresent(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	rt := &turnRuntime{
		session: &chat.ChatSession{
			Mode:      "async",
			ScopeType: "project_task",
		},
		turn: &chat.ChatTurn{
			ID: uuid.New(),
		},
		recoveryTurn: true,
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		"I'll write it now.",
		[]ModelToolCall{{
			ID:   "write-1",
			Name: "file.write",
			Tier: "tier2",
			Arguments: map[string]any{
				"path": "docs/target.md",
			},
		}},
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if synthesized {
		t.Fatal("synthesized = true, want false")
	}
	if len(toolCalls) != 1 || toolCalls[0].ID != "write-1" {
		t.Fatalf("toolCalls = %+v, want original tool call", toolCalls)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsUsesDirectSubstantiveAssistantBody(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	description := "Orchestration task: Validate that first-wave executable tasks can enter execution, advance through flows, and produce outputs. Parent task for first-wave validation subtasks."
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:          taskID,
		TaskNumber:  12,
		Title:       "V2: Validate First-Wave Task Execution & Flow Advancement",
		Description: &description,
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "repeated_non_substantive_recovery_checkpoint",
				"target_path":    "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md",
				"failure_reason": "repeated non-substantive recovery drafts",
			},
		}),
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	assistantBody := `# FIRST-WAVE EXECUTION VALIDATION PLAN

## Objective
Validate that the bounded first-wave task set enters execution, advances through work, review, and merge, and produces durable outputs.

## Validation Checklist
- Confirm first-wave tasks are queued with executable flow templates.
- Capture substantive work outputs and commit SHAs.
- Verify review decisions and terminal done transitions.

## Validation Log
| Task | Status | Evidence |
|------|--------|----------|
| OC-10 | pending | queue entry pending |
| OC-11 | pending | review pending |
`

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		assistantBody,
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md" {
		t.Fatalf("path = %q, want recovery target", got)
	}
	if got := stringValue(toolCalls[0].Arguments["content"]); !strings.Contains(got, "## Validation Log") {
		t.Fatalf("content = %q, want direct assistant body", got)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsOverridesNonMutationDiscoveryCall(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Execute test scenario 2 (edge cases): test capacity limits, concurrent assignments, boundary conditions. Log all test cases and verify system behavior under stress."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		OrganizationID: fixture.session.OrganizationID,
		Title:          "OC-4: Execute Test Scenario 2 (Edge Cases)",
		TaskNumber:     13,
		WorkStatus:     "in_progress",
		Description:    &description,
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "repeated_non_substantive_recovery_checkpoint",
				"target_path":    "Test/test-execution-oc13-scenario2-edge-cases.md",
				"failure_reason": "repeated non-substantive recovery drafts",
			},
		}),
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		"I'll write the test execution deliverable now using the continuation summary draft above.",
		[]ModelToolCall{{
			ID:   "read-1",
			Name: "file.list",
			Tier: "tier2",
			Arguments: map[string]any{
				"path": "Test",
			},
		}},
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want synthesized file.write override", toolCalls)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsOverridesInvalidMutationWithPersistedDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "happy-path-recovery"
	orgSlug := "default"
	targetPath := "test_execution_happy_path.md"
	targetDraft := strings.TrimSpace(`
# Happy Path Test Execution

## Task ID
- task-123

## Routing Trace
- Request entered intake at 2026-03-24T16:00:00Z
- Routed to the primary speaker-ops pool
- Load balancer selected worker-2

## Verdict
- PASS
`)

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     11,
		Title:          "Happy Path Test - Core Routing",
		WorkStatus:     "blocked",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "durable_recovery_checkpoint",
				"target_path":    targetPath,
				"failure_reason": "recovered file.write for test_execution_happy_path.md failed because the draft was narration instead of the file body",
			},
		}),
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	targetAbs := filepath.Join(dataDir, "workspaces", projectSlug, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(targetDraft+"\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		"I'll execute the Happy Path test now and write the complete test execution log with actual results, timestamps, and validation evidence.",
		[]ModelToolCall{{
			ID:   "write-1",
			Name: "file.write",
			Tier: "tier2",
			Arguments: map[string]any{
				"path":    targetPath,
				"content": "I'll execute the Happy Path test now and write the complete test execution log with actual results, timestamps, and validation evidence.",
			},
		}},
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one synthesized file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got := stringValue(toolCalls[0].Arguments["content"]); got != targetDraft {
		t.Fatalf("content = %q, want persisted target draft", got)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsUsesPersistedTargetDraftAfterGenericRetryReply(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	targetPath := "Test/test-execution-oc13-scenario2-edge-cases.md"
	targetDraft := strings.TrimSpace(`
# OC-4: Execute Test Scenario 2 (Edge Cases) Execution Log

## Objective
Execute the edge-case routing scenario and capture concrete evidence.

## Test Cases
- TC1: Capacity limits
- TC2: Concurrent assignments
- TC3: Boundary conditions

## Findings
- Routing remained deterministic under load.
`)

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     13,
		Title:          "OC-4: Execute Test Scenario 2 (Edge Cases)",
		WorkStatus:     "blocked",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "durable_recovery_checkpoint",
				"target_path":    targetPath,
				"failure_reason": "assistant draft for Test/test-execution-oc13-scenario2-edge-cases.md repeated a generic recovery reply instead of the file body",
			},
		}),
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	targetAbs := filepath.Join(dataDir, "workspaces", projectSlug, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(targetDraft+"\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	turnID := uuid.New()
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "I'll help you resume the task. Let me first get the current task context to understand the recovery state.",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		"I'll help you resume the task. Let me first get the current task context to understand the recovery state.",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got := stringValue(toolCalls[0].Arguments["content"]); got != targetDraft {
		t.Fatalf("content = %q, want persisted target draft", got)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsUsesSubstantiveAssistantDraftBlock(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	targetPath := "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md"
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	draftBody := strings.TrimSpace(`
# FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md

## Task Overview
Validate that first-wave executable tasks can enter execution, advance through flows, and produce concrete outputs.

## Validation Checks
- Confirm first-wave tasks leave draft and enter execution.
- Confirm flow advancement reaches review.
- Confirm concrete output artifacts are produced.
`)

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     12,
		Title:          "V2: Validate First-Wave Task Execution & Flow Advancement",
		WorkStatus:     "blocked",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "repeated_non_substantive_recovery_checkpoint",
				"target_path":    targetPath,
				"failure_reason": "repeated non-substantive recovery drafts",
			},
		}),
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	assistantContent := "Based on task OC-12, here is the deliverable:\n\n```markdown\n" + draftBody + "\n```"
	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		assistantContent,
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got := stringValue(toolCalls[0].Arguments["content"]); got != draftBody {
		t.Fatalf("content = %q, want extracted fenced draft body", got)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsSkipsRejectedPersistedTargetDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	targetPath := "Test/oc-21-boundary-test-design.md"
	targetDraft := "I'll start work on OC-21 by examining the task context, understanding the previous rejection, and designing the boundary test scenario for rate limits and capacity constraints.\n\n" +
		"Let me first get the full task details and understand what was previously rejected:"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     0,
		Title:          "Document boundary test design for rate limits and pipeline capacity",
		WorkStatus:     "blocked",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "durable_recovery_checkpoint",
				"target_path":    targetPath,
				"failure_reason": "assistant draft for Test/oc-21-boundary-test-design.md repeated a generic recovery reply instead of the file body",
			},
		}),
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	targetAbs := filepath.Join(dataDir, "workspaces", projectSlug, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(targetDraft+"\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	turnID := uuid.New()
	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		targetDraft,
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if synthesized {
		t.Fatal("synthesized = true, want false")
	}
	if len(toolCalls) != 0 {
		t.Fatalf("toolCalls = %+v, want none", toolCalls)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsPrefersCheckpointTargetOverHistoricalSiblingArtifact(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	targetPath := "Test/oc-21-boundary-test-design.md"
	siblingPath := "Test/oc-26-pipeline-capacity-test-spec.md"
	turnID := uuid.New()
	priorTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     21,
		Title:          "Design boundary test: rate limits and max pipeline capacity",
		WorkStatus:     "blocked",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "durable_recovery_checkpoint",
				"target_path":    targetPath,
				"failure_reason": "assistant draft for Test/oc-21-boundary-test-design.md repeated a generic recovery reply instead of the file body",
			},
		}),
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	targetAbs := filepath.Join(dataDir, "workspaces", projectSlug, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	targetBody := strings.TrimSpace(`# Boundary Test Design

## Rate Limit Threshold
- Submit 120 requests within one minute and expect HTTP 429 after the configured limit is crossed.
`)
	if err := os.WriteFile(targetAbs, []byte(targetBody+"\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path":    siblingPath,
				"content": "# OC-26: Pipeline Max Capacity Threshold Test Specification\n\nSubstantive sibling content.\n",
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	narration := "I'll start work on OC-21 by examining the task context, understanding the previous rejection, and designing the boundary test scenario for rate limits and capacity constraints.\n\n" +
		"Let me first get the full task details and understand what was previously rejected:"

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		narration,
		[]ModelToolCall{{
			ID:   "write-1",
			Name: "file.write",
			Tier: "tier2",
			Arguments: map[string]any{
				"path":    siblingPath,
				"content": narration,
			},
		}},
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want checkpoint target %q", got, targetPath)
	}
	if got := stringValue(toolCalls[0].Arguments["content"]); !strings.Contains(got, "## Rate Limit Threshold") {
		t.Fatalf("content = %q, want checkpoint target draft", got)
	}
}

func TestMaybeSynthesizeRecoveryFileWriteToolCallsSkipsReviewLane(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:         taskID,
		TaskNumber: 13,
		Title:      "Document success criteria",
		WorkStatus: "review",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":        1,
				"blocker_class":  "durable_recovery_checkpoint",
				"target_path":    "OC-13-PASS-FAIL-CHECKLIST.md",
				"failure_reason": "review lane recovery",
			},
		}),
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeRecoveryFileWriteToolCalls(
		context.Background(),
		rt,
		"I'm the reviewer and will decide after inspecting the deliverables.",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeRecoveryFileWriteToolCalls: %v", err)
	}
	if synthesized {
		t.Fatal("synthesized = true, want false for review-lane recovery")
	}
	if len(toolCalls) != 0 {
		t.Fatalf("toolCalls = %+v, want none", toolCalls)
	}
}

func TestMaybeSynthesizeTaskExecutionFileWriteToolCallsUsesInferredDraftOnGenericKickoffReply(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Execute the happy-path scenario end-to-end against the real speaker pipeline product. Capture screenshots, logs, and evidence of successful completion at each step."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     17,
		Title:          "Execute happy-path scenario",
		WorkStatus:     "in_progress",
		Description:    &description,
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeTaskExecutionFileWriteToolCalls(
		context.Background(),
		rt,
		"Excellent! Now I have everything I need. Let me create a comprehensive execution log and begin the happy-path scenario.",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeTaskExecutionFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one synthesized file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != "Test/test-execution-oc17-happy-path-scenario.md" {
		t.Fatalf("path = %q, want canonical task execution log target", got)
	}
	if got := stringValue(toolCalls[0].Arguments["content"]); !strings.Contains(got, "## Test Cases") {
		t.Fatalf("content = %q, want inferred execution-log draft", got)
	}
}

func TestMaybeSynthesizeTaskExecutionFileWriteToolCallsOverridesBadImprovisedWrite(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Execute edge-case scenarios end-to-end against the real speaker pipeline product. Test incomplete data, rejections, resubmissions, and error recovery. Capture evidence at each step."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     18,
		Title:          "Execute edge-case scenarios",
		WorkStatus:     "in_progress",
		Description:    &description,
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeTaskExecutionFileWriteToolCalls(
		context.Background(),
		rt,
		"Let me create the execution log with a new filename:",
		[]ModelToolCall{{
			ID:   "write-1",
			Name: "file.write",
			Tier: "tier2",
			Arguments: map[string]any{
				"path":    "test/OC-18-EDGE-CASE-EXECUTION-PLAN.md",
				"content": "Perfect! Now I have the complete edge-case execution plan.",
			},
		}},
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeTaskExecutionFileWriteToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "file.write" {
		t.Fatalf("toolCalls = %+v, want one synthesized file.write", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["path"]); got != "Test/test-execution-oc18-edge-case-scenarios.md" {
		t.Fatalf("path = %q, want canonical edge-case execution log target", got)
	}
}

func TestMaybeSynthesizeTaskExecutionFileWriteToolCallsSkipsReviewLane(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Execute the happy-path scenario end-to-end against the real speaker pipeline product. Capture screenshots, logs, and evidence of successful completion at each step."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		OrganizationID: fixture.session.OrganizationID,
		TaskNumber:     17,
		Title:          "Execute happy-path scenario",
		WorkStatus:     "review",
		Description:    &description,
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeTaskExecutionFileWriteToolCalls(
		context.Background(),
		rt,
		"Now I'll record my review decision with the review-scoped artifact:",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeTaskExecutionFileWriteToolCalls: %v", err)
	}
	if synthesized {
		t.Fatal("synthesized = true, want false for review lane")
	}
	if len(toolCalls) != 0 {
		t.Fatalf("toolCalls = %+v, want none", toolCalls)
	}
}

func TestMaybeSynthesizeTaskReviewDecisionToolCallsUsesExplicitRejectDecision(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	executionID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:         taskID,
		TaskNumber: 17,
		Title:      "Execute happy-path scenario",
		WorkStatus: "review",
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeTaskReviewDecisionToolCalls(
		context.Background(),
		rt,
		"## REVIEW ASSESSMENT\n\nThe deliverable is incomplete.\n\nDECISION: REJECT",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeTaskReviewDecisionToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "flow.review_decision" {
		t.Fatalf("toolCalls = %+v, want one flow.review_decision", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["decision"]); got != "reject" {
		t.Fatalf("decision = %q, want reject", got)
	}
	if got := stringValue(toolCalls[0].Arguments["flow_node_execution_id"]); got != executionID.String() {
		t.Fatalf("flow_node_execution_id = %q, want %s", got, executionID)
	}
}

func TestMaybeSynthesizeTaskReviewDecisionToolCallsSkipsWhenDecisionToolAlreadyPresent(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	executionID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:         taskID,
		TaskNumber: 17,
		Title:      "Execute happy-path scenario",
		WorkStatus: "review",
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	original := []ModelToolCall{{
		ID:   "review-1",
		Name: "flow.review_decision",
		Tier: "tier2",
		Arguments: map[string]any{
			"flow_node_execution_id": executionID.String(),
			"decision":               "reject",
		},
	}}
	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeTaskReviewDecisionToolCalls(
		context.Background(),
		rt,
		"DECISION: REJECT",
		original,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeTaskReviewDecisionToolCalls: %v", err)
	}
	if synthesized {
		t.Fatal("synthesized = true, want false")
	}
	if len(toolCalls) != 1 || toolCalls[0].ID != "review-1" {
		t.Fatalf("toolCalls = %+v, want original tool call", toolCalls)
	}
}

func TestMaybeSynthesizeTaskReviewDecisionToolCallsInfersRejectFromStrongFindings(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	executionID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:         taskID,
		TaskNumber: 10,
		Title:      "Analyze Results and Sign Off",
		WorkStatus: "review",
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	toolCalls, synthesized, err := fixture.engine.maybeSynthesizeTaskReviewDecisionToolCalls(
		context.Background(),
		rt,
		"The work deliverable file exists but contains only placeholder text. The required planning artifacts are missing from the workspace.",
		nil,
	)
	if err != nil {
		t.Fatalf("maybeSynthesizeTaskReviewDecisionToolCalls: %v", err)
	}
	if !synthesized {
		t.Fatal("synthesized = false, want true")
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "flow.review_decision" {
		t.Fatalf("toolCalls = %+v, want one flow.review_decision", toolCalls)
	}
	if got := stringValue(toolCalls[0].Arguments["decision"]); got != "reject" {
		t.Fatalf("decision = %q, want reject", got)
	}
}

func TestEnsureRecoveryTurnDurableTaskStateFailsWithoutTaskTransitions(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				Title:          "Recovery blocked task",
				WorkStatus:     "in_progress",
				Metadata:       json.RawMessage(`{"existing":"value"}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = nil

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID: uuid.New(),
		},
		recoveryBlockReason: "provider auth failed",
	}

	err := fixture.engine.ensureRecoveryTurnDurableTaskState(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), errMissingTaskTransitionServiceForRecoveryBlock) {
		t.Fatalf("ensureRecoveryTurnDurableTaskState error = %v, want contains %q", err, errMissingTaskTransitionServiceForRecoveryBlock)
	}

	taskRecord, getErr := taskRepo.GetByID(context.Background(), taskID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
}

func TestEnsureRecoveryTurnDurableTaskStateIgnoresQueuedInvalidTransition(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				Title:          "Recovery resumed task",
				WorkStatus:     "in_progress",
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{
		repo: taskRepo,
		err:  tasksvc.ErrInvalidStatusTransition{From: "queued", To: "blocked"},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID: uuid.New(),
		},
		recoveryBlockReason: "provider auth failed",
	}

	taskRecord := taskRepo.items[taskID]
	taskRecord.WorkStatus = "queued"
	taskRepo.items[taskID] = taskRecord

	if err := fixture.engine.ensureRecoveryTurnDurableTaskState(context.Background(), rt); err != nil {
		t.Fatalf("ensureRecoveryTurnDurableTaskState error = %v, want nil", err)
	}
}

func TestHandleValidationLoopBlockIgnoresQueuedInvalidTransition(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				Title:          "Queued validation task",
				WorkStatus:     "queued",
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{
		repo: taskRepo,
		err:  tasksvc.ErrInvalidStatusTransition{From: "queued", To: "blocked"},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	calls := []ToolCall{{ID: "read-1", Name: "file.read"}}
	results := []ToolResult{{ToolCallID: "read-1", Name: "file.read", Error: "recovery_target_focus_required"}}

	handled, err := fixture.engine.handleToolValidationResults(context.Background(), rt, calls, results)
	if err != nil {
		t.Fatalf("handleAgentTurnValidationFailures error = %v, want nil", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
}

func TestEnqueueAgentTurnIfActiveSuppressesRepeatedRecoveryRetryForSameMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      uuid.New(),
				WorkStatus:     "review",
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnRecord, _, err := fixture.engine.turns.CreateForMessageAttempt(context.Background(), fixture.session.ID, agentID, fixture.userMessageID, 4)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	stopReason := stopReasonRecoveryFileRejected
	if _, err := fixture.engine.turns.SetStopReason(context.Background(), turnRecord.ID, &stopReason); err != nil {
		t.Fatalf("SetStopReason: %v", err)
	}
	if err := fixture.chat.CompleteTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	enqueued, err := fixture.engine.enqueueAgentTurnIfActive(context.Background(), fixture.session, AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: 5,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive: %v", err)
	}
	if enqueued {
		t.Fatal("expected enqueue to be suppressed for completed recovery halt")
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent turn jobs = %d, want 0", len(jobs))
	}
}

func TestEnqueueAgentTurnIfActiveAddsFlowNodeExecutionIDFromSessionMetadata(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	executionID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": executionID.String(),
	})

	enqueued, err := fixture.engine.enqueueAgentTurnIfActive(context.Background(), fixture.session, AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive: %v", err)
	}
	if !enqueued {
		t.Fatal("expected enqueue to succeed")
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.FlowNodeExecutionID == nil || *jobs[0].payload.FlowNodeExecutionID != executionID {
		t.Fatalf("enqueued flow_node_execution_id = %v, want %s", jobs[0].payload, executionID)
	}
}

func TestEnqueueAgentTurnIfActivePreservesProvidedFlowNodeExecutionID(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	sessionExecutionID := uuid.New()
	providedExecutionID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": sessionExecutionID.String(),
	})

	enqueued, err := fixture.engine.enqueueAgentTurnIfActive(context.Background(), fixture.session, AgentTurnPayload{
		SessionID:           fixture.session.ID,
		MessageID:           fixture.userMessageID,
		FlowNodeExecutionID: &providedExecutionID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive: %v", err)
	}
	if !enqueued {
		t.Fatal("expected enqueue to succeed")
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.FlowNodeExecutionID == nil || *jobs[0].payload.FlowNodeExecutionID != providedExecutionID {
		t.Fatalf("enqueued flow_node_execution_id = %v, want %s", jobs[0].payload, providedExecutionID)
	}
}

func TestCancelRecoveryResumeDispatchMarksInitialMessageCancelled(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	rt := &turnRuntime{
		initialMessageID: fixture.userMessageID,
	}
	if err := fixture.engine.cancelRecoveryResumeDispatch(context.Background(), rt, "recovery halted after repeated non-substantive drafts"); err != nil {
		t.Fatalf("cancelRecoveryResumeDispatch: %v", err)
	}

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !chat.AgentTurnDispatchCancelled(message.Metadata) {
		t.Fatalf("expected cancelled dispatch metadata, got %s", string(message.Metadata))
	}
}

func TestRecoveryFileWriteDraftRejectReason(t *testing.T) {
	const targetPath = "docs/content-strategy.md"

	cases := []struct {
		name       string
		content    string
		targetPath string
		want       string
	}{
		{
			name: "rejects tool repair narration",
			content: "I can see the problem clearly from the conversation history. " +
				"Every `file_write` call has been emitted without the `content` parameter populated. " +
				"Let me do this now.",
			want: "tool-recovery troubleshooting",
		},
		{
			name: "rejects intent narration placeholder",
			content: "Now I have everything I need. Let me write the comprehensive content strategy document. " +
				"This needs to be the single deliverable that unblocks WS4 and serves as the strategic foundation for Sam.blog.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects shallow placeholder progress narration",
			content: "Time to write the comprehensive content strategy document for Sam.blog. " +
				"This is the critical deliverable that unblocks WS4. " +
				"The next step is to capture the audience, pillars, tone, and publishing cadence so the project can move forward.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects recovery context placeholder narration",
			content: "Excellent. I now have the full strategy context. " +
				"The recovery state indicates I should write the actual migration plan deliverable (the main output document). " +
				"Let me check what target file was specified:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects complete picture placeholder narration",
			content: "Excellent! Now I have a complete picture. " +
				"The draft document exists but is incomplete. " +
				"Let me resume work by completing the accessibility standards document. " +
				"I'll continue from where it was cut off and finish all remaining sections:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects task context placeholder narration",
			content: "Perfect! Now I understand the task context and I need to resume OC-28 correctly. " +
				"Let me now create the comprehensive accessibility standards document that unblocks the design system workstream.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects recovery target narration placeholder",
			content: "The recovery target is `Test/oc-21-boundary-test-design.md`. " +
				"Task OC-21 is in the Work node for designing boundary tests for rate limits and max pipeline capacity. " +
				"Writing the comprehensive boundary test design specification now:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects recovery placeholder narration",
			content: "Perfect. I understand the recovery state. The file exists but is just a placeholder. " +
				"Let me now write the comprehensive boundary test design specification based on the task requirements.",
			want: "instead of the file body",
		},
		{
			name: "rejects task-context inspection placeholder",
			content: "I'll start work on OC-21 by examining the task context, understanding the previous rejection, and designing the boundary test scenario for rate limits and capacity constraints.\n\n" +
				"Let me first get the full task details and understand what was previously rejected:",
			targetPath: "Test/oc-21-boundary-test-design.md",
			want:       "inspect task context instead of the file body",
		},
		{
			name: "rejects review rejection memo reused as execution draft",
			content: "## REVIEW FINDINGS — OC-15: Error Handling & Rollback Test\n\n" +
				"### Critical Quality Gate Violation: PRIMARY DELIVERABLE MISSING\n\n" +
				"**Decision**: ❌ **REJECT**\n\n" +
				"## REWORK GUIDANCE\n\n" +
				"Execute the missing error handling scenarios and resubmit the actual deliverable.",
			targetPath: "error_handling_test.md",
			want:       "review or rejection memo",
		},
		{
			name: "rejects full context workspace check placeholder narration",
			content: "Perfect. Now I have the full context. The OC-15 strategy work is complete with four locked artifacts. " +
				"Now I need to create the actual content migration plan deliverable. Let me check what's in the workspace currently and then write the migration plan document:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects resume by completing placeholder narration",
			content: "Perfect. Now I have full context. Let me resume the task by completing the migration plan document. " +
				"Based on the strategy artifacts and the checkpoint directive, I need to write a comprehensive migration plan that operationalizes these decisions.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects recovered full context planning placeholder narration",
			content: "Perfect. I have recovered the full context. The planning artifacts are complete and comprehensive. " +
				"Now I need to create the infrastructure spec and deployment checklist. Let me draft the comprehensive infrastructure specification:",
			want: "intent to write the deliverable",
		},
		{
			name:    "rejects placeholder file reread narration",
			content: "The target file contains only a placeholder stub. I need to read the checkpoint artifacts to understand what content model and editorial structure has been defined, then write the substantive migration plan.",
			want:    "intent to write the deliverable",
		},
		{
			name:    "rejects recovery artifact reread narration",
			content: "I'll read the target file and recovery artifact to assess the current state, then write the substantive migration plan content.",
			want:    "intent to write the deliverable",
		},
		{
			name: "rejects structured context summary placeholder narration",
			content: "Excellent. Now I have enough context. The strategy for OC-15 is locked with:\n" +
				"- **Staged cutover** (Tier 1 -> Tier 2 -> Archives)\n" +
				"- **Tier classification** based on voice audit (OC-13)\n" +
				"- **Extraction -> Transformation -> Validation** workflow\n" +
				"- **Markdown + YAML front-matter** target format\n" +
				"- **301 redirects** for SEO preservation\n\n" +
				"Now I'll write the comprehensive migration plan document. This is the deliverable that translates strategy into executability.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects generic recovery question echo",
			content: "I'm ready to help with the SAM.blog rebuild infrastructure task (OC-24).\n\n" +
				"I can see this is a durable recovery checkpoint with the success narrative partially drafted. What would you like me to do?\n\n" +
				"**Quick clarification:**\n" +
				"- Continue drafting the success narrative (complete the cutover scenario)?\n" +
				"- Move to the decision log or tradeoff matrix?\n" +
				"- Read existing planning artifacts to ground the infrastructure spec?\n\n" +
				"Please let me know the most useful next step.",
			want: "generic recovery reply",
		},
		{
			name: "rejects structured status report placeholder narration",
			content: "Perfect! I can see:\n" +
				"- **Current flow node:** 8cc3df4d (Work)\n" +
				"- **Status:** in_progress\n" +
				"- **Strategy artifacts:** All 4 required artifacts are present and locked\n" +
				"- **Target file:** planning/prd-spec/oc-24-infrastructure-spec.md (partially drafted)\n\n" +
				"The task is in the **Work phase**. I need to complete the infrastructure spec file. Let me check the full file to see what sections still need content:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects locked decisions task flow placeholder narration",
			content: "Perfect. I have all four strategy artifacts. The decisions are locked and clear. " +
				"Now I need to move forward to the infrastructure spec deliverable. " +
				"Let me check the task flow and understand what step we're on:",
			want: "generic recovery reply",
		},
		{
			name: "rejects drafting confirmation placeholder narration",
			content: "I'm ready to continue on OC-24: Plan hosting and infrastructure. " +
				"Before I proceed with drafting the infrastructure spec, I need to confirm the locked decisions are still valid.",
			want: "generic recovery reply",
		},
		{
			name: "rejects current state examination placeholder narration",
			content: "I'll help you resume work on OC-24: Plan hosting and infrastructure. " +
				"Let me first examine the current state of the project and task.",
			want: "generic recovery reply",
		},
		{
			name:    "rejects target file first placeholder narration",
			content: "I need to read the target file first to understand what work has already been persisted, since the recovery instruction indicates a target file path but the inline draft above is non-substantive.",
			want:    "intent to write the deliverable",
		},
		{
			name: "rejects full context acceptance criteria placeholder narration",
			content: "Excellent! Now I understand the full context. I need to complete OC-28 by writing the comprehensive accessibility standards document. " +
				"Let me check the acceptance criteria and dependency log as well:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects work node execution placeholder narration",
			content: "Good. I have the context. OC-15 (content migration) is in the Work node and needs execution. " +
				"Now write the migration plan:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects clear understanding deliverable placeholder",
			content: "Good. Now I have a clear understanding of the requirements. Let me create the Speaker Validation Agent with:\n" +
				"1. Comprehensive system prompt defining the agent's role and behavior\n" +
				"2. Validation logic implementation covering all criteria\n" +
				"3. Structured validation report format\n" +
				"4. Implementation details and testing guidance\n\n" +
				"I'll create the deliverable as a production-ready Python module with the validation agent:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects current state summary validation placeholder",
			content: "Now let me understand the current state and begin working on the validation task. Based on my review:\n\n" +
				"**Current State Summary:**\n" +
				"- OC-12 (this task) is in \"Intake & Planning\" phase\n" +
				"- I need to validate OC-10 schema against acceptance criteria\n" +
				"- Planning artifacts are well-defined\n\n" +
				"Let me start by populating the implementation plan and dependency log, then proceed to the validation work:",
			targetPath: "planning/prd-spec/oc-12-validation-report.md",
			want:       "intent to write the deliverable",
		},
		{
			name: "rejects clear picture flow execution placeholder",
			content: "Now I have a clear picture. The task OC-15 is to generate the validation report and recommendations. " +
				"The planning artifacts are currently scaffolds, but I have actual validation testing work and implementation specs.\n\n" +
				"Let me check the flow execution to see where we are in the process:",
			targetPath: "deliverables/oc-15-validation-report.md",
			want:       "intent to write the deliverable",
		},
		{
			name:    "rejects short imperative write stub",
			content: "Now write the full migration plan:",
			want:    "intent to write the deliverable",
		},
		{
			name: "rejects structured checkpoint list placeholder",
			content: "Good. I have the context. OC-15 (content migration) is in the **Work** node and needs execution. The checkpoint indicates I should:\n\n" +
				"1. **Not** re-list workspace state or create helper scripts (they exist)\n" +
				"2. **Read** persisted artifacts directly\n" +
				"3. **Write at least one migrated output file** before expanding\n" +
				"4. Start from the recovery checkpoints and target file draft\n\n" +
				"Let me read the OC-15 strategy artifacts to understand the editorial direction, then write the first complete output file: the migration plan.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects structured execute context placeholder",
			content: "Excellent. Now I understand the context:\n" +
				"- I'm in the **Execute & Build** phase (active)\n" +
				"- Planning artifacts are complete and approved\n" +
				"- I need to create the actual deliverable: **deliverables/oc-13-speaker-validation-agent.md**\n" +
				"- This file should contain the system prompt, validation logic, report schema, test cases, and integration guidance\n\n" +
				"Let me now create a comprehensive Speaker Validation Agent deliverable that meets all acceptance criteria:",
			targetPath: "deliverables/oc-13-speaker-validation-agent.md",
			want:       "intent to write the deliverable",
		},
		{
			name: "rejects review assessment placeholder",
			content: "Perfect. Now I have a comprehensive understanding of the deliverables. Let me assess the quality against the task's acceptance criteria. Let me prepare a detailed review now.\n\n" +
				"## Review Assessment: OC-13 Build Speaker Validation Agent\n\n" +
				"I am now conducting a **Quality Review** of the deliverable set against the task requirements.",
			targetPath: "deliverables/oc-13-speaker-validation-agent.md",
			want:       "intent to write the deliverable",
		},
		{
			name: "rejects structured locked decisions placeholder",
			content: "Perfect. I have all four strategy artifacts. The decisions are **locked and clear**:\n\n" +
				"- **Hosting:** Vercel Pro\n" +
				"- **CDN:** Vercel's integrated global CDN\n" +
				"- **DNS:** Vercel DNS\n\n" +
				"Now I need to move forward to the **infrastructure spec** deliverable. Let me check the task flow and understand what step we're on:",
			want: "instead of the file body",
		},
		{
			name: "rejects complete context execution placeholder",
			content: "Excellent. Now I have the complete context. The strategy phase for OC-15 is complete with all 4 artifacts locked. " +
				"Now I need to read the tradeoff matrix to understand the full decision context, then resume execution by creating the actual migration plan document.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects flow state status placeholder",
			content: "The task shows `current_flow_node_id: 8cc3df4d-91c3-4af7-9b45-a15adf0054dc` but the execution doesn't exist yet. " +
				"This means the task is in progress but hasn't yet entered the active flow node execution state.\n\n" +
				"Let me check what flow template is assigned:",
			want: "runtime status analysis instead of the file body",
		},
		{
			name: "rejects flow execution id request placeholder",
			content: "I need the flow node execution ID to continue. Can you provide the flow_node_execution_id for the current task node?\n\n" +
				"Alternatively, if you'd like me to check the current task state, I can retrieve OC-24 and determine the active flow node:",
			want: "runtime control-plane input instead of the file body",
		},
		{
			name: "allows validation plan checklist that mentions flow node execution id",
			content: "# First-Wave Task Execution & Flow Advancement Validation Plan\n\n" +
				"## Flow Node Advancement Validation\n" +
				"- [ ] flow_get_execution returns active node = review node after work complete\n" +
				"- [ ] flow_advance() callable with valid flow_node_execution_id\n" +
				"- [ ] Transition to review node preserves all task state\n",
			targetPath: "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md",
			want:       "",
		},
		{
			name: "rejects operator choice question placeholder",
			content: "I'm Alex, Technical Lead for the SAM.blog rebuild. I'm actively working on **OC-24: Plan hosting and infrastructure**, currently in the Work phase.\n\n" +
				"What is the current state you need me to continue from?\n\n" +
				"- Do you want me to complete the deployment checklist that was partially drafted?\n" +
				"- Or do you need me to start fresh on the full infrastructure specification?\n\n" +
				"The recovery context shows the deployment checklist draft exists, but I should confirm whether that's the priority.",
			want: "asked the operator to choose the next step instead of the file body",
		},
		{
			name: "rejects target deliverable clarification placeholder",
			content: "I'm ready to execute task OC-11 (Document as workflow specification). Based on the recovery checkpoint, I need to:\n\n" +
				"1. **Create the main workflow specification** in `deliverables/oc-15-validation-workflow.md`\n" +
				"2. **Copy/reference the schema** from planning to deliverables\n\n" +
				"However, I notice the recovery context references **OC-15** (validation workflow) but the active task is **OC-11** (document as workflow specification). Let me clarify:\n\n" +
				"**What is the target deliverable for OC-11?**\n" +
				"- Should I create `deliverables/oc-11-workflow-specification.md`?\n" +
				"- Or is OC-11 supposed to document OC-15's workflow?\n\n" +
				"Once you confirm the correct target file path and scope, I'll immediately execute the workflow specification documentation with all required sections from the implementation plan.",
			targetPath: "deliverables/oc-11-validation-workflow-spec.md",
			want:       "asked the operator to choose the next step instead of the file body",
		},
		{
			name: "rejects role introduction proceed questionnaire placeholder",
			content: "I'm Lina Farooqi, AI Workflow Designer, ready to execute **OC-11: Document as workflow specification** for Speaker Pipeline Ops Validation Fresh 5.\n\n" +
				"**Current Status:**\n" +
				"- Task in `in_progress` at Work flow node\n" +
				"- Active execution ID: 90b4601c-7605-4874-8470-2dbd0f89a168\n" +
				"- Pending: Internal Review -> Merge\n\n" +
				"**What I need to proceed:**\n" +
				"1. Should I inspect the planning artifacts (PRD, acceptance criteria, implementation plan, dependency log) to understand the specification scope?\n" +
				"2. Should I start drafting the workflow specification document, or do you have specific requirements/constraints?\n" +
				"3. Who should review the completed specification once drafted?",
			targetPath: "deliverables/oc-11-validation-workflow-spec.md",
			want:       "generic recovery reply",
		},
		{
			name: "rejects task situation reread placeholder",
			content: "Now I understand the situation. The task OC-11 is meant to document the workflow specification. The deliverable file currently at `deliverables/oc-11-validation-workflow-spec.md` is incomplete (788 bytes, just a question).\n\n" +
				"Let me check what the planned deliverables are according to the reference files and then complete the actual workflow specification document:",
			targetPath: "deliverables/oc-11-workflow-specification.md",
			want:       "intent to write the deliverable",
		},
		{
			name:       "rejects target file placeholder narrative",
			content:    "The target file currently contains placeholder narrative (366 bytes) instead of the substantive workflow specification. The acceptance criteria document confirms the deliverable should be a comprehensive 11-section workflow specification for speaker pipeline validation discovery (target size 13.3 KB). I need to write the actual workflow specification content now.",
			targetPath: "deliverables/oc-11-workflow-specification.md",
			want:       "intent to write the deliverable",
		},
		{
			name:       "rejects current state and planning reread placeholder",
			content:    "I need to check what the current state of the target file is, then read the planning artifacts to understand the workflow specification requirements before writing the deliverable.",
			targetPath: "deliverables/oc-11-workflow-specification.md",
			want:       "intent to write the deliverable",
		},
		{
			name: "rejects task-list structure placeholder narration",
			content: "Perfect! I can now see the structure. Looking at the task list, there are 5 test cases (OC-9 through OC-14 would give me the parent and child test cases). " +
				"The five test cases appear to be:\n" +
				"- OC-9: Parent-Child Task Persistence Validation\n" +
				"- OC-12, OC-13, OC-14: Child test cases\n" +
				"- OC-11 (already completed): Persistence validation report which analyzed these\n\n" +
				"Let me read the task for OC-9 (parent test) to understand the scope:",
			targetPath: "deliverables/oc-10-test-synthesis-report.md",
			want:       "intent to write the deliverable",
		},
		{
			name: "rejects bold status workflow summary placeholder",
			content: "**Status**: Task OC-11 is currently **in_progress** at the **Work** flow step. The target deliverable file is just a placeholder (180 bytes). I need to create the complete workflow specification.\n\n" +
				"Based on the planning artifacts, I understand that OC-11 requires:\n\n" +
				"1. **oc-11-workflow-specification.md** (13.3 KB, 11 comprehensive sections) - The main deliverable\n" +
				"2. A formal specification covering validation workflow, data model, quality gates, and decision framework\n\n" +
				"Let me now create the complete workflow specification:",
			targetPath: "deliverables/oc-11-workflow-specification.md",
			want:       "intent to write the deliverable",
		},
		{
			name: "rejects numbered validation status inventory placeholder",
			content: "Perfect. I can see:\n" +
				"1. **OC-13 is the synthesis task** (in_progress)\n" +
				"2. **Three completed validation tasks** (OC-10, OC-11, OC-12 all done)\n" +
				"3. **OC-12 report exists** with comprehensive speaker profile quality validation\n" +
				"4. **Planning artifact for OC-13 is complete**\n" +
				"5. **Target file is currently a placeholder** — needs to be overwritten with substantive synthesis report\n\n" +
				"Let me read the OC-10 and OC-11 reports to gather all findings:",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "asked the operator to choose the next step instead of the file body",
		},
		{
			name:       "rejects file write failed due to missing content narration",
			content:    "I see the file_write failed due to missing content parameter. Let me create the comprehensive workflow specification now:",
			targetPath: "deliverables/oc-11-workflow-specification.md",
			want:       "intent to write the deliverable",
		},
		{
			name:       "rejects content parameter complete specification narration",
			content:    "I need to provide the content parameter for the file write. Let me create the complete workflow specification:",
			targetPath: "deliverables/oc-11-workflow-specification.md",
			want:       "intent to write the deliverable",
		},
		{
			name:       "rejects actual content let me write specification narration",
			content:    "I need to provide the actual content. Let me write the complete workflow specification:",
			targetPath: "deliverables/oc-11-workflow-specification.md",
			want:       "intent to write the deliverable",
		},
		{
			name:    "rejects recovery context reread placeholder",
			content: "Perfect. I now have the recovery context. Let me check the strategy artifacts to understand the locked decisions before proceeding:",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name:    "rejects durable draft preface placeholder",
			content: "I'll write the deployment checklist to the target file now using the substantive draft provided above.",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name: "rejects continuation summary spec preface",
			content: "I'll continue the OC-24 infrastructure planning task by writing the concrete deliverables directly based on the continuation summary draft.\n\n" +
				"Let me write the infrastructure specification first:",
			want: "intent to write the deliverable",
		},
		{
			name:    "rejects deployment checklist progress preface",
			content: "Good. The spec file is partially complete. Now I need to write the complete deployment checklist, which is the critical missing deliverable. Let me create that:",
			want:    "intent to write the deliverable",
		},
		{
			name:    "rejects migration plan status placeholder",
			content: "Perfect! Now I see the situation clearly:\n\n1. **OC-15 has strategy artifacts already locked** (strategy brief, decision log, tradeoff matrix, success narrative)\n2. **The migration plan file is stubbed but incomplete**\n3. **I need to deliver the full migration plan** based on the locked strategy\n\nLet me read the strategy artifacts first, then write the complete migration plan:",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name:    "rejects replace stub imperative",
			content: "Now let me replace it with the complete migration plan:",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name:    "rejects delete recreate imperative",
			content: "Let me try a different approach - delete and recreate:",
			want:    "intent to write the deliverable",
		},
		{
			name: "rejects implementation preface with build list",
			content: "Since the prior tasks are still in draft and I don't have the data schema yet, I'll design the reporting script based on the task description requirements and best practices for pipeline analytics. Let me create a comprehensive, well-structured reporting script that's ready to integrate with upstream data pipelines.\n\n" +
				"I'll build:\n" +
				"1. **Core reporting module** with data validation\n" +
				"2. **Report generators** for each metric type\n" +
				"3. **HTML and Markdown templates** with styling\n" +
				"4. **Example outputs** demonstrating all report types\n" +
				"5. **Clear documentation** for operators\n\n" +
				"Let me start:",
			targetPath: "src/generate_reports.py",
			want:       "implementation plan instead of the file body",
		},
		{
			name: "rejects reporting scope summary preface",
			content: "Perfect. Now I understand the full scope. Based on the task description and the planning artifacts, I need to build `src/generate_reports.py` that generates reports for:\n" +
				"- Pipeline health\n" +
				"- Scoring distribution\n" +
				"- Outreach status\n" +
				"- Top-ranked opportunities\n" +
				"- Funnel metrics\n\n" +
				"Let me create a comprehensive, production-ready reporting script:",
			targetPath: "src/generate_reports.py",
			want:       "implementation plan instead of the file body",
		},
		{
			name: "rejects wrapped tool status for different file",
			content: "File written: `planning/metrics-framework/oc-16-review-cadence.md` (5.9 KB).\n\n" +
				"The metrics review cadence is now durable and operationalized. It defines weekly and monthly reviews.",
			targetPath: "src/generate_reports.py",
			want:       "different deliverable",
		},
		{
			name: "rejects wrapped tool status for same file",
			content: "File written: `src/generate_reports.py` (5.9 KB).\n\n" +
				"The reporting script is now durable and operationalized.",
			targetPath: "src/generate_reports.py",
			want:       "wrapped tool status instead of the file body",
		},
		{
			name: "rejects file write parameter troubleshooting without let-me phrasing",
			content: "`file_write` without `content` parameter is causing a placeholder to be written. I must provide the full Python script as the content parameter.\n\n" +
				"The concrete blocker: `file_write` requires a non-empty `content` parameter; I cannot proceed without drafting the complete generate_reports.py body and passing it in the `content` field.",
			targetPath: "src/generate_reports.py",
			want:       "tool-recovery troubleshooting",
		},
		{
			name:       "rejects file write function signature troubleshooting",
			content:    "I'm encountering a tool parameter validation issue with file_write-it requires the `content` parameter but the function signature isn't accepting my input correctly. Let me use file_edit instead to replace the placeholder with the full specification:",
			targetPath: "deliverables/oc-11-validation-workflow-spec.md",
			want:       "tool-recovery troubleshooting",
		},
		{
			name:       "rejects file write content parameter narration",
			content:    "I need to provide the content parameter for file_write. Here is the complete workflow specification:",
			targetPath: "deliverables/oc-11-validation-workflow-spec.md",
			want:       "tool-recovery troubleshooting",
		},
		{
			name: "rejects operator-choice status questionnaire",
			content: "I'm standing by to validate the Speaker Pipeline Ops workflow. I've reviewed the context:\n\n" +
				"**Current Task**: OC-13 (Synthesize Validation Findings & Report) - In progress, Work node active\n\n" +
				"**What I need from you**:\n" +
				"1. Should I begin OC-13 execution?\n" +
				"2. Or should I prioritize unblocking OC-19 and OC-26 first?\n" +
				"3. Or would you like me to investigate and report the current bottleneck status?\n\n" +
				"**Concise ask**: What's your priority?",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "asked the operator to choose the next step instead of the file body",
		},
		{
			name: "rejects clarification-heavy continuation summary",
			content: "# Validation Findings & Report Synthesis\n\n" +
				"## Task Status: IN PROGRESS\n\n" +
				"Before I proceed, I need clarification on:\n\n" +
				"1. **Source Data**:\n" +
				"   - Do you have existing validation findings/logs to consolidate?\n" +
				"2. **Severity Definitions**:\n" +
				"   - What specific criteria define each severity level?\n" +
				"3. **Documentation Format**:\n" +
				"   - Preferred output format?\n" +
				"   - Audience level: Executive, Technical, or Mixed?\n",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "asked for clarification instead of the file body",
		},
		{
			name:       "rejects continuation summary fallback text",
			content:    "Task execution is already underway. Reuse the existing workspace files, task state, prior tool results, and recent artifacts from this session to continue the active task directly.",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "continuation-summary fallback instead of the file body",
		},
		{
			name:       "rejects missing durable draft continuation summary",
			content:    "I don't see a durable draft for the OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md file in the context provided above. Please provide the substantive draft or recovery artifact for this task so I can write the target file directly.",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "runtime control-plane input instead of the file body",
		},
		{
			name: "rejects ready-to-continue confirmation stub",
			content: "# Ready to Continue OC-13 Validation Synthesis\n\n" +
				"**Current Task**: OC-13: Synthesize Validation Findings & Report\n" +
				"**Deliverable Target**: Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md\n\n" +
				"**What I can see**:\n- Prior tasks completed\n- Planning artifacts available\n\n" +
				"**What I need from you**:\n" +
				"1. Should I proceed with reading the planning artifacts?\n" +
				"2. Do you want me to compile findings from the workspace artifacts into the synthesis report immediately?\n\n" +
				"I'll move quickly once you confirm direction.",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "asked the operator to confirm execution direction instead of the file body",
		},
		{
			name: "rejects oc-13 work plan continuation summary",
			content: "# Task: Synthesize Validation Findings & Report\n\n" +
				"## Current Status: IN PROGRESS\n\n" +
				"I'm beginning work on consolidating validation findings into a comprehensive report.\n\n" +
				"## Work Plan\n\n" +
				"### Phase 1: Findings Consolidation (Current)\n" +
				"- Gather all validation findings from previous testing phases\n\n" +
				"### Phase 2: Categorization by Severity\n" +
				"- Blocker issues\n\n" +
				"## Notes on Previous Rejection\n" +
				"Previous flow step was rejected. To proceed effectively, I need clarification.\n",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "repeated a generic recovery reply instead of the file body",
		},
		{
			name: "rejects oc-13 clarification questionnaire",
			content: "I'm ready to help you synthesize the validation findings for OC-13. Before I proceed, I need to clarify one thing:\n\n" +
				"**What's the current state of the target file?**\n\n" +
				"Should I:\n" +
				"1. Check if `planning/discovery-plan/oc-13-validation-plan.md` already exists and continue from there, or\n" +
				"2. Start fresh with a comprehensive validation findings report?\n\n" +
				"Also, do you want me to pull findings from the completed sibling tasks (OC-10, OC-11, OC-12) and the test execution results documented above, or should I ask you to provide the key findings to synthesize?",
			targetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			want:       "repeated a generic recovery reply instead of the file body",
		},
		{
			name: "rejects p95 planning narration leaked into deliverable",
			content: "Perfect! Now I understand exactly what I need to deliver. The throughput test is an excellent template—I need to produce a similar comprehensive test execution report for P95 Latency Measurement with 5 baseline routing scenarios.\n\n" +
				"Let me now execute the P95 latency measurement test and document the results. I'll create a comprehensive test execution with 5 baseline scenarios, measuring end-to-end latency from task creation to agent notification, and then compare against the SLA.\n\n" +
				"Test Plan:\n1. Scenario 1: Happy Path\n2. Scenario 2: Edge Case - Single Pool at Capacity\n3. Scenario 3: Concurrent Requests\n4. Scenario 4: Network Latency Variance\n5. Scenario 5: Peak Load Stress Test\n",
			targetPath: "p95_latency_result.md",
			want:       "described intent to write the deliverable instead of the file body",
		},
		{
			name: "rejects sign-off planning narration leaked into deliverable",
			content: "Good. Based on my analysis, I can see:\n\n" +
				"1. Upstream task status is complex\n" +
				"2. The validation_scope.md is comprehensive and complete\n" +
				"3. Evidence exists but is fragmented\n" +
				"4. OC-16 is my responsibility\n\n" +
				"Let me now produce a comprehensive validation sign-off report that consolidates the validation scope and the evidence from the test execution results I've already read.\n",
			targetPath: "validation_sign_off.md",
			want:       "described intent to write the deliverable instead of the file body",
		},
		{
			name:    "accepts first-person file body",
			content: "I will write at dawn because the house is quiet and the work still matters.",
			want:    "",
		},
		{
			name: "accepts substantive draft body",
			content: `# Content Strategy

## Core Promise
Sam.blog should publish one durable operating system for thoughtful parents building resilient families and meaningful work.
`,
			want: "",
		},
		{
			name: "rejects mismatched deliverable heading",
			content: `# Core Page Structure Specification

## Overview
This specification defines the global page shell for the site.`,
			targetPath: "design/06-search-discovery-wireframes.md",
			want:       "different deliverable",
		},
		{
			name: "accepts matching deliverable heading",
			content: `# Search and Discovery Wireframes

## Overview
This document defines archive, results, and related-post wireframes.`,
			targetPath: "design/06-search-discovery-wireframes.md",
			want:       "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caseTargetPath := targetPath
			if strings.TrimSpace(tc.targetPath) != "" {
				caseTargetPath = tc.targetPath
			}
			got := recoveryFileWriteDraftRejectReason(tc.content, caseTargetPath)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want contains %q", got, tc.want)
			}
			if !strings.Contains(got, caseTargetPath) {
				t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want target path", got)
			}
		})
	}
}

func TestShouldBlockTaskRecoveryReadScopeTool(t *testing.T) {
	t.Parallel()

	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project_task",
			Mode:      "async",
		},
		recoveryTurn:       true,
		recoveryTargetPath: "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
	}

	cases := []struct {
		name      string
		toolName  string
		path      string
		wantBlock bool
	}{
		{
			name:      "allows target file",
			toolName:  "file.read",
			path:      "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			wantBlock: false,
		},
		{
			name:      "allows same-task planning artifact",
			toolName:  "file.read",
			path:      "planning/discovery-plan/oc-13-validation-plan.md",
			wantBlock: false,
		},
		{
			name:      "allows matching recovery artifact path",
			toolName:  "file.read",
			path:      ".ottercamp/recovery/Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md",
			wantBlock: false,
		},
		{
			name:      "blocks sibling task report",
			toolName:  "file.read",
			path:      "Work/OC-12-VALIDATION-REPORT.md",
			wantBlock: true,
		},
		{
			name:      "blocks sibling task root file",
			toolName:  "file.read",
			path:      "HANDOFF-COMPLETENESS-VALIDATION-OC-11.md",
			wantBlock: true,
		},
		{
			name:      "ignores non-read tools",
			toolName:  "file.search",
			path:      "Work",
			wantBlock: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldBlockTaskRecoveryReadScopeTool(rt, tc.toolName, map[string]any{"path": tc.path})
			if got != tc.wantBlock {
				t.Fatalf("shouldBlockTaskRecoveryReadScopeTool(%q, %q) = %v, want %v", tc.toolName, tc.path, got, tc.wantBlock)
			}
		})
	}
}

func TestLooksLikeRecoveryFileDraftRejectsLongConversationalLeadIn(t *testing.T) {
	t.Parallel()

	content := "I now have the strategy artifacts. These define the migration approach: staged cutover, extraction strategy, transformation to Markdown+YAML, validation gates, redirects, and rollback. I'll write the complete migration plan to the target file next."
	if looksLikeRecoveryFileDraft(content) {
		t.Fatalf("looksLikeRecoveryFileDraft(%q) = true, want false", content)
	}
}

func TestEnsureProjectBootstrapFirstWaveExecutionsStarted(t *testing.T) {
	t.Parallel()

	queuedID := uuid.New()
	inProgressID := uuid.New()
	reviewID := uuid.New()
	doneID := uuid.New()
	draftID := uuid.New()

	flowAdvancer := &fakeFlowAdvancer{}
	engine := &TurnEngine{flowAdvancer: flowAdvancer}

	err := engine.ensureProjectBootstrapFirstWaveExecutionsStarted(context.Background(), []repo.ProjectTask{
		{ID: queuedID, WorkStatus: "queued"},
		{ID: inProgressID, WorkStatus: "in_progress"},
		{ID: reviewID, WorkStatus: "review"},
		{ID: doneID, WorkStatus: "done"},
		{ID: draftID, WorkStatus: "draft"},
	})
	if err != nil {
		t.Fatalf("ensureProjectBootstrapFirstWaveExecutionsStarted: %v", err)
	}
	if flowAdvancer.ensureActiveCalls != 3 {
		t.Fatalf("ensure active execution calls = %d, want 3", flowAdvancer.ensureActiveCalls)
	}
}

func TestLooksLikeRecoveryFileDraftRejectsLongImperativeNarration(t *testing.T) {
	t.Parallel()

	content := "Excellent. I now have all four locked strategy artifacts for OC-15. The immediate directive is clear: write at least one migrated output file before expanding the plan.\n\nBased on the strategy, I need to write the Content Migration Plan (target file: docs/migration-plan/oc-15-content-migration-plan.md). This is the operative document that translates the locked decisions into a step-by-step execution guide.\n\nLet me write the comprehensive migration plan:"
	if looksLikeRecoveryFileDraft(content) {
		t.Fatalf("looksLikeRecoveryFileDraft(%q) = true, want false", content)
	}
}

type unitFixture struct {
	engine        *TurnEngine
	events        *fakeEventBus
	chat          *fakeChatService
	messages      *fakeMessageRepo
	invocations   *fakeInvocationRepo
	model         *fakeModelGateway
	dispatcher    *fakeDispatcher
	flowAdvancer  *fakeFlowAdvancer
	runCanceler   *fakeRunCanceler
	enqueuer      *fakeEnqueuer
	assembler     *fakeAssembler
	memories      *fakeMemoryRepo
	session       *chat.ChatSession
	userMessageID uuid.UUID
}

func newUnitFixture(t *testing.T, mode string) *unitFixture {
	t.Helper()
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()
	agentRecord := repo.Agent{ID: agentID, OrganizationID: orgID, DisplayName: "Frank"}

	session := &chat.ChatSession{ID: sessionID, OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: mode, Status: "active"}
	messages := newFakeMessageRepo()
	userMsg := messages.create(repo.ChatMessage{SessionID: sessionID, Role: "user", Status: "pending", Content: "hello"})

	chatSvc := &fakeChatService{
		session: session,
		participants: []*chat.ChatParticipant{{
			ID:              uuid.New(),
			SessionID:       sessionID,
			ParticipantType: "agent",
			ParticipantID:   agentID,
		}},
		messages:      messages,
		turns:         map[uuid.UUID]*chat.ChatTurn{},
		turnCh:        make(chan uuid.UUID, 4),
		enforceStatus: true,
	}

	profile := repo.ModelProfile{
		ID:                  uuid.New(),
		LogicalProfileID:    "test-profile",
		Version:             1,
		IsCurrent:           true,
		ProviderID:          uuid.New(),
		ModelName:           "test-model",
		InvocationPurpose:   "agent_turn",
		SupportsStreaming:   true,
		ContextWindowTokens: 4096,
		MaxOutputTokens:     1024,
	}

	toolResolver := &fakeToolResolver{}
	assembler := &fakeAssembler{results: []assembleResult{{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "s"}}, TotalTokens: 20}}}}
	modelGateway := &fakeModelGateway{}
	dispatcher := &fakeDispatcher{}
	events := &fakeEventBus{}
	enqueuer := &fakeEnqueuer{}
	invocations := &fakeInvocationRepo{}
	profiles := &fakeModelProfileRepo{profile: profile}
	resolver := &fakeProfileResolver{profile: profile}
	memSources := &fakeMemorySourceRepo{}
	memories := &fakeMemoryRepo{}
	taskRepo := &fakeTaskRepo{}
	taskTransitions := &fakeTaskTransitionService{repo: taskRepo}
	flowAdvancer := &fakeFlowAdvancer{tasks: taskRepo}

	engine, err := NewEngine(Options{
		Chat:            chatSvc,
		ToolResolver:    toolResolver,
		Assembler:       assembler,
		Summarization:   &fakeSummarizationChecker{},
		ModelGateway:    modelGateway,
		Dispatcher:      dispatcher,
		RunCanceler:     &fakeRunCanceler{},
		Events:          events,
		Enqueuer:        enqueuer,
		Invocations:     invocations,
		ModelProfiles:   profiles,
		Profiles:        resolver,
		Messages:        messages,
		Turns:           chatSvc,
		Sessions:        chatSvc,
		Agents:          &fakeAgentRepo{agent: agentRecord, starter: []repo.Agent{agentRecord}},
		Tasks:           taskRepo,
		TaskTransitions: taskTransitions,
		Projects:        &fakeProjectRepo{},
		FlowNodes:       &fakeFlowNodeRepo{},
		FlowAdvancer:    flowAdvancer,
		MemorySources:   memSources,
		Memories:        memories,
		Now:             func() time.Time { return time.Now().UTC() },
		Sleep:           func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	runCanceler := &fakeRunCanceler{}
	engine.runCanceler = runCanceler

	return &unitFixture{
		engine:        engine,
		events:        events,
		chat:          chatSvc,
		messages:      messages,
		invocations:   invocations,
		model:         modelGateway,
		dispatcher:    dispatcher,
		flowAdvancer:  flowAdvancer,
		runCanceler:   runCanceler,
		enqueuer:      enqueuer,
		assembler:     assembler,
		memories:      memories,
		session:       session,
		userMessageID: userMsg.ID,
	}
}

type fakeChatService struct {
	mu            sync.Mutex
	session       *chat.ChatSession
	participants  []*chat.ChatParticipant
	messages      *fakeMessageRepo
	turns         map[uuid.UUID]*chat.ChatTurn
	turnOrder     []uuid.UUID
	turnCh        chan uuid.UUID
	startedAt     time.Time
	enforceStatus bool
	completeNoop  bool
	cancelCalls   int
	completeCalls int
	failCalls     int
}

func (f *fakeChatService) GetSession(ctx context.Context, id uuid.UUID) (*chat.ChatSession, error) {
	if f.session.ID != id {
		return nil, repo.ErrNotFound
	}
	copySession := *f.session
	return &copySession, nil
}

func (f *fakeChatService) CreateSession(_ context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error) {
	session := chat.ChatSession{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		ScopeType:      input.ScopeType,
		ScopeID:        input.ScopeID,
		Mode:           input.Mode,
		Status:         "active",
		Metadata:       input.Metadata,
	}
	return &session, nil
}

func (f *fakeChatService) CreateForMessageAttempt(ctx context.Context, sessionID, agentID, messageID uuid.UUID, retryCount int) (repo.ChatTurn, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if retryCount < 0 {
		retryCount = 0
	}
	for _, turnID := range f.turnOrder {
		turn := f.turns[turnID]
		if turn == nil || turn.SessionID != sessionID {
			continue
		}
		if turn.TriggerMessageID != nil && *turn.TriggerMessageID == messageID && turn.RetryCount == retryCount {
			return repo.ChatTurn(*turn), false, nil
		}
	}

	cycleID := uuid.New()
	triggerMessageID := messageID
	turnID := uuid.New()
	turn := &chat.ChatTurn{
		ID:               turnID,
		SessionID:        sessionID,
		TurnNumber:       len(f.turnOrder) + 1,
		CycleID:          &cycleID,
		RespondingType:   "agent",
		RespondingID:     agentID,
		Status:           "pending",
		TriggerMessageID: &triggerMessageID,
		RetryCount:       retryCount,
	}
	f.turns[turnID] = turn
	f.turnOrder = append(f.turnOrder, turnID)
	f.session.CurrentTurnID = &turnID
	f.session.TurnCount++
	select {
	case f.turnCh <- turnID:
	default:
	}
	return repo.ChatTurn(*turn), true, nil
}

func (f *fakeChatService) CreateTurn(ctx context.Context, sessionID, agentID uuid.UUID) (*chat.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cycleID := uuid.New()
	turnID := uuid.New()
	turn := &chat.ChatTurn{
		ID:             turnID,
		SessionID:      sessionID,
		TurnNumber:     len(f.turnOrder) + 1,
		CycleID:        &cycleID,
		RespondingType: "agent",
		RespondingID:   agentID,
		Status:         "pending",
	}
	f.turns[turnID] = turn
	f.turnOrder = append(f.turnOrder, turnID)
	select {
	case f.turnCh <- turnID:
	default:
	}
	copyTurn := *turn
	return &copyTurn, nil
}

func (f *fakeChatService) StartTurn(ctx context.Context, turnID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "pending") {
		return chat.ErrInvalidStatusTransition
	}
	turn.Status = "in_progress"
	at := f.startedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	turn.StartedAt = &at
	return nil
}

func (f *fakeChatService) CompleteTurn(ctx context.Context, turnID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return chat.ErrInvalidStatusTransition
	}
	f.completeCalls++
	if f.completeNoop {
		return nil
	}
	turn.Status = "completed"
	now := time.Now().UTC()
	turn.CompletedAt = &now
	return nil
}

func (f *fakeChatService) CancelTurn(ctx context.Context, turnID uuid.UUID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return chat.ErrInvalidStatusTransition
	}
	f.cancelCalls++
	turn.Status = "cancelled"
	now := time.Now().UTC()
	turn.CompletedAt = &now
	turn.CancelRequestedAt = &now
	return nil
}

func (f *fakeChatService) FailTurn(ctx context.Context, turnID uuid.UUID, errorMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return chat.ErrInvalidStatusTransition
	}
	f.failCalls++
	turn.Status = "failed"
	now := time.Now().UTC()
	turn.CompletedAt = &now
	return nil
}

func (f *fakeChatService) GetTurn(ctx context.Context, turnID uuid.UUID) (*chat.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return nil, repo.ErrNotFound
	}
	copyTurn := *turn
	return &copyTurn, nil
}

func (f *fakeChatService) ListParticipants(ctx context.Context, sessionID uuid.UUID) ([]*chat.ChatParticipant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sessionID != f.session.ID {
		return nil, repo.ErrNotFound
	}
	result := make([]*chat.ChatParticipant, 0, len(f.participants))
	for _, item := range f.participants {
		copyItem := *item
		result = append(result, &copyItem)
	}
	return result, nil
}

func (f *fakeChatService) AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*chat.ChatParticipant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sessionID != f.session.ID {
		return nil, repo.ErrNotFound
	}
	if participantID == uuid.Nil {
		return nil, fmt.Errorf("participant_id is required")
	}
	normalizedType := strings.TrimSpace(strings.ToLower(participantType))
	normalizedRole := strings.TrimSpace(strings.ToLower(role))
	if normalizedRole == "" {
		normalizedRole = "member"
	}
	for _, existing := range f.participants {
		if existing == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(existing.ParticipantType), normalizedType) && existing.ParticipantID == participantID {
			return nil, chat.ErrAlreadyParticipant
		}
	}
	added := &chat.ChatParticipant{
		ID:              uuid.New(),
		SessionID:       sessionID,
		ParticipantType: normalizedType,
		ParticipantID:   participantID,
		Role:            normalizedRole,
	}
	f.participants = append(f.participants, added)
	copyItem := *added
	return &copyItem, nil
}

func (f *fakeChatService) AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error) {
	if !strings.EqualFold(strings.TrimSpace(f.session.Status), "active") {
		return nil, chat.ErrSessionClosed
	}
	item := repo.ChatMessage{
		SessionID:     input.SessionID,
		TurnID:        input.TurnID,
		Role:          strings.TrimSpace(input.Role),
		Content:       input.Content,
		ContentFormat: "text",
		Status:        "pending",
		Metadata:      input.Metadata,
	}
	if input.AuthorType != nil {
		item.AuthorType = input.AuthorType
	}
	if input.AuthorID != nil {
		item.AuthorID = input.AuthorID
	}
	if input.ToolCallID != nil {
		item.ToolCallID = input.ToolCallID
	}
	created := f.messages.create(item)
	copyItem := chat.ChatMessage(created)
	return &copyItem, nil
}

func (f *fakeChatService) UpdateMessageStatus(ctx context.Context, messageID uuid.UUID, newStatus, errorMsg string) error {
	_, err := f.messages.UpdateStatus(ctx, messageID, newStatus, errorMsg)
	return err
}

func (f *fakeChatService) waitForTurnID(t *testing.T) uuid.UUID {
	t.Helper()
	select {
	case id := <-f.turnCh:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for turn id")
		return uuid.Nil
	}
}

func (f *fakeChatService) waitForTurnIDOptional() uuid.UUID {
	select {
	case id := <-f.turnCh:
		return id
	default:
		return uuid.Nil
	}
}

func (f *fakeChatService) turnByID(id uuid.UUID) *chat.ChatTurn {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn, ok := f.turns[id]
	if !ok {
		return nil
	}
	copyTurn := *turn
	return &copyTurn
}

func (f *fakeChatService) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]repo.ChatTurn, 0, len(f.turnOrder))
	for _, id := range f.turnOrder {
		turn := f.turns[id]
		if turn == nil || turn.SessionID != sessionID {
			continue
		}
		items = append(items, repo.ChatTurn(*turn))
	}
	return items, nil
}

func (f *fakeChatService) SetStopReason(ctx context.Context, id uuid.UUID, stopReason *string) (repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[id]
	if turn == nil {
		return repo.ChatTurn{}, repo.ErrNotFound
	}
	if stopReason == nil || strings.TrimSpace(*stopReason) == "" {
		turn.StopReason = nil
	} else {
		reason := strings.TrimSpace(*stopReason)
		turn.StopReason = &reason
	}
	return repo.ChatTurn(*turn), nil
}

func (f *fakeChatService) SetTriggerMessageID(ctx context.Context, id uuid.UUID, triggerMessageID *uuid.UUID) (repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[id]
	if turn == nil {
		return repo.ChatTurn{}, repo.ErrNotFound
	}
	if triggerMessageID == nil || *triggerMessageID == uuid.Nil {
		turn.TriggerMessageID = nil
	} else {
		copyID := *triggerMessageID
		turn.TriggerMessageID = &copyID
	}
	return repo.ChatTurn(*turn), nil
}

func (f *fakeChatService) Create(ctx context.Context, turn repo.ChatTurn) (repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyTurn := chat.ChatTurn(turn)
	if copyTurn.ID == uuid.Nil {
		copyTurn.ID = uuid.New()
	}
	f.turns[copyTurn.ID] = &copyTurn
	f.turnOrder = append(f.turnOrder, copyTurn.ID)
	select {
	case f.turnCh <- copyTurn.ID:
	default:
	}
	return repo.ChatTurn(copyTurn), nil
}

func (f *fakeChatService) UpdateCurrentTurn(ctx context.Context, id uuid.UUID, currentTurnID *uuid.UUID) (repo.ChatSession, error) {
	if id != f.session.ID {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	f.session.CurrentTurnID = currentTurnID
	return repo.ChatSession(*f.session), nil
}

func (f *fakeChatService) IncrementCounts(ctx context.Context, id uuid.UUID, turnDelta, messageDelta int) (repo.ChatSession, error) {
	if id != f.session.ID {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	f.session.TurnCount += turnDelta
	f.session.MessageCount += messageDelta
	return repo.ChatSession(*f.session), nil
}

type fakeMessageRepo struct {
	mu              sync.Mutex
	items           map[uuid.UUID]repo.ChatMessage
	order           []uuid.UUID
	nextSeq         int64
	updateContentFn func(context.Context, uuid.UUID, string) (repo.ChatMessage, error)
}

func newFakeMessageRepo() *fakeMessageRepo {
	return &fakeMessageRepo{items: map[uuid.UUID]repo.ChatMessage{}, nextSeq: 1}
}

func (f *fakeMessageRepo) create(message repo.ChatMessage) repo.ChatMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if message.ID == uuid.Nil {
		message.ID = uuid.New()
	}
	if message.SequenceNumber == 0 {
		message.SequenceNumber = f.nextSeq
		f.nextSeq++
	}
	if strings.TrimSpace(message.Status) == "" {
		message.Status = "pending"
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	message.UpdatedAt = time.Now().UTC()
	f.items[message.ID] = message
	f.order = append(f.order, message.ID)
	return message
}

func (f *fakeMessageRepo) upsert(message repo.ChatMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[message.ID]; !ok {
		f.order = append(f.order, message.ID)
	}
	f.items[message.ID] = message
}

func (f *fakeMessageRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeMessageRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]repo.ChatMessage, 0)
	for _, id := range f.order {
		item := f.items[id]
		if item.SessionID == sessionID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SequenceNumber < items[j].SequenceNumber })
	return items, nil
}

func (f *fakeMessageRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	item.Status = strings.TrimSpace(status)
	if strings.TrimSpace(errorMessage) != "" {
		msg := strings.TrimSpace(errorMessage)
		item.ErrorMessage = &msg
	} else {
		item.ErrorMessage = nil
	}
	item.UpdatedAt = time.Now().UTC()
	f.items[id] = item
	return item, nil
}

func (f *fakeMessageRepo) UpdateContent(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error) {
	if f.updateContentFn != nil {
		return f.updateContentFn(ctx, id, content)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	item.Content = content
	item.UpdatedAt = time.Now().UTC()
	f.items[id] = item
	return item, nil
}

func (f *fakeMessageRepo) UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	item.Metadata = metadata
	item.UpdatedAt = time.Now().UTC()
	f.items[id] = item
	return item, nil
}

func (f *fakeMessageRepo) containsContent(content string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, item := range f.items {
		if strings.TrimSpace(item.Content) == strings.TrimSpace(content) {
			return true
		}
	}
	return false
}

func (f *fakeMessageRepo) containsContentSubstring(content string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	needle := strings.TrimSpace(content)
	for _, item := range f.items {
		if strings.Contains(strings.TrimSpace(item.Content), needle) {
			return true
		}
	}
	return false
}

func (f *fakeMessageRepo) containsStatus(role, status string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, item := range f.items {
		if strings.EqualFold(item.Role, role) && strings.EqualFold(item.Status, status) {
			return true
		}
	}
	return false
}

type fakeToolResolver struct {
	tools []tools.ToolDescriptor
}

func (f *fakeToolResolver) GetSessionToolSet(ctx context.Context, sessionID, agentID uuid.UUID) ([]tools.ToolDescriptor, error) {
	if len(f.tools) == 0 {
		return []tools.ToolDescriptor{{Name: "file.read", Tier: "tier1"}, {Name: "cli.execute", Tier: "tier2"}}, nil
	}
	return append([]tools.ToolDescriptor(nil), f.tools...), nil
}

type assembleResult struct {
	prompt *prompt.AssembledPrompt
	err    error
}

type fakeAssembler struct {
	mu         sync.Mutex
	results    []assembleResult
	calls      int
	onAssemble func(input prompt.AssemblyInput, call int)
}

func (f *fakeAssembler) Assemble(ctx context.Context, input prompt.AssemblyInput) (*prompt.AssembledPrompt, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	hook := f.onAssemble
	if len(f.results) == 0 {
		f.mu.Unlock()
		if hook != nil {
			hook(input, call)
		}
		return &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "default"}}, TotalTokens: 10}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	f.mu.Unlock()
	if hook != nil {
		hook(input, call)
	}
	return result.prompt, result.err
}

type fakeSummarizationChecker struct {
	shouldSummarize bool
}

func (f *fakeSummarizationChecker) ShouldSummarize(context.Context, uuid.UUID, int) (bool, error) {
	return f.shouldSummarize, nil
}

type fakeModelGateway struct {
	streamFn   func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error)
	completeFn func(ctx context.Context, req ModelRequest) (ModelResponse, error)

	streamCalls              int
	listeningEvalCalls       int
	continuationSummaryCalls int
}

func (f *fakeModelGateway) StreamComplete(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
	f.streamCalls++
	if f.streamFn != nil {
		return f.streamFn(ctx, req, onChunk)
	}
	_ = onChunk("ok")
	return ModelResponse{Content: "ok"}, nil
}

func (f *fakeModelGateway) Complete(ctx context.Context, req ModelRequest) (ModelResponse, error) {
	if req.Purpose == "listening_eval" {
		f.listeningEvalCalls++
	}
	if req.Purpose == "continuation_summary" {
		f.continuationSummaryCalls++
	}
	if f.completeFn != nil {
		return f.completeFn(ctx, req)
	}
	return ModelResponse{Content: "respond"}, nil
}

type fakeDispatcher struct {
	tier1Fn func(ctx context.Context, call ToolCall) (ToolResult, error)
	tier2Fn func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error)
}

func (f *fakeDispatcher) DispatchTier1(ctx context.Context, call ToolCall) (ToolResult, error) {
	if f.tier1Fn != nil {
		return f.tier1Fn(ctx, call)
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
}

func (f *fakeDispatcher) DispatchTier2(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
	if f.tier2Fn != nil {
		return f.tier2Fn(ctx, call, onRunStarted)
	}
	runID := uuid.New()
	onRunStarted(runID)
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}, RunID: &runID}, nil
}

type fakeRunCanceler struct {
	mu    sync.Mutex
	calls []uuid.UUID
}

func (f *fakeRunCanceler) RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy controlplane.CancelRequestActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, runID)
	return nil
}

type fakeEventBus struct {
	mu     sync.Mutex
	subs   []fakeSubscription
	events []eventbus.DomainEvent
}

type fakeSubscription struct {
	consumerName string
	orgID        *uuid.UUID
	handler      eventbus.EventHandler
}

func (f *fakeEventBus) Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error {
	f.mu.Lock()
	f.events = append(f.events, event)
	subs := append([]fakeSubscription(nil), f.subs...)
	f.mu.Unlock()
	for _, sub := range subs {
		if sub.handler == nil {
			continue
		}
		if sub.orgID != nil && *sub.orgID != event.OrganizationID {
			continue
		}
		if err := sub.handler(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeEventBus) Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fakeSubscription{consumerName: strings.TrimSpace(consumerName), orgID: orgID, handler: handler})
	return eventbus.Subscription{}
}

func (f *fakeEventBus) Unsubscribe(_ eventbus.Subscription) {}

func (f *fakeEventBus) subscriptionNamesWithPrefix(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.subs))
	for _, sub := range f.subs {
		if !strings.HasPrefix(sub.consumerName, prefix) {
			continue
		}
		out = append(out, sub.consumerName)
	}
	return out
}

type fakeEnqueuer struct {
	mu   sync.Mutex
	jobs []enqueuedJob
}

type enqueuedJob struct {
	jobType  string
	priority int
	payload  *AgentTurnPayload
	runAfter *time.Time
}

func (f *fakeEnqueuer) Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error) {
	entry := enqueuedJob{jobType: strings.TrimSpace(jobType), priority: priority}
	if runAfter != nil {
		at := *runAfter
		entry.runAfter = &at
	}
	if entry.jobType == AgentTurnJobType {
		typed, ok := payload.(AgentTurnPayload)
		if !ok {
			return uuid.Nil, fmt.Errorf("unexpected payload type %T", payload)
		}
		copyPayload := typed
		entry.payload = &copyPayload
	}
	f.mu.Lock()
	f.jobs = append(f.jobs, entry)
	f.mu.Unlock()
	return uuid.New(), nil
}

func (f *fakeEnqueuer) agentTurnJobs() []enqueuedJob {
	return f.jobsByType(AgentTurnJobType)
}

func (f *fakeEnqueuer) jobsByType(jobType string) []enqueuedJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]enqueuedJob, 0)
	for _, job := range f.jobs {
		if job.jobType != jobType {
			continue
		}
		copyJob := job
		if job.payload != nil {
			payload := *job.payload
			copyJob.payload = &payload
		}
		if job.runAfter != nil {
			runAfter := *job.runAfter
			copyJob.runAfter = &runAfter
		}
		out = append(out, copyJob)
	}
	return out
}

type fakeInvocationRepo struct {
	mu                  sync.Mutex
	creates             []repo.ModelInvocation
	updateCompletionErr error
}

func (f *fakeInvocationRepo) Create(ctx context.Context, invocation repo.ModelInvocation) (repo.ModelInvocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	invocation.ID = uuid.New()
	f.creates = append(f.creates, invocation)
	return invocation, nil
}

func (f *fakeInvocationRepo) UpdateStatus(context.Context, uuid.UUID, string, *string, *string) (repo.ModelInvocation, error) {
	return repo.ModelInvocation{}, nil
}

func (f *fakeInvocationRepo) UpdateCompletion(context.Context, uuid.UUID, int, int, int, int, int, *string, *string) error {
	return f.updateCompletionErr
}

type fakeModelProfileRepo struct {
	profile   repo.ModelProfile
	requested []string
}

func (f *fakeModelProfileRepo) GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error) {
	if strings.TrimSpace(logicalProfileID) == "" {
		return repo.ModelProfile{}, repo.ErrNotFound
	}
	f.requested = append(f.requested, strings.TrimSpace(logicalProfileID))
	copyProfile := f.profile
	copyProfile.LogicalProfileID = logicalProfileID
	return copyProfile, nil
}

type fakeProfileResolver struct {
	profile repo.ModelProfile
}

func (f *fakeProfileResolver) Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...model.Scope) (*repo.ModelProfile, error) {
	copyProfile := f.profile
	return &copyProfile, nil
}

type fakeAgentRepo struct {
	agent      repo.Agent
	items      map[uuid.UUID]repo.Agent
	starter    []repo.Agent
	starterErr error
}

func (f *fakeAgentRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error) {
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	if f.agent.ID != id {
		return repo.Agent{}, repo.ErrNotFound
	}
	return f.agent, nil
}

func (f *fakeAgentRepo) GetStarterTrio(context.Context, uuid.UUID) ([]repo.Agent, error) {
	if f.starterErr != nil {
		return nil, f.starterErr
	}
	return append([]repo.Agent(nil), f.starter...), nil
}

type fakeTaskRepo struct {
	items map[uuid.UUID]repo.ProjectTask
	err   error
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.ProjectTask{}, repo.ErrNotFound
}

func (f *fakeTaskRepo) GetByProjectAndNumber(_ context.Context, projectID uuid.UUID, taskNumber int) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	for _, item := range f.items {
		if item.ProjectID == projectID && item.TaskNumber == taskNumber {
			return item, nil
		}
	}
	return repo.ProjectTask{}, repo.ErrNotFound
}

func (f *fakeTaskRepo) ListByProject(_ context.Context, projectID uuid.UUID, _ ...string) ([]repo.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	items := make([]repo.ProjectTask, 0, len(f.items))
	for _, item := range f.items {
		if item.ProjectID == projectID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].TaskNumber == items[j].TaskNumber {
			return items[i].ID.String() < items[j].ID.String()
		}
		return items[i].TaskNumber < items[j].TaskNumber
	})
	return items, nil
}

func (f *fakeTaskRepo) Update(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	if f.items == nil {
		f.items = map[uuid.UUID]repo.ProjectTask{}
	}
	f.items[task.ID] = task
	return task, nil
}

func (f *fakeTaskRepo) UpdateMetadata(_ context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	item.Metadata = append(json.RawMessage(nil), metadata...)
	f.items[id] = item
	return item, nil
}

type fakeTaskTransitionService struct {
	repo            *fakeTaskRepo
	calls           []blockedTaskCall
	transitionCalls []transitionTaskCall
	err             error
}

type blockedTaskCall struct {
	taskID uuid.UUID
	reason string
	actor  tasksvc.Actor
}

type transitionTaskCall struct {
	taskID   uuid.UUID
	toStatus string
	actor    tasksvc.Actor
}

func (f *fakeTaskTransitionService) TransitionStatus(_ context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	target := strings.TrimSpace(toStatus)
	f.transitionCalls = append(f.transitionCalls, transitionTaskCall{
		taskID:   taskID,
		toStatus: target,
		actor:    actor,
	})
	if f.repo != nil {
		taskRecord, err := f.repo.GetByID(context.Background(), taskID)
		if err == nil {
			taskRecord.WorkStatus = target
			updated, updateErr := f.repo.Update(context.Background(), taskRecord)
			if updateErr == nil {
				transitioned := tasksvc.ProjectTask(updated)
				return &transitioned, nil
			}
		}
	}
	transitioned := tasksvc.ProjectTask{ID: taskID, WorkStatus: target}
	return &transitioned, nil
}

func (f *fakeTaskTransitionService) TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor, _ map[string]any) (*tasksvc.ProjectTask, error) {
	return f.TransitionStatus(ctx, taskID, toStatus, actor)
}

func (f *fakeTaskTransitionService) MarkBlocked(_ context.Context, taskID uuid.UUID, reason string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.calls = append(f.calls, blockedTaskCall{
		taskID: taskID,
		reason: strings.TrimSpace(reason),
		actor:  actor,
	})
	if f.repo != nil {
		taskRecord, err := f.repo.GetByID(context.Background(), taskID)
		if err == nil {
			taskRecord.WorkStatus = "blocked"
			updated, updateErr := f.repo.Update(context.Background(), taskRecord)
			if updateErr == nil {
				blocked := tasksvc.ProjectTask(updated)
				return &blocked, nil
			}
		}
	}
	blocked := tasksvc.ProjectTask{ID: taskID, WorkStatus: "blocked"}
	return &blocked, nil
}

type fakeFlowNodeRepo struct {
	items map[uuid.UUID]repo.FlowNode
	err   error
}

func (f *fakeFlowNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	if f.err != nil {
		return repo.FlowNode{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.FlowNode{}, repo.ErrNotFound
}

type fakeFlowAdvancer struct {
	tasks                 *fakeTaskRepo
	activeExecution       *repo.FlowNodeExecution
	ensureActiveCalls     int
	recordNodeCommitCalls int
	advanceFlowCalls      int
	rejectFlowCalls       int
	lastCommitSHA         string
	lastAdvanceActor      flowsvc.Actor
	lastRejectActor       flowsvc.Actor
}

func (f *fakeFlowAdvancer) EnsureActiveExecution(_ context.Context, taskID uuid.UUID) (*repo.FlowNodeExecution, error) {
	f.ensureActiveCalls++
	if f.activeExecution != nil {
		copyExecution := *f.activeExecution
		return &copyExecution, nil
	}
	return &repo.FlowNodeExecution{TaskID: taskID, Status: "active"}, nil
}

func (f *fakeFlowAdvancer) RecordNodeCommit(_ context.Context, taskID uuid.UUID, commitSHA, _ string) (*repo.FlowNodeExecution, error) {
	f.recordNodeCommitCalls++
	f.lastCommitSHA = strings.TrimSpace(commitSHA)
	commit := f.lastCommitSHA
	return &repo.FlowNodeExecution{TaskID: taskID, CommitSHA: &commit}, nil
}

func (f *fakeFlowAdvancer) AdvanceFlow(_ context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	f.advanceFlowCalls++
	f.lastAdvanceActor = actor
	if f.tasks != nil {
		taskRecord, err := f.tasks.GetByID(context.Background(), taskID)
		if err == nil {
			taskRecord.WorkStatus = "review"
			if _, updateErr := f.tasks.Update(context.Background(), taskRecord); updateErr != nil {
				return nil, updateErr
			}
		}
	}
	return &repo.FlowNodeExecution{TaskID: taskID, Status: "completed"}, nil
}

func (f *fakeFlowAdvancer) RejectFlowNode(_ context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	f.rejectFlowCalls++
	f.lastRejectActor = actor
	if f.tasks != nil {
		taskRecord, err := f.tasks.GetByID(context.Background(), taskID)
		if err == nil {
			taskRecord.WorkStatus = "in_progress"
			if _, updateErr := f.tasks.Update(context.Background(), taskRecord); updateErr != nil {
				return nil, updateErr
			}
		}
	}
	return &repo.FlowNodeExecution{TaskID: taskID, Status: "rejected"}, nil
}

type fakeAssignmentRepo struct {
	items map[uuid.UUID]repo.AgentProjectAssignment
	list  []repo.AgentProjectAssignment
	err   error
}

func (f *fakeAssignmentRepo) GetPM(_ context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error) {
	if f.err != nil {
		return repo.AgentProjectAssignment{}, f.err
	}
	if item, ok := f.items[projectID]; ok {
		return item, nil
	}
	return repo.AgentProjectAssignment{}, repo.ErrNotFound
}

func (f *fakeAssignmentRepo) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.list) > 0 {
		items := make([]repo.AgentProjectAssignment, 0, len(f.list))
		for _, item := range f.list {
			if item.ProjectID == projectID {
				items = append(items, item)
			}
		}
		return items, nil
	}
	items := make([]repo.AgentProjectAssignment, 0, len(f.items))
	for _, item := range f.items {
		if item.ProjectID == projectID {
			items = append(items, item)
		}
	}
	return items, nil
}

type fakeProjectRepo struct {
	items map[uuid.UUID]repo.Project
	err   error
}

func (f *fakeProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if f.err != nil {
		return repo.Project{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.Project{}, repo.ErrNotFound
}

type fakeTurnProjectEnvironmentRepo struct {
	items map[uuid.UUID][]repo.ProjectEnvironment
	err   error
}

func (f *fakeTurnProjectEnvironmentRepo) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]repo.ProjectEnvironment(nil), f.items[projectID]...), nil
}

type fakeOrganizationRepo struct {
	items map[uuid.UUID]repo.Organization
	err   error
}

func (f *fakeOrganizationRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Organization, error) {
	if f.err != nil {
		return repo.Organization{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.Organization{}, repo.ErrNotFound
}

type fakeMemorySourceRepo struct {
	sources []repo.MemorySource
}

func (f *fakeMemorySourceRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.MemorySource, error) {
	return append([]repo.MemorySource(nil), f.sources...), nil
}

type fakeMemoryRepo struct {
	mu          sync.Mutex
	items       map[uuid.UUID]repo.Memory
	updateCalls int
}

func (f *fakeMemoryRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items = map[uuid.UUID]repo.Memory{}
	}
	item, ok := f.items[id]
	if !ok {
		return repo.Memory{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeMemoryRepo) UpdateConfidence(ctx context.Context, id uuid.UUID, confidence float64) (repo.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	item, ok := f.items[id]
	if !ok {
		return repo.Memory{}, repo.ErrNotFound
	}
	item.Confidence = confidence
	f.items[id] = item
	return item, nil
}

func createCompletedTurnWithAssistantMessage(t *testing.T, fixture *unitFixture, agentID uuid.UUID, assistantContent string) uuid.UUID {
	t.Helper()

	turn, err := fixture.chat.CreateTurn(context.Background(), fixture.session.ID, agentID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := fixture.chat.CompleteTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	authorType := "agent"
	fixture.messages.create(repo.ChatMessage{
		SessionID:  fixture.session.ID,
		TurnID:     &turn.ID,
		AuthorType: &authorType,
		AuthorID:   &agentID,
		Role:       "assistant",
		Status:     "final",
		Content:    assistantContent,
	})
	return turn.ID
}

func appendToolResultMessage(t *testing.T, fixture *unitFixture, turnID uuid.UUID, payload map[string]any) {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Status:    "final",
		Content:   string(raw),
	})
}

func TestShouldSuppressAutoContinuationForStopReasonIncludesRecoveryFallback(t *testing.T) {
	t.Parallel()

	recoveryFileRejected := stopReasonRecoveryFileRejected
	legacyFallback := stopReasonRecoveryFileFallback
	validationBlocked := stopReasonValidationBlocked

	tests := []struct {
		name       string
		stopReason *string
		want       bool
	}{
		{name: "nil", stopReason: nil, want: false},
		{name: "preferred recovery file stop reason", stopReason: &recoveryFileRejected, want: true},
		{name: "legacy recovery fallback stop reason", stopReason: &legacyFallback, want: true},
		{name: "validation blocked", stopReason: &validationBlocked, want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldSuppressAutoContinuationForStopReason(tc.stopReason); got != tc.want {
				t.Fatalf("shouldSuppressAutoContinuationForStopReason(%v) = %t, want %t", tc.stopReason, got, tc.want)
			}
		})
	}
}

func TestCompleteTurnNoOpForFailedTurn(t *testing.T) {
	t.Parallel()

	turnID := uuid.New()
	sessionID := uuid.New()
	taskID := uuid.New()
	fakeChat := &fakeChatService{
		enforceStatus: true,
		session: &chat.ChatSession{
			ID:        sessionID,
			ScopeType: "project_task",
			ScopeID:   taskID,
			Status:    "active",
		},
		turns: map[uuid.UUID]*chat.ChatTurn{
			turnID: {
				ID:        turnID,
				SessionID: sessionID,
				Status:    "failed",
			},
		},
	}
	engine := &TurnEngine{
		chat:   fakeChat,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rt := &turnRuntime{
		session: fakeChat.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: sessionID,
			Status:    "in_progress",
		},
	}

	if err := engine.completeTurn(context.Background(), rt); err != nil {
		t.Fatalf("completeTurn failed terminal no-op: %v", err)
	}
	if rt.turn == nil || !strings.EqualFold(strings.TrimSpace(rt.turn.Status), "failed") {
		t.Fatalf("rt.turn status = %v, want failed", rt.turn)
	}
}

func TestLatestSubstantiveAssistantFinalForTurnSkipsLaterIntentPlaceholder(t *testing.T) {
	t.Parallel()

	turnID := uuid.New()
	messages := []repo.ChatMessage{
		{
			ID:             uuid.New(),
			TurnID:         &turnID,
			Role:           "assistant",
			Status:         "final",
			SequenceNumber: 10,
			Content: strings.TrimSpace(`# Deliverable

## Section
Real file body content that should be reused for the write fallback.
`),
		},
		{
			ID:             uuid.New(),
			TurnID:         &turnID,
			Role:           "assistant",
			Status:         "final",
			SequenceNumber: 11,
			Content:        "Now I'll create the acceptance criteria:",
		},
	}

	got := latestSubstantiveAssistantFinalForTurn(messages, turnID, "docs/target.md")
	if got == nil {
		t.Fatal("expected substantive assistant draft")
	}
	if got.SequenceNumber != 10 {
		t.Fatalf("sequence_number = %d, want 10", got.SequenceNumber)
	}
}

func TestHandleTaskFileWriteWithoutContentUsesRecoveryArtifactDraftAfterIntentReply(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()
	targetPath := "design-system/03-accessibility-standards.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	recoveryArtifact := strings.TrimSpace(`# Recovery file.write artifact

Task: OC-28
Target Path: design-system/03-accessibility-standards.md
Generated: 2026-03-23T06:21:48Z

## Draft Content

# Accessibility Standards

## Contrast Requirements
- Body copy must meet WCAG 2.2 AA contrast thresholds.
- Interactive focus states must remain visible on every background.
`)
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path":    ".ottercamp/recovery/design-system/03-accessibility-standards.md",
				"content": recoveryArtifact,
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "Now I'll write the full accessibility standards document:",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path": targetPath,
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWithoutContent(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWithoutContent: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "# Accessibility Standards") {
		t.Fatalf("content = %q, want hydrated recovery artifact draft", got)
	}
	if got := stringValue(call.Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got, ok := call.Arguments["create_dirs"].(bool); !ok || !got {
		t.Fatalf("create_dirs = %v, want true", call.Arguments["create_dirs"])
	}
}

func TestHandleTaskFileWriteWithoutContentPrefersPriorSubstantiveDraftOverIntentReply(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	turnID := uuid.New()
	targetPath := "planning/prd-spec/oc-24-infrastructure-spec.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Infrastructure Specification

## Hosting
- Vercel Pro with edge caching.
`),
	})

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "I need to provide the content parameter. Let me write the full infrastructure specification:",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path": targetPath,
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWithoutContent(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWithoutContent: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "## Hosting") {
		t.Fatalf("content = %q, want prior substantive draft", got)
	}
}

func TestHandleTaskFileWriteWithoutContentAppendsCorrectionWhenNoDraftExists(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()
	targetPath := "schema-definition.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "Let me provide the full schema definition content:",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path": targetPath,
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWithoutContent(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWithoutContent: %v", err)
	}
	if !handled || abort {
		t.Fatalf("handled=%v abort=%v, want true false", handled, abort)
	}
	if rt.taskFileFixes != 1 {
		t.Fatalf("taskFileFixes = %d, want 1", rt.taskFileFixes)
	}
	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	last := messages[len(messages)-1]
	if !strings.EqualFold(last.Role, "system") {
		t.Fatalf("last role = %q, want system", last.Role)
	}
	if !strings.Contains(last.Content, "Task execution correction") || !strings.Contains(last.Content, targetPath) {
		t.Fatalf("last content = %q, want task correction message for %s", last.Content, targetPath)
	}
}

func TestHandleTaskCLIExecuteWithoutCommandRewritesToFileWriteFromDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	turnID := uuid.New()
	targetPath := "deliverables/oc-11-workflow-specification.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.write",
			"output": map[string]any{
				"path":    targetPath,
				"content": "# Workflow Specification\n\n## Overview\n- Concrete workflow body.\n",
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "# Workflow Specification\n\n## Overview\n- Concrete workflow body.\n",
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "Let me write the complete workflow specification file using a heredoc:",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "cli-1",
		Name: "cli.execute",
		Arguments: map[string]any{
			"command": "",
		},
	}

	handled, abort, err := fixture.engine.handleTaskCLIExecuteWithoutCommand(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskCLIExecuteWithoutCommand: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if call.Name != "file.write" {
		t.Fatalf("call.Name = %q, want file.write", call.Name)
	}
	if got := stringValue(call.Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "## Overview") {
		t.Fatalf("content = %q, want rewritten substantive draft", got)
	}
	if got, ok := call.Arguments["create_dirs"].(bool); !ok || !got {
		t.Fatalf("create_dirs = %v, want true", call.Arguments["create_dirs"])
	}
}

func TestHandleTaskRejectedFileWriteContentRewritesPlaceholderFromPriorDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	turnID := uuid.New()
	targetPath := "docs/migration-plan/oc-15-content-migration-plan.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Content Migration Plan

## Migration Strategy
- Audit and classify all legacy posts.
- Migrate Tier 1 before Tier 2.
`),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    targetPath,
			"content": "Now write the full migration plan:",
		},
	}

	handled, abort, err := fixture.engine.handleTaskRejectedFileWriteContent(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskRejectedFileWriteContent: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "## Migration Strategy") {
		t.Fatalf("content = %q, want prior substantive draft", got)
	}
}

func TestHandleTaskFileWriteWrongPathRewritesToRecoveryTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	turnID := uuid.New()
	targetPath := "Scoring/scoring-model-v1.0.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := fixture.engine.tasks.(*fakeTaskRepo)
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:         taskID,
			WorkStatus: "in_progress",
			Metadata: mustRawJSON(t, map[string]any{
				"planning": map[string]any{
					"mode": string(taskplan.ModeExecutionFirst),
				},
			}),
		},
	}
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.write",
			"output": map[string]any{
				"path":    targetPath,
				"content": "# Scoring Model\n\nUse weighted criteria.\n",
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "Scoring/automation-implementation-brief.md",
			"content": "# Scoring Model\n\nUse weighted criteria.\n",
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWrongPath(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWrongPath: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got, ok := call.Arguments["create_dirs"].(bool); !ok || !got {
		t.Fatalf("create_dirs = %v, want true", call.Arguments["create_dirs"])
	}
}

func TestHandleTaskFileWriteWrongPathSkipsNonExecutionFirstTasks(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := fixture.engine.tasks.(*fakeTaskRepo)
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:         taskID,
			WorkStatus: "in_progress",
			Metadata:   mustRawJSON(t, map[string]any{"planning": map[string]any{"mode": "orchestration"}}),
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "Scoring/automation-implementation-brief.md",
			"content": "# Brief\n",
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWrongPath(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWrongPath: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["path"]); got != "Scoring/automation-implementation-brief.md" {
		t.Fatalf("path = %q, want unchanged", got)
	}
}

func TestHandleTaskFileWriteWrongPathRewritesToInferredTestExecutionTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()
	description := "Execute test scenario 3 (error handling): malformed input, agent unavailability, timeout conditions. Verify system gracefully handles errors. Produce test execution log."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := fixture.engine.tasks.(*fakeTaskRepo)
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:          taskID,
			TaskNumber:  14,
			Title:       "OC-5: Execute Test Scenario 3 (Error Handling)",
			Description: &description,
			WorkStatus:  "in_progress",
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "Error/test-execution-oc14-scenario3-error-handling.md",
			"content": "# OC-5\n",
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWrongPath(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWrongPath: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["path"]); got != "Test/test-execution-oc14-scenario3-error-handling.md" {
		t.Fatalf("path = %q, want inferred test execution target", got)
	}
}

func TestHandleTaskFileWriteWrongPathRewritesScenarioExecutionPlanToCanonicalTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()
	description := "Execute the happy-path scenario end-to-end against the real speaker pipeline product. Capture screenshots, logs, and evidence of successful completion at each step."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := fixture.engine.tasks.(*fakeTaskRepo)
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:          taskID,
			TaskNumber:  17,
			Title:       "Execute happy-path scenario",
			Description: &description,
			WorkStatus:  "in_progress",
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "test/OC-17-HAPPY-PATH-EXECUTION-PLAN.md",
			"content": "# OC-17 plan\n",
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWrongPath(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWrongPath: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["path"]); got != "Test/test-execution-oc17-happy-path-scenario.md" {
		t.Fatalf("path = %q, want canonical inferred execution target", got)
	}
}

func TestHandleTaskFileWriteWrongPathRewritesValidationExecutionDocumentToCanonicalTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()
	description := "Execute capacity test: submit registrations when pipeline at 90% and 100% capacity. Verify expected responses and behaviors. Record results. ~25 min."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := fixture.engine.tasks.(*fakeTaskRepo)
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:          taskID,
			TaskNumber:  27,
			Title:       "Validation execution: test pipeline capacity rejection",
			Description: &description,
			WorkStatus:  "in_progress",
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "Test/oc-27-capacity-rejection-test-design.md",
			"content": "# OC-27\n",
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWrongPath(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWrongPath: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["path"]); got != "Test/test-execution-oc27-test-pipeline-capacity-rejection.md" {
		t.Fatalf("path = %q, want canonical validation execution target", got)
	}
}

func TestHandleTaskFileWriteWrongPathRewritesGenericDocumentPathToCanonicalTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := fixture.engine.tasks.(*fakeTaskRepo)
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:         taskID,
			TaskNumber: 17,
			Title:      "Design scenario: speaker registration with complete profile data",
			WorkStatus: "in_progress",
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "validated",
			"content": "# OC-17 scenario design\n",
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWrongPath(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWrongPath: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["path"]); got != "Work/OC-17-DESIGN-SCENARIO-SPEAKER-REGISTRATION-WITH-COMPLETE-PROFILE-DATA.md" {
		t.Fatalf("path = %q, want canonical document target", got)
	}
}

func TestRecoveryFileWriteDraftRejectReasonRejectsExecutionPlanForTaskLog(t *testing.T) {
	t.Parallel()

	content := `# OC-17: HAPPY-PATH SCENARIO EXECUTION PLAN

## SCENARIO OVERVIEW
- Nominal input

## ACCEPTANCE CRITERIA
| ID | Criterion | Verification Method |

## SUCCESS METRICS
| ID | Metric | Target |

## EXECUTION PHASES
- [ ] Submit request
- [ ] Capture evidence
`
	reason := recoveryFileWriteDraftRejectReason(content, "Test/test-execution-oc17-happy-path-scenario.md")
	if !strings.Contains(reason, "execution plan/checklist") {
		t.Fatalf("reason = %q, want execution plan rejection", reason)
	}
}

func TestRecoveryFileWriteDraftRejectReasonAllowsValidationExecutionPlanDeliverable(t *testing.T) {
	t.Parallel()

	content := `# First-Wave Task Execution & Flow Advancement Validation Plan

## Validation Objective
Confirm first-wave executable tasks enter execution and reach done.

## Validation Checkpoints
- [ ] Task queued successfully
- [ ] flow_advance() callable with valid flow_node_execution_id

## Success Criteria
- First-wave tasks reach done

## Failure Mode Registry
- Review gate unavailable

## Validation Execution Plan
1. Observe queued tasks.
2. Capture durable outputs and decisions.
`
	reason := recoveryFileWriteDraftRejectReason(content, "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md")
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

func TestRecoveryFileWriteDraftRejectReasonUsesDraftContentFromRecoveryArtifactWrapper(t *testing.T) {
	t.Parallel()

	content := `# Recovery file.write artifact

Task: OC-19: Design boundary scenario: rate limits and pipeline capacity
Target Path: Work/OC-19-DESIGN-BOUNDARY-SCENARIO-RATE-LIMITS-AND-PIPELINE-CAPACITY.md

## Last Write Failure

assistant draft for Work/OC-19-DESIGN-BOUNDARY-SCENARIO-RATE-LIMITS-AND-PIPELINE-CAPACITY.md wrote an execution plan/checklist instead of concrete execution evidence

## Draft Content

# OC-19: Design Boundary Scenario — Rate Limits and Pipeline Capacity

## Scenario Overview

This boundary scenario validates throttling and capacity behavior.

## Scenario 1: Rate Limit Threshold Testing

### Objective
Validate that the system returns HTTP 429 with Retry-After once the threshold is exceeded.

### Execution Phases

- Submit 10 requests in-window and confirm acceptance.
- Submit one more request and confirm throttling.

## Success Metrics

- HTTP 429 returned at threshold breach
- Retry-After header present
- No data loss during overflow

## Expected Overflow Response Example

HTTP response:
HTTP/1.1 503 Service Unavailable
Retry-After: 45
`
	reason := recoveryFileWriteDraftRejectReason(content, "Work/OC-19-DESIGN-BOUNDARY-SCENARIO-RATE-LIMITS-AND-PIPELINE-CAPACITY.md")
	if reason != "" {
		t.Fatalf("reason = %q, want wrapper draft to stay usable", reason)
	}
}

func TestRecoveryFileWriteDraftRejectReasonRejectsExecutionSpecCompletionMemoWithoutArtifacts(t *testing.T) {
	t.Parallel()

	content := `# PRD / Requirements Spec: Test Environment Setup (OC-14)

- Kind: prd_spec
- Playbook: execution_spec
- Source task: OC-14 (Prepare test environment and fixtures)

## Goals

1. Seed comprehensive test speaker data
2. Define approval workflow templates
3. Create review templates
4. Document environment setup steps

## Non-Goals

- Real speaker recruitment
- Production environment setup

## Scope

- Test speaker fixture
- Approval workflow definitions
- Review templates

## Constraints

- Data format: JSON
- Setup time: <30 minutes

## Success Metrics

1. Fixture Completeness: All 6 speaker profiles present ✓
2. Workflow Definition: 2 approval workflows defined ✓
3. Review Templates: 4 templates created ✓
4. Documentation Completeness: setup guide complete ✓

## Open Questions

None at this phase. Scope is clear, fixtures are created, documentation is complete.
`
	reason := recoveryFileWriteDraftRejectReason(content, "Work/OC-14-PREPARE-TEST-ENVIRONMENT-AND-FIXTURES.md")
	if !strings.Contains(reason, "execution-spec completion memo") {
		t.Fatalf("reason = %q, want execution-spec completion memo rejection", reason)
	}
}

func TestRecoveryFileWriteDraftRejectReasonRejectsDeliverableCompletionSummaryWithoutBody(t *testing.T) {
	t.Parallel()

	content := `Design complete. OC-21 boundary test design substantive deliverables produced:

**Test Design (Test/oc-21-boundary-test-design.md)**: 8.6 KB
- Rate-limit test scenario (100+ req/min -> HTTP 429 responses)

**Planning Artifacts** (all substantive, no scaffolds):
1. **PRD** (planning/prd-spec/oc-21-prd.md): 5 concrete goals
2. **Acceptance Criteria** (planning/prd-spec/oc-21-acceptance-criteria.md): 3 test scenarios
3. **Implementation Plan** (planning/prc-spec/oc-21-implementation-plan.md): 5 execution phases
4. **Dependency Log** (planning/prd-spec/oc-21-dependency-log.md): critical path analysis

**Quality Status**: Design phase complete; ready for internal review gate after execution completes.
`
	reason := recoveryFileWriteDraftRejectReason(content, "Test/oc-21-boundary-test-design.md")
	if !strings.Contains(reason, "completion summary") {
		t.Fatalf("reason = %q, want completion summary rejection", reason)
	}
}

func TestRecoveryFileWriteDraftRejectReasonRejectsRecoveryGuidanceSummaryPlaceholder(t *testing.T) {
	t.Parallel()

	content := `Excellent! The boundary test design file exists and shows **design phase is complete** with substantive planning artifacts. According to the recovery guidance, this is the durable output I should continue from.

Now I need to verify these artifact files exist and then proceed with the execution test phase. Let me check the planning artifacts:
`
	reason := recoveryFileWriteDraftRejectReason(content, "Test/oc-21-boundary-test-design.md")
	if !strings.Contains(reason, "intent to write") {
		t.Fatalf("reason = %q, want intent-to-write rejection", reason)
	}
}

func TestHandleTaskFileWriteWrongPathRewritesToCheckpointTargetWhenPreferredUnknown(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := fixture.engine.tasks.(*fakeTaskRepo)
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:         taskID,
			TaskNumber: 15,
			Title:      "Define what errors are acceptable",
			WorkStatus: "in_progress",
			Metadata: mustRawJSON(t, map[string]any{
				"recovery_file_write_checkpoint": map[string]any{
					"version":       1,
					"target_path":   "planning_spec/oc-15-acceptable-errors-definition.md",
					"artifact_path": ".ottercamp/recovery/planning_spec/oc-15-acceptable-errors-definition.md",
				},
			}),
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    "VALIDATION-SUCCESS-CRITERIA-OC13.md",
			"content": "# Acceptable Errors\n\nSubstantive content.\n",
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWrongPath(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWrongPath: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["path"]); got != "planning_spec/oc-15-acceptable-errors-definition.md" {
		t.Fatalf("path = %q, want checkpoint target", got)
	}
}

func TestRecoveryFileWriteDraftContentUsesPriorTurnDraftAfterCurrentNarration(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	currentTurnID := uuid.New()
	targetPath := "design-system/03-accessibility-standards.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Accessibility Standards

## Keyboard Navigation
- Every interactive control must be reachable without a mouse.
- Skip links must be visible on focus.
`),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &currentTurnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "Excellent. Now I have all the context needed and will write the document.",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	draft, rejectReason, ok := fixture.engine.recoveryFileWriteDraftContent(context.Background(), rt, targetPath)
	if !ok {
		t.Fatal("expected recovery file write draft")
	}
	if rejectReason != "" {
		t.Fatalf("rejectReason = %q, want empty", rejectReason)
	}
	if !strings.Contains(draft, "## Keyboard Navigation") {
		t.Fatalf("draft = %q, want prior substantive draft", draft)
	}
}

func TestRecoveryFileWriteDraftContentUsesContinuationSummaryDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	currentTurnID := uuid.New()
	targetPath := "planning/prd-spec/oc-24-infrastructure-spec.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &currentTurnID,
		Role:      "system",
		Status:    "final",
		Content: strings.TrimSpace(`[Continuation summary] # Infrastructure Specification

## Hosting Provider Selection
- Primary platform: Vercel
- CDN: Edge Network with caching rules

## Monitoring
- Uptime and error-rate alerts wired to PagerDuty
`),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &currentTurnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "I'm ready to work on OC-24. What would you like me to focus on first?",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	draft, rejectReason, ok := fixture.engine.recoveryFileWriteDraftContent(context.Background(), rt, targetPath)
	if !ok {
		t.Fatal("expected recovery file write draft")
	}
	if rejectReason != "" {
		t.Fatalf("rejectReason = %q, want empty", rejectReason)
	}
	if !strings.Contains(draft, "## Hosting Provider Selection") {
		t.Fatalf("draft = %q, want continuation summary draft", draft)
	}
}

func TestRecoveryFileOutputContextPrefersOlderSubstantiveReadOverNewerStubPath(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	oldTurnID := uuid.New()
	newTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "docs/migration-plan/oc-15-content-migration-plan.md",
				"content": strings.TrimSpace(`# OC-15: Content Migration Plan

## Executive Summary
This migration plan operationalizes the staged cutover and validation strategy.
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &newTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path":    "docs/migration-plan/oc-15-complete-migration-plan.md",
				"content": "Perfect. Now I have full context. Let me resume the task by completing the migration plan document.",
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryFileOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery file output context")
	}
	if targetPath != "docs/migration-plan/oc-15-content-migration-plan.md" {
		t.Fatalf("targetPath = %q, want older substantive path", targetPath)
	}
	if !strings.Contains(draft, "## Executive Summary") {
		t.Fatalf("draft = %q, want substantive draft", draft)
	}
}

func TestRecoveryHistoricalSubstantiveOutputContextSkipsNewerStrategyArtifacts(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	oldTurnID := uuid.New()
	newTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "planning/prd-spec/oc-24-infrastructure-spec.md",
				"content": strings.TrimSpace(`# Infrastructure Specification

## Hosting Provider
- Vercel
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &newTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "planning/strategy-artifact/oc-24-success-narrative.md",
				"content": strings.TrimSpace(`# Success Narrative

## Scenario
- Launch day goes well.
`),
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryHistoricalSubstantiveOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected historical substantive output context")
	}
	if targetPath != "planning/prd-spec/oc-24-infrastructure-spec.md" {
		t.Fatalf("targetPath = %q, want deliverable path", targetPath)
	}
	if !strings.Contains(draft, "## Hosting Provider") {
		t.Fatalf("draft = %q, want deliverable draft", draft)
	}
}

func TestRecoveryFileOutputContextPrefersExplicitDeliverablePathOverHistoricalPlanningArtifacts(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	description := "Design the core data model for speaking opportunities in the pipeline. Output: schema-definition.md with complete field specifications."
	plan := taskplan.Analyze("Design Pipeline Data Schema & Fields", &description)

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	if taskRepo.items == nil {
		taskRepo.items = map[uuid.UUID]repo.ProjectTask{}
	}
	taskRepo.items[taskID] = repo.ProjectTask{
		ID:             taskID,
		ProjectID:      projectID,
		OrganizationID: fixture.session.OrganizationID,
		Description:    &description,
		Metadata:       taskplan.ApplyMetadata(nil, plan),
	}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "planning/strategy-artifact/oc-9-success-narrative.md",
				"content": strings.TrimSpace(`# Success narrative

- Kind: strategy_artifact
- Source task: OC-9
`),
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryFileOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery file output context")
	}
	if targetPath != "schema-definition.md" {
		t.Fatalf("targetPath = %q, want schema-definition.md", targetPath)
	}
	if draft != "" {
		t.Fatalf("draft = %q, want empty explicit-deliverable fallback", draft)
	}
}

func TestRecoveryFileOutputContextPrefersInferredTestExecutionTargetOverPlanningArtifacts(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()
	description := "Execute test scenario 2 (edge cases): test capacity limits, concurrent assignments, boundary conditions. Log all test cases and verify system behavior under stress."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo, ok := fixture.engine.tasks.(*fakeTaskRepo)
	if !ok {
		t.Fatal("expected fake task repo")
	}
	taskRepo.items = map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:          taskID,
			TaskNumber:  13,
			Title:       "OC-4: Execute Test Scenario 2 (Edge Cases)",
			Description: &description,
		},
	}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "planning/discovery-plan/oc-9-assumption-log.md",
				"content": strings.TrimSpace(`# Assumption log

- Kind: discovery_plan
- Source task: OC-9
`),
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryFileOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery file output context")
	}
	if targetPath != "Test/test-execution-oc13-scenario2-edge-cases.md" {
		t.Fatalf("targetPath = %q, want inferred test execution target", targetPath)
	}
	if draft != "" {
		t.Fatalf("draft = %q, want empty inferred-target fallback", draft)
	}
}

func TestLoadRecoveryResumeStateSynthesizesDraftForTestExecutionTask(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	initialMessageID := uuid.New()
	description := "Execute test scenario 2 (edge cases): test capacity limits, concurrent assignments, boundary conditions. Log all test cases and verify system behavior under stress."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  13,
				Title:       "OC-4: Execute Test Scenario 2 (Edge Cases)",
				Description: &description,
				WorkStatus:  "blocked",
				Metadata: mustRawJSON(t, map[string]any{
					"recovery_file_write_checkpoint": map[string]any{
						"version":        1,
						"blocker_class":  "repeated_non_substantive_recovery_checkpoint",
						"target_path":    "Test/test-execution-oc13-scenario2-edge-cases.md",
						"failure_reason": "repeated non-substantive recovery drafts",
					},
				}),
			},
		},
	}

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: initialMessageID,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	state, ok := fixture.engine.loadRecoveryResumeState(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery resume state")
	}
	if state.targetPath != "Test/test-execution-oc13-scenario2-edge-cases.md" {
		t.Fatalf("targetPath = %q, want inferred test execution target", state.targetPath)
	}
	if !strings.Contains(state.summaryDraft, "## Test Cases") {
		t.Fatalf("summaryDraft = %q, want synthesized test execution scaffold", state.summaryDraft)
	}
}

func TestLoadRecoveryResumeStateRejectsWrongTaskTargetDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	orgID := fixture.session.OrganizationID
	projectSlug := "resume-state-task-15"
	orgSlug := "test-org"

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				TaskNumber:     15,
				Title:          "Define what errors are acceptable",
				OrganizationID: orgID,
				ProjectID:      projectID,
				WorkStatus:     "blocked",
				Metadata: mustRawJSON(t, map[string]any{
					"recovery_file_write_checkpoint": map[string]any{
						"version":        1,
						"blocker_class":  "durable_recovery_checkpoint",
						"target_path":    "planning_spec/oc-15-acceptable-errors-definition.md",
						"failure_reason": "repeated non-substantive recovery drafts",
					},
				}),
			},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: orgID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		orgID: {ID: orgID, Slug: orgSlug},
	}}

	targetAbs := filepath.Join(dataDir, "workspaces", projectSlug, "planning_spec", "oc-15-acceptable-errors-definition.md")
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	wrongDraft := "The target file OC-16-WHAT-MUST-BE-FIXED.md already exists with substantive, production-ready content."
	if err := os.WriteFile(targetAbs, []byte(wrongDraft), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	state, ok := fixture.engine.loadRecoveryResumeState(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery resume state")
	}
	if state.targetPath != "planning_spec/oc-15-acceptable-errors-definition.md" {
		t.Fatalf("targetPath = %q, want checkpoint target", state.targetPath)
	}
	if state.targetDraft != "" {
		t.Fatalf("targetDraft = %q, want wrong-task draft to be rejected", state.targetDraft)
	}
	if !strings.Contains(state.targetDraftRejectedReason, "different task") {
		t.Fatalf("targetDraftRejectedReason = %q, want different-task rejection", state.targetDraftRejectedReason)
	}
}

func TestLoadRecoveryResumeStateIncludesValidationSiblingContext(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	task16ID := uuid.New()
	task17ID := uuid.New()
	task13ID := uuid.New()
	description := "Orchestration task: Validate that first-wave executable tasks can enter execution, advance through flows, and produce outputs. Parent task for first-wave validation subtasks."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				TaskNumber:     12,
				Title:          "V2: Validate First-Wave Task Execution & Flow Advancement",
				Description:    &description,
				WorkStatus:     "in_progress",
				Metadata: mustRawJSON(t, map[string]any{
					"recovery_file_write_checkpoint": map[string]any{
						"version":        1,
						"blocker_class":  "repeated_non_substantive_recovery_checkpoint",
						"target_path":    "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md",
						"failure_reason": "repeated non-substantive recovery drafts",
					},
				}),
			},
			task16ID: {
				ID:             task16ID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				TaskNumber:     16,
				Title:          "V2-1b: Execute First-Wave Test & Produce Results",
				WorkStatus:     "done",
			},
			task17ID: {
				ID:             task17ID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				TaskNumber:     17,
				Title:          "V2-2b: Execute Error-Handling Tests & Results",
				WorkStatus:     "done",
			},
			task13ID: {
				ID:             task13ID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				TaskNumber:     13,
				Title:          "V3: Validate Wave Transition & Later-Wave Queuing",
				WorkStatus:     "draft",
			},
		},
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	state, ok := fixture.engine.loadRecoveryResumeState(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery resume state")
	}
	if len(state.contextNotes) == 0 {
		t.Fatal("expected validation sibling context notes")
	}
	joined := strings.Join(state.contextNotes, "\n")
	if !strings.Contains(joined, "Task 16 (V2-1b: Execute First-Wave Test & Produce Results): done") {
		t.Fatalf("contextNotes = %q, want task 16 status", joined)
	}
	if !strings.Contains(joined, "Task 17 (V2-2b: Execute Error-Handling Tests & Results): done") {
		t.Fatalf("contextNotes = %q, want task 17 status", joined)
	}
	if strings.Contains(joined, "Task 13") {
		t.Fatalf("contextNotes = %q, did not want unrelated V3 task", joined)
	}
}

func TestRecoveryDraftClearlyBelongsToDifferentTaskRejectsMixedTaskReferences(t *testing.T) {
	t.Parallel()

	taskRecord := repo.ProjectTask{
		TaskNumber: 21,
		Title:      "Design boundary test: rate limits and max pipeline capacity",
	}
	content := strings.TrimSpace(`
The workspace appears to have minimal work. Based on the context materials, OC-21 was previously rejected for:

1. Scenario specification mismatch — test executed error handling instead of the OC-17 Scenario 3 (Classification Uncertainty)
2. Empty planning artifacts — all 4 scaffolds with placeholder text
`)
	if !recoveryDraftClearlyBelongsToDifferentTask(taskRecord, "Test/oc-21-boundary-test-design.md", content) {
		t.Fatal("expected mixed OC references to be treated as different-task content")
	}
}

func TestRecoveryDraftClearlyBelongsToDifferentTaskRejectsSemanticSiblingScenarioMismatch(t *testing.T) {
	t.Parallel()

	description := "Execute all error scenarios. Verify error messages, retry logic, fallback paths. Record whether each error is caught, logged, and recoverable."
	taskRecord := repo.ProjectTask{
		TaskNumber:  10,
		Title:       "Validation Execution: Run error-handling scenarios and validate recovery",
		Description: &description,
	}
	content := strings.TrimSpace(`
# OC-10: Duplicate Email Submission - Execution Log

## Objective
Validate duplicate email handling and duplicate key rejection.

## Findings
- Duplicate email submission returned 409 conflict twice.
- Duplicate key violation was logged for the email collision path.
- The database rejected the duplicate at the constraint layer, not by application logic.
`)

	if !recoveryDraftClearlyBelongsToDifferentTask(taskRecord, "Work/oc-10-timeout-recovery-execution.md", content) {
		t.Fatal("expected sibling-scenario execution log to be treated as different-task content")
	}
}

func TestRecoveryDraftClearlyBelongsToDifferentTaskAllowsMatchingTaskScopeDraft(t *testing.T) {
	t.Parallel()

	description := "Execute all error scenarios. Verify error messages, retry logic, fallback paths. Record whether each error is caught, logged, and recoverable."
	taskRecord := repo.ProjectTask{
		TaskNumber:  10,
		Title:       "Validation Execution: Run error-handling scenarios and validate recovery",
		Description: &description,
	}
	content := strings.TrimSpace(`
# OC-10: Error-Handling Recovery Execution Log

## Objective
Validate timeout recovery, retry logic, and fallback paths across the error-handling scenarios.

## Findings
- Timeout recovery retried once and recovered without data loss.
- Fallback path preserved the queued work when the primary operation exceeded the timeout window.
- Error messages were explicit and the failure remained recoverable.
`)

	if recoveryDraftClearlyBelongsToDifferentTask(taskRecord, "Work/oc-10-timeout-recovery-execution.md", content) {
		t.Fatal("matching recovery draft should not be rejected")
	}
}

func TestLoadRecoveryResumeStateRejectsSemanticSiblingScenarioDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	projectSlug := "semantic-sibling-recovery"
	orgSlug := "acme"
	description := "Execute all error scenarios. Verify error messages, retry logic, fallback paths. Record whether each error is caught, logged, and recoverable."
	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.engine.tasks = &fakeTaskRepo{items: map[uuid.UUID]repo.ProjectTask{
		taskID: {
			ID:             taskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			TaskNumber:     10,
			Title:          "Validation Execution: Run error-handling scenarios and validate recovery",
			Description:    &description,
			WorkStatus:     "blocked",
			Metadata: mustRawJSON(t, map[string]any{
				"recovery_file_write_checkpoint": map[string]any{
					"version":        1,
					"target_path":    "Work/oc-10-timeout-recovery-execution.md",
					"artifact_path":  ".ottercamp/recovery/Work/oc-10-timeout-recovery-execution.md",
					"failure_reason": "file.write content appears to be task narration about planning to write the deliverable, not the deliverable body itself. Write the concrete file contents directly.",
				},
			}),
		},
	}}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: orgID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		orgID: {ID: orgID, Slug: orgSlug},
	}}

	targetAbs := filepath.Join(dataDir, "workspaces", projectSlug, "Work", "oc-10-timeout-recovery-execution.md")
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	wrongDraft := strings.TrimSpace(`
# OC-10: Duplicate Email Submission - Execution Log

## Objective
Validate duplicate email handling and duplicate key rejection.

## Findings
- Duplicate email submission returned 409 conflict twice.
- Duplicate key violation was logged for the email collision path.
- The database rejected the duplicate at the constraint layer, not by application logic.
`)
	if err := os.WriteFile(targetAbs, []byte(wrongDraft), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	state, ok := fixture.engine.loadRecoveryResumeState(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery resume state")
	}
	if state.targetDraft != "" {
		t.Fatalf("targetDraft = %q, want semantic sibling draft to be rejected", state.targetDraft)
	}
	if !strings.Contains(state.targetDraftRejectedReason, "different task") {
		t.Fatalf("targetDraftRejectedReason = %q, want different-task rejection", state.targetDraftRejectedReason)
	}
}

func TestRecoveryFileWriteCheckpointCandidatePrefersHistoricalSubstantivePathOverInitialMessageMetadata(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	poisonedInitialID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		ID:        poisonedInitialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    "resume",
			"recovery_checkpoint_target_path":    "docs/migration-plan/oc-15-complete-migration-plan.md",
			"recovery_checkpoint_artifact_path":  ".ottercamp/recovery/docs/migration-plan/oc-15-complete-migration-plan.md",
			"recovery_checkpoint_failure_reason": "placeholder",
		}),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "docs/migration-plan/oc-15-content-migration-plan.md",
				"content": strings.TrimSpace(`# OC-15: Content Migration Plan

## Executive Summary
This migration plan operationalizes the staged cutover and validation strategy.
`),
			},
		})),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: poisonedInitialID,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	checkpoint, ok := fixture.engine.recoveryFileWriteCheckpointCandidate(context.Background(), rt, "placeholder")
	if !ok {
		t.Fatal("expected recovery checkpoint candidate")
	}
	if checkpoint.TargetPath != "docs/migration-plan/oc-15-content-migration-plan.md" {
		t.Fatalf("TargetPath = %q, want historical substantive path", checkpoint.TargetPath)
	}
	if checkpoint.ArtifactPath != "" {
		t.Fatalf("ArtifactPath = %q, want empty after poisoned checkpoint override", checkpoint.ArtifactPath)
	}
}

func TestRecoveryFileWriteCheckpointCandidateRejectsCheckpointFromDifferentTaskContent(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	poisonedInitialID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				TaskNumber: 15,
				WorkStatus: "blocked",
			},
		},
	}

	fixture.messages.create(repo.ChatMessage{
		ID:        poisonedInitialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    "resume",
			"recovery_checkpoint_target_path":    "agents/speaker-validation-agent.md",
			"recovery_checkpoint_failure_reason": "placeholder",
		}),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "deliverables/oc-15-validation-report.md",
				"content": strings.TrimSpace(`# Validation Report

## Executive Summary
This report synthesizes the speaker pipeline validation findings and recommendations.
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "agents/speaker-validation-agent.md",
				"content": strings.TrimSpace(`# Speaker Validation Agent Specification

**Task**: OC-13
`),
			},
		})),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: poisonedInitialID,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	checkpoint, ok := fixture.engine.recoveryFileWriteCheckpointCandidate(context.Background(), rt, "placeholder")
	if !ok {
		t.Fatal("expected recovery checkpoint candidate")
	}
	if checkpoint.TargetPath != "deliverables/oc-15-validation-report.md" {
		t.Fatalf("TargetPath = %q, want current-task substantive path", checkpoint.TargetPath)
	}
}

func TestRecoveryHistoricalSubstantiveOutputContextPrefersWriteTargetOverPlanningRead(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()
	dataDir := t.TempDir()
	projectID := uuid.New()
	projectSlug := "speaker-pipeline-ops-fresh-restart"
	orgSlug := "default"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.dataDir = dataDir
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "in_progress",
			},
		},
	}

	workspaceRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	generateReportsPath := filepath.Join(workspaceRoot, "src", "generate_reports.py")
	if err := os.MkdirAll(filepath.Dir(generateReportsPath), 0o755); err != nil {
		t.Fatalf("mkdir generate_reports.py: %v", err)
	}
	if err := os.WriteFile(generateReportsPath, []byte("def generate_pipeline_report(data):\n    return {'count': len(data)}\n"), 0o644); err != nil {
		t.Fatalf("write generate_reports.py: %v", err)
	}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "planning/metrics-framework/oc-16-instrumentation-plan.md",
				"content": strings.TrimSpace(`# Instrumentation plan

- Kind: metrics_framework
- Source task: OC-16
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.write",
			"output": map[string]any{
				"path":      "src/generate_reports.py",
				"byte_size": 68,
				"created":   false,
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryHistoricalSubstantiveOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected historical substantive output context")
	}
	if targetPath != "src/generate_reports.py" {
		t.Fatalf("targetPath = %q, want src/generate_reports.py", targetPath)
	}
	if !strings.Contains(draft, "def generate_pipeline_report") {
		t.Fatalf("draft = %q, want substantive code draft", draft)
	}
}

func TestRecoveryHistoricalSubstantiveOutputContextSkipsDifferentTaskDrafts(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				TaskNumber: 15,
				WorkStatus: "in_progress",
			},
		},
	}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "agents/speaker-validation-agent.md",
				"content": strings.TrimSpace(`# Speaker Validation Agent Specification

**Task**: OC-13
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "deliverables/oc-15-validation-report.md",
				"content": strings.TrimSpace(`# Validation Report

## Executive Summary
This report synthesizes findings, gaps, and recommendations for the speaker pipeline validation run.
`),
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryHistoricalSubstantiveOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected historical substantive output context")
	}
	if targetPath != "deliverables/oc-15-validation-report.md" {
		t.Fatalf("targetPath = %q, want current-task deliverable", targetPath)
	}
	if !strings.Contains(draft, "Executive Summary") {
		t.Fatalf("draft = %q, want current-task deliverable draft", draft)
	}
}

func TestRecoveryHistoricalSubstantiveOutputContextPrefersTaskMatchingDeliverable(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()
	description := "Synthesize all validation testing results into a comprehensive validation report."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  15,
				WorkStatus:  "blocked",
				Title:       "Generate Validation Report & Recommendations",
				Description: &description,
			},
		},
	}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "deliverables/oc-15-validation-workflow.md",
				"content": strings.TrimSpace(`# Validation Workflow

## Overview
This document describes the intake workflow.
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "deliverables/oc-15-validation-report.md",
				"content": strings.TrimSpace(`# Validation Report

## Findings
The speaker pipeline validation run passed core scenarios and identified follow-up recommendations.
`),
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryHistoricalSubstantiveOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected historical substantive output context")
	}
	if targetPath != "deliverables/oc-15-validation-report.md" {
		t.Fatalf("targetPath = %q, want report deliverable", targetPath)
	}
	if !strings.Contains(draft, "## Findings") {
		t.Fatalf("draft = %q, want report draft", draft)
	}
}

func TestRecoveryFileWriteCheckpointCandidatePrefersBetterTaskMatchedHistoricalTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	poisonedInitialID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()
	description := "Synthesize all validation testing results into a comprehensive validation report."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  15,
				WorkStatus:  "blocked",
				Title:       "Generate Validation Report & Recommendations",
				Description: &description,
			},
		},
	}

	fixture.messages.create(repo.ChatMessage{
		ID:        poisonedInitialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    "resume",
			"recovery_checkpoint_target_path":    "deliverables/oc-15-validation-workflow.md",
			"recovery_checkpoint_failure_reason": "placeholder",
		}),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "deliverables/oc-15-validation-workflow.md",
				"content": strings.TrimSpace(`# Validation Workflow

## Overview
This document describes the intake workflow.
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "deliverables/oc-15-validation-report.md",
				"content": strings.TrimSpace(`# Validation Report

## Findings
The report captures validation results, recommendations, and next steps.
`),
			},
		})),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: poisonedInitialID,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	checkpoint, ok := fixture.engine.recoveryFileWriteCheckpointCandidate(context.Background(), rt, "placeholder")
	if !ok {
		t.Fatal("expected recovery checkpoint candidate")
	}
	if checkpoint.TargetPath != "deliverables/oc-15-validation-report.md" {
		t.Fatalf("TargetPath = %q, want report target", checkpoint.TargetPath)
	}
}

func TestRecoveryFileWriteCheckpointCandidatePrefersHistoricalTargetHintOverGenericSummary(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	poisonedInitialID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()
	description := "Document as workflow specification."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  11,
				WorkStatus:  "blocked",
				Title:       "Document as workflow specification.",
				Description: &description,
			},
		},
	}

	fixture.messages.create(repo.ChatMessage{
		ID:        poisonedInitialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    "resume",
			"recovery_checkpoint_target_path":    "deliverables/oc-11-task-summary.md",
			"recovery_checkpoint_failure_reason": "placeholder",
		}),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.write",
			"output": map[string]any{
				"path":      "deliverables/oc-11-validation-workflow-spec.md",
				"byte_size": 788,
				"created":   true,
			},
		})),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: poisonedInitialID,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	checkpoint, ok := fixture.engine.recoveryFileWriteCheckpointCandidate(context.Background(), rt, "placeholder")
	if !ok {
		t.Fatal("expected recovery checkpoint candidate")
	}
	if checkpoint.TargetPath != "deliverables/oc-11-validation-workflow-spec.md" {
		t.Fatalf("TargetPath = %q, want workflow spec target", checkpoint.TargetPath)
	}
}

func TestRecoveryFileWriteCheckpointCandidatePrefersHistoricalTargetHintOverPlanningCheckpoint(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	poisonedInitialID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()
	description := "Verify the speaker pipeline data model, schema, and integration points."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  12,
				WorkStatus:  "blocked",
				Title:       "Validate Speaker Pipeline Data Model",
				Description: &description,
			},
		},
	}

	fixture.messages.create(repo.ChatMessage{
		ID:        poisonedInitialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    "resume",
			"recovery_checkpoint_target_path":    "planning/prd-spec/oc-12-acceptance-criteria.md",
			"recovery_checkpoint_failure_reason": "placeholder",
		}),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.write",
			"output": map[string]any{
				"path":      "planning/prd-spec/oc-12-validation-report.md",
				"byte_size": 8192,
				"created":   true,
			},
		})),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: poisonedInitialID,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	checkpoint, ok := fixture.engine.recoveryFileWriteCheckpointCandidate(context.Background(), rt, "placeholder")
	if !ok {
		t.Fatal("expected recovery checkpoint candidate")
	}
	if checkpoint.TargetPath != "planning/prd-spec/oc-12-validation-report.md" {
		t.Fatalf("TargetPath = %q, want validation report target", checkpoint.TargetPath)
	}
}

func TestPersistRecoveryFileWriteCheckpointKeepsExistingSubstantiveTargetPath(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "sam-blog"
	orgSlug := "default"
	workspaceRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	existingTarget := "docs/migration-plan/oc-15-content-migration-plan.md"
	driftTarget := "docs/migration-plan/oc-15-complete-migration-plan.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	existingMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   existingTarget,
		ArtifactPath: ".ottercamp/recovery/docs/migration-plan/oc-15-content-migration-plan.md",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "in_progress",
				Metadata:       existingMetadata,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	existingAbs := filepath.Join(workspaceRoot, filepath.FromSlash(existingTarget))
	if err := os.MkdirAll(filepath.Dir(existingAbs), 0o755); err != nil {
		t.Fatalf("mkdir existing target: %v", err)
	}
	if err := os.WriteFile(existingAbs, []byte("# Migration Plan\n\n## Executive Summary\nSubstantive body.\n"), 0o644); err != nil {
		t.Fatalf("write existing target: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	if err := fixture.engine.persistRecoveryFileWriteCheckpoint(context.Background(), rt, driftTarget, ".ottercamp/recovery/docs/migration-plan/oc-15-complete-migration-plan.md", "placeholder", uuid.New()); err != nil {
		t.Fatalf("persistRecoveryFileWriteCheckpoint: %v", err)
	}

	updated := taskRepo.items[taskID]
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updated.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != existingTarget {
		t.Fatalf("TargetPath = %q, want %q", checkpoint.TargetPath, existingTarget)
	}
}

func TestPersistRecoveryFileWriteCheckpointDiscardsCrossTaskExistingTargetPath(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	workspaceRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	poisonedTarget := "Test/oc-10-fulfillment-readiness-test-plan.md"
	currentTarget := "Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	existingMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   poisonedTarget,
		ArtifactPath: ".ottercamp/recovery/Test/oc-10-fulfillment-readiness-test-plan.md",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				TaskNumber:     13,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "blocked",
				Metadata:       existingMetadata,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	poisonedAbs := filepath.Join(workspaceRoot, filepath.FromSlash(poisonedTarget))
	if err := os.MkdirAll(filepath.Dir(poisonedAbs), 0o755); err != nil {
		t.Fatalf("mkdir poisoned target: %v", err)
	}
	if err := os.WriteFile(poisonedAbs, []byte("# OC-10 Test Plan\n\nSubstantive body.\n"), 0o644); err != nil {
		t.Fatalf("write poisoned target: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	if err := fixture.engine.persistRecoveryFileWriteCheckpoint(context.Background(), rt, currentTarget, ".ottercamp/recovery/Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md", "placeholder", uuid.New()); err != nil {
		t.Fatalf("persistRecoveryFileWriteCheckpoint: %v", err)
	}

	updated := taskRepo.items[taskID]
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updated.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != currentTarget {
		t.Fatalf("TargetPath = %q, want %q", checkpoint.TargetPath, currentTarget)
	}
}

func TestPersistRecoveryFileWriteCheckpointPrefersAuthoritativeFailureTargetPath(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	workspaceRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	poisonedTarget := "planning/strategy-artifact/oc-20-success-narrative.md"
	deliverableTarget := "schemas/pipeline-schema-v1.0.md"
	failureReason := "This execution-first task already has an explicit deliverable path `schemas/pipeline-schema-v1.0.md`. Do not write `planning/strategy-artifact/oc-20-success-narrative.md` during task execution. Continue the concrete deliverable instead."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	existingMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   poisonedTarget,
		ArtifactPath: ".ottercamp/recovery/planning/strategy-artifact/oc-20-success-narrative.md",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				TaskNumber:     20,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "blocked",
				Metadata:       existingMetadata,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	poisonedAbs := filepath.Join(workspaceRoot, filepath.FromSlash(poisonedTarget))
	if err := os.MkdirAll(filepath.Dir(poisonedAbs), 0o755); err != nil {
		t.Fatalf("mkdir poisoned target: %v", err)
	}
	if err := os.WriteFile(poisonedAbs, []byte("# Success narrative\n\nSubstantive body.\n"), 0o644); err != nil {
		t.Fatalf("write poisoned target: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	if err := fixture.engine.persistRecoveryFileWriteCheckpoint(context.Background(), rt, deliverableTarget, ".ottercamp/recovery/schemas/pipeline-schema-v1.0.md", failureReason, uuid.New()); err != nil {
		t.Fatalf("persistRecoveryFileWriteCheckpoint: %v", err)
	}

	updated := taskRepo.items[taskID]
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updated.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != deliverableTarget {
		t.Fatalf("TargetPath = %q, want %q", checkpoint.TargetPath, deliverableTarget)
	}
}

func TestPersistRecoveryFileWriteCheckpointPrefersBetterTaskMatchedExistingTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	existingTarget := "deliverables/OC-18-INTAKE-FRAMEWORK-SCHEMA.md"
	driftTarget := "schemas/scoring-algorithm-v1.0.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	existingMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   existingTarget,
		ArtifactPath: ".ottercamp/recovery/deliverables/OC-18-INTAKE-FRAMEWORK-SCHEMA.md",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	description := "Define sourcing channels, intake form structure, lead qualification criteria, and triage logic for speaking opportunity identification and capture."
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				TaskNumber:     18,
				Title:          "OC-01: Design Intake Framework",
				Description:    &description,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "blocked",
				Metadata:       existingMetadata,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	if err := fixture.engine.persistRecoveryFileWriteCheckpoint(context.Background(), rt, driftTarget, ".ottercamp/recovery/schemas/scoring-algorithm-v1.0.md", "flow rejection max visits exceeded", uuid.New()); err != nil {
		t.Fatalf("persistRecoveryFileWriteCheckpoint: %v", err)
	}

	updated := taskRepo.items[taskID]
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updated.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != existingTarget {
		t.Fatalf("TargetPath = %q, want %q", checkpoint.TargetPath, existingTarget)
	}
}

func TestPersistRecoveryFileWriteCheckpointKeepsCurrentTaskTargetWhenHistoricalSiblingArtifactExists(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "speaker-pipeline"
	orgSlug := "default"
	workspaceRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	existingTarget := "FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md"
	siblingTarget := "BOOTSTRAP-VALIDATION-OC9.md"
	description := "Orchestration task: Validate that first-wave executable tasks can enter execution, advance through flows, and produce outputs. Parent task for first-wave validation subtasks."

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	existingMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   existingTarget,
		ArtifactPath: ".ottercamp/recovery/FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				TaskNumber:     12,
				Title:          "V2: Validate First-Wave Task Execution & Flow Advancement",
				Description:    &description,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "blocked",
				Metadata:       existingMetadata,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	existingAbs := filepath.Join(workspaceRoot, filepath.FromSlash(existingTarget))
	if err := os.MkdirAll(filepath.Dir(existingAbs), 0o755); err != nil {
		t.Fatalf("mkdir existing target: %v", err)
	}
	if err := os.WriteFile(existingAbs, []byte("The recovery system is blocking all reads other than the recovery artifact itself."), 0o644); err != nil {
		t.Fatalf("write existing target: %v", err)
	}

	siblingAbs := filepath.Join(workspaceRoot, filepath.FromSlash(siblingTarget))
	if err := os.MkdirAll(filepath.Dir(siblingAbs), 0o755); err != nil {
		t.Fatalf("mkdir sibling target: %v", err)
	}
	siblingBody := "# Bootstrap Validation Report - OC-9\n\n**Task**: OC-9 (Task #9)\n\nSubstantive bootstrap validation content.\n"
	if err := os.WriteFile(siblingAbs, []byte(siblingBody), 0o644); err != nil {
		t.Fatalf("write sibling target: %v", err)
	}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path":    siblingTarget,
				"content": siblingBody,
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	if err := fixture.engine.persistRecoveryFileWriteCheckpoint(context.Background(), rt, existingTarget, ".ottercamp/recovery/FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md", "flow rejection max visits exceeded", uuid.New()); err != nil {
		t.Fatalf("persistRecoveryFileWriteCheckpoint: %v", err)
	}

	updated := taskRepo.items[taskID]
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updated.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != existingTarget {
		t.Fatalf("TargetPath = %q, want %q", checkpoint.TargetPath, existingTarget)
	}
}

func TestRewriteRecoveryCLIExecuteWithoutCommandToFileWriteUsesPriorTurnDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	targetPath := "docs/content-strategy.md"
	priorTurnID := uuid.New()
	currentTurnID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, ProjectID: projectID, WorkStatus: "in_progress", AssignedAgentID: &assignedAgentID},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Content Strategy

## Migration Approach
- Audit legacy content types before import.
- Map each retained post to the new information architecture.
`),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.write",
			"output": map[string]any{
				"path":      targetPath,
				"byte_size": 128,
				"created":   false,
			},
		})),
	})

	rt := &turnRuntime{
		recoveryTurn: true,
		session:      fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:        "cli-1",
		Name:      "cli.execute",
		Arguments: map[string]any{},
	}

	rewritten, err := fixture.engine.rewriteRecoveryCLIExecuteWithoutCommandToFileWrite(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("rewriteRecoveryCLIExecuteWithoutCommandToFileWrite: %v", err)
	}
	if rewritten {
		t.Fatal("expected rewrite helper to continue with mutated call")
	}
	if call.Name != "file.write" {
		t.Fatalf("call.Name = %q, want file.write", call.Name)
	}
	if got := stringValue(call.Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "## Migration Approach") {
		t.Fatalf("content = %q, want prior substantive draft", got)
	}
	if got, ok := call.Arguments["create_dirs"].(bool); !ok || !got {
		t.Fatalf("create_dirs = %v, want true", call.Arguments["create_dirs"])
	}
}

func TestCompletedWorkSignalFromMessagesAcceptsExplicitDeliverableWriteForExecutionFirstTask(t *testing.T) {
	t.Parallel()

	description := "Create Python script to generate reports. Deliverable: src/generate_reports.py with report templates and example outputs."
	plan := taskplan.Analyze("Build reporting and pipeline analytics script", &description)
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}
	turnID := uuid.New()
	messages := []repo.ChatMessage{
		{
			TurnID: &turnID,
			Role:   "tool_result",
			Status: "final",
			Content: string(mustRawJSON(t, map[string]any{
				"tool_name": "file.write",
				"output": map[string]any{
					"path":      "src/generate_reports.py",
					"byte_size": 14277,
					"created":   false,
				},
			})),
		},
	}

	signal, ok := completedWorkSignalFromMessages(taskRecord, messages, turnID)
	if !ok {
		t.Fatal("expected completion signal from explicit deliverable write")
	}
	if signal.filesCommitted != 1 {
		t.Fatalf("filesCommitted = %d, want 1", signal.filesCommitted)
	}
}

func TestCompletedWorkSignalFromMessagesIgnoresPlanningArtifactWriteForExecutionFirstTask(t *testing.T) {
	t.Parallel()

	description := "Create Python script to generate reports. Deliverable: src/generate_reports.py with report templates and example outputs."
	plan := taskplan.Analyze("Build reporting and pipeline analytics script", &description)
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}
	turnID := uuid.New()
	messages := []repo.ChatMessage{
		{
			TurnID: &turnID,
			Role:   "tool_result",
			Status: "final",
			Content: string(mustRawJSON(t, map[string]any{
				"tool_name": "file.write",
				"output": map[string]any{
					"path":      "planning/metrics-framework/oc-16-instrumentation-plan.md",
					"byte_size": 7020,
					"created":   false,
				},
			})),
		},
	}

	if _, ok := completedWorkSignalFromMessages(taskRecord, messages, turnID); ok {
		t.Fatal("unexpected completion signal from planning artifact write")
	}
}

func TestCompletedWorkSignalFromMessagesUsesSubstantiveRecoveryTargetRead(t *testing.T) {
	t.Parallel()

	taskRecord := repo.ProjectTask{
		TaskNumber: 21,
		Title:      "Design boundary test: rate limits and max pipeline capacity",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":     1,
				"target_path": "Test/oc-21-boundary-test-design.md",
			},
		}),
	}
	turnID := uuid.New()
	messages := []repo.ChatMessage{
		{
			TurnID: &turnID,
			Role:   "tool_result",
			Status: "final",
			Content: string(mustRawJSON(t, map[string]any{
				"tool_name": "file.read",
				"output": map[string]any{
					"path": "Test/oc-21-boundary-test-design.md",
					"content": `# OC-21: Boundary Test Design

## Executive Summary
This boundary test design defines concrete rate-limit and capacity scenarios with measurable success criteria and HTTP response expectations.

## Test Scenarios
- Scenario A: rate-limit threshold trigger
- Scenario B: pipeline capacity stress

## Success Criteria
- HTTP 429 with Retry-After
- Queue integrity preserved
- Graceful degradation at capacity
`,
				},
			})),
		},
	}

	signal, ok := completedWorkSignalFromMessages(taskRecord, messages, turnID)
	if !ok {
		t.Fatal("expected completion signal from substantive recovery target read")
	}
	if signal.filesCommitted != 1 {
		t.Fatalf("filesCommitted = %d, want 1", signal.filesCommitted)
	}
}

func TestCompletedWorkSignalFromMessagesIgnoresPlaceholderRecoveryTargetRead(t *testing.T) {
	t.Parallel()

	taskRecord := repo.ProjectTask{
		TaskNumber: 21,
		Title:      "Design boundary test: rate limits and max pipeline capacity",
		Metadata: mustRawJSON(t, map[string]any{
			"recovery_file_write_checkpoint": map[string]any{
				"version":     1,
				"target_path": "Test/oc-21-boundary-test-design.md",
			},
		}),
	}
	turnID := uuid.New()
	messages := []repo.ChatMessage{
		{
			TurnID: &turnID,
			Role:   "tool_result",
			Status: "final",
			Content: string(mustRawJSON(t, map[string]any{
				"tool_name": "file.read",
				"output": map[string]any{
					"path": "Test/oc-21-boundary-test-design.md",
					"content": `Excellent! The boundary test design file exists and shows design phase is complete. According to the recovery guidance, this is the durable output I should continue from.

Now I need to verify these artifact files exist and then proceed with the execution test phase. Let me check the planning artifacts:`,
				},
			})),
		},
	}

	if _, ok := completedWorkSignalFromMessages(taskRecord, messages, turnID); ok {
		t.Fatal("unexpected completion signal from placeholder recovery target read")
	}
}

func TestExplicitExecutionDeliverableWriteCompleted(t *testing.T) {
	t.Parallel()

	description := "Create Python script to generate reports. Deliverable: src/generate_reports.py with report templates and example outputs."
	plan := taskplan.Analyze("Build reporting and pipeline analytics script", &description)
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}

	if !explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "src/generate_reports.py",
			"byte_size": 14277,
		},
	}) {
		t.Fatal("expected explicit deliverable write to count as completed work")
	}

	if explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "planning/metrics-framework/oc-16-instrumentation-plan.md",
			"byte_size": 7020,
		},
	}) {
		t.Fatal("unexpected completion from planning artifact write")
	}
}

func TestExplicitExecutionDeliverableWriteCompletedRecognizesOutputPath(t *testing.T) {
	t.Parallel()

	description := "Test invalid request rejection. Output: test-result-validation.log with structured test evidence."
	plan := taskplan.Analyze("Test input validation", &description)
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}

	if !explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "test-result-validation.log",
			"byte_size": 4096,
		},
	}) {
		t.Fatal("expected Output: path to count as explicit deliverable write")
	}

	if explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "oc-17-validation-test-execution.py",
			"byte_size": 4096,
		},
	}) {
		t.Fatal("unexpected completion from alternate execution helper file")
	}
}

func TestExplicitExecutionDeliverableWriteCompletedRecognizesDirectoryOutputChildFile(t *testing.T) {
	t.Parallel()

	description := "Review SLA compliance report. Check that metrics are valid and analysis is sound. Output: Metrics review memo with approval status and any data quality concerns."
	plan := taskplan.Analyze("Review Metrics & Analysis", &description)
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}

	if !explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "Metrics/OC-26-QUALITY-GATE-SECOND-REVIEW.md",
			"byte_size": 10415,
		},
	}) {
		t.Fatal("expected child file under Output directory to count as explicit deliverable write")
	}

	if explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "planning/metrics-framework/oc-26-metric-tree.md",
			"byte_size": 10415,
		},
	}) {
		t.Fatal("unexpected completion from planning artifact outside Output directory")
	}
}

func TestExplicitExecutionDeliverableWriteCompletedRecognizesInferredTestExecutionLogTarget(t *testing.T) {
	t.Parallel()

	description := "Execute test scenario 2 (edge cases): test capacity limits, concurrent assignments, boundary conditions. Log all test cases and verify system behavior under stress."
	taskRecord := repo.ProjectTask{
		TaskNumber:  13,
		Title:       "OC-4: Execute Test Scenario 2 (Edge Cases)",
		Description: &description,
	}

	if !explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "Test/test-execution-oc13-scenario2-edge-cases.md",
			"byte_size": 9751,
		},
	}) {
		t.Fatal("expected inferred test execution log path to count as completed work")
	}

	if explicitExecutionDeliverableWriteCompleted(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "planning/discovery-plan/oc-9-validation-plan.md",
			"byte_size": 9751,
		},
	}) {
		t.Fatal("unexpected completion from unrelated planning artifact write")
	}
}

func TestShouldStopAfterExecutionArtifactWriteForPlannedArtifact(t *testing.T) {
	t.Parallel()

	description := "Document findings on sourcing channels, qualification criteria, and intake workflows."
	plan := taskplan.Plan{
		Mode:     taskplan.ModeExecutionFirst,
		Playbook: taskplan.PlaybookExecutionSpec,
		Artifacts: []taskplan.PlannedArtifact{
			{Slug: "oc-21-prd", Title: "OC-21 PRD", RepoPath: "planning/prd-spec/oc-21-prd.md"},
			{Slug: "oc-21-implementation-plan", Title: "OC-21 Implementation Plan", RepoPath: "planning/prd-spec/oc-21-implementation-plan.md"},
		},
	}
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}

	if !shouldStopAfterExecutionArtifactWrite(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "planning/prd-spec/oc-21-prd.md",
			"byte_size": 10245,
		},
	}) {
		t.Fatal("expected stop signal from planned execution artifact write")
	}
}

func TestShouldStopAfterExecutionArtifactWriteForExplicitDirectoryOutput(t *testing.T) {
	t.Parallel()

	description := "Review SLA compliance report. Check that metrics are valid and analysis is sound. Output: Metrics review memo with approval status and any data quality concerns."
	plan := taskplan.Analyze("Review Metrics & Analysis", &description)
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}

	if !shouldStopAfterExecutionArtifactWrite(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "Metrics/OC-26-QUALITY-GATE-SECOND-REVIEW.md",
			"byte_size": 10415,
		},
	}) {
		t.Fatal("expected stop signal from explicit deliverable directory write")
	}
}

func TestShouldStopAfterExecutionArtifactWriteIgnoresUndeclaredPath(t *testing.T) {
	t.Parallel()

	description := "Document findings on sourcing channels, qualification criteria, and intake workflows."
	plan := taskplan.Plan{
		Mode:     taskplan.ModeExecutionFirst,
		Playbook: taskplan.PlaybookExecutionSpec,
		Artifacts: []taskplan.PlannedArtifact{
			{Slug: "oc-21-prd", Title: "OC-21 PRD", RepoPath: "planning/prd-spec/oc-21-prd.md"},
		},
	}
	taskRecord := repo.ProjectTask{
		Description: &description,
		Metadata:    taskplan.ApplyMetadata(nil, plan),
	}

	if shouldStopAfterExecutionArtifactWrite(taskRecord, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "research/oc-21-extra-notes.md",
			"byte_size": 2048,
		},
	}) {
		t.Fatal("unexpected stop signal from undeclared path")
	}
}

func TestShouldStopAfterExecutionDeliverableWriteStopsForRecoveryCheckpointTarget(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	initialMessageID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				TaskNumber: 21,
				Title:      "Design boundary test: rate limits and max pipeline capacity",
				WorkStatus: "blocked",
			},
		},
	}
	fixture.messages.create(repo.ChatMessage{
		ID:        initialMessageID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    recoveryActionValidationResume,
			"recovery_checkpoint_target_path":    "Test/oc-21-boundary-test-design.md",
			"recovery_checkpoint_failure_reason": "repeated narrated recovery writes",
		}),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: initialMessageID,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	stop, err := fixture.engine.shouldStopAfterExecutionDeliverableWrite(context.Background(), rt, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "Test/oc-21-boundary-test-design.md",
			"byte_size": 8192,
		},
	})
	if err != nil {
		t.Fatalf("shouldStopAfterExecutionDeliverableWrite: %v", err)
	}
	if !stop {
		t.Fatal("expected stop signal from direct recovery write to checkpoint target")
	}
	if !rt.recoveryWriteDone {
		t.Fatal("recoveryWriteDone = false, want true after target write")
	}
}

func TestShouldStopAfterExecutionDeliverableWriteIgnoresNonTargetRecoveryWrite(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	initialMessageID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				TaskNumber: 21,
				Title:      "Design boundary test: rate limits and max pipeline capacity",
				WorkStatus: "blocked",
			},
		},
	}
	fixture.messages.create(repo.ChatMessage{
		ID:        initialMessageID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    recoveryActionValidationResume,
			"recovery_checkpoint_target_path":    "Test/oc-21-boundary-test-design.md",
			"recovery_checkpoint_failure_reason": "repeated narrated recovery writes",
		}),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: initialMessageID,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
		recoveryTurn: true,
	}

	stop, err := fixture.engine.shouldStopAfterExecutionDeliverableWrite(context.Background(), rt, ToolResult{
		Name: "file.write",
		Output: map[string]any{
			"path":      "Test/oc-26-pipeline-capacity-test-spec.md",
			"byte_size": 8192,
		},
	})
	if err != nil {
		t.Fatalf("shouldStopAfterExecutionDeliverableWrite: %v", err)
	}
	if stop {
		t.Fatal("unexpected stop signal from non-target recovery write")
	}
	if rt.recoveryWriteDone {
		t.Fatal("recoveryWriteDone = true, want false for non-target write")
	}
}

func TestBuildTaskReviewActionPromptWithoutArtifactContractWarnsAgainstInventedPlanningRequirements(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Define rate-limit and capacity tests: rapid fire 100+ requests/min to trigger rate limit; submissions when pipeline at 90%, 100% capacity. Document expected HTTP 429 responses and queue behavior. ~10 min."
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Metadata = mustRawJSON(t, map[string]any{
		"flow_node_execution_id": uuid.NewString(),
	})
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  21,
				Title:       "Design boundary test: rate limits and max pipeline capacity",
				Description: &description,
				WorkStatus:  "review",
				Metadata:    mustRawJSON(t, map[string]any{"bootstrap_first_wave_selected": true}),
			},
		},
	}

	prompt := fixture.engine.buildTaskReviewActionPrompt(context.Background(), fixture.session)
	if !strings.Contains(prompt, "Do not invent companion planning-artifact requirements") {
		t.Fatalf("prompt = %q, want invented-artifact warning", prompt)
	}
}

func TestBuildTaskReviewActionPromptIncludesExplicitArtifactContractPaths(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	description := "Prepare environment fixtures."
	plan := taskplan.Plan{
		Mode:     taskplan.ModeExecutionFirst,
		Playbook: taskplan.PlaybookExecutionSpec,
		Artifacts: []taskplan.PlannedArtifact{
			{Slug: "prd", Title: "PRD", RepoPath: "planning_spec/oc-14-prd.md"},
			{Slug: "acceptance", Title: "Acceptance Criteria", RepoPath: "planning_spec/oc-14-acceptance-criteria.md"},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:          taskID,
				TaskNumber:  14,
				Title:       "Prepare test environment and fixtures",
				Description: &description,
				WorkStatus:  "review",
				Metadata:    taskplan.ApplyMetadata(nil, plan),
			},
		},
	}

	prompt := fixture.engine.buildTaskReviewActionPrompt(context.Background(), fixture.session)
	if !strings.Contains(prompt, "PRD") {
		t.Fatalf("prompt = %q, want artifact contract title", prompt)
	}
	if !strings.Contains(prompt, "Do not reject for any other missing sibling files.") {
		t.Fatalf("prompt = %q, want bounded artifact guidance", prompt)
	}
}

func TestFlowNodeExecutionIDFromSessionMetadata(t *testing.T) {
	t.Parallel()

	executionID := uuid.New()
	session := &chat.ChatSession{
		Metadata: mustRawJSON(t, map[string]any{
			"flow_node_execution_id": executionID.String(),
		}),
	}

	got := flowNodeExecutionIDFromSessionMetadata(session)
	if got == nil || *got != executionID {
		t.Fatalf("flowNodeExecutionIDFromSessionMetadata() = %v, want %s", got, executionID)
	}

	session.Metadata = json.RawMessage(`{"flow_node_execution_id":"not-a-uuid"}`)
	if got := flowNodeExecutionIDFromSessionMetadata(session); got != nil {
		t.Fatalf("flowNodeExecutionIDFromSessionMetadata() invalid = %v, want nil", got)
	}
}

func TestTaskContinuationResumeMessageMetadataIncludesFlowNodeExecutionID(t *testing.T) {
	t.Parallel()

	executionID := uuid.New()
	session := &chat.ChatSession{
		Metadata: mustRawJSON(t, map[string]any{
			"flow_node_execution_id": executionID.String(),
		}),
	}

	var payload map[string]any
	if err := json.Unmarshal(taskContinuationResumeMessageMetadata(session, 2), &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if payload["flow_node_execution_id"] != executionID.String() {
		t.Fatalf("flow_node_execution_id = %v, want %s", payload["flow_node_execution_id"], executionID)
	}
	if payload["continuation_attempt"] != float64(2) {
		t.Fatalf("continuation_attempt = %v, want 2", payload["continuation_attempt"])
	}
}

func TestSyntheticContinuationActionMessageMetadataIncludesFlowNodeExecutionID(t *testing.T) {
	t.Parallel()

	executionID := uuid.New()
	session := &chat.ChatSession{
		Metadata: mustRawJSON(t, map[string]any{
			"flow_node_execution_id": executionID.String(),
		}),
	}

	var payload map[string]any
	if err := json.Unmarshal(syntheticContinuationActionMessageMetadata(session, "task_recovery_resume"), &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if payload["flow_node_execution_id"] != executionID.String() {
		t.Fatalf("flow_node_execution_id = %v, want %s", payload["flow_node_execution_id"], executionID)
	}
	if payload["source"] != "task_recovery_resume" {
		t.Fatalf("source = %v, want task_recovery_resume", payload["source"])
	}
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}
