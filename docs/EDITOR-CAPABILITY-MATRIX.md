# Editor Capability Matrix (MVP)

This matrix is the single source of truth for file-type behavior in OtterCamp MVP.

## File Type Rules

- `.md`
  - Editor mode: Markdown review workspace
  - Features: rendered/source toggle, CriticMarkup inline comments
- `.txt`
  - Editor mode: plain text
  - Features: direct editing, no rendered markdown mode
- `.go`, `.ts`, `.js`, `.py`
  - Editor mode: code editor
  - Features: syntax highlighting, diff view
- `.png`, `.jpg`, `.gif`
  - Editor mode: image preview
  - Features: preview only in MVP
  - Inline comments: not supported in MVP (post-MVP)

## Notes

- For unsupported extensions, use safe fallback plain-text rendering.
- Any change to this matrix should update:
  - `use-cases/projects/Technonymous/07-editor-detection.md`
  - `use-cases/projects/Technonymous/09-media-and-metadata.md`
