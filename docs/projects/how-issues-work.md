# Projects: How Issues Work

> Summary: Canonical issue data model and operational semantics used by Otter Camp.
> Last updated: 2026-02-16
> Audience: Agents writing issue automation or UI/API behavior.

## Canonical Model

Issue store: `internal/store/project_issue_store.go`

Key fields:
- identity: `id`, `issue_number`, `project_id`, `org_id`
- ownership: `owner_agent_id`, participants
- state: `state`, `work_status`, `approval_state`
- planning: `priority`, `due_at`, `next_step`, `next_step_due_at`
- linkage: GitHub link, review versions, comments

## Why This Model

Design moved from simple open/closed issue state to explicit work-tracking + review state so:
- Agents can reason on machine-readable work lifecycle
- Humans can approve/reject with explicit transitions
- Dashboards can surface blocked/review/done accurately

## Core Endpoints

Representative routes (see `internal/api/router.go`):
- `POST /api/projects/{id}/issues`
- `GET /api/issues`
- `PATCH /api/issues/{id}`
- `POST /api/issues/{id}/comments`
- `POST /api/issues/{id}/approval-state`
- `POST /api/issues/{id}/approve`
- Review/history endpoints under `/api/issues/{id}/review/*`

## Change Log

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
