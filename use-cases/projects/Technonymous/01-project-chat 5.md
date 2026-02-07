# Technonymous — Project Chat

## Objective
Provide a persistent, project‑level chat for brainstorming and idea capture.

## Requirements
- **Always available** for the project (not tied to any issue).
- Supports quick idea drops from Sam and Stone.
- Messages are **searchable** and can be referenced later.

## Data Model (MVP)
- `project_id`
- `message_id`
- `author` (human or agent)
- `timestamp`
- `body`

## UI
- Simple chat panel (like issue comments, but scoped to project).
- No need for threading in MVP.

## Activity Feed
- Project chat activity does **not** clutter the global activity feed.
- Commits remain the canonical activity stream.
