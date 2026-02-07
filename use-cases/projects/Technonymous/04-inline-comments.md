# Technonymous — Inline Comments

## Objective
Allow reviewers to insert inline feedback directly in Markdown text without manual curly‑brace editing.

## UX
- Click anywhere in the document to add a comment.
- UI inserts a comment marker in the text.
- Comments render as inline bubbles in the rendered view.

## Storage Format (MVP)
- Use **CriticMarkup** (open, non‑proprietary) for inline comments:

```
{>> comment <<}
```

- CriticMarkup is human‑readable and designed for parsing/rendering.
- Delimiter format must be **round‑trippable** between UI and raw Markdown.

## Rendering Rules
- Render view hides raw markers and shows comments inline.
- Source view shows exact stored markup.

## Acceptance Criteria
- Click‑to‑comment inserts a marker at cursor.
- Comments survive edits + commits.
- Render view displays comments cleanly.
