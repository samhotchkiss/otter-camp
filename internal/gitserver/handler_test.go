package gitserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestGitHandlerUnauthorized(t *testing.T) {
	h := &Handler{
		RepoResolver: func(ctx context.Context, orgID, projectID string) (string, error) {
			return "/tmp/nowhere", nil
		},
	}

	router := chi.NewRouter()
	router.Mount("/git", AuthMiddleware(func(ctx context.Context, token string) (string, string, error) {
		return "", "", nil
	})(h.Routes()))

	req := httptest.NewRequest(http.MethodGet, "/git/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11/11111111-1111-1111-1111-111111111111.git/info/refs?service=git-upload-pack", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGitHandlerInfoRefsAuthorized(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	repoPath := filepath.Join(repoDir, "repo.git")
	require.NoError(t, exec.Command("git", "init", "--bare", repoPath).Run())

	h := &Handler{
		RepoResolver: func(ctx context.Context, orgID, projectID string) (string, error) {
			return repoPath, nil
		},
	}

	router := chi.NewRouter()
	router.Mount("/git", AuthMiddleware(func(ctx context.Context, token string) (string, string, error) {
		return "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "user-1", nil
	})(h.Routes()))

	req := httptest.NewRequest(http.MethodGet, "/git/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11/11111111-1111-1111-1111-111111111111.git/info/refs?service=git-upload-pack", nil)
	req.Header.Set("Authorization", "Bearer oc_sess_test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Body.String())
}

func TestGitHandlerRejectsBadUUID(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	repoPath := filepath.Join(repoDir, "repo.git")
	require.NoError(t, exec.Command("git", "init", "--bare", repoPath).Run())

	h := &Handler{
		RepoResolver: func(ctx context.Context, orgID, projectID string) (string, error) {
			return repoPath, nil
		},
	}

	router := chi.NewRouter()
	router.Mount("/git", AuthMiddleware(func(ctx context.Context, token string) (string, string, error) {
		return "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "user-1", nil
	})(h.Routes()))

	req := httptest.NewRequest(http.MethodGet, "/git/not-a-uuid/11111111-1111-1111-1111-111111111111.git/info/refs?service=git-upload-pack", nil)
	req.Header.Set("Authorization", "Bearer oc_sess_test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	_ = os.RemoveAll(repoDir)
}
