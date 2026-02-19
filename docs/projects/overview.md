# Projects: Overview

> Summary: Project model, issue model, Git/repo integration, and how project work flows through Otter Camp.
> Last updated: 2026-02-19
> Audience: Agents doing project-level execution or platform changes.

## Project Core

Projects are first-class containers for:
- Issues and issue workflows
- Repo mapping and commit/activity streams
- Project chat and content workflows
- Optional GitHub sync metadata

Primary code:
- `internal/api/projects.go`
- `internal/store/project_store.go`
- `internal/api/issues.go`
- `internal/store/project_issue_store.go`

## Project Work Objects

Main operational primitive is now project issues (`project_issues`), with:
- Work status (`queued`, `in_progress`, `blocked`, `review`, `done`, `cancelled`)
- Approval state (`draft` -> `ready_for_review` -> ... -> `approved`)
- Priority (`P0`..`P3`)
- Owner agent + due/next-step tracking

## Git and Sync

Project repos can be:
- Local-only (fully local operation)
- Synced with GitHub (manual/poll/webhook paths)

Primary modules:
- `internal/api/github_integration.go`
- `internal/githubsync/*`
- `internal/gitserver/*`

## Related Docs

- `docs/projects/how-issues-work.md`
- `docs/projects/issue-flow.md`
- `docs/projects/local-vs-bridge.md`
- `docs/projects/hosted-wildcard-routing.md`

## Change Log

- 2026-02-19: Bug fix in project detail CI stability/docs guard compliance for Spec 515 follow-up; no behavior change.
- 2026-02-16: Added hosted wildcard routing runbook link for DNS/TLS and validation workflow (Spec 311).
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
