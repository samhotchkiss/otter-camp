# Issue #124 — Local Setup Should Bootstrap Postgres Role/DB

> STATUS: READY

## Problem

Fresh local setup can fail during migrations with:

`pq: role "otter" does not exist`

Current setup flow supports either Docker Compose Postgres or a local `psql` installation. In the local-`psql` path, the script does not ensure the role/database from `DATABASE_URL` actually exist before running migrations.

## Goal

`make setup` should successfully bootstrap a fresh local environment when `DATABASE_URL` points at a local PostgreSQL instance, including first-run cases where the role and database are missing.

## Scope

In scope:
- Parse `DATABASE_URL` role/database fields needed for bootstrap.
- Add guarded role/database ensure step for local-`psql` path before migrations.
- Add setup-script regression tests for the role/database bootstrap behavior.

Out of scope:
- Docker Compose startup behavior changes.
- Broader migration/tooling redesign.

## Acceptance Criteria

- [ ] Setup local-`psql` path attempts to create missing role/database for `DATABASE_URL` before migrations.
- [ ] Existing role/database path remains idempotent (no failure if already present).
- [ ] Script tests cover both creation-needed and already-present cases.
- [ ] `scripts/setup_test.sh` passes after implementation.

## Implementation Plan

1. Create micro-issue #728: setup local Postgres role/database bootstrap with explicit red/green test commands.
2. Add failing tests in `scripts/setup_test.sh` for local-`psql` bootstrap behavior.
3. Implement minimal setup bootstrap helpers in `scripts/setup.sh` to satisfy tests.
4. Re-run targeted and broader setup-script tests, then close the micro-issue.

## Execution Log

- [2026-02-10 22:06 MST] Issue #715 | Commit n/a | mapped | Created spec #124 in 01-ready from open bug "Error creating pq role" to restore actionable queue | Tests: n/a
- [2026-02-10 22:07 MST] Issue #728 | Commit n/a | created | Opened micro-issue with required problem/scope/checklist/explicit tests prior to implementation | Tests: n/a
- [2026-02-10 22:07 MST] Issue #728 | Commit n/a | moved | Moved spec #124 from 01-ready to 02-in-progress for active implementation | Tests: n/a
- [2026-02-10 22:07 MST] Issue #728 | Commit n/a | in-progress | Created branch codex/spec-124-local-setup-role-bootstrap from origin/main (branch isolation for single spec) | Tests: n/a
- [2026-02-10 22:10 MST] Issue #728 | Commit n/a | validated-failing | Added local-psql bootstrap regression tests and confirmed expected red state before helper implementation (missing ensure_local_postgres_role_and_database) | Tests: bash scripts/setup_test.sh
- [2026-02-10 22:14 MST] Issue #728 | Commit 9cd28ed | pushed | Added local DATABASE_URL role/database bootstrap helper in setup flow before migrations with idempotent SQL and updated setup test coverage | Tests: bash scripts/setup_test.sh
- [2026-02-10 22:15 MST] Issue #728 | Commit 9cd28ed | closed | Closed micro-issue with commit and test evidence after branch push | Tests: n/a
- [2026-02-10 22:15 MST] Issue #715 | Commit 9cd28ed | closed | Closed source bug after confirming setup bootstrap fix and test pass | Tests: bash scripts/setup_test.sh
- [2026-02-10 22:16 MST] Issue #729 | Commit 9cd28ed | PR opened | Opened PR #729 for reviewer visibility on branch codex/spec-124-local-setup-role-bootstrap | Tests: n/a
- [2026-02-10 22:16 MST] Issue #728 | Commit 9cd28ed | moved | Moved spec #124 from 02-in-progress to 03-needs-review after implementation complete | Tests: n/a
