package api

import (
	"mime"
	"path/filepath"
	"strings"
)

type editorMode string

const (
	editorModeMarkdown editorMode = "markdown"
	editorModeText     editorMode = "text"
	editorModeCode     editorMode = "code"
	editorModeImage    editorMode = "image"
)

type editorCapabilities struct {
	Editable                bool `json:"editable"`
	SupportsDiff            bool `json:"supports_diff"`
	SupportsInlineComments  bool `json:"supports_inline_comments"`
	SupportsMarkdownView    bool `json:"supports_markdown_view"`
	SupportsSyntaxHighlight bool `json:"supports_syntax_highlight"`
	SupportsImagePreview    bool `json:"supports_image_preview"`
}

type editorResolution struct {
	Mode         editorMode         `json:"editor_mode"`
	Capabilities editorCapabilities `json:"capabilities"`
	MimeType     string             `json:"mime_type,omitempty"`
	Extension    string             `json:"extension"`
}

var (
	markdownExtensions = map[string]struct{}{
		".md":       {},
		".markdown": {},
	}
	textExtensions = map[string]struct{}{
		".txt": {},
	}
	codeExtensions = map[string]struct{}{
		".go": {}, ".ts": {}, ".tsx": {}, ".js": {}, ".jsx": {}, ".py": {},
		".json": {}, ".yaml": {}, ".yml": {}, ".sh": {}, ".sql": {}, ".rb": {},
		".rs": {}, ".java": {}, ".c": {}, ".h": {}, ".cpp": {},
	}
	imageExtensions = map[string]struct{}{
		".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".svg": {},
	}
)

func resolveEditorForPath(path string) editorResolution {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	if ext == "" {
		return editorResolution{
			Mode: editorModeText,
			Capabilities: editorCapabilities{
				Editable:                true,
				SupportsDiff:            true,
				SupportsInlineComments:  false,
				SupportsMarkdownView:    false,
				SupportsSyntaxHighlight: false,
				SupportsImagePreview:    false,
			},
			Extension: ext,
		}
	}

	if _, ok := markdownExtensions[ext]; ok {
		return editorResolution{
			Mode: editorModeMarkdown,
			Capabilities: editorCapabilities{
				Editable:                true,
				SupportsDiff:            true,
				SupportsInlineComments:  true,
				SupportsMarkdownView:    true,
				SupportsSyntaxHighlight: false,
				SupportsImagePreview:    false,
			},
			MimeType:  mime.TypeByExtension(ext),
			Extension: ext,
		}
	}

	if _, ok := textExtensions[ext]; ok {
		return editorResolution{
			Mode: editorModeText,
			Capabilities: editorCapabilities{
				Editable:                true,
				SupportsDiff:            true,
				SupportsInlineComments:  false,
				SupportsMarkdownView:    false,
				SupportsSyntaxHighlight: false,
				SupportsImagePreview:    false,
			},
			MimeType:  mime.TypeByExtension(ext),
			Extension: ext,
		}
	}

	if _, ok := codeExtensions[ext]; ok {
		return editorResolution{
			Mode: editorModeCode,
			Capabilities: editorCapabilities{
				Editable:                true,
				SupportsDiff:            true,
				SupportsInlineComments:  false,
				SupportsMarkdownView:    false,
				SupportsSyntaxHighlight: true,
				SupportsImagePreview:    false,
			},
			MimeType:  mime.TypeByExtension(ext),
			Extension: ext,
		}
	}

	if _, ok := imageExtensions[ext]; ok {
		return editorResolution{
			Mode: editorModeImage,
			Capabilities: editorCapabilities{
				Editable:                false,
				SupportsDiff:            false,
				SupportsInlineComments:  false,
				SupportsMarkdownView:    false,
				SupportsSyntaxHighlight: false,
				SupportsImagePreview:    true,
			},
			MimeType:  mime.TypeByExtension(ext),
			Extension: ext,
		}
	}

	// Safe fallback for unknown file types.
	return editorResolution{
		Mode: editorModeText,
		Capabilities: editorCapabilities{
			Editable:                true,
			SupportsDiff:            true,
			SupportsInlineComments:  false,
			SupportsMarkdownView:    false,
			SupportsSyntaxHighlight: false,
			SupportsImagePreview:    false,
		},
		MimeType:  mime.TypeByExtension(ext),
		Extension: ext,
	}
}
