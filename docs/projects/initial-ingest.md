# Projects: Initial Ingest

> Summary: How project/issue/repo context enters Otter Camp from OpenClaw and GitHub.
> Last updated: 2026-02-16
> Audience: Agents building migration/import pathways.

## OpenClaw Import

Through `otter migrate from-openclaw`:
- Agent roster import
- Conversation history backfill
- Ellie backfill
- Project/issue discovery pass

Core modules:
- `internal/import/migration_runner.go`
- `internal/import/openclaw_project_discovery.go`

## GitHub Import/Sync

Project issue and repo ingest use:
- GitHub webhooks
- Poll fallback
- Manual sync endpoints

Core modules:
- `internal/api/github_integration.go`
- `internal/githubsync/repo_drift_poller.go`
- `internal/api/project_issue_sync.go`

## Guardrails

- Workspace/org scoping on all write paths
- Capability checks on high-risk GitHub actions
- Conflict resolution endpoints for divergent repo state

## Change Log

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
