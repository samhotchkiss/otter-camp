# OtterCamp Issue Execution Instructions

This file is the working contract for any agent executing issue specs in `/Users/sam/Documents/Dev/otter-camp/issues`.

## Start-of-Run Preflight (Do This First)

Before coding anything, run this checklist in order:

1. Check in-progress work:
   - Inspect `issues/02-in-progress/`.
   - If any spec exists there, continue that spec first.
   - Do not start a new ready spec until `02-in-progress` is cleared.
2. Check why prior execution stopped:
   - Read the last entries in `issues/progress-log.md`.
   - Read `issues/notes.md` for blockers/follow-ups.
   - Identify whether prior stop was:
     - test failure
     - merge/conflict
     - runtime/env issue
     - unclear product decision
3. Reconcile local state vs GitHub state:
   - Verify open/closed GitHub issues match local spec phase progress.
   - If mismatched, update local logs/state first, then continue.
4. Check repo safety:
   - Run `git status --short`.
   - Do not revert unrelated changes.
   - If working tree is dirty, determine whether changes belong to the active spec branch. If not, stop and reconcile before continuing.
5. Check branch isolation:
   - Implementation must never happen on `main`.
   - If current branch is `main`, create/switch to a spec branch before coding.
   - Branch naming: `codex/spec-<spec-number>-<short-slug>` (example: `codex/spec-007-cli-e2e-bugs`).
   - Keep exactly one active spec per branch.
6. Select next task:
   - Priority order is `02-in-progress` first, then `01-ready` in numeric order.
   - In `01-ready`, if a spec has a top-level `## Reviewer Required Changes` block, prioritize that spec before net-new ready specs.

## What OtterCamp Is and How It Works

OtterCamp is a work management system for human + AI teams. It combines issue tracking, project coordination, chat/session routing, and Git-backed execution so work is visible and auditable end to end.

How work flows through the system:

- Specs define intended behavior and outcomes.
- GitHub issues break specs into small, testable implementation units.
- Agents execute those units with TDD and frequent commits.
- Pushes trigger deployment and surface activity to reviewers.

## 1. Goal and Standard of Work

- Deliver production-ready changes with test-first development.
- Break large specs into many small, independently shippable GitHub issues.
- Work issues in order, one at a time, with clear traceability from spec -> issue -> commit -> deploy.

## Local Issues Directory Policy

- `issues/` is a local working directory and is gitignored.
- Keep spec files and notes current locally while working.
- Never stage or commit files under `issues/`.
- Commits should include only product code/tests/docs outside `issues/`.

## Branch Isolation Policy (Required)

- All implementation work must happen on feature branches, never directly on `main`.
- One spec per branch. Do not mix commits from multiple specs on the same branch.
- If a spec is moved to `02-in-progress`, create or reuse its dedicated branch immediately.
- Push that branch as you go.
- Reviewer owns merging approved branches into `main`.
- If a spec in review is blocked by unrelated branch changes, move the blocking spec back to `01-ready`, isolate fixes on the proper branch, then re-queue review.

## Priority and Source of Truth

- Primary queue source is local spec folders in `issues/`.
- Execution order:
  1. Finish any spec in `02-in-progress` first.
  2. Then process `01-ready` in numeric order.
  3. Ignore `00-not-ready` unless explicitly requested.
- GitHub issues are implementation units generated from specs; they do not replace local spec state.
- If local folder state and GitHub status differ, reconcile local folder state first, then continue execution.

## Reviewer Required Changes Handling

- If a spec in `01-ready` includes a top-level `## Reviewer Required Changes` block:
  1. Treat those items as mandatory acceptance criteria.
  2. Re-open/split follow-up GitHub issues as needed (small, test-defined units).
  3. Implement fixes with TDD and close all required items in the block.
  4. Remove or mark resolved checklist items in that block.
  5. Move the spec to `03-needs-review` for re-review (do not move directly to `05-completed`).
  6. After all required items are resolved, remove the `## Reviewer Required Changes` block from the top of the spec and preserve a summary in `## Execution Log`.

## Autonomous Execution Defaults

- Assume the operator may be offline.
- Do not pause for routine decisions; choose the most reasonable path and continue.
- If truly blocked, note required follow-up in `issues/notes.md`, then move to the next actionable issue/spec.
- Keep shipping: small commits, push immediately, continue to next sub-issue.

## 2. Issue Folder Workflow (Local Specs)

Issue specs are organized by state:

- `00-not-ready`: draft ideas, missing details, not ready to implement
- `01-ready`: ready for planning and issue breakdown
- `02-in-progress`: currently being implemented
- `03-needs-review`: implementation complete, awaiting validation/review
- `04-in-review`: actively under review
- `05-completed`: fully merged and done

Use these folders as a state machine. Move spec files forward as work progresses.

## 3. Required Execution Process (Always)

1. Read the next spec in `01-ready` in numeric order.
2. Create a full implementation plan for the entire spec (all phases/work units in build order).
3. Create GitHub issues for all planned phases/work units before any coding starts.
4. Include explicit tests in every GitHub issue.
5. Verify the full planned issue set exists (and is logged) before writing code.
6. Implement one GitHub issue at a time using TDD.
7. Commit small, descriptive commits tied to the issue (code/tests/docs outside `issues/` only).
8. Push as you go to the active spec branch.
9. Open/update the PR for reviewer visibility.
10. Move spec file to the next folder state.
11. Continue until all requested specs are complete.

Do not batch multiple unrelated issue implementations into one commit.
Do not batch multiple unrelated workstreams into one GitHub issue.

Execution loop for each spec:

1. Move spec to `02-in-progress`.
2. Create the full micro-issue set for all phases (each with explicit tests + acceptance criteria) before first code change.
3. On resume/restart, verify all planned phase issues still exist; if any are missing, create/backfill them before coding.
4. Implement one GitHub issue at a time with TDD.
5. Commit and push each passing unit.
6. Close GitHub issue with commit hash + tests run.
7. When implementation is fully done, move spec to `03-needs-review` (do not skip this).
8. Move spec to `05-completed` only after external reviewer sign-off.

## 4. TDD Policy (Non-Negotiable)

For each GitHub issue:

1. Write or update tests first (unit/integration/e2e as applicable).
2. Run tests and confirm failure for the expected reason.
3. Implement the smallest code change to pass.
4. Re-run targeted tests.
5. Run broader regression tests before commit.

Minimum test checklist per issue:

- New behavior tests
- Regression test for the bug/risk being fixed
- Edge-case test(s) for null/empty/error states where relevant

## 5. GitHub Issue Requirements

Each issue must include:

- Problem statement
- Scope (in/out)
- Implementation notes
- Ordered checklist
- Test plan (explicit command-level tests)
- Acceptance criteria
- Dependencies (if any)

Prefer many small issues over a few large ones. If two tasks can ship independently, they should be separate issues.

Micro-issue sizing rules (required):
- One issue should target one shippable behavior/fix slice.
- If an issue needs multiple independent acceptance criteria, split it.
- If an issue spans multiple subsystems (API + bridge + UI), split by subsystem unless strictly required for a single behavior.
- Prefer 5-10 small issues over 1-2 large issues for the same spec phase.
- Before coding, split any oversized phase issue into smaller linked issues and work those in order.

## 6. Commit and Push Rules

- Make small commits with descriptive messages.
- Reference issue numbers in commit bodies (`Refs: #<number>` or `Closes: #<number>`).
- Push after each meaningful, passing unit of work to the current spec branch.
- Do not wait to push until the full spec is complete.
- Do not merge to `main` during implementation; reviewer merges after approval.
- Never include `issues/` files in commits.

Recommended commit format:

```
type(scope): concise summary

What changed and why.
Key implementation details.
Tests run and outcomes.

Refs: #123
```

## 7. Optional Local Notes

- `issues/notes.md` is optional local context for blockers and follow-ups.
- Use it when needed for handoff clarity, but there is no required per-step update cadence.

## 7b. Per-Spec Execution Log (Required)

- Every active spec file must include a `## Execution Log` section at the end of the file.
- Log entries are append-only and newest goes at the bottom.
- Add one log entry for each meaningful step:
  - GitHub issue created/closed
  - Commit created/pushed
  - Spec folder-state transition
  - Blocker encountered/resolved
- Do not overwrite prior entries. Never remove history.

Required log line format:

```markdown
- [YYYY-MM-DD HH:MM TZ] Issue #<n> | Commit <sha> | <status/action> | <one-line summary> | Tests: <commands or n/a>
```

Example:

```markdown
- [2026-02-08 10:47 MST] Issue #300 | Commit 2b9ffd8 | closed | Scoped questionnaire store queries by org_id and added cross-org tests | Tests: go test ./internal/store -run TestQuestionnaireStore(GetByID|Respond)CrossOrgDenied -count=1
```

## 8. OtterCamp Infrastructure Overview (Operator Context)

Core stack:

- API: Go service (`/cmd/server`)
- Web: React + TypeScript + Vite (`/web`)
- Database: Postgres via `DATABASE_URL`
- Optional cache/queue support: Redis via `REDIS_URL`
- Bridge: local OpenClaw bridge (`/bridge/openclaw-bridge.ts`) for agent session sync and message delivery

Key runtime and auth notes:

- Org-scoped auth is required to avoid demo data.
- Otter CLI config path: `~/Library/Application Support/otter/config.json`
- OpenClaw sync/websocket secrets must match between API and bridge runtime.
- Message delivery depends on bridge connectivity and valid scope/credentials.

Key commands:

- API dev: `go run ./cmd/server`
- Web dev: `cd web && npm run dev`
- Full dev: `make dev`
- Go tests: `go test ./...`
- Web tests: `cd web && npm test`
- Migrate up: `make migrate-up`
- Migration status: `make migrate-status`

Deployment:

- Main branch auto-deploys after reviewer merge.
- Standard flow is branch commit -> branch push -> review -> merge to main -> verify production behavior/logs.

## 9. Practical Guardrails

- Never commit secrets/tokens.
- Prefer UUID-based identifiers over display names for durable references.
- Treat websocket and bridge delivery as failure-prone paths; always include resilient UI state and retry behavior.
- Preserve existing user-visible state and data when migrating schema or chat/session behavior.

## 10. Definition of Done

A spec is done only when:

- All planned GitHub issues are completed.
- Tests for each issue are written and passing.
- Code is pushed.
- Any required operator actions are documented in `notes.md`.
- Spec file is moved to `03-needs-review` after implementation, then to `05-completed` only after review approval.

## 11. Review Gate (Required)

- No spec should move directly from `02-in-progress` to `05-completed`.
- Required path is: `02-in-progress` -> `03-needs-review` -> (`04-in-review` optional) -> `05-completed`.
- Reviewer findings must be addressed with follow-up commits before final move to `05-completed`.
