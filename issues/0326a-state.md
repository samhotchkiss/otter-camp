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

## Update 22:44 MDT

I finished the native helper that makes those bounded review prompts pay off more consistently once the model actually opens the target file.

What changed:

- [`internal/tools/native/file_tools.go`](../internal/tools/native/file_tools.go)
  - `latestRecoveryTargetPathForSession(...)` now falls back from system recovery messages to recent `tool_result` payloads in the same session
  - it prefers explicit `output.deliverable_path`
  - otherwise it accepts recent `file.read` / `file.write` `output.path` values when they still look like explicit deliverables
- this feeds the existing native `file.read` guards:
  - `placeholder_deliverable`
  - `mismatched_deliverable_context`
- net effect:
  - when a hot review lane has already been steered to a concrete target like `src/pipeline_logger.py`
  - and that file turns out to be placeholder/status narration
  - the native read layer can now stop the reread immediately even if task metadata itself never recorded that path
- after live sampling I tightened that one step further:
  - a brand-new review session may have the concrete target only in the review prompt itself, not yet in any prior `tool_result`
  - so `latestRecoveryTargetPathForSession(...)` now also parses the exact prompt line we emit:
    - `Start with the preferred deliverable target \`...\``
  - that covers the first direct `file.read` in a fresh review lane, which was the live miss on task `12`
- I then followed the task-12 replay one layer deeper and found the next miss:
  - the target path was available, but the file body itself was a pasted review/request prompt that our placeholder detector still treated as normal content
  - the live body started with:
    - `Active task request: Start review on task: ...`
    - and also included `Task description:`, `Review instruction:`, and `Flow node execution:`
  - `looksLikeRejectedDeliverablePlaceholder(...)` now treats that exact persisted prompt-copy shape as a rejected placeholder deliverable too
- the final live seam under that was task metadata:
  - task `12` does **not** currently carry execution-first planning metadata; its metadata is just `{"bootstrap_first_wave_selected": true}`
  - so `rejectPlaceholderDeliverableRead(...)` was still exiting before it considered the known review target
  - the guard now also applies when `work_status='review'`, even if `taskplan.Parse(...)` does not resolve `ModeExecutionFirst`

Verification:

- added focused unit coverage in [`internal/tools/native/file_tools_test.go`](../internal/tools/native/file_tools_test.go):
  - `TestParseRecentDeliverableTargetFromToolResultPrefersDeliverablePath`
  - `TestLatestRecoveryTargetPathForSessionFallsBackToRecentToolResult`
  - `TestLatestRecoveryTargetPathForSessionFallsBackToReviewPromptTarget`
  - `TestLatestRecoveryTargetPathForSessionPrefersSystemRecoveryTarget`
  - `TestFileReadRejectsPlaceholderRecentReadTargetWithoutExplicitDeliverable`
  - `TestFileReadRejectsPlaceholderReviewPromptTargetWithoutExplicitDeliverable`
- focused slice passed:
  - `go test ./internal/tools/native -run 'Test(ParseRecentDeliverableTargetFromToolResultPrefersDeliverablePath|LatestRecoveryTargetPathForSession(FallsBackToRecentToolResult|FallsBackToReviewPromptTarget|PrefersSystemRecoveryTarget)|FileReadRejectsPlaceholder(RecentReadTargetWithoutExplicitDeliverable|ReviewPromptTargetWithoutExplicitDeliverable))$' -count=1`

Why I changed the proof shape:

- I initially tried to prove this with another integration test on the existing placeholder harness
- that harness is currently tripping earlier on a preexisting `git worktree prune ... fatal: not a git repository` bootstrap failure
- so I switched the proof to native unit coverage rather than pretending the new behavior was unverified

Current live gap before redeploy:

- the first deployed helper slice handled prior `tool_result` / recovery-message evidence correctly
- but live task-12 review session `dfc15ded-5176-4dc5-b78d-91b9d87c11d2` showed the first fresh read still returning raw placeholder content because the target existed only in the review prompt
- the prompt-target fallback above is the direct correction for that exact production miss
- live task-12 replay `b2461d64-6971-482d-b7dc-dc49ac824fae` then exposed the last remaining piece:
  - first direct `file.read` still returned raw content because the body itself was a pasted review prompt
  - the new `Active task request:` placeholder detector is the direct correction for that exact file body

Live proof after the final review-task gate change:

- runtime rebuilt and tmux `codex-e2e-20260324` restarted cleanly again
- `./bin/ottercamp health --output json` returned `status=ok`
- fresh task-12 review session `0120eeec-a720-439b-b908-fff743eb4275`
  - turn `8a19ad7f-dc42-4f4b-a9e3-c18b1fa14cca`
  - first direct target read at `22:54:45 MDT` now returned:
    - `tool_name=file.read`
    - `error=placeholder_deliverable`
    - `deliverable_path=src/pipeline_logger.py`
- this is the exact live seam that had been failing one restart earlier, when the same `src/pipeline_logger.py` read returned raw prompt-copy content instead of a native guard
- the next surviving seam is narrower:
  - after seeing `placeholder_deliverable`, the review model is still narrating “let me check” / “let me inspect” and drifting into task metadata or git history instead of calling `flow.review_decision reject`
  - I added the next prompt hardening for that exact shape:
    - when the preferred target returns `placeholder_deliverable` or `mismatched_deliverable_context`, stop broad inspection and call `flow.review_decision reject` using that tool result as evidence
  - focused turn-engine coverage for that prompt contract is green; live proof is the next step

## Update 23:03 MDT

The review placeholder seam is now resolved all the way through the decision tool, and the remaining hot family moved back to a task work lane.

Live proof:

- fresh task-12 review retry on session `0120eeec-a720-439b-b908-fff743eb4275`
  - assistant explicitly recognized `placeholder_deliverable` on `src/pipeline_logger.py`
  - then called `flow.review_decision reject` instead of drifting back into repo inspection
  - tool result at `22:59:08 MDT` recorded:
    - `blocked=true`
    - `review rejection recorded, but the reject path has exhausted its allowed visits and the task is now blocked`
- so the prompt hardening is now live-proven:
  - bounded review target read
  - native placeholder detection
  - direct `flow.review_decision reject`

The next hot seam is task `16`, not task `12`.

What I found in the new seam:

- session `f2eb9489-24f9-4cdb-9fa8-ae615dd8a232`
- target deliverable `scripts/validate-error-handling.sh`
- the assistant started narrating tool troubleshooting instead of emitting the file body:
  - `I see the file_write calls are going through but not actually replacing the content because I'm not providing content. Let me fix that:`
- persisted assistant `tool_calls` metadata on that turn showed `file_write` with only:
  - `{\"path\":\"scripts/validate-error-handling.sh\"}`
- no `content` argument was present in the call metadata

Why that matters:

- this is the same recovery-draft family we already guard elsewhere
- but the exact live wording above was not yet in the `recoveryFileWriteDraftRejectReason(...)` matcher
- so task `16` exposed a narrow wording gap, not a new architectural class

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - expanded the existing recovery-draft rejection matcher to classify:
    - `i see the file_write calls are going through but not actually replacing the content`
    - `the file_write calls are going through but not actually replacing the content`
    - `i'm not providing content`
    - `i am not providing content`
  - as `tool-recovery troubleshooting instead of the file body`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused case:
    - `rejects file write not replacing content narration`
  - content:
    - `I see the file_write calls are going through but not actually replacing the content because I'm not providing content. Let me fix that:`
  - target:
    - `scripts/validate-error-handling.sh`
  - expected reason:
    - `tool-recovery troubleshooting`

Verification:

- `go test ./internal/turn -run 'TestRecoveryFileWriteDraftRejectReason$' -count=1`

Post-deploy live status:

- runtime was rebuilt and restarted cleanly on top of this matcher expansion
- the first post-deploy retry on session `f2eb9489-24f9-4cdb-9fa8-ae615dd8a232` did **not** re-emit the bad narration
- instead, the lane took the older synthetic draft-rewrite path and moved forward:
  - synthetic `file.write` for `scripts/validate-error-handling.sh`
  - direct script verification and execution
  - results readback from `results/error-handling-results.md`
  - then a separate `git_commit` boundary on the completed deliverables

So the current state of this fix is:

- deployed
- unit-proven
- still preventive rather than freshly exercised on the exact narration string

The remaining proof gap is narrower:

- we still want one fresh production example where that exact `file_write`-without-content narration is rejected before dispatch
- but task `16` is no longer actively stuck on that family right now

## Update 23:18 MDT

The next hot lane after task `16` was a stale-target rewrite bug in task `14`, not another provider or review problem.

Live evidence:

- session `5c52a192-98e7-4b9d-9a41-fa5cc21811d4`
- turn `837c5a56-ec91-4c90-80b7-9de40e7e0f46`
- the lane had already written `config/pipeline-config-invalid.yaml`
- then the assistant said:
  - `Now write the comprehensive test file:`
- but the next empty `cli.execute` call was auto-rewritten into another `file.write` for:
  - `config/pipeline-config-invalid.yaml`
- that repeated three times and the existing duplicate-write churn guard eventually stopped the turn with:
  - `Repeated identical successful file.write churn in this turn`

Why this was happening:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `handleTaskCLIExecuteWithoutCommand(...)` used `recoveryFileOutputContext(...)` plus `taskContinuationDraftContent(...)`
  - that allowed an empty task-lane `cli.execute` to hydrate from broad historical draft sources even when the current assistant step had not emitted a substantive draft for that file
- in this live lane, that meant a stale config-fixture target kept winning over the assistant’s current intent to move on to the test file

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - empty task-lane `cli.execute` rewrites now use `taskContinuationHighConfidenceDraftContent(...)`
  - the rewrite still works for:
    - substantive current-turn assistant drafts
    - current-turn recovery artifact drafts
    - prior substantive assistant drafts
    - continuation-summary drafts
  - but it no longer rewrites from looser historical fallback alone
  - if there is no high-confidence draft for the inferred target, the runtime now appends:
    - `Task execution correction: cli.execute was emitted without command ...`
    - and does **not** silently rewrite to a stale prior target path
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - existing rewrite coverage remains:
    - `TestHandleTaskCLIExecuteWithoutCommandRewritesToFileWriteFromDraft`
  - new focused regression covers the live failure shape:
    - `TestHandleTaskCLIExecuteWithoutCommandAppendsCorrectionWithoutHighConfidenceDraft`

Verification:

- `go test ./internal/turn -run 'TestHandleTaskCLIExecuteWithoutCommand(RewritesToFileWriteFromDraft|AppendsCorrectionWithoutHighConfidenceDraft)$' -count=1`

Current remaining proof step:

- deploy this guard on the live runtime
- then wait for the next empty task-lane `cli.execute` in a multi-deliverable lane
- desired behavior:
  - no stale rewrite to an unrelated prior target
  - one bounded task correction instead of duplicate successful writes to the wrong file

## Update 23:31 MDT

The next slice was operator-report accuracy, not runtime behavior.

What was wrong:

- package-install churn reporting in both:
  - [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
- used:
  - `regexp_replace(command, '^.*?\\binstall\\b\\s+', '', 'i')`
- in PostgreSQL that was too loose for the live command shapes, so the report could smear whole shell commands into `attempted_specs`
- live bad examples looked like:
  - `pip install pyyaml | pip3 install pyyaml | python3 -c "import pytest`
  - `/usr/bin/python3 -m pip install --user pyyaml | pip install pyyaml | pip3 install pyyaml`

What changed:

- both report paths now:
  - strip shell suffixes first
  - anchor specifically on `pip install` / `pip3 install` / `python -m pip install`
  - split the remaining command into tokens
  - ignore installer flags such as `--user`, `--index-url`, `--target`, `-r`, and the values for flags that take one
  - aggregate only the surviving package-spec tokens
- the CLI integration test in [`cmd/ottercamp/main_db_integration_test.go`](../cmd/ottercamp/main_db_integration_test.go) now asserts:
  - `attempted_specs == "pyyaml"`

Verification:

- `bash -n scripts/token-usage-report.sh`
- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`
- live shell smoke:
  - `scripts/token-usage-report.sh --hours 6 --limit 8`
  - repeated package install rows now show:
    - turn `e9661e22-fa24-4fd3-8c6a-fff39ab49b43` -> `pyyaml`
    - turn `093d95ca-af70-4987-afb9-3b38aef82d5f` -> `pyyaml`
- live CLI smoke on the fresh source build:
  - `go run ./cmd/ottercamp db token-usage --hours 6 --limit 8 --output json`
  - repeated package install rows now show:
    - `"attempted_specs": "pyyaml"`

Why this matters:

- the runtime churn guard for repeated installs was already more precise
- the operator surfaces were lagging behind it
- this closes that observability gap, so the report now tells the truth about which package the lane is actually looping on

## Update 23:43 MDT

The next live hotspot was not package-install churn. It was a project bootstrap self-rearming loop.

Live evidence before the fix:

- session `db21265f-c37d-40e4-9ed5-13def09970f8`
- in the last hour it burned about `1.16M` tokens by itself
- recent turns `586` through `594` were all:
  - `completed`
  - `stop_reason=validation_loop_blocked`
- every turn followed the same shape:
  - assistant says it will persist first-wave selection
  - then immediately calls `project.get`, `project.list`, or `task.list`
  - native guard returns:
    - `late bootstrap resume already has persisted staffing, tasks, flows, and first-wave state... call bootstrap.setup.persist now`
  - runtime appends:
    - `[Bootstrap validation recovery reread blocked - ending this turn so the next continuation can repair the named blocker directly.]`
  - but then the completed-turn handler appends the same generic bootstrap continuation again

Root cause:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - the reread guard ended the turn early
  - but that guard did **not** map to a recognized recoverable bootstrap validation failure
  - so the project-bootstrap completed-turn path treated the turn as “no validation failure” and auto-continued with the generic bootstrap prompt instead of the stricter recovery prompt

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `projectBootstrapBlockedRecoveryFailure(...)` now turns reread-guard outcomes into:
    - `kickoff validation failed: late bootstrap resume reread broad persisted project state instead of acting on bootstrap.setup.persist...`
  - that failure is classified as:
    - `projectBootstrapFailureRuntime`
  - `projectBootstrapRecoverableMaxToolCallFailure(...)` now treats that failure as recoverable
  - `buildProjectBootstrapValidationRecoveryPrompt(...)` now has a dedicated branch for this exact failure:
    - start with `bootstrap.setup.persist`
    - do not start with `project.get`, `project.list`, `task.list`, `flow.list_templates`, `agent.list`, or scaffold reads
    - only inspect one specifically named blocker if the persist call returns one
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - reread-guard reason classification
    - recoverable runtime classification
    - persist-first bootstrap recovery prompt text

Verification:

- `go test ./internal/turn -run 'Test(ProjectBootstrapBlockedRecoveryFailureUsesRereadGuardReason|ProjectBootstrapRecoverableMaxToolCallFailure|BuildProjectBootstrapValidationRecoveryPromptForBlockedRecoveryReread)$' -count=1`

Deploy status:

- rebuilt `./bin/ottercamp`
- restarted tmux `codex-e2e-20260324` serve/worker on the finished build
- `./bin/ottercamp health --output json` is `ok`

Important caveat on live proof:

- my first restart raced the rebuild and briefly brought the old binary back up
- I corrected that with a second restart after the build completed
- the hot session was also carrying a pre-deploy generic continuation turn:
  - turn `595` (`8c377af8-c9ba-454c-9c61-6a1a65ece3c2`)
- that stale turn failed during the restart before the new recovery continuation could be generated

So the current state is:

- code is in
- focused tests are green
- runtime is now on the correct new build
- live proof is still pending on the **next fresh bootstrap continuation generated by the new build**, not the stale generic continuation that was already in flight before deploy

## Update 23:45 MDT

The live proof landed on the very next fresh bootstrap continuation after the stale turn drained.

What happened:

- stale pre-deploy turn `595` failed during restart and its claimed dispatch was retired
- fresh recovery continuation message was generated at `23:42:48 MDT`
- that message now contains the new recovery target:
  - `kickoff validation failed: late bootstrap resume reread broad persisted project state instead of acting on bootstrap.setup.persist...`
- fresh turn `596` completed `validation_loop_blocked` against the old bad behavior and created that new recovery continuation
- fresh turn `597` then ran entirely on the new recovery path

Live proof from turn `597`:

- first assistant action:
  - `I'll proceed directly with the bootstrap setup persist call to advance the workflow.`
- first tool result:
  - `bootstrap.setup.persist`
  - completed `bind-repo-environment`
  - prompted for `select-first-wave`
- follow-on system guidance in the same turn:
  - `[Bootstrap first-wave selection] The next bootstrap step is select-first-wave...`
- second successful persist:
  - completed `select-first-wave`
  - selected only Workstream A as first wave
- third successful persist:
  - completed `record-frank-sign-off`
  - `Bootstrap setup checklist is fully persisted. The governance gate will complete automatically once validation passes.`

Why this matters:

- before the fix, this session was burning hundreds of short `validation_loop_blocked` turns by rereading `project.get`, `project.list`, and `task.list`
- after the fix, the very next fresh continuation:
  - switched to the recoverable bootstrap-validation prompt
  - started with `bootstrap.setup.persist`
  - advanced bootstrap state materially in one turn

So this slice is now:

- code complete
- tested
- deployed
- live-proven in production on a fresh continuation turn

## Update 23:51 MDT

I cleaned up the last operator-report path-hint artifact that was still making shell churn harder to read.

What changed:

- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - shell file-build/readback path extraction now strips markdown-style backticks after capture
- [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
  - the CLI JSON report now applies the same cleanup without embedding a literal backtick inside the Go raw SQL string
- [`cmd/ottercamp/main_db_integration_test.go`](../cmd/ottercamp/main_db_integration_test.go)
  - tightened the JSON assertion so `shell_file_build_readback_churn[0].path_hints` must equal exactly `scripts/demo.sh`

Verification:

- `gofmt -w cmd/ottercamp/main.go cmd/ottercamp/main_db_integration_test.go`
- `bash -n scripts/token-usage-report.sh`
- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`

Live proof:

- shell report:
  - `scripts/token-usage-report.sh --hours 1 --limit 8`
  - `Shell File Build / Readback Churn By Turn` now shows clean rows such as:
    - `scripts/validate-ingestion.sh`
    - `scripts/pipeline_error_sim.py | scripts/validate-error-handling.sh`
- CLI JSON report:
  - `go run ./cmd/ottercamp db token-usage --hours 1 --limit 8 --output json`
  - `shell_file_build_readback_churn[*].path_hints` now emits those same clean values, with the last stray backtick suffix gone

Why this matters:

- the runtime cutoffs were already using these churn families
- but the operator surfaces still had one remaining formatting artifact
- now the shell report and the CLI JSON agree on the exact normalized target paths, which makes the next live canary comparison easier to trust

## Update 00:02 MDT

I added one more narrow same-turn cutoff for the current helper-file rewrite loop.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async task turns now classify repeated redirected `file.write` attempts as deterministic churn when the model keeps asking to write helper paths like `gen.py` / `tools/gen.py` but the runtime keeps writing the same target deliverable instead
  - once the current turn has already observed `3` such redirected writes, the turn now stops immediately instead of waiting for a prior cross-turn guard count
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for the exact helper-file redirect shape and kept adjacent file-write/readback churn regressions green

Why this slice exists:

- the hottest recent turn was `7cf145f0-dcc0-45f0-85ab-57b5ea2f17f7` on session `a0389ee3-e6bb-49ce-a942-ce612e146459`
- that turn kept trying helper-file paths such as:
  - `gen_script.py`
  - `tools/gen.py`
  - `gen.py`
- but each successful `file.write` result still reported the same redirected target:
  - `scripts/validate-error-handling.sh`
- the turn then burned more rounds checking whether those helper files existed, rereading the target, and trying another helper path

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdRedirectedFileWriteInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdPackageInstallAttemptWithInterleavedChecks|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdShellFileReadbackInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdWrittenFileReadbackInSameTurn|HandleToolValidationResultsStopsAsyncTaskTurnAfterThirdIdenticalSuccessfulFileWriteInSameTurn)$' -count=1`

Deploy status:

- rebuilt `./bin/ottercamp`
- restarted tmux `codex-e2e-20260324` serve/worker
- `./bin/ottercamp health --output json` is `ok`

Live-proof status:

- deployed, but not yet freshly exercised in production
- immediately after restart, fresh task traffic was hitting a different family first:
  - turn `bc253bf2-288a-426d-abda-7480bc5856dc` on session `13bf8058-133d-49e8-97be-6e1853fbd2f7`
  - current pattern there is `file.edit -> old_string_not_found` followed by a bad `cat` probe, not another helper-file redirect loop

So this slice is:

- code complete
- tested
- deployed
- awaiting first fresh live repro

## Update 00:34 MDT

I confirmed and fixed a real cross-tool workspace mismatch in task lanes.

What was happening:

- on hot task session `13bf8058-133d-49e8-97be-6e1853fbd2f7`, native `file.write` wrote `scripts/validate-metrics-alerting.sh` into the task worktree
- the next `cli.execute` calls for that same task ran from the project workspace root instead
- so the model was not hallucinating when it said the file had disappeared:
  - `file.write` returned success for a task-worktree path
  - `wc -c scripts/validate-metrics-alerting.sh` then failed from the project workspace with `No such file or directory`

Root cause:

- task-scoped native file tools already resolved through `taskWorkspaceRoot(...)`
- task-scoped `cli.execute` was still using `cli.Executor.resolveWorkingDirectory(...)`, which defaulted to the project workspace root
- that meant shell and file tools were operating against different roots in the same `project_task` session

What changed:

- [`internal/tools/native/task_worktree.go`](../internal/tools/native/task_worktree.go)
  - extracted shared `ResolveTaskWorkspaceRoot(...)` so both native file tools and CLI execution can use the same task-worktree resolver
- [`internal/cli/executor.go`](../internal/cli/executor.go)
  - added task lookup to the CLI executor
  - task-scoped shell commands now resolve through that shared task-worktree helper when task metadata is available
  - if task-worktree setup is unavailable, the CLI executor falls back to the project workspace root and logs a warning instead of breaking legacy/non-git callers
- [`internal/cli/executor_test.go`](../internal/cli/executor_test.go)
  - added a git-backed unit test proving task-scoped CLI working-directory resolution lands in `task-worktrees/.../task-<n>`
- [`internal/cli/executor_integration_test.go`](../internal/cli/executor_integration_test.go)
  - updated the EX303 integration fixture to create a real git-backed workspace
  - the task-scoped `pwd` assertion now expects the task worktree root, not the project workspace root
- [`internal/tools/native/executor_test.go`](../internal/tools/native/executor_test.go)
  - updated the stale task-scope workspace unit test to match the real git-backed contract

Verification:

- `go test ./internal/cli -run 'Test(ResolveWorkingDirectoryUsesProjectSlugWorkspace|ResolveWorkingDirectoryUsesTaskWorktreeWhenTaskRepoConfigured)$' -count=1`
- `go test -tags=integration ./internal/cli -run 'TestIntegrationTaskScopedCLIExecute(SharesTaskWorkspaceRoot|WritesVisibleToFileTools)EX303$' -count=1`
- `go test ./internal/tools/native -run 'Test(TaskWorkspaceRootFallsBackToProjectRootWhenMainWorktreeOwns(TaskBranch|UnbornTaskBranch)|WorkspaceForContextTaskScopeUsesTaskWorkspace)$' -count=1`

Why this matters:

- this is a product bug, not just prompt churn
- it directly explains one of the hot token-burning loops where the model kept rewriting or shell-rebuilding files that had already been written successfully
- fixing it should remove a whole class of “write succeeded, shell says missing” rediscovery turns before any additional prompt guardrails have to fire

Deploy status:

- code complete
- tests green
- runtime restart still pending at the time of this note

## Update 09:31 MDT

I immediately followed the previous slice with one more guard based on the exact persisted tool calls from the hot project lane.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - rooted/snapshotted async `project` continuations now block `flow.get_execution` before the project lane starts probing flow execution records again
  - the same snapshot guard now blocks recursive broad artifact browsing for:
    - `file.list(path="results")`
    - `file.list(path="pipeline/fixtures")`
  - this only applies when the project continuation prompt already names actionable draft tasks in the tree
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - blocked project-lane `flow.get_execution` under the snapshot prompt
    - blocked recursive `file.list(path=results)` under the snapshot prompt
    - preserving the existing parent-scoped `task.list` allowance

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksBroadTaskList|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksNamedTaskGet|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksFlowGetExecution|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksBroadResultsList|ShouldNotBlockProjectContinuationSnapshotRediscoveryToolForParentScopedTaskList|ShouldBlockProjectContinuationFlowExecutionLookupToolForTaskCurrentFlowNodeID|ShouldNotBlockProjectContinuationFlowExecutionLookupToolForUnknownExecutionID)$' -count=1`

Why this slice exists:

- the persisted assistant tool metadata for hot session `db21265f-c37d-40e4-9ed5-13def09970f8` showed the next exact fallback pattern after `task.list` was blocked:
  - `flow.get_execution(flow_node_execution_id=34670c0a-833e-457b-bfb4-e39dd64d7843)`
  - `flow.get_execution(flow_node_execution_id=5fb4f538-9e45-4173-a0b3-16ab8a3c452e)`
  - `flow.get_execution(flow_node_execution_id=4dbe4e97-e438-431b-aa6e-80fc5eef1418)`
  - `file.list(path="results", recursive=true)`
- all three execution lookups paid for the same deterministic `flow_node_execution_id_required` failure, and the results browse was another broad project-lane workspace read

Expected effect:

- project continuations with a named actionable task snapshot should skip both of those fallback rediscovery steps and move straight to task mutation / queueing / narrower evidence

Deploy status:

- code complete
- tests green
- runtime rebuild/restart still pending at the time of this note

## Update 09:39 MDT

I tightened the continuation-summary sanitizer for the same hot project lane instead of waiting for another replay.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `continuationSummaryLooksUnavailable(...)` now treats generic inspection-plan summaries as unusable
  - new normalized patterns include:
    - `I need to inspect the current task tree`
    - `Let me examine the project structure`
    - `The path forward is clear:`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving that a continuation summary made of generic inspection-plan narration now normalizes to `Continuation summary unavailable.`

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ContinuationTurnNormalizesFunctionCallPlanSummary|ContinuationTurnNormalizesInspectionPlanSummary)$' -count=1`

Why this slice exists:

- the hot project session was still generating continuation summaries like:
  - `I need to inspect the current task tree to identify the 2 remaining draft tasks ...`
  - fenced `Let me examine the project structure ...`
  - `The path forward is clear: ...`
- those are not durable handoff summaries; they just re-inject meta intent into the next async turn

Expected effect:

- when the summary model produces one of those generic plan-only summaries, async project continuations fall back to the deterministic project continuation summary instead of preserving more rediscovery narration

Deploy status:

- code complete
- tests green
- runtime rebuild/restart still pending at the time of this note

## Update 09:20 MDT

I cut the next project-continuation seam instead of waiting on another replay.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - completed-task `project_execution_continuation` prompts now use the same embedded project snapshot as rooted `project_continuation_resume`
  - that snapshot includes:
    - active project id
    - already-active non-terminal tasks
    - actionable draft tasks already in the tree
    - one direct focus task
  - async project lanes now preflight-block `flow.get_execution` when the provided `flow_node_execution_id` exactly matches a task's `current_flow_node_id`
  - the new guard is intentionally narrow:
    - async `project` scope only
    - exact current-node-id match only
    - unknown execution ids still pass through
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - updated completed-task continuation prompt assertions for the shared snapshot guidance
  - added focused coverage for the new `flow.get_execution(task.current_flow_node_id)` project-lane block

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(BuildProjectExecutionContinuationPrompt|BuildProjectContinuationActionPrompt|ProjectExecutionContinuationSnapshotSummarizesProjectState|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksBroadTaskList|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksNamedTaskGet|ShouldNotBlockProjectContinuationSnapshotRediscoveryToolForParentScopedTaskList|ShouldBlockProjectContinuationFlowExecutionLookupToolForTaskCurrentFlowNodeID|ShouldNotBlockProjectContinuationFlowExecutionLookupToolForUnknownExecutionID|HandleCompletedProjectExecutionContinuationTurnHandlesProjectContinuationResumeSource|HandleCompletedProjectExecutionContinuationTurnAutoQueuesRunnableDraft)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`

Why this slice exists:

- rooted `project_continuation_resume` already had the project snapshot and named-task rediscovery guard
- completed-task wakeups were still using the older generic continuation prompt, so the `project_execution_continuation` path remained softer and kept reopening broad rediscovery
- the live project session also showed repeated `flow.get_execution` calls using task node ids instead of real execution ids, which is deterministic waste, not useful probing

Expected effect:

- fewer broad rereads on the completed-task continuation path
- no more paying for the known `flow_node_execution_id_required` misuse when project continuations pass `task.current_flow_node_id`

Deploy status:

- code complete
- tests green
- runtime restart still pending at the time of this note

## Update 08:53 MDT

I tightened the generic async project-continuation resume path itself instead of waiting for another bespoke guardrail.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `project_continuation_resume` prompts now embed a compact project-state snapshot directly into the rooted synthetic user message
  - the new snapshot includes:
    - `Active project id: ...`
    - `Already-active non-terminal tasks in the tree: ...`
    - `Actionable draft tasks already in the tree: ...`
    - `Start from this existing actionable draft before broad rediscovery ...`
  - the prompt now explicitly says not to begin with broad `project.get`, `task.list`, or `task.get` rediscovery when those actionable draft tasks are already named
  - snapshot generation reuses the existing `isActionableProjectDraftTask(...)` filter, so shell/meta drafts like `Select and Decompose Next Bounded Task` are excluded instead of being treated as real remaining work
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - prompt rendering with embedded project snapshot lines
    - snapshot generation that skips continuation-shell meta drafts while still surfacing real active/draft work

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(BuildProjectContinuationActionPrompt|ProjectExecutionContinuationSnapshotSummarizesProjectState)$' -count=1`
- `go test ./internal/turn -run 'Test(ListeningEvalSkippedForSyntheticProjectContinuationMessage|HandleCompletedProjectExecutionContinuationTurnHandlesProjectContinuationResumeSource|MaybeContinueProjectExecutionAfterTaskCompletionSupersedesStaleProjectContinuationTurn|BuildProjectContinuationActionPrompt|ProjectExecutionContinuationSnapshotSummarizesProjectState|IsActionableProjectDraftTaskSkipsProjectContinuationMetaDrafts)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- runtime restarted on tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Why this slice exists:

- the hottest remaining project sink was not malformed summaries anymore
- it was the normal `project_continuation_resume` path staying too generic, which let the project lane reopen the same draft/in-progress task tree with repeated `task.list` / `task.get` discovery until `max_tool_calls`
- embedding a compact snapshot into the rooted continuation message is the lowest-risk way to tighten that lane without inventing more runtime state or another special-case blocker

Live proof status:

- deploy is complete and health is green
- live proof now exists on hot session `db21265f-c37d-40e4-9ed5-13def09970f8`
- fresh `project_continuation_resume` at `2026-03-27 08:55:43 MDT` carried:
  - `Active project id: f56c456f-870a-4647-be30-c9d256e0ea12`
  - named active tasks `16`, `18`, `13`, `12`
  - named actionable draft tasks `22`, `21`, `20`, `10`
  - direct anti-rediscovery guidance plus a focus task (`22`)

## Update 09:00 MDT

The embedded snapshot alone was not enough; the hot project lane still reread named task records and then re-listed the tree. I cut that next seam directly at tool preflight.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project` turns now block a narrow rediscovery family once the rooted `project_continuation_resume` prompt already names actionable draft task ids
  - blocked now:
    - broad `task.list` without `parent_task_id`
    - `project.get` / `project.list`
    - `task.get` when the requested `task_id` already appears in the continuation prompt snapshot
  - still allowed:
    - `task.list(parent_task_id=...)` for genuinely narrower child inspection
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - blocking broad `task.list`
    - blocking `task.get` on a prompt-named task id
    - preserving parent-scoped `task.list`

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksBroadTaskList|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksNamedTaskGet|ShouldNotBlockProjectContinuationSnapshotRediscoveryToolForParentScopedTaskList|BuildProjectContinuationActionPrompt|ProjectExecutionContinuationSnapshotSummarizesProjectState)$' -count=1`
- `go test ./internal/turn -run 'Test(ListeningEvalSkippedForSyntheticProjectContinuationMessage|HandleCompletedProjectExecutionContinuationTurnHandlesProjectContinuationResumeSource|MaybeContinueProjectExecutionAfterTaskCompletionSupersedesStaleProjectContinuationTurn|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksBroadTaskList|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksNamedTaskGet|ShouldNotBlockProjectContinuationSnapshotRediscoveryToolForParentScopedTaskList|BuildProjectContinuationActionPrompt|ProjectExecutionContinuationSnapshotSummarizesProjectState|IsActionableProjectDraftTaskSkipsProjectContinuationMetaDrafts)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- runtime restarted on tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Live proof:

- hot session `db21265f-c37d-40e4-9ed5-13def09970f8`
- fresh post-deploy continuation at `2026-03-27 08:59:42 MDT`
- first assistant reply still tried to inspect the already-named key tasks, then attempted broad `task.list`
- runtime blocked that exact reread family at `2026-03-27 09:00:16 MDT` with:
  - `project continuation already has named actionable draft tasks in the continuation prompt. Do not re-list the broader project task tree...`

Why this matters:

- this is the first direct same-turn project-lane rediscovery block for the hot `project_continuation_resume` family
- it converts the new prompt snapshot from “advice only” into actual runtime enforcement for the broadest reread path
- the next live seam in that project lane is narrower now:
  - same-turn rereads of named active tasks via `task.get`
  - other non-essential project-lane discovery like `agent.list`

## Update 09:04 MDT

I tightened the continuation-summary sanitizer again so project continuations stop inheriting pseudo-tool plans as if they were real summaries.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `continuationSummaryLooksUnavailable(...)` now also treats embedded pseudo-tool plans as unusable
  - newly normalized patterns include:
    - `<function_calls>`
    - serialized `task.update`
    - serialized `project.get`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added `TestContinuationTurnNormalizesFunctionCallPlanSummary`

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ContinuationTurnNormalizesFunctionCallPlanSummary|ContinuationTurnNormalizesOperatorFacingCommandRequestSummary|ContinuationTurnNormalizesOperatorFacingClaudeCLICommandSummary)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- runtime restarted on tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Live proof:

- hot session `db21265f-c37d-40e4-9ed5-13def09970f8`
- before deploy:
  - `2026-03-27 09:00:59 MDT` summary included `<function_calls>` with `task.update` + `task.get`
  - `2026-03-27 09:01:19 MDT` summary included `<function_calls>` with `task.update` + `project.get`
- after deploy:
  - `2026-03-27 09:03:09 MDT`
  - `2026-03-27 09:03:32 MDT`
  - `2026-03-27 09:03:50 MDT`
  - all three normalized straight to the fallback:
    - `Project execution is already underway. Reuse the existing project task tree, workspace artifacts, planning files, and recent tool results from this session to keep the active work moving forward.`

Why this matters:

- it removes another prompt-chatter leak from the project continuation lane
- it keeps the rooted continuation turn centered on the runtime-authored snapshot/guardrails instead of model-authored pseudo-tool plans

## Update 08:24 MDT

I tightened the orchestration-parent same-turn review guard to keep using the session deliverable target even when the prompt text itself is too generic.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - both orchestration-parent reject/discovery guard paths now fall back to `sessionTaskDeliverablePath(...)` when `parsePromptDeliverableTarget(...)` returns empty
  - that means the guard still recognizes the already-read parent summary as authoritative even if the live review prompt only says `Review only...` and omits an explicit `Start with the preferred deliverable target ...` line
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving the unfinished-child same-turn guard still blocks child `file.read` drift when the prompt omits the preferred-target line and only the session deliverable path identifies the parent summary

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ShouldBlockOrchestrationParentReviewUnfinishedChildDiscoveryTool|ShouldBlockOrchestrationParentReviewUnfinishedChildDiscoveryToolUsesSessionDeliverableFallback|ShouldBlockOrchestrationParentReviewCurrentTaskGetTool|ShouldBlockOrchestrationParentReviewTaskListToolRequiresParentScopedList)$' -count=1`

Why this slice exists:

- fresh task-11 session `3cb092fb-c55a-4cb0-99ff-c6675e07fdfd` still showed the remaining leak after the earlier same-turn guard landed:
  - the turn had already read the parent summary
  - `task.list(parent_task_id=...)` had already shown an unfinished direct child
  - `task.get` was blocked correctly
  - but a later child `file.read -> not_found` still slipped through because the prompt text did not expose a parseable preferred-target line
- the product-safe fix is to trust the session deliverable target that the turn already used, not to require one exact prompt wording before the guard can fire

Deploy status:

- code complete
- tests green
- runtime rebuilt and restarted on the new binary
- health check green: `./bin/ottercamp health --output json`

Live proof:

- fresh task-11 review session `3cb092fb-c55a-4cb0-99ff-c6675e07fdfd` on the new runtime now shows the fallback working against the previously leaking generic prompt:
  - user prompt was the generic `Review only. Inspect the current deliverables ...` form with no explicit preferred-target line
  - the turn still recognized the already-read parent summary via the session deliverable target
  - follow-on child drift was blocked directly in the same turn:
    - child `file.read`
    - extra `task.list`
    - broader `git.status`
- the turn then halted under the existing review retry ceiling instead of paying for another child-deliverable miss

## Update 08:36 MDT

I fixed the next orchestration-parent review bug that showed up immediately after the same-turn discovery guards started working.

What changed:

- [`internal/flow/execution_service.go`](../internal/flow/execution_service.go)
  - reject transitions now compute a special target status for orchestration-only parent tasks
  - when `flow.review_decision reject` sends an orchestration parent from review back to its work node, the flow service now keeps the parent in `draft` instead of forcing generic `in_progress`
- [`internal/task/service.go`](../internal/task/service.go)
  - the task runtime now allows that one flow-owned `review -> draft` transition only when:
    - the actor is flow-runtime bypass
    - the transition is tagged `flow.rejected`
    - the task is an orchestration-only / bounded-child parent
- [`internal/flow/execution_service_integration_test.go`](../internal/flow/execution_service_integration_test.go)
  - added focused coverage proving `RejectFlowNode(...)` returns the parent to the work node while preserving `work_status=draft`
- [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
  - added a `flow.review_decision reject` integration regression for the same orchestration-parent case

Verification:

- `gofmt -w internal/task/service.go internal/flow/execution_service.go internal/flow/execution_service_integration_test.go internal/tools/native/native_integration_test.go`
- `go test ./internal/task -run 'Test(TransitionStatus|.*Orchestration.*)' -count=1`
- `go test -tags=integration ./internal/flow -run 'TestFlowExecutionService(RejectFlowNodeKeepsOrchestrationParentDraft|RejectionLoopVisitIncrements|RejectFlowNodeFallsBackToPreviousOrderedNodeWhenReviewPathIsImplicit)$' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegrationFlowReviewDecision(RejectCreatesCanonicalRejectionCommit|RejectKeepsOrchestrationParentDraft)$' -count=1`

Why this slice exists:

- fresh live task-11 review session `cdd8bb9a-e420-441d-9b18-78721c57aa9f` showed the next failure clearly:
  - the orchestration-parent review guard had already done the right thing
  - the assistant then called `flow.review_decision reject` with correct evidence
  - but the tool itself returned `task must remain orchestration-only while executable child tasks exist`
- the issue was not the review reasoning anymore; it was the reject transition forcing the parent back to normal executable `in_progress`
- the product-safe fix is to move the parent back to the work node while preserving draft/integration status, which already matches the existing orchestration-parent activation model

Deploy status:

- code complete
- tests green
- runtime rebuilt and restarted on the new binary
- health check green: `./bin/ottercamp health --output json`

Initial live read:

- the last observed `task must remain orchestration-only while executable child tasks exist` `flow.review_decision` tool-result rows were from the pre-restart wave:
  - task-11 session `0184e000-73fc-4ef1-85d0-aac8b87b1965` at `08:37:18 MDT`
  - task-10 session `20350145-584d-46a6-bfcc-f5bdc1fc6744` at `08:36:38 MDT`
- in the short post-restart window immediately after the new binary came up, there were no newer copies of that exact reject-path failure
- stronger live proof still depends on the next fresh orchestration-parent review rejection reaching `flow.review_decision` on this build

## Update 08:42 MDT

I closed the next live project-lane summary leak and proved it on the hot canary session.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - widened `continuationSummaryLooksUnavailable(...)` so operator-facing command-request summaries are also rejected when they arrive as fenced `claude-cli` / `claude-code` task-list commands, not just `bash` / `sh`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for the exact live shape:
    - ```` ```claude-cli project:tasks:list --filter status=draft --sort priority ````

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'TestContinuationTurnNormalizes(OperatorFacingCommandRequestSummary|OperatorFacingClaudeCLICommandSummary)$' -count=1`

Live proof:

- hot project session `db21265f-c37d-40e4-9ed5-13def09970f8` was still preserving raw command-request summaries just before the patch:
  - `08:38:48 MDT` summary with ```` ```claude-cli project:tasks:list --filter status=draft --sort priority ````
  - `08:40:19 MDT` summary with ```` ``` claude-code task:list --filter=status:draft ````
- after the new binary came up, the same session switched to the fallback summary instead:
  - `08:40:41 MDT` `[Continuation summary] Project execution is already underway...`
  - `08:41:57 MDT` `[Continuation summary] Project execution is already underway...`

Why this matters:

- this was another path where async project continuations were inheriting operator-facing requests instead of actionable runtime context
- the fallback summary is not perfect, but it is materially better than feeding the project lane a fake terminal command to run inside autonomous execution

## Update 08:12 MDT

I traced the live task-14 review behavior after the new same-turn missing-tests guard fired and found the next seam precisely:

- the guard was already blocking follow-on discovery inside the turn with text like:
  - `cannot be approved because required test artifacts under tests could not be verified...`
  - `call flow.review_decision with decision=reject immediately`
- but when that same turn then ended empty, the follow-on retry prompt was still too generic
- so the problem was no longer the guard itself; it was the retry-prompt evidence parser

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - added `reviewMissingTestsRejectEvidenceFromGuardError(...)`
  - same-turn `reviewRetryPromptForMissingRequiredTests(...)` now treats the guard's blocked-tool reject payload as valid missing-tests evidence
  - persisted `reviewRetryPromptForPersistedMissingRequiredTests(...)` now does the same across later retry turns
  - `currentTurnReviewMissingTestsEvidence(...)` also recognizes that guard payload shape directly, so the same summary can be reused consistently by both blocking and retry logic
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - same-turn guard evidence converting the next retry into a reject-only review prompt
    - persisted guard evidence across retry turns converting the next retry into a reject-only review prompt

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified|ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerifiedViaRecoveryFocus|ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerifiedAcrossRetryTurns|ReviewApprovalRetryPromptRejectsWhenMissingTestsGuardAlreadyFiredThisTurn|ReviewApprovalRetryPromptRejectsWhenMissingTestsGuardFiredAcrossRetryTurns|ShouldBlockReviewMissingTestsDiscoveryTool|ShouldBlockReviewMissingTestsDiscoveryToolRequiresSameTurnEvidence)$' -count=1`

Why this matters:

- the live same-turn guard was already doing the expensive part correctly: it stopped more browsing
- without this slice, the empty-output retry could still burn another loop before the model finally rejected
- with this patch, the next retry prompt should now go straight to `flow.review_decision reject` using the guard's own evidence instead of regenerating a base review pass

Deploy status:

- code complete
- tests green
- runtime restarted on the new binary

Live proof:

- after the `2026-03-27 08:12:50 MDT` restart, there are `0` async review-session user messages using the generic:
  - `Continue the active task now from the continuation summary above.`
- direct DB proof:
  - query filtered to `chat_message.role='user'`
  - `created_at >= '2026-03-27 08:12:50-06'`
  - `session.scope_type='project_task'`
  - `project_task.work_status='review'`
  - `content like 'Continue the active task now from the continuation summary above.%'`
  - returned zero rows
- that is the concrete confirmation that review continuations are no longer being handed off through the generic task-continuation prompt on the new build

## Update 08:24 MDT

The hottest remaining live session shifted back to async project orchestration, and the newest bad shape was visible directly in the continuation summary itself:

- hot project session `db21265f-c37d-40e4-9ed5-13def09970f8`
- latest continuation summary contained:
  - `I'll inspect the current task tree to identify the next runnable work.`
  - a fenced shell snippet
  - `Please provide the output so I can immediately identify and advance the next bounded task.`
- that summary was then being preserved into the next async project turn, which is the wrong behavior for autonomous runtime execution

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - widened `continuationSummaryLooksUnavailable(...)` so operator-facing command-request summaries are normalized away
  - new unavailable patterns include:
    - `please provide the output`
    - `run this command`
    - fenced shell snippets like ```` ```bash ```` and ```` ```sh ````
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for a continuation summary that includes a shell snippet plus `Please provide the output...`
  - preserved the older no-context / missing-draft / missing-history normalization tests

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ContinuationTurnNormalizesGenericNoContextSummary|ContinuationTurnNormalizesMissingDurableDraftSummary|ContinuationTurnNormalizesMissingTaskSessionHistorySummary|ContinuationTurnNormalizesOperatorFacingCommandRequestSummary|ContinuationTurnUsesTaskFallbackSummaryForAsyncProjectTask|BuildProjectContinuationActionPrompt)$' -count=1`

Why this matters:

- the runtime should never carry an operator-directed `please provide output` memo into an autonomous async continuation
- normalizing that shape to `Continuation summary unavailable.` forces project/task continuations back onto the safer built-in fallback summaries instead of preserving a dead-end request for human help

Deploy status:

- code complete
- tests green
- runtime restarted on the new binary

Live proof status:

- no post-restart project continuation summary rows had been emitted yet in the short verification window after `2026-03-27 08:18:13 MDT`
- so this slice is deployed and test-green, but still waiting on the next real async project continuation to prove the operator-facing summary is now normalized away in production

## Update 08:20 MDT

The next live seam showed up immediately after the prior fix:

- task `14`'s same-turn missing-tests guard was firing correctly
- but when that guarded review turn hit `max_tool_calls`, `continueTurn(...)` still appended:
  - `[Continuation summary] Active task request: Review only...`
  - followed by the generic user prompt:
    - `Continue the active task now from the continuation summary above.`
- that dropped the lane back into generic task-execution language before the reject-oriented review retry logic could take over

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `continueTurn(...)` now passes the just-completed turn into continuation handoff
  - `appendContinuationSummaryAndAction(...)` now detects async review lanes and skips the generic task continuation prompt for them
  - added `taskReviewContinuationActionPrompt(...)`
    - it reuses `reviewApprovalRetryPrompt(...)` against the just-completed turn when reject evidence already exists
    - otherwise it still falls back to the normal `buildTaskReviewActionPrompt(...)`
    - it carries forward review-scoped synthetic metadata as `source=task_review_action`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving:
    - review continuation handoff uses reject retry guidance when the previous turn already established missing-tests reject evidence
    - the existing generic async task continuation path still stays green

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ContinuationTurnAppendsDirectActionPromptForAsyncProjectTask|TaskReviewContinuationActionPromptUsesRejectRetryGuidance|ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified|ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerifiedViaRecoveryFocus|ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerifiedAcrossRetryTurns|ReviewApprovalRetryPromptRejectsWhenMissingTestsGuardAlreadyFiredThisTurn|ReviewApprovalRetryPromptRejectsWhenMissingTestsGuardFiredAcrossRetryTurns|ShouldBlockReviewMissingTestsDiscoveryTool|ShouldBlockReviewMissingTestsDiscoveryToolRequiresSameTurnEvidence)$' -count=1`

Why this matters:

- the previous patch fixed the empty-output retry path
- this patch fixes the earlier `max_tool_calls` continuation handoff that was bypassing that retry path entirely
- together they close both ways the runtime could lose same-turn missing-tests reject evidence and reopen extra browsing

Deploy status:

- code complete
- tests green
- runtime restart pending at the time of this note

## Update 07:27 MDT

I cut the next orchestration-parent review seam that showed up in fresh live task-11 traffic.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - orchestration-only parent review retries now reject immediately when the current scoped `task.list(parent_task_id=...)` evidence already shows unfinished direct child tasks
  - that reject prompt now carries the child-status evidence itself, for example `OC-13 (...) is in_progress`, so the model does not need to inspect child deliverables to justify rejection
  - persisted retry prompts now reuse that unfinished-child evidence across later transient retries, not just within the immediately failed turn
  - the existing orchestration-parent reject-only discovery guard now recognizes this unfinished-child mode and blocks follow-on child deliverable reads and extra task relisting once rejection evidence is already sufficient
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - same-turn unfinished-child reject prompt
    - persisted unfinished-child reject prompt across retry turns
    - reject-only discovery blocking for unfinished-child evidence

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run "Test(ReviewApprovalRetryPrompt(RejectsOrchestrationParentWithUnfinishedDirectChildren|RejectsOrchestrationParentWithUnfinishedDirectChildrenAcrossRetryTurns|RejectsOrchestrationParentWithoutDirectChildren|RejectsOrchestrationParentWithoutDirectChildrenAcrossRetryTurns|CarriesForwardOrchestrationParentSummaryEvidence)|ShouldBlockOrchestrationParentReviewRejectDiscoveryTool(ForUnfinishedChildren)?)$" -count=1`

Why this slice exists:

- fresh task-11 review traffic had already proved the direct child statuses were enough to reject:
  - one child was still `in_progress`
  - another child was already `done`
- but the next review step still drifted into child deliverable `file.read` calls and ended as another `validation_loop_blocked` turn on `file.read -> not_found`
- the product-safe fix is to treat unfinished direct child status as sufficient reject evidence for the orchestration parent, not to keep exploring child artifacts

Deploy status:

- code complete
- tests green
- runtime restart pending at the time of this note

## Update 07:58 MDT

I cut the next same-turn review seam directly from fresh task-14 traffic.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async review turns that already proved the preferred deliverable is substantive and then fail to verify required test artifacts now block further same-turn discovery immediately
  - once the current turn has both:
    - a successful primary deliverable read
    - a `tests`-scoped verification failure (`not_found` or recovery-focus redirect)
  - follow-on discovery calls are blocked with immediate reject guidance, including:
    - `file.read`
    - `file.list`
    - `file.search`
    - `task.get`
    - `git.log`
    - related review discovery rereads
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - same-turn missing-tests discovery block
    - negative case when the same-turn test-verification failure has not happened yet

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run "Test(ShouldBlockReviewMissingTestsDiscoveryTool(RequiresSameTurnEvidence)?|ShouldBlockOrchestrationParentReview(UnfinishedChildDiscoveryTool|RejectDiscoveryToolForUnfinishedChildren)|ReviewApprovalRetryPrompt(RejectsWhenRequiredTestsCannotBeVerified|RejectsWhenRequiredTestsCannotBeVerifiedViaRecoveryFocus|RejectsWhenRequiredTestsCannotBeVerifiedAcrossRetryTurns)|TaskExecutionRetryPromptFor(RecoveryTargetFocus|RecoveryTargetFocusSkipsWhenTargetAlreadyWritten))$" -count=1`

Why this slice exists:

- fresh task-14 session `834ee8c6-60d7-455d-a6a9-ba97918a4f83` showed the exact in-turn waste:
  - `config/pipeline-config-invalid.yaml` was already readable
  - a `tests` lookup had already been redirected by recovery focus
  - the review still continued with `task.get`, `git.log`, and more discovery instead of rejecting
- the retry-persistence work helps later turns, but this waste was happening inside the same turn before the retry prompt ever had a chance to help

Deploy status:

- code complete
- tests green
- runtime restart pending at the time of this note

## Update 07:49 MDT

I cut the sharper orchestration-parent review seam that was still live in task-11 even after the retry-persistence work.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async orchestration-parent review now has a same-turn discovery guard for unfinished direct child evidence
  - if the current turn already proved:
    - the parent orchestration summary is readable
    - `task.list(parent_task_id=<current task>)` shows unfinished direct child tasks
  - then follow-on discovery calls in that same turn are blocked immediately, including:
    - child `file.read`
    - `task.get`
    - broader `task.list`
    - other review discovery rereads
  - the guard tells the model to call `flow.review_decision reject` immediately using the direct child-status evidence it already has
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for the same-turn unfinished-child discovery block

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run "Test(ShouldBlockOrchestrationParentReview(UnfinishedChildDiscoveryTool|RejectDiscoveryToolForUnfinishedChildren)|ReviewApprovalRetryPrompt(RejectsWhenRequiredTestsCannotBeVerified|RejectsWhenRequiredTestsCannotBeVerifiedViaRecoveryFocus|RejectsWhenRequiredTestsCannotBeVerifiedAcrossRetryTurns|RejectsOrchestrationParentWithUnfinishedDirectChildren|RejectsOrchestrationParentWithUnfinishedDirectChildrenAcrossRetryTurns)|TaskExecutionRetryPromptFor(RecoveryTargetFocus|RecoveryTargetFocusSkipsWhenTargetAlreadyWritten|MissingPreferredDeliverable|MissingPreferredDeliverableSkipsWhenTargetAlreadyReadable))$" -count=1`

Why this slice exists:

- fresh task-11 session `a84031f8-0c0c-4eb5-93e1-0f1fbee13391` exposed the same-turn gap directly:
  - the turn had already read the parent summary
  - `task.list(parent_task_id=...)` had already shown `OC-13` still `in_progress`
  - the assistant then kept trying child `task.get` / `file.read` calls and the turn ended `validation_loop_blocked`
- the retry-prompt work was not enough for this case because the waste happened before the turn ended
- this new guard stops that drift inside the same turn where the evidence first appears

Deploy status:

- code complete
- tests green
- runtime restart pending at the time of this note

## Update 07:42 MDT

I cut the next review-specific persistence gap exposed by task-14.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - review retry prompting now persists missing-tests rejection evidence across retry turns
  - if a prior review turn already established:
    - the primary deliverable is substantive
    - the task explicitly requires tests
    - related test artifacts could not be verified from the workspace
  - then a later retry that only rereads the primary deliverable now goes straight to `flow.review_decision reject` guidance instead of re-opening the same target again
  - the helper also now treats `file.search` recovery-focus failures as test-verification evidence alongside `file.read` / `file.list`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - same-turn missing-tests rejection via recovery-focus evidence
    - persisted missing-tests rejection across retry turns

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run "Test(ReviewApprovalRetryPrompt(RejectsWhenRequiredTestsCannotBeVerified|RejectsWhenRequiredTestsCannotBeVerifiedViaRecoveryFocus|RejectsWhenRequiredTestsCannotBeVerifiedAcrossRetryTurns|RejectsOrchestrationParentWithUnfinishedDirectChildren|RejectsOrchestrationParentWithUnfinishedDirectChildrenAcrossRetryTurns)|TaskExecutionRetryPromptFor(RecoveryTargetFocus|RecoveryTargetFocusSkipsWhenTargetAlreadyWritten|MissingPreferredDeliverable|MissingPreferredDeliverableSkipsWhenTargetAlreadyReadable)|ShouldBlockOrchestrationParentReviewRejectDiscoveryToolForUnfinishedChildren)$" -count=1`

Why this slice exists:

- task-14 session `7dfae433-450d-4e18-8170-b642dd526229` showed the exact persistence gap:
  - one turn read `config/pipeline-config-invalid.yaml` successfully
  - follow-on review steps then failed under recovery focus while trying to verify test artifacts
  - the next retry reverted to the base review prompt and reread the deliverable instead of rejecting with the already-established missing-tests evidence
- the safe fix is to preserve that review evidence across retries, not to let the lane rediscover the same target file again

Deploy status:

- code complete
- tests green
- runtime restart pending at the time of this note

## Update 07:34 MDT

I used the live one-hour report again and cut the next highest-signal async task seam.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - the existing validation-blocked continuation hook for async non-review task lanes now handles `recovery_target_focus_required` / `explicit_deliverable_focus_required` in addition to simple `file.read -> not_found`
  - when the current turn already proved that recovery identified a concrete target like `pipeline/fixtures/README.md`, but the model kept reading sibling fixture paths instead, the next synthetic continuation now says to mutate the known target directly
  - the narrowed continuation explicitly says:
    - the runtime already identified the active deliverable target
    - do not `file.read` / `file.list` sibling paths again
    - continue directly with `file.write` / `file.edit` on the target or emit one short blocker
  - the helper intentionally skips once the target already received a successful `file.write` or `file.edit` in the same turn
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - recovery-target-focus narrowed continuation prompt
    - skip behavior once the target was already written

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run "Test(TaskExecutionRetryPromptFor(MissingPreferredDeliverable|MissingPreferredDeliverableSkipsWhenTargetAlreadyReadable|RecoveryTargetFocus|RecoveryTargetFocusSkipsWhenTargetAlreadyWritten)|ReviewApprovalRetryPrompt(RejectsOrchestrationParentWithUnfinishedDirectChildren|RejectsOrchestrationParentWithUnfinishedDirectChildrenAcrossRetryTurns)|ShouldBlockOrchestrationParentReviewRejectDiscoveryToolForUnfinishedChildren)$" -count=1`

Why this slice exists:

- the live one-hour report shifted away from the orchestration-parent review loop and toward task work lane recovery drift
- task-16 session `da709601-92be-490e-b074-0615f26fc1cf` showed the exact waste shape:
  - repeated `file.read` on sibling fixture files
  - tool results already saying recovery had identified `pipeline/fixtures/README.md` as the active target
  - only on the next retry did the lane finally switch to direct workspace mutation
- this patch uses that already-known target to tighten the very next continuation instead of letting the session rediscover the same focus rule again

Deploy status:

- code complete
- tests green
- runtime restart pending at the time of this note

## Update 06:54 MDT

I cut the next project-control guidance seam instead of waiting on another opaque `not_found`.

What changed:

- [`internal/tools/native/query_tools.go`](../internal/tools/native/query_tools.go)
  - `flow.get_execution` now checks whether a `repo.ErrNotFound` execution id is actually a real `flow_node` id
  - when that happens, the tool returns:
    - `error = flow_node_execution_id_required`
    - a direct message saying not to call `flow.get_execution` with `task.current_flow_node_id`
- [`internal/tools/native/query_tools_test.go`](../internal/tools/native/query_tools_test.go)
  - added focused coverage for the mistaken flow-node-id case

Verification:

- `gofmt -w internal/tools/native/query_tools.go internal/tools/native/query_tools_test.go`
- `go test ./internal/tools/native -run 'TestFlowGetExecutionDistinguishesFlowNodeID$' -count=1`

Why this slice exists:

- live `project` continuation session `db21265f-c37d-40e4-9ed5-13def09970f8` was still spending turns on opaque `flow.get_execution -> not_found`
- the recent assistant message around `2026-03-27 06:44:48 MDT` called `flow.get_execution` on ids that matched task `current_flow_node_id` values rather than actual `flow_node_execution_id`s
- the old plain `not_found` gave the model no correction signal, so the project lane could rediscover the same mistake

Expected effect:

- turn the project-lane error from opaque failure into actionable correction
- reduce repeated `flow.get_execution` browse churn without widening or mutating any task-lane authority

## Update 07:09 MDT

I cut the next runtime seam behind the recent “wait window” behavior: remote model calls were still allowed to hang for the full async turn watchdog window.

What changed:

- [`internal/gateway/client.go`](../internal/gateway/client.go)
  - remote provider calls now keep the shared HTTP client timeout instead of extending to the async turn deadline
  - only local provider calls still get the longer per-call timeout extension
- [`internal/gateway/client_test.go`](../internal/gateway/client_test.go)
  - updated gateway coverage so remote providers explicitly keep the shared timeout even when the request context deadline is much longer

Verification:

- `gofmt -w internal/gateway/client.go internal/gateway/client_test.go`
- `go test ./internal/gateway -count=1`

Why this slice exists:

- the live one-hour report still showed old remote `agent_turn` invocations sitting `in_flight` while tokens were available and the queue kept moving
- example:
  - turn `3af5ee5a-f290-4a4d-8f2a-52e49d9d4a03`
  - invocation `9a6afe01-4cb9-4ac0-bb62-f23b259d23df`
  - `Anthropic Primary`
  - created at `2026-03-27 06:44:54 MDT`
  - still `in_flight` more than 13 minutes later
- root cause:
  - the gateway was cloning the HTTP client timeout upward to match the async turn context deadline
  - that effectively let a single remote stream hang toward the `30m` async watchdog instead of respecting the normal `5m` remote timeout

Expected effect:

- reduce the “available tokens but still waiting” symptom caused by hung remote calls
- free provider slots and turn ownership sooner when Anthropic goes transient instead of simply stalling

## Update 07:18 MDT

I cut the next hot async task-lane seam after the report made it concrete: repeated `file.read -> not_found` turns were often just rereading the current missing deliverable target.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - when a non-review async `project_task` turn stops with `validation_loop_blocked` and the completed turn already proved the current task deliverable target is missing, the next synthetic continuation prompt is now narrowed
  - the retry prompt explicitly says:
    - the target file is missing
    - do not call `file.read` on it again yet
    - create it directly with `file.write` or emit one short blocker sentence
  - target resolution reuses the existing session/task deliverable path logic, so this stays grounded in explicit deliverable paths, recovery checkpoints, or session-learned targets
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for the missing-deliverable retry prompt helper

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(TaskExecutionRetryPromptForMissingPreferredDeliverable|TaskExecutionRetryPromptForMissingPreferredDeliverableSkipsWhenTargetAlreadyReadable)$' -count=1`

Why this slice exists:

- the fresh blocker report showed recent async task lanes like:
  - task `17` session `2ef376de-6767-4528-9369-185c647d77b5`
  - task `19` session `02a6f625-ba6a-4150-b917-71b9f04c6ced`
  - task `11` session `b8eb751c-5e14-443e-9121-2fd877b7be4d`
- these turns were ending on repeated `file.read -> not_found` while trying to inspect deliverable-style paths before any file had been written
- the old auto-continuation path simply re-enqueued the same kickoff/continuation message, which left too much room to rediscover the same missing target again

Expected effect:

- reduce repeated missing-file discovery churn in execution lanes
- push the next turn toward the first concrete write instead of another read-first loop

## Update 05:55 MDT

I landed the tiny compatibility slice that the first live task-9 proof exposed.

What changed:

- [`internal/tools/native/query_tools.go`](../internal/tools/native/query_tools.go)
  - `task.list(status=all)` now normalizes `all` to the same behavior as omitting the status filter entirely
  - this keeps the new `parent_task_id` path compatible with the exact argument shape Anthropic was already emitting in live review turns
- [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
  - `TestIntegrationTaskListFiltersByParentTaskID` now exercises the live-shaped request:
    - `task.list(parent_task_id=..., status=all)`

Verification:

- `gofmt -w internal/tools/native/query_tools.go internal/tools/native/native_integration_test.go`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegration(TaskListFiltersByParentTaskID|TaskListHidesProjectContinuationMetaDraftsByDefault|TaskListDefaultsToCurrentProjectSessionScope)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Why this matters:

- after the first bounded-review deploy, live task-9 traffic immediately tried `task.list` with `status=all`
- without this alias, a future correctly parent-scoped call using that same argument shape would have returned an empty set for the wrong reason
- this keeps the newly added child-task lookup path permissive in the exact way the live model is already asking for it, without reopening the broader project-wide task listing surface

## Update 06:00 MDT

I tightened review rejection guidance for the next hot family: missing preferred deliverables.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - review prompts now say that if reading the preferred deliverable target returns `not_found`, the model should stop broad inspection and call `flow.review_decision reject`
  - `reviewApprovalRetryPrompt(...)` now routes a retrying review directly to `flow.review_decision reject` when the last review turn already established that the preferred deliverable target itself is missing
  - this is bounded to the preferred-target path parsed from the review prompt; it does not change generic secondary-artifact `not_found` handling
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - prompt coverage now asserts `not_found` is included in the reject guidance
  - added focused retry-path coverage for a missing preferred deliverable target

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(BuildTaskReviewActionPromptIncludesPreferredDeliverableTarget|ReviewApprovalRetryPromptRejectsMissingPreferredDeliverable|HandleTurnCompletedEventBlocksRepeatedReviewFileReadNotFoundTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedRetriesWithoutReviewDecision)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Live state right after deploy:

- task-10 review session [`f83171e8-0068-4f1f-8776-8186daff3a0e`](../issues/0326a-state.md)
  - fresh retry turn at `2026-03-27 05:58:53 MDT`
  - assistant started with the preferred deliverable target as expected
  - `file.read` returned `not_found`
  - then Anthropic failed transiently before the assistant could emit the new `flow.review_decision reject`
- so this slice is deployed and healthy, but its live behavioral proof is still pending a non-interrupted post-`not_found` review turn

## Update 06:06 MDT

I tightened the transient-provider retry path so review evidence is not discarded when Anthropic fails after the turn already learned something deterministic.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `handleTransientModelTurnFailure(...)` now asks for a stronger retry message before requeueing the next async `project_task` turn
  - for review / recovery-resume turns, it reuses `reviewApprovalRetryPrompt(...)`
  - when the failed turn already established a deterministic reject case, the next queued retry uses a synthesized review prompt instead of the stale original prompt
  - the current bounded case is the one we just hardened:
    - missing preferred deliverable target
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving a transiently failed review turn with `file.read -> not_found` queues a new reject-oriented user message rather than the original review prompt

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(HandleTurnJobAsyncProjectTaskTransientProviderEnqueuesRetryWithoutSameTurnRetry|HandleTransientModelTurnFailureUsesReviewRetryPromptFromFailedTurn|ReviewApprovalRetryPromptRejectsMissingPreferredDeliverable|BuildTaskReviewActionPromptIncludesPreferredDeliverableTarget)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Why this matters:

- the dominant live failure family is still `provider_transient_failure`
- before this slice, a transient provider failure after a decisive review `tool_result` meant the next retry started from the original broad review prompt again
- after this slice, the next retry can carry forward the already-earned reject evidence and avoid re-reading the same missing target

Live status:

- deployed and healthy on the new binary
- fresh live proof is still pending the next real transiently interrupted review turn on this runtime

## Update 06:11 MDT

I removed another deterministic retry from orchestration-parent review lanes by repairing the exact `task.list` argument shape the live model had been getting wrong.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project_task` review lanes for orchestration-only parent tasks now auto-inject `parent_task_id=current_task` for `task.list` when the raw model call is still clearly trying to scope to the current parent
  - the current bounded injection covers the live shape we already observed:
    - raw `project_id=current_task`
    - optional `status=all`
  - explicit broad-project/task requests are still left alone and remain blocked by the existing review guard
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving:
    - the narrow `parent_task_id` injection happens for the live task-9 shape
    - explicit broad-project requests do not get auto-corrected

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ShouldBlockOrchestrationParentReviewTaskListToolRequiresParentScopedList|MaybeInjectOrchestrationParentReviewTaskListParentID|MaybeInjectOrchestrationParentReviewTaskListParentIDIgnoresExplicitBroadProjectScope)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Live status right after deploy:

- task-9 review session [`729dd9e7-36c4-46a1-988b-8e35e5b96b88`](../issues/0326a-state.md)
  - first fresh post-deploy retries at `2026-03-27 06:06:46 MDT` were still interrupted by transient provider failure immediately after rereading the orchestration summary
  - so this slice is deployed and healthy, but the new auto-injected child-task lookup has not yet been observed on a completed live review turn

## Update 06:14 MDT

I extended the transient review retry carry-forward logic to the orchestration-parent summary case that task 9 is currently burning on.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `reviewApprovalRetryPrompt(...)` now emits a specialized orchestration-parent retry prompt when:
    - the task is an orchestration-only parent in review
    - the preferred parent summary target was already read successfully in the failed turn
    - the turn did not yet reach direct child-task evidence
  - that retry prompt says:
    - do not reread the parent summary
    - do not call `task.get` on the parent
    - continue directly with `task.list parent_task_id=<current task> status=all`
  - this composes with the just-landed narrow `parent_task_id` auto-injection, so transiently interrupted task-9 reviews should now resume on child-task evidence instead of paying for another summary reread
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for the new orchestration-parent retry prompt helper

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ReviewApprovalRetryPromptCarriesForwardOrchestrationParentSummaryEvidence|HandleTransientModelTurnFailureUsesReviewRetryPromptFromFailedTurn|MaybeInjectOrchestrationParentReviewTaskListParentID|MaybeInjectOrchestrationParentReviewTaskListParentIDIgnoresExplicitBroadProjectScope)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Live status:

- deployed and healthy on the new binary
- fresh live proof is still pending the next post-restart task-9 retry on this runtime

## Update 06:20 MDT

I now have fresh live proof for two of the review hardening slices, and I landed the next task-9-specific follow-on on top of them.

Fresh live proofs:

- task-10 missing preferred deliverable rejection is end-to-end proven:
  - session `f83171e8-0068-4f1f-8776-8186daff3a0e`
  - turn at `2026-03-27 06:09:01 MDT`
  - `file.read` on `Work/OC-10-WORKSTREAM-B-REVIEW-PATH-VALIDATION.md` returned `not_found`
  - assistant then emitted `flow_review_decision` with:
    - `decision=reject`
    - `flow_node_execution_id=34a65519-ec31-485c-bed4-e53a91c031ed`
- task-9 orchestration-parent `task.list` auto-injection is live-proven:
  - session `729dd9e7-36c4-46a1-988b-8e35e5b96b88`
  - turn at `2026-03-27 06:11:57 MDT`
  - assistant persisted the old raw call shape:
    - `task_list(status=all, project_id=a5cc62da-5ae4-4c0f-b299-d25fd6f743fb)`
  - runtime no longer blocked it
  - the resulting `task.list` succeeded and returned `tasks: []`
- task-9 transient review narrowing is also live-proven:
  - after the `06:17:10 MDT` transient provider failure, the runtime appended:
    - `[Transient retry narrowed the next review prompt using evidence gathered before the provider failure.]`
  - the fresh user message at `06:17:14 MDT` explicitly added:
    - the parent summary was already established as present and substantive
    - do not reread the parent summary
    - continue directly with `task.list parent_task_id=<current task> status=all`

What I changed next:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - review retries for orchestration-only parent tasks now reject when:
    - the parent summary is readable
    - the direct child-task lookup succeeds
    - and that lookup returns zero direct child tasks
  - this converts the newly observed live task-9 shape into a reject-oriented retry instead of another loop
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for the empty-direct-child reject branch

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ReviewApprovalRetryPromptRejectsOrchestrationParentWithoutDirectChildren|ReviewApprovalRetryPromptCarriesForwardOrchestrationParentSummaryEvidence|MaybeInjectOrchestrationParentReviewTaskListParentID|HandleTransientModelTurnFailureUsesReviewRetryPromptFromFailedTurn)$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Current status:

- the new empty-direct-child rejection slice is deployed and healthy on the newest binary
- fresh live proof for that final step is still pending the next post-`06:18 MDT` task-9 retry

## Update 06:42 MDT

The next task-9 review retries exposed a narrower gap in the empty-direct-child reject path.

What happened live:

- task-9 session `729dd9e7-36c4-46a1-988b-8e35e5b96b88`
- the retry at `06:23:10 MDT` still used the correct orchestration-parent review prompt and reread the parent summary
- the transient-narrowed retry prompt at `06:23:15 MDT` correctly carried forward:
  - the parent summary was already established as readable
  - do not reread the parent summary
  - continue with `task.list parent_task_id=<current task> status=all`
- but it did **not** yet carry forward the earlier successful direct-child lookup result from `06:11:57 MDT`, where `task.list` had already returned `tasks: []`

That showed the bug clearly:

- orchestration-parent empty-child rejection was only using evidence from the immediately failed turn
- it was not reusing already-proven `task.list(parent_task_id=...) -> []` evidence from an earlier retry turn in the same review session

What I changed next:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - added a session-level fallback for orchestration-parent review retries:
    - if prior review evidence in the same session already proved the preferred parent summary is readable
    - and the latest scoped child-task lookup already established `0` direct child tasks
    - the next retry prompt now goes straight to `flow.review_decision reject`
  - the fallback keys off persisted assistant `tool_calls` metadata so it only treats scoped `task.list(parent_task_id=...)` or injected `task.list(project_id=<current task id>)` calls as direct-child evidence
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for:
    - empty direct-child rejection within a single turn
    - empty direct-child rejection carried across a later summary-only retry turn

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ReviewApprovalRetryPromptRejectsOrchestrationParentWithoutDirectChildren|ReviewApprovalRetryPromptRejectsOrchestrationParentWithoutDirectChildrenAcrossRetryTurns|ReviewApprovalRetryPromptCarriesForwardOrchestrationParentSummaryEvidence|HandleTransientModelTurnFailureUsesReviewRetryPromptFromFailedTurn)$' -count=1`

Current status:

- the session-level empty-child carry-forward fix is coded and test-green
- runtime restart is complete and healthy
- fresh prompt-level live proof now exists:
  - task-9 session `729dd9e7-36c4-46a1-988b-8e35e5b96b88`
  - retry `16` started at `06:28:59 MDT` on the new binary
  - the transient failure happened before any tool call, which forced the narrowed retry-prompt path to run immediately
  - the new synthesized user message at `06:29:00 MDT` now explicitly carries forward:
    - the parent summary is already established as substantive
    - `task.list` already returned zero direct child tasks beneath the orchestration parent
    - call `flow.review_decision` immediately with `decision=reject`

That is the proof we needed for this slice:

- session-level direct-child-empty evidence is now surviving across retry turns
- the runtime no longer falls back to another “inspect child tasks” loop once that empty-child evidence is already known

## Update 06:41 MDT

The next live retries showed the remaining behavior much more clearly.

What happened live:

- task-9 session `729dd9e7-36c4-46a1-988b-8e35e5b96b88`
- the reject-only review prompt was now being generated correctly
- but the model still opened the next turn with `file.read Work/OC-9-WORKSTREAM-A-PIPELINE-SCAFFOLD-SETUP.md` instead of calling `flow.review_decision reject`

What I changed next:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - added an orchestration-parent review reject-only guard:
    - when the current review prompt already states that zero direct child tasks are established
    - and that the model should call `flow.review_decision` immediately with `decision=reject`
    - the runtime now blocks rereading the parent summary target with `file.read`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for that reject-only reread block

Live proof:

- retry `19` at `06:35:24 MDT`
- retry `20` at `06:35:49 MDT`
- both turns attempted:
  - `file.read Work/OC-9-WORKSTREAM-A-PIPELINE-SCAFFOLD-SETUP.md`
- both turns now received the new guard error instead:
  - `task review already established that task 9 (Workstream A: Pipeline Scaffold Setup) has zero direct child-task evidence and should reject. Do not reread \`Work/OC-9-WORKSTREAM-A-PIPELINE-SCAFFOLD-SETUP.md\`; call flow.review_decision with decision=reject immediately using the existing evidence.`

That proved the runtime guard, but it also exposed one more escape path:

- after the blocked reread, the model tried broader discovery again (`file.list Work pattern=OC-9*`) before the provider failed

What I changed immediately after:

- widened the same reject-only guard in [`internal/turn/engine.go`](../internal/turn/engine.go) to cover broader discovery tools in this exact orchestration-parent reject mode:
  - `file.list`
  - `file.search`
  - `task.list`
  - `task.get`
  - `project.get`
  - `project.list`
  - `git.diff`
  - `git.log`
  - `git.status`
- updated focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ShouldBlockOrchestrationParentReviewRejectDiscoveryTool|ShouldBlockOrchestrationParentReviewTaskListToolRequiresParentScopedList|ShouldBlockOrchestrationParentReviewCurrentTaskGetTool|ReviewApprovalRetryPromptRejectsOrchestrationParentWithoutDirectChildrenAcrossRetryTurns)$' -count=1`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Current status:

- the reject-only `file.read` block is live-proven
- the broadened reject-only discovery block is now live-proven too

Fresh live proof:

- task-9 retry `22` started at `06:41:53 MDT`
- first tool call:
  - `file.read Work/OC-9-WORKSTREAM-A-PIPELINE-SCAFFOLD-SETUP.md`
  - blocked again by the reject-only reread guard
- next assistant fallback:
  - `file.list path=Work pattern=OC-9*`
  - blocked by the widened reject-only discovery guard with:
    - `task review already established that task 9 (Workstream A: Pipeline Scaffold Setup) has zero direct child-task evidence and should reject. Do not inspect additional files, tasks, or project state with file.list; call flow.review_decision with decision=reject immediately using the existing evidence.`

What this means:

- the file-summary reread escape path is closed
- the broader workspace-discovery escape path is also closed
- the remaining live seam is now narrower still: whether the next retry finally issues `flow.review_decision reject` or simply bounces between blocked discovery attempts until the provider fails again

## Update 06:48 MDT

The transient retry scheduler fix is now live-proven too.

What was still wrong before this slice:

- when a reject-only review prompt hit a provider failure before any tool call, the next queued retry could still fall back to the base review prompt
- that kept alternating base prompts and reject prompts even though no new evidence had appeared

What I changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `transientTurnRetryMessageID(...)` now reuses an already reject-only review prompt instead of regenerating a fresh retry prompt after a transient provider failure
  - the trigger is intentionally narrow:
    - only when the current review prompt already says `Call flow.review_decision immediately with decision=reject`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage that a reject-only review prompt is reused across transient failures

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(HandleTransientModelTurnFailureUsesReviewRetryPromptFromFailedTurn|HandleTransientModelTurnFailureReusesImmediateRejectReviewPrompt|ShouldBlockOrchestrationParentReviewRejectDiscoveryTool|ReviewApprovalRetryPromptRejectsOrchestrationParentWithoutDirectChildrenAcrossRetryTurns)$' -count=1`
- rebuilt/restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json`

Live proof:

- task-9 retry `24` started at `06:47:45 MDT`
- it failed before any tool call, so this was the exact old regression case
- on the new binary, the next queued retry `25` now points to message `6f334db1-0ff9-48de-8daf-9d0fc43a916a`
- that message still carries the reject-only evidence markers:
  - `zero direct child tasks`
  - `decision=reject`

That is the proof we needed:

- reject-only review prompts no longer fall back to the base review prompt after a transient provider failure
- the remaining question is purely behavioral at this point: whether the next successful provider window causes the model to finally call `flow.review_decision reject`

## Update 05:40 MDT

The orchestration-parent review prompt fix is now live-proven, and the next concrete leak is narrower.

Live proof:

- the prompt created at `2026-03-27 05:22:17 MDT` for task `9` was from the old binary; the serve pane restart happened at `2026-03-27 05:22:22 MDT`
- the first fresh post-restart task-9 review prompt, message `0b6d59ee-e3dd-4f91-a279-08902f79cbb3` in session `e067dff5-f31e-41c6-b584-d1f90dc1e91c`, includes the new orchestration-parent override:
  - stay on the parent orchestration summary
  - do not inspect `planning/prd-spec` or other companion planning files
  - use `task.list` with `parent_task_id=<current task>` for child-task evidence only

What still drifted:

- after reading `Work/OC-9-WORKSTREAM-A-PIPELINE-SCAFFOLD-SETUP.md`, that same fresh review lane still chose:
  - `task.get` on the current parent task
  - then broad `task.list` against the whole project
  - then `file.list Work` and `git.log`
- the planning-artifact leak is fixed; the remaining leak is broad child-evidence discovery

Code now landed for that narrower seam:

- [`internal/tools/native/query_tools.go`](../internal/tools/native/query_tools.go)
  - `task.list` now accepts `parent_task_id`
  - filtering uses `metadata.decomposition_parent_task_id`
- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - orchestration-parent review prompts now explicitly instruct:
    - use `task.list(parent_task_id=current_task)` for direct child-task evidence
    - do not use `task.list(project_id=...)`
  - async orchestration-parent review lanes now block the broad `task.list` variant and return a direct correction pointing at `parent_task_id=<current task id>`
- [`migrations/0130_task_list_parent_task_schema.sql`](../migrations/0130_task_list_parent_task_schema.sql)
  - exposes `parent_task_id` and `include_meta_drafts` on the live `task.list` schema

Verification:

- `go test ./internal/turn -run 'Test(ShouldBlockOrchestrationParentReviewTaskListToolRequiresParentScopedList|ShouldBlockTaskExecutionBroadContextToolAllowsOrchestrationValidationContextReads|BuildTaskReviewActionPromptSpecializesOrchestrationOnlyParentReview)$' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegration(TaskListFiltersByParentTaskID|TaskListHidesProjectContinuationMetaDraftsByDefault|TaskListDefaultsToCurrentProjectSessionScope)$' -count=1`
- `go test -tags=integration ./internal/repo -run 'TestKeyToolSchemasExposeRequiredParameters$' -count=1`

Deploy status at this note:

- code complete
- focused tests green
- runtime rebuilt/restarted successfully at `2026-03-27 05:35 MDT`
- `./bin/ottercamp health --output json` returned `status=ok`
- live schema proof:
  - `tool_definition.name='task.list'` now exposes both `parent_task_id` and `include_meta_drafts`
- remaining live proof gap:
  - the first fresh post-deploy task-9 retry at `05:36:50 MDT` still died before any tool call because Anthropic failed immediately

## Update 05:47 MDT

The orchestration-parent review `task.list` guard is now live-proven on the new runtime.

Fresh live proof:

- session `729dd9e7-36c4-46a1-988b-8e35e5b96b88`
- retry turn starting at `2026-03-27 05:45:57 MDT`
- assistant behavior:
  - read `Work/OC-9-WORKSTREAM-A-PIPELINE-SCAFFOLD-SETUP.md`
  - then attempted `task_list` again with the wrong broad shape instead of the new bounded child-task form
- runtime response at `2026-03-27 05:46:12 MDT`:
  - `task execution should not re-list the broader project task tree from task 9 (Workstream A: Pipeline Scaffold Setup) while it is in review. If you need task evidence, call task.list with parent_task_id=a5cc62da-5ae4-4c0f-b299-d25fd6f743fb to inspect that parent's direct child tasks only.`

What this proves:

- the deployed review-lane guard is actually intercepting the old bad branch in production
- task-9 review no longer gets to browse the broad project task tree once the parent summary is already in hand

Follow-on hardening now layered on top:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - orchestration-parent review lanes now also block `task.get` on the current parent task itself
  - the orchestration-parent review prompt now explicitly says not to call `task.get` on the parent again during review
- focused verification:
  - `go test ./internal/turn -run 'Test(ShouldBlockOrchestrationParentReview(CurrentTaskGetTool|TaskListToolRequiresParentScopedList)|BuildTaskReviewActionPromptSpecializesOrchestrationOnlyParentReview)$' -count=1`

Current proof state:

- live-proven:
  - orchestration-parent review prompt specialization
  - `task.list(parent_task_id=...)` schema exposure
  - orchestration-parent review guard against broad `task.list`
- still pending fresh live proof:
  - the new `task.get(current parent)` guard specifically

## Update 05:15 MDT

The next live investigation changed the diagnosis on the `task 11` churn.

What I verified live:

- project session `db21265f-c37d-40e4-9ed5-13def09970f8` auto-queued task `11` after a read-only project continuation turn
- the first `project_task` turn for task `11` then opened with:
  - `task.get`
  - `flow.get_execution`
  - `file.read planning/discovery-plan/oc-11-validation-plan.md`
  - `file.read planning/discovery-plan/oc-11-problem-brief.md`
  - both planning reads returned `not_found`, then the turn halted on the existing repeated `file.read` cutoff
- querying the live task row showed task `11` is not normal execution work:
  - title: `Workstream C: Wave Gating Validation`
  - description: `Parent/orchestration task ... Does not do execution work itself.`
  - metadata already carries discovery-plan artifact contracts for `problem-brief`, `research-plan`, `assumption-log`, and `validation-plan`

What this means:

- the immediate read misses were not a bad recovery-target inference
- they were a downstream symptom of the wrong task being promoted into a task-execution lane
- the actual bug is in the project continuation auto-queue selector, which was still treating orchestration-only parents as runnable draft execution work

Code/test slice landed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `nextRunnableDraftProjectTask(...)` now skips draft tasks that match the orchestration-only parent heuristic
  - task-lane helpers now share a broader orchestration-only heuristic that recognizes the live description shape:
    - `Parent/orchestration task`
    - `Does not do execution work itself`
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving:
    - the orchestration-only heuristic matches this parent-task description shape
    - the next-runnable selector skips the orchestration parent and selects the bounded child task instead
  - also reran the adjacent `handleCompletedProjectExecutionContinuationTurn` slice to keep the auto-queue repair path covered

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(TaskLooksLikeOrchestrationOnlyParent|NextRunnableDraftProjectTaskSkipsOrchestrationOnlyParent)$' -count=1`
- `go test ./internal/turn -run 'TestHandleCompletedProjectExecutionContinuationTurn.*' -count=1`

Deploy status:

- code complete
- tests green
- runtime is now rebuilt/restarted on the latest binary and `./bin/ottercamp health --output json` is `ok`
- first tmux respawn attempt failed with `config error: OTTERCAMP_MODE is required` because `.env` was sourced without export in the non-interactive shell
- second respawn used:
  - `set -a`
  - `source .env`
  - `set +a`
  - `export OTTERCAMP_MODE=development`
- fresh live proof of the selector change is still pending the next project continuation retry on this build

## Update 05:20 MDT

The first fresh retry on the new runtime showed there is a second orchestration-parent activation path.

What happened live:

- project session `db21265f-c37d-40e4-9ed5-13def09970f8` resumed on a fresh pending continuation message
- before that project turn could settle, task `11` got a new active task session again:
  - session `fcef7e7d-847d-4caa-9bd4-c8c85e41a337`
  - kickoff user message `f58b0ec6-0d37-4935-b148-edb43cd88e30`
  - content still started with `Start work on task: Workstream C: Wave Gating Validation`
- the kickoff metadata showed this was not the patched project continuation selector:
  - `source = task_queue_processor`
  - `flow_event_type = flow.rejected`
  - `flow_node_execution_id = 283e4c7f-be76-436e-b769-6cdcfe3f684f`

What this means:

- skipping orchestration parents in `nextRunnableDraftProjectTask(...)` was still the right fix for the project-continuation repair path
- but `task_queue_processor` was separately reactivating the same orchestration parent after review rejection with a generic task-work kickoff
- that second path is what kept feeding `task 11` back into planning-artifact rereads

Code/test slice landed:

- [`internal/controlplane/task_queue_processor.go`](../internal/controlplane/task_queue_processor.go)
  - `buildQueueKickoffMessage(...)` now adds orchestration-only parent instructions:
    - do not execute the parent deliverable directly
    - inspect the child-task set
    - create or repair bounded executable child tasks beneath the parent
    - do not begin by rereading planning artifacts unless a concrete blocker names one
  - `buildFlowTransitionKickoffMessage(...)` now adds explicit rejection-recovery guidance for orchestration-only parents on `flow.rejected`
  - the queue processor uses the same description/metadata heuristic shape that now exists in the turn engine for orchestration-only parents
- [`internal/controlplane/task_queue_processor_test.go`](../internal/controlplane/task_queue_processor_test.go)
  - added focused unit coverage for:
    - orchestration-parent queue kickoff guidance
    - orchestration-parent `flow.rejected` kickoff guidance

Verification:

- `gofmt -w internal/controlplane/task_queue_processor.go internal/controlplane/task_queue_processor_test.go`
- `go test ./internal/controlplane -run 'Test(BuildQueueKickoffMessageForOrchestrationOnlyParent|BuildFlowTransitionKickoffMessageForRejectedOrchestrationOnlyParent|TaskQueueProcessorHandleFlowAdvancedEvent.*)$' -count=1`

Deploy status:

- code complete
- tests green
- runtime is now rebuilt/restarted on the latest binary and `./bin/ottercamp health --output json` is `ok`
- fresh live proof of the new kickoff wording is still pending because the only observed `task 11` `flow.rejected` kickoff on this path predates the deploy

## Update 05:26 MDT

The next live family is now isolated to orchestration-parent review lanes, not kickoff/wakeup selection.

Concrete live trace:

- task `9` latest review turn `20d25c95-b9c2-4bbe-a247-71270c14d241`
- session `a5cc62da-5ae4-4c0f-b299-d25fd6f743fb`
- prompt was already the normal `Review only ... flow.review_decision` prompt
- that turn did the following:
  - read preferred deliverable target `Work/OC-9-WORKSTREAM-A-PIPELINE-SCAFFOLD-SETUP.md` successfully
  - then immediately tried:
    - `planning/prd-spec/oc-9-acceptance-criteria.md`
    - `planning/prd-spec/oc-9-prd.md`
    - `planning/prd-spec/oc-9-implementation-plan.md`
    - `planning/prd-spec/oc-9-dependency-log.md`
  - all four returned `file.read -> not_found`
  - the turn then hit the existing repeated `file.read` cutoff and eventually the repeated-review block

What this means:

- the remaining waste on these parent tasks is no longer about wakeup selection
- it is the review prompt still allowing orchestration-parent reviews to reconstruct the rubric from companion planning artifacts after the parent summary file is already present

Code/test slice landed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `buildTaskReviewActionPrompt(...)` now special-cases orchestration-only parent tasks
  - those review prompts now tell the model to review:
    - the parent orchestration summary
    - direct child-task outcomes / outputs
  - and explicitly not to inspect:
    - `planning/prd-spec/*`
    - `planning/discovery-plan/*`
    - other companion planning files for the parent task once the parent summary is readable
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused prompt coverage for this orchestration-parent review override
  - reran the adjacent preferred-target / missing-tests review prompt slice

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(BuildTaskReviewActionPrompt(IncludesPreferredDeliverableTarget|SpecializesOrchestrationOnlyParentReview)|ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified)$' -count=1`

Deploy status:

- code complete
- tests green
- runtime restart/live proof still pending at the time of this note

## Update 04:55 MDT

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - project continuation read-only discovery classification now also includes:
    - `session.list`
    - `inbox.list`
  - this widens the same post-turn auto-queue repair for browse-only `project_execution_continuation` / `project_continuation_resume` turns
- [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `RecoverStaleClaims(...)` no longer exempts every claimed `agent_turn` that merely has any `in_flight` invocation
  - the exemption now only holds while that invocation is still within the existing scope/model stale window:
    - `project_task`: `defaultStaleThreshold`
    - normal async `project`: `staleContinuationThreshold`
    - slow local-model async `project`: `slowProjectAsyncModelThreshold`
  - this lets dead claimed jobs with clearly stale live invocations fall through to the existing stale-invocation cleanup path instead of pinning a worker slot forever
- [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
  - `ottercamp db token-usage` now includes `in_flight_agent_turns`
- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now prints `Oldest In-Flight Agent Turns`

Verification:

- `go test ./internal/turn -run 'TestHandleCompletedProjectExecutionContinuationTurn(AutoQueuesRunnableDraft|AutoQueuesAfterReadOnlyToolResults|HandlesProjectContinuationResumeSource|IgnoresMutatingToolResults|ConsumesBoundedSizeQueueFailure|RetriesGenericReplyWithFreshMessage|RetriesDependencyErrorCoachingReplyWithFreshMessage|RetriesAllScopeTaskCreateCoachingReplyWithFreshMessage)$' -count=1`
- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerRecoverStaleClaims(KeepsClaimedAgentTurnWithLiveInvocation|ReleasesClaimedAgentTurnWithStaleLiveInvocation)$' -count=1`
- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`
- `bash -n scripts/token-usage-report.sh`

Live proof:

- the new `Oldest In-Flight Agent Turns` report initially surfaced three live stuck async turns, including:
  - invocation `9b2e868d-c451-47b4-9b8d-33a2677cbaa7`
  - turn `e156b347-a86b-4cb1-ae47-e5f87e25c14e`
  - session `db21265f-c37d-40e4-9ed5-13def09970f8`
- before I restarted anything, the existing stale-invocation cleanup fired and the report dropped back to zero in-flight rows
- direct DB proof:
  - turn `e156b347-a86b-4cb1-ae47-e5f87e25c14e` is now `failed`
  - `error_message = worker cleanup failed stale in_flight model invocation without live in-progress turn`
  - completed at `04:52:19 MDT`

Deploy status:

- `session.list` / `inbox.list` continuation repair, the new in-flight operator report, and the narrowed stale-claim exemption are now live on the rebuilt runtime

Fresh live proof after deploy:

- project session `db21265f-c37d-40e4-9ed5-13def09970f8`
- turn `89e07a94-39c5-4790-9618-43454c21e8f5`
- trigger source: `project_continuation_resume`
- read-only tool mix on the max-tool-call turn:
  - `task.list`
  - `project.get`
  - `task.get`
  - `flow.get_execution`
  - `session.list`
  - `agent.list`
- resulting system message:
  - `[Project continuation auto-queued task 11 (Workstream C: Wave Gating Validation) after a non-mutating continuation left runnable draft work untouched.]`

This is the direct live proof that the widened continuation repair is now firing on the resume-shaped browse turns that were previously burning full extra project continuations.

## Update 04:16 MDT

I closed the next provider-churn seam: transient-provider retry budgeting was still using the generic queued-turn `RetryCount`, which let worker-repaired async sessions arrive at the turn engine looking artificially “exhausted.”

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `handleTransientModelTurnFailure(...)` no longer uses raw payload `RetryCount` to decide whether transient-provider retries are exhausted
  - it now counts consecutive prior failed turns for the same `trigger_message_id` whose terminal `chat_turn.error_message` still looks provider-transient
  - that transient-only counter drives both:
    - exhausted-provider stop decisions
    - the transient retry delay ladder
  - the generic queued-turn retry counter is still preserved on the replacement `agent_turn` payload for visibility, but it no longer trips the transient-provider cap by itself
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - widened the retry-cap coverage so it proves a real chain of prior transient failures still blocks cleanly
  - added coverage proving a high generic retry count without prior transient failed turns still gets a delayed retry instead of a false exhausted-provider stop

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'TestHandleTurnJobAsyncProjectTaskTransientProvider(RetryCapStopsRequeue|IgnoresHighGenericRetryCountWithoutPriorTransientFailures|EnqueuesRetryWithoutSameTurnRetry)$' -count=1`
- `go test ./internal/turn -run 'Test(HandleTurnCompletedEventEnqueuesAutoContinuation|HandleTurnCompletedEventDefersAutoContinuationAfterTransientProviderRetryExhausted|HandleTurnJobAsyncProjectTaskTransientAvailabilityPreflightDefersBeforePromptAssembly|HandleTurnJobAsyncProjectTaskTransientProviderEnqueuesRetryWithoutSameTurnRetry|HandleTurnJobAsyncProjectTaskTransientProviderRetryCapStopsRequeue|HandleTurnJobAsyncProjectTaskTransientProviderIgnoresHighGenericRetryCountWithoutPriorTransientFailures|HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersPastRetryCapWithoutTurn)$' -count=1`

Why this matters:

- the worker-side patch already stopped immediate idle-session requeue churn
- but live backlog inspection still showed pending async jobs like:
  - retry `7`
  - retry `21`
- those large numbers were legitimate generic recovery counters, not evidence that the same session had already burned that many consecutive transient model failures
- before this change, the next transient miss could be misclassified as “retries exhausted” on the first fresh turn after repair

Expected live effect:

- async sessions coming out of worker-preserved provider backoff should now get the intended delayed transient retry behavior even when their generic queued-turn retry counter is already large
- the next real transient-provider lane should no longer jump straight from “worker delayed me for a while” to “provider retries exhausted” unless it actually has consecutive prior transient failed turns on the same trigger message

Deploy status:

- rebuilt `./bin/ottercamp`
- restarted tmux `codex-e2e-20260324`
- `./bin/ottercamp health --output json` is `ok`

Live proof:

- the decoupling is now live-proven on two high-generic-retry async task sessions:
  - session `20bd72b0-375e-4a02-82d5-c2c98929c2aa`
    - failed turn `707d69a9-3218-4d74-80fe-bc8e58bfa0eb`
    - turn `retry_count = 21`
    - created `2026-03-27 04:16:07 MDT`
    - still produced a delayed pending retry job `c1730360-8a47-4066-9199-2376f3eab708`
    - pending payload `retry_count = 22`
    - `run_after = 2026-03-27 04:21:07 MDT`
  - session `aae5aae5-0298-44df-9932-fccf8db6a416`
    - failed turn `c462f0e0-007c-47d0-9733-6231c9354c00`
    - turn `retry_count = 7`
    - created `2026-03-27 04:15:22 MDT`
    - still produced a delayed pending retry job `176a9b91-fc69-4099-aa80-ea0066824874`
    - pending payload `retry_count = 8`
    - `run_after = 2026-03-27 04:20:23 MDT`
- that is the exact proof target for this slice:
  - large generic queued-turn retry counts are still visible
  - but they no longer cause an immediate exhausted-provider stop on the next transient miss

## Update 04:41 MDT

I cut the next control-lane seam in the live async project session churn: project continuations were treating successful read-only discovery turns as “real progress,” which let them burn a full turn on browse/read tools and then auto-continue instead of taking the already-available draft-task action.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `handleCompletedProjectExecutionContinuationTurn(...)` no longer treats any successful tool result as sufficient progress
  - it now distinguishes:
    - successful mutating tool results: leave the continuation alone
    - successful read-only discovery results: still eligible for the existing draft-task auto-queue repair
  - added `turnHasSuccessfulMutatingToolResult(...)`
  - widened read-only discovery classification to include the actual project-continuation browse tools showing up live:
    - `project.list`
    - `project.get`
    - `task.list`
    - `task.get`
    - `flow.list_templates`
    - `flow.get_template`
    - `flow.get_execution`
    - `session.list`
    - `inbox.list`
    - `agent.list`
    - `memory.query`
    - `memory.list`
  - widened the same repair path to accept both continuation message roots:
    - `project_execution_continuation`
    - `project_continuation_resume`
  - that matters because the live hot `db212...` turns were resume turns appended after `[Max tool calls reached - continuing in a new turn.]`, not the post-task-completion continuation source
  - read-only `cli.execute` inspection still stays read-only; mutating shell commands still count as progress
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving a read-only continuation with successful browse/read results still auto-queues the next runnable draft task
  - added focused coverage proving the `project_continuation_resume` source uses the same repair path
  - added coverage proving a continuation that already made a mutating tool call does not trigger the auto-queue repair path

Why this slice exists:

- live project session `db21265f-c37d-40e4-9ed5-13def09970f8` showed the exact bad turn shape
- turn `dd039a2d-5235-42f3-999b-88210b38851a` at `04:20 MDT` spent its full budget on:
  - `project.list`
  - `task.list`
  - `task.list`
  - `file.read`
  - `file.read`
  - `flow.list_templates`
  - `task.get`
  - `task.get`
- then ended with:
  - `[Max tool calls reached. Turn ended.]`
  - `[Max tool calls reached - continuing in a new turn.]`
- that turn had no successful mutating tool result at all, but the old continuation repair path still ignored it because the read-only tools were technically “successful”

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'TestHandleCompletedProjectExecutionContinuationTurn(AutoQueuesRunnableDraft|AutoQueuesAfterReadOnlyToolResults|IgnoresMutatingToolResults|ConsumesBoundedSizeQueueFailure|RetriesGenericReplyWithFreshMessage)$' -count=1`
- `go test ./internal/turn -run 'TestHandleCompletedProjectExecutionContinuationTurn(AutoQueuesRunnableDraft|AutoQueuesAfterReadOnlyToolResults|IgnoresMutatingToolResults|ConsumesBoundedSizeQueueFailure|RetriesGenericReplyWithFreshMessage|RetriesDependencyErrorCoachingReplyWithFreshMessage|RetriesAllScopeTaskCreateCoachingReplyWithFreshMessage)$' -count=1`

Expected live effect:

- async project continuations that only browse/read and summarize should now fall back to the existing direct project action repair instead of getting a free pass just because the read tools succeeded
- the specific `db212...` family should shift from repeated read-only `max_tool_calls` turns toward direct auto-queuing of the next runnable draft task whenever one still exists

## Update 04:05 MDT

I fixed the worker-side transient-provider recovery gap that was still rearming idle async sessions immediately after a failed provider-transient turn.

What changed:

- [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `RequeueActiveExecutionSessionsWithoutTurns(...)` now treats transient provider failures like delayed work, not immediately runnable backlog
  - `RequeueActiveProjectSessionsWithoutTurns(...)` now does the same for async project continuations
  - both paths now:
    - detect transient provider failure text in `chat_turn.error_message`
    - parse hinted `retry_after=` windows from `TransientModelError` text when present
    - schedule the replacement `agent_turn` with transient backoff instead of `run_after = now`
- [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - added focused integration coverage for:
    - active execution sessions preserving generic transient-provider backoff
    - active project sessions preserving hinted transient-provider backoff
  - also corrected an older stale expectation in the active-execution rate-limit test: the worker has long requeued that path with `retry_count = 1`, not `0`

Why this was necessary:

- the previous turn-engine patches stopped same-turn transient retry burn
- but idle-session worker recovery was still requeuing active async sessions immediately whenever the last failed turn left no pending replacement job
- that meant review/work lanes could still climb as:
  - `Retry attempt 6 started.`
  - `Retry attempt 7 started.`
  - `Retry attempt 8 started.`
  - even though the underlying failures were still provider-transient and should have been delayed

Verification:

- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerRequeueActive(ExecutionSessionsWithoutTurnsPreserves(TransientProvider|RateLimit)Backoff|ProjectSessionsWithoutTurnsPreserves(TransientProvider|RateLimit)Backoff)$' -count=1`
- rebuilt `./bin/ottercamp`
- recreated tmux session `codex-e2e-20260324` with `.env` sourced for both:
  - `./bin/ottercamp serve`
  - `./bin/ottercamp worker --concurrency 2`
- `./bin/ottercamp health --output json` is now:
  - `status = ok`

Current live proof status:

- the patch is deployed and the runtime is healthy
- I queried for fresh pending async `agent_turn` rows whose latest failed turn looked transient-provider-related and matched the worker’s new delayed-requeue shape
- that query returned no rows in the short post-restart window, which means:
  - there was no fresh live transient-idle async session to recover yet
  - so this slice is green in integration and live-deployed, but still waiting for the next real transient-worker-recovery event to prove itself in production

## Update 03:33 MDT

I landed the next provider-churn hardening slice aimed at the remaining transient-failure bursts that were still creating fresh failed turns every few seconds.

What changed:

- [`internal/gateway/router.go`](../internal/gateway/router.go)
  - the router now distinguishes:
    - all eligible connections `rate_limited`
    - all eligible connections `unavailable` but still inside their recovery window
  - the second case now returns `ConnectionsUnavailableError{RetryAfter}` instead of collapsing to plain `ErrNoHealthyConnection`
- [`internal/gateway/client.go`](../internal/gateway/client.go)
  - `ProbeAvailability(...)` now maps that router error to `turn.NewTransientModelError(retry_after, ...)`
  - normal request routing now maps the same condition to the same hinted transient error if it occurs outside preflight
  - while widening gateway coverage, I also fixed an existing test-only nil-clock bug in `mapProviderError(...)`; direct unit construction now safely falls back to `time.Now` when `g.now` is unset
- [`internal/turn/errors.go`](../internal/turn/errors.go)
  - added `TransientModelError`, parallel to `RateLimitedError`, so retry timing survives `errors.Is(err, ErrModelTransient)`
- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async cooldown preflight now also defers on hinted transient recovery windows before prompt assembly / turn creation / invocation creation
  - the delayed transient-provider retry path now honors the hinted `retry_after` window instead of always using the generic transient backoff ladder

Verification:

- `go test ./internal/gateway -count=1`
- `go test ./internal/turn -run 'Test(HandleTurnJobAsyncProjectTaskTransientAvailabilityPreflightDefersBeforePromptAssembly|HandleTurnJobAsyncProjectTaskAvailabilityProbeFallsBackOnUnhintedTransientError|HandleTurnJobAsyncProjectTaskAvailabilityProbeFallsBackOnNonRateLimitError|HandleTurnJobAsyncProjectTaskTransientProviderEnqueuesRetryWithoutSameTurnRetry|HandleTurnJobAsyncProjectTaskTransientProviderRetryCapStopsRequeue|HandleTurnJobAsyncProjectTaskRateLimitedPreflightDefersPastRetryCapWithoutTurn)$' -count=1`

What this should save:

- async turns should no longer create a fresh failed `chat_turn` / `model_invocation` when the router can already prove every eligible connection is merely still in transient recovery
- the follow-on retry should now wait for the actual connection recovery window instead of paying for a chain of `15s` transient retries that cannot succeed yet

Current live-proof status:

- code and tests are complete
- docs are updated
- runtime is now restarted on the new binary and health is green
- fresh live proof of the exact preflight-only path is still pending on the next window where all eligible connections are transiently recovering at once; immediately after deploy, Anthropic still had one `healthy` primary connection, so the router never had to return the new all-unavailable recovery-window error in production yet

## Update 03:43 MDT

The next live seam was not another task guard. It was a turn-lifecycle bug: after a task turn already emitted

- `[Turn failed: temporary model provider retries exhausted after 5 attempts.]`

the completed-turn handler was still auto-continuing the same session almost immediately, effectively resetting the retry budget and burning a brand-new turn on the same provider outage.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `HandleTurnCompletedEvent(...)` now inspects the completed turn’s system messages
  - if the turn already ended with:
    - transient model provider retries exhausted
    - or transient infrastructure retries exhausted
  - the handler now schedules the next fresh retry after `maxTransientInfraBackoff` instead of using the normal fast auto-continuation delay
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving the exhausted-provider case now defers by `maxTransientInfraBackoff`
  - preserved the normal immediate auto-continuation test so the default work-lane behavior stays covered

Verification:

- `go test ./internal/turn -run 'Test(HandleTurnCompletedEventEnqueuesAutoContinuation|HandleTurnCompletedEventDefersAutoContinuationAfterTransientProviderRetryExhausted|HandleTurnJobAsyncProjectTaskTransientAvailabilityPreflightDefersBeforePromptAssembly|HandleTurnJobAsyncProjectTaskTransientProviderEnqueuesRetryWithoutSameTurnRetry)$' -count=1`

Why this matters:

- the live sessions `ef68ce67-04f9-4295-9151-6c417fd22f4a` and `db21265f-c37d-40e4-9ed5-13def09970f8` showed the same bad shape:
  - a turn exhausted its in-turn provider retries
  - then the completed-turn event immediately minted another fresh turn on the same user message
- that is pure provider churn, not productive reasoning

Deploy status:

- code and tests are complete
- runtime restart is the next step now that the worker is on a quiet edge again

## Update 02:52 MDT

I found a real provider-routing bug while checking why `claude-swh-me` still looked dead even after its transient-failure burst should have aged out.

What was wrong:

- the in-memory gateway health checker only treats `unavailable` as a temporary probe quarantine:
  - before `recoveryReadyAt`, `GetStateKnown(...)` returns `unavailable`
  - after that backoff elapses, it degrades the connection so routing can probe it again
- but cold-start routing uses the persisted `provider_connection.health_status` row when there is no in-memory health record yet
- in [`internal/gateway/router.go`](../internal/gateway/router.go), persisted `unavailable` was being skipped unconditionally
- that meant a transient failure burst could leave a connection effectively dead after worker restart, even though the runtime only intended a short quarantine

Live evidence:

- `claude-swh-me` was still persisted as `unavailable` at `02:47 MDT`
- its metadata had no explicit retry timestamp, unlike the `rate_limited` path
- the last failures on that connection were:
  - `01:16 MDT`: normal `provider_rate_limited`
  - `02:00 MDT`: seven consecutive `provider_transient_failure` rows
- after that burst, the connection stayed persisted as `unavailable`, which made it a cold-start routing seam rather than a mere display issue

What I changed:

- [`internal/gateway/router.go`](../internal/gateway/router.go)
  - persisted `unavailable` now uses the same recovery-window logic the router already applies to expired persisted `rate_limited`
  - once `ConnectionRecoveryReadyAt(...)` has elapsed, the router treats that persisted row as `degraded` instead of hard-skipping it forever
- [`internal/gateway/router_test.go`](../internal/gateway/router_test.go)
  - added focused unit coverage proving an expired persisted `unavailable` connection is selected on cold start
- [`internal/modelgw/routing_integration_test.go`](../internal/modelgw/routing_integration_test.go)
  - added an integration proof where the only connection starts persisted as `unavailable`, the row is aged past the probe window, and the priority queue successfully routes through it and persists it back to `healthy`

Verification:

- `go test ./internal/gateway -run 'TestRouterSelectConnection(TreatsExpiredPersistedUnavailableAsDegraded|TreatsExpiredPersistedRateLimitAsDegraded|SkipsPersistedUnavailableOnColdStart)$' -count=1`
- `go test -tags=integration ./internal/modelgw -run 'TestPriorityQueue_(PersistsHealthyStatusAfterSuccessfulRecovery|SelectsExpiredPersistedUnavailableConnectionOnColdStart)$' -count=1`

Why this matters:

- it is the first concrete explanation for the live mismatch where the user believed `claude-swh-me` had usable credits but OtterCamp was behaving as if the key were still dead
- the problem was not purely quota; it was also persisted transient unavailability surviving restarts in a harsher form than intended

Deploy status:

- code complete
- tests green
- runtime restart still pending at the time of this note
- the next live proof target is the `02:58-03:01 MDT` retry wave for sessions already queued on the new worker

## Update 03:04 MDT

The persisted-unavailable provider recovery fix is now live-proven.

What happened after restart:

- the worker was restarted on the patched router before the queued `02:58-03:01 MDT` retry wave
- by `03:01:23 MDT`, provider health showed:
  - `claude-swh-me` -> `healthy`
  - `Anthropic Primary` -> `degraded`
  - `pearl-swh-me` still correctly `rate_limited`
- there were no remaining `agent_turn` jobs left in `job_queue` for the watched sessions:
  - `4e2277bb-09aa-4267-a12e-cdd92fe6587c`
  - `8b8f849f-e367-4366-8e25-6f5770fcdf62`
  - `eb57c99d-f1ab-4778-ad05-3bb55b78e575`
  - `e6d4faaa-2490-4572-8c6a-a58f5ffe27d2`
  - `db21265f-c37d-40e4-9ed5-13def09970f8`

Direct live proof:

- session `8b8f849f-e367-4366-8e25-6f5770fcdf62` produced fresh invocations on `claude-swh-me` after restart:
  - `03:01:44 MDT` completed on `claude-swh-me`
  - `03:01:47 MDT` another `claude-swh-me` invocation was already `in_flight`
- that is the exact cold-start seam we were chasing:
  - before the fix, this connection could sit persisted as `unavailable` after a restart
  - after the fix, the aged persisted row became probeable again and was actually routed in production

So this is no longer a speculative router hardening slice. It is behaving live on the real Anthropic worker.

## Update 03:09 MDT

The same live query surfaced a second, smaller correctness bug in usage persistence.

What was wrong:

- some `model_invocation` rows were `status='completed'` but still carried:
  - `error_code='provider_transient_failure'`
  - `failure_class='provider_transient'`
- example session:
  - `eb57c99d-f1ab-4778-ad05-3bb55b78e575`
- example rows:
  - `4f2306eb-f015-4190-81de-be0a044fffcb`
  - `9dc31454-c62c-4fde-95ef-b211e92ebc32`

Root cause:

- [`internal/repo/model_invocation.go`](../internal/repo/model_invocation.go)
  - `UpdateCompletion(...)` was setting `status='completed'` and token fields
  - but it was not clearing any previously written `failure_class`, `error_code`, or `error_message`
- so a row that had first been marked failed during a retry/fallback path could later complete successfully and still retain stale provider-failure metadata

What I changed:

- [`internal/repo/model_invocation.go`](../internal/repo/model_invocation.go)
  - `UpdateCompletion(...)` now clears `failure_class`, `error_code`, and `error_message`
- [`internal/repo/model_invocation_integration_test.go`](../internal/repo/model_invocation_integration_test.go)
  - added focused coverage proving a previously failed invocation loses stale failure fields once completion is persisted

Verification:

- `go test -tags=integration ./internal/repo -run 'TestModelInvocationRepo(CreateAndUpdateCompletion|UpdateCompletionClearsFailureFields)$' -count=1`

Deploy status:

- code complete
- tests green
- runtime restart for this smaller persistence-only cleanup was still pending at the time of this note

## Update 03:14 MDT

I tightened the operator view so provider health now reflects routing reality instead of raw persisted rows.

What changed:

- [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
  - `ottercamp db token-usage` provider-health rows now include:
    - `effective_health_status`
    - `recovery_ready_at`
  - those are computed with the same gateway helpers the router uses:
    - [`gateway.EffectiveConnectionHealthState(...)`](../internal/gateway/router.go)
    - [`gateway.ConnectionRecoveryReadyAt(...)`](../internal/gateway/router.go)
- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - the shell report now exposes the same two columns directly from SQL
- [`cmd/ottercamp/main_db_integration_test.go`](../cmd/ottercamp/main_db_integration_test.go)
  - CLI integration coverage still passes with the new fields present

Why this matters:

- before this slice, the raw row could say:
  - `claude-swh-me -> unavailable`
- while the router would already treat that same connection as effective `degraded` and eligible for probing once the backoff window elapsed
- that mismatch made it harder to tell whether OtterCamp was truly blocked or just waiting for the next provider probe window

Live smoke result:

- shell report now shows the more truthful shape, for example:
  - `claude-swh-me`
  - `health_status = unavailable`
  - `effective_health_status = degraded`
  - `recovery_ready_at = 2026-03-27 03:12:23 MDT`
- healthy rows no longer show meaningless synthetic recovery timestamps

Verification:

- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`
- `bash -n scripts/token-usage-report.sh`
- live smoke:
  - `go run ./cmd/ottercamp db token-usage --hours 1 --limit 5 --output json | jq '.provider_health'`
  - `./scripts/token-usage-report.sh --hours 1 --limit 5`

Deploy status:

- this is operator-surface only; no runtime restart was needed for it
- the remaining undeployed runtime slice is still the `model_invocation` completion cleanup that clears stale failure metadata on successful retries
- I deferred that restart because fresh Anthropic `agent_turn` invocations became active again while I was checking the deployment window

## Update 02:15 MDT

I found the next narrow deliverable-targeting bug in fresh live traffic and patched it.

What happened live:

- the repaired task-10 review retry prompt is now correct in the database:
  - session `8b8f849f-e367-4366-8e25-6f5770fcdf62`
  - message `17` now starts with:
    - `Start with the preferred deliverable target \`results/review-path-validation-summary.md\``
- that lane did not yet prove the new target in-model because the first post-repair retry still died in provider backoff:
  - turn `7a01899f-1772-45e1-82ca-4134e80293f6`
  - failed at `02:00:23 MDT`
  - no fresh assistant/tool step after the repaired prompt
- the hottest actual completed task turn in the last hour was task-13 session `eb57c99d-f1ab-4778-ad05-3bb55b78e575`
  - on that turn, the model attempted to write helper path `gen_script.py`
  - runtime silently redirected the write into `scripts/validate-metrics-alerting.sh`
  - the resulting file contained Python generator code inside the bash deliverable
  - the lane then had to spend extra rounds reading the file back, explaining the bad redirect, replacing the whole body with `file.edit`, rereading it again, and only then running the real validation script

What I changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - narrowed `shouldRewriteTaskFileWritePath(...)`
  - the runtime still rewrites obvious same-kind aliases onto the intended deliverable
  - but it now skips the rewrite when both the attempted path and the target path have concrete file extensions and those extensions differ
  - practical effect:
    - `.md -> .md` canonical document rewrites still work
    - helper-file shapes like `.py -> .sh` no longer silently retarget into the deliverable
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added `TestHandleTaskFileWriteWrongPathSkipsExtensionMismatchRewrite`
  - this reproduces the exact live task-13 family:
    - attempted `gen_script.py`
    - deliverable `scripts/validate-metrics-alerting.sh`
    - rewrite must now be skipped

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(HandleTaskFileWriteWrongPath(SkipsExtensionMismatchRewrite|SkipsCrossArtifactFamilyRewrite|SkipsScriptToConfigRewrite|PrefersSessionDeliverableTargetOverInferredReportPath)|RecoverySynthesizedFileWriteTargetPathPrefersSessionDeliverableTargetOverInferredReportPath|NormalizeRecoveryCheckpointTargetForTask.*|SessionTaskDeliverablePathPrefersCheckpointTargetOverInferredReportPath)$' -count=1`

Why this is the right cut:

- the earlier family-level rewrite narrowing already prevented strong cross-artifact redirects like `scripts/ -> config/`
- this patch removes the next damaging silent retarget case without backing out the helpful canonical alias behavior
- it should reduce “write helper, repair deliverable, reread, explain, rerun” waste without making normal deliverable writes harder

Current proof state:

- code and tests are complete
- runtime restart was the next step after this note
- live proof is still pending the next fresh task lane that would otherwise try a helper-file write with a mismatched extension

## Update 02:35 MDT

I found and fixed the next queue/runtime interaction bug while waiting on the repaired task retries.

What happened live:

- task-10 still did not reach a fresh model turn by `02:32 MDT`
  - the repaired review prompt is still correct and still points at:
    - `results/review-path-validation-summary.md`
  - but the lane only appended another:
    - `[Rate limited, retrying in 30m6s...]`
  - there was still no new `chat_turn`, which confirms the cooldown preflight is continuing to defer before turn creation
- while checking other pending async lanes, I found exactly one session with a stale `pending current_turn` older than 15 minutes:
  - session `4e2277bb-09aa-4267-a12e-cdd92fe6587c`
  - pending turn `2a38334b-9102-4dd2-ab71-1d5cf69120b9`
  - trigger message `87ce7109-c403-4cf0-82cb-6d29a0bb8c17`
  - content `supervisor recovery: resume task`
  - newer queued retry job already existed for that same message at higher `retry_count`
- after I cleared that one stale row operationally, the worker immediately dead-lettered the old retry as a stale orphan and minted a fresh retry job:
  - old job `933604f2-51b6-408f-b98d-7eecfe7b3660` -> `dead_letter`
  - new job `3d92c8cc-542f-434f-9355-528a12cefddd` -> `pending`
  - same message `87ce7109-c403-4cf0-82cb-6d29a0bb8c17`
  - new `retry_count=2`

Root cause:

- the stale row was not coming from the worker claim filter
- it came from two legitimate features interacting badly:
  - supervisor stranded-execution recovery in [`internal/controlplane/stranded_execution.go`](../internal/controlplane/stranded_execution.go) intentionally precreates a `pending` current turn for `supervisor recovery: resume task`
  - the newer cooldown preflight in [`internal/turn/engine.go`](../internal/turn/engine.go) now returns before `CreateForMessageAttempt(...)`
- when the provider is already rate limited, that preflight correctly enqueues the retry but previously left the already-precreated pending supervisor-recovery turn behind as `current_turn_id`

What I changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - expanded the `turnRepository` contract to include `SetFailed(...)`
  - added `retireDeferredPendingCurrentTurn(...)`
  - `handlePreTurnRateLimitedAvailability(...)` now:
    - enqueues the delayed retry
    - then checks whether the session already has a matching `pending` current turn for the same `message_id` and `retry_count`
    - if so, it marks that never-started turn failed and clears `current_turn_id` before appending the session-scoped rate-limit message
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added `TestHandleTurnJobAsyncProjectTaskRateLimitedPreflightRetiresMatchingPendingCurrentTurn`
  - extended the fake turn repo/service with `SetFailed(...)` support so the regression matches the production path

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(HandleTurnJobAsyncProjectTaskRateLimitedPreflight(DefersBeforePromptAssembly|DefersPastRetryCapWithoutTurn|RetiresMatchingPendingCurrentTurn)|HandleTurnJobAsyncOrganizationRateLimitedPreflightDefersPastRetryCapWithoutTurn)$' -count=1`

Why this matters:

- the original cooldown preflight fix removed throwaway turn creation for normal async retries
- supervisor recovery was the last path that could still strand a never-started pending turn behind that same defer-before-turn-creation behavior
- this patch closes that gap without weakening the preflight itself

Current live proof state:

- the stale supervisor-recovery row was repaired operationally before the code fix was deployed
- so the new retirement helper is test-green and ready for runtime restart, but not yet freshly live-proven on a new supervisor-recovery cooldown cycle

## Update 01:36 MDT

I traced the next recovery seam to an engine/native target mismatch on task `10`.

What was wrong:

- native task/file guards had already established the concrete deliverable target
  - `results/review-path-validation-summary.md`
- but several engine runtime paths were still preferring the generic inferred execution-first report target
  - `Work/OC-10-WORKSTREAM-B-REVIEW-PATH-VALIDATION.md`
- that meant the same async task lane could:
  - have native `deliverable_path_required` errors pointing at `results/...`
  - while engine recovery and file-write rewrite logic kept steering back toward `Work/...`

Why that happened:

- engine-side `latestRecoveryTargetPathForSession(...)` only looked for `[Recovery resume state]` system messages with `Target file: ...`
- native-side `latestRecoveryTargetPathForSession(...)` was broader and already accepted:
  - review prompt lines with `Start with the preferred deliverable target ...`
  - recent `tool_result` payloads carrying `deliverable_path`
- separately, several engine runtime paths consulted `preferredTaskDeliverablePath(taskRecord)` first, which can still infer a generic `Work/OC-...md` report path for execution-first tasks

What I changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - broadened `latestRecoveryTargetPathForSession(...)` to also recognize:
    - review prompt preferred-target lines
    - recent `tool_result.output.deliverable_path`
    - recent `file.read` / `file.write` tool-result paths
  - added `sessionTaskDeliverablePath(...)` so runtime task-lane logic prefers:
    - explicit task deliverable path
    - then session-discovered concrete target
    - then generic inferred fallback
  - switched these runtime paths to that session-aware target:
    - task `file.write` wrong-path rewrite
    - recovery synthesized `file.write` target selection
    - recovery file-output context
    - review prompt preferred target
    - task off-target evidence guard
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused regressions proving:
    - review prompt target fallback
    - `deliverable_path` tool-result fallback
    - recovery synthesized target selection prefers session target over inferred `Work/...`
    - task-lane `file.write` rewrite prefers session target over inferred `Work/...`

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(LatestRecoveryTargetPathForSessionFallsBackTo(ReviewPromptTarget|RecentToolResultDeliverablePath)|RecoverySynthesizedFileWriteTargetPathPrefersSessionDeliverableTargetOverInferredReportPath|HandleTaskFileWriteWrongPathPrefersSessionDeliverableTargetOverInferredReportPath)$' -count=1`
- `go test ./internal/turn -run 'Test(ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified|HandleTaskFileWriteWrongPath.*|RecoverySynthesizedFileWriteTargetPath.*|LatestRecoveryTargetPathForSessionFallsBackTo(ReviewPromptTarget|RecentToolResultDeliverablePath)|HandleTurnCompletedEventBlocksRepeatedReviewFileReadNotFoundTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedRetriesWithoutReviewDecision)$' -count=1`

Status:

- code + focused tests are green
- runtime restart and live proof were still pending when I wrote this note

## Update 02:10 MDT

The first fresh `02:00 MDT` retry window exposed a second-order bug in the same area.

What the live turns showed:

- fresh task-10 retry session [`8b8f849f-e367-4366-8e25-6f5770fcdf62`](../issues/0326a-state.md) still emitted:
  - `Start with the preferred deliverable target Work/OC-10-WORKSTREAM-B-REVIEW-PATH-VALIDATION.md`
- even though a prior task-10 work session had already emitted native `deliverable_path_required` results naming:
  - `results/review-path-validation-summary.md`
- the same pattern also showed up on task `13`, where historical concrete deliverable evidence existed but the task row still carried no recovery target

Root cause:

- the prior slice fixed session-local target selection
- but `normalizeRecoveryCheckpointTargetForTask(...)` still let `preferredTaskDeliverablePath(...)` overwrite a concrete checkpoint target with the generic inferred `Work/OC-...md` fallback
- so even when a work lane persisted `deliverable_path_required -> results/...`, normalization collapsed it back to `Work/...`

What I changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `normalizeRecoveryCheckpointTargetForTask(...)` now preserves concrete non-planning checkpoint targets when there is no explicit task deliverable path
  - explicit deliverable paths still override checkpoints
  - planning-path checkpoints still collapse to the canonical preferred target
  - `sessionTaskDeliverablePath(...)` now consults the normalized checkpoint target before session-local history and generic inference
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving:
    - concrete checkpoint targets survive normalization without an explicit deliverable path
    - session deliverable selection prefers the preserved checkpoint target over inferred `Work/...`

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified|NormalizeRecoveryCheckpointTargetForTask.*|LatestRecoveryTargetPathForSessionFallsBackTo(ReviewPromptTarget|RecentToolResultDeliverablePath)|SessionTaskDeliverablePathPrefersCheckpointTargetOverInferredReportPath|RecoverySynthesizedFileWriteTargetPathPrefersSessionDeliverableTargetOverInferredReportPath|HandleTaskFileWriteWrongPath.*|HandleTurnCompletedEventBlocksRepeatedReviewFileReadNotFoundTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedRetriesWithoutReviewDecision)$' -count=1`
- rebuilt and restarted tmux `codex-e2e-20260324`
- health is green on the new binary

Operational repair applied:

- because the old normalization bug had already poisoned two active task rows, I repaired their checkpoint targets directly in the DB so future fresh continuations do not inherit stale targets:
  - task `10` `Workstream B: Review Path Validation`
    - checkpoint target repaired from `Work/OC-10-WORKSTREAM-B-REVIEW-PATH-VALIDATION.md`
    - to `results/review-path-validation-summary.md`
  - task `13` `Validate pipeline metrics and alerting hooks`
    - checkpoint target repaired from empty
    - to `scripts/validate-metrics-alerting.sh`

Current live-proof status:

- the code fix is deployed
- the task rows are repaired
- but the current pending retry for task `10` is still tied to the already-minted stale review prompt, so that specific pending turn will not prove the new target choice until a fresh continuation prompt is generated after the next retry cycle

## Update 01:02 MDT

I tightened one narrower review seam instead of adding another generic cap.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `reviewApprovalRetryPrompt(...)` no longer probes task worktree cleanliness unless the retry is actually on the dirty-workspace approval path
  - that keeps the ordinary review-retry path from doing unnecessary repo/worktree setup before deciding whether a better retry prompt is available
  - review retry prompts now detect a specific missing-tests family:
    - the task title/description explicitly requires tests
    - the latest completed review turn already proved the preferred deliverable is readable and substantive
    - the same turn then failed to verify tests via:
      - `file.read -> not_found`
      - `file.list -> not_found`
      - `recovery_target_focus_required` while probing test paths
      - or the existing repeated side-artifact blocker messages for that family
  - when that pattern is present, the next review retry prompt now says:
    - stop searching
    - call `flow.review_decision reject`
    - use the missing or inaccessible required test artifacts as evidence
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage for the new prompt branch:
    - `TestReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified`
  - reran the adjacent repeated-review blocker slice to ensure the new prompt logic does not regress the current caps

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified|HandleTurnCompletedEventBlocksRepeatedReviewFileReadNotFoundTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedRetriesWithoutReviewDecision)$' -count=1`

Why this slice exists:

- the next hot review session after the earlier `file.read -> not_found` cap was `306cc2b2-de58-46ad-b3ae-bca729d1c131`
- that task explicitly required tests
- the review model could read the primary config deliverable successfully
- but then drifted through missing/inaccessible test paths and recovery-focus blockers instead of turning that evidence into `flow.review_decision reject`
- this change keeps the review lane evidence-based, but shortens that specific retry path:
  - the model does not need more file discovery once it already knows the required tests are missing or unverifiable

Deploy status:

- code complete
- focused tests green
- runtime rebuilt/restarted on the latest binary
- `./bin/ottercamp health --output json` returned `status=ok`

Live proof:

- fresh review session `d77dafe1-5ab5-47e7-98a4-0197ea5bdb9d` for task `14` (`Validate config loading and environment overrides`) received the new retry prompt at message `956c40e9-7e4d-424c-9f29-9c332fe134f0`
- that prompt includes the new reject-with-evidence wording verbatim:
  - the preferred deliverable is already present and substantive
  - the task explicitly requires tests
  - the required test coverage or related test artifacts could not be verified
  - stop searching and call `flow.review_decision reject`
- the follow-on review turn in that exact session was superseded by session turnover before it could settle
- but the task-14 lane then advanced immediately into fresh session `4e2277bb-09aa-4267-a12e-cdd92fe6587c`, where the runtime-visible commit history already includes:
  - `flow(review:review#43): reject`
  - commit `17223cd142debf7e9489d714d08c731b95237e61`

What this means:

- the new branch is not just unit-tested; it is live and issuing the intended review retry guidance on the real hot task-14 lane
- the live sequence also shows that task `14` moved through a real review rejection immediately after that retry generation instead of remaining stuck in the older test-artifact search loop

## Update 01:21 MDT

The next hot seam after that review-retry fix was not another generic review loop. It was recovery target drift onto trivial package-marker files.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - recovery scoring now penalizes low-signal package-marker targets like `tests/__init__.py`
  - historical read-path fallback now treats those package markers like `planning/*` paths:
    - they can still be observed
    - but they should not anchor recovery if better task-aligned files exist
  - recovery checkpoint normalization now clears those package-marker targets unless the task explicitly requested that path
  - stale `recovery_target_focus_required` validation guards now invalidate on those package-marker targets too, instead of preserving them as if they were authoritative deliverables
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving:
    - explicit deliverable metadata replaces a `tests/__init__.py` recovery target
    - package-marker recovery targets invalidate the stale blocked guard path
    - package-marker paths are fallback-only in historical read scoring
    - substantive test files outrank package markers in recovery target scoring

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(NormalizeRecoveryCheckpointTargetForTask(ReplacesPackageMarkerWithExplicitDeliverable|RepointsPlanningTargetForCanonicalWorkTask)|ValidationLoopBlockerForSessionClears(PackageMarkerRecoveryTargetFocusGuard|StaleRecoveryTargetFocusGuard)|Recovery(HistoricalReadPathShouldFallbackForPackageMarker|TaskTargetPathScorePenalizesPackageMarkerPath)|ReviewApprovalRetryPromptRejectsWhenRequiredTestsCannotBeVerified|HandleTurnCompletedEventBlocksRepeatedReviewFileReadNotFoundTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedRetriesWithoutReviewDecision)$' -count=1`

Why this slice exists:

- fresh task `14` work turns were still getting pinned to `tests/__init__.py`
- that low-value target then produced:
  - repeated `recovery_target_focus_required`
  - rereads of side artifacts
  - another avoidable continuation cycle even after the review side had already rejected correctly
- the goal here is not to force a specific target blindly
- it is to stop trivial package markers from masquerading as the recovery anchor when the lane already has stronger concrete files

Deploy status:

- code complete
- focused tests green
- runtime rebuild/restart still pending at the time of this note

## Update 00:33 MDT

I closed the remaining uncertainty on the task-worktree fix and turned it into an operator-visible report.

What I proved:

- the shared resolver is not the problem
- a direct probe through `cli.Executor.resolveExecutionRoot(...)` against the live project/task rows returned:
  - task `13` -> `/Users/sam/otter-data/task-worktrees/speaker-pipeline-ops-validation-fresh-20260325-rerun-7-restart-9/task-13`
  - task `16` -> `/Users/sam/otter-data/task-worktrees/speaker-pipeline-ops-validation-fresh-20260325-rerun-7-restart-9/task-16`
- fresh production `cli_execution` rows then confirmed the live worker is now using that task root:
  - `6de717c8-664a-4705-95b6-425c25b1da3e` at `2026-03-27 00:27:46 MDT`
  - `3f944857-c17f-4f39-b445-826ba915b030` at `2026-03-27 00:27:53 MDT`
  - both for task `16`, both with `working_directory=/Users/sam/otter-data/task-worktrees/.../task-16`

What that means:

- the earlier `project_workspace` rows were mixed pre-cutover traffic, not evidence that the new resolver path was still broken
- the product bug is fixed live now
- the remaining value is observability, not another runtime change on this seam

What I changed:

- [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
  - `ottercamp db token-usage` now includes `task_cli_working_directory_roots`
  - it groups recent `cli_execution.working_directory` values into:
    - `task_worktree`
    - `project_workspace`
    - `other`
- [`cmd/ottercamp/main_db_integration_test.go`](../cmd/ottercamp/main_db_integration_test.go)
  - integration coverage now inserts both a task-worktree row and a project-workspace row and asserts both appear in the JSON report
- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now prints the same root-kind rollup in the shell report

Verification:

- `bash -n scripts/token-usage-report.sh`
- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`
- `scripts/token-usage-report.sh --hours 1 --limit 8`
- `go run ./cmd/ottercamp db token-usage --hours 1 --limit 8 --output json`

Live proof:

- shell report now shows both roots in the last hour:
  - `project_workspace | 152 | 3 | 2026-03-27 00:27:06 MDT`
  - `task_worktree | 4 | 1 | 2026-03-27 00:33:25 MDT`
- the JSON report shows the same split under `task_cli_working_directory_roots`

Why this matters:

- it converts a previously hand-debugged runtime seam into a first-class operator signal
- it gives us an immediate canary for whether task-lane shell/file alignment stays healthy after future changes
- it also prevents us from chasing old rows and mixed restart windows as if they were fresh regressions

## Update 00:39 MDT

I added one more operator slice so the next guardrail decision is based on the actual live blocker text, not just stop-reason counts.

What changed:

- [`cmd/ottercamp/main.go`](../cmd/ottercamp/main.go)
  - `ottercamp db token-usage` now includes `recent_validation_loop_blocks`
  - each row shows:
    - `turn_id`
    - `session_id`
    - `scope_type`
    - `mode`
    - the latest system-message `block_excerpt`
    - `completed_at`
- [`cmd/ottercamp/main_db_integration_test.go`](../cmd/ottercamp/main_db_integration_test.go)
  - the integration fixture now inserts a synthetic validation-loop block system message
  - the JSON report test asserts that `recent_validation_loop_blocks` is present and carries the blocker excerpt
- [`scripts/token-usage-report.sh`](../scripts/token-usage-report.sh)
  - now prints the same `Recent Validation-Loop Blocks` section in the shell report

Verification:

- `gofmt -w cmd/ottercamp/main.go cmd/ottercamp/main_db_integration_test.go`
- `bash -n scripts/token-usage-report.sh`
- `go test -tags=integration ./cmd/ottercamp -run 'TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- live smoke on both:
  - `./bin/ottercamp db token-usage --hours 2 --limit 8`
  - `scripts/token-usage-report.sh --hours 2 --limit 8`

Live read right after landing:

- the dominant current blockers are no longer guesswork
- the newest rows show:
  - repeated `file.read` validation failures with `not_found`
  - review turns halted after repeated empty outputs / missing `flow.review_decision`
  - `file.list` failures with `recovery_target_focus_required`
  - `file.write` failures with `non_substantive_content`

Why this matters:

- the top aggregate bucket is still `project_task async -> validation_loop_blocked`
- before this slice, we could count those turns but still needed ad hoc SQL to know what they actually were
- now the report itself tells us which blocker family is hot enough to justify the next runtime cut

## Update 00:45 MDT

I used the new blocker report immediately and cut the first follow-on runtime seam it exposed.

What changed:

- [`internal/turn/engine.go`](../internal/turn/engine.go)
  - review lanes now track consecutive turns that already ended with:
    - `[Repeated identical file.read validation failure in this turn (2/3): not_found ...]`
  - after `3` consecutive review turns with that exact blocker, the third turn now blocks immediately instead of queuing a fourth near-identical review retry
  - this is review-only and exact-message-bound; it does not alter normal task work lanes or generic `file.read` handling
- [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - added focused coverage proving the new repeated `file.read -> not_found` review cap
  - preserved the existing empty-review-output cap and generic repeated-retries review block behavior

Verification:

- `gofmt -w internal/turn/engine.go internal/turn/engine_test.go`
- `go test ./internal/turn -run 'Test(HandleTurnCompletedEventBlocksRepeatedReviewFileReadNotFoundTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedEmptyReviewTurnsAcrossSession|HandleTurnCompletedEventBlocksRepeatedRetriesWithoutReviewDecision)$' -count=1`

Why this slice exists:

- the new `Recent Validation-Loop Blocks` report showed session `3dbbeb30-3359-4e5b-b6a0-e15b419fccae` burning:
  - turn `28a683c0-...`
  - turn `ffe8b62e-...`
  - turn `9f75e38b-...`
  - all on the same review pattern:
    - valid primary deliverable read
    - then repeated secondary `file.read -> not_found`
  - before turn `c137f202-...` finally hit the older repeated-retries block

What this should save:

- one full extra review retry turn for that exact family
- without making the broader review path more brittle

Deploy status:

- code complete
- tests green
- runtime restart still pending at the time of this note
