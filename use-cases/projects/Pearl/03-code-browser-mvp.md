# Pearl — Code Browser MVP (Commit‑First)

## Objective
Expose code history without emphasizing raw code. The MVP is **commit‑first**.

## UX Flow
1) **Commit list** (primary view)
   - Each row shows: author, date, short subject line.
2) **Expand row → verbose description**
   - Uses **commit message body** as the detailed description.
3) **Expand again → diff**
   - Render diff with basic syntax highlighting.
   - No full file tree UI required for MVP.

## Content Rules
- **Verbose description comes from commit body.**
- If body is empty, show a light warning and fallback: “No description provided.”

## Data Requirements
- Commit metadata (sha, author, date, subject, body).
- Diff data for each commit (generated on demand).

## API Surface (MVP)
- `GET /api/projects/:id/commits?limit=…`
- `GET /api/projects/:id/commits/:sha`
- `GET /api/projects/:id/commits/:sha/diff`

## Acceptance Criteria
- Commit list loads for Pearl.
- Expanding shows commit body (verbose description).
- Diff renders with basic syntax highlighting.

## Explicit Non‑Goals (MVP)
- Full repo browser or file tree navigation.
- Inline code review/comments.
- Rich diff UI (basic highlighting is enough).
