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
