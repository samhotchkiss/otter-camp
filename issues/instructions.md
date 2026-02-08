# OtterCamp Issue Execution Instructions

This file is the working contract for any agent executing issue specs in `/Users/sam/Documents/Dev/otter-camp/issues`.

## What OtterCamp Is and How It Works

OtterCamp is a work management system for human + AI teams. It combines issue tracking, project coordination, chat/session routing, and Git-backed execution so work is visible and auditable end to end.

How work flows through the system:

- Specs define intended behavior and outcomes.
- GitHub issues break specs into small, testable implementation units.
- Agents execute those units with TDD and frequent commits.
- Pushes trigger deployment and surface activity to reviewers.
- Logs and notes provide operational context, decisions, and follow-ups.

## 1. Goal and Standard of Work

- Deliver production-ready changes with test-first development.
- Break large specs into many small, independently shippable GitHub issues.
- Work issues in order, one at a time, with clear traceability from spec -> issue -> commit -> deploy.
- Keep reviewer context complete via `progress-log.md`, `notes.md`, and the execution log section in this file.

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
2. Create a small-issue implementation plan (phases only if required by dependencies).
3. Create GitHub issues for each small work unit in build order.
4. Include explicit tests in every GitHub issue.
5. Implement one GitHub issue at a time using TDD.
6. Commit small, descriptive commits tied to the issue.
7. Push as you go (main auto-deploys).
8. Update logs after every meaningful step.
9. Move spec file to the next folder state.
10. Continue until all requested specs are complete.

Do not batch multiple unrelated issue implementations into one commit.

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

## 6. Commit and Push Rules

- Make small commits with descriptive messages.
- Reference issue numbers in commit bodies (`Refs: #<number>` or `Closes: #<number>`).
- Push after each meaningful, passing unit of work.
- Do not wait to push until the full spec is complete.

Recommended commit format:

```
type(scope): concise summary

What changed and why.
Key implementation details.
Tests run and outcomes.

Refs: #123
```

## 7. Logging and Reviewer Visibility

You must maintain three logs:

- `~/Documents/Dev/otter-camp/issues/progress-log.md`
  - Append timestamped updates as work progresses.
  - Include what is done and what remains.
- `~/Documents/Dev/otter-camp/issues/notes.md`
  - Capture blockers, required human follow-up, spec concerns, and recommended changes.
  - If you make a spec interpretation change, document it here.
- Execution Log section at the bottom of this file
  - Append-only operational trace with issue numbers and commit hashes.

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

- Main branch auto-deploys.
- Standard flow is commit -> push -> verify production behavior/logs.

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
- Logs are up to date.
- Any required operator actions are documented in `notes.md`.
- Spec file moved to `05-completed`.

---

## Execution Log (Append-Only)

Template for every entry:

```
### YYYY-MM-DD HH:MM TZ
- Scope:
- GitHub issues:
- Commits:
- Tests run:
- Remaining work:
- Notes:
```

### 2026-02-08 07:51 MST
- Scope: Created instructions contract for issue execution workflow.
- GitHub issues: N/A
- Commits: (pending)
- Tests run: N/A (documentation change only)
- Remaining work: Continue with next requested issue-spec execution tasks.
- Notes: Added mandatory append-only trace format with issue numbers and commit hashes for reviewer auditing.

### 2026-02-08 07:58 MST
- Scope: Added a concise OtterCamp overview section explaining purpose and system workflow.
- GitHub issues: N/A
- Commits: (pending)
- Tests run: N/A (documentation change only)
- Remaining work: None for this request.
- Notes: Kept the overview short and placed it near the top for fast onboarding context.

### 2026-02-08 08:02 MST
- Scope: Broke down Spec004 into small implementation tickets and moved spec to in-progress.
- GitHub issues: #277, #278, #279, #280, #281
- Commits: (pending)
- Tests run: N/A (planning + issue creation only)
- Remaining work: Implement phases in order starting with #277.
- Notes: Phase ordering enforces schema/API before UI work to keep TDD loop stable.

### 2026-02-08 08:11 MST
- Scope: Completed Spec004 backend phase for attachment_ids and project-chat attachment payloads.
- GitHub issues: #278
- Commits: (pending)
- Tests run:
  - `go test ./internal/api -run 'TestNormalizeProjectChatAttachmentIDs|TestToProjectChatPayloadDecodesAttachments' -count=1` (pass)
  - `go test ./internal/api -run TestProjectChatHandlerCreateWithAttachmentOnlyBody -v -count=1` (skipped: OTTER_TEST_DATABASE_URL)
  - `go test ./internal/api -count=1` (pass)
  - `go test ./internal/store -run TestSchemaProjectChatAttachmentColumnsAndForeignKey -v -count=1` (skipped: OTTER_TEST_DATABASE_URL)
  - `go test ./internal/store -count=1` (pass)
- Remaining work: Implement UI upload queue + attachment rendering phases (#279, #280, #281).
- Notes: Runtime lacks docker/local postgres, so DB integration tests are skipped in this environment.

### 2026-02-08 08:18 MST
- Scope: Completed Spec004 composer upload queue UX phase.
- GitHub issues: #279
- Commits: (pending)
- Tests run:
  - `cd web && npm test -- src/components/chat/GlobalChatSurface.test.tsx --run` (pass)
  - `cd web && npm run build:typecheck` (pass)
- Remaining work: #280 attachment rendering and #281 upload route hardening.
- Notes: For issue chat, attachments are currently appended as markdown links since issue comment API does not yet accept attachment_ids.
