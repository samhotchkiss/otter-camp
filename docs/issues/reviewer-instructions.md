# OtterCamp Reviewer Instructions (Claude Opus)

Use this file when reviewing implementation work completed from specs in `/Users/sam/Documents/Dev/otter-camp/issues`.

## Workspace Paths (Required)

- Review worktree (all git/test commands): `/Users/sam/Documents/Dev/otter-camp-review`
- Shared spec queue (local, gitignored): `/Users/sam/Documents/Dev/otter-camp/issues`

## Reviewer Prompt Template (Copy/Paste)

Use this prompt when starting a review with Claude Opus:

```text
You are the reviewer for OtterCamp implementation work.

Follow this file exactly:
/Users/sam/Documents/Dev/otter-camp/issues/reviewer-instructions.md

Scope selection:
- Automatically pick the next spec that needs review.
- Do not wait for a human to specify a spec.
- Use this queue order:
  1) `/Users/sam/Documents/Dev/otter-camp/issues/03-needs-review` (oldest/lowest-numbered first)
- Review exactly one spec at a time.
- If no spec exists in `/Users/sam/Documents/Dev/otter-camp/issues/03-needs-review`, report \"No specs pending review\" and EXIT IMMEDIATELY. Do not wait or loop.

Workspace rule:
- Execute all git + test commands from `/Users/sam/Documents/Dev/otter-camp-review`.
- Read/update spec files only in `/Users/sam/Documents/Dev/otter-camp/issues`.

Review requirements:
1) Run preflight checks from the reviewer instructions.
2) Audit functionality, regressions, and security.
3) Validate tests (new behavior, regressions, edge cases).
4) Report findings first, ordered by severity (P0-P3), with exact file/line references.
5) For each finding, include impact, recommended fix, and test coverage needed.
6) If no findings, explicitly say “No findings” and list residual risks/gaps.
7) Provide final disposition:
   - APPROVED (move to 05-completed), or
   - CHANGES REQUIRED (add required-changes block at top of spec and move to 01-ready).

Do not skip verification steps, and do not infer correctness without evidence.
```

## Reviewer Mission

Validate that delivered work is correct, secure, tested, and production-safe before it moves from `03-needs-review` to `05-completed`.

Primary focus order:
1. Functional correctness and regressions
2. Security and data-safety risks
3. Missing/weak tests
4. Maintainability and clarity

## Local Issues Directory Policy

- `issues/` is local-only and gitignored.
- Do not require commits to `issues/` files.
- Use folder state transitions as review control.

## Branch and Merge Policy (Required)

- Implementation is branch-only; do not accept direct implementation commits to `main`.
- One active spec per implementation branch.
- Reviewer validates and merges approved implementation branches into `main`.
- If unrelated spec work is mixed into the review branch, fail review and require branch isolation before re-review.

## Preflight (Run First)

1. Confirm what is waiting for review:
   - Check `/Users/sam/Documents/Dev/otter-camp/issues/03-needs-review/`.
   - Select the next spec automatically (oldest/lowest-numbered first).
   - If none exist, report \"No specs pending review\" and stop.
2. Check active implementation overlap:
   - Check `/Users/sam/Documents/Dev/otter-camp/issues/02-in-progress/` to avoid reviewing a moving target unless explicitly requested.
3. Verify implementation scope:
   - Read the selected spec file from the review queue.
   - Read linked GitHub issue(s) and acceptance criteria.
   - Verify the spec contains a `## Execution Log` section with timestamped issue/commit/test entries.
   - Verify implementation issues are micro-sized (single shippable behavior per issue); flag oversized/bundled issues.
4. Verify code state:
   - Run `git status --short` and identify uncommitted changes.
   - Review recent commits related to the spec.
5. Verify branch isolation:
   - Confirm review is being performed on the spec's implementation branch, not `main`.
   - Confirm branch does not include unrelated spec commits.

## Review Procedure (Per Spec)

1. Move selected spec from `03-needs-review` to `04-in-review` before starting analysis.
2. Read spec requirements and acceptance criteria.
3. Map spec requirements to concrete code changes.
4. Run tests:
   - targeted tests for changed areas
   - regression suite as appropriate
5. Inspect for failure modes and security:
   - auth/org scoping
   - input validation
   - permissions checks
   - error handling/retry behavior
   - unsafe defaults
6. Validate UX/behavior edge cases:
   - empty/null states
   - reconnect/retry paths
   - stale data behavior
7. Produce findings with severity and exact file references.

## Severity Model

- `P0`: must-fix, production break/security critical
- `P1`: high risk, likely user-visible failure
- `P2`: moderate defect or meaningful gap
- `P3`: low-risk polish or clarity issue

Each finding should include:
- severity
- exact file path and line reference
- impact
- concrete fix recommendation
- test needed to prevent recurrence

## Required Test Validation

For each reviewed spec, confirm:
1. New behavior tests exist and pass.
2. Regression tests exist for bug fixes.
3. Critical edge cases are covered.
4. Commands executed and outcomes are recorded in review notes.

If tests are missing:
- mark as finding
- specify exact test file(s) and scenarios to add

## Pre-Merge Gate (MANDATORY)

Before merging ANY branch to main, run these checks from the review worktree on the branch:

```bash
cd /Users/sam/Documents/Dev/otter-camp-review
git checkout <branch>
git merge main --no-commit --no-ff  # test merge with current main
go vet ./...                         # must pass with zero errors
go build ./...                       # must compile cleanly
cd web && npx vitest run             # all frontend tests must pass
```

**If any of these fail, DO NOT MERGE.** The branch has dependencies on code that hasn't landed yet or introduces compile errors. Mark as CHANGES REQUIRED and note the specific failures.

Common failure: Codex specs write tests referencing types/methods from OTHER specs that haven't merged. If you see `undefined: SomeType` or `unknown field X`, the branch depends on another unmerged spec. Do not merge — move to `01-ready` with a note about the missing dependency.

## Session Lifecycle (MANDATORY)

**After completing your review (whether APPROVED or CHANGES REQUIRED), you MUST exit.** Do not loop, do not wait, do not pick up another spec. The cron will start a fresh session for the next review.

If no specs are pending review, report "No specs pending review" and exit immediately.

## Review Outcome Rules

If review passes (AND pre-merge gate passes):
1. Move spec from `04-in-review` to `05-completed`.
2. Add a short approval note in `/Users/sam/Documents/Dev/otter-camp/issues/notes.md` (optional but recommended).
3. Merge the reviewed implementation branch into `main`.
4. Ensure no `## Reviewer Required Changes` block remains at the top of the spec; resolved items should be preserved in `## Execution Log` history.
5. **Exit the session.**

If findings exist:
1. Keep spec out of `05-completed`.
2. Add a new `## Reviewer Required Changes` block at the very top of the spec file, including:
   - review date/time
   - reviewer name
   - severity-ordered findings (P0-P3)
   - exact file references
   - required tests for each fix
3. Move spec from `04-in-review` to `01-ready`.
4. Open follow-up GitHub issue(s) for each required fix (small, test-defined units).
5. Keep implementation branch open for fixes; do not merge it to `main`.
6. If the execution log is missing or incomplete, include it as a required change.
7. If issue decomposition is too coarse (few large bundled issues), include splitting requirements as a required change.
8. **Exit the session.**

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

## Practical Commands

- `cd /Users/sam/Documents/Dev/otter-camp-review && git status --short`
- `cd /Users/sam/Documents/Dev/otter-camp-review && git log --oneline -n 30`
- `cd /Users/sam/Documents/Dev/otter-camp-review && gh issue list --limit 50`
- `cd /Users/sam/Documents/Dev/otter-camp-review && go test ./...`
- `cd /Users/sam/Documents/Dev/otter-camp-review/web && npm test`
- `cd /Users/sam/Documents/Dev/otter-camp-review/web && npm run build:typecheck`

Use targeted test commands first, then broader suites.

## Default Reviewer Stance

- Assume implementations may be incomplete even if tests pass.
- Prefer explicit verification over inference.
- Be strict on production risks, practical on minor style concerns.
- Block completion when behavior, safety, or tests are not convincingly correct.
