# Issues and Old Code

> Summary: Concrete code/docs problems discovered during full repo audit, including deprecated/stale patterns, security risks, and structural cleanup debt.
> Last updated: 2026-02-16
> Audience: Agents doing cleanup, hardening, and reliability work.

## High Severity

1. Potential secret leak in repo script defaults.
- File: `scripts/generate-avatars.sh`
- Problem: script includes a `GEMINI_API_KEY` default value pattern that appears to be a live key.
- Risk: credential exposure and accidental downstream reuse.
- Action: remove embedded key, require env injection only, rotate any exposed key.

2. CORS is fully open in API router.
- File: `internal/api/router.go`
- Problem: `AllowedOrigins: []string{"*"}`.
- Risk: broad cross-origin surface; harder to enforce least privilege in hosted mode.
- Action: environment-based allowlist with strict production defaults.

## Medium Severity

1. Frontend/backend notification API mismatch.
- Files: `web/src/contexts/NotificationContext.tsx`, `internal/api/router.go`
- Problem: frontend calls `/api/notifications*`; backend exposes `/api/settings/notifications` and lacks matching CRUD endpoints.
- Impact: notification panel behavior can silently fail or be incomplete.
- Action: align contracts (implement `/api/notifications*` or switch frontend to existing endpoints).

2. Duplicate migration files present.
- Files:
  - `migrations/047_add_settings_columns.up 2.sql`
  - `migrations/047_add_settings_columns.down 2.sql`
- Problem: duplicated migration artifacts increase migration confusion/risk.
- Action: remove duplicates after validating migration history and tooling assumptions.

3. Repo contains many duplicate file/folder artifacts with suffixes (` 2`, ` 3`, etc.).
- Examples:
  - `Dockerfile 2`, `Dockerfile 3`, `Dockerfile 4`
  - `otter 2`, `otter 3`, `otter 4`, `otter 5`
  - `web/src/pages/settings 2`
  - `web/src/pages/__tests__ 2`
  - `.github/workflows 2`
- Impact: ambiguity, accidental edits/tests on wrong paths, CI/local drift.
- Action: perform controlled cleanup pass with explicit keep/delete list.

## Low Severity / Drift

1. Legacy docs naming and architecture references were stale.
- Prior docs used old names (`AI Hub`, `GitClaw`) and old assumptions.
- Action taken: replaced with this new canonical docs structure.

2. Historical prototypes/wireframes are detached from current implementation.
- Paths: `docs/prototype/*`, `docs/wireframes/*`
- Impact: useful references, but not guaranteed to match current product behavior.
- Action: keep as design artifacts; clearly mark non-canonical in follow-up.

3. Workspace trust toggle can weaken auth assumptions if enabled.
- File: `internal/middleware/workspace.go`
- Setting: `TRUST_UNVERIFIED_JWT_WORKSPACE_CLAIMS=true`
- Impact: unsafe if used without trusted upstream verification.
- Action: keep disabled by default; document as advanced/unsafe mode.

## Operational Risks to Track

- Hosted bridge freshness and reconnect behavior directly affect perceived product reliability.
- Migration/import paths are robust but still complex; keep high test coverage on source guard and resume logic.
- Memory retrieval quality relies on embedding correctness and configuration consistency.

## Change Log

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
