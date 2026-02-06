package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestProjectContentMetadataIncludesEditorModeAndCapabilities(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-metadata-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Metadata")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	markdownPath := filepath.Join(root, projectID, "posts", "2026-02-06-launch-plan.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(markdownPath), 0o755))
	require.NoError(t, os.WriteFile(markdownPath, []byte("# Launch Plan\n"), 0o644))

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/content/metadata?org_id="+orgID+"&path=/posts/2026-02-06-launch-plan.md",
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp projectContentMetadataResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "/posts/2026-02-06-launch-plan.md", resp.Path)
	require.True(t, resp.Exists)
	require.Equal(t, editorModeMarkdown, resp.EditorMode)
	require.True(t, resp.Capabilities.Editable)
	require.True(t, resp.Capabilities.SupportsInlineComments)
	require.True(t, resp.Capabilities.SupportsMarkdownView)
	require.False(t, resp.Capabilities.SupportsImagePreview)
	require.Greater(t, resp.SizeBytes, int64(0))
}

func TestProjectContentMetadataValidatesPathPolicy(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-metadata-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Metadata Validate")

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/content/metadata?org_id="+orgID+"&path=../../etc/passwd",
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
