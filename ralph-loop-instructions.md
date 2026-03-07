# Ralph Loop — End-to-End Test Instructions

## What is this?

An end-to-end test of OtterCamp using a real project. The methodology: attempt the workflow via the TUI exactly as a human user would, fix any OtterCamp bugs encountered (for all users, not just this instance), wait for fixes to clear the Codex pipeline, rebuild, and continue. All project work is done by OtterCamp agents — the operator only steers via chat messages in the TUI and makes judgment calls when the spec doesn't cover something.

## Rules

1. **TUI only.** 100% of interaction with OtterCamp happens via tmux send-keys to the `oc-test` session. No curl, no API calls. If the TUI can't do something needed, file an issue and wait.

2. **Operator is the human.** Send messages, approve/reject staffing proposals, approve/reject inbox review items, and make judgment calls.

3. **File issues, don't fix.** When a bug is found, file it in `issues/01-ready/` per `issues/instructions.md`. Only fix directly if it's 2 lines of code or less. Otherwise Codex handles it.

4. **Spec compliance at every gate.** Every step is verified against `docsv2/` spec docs. If something violates the spec, it's a bug — file an issue.

5. **Push every commit.** After every commit, `git push origin v2` immediately. Also verify Codex's commits are pushed.

6. **Log judgment calls.** Any decision not covered by the spec goes into `decisions.md` with context, rationale, and alternatives. Sam reviews at the end.

## Pipeline Gate

Before proceeding with any testing phase:

1. Check: `ls issues/01-ready/ issues/02-in-progress/ issues/03-needs-review/ issues/04-in-review/`
2. If ANY files exist → wait 2 minutes, check again.
3. If an issue has been in the same stage for >10 minutes:
   - Read the issue file and check git log for recent Codex activity
   - If Codex produced a failing implementation: read the error, update the issue with better guidance, move it back to `01-ready/`
   - If Codex is confused about requirements: clarify the issue description
   - If there's a missing dependency: file it as a new issue or fix it (if ≤2 lines)
   - If Codex isn't running: alert Sam
4. Once all four directories are empty:
   - `./build/verify-restart-readiness.sh`
   - If the smoke gate fails: stop, file/update the blocking issue, and do NOT restart the full Sam.blog run.
   - Restart server + worker using the fresh `bin/ottercamp` build produced by the smoke gate
   - Verify no unpushed commits, push if any exist
   - Proceed with testing

## Phase 1: Project Creation

Run the pipeline gate. Once clear, rebuild and restart.

Reset the org session via `:clear`. Send message to Frank via TUI with the project brief (customize per project). Example for Sam.blog:

> Create a project called 'Sam.blog' for rebuilding my personal website. Sam.blog will be a central hub for my identity on the internet. The goal is to show my breadth and depth and make people want to ask me to come speak at their events, use me as an expert consultant, or offer me a ridiculous salary to come and work for them.
>
> The project needs to:
> 1. Visit technonymous.org, pull ALL of my existing blog posts, and store them in the project's git repo. These cover ethics, the internet, and parenting. Every single post needs to be captured — don't stop early.
> 2. Build 10 different layout template options as HTML files for the new site design.
> 3. Create a content strategy document. The new site will contain the migrated Technonymous posts plus new technical posts about AI and orchestration, general thought leadership, and an archive of my photography work.
> 4. Generate ideas for 20 new blog posts that incorporate my thoughts and technical capabilities across all these areas.
>
> Hand this off to Lori for staffing.

**Verify** (spec doc 03, 05):
- Frank calls `project.create`
- `project.staffing_needed` event published
- Frank does NOT assign himself as PM
- Frank messages Lori with project context

If something breaks → file issue → pipeline gate → rebuild → retry.

## Phase 2: Staffing

Lori responds to the staffing event by proposing agents.

**Verify** (spec doc 05 §Staffing Workflow):
- Lori proposes a PM (NOT Frank, Lori, or Ellie)
- Lori proposes workers/reviewers (NOT starter trio)
- Staffing plan presented for approval
- Approve via TUI (`:inbox` → `a`)
- PM assigned to project

If Lori tries to assign starter trio as PM/worker/reviewer → should be blocked. If Lori skips staffing and goes straight to tasks → that's a bug, file an issue.

Judgment calls about agent specializations, team size, etc. → decide, log to `decisions.md`.

## Phase 3: Task Scoping

The PM (not Lori, not starter trio) breaks the project into tasks.

**Verify** (spec doc 03 §Task Lifecycle):
- Each task has a flow template attached
- Each flow template has at least one review node on every path to done
- Review nodes have both approve and reject edges
- Tasks start in `draft`
- Tasks cannot leave draft without a flow template

## Phase 4: Task Execution

Tasks transition `draft → queued → in_progress` automatically. For each task:

**Queue gate**: `TaskQueueProcessor` picks up the task, starts the flow, creates a run, agent gets kickoff message.

**Execution**:
- Assigned agent (NOT starter trio) does the work
- Agent works in `~/otter-data/workspaces/{orgSlug}/{projectSlug}/`
- Files written to project workspace git repo
- If max tool calls hit → auto-continuation kicks in
- Agent calls `flow.advance` when work node is complete

**Review gate**:
- Flow advances to review node automatically
- Reviewer agent (different from the worker) evaluates the work
- Approve → flow advances toward done
- Reject → flow loops back to work with feedback
- Human review nodes → inbox item → approve/reject via TUI

**Completion gate**:
- Task reaches "done" ONLY via flow terminal node
- No agent can bulk-mark tasks as "done"

## Phase 5: Content Verification + Full Spec Audit

**Verify deliverables** — check that the actual output matches what was requested. Don't trust agent claims of completeness — verify independently.

**Full spec audit** — go through every spec doc in `docsv2/` and verify the process that happened lines up with the intended process:
- Agent assignments (doc 05)
- Flow progression (doc 03)
- Task lifecycle (doc 03)
- Workspace paths (doc 08)
- Turn handling (doc 16)
- Memory (doc 06)
- Control plane (doc 16)
- Any other relevant spec

## Phase 6: When Something Breaks

1. Identify root cause, check against `docsv2/` spec
2. File issue in `issues/01-ready/`
3. **Stop testing**
4. Run pipeline gate (wait for all lanes empty)
5. Rebuild, restart
6. Resume from where it broke

## Interaction Cheat Sheet

| Action | tmux command |
|--------|-------------|
| Send message | `tmux send-keys -t oc-test "message text" Enter` |
| Navigate to project | `tmux send-keys -t oc-test ":project Sam.blog" Enter` |
| Navigate to inbox | `tmux send-keys -t oc-test ":inbox" Enter` |
| Navigate to task | `tmux send-keys -t oc-test ":task OC-1" Enter` |
| Navigate to dashboard | `tmux send-keys -t oc-test ":dashboard" Enter` |
| Approve inbox item | Navigate → `tmux send-keys -t oc-test "a"` |
| Reject inbox item | Navigate → `tmux send-keys -t oc-test "x"` |
| Refresh sidebar | `tmux send-keys -t oc-test "r"` |
| Cancel active turn | `tmux send-keys -t oc-test ":cancel-turn" Enter` |
| Capture screen | `tmux capture-pane -t oc-test -p` |

## Success Criteria

- Project exists with all tasks in `done` status
- Every task went through the full flow: `work → review → done`
- All work done by project-level agents (not starter trio)
- Deliverables verified independently
- All files in project workspace at `~/otter-data/`
- Pipeline empty (no issues in 01-04)
- `decisions.md` captures any judgment calls made
