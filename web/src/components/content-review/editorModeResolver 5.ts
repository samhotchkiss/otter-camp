export type EditorMode = "markdown" | "text" | "code" | "image";

export type EditorCapabilities = {
  editable: boolean;
  supportsDiff: boolean;
  supportsInlineComments: boolean;
  supportsMarkdownView: boolean;
  supportsSyntaxHighlight: boolean;
  supportsImagePreview: boolean;
};

export type EditorResolution = {
  editorMode: EditorMode;
  capabilities: EditorCapabilities;
  extension: string;
};

export type EditorComponentKey = "markdown_review" | "plain_text" | "code_editor" | "image_preview";

const MARKDOWN_EXTENSIONS = new Set([".md", ".markdown"]);
const TEXT_EXTENSIONS = new Set([".txt"]);
const CODE_EXTENSIONS = new Set([
  ".go",
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".py",
  ".json",
  ".yaml",
  ".yml",
  ".sh",
  ".sql",
  ".rb",
  ".rs",
  ".java",
  ".c",
  ".h",
  ".cpp",
]);
const IMAGE_EXTENSIONS = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"]);

const TEXT_FALLBACK: EditorResolution = {
  editorMode: "text",
  capabilities: {
    editable: true,
    supportsDiff: true,
    supportsInlineComments: false,
    supportsMarkdownView: false,
    supportsSyntaxHighlight: false,
    supportsImagePreview: false,
  },
  extension: "",
};

function extensionForPath(path: string): string {
  const normalized = path.trim().toLowerCase();
  const lastDot = normalized.lastIndexOf(".");
  const lastSlash = normalized.lastIndexOf("/");
  if (lastDot === -1 || lastDot < lastSlash) {
    return "";
  }
  return normalized.slice(lastDot);
}

export function resolveEditorForPath(path: string): EditorResolution {
  const extension = extensionForPath(path);
  if (extension === "") {
    return TEXT_FALLBACK;
  }

  if (MARKDOWN_EXTENSIONS.has(extension)) {
    return {
      editorMode: "markdown",
      capabilities: {
        editable: true,
        supportsDiff: true,
        supportsInlineComments: true,
        supportsMarkdownView: true,
        supportsSyntaxHighlight: false,
        supportsImagePreview: false,
      },
      extension,
    };
  }

  if (TEXT_EXTENSIONS.has(extension)) {
    return {
      editorMode: "text",
      capabilities: {
        editable: true,
        supportsDiff: true,
        supportsInlineComments: false,
        supportsMarkdownView: false,
        supportsSyntaxHighlight: false,
        supportsImagePreview: false,
      },
      extension,
    };
  }

  if (CODE_EXTENSIONS.has(extension)) {
    return {
      editorMode: "code",
      capabilities: {
        editable: true,
        supportsDiff: true,
        supportsInlineComments: false,
        supportsMarkdownView: false,
        supportsSyntaxHighlight: true,
        supportsImagePreview: false,
      },
      extension,
    };
  }

  if (IMAGE_EXTENSIONS.has(extension)) {
    return {
      editorMode: "image",
      capabilities: {
        editable: false,
        supportsDiff: false,
        supportsInlineComments: false,
        supportsMarkdownView: false,
        supportsSyntaxHighlight: false,
        supportsImagePreview: true,
      },
      extension,
    };
  }

  return {
    ...TEXT_FALLBACK,
    extension,
  };
}

export function resolveEditorComponent(path: string): EditorComponentKey {
  const resolution = resolveEditorForPath(path);
  switch (resolution.editorMode) {
    case "markdown":
      return "markdown_review";
    case "text":
      return "plain_text";
    case "code":
      return "code_editor";
    case "image":
      return "image_preview";
    default:
      return "plain_text";
  }
}
