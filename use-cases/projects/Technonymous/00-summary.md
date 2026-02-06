# Technonymous — Use Case Summary

**Project type:** Long‑form blog (weekly cadence)

**Core model:** OtterCamp is the authoring + review layer. Git commits are the canonical activity stream.

## Goals
- Provide a **project‑level chat** for brainstorming (not tied to issues).
- Treat each post as a **PR‑like issue** with inline feedback and approvals.
- Support **Markdown render ↔ source toggle**.
- Allow **in‑browser edits + commit** (commit message optional).
- Enforce **commit‑first workflow** (commit on each meaningful change).

## Key Decisions (from Sam)
- **Project chat exists for all projects** and is not issue‑scoped.
- **Inline feedback**: click‑to‑comment UI; backend stores comments using **CriticMarkup** `{>> comment <<}`.
- **No external publishing** in MVP (Substack etc. later).

## Files in this Use Case
- `01-project-chat.md` — project‑level chat model
- `02-repo-structure.md` — repo layout (scratch + posts)
- `03-issue-as-pr.md` — issue workflow + review
- `04-inline-comments.md` — comment model + markup encoding
- `05-markdown-viewer-editor.md` — render/source toggle + edits + commit
- `06-commit-cadence.md` — agent commit rules
