package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveEditorForPathMapsKnownExtensions(t *testing.T) {
	cases := []struct {
		path       string
		mode       editorMode
		inline     bool
		syntax     bool
		image      bool
		markdownUI bool
	}{
		{path: "/posts/2026-02-06-launch.md", mode: editorModeMarkdown, inline: true, markdownUI: true},
		{path: "/notes/quick.txt", mode: editorModeText},
		{path: "/notes/main.go", mode: editorModeCode, syntax: true},
		{path: "/assets/mockup.png", mode: editorModeImage, image: true},
	}

	for _, tc := range cases {
		resolution := resolveEditorForPath(tc.path)
		require.Equal(t, tc.mode, resolution.Mode, tc.path)
		require.Equal(t, tc.inline, resolution.Capabilities.SupportsInlineComments, tc.path)
		require.Equal(t, tc.syntax, resolution.Capabilities.SupportsSyntaxHighlight, tc.path)
		require.Equal(t, tc.image, resolution.Capabilities.SupportsImagePreview, tc.path)
		require.Equal(t, tc.markdownUI, resolution.Capabilities.SupportsMarkdownView, tc.path)
	}
}

func TestResolveEditorForPathUnknownExtensionFallsBackToSafeTextMode(t *testing.T) {
	resolution := resolveEditorForPath("/notes/spec.custom")
	require.Equal(t, editorModeText, resolution.Mode)
	require.True(t, resolution.Capabilities.Editable)
	require.False(t, resolution.Capabilities.SupportsInlineComments)
	require.False(t, resolution.Capabilities.SupportsSyntaxHighlight)
	require.False(t, resolution.Capabilities.SupportsImagePreview)
}
