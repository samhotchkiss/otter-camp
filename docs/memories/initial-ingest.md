# Memories: Initial Ingest

> Summary: How Otter Camp bootstraps memory from imported OpenClaw history and early project chat history.
> Last updated: 2026-02-16
> Audience: Agents working on migration/import correctness.

## Sources

Initial memory context comes from:
- OpenClaw import (`otter migrate from-openclaw`)
- Existing conversation/chat history already in Otter Camp

## Migration Phases Relevant to Memory

Implemented in `internal/import/migration_runner.go`:
1. Agent import
2. History backfill
3. Ellie backfill (memory extraction)
4. Project/issue discovery

## Safety Guarantee

OpenClaw is treated as read-only during migration.

Guard implementation:
- `internal/import/openclaw_source_guard.go`

Guard behavior:
- Validates read paths stay in allowed source graph
- Rejects writes to source
- Snapshot/verify source hashes around migration run

## Practical Notes

- Backfill can pause/resume.
- History-only and agents-only modes are supported.
- Import output includes counters and warnings for auditability.

## Related Docs

- `docs/projects/initial-ingest.md`
- `docs/memories/ongoing-ingest.md`

## Change Log

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
