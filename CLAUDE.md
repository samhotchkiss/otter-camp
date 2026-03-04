# OtterCamp — Claude Code Instructions

## Ralph Loop Rules (MANDATORY)

When running a Ralph Loop (the `/ralph-loop` skill or any end-to-end platform test):

### BEFORE EVERY ACTION, check these rules:

1. **ALL interaction with OtterCamp MUST happen via the TUI.** Use `tmux send-keys -t oc-test` for everything. NEVER use:
   - `curl` to hit the API directly
   - `psql` to query or modify the database
   - Direct CLI commands against the OtterCamp binary
   - Any other backdoor that bypasses the TUI

2. **File issues for Codex.** Do NOT fix bugs directly (unless truly ≤2 lines). Write an issue file in `issues/01-ready/` and let the Codex pipeline handle it. The whole point of the Ralph Loop is to test the platform as a user would experience it.

3. **Push every commit.** If you do make a code change, push it immediately.

4. **You are the human operator, not a developer.** Your job is to:
   - Send messages to agents via the TUI chat
   - Approve/reject staffing decisions
   - Review inbox items
   - Make judgment calls
   - Log decisions to `decisions.md`

5. **Don't tell agents HOW to do things.** Let them figure it out. Guide with WHAT, not HOW.

6. **Never modify the database directly.** No SQL updates, no job queue manipulation, no model profile switches. If something is broken, file an issue.

7. **Never switch model providers without Sam's approval.** The model profiles are set deliberately.

### CONTEXT COMPACTION RULE

**If you are in a Ralph Loop and your context has been compacted, you MUST re-read the loop instruction file before taking any action.** The loop instructions are stored at:
- The ralph-loop skill definition (invoke `/ralph-loop:help` if unsure)
- `decisions.md` in the repo root for decisions already made
- `.claude/plans/` for the current plan

Context compaction is the #1 cause of rule violations. When you lose context, you lose the rules. RE-READ THEM.

### What to do when blocked

When you hit a blocker (API credits exhausted, bugs, etc.):
1. **STOP.** Do not work around it.
2. **Tell Sam.** Describe the blocker clearly.
3. **File an issue** if it's a code bug.
4. **Wait** for Sam's guidance or for the issue to be fixed.

Do NOT: switch providers, dead-letter jobs, send API calls directly, modify the DB, or take any other shortcut. These shortcuts corrupt the test and invalidate the entire loop.

## General Development

- Build: `go build -o bin/ottercamp ./cmd/ottercamp`
- Branch: `v2` is the active development branch
- Tests: `go test ./...`
