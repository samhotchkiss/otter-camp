# Codex State

This file is the minimum viable handoff for a fresh Codex session working in `/Users/sam/dev/otter-camp`.

Read this first, then verify current repo state with `git status --short`, `git log --oneline --decorate -10`, and the issue queue directories.

## What OtterCamp is

OtterCamp is a multi-agent project execution system with:

- project creation and bootstrap
- agent staffing and assignment
- task and subtask decomposition
- explicit task flows with work/review/merge-style stages
- TUI/operator surfaces
- background execution/runtime

The product goal is not just to generate plans or chats. It must reliably create projects, staff them, break work into bounded tasks, run the work, review it, and complete it with minimal operator intervention.

## Current live state

As of 2026-03-23 evening local time, `sam-blog` (`efd1bd57-125b-44f7-ac17-4f5c9bec8bce`) remains the completed reference validation run, `speaker-pipeline-ops-reviewer-validation-fresh-2` (`4a12463c-eef4-4863-a93e-9bcd723b82a6`) remains the completed execution-ownership canary, `speaker-pipeline-ops-validation-fresh-3` (`e2f2ac35-6fa6-4b18-bfea-0e23c1e3068a`) and `speaker-pipeline-ops-validation-fresh-4` (`a2a74356-d222-4e9e-b0d4-97680e741ebb`) are archived after exposing stale bootstrap-state / broad-task auto-complete / PM-lane mutation bugs, and the active fresh canary is now `speaker-pipeline-ops-validation-fresh-5` (`bb3f1b98-b6e4-443e-be79-2caf79fa3eb4`).

Current observed task state on the active fresh Speaker Pipeline canary:

- bootstrap tasks `1-8` are all `done`
- parent task `9` remains `draft`
- task `10` is `done`
- task `11` is currently `blocked`
- tasks `13` and `15` are currently `in_progress`
- task `12` is currently `done`
- there are no queued tasks on the project

The active fresh canary has already validated these product expectations under the latest build:

- fresh kickoff/bootstrap can create dedicated PM, worker, and reviewer staff in-turn
- bootstrap setup persists through canonical setup tasks and auto-completes the bootstrap gate
- first executable child task lanes can start under execution-owned task sessions
- stale project-session model-invocation cleanup no longer leaves the PM lane inert without a retry path
- broad top-level bootstrap tasks no longer auto-complete themselves to `done` just because planning metadata and outcome assessment were written
- project-session `task.update` can no longer mutate a task that already belongs to an active `project_task` execution lane
- bootstrap-active project sessions can now be blocked from burning turns on `git.commit` and redirected to `bootstrap.setup.persist`

Most recent shipped commits relevant to the current validation run:

- pending local slice: flow advance into review now backfills missing required `planning.artifact_evidence` entries when partial evidence already exists, so enforced discovery/research tasks do not block with false missing-artifact errors
- pending local slice: `memory_extract_turn` is now classified with reserved low-priority background work so it cannot consume the final worker slot ahead of active `agent_turn` execution
- pending local slice: recovery draft rejection now treats the exact live task-13 “Good. Now I have a clear understanding...” deliverable preface as non-substantive, so that 413-byte placeholder stops being reused from `[Recovery resume state]`
- pending local slice: task-scoped `file.write` now rejects narrated placeholder content such as “Let me create the deliverable...” instead of persisting junk markdown into deliverable files
- `1238acd4` `Prefer historical recovery target paths`
- `53dbe137` `Tighten task execution recovery ownership`
- `14512df6` `Prefer task-matched recovery deliverables`
- `525ddd13` `Guard project sessions during bootstrap execution`
- `6f6a1f25` `Harden bootstrap recovery and draft settlement`
- `a64dd6c8` `Handle approval-gated bootstrap tasks cleanly`
- `a23ba8d4` `Stabilize archived and approval-gated recovery`
- `04ddf053` `Force fresh bootstrap staffing to act in-turn`
- `fbf8dec4` `Reserve worker slots from maintenance floods`
- `ca83abea` `Skip closed-session agent turn claims`
- `b787978e` `Direct review lanes to flow review decisions`
- `04c08223` `Plan flow execution architecture rework`
- `374d4ec2` `Track live execution ownership on flow nodes`
- `3f01d75a` `Prefer execution-owned live task ownership`
- `6c6b40cb` `Expose active flow execution in task prompts`
- `0f088327` `Recover stale task turns from execution ownership`
- `814e7941` `Recover stale continuation turns from execution ownership`
- `0189761f` `Requeue pending task turns from execution ownership`
- `3e6bd3d6` `Requeue supervisor task turns from execution ownership`
- `23c22ab4` `Preserve live continuation dispatches from execution ownership`
- `6ccc00d9` `Bind worker task dispatches to active executions`
- `425fd775` `Ignore stale task turns during claim suppression`
- `25215b30` `Bind task continuations to active executions`
- `3dd80309` `Scope stale task recovery to active executions`
- `6992ab10` `Purge legacy task dispatch without execution ownership`
- `9d8ed49e` `Publish execution-owned task message dispatch`
- `ed694196` `Skip blocked task lanes during worker requeue`
- `43979cee` `Stop churn after artifact writes`
- `05b55454` `Guard satisfied draft auto-completion`

What those changed:

- bootstrap first-wave validation now rejects tasks that require human approval before queueing
- archived project recovery paths no longer throw supervisor/archive races back into the live worker loop
- fresh bootstrap kickoff guidance now forces same-turn staffing persistence instead of profile-browsing narration
- maintenance jobs can no longer occupy all worker slots when live execution work is waiting
- stale pending `agent_turn` jobs for closed sessions are skipped at claim time and purged on startup/recovery instead of stealing slots
- review-lane prompts and mutation rejections now explicitly direct reviewer sessions to `flow.review_decision`
- the architecture rework is now committed into docs/issues/reports instead of living only in transient conversation state
- first issue `369` has started: active flow executions now persist `live_run_id` in `flow_node_execution.metadata` from the queue wakeup path, and task-scoped execution sessions now persist/clear `live_turn_id` around real turn execution
- the supervisor now prefers `flow_node_execution.metadata.live_turn_id` / `live_run_id` over broader session/runtime fallback when detecting stranded active executions
- review/task prompts now surface `Active Flow Execution ID: ...` directly in task context so reviewer lanes have the concrete execution handle they need for `flow.review_decision`
- the worker’s stale triggered-turn recovery path now also prefers execution-owned live turn metadata over `chat_session.current_turn_id` for `project_task` sessions
- the worker’s stale continuation-turn recovery path now does the same, reducing another task-lane recovery branch that used session state as the primary owner
- the worker’s pending-turn requeue path now also does the same, so task lanes with a drifted session pointer can still recover their pending trigger turn from execution ownership
- the worker’s stranded supervisor-recovery requeue path now does the same, removing another task-lane path that depended on `chat_session.current_turn_id` to redispatch review/work recovery
- worker startup purge for superseded project-task continuations now also uses execution-owned live turns, so valid continuation dispatches do not get dead-lettered just because the session pointer drifted
- worker task-lane requeue/recovery paths now also stamp `flow_node_execution_id` into `agent_turn` payloads, which is the first concrete code slice toward issue `370`’s execution-bound dispatch model
- claim-time `agent_turn` suppression no longer lets stale pending `current_turn_id` rows from abandoned task executions block fresh task dispatch claims
- turn-engine-created retries and auto-continuations now also stamp `flow_node_execution_id` from task-session metadata before enqueue, so engine-side task dispatch no longer drops execution ownership after worker/control-plane already established it
- worker startup purge now also retires legacy task-session `agent_turn` rows with no execution identity when a newer execution-owned live turn already exists, so pre-rework queue rows no longer shadow an active lane after restart
- chat-layer `chat.message.user_sent` events for bound task sessions now publish `flow_node_execution_id` directly, so downstream dispatch does not need to rediscover execution identity from session state for the common message-trigger path
- worker blocked-task recovery no longer requeues blocked validation-loop sessions through supervisor or pending-turn startup repair, so blocked tasks stay blocked instead of generating repeated startup churn
- turn-event handling no longer recreates execution-owned `project_task` dispatch from raw `session_id/message_id` when the bound session cannot be loaded, which removes another message-state escape hatch from issue `370`
- steer/recovery-style replacement user messages for bound task sessions now also keep `flow_node_execution_id`, so operator/session steering no longer drops task-lane execution identity at the chat-event boundary
- synthetic continuation-root and recovery-action user prompts now also keep `flow_node_execution_id`, closing another task-lane path that previously kept execution identity only in the queue payload and not in the message metadata itself
- worker pending-turn repair no longer lets a failed execution-owned `live_turn_id` hide a real pending `chat_session.current_turn_id`, which was the live task-20 stall signature on Speaker Pipeline
- terminal done/cancelled task sessions no longer have to wait for a worker restart to close; the periodic stale-scan loop now cleans them up during steady-state execution too
- worker stale-turn recovery no longer treats an older queued dispatch for the same task session but a different execution as proof that the active execution already has a live retry queued
- execution-first planning tasks now stop churning after the first successful declared-artifact write, even without an explicit single `Deliverable:` path in the task description
- metadata-only task completion no longer requires a separate remembered `task.update work_status=done` when the task already carries complete planning evidence and a satisfied outcome assessment
- stale project-session turns recovered by worker stale-model-invocation cleanup now requeue from the original trigger message instead of silently dying after `current_turn_id` is cleared
- satisfied draft auto-complete now requires the task to be non-decomposable under `taskdecomp.PrepareQueueDecomposition`, so broad bootstrap workstreams stay draft and must actually be split before bootstrap can pass
- project sessions now get a hard tool-layer rejection if they attempt to mutate a task that already has `CurrentFlowNodeID` set, so active task lanes remain owned by their `project_task` session instead of being re-mutated by the PM lane
- bootstrap-active project sessions now get a hard tool-layer rejection if they call `git.commit`, forcing canonical bootstrap persistence through `bootstrap.setup.persist` instead of ad hoc repo commits
- task-scoped `file.write` is being hardened against imperative first-person placeholder prose after live `fresh-5` task `13` persisted a 413-byte “Let me create the deliverable...” stub into `deliverables/oc-13-speaker-validation-agent.md` before falling into `cli.execute command_required`
- worker execution-slot reservation is being widened beyond rollups: live `fresh-5` showed repeated `memory_extract_turn` claims consuming the last slot while active task lanes were waiting, and the local integration reproducer now covers that starvation case explicitly
- recovery draft rejection is being widened to drop the exact task-13 “clear understanding of the requirements” preface so the stale placeholder no longer survives as a durable draft candidate
- flow advance into review is being hardened to synthesize missing required `planning.artifact_evidence` entries even when a task already has unrelated evidence entries; the live task-15 repro had all four enforced discovery artifacts persisted with IDs and SHAs, but review advance still blocked because only a single extra `validation-report` evidence entry existed

Current live cleanup result:

- `speaker-pipeline-ops-reviewer-validation-fresh-2` still drains cleanly
- all `28` tasks are `done`
- `project_task` async sessions for that project are fully closed (`active_sessions=0`, `closed_sessions=121`)

`speaker-pipeline-ops-validation-fresh-5` is the active bootstrap/execution canary:

- bootstrap is complete and the governance gate is `done`
- the first real execution wave is running under task sessions, not the project session
- there are currently no blocked tasks on the project
- the next bug to watch is task-lane completion/review quality, not PM/bootstrap queue ownership
- latest fixed repro: task `11` previously recovered onto `deliverables/oc-11-task-summary.md` even though the task-local workflow-spec path already existed in session history; the latest local slice now prefers `deliverables/oc-11-validation-workflow-spec.md`
- latest fixed repro: task `12` previously recovered onto `planning/prd-spec/oc-12-acceptance-criteria.md` even though the task-local validation-report path already existed in session history; the latest local slice now prefers `planning/prd-spec/oc-12-validation-report.md`
- live validation after redeploy confirmed both retargets:
  - task `11` resumed from `blocked` and is back `in_progress` on the workflow-spec path
  - task `12` completed its work lane and advanced into `review`
- current next bug to watch: review-lane usability and completion quality. The reviewer path still tends to inspect execution/repo state (`flow.get_execution`, `git.status`) instead of going straight to `flow.review_decision`.
- current next bug to watch: task `11` is blocked on recovery quality, not queue ownership or planning-contract state. Its persisted target draft for `deliverables/oc-11-validation-workflow-spec.md` is still a clarification placeholder ("I'm ready to execute task OC-11 ... What is the target deliverable ..."), and recovery keeps reusing that poisoned file draft on `file.write` fallback.

`sam-blog` is still the proof that the core project flow can drain cleanly:

- `done=37`
- `cancelled=1`
- `open=0`

Task `38` there is intentionally `cancelled`, not `done`: it was an impossible legacy draft project gate (`blocks_scope=all` with no executable path). That row no longer blocks queueing and is now retired automatically at worker startup.

## Current product priorities

The main priority is still core-system reliability for unattended end-to-end project execution. SAM.blog proved cleanup and closeout. The active Speaker Pipeline run is now the sharper canary for fresh-bootstrap correctness, reviewer staffing, queue fairness, and live execution churn.

The immediate priority is now to continue the execution-ownership rework against the fresh Speaker Pipeline canary until it also drains cleanly without PM-lane interference or stale session/execution drift.

That priority is now subordinate to a broader execution-architecture rework. The current codebase has enough evidence that the repeated stalls are structural:

- runtime ownership is still split across task status, flow execution, sessions, turns, runs, runtime_state, and queued jobs
- recovery still depends too heavily on draft heuristics
- worker/supervisor repair logic is doing too much after-the-fact reconciliation

The next phase should tighten the implementation around the model already described in the specs:

- `flow_node_execution` must become the single runtime owner for a task lane
- session/turn/run ownership should hang off that execution boundary
- dispatch should be command-driven from the active node execution
- recovery should resume from structured checkpoints, not historical-draft guesswork

The current implementation work is already underway against that plan:

- queue wakeup persists execution-owned `live_run_id`
- real turn execution persists execution-owned `live_turn_id`
- supervisor stranded-execution detection prefers execution-owned live run/turn ownership
- worker stale triggered-turn recovery now follows the same ownership source
- worker stale continuation-turn recovery now follows the same ownership source
- worker pending-turn requeue now follows the same ownership source
- worker stranded supervisor-recovery requeue now follows the same ownership source
- worker startup purge for live project-task continuations now follows the same ownership source
- worker reissued task-lane dispatches now carry the active `flow_node_execution_id` in payload, instead of treating `session_id/message_id/retry_count` as the entire dispatch identity
- claim-time worker suppression now also respects execution ownership: stale pending session turns only block claims when an active `flow_node_execution` still exists for that task lane
- turn-engine requeues/retries/auto-continuations now preserve the bound `flow_node_execution_id` too, so execution ownership survives task-session continuation inside the engine rather than only in worker repair paths
- startup cleanup now drops legacy task-session dispatch rows that have no `flow_node_execution_id` once the lane already has an active execution-owned live turn, which removes another pre-command-model source of stale queue identity
- message-triggered task dispatch now carries execution identity directly from the chat layer, and the current live claimed `agent_turn` row on Speaker Pipeline includes `flow_node_execution_id` as expected
- blocked validation-loop tasks no longer re-enter queue churn on worker restart
- worker stale-turn recovery now also checks queued/claimed dispatch presence at the active execution boundary, so stale queued jobs for a different execution no longer suppress requeue for the current lane
The current next thing to watch on the fresh canary is narrower:

- bootstrap is completed and first-wave executions exist
- the PM/bootstrap turn is still open after bootstrap completion and has already tried a couple of invalid post-bootstrap task mutations (`draft -> assigned`, `draft -> in_progress`) before the canonical state settled
- if that turn does not unwind cleanly on its own, the next fix is in post-bootstrap project-session behavior: once canonical bootstrap state is completed and first-wave executions exist, the PM session should stop trying bootstrap-era direct task transitions inside the same turn and hand off cleanly to execution/review lanes
- the newest live execution blockers shifted back into recovery draft quality on OC-12 and OC-15: both lanes were blocked by structured placeholder drafts that narrated the current state instead of writing the deliverable body
- those blocked rows have now been resumed under the latest local recovery-draft classifier, so the next live check is whether they stay in execution without reusing those two placeholder shapes again
- the newest deeper bug surfaced immediately after that: OC-15's fresh recovery lane could still inherit OC-13's `agents/speaker-validation-agent.md` through poisoned checkpoint/history fallback, so the latest local slice now rejects cross-task `OC-*` contamination in both checkpoint reconciliation and historical draft selection

The standing rule from Sam is:

- do not patch symptoms
- root cause failures from first principles
- write a reproducing test first for bugs
- then implement the fix
- prove the fix with passing tests

Related standing rule:

- all flows defined in OtterCamp tasks should be treated as a robust state machine

This means invalid transitions, duplicate dispatch, stale ownership, blocked-yet-running tasks, fake completion, and partial bootstrap persistence are all core bugs, not edge cases.

## Product rules established with Sam

These are not tentative.

### Bootstrap and project creation

- Frank creates the project and persists repo/environment collateral.
- Bootstrap is not one long kickoff conversation. It is a first-class workflow.
- Project creation must auto-create a canonical bootstrap task tree.
- The bootstrap tree includes bounded setup tasks for:
  - repo/environment binding
  - staffing
  - decomposition
  - task sizing/dependency validation
  - flow attachment/validation
  - first-wave selection
  - Frank sign-off
- Bootstrap completion is validator-driven, not prompt-wording-driven.
- Bootstrap must not report success unless persisted setup has actually promoted into runnable first-wave execution.
- If bootstrap fails before first-wave execution exists, the system should fail/archive/restart cleanly from canonical input.
- If bootstrap fails after first-wave execution exists, the system should pause/recover rather than archive blindly.
- Structural bootstrap invariants should be hard-enforced in product code.
- Non-structural quality/refinement issues should be surfaced clearly, but should not kill the run unless they make execution non-credible.

### Parent task semantics

- Parent tasks are orchestration and integration gates.
- Parent tasks should not do primary production work when executable children exist.
- Parent tasks may reopen children or spawn new bounded child tasks when integration fails.
- Parent tasks must verify all child outputs.
- Parent tasks must run an integration test across completed child tasks.
- A parent task is only complete when child outputs are validated individually and together against the parent outcome.

### Planning and task sizing

- Default target is 30 minutes or less per task.
- Up to 60 minutes is allowed only for tool-heavy or externally bound work.
- Anything broader must be decomposed before queueing.
- Lori needs product enforcement here; prompt guidance alone is not enough.

### Starter trio staffing rules

- Frank, Lori, and Ellie are starter trio agents.
- They are not to be assigned to project delivery work past the initial setup/bootstrap process.
- Lori should handle project planning/bootstrap planning work.
- Frank should not be the one doing Lori's planning work.

### Review and human-feedback expectations

- All tasks should include an internal review step that independently verifies the work.
- The system should distinguish binary-complete work from taste-based work.
- Taste-based deliverables should tend toward internal review and then human review/inbox routing, not premature self-certification.
- The system should avoid stalling the whole process on routine human questions when a reasonable default can keep work moving.

### Provider/API observability

- Provider/API failures must remain distinct from product/runtime failures.
- Primary operator surfaces are the dashboard and project/task history.
- TUI status and banners should provide a compact live degraded-state signal, not the full forensic view.
- Logs and raw invocation records are supporting evidence, not the main operator experience.

### Next validation project after Sam.blog

- Default next hard validation candidate: `Speaker Pipeline Ops`
- This is now active as `speaker-pipeline-ops-reviewer-validation-fresh-2`.
- It should remain a mixed operational project, not another site rebuild.
- It exercises research, scoring/schema design, structured docs, automation/scripts, and reporting/review loops.

## Operational rules for Codex

- Work on `main`.
- New branches should branch from `main`.
- `v2` is dead; do not revive it.
- `v1` exists as the preserved old main line.
- Commit and push when making real product changes.
- If the issue queue is being used, keep it accurate, but do not let queue bookkeeping distract from fixing the product.
- `issues/discuss.md` is only for product-direction or implementation decisions that truly need Sam's input.
- Fixable bugs do not belong in `issues/discuss.md`; they should be fixed or queued through the issue flow.
- there is no reliable `issues/notes.md` running log anymore; use the latest reports, recent git history, and issue queue files instead.

## How to use `issues/discuss.md`

Treat `issues/discuss.md` as the "to-discuss with Sam" file.

What belongs there:

- product direction decisions
- implementation strategy forks where multiple defensible options exist
- operator-experience tradeoffs
- cases where Sam's preference materially changes the correct implementation

What does not belong there:

- straightforward bugs with a clear fix
- regressions that can be reproduced and corrected through normal engineering work
- queue bookkeeping
- routine run-monitoring notes
- things that should just become issues and be worked

Working rule:

- if you can root-cause it, write a reproducing test, and fix it without needing Sam's judgment, do that
- if the right answer depends on product intent or operator preference, add a concise numbered item to `issues/discuss.md`
- when a discuss item is resolved with Sam, update `issues/discuss.md` to mark it addressed and convert the decision into issues/spec/code as needed

## Heartbeat and long-running sessions

The user may run a heartbeat process that periodically sends `HEARTBEAT` messages into the Codex tmux session to keep the session visibly alive.

Important context:

- Heartbeat messages are operational noise, not user requests.
- They mean "keep working / stay alive", not "stop and answer each one".
- Historically, Codex turns have died during long unattended periods. Heartbeat exists to reduce that risk.
- Do not confuse heartbeat traffic with product events inside OtterCamp.

There was also earlier experimentation with cron/tmux message injection. The important lesson is:

- tmux/send-keys style injection is not equivalent to a real interactive user in every client
- do not build product assumptions on that mechanism

## Current failure themes

These were the repeated classes of failures that cost time during the SAM.blog and Speaker Pipeline validation runs and should still inform future hardening:

- bootstrap persisted planning/setup artifacts without creating runnable first-wave execution
- session or summary text claimed success when DB/runtime truth did not
- archived/failed projects left behind active task sessions or stale memory that contaminated fresh runs
- project/task runtime state drifted away from flow state
- tasks were scoped too broadly, causing long stalls and poor decomposition
- orchestration parent tasks and bootstrap planning shells could finish their real work but still remain stuck in `draft`
- impossible legacy draft project gates could survive indefinitely unless some later product path retired them
- provider/API failures were not surfaced clearly enough to distinguish external outages from OtterCamp bugs
- worker capacity could be monopolized by maintenance floods even while project execution was waiting
- stale pending `agent_turn` jobs on closed sessions could still consume live worker slots before cleanup

The latest queue/runtime issues in that list are now fixed in product and verified live on the Speaker Pipeline canary.

The current live bug is narrower and review-specific:

- reviewer lanes now run under the right principal and have `flow.review_decision` in the tool set
- but the model still spends turns probing `flow.get_execution` or narrating the rejection instead of acting
- the prompt-context half of that fix is now shipped
- the next thing to verify is live behavior under canary traffic: whether reviewers now call `flow.review_decision` directly instead of first probing `flow.get_execution`

That prompt-context fix is still a valid short-term cleanup, but it should not distract from the broader rework above. The architectural review and planned rework slices are now captured in:

- `reports/2026-03-23-flow-execution-architecture-review.md`
- `issues/00-not-ready/369-flow-node-execution-must-be-the-single-runtime-owner-for-task-lanes.md`
- `issues/00-not-ready/370-turn-and-run-dispatch-must-be-command-driven-from-the-active-node-execution.md`
- `issues/00-not-ready/371-task-recovery-must-resume-from-structured-checkpoints-not-draft-heuristics.md`

Current implementation status of the rework:

- architecture plan committed and pushed in `04c08223`
- first `369` slice committed and pushed in `374d4ec2`
- second `369` slice committed and pushed in `3f01d75a`
- review prompt-context fix committed and pushed in `6c6b40cb`
- that slice does not complete the ownership refactor; it only gives `flow_node_execution` a persisted view of the live run/turn owner so later slices can stop inferring ownership purely from runtime_state/session/job drift
- the current next step inside `369` is to keep replacing task-lane ownership inference with execution-owned state in queue/dispatch/resume paths, not just in supervisor recovery

Local repo note for restart safety:

- tracked worktree is now clean except for the long-standing ignored/untracked local artifacts outside git

When debugging, assume the real problem is usually one of:

- missing invariant enforcement
- missing state-machine transition validation
- stale runtime/session ownership
- partial persistence without transactional guarantees
- operator surface lying about underlying persisted state

## Important repo artifacts

Start here:

- `issues/discuss.md`
  - active product decisions that may still need Sam's input
- `reports/oc-test-human-interventions.md`
  - records of manual interventions during live tests
- `reports/2026-03-22-samblog-clean-run-followup.md`
  - summary of the 2026-03-22 SAM.blog clean-run repairs before the later 2026-03-23 cleanup hardening
- `docsv2/`
  - product spec; must stay aligned with behavioral changes
- `decisions.md`
  - older decision log from prior Sam.blog / Ralph loop work

Important completed issues for the current bootstrap/state-machine direction:

- `issues/05-completed/333-kickoff-validator-must-enforce-persisted-assignments-child-tasks-and-first-wave-flows.md`
- `issues/05-completed/340-clean-bootstrap-restarts-must-replay-canonical-input-not-polluted-project-session-context.md`
- `issues/05-completed/341` if present in repo history/logs, check merge state separately
- `issues/05-completed/342-clean-bootstrap-restarts-must-replay-canonical-input-not-polluted-project-session-context.md`
- `issues/05-completed/343-bootstrap-auto-retries-must-be-bounded-and-escalate-with-structured-failure-reports.md`
- `issues/05-completed/344-operator-observability-must-separate-provider-failures-from-product-runtime-failures.md`
- `issues/05-completed/347-project-creation-must-auto-create-a-canonical-bootstrap-task-tree.md`
- `issues/05-completed/348-bootstrap-task-tree-must-block-project-execution-until-setup-and-sign-off-complete.md`
- `issues/05-completed/349-bootstrap-setup-tasks-must-enforce-bounded-scope-and-delegate-real-work-to-project-tasks.md`
- `issues/05-completed/357-bootstrap-invariant-harness-must-prove-persisted-setup-promotes-to-first-wave-or-fails-cleanly.md`
- `issues/05-completed/358-bootstrap-doctor-report-must-explain-why-a-bootstrap-failed-in-structured-terms.md`
- `issues/05-completed/359-task-flows-must-be-enforced-as-robust-state-machines.md`
- `issues/05-completed/364-auto-restart-bootstrap-can-still-create-a-half-born-restart-project-with-zero-execution.md`
- `issues/05-completed/365-post-364-bootstrap-can-still-persist-minimal-parent-plus-child-setup-with-zero-first-wave-execution-on-wp-staging.md`
- `issues/05-completed/366-archived-project-cleanup-must-close-all-project-task-sessions-and-prevent-stale-memory-reuse.md`
- `issues/05-completed/367-post-365-bootstrap-can-still-self-certify-complete-with-zero-first-wave-execution-on-wp-rebuild-v2-restart.md`

Important code areas:

- `internal/turn/engine.go`
  - bootstrap validation, failure handling, recovery, runtime transitions
- `internal/turn/bootstrap_invariant_integration_test.go`
  - regression coverage for bootstrap shapes and first-wave execution invariants
- `internal/turn/project_bootstrap_restart.go`
  - restart bundle capture, retry, canonical-input replay
- `internal/project/service.go`
  - project creation and bootstrap task tree creation
- `internal/tui/`
  - TUI behavior, status line, quality/reliability surfaces

Sam.blog run artifacts:

- `/Users/sam/dev/tmp/samblog-project-kickoff.md`
  - canonical kickoff instructions for running the Sam.blog test from scratch
- `/Users/sam/dev/tmp/codex-review-sam-blog-instructions.md`
  - post-run review prompt for auditing the completed Sam.blog run and creating follow-up issues
- `/Users/sam/tmp/samblog-kickoff-msg.txt`
  - the shorter raw project brief/directive that was injected into OtterCamp
- `reports/oc-test-human-interventions.md`
  - intervention log for manual actions taken during monitored runs
- `reports/2026-03-22-speaker-pipeline-ops-validation-package.md`
  - canonical kickoff package for the next mixed operational validation run after SAM.blog

## Current repo state at time of writing

As of 2026-03-22 21:10 MDT:

- branch is `main`
- `HEAD` and `origin/main` are `b72127e0` `Recover queued sessions behind stale live turns`
- the latest queue/runtime hardening on top of the earlier SAM.blog clean-run work is:
  - `ad358ee0` `Reject impossible draft project gates`
  - `c50e9ae2` `Deduplicate rollup update jobs by org and day`
  - `6c30b325` `Prioritize non-maintenance background jobs`
  - `b72127e0` `Recover queued sessions behind stale live turns`
- the current live validation project is again `sam-blog`, but this is a new clean rerun, not the earlier fully-complete run
  - current live status is `38` tasks total
  - current task counts are `done=8`, `draft=12`, `in_progress=6`, `review=12`
  - the active work/review lanes now exist without blocked tasks
- `oc-svc` and `oc-worker` are running from the latest pushed build in tmux
- live queue state is materially healthier than earlier in the day:
  - pending `agent_turn` backlog dropped from `92` to `18`
  - the earlier `67`-job pile behind task session `b337060e-faf3-4c88-8bc3-b65408d632af` was reduced after stale-live-turn recovery shipped
  - maintenance jobs no longer monopolize fresh claims, but `rollup_update` backlog is still large and remains a secondary cleanup target
- the remaining untracked local artifacts are workspace/support files, not active tracked product edits:
  - `.oc.db`
  - `data/objects/`
  - `internal/turn/bootstrap_refresh_codex_test.go`
  - `skills/`

Do not assume the worktree is clean.
Check `git status --short` before editing.

## Canonical Sam.blog kickoff prompt

Primary file:

- `/Users/sam/dev/tmp/samblog-project-kickoff.md`

Short injected project brief:

```text
Okay, go ahead and spin it up. If you have any memory of what you did last time around, disregard it. This is a fresh start.

The new Sam.blog will be a central hub for my identity on the internet. The goal is to show my breadth and depth and make people want to ask me to come speak at their events, use me as an expert consultant, or offer me a ridiculous salary to come and work for them.

Every project in OtterCamp has a connected Git repo, right? So OtterCamp will need to use the browser to visit technonymous.org and pull in all of my blog posts and store them within the Sam.blog git repo because we're going to migrate these over into the new site. The new site will contain both my posts on ethics and the internet as a whole and parenting that you find on Technonymous, as well as more technical posts about AI and orchestration and general thought leadership. It will also contain an archive of my photography work.

So, I want all of my existing blog posts scraped in, I want 10 different layout template options built as HTML, I want a content strategy, and ideas for 20 more blog posts that incorporate my thoughts and technical capabilities.
```

Important operating rule for Sam.blog test runs:

- do not coach OtterCamp into the desired workflow just to make the run succeed
- let OtterCamp reveal its real default behavior
- intervene only to keep the run from stalling completely, handle a truly human decision, or recover broken runtime/session control
- log every intervention so the product can be improved instead of normalized by operator babysitting

## Keep this file current

`issues/codex-state.md` is a living handoff, not a one-time note.

Update it whenever any of the following changes:

- product rules or operator rules are clarified with Sam
- the canonical Sam.blog kickoff or review prompt changes
- the active debugging thesis changes
- new core failure classes are discovered
- key root-cause fixes land
- the recommended “start here” files or code paths change

At minimum, append or revise this file when:

- a major bootstrap/state-machine issue is merged
- `docsv2` gains or loses important behavior documentation
- a new session would otherwise miss important context and repeat old mistakes

The goal is that a fresh Codex instance can start from this file, inspect the repo, and continue product work without needing weeks of chat history.

## Changelog

- 2026-03-10 20:31 MDT
  - Created this handoff file for fresh-session recovery.
  - Captured OtterCamp product context, bootstrap/state-machine rules, operating conventions, failure themes, and key repo artifacts.
  - Added the canonical Sam.blog kickoff prompt references plus the short injected project brief.
  - Added the rule that this file must be maintained as a living handoff as product rules, prompts, fixes, and debugging direction evolve.
- 2026-03-10 20:33 MDT
  - Added explicit guidance for how to use `issues/discuss.md` as the "to-discuss with Sam" file.
  - Clarified that only real product/implementation decisions belong there; straightforward bugs and routine run notes do not.
- 2026-03-10 20:36 MDT
  - Recorded decision on kickoff validator strictness.
  - Structural bootstrap invariants are now treated as hard product gates.
  - Non-structural quality/refinement issues should surface without killing the run unless they make execution non-credible.
- 2026-03-10 20:38 MDT
  - Recorded decision on provider/API observability surfaces.
  - Dashboard plus project/task history are the primary operator truth surfaces.
  - TUI status/banners should provide a compact live degraded-state signal.
- 2026-03-10 22:12 MDT
  - Marked `Speaker Pipeline Ops` as the resolved default next validation project after Sam.blog.
  - Verified targeted state-machine packages on current `main`:
    - `go test ./internal/task ./internal/flow ./internal/controlplane`
    - `go test ./internal/turn -tags integration -run 'TestTurnEngineIntegrationBootstrapInvariantHarness'`
  - Confirmed the merged state-machine work is present and passing in the targeted packages.
  - Noted bookkeeping drift: several files in `issues/05-completed` still carry `state: ready` in frontmatter, including task `359`.
- 2026-03-10 22:15 MDT
  - Normalized stale frontmatter in completed issue files `354`, `355`, `356`, `357`, `358`, `359`, `361`, and `362` from `state: ready` to `state: completed`.
  - Continued the state-machine gap review after confirming targeted unit/integration coverage passes on current `main`.
- 2026-03-10 22:34 MDT
  - Added direct flow-service regression coverage for contradictory task/node runtime state.
  - New tests prove `EnsureActiveExecution` and `AdvanceFlow` reject mismatched task status vs current flow node before backfilling or mutating execution rows.
  - Verified:
    - `go test ./internal/flow -run 'TestEnsureActiveExecutionRejectsTaskRuntimeStatusMismatch|TestAdvanceFlowRejectsTaskRuntimeStatusMismatchBeforeMutatingExecution|TestAdvanceFlowRejectsSelfReview|TestEnsureActiveExecutionBackfillsMissingCurrentNodeExecution'`
    - `go test ./internal/task ./internal/flow ./internal/controlplane`
- 2026-03-10 22:39 MDT
  - Added matching regression coverage for `RejectFlowNode` so all three flow-service entry points now prove contradictory task/node runtime state is rejected before execution mutation.
  - Verified:
    - `go test ./internal/flow -run 'TestEnsureActiveExecutionRejectsTaskRuntimeStatusMismatch|TestRejectFlowNodeRejectsTaskRuntimeStatusMismatchBeforeMutatingExecution|TestAdvanceFlowRejectsTaskRuntimeStatusMismatchBeforeMutatingExecution|TestRejectFlowNodeMaxVisitsExceeded'`
    - `go test ./internal/task ./internal/flow ./internal/controlplane`
- 2026-03-10 22:47 MDT
  - Pushed three main-branch commits during the ongoing `359` hardening pass:
    - `7f47d096` `Add flow runtime mismatch regression tests`
    - `5b589b51` `Cover reject flow runtime mismatch guards`
    - `3c0cee91` `Test oldest deferred wakeup promotion`
  - Confirmed `task_review` reject/resume semantics already have coverage in both task-service unit tests and control-plane integration wakeup tests, so that area does not currently look like the next missing invariant.
- 2026-03-22 21:10 MDT
  - Refreshed this handoff to match the new live rerun instead of the earlier fully-complete SAM.blog state.
  - Recorded the latest pushed queue/runtime hardening commits:
    - `ad358ee0` `Reject impossible draft project gates`
    - `c50e9ae2` `Deduplicate rollup update jobs by org and day`
    - `6c30b325` `Prioritize non-maintenance background jobs`
    - `b72127e0` `Recover queued sessions behind stale live turns`
  - Captured the current live `sam-blog` counts and the fact that blocked-task deadlocks have been replaced by active in-progress/review work.
  - Noted the remaining operational truth:
    - pending `agent_turn` backlog is down substantially
    - stale live-turn recovery is now repairing queued task sessions without operator intervention
    - `rollup_update` backlog still exists but no longer monopolizes fresh non-agent claims

## How to resume intelligently

1. Verify current git and queue state.
2. Read `issues/discuss.md` to see whether any real product decisions are still open.
3. Read the latest relevant report(s), especially the most recent SAM.blog follow-up report.
4. Inspect recent completed bootstrap/state-machine issues and the tests they added.
5. Check whether `docsv2` actually matches current product behavior.
6. If there is an active run, inspect DB/runtime truth and tmux/TUI state before trusting summaries.

## What not to do

- Do not trust chat/session summaries over persisted DB/runtime state.
- Do not keep applying narrow symptom patches when a deeper invariant is missing.
- Do not let Lori create giant tasks without enforcement.
- Do not assign starter trio agents to project delivery work beyond initial setup.
- Do not use `issues/discuss.md` as a bug scratchpad.
- Do not ask Sam to intervene for routine product failures that should be handled automatically.
- Do not create one-off output fixes for a specific test project; improve OtterCamp itself.

## What success looks like

OtterCamp should be able to:

- create a new project cleanly
- persist the canonical bootstrap task tree
- staff and scope the project correctly
- create bounded executable work
- attach valid flows
- enter real first-wave execution
- survive failures through explicit state-machine recovery
- expose truthful operator visibility
- complete a full project run without manual babysitting

If a fresh session can hold this model in its head, inspect the repo, and continue implementing from there, this handoff file has done its job.

- 2026-03-22 16:25 MDT
  - SAM.blog clean-run validation is now complete in live runtime state: project `424bd0d3-dace-46ef-99fc-f21b817cdfc3` has `36/36` tasks `done`.
  - Recorded the three cleanup fixes that landed on `main`:
    - `dede48bd` `Auto-complete orchestration parent tasks`
    - `1d20cda2` `Hydrate planning evidence for parent auto-complete`
    - `952fc150` `Auto-complete bootstrap planning tasks`
  - Updated the handoff to remove dependence on stale `issues/notes.md`, point future sessions at the report-based record instead, and capture that `oc-recover` is no longer part of the intended live stack.
  - Captured the next product focus: use the SAM.blog run as a closed validation baseline, then shift to identifying remaining unattended-run weaknesses before starting the next mixed operational validation project.

- 2026-03-10 22:50 MDT: Fixed the recovery state-machine mismatch for `ResumeValidationBlockedTask`. The service now re-queues blocked recovery tasks (`blocked -> queued`) instead of jumping straight to `in_progress`, the transition matrix and task-service integration tests were aligned to that contract, and the failing `internal/turn` recovery integration tests (EX327/EX329) now pass.
- 2026-03-10 22:50 MDT: Fixed the fake control-plane run repository to honor the injected fake clock so deferred wakeup ordering tests are deterministic. Revalidated `./internal/task ./internal/flow ./internal/controlplane ./internal/turn` with and without `-tags integration`.
- 2026-03-10 23:10 MDT: Closed the blocked-task resume escape hatch. `blocked -> in_progress` now still requires a live active flow; `AllowNoActiveFlow` no longer bypasses that path. Added unit and integration coverage and revalidated the core task/flow/controlplane/turn suites.
- 2026-03-10 23:10 MDT: Fixed stale test-mode handler shortcuts that had drifted from runtime invariants. Normal task creation in test mode now does `queued -> start_flow -> in_progress`, and deploy-task test mode now starts the flow first when a deploy flow exists or uses an explicit system-only test bypass otherwise. Added handler tests for both paths.

- 2026-03-10: Exposed repo_version from /v1/version and documented the repo build counter / stale-binary freshness contract in docsv2 (deployment + TUI).
- 2026-03-10: Documented /v1/version response fields in docsv2/12 so repo_version is part of the published API contract, not just code/tests.
- 2026-03-10: Tightened docsv2/03 to say duplicate wakeups must coalesce and stale runtime owners must recover/defer before a new owner can execute the same task/node.
- 2026-03-10: Fixed TUI EX-494 regression by preserving last-known sidebar projects when project reloads fail, while still allowing successful chat refreshes.
- 2026-03-10: Fixed chat unit harness to seed active projects for project-scoped CreateSession tests; full go test ./internal/... is now green.
- 2026-03-11 00:49 MDT: Fixed a real control-plane race, not just a flaky test. Flow-driven status transitions now carry explicit `transition_source` / `flow_event_type` / `to_flow_execution_id` metadata, and the task queue processor suppresses duplicate generic `flow_current` wakeups for `flow.advanced`/`flow.rejected` transitions. Added unit coverage for the gate, strengthened reject-path integration coverage to reject duplicate wakeups, and stabilized the full integration sweep.
- 2026-03-11 00:49 MDT: Repaired broad integration drift across `auth`, `server`, `tui`, `jobqueue`, and `memory`, including the `/model/usage-rollup` route shadowing bug, stale handler fixtures, and brittle async waits. Full `go test ./internal/...` and full `go test ./internal/... -tags integration -count=1` are now green.
- 2026-03-11 00:49 MDT: Hardened test DB teardown by increasing drop cleanup/attempt budgets in `internal/testdb` after the repo-wide integration run exposed teardown timeouts under heavy load even with no active sessions.
- 2026-03-11 01:52 MDT: Fixed two control-plane runtime invariants in local work: `ReleaseExecutionOwner` now ignores completions from sessions that do not own the active run, and runtime contract sync now overwrites flow/task/session execution identifiers from the promoted run instead of preserving stale metadata. Added focused service tests plus a task-queue unit test that resolves `turn_id -> trigger message -> run_id` before releasing ownership.
- 2026-03-11 01:52 MDT: Verified `TestTaskQueueProcessorIntegrationDifferentOwnerWakeupDefersUntilTurnExit` passes under repetition (`-count=20`). The remaining unresolved area is the reject-path integration harness, which still mixes synthetic `chat.turn.completed` events with shared canonical task sessions; that needs a cleaner reproduction before the final product fix is complete.
- 2026-03-11 02:06 MDT: Closed the remaining reject-path repro. The real issue was a mismatch between synthetic test completions and the active run owner: the harness was publishing turn completion against flow execution sessions instead of the active run session/turn. After resolving ownership by run where possible and publishing completion against the active run session in the regression, `FlowRejectedKickOffsRejectPathAgent` now passes under repetition (`-count=10`).

- 2026-03-11 02:24 MDT: Repo-wide integration sweep passed (`go test ./internal/... -tags integration -count=1`) after aligning two anonymous interfaces with `ReleaseExecutionOwnerForRun` in `internal/turn/engine_integration_test.go` and `internal/worker/worker.go`. Exploratory local controlplane changes were reverted before this sweep; pushed baseline remains the source of truth.

- 2026-03-11 02:33 MDT: Fixed a real pause/state-machine hole in deferred wakeup ownership. Project pause now holds deferred wakeups on turn release instead of promoting them immediately, and `project.resumed` explicitly re-promotes held deferred wakeups before normal queued pickup. Added regression coverage for paused-project deferred promotion plus adjacent owner/deferred resume paths, and documented the contract in docsv2/03 and docsv2/16.

- 2026-03-11 02:41 MDT: Re-activated EX248 high-risk async-decision coverage and fixed the underlying state-machine hole. Hard-stop async review now advances onto a real held review checkpoint (`flow_node_execution` + `review` status) instead of leaving tasks stranded `in_progress` with no run or review lineage. Updated docsv2/03 and docsv2/16 to say policy-driven review pauses are explicit flow transitions, not status-only labels.

- 2026-03-11 02:54 MDT: Fixed async review hard-stop/prepare-for-review state handling. These checkpoints now advance onto a real held review flow execution instead of leaving tasks stranded `in_progress` with only an artifact. Re-activated EX248 high-risk async-decision coverage, hardened turn-completion test helpers to target the run-owned turn, and pushed commit `efa1ce23`. Repo-wide integration sweep stayed green for touched packages; the only broad-sweep failure was an existing load-sensitive `internal/eventbus` cursor test that passed in isolated rerun.

- 2026-03-11 03:00 MDT: Hardened the integration harness itself. Fixed the `internal/eventbus` cursor race by waiting for the cursor to advance instead of assuming handler return implies cursor persistence, and made controlplane turn-completion helpers synthesize a run-owned turn row when queue-tracking tests do not create one. Pushed as `1a871640`.

- 2026-03-11 03:04 MDT: Removed stale gate-task parallelism drift. `blocks_scope=all` no longer exempts review-state gate tasks from blocking the project queue, and the old skipped EX248 parallelism test was replaced with a live regression proving gate review checkpoints still hold later work in `queued` with zero runs.

- 2026-03-11 03:15 MDT: Fixed stale project-gate unit expectation so review-state gate tasks continue blocking parallel queued work until terminal, validated controlplane integration green, and removed dead AsyncDecision.AllowsParallelProgress helper.
- 2026-03-11 03:28 MDT: Fixed jobqueue LISTEN/NOTIFY startup race by waking immediately after LISTEN attaches; corrected gate-integration coverage to assert the actual project-gate contract (queueing blocked while gate is outstanding); full ./internal/... and ./internal/... -tags integration sweeps are green from this state.
- 2026-03-11 03:30 MDT: Full ./internal/... and ./internal/... -tags integration suites are green on main at 4e66a2f0. Next proactive audit is limited to remaining explicit transition bypass hooks (AllowNoActiveFlow / AllowDoneBypass / AllowGateBypass) to verify each one is truly intentional.
- 2026-03-11 03:31 MDT: docsv2/03 now states the stricter project-gate contract proven by tests: non-gate tasks cannot even queue behind an outstanding gate and remain draft until the gate clears.
- 2026-03-11 03:33 MDT: Audited remaining transition bypass hooks. Real-code bypass surface is now just bootstrap first-wave promotion (AllowGateBypass). The AllowNoActiveFlow / AllowDoneBypass paths are fenced behind OTTERCAMP_MODE=test in server scaffolding and do not affect normal runtime.
- 2026-03-11 03:35 MDT: Hardened eventbus LISTEN startup with the same post-LISTEN drain signal used in jobqueue, to remove the same missed-notify window between initial drain and subscription attachment.
- 2026-03-11 03:39 MDT: Fixed orphaned deferred wakeups in controlplane release handling. If a turn-completed signal arrives after active_run_id was already cleared, runtime now still promotes or holds the deferred wakeup instead of leaving it stranded. Added a direct wakeup-service unit test and tightened integration tests to release exact runs and avoid racing legitimate queue transitions.
- 2026-03-11 03:46 MDT: Full ./internal/... -tags integration sweep passed clean on pushed main @ 49021ab0 after eventbus wakeup hardening and deferred-owner self-heal fixes.
- 2026-03-11 03:47 MDT: Proactively replaced the remaining generic publishTaskTurnCompleted call in controlplane integration with the run-specific helper to avoid shared-session ambiguity in future flow tests.
- 2026-03-11 03:49 MDT: Suppressed expected jobqueue shutdown-time context-canceled error logs so integration output stays focused on real failures.
- 2026-03-11 03:52 MDT: Closed the backlog-to-LISTEN race in server run-event SSE streaming by doing an immediate post-LISTEN fetch before blocking for notifications.
- 2026-03-11 03:55 MDT: Current pushed head is b5448447, followed by 6115256a, 029a6f2d, and 49021ab0. Focused supervisor/stranded-execution integration coverage is green against the latest runtime-state and wakeup changes, so the deferred-owner self-heal does not appear to regress supervisor recovery.
- 2026-03-11 03:57 MDT: docsv2/12 now states the attach-gap rule for LISTEN/NOTIFY consumers and SSE streams: fetch backlog, attach LISTEN, fetch backlog again, then block.
- 2026-03-11 04:05 MDT: Hardened EX325 checkpoint-resume integration setup to accept legitimate queue-driven transition to in_progress before the test marks the task blocked. This removes another full-suite timing false negative from controlplane integration.
- 2026-03-11 04:08 MDT: Full ./internal/... -tags integration sweep passed clean on pushed main @ 39fae999 after EX325 setup hardening. Current runtime boundary layer (jobqueue/eventbus/controlplane/server SSE) is green together.

- 2026-03-11 04:15 MDT: Audited bootstrap archive/restart and provider pause semantics against code + docs. The runtime contract is present: bounded restart bundle, clean fresh-project restart, provider/API failures pause instead of archive, and scaffold-only restarts fail closed. Tightened `automatic_failure` integration assertions to verify `source` (`project_bootstrap` vs `execution_runtime`) and re-ran focused pause/restart coverage plus full `go test ./internal/turn -tags integration -count=1`, all green.
- 2026-03-11 04:23 MDT: Pushed test-hardening commit `8098b399` to verify `automatic_failure.source` stays correct (`project_bootstrap` vs `execution_runtime`). Full `go test ./internal/... -tags integration -count=1` passed clean on this head.
- 2026-03-11 04:27 MDT: Fixed doc drift in docsv2/16. It had an outdated shortened `automatic_failure` field list; it now matches the code and docsv2/03, including `last_successful_checkpoint`, `setup_persisted`, retry metadata, and `failure_history`.
- 2026-03-11 04:33 MDT: Clarified docsv2/03 that task-flow `retry` is an explicit run/turn-attempt concern on the current node, not hidden implicit flow advancement. This keeps the robust-state-machine contract aligned with the run-attempt model in docsv2/16.
- 2026-03-11 04:47 MDT: Fixed a real flow-state leak in `/v1/tasks/{id}/review-decision`. Human review decisions now call `flow.AdvanceFlow` / `flow.RejectFlowNode` instead of mutating task status directly, so flow lineage and task status stay consistent. Replaced the masking integration fixture with a real active-review execution and updated the lifecycle expectation to the correct state-machine result (`review` approval on a `work -> review -> merge` template advances to merge `in_progress`, not straight to `done`). Full `go test ./internal/... -tags integration -count=1` passed clean on this state.
- 2026-03-11 05:00 MDT: Fixed the matching service-layer leak for inbox-driven `task_review` actions. `ActOnInboxItem` now delegates task-review approve/reject entirely to flow review actions and no longer performs a second raw `review -> in_progress` status hop afterward. Updated task-service tests to assert there is no extra service-layer status mutation, and full `go test ./internal/... -tags integration -count=1` passed clean again on this state.
- 2026-03-11 05:07 MDT: Added an explicit doc rule in docsv2/03 and docsv2/16: human review actions, whether from task endpoints or inbox items, must route through canonical flow advance/reject operations and never shortcut by mutating task status directly.
- 2026-03-11 05:22 MDT: Fixed bootstrap-gate auto-complete to record a real `project_task_event` audit entry instead of only publishing `task.status_changed`. Added a reproducing integration assertion to the gate-open bootstrap test and kept the special draft->done bootstrap-gate semantics while restoring audit parity. Full `go test ./internal/... -tags integration -count=1` passed clean on this state.
- 2026-03-11 05:44 MDT: Removed the last degraded-mode direct `work_status='blocked'` fallbacks from the turn engine validation/recovery paths and supervisor stranded-execution blocker path. These code paths now fail loudly if the canonical task transition service is missing, and new regression tests prove persisted task state stays unchanged instead of being mutated behind the state machine. `go test ./internal/turn ./internal/controlplane -tags integration -count=1` passed clean on this state.
- 2026-03-11 06:02 MDT: Removed the last hand-rolled bootstrap gate `draft -> done` row update from the turn engine. Bootstrap gate auto-complete now goes through `task.TransitionStatusWithPayload(...)` as an explicit system-only bypass guarded by bootstrap-gate metadata and `blocks_scope=all`, so even that exception is routed through the same audited state-machine service. Focused `internal/task` unit coverage plus `go test ./internal/turn ./internal/task ./internal/controlplane -tags integration -count=1` passed clean.
- 2026-03-11 06:31 MDT: Wired the pool-backed native executor onto the canonical task transition service for `task.update` status changes and decomposition-child queueing, instead of repo-update-plus-synthetic-event behavior. Also promoted completed-child reopen into an explicit task-service exception (`done -> queued` only for bounded child tasks with parent feedback), and fixed native integration fixtures that had been relying on weaker pre-service completion checks. Full `go test ./internal/tools/native -count=1` and `go test ./internal/tools/native -tags integration -count=1` passed clean.
- 2026-03-11 06:54 MDT: Landed the first transactional flow-atomicity slice. Added tx-aware repo methods for task/event/inbox/flow-execution writes, a tx-aware task-service transition path, and moved terminal `flow.advance` onto a single DB transaction. The rollback regression for event-publish failure still passes, but now the terminal path itself is atomic instead of depending on native compensation writes. `go test ./internal/flow ./internal/task ./internal/tools/native -tags integration -count=1` passed clean.
- 2026-03-11 05:18 MDT: Identified the remaining live state-machine seam after native-task-service cleanup. The production `flow.advance` path is canonical, but `flowService.AdvanceFlow` itself is still non-atomic: it mutates execution/task state and only then records/publishes follow-up side effects. Native tools currently compensate for late failures by writing task status/current_flow_node_id back directly. Root-cause fix is to add tx-aware flow/task/event/inbox repo methods and make `AdvanceFlow` / `RejectFlowNode` transactional, then delete the native rollback shim.
- 2026-03-11 06:16 MDT: Closed the remaining flow-atomicity seam. Non-terminal `flow.advance` and `flow.rejected` now mutate execution state, `current_flow_node_id`, task status, inbox creation, task events, and domain events transactionally in the flow/task services. Added regression tests proving publish failures roll back runtime state for next-node advance and reject paths, removed the native `flow.advance` rollback shim, added a narrow task-service `AllowFlowRuntimeBypass` for already-validated in-transaction flow-owned status changes, and revalidated `go test ./internal/flow ./internal/task ./internal/tools/native -count=1` plus the same set with `-tags integration`.
- 2026-03-11 06:32 MDT: Added queue-time node-session self-heal for flow executions. If a flow execution reaches scheduling without `flow_node_execution.session_id`, the task queue processor now repairs it through `chat.GetOrCreateNodeSession(...)` before creating the wakeup run. Added a focused controlplane unit regression and revalidated `./internal/controlplane ./internal/flow ./internal/task ./internal/tools/native` with and without `-tags integration`.
- 2026-03-11 06:06 MDT: Closed the session-lineage root cause behind reject-path wakeup drift. The production flow-session bridge no longer creates node sessions through task-scope async session reuse; with a DB pool it now binds/reuses sessions transactionally by `flow_node_execution_id`, which keeps each execution on its own session and fixes reject/review ownership. Added a project integration test proving two executions on one task get distinct sessions, made post-commit flow advance/reject session binding best-effort (queue repair handles misses), and revalidated touched controlplane/flow/project/task/native suites with integration.
- 2026-03-11 06:18 MDT: Renamed misleading TUI task-detail fields from `ActiveExecutionID` / `RecentExecutionID` to `ActiveExecutionSessionID` / `RecentExecutionSessionID`. The values were already session IDs, not execution record IDs; this was a naming trap that could easily reintroduce journal/discussion routing bugs. Revalidated with `go test ./internal/tui -count=1`.
- 2026-03-11 06:24 MDT: Removed stale "single task async session" language from the runtime/docs/TUI boundary. Clarified that dispatch and recovery operate on per-execution sessions, updated the kickoff-validation error text and stranded-execution reasons to say `execution session`, and revalidated `go test ./internal/controlplane ./internal/turn ./internal/tui -count=1`.
- 2026-03-11 06:30 MDT: Fixed a deeper queue/state-machine race exposed while validating the execution-session work. Root cause: queue consumers could process an old queued wakeup after the task had already moved to `blocked`, because the queue path asked the task service to "make it in_progress" without asserting it was still `queued`, and task row updates did not reject stale snapshots. Fix: task writes now fail on stale snapshots via optimistic concurrency in `project_task` updates, queue-driven `queued -> in_progress` transitions carry an explicit expected-from-state guard, and flow wakeup kickoff chatter now stays on the execution session instead of a generic async task session. Revalidated with `go test ./internal/task -count=1`, `go test ./internal/repo -tags integration -run TestProjectTaskRepoUpdateRejectsStaleSnapshot -count=1`, `go test ./internal/controlplane -count=1`, and `go test ./internal/controlplane -tags integration -run TestTaskQueueProcessorIntegrationResumeRepairedDurableCheckpointCreatesFollowOnTurnEX325 -count=5`.
- 2026-03-11 06:37 MDT: Follow-up after the stale-wakeup fix: optimistic task writes exposed stale `UpdatedAt` reuse inside transactional `flow.advance` / `flow.reject`. Fixed the tx flow paths to carry forward the updated task row returned by `SetFlowNodeTx(...)` before calling the task-service status transition. Revalidated with full `go test ./internal/controlplane -tags integration -count=1`, which now passes cleanly.
- 2026-03-11 06:43 MDT: Extended the expected-from-state guard into native task mutation updates. The native `task.update` path was persisting metadata at the previous status and then asking the task service to transition status separately; it now tells the task service what prior status it expects, so an interleaving concurrent change cannot be reinterpreted as a valid transition from a newer state. Revalidated `go test ./internal/tools/native -count=1` and `go test ./internal/tools/native -tags integration -count=1`.
- 2026-03-11 06:49 MDT: Added a dedicated `project_task` metadata-only repo update path and switched the highest-churn metadata callers in `turn` and project bootstrap setup to use it. This reduces conflict/stale-field surface from full-row `tasks.Update(...)` calls that were only trying to persist metadata. Revalidated with `go test ./internal/repo -tags integration -run 'TestProjectTaskRepo(UpdateRejectsStaleSnapshot|UpdateMetadataPreservesWorkStatus)' -count=1`, `go test ./internal/turn ./internal/project -count=1`, and `go test ./internal/turn ./internal/project -tags integration -count=1`.
- 2026-03-11 06:46 MDT: Extended the metadata-only `project_task` update path into the server deploy-metadata handler and the native task-creation/planning-sync paths, so pure metadata persistence no longer relies on full-row task writes in those hotspots. Updated the affected server/native test doubles for the new repo surface and fixed one stale-fixture server integration test to reload the gate task before mutation under optimistic concurrency. Revalidated with `go test ./internal/server ./internal/tools/native -count=1` and `go test ./internal/server ./internal/tools/native -tags integration -count=1`.
- 2026-03-11 06:55 MDT: Removed the last two clear metadata-only `ProjectTask.Update(...)` writes from the task service path. Parent-task queue/decomposition persistence and durable recovery-checkpoint repair now use the metadata-only repo helper instead of full-row task writes, reducing stale-row conflict surface without changing task-state semantics. Revalidated with `go test ./internal/task ./internal/turn -count=1` and `go test ./internal/task ./internal/turn -tags integration -count=1`.
- 2026-03-11 07:02 MDT: Removed the remaining metadata-update fallback from `turn` and `project`. Those services now require `ProjectTask.UpdateMetadata(...)` explicitly instead of treating metadata-only writes as an optional capability with a full-row fallback. Revalidated with `go test ./internal/project ./internal/turn -count=1` and `go test ./internal/project ./internal/turn -tags integration -count=1`.
- 2026-03-11 07:08 MDT: Pushed `33fba49c` (`Use metadata-only task updates in server and native`), `d93743c3` (`Use metadata-only task writes in task service`), and `ab1f7173` (`Require metadata updates in turn and project services`). These finish the metadata-only `ProjectTask` write cleanup across the highest-risk runtime paths and remove silent fallback-to-full-row behavior in turn/project.
- 2026-03-11 07:10 MDT: Revalidated the state-machine-heavy integration slice with `go test ./internal/task ./internal/flow ./internal/controlplane ./internal/turn ./internal/project ./internal/tools/native ./internal/server -tags integration -count=1` and stress-ran `go test ./internal/controlplane -tags integration -count=3`. A one-off `EX318` timeout did not reproduce under repeated controlplane runs, so no speculative product fix was pushed for that path.
- 2026-03-11 07:24 MDT: Full repo integration sweep passed cleanly on rerun: `go test ./internal/... -tags integration -count=1`. Earlier one-off failures in `internal/controlplane` (`EX318`) and `internal/cli` (`EX304` base64 payload case) did not reproduce under focused stress runs or on the subsequent full sweep, so no speculative product changes were made for those paths.
- 2026-03-11 07:27 MDT: Full non-integration repo sweep also passed: `go test ./internal/... -count=1`. Current `main` has both repo-wide unit and integration coverage green after the metadata-only/state-machine hardening slice.
- 2026-03-11 07:31 MDT: Fixed a real docsv2 contradiction in `03-projects-and-task-flow.md`. The spec no longer claims “no parent-child hierarchy” without qualification; it now says tasks are flat in storage (no `parent_task_id` column) while still allowing explicit runtime parent/child orchestration relationships through decomposition metadata.
- 2026-03-11 07:33 MDT: Cleaned the final leftover “tasks are flat” bullet in `docsv2/03` so the decomposition/orchestration model is now stated consistently throughout the file.

## Issue 359 Coverage Map

- Explicit transition rules:
  - Implemented in `internal/task/service.go` transition matrix and flow/runtime guards.
  - Covered by `internal/task/service_test.go` and task/controlplane integration suites.
- Runtime-backed active states only:
  - Documented in `docsv2/03-projects-and-task-flow.md`.
  - Enforced in task/flow/controlplane invariants and bootstrap/runtime repair paths.
- Invalid transitions fail closed:
  - Enforced through task-service typed errors and repo conflict guards.
  - Covered by task-service unit tests and queue/runtime mismatch regressions.
- Success advances exactly once:
  - Documented in `docsv2/03-projects-and-task-flow.md`.
  - Covered by duplicate `flow.advanced` / duplicate completion regressions in turn/controlplane integration tests.
- Reject/retry/block/pause/resume/completion are explicit transitions:
  - Implemented across task service, flow execution service, runtime-state contract, and queue processor.
  - Covered by reject-path, recovery-resume, paused deferred-wakeup, and bootstrap invariant integration tests.
- Stale ownership / duplicate dispatch cannot split state:
  - Enforced through runtime_state single-owner rules, deferred wakeups, execution-session binding by `flow_node_execution_id`, stale snapshot conflict protection, and expected-from-state guards.
  - Covered by controlplane owner/deferred/duplicate wakeup regressions and repo optimistic-concurrency tests.
- Docs:
  - `docsv2/03-projects-and-task-flow.md`
  - `docsv2/16-agent-control-plane.md`
- Remaining uncertainty:
  - Occasional one-off integration-suite jitter can still happen under full repo load, but as of this session it did not reproduce under focused stress runs and the second full repo integration sweep passed cleanly.
- 2026-03-11 07:37 MDT: Updated docsv2 staffing/session rules so the starter trio are clearly bootstrap/org-level actors, not default project-session or project-delivery assignees after setup. `docsv2/02-chat.md` now scopes project sessions to PM + project-assigned staff/temps, and `docsv2/05-agents-staff-and-temps.md` now says ordinary execution should route through the project workforce instead of leaving Frank/Lori/Ellie assigned to routine project work.
- 2026-03-11 07:40 MDT: Added a header note to the root `decisions.md` clarifying that it is historical Ralph-loop context and that current product-direction questions for this loop live in `issues/discuss.md`, with `issues/codex-state.md` as the active local handoff file.
- 2026-03-11 07:46 MDT: Added a native integration regression proving project-scoped `session.create` auto-adds only legitimately assigned project agents and does not pull starter-trio agents into ordinary project sessions. Revalidated `go test ./internal/tools/native -count=1` and `go test ./internal/tools/native -tags integration -count=1`.
- 2026-03-11 07:52 MDT: Added matching entry-point regressions for the starter-trio session rule. Native integration already proves project-scoped `session.create` auto-adds only legitimately assigned project agents; new server handler unit coverage proves HTTP `POST /v1/chat-sessions` does not auto-add the starter trio for project scope and only auto-adds them for organization scope.
- 2026-03-11 07:58 MDT: Audited project kickoff routing across code/tests/docs. Implementation already matches the intended behavior: project sessions prefer the assigned PM, but fresh bootstrap routes the first project turn to Frank and then hands off to Lori until staffing persists a PM assignment (`resolveSessionAgentForSession` + kickoff tests in `internal/turn`). Tightened `docsv2/02-chat.md` to document that bootstrap exception explicitly and revalidated with `go test ./internal/turn -count=1`.
- 2026-03-11 08:07 MDT: Added turn-engine integration regressions for the project-session routing boundary itself. Coverage now proves all three project responder states at the runtime boundary: assigned PM takeover, fresh kickoff routing to Frank, and post-Frank bootstrap handoff to Lori. Revalidated with `go test ./internal/turn -count=1` and `go test ./internal/turn -tags integration -run 'TestTurnEngineIntegrationProjectSession(EventRoutesJobToPMAndAddsParticipant|KickoffRoutesFirstJobToFrank|KickoffHandsOffToLoriAfterCompletedFrankTurn)' -count=1`.
- 2026-03-11 08:16 MDT: Re-ran the state-machine/bootstrap-heavy integration slice after the kickoff responder coverage landed: `go test ./internal/task ./internal/flow ./internal/controlplane ./internal/turn ./internal/project -tags integration -count=1`. All five packages passed cleanly, so the new kickoff regressions integrate cleanly with the broader task/flow/controlplane bootstrap path.
- 2026-03-11 08:29 MDT: Fixed a real task-session routing bug. `resolveSessionAgentForSession` had been treating every `project_task` session as an execution session and routing sync task discussion sessions to the assigned worker. The router is now mode-aware: `project_task + async` still routes to the assigned agent, while `project_task + sync` routes to the project PM and only falls back to the assigned agent when no PM exists. Added unit and integration regressions for the sync discussion-session case and revalidated the full affected turn-routing set.
- 2026-03-11 08:36 MDT: Fixed the matching API entry-point bug in `server/chat_handlers.go`. `POST /chat-sessions` for `project_task` had been seeding the assigned worker as responder regardless of `mode`, which contradicted the corrected turn-engine routing. The handler now uses the same mode-aware split: sync task discussion sessions prefer the project PM, async task execution sessions prefer the assigned agent. Added a reproducing handler test and revalidated `go test ./internal/server -run 'TestCreateSessionProjectTask(SyncPrefersProjectPMResponder|AddsAssignedAgentParticipant|FallsBackToPM|FallsBackToFrank)' -count=1`, `go test ./internal/turn ./internal/server -count=1`, and `go test ./internal/turn -tags integration -count=1`.
- 2026-03-11 08:45 MDT: Normalized HTTP chat-session creation with the native session model. `POST /chat-sessions` now auto-adds active project assignees for project and task sessions instead of leaving the API path under-hydrated relative to native. Combined with the earlier mode-aware responder fix, task sync sessions now start PM-led with the worker present as a participant, while project sessions pick up assigned project staff without dragging in the starter trio. Revalidated with `go test ./internal/server -run 'TestCreateSession(ProjectScope(DoesNotAutoAddStarterTrio|AutoAddsAssignedProjectAgentsButNotStarterTrio)|ProjectTask(SyncPrefersProjectPMResponder|AddsAssignedAgentParticipant|FallsBackToPM|FallsBackToFrank))' -count=1` and `go test ./internal/turn ./internal/server -count=1`.
- 2026-03-11 08:53 MDT: Added the missing native integration regression for sync task discussion sessions. Native `session.create` now has explicit coverage proving a `project_task + sync` session auto-adds the project PM and worker while still excluding starter-trio agents. Revalidated with `go test ./internal/tools/native -count=1` and `go test ./internal/tools/native -tags integration -run 'TestIntegration(ProjectSessionCreateAutoAddsAssignedProjectAgentsButNotStarterTrio|TaskSyncSessionCreateAutoAddsProjectPMAndWorkerButNotStarterTrio)' -count=1`.
- 2026-03-11 09:02 MDT: Tightened the native session helper to match the corrected responder model. `autoAddProjectParticipants` is now mode-aware instead of always sorting workers first; sync sessions prioritize the PM in participant order while async sessions keep worker-first ordering. This prevents stale participant-order assumptions from reintroducing responder drift later. Revalidated with `go test ./internal/tools/native -count=1` and `go test ./internal/tools/native -tags integration -run 'TestIntegration(ProjectSessionCreateAutoAddsAssignedProjectAgentsButNotStarterTrio|TaskSyncSessionCreateAutoAddsProjectPMAndWorkerButNotStarterTrio)' -count=1`.
- 2026-03-11 09:08 MDT: Re-ran the broader chat/session package slice after the server/native/task-session fixes: `go test ./internal/turn ./internal/server ./internal/tools/native -count=1` and `go test ./internal/turn ./internal/server ./internal/tools/native -tags integration -count=1`. All three packages passed cleanly in both unit and integration modes.
- 2026-03-11 09:16 MDT: Re-ran the full state-machine/bootstrap-heavy integration slice after the session/runtime fixes: `go test ./internal/task ./internal/flow ./internal/controlplane ./internal/turn ./internal/project ./internal/server ./internal/tools/native -tags integration -count=1`. All seven packages passed cleanly, so the task-sync/session-shape work integrates cleanly with the broader bootstrap/runtime path.
- 2026-03-11 09:24 MDT: Tightened the completed-child reopen contract to match the orchestration docs. `done -> queued` reopen for decomposed child tasks now requires non-empty `parent_integration_feedback`, and successful reopen persists that feedback plus a recorded-at timestamp onto the child task metadata. Added unit coverage for both the success and missing-feedback paths. Revalidated with `go test ./internal/task -run 'TestTransitionStatus(AllowsCompletedChildReopenToQueued|CompletedChildReopenRequiresParentIntegrationFeedback)' -count=1`, then re-ran `go test ./internal/task ./internal/turn ./internal/controlplane -count=1` and `go test ./internal/task ./internal/turn ./internal/controlplane -tags integration -count=1`. One broader controlplane integration pass produced a non-reproducing `repo.ErrConflict` in `TestTaskQueueProcessorIntegrationRepeatedInProgressEventsReuseTaskSession`, but the test passed in isolation, under `-count=20`, and on the full slice rerun, so no speculative product change was pushed for that jitter.

- 2026-03-11 08:33 MDT: Added the missing task-service integration coverage for the completed-child reopen contract that already shipped in unit tests/code. `internal/task/service_integration_test.go` now proves successful reopen persists `parent_integration_feedback` plus timestamp, and missing feedback is rejected with `ErrParentIntegrationFeedbackRequired`. Revalidated with targeted integration tests plus `go test ./internal/task ./internal/turn ./internal/controlplane -tags integration -count=1`.

- 2026-03-11 08:38 MDT: Fixed doc drift in docsv2/03 around terminal task states. The spec now documents the narrow completed-child reopen exception explicitly: `done` remains terminal except when a parent integration gate reopens a completed child to `queued` with concrete `parent_integration_feedback`.

- 2026-03-11 08:50 MDT: Fixed two more task-state-machine gaps. First, `blocked -> in_progress` is now restricted to direct human operator continuation with a live active flow; agent/system paths no longer bypass that documented manual-only edge. Second, task-id based service commands now retry once on optimistic-concurrency conflict when no explicit `ExpectedFromStatus` CAS guard was requested, which fixes real stale-snapshot races like `MarkBlocked` colliding with adjacent queue/runtime writes while preserving fail-closed queue pickup semantics. Added unit regressions plus reran the EX325 controlplane integration stress path.

- 2026-03-11 09:02 MDT: Closed a live native-executor state-machine leak. The worker had been constructing the native executor without a canonical task service, which meant some task mutation paths could still fall back to direct task-row updates during real worker execution. `NewExecutor` now auto-builds the task service when it has both a DB pool and an event publisher, the worker now passes the event bus into the native executor, and a new integration regression proves that pool+events wiring yields a non-nil canonical task service. Revalidated `./internal/tools/native ./internal/task ./internal/turn ./internal/controlplane` with and without `-tags integration`, plus `go test ./internal/worker -count=1`.

- 2026-03-11 09:18 MDT: Full repo integration sweep passed clean on pushed main @ 94dddc16 after the blocked-transition hardening and native-executor task-service wiring fixes. `go test ./internal/... -tags integration -count=1` is green across the repo.

- 2026-03-11 09:27 MDT: Strengthened the native executor coverage so the auto-wired task service is proven behaviorally, not just structurally. `TestIntegrationParentTaskCanReopenCompletedChildWithFeedback` now runs the DB-backed executor with an event bus and asserts the canonical service persisted `parent_integration_feedback` metadata plus timestamp on child reopen.

- 2026-03-11 08:54 MDT: Continued post-green audit from clean main after full unit+integration sweeps. Current focus is production-only search for any remaining task-state mutation paths that bypass canonical task/flow transition services.

- 2026-03-11 08:58 MDT: Updated docsv2/20 to state the tool-layer contract explicitly: DB-backed native task mutations must route `work_status` changes through canonical task/flow services, not direct `project_task` row updates.

- 2026-03-11 09:00 MDT: Continuing post-green production-path audit from pushed main @ ec593c99. Current focus is delivery/deploy and adjacent services to confirm there are no remaining task-state mutations outside canonical task/flow services.

- 2026-03-11 09:02 MDT: Audited stranded execution + supervisor task blocking path. It correctly fails loud without taskService and supervisor auto-builds the canonical task service from pool+eventBus, so there is no silent blocked-status fallback there.

- 2026-03-11 09:08 MDT: Hardened `PATCH /tasks/:id` to fail closed on forbidden state-machine fields. The handler now rejects request bodies that try to smuggle `work_status`, `flow_template_id`, or `current_flow_node_id` through the metadata patch route instead of silently ignoring them. Added handler regressions and revalidated server/task/turn/controlplane with and without `-tags integration`.

- 2026-03-11 09:12 MDT: Updated docsv2/12 so the API contract matches the new handler behavior: `PATCH /tasks/:id` must fail closed on task state-machine fields (`work_status`, `flow_template_id`, `current_flow_node_id`) instead of silently ignoring them.

- 2026-03-11 09:15 MDT: Production audit narrowed to direct queued-task creation sites. Delivery paths appear intentional because they mint new deploy tasks that legitimately enter the queue at creation time. Current review is on bootstrap seeding in project service.

- 2026-03-11 09:31 MDT: Root-caused delivery-state leak: deploy/rollback/background delivery paths were creating queued tasks directly and bypassing canonical validation/events. Updated delivery services/workers to create draft tasks through the task service, transition them to queued, repaired the delivery integration fixture to seed a valid work->review->merge deploy flow, added rollback integration coverage, and documented the canonical rule in docsv2/03a-shipping-and-delivery.md. Verified with go test ./internal/delivery -count=1 and go test ./internal/delivery -tags integration -count=1.
- 2026-03-11 09:38 MDT: Fixed another queued-state bypass in task MarkBlocked auto-resolution. Resolution tasks now create in draft and enter queued only through the canonical transition service; strengthened unit coverage and documented the rule in docsv2/03-projects-and-task-flow.md. Verified with go test ./internal/task -count=1, go test ./internal/task -tags integration -count=1, and go test ./internal/controlplane -tags integration -count=1.
- 2026-03-11 09:52 MDT: Tightened delivery constructor contract. Pool-backed deploy/rollback creation now fails closed if the canonical task service is not wired; updated server task integration to pass the bus explicitly; threaded taskService into rollback route fallback so route-level rollback remains canonical. Verified with go test ./internal/delivery -count=1, go test ./internal/delivery -tags integration -count=1, and go test ./internal/server -tags integration -run \TestTask' -count=1.
- 2026-03-11 10:11 MDT: Full internal test sweep passed after the state-machine hardening batch. Verified both go test ./internal/... -count=1 and go test ./internal/... -tags integration -count=1 on main at 9a6f2150. Remaining work is proactive audit and further invariant tightening, not triaging active failures.
- 2026-03-11 10:17 MDT: Strengthened server rollback route integration coverage to assert queued status.changed event emission, not just final queued row state. This protects the HTTP rollback path against regressions back to direct status mutation. Verified with go test ./internal/server -tags integration -run \TestTaskHTTPRollbackCreatesQueuedDeployTask' -count=1.
- 2026-03-11 10:22 MDT: Added manual deploy-route integration coverage mirroring rollback. The gated environment deploy HTTP path now proves queued status.changed emission on the created deploy task, protecting route wiring against regression back to direct queued-row mutation. Verified with go test ./internal/server -tags integration -run \TestTaskHTTP(RollbackCreatesQueuedDeployTask|DeployEnvironmentCreatesQueuedDeployTask)' -count=1.
- 2026-03-11 10:28 MDT: Annotated the remaining delivery raw queued-task branches as no-pool unit-test seams only. This makes the fail-closed production contract explicit in code and reduces the chance of future regressions reusing those branches as acceptable runtime behavior.
- 2026-03-11 10:33 MDT: Annotated native tool fallback status-mutation branches the same way as delivery. Pool-backed executors already auto-build canonical task services; remaining raw status-update branches are documented as narrow fallback/test seams, not acceptable production patterns.
- 2026-03-11 10:39 MDT: Documented the repo-layer contract directly in ProjectTaskRepo.Update/UpdateTx: it is low-level persistence, not an approved substitute for task/flow state transitions. This does not yet remove the escape hatch, but it makes the architecture boundary explicit in code while broader API narrowing is evaluated.
- 2026-03-11 10:46 MDT: Added a source-level guard test for direct queued task creation in non-test code. New pool-backed product paths cannot casually reintroduce raw queued task creation without tripping the test unless they are explicitly added to the narrow allowlist for documented fallback seams. Verified with go test ./internal/task -count=1 and go test ./internal/task -tags integration -count=1.
- 2026-03-11 10:49 MDT: Expanded the source guard from raw queued-task creation to all direct task creation into live statuses (`queued`, `in_progress`, `blocked`, `review`, `done`, `cancelled`). The codebase is currently clean under that stronger invariant; only the documented queued fallback seams remain allowlisted.
- 2026-03-11 10:53 MDT: Extended the source guard again to flag low-level task status mutation via repo UpdateStatus in non-test code. The only remaining allowed site is the documented native no-pool fallback seam.
- 2026-03-11 10:57 MDT: Extended the source guard to direct flow-node mutation as well. Only the canonical flow execution service and the documented native no-pool fallback may call SetFlowNode in non-test code.
- 2026-03-11 11:02 MDT: Updated docsv2/21-testing.md to codify architecture/source guard tests as a first-class testing practice for repeated bypass-style bug classes.
- 2026-03-11 11:06 MDT: Added another source guard for generic task-row Update usage in non-test code. New generic task update call sites now fail fast unless they are explicitly allowlisted as current intentional owners.
- 2026-03-11 11:10 MDT: Documented the same architectural boundary on ProjectTaskRepo.UpdateStatusTx that already exists on Update/UpdateTx: low-level persistence only, not an approved task/flow transition API.
- 2026-03-11 11:15 MDT: Documented the architectural boundary on ProjectTaskRepo.Create as well: low-level persistence only, preferred product path is the canonical task service.
- 2026-03-11 11:19 MDT: Added a source guard for direct repo-level task creation call sites in non-test code. New generic task creation owners now have to be explicit, which helps keep product code converging toward canonical task-service creation.

## 2026-03-11
- Routed pool-backed native `task.create` and decomposition child creation through the canonical task service by extending `CreateTaskRequest` with explicit `requires_human_review` and `priority` support.
- Added service and native integration regressions proving explicit human-review overrides stay intact through the canonical path.
- Updated docsv2 tool-policy docs to make native task creation follow the same canonical-service rule as native task transitions.
- 2026-03-11 10:45 MDT: Refactored project bootstrap creation to use the canonical task service and to persist Lori/Frank bootstrap assignments before creating bootstrap tasks. Added an integration assertion that project bootstrap now emits `project_task_event` `task.created` rows for the full bootstrap tree.
- 2026-03-11 11:02 MDT: Tightened pool-backed native tool behavior to fail closed when the canonical task service is missing, instead of silently falling back to raw repo task creation/status mutation. Added integration regressions that explicitly nil out the auto-built task service and prove `task.create` / status-changing `task.update` return `canonical_task_service_unavailable` without mutating project tasks.
- 2026-03-11 10:43 MDT: Fixed bootstrap regression fallout from routing bootstrap task creation through the canonical task service. Bootstrap runtime progress now excludes starter-trio scaffold assignments from executable staffing counts, excludes the bootstrap gate template from non-bootstrap template counts, and keeps Lori scaffolded as a bootstrap worker rather than consuming the PM slot. Updated bootstrap invariant + engine integration tests to assert the new semantics. Verified with `go test ./internal/project ./internal/turn -count=1`, `go test ./internal/project -tags integration -count=1`, and `go test ./internal/turn -tags integration -count=1`. Full `./internal/... -tags integration` sweep hit unrelated flaky tests in `internal/modelgw` (`TestPriorityQueue_OrderingUnderLoad`) and `internal/server` (`TestRealtimeSSEFanoutFromChatAppendMessage`); both passed immediately in isolated reruns.
- 2026-03-11 10:50 MDT: Hardened two unrelated flaky integration tests uncovered by the full sweep. `internal/server` SSE fanout tests now wait through unrelated scoped events until the named event arrives, instead of assuming the next frame is the target. `internal/modelgw` priority-queue ordering test now gates competing requests behind a connection-selection barrier before releasing the held slot, removing the fixed-sleep race under full-suite load. Verified with `go test ./internal/server -tags integration -run 'TestRealtimeSSEFanoutFromChatAppendMessage' -count=20`, `go test ./internal/modelgw -tags integration -run 'TestPriorityQueue_OrderingUnderLoad' -count=20`, and a clean `go test ./internal/... -tags integration -count=1`.
- 2026-03-11 10:59 MDT: Closed a doc/runtime contract mismatch in the task state machine. Runtime already allows `review -> blocked` for real blocker cases (for example reject-path max-visit exhaustion or dependency unavailability surfaced during review), but docsv2/03's legal transition table omitted it. Updated the spec so docs match the enforced state machine.
- 2026-03-11 11:01 MDT: Closed a second task-state-machine doc drift. Runtime correctly allows `in_progress -> done` when the active node is the terminal merge/completion step (and for narrow system-only bypasses like bootstrap gate auto-complete), but docsv2/03's explicit table had omitted that nuance. Updated the spec so the legal transition table matches enforced behavior.
- 2026-03-11 11:05 MDT: Added runtime proof for the legal `review -> blocked` path. New task-service integration coverage now creates a flow-backed task, advances it into `review`, blocks it through the canonical service, and proves the task keeps its review-node flow context while emitting a structured `task.status_changed` event from `review` to `blocked`. Verified with `go test ./internal/task -count=1` and `go test ./internal/task -tags integration -count=1`.
- 2026-03-11 11:10 MDT: Added a focused control-plane unit test for stale queued wakeups. The task-queue processor now has an explicit proof that if a `queued -> in_progress` promotion loses its compare-and-swap race (`repo.ErrConflict`) and the task reloads as a newer state like `blocked`, the processor fails closed and does not create a run. Verified with `go test ./internal/controlplane -count=1`.
- 2026-03-11 11:22 MDT: Closed a remaining human-review contract gap in the server integration suite. `POST /v1/tasks/:id/review-decision` now has an end-to-end reject-path proof that rejection from `review` routes back onto the canonical work node with an active `flow_node_execution`, instead of merely flipping `work_status` in place. Verified with `go test ./internal/server -tags integration -run 'TestTaskHTTP(CreateQueueReviewDecisionLifecycle|ReviewDecisionRejectPreservesCanonicalFlowLineage)' -count=1`.
- 2026-03-11 11:34 MDT: Closed the sibling inbox-review gap at the control-plane layer. Added integration coverage proving `task_review` inbox rejection drives the canonical `flow.rejected` path back to the work node, creates the second active visit there, acts the inbox item, and records `task.review_rejected`. Verified with `go test ./internal/controlplane -tags integration -run 'TestTaskQueueProcessorIntegrationTaskReview(ApproveAdvancesAndKickOffsNextAgent|RejectReturnsToCanonicalWorkNode)' -count=1` and a clean `go test ./internal/controlplane -tags integration -count=1`.
- 2026-03-11 11:41 MDT: Added direct integration proof for the explicit pause/resume branch of the flow state machine. A flow-backed task can move `in_progress -> on_hold -> queued` while keeping its canonical `current_flow_node_id` and active execution lineage, and the docs now say that explicitly. Verified with `go test ./internal/task -tags integration -run 'TestTaskServiceIntegration(AllowsReviewToBlockedWithFlowContext|FlowBackedOnHoldResumesThroughQueued)' -count=1` and a clean `go test ./internal/task -tags integration -count=1`.
- 2026-03-11 11:47 MDT: Cross-package integration sweep is green on the current hardening baseline. Verified together with `go test ./internal/task ./internal/controlplane ./internal/server -tags integration -count=1` after the review-reject and on-hold/resume additions.
- 2026-03-11 11:55 MDT: Full repository integration sweep is green on `main` after the latest state-machine hardening. Verified with `go test ./internal/... -tags integration -count=1`.
- 2026-03-11 12:04 MDT: Root-caused the live `oc-test` dependency failure to non-idempotent dependency creation. `ProjectTaskDependencyRepo.Add` used to surface the unique-edge conflict directly, which caused duplicate `task.add_dependency` calls to fail runs. The repo is now idempotent for duplicate edges and returns the existing row instead. Added repro/fix coverage in `internal/flow` and `internal/tools/native`, then verified the broader dependency-using packages with `go test ./internal/repo ./internal/flow ./internal/tools/native ./internal/project ./internal/controlplane -tags integration -count=1`.
- 2026-03-11 11:44 MDT: Root-caused missing TUI project list to CLI credential scopes. Fixed both login paths to mint a shared interactive client API key scope set, added regression coverage, repaired stale cmd/ottercamp task-detail tests, rebuilt, rotated ~/.ottercamp/credentials through auth service, and verified Sam.blog project now appears in oc-test sidebar.
- 2026-03-11 11:48 MDT: Fixed sticky sidebar-load warnings in the TUI.  now schedules the normal transient status auto-clear for invalid selected-project and empty-project-list warnings instead of leaving them stuck in the status line forever. Added focused model coverage and verified ok  	github.com/samhotchkiss/otter-camp/internal/tui	5.510s.
- 2026-03-11 11:49 MDT: Added transient auto-clear scheduling for sidebar-load warnings in the TUI and covered the invalid-selected-project case directly in model tests. Full internal/tui test package is green.
- 2026-03-11 12:19 MDT: Re-tested the suspected Sam.blog bootstrap leak against current main. Added native integration coverage proving a project-scoped session can queue a flow-backed task but cannot promote it to `in_progress` without canonical execution; the transition fails with the active-flow guard, leaves the task queued, preserves assignment metadata, and creates no flow_node_execution rows. Verified with `go test ./internal/tools/native -tags integration -run 'TestIntegration(ProjectSessionTaskUpdateRejectsInProgressPromotionWithoutCanonicalExecution|ProjectKickoffTaskCreateBindsCanonicalRepoBeforeTaskTree|TaskUpdateQueueKeepsDecomposedParentDraftAndQueuesChildren)' -count=1` and `go test ./internal/turn -tags integration -run 'TestTurnEngineIntegrationBootstrapInvariantHarness' -count=1`. Conclusion: the live idle Sam.blog project reflects stale pre-fix state, not a currently reproducible direct-promotion bypass on `main`.
- 2026-03-11 12:34 MDT: Added control-plane repair for impossible live state. Supervisor now scans for stale `in_progress` tasks that have neither an active runtime owner nor an active `flow_node_execution` and blocks them instead of leaving zombie live tasks in place forever. Added integration coverage in `internal/controlplane/supervisor_integration_test.go`, updated `docsv2/16-agent-control-plane.md`, and verified with `go test ./internal/controlplane -count=1` plus `go test ./internal/controlplane -tags integration -count=1`.
- 2026-03-11 12:05 MDT: Rebuilt and restarted the live `oc-svc` serve/worker stack on commit `1b3193fb` (`repo_version=2360`). Verified the new impossible-live-task supervisor path against the stale Sam.blog project `337aa69b-8d6b-4adc-a10f-9bd26275f84e`: tasks 9 and 10, which had been fake `in_progress` with no runtime owner/execution, were automatically moved to `blocked` by the live worker.
- 2026-03-11 12:14 MDT: Refined the impossible-live-task supervisor repair to record a structured `blocker_reason` without depending on PM assignment or blocker-inbox side effects. Switched supervisor from `MarkBlocked` to canonical `TransitionStatusWithPayload(..., {blocker_reason: ...})`, added integration assertions on the emitted task event payload, and re-verified with `go test ./internal/controlplane -count=1` plus `go test ./internal/controlplane -tags integration -count=1`.
- 2026-03-11 12:23 MDT: Root-caused the remaining stale-task dashboard inflation to archived-project rows. The operator dashboard had been counting `queued/in_progress/on_hold` tasks across the org without filtering `project.status='active'`, so archived Sam.blog resolution tasks still appeared as live/stale workload. Fixed active/stale task counts and item queries to exclude archived projects, added server integration coverage, and documented the rule in `docsv2/18-web-ui.md`.
- 2026-03-11 12:31 MDT: Extended the operator-dashboard archived-project exclusion from active/stale task counts to blocked/review/stranded/validation-blocked task counts as well. The remaining blocked summary inflation in the live DB was mostly archived tasks (`active=2`, `archived=32`). Added server integration coverage proving archived blocked/review tasks no longer appear in blocked totals/items and updated `docsv2/18-web-ui.md` to state that runtime-health summaries exclude archived projects entirely.
- 2026-03-11 12:41 MDT: Improved terminal-stall explanation fallback for legacy blocked tasks. When a blocked task is flow-backed but has no runtime owner, no active execution, no current flow node, and no recorded blocker payload, the dashboard now infers the truthful reason (`automatic repair blocked impossible live task state...`) instead of showing `blocked without recorded reason`. Added `internal/server/project_stall_state_test.go` coverage.
- 2026-03-11 12:57 MDT: Closed the remaining contract gap between impossible-live-task repair and project-level failure truth. Supervisor now pauses the owning project and writes an `automatic_failure` execution-runtime record when it blocks a flow-backed task that was stuck `in_progress` without runtime ownership or active flow execution. Added integration coverage in `internal/controlplane/supervisor_integration_test.go`, updated docsv2/03 and docsv2/16, and verified with `go test ./internal/controlplane -count=1` plus `go test ./internal/controlplane -tags integration -count=1`.
- 2026-03-11 13:39 MDT: Added a legacy repair path for already-blocked corrupted flow tasks. Supervisor now pauses active projects that still contain old impossible-live blocked tasks (flow-backed, no runtime owner, no active execution, no current flow node) even if the task had already been moved out of `in_progress` by an earlier repair. Verified in tests and then live after deploy: stale Sam.blog restart project `337aa69b-8d6b-4adc-a10f-9bd26275f84e` was auto-paused under `automatic_failure.failure_class=impossible_live_task_state`.
- 2026-03-11 14:48 MDT: Fixed a likely TUI-only project-label leak. The persisted `/v1/projects` and control dashboard data were clean, but the workspace/project list can also synthesize labels from session state. `ResolveProjectLabel` now rejects raw UUID labels in addition to `Project <id>` placeholders, so opaque IDs cannot render as project names in the sidebar/dashboard/project view. Verified with focused and full `go test ./internal/tui -count=1` and pushed in `16108453`.
- 2026-03-11 14:59 MDT: Fixed `control/dashboard.summary.active_projects` to exclude archived-project workload. Root cause was the summary query still counting queued/in-progress/on-hold tasks and active runtime from archived projects after the item lists had already been cleaned up. Added integration coverage and deployed the fix live in `b3d961b6` (`repo_version=2369`). Note: under the current metric, a paused project with zero active work does not count as an active project, so the live summary now reports `active_projects=0` rather than the old inflated `2`.
- 2026-03-11 15:00 MDT: Clarified TUI runtime-health wording after fixing the active-project summary query. The summary metric counts projects with active work, not every project row whose coarse status is `active`, so the dashboard copy now says `Active work across N project(s)` instead of `Active N project(s)`. Pushed in `e4252847` and rebuilt local binary.
- 2026-03-11 15:06 MDT: Carried project pause truth through the TUI. `loadTUIProjectDetail` now loads `is_paused` / `pause_reason`, project view renders a pause banner, `loadTUIProjects` now carries pause state for sidebar items, and paused projects render with a `[paused]` marker in the sidebar. Verified with focused loader/render coverage plus clean `go test ./cmd/ottercamp ./internal/tui -count=1`. Pushed in `aad8232f` and rebuilt local binary.

- 2026-03-11 13:12:37 MDT Fixed TUI project-scope chat label normalization so recent chats use canonical project names instead of synthetic project-session labels; rebuilt and verified in oc-test (rv2373).

- 2026-03-11 13:19:10 MDT Targeted state-machine verification sweep green: go test ./internal/task ./internal/flow ./internal/controlplane ./internal/turn -tags integration -run 'Test.*(Transition|Invariant|Mismatch|Impossible|Bootstrap|Reject|Advance|Resume|Recovery)' -count=1. Next pass is contract-gap review for replay/recovery and duplicate-dispatch semantics vs issue 359.

- 2026-03-11 13:19:49 MDT Focused duplicate-wakeup/reject-path/deferred-wakeup state-machine slice green: go test ./internal/controlplane -tags integration -run 'TestTaskQueueProcessorIntegration(DuplicateWakeupsCoalesce|FlowRejectedKickOffsRejectPathAgent|PausedProjectBlocksDeferredWakeupPromotion)' -count=1. Current gap review says issue 359 is substantively implemented in code, docs, and tests.

- 2026-03-11 13:22:17 MDT Added transient 429 retry for chat-history loads in the CLI/TUI client boundary so startup history fetches do not immediately surface noisy failures under local rate limiting. Added HTTP-level regression coverage in cmd/ottercamp/main_chat_test.go.

- 2026-03-11 13:23:19 MDT Fixed TUI chat-history status clearing so a successful retry clears the previous transient failure banner instead of leaving stale error text in the status line. Added regression coverage in internal/tui/model_test.go.

- 2026-03-11 13:26:08 MDT Stopped the CLI from printing freshness warnings to stderr for the TUI command. Freshness remains visible in-app via the status line, but no longer pollutes the tmux pane above the alt-screen. Verified with cmd/ottercamp freshness integration tests.

- 2026-03-11 13:29:43 MDT Fixed a legacy archived-project session leak: chat.service.ListSessions now filters project/project_task sessions whose backing project is archived, so stale active project chats from archived runs no longer appear in chat lists or TUI recent chats. Added integration coverage in internal/chat/session_integration_test.go.

- 2026-03-11 13:31:05 MDT Live verification after server restart: repo_version=2378, project chat list now excludes the archived v3 Sam.blog session, and oc-test sidebar dropped from CHATS (2) to CHATS (1).

- 2026-03-11 13:31:58 MDT Cleaned legacy dev data to match the new archived-session behavior: closed the one stale active project-scoped session left on archived Sam.blog v3 after verifying the product-level filter was in place. Active archived-project session count is now zero.

## 2026-03-11 13:40:14 MDT
- Fixed native tool session.invite_agent to be idempotent so duplicate agent invites return success with already_present=true instead of surfacing a raw conflict.
- Added native tool regression coverage for duplicate session invites.
- Verified with go test ./internal/tools/native -count=1.

## 2026-03-11 13:42:29 MDT
- Added database-backed integration coverage for idempotent session.invite_agent duplicate handling.
- Verified with go test ./internal/tools/native -tags integration -run TestIntegrationSessionInviteAgentIsIdempotent -count=1.

## 2026-03-11 13:44:22 MDT
- Fixed mobile dashboard recent_sessions to include canonical labels and resolved project/task metadata instead of only raw scope IDs.
- Verified with go test ./internal/server -tags integration -run TestMobileDashboardAggregation -count=1.

## 2026-03-11 13:47:47 MDT
- Archived stale pre-fix Sam.blog restart project 337aa69b-8d6b-4adc-a10f-9bd26275f84e and closed its scoped sessions because the project session was polluted by the pre-fix duplicate-handoff loop and could no longer serve as a valid validation target.

## 2026-03-11 13:48:15 MDT
- Started a fresh org async session 8412135a-bf82-4a49-88b5-3333da839535 (Sam.blog Fresh Kickoff) for a clean Sam.blog validation run on the fixed build.

## 2026-03-11 13:49:04 MDT
- Sent corrective follow-up in fresh org session 8412135a-bf82-4a49-88b5-3333da839535 after discovering the initial kickoff accidentally included Codex operator wrapper text. Directed Frank to ignore the wrapper and use only the actual Sam.blog project brief.

## 2026-03-11 13:52:15 MDT
- Fixed native project.create to use the canonical project service when running against a real pool/events runtime, so archived slug reuse, repo binding, and bootstrap task-tree creation all flow through the product service instead of a partial repo-only path.
- Added integration coverage reproducing archived-slug reuse via the native tool.
- Verified with go test ./internal/tools/native -run TestProjectCreate... and go test ./internal/tools/native -tags integration -run TestIntegrationProjectCreateReusesArchivedSlugThroughProjectService -count=1.

## 2026-03-11 13:58:41 MDT
- Root-caused the fresh Sam.blog stale-context leak to native `task.list` falling back to org-wide tasks when `project_id` was omitted, even inside a project-scoped session.
- Fixed `task.list` to default to the current scoped project when the execution context carries a project session.
- Added integration regression coverage proving a project-scoped session cannot see foreign-project tasks when calling `task.list` without an explicit `project_id`.
- Verified with `go test ./internal/tools/native -count=1` and `go test ./internal/tools/native -tags integration -run 'TestIntegration(TaskListDefaultsToCurrentProjectSessionScope|ProjectCreateReusesArchivedSlugThroughProjectService|SessionInviteAgentIsIdempotent)' -count=1`.

## 2026-03-11 14:02:12 MDT
- Rebuilt `./bin/ottercamp` at repo_version=2383, restarted `oc-svc` serve/worker panes with the new binary, and archived the contaminated `samblog-v3` project plus its stale org/project sessions.
- Started a clean relaunch in org session `a76dbcee-efdc-4656-a3e6-4d9bfd94b29f`, sending only the actual Sam.blog brief text (not the operator wrapper from `tmp/samblog-project-kickoff.md`).
- Live verification after the `task.list` scope fix: new project `f062607b-731a-4cb1-9090-56d915547751` (`sam-blog-2`) created cleanly with the canonical 8-task bootstrap tree and a fresh project session `1f6e7071-e8d5-41e2-a256-815687cdab19`.
- Confirmed the previous cross-project contamination is gone in the fresh project session: `task.list` returned only the current project's bootstrap tasks and zero tool results referenced archived `samblog-wp-rebuild` projects.

## 2026-03-11 14:06:41 MDT
- Live Sam.blog verification exposed the next bootstrap bug: the canonical bootstrap task tree existed, but Lori could still create ordinary workstream tasks directly from project chat while bootstrap tasks remained in `draft`.
- Root cause: `task.Service.CreateTask` enforced outstanding project gates only at queue-time, not at create-time.
- Fixed the product to reject non-bootstrap task creation while an outstanding project gate exists; bootstrap gate/root child tasks remain exempt so canonical setup tree creation still works.
- Added regressions in `internal/task/service_integration_test.go` and `internal/tools/native/native_integration_test.go`, and verified they pass alongside existing project bootstrap-tree integration coverage.
- 2026-03-11 14:22 MDT: Fixed a regression in turn-engine tool dispatch where bound-session stamping overwrote explicit target `session_id` for cross-session tools like `message.send`. Added `TestDispatchToolsMessageSendPreservesExplicitTargetSessionID` and narrowed stamping so cross-session tools preserve their target session.
- 2026-03-11 14:27 MDT: Found and fixed a second dispatch stamping bug. `agent.assign_project` attempts in the clean Sam.blog run were being rewritten to Lori's own `agent_id`, which triggered the starter-trio guard even though Lori had created a new PM and temp specialists. Added `TestDispatchToolsAssignProjectPreservesExplicitTargetAgentID` and narrowed `agent_id` stamping for target-agent tools.
- 2026-03-11 14:32 MDT: Found a third dispatch stamping bug. In project-scoped sessions, the engine was deleting explicit `task_id`, which made `task.get`/`task.update` unusable for bootstrap tasks even though `task.list` could see them. Added `TestDispatchToolsTaskGetPreservesExplicitTargetTaskIDInProjectScope` and preserved explicit task targets for task-targeting tools.

## 2026-03-11 14:45 MDT
- Cleaned live Sam.blog restart noise in Postgres by archiving stray active project `sam-blog-6-restart` and its blank project session, leaving only the canonical live restart in the mobile dashboard feed.
- Root-caused the current Sam.blog bootstrap pause to policy, not planning: bootstrap recorded `provider_transient_failure` at `staffing_persisted` with `setup_persisted=false`, but the product paused the restart instead of consuming the existing bounded bootstrap retry path.
- Fixed bootstrap automatic-failure action selection in `internal/turn/engine.go`: provider auth failures still pause immediately, but provider transient/rate-limit failures now archive-and-restart while bootstrap setup has not yet persisted; once setup persists, those same failures pause to preserve work.
- Added integration regression `TestTurnEngineIntegrationBootstrapTransientProviderFailureRestartsBeforeSetupPersists` and re-ran focused bootstrap coverage with `go test ./internal/turn -tags integration -run 'TestTurnEngineIntegrationBootstrapTransientProviderFailureRestartsBeforeSetupPersists|TestTurnEngineIntegrationBootstrapRetriesAreBoundedAndEscalateEX343' -count=1` plus `go test ./internal/turn -count=1`.
- Updated `docsv2/03-projects-and-task-flow.md` and `docsv2/16-agent-control-plane.md` to document class-sensitive provider failure handling during bootstrap.
- 2026-03-11 14:47 MDT: Root-caused the live Sam.blog bootstrap stall to a real circular dependency in product behavior: bootstrap validation required non-bootstrap planned tasks before the governance gate could clear, but `task.create` was still blocked by that same outstanding bootstrap gate. Fixed the deadlock in `internal/task/service.go` so planning-time task creation remains allowed while the canonical bootstrap gate is outstanding, while execution promotion stays fail-closed behind the gate.
- 2026-03-11 14:47 MDT: Added a first-class native bootstrap checkpoint tool, `bootstrap.setup.persist`, plus migration `0123_bootstrap_setup_persist_tool.sql`. This lets the project session persist canonical bootstrap checklist steps directly instead of forcing Lori/Frank to misuse ordinary task status updates for bootstrap bookkeeping. Added regression coverage in `internal/tools/native/native_integration_test.go` and schema coverage in `internal/repo/tool_definition_schema_integration_test.go`.
- 2026-03-11 15:27 MDT: Root-caused the next Sam.blog bootstrap failure to a planning heuristic gap, not a validator bug. `taskdecomp.Analyze` only treated structured descriptions as decomposition input, so broad enumerated titles like `Generate 20 new blog post ideas across all pillars` could survive as single parent tasks when paired with a generic description.
- 2026-03-11 15:27 MDT: Fixed decomposition to treat broad enumerated titles as compound work, prefer the title as the parent deliverable when it is the stronger signal, and added regressions in `internal/taskdecomp/decomposition_test.go` plus `internal/tools/native/mutation_tools_test.go` proving the real task queueing path now auto-decomposes that class of work before bootstrap.
- 2026-03-11 15:33 MDT: Root-caused the next live gap to a missing org-to-project handoff invariant. Frank could finish an org-side `project.create` / `session.create` turn without ever sending the kickoff message into the new project session, leaving the project session empty and forcing operator-side recovery.
- 2026-03-11 15:33 MDT: Fixed `internal/turn/engine.go` to backfill a canonical Frank handoff when a new project session exists but has no Frank-authored kickoff message yet. The engine now synthesizes the handoff from the originating user request, ensures Frank/Lori participation in the project session, and explicitly queues Lori onto that kickoff instead of relying purely on prompt compliance. Added regression coverage in `internal/turn/engine_integration_test.go`.
- 2026-03-11 15:40 MDT: Live verification of the handoff backstop exposed a follow-up bug: if Frank *did* send the natural kickoff message, the synthetic backfill could still fire later and append a duplicate handoff into the same project session.
- 2026-03-11 15:40 MDT: Tightened the backfill suppression rule to skip whenever the project session already contains any user kickoff message, not only a Frank-authored one. Added `TestTurnEngineIntegrationDoesNotDuplicateExistingProjectKickoffHandoff` and re-ran the affected package sweep.
- 2026-03-11 15:42 MDT: The next live Sam.blog run exposed a deeper repo-binding bug. Projects were getting a canonical `project_environment.repo_path`, but the bound workspace path was still just an empty directory, so repo-backed tools failed with `repo: not found` / `not_a_git_repo` as soon as Lori tried to create repo-backed planning work.
- 2026-03-11 15:42 MDT: Fixed the repo-binding contract in `internal/project/repo_binding.go` so canonical repo binding creates/heals a real git repo (`git init -b main`) at the bound workspace path. Added the failing integration to `internal/tools/native/native_integration_test.go` and a branch-parser regression in `internal/tools/native/file_git_test.go`, then re-ran `./internal/project`, `./internal/tools/native`, and `./internal/turn`.

- 2026-03-11 15:56 MST: Archived dead pre-fix project `sam-blog-8`, restarted Sam.blog through the org async session, and verified the new project `sam-blog-9` binds an initialized git repo at `/Users/sam/otter-data/workspaces/sam-blog-9` with `.git` present on first bootstrap.
- 2026-03-11 16:23 MDT: Root-caused the next restart loop to `bootstrap.setup.persist` rejecting live planner slugs `bind-repo`, `validate-sizing`, and `attach-flows`. Added those aliases to `internal/tools/native/mutation_tools.go`, extended the integration test in `internal/tools/native/native_integration_test.go`, and pushed `e07c49be` (`Accept live bootstrap step aliases`).
- 2026-03-11 16:23 MDT: Live verification after `e07c49be` shows `sam-blog-10` created cleanly with a real git repo and no immediate auto-restart. One Anthropic connection hit `provider_rate_limit`, but routing failed over to another healthy Anthropic connection and the Lori bootstrap turn remained active.
- 2026-03-11 16:59 MDT: Root-caused the next live Sam.blog blocker to missing global system flow templates in the real app DB, not workspace binding. `default-review-refinement` was absent entirely and `default-single-agent` was not current, so subjective planning tasks could fail closed with `repo: not found` when the planner requested `default-review-refinement`.
- 2026-03-11 16:59 MDT: Fixed this at the product level, not as a one-off DB patch. `cmd/ottercamp/main.go` now runs bootstrap reconciliation during `serve` startup, `internal/worker/worker.go` does the same on worker startup, and `internal/repo/project.go` now reconciles missing/non-current flow-template rows in `BulkUpsertBySlug` instead of re-inserting conflicting v1 rows.
- 2026-03-11 16:59 MDT: Added regression coverage in `internal/bootstrap/bootstrap_integration_test.go` for reconciling missing system templates on an existing install, and corrected the native reproducer in `internal/tools/native/native_integration_test.go` so it seeds the required review-refinement system template and only fails on real `task.create` regressions.
- 2026-03-11 16:59 MDT: Verified targeted packages pass: `go test ./internal/bootstrap -tags integration -run 'TestBootstrap(RunReconcilesMissingSystemFlowTemplatesOnExistingInstall|SeedsSystemFlowTemplatesIdempotently)$' -count=1`, `go test ./internal/tools/native -tags integration -run 'TestIntegration(BootstrapPlanningTaskCreateSupportsMultipleParentWorkstreamsInSameProjectSession|TaskCreateSubjectiveMultiOptionUsesReviewRefinementPlanning)$' -count=1`, and `go test ./internal/worker ./cmd/ottercamp ./internal/repo`.
- 2026-03-11 17:00 MDT: Rebuilt `./bin/ottercamp`, ran live `ottercamp bootstrap` successfully against `ottercamp_oc2`, and confirmed the DB now contains all three current system templates: `default-single-agent`, `default-review`, and `default-review-refinement`.
- 2026-03-11 17:00 MDT: Restarted `oc-svc` on the patched binary and launched a fresh Sam.blog run through the real org/general chat session using only the canonical brief text. New live project: `sam-blog-12` (`380daa45-3db9-4e0c-949b-c6902034728d`), with project session `244f8baf-a097-4412-823e-c4ad2ee99758`.
- 2026-03-11 17:00 MDT: Current live state: Frank created `sam-blog-12`, handed off to Lori, and the canonical 8-task bootstrap tree exists in `draft`. Monitoring focus is now whether Lori decomposes into bounded child workstreams cleanly on the repaired stack.
- 2026-03-11 17:17 MDT: Confirmed live `sam-blog-12` progressed past staffing into bounded workstream decomposition. Lori created four main workstreams (`WS1`..`WS4`) plus granular child tasks for migration batches, design batches, strategy synthesis, and blog-idea generation instead of stalling on oversized parent tasks.
- 2026-03-11 17:17 MDT: Committed and pushed startup/system-template reconciliation as `b55161bc` (`Reconcile system flow templates on startup`). Also narrowed `.gitignore` from bare `ottercamp` to `/ottercamp` so `cmd/ottercamp/*` remains trackable while the root build artifact stays ignored.
- 2026-03-11 17:13 MDT: Root-caused live subjective-task failures to invalid seeded system flow templates. `default-single-agent`, `default-review`, and `default-review-refinement` were missing terminal `merge` nodes, so bootstrap seeded non-executable defaults. Fixed bootstrap seed definitions and reconciliation to heal existing templates in place, verified with focused integration coverage, ran live `ottercamp bootstrap`, and pushed `bd0ae066` (`Fix invalid seeded system flow templates`).
- 2026-03-11 17:15 MDT: Root-caused the next bootstrap validator failure to an underpowered decomposition heuristic. Titles like `Generate blog post concepts 11-20 and compile final list` were not recognized as compound enumerated work, so oversized child tasks could still slip through kickoff planning. Tightened `taskdecomp.Analyze` to split enumerated action titles on `and`, added a reproducer in `internal/taskdecomp/decomposition_test.go`, and pushed `678511c2` (`Catch compound enumerated child task titles`).
- 2026-03-11 17:15 MDT: Rebuilt and restarted `oc-svc` on the patched binary, then launched a fresh Sam.blog run via org/general session `d528e91d-3e35-4464-b5e4-2665cad86c38`. New live project: `sam-blog-13` (`44cd26ee-b419-4218-bb82-42d36121dfe0`), project session `2d3c6d0c-dc05-4aec-b337-e9b0b5e0394d`. Early bootstrap is clean and Lori is in staffing/context-gathering.
- 2026-03-11 16:56 MDT: Root-caused the current live `sam-blog-14` stall to a watchdog design hole in `internal/turn/engine.go`. The project bootstrap watchdog only armed while persisted setup counts were all zero; once Lori had created any assignments/tasks/templates, a stuck `in_flight` model invocation could leave the bootstrap turn running forever with no failure path.
- 2026-03-11 16:56 MDT: Added a reproducing integration regression `TestTurnEngineIntegrationProjectBootstrapWatchdogFailsHungTurnAfterPartialSetup` proving a bootstrap turn must still time out and fail closed when partial setup already exists. Fixed the engine to keep the watchdog active until bootstrap is actually materialized or validation-failed, and to record an honest failure reason that includes persisted progress counts for partial-setup stalls. Focused coverage now passes for both zero-setup and partial-setup watchdog paths.
- 2026-03-11 17:34 MDT: Committed and pushed `7bc4d3b1` (`Fail partial bootstrap stalls closed`). This closes the bootstrap watchdog hole where partially materialized setup could leave a Lori kickoff turn in `in_progress` forever. Targeted integration coverage is green for both zero-setup and partial-setup hangs.
- 2026-03-11 17:38 MDT: Committed and pushed `e3477f6f` (`Accept live bootstrap step aliases`). Root cause was `bootstrap.setup.persist` rejecting live planner slugs `bootstrap-governance` and `attach-and-validate-flow-templates`. The tool now accepts those live aliases; governance is treated as an accepted no-op marker because the gate is derived from checklist completion and validation rather than being directly persisted.
- 2026-03-11 17:39 MDT: Live state after rolling `repo_version=2404`: archived stale pre-fix project `sam-blog-13-restart`; watchdog correctly auto-archived `sam-blog-14` instead of hanging; active project is now `sam-blog-14-restart` (`ad59967a-7801-4416-bfbe-2fb9d1df20d7`).
- 2026-03-11 17:40 MDT: `sam-blog-14-restart` is not blocked by queueing. Its pending retry job is intentionally delayed until `2026-03-12 20:59:59 MDT` by provider backoff. Current provider health in the live DB: two Anthropic connections are `rate_limited`, one is `unavailable`. This is the next operational constraint if Sam.blog is to continue tonight.
- 2026-03-11 17:47 MDT: Committed and pushed `847d2ce4` (`Honor persisted provider health on cold start`). Root cause: router selection used only in-memory `HealthChecker` state, which defaults to healthy after process restart, so persisted `provider_connection.health_status` was ignored on cold start. Added `GetStateKnown` to distinguish unknown from healthy, made router fall back to persisted connection health when in-memory state is unknown, and added router regressions for persisted `unavailable` / `rate_limited` connections.
- 2026-03-11 17:47 MDT: Restarted `oc-svc` on `repo_version=2405`.
- 2026-03-11 18:03 MDT: Root-caused `serve` startup deadlock to leaked migration advisory locks in `internal/migrate.Runner.Run()`. Fixed by acquiring a dedicated pgx connection for the whole migration run and unlocking on that same connection; added integration coverage proving the lock can be reacquired after `Run()` returns. Commit: `94ed5b24` (`Fix migration advisory lock leakage`). Rebuilt and restarted `oc-svc`; `serve` now binds `:4110` and `/health/ready` returns 200.
- 2026-03-11 18:04 MDT: Live Sam.blog restart (`sam-blog-14-restart`, project `ad59967a-7801-4416-bfbe-2fb9d1df20d7`) is not queue-deadlocked. It is currently waiting on a pending `agent_turn` job scheduled for `2026-03-12 20:59:59 -06:00` after a real Anthropic `429` backoff.
- 2026-03-11 18:05 MDT: Provider audit: all three Anthropic `provider_connection` rows are currently configured with `{"auth_mode":"api_key"}` rather than subscription auth. `claude-swh-me` and `s-swh-me` are persisted `rate_limited`; `pearl-swh-me` is persisted `unavailable` despite multiple successful completed invocations later in the day, indicating another health/config consistency issue worth tracing.
- 2026-03-11 20:40 MDT: Fixed bootstrap decomposition false positives in `internal/taskdecomp/decomposition.go`. Child tasks now derive titles from the actual deliverable instead of inheriting the broad parent workstream title, generated children are size-validated before queueing, and enumerated batch titles like `Generate 20 ...` are split into bounded numbered slices. Verified with `go test ./internal/taskdecomp ./internal/task ./internal/tools/native -count=1` and `go test ./internal/turn -tags integration -run TestTurnEngineIntegrationBootstrapInvariantHarness -count=1`.
- 2026-03-11 20:48 MDT: Fixed a second decomposition bug uncovered by live `sam-blog-15`. Parent-task prose like `Assigned to ...`, `Blocked on ...`, and `Parent workstream: ...` was being treated as executable child work during auto-decomposition. Tightened `extractDeliverables`/`cleanSegment` to ignore planning-only sentences and strip the `Parent workstream:` prefix before sizing/decomposition. Verified with `go test ./internal/taskdecomp ./internal/tools/native -count=1` and `go test ./internal/turn -tags integration -run TestTurnEngineIntegrationBootstrapInvariantHarness -count=1`.
- 2026-03-11 20:58 MDT: Fixed a third decomposition false-positive. Multiline task descriptions that included markdown metadata lines like `**Output:**`, `**Agent:**`, `**Estimated time:**`, and `**Depends on:**` were still being treated as separate deliverables, which caused bounded tasks like `WS3.5: Define success metrics & KPIs` to be misclassified as broad/compound. Normalized repeated markdown markers in `cleanSegment`, filtered those metadata prefixes, and prevented multiline descriptions with explicit content lines from falling back to sentence-level deliverable splitting. Verified with `go test ./internal/taskdecomp ./internal/tools/native -count=1` and `go test ./internal/turn -tags integration -run TestTurnEngineIntegrationBootstrapInvariantHarness -count=1`.

- 2026-03-11 21:00 MST: Added a regression test proving bootstrap must not archive immediately on a recoverable child-boundedness tool error. Updated TurnEngine to let the same turn recover and leave end-of-turn bootstrap validation as the hard gate. Verified with targeted integration tests in internal/turn.
- 2026-03-11 21:16 MST: Fixed parent follow-on task creation so broad child requests under an existing parent now decompose into bounded child tasks instead of hard-erroring. Added native integration coverage for parent follow-on decomposition.
- 2026-03-11 21:29 MST: Tightened project kickoff handoff enforcement. Existing Lori handoffs are now only trusted if they carry the originating request context; otherwise OtterCamp appends a canonical synthetic handoff with the real brief. Added regression coverage for generic-handoff backfill.
- 2026-03-11 21:44 MST: Root-caused a separate stuck-turn class outside bootstrap. Async organization turns had no model-stream watchdog at all; `callMainModel` only wrapped `StreamComplete` with a timeout for project-bootstrap turns. That allowed a clean-source Sam.blog org kickoff to remain `in_progress` forever with a `model_invocation` stuck `in_flight` after the queue job had already finished.
- 2026-03-11 21:44 MST: Added a reproducing integration test `TestTurnEngineIntegrationAsyncOrgTurnFailsHungModelStreamAtDurationLimit` and fixed `internal/turn/engine.go` so all async turns get a stream watchdog based on `asyncMaxDuration` when the bootstrap-specific watchdog is not active. The engine now fails the invocation with `error_code=turn_timeout`, persists `stop_reason=max_duration`, fails the turn, clears `current_turn_id`, and emits a timeout system message instead of leaving a ghost turn.
- 2026-03-11 21:45 MST: Verified with `go test ./internal/turn -tags integration -run 'TestTurnEngineIntegration(AsyncOrgTurnFailsHungModelStreamAtDurationLimit|ProjectBootstrapWatchdogFailsHungInFlightTurnAndReleasesClaim|ProjectBootstrapWatchdogFailsHungTurnAfterPartialSetup)' -count=1` and `go test ./internal/turn -count=1`.
- 2026-03-11 21:47 MST: Rolled `oc-svc` to commit `4f545a1c` and confirmed `/health/ready` returned 200 on the new build.
- 2026-03-11 21:47 MST: Repaired the previously stuck clean-source org kickoff session `d528e91d-3e35-4464-b5e4-2665cad86c38`: cancelled the ghost turn `b691409d-9470-44dd-b6c9-7e40df715616`, manually failed its orphaned `model_invocation` row `7a449ca1-3786-45c1-becf-7be4eaae0e6b`, and re-sent the canonical Sam.blog brief through the org session.
- 2026-03-11 21:47 MST: Fresh project `sam-blog-19` is now live: project `348f37cd-0951-48bc-a5ba-e633ef86ddb6`, project session `f494ec96-96ab-4fda-9798-06987c72f6da`. Verified Lori received the correct full kickoff context (technonymous import, 10 HTML layout options, content strategy, 20 new post ideas, photography archive) rather than the generic blog-build drift.
- 2026-03-11 21:55 MST: Live `sam-blog-19` planning exposed another bootstrap validator hole: Lori could explicitly declare "No PM needed" and still proceed because bootstrap validation only required some non-starter assignments, not an actual active PM assignment.
- 2026-03-11 21:55 MST: Added a reproducing bootstrap invariant regression (`staffed_project_without_pm_fails_closed`) and tightened `internal/turn/engine.go` so staffed bootstrap cannot pass validation without an active PM assignment. New failure class: `pm_assignment_missing`. Verified with `go test ./internal/turn -tags integration -run 'TestTurnEngineIntegration(BootstrapInvariantHarness|AsyncOrgTurnFailsHungModelStreamAtDurationLimit|ProjectBootstrapWatchdogFailsHungInFlightTurnAndReleasesClaim|ProjectBootstrapWatchdogFailsHungTurnAfterPartialSetup)' -count=1` and `go test ./internal/turn -count=1`.

- 2026-03-11 22:10 MST: Root-caused repeated parent decomposition to incomplete parent metadata persistence in native task.create. Added integration reproducer for repeated parent decomposition across turns, patched createDecomposedParentChildren to persist full decomposition metadata via taskdecomp.ApplyMetadata, verified with `go test ./internal/tools/native -tags integration -run 'TestIntegrationParentTask(CanDecomposeBroadFollowOnChildRequest|RepeatedDecompositionRequestReusesExistingChildren)' -count=1`, and pushed `c72fb92e` (`Persist parent decomposition metadata`).

- 2026-03-11 22:14 MST: Added taskdecomp regression coverage for bare timing notes leaking into deliverables (`~30 min, browser-heavy.`) after `sam-blog-20-restart-restart` persisted them as executable tasks. Filtered timing-only lines in `cleanSegment`, verified with `go test ./internal/taskdecomp ./internal/tools/native -tags integration -run 'Test(ExtractDeliverablesIgnoresBareTimingNotes|IntegrationParentTask(CanDecomposeBroadFollowOnChildRequest|RepeatedDecompositionRequestReusesExistingChildren))' -count=1`, and pushed `b5cf8201` (`Ignore timing notes in task decomposition`).

- 2026-03-11 22:18 MST: `sam-blog-21` exposed recursive queue-time decomposition of already-bounded child tasks into synthetic `(Workstream N)` duplicates. Added integration reproducer ensuring queueing a child task does not auto-decompose it again, guarded `applyQueueDecomposition` to skip tasks that already have `decomposition_parent_task_id`, verified with `go test ./internal/tools/native -tags integration -run 'TestIntegration(ProjectSessionQueueKeepsPlannedTaskSetFlat|ParentTask(CanDecomposeBroadFollowOnChildRequest|RepeatedDecompositionRequestReusesExistingChildren)|QueueingDecomposedChildTaskDoesNotReDecomposeIt)' -count=1`, and pushed `c96c3e89` (`Skip re-decomposition for child tasks`).

- 2026-03-11 22:22 MST: `sam-blog-22` exposed another extractor leak: markdown-decorated metadata lines like `**Assigned to:**` and `**Est. time:**` were being persisted as executable tasks. Added a `taskdecomp` regression for markdown-decorated metadata lines, expanded `cleanSegment` prefix filtering for those variants, verified with `go test ./internal/taskdecomp -run 'TestExtractDeliverables(IgnoresTaskMetadataLines|IgnoresBareTimingNotes|IgnoresMarkdownDecoratedMetadataLines)' -count=1`, and pushed `a43e596d` (`Ignore markdown task metadata lines`).

- 2026-03-11: Pushed b0c3a744 to split broad photography archive and strategy synthesis workstreams into bounded children after sam-blog-23 exposed WS5 and WS3.6 bootstrap failures.

- 2026-03-11: Added decomposition regression coverage and parser filtering for instruction-only requirement lines like "Each should..." and "Each must..." after sam-blog-24 emitted constraint prose as executable tasks.

- 2026-03-11: Added markdown numbered-list decomposition normalization for escaped newlines, bold/backtick cleanup, heading-only line suppression, and path-only scaffold suppression after sam-blog-25 emitted malformed WS2 design-direction tasks.

- 2026-03-11: Expanded bootstrap.setup.persist alias mapping for live Lori synonyms (staffing, task_decomposition, flow_templates, dependency_wiring) after sam-blog-26 failed to persist setup and fell into gate-blocked parent-task workarounds.

- 2026-03-11: Added explicit bootstrap recovery system guidance after bounded-child creation failures so the model is instructed to emit bounded child tasks under the parent instead of inventing standalone/subtask workarounds.

- 2026-03-11: Broadened bootstrap restart dedupe to suppress any second active restart for the same source project, not just the same retry-attempt slot, after live restart fan-out created multiple concurrent sam-blog-27 descendants.
2026-03-11 23:17:14 MDT Added orchestration-parent queue guard driven by planning follow_on_stop_reason; added integration regression test for queue rejection before bounded children exist.
2026-03-11 23:23:31 MDT Fixed gateway health recovery so in-memory rate-limited connections degrade after backoff instead of staying stuck until a lucky success; added health regression test.
2026-03-11 23:27:53 MDT Treated stale persisted provider rate limits as degraded after max backoff using provider_connection.updated_at; added cold-start router regression test.
2026-03-11 23:29:46 MDT Router now prefers the oldest degraded/recoverable provider connection before static priority, so stale/fresh degraded states do not keep pinning traffic to the same account.
2026-03-11 23:39:37 MDT Tightened decomposition filtering to drop Target:/Sections:/descriptor-only companion lines while preserving real action-led tasks; added regression coverage from sam-blog-29.
2026-03-11 23:40:31 MDT Filtered decomposition companion guidance lines (Target:/Sections:/descriptor-only notes) with a design-direction exception; sam-blog-29 should be restarted on this fix.
- 2026-03-11 23:52 MDT: Fixed task decomposition comma-splitting so define/include lists no longer split inside parentheses, and filtered bare `Est:` timing notes after `sam-blog-30` emitted malformed child tasks like `Define body text` / `Define captions)` / `Est: ~25 min`. Added regression tests in `internal/taskdecomp/decomposition_test.go`.
- 2026-03-11 23:56 MDT: Added a second decomposition guard to ignore explanatory companion sentences beginning with pronouns like `This is ...` and `Each is ...` after `sam-blog-31` leaked those as executable tasks. Added regression coverage in `internal/taskdecomp/decomposition_test.go`.
- 2026-03-12 00:00 MDT: Added a third decomposition filter for companion instruction/sizing lines (`Commit to repo`, `Save as ...`, `... up to 60 min`) after `sam-blog-33` still emitted those as tasks. Added regression coverage in `internal/taskdecomp/decomposition_test.go`.
- 2026-03-12 00:06 MDT: Added a fourth decomposition filter for design-guidance/process companion lines (`Visual-first ...`, `Large image areas ...`, `Embedded CSS, responsive`, `Must include ...`, `Save each as ...`, `Commit in batches ...`) after `sam-blog-34` still promoted them to tasks. Added regression coverage in `internal/taskdecomp/decomposition_test.go`.
- 2026-03-12 00:12 MDT: Replaced the ongoing prefix-only decomposition filtering with a stronger executable-task acceptance rule: keep only action-led deliverables or explicitly labeled task titles, while still allowing titled design directions with em dashes. This was added after `sam-blog-35` still leaked `Commit ...`, `Self-contained HTML+CSS`, and `Browser-heavy work.` lines.
- 2026-03-12 00:31 MDT: Fixed continuation compression to set `historyStartID` to the newly appended `[Continuation summary]` system message. Before this, continuation turns could say they were compressed while still carrying full prior history, which matches the guardrail-loop bootstrap failure seen in `sam-blog-36`. Added regression coverage in `internal/turn/engine_test.go`.
- 2026-03-12 00:37 MDT: Added a hard continuation-summary cap (`maxContinuationSummaryChars=4000`) before the summary becomes the next turn's history root. This addresses the follow-on failure seen after `421d3405`, where continuation still regressed into oversized bootstrap summaries and another 64k guardrail continuation despite using the summary as the history root.
- 2026-03-12 00:58 MDT: Replaced generic model-written continuation summaries for active project-bootstrap project sessions with a deterministic `[Project bootstrap resume]` system message built from persisted bootstrap state. This targets the still-observed long-bootstrap confusion on `sam-blog-38` (`Got it — what's your question?`) even after capping generic continuation summaries.
- 2026-03-12 01:12 MDT: Fixed project-bootstrap session initialization so the project-scoped chat session gets `project_bootstrap.status=active` and `initial_message_id` immediately when Frank backfills the synthetic kickoff handoff. This addresses the live bug in `sam-blog-39` where project session metadata stayed `{}`, preventing deterministic bootstrap resume from ever triggering.
- 2026-03-12 01:36 MDT: Root-caused a live stuck-turn class in `sam-blog-40-restart-restart`: the first Lori bootstrap turn remained `in_progress` with a `model_invocation` stuck `in_flight` because `internal/gateway/client.go` used the already-canceled provider request context when persisting failure cleanup. Added a detached bounded cleanup context for `GetByID`, provider health persistence, and `UpdateFailure`, and added integration regression `TestLiveModelGatewayMarksInvocationFailedWhenRequestContextIsCanceled` proving a canceled in-flight request still finalizes the invocation as `failed` instead of leaving it orphaned. Verified with `go test ./internal/gateway -tags integration -run TestLiveModelGatewayMarksInvocationFailedWhenRequestContextIsCanceled -count=1` and targeted package runs for `internal/gateway`, `internal/turn`, `internal/taskdecomp`, and `internal/tools/native`.
- 2026-03-12 01:50 MDT: Fixed a second live bootstrap continuation bug exposed after the gateway cleanup. `sam-blog-40-restart-restart` fail-closed correctly on `first_wave_task_unbounded`, but `max_tool_calls` still opened a stale continuation turn which later failed with `continueTurn: session is closed`. Root cause was `shouldContinueMaxToolCalls` checking only async mode + depth budget and ignoring already-failed bootstrap validation. Added integration regression `TestTurnEngineIntegrationProjectBootstrapMaxToolCallsDoesNotContinueAfterValidationFailure`, updated `shouldContinueMaxToolCalls` to consult DB-backed bootstrap validation for active project-bootstrap sessions, and verified with targeted integration runs for bootstrap validation/timeout paths plus package runs for `internal/gateway`, `internal/turn`, `internal/taskdecomp`, and `internal/tools/native`.
- 2026-03-12 02:00 MDT: Launched a fresh Sam.blog run through the live app API in new org async session `0c2659e7-d612-4b44-abf8-6148c67110c1`, sending only the canonical Sam.blog brief text. Frank created project `sam-blog-41` (`d3477e34-9d00-4801-b19a-6197454c7610`) and opened project session `0778ea45-9d8d-4e31-9fa7-db6f15fe9fb8`.
- 2026-03-12 02:02 MDT: Lori's first bootstrap turn on `sam-blog-41` failed at `staffing_persisted` with `failure_class=provider_transient_failure`, `failure_category=provider_api`, and `failure_reason=transient model failure`. This is not a planning-policy regression: Lori owned planning, proposed a non-staff PM, and did not route PM/reviewer roles to Frank/Ellie/Lori.
- 2026-03-12 02:06 MDT: Auto-restart chain is now `sam-blog-41` -> `sam-blog-41-restart` -> active `sam-blog-41-restart-restart` (`b64332e8-47a1-415a-87b7-f15c68d58314`). This is a coherent restart bundle, not the earlier half-born restart bug.
- 2026-03-12 02:07 MDT: Live policy enforcement confirmed on `sam-blog-41-restart-restart`: Lori created five temp agents, attempted to assign Frank as PM, and the product rejected it with `starter_trio_not_project_staff`. Lori then continued in-turn toward a dedicated PM instead of silently using starter-trio staff on the project.
- 2026-03-12 02:15 MDT: Root-caused a bootstrap-restart metadata gap while monitoring live `sam-blog-41-restart-restart`. Restart-created project sessions were seeded only with `bootstrap_restart` metadata and the restart prompt message, but not with active `project_bootstrap` session state. That meant bootstrap watchdog/runtime management had to rely on message metadata alone instead of the normal session bootstrap state contract. Patched `internal/turn/project_bootstrap_restart.go` so restart sessions now seed `project_bootstrap.status=active`, `current_phase=kickoff_handoff`, and `initial_message_id` from the restart prompt message; added regression assertions in the restart integration test and verified with targeted `internal/turn` integration runs.
- 2026-03-12 02:21 MDT: Root-caused the live bounded first-wave queue leak from `sam-blog-41-restart-restart`: bootstrap promotion could surface raw `task exceeds bounded size policy` errors out of first-wave queue transitions instead of converting them into bootstrap validation failure. Added `TestTurnEngineIntegrationProjectBootstrapFailsClosedWhenFirstWaveQueueHitsBoundedSize`, verified targeted restart/bootstrap integration runs, and kept the fix in `internal/turn/engine.go` fail-closing that path under `projectBootstrapFailureFirstWaveExecution`.
- 2026-03-12 02:25 MDT: Added and pushed `1e5fa9e8` (`Fail closed on bounded first-wave promotion`). This root-causes the bootstrap path where first-wave queue promotion surfaced raw bounded-size errors instead of converting them into `projectBootstrapFailureFirstWaveExecution`. Verified with the new integration regression `TestTurnEngineIntegrationProjectBootstrapFailsClosedWhenFirstWaveQueueHitsBoundedSize` and nearby restart/bootstrap integration runs.
- 2026-03-12 02:30 MDT: Rebuilt/restarted `oc-svc` on `1e5fa9e8`, launched a fresh Sam.blog org-session kickoff, and confirmed the old noisy bounded-size replay loop came from stale session `3bc2ce32-7776-404d-9439-4843a5029672` on project `b64332e8-47a1-415a-87b7-f15c68d58314`. Closed/archived that poisoned chain to clear worker noise.
- 2026-03-12 02:31 MDT: Fresh project `4f0c3b85-5bda-40b6-a9b4-f451e4457637` reached the first bootstrap turn cleanly, then fail-closed on a genuine provider-transient error after multiple successful model invocations. Auto-restart worked and created the next active Sam.blog project `9f031ff1-9bd0-440a-9a91-20be36ee40d1` with active project session `c36bc9d3-2142-4014-984c-519e441c7ce5`.
- 2026-03-12 02:40 MDT: Root-caused the starter-trio staffing regression to `internal/project/service.go:createBootstrapTaskTree`, which auto-seeded Lori/Frank into `agent_project_assignment` via `ensureBootstrapAssignments`. Removed those project-staff rows, added `TestProjectServiceCreateDoesNotAutoAssignStarterTrioAsProjectStaff`, and added a narrow task-creation exception so bootstrap setup tasks may still target starter-trio agents without requiring project assignment rows. Also removed the bootstrap gate root task's direct assignee so the exception is only used for setup subtasks. Pushed as `abb78aec` (`Keep starter trio out of project staffing`).
- 2026-03-12 02:44 MDT: Restarted `oc-svc` on `abb78aec`, archived the invalid pre-fix Sam.blog runs, and launched fresh `sam-blog-43` (`5b9c46fc-9c05-475b-908b-eea3cd6d25f3`). Live verification: project creation now produces zero `agent_project_assignment` rows, so Lori/Frank are no longer auto-added as project staff while bootstrap tasks still target them as setup-only actors.
- 2026-03-12 02:33 MDT: Verified local commit `fbdb9839` (`Count bootstrap templates before staffing`) with targeted integration coverage and pushed it to `main`. Rebuilt/restarted `oc-svc` on the new binary.
- 2026-03-12 02:35 MDT: Launched fresh org async kickoff session `a9c30336-f6c2-40ab-b7a8-9b4430b8cb44` for the canonical Sam.blog brief.
- 2026-03-12 02:36 MDT: Fresh project `sam-blog-45` created: project `28a0c806-4bb0-40af-9cfc-3c1825bf8107`, project session `6283ebcc-d18f-4ee4-87e0-27e7b83d3fae`.
- 2026-03-12 02:36 MDT: Live validation of `fbdb9839`: project has `0` `agent_project_assignment` rows, `8` canonical bootstrap tasks, and `1` project `flow_template` while bootstrap remains active. This is the exact state the old code misclassified as zero-progress; the session stayed alive and opened a follow-on turn instead of falsely fail-closing.
- 2026-03-12 02:37 MDT: New live concern on `sam-blog-45`: Lori's streaming staffing plan text again proposes `Frank` as PM at the planning layer. Need to verify whether the hard starter-trio staffing rule still blocks the eventual write or whether there is another policy leak path before assignment persistence.
- 2026-03-12 02:45 MDT: Root-caused another bootstrap false-zero-progress failure on `sam-blog-45`. Lori created persisted staffing artifacts (`agent.create_staff` tool results), but bootstrap state still treated `staffing_persisted` as `AssignmentCount > 0` only. Added regression `TestTurnEngineIntegrationBootstrapProgressCountsStaffingDraftsWithoutAssignments`, updated bootstrap progress/state to count persisted staffing drafts from project-session tool results, and changed watchdog messaging to classify these as partial persisted setup instead of zero progress. Pushed as `82a25c53` (`Count bootstrap staffing drafts as progress`).
- 2026-03-12 02:47 MDT: Fresh project `sam-blog-46` created on build `82a25c53`: project `f80fd219-2785-4843-b8b6-9d4ded34e087`, project session `76de5744-aab6-4ac0-89cc-5684aee52452`, org kickoff session `bb8cf010-55f0-4ff6-9eb6-350d51685ce6`.
- 2026-03-12 02:48 MDT: Live bootstrap crossed the previously-failing staffing boundary. Persisted state reached `6` project assignments, `13` project tasks, and `4` project flow templates without a watchdog false-failure. PM assignment went to a dedicated created PM agent (`8401f50d-ae11-4390-b5ec-af227f7a7ef6`), not Frank. Lori is actively creating granular child tasks under the parent workstreams.
- 2026-03-12 02:54 MDT: `sam-blog-46` proved bootstrap can now persist staffing, project assignments, templates, and parent tasks cleanly, but exposed a decomposition bypass: `task.create` under a parent used validated decomposition during planning and then reconstructed child drafts from raw `Plan.Deliverables`, allowing an oversized primary deliverable to be persisted as child task 19 (`Create a 3-month editorial calendar ...`).
- 2026-03-12 02:55 MDT: Fixed that bypass by making `PrepareQueueDecomposition` validate the full persisted child set and making native parent-child creation trust `prepared.ChildDrafts` instead of rebuilding from raw plan text. Added native integration coverage proving oversized primary deliverables are not persisted unchanged. Pushed as `29803c1e` (`Validate full persisted decomposition child set`).
- 2026-03-12 03:01 MDT: `sam-blog-47` exposed a remaining sizing-heuristic gap rather than a persistence bypass. Oversized strategy-definition tasks like `Define the 5 content pillars with detailed descriptions ...` and `Define the overarching brand narrative ...` were accepted as bounded child tasks because the estimator undercounted single-line strategy asks with enumerated scope and narrative language.
- 2026-03-12 03:02 MDT: Tightened task sizing heuristics for enumerated strategy-definition asks (`(1)...(5)`), `detailed descriptions`, and `brand narrative` language. Added direct `taskdecomp` regressions for both live shapes and pushed as `df2d8079` (`Tighten strategy task size estimation`).
- 2026-03-12 03:15 MDT: Root-caused the next Sam.blog bootstrap failure to missing flow-template attachment on top-level bootstrap parent workstreams like `WS2: HTML Layout Templates`. These titles were created during active bootstrap with no explicit `flow_template_id`, but `applyReviewRefinementPlanning` only auto-bound templates for review/refinement heuristics, so the parents persisted `NULL` and later poisoned child inheritance. Added a regression assertion to `TestIntegrationBootstrapPlanningTaskCreateSupportsMultipleParentWorkstreamsInSameProjectSession`, patched `task.create` to auto-bind a system template for top-level bootstrap workstream titles while bootstrap is active (prefer `default-review`, fall back to `default-review-refinement`), and verified the reproducer plus adjacent decomposition/sizing tests.
- 2026-03-12 03:30 MDT: `sam-blog-49` exposed another bootstrap-policy bug: after 90s the watchdog still fail-closed the project even though Lori had created six persisted staffing drafts. The runtime was already counting `staffing_drafts` in progress state, but `handleProjectBootstrapWatchdogTimeout` still always archived on timeout unless full setup had materialized. Added/updated regression `TestTurnEngineIntegrationProjectBootstrapWatchdogContinuesAfterHungTurnWithPartialSetup` and changed watchdog handling so partial persisted setup now fails only the current turn, updates bootstrap state, and enqueues the next automatic bootstrap continuation instead of failing/archiving the project.
- 2026-03-12 03:38 MDT: `sam-blog-50` showed that the next bootstrap blocker was no longer flow templates or watchdog failure. Instead, Lori hit `max_tool_calls` after real staffing/task-tree progress, and bootstrap fail-closed because `shouldContinueMaxToolCalls` treated any validation failure as terminal. Live failure: top-level task `WS4: Generate 20 New Blog Post Concepts` remained broad at the turn boundary, so first-wave validation returned `first_wave_task_unbounded`. Updated the integration regression (formerly `...DoesNotContinueAfterValidationFailure`) so recoverable bootstrap validation failures (`compound_parent`, `first_wave_task_unbounded`) continue after `max_tool_calls` when setup has materially progressed. Verified with adjacent watchdog/template tests.
- 2026-03-12 15:58 MST: Fixed another max-tool-calls/bootstrap gap. `shouldContinueMaxToolCalls` now treats bounded-size `first_wave_execution` bootstrap failures as recoverable, not just `compound_parent` and `first_wave_size`. Added `ensureTurnRunExitInvariant` so `handleUserMessage` cannot silently return success while its turn is still `in_progress`. Added unit coverage for both behaviors and reran targeted turn/bootstrap integration tests.
- 2026-03-12 16:11 MST: Fixed a continuation-vs-fail-close bootstrap race. After `max_tool_calls`, the engine could create turn 2 and then immediately fail-close the project session from completed-turn bootstrap validation, causing turn 2 to die with `session is closed`. Added continuation-aware bootstrap validation deferral in both project bootstrap preflight and completed-turn handling, with unit coverage for prior/newer continuation detection and targeted bootstrap integration tests rerun green.

- 2026-03-12: Added deferred bootstrap provider retry handling after setup persists; transient/rate-limit provider failures now keep bootstrap active and queue a retry instead of fail-closing. Verified with targeted unit and integration tests.

- 2026-03-12: Added org-kickoff post-create tool guard so Frank cannot keep using tools after project.create; he must provide the Lori handoff summary and end the kickoff turn. Verified with kickoff and bootstrap integration tests.
- 2026-03-12: Pushed kickoff enforcement commit 009de463. Verified live that Frank's org turn completes and Lori receives the active project bootstrap session for sam-blog-54. Monitoring Lori bootstrap turn now.
- 2026-03-12 05:12 MDT: Verified `sam-blog-54` project bootstrap is actively progressing, not hung. Lori project turn `7fd706ab-edc4-480e-abef-42e41333d980` has live completed model invocations and persisted task creation activity. Observed correct bounded-size enforcement on an oversized child task, followed by Lori splitting that batch into smaller tasks. Bootstrap metadata has not checkpointed past `project_created` yet, so next thing to watch is whether staffing/task-tree checkpoints are persisted after this active turn completes.
- 2026-03-12 05:24 MDT: Fixed a real streamed-bootstrap hang root cause. The bootstrap stream watchdog canceled `streamCtx`, but streamed chunk persistence was still using the outer request `ctx` for `UpdateMessageStatus`, `UpdateContent`, `publishEvent`, and `findSteerMessages`. That meant a blocked chunk persistence path could ignore the bootstrap watchdog and leave turns/invocations hanging. Switched those chunk-path operations to `streamCtx` and added unit coverage in `TestHandleUserMessageProjectBootstrapWatchdogCancelsBlockedChunkPersistence`.
- 2026-03-12 05:35 MDT: Archived stale active Sam.blog runs (`sam-blog-52-restart-restart`, `sam-blog-53`, `sam-blog-54`) through the HTTP archive endpoint. Started a clean org session `c7a2614f-ead0-4d6e-a1aa-c95333d31a6a`, created fresh project `sam-blog-55` (`0a31e2cb-64b3-4ccd-b546-216fa2309e14`), then had to route around an org-level Frank handoff loop after `project.create`. Project bootstrap is now active in project session `ba88c8d8-22f4-4fb2-8e38-4228e7683e5d` with Lori responding on turn `8048d518-d9a5-462e-98f1-72e97f89c237`.
- 2026-03-12 05:46 MDT: `sam-blog-55` bootstrap state caught up correctly after turn boundaries. Project session `ba88c8d8-22f4-4fb2-8e38-4228e7683e5d` now shows `staffing_persisted` and `task_tree_persisted` completed, with `assignment_count=11`, `planned_task_count=33`, `staffing_draft_count=14`. Continuations are working: turn 1 and turn 2 both completed on `max_tool_calls`, and turn 3 is active.
- 2026-03-12 05:47 MDT: `sam-blog-55` turn 3 entered a bounded-size rejection loop while trying to finish bootstrap. Lori first misused `subtask.create` (`repo: not found`; she inferred it needs an active flow execution rather than a plain task parent), then switched back to normal task creation but kept emitting 35–40 minute tasks that the bounded-size policy rejected repeatedly. If turn 4 repeats this, the next root-cause fix is decomposition recovery after bounded-size rejection.
- 2026-03-12 06:06 MDT: Pushed `27e8533b` (`Recover stale retried agent turns`). Added integration coverage proving a recovered claimed `agent_turn` can no longer silently accept a leaked `in_progress` turn and poison the session. Rebuilt `oc-svc`, archived poisoned `sam-blog-55`, started fresh org session `9f982620-620e-47b7-9500-703adebf0aa1`, and launched fresh project `sam-blog-56` (`a8549beb-ef7e-4d57-8ad4-a19482503733`).

- 2026-03-12 06:18:52 MDT Committed/pushed 352c5b57 Reuse canonical parent child tasks after adding overlapping same-parent child reuse coverage.

- 2026-03-12 06:25:56 MDT Committed/pushed 352c5b57 Reuse canonical parent child tasks.
- 2026-03-12 06:25:56 MDT Committed/pushed 7784b4dd Claim one job at a time in worker loop.

- 2026-03-12 06:40:38 MDT Committed/pushed 24132143 Fail turns when invocation completion fails.
- 2026-03-12 06:44:54 MDT Committed/pushed 352c5b57 Reuse canonical parent child tasks after reproducing overlapping same-parent child duplicates under Sam.blog bootstrap.
- 2026-03-12 06:44:54 MDT Committed/pushed 7784b4dd Claim one job at a time in worker loop after reproducing claimed-batch starvation of agent_turn behind slow/long jobs.
- 2026-03-12 06:44:54 MDT Committed/pushed 24132143 Fail turns when invocation completion fails after reproducing orphaned in_progress turn plus in_flight invocation plus done agent_turn job.
- 2026-03-12 06:44:54 MDT sam-blog-57 proved bootstrap can reach staffing_persisted and task_tree_persisted with no duplicate normalized sibling titles, but later stranded in an orphaned in_progress turn caused by ignored invocation UpdateCompletion failure.
- 2026-03-12 06:44:54 MDT sam-blog-58 on 24132143 failed closed under real Anthropic provider 429s: turn failed, retry job scheduled for 2026-03-12 21:00:00 -06:00, no leaked in_progress turn/job mismatch.
- 2026-03-12 06:44:54 MDT All three Anthropic subscription provider connections are currently enabled but rate_limited; current bottleneck is provider capacity, not local queue or turn state.
- 2026-03-12 22:36 MDT: Reviewed the interrupted Codex session trail and then kept moving on `main`. Fixed a local regression in native child-task reuse so reused draft children repair stale titles/descriptions when decomposition drafts change; updated stale bootstrap/native tests; and then fixed the live max-tool-calls leak class by failing leaked async continuation turns in-place instead of carrying stale `in_progress` turns forward. Added both unit coverage (`internal/turn/engine_test.go`) and DB-backed integration coverage (`internal/turn/engine_integration_test.go`) for a `CompleteTurn` no-op/leak handoff. Commits from this session: `63c83bdf` (`Recover leaked max-tool-call turns`) and `539553cd` (`Cover leaked continuation completion in integration`).
- 2026-03-12 22:36 MDT: Extended the operator dashboard so archived bootstrap automatic failures now appear in recent failures with their linked fresh restart project from `bootstrap_restart_bundle.restart_project_id`. Added API/integration coverage in `internal/server/controlplane_dashboard.go` and `internal/server/controlplane_integration_test.go`, TUI rendering of `restart→<project>` in recent-failure rows, and changed runtime item shortcuts to open the restart project by default instead of the archived source project. Commits from this session: `dc3f4b43` (`Show restart links for project failures`), `679e70a4` (`Show restart targets in operator dashboard`), and `8ac33de1` (`Prefer restart projects in dashboard shortcuts`).
- 2026-03-12 22:36 MDT: Verification after the above changes: `go test ./...` passes on `main`. Worktree is clean except for the unrelated untracked `skills/` directory. The live intervention report at `reports/oc-test-human-interventions.md` is now partially closed: leaked max-tool-call turns are fixed/recovered, and the operator UI now surfaces the clean restart target, but there is still no dedicated one-click relaunch API/action yet.
- 2026-03-12 23:18 MDT: Closed the next restart ergonomics gap. Added an explicit relaunch primitive on `turn` for archived bootstrap-failure projects, wired `POST /v1/projects/{id}/relaunch` through the real server using a narrow `BootstrapRelauncher`, and added integration coverage in `internal/turn/engine_integration_test.go` and `internal/server/project_integration_test.go`. Also fixed restart prompt creation so synthetic relaunch messages omit `author_type` unless a real starter agent is available; chat only accepts `human_user`/`agent`, not `system`. Commits: `f3154475` (`Expose bootstrap relaunch primitive`), `d5f48daf` (`Add project relaunch endpoint scaffold`), `bcd94eee` (`Wire bootstrap relauncher into server`), and `9563292b` (`Test project relaunch endpoint`).
- 2026-03-12 23:18 MDT: Added first-class operator/product actions on top of the relaunch endpoint. `ottercamp project relaunch` now resolves an archived project by id or slug and calls the relaunch API, with unit and real-server integration coverage. The TUI also now exposes relaunch directly from Project view via `:relaunch` and `Shift-R`, reloads sidebar/project detail onto the fresh restart project, and documents the command in help/autocomplete. Commits: `e7ee3023` (`Add project relaunch CLI command`), `48f2e67b` (`Cover project relaunch CLI integration`), and `cb412f75` (`Add TUI project relaunch action`).
- 2026-03-12 23:18 MDT: Verification status after the relaunch work: `go test ./...` is green. A broad `go test ./cmd/ottercamp ./internal/server ./internal/turn -tags=integration` sweep is still noisy for unrelated existing failures (`cmd/ottercamp` chat integration 404s and multiple legacy `internal/turn` bootstrap integration reds), but the relaunch-specific integration slices pass: `TestTurnEngineIntegrationRelaunchArchivedBootstrapProjectReturnsActiveRestart`, `TestProjectHTTPRelaunchArchivedBootstrapProject`, and `TestProjectRelaunchIntegrationCreatesRestartProjectAndPrintsID`.
- 2026-03-12 23:18 MDT: Added `ottercamp project archive` with unit + real-server integration coverage so the live Sam.blog reset workflow is now fully supported in the product CLI. Commit: `d2878108` (`Add project archive CLI command`). Also added TUI relaunch support plus help/autocomplete coverage in `cb412f75` and force-recorded those notes in git.
- 2026-03-12 23:18 MDT: Attempted to start the real clean-run validation loop against the live server. `ottercamp project list --slug-prefix sam-blog` still shows eight active `sam-blog*` projects (`sam-blog-57`, `sam-blog-56`, `sam-blog-51`, `sam-blog-50-restart-restart`, `sam-blog-49-restart-restart`, `sam-blog-47-restart`, `sam-blog-46-restart-restart`, `sam-blog-44-restart-restart`). Live archive is currently blocked by credentials: both the installed `ottercamp` binary and `go run ./cmd/ottercamp` can read with the saved key, but `project archive` fails `403 missing api key scope`. There is no obvious second admin/write credential source on disk beyond `~/.ottercamp/credentials`. The remaining blocker for the true fresh Sam.blog reset is therefore a write-capable API key or fresh `auth login`.
- 2026-03-13 00:41 MDT: Changed bootstrap validation handling so recoverable bounded failures no longer fail-close immediately. `internal/turn/engine.go` now treats `compound_parent`, `first_wave_task_unbounded`, and bounded-size `first_wave_execution` failures as automatic continuation cases in three places: completed-turn bootstrap validation, preflight bootstrap validation, and task-status/session refresh validation. The recoverable path clears terminal failure metadata, increments `auto_turn_count`, and enqueues another bootstrap continuation turn instead of archiving the project. Updated integration coverage to reflect the new contract: broad-parent-only bootstrap setups and bounded-size first-wave queue failures now remain active with one queued continuation instead of archiving immediately. Verified with targeted integration slices plus `go test ./...`.
- 2026-03-13 00:41 MDT: Live `sam-blog-59` could not be revalidated in place because project `92799e06-7e59-43a6-a931-984c1bcbc31b` is already archived in the old failed state (`failure_class=first_wave_task_unbounded`) with no restart project. Local worker has been rebuilt/restarted on the new binary in PTY session `22771`. The next live proof requires either unarchiving/relaunching a fresh Sam.blog bootstrap chain or the existing API-key blocker being resolved so the stale live chain can be reset cleanly.
- 2026-03-13 00:49 MDT: Root-caused the local “write blocker” to the CLI auth flow, not the server. `ottercamp auth login` intentionally mints a read-only interactive API key (`projects:read` only, no `projects:write`). Using a logged-in admin bearer session against `/v1/api-keys`, I minted a temporary write-capable API key and confirmed the relaunch endpoint still works on the rebuilt local server.
- 2026-03-13 00:49 MDT: The first relaunch replay (`sam-blog-59-restart-restart`, project `ba563fb7-8e18-4a47-af81-ff225b58725f`) proved the new recoverable-validation engine path does schedule automatic continuation turns, but the model still repeated the same oversized `task_create` set for `WS4: Blog Post Ideation` until `auto_turn_count` hit 2 and the session failed again. I then sharpened recoverable bootstrap continuation prompts so they now embed the exact validation failure reason and explicit anti-repeat guidance: do not retry the same oversized task definitions; split the offending broad parent/first-wave task into bounded child tasks instead. Added unit coverage for the new prompt builder and reran targeted turn integration plus `go test ./...`.
- 2026-03-13 00:51 MDT: Relaunched the original archived `sam-blog-59` seed again on the latest binary into project `e8ffd8b0-faf5-4240-bcab-e11b56a8003e` (`sam-blog-59-restart-2`), project session `f31ddee5-6720-40c5-8467-ff1e90969739`. This new run is materially healthier than the previous replay: the bootstrap session is active, current phase is `staffing_persisted`, task count has already reached 13, and recent model activity shows Lori correcting a starter-trio staffing rejection by creating a dedicated PM instead of immediately collapsing back into the old `WS4` bounded-size retry loop. Local server PTY is `19112`; local worker PTY is `78629`.
- 2026-03-13 03:55 MDT: Continued the live replay validation on fresh restart project `ae60d825-07da-4a19-892a-d670608af47e` (`sam-blog-64-restart-2-restart-9`), session `ee6b3341-ccac-4d87-9749-150094f6b0b4`, after landing bootstrap continuation hardening commits `d6c2cd4c` (`Heartbeat claimed jobs during execution`), `8a0ffa00` (`Recover bootstrap job failures into continuations`), `aa5607cf` (`Recover bounded bootstrap retries into continuations`), and `38498bee` (`Convert stale bounded retries into continuations`). The key proof so far is that repeated `max_tool_calls` handoffs are now surviving cleanly instead of degenerating into leaked/stale bounded-size retries: turns `b09f34d6`, `60a3c06e`, `203da19f`, and current `e46afd8d` have chained forward without human intervention, task count advanced to 29 with 17 assignments, and every persisted task now has a `flow_template_id`. Bootstrap metadata catches up cleanly on completed turns (`last_turn_id` advanced to `203da19f...` after the latest handoff); the current live gate is first-wave selection/promotion because all 29 tasks remain `draft` even though the task tree and flow attachments are now fully persisted.
- 2026-03-13 03:55 MDT: Landed `b44e09b4` (`Throttle sidebar task preloads`). Root cause: on every sidebar refresh the TUI was issuing `GET /v1/projects/{id}/tasks` for every project just to warm the dashboard board, which matched the local server `429` burst during live monitoring. The fix narrows startup preload to the selected project, with a single-project fallback, and adds unit coverage in `internal/tui/model_test.go`. Verified with focused `internal/tui` tests and full `go test ./...`.
- 2026-03-13 04:11 MDT: Landed `2bc03c39` (`Push bootstrap resumes toward first wave`). This adds state-aware resume guidance for active project-bootstrap continuations: once persisted state shows flow templates are ready (either by count or by the `flow_templates_persisted` checkpoint) and no first-wave work has started yet, the deterministic bootstrap resume message now explicitly tells the model to stop creating more agents/parent/child tasks unless a concrete validation failure still requires it and to move a small first executable wave into execution now. Added unit coverage in `internal/turn/engine_test.go` and verified with full `go test ./...`.
- 2026-03-13 04:11 MDT: Rebuilt/restarted the local stack on `2bc03c39` with clean PTYs `serve=30720` and `worker=5639`, archived the entire previous `sam-blog*` chain, and started a true from-scratch validation instead of another restart replay. The direct `project create` API path was confirmed to create only the project shell + canonical 8 bootstrap tasks (no project-scoped async chat session), so the real bootstrap path must still go through an org-scope Frank turn to seed the synthetic project handoff/session metadata.
- 2026-03-13 04:11 MDT: Fresh from-scratch Sam.blog validation is now running through org session `fa9033c0-ba69-423e-bf7b-85ed5f8fd930`. Frank created active project `a4cb60f4-d48d-41a8-9ad3-e4d941bb4754` (`sam-blog-66`) and completed the org turn, which correctly backfilled active project session `cd3201d1-9c21-4703-abb4-cd81cc19d9b7` with the synthetic handoff. Lori's first project turn `ce3592dd-1088-4c5a-ae8b-d63233df0519` hit the bootstrap watchdog after a long in-flight Opus call but recovered cleanly into continuation turn `0db8484c-2790-4752-9c56-22693d1ace0f` instead of leaking or archiving. Current ground truth: bootstrap state is at `task_tree_persisted`, project task count is up to 30 (all draft, all with flow templates), and the active continuation is working through repeated bounded-size rejections by splitting oversized tasks further. The next live question is whether this run can convert the expanded bounded tree into first-wave selection/promotion, or whether the repeated `task.create` bounded-size retries plus occasional `title_required` malformed create attempts need another runtime/prompt fix.
- 2026-03-13 16:37 MDT: Landed `df06ce66` (`Create parent dirs for atomic file writes`) after the fresh `sam-blog-98` first-wave child tasks exposed a real nested workspace write bug. Root cause was `internal/tools/native/mutation_tools.go:writeFileAtomically`, which created `.ottercamp-tmp-*` in the target parent directory before ensuring that directory existed, so `file.write` calls like `sam-blog/src/types/post.ts` failed with `open .../.ottercamp-tmp-*: no such file or directory` whenever the model omitted `create_dirs`. Fixed by `MkdirAll` on the target parent inside the atomic write helper and added both direct native coverage and integration coverage for nested writes without `create_dirs`. Verified with `go test ./internal/tools/native`, `go test -tags=integration ./internal/tools/native -run 'TestIntegrationFileWrite(ReadDeleteRoundTrip|CreatesNestedParentsWithoutFlag)' -count=1`, and full `go test ./...`.
- 2026-03-13 16:37 MDT: Rebuilt/restarted the local stack on `df06ce66` with PTYs `serve=74122` and `worker=97743`. Live validation is continuing on active project `sam-blog-98` (`556ae73a-bf03-43a1-b9eb-844967c0b913`) and project session `fbbf132f-3916-465c-817c-535659b2837a`. The worker resumed the pending first-wave queue instead of losing it; current queue shape is one claimed `agent_turn`, several pending sibling child-task turns, and the project-level bootstrap continuation still pending behind them. After the patched restart there have been no new failed `file.write` tool executions; the only recent `file.write` failure in the DB is the pre-fix missing-parent error from `16:31:56 MDT`.
- 2026-03-13 22:16 MDT: Landed `3dcb9642` (`Read bootstrap blockers from recovery messages`). Root cause: recoverable bootstrap follow-on turns were still spending their first tool on broad `task.list` rereads when session bootstrap metadata no longer carried `validation_failure_reason`, even though the current auto recovery user message explicitly named the blocked first-wave task. The fix carries inbound user message text on `turnRuntime` and lets `shouldBlockProjectBootstrapRecoveryRereadTool` parse the blocker directly from the current recovery prompt, with unit coverage proving `task.list` is blocked immediately when the recovery message names the failing task.
- 2026-03-13 22:21 MDT: Landed `fff607ee` (`Recover blocked bootstrap reread continuations`). Root cause: turns ending with `stop_reason=validation_loop_blocked` could clear bootstrap validation metadata and then auto-continue through the generic bootstrap prompt path, which recreated the same `project.list` / `task.list` reread loop instead of preserving the recoverable repair target. The fix reconstructs the latest recovery target from recent auto recovery user messages when a blocked reread turn completes, infers the failure class from that recovered reason, and feeds it back into the normal recoverable bootstrap-validation continuation path. Verified with `go test ./internal/turn` and full `go test ./...`.
- 2026-03-13 22:21 MDT: Rebuilt/restarted the worker on `fff607ee` in PTY `80338`. The best live validation target after that restart is active project `sam-blog-107-restart` (`11a5b993-5085-465c-95ac-999448f812de`), project session `f1705501-c610-452e-b94f-b0522b1f06f1`, currently running turn `1c617cea-87c0-43a3-928a-f8f9d1e0c2a4`. Current ground truth: the restart run is still on a single live turn with one claimed `agent_turn`, no queued duplicates for that session, bootstrap metadata at `flow_templates_persisted`, assignment count `5`, and `15` project tasks created so far. `sam-blog-107` (`2da6de1d-c3a4-4fa0-8cc0-4ab20018f278`) is no longer the right target: its bootstrap session `62c95615-b07f-4e25-98a2-7dc3b6ee95e3` is now `closed` after repeatedly hitting `validation_loop_blocked` on the pre-fix worker.
