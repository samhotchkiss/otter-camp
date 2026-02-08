package gitserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type fakeActivityStore struct {
	mu    sync.Mutex
	calls []activityCall
}

type activityCall struct {
	workspaceID string
	input       store.CreateActivityInput
}

func (f *fakeActivityStore) CreateWithWorkspaceID(ctx context.Context, workspaceID string, input store.CreateActivityInput) (*store.Activity, error) {
	f.mu.Lock()
	f.calls = append(f.calls, activityCall{workspaceID: workspaceID, input: input})
	f.mu.Unlock()

	return &store.Activity{
		ID:        "activity-1",
		OrgID:     workspaceID,
		Action:    input.Action,
		Metadata:  input.Metadata,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (f *fakeActivityStore) Calls() []activityCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyCalls := make([]activityCall, len(f.calls))
	copy(copyCalls, f.calls)
	return copyCalls
}

func TestGitHandlerReceivePackUnauthorized(t *testing.T) {
	h := &Handler{
		RepoResolver: func(ctx context.Context, orgID, projectID string) (string, error) {
			return "/tmp/nowhere", nil
		},
	}

	router := chi.NewRouter()
	router.Mount("/git", AuthMiddleware(func(ctx context.Context, token string) (AuthInfo, error) {
		return AuthInfo{}, nil
	})(h.Routes()))

	req := httptest.NewRequest(http.MethodPost, "/git/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11/11111111-1111-1111-1111-111111111111.git/git-receive-pack", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGitHandlerReceivePackAuthorizedPushLogsActivityAndBroadcasts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	orgID := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	projectID := "11111111-1111-1111-1111-111111111111"
	userID := "user-123"

	repoDir := t.TempDir()
	bareRepo := filepath.Join(repoDir, "repo.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bareRepo).Run())

	workDir := filepath.Join(repoDir, "work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	runGit(t, workDir, "init")
	runGit(t, workDir, "checkout", "-b", "main")
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "README.md"), []byte("hello"), 0o644))
	runGit(t, workDir, "add", "README.md")
	runGit(t, workDir, "commit", "-m", "initial")

	activityStore := &fakeActivityStore{}
	hub := ws.NewHub()
	go hub.Run()

	client := ws.NewClient(hub, nil)
	client.SetOrgID(orgID)
	hub.Register(client)
	t.Cleanup(func() { hub.Unregister(client) })
	time.Sleep(25 * time.Millisecond)

	h := &Handler{
		RepoResolver: func(ctx context.Context, org, project string) (string, error) {
			return bareRepo, nil
		},
		ActivityStore: activityStore,
		Hub:           hub,
	}

	router := chi.NewRouter()
	router.Mount("/git", AuthMiddleware(func(ctx context.Context, token string) (AuthInfo, error) {
		if token != "test-token" {
			return AuthInfo{}, errors.New("invalid token")
		}
		return AuthInfo{
			OrgID:  orgID,
			UserID: userID,
			Permissions: map[string]ProjectPermission{
				projectID: PermissionWrite,
			},
		}, nil
	})(h.Routes()))

	server := httptest.NewServer(router)
	defer server.Close()

	remoteURL := fmt.Sprintf("%s/git/%s/%s.git", server.URL, orgID, projectID)
	authURL := strings.Replace(remoteURL, "http://", "http://user:test-token@", 1)
	runGit(t, workDir, "remote", "add", "origin", authURL)
	runGit(t, workDir, "push", "origin", "main")

	calls := activityStore.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, orgID, calls[0].workspaceID)
	require.Equal(t, "git.push", calls[0].input.Action)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(calls[0].input.Metadata, &metadata))
	require.Equal(t, projectID, metadata["project_id"])
	require.Equal(t, userID, metadata["user_id"])
	require.Equal(t, "main", metadata["branch"])
	require.Equal(t, "initial", metadata["commit_message"])

	select {
	case payload := <-client.Send:
		var event gitPushEvent
		require.NoError(t, json.Unmarshal(payload, &event))
		require.Equal(t, ws.MessageGitPush, event.Type)
		require.Equal(t, orgID, event.OrgID)
		require.Equal(t, projectID, event.ProjectID)
		require.Equal(t, userID, event.UserID)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected websocket push event")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", strings.Join(args, " "), string(output))
}
