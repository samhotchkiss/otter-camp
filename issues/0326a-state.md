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
