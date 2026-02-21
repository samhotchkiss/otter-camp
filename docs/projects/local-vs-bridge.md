# Projects: Local vs Bridge Runtime

> Summary: Project behavior differences and invariants between local-only runtime and hosted+bridge runtime.
> Last updated: 2026-02-16
> Audience: Agents needing environment-aware behavior.

## Invariants (Both Modes)

- Project/task truth is in Otter Camp DB.
- Task and work-status semantics are identical.
- Memory/retrieval worker behavior is backend-driven.

## Local Mode

- API/UI usually at `http://localhost:4200`.
- Git repo and uploads are local filesystem paths.
- Bridge can still run locally to connect local OpenClaw.

## Hosted + Bridge Mode

- API hosted (`api.otter.camp` style endpoint).
- UI hosted (`{site}.otter.camp` style endpoint).
- Bridge runs near OpenClaw and streams sync/dispatch over internet.
- Connections diagnostics become critical to detect stale bridge state.

## Agent Guidance

- Never assume hosted base URL in code paths that should run local.
- Always read base URLs from config/env (`API_URL`, CLI config, `OTTERCAMP_URL`).

## Change Log

- 2026-02-21: Renamed project issue terminology to project task terminology.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
