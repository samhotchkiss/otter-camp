package api

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateContentWritePathAcceptsAllowedRoots(t *testing.T) {
	valid := []string{
		"/notes/idea.md",
		"notes/scratch.txt",
		"/assets/cover.png",
		"/posts/2026-02-06-launch-plan.md",
	}

	for _, input := range valid {
		normalized, err := validateContentWritePath(input)
		require.NoError(t, err, input)
		require.True(t, normalized[0] == '/')
	}
}

func TestValidateContentWritePathRejectsTraversalAndOffPolicyPaths(t *testing.T) {
	invalid := []string{
		"",
		"/",
		"../notes/escape.md",
		"/notes/../../etc/passwd",
		"/tmp/off-policy.md",
		"/README.md",
		"/posts/not-a-date.md",
		"/posts/2026-99-99-bad-date.md",
		"/posts/2026-02-06-BadSlug.md",
	}

	for _, input := range invalid {
		_, err := validateContentWritePath(input)
		require.Error(t, err, input)
	}
}

func TestValidatePostPathConventionRequiresDatePrefixAndSlug(t *testing.T) {
	require.NoError(t, validatePostPathConvention("/posts/2026-02-06-good-title.md"))
	require.Error(t, validatePostPathConvention("/posts/2026-02-06.md"))
	require.Error(t, validatePostPathConvention("/posts/title-only.md"))
	require.Error(t, validatePostPathConvention("/posts/2026-02-06-Bad-Title.md"))
}

func TestResolveProjectContentWritePathUsesProjectScopedRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	normalized, absolute, err := resolveProjectContentWritePath("project-123", "/notes/research.md")
	require.NoError(t, err)
	require.Equal(t, "/notes/research.md", normalized)
	require.Equal(t, filepath.Join(root, "project-123", "notes", "research.md"), absolute)
}

func TestBootstrapProjectContentLayoutCreatesExpectedDirectories(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	result, err := bootstrapProjectContentLayout("project-boot")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"/notes", "/posts", "/assets"}, result.Created)

	for _, dir := range []string{"notes", "posts", "assets"} {
		require.DirExists(t, filepath.Join(root, "project-boot", dir))
	}
}
