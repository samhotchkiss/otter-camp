package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestFileWriteAtomicLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	out, err := executor.Execute(testExecCtx(), "file.write", map[string]any{
		"path":        "docs/readme.txt",
		"content":     "hello",
		"create_dirs": true,
	})
	if err != nil {
		t.Fatalf("file.write: %v", err)
	}
	if out["created"] != true {
		t.Fatalf("created = %v, want true", out["created"])
	}
	entries, err := filepath.Glob(filepath.Join(root, "docs", ".ottercamp-tmp-*"))
	if err != nil {
		t.Fatalf("glob tmp files: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected temp files: %v", entries)
	}
}

func TestFileEditAmbiguousMatchDoesNotModifyFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "dup.txt")
	if err := os.WriteFile(target, []byte("alpha\nalpha\n"), 0o644); err != nil {
		t.Fatalf("write dup file: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.edit", map[string]any{
		"path":       "dup.txt",
		"old_string": "alpha",
		"new_string": "beta",
	})
	if err != nil {
		t.Fatalf("file.edit: %v", err)
	}
	if out["error"] != "ambiguous_match" {
		t.Fatalf("error = %v, want ambiguous_match", out["error"])
	}
	if out["count"] != 2 {
		t.Fatalf("count = %v, want 2", out["count"])
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read dup file: %v", err)
	}
	if string(got) != "alpha\nalpha\n" {
		t.Fatalf("file changed unexpectedly: %q", string(got))
	}
}

func TestFileEditOldStringNotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.edit", map[string]any{
		"path":       "file.txt",
		"old_string": "missing",
		"new_string": "value",
	})
	if err != nil {
		t.Fatalf("file.edit: %v", err)
	}
	if out["error"] != "old_string_not_found" {
		t.Fatalf("error = %v, want old_string_not_found", out["error"])
	}
}

func TestGitCommitMainBranchReturnsPayloadError(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if len(args) == 3 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" && args[2] == "HEAD" {
				return helperCommand(ctx, "git-branch-main", nil)
			}
			t.Fatalf("unexpected git invocation: %v", args)
			return nil
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.commit", map[string]any{"message": "test commit"})
	if err != nil {
		t.Fatalf("git.commit: %v", err)
	}
	if out["error"] != "cannot_commit_to_main" {
		t.Fatalf("error = %v, want cannot_commit_to_main", out["error"])
	}
}

func TestGitPushForceProtectedBranchDeniedWithoutGitInvocation(t *testing.T) {
	calls := 0
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(_ context.Context, _ string, _ ...string) *exec.Cmd {
			calls++
			return nil
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.push", map[string]any{
		"branch": "shared/staging",
		"force":  true,
	})
	if err != nil {
		t.Fatalf("git.push: %v", err)
	}
	if out["error"] != "force_push_denied" {
		t.Fatalf("error = %v, want force_push_denied", out["error"])
	}
	if calls != 0 {
		t.Fatalf("git should not be invoked, calls=%d", calls)
	}
}

func TestGitPushForceFeatureBranchUsesForceFlag(t *testing.T) {
	var pushArgs []string
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if len(args) == 0 {
				t.Fatal("missing git args")
			}
			switch args[0] {
			case "rev-list":
				return helperCommand(ctx, "git-rev-list-2", nil)
			case "push":
				pushArgs = append([]string(nil), args...)
				return helperCommand(ctx, "git-push-ok", nil)
			default:
				t.Fatalf("unexpected git invocation: %v", args)
				return nil
			}
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.push", map[string]any{
		"remote": "origin",
		"branch": "feature/demo",
		"force":  true,
	})
	if err != nil {
		t.Fatalf("git.push: %v", err)
	}
	if out["branch"] != "feature/demo" {
		t.Fatalf("branch = %v, want feature/demo", out["branch"])
	}
	if !containsArg(pushArgs, "--force") {
		t.Fatalf("push args missing --force: %v", pushArgs)
	}
}

type stubMemoryRecorder struct {
	called      bool
	lastAgentID uuid.UUID
	lastContent string
}

func (s *stubMemoryRecorder) RecordExplicit(_ context.Context, agentID uuid.UUID, content, scope, sensitivity string, tags []string) (uuid.UUID, string, error) {
	s.called = true
	s.lastAgentID = agentID
	s.lastContent = content
	return uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), "queued", nil
}

func TestMemoryRecordDelegatesToRecorder(t *testing.T) {
	recorder := &stubMemoryRecorder{}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot:  t.TempDir(),
		MemoryRecorder: recorder,
	})

	out, err := executor.Execute(testExecCtx(), "memory.record", map[string]any{
		"content": "store this",
		"scope":   "agent",
	})
	if err != nil {
		t.Fatalf("memory.record: %v", err)
	}
	if !recorder.called {
		t.Fatal("expected memory recorder to be called")
	}
	if recorder.lastContent != "store this" {
		t.Fatalf("content = %q, want store this", recorder.lastContent)
	}
	if out["status"] != "queued" {
		t.Fatalf("status = %v, want queued", out["status"])
	}
}

type fakeInboxRepo struct {
	created []repo.InboxItem
}

func (f *fakeInboxRepo) Create(_ context.Context, item repo.InboxItem) (repo.InboxItem, error) {
	item.ID = uuid.New()
	f.created = append(f.created, item)
	return item, nil
}

func (f *fakeInboxRepo) ListForUser(context.Context, uuid.UUID, uuid.UUID, repo.InboxListOptions) ([]repo.InboxItem, error) {
	return nil, nil
}

func (f *fakeInboxRepo) ListBroadcast(context.Context, uuid.UUID, repo.InboxListOptions) ([]repo.InboxItem, error) {
	return nil, nil
}

func (f *fakeInboxRepo) MarkActed(context.Context, uuid.UUID, uuid.UUID) (repo.InboxItem, error) {
	return repo.InboxItem{}, nil
}

func TestEmailComposeCreatesDraftActionReviewInboxItem(t *testing.T) {
	inbox := &fakeInboxRepo{}
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: t.TempDir()})
	executor.inbox = inbox

	out, err := executor.Execute(testExecCtx(), "email.compose", map[string]any{
		"to":      "ops@example.com",
		"subject": "Status",
	})
	if err != nil {
		t.Fatalf("email.compose: %v", err)
	}
	if out["status"] != "pending_review" {
		t.Fatalf("status = %v, want pending_review", out["status"])
	}
	if len(inbox.created) != 1 {
		t.Fatalf("created inbox items = %d, want 1", len(inbox.created))
	}
	if inbox.created[0].ItemType != "draft_action_review" {
		t.Fatalf("item_type = %q, want draft_action_review", inbox.created[0].ItemType)
	}
	var payload map[string]any
	if err := json.Unmarshal(inbox.created[0].ActionPayload, &payload); err != nil {
		t.Fatalf("unmarshal action payload: %v", err)
	}
	if payload["action"] != "email.compose" {
		t.Fatalf("action = %v, want email.compose", payload["action"])
	}
	input, _ := payload["input"].(map[string]any)
	if strings.TrimSpace(fmt.Sprintf("%v", input["to"])) != "ops@example.com" {
		t.Fatalf("payload input.to = %v, want ops@example.com", input["to"])
	}
}
