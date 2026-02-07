# Technonymous — Markdown Viewer/Editor

## Objective
Support read + edit of Markdown with a **rendered ↔ source** toggle and in‑browser commits.

## Requirements
- **Rendered view**: clean reading mode with inline comments.
- **Source view**: raw Markdown, including comment markers.
- **Edit in browser**: modify text and commit from OtterCamp.

## Commit Flow
1. Edit in browser.
2. Optional commit message (subject/body) — **not required**.
3. Commit to repo (local).
4. Notify issue owner agent; agent incorporates updates and continues.
5. Push to GitHub (optional, human‑initiated).

## Acceptance Criteria
- Toggle works without losing comment markers.
- Edits can be committed from the browser.
- Commit message is optional (auto‑generated default if omitted).
