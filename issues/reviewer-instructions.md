# Reviewer Instructions (Claude)

Queue root: `issues/`

Lane flow:
1. Pull next file from `03-needs-review`
2. Move to `04-in-review` before reviewing
3. Review implementation against the task spec and required tests
4. If accepted: ensure task branch is merged into `v2`, then move to `05-completed`
5. If changes required: add a top-level `## Reviewer Required Changes` block to the task file, move back to `01-ready`, and append concrete feedback to `issues/notes.md`

Review standards:
- Verify acceptance criteria from the task file are actually met.
- Verify required test layers for that task were run and results are plausible.
- Verify the implementer reported scoped-vs-baseline test classification:
  - `task_scope` failures block completion.
  - `baseline_unrelated` failures are only acceptable when clearly pre-existing and outside touched scope.
- Verify the implementation includes matching test updates (new/changed `*_test.go`, integration/e2e additions, or explicit task-justified rationale when no test file change is expected).
- Prioritize bugs, regressions, missing tests, and spec non-compliance.
- Keep feedback concrete and actionable (file paths, failing behavior, missing assertions).
- Confirm the PR target is `v2`. Do not close review as completed until merge to `v2` is done.
- Dependency gate: do not move a task to `05-completed` if any task listed in its `Depends on` header is not already in `05-completed`.
- Use non-interactive GitHub CLI checks/actions (for example `gh pr view`, `gh pr list`, `gh pr checks`, `gh pr merge`).
- Do not run `gh pr create` or any interactive auth/login command from reviewer flow.
- Never inline multi-line markdown payloads in quoted shell one-liners.
- For notes append operations, use a single-quoted heredoc delimiter: `cat <<'EOF' >> issues/notes.md`.
- If accepted and dependency gate passes, attempt to merge the PR to `v2` from reviewer flow.
- Only move to `05-completed` after merge is confirmed.
- If PR/merge state cannot be verified non-interactively, or merge fails/is blocked (conflicts, permissions, required checks), append blocker details to `issues/notes.md` and move the task back to `01-ready`.
- If findings exist, required changes must be written directly into the task file in a top-level `## Reviewer Required Changes` block.
- Headless MCP policy for reviewer autowork:
  - `AUTO_REVIEW_ENABLE_CLAUDEAI_MCP=1` is required to enable claude.ai MCP servers in unattended runs.
  - Default unattended behavior disables claude.ai MCP servers to avoid repeated Gmail/Calendar auth noise.
  - If runner logs `cached_needs_auth_for_google_connectors`, keep MCP disabled until OAuth is configured.

Routing/test conventions:
- API routes: `/v1/*`
- Health: `/health/live`, `/health/ready` (aliases `/health`, `/ready`)
- Test reset in test mode only: `POST /test/reset`

Task authority:
- Task files in `issues/*` are authoritative.
- `build/ISSUES.md` is resolved; historical "open blocker" sections in `build/DEPENDENCY-GRAPH.md` and `build/SUMMARY.md` are stale.

Stuck-review policy:
- Do not hold files in `04-in-review` indefinitely.
- If blocked, write blocker details in `issues/notes.md` and move the file back to `01-ready`.
- The watchdog may auto-requeue stale files from `04-in-review` back to `03-needs-review` to prevent deadlock.

If changes are required:
1. Keep task out of `05-completed`.
2. Add a new top-level `## Reviewer Required Changes (YYYY-MM-DD HH:MM TZ)` block to the task file with:
   - Reviewer name/model
   - Severity-ordered findings (P0-P3)
   - File references for each finding
   - Required fix for each finding
   - Required test(s) for each finding
3. Move the task from `04-in-review` to `01-ready`.
4. Append a concise summary and blockers to `issues/notes.md`.
5. Leave the implementation branch unmerged.

Required changes block template:

```markdown
## Reviewer Required Changes (YYYY-MM-DD HH:MM TZ)
Reviewer: <name/model>

### P1
- [ ] <finding summary>
  - Files: `<path:line>`
  - Required fix:
  - Required test:

### P2
- [ ] <finding summary>
  - Files: `<path:line>`
  - Required fix:
  - Required test:
```
