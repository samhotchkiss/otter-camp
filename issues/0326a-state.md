# 0326a State Report

## Scope

This report captures the exact live state of the rerun-88 canary and the surrounding runtime as of 2026-03-26 09:46 MDT.

Primary canary:
- project: `3ee5af44-f1b6-4f03-9664-ef3116fad9ee`
- project session: `1a9edb0a-a817-46b1-975d-4d96c8164bcb`

## Update 10:05 MDT

This section captures the current state after the Anthropic-only worker restart and the post-report canary progress.

### What changed after the initial report

- The old project continuation turn `b33b4142-86fc-44d8-8ef8-a9ad27093567` was manually cleaned up after a worker-shutdown transient provider failure.
- The worker had been restarted on a stale binary earlier in the morning; `./bin/ottercamp` has since been rebuilt at `052a8aa1` and the worker was restarted onto that rebuilt binary.
- The rebuilt worker is now running cleanly:
  - no binary commit mismatch warning
  - one live worker process
  - Anthropic-only routing remains in effect

### Current project shape now

The project has advanced beyond the earlier `OC-36` / `OC-41` / `OC-42` snapshot.

Current relevant task state:
- `OC-36` `Verify ingestion workstream child outputs and close parent` -> `in_progress`
- `OC-41` `Verify processing workstream outputs` -> `done`
- `OC-42` `Verify output/integration workstream outputs` -> `done`
- `OC-43` `Close processing workstream parent with integration check` -> `in_progress`
- `OC-44` `Close output/integration workstream parent with integration check` -> `in_progress`

This means the old verification pair for processing/output completed, and the project lane created the next two bounded closeout tasks for the corresponding parent orchestration work.

### Current live runtime state

Project session:
- `1a9edb0a-a817-46b1-975d-4d96c8164bcb`
- `current_turn_id = NULL`

Queued canary jobs that remain pending:
- project continuation job `cd8ad5d1-ba41-4dca-8c19-9e0e08e8d894`
- `OC-36` recovery-resume job `f0572719-de41-4d87-93e6-6ebbbe997d0a`
- `OC-43` work job `e715630c-f36a-4944-a316-751b0ec4cb48`

One canary job has already broken through on the rebuilt worker:
- `OC-44` work job `25f67b1a-9561-419f-8111-a9418644bef3` -> `claimed`
- session `ed9cd474-56b4-4a6f-8cce-ee4ba04b87d3`
- turn `e3123e7d-9836-4dc2-b97c-e1590800a7bf` -> `in_progress`
- latest live invocation `e8904bcb-0b35-4c9c-99a0-61c72c8ead76`
- model `claude-opus-4-6`

### What is working in this updated state

- The rebuilt worker is live on the current repo state, not the stale pre-rebuild binary.
- Anthropic Opus turns are still completing normally on the rebuilt worker.
- The canary no longer depends on local-model fallback.
- The queue-priority nudge worked: the next free slot went to a rerun-88 canary job instead of unrelated backlog.
- The newer task protections are firing in live Anthropic turns:
  - unrelated `memory.query` attempts are rejected instead of silently widening scope
  - the task lane continues after those rejections instead of hard-stalling

### What is still not working cleanly

- The canary still is not complete.
- The project continuation lane itself has not yet resumed after the rebuilt-worker restart; it is still queued behind active task work.
- `OC-36` is still `in_progress` with a pending recovery-resume lane, not yet re-entered.
- `OC-43` is still `in_progress` but has not yet started running on the rebuilt worker.
- `OC-44` is active, but it is still spending tool rounds reconstructing the parent-task linkage instead of finishing immediately.

### Bottom line for the update

The system is moving again on Anthropic and the rebuilt worker is now the one doing the work. The canary is down to one active verification/closeout lane (`OC-36`) plus two new bounded closeout tasks (`OC-43`, `OC-44`). The next proof target is straightforward: `OC-44` should either finish cleanly or reveal the next concrete blocker, and then the queued `OC-36` / `OC-43` / project continuation jobs should follow behind it.

## Working

### Model/provider routing

- Organization model-profile assignments are back on Anthropic:
  - `agent_turn` / `high-capability` -> `claude-opus-4-6`
  - `summarization` / `standard` -> `claude-sonnet-4-6`
  - `haiku` lanes -> `claude-haiku-4-5-20251001`
- `Local Ollama` is now disabled:
  - provider connection `34b13633-46b0-4471-ba52-1c0ad20cd36b`
  - `is_enabled = false`
- Anthropic is currently usable again:
  - `pearl-swh-me` `a61ed950-69e0-4693-b555-a8b5f1f87f7e` is `healthy`
  - fresh completions are already landing on Anthropic Opus 4.6

Fresh proof:
- `22c384dc-f737-4835-a6cb-d97e10e6b42a` -> `claude-opus-4-6` on `pearl-swh-me`
- `defcc2a7-9873-443d-8e80-4074e970e356` -> `claude-opus-4-6` on `pearl-swh-me`
- `b7cc393c-854a-4c87-b44b-a9dddb169e1f` -> `claude-opus-4-6` on `pearl-swh-me`

### Project-lane stabilization work that is now effective

The following classes of failures were previously active in rerun-88 and are no longer the main blocker:

- Anthropic setup-token tool streaming works.
- Org profile mapping back to Claude Opus / Sonnet / Haiku is correct in bootstrap defaults.
- Startup/worker races around duplicate dispatch, tool-set cache conflicts, stale continuation reuse, and worker purge behavior have been hardened.
- Synthetic project continuation messages now skip `listening_eval`.
- Duplicate dependency edges are treated as explicit no-ops instead of hard errors.
- Historical continuation-shell drafts are filtered from:
  - actionable draft counting
  - async project-session `task.list`
  - project gate selection
- The newer shell families are also filtered now, including:
  - `Review and approve E2E test results`
  - `Analyze Test Results and Identify Issues`
  - `Analyze Test Results and Prepare Report`
  - `Review test results and identify issues`
- The project lane now blocks duplicate meta-task creates instead of silently creating more junk review/promote shells.

### Current canary project shape

The project is much cleaner than before.

Current project-task counts:
- `done`: 16
- `cancelled`: 20
- `draft`: 3
- `in_progress`: 3

Current non-terminal tasks:
- `OC-9` `Speaker Ingestion Workflow Validation` -> `draft`
- `OC-10` `Speaker Processing and Enrichment Validation` -> `draft`
- `OC-11` `Pipeline Output and Integration Validation` -> `draft`
- `OC-36` `Verify ingestion workstream child outputs and close parent` -> `in_progress`
- `OC-41` `Verify processing workstream outputs` -> `in_progress`
- `OC-42` `Verify output/integration workstream outputs` -> `in_progress`

The latest successful Anthropic turn compacted the project correctly:
- it cancelled redundant duplicate tasks `OC-20` through `OC-40`
- it created the right three bounded verification tasks:
  - `OC-36`
  - `OC-41`
  - `OC-42`
- it queued those three verification tasks

### Fresh continuation prompt quality

The newest continuation message now reflects the cleaned project state:
- message `7bfae741-77ec-4e16-9d9f-5c7dfeb762cb`
- content says `There are 3 remaining draft project tasks`

That is a real improvement over the prior polluted prompts that were repeatedly claiming `8 remaining draft project tasks`.

## Not Working

### The canary is not complete yet

Rerun-88 is still not finished end-to-end.

What remains open:
- parent orchestration tasks `OC-9`, `OC-10`, and `OC-11` are still `draft`
- child verification tasks `OC-36`, `OC-41`, and `OC-42` are still running
- the project session currently has:
  - no `current_turn_id`
  - one pending continuation job

Current queued continuation:
- message `7bfae741-77ec-4e16-9d9f-5c7dfeb762cb`
- job `5583674d-2000-4911-bcc6-18b987e37a84`
- status `pending`

### Project continuation quality is still brittle

Even after the cleanup work, the project lane still has a real behavioral weakness:
- it can drift into repeated orchestration / cleanup / narration cycles before it settles on the next bounded action
- many fixes have been about catching and recovering those drifts
- this means the product is more resilient than before, but not yet cleanly reliable

Concrete evidence from this session:
- qwen-based turns earlier in the morning repeatedly created synthetic shell tasks
- later turns needed explicit filtering and cleanup logic to collapse those duplicates
- the most recent successful turn still spent multiple tool rounds cleaning up mistaken decomposition before landing on the right bounded verification tasks

### Stale local-turn cleanup was still needed

The last local-model turns did not exit cleanly on their own.

Examples:
- turn `2d8590ce-5331-47d9-b00c-b7a04ced4c8b` failed with:
  - `worker cleanup failed stale in_flight model invocation without live in-progress turn`
- turn `6a768e05-c7cd-42a1-8781-ac933ca9dd49` failed with the same reason

That means the stale-model cleanup logic is still an active part of keeping project continuations moving. It is functioning, but the underlying local-turn behavior was not healthy.

### Anthropic capacity is not fully healthy across all connections

Current provider status:
- `pearl-swh-me` -> `healthy`
- `claude-swh-me` -> `rate_limited`
- `Anthropic Primary` -> `rate_limited`

So the system is usable on Anthropic again, but failover depth is still degraded.

## Current live state

Latest completed project turn:
- turn `27884956-941f-4ee9-9743-58aeb8921010`
- completed `2026-03-26 09:45:44 MDT`
- Anthropic Opus 4.6

Latest completed assistant summary in the session:
- message `cf50cbc8-ea5a-4436-9fc9-85d53721d0f8`
- reported that duplicate tasks were cancelled and `OC-36`, `OC-41`, `OC-42` were queued

Current session state:
- session `1a9edb0a-a817-46b1-975d-4d96c8164bcb`
- `status = active`
- `current_turn_id = NULL`

Next expected step:
- worker should claim pending job `5583674d-2000-4911-bcc6-18b987e37a84`
- run the fresh continuation message `7bfae741-77ec-4e16-9d9f-5c7dfeb762cb`
- continue on Anthropic, not local models

## Bottom line

What is working:
- Anthropic is back in control of the org
- local models are disabled
- the project is no longer buried under dozens of junk draft shells
- the continuation prompt now reflects the real remaining draft count
- the canary has been reduced to three real draft parents and three active bounded verification tasks

What is not working:
- rerun-88 is not complete
- continuation behavior is still too recovery-heavy and cleanup-dependent
- two Anthropic connections remain rate-limited
- the next continuation after `7bfae741-...` has not yet been proven through to completion

## Update 10:48 MDT

The biggest remaining product bug from the earlier report is now fixed and live:
- orchestration-parent auto-complete can now backfill a current project flow template before transitioning `draft -> done`
- project continuations now reject a satisfied-parent "close-out summary" shell child instead of allowing it to rewrite the parent decomposition

Live rerun-88 state after deploying that fix:
- `OC-11` is now `done`
- stray shell parent `OC-50` is `cancelled`
- duplicate child shells `OC-51` and `OC-52` are `cancelled`
- close-out artifact task `OC-53` is `done`
- there are no remaining non-terminal tasks in project `3ee5af44-f1b6-4f03-9664-ef3116fad9ee`

What is working now:
- all three real validation parents are terminal:
  - `OC-9` done
  - `OC-10` done
  - `OC-11` done
- the live canary task tree is fully settled at the task-row level
- the specific `OC-11` miss was not just papered over manually; it is covered by new integration tests and running on the rebuilt worker

What is still not working:
- the project-level async session `1a9edb0a-a817-46b1-975d-4d96c8164bcb` is still `active` and idle instead of producing a final project wrap-up / completion continuation
- the latest project-lane continuation attempt is rate-limited on Anthropic, with session history currently ending in:
  - pending synthetic continuation message `ca5bc06f-1fd2-437a-90fe-18cf271e23c2`
  - failed assistant turn `875f6046-d224-423f-b9e2-8da3c036bc4b`
  - final system backoff message `1f8d6ec7-37ed-4817-b05d-fd02b291a48f`
- all three Anthropic connections hit rate limits during the latest retry window:
  - `pearl-swh-me`
  - `claude-swh-me`
  - `Anthropic Primary`

So the current blocker is no longer task-state correctness. It is final project-session completion under provider backoff.

## Update 11:09 MDT - Token Usage Analysis

The current Anthropic usage is too high for the amount of human testing being performed. The data points to OtterCamp runtime behavior multiplying usage internally, not just expensive models.

### What the database shows

Completed `model_invocation` usage in the last 24 hours:
- `92,940,989` input tokens
- `3,949,887` output tokens
- `67,707,836` cache-read tokens
- `164,598,712` total recorded completed tokens

Completed usage on `2026-03-25` alone:
- `143,345,645` total recorded tokens

Failure volume in the last 24 hours:
- `8,185` completed invocations
- `3,051` failed invocations
- `27.1%` failed overall
- `1,524` failed invocations were explicitly rate-limit related

Top canary project sessions by completed-token burn in the last 24 hours:
- rerun-86 project session `0ffc31b3-c88d-49dc-aab6-f8ff0b45839e` -> `14,926,074`
- rerun-87 project session `ec26eddb-66be-42a5-9859-64cb24c7c820` -> `13,753,320`
- rerun-88 project session `1a9edb0a-a817-46b1-975d-4d96c8164bcb` -> `9,763,939`

These three project sessions alone account for roughly `38.4M` tokens in 24 hours. That is not proportional to the amount of operator testing.

### Primary multiplier: too many model calls inside one turn

The strongest runtime multiplier is that a single `chat_turn` can make dozens of `agent_turn` model invocations before it ends.

Code path:
- [`internal/turn/engine.go`](../internal/turn/engine.go)
- `defaultMaxToolCalls = 75`
- the main `runTurn(...)` loop keeps calling the model after tool results until the tool-call budget or duration limit is reached

Observed live example:
- turn `d04a74a0-25a2-40d2-a899-677f896076a3`
- session `0ffc31b3-c88d-49dc-aab6-f8ff0b45839e`
- completed in about 7 minutes
- produced:
  - `31` completed `agent_turn` model invocations
  - `1` completed `listening_eval`
  - `1` completed `continuation_summary`
  - `75` `tool_result` messages
  - `31` assistant messages

Within that one turn, prompt input grew from `494` tokens to `59,015` tokens before the turn ended. That is the opposite of healthy convergence. The model kept paying to reread a larger and larger tool transcript while still exploring the same lane.

Across all completed `agent_turn` invocations in the last 24 hours:
- average completed calls per turn: `3.9`
- max completed calls inside a single turn: `70`

That max is not a normal user interaction pattern. It is a runtime loop pattern.

Scope split for completed `agent_turn` work in the last 24 hours:
- `project_task`
  - `6,304` completed invocations
  - average input `8,338.2`
  - max input `59,878`
  - `114,454,824` total tokens
- `project`
  - `1,499` completed invocations
  - average input `19,826.2`
  - max input `90,091`
  - `40,724,466` total tokens

Per-turn aggregation shows the same shape:
- `project_task`
  - average completed calls per turn: `5.09`
  - max completed calls per turn: `70`
  - average tokens per turn: `92,376.8`
  - max tokens per turn: `1,889,087`
- `project`
  - average completed calls per turn: `2.04`
  - max completed calls per turn: `31`
  - average tokens per turn: `55,482.9`
  - max tokens per turn: `1,434,958`

So the problem is not just "project continuations are expensive." The larger burn is repeated multi-call task turns, with project turns adding a second expensive control-plane layer on top.

Representative expensive `project_task` turns in the last 24 hours:
- `fb6ea882-1514-4987-b538-2f5b6998dc75`
  - task `Validate config loading and environment overrides`
  - `46` completed model calls
  - `1,889,087` tokens
- `a9620ed2-16d7-40ea-822e-54b7dc017c4b`
  - task `Verify ingestion workstream child outputs and close parent`
  - `31` completed model calls
  - `1,765,101` tokens
- `84b2d9e1-4a38-4c26-b48b-7bb542da7d2d`
  - task `Validate pipeline stage execution and ordering`
  - `49` completed model calls
  - `1,711,468` tokens

The first of those traces is important because it shows why a blunt cap could make the product worse. That turn was not only dead looping. It mixed:
- repeated tool-call formatting confusion
- repeated `file.write` redirects back into the recovery target
- CLI retries and verification loops
- real debugging, test execution, and workspace validation

So the safe interpretation is:
- there is real work happening inside some long task turns
- but tool friction and recovery-tool confusion are inflating those turns dramatically
- the product-safe fix is targeted early stop / recovery on repeated blocker fingerprints and repeated recovery-target misfires, not a blanket "all task turns must be tiny" rule

### Secondary multiplier: blocked-tool loops are not cut off early enough

Representative high-cost project turns repeatedly hit blocked tool results such as:
- `task_lane_owned_by_project_task_session`
- `task_execution_required`
- `task must remain orchestration-only while executable child tasks exist`
- `not_found`

In one representative turn, the most common tool-result prefixes were:
- `task_lane_owned_by_project_task_session` -> `8`
- PM directive/session history payloads -> `7`
- planning payloads -> `6`
- `task_execution_required` -> `4`
- orchestration-only guard -> `4`
- `flow.get_execution not_found` -> `3`

The current stop predicate for blocked project-execution mutations is too narrow. It stops on a few known `project.create` / `task.create` failures, but not on the higher-frequency blockers above. That means the model can keep probing the same dead end while the prompt keeps expanding.

### Secondary multiplier: listening_eval is expensive and concentrated on async project sessions

Completed `listening_eval` usage in the last 24 hours:
- `376` completed
- `13,532,679` total tokens
- average input around `35.8k` tokens

Breakdown:
- async project sessions:
  - `358` completed
  - `246` failed
- async project_task sessions:
  - `0` completed

So `listening_eval` is almost entirely a project-session cost center right now. It is not a broad, unavoidable platform cost. It is a design choice concentrated in the async project lane.

Top `listening_eval` sessions in the last 24 hours:
- rerun-87 project session `ec26eddb-66be-42a5-9859-64cb24c7c820`
  - `120` completed
  - `71` failed
  - `6,042,410` tokens
- rerun-88 project session `1a9edb0a-a817-46b1-975d-4d96c8164bcb`
  - `77` completed
  - `75` failed
  - `2,909,413` tokens
- project session `856ae42a-5ed8-4c53-bf70-53dc6a9e0c46`
  - `54` completed
  - `20` failed
  - `1,827,564` tokens

The control flow explains why. In [`internal/turn/engine.go`](../internal/turn/engine.go), `runListeningEval(...)` skips:
- `project_task` sessions
- synthetic project-continuation resume messages
- active bootstrap project sessions

But after those special cases, the gating logic is:
- if the session is **not** async and there is `<= 1` pending user message, skip
- otherwise run `listening_eval`

That means async project sessions effectively pay this extra model call by default, even when there is no genuine operator "is more context incoming?" ambiguity.

### Secondary multiplier: summarization is adding pressure while the provider is already rate-limited

Summarization in the last 24 hours:
- `113` completed
- `902` failed

Scope split:
- `project_task`
  - `109` completed
  - `440` failed
  - `476,306` completed tokens
- `project`
  - `5` completed
  - `462` failed
  - `442,005` completed tokens

The main path is prompt assembly, not the dormant `runTurn(...)` check in `engine.go`. In [`internal/prompt/assembler.go`](../internal/prompt/assembler.go):
- `defaultLayer6BudgetTokens = 12000`
- summarization is requested when unsummarized history reaches `55%` of that budget
- that means summarize pressure begins at roughly `6,600` estimated unsummarized tokens

That threshold is low enough that long-running canary sessions can spend much of their life qualifying for background summarization.

Summarize jobs do dedupe while a pending job for the same session already exists, but the dedupe key is only session-scoped and pending-job-scoped. There is no broader suppression after repeated failure, so noisy sessions can keep re-entering the same pressure pattern.

The failure mix is also more nuanced than "just 429s":
- `196` summarization failures are explicit Anthropic 429s
- `35` are stale-model cleanup failures
- `668` are avoidable `claude-sonnet-4` 404s from the old model name, concentrated between `2026-03-25 19:00 MDT` and `2026-03-25 22:00 MDT`

So summarization is both:
- contesting quota during overload
- and creating avoidable background churn when model routing is stale or sessions are already unstable

### Accounting gap: internal budgets undercount real provider-facing load

Usage persistence mostly works for completed invocations, but internal budget sums are softer than provider reality.

Current budget query behavior:
- [`internal/budget/service.go`](../internal/budget/service.go)
- `SumTokens(...)` uses `SUM(input_tokens + output_tokens)`
- it ignores `cache_read_tokens`

In the last 24 hours:
- cache-read tokens were `67,707,836`
- cache-read tokens were `41.1%` of all completed recorded tokens

So OtterCamp's own budget view materially underreports actual token throughput seen by Anthropic.

### Safe product conclusion

The evidence does **not** support "send more context" as the fix.

The evidence supports:
- too many model/tool cycles inside single turns
- prompt history growing inside those turns instead of resetting sooner
- expensive project-session `listening_eval`
- prompt-assembly summarization firing once unsummarized history crosses only about `6.6k` estimated tokens
- overload-unaware summarization retry churn
- budgets undercounting cache reads

The likely product-safe direction is:
- reduce per-turn tool/model cycle budgets for async project and project_task turns
- add early stop conditions for repeated blocked-tool fingerprints
- reduce or disable `listening_eval` for async project continuations
- suppress summarization attempts while the selected provider is already rate-limited
- include cache-read tokens everywhere budgets and operator usage views are computed

## Implementation checkpoint

The first runtime slices from `spec-0326a.md` are now in code and tested:

- budget usage now includes `cache_read_tokens`
- `listening_eval` is disabled for async `project` sessions
- prompt-assembly summarization is suppressed for active async `project` and `project_task` sessions
- summarize enqueue now defers behind provider backoff and session-level summarization backoff
- scope-aware async turn budgets are in place for `project`, `project_task` review, and `project_task` work
- async `project_task` turns now stop after the second identical deterministic validation failure within the same turn instead of paying for a third rediscovery cycle
- when that repeated same-turn validation failure names a concrete deliverable target, the checkpoint is now persisted before the turn stops
- ordinary async `project_task` execution lanes now preflight-block `task.create`
- explicit orchestration-only parent tasks can still decompose, but only by creating bounded child tasks beneath themselves with `parent_task_id` set to the current task
- those blocked task-lane decomposition attempts now stop the turn immediately instead of spending another model round on the same boundary mistake
- async `project` continuation lanes now also preflight-block `subtask.create`; that tool now hard-bounces at the project boundary instead of paying for a `flow_node_execution_id_required` coaching round
- async `project_task` lanes now preflight-block `subtask.create` when there is no active flow execution bound to the session
- when a task lane does have an active bound flow execution, missing `subtask.create` execution IDs are now injected from the session instead of forcing a correction round

The repeated-validation change is deliberately narrow:

- it only applies to async `project_task` turns
- it does **not** preempt the dedicated review retry path for `review_action_required`
- it does **not** change the existing third-strike blocked-task path

I also added a lightweight operator report at [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh). It reports:

- window totals including cache reads
- purpose / model / connection breakdown
- top sessions
- top turns
- most common failures

The script smoke-ran successfully against the local dev database after sourcing `.env`.

So the implementation has moved beyond diagnosis, but it is still a partial rollout. The largest remaining runtime gap from this spec is repeated recovery-target drift, plus richer in-product per-session / per-turn token diagnostics.

### Deployment checkpoint

The latest `0326a` slices are now also deployed locally:

- rebuilt `./bin/ottercamp`
- respawned tmux `codex-e2e-20260324` serve and worker panes on the new binary
- `./bin/ottercamp health --output json` returned `status=ok`

That means the currently running runtime now includes:

- cache-read-aware budget accounting
- async `project` `listening_eval` suppression
- async summarize suppression/backoff
- scope-aware async turn budgets
- same-turn repeated validation cutoff for async task turns
- repeated recovery-target focus cutoff across explicit recovery resumes
- async task-lane `task.create` boundary enforcement with orchestration-only self-decomposition as the only escape hatch
- async project-lane `subtask.create` boundary enforcement
- async task-lane `subtask.create` boundary enforcement plus session execution-id injection

## Update 13:27 MDT

The next `0326a` boundary slices are now deployed locally on the rebuilt runtime.

New behavior now in the running binary:

- async `project_task` execution lanes preflight-block `task.create` unless the current task is explicitly orchestration-only
- even orchestration-only task lanes may only decompose beneath themselves by setting `parent_task_id` to the current task
- async `project` continuation lanes preflight-block `subtask.create`
- async `project_task` lanes preflight-block `subtask.create` when the session has no active `flow_node_execution_id`
- when the task session does have an active bound execution, missing `subtask.create.flow_node_execution_id` is injected from session metadata automatically
- blocked task-lane decomposition attempts now stop the turn immediately instead of paying for another model round

Focused verification that passed before deploy:

- `go test ./internal/turn -run 'Test(ShouldBlockTaskExecutionBroadContextTool|ShouldBlockTaskExecutionBroadContextToolAllowsOrchestrationValidationContextReads|ShouldBlockTaskExecutionTaskCreateToolBlocksNonOrchestrationTask|ShouldBlockTaskExecutionTaskCreateToolAllowsOrchestrationParentChildCreate|ShouldBlockTaskExecutionOffTargetEvidenceTool|ShouldBlockTaskRecoveryStatusPathTool|ShouldBlockTaskStatusMessageTool|ShouldStopAfterBlockedTaskExecutionBoundaryMutation|HandleUserMessageTaskScopeStopsAfterBlockedTaskCreateBoundaryMutation)$' -count=1`
- `go test ./internal/turn -run 'Test(ShouldBlockProjectExecutionSubtaskCreateTool|ShouldBlockTaskExecutionSubtaskCreateTool|ShouldStopAfterBlockedProjectExecutionBlockedMutationOnSubtaskCreateBoundaryError|ShouldStopAfterBlockedTaskExecutionBoundaryMutation|HandleUserMessageProjectScopeStopsAfterBlockedSubtaskCreateBoundaryMutation|HandleUserMessageTaskScopeStopsAfterBlockedSubtaskCreateBoundaryMutation|HandleUserMessageTaskScopeInjectsFlowNodeExecutionIDForSubtaskCreate|ShouldBlockTaskExecutionTaskCreateToolBlocksNonOrchestrationTask|ShouldBlockTaskExecutionTaskCreateToolAllowsOrchestrationParentChildCreate|HandleUserMessageTaskScopeStopsAfterBlockedTaskCreateBoundaryMutation)$' -count=1`

Deployment checkpoint:

- rebuilt `./bin/ottercamp`
- respawned tmux `codex-e2e-20260324` serve and worker panes
- `./bin/ottercamp health --output json` returned `status=ok`

What is still not proven live from this slice:

- a fresh Anthropic canary turn has not yet been observed tripping these newest preflight guards in production traffic
- broader repeated-recovery drift cutoffs are still pending beyond the already-shipped repeated focus-failure handling

## Update 13:54 MDT

The next `0326a` recovery-hardening slice is now in code and focused-test green.

New behavior:

- recovery checkpoints that already record repeated target drift now stop the next async recovery turn on the first new focus miss instead of paying another rediscovery cycle
- recovery checkpoint persistence now strengthens the stored failure reason when an explicit resume attempt drifts away from an already-established target path
- the recovery prompt strategy already references that hardened checkpoint state, so the next continuation is told not to switch files again without a new authoritative signal

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsRecoveryTurnAfterRepeatedTargetDriftAcrossResumes|HandleToolValidationResultsStopsRecoveryTurnAfterRepeatedFocusFailureAcrossResumes|HandleToolValidationResultsPersistsRecoveryCheckpointOnRepeatedFocusFailureStop|PersistRecoveryFileWriteCheckpointStrengthensRepeatedTargetDriftFailureReason)$' -count=1`
- `go test ./internal/taskcheckpoint -count=1`

What is still pending after this slice:

- live proof on a fresh Anthropic canary recovery turn
- broader repeated-recovery cutoffs beyond focus / target-drift failures

## Update 14:17 MDT

The next deterministic-cutoff widening slice is now in code and focused-test green.

New behavior:

- repeated same-turn async `project_task` validation stops now explicitly classify `cli.execute` shell-injection denials as deterministic blocker fingerprints
- repeated same-turn async `project_task` validation stops now also classify `file.read` `not_found` misses as deterministic blocker fingerprints
- `file.read` attempt fingerprints are now path-aware, so the early stop only triggers when the model rereads the same missing path instead of collapsing all read misses together
- the shell-injection path keeps using command-aware fingerprints, so the stop is still scoped to the same denied command rather than all `cli.execute` failures

Focused verification that passed:

- `go test ./internal/toolargs -run 'Test(FileReadAttemptFingerprintTracksPath|CanonicalToolNameNormalizesFileReadAlias)$' -count=1`
- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalShellInjectionFailureInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFileReadNotFoundInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFailureInSameTurn)$' -count=1`

What is still pending after this slice:

- rebuild / restart on this newest deterministic-cutoff widening
- live proof on a fresh Anthropic canary task turn
- broader repeated-recovery cutoffs beyond focus / target-drift / repeated read-only discovery handling

## Update 14:23 MDT

The next `0326a` telemetry-hardening slice is now in code and focused-test green.

New behavior:

- streamed `agent_turn` invocation failures now preserve provider failure classification in the turn engine instead of flattening provider 429s into generic `model_error`
- the failure row for a streamed Anthropic rate-limit now records:
  - `failure_class=provider_rate_limit`
  - `error_code=provider_rate_limited`
  - the original rate-limit detail in `error_message`

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint|HandleTurnJobRateLimitedPersistsProviderRateLimitFailureClassification|HandleTurnJobRateLimitedUsesBackoffWhenNoRetryHint|HandleTurnJobRateLimitedCapsProviderHintAtMaxBackoff)$' -count=1`

Why this mattered:

- live Anthropic continuation failures were already surfacing the correct retry UX (`[Rate limited, retrying in ...]`)
- but the persisted `model_invocation` rows were still landing as `error_code=model_error` with no `failure_class`
- that made operational reporting and backoff debugging noisier than the actual runtime behavior

What is still pending after this slice:

- rebuild / restart on this telemetry patch
- live proof on a fresh Anthropic 429 after the rebuild
- broader repeated-recovery cutoffs beyond focus / target-drift / repeated read-only discovery handling

## Update 14:30 MDT

The next deterministic-cutoff widening slice is now in code and focused-test green.

New behavior:

- repeated same-turn async `project_task` validation stops now also classify `file.list -> not_found` as deterministic churn
- `file.list` attempt fingerprints are now path-aware, so the early stop only triggers when the model repeats the same missing list target instead of collapsing unrelated list misses together
- repeated same-turn async `project_task` validation stops now also classify `git.commit -> task_git_commit_blocked` as deterministic churn
- that means task lanes that keep trying to commit after the runtime already told them commits are flow-owned now stop early instead of paying another full round

Focused verification that passed:

- `go test ./internal/toolargs -run 'Test(FileListAttemptFingerprintTracksPath|CanonicalToolNameNormalizesFileListAlias)$' -count=1`
- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFileListNotFoundInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalTaskGitCommitBlockedInSameTurn)$' -count=1`

Why this slice was next:

- in the current top 10 async `project_task` turns from the last 6 hours, the biggest remaining repeated deterministic families after the already-shipped cutoffs were:
  - `cli.execute` shell-injection denials: `30`
  - generic/remaining other deterministic errors: `29`
  - `file.read` `not_found`: `11`
  - `recovery_target_focus_required`: `10`
- drilling into those same top turns showed repeated `task_git_commit_blocked` and `file.list -> not_found` patterns as the next highest deterministic drains

What is still pending after this slice:

- rebuild / restart on this newest deterministic-cutoff widening
- live proof on a fresh Anthropic or resumed canary task turn after the rebuild
- broader repeated-recovery cutoffs beyond focus / target-drift / repeated read-only discovery handling

## Update 14:40 MDT

The next deterministic-cutoff widening slice is now in code and focused-test green.

New behavior:

- repeated same-turn async `project_task` validation stops now also catch task-lane broad-context drift on:
  - `task.list` when the runtime already told the lane not to re-list the broader project task tree
  - `memory.query` when the runtime already told the lane not to browse org/project memory for unrelated prior work
- repeated same-turn async `project_task` validation stops now also catch `file.read -> mismatched_deliverable_context`
- those broad-context tool detections reuse the existing per-tool attempt fingerprinting, while the mismatched-deliverable read path still benefits from `file.read` path-aware fingerprints

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalTaskListBroadContextFailureInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalMismatchedDeliverableContextInSameTurn)$' -count=1`

Why this slice was next:

- after the last widening, the top remaining deterministic families inside the `other` bucket from the current top async task turns were:
  - task-lane broad-context probes (`task.list`, `memory.query`)
  - `file.read -> mismatched_deliverable_context`
  - smaller residual `file.edit -> old_string_not_found`

What is still pending after this slice:

- rebuild / restart on this newest deterministic-cutoff widening
- live proof on a fresh Anthropic or resumed canary task turn after the rebuild
- any further widening should probably focus on `file.edit -> old_string_not_found` only if it still survives after the shipped task-list / memory / read-context cutoffs

## Update 14:43 MDT

The next deterministic-cutoff widening slice is now in code and focused-test green.

New behavior:

- repeated same-turn async `project_task` validation stops now also catch `file.edit -> old_string_not_found`
- `file.edit` attempt fingerprints are now scoped to:
  - path
  - `old_string`
  - `new_string`
- that keeps the early stop tied to the same failed edit attempt instead of collapsing unrelated file edits together

Focused verification that passed:

- `go test ./internal/toolargs -run 'Test(FileEditAttemptFingerprintTracksPathAndOldString|CanonicalToolNameNormalizesFileEditAlias)$' -count=1`
- `go test ./internal/turn -run 'TestHandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFileEditOldStringNotFoundInSameTurn$' -count=1`

Why this matters:

- after the last widening, `file.edit -> old_string_not_found` was the last obvious recurring structured file-mutation churn family still visible in the evidence set from the highest-cost async task turns

What is still pending after this slice:

- rebuild / restart on this newest deterministic-cutoff widening
- live proof on a fresh Anthropic or resumed canary task turn after the rebuild
- the remaining open work is now less about obvious deterministic same-turn churn and more about live provider windows plus any deeper recovery-specific loops that still survive these guardrails

## Update 14:09 MDT

The operator-diagnostics slice is now in the product surface, not just the shell script.

New command:

- `ottercamp db token-usage [--hours N] [--limit N] [--org UUID] [--output table|json|quiet]`

What it reports:

- overall invocation totals including cache-read tokens
- purpose / model / provider-connection breakdown
- top sessions
- top turns
- most common failures

Verification that passed:

- `go test ./cmd/ottercamp -run '^TestMigrationAppliedLine$' -count=1`
- `go test -tags=integration ./cmd/ottercamp -run '^TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`

This closes the spec item for richer in-product top-session / top-turn token diagnostics. The remaining runtime gap is still repeated-recovery fingerprints beyond the already-shipped focus and target-drift cutoffs.

## Update 14:27 MDT

The next recovery cutoff slice is focused on same-turn read-only discovery churn.

New behavior:

- the first read-only discovery cycle in a recovery turn is still allowed
- the second read-only discovery cycle in that same recovery turn now halts early, persists the recovery checkpoint, and tells the next continuation to write the deliverable or resume from the durable artifact instead of rereading context again

Focused verification that passed:

- `go test ./internal/turn -run 'Test(MaybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatchStopsSecondReadOnlyRecoveryCycle|MaybeBlockRejectedRecoveryAssistantDraftBeforeToolDispatchBlocksReadOnlyRecoveryNarration|HandleToolValidationResultsStopsRecoveryTurnAfterRepeatedTargetDriftAcrossResumes|HandleToolValidationResultsStopsRecoveryTurnAfterRepeatedFocusFailureAcrossResumes)$' -count=1`

This narrows the remaining recovery gap again. What is still pending is broader repeated-recovery fingerprint handling beyond the shipped focus-failure, repeated-target-drift, and repeated read-only discovery stops.

## Update 14:45 MDT

The `listening_eval` policy now matches the spec more closely.

New behavior:

- `listening_eval` is now skipped for all async sessions, not just async `project` and `project_task`
- that closes the small remaining async `organization` leak that still showed up in the live database after the previous restart

Focused verification that passed:

- `go test ./internal/turn -run 'Test(ListeningEvalSkippedForAsyncOrganizationSession|ListeningEvalSkippedForAsyncProjectTaskSession|ListeningEvalSkippedForAsyncProjectSession|ListeningEvalWaitReenqueuesAndSkipsPhase2|ListeningEvalSkippedForSyncSinglePending)$' -count=1`

This does not yet have post-restart live traffic proof for a fresh async organization turn, but the runtime contract and focused tests are now aligned with the spec: only sync sessions should pay for `listening_eval`.

## Update 15:18 MDT

The next `0326a` slice hardens provider cooldown behavior across processes and restarts.

New behavior:

- explicit provider `retry_after` windows are now persisted on `provider_connection.metadata.health_rate_limited_until`
- the router now treats those persisted cooldown windows as authoritative when deciding whether a connection is still rate-limited
- if every eligible connection for a routed profile is still inside cooldown, the router now returns a rate-limited backoff error instead of selecting one and burning another guaranteed 429
- live model calls translate that router-level cooldown into the normal turn-level `ErrRateLimited` retry path
- moving a connection back to `healthy` or another non-rate-limited state clears the persisted cooldown marker automatically

Why this matters:

- before this change, explicit provider backoff only lived in in-memory gateway health state
- once health was persisted to Postgres, other processes and cold starts only saw `health_status = rate_limited` plus `updated_at`
- that collapsed a real provider `retry_after` window down to the generic one-minute persisted backoff and made repeated 429 clusters much more likely after restarts or cross-process routing

Focused verification that passed:

- `go test ./internal/gateway -run 'Test(RouterSelectConnection(ReturnsRateLimitedBackoffWhenAllConnectionsCoolingDown|TreatsExpiredPersistedRateLimitAsDegraded)|ListeningEvalSkippedForAsyncProjectSession)' -count=1`
- `go test -tags=integration ./internal/repo -run '^TestProviderConnectionRepoCRUDAndHealthStatus$' -count=1`
- `go test -tags=integration ./internal/gateway -run 'TestLiveModelGateway(ClassifiesRateLimitFailures|ReturnsRateLimitedWithoutProviderCallWhenAllConnectionsCoolingDown)$' -count=1`

Deployment checkpoint:

- rebuilt `./bin/ottercamp`
- respawned tmux `codex-e2e-20260324` serve and worker panes on the new binary
- `./bin/ottercamp health --output json` returned `status=ok`

Live operational note:

- the existing long Anthropic cooldown on `claude-swh-me` had been recorded before this slice existed, so it had no persisted metadata yet
- before the restart, I backfilled `provider_connection.metadata.health_rate_limited_until = 2026-03-26T20:59:58.024902-06:00` for `claude-swh-me` from its latest stored `retry_after=6h59m43s` failure row
- `pearl-swh-me` and `Anthropic Primary` are still only carrying the older coarse `rate_limited` state because their latest stored failures did not include an explicit retry-after window to preserve

What is not yet proven live:

- a fresh post-deploy Anthropic 429 has not yet been observed to stamp `health_rate_limited_until` in the real local runtime
- a fresh post-deploy router refusal has not yet been observed on a live Anthropic call with all eligible connections still cooling down

## Update 15:10 MDT

The next `0326a` slice fixed a different rate-limit churn path: active async `project` sessions were still ignoring provider backoff during worker repair and bootstrap retry.

Root cause:

- `RequeueActiveProjectSessionsWithoutTurns(...)` repaired active async `project` sessions immediately, even when the latest terminal turn on the same trigger message had already failed with a concrete provider `retry_after`
- deferred bootstrap provider failures also re-enqueued without carrying forward the next `retry_count`, which made the delayed retry path less durable than the ordinary turn retry path

New behavior:

- worker repair for active async `project` sessions now inspects the latest terminal turn for the same bootstrap / continuation message
- if that turn failed with a concrete rate-limit hint, the repair path now schedules the next `agent_turn` dispatch at a future `run_after` instead of immediately
- the repaired retry payload now marks `rate_limit_jitter_applied = true`
- deferred project-bootstrap provider failures now enqueue with `retry_count = current_turn_retry + 1`
- those deferred bootstrap retries also use the same rate-limit-aware delayed scheduling path instead of falling back to a generic immediate retry

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleDeferredBootstrapProviderFailureEnqueuesIncrementedRateLimitedRetry|HandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint|HandleTurnJobRateLimitedPersistsProviderRateLimitFailureClassification)$' -count=1`
- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RequeueActiveProjectSessionsWithoutTurnsPreservesRateLimitBackoff|RequeueActiveProjectSessionsWithoutTurns|RequeueActiveProjectSessionsWithoutTurnsIgnoresStalePendingDispatch)$' -count=1`

Deployment checkpoint:

- rebuilt `./bin/ottercamp`
- respawned tmux `codex-e2e-20260324` serve and worker panes with exported `.env`
- `./bin/ottercamp health --output json` returned `status=ok`

Live proof:

- the previously storming active async project session `db21265f-c37d-40e4-9ed5-13def09970f8` is no longer producing a new failed turn every minute
- latest failed turn remains:
  - `9518ecd6-0d67-4e2a-ba29-281fa4984f1f`
  - `retry_count = 57`
  - `completed_at = 2026-03-26 15:05:04 MDT`
  - error `model provider rate limited (retry_after=54m55s): all provider connections are rate limited ...`
- latest dispatch state is now one future-dated pending retry instead of an immediate repair loop:
  - job `4554c05f-7c8a-41cb-b458-a074865ef5d5`
  - `status = pending`
  - `retry_count = 58`
  - `run_after = 2026-03-26 15:35:04 MDT`
  - `rate_limit_jitter_applied = true`

What this fixes:

- worker startup / repair no longer burns a project bootstrap turn every minute while every Claude connection is cooling down
- project bootstrap retry behavior is now aligned with the task-session repair path and with ordinary rate-limited turn retries

What is still pending:

- a fresh post-deploy Anthropic 429 should still be observed end-to-end to confirm the streamed failure classification and persisted cooldown path together on a new real invocation
- the next remaining `0326a` runtime gap is still broader repeated-recovery fingerprint handling beyond the already-shipped focus-failure, target-drift, and repeated read-only discovery stops

## Update 15:18 MDT

The next `0326a` slice narrows another specific recovery churn family: repeated empty file-mutation retries across explicit recovery resumes.

New behavior:

- when a recovery checkpoint already shows `file.write` previously retried without `content`, the next recovery turn now halts immediately on the first repeated empty `file.write` instead of spending another correction round
- when a recovery checkpoint already shows `cli.execute` previously retried without `command` for a file-output repair, the next recovery turn now halts immediately on the first repeated empty `cli.execute` instead of spending another correction round
- recovery file-output context now treats the checkpoint target path itself as sufficient context even when no artifact or target draft exists yet, so these resume-level halts still fire from a durable checkpoint rather than silently degrading back into another retry message
- recovery prompt strategy lines now explicitly warn against repeating empty `file.write` and empty `cli.execute` retries once the checkpoint has already proven those shapes are dead ends

Focused verification that passed:

- `go test ./internal/taskcheckpoint -run 'TestRecoveryFileWritePromptStrategyLines(HardensRejectedDraftResume|HardensEmptyMutationResume)$' -count=1`
- `go test ./internal/turn -run 'Test(HandleRecoveryFileWriteWithoutContentStopsAfterRepeatedResumeFailure|HandleRecoveryCLIExecuteWithoutCommandStopsAfterRepeatedResumeFailure|HandleToolValidationResultsStopsRecoveryTurnAfterRepeatedFocusFailureAcrossResumes|HandleToolValidationResultsStopsRecoveryTurnAfterRepeatedTargetDriftAcrossResumes)$' -count=1`

What this closes:

- a recovery lane no longer needs to spend a new correction round rediscovering that it still has no file body
- a recovery lane no longer needs to spend a new correction round rediscovering that it still has no `cli.execute.command`

Current status of this slice:

- rebuilt `./bin/ottercamp`
- respawned tmux `codex-e2e-20260324` serve and worker panes on the new binary
- `./bin/ottercamp health --output json` returned `status=ok`

What is still pending after this slice:

- broader repeated-recovery fingerprints beyond the shipped focus-failure, target-drift, repeated empty-mutation, and repeated read-only discovery stops

## Update 15:20 MDT

The durable provider-cooldown path now has fresh live proof.

Fresh evidence from the running runtime:

- recent Anthropic refusals are now recorded as `error_code=provider_rate_limited`, not flattened `model_error`
- latest fresh rows:
  - invocation `623d6567-c626-4c3e-a216-ea9bcb6fcd52`
  - session `07ab860d-1c73-4e73-a160-3b8fe3c23998`
  - turn `2323b41a-5499-40cf-97fb-ba994322fcc3`
  - model `claude-opus-4-6`
  - `provider_connection_id = NULL`
  - error `model provider rate limited (retry_after=42m29s): all provider connections are rate limited ...`
  - created `2026-03-26 15:17:30 MDT`

Why that matters:

- this is the router-refusal path, not a provider-call path
- the runtime is now surfacing the cooldown refusal with the correct durable error code instead of collapsing it back into generic `model_error`

The project-bootstrap cooldown storm fix also survived the runtime restart cleanly:

- active async project session `db21265f-c37d-40e4-9ed5-13def09970f8` still has the same future-dated retry after the restart
- current job state remains:
  - job `4554c05f-7c8a-41cb-b458-a074865ef5d5`
  - `status = pending`
  - `retry_count = 58`
  - `run_after = 2026-03-26 15:35:04 MDT`
- no new failed turn was spawned immediately on startup, which is the behavior that used to recreate the one-minute storm

This closes the two remaining proof items for the provider-cooldown work:

- fresh live `provider_rate_limited` classification exists
- project-bootstrap repair backoff survives a runtime restart without immediate churn

## Update 15:24 MDT

The next `0326a` slice removes one more rate-limit waste path inside a single turn.

Root cause:

- `callMainModel(...)` still treated `ErrRateLimited` as a generic transient model error
- that meant a router/provider cooldown refusal could create multiple failed `model_invocation` rows on the same turn before the outer turn handler finally enqueued the delayed retry

Concrete live evidence before the fix:

- turn `2323b41a-5499-40cf-97fb-ba994322fcc3`
- session `07ab860d-1c73-4e73-a160-3b8fe3c23998`
- three failed invocations in roughly three seconds:
  - `67302878-df3e-4f16-ba47-bb53bcfcfc22`
  - `0a49264e-dd3b-4654-80f3-f903311c3e18`
  - `623d6567-c626-4c3e-a216-ea9bcb6fcd52`
- all three had `error_code=provider_rate_limited`
- all three reported `all provider connections are rate limited`

New behavior:

- `ErrRateLimited` now exits `callMainModel(...)` immediately
- the outer turn failure path still enqueues the delayed retry as before
- but the same turn no longer burns extra internal model attempts first

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint|HandleTurnJobRateLimitedPersistsProviderRateLimitFailureClassification|HandleTurnJobRateLimitedDoesNotRetryInsideSingleTurn|HandleTurnJobRateLimitedUsesBackoffWhenNoRetryHint)$' -count=1`

What this closes:

- a single Anthropic cooldown refusal should now produce one failed invocation row, not a mini burst of same-turn failures
- provider cooldown windows should consume less quota pressure and less local write churn during exactly the periods when the system is already constrained

Live proof after deploy:

- the restarted runtime produced a fresh Anthropic cooldown refusal on task session `a4a2681b-3118-4e7e-bbad-1ad4e94d7e25`
- fresh failed turn:
  - `dd3d768d-fee4-4812-8d6d-7f47be9a0e14`
  - `retry_count = 5`
  - `completed_at = 2026-03-26 15:30:54 MDT`
  - error `model provider rate limited (retry_after=29m5s): all provider connections are rate limited ...`
- corresponding invocation activity in the last two minutes showed exactly one failed `provider_rate_limited` row for that turn:
  - `cddff5d6-8b69-4f47-923f-17bb2608114a`
- the delayed retry still landed correctly:
  - job `16762ca3-53cf-4d94-8b8a-b5a7932c7c8a`
  - `status = pending`
  - `retry_count = 6`
  - `run_after = 2026-03-26 15:59:59 MDT`
  - `rate_limit_jitter_applied = true`

This is the live before/after closure for the bug:

- before the fix, recent cooldown traffic was `84` failed invocations across only `28` turns, with the hot turns burning `3` same-turn failures each
- after the fix, the fresh post-deploy turn above burned `1` failed invocation and then moved directly to its delayed retry

## Update 15:43 MDT

I used the cooldown window to inspect the hottest recent async `project_task` turns for another deterministic waste family.

What I found:

- one hot task turn, `fb6ea882-1514-4987-b538-2f5b6998dc75`, rewrote the same existing file `config/pipeline-config-invalid.yaml` over and over inside one turn
- the repeated successful writes all returned the same shape:
  - `tool_name = file.write`
  - `path = config/pipeline-config-invalid.yaml`
  - `byte_size = 731`
  - `created = false`
- the assistant text between those writes was mostly blank or obvious tool-troubleshooting narration such as:
  - `Let me use cli_execute to create the test file:`
  - `It seems like something is off with the function calls.`
  - `Let me try a different approach`

Why that matters:

- this is not a blocked validation loop
- it is successful tool churn inside a single async task turn
- letting the turn keep paying for identical rewrites of the same existing file is wasted model/tool budget and usually a sign that the lane is stuck in the wrong local strategy

New behavior on the latest code:

- async `project_task` turns now classify repeated successful rewrites of the same existing file as same-turn churn when the runtime sees identical `file.write` output path plus identical `byte_size`
- the first rewrite is tolerated
- the second identical rewrite seeds the existing validation-guard path
- the third identical rewrite in the same turn now ends the turn early instead of letting the loop keep burning rounds
- this is scoped narrowly:
  - async `project_task` only
  - not recovery turns
  - only `file.write`
  - only when the file already existed (`created = false`)
  - only when the runtime sees the same normalized output path and identical `byte_size`

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdIdenticalSuccessfulFileWriteInSameTurn|HandleToolValidationResultsIgnoresSuccessfulFileWriteChurnWhenByteSizeChanges|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFileEditOldStringNotFoundInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFileReadNotFoundInSameTurn)$' -count=1`

What this closes:

- async task turns no longer get an unlimited leash for successful-but-identical file rewrite churn
- one more high-cost same-turn waste family now routes through the existing early-stop / fresh-continuation machinery instead of relying on max-tool-call caps

Current status of this slice:

- code is implemented and focused tests pass
- docs are updated
- runtime rebuild/restart is the next step before the next Anthropic rerun starts

## Update 15:48 MDT

I also upgraded the operator token report so the churn we just hardened is visible without ad hoc SQL.

New report sections in [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh):

- `Hot Rate-Limit Turns`
- `Duplicate Successful File Writes By Turn`

Smoke run:

- `scripts/token-usage-report.sh --hours 6 --limit 5`

Useful fresh output from that real run:

- repeated same-turn rate-limit amplification is now trivially visible in the report:
  - turn `2323b41a-5499-40cf-97fb-ba994322fcc3` -> `3` failed rate-limit invocations
- duplicate-write churn is now visible in one place:
  - turn `fb6ea882-1514-4987-b538-2f5b6998dc75`
  - path `config/pipeline-config-invalid.yaml`
  - `byte_size = 731`
  - `write_count = 11`
- the report also surfaced two more script-write churn families that the new runtime guardrail should now catch on future reruns:
  - `scripts/validate-stage-execution.sh` with `write_count = 14`
  - `scripts/validate-error-handling.sh` with `write_count = 9`

Why this matters:

- the runtime hardening is no longer a one-off anecdote from manual SQL spelunking
- we now have a repeatable operator report that can show whether the next Anthropic canary actually reduces these same-turn churn patterns

The expanded report also answered two open observability questions in one real run:

- `listening_eval` is not showing up on async `project` sessions in the current 6-hour window
- the only visible `listening_eval` traffic in that window is `organization` async on `claude-haiku-4-5-20251001`
- there are currently `0` sessions with active summarization-backoff metadata, so summarization backoff is not silently piling up across the live session set right now

## Update 16:06 MDT

I used the remaining Anthropic cooldown window for one more low-risk runtime hardening slice: background summarization jobs now defer cleanly when the runtime already knows they should wait.

What changed:

- the job worker now supports a first-class deferred-job path via `DeferredJobError`
- deferred jobs release their claim, move back to `pending`, keep a future `run_after`, and do not consume a worker attempt
- `chat_summarize` now checks both:
  - session-level summarization backoff metadata
  - summarization-provider cooldown from `NextSummarizationRunAfter(...)`
- if either gate says "wait", the summarization job returns a deferred-job signal before any model call

Why this matters:

- prior enqueue points were already cooldown-aware, but an already-claimed summarization job could still wake up, hit the model path, and fail
- this closes that remaining gap
- summarization is background work, so it should preserve provider/session cooldown instead of converting known wait conditions into noisy failures

Focused verification that passed:

- `go test ./internal/chat -run 'TestSummarizerRegisterJobs(RecordsBackoffOnFailure|DefersWhenSessionBackoffActive)$' -count=1`
- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerDeferredJobRequeuesWithoutConsumingAttempt$' -count=1`
- `go test -tags=integration ./internal/chat -run 'Test(NextSummarizationRunAfterReturnsProviderBackoffWhenSummaryProviderRateLimited|SummarizeJobHandlerDefersWhenProviderCooldownActive)$' -count=1`

What this closes:

- background summarization jobs no longer need to burn a failed attempt just to respect an already-known cooldown
- provider/session backoff is now preserved consistently at:
  - prompt-assembly enqueue
  - session-close enqueue
  - turn-runtime enqueue
  - claimed summarization job execution

What remains true:

- this does not change sync-chat summarization eligibility
- this does not reduce prompt history by itself; it only removes avoidable background failure churn during cooldown periods

## Update 16:18 MDT

I extended the same cooldown-preserving behavior to session cleanup's summary-consolidation path.

What changed:

- closed-session cleanup `summary_consolidation` now checks summarization provider cooldown before calling the model
- if the selected summarization route is still in cooldown, cleanup returns a deferred-job signal instead of attempting the consolidation call
- this uses the same worker-level `DeferredJobError` path that now powers `chat_summarize`

Why this matters:

- `summary_consolidation` is also background summarization work
- without this extension, the runtime could still spend failed Sonnet calls from cleanup even after `chat_summarize` itself had been made cooldown-aware
- this closes the other obvious background summarization hole in the runtime

Focused verification that passed:

- `go test -tags=integration ./internal/chat -run 'Test(SessionCleanerIntegrationSummaryConsolidation|SessionCleanerSummaryConsolidationDefersWhenProviderCooldownActive|SummarizeJobHandlerDefersWhenProviderCooldownActive|NextSummarizationRunAfterReturnsProviderBackoffWhenSummaryProviderRateLimited)$' -count=1`
- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerDeferredJobRequeuesWithoutConsumingAttempt$' -count=1`

What this closes:

- both background summarization entry points now preserve cooldown before model invocation:
  - `chat_summarize`
  - cleanup `summary_consolidation`

## Update 16:27 MDT

I also tightened the operator report for live monitoring:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh) now accepts fractional `--hours` values, so short windows like `--hours 0.25` work instead of erroring on integer casts

Why this matters:

- short-window live checks are the fastest way to confirm whether fresh runtime changes are reducing churn on real Anthropic traffic
- the old integer-only window made it clumsy to inspect "just the last 15 minutes" without ad hoc SQL

Fresh proof from the new short-window report:

- `scripts/token-usage-report.sh --hours 0.25 --limit 3`
- in that 15-minute window:
  - `0` rate-limit failures
  - `0` `listening_eval` invocations
  - `0` sessions with active summarization backoff metadata
- the report still highlights the hot recent task sessions and turns, but now at the exact live-monitoring granularity needed for the next rerun

## Update 16:35 MDT

Fresh live Anthropic proof on the hardened build:

- session `803a3410-c820-429e-991e-44f32efc8e2d`
- project `speaker-pipeline-ops-validation-fresh-20260325-rerun-7-restart-9`
- task `13` `Validate pipeline metrics and alerting hooks`

What happened:

- hot turn `32eb8232-243c-49db-bada-9c343e5fc459` ran to exactly `16` model invocations
- recorded token burn on that one turn was `582,615` total tokens
- the turn then completed and the session advanced to fresh turn `7` (`ad82246e-749e-4eb7-8086-3656142b156b`) instead of staying stuck indefinitely on the same turn

Why this matters:

- this is the first fresh live proof on recovered Anthropic traffic that the async `project_task` work-turn budget is actually acting as a ceiling in production
- the same 15-minute live window still showed:
  - `0` rate-limit failures
  - `0` `listening_eval`
  - `0` active summarization-backoff sessions

What it does not prove yet:

- it does not prove the task finished successfully
- it does prove the runtime is now willing to cut a hot turn over to a fresh turn instead of letting one task turn expand without bound

## Update 16:28 MDT

I used the fresh Anthropic traffic to look for the next safe hardening slice instead of guessing.

What the live report showed:

- `scripts/token-usage-report.sh --hours 0.25 --limit 6` now reports completed turns by `stop_reason`
- in the last 15 minutes:
  - `129` async `project_task` turns completed with `stop_reason=max_tool_calls`
  - only `5` async `project_task` turns completed with `stop_reason=validation_loop_blocked`
  - there were still `0` rate-limit failures and `0` `listening_eval` invocations

The important discovery was a new cross-turn churn family:

- hot session `f0157711-35ea-43cc-8b2d-76cd940d96c9` kept burning many tiny async task turns
- recent turns were a long chain of `3` completed model invocations each, all ending on `max_tool_calls`
- the tool mix across those turns was read-only discovery only:
  - `file.list`
  - `git.log`
  - `task.get`
  - `file.read`
  - `git.diff`
  - occasional `flow.get_template`
- recurring non-progress errors included `path_traversal` and `exit status 128`

That is not healthy bounded execution. It is cross-turn discovery churn that survives the same-turn guards.

I built the next runtime slice for that exact family:

- async `project_task` work lanes now stop after `5` consecutive `max_tool_calls` turns when every turn in the sequence is read-only discovery only
- the runtime marks the task lane `blocked` instead of auto-continuing another discovery-only pass
- the operator report now also reads native validation failures from `output.error`, which fixes a diagnostics blind spot around native tool results

Focused verification that passed:

- `go test ./internal/turn -run 'Test(ParseToolResultMessageUsesOutputErrorFallback|MaybeBlockRepeatedReadOnlyDiscoveryCapTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutationTurns)$' -count=1`
- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdIdenticalSuccessfulFileWriteInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFileReadNotFoundInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterSecondIdenticalFileEditOldStringNotFoundInSameTurn|ParseToolResultMessageUsesOutputErrorFallback|MaybeBlockRepeatedReadOnlyDiscoveryCapTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutationTurns)$' -count=1`

Current status of this slice:

- code complete
- focused tests green
- docs/spec updated
- not yet rebuilt/restarted into the live runtime

Why I stopped here:

- this is the first new slice after Anthropic recovered that is clearly data-backed and narrow
- deploying it will interrupt the current live runtime, so I kept this as a clean checkpoint first

## Update 16:42 MDT

I extended the cross-turn read-only discovery cutoff so it can catch the shell-based discovery turns that were still escaping the direct read-tool allowlist.

Why this was necessary:

- the fresh post-recovery capped turns were no longer always using `file.read` / `git.log` / `task.get` directly
- some of the hottest async `project_task` turns were spending the entire tool budget on `cli.execute` just to inspect the workspace:
  - `pwd`
  - `ls`
  - `cat`
  - `git diff`
  - `git log`
- because the existing cutoff only recognized direct read-only tools, those shell-wrapped discovery turns could still chain through `max_tool_calls`

What I changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - the read-only discovery cutoff now also treats these tools as read-only discovery:
    - `file.search`
    - `git.status`
    - `project.get`
    - `flow.get_execution`
  - it now parses persisted assistant `tool_calls` metadata on the turn and classifies `cli.execute` as read-only discovery only when the normalized command is inspection-only
  - the CLI classifier is conservative: it allowlists shell inspection commands such as `pwd`, `ls`, `cat`, `sed` (without `-i`), `grep`, `rg`, `git diff`, `git log`, and `git status`, and rejects mutation markers such as redirection, heredocs, `python -c`, `git commit`, `mv`, `cp`, `rm`, and similar write paths
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving:
    - read-only `cli.execute` counts toward the cross-turn discovery cutoff
    - mutating `cli.execute` does not

Focused verification that passed:

- `go test ./internal/turn -run 'Test(MaybeBlockRepeatedReadOnlyDiscoveryCapTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutationTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsTreatsReadOnlyCLIExecuteAsDiscovery|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutatingCLIExecute)$' -count=1`

Deployment status:

- rebuilt `./bin/ottercamp`
- restarted tmux `codex-e2e-20260324` serve/worker with `.env` loaded and `OTTERCAMP_MODE=development`
- `./bin/ottercamp health --output json` returned `status=ok`

Fresh post-restart live window:

- `scripts/token-usage-report.sh --hours 0.08 --limit 8`
- still `0` rate-limit failures
- still `0` `listening_eval`
- fresh async `project_task` `validation_loop_blocked` turns are still showing up on the hardened build

What is proven vs not yet proven:

- proven:
  - the new shell-based read-only discovery classifier is implemented, tested, and live
  - the runtime restarted cleanly on the new build
- not yet proven:
  - a fresh Anthropic task lane has not yet emitted a stop message explicitly showing the new repeated read-only `cli.execute` cutoff firing in production
  - the latest blocked turns in the post-restart window were still older guard families:
    - repeated `file.list (not_found)`
    - repeated `cli.execute (shell_injection)`
    - repeated identical successful `file.write` churn

So this slice is live, but its new proof target is still pending: a capped shell-discovery task lane needs to actually hit the new stop path.

## Update 16:47 MDT

I found and fixed the next miss immediately after deploying the shell-discovery classifier.

What the live data showed:

- hot session `fc516b1d-0343-450b-bf1c-dea4351c7c07`
- task `13` `Validate pipeline metrics and alerting hooks`
- task status was `review`
- that session had a long consecutive chain of pure read-only `max_tool_calls` turns:
  - turns `33` through `39`
  - all of them were discovery-only mixes such as `file.list`, `file.read`, `git.diff`, `git.log`, and `task.get`
- the cutoff did not fire there because the original implementation still exempted `review` tasks

That review exemption was wrong for the live product behavior. A review lane that spends many capped turns only rereading diff/log/task context is still runaway token churn.

I patched that runtime seam:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - repeated cross-turn read-only discovery churn is now handled for async `project_task` review lanes too
  - review lanes do **not** get marked `blocked` here
  - instead, the runtime ends the hot turn and queues a fresh `task_review_action` prompt so the next turn is forced back toward `flow.review_decision`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving the review-lane retry behavior

Focused verification that passed:

- `go test ./internal/turn -run 'Test(MaybeBlockRepeatedReadOnlyDiscoveryCapTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutationTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsTreatsReadOnlyCLIExecuteAsDiscovery|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutatingCLIExecute|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsRetriesReviewLane)$' -count=1`

Deployment status:

- rebuilt `./bin/ottercamp`
- restarted tmux `codex-e2e-20260324` serve/worker
- `./bin/ottercamp health --output json` returned `status=ok`

Fresh live evidence after the deploy:

- session `fc516b1d-0343-450b-bf1c-dea4351c7c07` is now on turn `41`
- the session now has a fresh user review-action prompt:
  - `Review only. Inspect the current deliverables and use flow.review_decision to approve or reject this review step.`

What is still not proven:

- I do not yet have a completed turn row showing the new repeated-read-only-review cutoff message itself on live Anthropic traffic
- the hot review session had already rolled into restart/retry behavior by the time the fix landed, so the fresh proof target is still the next review-discovery churn chain

## Update 16:50 MDT

The live proof for the review-lane cutoff has now landed.

Fresh production evidence on session `fc516b1d-0343-450b-bf1c-dea4351c7c07`:

- turn `47` `6e5d9181-d75c-495b-b7d4-2101d412bc8b`
- turn `52` `dce9c580-ade7-456f-9523-73dc113ef725`
- both completed with `stop_reason=validation_loop_blocked`
- both emitted the exact new system message family:
  - `Repeated read-only discovery churn across 5 consecutive max-tool-call turns using file.list, file.read, git.diff, git.log, task.get with recurring exit_status_128, path_traversal`

What that proves:

- the cross-turn read-only discovery cutoff is now live-proven on async `project_task` review lanes
- the runtime is no longer letting that review session burn unbounded chains of capped read-only turns
- the follow-on path is now the intended review retry loop rather than endless `max_tool_calls` continuation

Current remaining proof gap:

- I still do not have equivalent live proof for the shell-wrapped `cli.execute` read-only discovery variant
- the direct read-tool family is now proven in production

Follow-up hardening since that proof:

- review-lane cross-turn discovery churn now trips at `3` consecutive `max_tool_calls` turns instead of `5`
- the `dispatchTools(...)` budget-hit branch now calls the same read-only discovery cutoff helper before it appends `[Max tool calls reached. Turn ended.]`
- focused coverage now includes `TestDispatchToolsMaxToolCallsStopsReviewDiscoveryChurn`, which reproduces the exact budget-path miss that let review turns `59`/`60`/`61` end as plain `max_tool_calls`

Current live-proof status for that narrower fix:

- code is built, tested, and deployed
- the restart that rolled it out closed the two hot review sessions I was using as proof targets:
  - `fc516b1d-0343-450b-bf1c-dea4351c7c07`
  - `8dbd053a-b40a-4036-a59e-f33f66b8b9f5`
- so the new budget-path routing fix still needs fresh post-restart Anthropic traffic for live proof

## Update 17:18 MDT

I kept working offline while Anthropic stayed constrained and added one more deterministic same-turn cutoff plus a matching operator report view.

What shipped in code:

- async `project_task` turns now classify repeated `cli.execute` package-install attempts for the same package spec as same-turn churn
- the new fingerprint covers the concrete family we saw in the hottest recent turns:
  - `pip install pyyaml`
  - `pip3 install pyyaml`
  - `/usr/bin/python3 -m pip install --user pyyaml`
  - similar `python -m pip install` variants for the same package
- the stop is intentionally narrow: it does not target productive same-file script construction, only repeated package-install retries for the same package target in the same turn

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdPackageInstallAttemptInSameTurn|HandleToolValidationResultsIgnoresPackageInstallChurnWhenPackageChanges|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdIdenticalSuccessfulFileWriteInSameTurn|DispatchToolsMaxToolCallsStopsReviewDiscoveryChurn)$' -count=1`

Operator diagnostics now also include:

- `Repeated Package Install Attempts By Turn` in [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
- `Shell File Build / Readback Churn By Turn` in [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)

Fresh report proof on the recent two-hour window:

- turn `d40b49ee-a369-4f95-98d5-aa2b2ca08bbc` now shows up cleanly with `6` repeated install attempts in one turn
- the report surfaces the full variant set instead of forcing ad hoc SQL:
  - `pip install pyyaml`
  - `pip3 install pyyaml`
  - `/usr/bin/python3 -m pip install pyyaml`
  - `/Library/Developer/CommandLineTools/usr/bin/python3 -m pip install pyyaml`
  - `python3 -m pip install --user pyyaml`
  - `python3 -m pip install --target=./lib pyyaml`
- the same report now also shows the next highest-burn family waiting behind package installs:
  - shell-based file construction plus repeated readbacks on the same script path
  - example hotspot: turn `bf2139fd-5b8a-46b8-93b9-5f1ab2709934` with `7` shell file-build commands and `4` readback checks in one turn

Deployment status for this newest package-install cutoff:

- code and tests are complete locally
- I have not restarted the runtime on this latest slice yet, because the goal in this cooldown window was to batch offline hardening before the next fresh Anthropic run instead of repeatedly churning live sessions

## Update 17:42 MDT

The next offline hardening slice is now in code and unit-tested: async execution lanes no longer burn same-turn retries on ordinary provider-transient failures.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project_task` and non-bootstrap async `project` turns now treat `provider_transient_failure` as delayed retry work instead of retrying again inside the same turn
  - the new path enqueues a fresh `agent_turn` retry with backoff and ends the hot turn immediately
  - active project bootstrap sessions are explicitly excluded so they keep their existing bootstrap-specific provider failure handling
  - sync sessions are also excluded so normal interactive turns keep the old inline transient retry behavior

Why this was worth doing:

- recent live analysis showed that once a turn hit its first provider-transient failure, recovery inside the same turn was very rare
- the expensive pattern was late-turn tail churn:
  - useful work had already happened earlier in the turn
  - then the same turn paid for one or more transient failures before finally rate-limiting or exiting
- that makes async cross-turn retry a better fit than in-turn retry for this failure family

Focused verification that passed:

- `go test ./internal/turn -run 'Test(ShouldDeferTransientModelTurnFailure|HandleTurnJobAsyncProjectTaskTransientProviderEnqueuesRetryWithoutSameTurnRetry|HandleTurnJobAsyncProjectTaskTransientProviderRetryCapStopsRequeue|HandleTurnJobRateLimitedDoesNotRetryInsideSingleTurn|HandleTurnJobTransientInfrastructureEnqueuesRetry|HandleTurnJobTransientInfrastructureRetryCapStopsRequeue)$' -count=1`

What those tests prove:

- async `project_task` transient provider failures now produce exactly one failed invocation for the turn and then schedule delayed retry work
- transient provider retries now cap cleanly with a distinct exhausted-retries message
- the existing rate-limit and transient-infrastructure retry behavior still passes unchanged
- the helper gate correctly excludes:
  - sync task lanes
  - active project bootstrap sessions
  - rate-limited failures

What is still not proven:

- I have not yet restarted the runtime on this newest transient-provider slice
- so there is not yet fresh production evidence showing a real Anthropic `provider_transient_failure` that:
  - produces only one failed invocation row for the turn
  - then lands on the delayed retry path instead of same-turn churn

Current status:

- code complete
- unit-tested
- docs updated
- waiting for the next deliberate runtime roll so it can be proven on fresh Anthropic traffic

## Update 17:58 MDT

I kept going on the next deterministic async-task churn family and landed a narrow shell-builder cutoff in code.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project_task` turns now classify repeated successful shell-based `cli.execute` file construction targeting the same `scripts/`, `config/`, or `results/` path as same-turn churn
  - the classifier is intentionally narrow:
    - same target path only
    - successful `cli.execute` only (`exit_code = 0`)
    - shell builder shapes only, such as:
      - `cat > path`
      - `printf ... > path`
      - `python3 -c "with open('path', ...)"` 
  - the threshold is `3` attempts in one turn, which leaves room for one correction pass but cuts off the obvious script-construction spiral

Why this was worth doing:

- the operator report surfaced turns like:
  - `cc90162c-b1e3-40f6-8036-5f6cb27e0402`
  - `ae637e85-8b6b-4f15-9ed6-b9311a46f725`
- those turns were not just editing a script once or twice
- they were repeatedly rebuilding the same target path with a long series of shell wrappers:
  - `cat > scripts/validate-stage-execution.sh`
  - `printf ... > scripts/validate-stage-execution.sh`
  - `python3 -c "with open('scripts/validate-stage-execution.sh', 'a') ..."`
- that is exactly the kind of same-turn burn where direct file mutation tools should win instead of paying for more shell wrappers

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdShellFileBuildAttemptInSameTurn|HandleToolValidationResultsIgnoresShellFileBuildChurnWhenTargetPathChanges|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdPackageInstallAttemptInSameTurn|HandleToolValidationResultsIgnoresPackageInstallChurnWhenPackageChanges|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdIdenticalSuccessfulFileWriteInSameTurn|HandleToolValidationResultsIgnoresSuccessfulFileWriteChurnWhenByteSizeChanges)$' -count=1`

What is still not proven:

- this newest shell-builder cutoff is not deployed yet
- so there is not yet live evidence showing one of the known hot script-construction turns getting cut off by the new guardrail

## Update 17:48 MDT

I kept going on the same shell-driven churn family and added a narrower follow-on cutoff for repeated readbacks of the same shell-built target.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project_task` turns now classify repeated rereads of the same shell-built `scripts/`, `config/`, or `results/` path as same-turn churn
  - the new cutoff activates only after the turn has already successfully shell-built that exact target path
  - it currently covers:
    - successful `file.read` on that same exact path
    - successful read-only `cli.execute` inspection on that same exact path, such as:
      - `cat`
      - `sed`
      - `head`
      - `tail`
      - `grep`
      - `rg`
      - `wc`
      - `stat`
      - `file`

Why this was worth doing:

- the operator report still showed hot turns that were no longer mostly creating the same script again, but were still burning extra rounds rereading the exact same generated target
- examples from the live report included turns with combinations like:
  - `3` shell file builds
  - `4` to `7` readback checks
- that is a safer thing to cut than general project/task reads because it is scoped to:
  - async task lanes only
  - a same-turn shell-built target only
  - repeated inspection of that same target only

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdShellFileReadbackInSameTurn|ShellFileReadbackTargetFromCLICommandRecognizesReadOnlyCatCommand|HandleToolValidationResultsIgnoresShellFileReadbackChurnWithoutPriorShellBuild|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdShellFileBuildAttemptInSameTurn|HandleToolValidationResultsIgnoresShellFileBuildChurnWhenTargetPathChanges)$' -count=1`

What is still not proven:

- this newest shell-file readback cutoff is not deployed yet
- so there is not yet live evidence showing one of the known shell-builder/readback turns ending early on the new `duplicate_shell_file_readback_churn` path

## Update 17:52 MDT

I did not add another runtime cutoff in this slice. The next remaining high-burn family is less obviously safe to block, so I tightened diagnostics instead.

What changed:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - added `Written File Readback Churn By Turn`
  - this report groups turns that:
    - successfully `file.write` a concrete path
    - then repeatedly reread that same path via:
      - `file.read`
      - read-only `cli.execute` inspection such as `cat`, `sed`, `head`, `tail`, `grep`, `rg`, `wc`, `stat`, or `file`

Why I stopped at diagnostics here:

- the remaining hottest turns are not pure nonsense anymore
- they often mix:
  - a real file write
  - some verification
  - a real execution step
  - then extra rereads of the same generated file
- that may still deserve a later cutoff, but it is not yet safe enough to block blindly without better measurement

Intended use:

- this new section gives us a cleaner before/after view on the next Anthropic canary
- if one turn family still dominates through repeated written-file readbacks, we can decide whether to:
  - broaden the runtime cutoff beyond shell-built files
  - or leave it alone because the rereads are actually paying for real debugging progress

## Update 17:58 MDT

I went one step further on the written-file reread family and landed a narrow runtime cutoff instead of leaving it as diagnostics only.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project_task` turns now classify repeated rereads of the same recently written `scripts/`, `config/`, or `results/` file as same-turn churn
  - this only activates after a successful `file.write` to that exact path in the same turn
  - it currently covers:
    - successful `file.read`
    - successful read-only `cli.execute` inspection such as `cat`, `sed`, `head`, `tail`, `grep`, `rg`, `wc`, `stat`, or `file`
  - a later successful `file.write` to that same path resets the reread window, so iterative edit-then-check loops still get another pass

Why this was safe enough to harden:

- the new report section showed concrete same-turn families like:
  - write `scripts/pipeline_config.py`, then reread it `4` times
  - write `scripts/validate-metrics-alerting.sh`, then reread it `3` times
- the narrower rule here is not “do not reread files”
- it is “after you have already successfully written this exact tracked target, do not spend a third same-turn reread on that unchanged file before mutating it again”

Focused verification that passed:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdWrittenFileReadbackInSameTurn|HandleToolValidationResultsIgnoresWrittenFileReadbackChurnAfterInterveningRewrite|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdShellFileReadbackInSameTurn|HandleToolValidationResultsIgnoresShellFileReadbackChurnWithoutPriorShellBuild)$' -count=1`

What is still not proven:

- this newest written-file reread cutoff is not deployed yet
- so there is not yet live evidence showing one of the known `file.write -> reread -> reread -> reread` task turns ending early on the new `duplicate_written_file_readback_churn` path

## Update 18:03 MDT

I added one more operator-side improvement to make the next canary restart easier to evaluate.

What changed:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now accepts `--session <uuid>`
  - all report sections honor that filter, including:
    - top turns
    - churn families
    - stop reasons
    - failures
    - summarization backoff rows

Why this was worth doing:

- the next validation step is not “look at the whole last 24 hours again”
- it is “watch one canary session on the newest build and see which burn families are left”
- the session filter now lets us answer that directly with one command instead of ad hoc SQL

Smoke proof:

- `bash -n scripts/token-usage-report.sh`
- `scripts/token-usage-report.sh --hours 6 --limit 5 --session 05797f29-cf07-48fa-852d-7c081bbab17d`

That session-scoped run correctly narrowed the report to the one hot async task lane and showed:

- `51` invocations
- `1.834M` total tokens
- top turns `b1d57839-...`, `88ff2e07-...`, `446b9276-...`
- the exact churn sections for that one session only, including:
  - duplicate successful writes
  - written-file readback churn
  - shell file build / readback churn

## Update 18:08 MDT

I added one more operator-facing section to expose queue drift that could mask the next live token measurements.

What changed:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now includes `Pending Agent Turn Backlog`
  - it groups pending `agent_turn` jobs by session and reports:
    - `scope_type`
    - `mode`
    - `session_status`
    - pending job count
    - oldest/newest `run_after`
    - oldest/newest `created_at`

Why this matters:

- the short recent window is still all Anthropic rate limits
- if the queue also carries a tail of stale pending async sessions, that can distort what “the next run” really means operationally
- this does not change runtime behavior yet; it just makes the backlog visible in the same report we are already using for token burn

Smoke proof:

- `bash -n scripts/token-usage-report.sh`
- `scripts/token-usage-report.sh --hours 0.25 --limit 8`

That fresh report immediately surfaced an old pending backlog, for example:

- `856ae42a-5ed8-4c53-bf70-53dc6a9e0c46` `project async active` with pending `run_after` on `2026-03-25 12:07:27 MDT`
- `da3ba22a-5bf9-4467-8ec1-61caca1f0235` `project_task async active` with pending `run_after` on `2026-03-25 13:48:33 MDT`

## Update 18:10 MDT

I turned that backlog signal into a narrow cleanup plus a better reason breakdown.

What changed:

- [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `claimPendingAgentTurns(...)` now dead-letters project dispatches that the existing claim SQL already knows are permanently stale:
    - `project_bootstrap` when the session bootstrap state is no longer `active`
    - `project_execution_continuation` / `project_continuation_resume` when the project has no unfinished tasks
  - this is intentionally narrower than “old pending backlog cleanup”; paused task/project retries are still preserved
- [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - added focused coverage for both stale-project dispatch classes
  - corrected the older same-session bootstrap claim test to match the worker’s actual long-standing behavior: once the newer same-session dispatch is claimed, the older sibling is dead-lettered as superseded
- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - `Pending Agent Turn Backlog` now also shows:
    - `current_turn_id`
    - `is_paused`
    - `stale_project_source`
    - derived `backlog_state`

Why this matters:

- the raw backlog count was misleading
- the oldest pending rows are mostly paused sessions that should stay parked
- a smaller slice was genuinely stale project bootstrap / continuation queue debt that could be retired safely instead of lingering forever

Focused proof:

- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerClaimPendingAgentTurns(DeadLettersInactiveProjectBootstrapDispatch|DeadLettersSettledProjectContinuationDispatch|SkipsOlderProjectBootstrapWhenNewerSameSessionAlreadyClaimed|IgnoresStalePendingTurnWithoutActiveExecution|IgnoresStalePendingCurrentTurnWithoutLiveOwnership)$' -count=1`
- `bash -n scripts/token-usage-report.sh`
- `scripts/token-usage-report.sh --hours 24 --limit 8`

The improved backlog view now makes the queue state legible:

- `856ae42a-5ed8-4c53-bf70-53dc6a9e0c46` shows `backlog_state=paused`
- `3764d5d5-8c0e-45b4-87be-0b94c16e58e3` shows `backlog_state=stale_project_source`

So the next restart can clear the truly stale project rows without confusing them with intentionally parked paused work.

## Update 18:17 MDT

I added one more operator-only slice so the next Anthropic window is easier to interpret without changing runtime behavior again.

What changed:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now includes `Repeated Script Execution By Turn`
  - it extracts `cli.execute` calls that invoke `bash` / `sh` / `zsh` / `python` against `scripts/`, `config/`, or `results/` targets
  - it reports:
    - `turn_id`
    - `session_id`
    - `script_path`
    - execution count
    - first/last sequence number in the turn

Why this matters:

- the hottest completed async task turns are still CLI-heavy
- the remaining question is whether the burn is mostly legitimate edit-then-run loops or repeated execution of the same generated script in one hot turn
- this section gives us that answer quickly once Claude quota opens again

Smoke proof:

- `bash -n scripts/token-usage-report.sh`
- current six-hour sample only shows two repeated-script cases, both at exactly two runs:
  - `bf2139fd-5b8a-46b8-93b9-5f1ab2709934` on `scripts/validate-stage-execution.sh`
  - `7c489e92-6cec-4246-885a-a65500c91cc6` on `scripts/validate-metrics-alerting.sh`

So this is a measurement slice, not a new runtime cutoff. The current data does not justify a safe hard stop on script reruns yet.

## Update 18:19 MDT

I added provider cooldown visibility to the same operator report so we stop guessing when Anthropic is actually available again.

What changed:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now includes `Provider Connection Health`
  - it reports:
    - provider connection display name
    - provider slug
    - `health_status`
    - `is_enabled`
    - `failover_priority`
    - `max_concurrent`
    - `health_rate_limited_until`
  - when `--session` is supplied, it scopes the provider-health view to that session’s organization

Why this matters:

- the last 30 minutes are still pure Anthropic 429s with no successful invocations
- we already persist cooldown windows on `provider_connection.metadata`
- exposing that directly in the report tells us when a fresh canary is worth launching instead of probing blindly

Current live signal from the DB:

- `pearl-swh-me` is `rate_limited` until `2026-03-27 11:00:00 MDT`
- `claude-swh-me` is `rate_limited` until `2026-03-26 20:59:58 MDT`
- `Anthropic Primary` is currently `unavailable`

This is still an operator-observability slice only. No runtime behavior changed here.

## Update 18:21 MDT

I added one more measurement slice to tell us whether the remaining rate-limit churn is happening before or after connection routing.

What changed:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now includes `Rate-Limit Failure Routing Split`
  - it groups `provider_rate_limited` failed invocations into:
    - `pre_routing` when `provider_connection_id` is null
    - `post_routing` when a specific provider connection had already been selected

Why this matters:

- if almost all current cooldown churn is `pre_routing`, the next runtime optimization is a router-aware defer path before more turn work happens
- if the failures are mostly `post_routing`, then the remaining work belongs deeper in gateway/connection reservation behavior instead

Current signal:

- the last 30 minutes were entirely `pre_routing`
- that means the router is already telling us “all connections cooling down” before a specific connection is chosen

So the next possible runtime slice, if we decide it is worth it, is a preflight defer for async turns when routing returns an all-connections-cooling-down backoff.

## Update 18:37 MDT

I implemented that router-aware preflight defer for async execution turns.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project` and `project_task` turns now ask the model gateway for an availability preflight immediately after profile resolution and before tool resolution / prompt assembly
  - when that probe returns a real `ErrRateLimited`, the turn now exits directly into the existing delayed retry path instead of assembling prompt context, appending an assistant placeholder, or creating a no-op `agent_turn` invocation row
  - non-rate-limit probe failures are ignored and fall back to the normal model path, so this slice only changes the known all-connections-cooling-down case
- [`internal/gateway/client.go`](../internal/gateway/client.go)
  - `LiveModelGateway` now implements the optional availability probe by reusing router selection
  - router-level `ConnectionsRateLimitedError` is translated into the ordinary turn-level `ErrRateLimited`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused async tests proving:
    - rate-limited preflight defers before prompt assembly
    - non-rate-limit probe errors do not short-circuit normal execution

Focused verification:

- `go test ./internal/turn -run 'Test(HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersBeforePromptAssembly|HandleTurnJobAsyncProjectTaskAvailabilityProbeFallsBackOnNonRateLimitError|HandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint|HandleTurnJobRateLimitedDoesNotRetryInsideSingleTurn)$' -count=1`
- `go test ./internal/gateway -run '^$' -count=1`

Why this is worth doing even though it changes telemetry shape:

- the recent `Rate-Limit Failure Routing Split` showed the current cooldown churn is almost entirely `pre_routing`
- that means the router already knows no Anthropic connection can be selected
- in that case, spending prompt assembly plus a synthetic failed `agent_turn` invocation is wasted work

What is still not live-proven yet:

- I have not restarted the runtime on this newest slice yet
- I also have not yet observed a fresh cooldown retry on the new binary to confirm the intended effect in production:
  - delayed retry still enqueued
  - no prompt assembly for that turn
  - no new pre-routing `agent_turn` invocation row created for the deferred attempt

## Update 18:40 MDT

While the cooldown-preflight proof window was still pending, the live worker exposed another cheap waste family: blocked validation-loop task sessions were being requeued by worker repair and then immediately suppressed by the turn engine.

What changed:

- [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `RequeueActiveExecutionSessionsWithoutTurns(...)` now skips async `project_task` sessions when the owning task is already:
    - `work_status = blocked`
    - and `metadata.agent_turn_validation_guard.blocked = true`
  - this is deliberately narrow: it only suppresses worker recovery when the runtime has already persisted the same validation-loop block that `HandleUserMessage(...)` uses to skip dispatch
- [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - added `TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsSkipsValidationLoopBlockedSession`
  - kept the nearby recovery-halt exceptions covered so review recovery retries still work where intended

Focused verification:

- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerRequeueActiveExecutionSessionsWithoutTurns(SkipsValidationLoopBlockedSession|SkipsRecoveryHaltLoop|AllowsReviewRecoveryHaltRetry|AllowsSyntheticReviewRecoveryRetry|AllowsTaskReviewActionRetry)$' -count=1`

Live proof:

- rebuilt `./bin/ottercamp`, restarted tmux `codex-e2e-20260324`, and `./bin/ottercamp health --output json` returned `status=ok`
- hot blocked review session `3d360d34-382d-4847-837b-4b067036610c` had been minting fresh `agent_turn` jobs continuously before this slice
- on the new worker, that session stopped at the single startup recovery pass:
  - newest job timestamp stayed fixed at `2026-03-26 18:39:04.073 MDT`
  - no newer `agent_turn` rows appeared for that session in the follow-up check

So that worker-side queue churn is now cut off. The remaining pending proof item is still the async cooldown preflight on a fresh live retry after `18:45 MDT`.

## Update 18:49 MDT

The async cooldown-preflight slice is now live-proven.

Live proof:

- after the restarted runtime crossed the first scheduled retry window, the due non-paused async sessions rolled forward to fresh delayed retry jobs:
  - project session `3764d5d5-8c0e-45b4-87be-0b94c16e58e3`
    - old job `done` at `run_after=2026-03-26 18:45:23 MDT`
    - fresh replacement `pending` at `created_at=2026-03-26 18:45:27 MDT`
    - new `run_after=2026-03-26 19:15:32 MDT`
  - task session `d602574b-392a-4aa5-9523-73830c77790d`
    - old job `done` at `run_after=2026-03-26 18:47:52 MDT`
    - fresh replacement `pending` at `created_at=2026-03-26 18:47:54 MDT`
    - new `run_after=2026-03-26 19:17:53 MDT`
  - task session `ff24b664-4a00-43a4-9bbc-8db3921f1b7f`
    - old job `done` at `run_after=2026-03-26 18:47:52 MDT`
    - fresh replacement `pending` at `created_at=2026-03-26 18:48:23 MDT`
    - new `run_after=2026-03-26 19:17:53 MDT`
- in the same check window, there were still zero fresh `model_invocation` rows for `invocation_purpose='agent_turn'` in the last 20 minutes

Why this matters:

- before this slice, router-level all-connections-cooling-down retries still created failed pre-routing `agent_turn` invocation rows
- on the new build, those due async retries are being rescheduled without spending prompt assembly plus synthetic failed invocation rows

So the cooldown optimization is no longer just unit-tested; it is now behaving live on real pending async sessions.

## Update 18:58 MDT

I tightened the same cooldown path one step further so async sessions can defer before creating a throwaway `chat_turn`, not just before prompt assembly.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `handleUserMessage(...)` now probes provider availability before `CreateForMessageAttempt(...)`
  - if the selected async `project` / `project_task` profile is already in a router-level cooldown window, the engine:
    - enqueues the delayed retry immediately
    - appends a session-scoped system message
    - returns without creating a `chat_turn`
  - the runtime now tracks whether availability was already probed on this dispatch attempt so the normal `runTurn(...)` path does not probe twice when the pre-turn probe hits a non-rate-limit error and falls back to ordinary execution
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - the focused preflight regression now proves:
    - rate-limited async dispatch defers with `0` turn records created
    - non-rate-limit probe failures still continue to a normal turn, but only after a single availability probe on that attempt

Focused verification:

- `go test ./internal/turn -run 'Test(HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersBeforePromptAssembly|HandleTurnJobAsyncProjectTaskAvailabilityProbeFallsBackOnNonRateLimitError|HandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint|HandleTurnJobRateLimitedDoesNotRetryInsideSingleTurn)$' -count=1`

Deployment status:

- rebuilt `./bin/ottercamp`
- restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json` returned `status=ok`

Current proof gap:

- the first fresh due cooldown retries on the new worker are not until the next `19:15-19:24 MDT` window
- the sessions I checked immediately after deploy (`40bc5db0-...`, `db21265f-...`) had already burned their `18:54 MDT` failed turns before this narrower patch was live
- so this slice is:
  - code-complete
  - tested
  - deployed
  - but still waiting on the next due cooldown retry to prove:
    - delayed retry still rolls forward
    - no new `model_invocation` row is created
    - and now also no new `chat_turn` row is created

## Update 19:24 MDT

I used the Anthropic cooldown window to harden a different queue seam that was hiding runnable work behind dead backlog.

What changed:

- [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `claimPendingAgentTurns(...)` now dead-letters orphaned stale `project_task` execution dispatches when all of the following are true:
    - async `project_task` session is still `active`
    - the session has an active `flow_node_execution`
    - `current_turn_id IS NULL`
    - the dispatch is already past the stale-claim threshold
    - and there is no matching `chat_turn` for that exact message attempt
  - after purging those stale rows, the worker immediately runs `RequeueActiveExecutionSessionsWithoutTurns(...)` so a fresh current-state dispatch can be recreated instead of leaving the session blocked behind the old pending row
- [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - added `TestJobWorkerClaimPendingAgentTurnsRequeuesStaleOrphanTaskExecutionDispatch`

Focused verification:

- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerClaimPendingAgentTurns(RequeuesStaleOrphanTaskExecutionDispatch|DeadLettersTerminalMessageAttemptDispatch|IgnoresStalePendingTurnWithoutActiveExecution|IgnoresStalePendingCurrentTurnWithoutLiveOwnership)$' -count=1`

Live proof:

- rebuilt `./bin/ottercamp`, restarted tmux `codex-e2e-20260324`, and `./bin/ottercamp health --output json` returned `status=ok`
- on startup, the new worker logged:
  - `job queue: dead-lettered stale orphan task execution dispatches before claim count=22`
- direct DB verification after that sweep:
  - non-paused orphan stale `project_task` dispatches of that exact shape dropped to `0`
  - the only remaining rows in the raw orphan query belonged to paused projects (`rerun-45`, `rerun-41`), so they are correctly excluded from claim as parked backlog rather than runnable work

This does not restore Anthropic capacity by itself, but it removes another source of misleading queue pressure and ensures active task executions are not blocked behind dead ancient dispatches once provider capacity returns.

## Update 19:36 MDT

I tightened the paused-project queue path so paused work stops polluting the due `agent_turn` backlog.

What changed:

- [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `RequeueActiveExecutionSessionsWithoutTurns(...)` now suppresses async `project_task` recovery dispatches when the parent project is paused
  - `claimPendingAgentTurns(...)` now dead-letters already-due paused `agent_turn` rows for both async `project` and async `project_task` sessions with:
    - `last_error = 'purged paused project dispatch during claim'`
- [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - added `TestJobWorkerClaimPendingAgentTurnsDeadLettersPausedProjectDispatches`
  - added `TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsSkipsPausedProjectUntilResume`

Focused verification:

- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(ClaimPendingAgentTurnsDeadLettersPausedProjectDispatches|RequeueActiveExecutionSessionsWithoutTurnsSkipsPausedProjectUntilResume|RequeuePendingTurnsWithoutJobs(RequeuesAfterProjectResume|SkipsPausedAndArchivedProjects)|ClaimPendingAgentTurns(RequeuesStaleOrphanTaskExecutionDispatch|DeadLettersInactiveProjectBootstrapDispatch|DeadLettersSettledProjectContinuationDispatch))$' -count=1`

Live proof:

- rebuilt `./bin/ottercamp`, respawned tmux `codex-e2e-20260324`, and `./bin/ottercamp health --output json` returned `status=ok`
- due paused async `agent_turn` backlog dropped from `12` to `0` on the restarted worker
- aggregate paused-project queue state now includes:
  - `dead_letter | purged paused project dispatch during claim | 20`
  - no remaining `pending` rows for paused async project / task sessions with `run_after <= now()`

Why this matters:

- previously, claim SQL already refused paused work, but `RequeueActiveExecutionSessionsWithoutTurns(...)` could still recreate paused task-session recovery jobs
- that meant paused projects accumulated misleading due backlog without ever becoming runnable
- this slice closes that loop while keeping resume behavior intact: when a project is unpaused, the existing requeue passes can mint a fresh current-state dispatch instead of reviving the stale paused row

## Update 19:56 MDT

I found and fixed one remaining cooldown edge case in async task retries.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - pre-turn availability deferral no longer disables itself once `retry_count >= maxRateLimitRetries` for async `project` and `project_task` sessions
  - that old gate was safe for synchronous in-turn retry exhaustion, but it was wrong for queued async cooldown retries because the worker legitimately keeps rolling those jobs forward beyond the original retry cap during provider backoff windows
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added `TestHandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersPastRetryCapWithoutTurn`

Focused verification:

- `go test ./internal/turn -run 'Test(HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersBeforePromptAssembly|HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersPastRetryCapWithoutTurn|HandleTurnJobAsyncProjectTaskAvailabilityProbeFallsBackOnNonRateLimitError|HandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint|HandleTurnJobRateLimitedDoesNotRetryInsideSingleTurn)$' -count=1`

Live proof:

- before the patch, task session `d602574b-392a-4aa5-9523-73830c77790d` hit the bad path:
  - due retry at `19:47:55 MDT`
  - fresh failed turn `28c59f89-0f93-4ceb-a3ca-a93a077d34a9` created at `19:47:59 MDT`
  - no new `model_invocation` row
  - job still rolled forward to retry `7`
- after deploying the fix, task session `40bc5db0-bb71-4a26-a1ac-ac4b11fb6cd2` proved the corrected behavior:
  - old due job `a4ce79aa-35a9-4745-b20b-e175234962e5` at `19:54:35 MDT`
  - fresh retry job `2c0fb337-b15a-4d2a-8a4c-ba1db846f00b` created the same minute with `run_after=20:24:48 MDT` and `retry_count=6`
  - last `chat_turn` stayed `4d7eae1c-8d71-4b6b-bd9c-712a35893839` from `18:54:02 MDT`
  - last `agent_turn` invocation stayed `f9b92830-77ec-4335-b393-12a0ec528bb3` from `18:23:44 MDT`
  - so the async task retry now rolls forward without creating either a new turn or a new invocation while Anthropic is still cooling down

## Update 20:17 MDT

I found the last remaining cooldown-churn scope and fixed it too.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - model-availability preflight now also applies to async `organization` sessions, not just async `project` and `project_task`
  - the same async cooldown preflight now stays active past the old retry cap for org scope too
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added `TestHandleTurnJobAsyncOrganizationRateLimitedPreflightDefersPastRetryCapWithoutTurn`

Focused verification:

- `go test ./internal/turn -run 'Test(HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersBeforePromptAssembly|HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersPastRetryCapWithoutTurn|HandleTurnJobAsyncOrganizationRateLimitedPreflightDefersPastRetryCapWithoutTurn|HandleTurnJobAsyncProjectTaskAvailabilityProbeFallsBackOnNonRateLimitError|HandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint|HandleTurnJobRateLimitedDoesNotRetryInsideSingleTurn)$' -count=1`

Live proof:

- before the patch, org async canary session `b1000247-b94d-4c14-a942-4b90dc7e8727` still hit the old pre-routing churn path at `20:13:40 MDT`:
  - fresh failed turn `d103c0f9-1666-4a9e-9ed1-e9a096cbed9c`
  - fresh failed invocation `7f7f40db-cd35-4b94-9ae4-b2218859df8c`
  - invocation had `provider_connection_id = NULL`, so this was the router-level all-connections-rate-limited case, not a live routed connection
- after deploying the org-scope fix, I forced the same pending org retry due immediately:
  - old pending job `f401d567-f030-4316-826e-2dd221b8a367` was pulled forward from `20:43:45 MDT`
  - fresh retry job `e838b49a-564f-4121-b1b6-071fbd5ea617` was created at `20:16:41 MDT` with `run_after=20:46:48 MDT` and `retry_count=4`
  - last turn stayed `d103c0f9-1666-4a9e-9ed1-e9a096cbed9c`
  - last invocation stayed `7f7f40db-cd35-4b94-9ae4-b2218859df8c`
  - so org async retries now also roll forward without creating a new turn or a new invocation while Anthropic is still in an all-connections-cooling-down state

## Update 20:31 MDT

I used the remaining provider cooldown window for a safe operator-surface slice instead of another speculative runtime cutoff.

What changed:

- [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
  - `ottercamp db token-usage` now includes:
    - completed turns grouped by `stop_reason`
    - provider connection health with persisted `health_rate_limited_until`
    - `provider_rate_limited` failures split into `pre_routing` vs `post_routing`
    - pending `agent_turn` backlog grouped by session with:
      - `current_turn_id`
      - `is_paused`
      - `stale_project_source`
      - derived `backlog_state`
- [`cmd/ottercamp/main_db_integration_test.go`](../cmd/ottercamp/main_db_integration_test.go)
  - the DB integration slice now seeds:
    - provider health rows
    - pre-routing and post-routing `provider_rate_limited` failures
    - a pending async `agent_turn` job
    - a completed turn with `stop_reason`

Focused verification:

- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution' -count=1`

Live smoke:

- `go run ./cmd/ottercamp db token-usage --output json --hours 1 --limit 5`
- the returned JSON now includes:
  - `completed_by_stop_reason`
  - `provider_health`
  - `rate_limit_routing_split`
  - `pending_agent_turn_backlog`
- live smoke at `20:30 MDT` showed:
  - `provider_health` rows for all 4 configured connections
  - `rate_limit_routing_split` present with current `pre_routing` failures
  - `pending_agent_turn_backlog` present with 5 queued async sessions

Why this matters:

- it closes the remaining “shell script only” visibility gap for the most important live rerun diagnostics
- when Anthropic quota opens again, the next canary window can be measured directly from product CLI output instead of ad hoc SQL plus the shell wrapper

## Update 20:56 MDT

I used the remaining offline window for one more operator-surface upgrade instead of guessing at another runtime cutoff.

What changed:

- [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
  - `ottercamp db token-usage` now also includes:
    - `repeated_package_installs`
    - `shell_file_build_readback_churn`
- [`cmd/ottercamp/main_db_integration_test.go`](../cmd/ottercamp/main_db_integration_test.go)
  - the integration fixture now seeds:
    - repeated `pip` / `python -m pip install` calls for the same package in one turn
    - shell-based file build plus readback assistant calls on the same script path

Focused verification:

- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`

Live smoke on the rebuilt CLI:

- `set -a && source .env && set +a && ./bin/ottercamp db token-usage --output json --hours 6 --limit 3`
- the JSON now includes:
  - `repeated_package_installs`
  - `shell_file_build_readback_churn`
- live rows surfaced exactly the current high-burn families:
  - turn `d40b49ee-a369-4f95-98d5-aa2b2ca08bbc` with `6` repeated `pyyaml` install attempts
  - turn `bf2139fd-5b8a-46b8-93b9-5f1ab2709934` with `7` shell file builds and `4` readback checks

Deployment status:

- this slice is CLI/operator-only
- I rebuilt `./bin/ottercamp` for local use, but did not restart tmux serve/worker because runtime behavior is unchanged

## Update 20:44 MDT

I tightened one more package-install churn edge that showed up during offline review of the hot turn family.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - repeated same-package `cli.execute` install churn now carries an observed attempt count from the same-turn message scan
  - async task early-stop logic now uses that observed count before the persisted validation-guard counter, so the turn stops on the third install even if the model interleaves failed import-check probes between the install attempts
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added `TestHandleToolValidationResultsStopsAsyncTaskTurnAfterThirdPackageInstallAttemptWithInterleavedChecks`
  - the focused slice also reran the existing direct package-install churn stop, the package-change exemption, the shell-file-build stop, and the review retry path

Focused verification:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThird(PackageInstallAttemptInSameTurn|PackageInstallAttemptWithInterleavedChecks|ShellFileBuildAttemptInSameTurn)|HandleToolValidationResultsIgnoresPackageInstallChurnWhenPackageChanges|HandleUserMessageRetriesRepeatedReviewActionRequiredFailures)$' -count=1`

Why this matters:

- the old cutoff only stopped quickly when the identical install attempts were consecutive at the validation-guard layer
- a hot task turn could still burn multiple rounds on:
  - `pip install pyyaml`
  - `python3 -c "import yaml"`
  - `pip3 install pyyaml`
  - another import probe
  - `/usr/bin/python3 -m pip install --user pyyaml`
- with this slice, the turn now recognizes that as the same deterministic package-install churn family and stops on the third observed install instead of paying for another model cycle

Deployment status:

- code and focused tests are complete locally
- the runtime has not been restarted on this newest slice yet

## Update 21:00 MDT

I found and fixed one more offline queue-churn seam that was still making settled async project sessions look runnable.

What changed:

- [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - added a shared retirement path for settled `project_execution_continuation` messages:
    - `RequeueActiveProjectSessionsWithoutTurns(...)` now fails the pending continuation message in place when the project has zero unfinished tasks
    - `RequeueStrandedUserMessageTurns(...)` now applies the same retirement rule for pending continuation messages that have no turn history yet
  - retirement writes the explicit message error:
    - `project continuation no longer needed; all project tasks settled`
- [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - added `TestJobWorkerRequeueStrandedUserMessageTurnsRetiresSettledProjectContinuation`
  - tightened `TestJobWorkerRequeueActiveProjectSessionsWithoutTurnsIgnoresStalePendingDispatch` so it keeps one unfinished project task and still exercises the fresh-requeue path

Focused verification:

- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RequeueStrandedUserMessageTurns(RetiresSettledProjectContinuation|IgnoresNewerFailedAssistantStub)?|RequeueActiveProjectSessionsWithoutTurns(RetiresSettledContinuationProjects|SkipsFinalMessages|IgnoresStalePendingDispatch|PreservesRateLimitBackoff))$' -count=1`

Live proof after rebuild and tmux restart:

- runtime health stayed green: `./bin/ottercamp health --output json` returned `status=ok`
- after the next scheduler tick, there were `0` fresh `agent_turn` rows in the last `90s` for the two settled async project sessions that had been churning:
  - `1a9edb0a-a817-46b1-975d-4d96c8164bcb`
  - `ec26eddb-66be-42a5-9859-64cb24c7c820`
- their previously pending continuation messages are now failed in place:
  - `ae5f0821-0135-4707-b571-f2ab017dcbcd`
  - `8936834d-76a3-4e6b-9a5a-084c1b5eafab`
- both now carry:
  - `project continuation no longer needed; all project tasks settled`

Why this matters:

- the old claim-time purge was only trimming the symptom
- the worker was still recreating the same stale continuation work from a second requeue path
- with both requeue paths fixed, those settled project sessions are no longer polluting the runnable queue with dead-on-arrival continuation dispatches

## Update 21:18 MDT

Anthropic traffic is active again, which exposed the next concrete hot-turn family under real load.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - narrowed `handleTaskFileWriteWrongPath(...)` so it no longer silently rewrites `file.write` across strong artifact-family boundaries
  - example of the now-blocked rewrite family:
    - `tests/test_pipeline_logger.py` or `results/logging-results.md`
    - silently retargeted into the execution deliverable `src/pipeline_logger.py`
  - the engine still rewrites obviously equivalent generic document aliases to the intended deliverable target; it now only skips the rewrite when both attempted path and target path have known but different artifact families
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added `TestHandleTaskFileWriteWrongPathSkipsCrossArtifactFamilyRewrite`
  - reran the existing canonical-path rewrite cases to confirm the narrower rule still preserves intended rewrite behavior for recovery-target and inferred-document aliases

Focused verification:

- `go test ./internal/turn -run 'TestHandleTaskFileWriteWrongPath(RewritesToRecoveryTarget|SkipsCrossArtifactFamilyRewrite|SkipsNonExecutionFirstTasks|RewritesToInferredTestExecutionTarget|RewritesScenarioExecutionPlanToCanonicalTarget|RewritesValidationExecutionDocumentToCanonicalTarget|RewritesGenericDocumentPathToCanonicalTarget|RewritesToCheckpointTargetWhenPreferredUnknown)$' -count=1`

Live read after rebuild and tmux restart:

- runtime health stayed green: `./bin/ottercamp health --output json` returned `status=ok`
- in the fresh post-deploy window, there were `0` new async task assistant `file.write` calls targeting `tests/`, `test/`, or `results/`
- the only fresh `mismatched_deliverable_context` rows were on session `75a97d7c-cc59-4f3f-970c-775d9226e908`, and those turns were read-only discovery churn:
  - `7aa49903-c1c1-451f-bad8-aab69b60eb86`
  - `41d0e2af-b0e8-4a6f-bf71-a49f41c92737`
  - `637930d4-6602-40d2-b546-0d3ba663d3e0`
- those turns did not include any new `file.write` attempts to `tests/` or `results/`; they were already inspecting the polluted deliverable and then stopped via `max_tool_calls` / `validation_loop_blocked`

Why this matters:

- the old behavior hid a wrong-path mutation by mutating the tool call itself, which is how support artifacts could overwrite the primary deliverable and trigger more recovery churn
- the narrowed rewrite rule keeps the intended “canonicalize obvious aliases” behavior while removing the most damaging silent retarget case that showed up in live traffic

## Update 21:35 MDT

I shipped one narrow runtime slice aimed at the new post-`21:18` async task burn family.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project_task` lanes now classify pure same-turn read-only discovery rounds as deterministic churn
  - the classifier only applies when the current turn has no mutation tools anywhere in its assistant tool-call history
  - it fingerprints the round family from persisted assistant `tool_calls` metadata and stops on the third discovery-only round in the same turn instead of paying for another continuation
  - review lanes now route that same-turn stop through the review retry path with a fresh `task_review_action` prompt instead of generic “take a concrete mutation step” messaging
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added:
    - `TestHandleToolValidationResultsStopsAsyncTaskTurnAfterThirdReadOnlyDiscoveryRoundInSameTurn`
    - `TestHandleToolValidationResultsIgnoresReadOnlyDiscoveryRoundChurnAfterMutationInSameTurn`
    - `TestHandleToolValidationResultsRetriesReviewAfterSameTurnReadOnlyDiscoveryChurn`
  - reran the adjacent same-turn churn slice plus the existing multi-turn read-only discovery cap slice

Focused verification:

- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdReadOnlyDiscoveryRoundInSameTurn|HandleToolValidationResultsIgnoresReadOnlyDiscoveryRoundChurnAfterMutationInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdWrittenFileReadbackInSameTurn|HandleToolValidationResultsIgnoresWrittenFileReadbackChurnAfterInterveningRewrite|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdPackageInstallAttemptInSameTurn)$' -count=1`
- `go test ./internal/turn -run 'Test(MaybeBlockRepeatedReadOnlyDiscoveryCapTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutationTurns|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsTreatsReadOnlyCLIExecuteAsDiscovery|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsIgnoresMutatingCLIExecute|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsRetriesReviewLane|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdReadOnlyDiscoveryRoundInSameTurn|HandleToolValidationResultsIgnoresReadOnlyDiscoveryRoundChurnAfterMutationInSameTurn)$' -count=1`

Deployment status:

- rebuilt `./bin/ottercamp`
- restarted tmux `codex-e2e-20260324` `serve` and `worker --concurrency 2`
- `./bin/ottercamp health --output json` returned `status=ok`

Fresh live read after deploy:

- Anthropic traffic resumed immediately after the restart
- the new live hot family is **not** the work-lane same-turn discovery case I just cut
- it is a review-lane cross-turn discovery loop on session `b93b49f3-ca00-472f-9531-2adf6198f374`
- consecutive review turns:
  - `b769c519-95d4-408b-91c3-c949846d1b52`
  - `db73a3fe-0bb2-4994-a4ef-eade59dbeb52`
  - `b0ef8a5f-f97e-4e6e-933c-bed3527db160`
  - `162e432f-bfb4-40a5-98ea-d68d8559a85c`
- all four completed with `stop_reason=max_tool_calls`
- the assistant tool-call families on those turns were still pure read-only discovery:
  - `file.list`
  - `task.get`
  - `git.log`
  - `git.diff`
  - `file.read`
- there were no fresh system messages containing `read-only discovery churn` and no fresh `validation_loop_blocked` turns in that post-restart window

What this means:

- the new work-lane same-turn cutoff is shipped, tested, and live on the binary
- but it is not the current live bottleneck
- the next real runtime seam looked like the older review-lane cross-turn cap might be missing that family

Follow-up live read a few minutes later:

- the review-lane cap did fire on the next retry turn for that same session:
  - turn `2656120e-b223-4aec-9305-6f0e9a4837ed`
  - `stop_reason=validation_loop_blocked`
  - system message:
    - `Repeated read-only discovery churn across 3 consecutive max-tool-call turns using file.list, file.read, git.diff, git.log, task.get with recurring not_found, path_traversal`
- the same turn then appended a fresh review-action prompt instead of auto-continuing another discovery-only review pass

What this means now:

- the older review-lane cross-turn read-only discovery cap is live-proven again on fresh Anthropic traffic
- the newer same-turn read-only discovery cutoff is also already showing up live on review traffic, for example:
  - `52ffc07e-9b7f-49d8-a36c-3f4a932b75ff`
  - `2eeb3d95-b3a5-4fbc-8514-d81c933c9b95`
  - `ce443718-970d-4f12-85d7-4c0d5934ea76`
  - `dcfe98ee-c248-4043-9d6f-d01c28d175f7`
  - each emitted `Repeated same-turn read-only discovery churn (2/3)` on review-lane traffic
- the remaining proof gap is narrower:
  - we still need a fresh live example where the same-turn cutoff fires on a non-review work lane before the older cross-turn machinery would have taken over

## Update 21:58 MDT

I shipped the next narrow review hardening slice aimed at the hottest remaining burn family after the read-only discovery cuts.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - empty review turns now honor the normal review retry ceiling instead of bypassing it
  - added a session-level brake for repeated empty review outputs:
    - after `3` consecutive review turns in the same session hit the empty-output retry path, the runtime now blocks the lane instead of appending yet another fresh `task_review_action`
  - factored the repeated review-decision block path into a shared helper so the retry-limit and repeated-empty-output cases both persist the same validation guard metadata before blocking
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - renamed and tightened the retry-limit regression to assert block behavior:
    - `TestHandleTurnCompletedEventBlocksEmptyReviewTurnWithoutDecisionAtRetryLimit`
  - added:
    - `TestHandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession`

Focused verification:

- `go test ./internal/turn -run 'Test(HandleTurnCompletedEventBlocksEmptyReviewTurnWithoutDecisionAtRetryLimit|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleToolValidationResultsRetriesReviewAfterSameTurnReadOnlyDiscoveryChurn|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsRetriesReviewLane)$' -count=1`

Why this slice matters:

- the live hot review sessions were not just hitting discovery churn; they were also appending `Review turn returned empty assistant output` across long session-level streaks
- the old empty-output branch returned before the normal `maxGenericRecoveryReplyRetries` gate, which meant blank/tool-only review passes could keep auto-retrying far longer than intended
- this patch closes that hole and adds a session-level cutoff for the repeated-empty family itself, which is the safer way to stop the `0,1,0,1` review oscillation that was still dominating sessions like `14a8b07b-f9c1-457d-a63e-e90c08be948e`

Deployment status:

- code and focused tests are complete locally
- next step is rebuild/restart and then live verification on fresh review traffic

## Update 22:12 MDT

I followed the live proof for the empty-review-output fix and found one more narrow leak immediately behind it.

What happened live:

- session `028bdacd-d478-4f7c-930b-80266a1f7a37` proved the new empty-output guard:
  - turn `29711f2f-ece4-474c-b88c-1e965126a863`
  - `retry_count=2`
  - completed with:
    - `Review turn halted: review turn completed without calling flow.review_decision after repeated retries...`
  - task `13` moved to `blocked`
- but the same turn had already emitted:
  - `[Max tool calls reached - continuing in a new turn.]`
  - then a synthetic `task_continuation_resume`
  - so one extra continuation turn (`dc62605e-401c-4ee3-88d7-dce9f4f1350b`) still started after the block landed

Root cause:

- this was not a stale queued dispatch
- the extra continuation was being created inside the `max_tool_calls` handoff path before the review completion handler got a chance to block the lane

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `shouldContinueMaxToolCalls(...)` now checks async `project_task` review lanes directly
  - when the current review turn is already at `maxGenericRecoveryReplyRetries`, it now returns `false` instead of creating another `task_continuation_resume`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added:
    - `TestMaxToolCallsAsyncReviewAtRetryLimitDoesNotContinue`
  - reran the adjacent review retry / empty-output / review-discovery slice

Focused verification:

- `go test ./internal/turn -run 'Test(MaxToolCallsAsyncReviewAtRetryLimitDoesNotContinue|HandleTurnCompletedEventBlocksEmptyReviewTurnWithoutDecisionAtRetryLimit|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleToolValidationResultsRetriesReviewAfterSameTurnReadOnlyDiscoveryChurn|MaybeBlockRepeatedReadOnlyDiscoveryCapTurnsRetriesReviewLane)$' -count=1`

Deployment status:

- code and focused tests are complete locally
- next step is rebuild/restart and confirm that a review turn blocked at the retry ceiling no longer emits a fresh continuation summary first

## Update 22:16 MDT

The `max_tool_calls` review-handoff patch is now live-proven.

Live proof:

- runtime rebuilt and tmux `codex-e2e-20260324` restarted cleanly
- `./bin/ottercamp health --output json` returned `status=ok`
- fresh review session `f09bb3c9-cd94-4e0a-8fef-c4f591986b9a` then hit the same shape on:
  - turn `ff584837-6921-47dc-a12e-063a1ab70da0`
  - `retry_count=2`
  - final system message at `22:16:13 MDT`:
    - `Review turn halted: review turn completed without calling flow.review_decision after repeated retries...`

Most important negative proof:

- after that halted turn, the session did **not** emit:
  - `[Max tool calls reached - continuing in a new turn.]`
  - `[Continuation summary]`
  - synthetic `task_continuation_resume`
- querying the latest turns for session `f09bb3c9-cd94-4e0a-8fef-c4f591986b9a` showed `ff584837-...` as the newest turn, with no follow-on continuation turn after the halt

Result:

- the extra post-block review continuation leak is closed
- remaining review churn is now the older discovery/retry families, not a same-turn `max_tool_calls` handoff escaping after the review lane is already at its retry ceiling

## Update 22:24 MDT

While sampling the hottest surviving work turns after the review-handoff fix, I found a different execution-lane bug that explains some of the remaining `cli.execute` churn.

Live signal:

- hot task session `429c2e7b-921c-40be-81a6-373678bc9958` (task `14`, "Validate config loading and environment overrides") produced a large work turn with:
  - `16` completed model invocations
  - `14` `cli.execute` tool runs
  - assistant narration that explicitly said:
    - the attempted `file.write` to `scripts/pipeline_config.py` had been redirected to `config/pipeline-config-invalid.yaml`
    - the model then switched to `cli.execute` to write files as a workaround

Root cause:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `handleTaskFileWriteWrongPath(...)` uses `classifyTaskFileArtifactFamily(...)` before rewriting a `file.write` to the preferred task deliverable
  - both `scripts/` and `config/` previously collapsed to the empty family, so the rewrite layer treated them as compatible and silently redirected across them

What changed:

- `classifyTaskFileArtifactFamily(...)` now returns distinct families for:
  - `scripts/` -> `script`
  - `config/` -> `config`
- added focused regression:
  - `TestHandleTaskFileWriteWrongPathSkipsScriptToConfigRewrite`

Focused verification:

- `go test ./internal/turn -run 'Test(HandleTaskFileWriteWrongPathSkipsScriptToConfigRewrite|HandleTaskFileWriteWrongPathSkipsCrossArtifactFamilyRewrite|HandleTaskFileWriteWrongPathRewritesToRecoveryTarget|MaxToolCallsAsyncReviewAtRetryLimitDoesNotContinue)$' -count=1`

Deployment status:

- runtime rebuilt and tmux `codex-e2e-20260324` restarted cleanly
- `./bin/ottercamp health --output json` returned `status=ok`
- short live smoke after restart:
  - querying assistant messages for `redirected to` / `cli_execute with python3` over the last ten minutes only surfaced the older pre-fix task-14 message at `22:14:05 MDT`
- no fresh post-restart assistant message has repeated the `scripts/...` -> `config/pipeline-config-invalid.yaml` redirect complaint yet
- next step is stronger live proof on a fresh execution-first config/script task turn

## Update 22:31 MDT

I sampled the active post-restart review turns and found another small but high-signal improvement opportunity.

Live signal:

- fresh review turn `6edb9cc3-653f-476c-a64f-f1b6d544bfbf` for task `16` started with the same generic discovery pattern:
  - assistant: "I'll start by inspecting the task details and the current state of deliverables in the workspace."
  - then `task.get`
  - then broad `file.list`
  - then commit history
  - only after that did it start reading the actual deliverable

Root cause:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `buildTaskReviewActionPrompt(...)` told the model to "inspect the current deliverables" but did not surface a concrete target path
  - for the hot review lanes, task metadata alone often had no explicit deliverable path, even though the session already knew the real file from recent `file.read` history

What changed:

- `buildTaskReviewActionPrompt(...)` now adds:
  - the preferred deliverable target path when `preferredTaskDeliverablePath(taskRecord)` resolves
  - otherwise, the most recent session-level `deliverable_path` / `file.read` / `file.write` target
  - an explicit instruction to inspect that target directly before broad workspace discovery
  - when no explicit companion-artifact contract exists, an explicit instruction not to inspect planning artifacts or list the full repository tree while that target is present and readable
- added focused regression:
  - `TestBuildTaskReviewActionPromptIncludesPreferredDeliverableTarget`
  - `TestBuildTaskReviewActionPromptFallsBackToRecentSessionDeliverableTarget`

Focused verification:

- `go test ./internal/turn -run 'Test(BuildTaskReviewActionPrompt(FallsBackToRecentSessionDeliverableTarget|IncludesPreferredDeliverableTarget|IncludesExplicitArtifactContractPaths)|HandleTaskFileWriteWrongPathSkipsScriptToConfigRewrite|MaxToolCallsAsyncReviewAtRetryLimitDoesNotContinue)$' -count=1`

Deployment status:

- runtime rebuilt and tmux `codex-e2e-20260324` restarted cleanly
- `./bin/ottercamp health --output json` returned `status=ok`
- live proof landed immediately on task-14 review session `001ec072-f681-400a-bf5e-f4a22c42e1fb`:
  - fresh `task_review_action` turn `4bd77cd0-ed9e-4bfb-8bd7-bad0a475dbf2`
  - prompt now includes:
    - `Start with the preferred deliverable target \`config/pipeline-config.yaml\`...`
  - first assistant/tool sequence changed accordingly:
    - assistant: `I'll start by inspecting the task details and the preferred deliverable target.`
    - then direct `file.read` of `config/pipeline-config.yaml`
    - only later did it drift into broader repo inspection
- so the review prompt is now successfully steering the first inspection step toward the concrete known deliverable instead of opening with root-level discovery
- follow-up local hardening is also complete:
  - the same prompt now tells the model not to inspect planning artifacts or list the full repository tree while that concrete target is present and readable
  - this is now live too
  - fresh post-`22:33 MDT` review prompts include, for example:
    - `Start with the preferred deliverable target \`src/pipeline_logger.py\`...`
    - `Do not inspect planning artifacts or list the full repository tree while \`src/pipeline_logger.py\` is present and readable...`
  - live behavior on task-12 review turn `24d9f369-66e7-463b-9d7c-6bf391eff5ce` changed accordingly:
    - assistant opened with `I'll start by inspecting the preferred deliverable target directly.`
    - first inspection step went straight to `src/pipeline_logger.py`
  - the turn still drifted later into branch-history / diff probing, so this did not eliminate review churn, but it did tighten the opening path in production
