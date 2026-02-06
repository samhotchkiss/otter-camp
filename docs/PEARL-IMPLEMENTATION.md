# Pearl Implementation Guide

This document captures the implemented Pearl GitHub workflow in Otter Camp, including API surface, end-to-end behavior, and operator runbooks.

## End-to-End Flow (Implemented)

1. GitHub push webhook arrives at `POST /api/github/webhook`.
2. Otter Camp validates signature and enqueues a webhook job and (for push events) a repo-sync job.
3. Push commits are ingested into `project_commits`, then surfaced by commit APIs.
4. Poll fallback (`githubsync.RepoDriftPoller`) detects branch drift and enqueues repo-sync jobs when webhook delivery is missed.
5. Manual issue import (`POST /api/projects/{id}/issues/import`) queues import jobs; imported issues are upserted into project issues + GitHub links.
6. GitHub issue/PR webhooks upsert the same linked issue records to stay consistent with imported data.
7. Human-triggered publish (`POST /api/projects/{id}/publish`) pushes local commits and resolves linked GitHub issues with comment + close operations.

## Pearl API Surface

### Sync + Ingestion
- `POST /api/github/webhook`
- `POST /api/projects/{id}/repo/sync`
- `POST /api/projects/{id}/issues/import`
- `GET /api/projects/{id}/issues/status`

### Commit Visibility
- `GET /api/projects/{id}/commits`
- `GET /api/projects/{id}/commits/{sha}`
- `GET /api/projects/{id}/commits/{sha}/diff`

### Publish + Resolution
- `POST /api/projects/{id}/publish`
- `POST /api/projects/{id}/repo/conflicts/resolve`
- `GET /api/projects/{id}/repo/branches`

## Runbook: Manual Re-Sync

Use this when commits/issues look stale or webhook delivery is delayed.

1. Inspect queue health: `GET /api/github/sync/health`.
2. Trigger manual repo sync: `POST /api/projects/{id}/repo/sync`.
3. Trigger manual issue import: `POST /api/projects/{id}/issues/import`.
4. Confirm latest state:
- `GET /api/projects/{id}/commits`
- `GET /api/projects/{id}/issues/status`
5. If dead letters exist, replay failed jobs: `POST /api/github/sync/dead-letters/{id}/replay`.

## Runbook: Conflict Resolution

Use this when repository conflict state is `needs_decision`.

1. Inspect branch/conflict details: `GET /api/projects/{id}/repo/branches`.
2. Resolve:
- Keep GitHub state: `POST /api/projects/{id}/repo/conflicts/resolve` with `{"action":"keep_github"}`
- Keep Otter Camp state: `POST /api/projects/{id}/repo/conflicts/resolve` with `{"action":"keep_ottercamp"}`
3. Re-run sync: `POST /api/projects/{id}/repo/sync`.
4. Publish after conflict is cleared: `POST /api/projects/{id}/publish`.

## Current E2E Coverage

- `internal/api/pearl_e2e_test.go`
  - webhook push -> commit API visibility
  - webhook repo-sync enqueue + poll fallback enqueue
  - manual issue import path + webhook upsert consistency
- `internal/api/github_integration_test.go`
  - publish and linked issue closure/idempotency/failure recovery
  - webhook dedupe and commit/issue ingestion
- `internal/api/project_issue_sync_test.go`
  - manual import enqueue/status/safe errors
- `internal/githubsync/repo_drift_poller_test.go`
  - poll fallback drift detection and enqueue semantics
