# Spec 0326A: Reduce Runaway Token Usage Without Making OtterCamp Worse

Date: 2026-03-26

Primary supporting analysis:
- [`issues/0326a-state.md`](./issues/0326a-state.md)
- [`docsv2/visual/02-chat-flow-runtime.html`](./docsv2/visual/02-chat-flow-runtime.html)

## Purpose

OtterCamp is spending far more model tokens than the human testing volume justifies. The goal of this spec is to reduce runaway token usage by making task boundaries real in runtime behavior, not just in task metadata, while preserving product quality and completion reliability.

This is not a cost-cutting spec in the abstract. It is a runtime-correction spec. The system is currently paying for too many model cycles, too much repeated context, and too much soft-rejection churn inside async project and task execution.

## Problem Statement

The current runtime permits long, expensive turns that repeatedly reread expanding prompt history while probing blocked or redundant actions.

Observed in live data:

- last 24 hours completed usage: about `164.6M` tokens
  - `92.9M` input
  - `3.95M` output
  - `67.7M` cache-read
- last 24 hours failed invocations: `3,051`
- last 24 hours explicit rate-limit failures: `1,524`
- one representative project turn consumed `31` completed `agent_turn` invocations and grew from `494` input tokens to `59,015` input tokens inside a single turn
- one representative task turn consumed `46` completed model calls and `1,889,087` tokens
- completed `listening_eval` in last 24 hours:
  - `376` invocations
  - `13.5M` tokens
  - almost entirely on async `project` sessions
- summarization in last 24 hours:
  - `113` completed
  - `902` failed

The important conclusion is that OtterCamp already has tasks and subtasks as decomposition objects, but they are not behaving as reliable bounded execution containers.

## Goals

1. Reduce unnecessary model cycles in async `project` and `project_task` execution.
2. Make task boundary enforcement happen earlier and more deterministically.
3. Prevent repeated blocked-tool and recovery-target loops from burning full model rounds.
4. Remove optional background model work from hot async execution paths unless there is a strong product reason to keep it.
5. Make internal usage accounting match provider-facing reality by including cache-read tokens.
6. Preserve or improve completion quality on rerun-style canaries.

## Non-Goals

1. Do not globally shrink all prompt context without scope-specific evidence.
2. Do not weaken review semantics, flow ownership, or recovery guarantees.
3. Do not replace Claude with cheaper models as the primary strategy.
4. Do not introduce a permanent multi-agent internal dialogue loop as part of this fix.
5. Do not rely on manual DB repair or operator intervention to make the new behavior look successful.

## Design Principles

1. Prefer hard runtime boundaries over advisory prompts.
2. Stop repeated dead ends early instead of letting the model rediscover the same rejection.
3. Scope changes to async execution paths first.
4. Keep sync human chat quality stable unless the same issue appears there too.
5. Use measured guardrails, not a blind global cap.
6. Make every major runtime reduction observable in metrics and logs.

## Current Boundary Model

Today task boundary is attempted through a stack of partial controls:

- prompt scoping:
  - `project_task` sessions get task-specific context, acceptance criteria, flow-step state, and review-mode instructions
- execution binding:
  - tool broker binds a `project_task` session to its exact task and project IDs
- mutation guards:
  - project sessions are blocked from mutating active task lanes
  - task sessions are blocked from directly forcing flow-owned status changes
  - review lanes are blocked from normal implementation writes and CLI execution
  - task lanes are blocked from direct `git.commit`
- lifecycle cleanup:
  - orphaned or terminal async `project_task` sessions are closed automatically

That architecture is directionally correct, but in practice most enforcement still happens as tool-time rejection after the model has already spent reasoning budget and reread more context.

## Why Task Boundary Still Leaks

The current abstraction leaks in five ways:

1. The model is allowed to keep trying after many blocked tool results.
2. Tool rejections happen too late to prevent prompt growth inside the same turn.
3. Async `project` sessions pay for extra `listening_eval` calls by default.
4. Prompt assembly can enqueue background summarization once unsummarized history crosses a low threshold.
5. Some task turns mix real work with repeated tool friction and recovery-target confusion, which inflates them dramatically.

## Scope Of This Spec

This spec covers:

- async `project` turn control
- async `project_task` turn control
- tool-surface enforcement for project, work, and review lanes
- repeated-blocker cutoffs
- `listening_eval` policy for async execution
- summarization enqueue policy for active async execution
- usage accounting and operator-facing token observability

This spec does not cover:

- broad product redesign
- dual-agent execution architecture
- memory-system redesign
- provider pricing strategy

## Workstream 1: Make Task Boundary Enforcement Earlier And Harder

### Goal

Move from “soft tool-time rejection after the model has already spent a round” toward earlier and narrower runtime control of what each lane is even allowed to do.

### Required changes

1. Narrow tool availability or preflight tool eligibility by session scope and lane type.
   - async `project` lanes with executable tasks should not present or execute deliverable-writing paths outside bootstrap/planning-safe areas
   - async `project_task` review lanes should not present or execute `cli.execute`, `git.commit`, or non-review `file.write` actions
   - async `project_task` execution-first tasks should enforce explicit deliverable-path targeting before write attempts leave the model loop

2. Promote existing guardrails into stronger boundary controls where possible.
   - existing guards such as:
     - `task_lane_owned_by_project_task_session`
     - `task_execution_required`
     - `task_git_commit_blocked`
     - `review_action_required`
     - `deliverable_path_required`
     - orchestration-only parent guards
   - should be used not only as tool responses, but as early routing signals

3. Make review, work, and orchestration lanes visibly distinct in runtime behavior.
   - work lanes write deliverables
   - review lanes inspect and decide
   - project lanes decompose, queue, assign, and recover

### Primary files

- [`internal/controlplane/broker.go`](./internal/controlplane/broker.go)
- [`internal/tools/native/mutation_tools.go`](./internal/tools/native/mutation_tools.go)
- [`internal/turn/engine.go`](./internal/turn/engine.go)
- [`internal/prompt/assembler.go`](./internal/prompt/assembler.go)

## Workstream 2: Add Repeated-Blocker And Repeated-Recovery Cutoffs

### Goal

Stop paying for repeated rediscovery of the same blocked state.

### Required changes

1. Add per-turn blocker fingerprint tracking.
   - fingerprint should include at least:
     - tool name
     - normalized error family
     - lane type
   - examples of blocker families to classify:
     - `task_lane_owned_by_project_task_session`
     - `task_execution_required`
     - `flow_owned_status_blocked`
     - `review_action_required`
     - `deliverable_path_required`
     - `project_session_meta_task_disallowed`
     - orchestration-only parent guard
     - `blocks_scope=all` create rejection

2. Add deterministic early-stop thresholds.
   - same fingerprint twice in one turn should normally stop the turn
   - same blocker family three times in one turn should always stop the turn

3. Add repeated recovery-target / redirected write detection.
   - if the same task lane keeps being redirected to the same recovery target or keep writing the wrong path family, stop and convert to a fresh recovery continuation rather than paying for more retries inside the same turn

4. Emit explicit structured stop reasons so these cases are measurable.

### Product requirement

Stopping must not silently discard real work. The runtime must convert the blocker into a fresh continuation or explicit recovery prompt when appropriate.

### Primary files

- [`internal/turn/engine.go`](./internal/turn/engine.go)
- [`internal/tools/native/mutation_tools.go`](./internal/tools/native/mutation_tools.go)

## Workstream 3: Replace One Global Turn Budget With Scope-Aware Guardrails

### Goal

Prevent a single async turn from becoming a token furnace.

### Required changes

1. Replace broad reliance on the global `defaultMaxToolCalls = 75` with scope-aware budgets.

2. Add configurable starting defaults for async execution:
   - async `project` turns:
     - maximum model/tool cycles: `8`
     - prompt-input guardrail: `25k` tokens
   - async `project_task` work turns:
     - maximum model/tool cycles: `16`
     - prompt-input guardrail: `35k` tokens
   - async `project_task` review turns:
     - maximum model/tool cycles: `6`
     - prompt-input guardrail: `20k` tokens

3. Make these values configuration-backed and observable, not hard-coded forever.

4. When a guardrail is hit:
   - do not simply fail
   - emit a structured continuation reason
   - continue in a fresh turn with narrower history where possible

### Why these are starting values

They are not “ideal truth.” They are conservative starting caps meant to break the worst runaway behavior while still allowing bounded multi-step work.

### Primary files

- [`internal/turn/engine.go`](./internal/turn/engine.go)
- [`internal/prompt/assembler.go`](./internal/prompt/assembler.go)

## Workstream 4: Remove Listening Eval From Async Execution Paths

### Goal

Stop paying an extra model call for “should I wait?” in places where there is no real conversational ambiguity.

### Required changes

1. Disable `listening_eval` for async `project` sessions.
2. Keep it disabled for async `project_task` sessions.
3. Retain `listening_eval` only for sync human conversational sessions, or other explicitly justified lanes.

### Expected outcome

- zero `listening_eval` invocations on async `project`
- zero `listening_eval` invocations on async `project_task`

### Primary files

- [`internal/turn/engine.go`](./internal/turn/engine.go)

## Workstream 5: Make Summarization Overload-Aware And Async-Aware

### Goal

Stop background summarization from contesting capacity during hot execution paths and provider distress.

### Required changes

1. Do not enqueue prompt-assembly summarization for active async `project` or `project_task` sessions.
2. Suppress summarization enqueue when the selected provider connection is rate-limited.
3. Add session-level backoff after summarization failures.
   - repeated summarization failure for the same session should delay the next summarize enqueue attempt for a meaningful interval
4. Keep dedupe behavior, but make it robust against repeated enqueue/fail/re-enqueue churn.
5. Add regression coverage ensuring summarization uses current valid model profiles.

### Important note

The issue is not that summarization is conceptually bad. The issue is that it is currently being requested from the same growth path that is already making active async turns expensive.

### Primary files

- [`internal/prompt/assembler.go`](./internal/prompt/assembler.go)
- [`internal/chat/summarization.go`](./internal/chat/summarization.go)
- [`internal/jobqueue/worker.go`](./internal/jobqueue/worker.go)

## Workstream 6: Fix Usage Accounting And Add Token Diagnostics

### Goal

Make OtterCamp’s own accounting and dashboards reflect the real load being sent upstream.

### Required changes

1. Include `cache_read_tokens` in all budget and usage totals.
2. Add operator-visible per-turn metrics:
   - completed model calls per turn
   - total tokens per turn
   - repeated blocker family count
   - whether the turn hit a configured guardrail
3. Add operator-visible per-session metrics:
   - listening_eval count
   - summarization enqueue count
   - summarization failure count
4. Add a simple report/query surface for top token-burning sessions and turns.

### Primary files

- [`internal/budget/service.go`](./internal/budget/service.go)
- [`internal/model/cost_query.go`](./internal/model/cost_query.go)
- server/dashboard or report surfaces as appropriate

## Workstream 7: Canary Validation And Rollout

### Goal

Prove the runtime is cheaper and cleaner without degrading outcome quality.

## Execution Order

Implementation should land in this order, with tests after each slice:

1. Accounting truth first
   - include `cache_read_tokens` in budget and rollup math
   - add per-turn / per-session reporting hooks where straightforward
   - reason: measure the real load before changing runtime behavior

2. Remove optional async execution model calls
   - disable `listening_eval` for async `project`
   - keep it disabled for async `project_task`
   - reason: high-confidence reduction with low product risk

3. Stop background pressure on hot async sessions
   - suppress prompt-assembly summarization for active async `project` and `project_task`
   - suppress summarize enqueue while provider is rate-limited
   - add summarize backoff after repeated failure

4. Add repeated-blocker and repeated-recovery cutoffs
   - detect repeated blocked tool families and stop early
   - detect repeated recovery-target misfires and convert to a fresh continuation

5. Add scope-aware per-turn budgets
   - async `project`
   - async `project_task` work
   - async `project_task` review

6. Narrow tool surfaces further if the canary still drifts
   - only after the earlier slices land and are measured

7. Ship operator-facing token diagnostics in-product
   - expose a first-class CLI report for recent model invocation usage, including cache reads, top sessions, top turns, and failure pressure
   - keep the shell script as a convenience wrapper, but make the primary operator path versioned and test-covered in the product itself

Each slice should be validated on focused tests first and then on the rerun-style canary.

### Implementation Status

Completed so far:

1. Accounting truth first
   - `cache_read_tokens` now count in budget and rollup totals

2. Remove optional async execution model calls
   - `listening_eval` is disabled for async sessions
   - that now includes `project`, `project_task`, and `organization` async lanes

3. Stop background pressure on hot async sessions
   - prompt-assembly summarization is suppressed for active async `project` and `project_task`
   - summarize enqueue now defers behind summary-provider rate-limit backoff in prompt assembly, turn runtime, and session close
   - repeated summarization failures now record session-level backoff and defer later summarize enqueue attempts for that session
   - explicit provider `retry_after` windows are now persisted on provider connections so rate-limited Anthropic backoff survives process boundaries and worker restarts

4. Early blocker handling, partial
   - common blocked async `project` mutation families now stop the turn early instead of burning more rounds
   - async `project` lanes now preflight-block `subtask.create`; that tool belongs only inside a running task execution with an active `flow_node_execution_id`
   - async `project_task` turns now stop after the second identical deterministic validation failure within the same turn
   - that repeated same-turn cutoff now explicitly covers `cli.execute` shell-injection denials and `file.read` `not_found` misses using attempt fingerprints scoped to the exact command or path
   - that same-turn cutoff now also covers repeated `file.list` `not_found` misses with path-aware fingerprints, plus repeated task-lane `git.commit -> task_git_commit_blocked` failures
   - repeated task-lane broad-context probes (`task.list`, `memory.query`) and repeated `file.read -> mismatched_deliverable_context` failures now also enter the same-turn cutoff path
   - repeated `file.edit -> old_string_not_found` failures now also enter the same-turn cutoff path with file/path-aware edit fingerprints
   - async `project_task` lanes now preflight-block `subtask.create` when the session has no active `flow_node_execution_id`
   - when an async `project_task` lane does have an active `flow_node_execution_id`, missing `subtask.create` arguments are hydrated from the bound session execution instead of burning a correction round
   - repeated same-turn recovery-focus failures now persist the named deliverable checkpoint before stopping
   - recovery turns that already carry a repeated target-drift checkpoint now stop on the first new focus miss across explicit resume attempts instead of paying another rediscovery cycle
   - recovery turns now also stop after the second read-only discovery cycle in the same turn instead of rereading artifacts/context repeatedly without attempting the deliverable write
   - recovery turns that already carry an empty-write checkpoint now stop on the first repeated `file.write`-without-`content` retry across explicit resume attempts instead of paying another correction round
   - recovery turns that already carry an empty-command checkpoint now stop on the first repeated `cli.execute`-without-`command` retry across explicit resume attempts instead of paying another correction round
   - recovery file-output context now treats the checkpoint target path itself as sufficient recovery context, even when no artifact or target draft exists yet, so empty-command recovery halts can fire from durable checkpoints instead of falling back to another correction round
   - ordinary async `project_task` execution lanes now preflight-block `task.create`; only explicit orchestration-only parent tasks may decompose further, and only beneath themselves via `parent_task_id = current_task`
   - blocked task-lane decomposition attempts now end the turn immediately instead of paying for another model round
   - existing third-strike blocked-task behavior remains intact
   - broader repeated-recovery cutoffs are still pending beyond the shipped focus-failure, repeated-target-drift, repeated empty-mutation, and same-turn repeated read-only discovery stops

5. Scope-aware per-turn budgets, first pass
   - async `project` turns now cap at `8` tool/model cycles and `25k` prompt tokens
   - async `project_task` review turns now cap at `6` tool/model cycles and `20k` prompt tokens
   - async `project_task` work turns now cap at `16` tool/model cycles and use a `35k` prompt-token ceiling as an upper bound
   - explicit lower runtime overrides still win

6. Provider cooldown routing, first pass
   - when every eligible connection for a routed profile is still inside a persisted rate-limit cooldown window, the router now returns a rate-limited backoff error instead of selecting one and burning another guaranteed 429
   - live model calls now translate that router-level cooldown into the normal turn-level `ErrRateLimited` retry path
   - `provider_connection.metadata.health_rate_limited_until` is cleared automatically once the connection is marked healthy or otherwise moved out of rate-limited state
   - streamed `agent_turn` invocation rows now preserve provider failure classification for 429s instead of flattening them into generic `model_error`
   - active async `project` sessions now preserve the latest turn-level rate-limit backoff when worker repair requeues a project bootstrap / continuation lane with no current turn
   - deferred project-bootstrap provider failures now enqueue the next retry with an incremented `retry_count` and a rate-limit-aware `run_after` instead of falling back to an immediate or generic retry
   - fresh live Anthropic cooldown refusals now record `error_code=provider_rate_limited` instead of flattening back to `model_error`
   - the future-dated project-bootstrap retry now survives runtime restarts without degrading back into a one-minute repair storm
   - `callMainModel(...)` no longer retries `ErrRateLimited` inside the same turn, so a router/provider cooldown refusal now burns one failed invocation row and one delayed turn retry instead of multiple same-turn invocation attempts

Still pending from this spec:

- broader repeated-recovery fingerprint cutoffs beyond focus / target-drift / repeated empty-mutation / repeated read-only discovery failures
- stronger tool-surface narrowing where canary drift still survives beyond the new task-lane `task.create` boundary

Operator tooling now available:

- `scripts/token-usage-report.sh` provides a lightweight direct-to-Postgres token report with:
  - overall token totals including cache reads
  - purpose / model / provider breakdown
  - top sessions
  - top turns
  - most common failures
- `ottercamp db token-usage` now exposes the same core breakdowns inside the product surface with `table|json|quiet` output modes

### Rollout order

1. Instrumentation and accounting fix
2. `listening_eval` removal for async execution
3. summarization suppression/backoff for async execution and rate-limited providers
4. repeated-blocker and repeated-recovery cutoffs
5. scope-aware turn budgets
6. stronger tool-surface narrowing if the canary still drifts

### Validation method

Use the same rerun-style canary pattern and compare:

- completed model calls per project turn
- completed model calls per task turn
- total tokens per settled task
- total tokens per settled project session
- rate-limit failures per hour
- completion quality for verification, work, and review lanes

## Acceptance Criteria

### Runtime behavior

1. Async `project` and async `project_task` sessions never invoke `listening_eval`.
2. Repeated blocked tool patterns stop the turn deterministically instead of consuming many additional model rounds.
3. Async turns use scope-aware budgets rather than the current one-size-fits-all ceiling.
4. Review lanes cannot continue implementation work via normal write/CLI paths.
5. Project lanes cannot continue deliverable-writing once executable task lanes exist.

### Usage/accounting

6. Budget totals include `cache_read_tokens`.
7. Operators can identify top token-burning sessions and turns without ad hoc SQL spelunking.

### Canary outcome

8. A rerun-style validation canary completes without:
   - any async `project` turn exceeding `8` completed `agent_turn` invocations
   - any async `project_task` turn exceeding `16` completed `agent_turn` invocations unless the runtime explicitly created a fresh continuation boundary first
9. The canary completes on Anthropic-only routing without needing local-model fallback.
10. The canary’s completion quality is not worse than current behavior:
   - work still lands
   - review still approves/rejects correctly
   - runtime still owns commit/flow completion

## Test Plan

### Unit tests

- blocker fingerprint normalization and stop-threshold tests
- scope-aware budget selection tests
- `listening_eval` gating tests
- summarization backoff and provider-rate-limit suppression tests
- budget sum tests including `cache_read_tokens`

### Integration tests

- async `project` turn does not create `listening_eval` invocation rows
- repeated blocked tool result in one turn produces fresh continuation instead of more retries
- review lane rejects implementation writes and CLI paths
- project lane rejects deliverable writes once executable task lanes exist
- summarization does not enqueue on active async sessions
- summarization does not enqueue when provider is rate-limited

### Canary validation

- run a fresh rerun-style validation project
- record before/after metrics in the operator log and state report

## Risks

1. Over-tight budgets could cut off legitimate long-running execution before useful work is done.
2. Removing `listening_eval` from async project sessions could expose a small number of real wait-for-human cases that currently piggyback on it.
3. Suppressing summarization too aggressively could allow prompt bloat in long-lived sync conversations.
4. Stronger tool-surface narrowing could break implicit workflows that currently work only because the model can try too many things.

## Risk Mitigations

1. Scope changes to async execution first.
2. Make guardrails configurable and observable.
3. Roll out in the order listed above instead of landing all changes blindly.
4. Validate on the same canary family that exposed the problem.

## Implementation Status

Implemented so far in this spec:

- budget/accounting now includes `cache_read_tokens`
- async `project` `listening_eval` is disabled
- active async `project` and `project_task` sessions suppress summarization enqueue, and summarization defers behind provider cooldown
- background summarization jobs now self-defer behind session backoff and provider cooldown without consuming a worker attempt:
  - `chat_summarize`
  - cleanup `summary_consolidation`
- async scope-aware prompt/tool budgets are in place for:
  - async `project`
  - async `project_task` review
  - async `project_task` work
- repeated deterministic same-turn validation failures now stop early for multiple high-cost blocker families
- recovery resumes now stop early on repeated focus drift, repeated target drift, repeated empty mutations, and repeated read-only rediscovery
- provider cooldown telemetry and retry scheduling are preserved across turn failure and runtime restart
- same-turn retries on `provider_rate_limited` are removed
- async execution lanes (`project_task` and non-bootstrap `project`) no longer spend same-turn model retries on ordinary provider-transient failures:
  - they now fail the hot turn immediately
  - enqueue a delayed cross-turn retry with bounded backoff
  - keep sync turns and active project bootstrap on the old path
- async task lanes now also stop early on repeated successful rewrites of the same existing file when the runtime sees identical `file.write` output path plus `byte_size` churn in one turn
- async task lanes now also stop early on repeated `cli.execute` package-install churn for the same package spec in one turn instead of spending multiple rounds on `pip` / `python -m pip install` variants
  - the same cutoff now also stops on the third observed same-package install even when the model interleaves failed import-check probes between those install attempts
- async task lanes now also stop early on repeated shell-based `cli.execute` file construction targeting the same `scripts/`, `config/`, or `results/` path in one turn instead of wrapping more `cat >`, `printf >`, or `python -c "with open(...)"` builders around the same file
- async task lanes now also stop early on repeated rereads of the same shell-built `scripts/`, `config/`, or `results/` file in one turn:
  - successful `file.read` on the same exact built path
  - successful read-only `cli.execute` inspection of the same exact built path, such as `cat`, `sed`, `head`, `tail`, `grep`, `rg`, `wc`, `stat`, or `file`
  - the guard only activates once the turn has already successfully shell-built that exact target, so ordinary deliverable reads do not get swept into this cutoff
- async task lanes now also stop early on repeated rereads of the same recently written `scripts/`, `config/`, or `results/` file in one turn:
  - the guard activates only after a successful `file.write` to that exact path
  - repeated `file.read` or read-only `cli.execute` inspection of that unchanged target will now stop on the third reread
  - a fresh successful write to that same path resets the reread counter, so iterative edit-then-check loops can still proceed
- `scripts/token-usage-report.sh` now surfaces:
  - hot turns with repeated rate-limit failures
  - duplicate successful `file.write` churn grouped by turn/path/byte-size
  - repeated package-install attempts grouped by turn
  - repeated readbacks of the same successfully written file grouped by turn/path/source
  - repeated script execution grouped by turn/path
  - shell file-build / readback churn grouped by turn
  - completed turns grouped by `stop_reason`
  - `listening_eval` grouped by session scope/mode
  - sessions with active summarization backoff metadata
  - provider connection health plus `health_rate_limited_until`
  - `provider_rate_limited` failures split into pre-routing vs post-routing
  - fractional `--hours` windows for short live checks such as `--hours 0.25`
  - optional `--session <uuid>` filtering so the same report can isolate a single canary session end-to-end
  - pending `agent_turn` backlog grouped by session with:
    - oldest/newest `run_after` and `created_at`
    - `current_turn_id`
    - `is_paused`
    - `stale_project_source`
    - derived `backlog_state`
- `ottercamp db token-usage` now also exposes the most useful live-ops sections from that shell report directly in the product surface:
  - completed turns grouped by `stop_reason`
  - provider connection health including persisted `health_rate_limited_until`
  - `provider_rate_limited` failures split into pre-routing vs post-routing
  - pending `agent_turn` backlog grouped by session with `current_turn_id`, pause state, stale-source detection, and derived `backlog_state`
  - repeated package-install attempts grouped by turn with attempted specs
  - shell file-build / readback churn grouped by turn with path hints
- claim-time worker cleanup now retires project dispatches that were already permanently unclaimable under existing claim SQL:
  - inactive `project_bootstrap` dispatches
  - settled `project_execution_continuation` / `project_continuation_resume` dispatches with no unfinished tasks
- worker-side async project requeue paths now retire settled continuation messages before they become repeat queue churn:
  - `RequeueActiveProjectSessionsWithoutTurns(...)` now fails a pending `project_execution_continuation` message in place when the project has zero unfinished tasks, instead of creating another minute-by-minute claim/dead-letter loop
  - `RequeueStrandedUserMessageTurns(...)` applies the same retirement rule for pending continuation messages that have no turn history yet
  - the retirement reason is explicit on the message row: `project continuation no longer needed; all project tasks settled`
- claim-time worker cleanup now also retires orphaned stale `project_task` execution dispatches:
  - pending or claimed `agent_turn` rows older than the stale-claim threshold
  - active async `project_task` session
  - active `flow_node_execution`
  - `current_turn_id IS NULL`
  - and no matching `chat_turn` exists for that message attempt
  - after purging those rows, the worker immediately reruns `RequeueActiveExecutionSessionsWithoutTurns(...)` so current state can mint a fresh dispatch instead of leaving the session parked behind the dead backlog
- async `project` and `project_task` turns now preflight model availability before prompt assembly:
  - the engine asks the live gateway whether the routed profile is already in an all-connections-cooling-down window
  - on a real router-level cooldown, the turn now goes straight to the existing delayed `ErrRateLimited` retry path without assembling prompt context, appending an assistant placeholder, or creating a no-op `agent_turn` invocation row
  - non-rate-limit probe failures fall back to the normal model path so this slice only changes the known cooldown case
- async `project` and `project_task` dispatch now also preflight model availability before turn creation:
  - when the routed provider is already cooling down, `handleUserMessage(...)` now reschedules the retry and appends a session-scoped system message without calling `CreateForMessageAttempt(...)`
  - that removes the last known throwaway `chat_turn` churn on cooldown retries
  - the dispatch attempt tracks whether availability was already probed so normal fallback execution does not pay a second probe on non-rate-limit errors
  - async `organization`, `project`, and `project_task` cooldown preflight now continues to apply even after the old `maxRateLimitRetries` threshold:
    - queued cooldown retries can legitimately exceed that counter because the worker keeps rolling them forward during provider backoff windows
    - late-stage async retries now still defer before turn creation instead of minting throwaway failed turns with no invocation
- worker recovery now skips requeueing `project_task` async sessions that are already blocked by a persisted validation-loop guard:
  - active flow-node executions with `work_status='blocked'` plus `agent_turn_validation_guard.blocked=true` no longer get revived by `RequeueActiveExecutionSessionsWithoutTurns(...)`
  - that removes hot queue churn where the worker would enqueue another `agent_turn` only for the turn engine to immediately suppress it as `validation_loop_blocked`
- paused projects no longer leak async `project_task` recovery dispatches back into the runnable queue:
  - `RequeueActiveExecutionSessionsWithoutTurns(...)` now suppresses paused parent projects
  - claim-time cleanup now retires already-due paused `agent_turn` rows for both async `project` and async `project_task` sessions
  - resume behavior is preserved because the existing requeue passes recreate fresh dispatches once the project pause flag is cleared
- tool-result parsing and operator diagnostics now treat native validation failures stored in `output.error` as real tool errors instead of silently classifying them as successes
- async `project_task` work lanes now have a session-level cutoff for cross-turn read-only discovery churn:
  - after `5` consecutive `max_tool_calls` turns
  - when every turn is limited to read-only discovery tools such as `file.list`, `file.read`, `file.search`, `git.log`, `git.diff`, `git.status`, `task.get`, `project.get`, `flow.get_template`, and `flow.get_execution`
  - the same cutoff now also recognizes read-only `cli.execute` turns by parsing persisted assistant `tool_calls` metadata and allowlisting inspection-only shell commands such as `pwd`, `ls`, `cat`, `rg`, `git diff`, and `git log`
  - the runtime blocks the task lane instead of auto-continuing another discovery-only pass
  - review lanes are no longer exempt from that cutoff; their threshold is tighter at `3` consecutive `max_tool_calls` turns, and when review churn hits the same pattern, the runtime ends the hot turn and queues a fresh `task_review_action` prompt instead of letting review keep burning `max_tool_calls`
  - the `max_tool_calls` budget-hit path now routes through the same cutoff helper inside `dispatchTools(...)`, so capped review turns cannot skip the classifier just because the stop happened before the outer turn-finalization branch
- async `project_task` wrong-path `file.write` correction is now narrower:
  - the engine still canonicalizes obviously equivalent or generic wrong paths to the intended task deliverable
  - but it no longer silently rewrites across strong artifact-family boundaries such as `tests/` or `results/` into a code deliverable under `src/`
  - that keeps auxiliary test/result artifacts from clobbering the primary deliverable path and pushes those attempts back through normal deliverable-bound validation instead
- async `project_task` lanes now also stop early on repeated same-turn read-only discovery rounds:
  - it fingerprints pure discovery-only rounds using persisted assistant `tool_calls` metadata
  - it allows an initial discovery pass plus one repeat, then stops on the third discovery-only round in the same turn instead of paying for another `max_tool_calls` continuation
  - it only engages when the turn has no mutation tools anywhere in its assistant tool-call history, so mixed inspect-then-edit turns do not fall into this cutoff
  - when the lane is a review lane, the same cutoff now routes into the review retry path with fresh `task_review_action` guidance instead of generic “take a concrete mutation step” messaging
- review-lane empty-output churn now has a real brake:
  - empty review turns no longer bypass the normal retry ceiling
  - after `3` consecutive review turns in the same session return the empty-output retry path, the runtime blocks the lane instead of auto-retrying another blank/tool-only review pass
  - that specifically targets the hot Anthropic review family where repeated inspect-only turns kept appending `Review turn returned empty assistant output` without ever emitting `flow.review_decision`
- review lanes at the retry ceiling no longer auto-continue on `max_tool_calls`:
  - when an async `project_task` review turn is already at `maxGenericRecoveryReplyRetries`, `shouldContinueMaxToolCalls(...)` now returns false
  - that prevents the runtime from creating a fresh `task_continuation_resume` moments before the review-completion handler blocks the lane for repeated non-decision output
- task-lane file-write canonicalization no longer crosses `scripts/` and `config/` families:
  - `handleTaskFileWriteWrongPath(...)` now classifies `scripts/` and `config/` as distinct artifact families
  - that prevents execution-first task rewrites from silently redirecting a script write like `scripts/pipeline_config.py` into a config target such as `config/pipeline-config-invalid.yaml`
- review action prompts now surface the preferred deliverable target when one exists:
  - `buildTaskReviewActionPrompt(...)` tells the model to inspect the concrete target path directly before broad workspace discovery
  - that is aimed at the surviving review turns that still spend their early budget on root `file.list` / generic repo inspection before opening the one actual deliverable
  - when static task metadata does not expose a target path, the prompt now falls back to the most recent session-level `file.read` / `file.write` / `deliverable_path` evidence instead of remaining generic
  - when the task has no explicit companion-artifact contract and a concrete target is known, the prompt now explicitly forbids planning-artifact scans and full-tree listing while that target is present and readable
  - the review prompt now also tells the model what to do with direct target validation failures:
    - if reading the preferred deliverable returns `placeholder_deliverable` or `mismatched_deliverable_context`, stop broad inspection and call `flow.review_decision reject` using that tool result as evidence
- native deliverable-read guards now use that same recent session evidence when task metadata is missing a bound path:
  - `latestRecoveryTargetPathForSession(...)` now falls back from system recovery messages to recent `tool_result` payloads
  - it also recognizes the exact review prompt line we emit for bounded review:
    - `Start with the preferred deliverable target \`...\``
  - it accepts explicit `output.deliverable_path` first, then recent `file.read` / `file.write` `output.path` values that still look like real deliverables
  - that lets `file.read` reject placeholder or mismatched deliverables on hot review lanes even when the task record itself has no explicit deliverable metadata yet
  - the placeholder detector now also catches pasted task/review prompt copies that start with:
    - `Active task request:`
    - plus the persisted `Task description:`, `Review instruction:`, and `Flow node execution:` sections
  - the placeholder read guard now applies to review tasks even when they do not carry execution-first planning metadata:
    - live validation tasks like task `12` currently have only bootstrap metadata, so the review-lane guard must not depend on `taskplan.ModeExecutionFirst`
- recovery draft rejection now catches the live `file_write` no-content troubleshooting wording from task work lanes:
  - `recoveryFileWriteDraftRejectReason(...)` now classifies drafts such as:
    - `I see the file_write calls are going through but not actually replacing the content because I'm not providing content. Let me fix that:`
  - as `tool-recovery troubleshooting instead of the file body`
  - that closes the surviving task-16 family where the model narrated why `file_write` was not replacing content instead of emitting the actual script body for `scripts/validate-error-handling.sh`
- task-lane empty `cli.execute` rewrites now require a high-confidence draft source:
  - `handleTaskCLIExecuteWithoutCommand(...)` still rewrites to `file.write` when the session has a substantive assistant draft, recovery artifact draft, prior substantive draft, or continuation summary for the same target
  - but it no longer hydrates from looser historical target-path fallback alone
  - when the current step has no high-confidence draft for that file, the runtime now appends a task correction instead of silently rewriting to a stale prior target path
  - this targets the live config-loader family where an empty `cli.execute` intended to write `tests/test_config_loader.py` kept being rewritten into another `file.write` for `config/pipeline-config-invalid.yaml`

Still pending from this spec:

- stronger live proof that the same-turn read-only discovery cutoff is catching fresh non-review work-lane Anthropic churn before the older cross-turn machinery takes over:
  - after the latest deploy, the dominant live family was still review-lane discovery churn rather than a clear non-review work-lane sample
  - the review side is now live-proven in two ways on fresh traffic:
    - cross-turn review cap on session `b93b49f3-ca00-472f-9531-2adf6198f374`, where turn `2656120e-b223-4aec-9305-6f0e9a4837ed` completed `validation_loop_blocked` with:
    - `Repeated read-only discovery churn across 3 consecutive max-tool-call turns using file.list, file.read, git.diff, git.log, task.get with recurring not_found, path_traversal`
    - same-turn review cutoff on fresh review turns such as `52ffc07e-9b7f-49d8-a36c-3f4a932b75ff`, where the runtime emitted `Repeated same-turn read-only discovery churn (2/3)` before retrying review
  - so the remaining proof gap is narrower: we still need a fresh live example where the same-turn cutoff fires on a non-review work lane before the older cross-turn machinery
- any additional recovery-specific fingerprints that remain after the current async task guardrails
- live proof that the new task-16 recovery-draft matcher variant fires before dispatch on a fresh retry that actually reuses the bad narration:
  - focused unit coverage is green
  - the runtime is already deployed on this matcher
  - the first post-deploy task-16 retry instead took the preexisting synthetic `file.write` rewrite path and settled without re-emitting the bad narration, so this matcher remains preventive rather than freshly exercised in production

## Deferred Follow-Up, Not In This Spec

The idea of a manager-specialist or planner-specialist runtime is worth exploring, but it should not be part of this first correction pass. First we need to make the current task abstraction behave like a true bounded-execution container. Only after that should we consider adding a more explicit two-layer execution model.
