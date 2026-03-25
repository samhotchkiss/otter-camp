# Codex State

Local-only handoff for restarting work in `/Users/sam/dev/otter-camp`.

## 2026-03-25 14:32 MDT update

- deployed runtime is now local patch-on-top of pushed commit `f4721c1b`
- tmux runtime remains `codex-e2e-20260324`
- health is green after the latest rebuild/restart
- the fresh project-session stale retry loop is narrowed again
- the newest live fix is in worker claim budgeting, not bootstrap heuristics

### New local patch: retried project/org session turns now ignore live or just-completed invocations before stale-leak recovery

- root cause:
  - `internal/turn/engine.go`
  - `recoverRetriedAgentTurnLeak(...)` and `recoverRetriedSessionCurrentTurnLeak(...)` treated any `attempts > 1` claimed job as proof that the current in-progress project/org continuation turn was stale
  - they did not check whether the turn still had a live model invocation or a just-completed invocation inside the same post-model grace window already used by worker cleanup
- behavior:
  - non-task async session leak recovery now calls a shared `turnHasLiveOrRecentCompletedInvocation(...)` guard before failing/retrying the current turn
  - this makes project/org continuation recovery consistent with the existing project-task stale inbound-turn guard
- code:
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
- focused verification:
  - `go test ./internal/turn -run 'Test(ProjectBootstrapWatchdogTimeoutForModel|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation|RecoverRetriedAgentTurnLeakKeepsRecentCompletedInvocation)$' -count=1`

### New local patch: project continuation claim budget now ignores in-flight invocations already attached to failed turns

- root cause:
  - `claimPendingByFilter(...)` computed `project_async_budget` from all project-scope `model_invocation.status='in_flight'` rows
  - live DB proof showed one of those rows belonged to a project turn already marked `failed`, so it still consumed one of the four project continuation slots and starved fresh bootstrap continuations
  - on rerun-60, the fresh project bootstrap dispatch `41c2b147-d39d-476d-ac1c-a3f9091ab116` stayed `pending` with no current turn purely because the budget had fallen to `0`
- behavior:
  - project continuation budget now counts only:
    - in-flight project invocations with no bound turn yet, or
    - in-flight project invocations whose bound turn is still `pending` or `in_progress`
  - stale in-flight rows on terminal project turns no longer block fresh project bootstrap/continuation claims
- code:
  - [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
- focused verification:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerClaimPendingAgentTurns(CapsConcurrentProjectContinuations|IgnoresInFlightProjectInvocationOnFailedTurn)$' -count=1`

### Live proof on rerun-60

- fresh direct project canary:
  - project `2fd1f96a-eb29-4768-bc56-31ac234c30b1`
  - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-60`
  - async project session `d4d47045-01bc-4b7a-843b-f2e4383c3a91`
- pre-fix blocker:
  - pending bootstrap dispatch `41c2b147-d39d-476d-ac1c-a3f9091ab116` remained unclaimed
  - session had `current_turn_id = NULL`
  - worker logs showed `no pending jobs` even though the dispatch row was still `pending`
  - direct DB inspection showed `project_async_budget` was exhausted by 4 project-scope `in_flight` invocations, including stale row:
    - invocation `d09718cb-827c-4f8c-85e4-b8be82b1b67c`
    - session `72d87248-251c-4b9e-936f-b60edd78195a`
    - turn `f99978a9-2f3d-43e8-b16f-aaa49a2cb399`
    - invocation `status = in_flight` while turn `status = failed`
- post-fix live proof after redeploy:
  - worker immediately claimed rerun-60 dispatch `41c2b147-d39d-476d-ac1c-a3f9091ab116`
  - it created fresh turn `cfa24fc7-f48c-4e77-bee3-8295f1efb9ea`
  - session `d4d47045-01bc-4b7a-843b-f2e4383c3a91` now has `current_turn_id = cfa24fc7-f48c-4e77-bee3-8295f1efb9ea`
  - live invocation `cf93c11f-f99a-469e-9da3-9214294f58c4` is `in_flight` on `qwen2.5:72b`
- remaining seam:
  - at this checkpoint the duplicate kickoff handoff seam still looked open, but later live investigation narrowed it to an operational multi-worker problem, not just the bootstrap code path
  - the starvation seam is fixed: the surviving bootstrap job is now claimable and actively running

## 2026-03-25 14:42 MDT update

- latest local runtime is now patch-on-top of pushed commit `664ff7bd`
- health is green after another rebuild/restart
- rerun-62 proved the duplicate kickoff-handoff symptom disappears when only one live worker process remains

### New local patch: bootstrap kickoff seeding now has a session-level single-winner guard

- code:
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
- behavior:
  - `maybeSeedProjectBootstrapKickoffFromHumanMessage(...)` now calls `tryActivateProjectBootstrapKickoff(...)` before appending the first synthetic kickoff handoff
  - the guard flips `project_bootstrap.status=active` on the session row only if it was not already active, so late duplicate consumers lose the race before they can append another kickoff handoff
- focused verification:
  - `go test ./internal/turn -run 'Test(ProjectBootstrapWatchdogTimeoutForModel|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation|RecoverRetriedAgentTurnLeakKeepsRecentCompletedInvocation|TryActivateProjectBootstrapKickoffIsSingleWinner)$' -count=1`

### Live root cause: duplicate kickoff handoffs were being driven by multiple worker processes

- direct process inspection showed two extra worker processes were still alive outside the current tmux pane:
  - stale worker `53734`
  - stale worker `56814`
  - active tmux worker `93337`
- that explained rerun-61:
  - fresh project `f6165049-70d8-45b4-bfdf-5012ab053b7a`
  - session `6e05b77d-154a-43a8-ab68-414483fd9e99`
  - original user request `65162dda-e876-47e0-b09b-77a156127b9d`
  - duplicate kickoff handoffs:
    - `618f5954-d014-47a9-ac70-9575a1c83e62`
    - `ced49a65-7250-47fa-80fe-fa7fb6e35f40`
  - both handoffs were authored by the same Frank agent id and emitted within milliseconds, consistent with duplicate event consumption

### Live proof after removing stray workers

- killed stale workers `53734` and `56814`
- rerun-62 fresh project:
  - project `4b4431da-0f3d-47bc-8474-c71736f7881f`
  - session `60c80309-8f66-4d90-b53b-86113b6bcca7`
  - original user request `605095e9-a187-4f21-83bc-146cd9f337d1`
- clean outcome under a single live worker:
  - exactly one kickoff handoff message exists:
    - `8a15ed24-981f-4267-afec-d125079d1ca1`
  - there is no second `project_bootstrap` handoff message on the session
  - current turn is active:
    - `0fa698b6-f76f-4d56-b3b9-af40ff168386`
- conclusion:
  - the fresh duplicate kickoff symptom is not reproducible once the stale extra worker processes are removed
  - the single-winner guard remains a reasonable defensive hardening, but the live symptom was primarily operational multi-worker drift

## 2026-03-25 14:56 MDT update

- deployed runtime is now actually rebuilt from the current tree and restarted cleanly
- tmux runtime remains `codex-e2e-20260324`
- live `serve` / `worker` start time is `2026-03-25 14:48:57 MDT`
- health is green after the rebuild/restart
- the old `1m30s` bootstrap watchdog seam is no longer reproducing on the fresh live canary

### Important deployment correction

- rerun-63 was not a trustworthy proof of the watchdog fix because the worker process was still an old binary:
  - worker start time was `13:03:32 MDT`
  - worker startup banner warned:
    - `binary commit 664ff7bd... != HEAD 8c9b603e...`
- I rebuilt `./bin/ottercamp` at `14:48:50 MDT` and restarted both panes:
  - `65627 ./bin/ottercamp serve`
  - `65629 ./bin/ottercamp worker --concurrency 24`
- after that restart, the stale-binary mismatch warning disappeared; only the expected dirty-worktree warning remains because unrelated tracked changes are still present in the repo

### Fresh live canary: rerun-65 bootstrap lane is active on the real deployed build

- project:
  - `658df263-20c5-43cf-8cc8-72cc0a2bf0dc`
  - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-65`
- note on create surface:
  - direct `project create` still seeds the canonical bootstrap task tree (`8` tasks) but does not create an async project session by itself
  - to keep the operator run moving, I used the normal product surface to create the async project session explicitly and then sent the canonical Frank bootstrap handoff into that session
- session / kickoff state:
  - async project session `f4185138-9bf9-4e12-8dde-413ef4fb1a47`
  - bootstrap handoff message `d45985dc-bad6-4eff-9e80-9e5eb8ef2a23`
  - kickoff job `5f963302-da3d-469d-a356-dc5d41473cac`
  - kickoff turn `7fd11bed-8eba-4c68-8f72-050fa81583d0`
  - invocation `75758be1-11be-4308-9c87-32a562ab9fb4`
  - resolved agent `271c2b3e-e44c-42da-962b-9cab89110d81`

### Live proof: the old 90-second watchdog seam is broken

- at `14:56:02 MDT`, which is already past the old `1m30s` failure boundary for this turn:
  - session `f4185138-9bf9-4e12-8dde-413ef4fb1a47` was still `active`
  - turn `7fd11bed-8eba-4c68-8f72-050fa81583d0` was still `in_progress`
  - invocation `75758be1-11be-4308-9c87-32a562ab9fb4` was still `in_flight`
- by `14:56:43 MDT`, the session metadata had advanced further:
  - `project_bootstrap.status = active`
  - `project_bootstrap.current_phase = staffing_persisted`
- so the prior failure mode:
  - `bootstrap setup watchdog timed out after 1m30s with zero persisted staffing drafts...`
  is no longer the current live blocker on the fresh rebuilt deployment

### Current live seam

- rerun-65 bootstrap is still in progress; it has not yet completed or exposed the next hard runtime failure
- there is still background noise from older project sessions:
  - worker periodic cleanup continues to log `requeued active project sessions without turns`
  - those old lanes are no longer starving the fresh rerun-65 bootstrap lane, but they are still active background churn
- the next concrete checkpoint is:
  - either first real persisted bootstrap materialization beyond `staffing_persisted`
  - or the next longer-timeout/runtime seam if this turn eventually stalls again later

### Current next step

- keep rerun-65 running on the clean deployed stack
- capture the first post-90-second real seam or the first successful bootstrap completion checkpoint
- if no new product seam appears, proceed from rerun-65 into the next Plan 0325B verification phase

## 2026-03-25 15:00 MDT update

- rerun-65 is still the active proof target
- the fresh bootstrap-resume turn is alive; the lane has not reproduced the old watchdog seam
- no code changes were made in this slice; this is a live-state checkpoint only

### Live rerun-65 state at 15:00 MDT

- project:
  - `658df263-20c5-43cf-8cc8-72cc0a2bf0dc`
  - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-65`
- async project session:
  - `f4185138-9bf9-4e12-8dde-413ef4fb1a47`
- kickoff turn completed successfully:
  - turn `7fd11bed-8eba-4c68-8f72-050fa81583d0`
  - invocation `75758be1-11be-4308-9c87-32a562ab9fb4`
- active bootstrap-resume turn:
  - turn `bf38eb1a-b4dd-4241-b9f9-7a164336b95b`
  - trigger message `7bb0c1c5-d53a-49d1-ad5b-40ed04c4c6f9`
  - invocation `0bb28f4e-9757-4a3b-97f1-4c1d55dacaf8`
  - model `qwen2.5:72b`
  - status `in_progress` / `in_flight`

### Persisted bootstrap state

- session remains `active`
- `project_bootstrap.status = active`
- `project_bootstrap.current_phase = staffing_persisted`
- `project_bootstrap.last_successful_checkpoint = project_created`
- current task tree:
  - task `2` (`Bind repo and environment`) is `done`
  - tasks `1` and `3-7` remain `draft` and are assigned to bootstrap agent `271c2b3e-e44c-42da-962b-9cab89110d81`
  - task `8` remains `draft` and is assigned to Frank `391aa626-d434-4e68-81ff-af29c55a8a20`
- active project-scoped flow template still exists
- no `agent_project_assignment` rows yet

### Current seam

- the fresh lane is no longer failing at the old `1m30s` bootstrap watchdog boundary
- the current question is whether the live bootstrap-resume turn eventually persists staffing/materialization or stalls later in the resume path
- background worker churn from older project sessions still exists, but it is not starving rerun-65 at this checkpoint

## 2026-03-25 15:03 MDT update

- latest local runtime is now patch-on-top of pushed commit `8c9b603e`
- rerun-65 exposed the next real bootstrap seam cleanly
- a new watchdog-policy fix is in code with focused `internal/turn` coverage and is ready to deploy

### New local patch: bootstrap watchdog now gives materially longer time to slow/local model families during setup materialization

- root cause:
  - `internal/turn/engine.go`
  - `projectBootstrapWatchdogTimeoutForModel(...)` still applied a flat `max(base, 4m)` timeout to every async bootstrap turn
  - live rerun-65 proved that this was still a false-positive timeout for qwen-backed bootstrap resume: kickoff succeeded in `184s`, but the very next bootstrap-resume invocation was still `in_flight` at `4m0s` and got failed even though no provider/runtime crash had occurred
  - historical live data in the same DB shows at least one qwen bootstrap invocation that ran `925s`, which makes a universal `4m` cap incompatible with the current local-model runtime
- behavior:
  - async bootstrap turns now use:
    - `20m` floor for clearly slow/local model families (`qwen`, `mistral`, `llama`, `gemma`, `deepseek`)
    - `8m` floor for the remaining model families
  - larger explicit configured base timeouts still win unchanged
- code:
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
- focused verification:
  - `go test ./internal/turn -run 'Test(ProjectBootstrapWatchdogTimeoutForModel|HandleCompletedProjectExecutionContinuationTurnConsumesBoundedSizeQueueFailure|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation|RecoverRetriedAgentTurnLeakKeepsRecentCompletedInvocation)$' -count=1`

### Live proof of the seam on rerun-65

- original project:
  - `658df263-20c5-43cf-8cc8-72cc0a2bf0dc`
  - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-65`
- bootstrap-resume failure:
  - session `f4185138-9bf9-4e12-8dde-413ef4fb1a47`
  - turn `bf38eb1a-b4dd-4241-b9f9-7a164336b95b`
  - invocation `0bb28f4e-9757-4a3b-97f1-4c1d55dacaf8`
  - failed at `15:01:38 MDT`
  - failure:
    - `bootstrap setup watchdog timed out after 4m0s with zero persisted staffing drafts, project assignments, scoped tasks, or flow templates; model invocation 0bb28f4e-9757-4a3b-97f1-4c1d55dacaf8 remained in_flight`
- project metadata after failure:
  - `project_bootstrap.status = failed`
  - `project_bootstrap.current_phase = staffing_persisted`
  - `last_successful_checkpoint = project_created`
- automatic restart lane created by product:
  - restart project `2ed2fcef-b110-4171-bd50-4b574a059a2f`
  - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-65-restart`
  - restart session `16b95db3-58bf-4e6d-905c-a75fe2c6d1dc`
  - restart turn `ac270264-4c1d-4bb1-853b-be37411a70a0`
  - restart invocation `d64052eb-19ac-430f-80a1-38c76554754b` is now the fresh live bootstrap target

### Current next step

- commit and push this watchdog-policy slice
- rebuild/restart tmux `serve` and `worker`
- keep the fresh restart lane (`rerun-65-restart`) under observation on the new timeout policy
- if that lane still fails before materializing staffing, capture the next bootstrap-progress seam rather than treating timeout policy as the blocker anymore

## 2026-03-25 15:12 MDT update

- commit `9ab856c0` is now pushed to `origin/main`
- tmux `serve` / `worker` were rebuilt and restarted on that commit at `15:05 MDT`
- the next live seam is not the bootstrap timeout anymore; it is restart recovery for inherited async project invocations

### Deployed checkpoint

- pushed commit:
  - `9ab856c0` `Extend bootstrap watchdog for slow models`
- live processes after redeploy:
  - `88670 ./bin/ottercamp serve`
  - `88679 ./bin/ottercamp worker --concurrency 24`
- `./bin/ottercamp health` is green on this deployment

### New local patch: worker startup cleanup now treats inherited pre-start async invocations as orphaned

- root cause:
  - after the redeploy, restart session `16b95db3-58bf-4e6d-905c-a75fe2c6d1dc` kept:
    - turn `ac270264-4c1d-4bb1-853b-be37411a70a0`
    - invocation `d64052eb-19ac-430f-80a1-38c76554754b`
    - status `in_progress` / `in_flight`
  - there was no `run` row and no worker claim activity, but `FailStaleModelInvocations(...)` would still wait for the generic `15m` continuation threshold because the session remained active/current and the invocation still looked `in_flight`
  - on a worker restart, those pre-start invocations are inherited from a dead process and should be reclaimed promptly, not left wedged for a quarter hour
- behavior:
  - `internal/jobqueue/worker.go`
  - `Worker` now records `startupAt`
  - stale-model cleanup now treats async invocations created meaningfully before the current worker startup (`startupAt - claimedAgentTurnHeartbeatGrace`) as inherited/orphaned candidates, so startup cleanup can fail and clear them promptly
  - invocations started by the current worker are still protected by the normal thresholds
- code:
  - [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
- focused verification:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerFailStaleModelInvocations(RequeuesTriggeredProjectSession|RecoversInheritedAsyncProjectInvocationAfterWorkerRestart|SkipsActiveAsyncOrganizationSession)$' -count=1`

### Current live target

- restart project:
  - `2ed2fcef-b110-4171-bd50-4b574a059a2f`
  - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-65-restart`
- restart session:
  - `16b95db3-58bf-4e6d-905c-a75fe2c6d1dc`
- pre-fix wedged state captured at `15:06 MDT`:
  - current turn `ac270264-4c1d-4bb1-853b-be37411a70a0`
  - inherited invocation `d64052eb-19ac-430f-80a1-38c76554754b`
  - no `run` row
  - no worker claim activity for that session after restart

### Current next step

- commit and push this worker restart-recovery slice
- rebuild/restart the worker on top of `9ab856c0`
- confirm the inherited rerun-65-restart invocation is failed/cleared quickly and the session gets a fresh retry under the current build
- then continue the fresh restart lane until the next real bootstrap-progress seam appears

## 2026-03-25 14:12 MDT update

- deployed runtime is now local patch-on-top of pushed commit `76537eb9`
- tmux runtime remains `codex-e2e-20260324`
- health is green after the latest rebuild/restart
- the old bootstrap tool-set conflict seam is fixed in code with focused tests
- the current live blocker is now fresh project-session handoff dispatch: the queued Frank handoff jobs are being suppressed/purged before a turn starts

### Live proof: the earlier watchdog heuristic slice was still insufficient

- clean rerun-56 project:
  - project `8cd0efe8-cab7-4dc6-8848-702f4993ea1b`
  - session `bf0fc527-28f6-49e5-ba21-546a61699058`
  - turn `a8602608-1187-40dc-a2d0-cc79c676a645`
  - invocation `95c72b71-8999-44d7-8790-ee2b2672f667`
- result:
  - the very first bootstrap turn still failed at `14:04:39 MDT`
  - failure reason: `bootstrap setup watchdog timed out after 1m30s with zero persisted staffing drafts, project assignments, scoped tasks, or flow templates; model invocation 95c72b71-8999-44d7-8790-ee2b2672f667 remained in_flight`
- conclusion:
  - the model-name heuristic from commit `76537eb9` was not sufficient in live traffic

### New local patch: bootstrap watchdog now uses a hard 4-minute floor for all async bootstrap turns

- code:
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
- behavior:
  - bootstrap watchdog now uses `max(base, 4m)` for every async project-bootstrap turn, regardless of model name
  - this removes the unreliable model-name branch that still allowed live qwen bootstrap turns to time out at `1m30s`
- focused verification:
  - `go test ./internal/turn -run 'Test(ProjectBootstrapWatchdogTimeoutForModel|HandleCompletedProjectExecutionContinuationTurnConsumesBoundedSizeQueueFailure)' -count=1`
- live repro status:
  - not fully reproved yet because the next fresh canaries were blocked earlier by a newer handoff-dispatch seam before any bootstrap turn could start cleanly

### New local patch: session tool-set cache creation now tolerates active-row conflicts

- root cause:
  - fresh rerun-58 bootstrap session `defebd4f-538b-4be1-9682-563d1e5c3844` reached the first live bootstrap turn and then failed immediately with:
    - `getSessionToolSet: repo: conflict: duplicate key value violates unique constraint "session_tool_set_active_unique_idx"`
  - the session then closed and canceled the just-created qwen invocation
- code:
  - [`internal/tools/resolver.go`](../internal/tools/resolver.go)
  - [`internal/tools/resolver_test.go`](../internal/tools/resolver_test.go)
- behavior:
  - `ToolResolver.GetSessionToolSet(...)` now treats `repo.ErrConflict` on cache insert as a winner-loser race and re-reads the active cache row instead of failing the turn
- focused verification:
  - `go test ./internal/tools -run 'TestGetSessionToolSet(CreateConflictFallsBackToActiveCache|PropagatesCreateError|CacheLookupError|RejectsInvalidCachedJSON)' -count=1`

### Current live blocker

- fresh rerun-59 project:
  - project `deda1a3b-7f04-475c-8ef3-fc01940222f9`
  - session `16a6c236-75ab-4753-b058-6a1948ab59d8`
- current state:
  - the session has no current turn and no model invocation
  - two synthetic Frank handoff messages were created
  - one corresponding job was dead-lettered and the surviving handoff job remains/purges without ever starting a turn
- evidence:
  - session rows show:
    - `331e426b-f7dc-4766-8146-09283efcce29` -> `dead_letter`
    - `162132d1-f9d0-4fae-a83f-78dac686ed0c` -> `pending`
  - worker log repeatedly reports:
    - `job queue: suppressed duplicate active dispatch` for the rerun-59 handoff jobs
    - then `job queue: no pending jobs`
    - later `job queue: purged stale agent_turn jobs`
- conclusion:
  - the next real seam is in fresh project-session handoff dispatch / agent-turn dedupe-purge behavior, not in the tool-set conflict path anymore

### Current next step

- patch the worker enqueue/claim/purge path so the surviving fresh project-session Frank handoff job is claimable and not purged as a phantom duplicate
- rerun a fresh clean project session after that fix
- then reprove that the new 4-minute bootstrap watchdog floor survives the old `1m30s` boundary in live traffic

## 2026-03-25 13:39 MDT update

- deployed runtime is now local patch-on-top of pushed commit `4bcbdc2b`
- dedicated tmux runtime is still `codex-e2e-20260324`
- service health is green
- the next real seam is no longer bootstrap first-wave parent selection
- the current live seam was a bootstrap/model-profile preservation regression, and it is now fixed in code and live-proven

### New product fix: bootstrap restarts must not clobber operator-selected current profiles

- root cause:
  - `internal/bootstrap/bootstrap.go`
  - `upsertOrgProfile(...)` treated any current-profile mismatch as a reason to rotate back to the seeded Anthropic default
  - restarting the worker at `13:34:39 MDT` reran bootstrap step 5 and silently created new current Anthropic rows:
    - `haiku` -> version `9`
    - `standard` -> version `9`
    - `high-capability` -> version `9`
  - this immediately broke the local-model fallback plan because fresh org turns kept resolving `claude-haiku-4-5-20251001` and failing with Anthropic 429s even after the operator had switched the current profiles to qwen

### Code change

- patched [`internal/bootstrap/bootstrap.go`](../internal/bootstrap/bootstrap.go)
  - if the desired seeded version already exists in org profile history, bootstrap now preserves the current operator-selected profile instead of rotating back to the seed
  - true unseen seed upgrades still rotate when needed
- added focused integration coverage in [`internal/bootstrap/bootstrap_integration_test.go`](../internal/bootstrap/bootstrap_integration_test.go)
  - `TestBootstrapRunPreservesOperatorSelectedCurrentProfileWhenSeedVersionAlreadyExists`

### Verification completed

- `go test -tags=integration ./internal/bootstrap -run 'TestBootstrapRun(SeedsAndIsIdempotent|PreservesExistingCurrentModelProfileVersion|RotatesCurrentModelProfileVersionWhenSeedChanges|PreservesOperatorSelectedCurrentProfileWhenSeedVersionAlreadyExists)$' -count=1`

### Live proof after redeploy

- switched all three logical profiles back to qwen through the supported model-profile API
  - `haiku` -> version `10` -> `qwen2.5:72b`
  - `standard` -> version `10` -> `qwen2.5:72b`
  - `high-capability` -> version `10` -> `qwen2.5:72b`
- restarted the worker again after the patch
- queried the live DB afterward and bootstrap step 5 had preserved the qwen current rows:
  - `haiku|10|t|qwen2.5:72b`
  - `standard|10|t|qwen2.5:72b`
  - `high-capability|10|t|qwen2.5:72b`
- importantly, no new Anthropic `version 11` rows were created on restart

### Current live canary state

- fresh async org session: `b1000247-b94d-4c14-a942-4b90dc7e8727`
- latest fresh create-project request:
  - message `0db25069-6be1-4f7c-9454-a5f52cd60236`
- active turn:
  - turn `4033be87-f829-4d23-9548-2448f4a745a0`
  - status `in_progress`
- active model invocation:
  - invocation `11c97a3f-6ca4-45e1-915b-aa8d549308d9`
  - status `in_flight`
  - model `qwen2.5:72b`
  - provider connection `34b13633-46b0-4471-ba52-1c0ad20cd36b`
- no fresh rerun-52 project row exists yet

### Current next step

- let the rerun-52 qwen-backed org turn complete
- if it creates the requested fresh project cleanly, proceed with the `plan-0325b` operator run from there
- if it produces another malformed org result or stalls, treat the org-session/qwen path as the next runtime seam and patch that next

## 2026-03-25 13:54 MDT update

- bootstrap model-profile preservation fix is committed and pushed on `main`
  - commit `12a9e773`
  - message: `Preserve operator-selected bootstrap model profiles`
- the next runtime seam after that was the bootstrap watchdog policy for slow local models

### New product fix: bootstrap watchdog must not kill qwen kickoff turns after 90 seconds

- root cause:
  - `internal/turn/engine.go`
  - bootstrap stream watchdog always used the base `projectBootstrapTurnTimeout` (`90s`)
  - clean local qwen kickoffs were still legitimately `in_flight`, but the watchdog failed them after `1m30s`
  - those false stalled failures then triggered automatic bootstrap restart behavior, creating unwanted `-restart` projects

### Failing live proof before the fix

- clean `rerun-54` project:
  - project `3385c7bf-49df-4dc1-927a-f10161bfb3dd`
  - session `ecbba5e1-898b-42ca-a430-ba8fe58c9fdb`
  - turn `01919eb1-b18d-47cb-88b6-ae38f41e2b89`
  - invocation `672ff280-46a1-44a1-a325-e8ffef9e9b90`
- live failure:
  - turn failed at `13:48:44 MDT`
  - failure reason: `bootstrap setup watchdog timed out after 1m30s with zero persisted staffing drafts, project assignments, scoped tasks, or flow templates; model invocation 672ff280-46a1-44a1-a325-e8ffef9e9b90 remained in_flight`
  - automatic restart project was created:
    - `f2268c0f-8468-494f-8bc9-39c4012d5246`
    - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-54-restart`

### Code change

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - bootstrap watchdog timeout now expands from `90s` to `4m` for slow local model names such as `qwen2.5:72b` and `mistral-nemo:latest`
  - hosted fast model names (`claude-*`, `gpt-*`, `o1*`, `o3*`) still use the base timeout
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestProjectBootstrapWatchdogTimeoutForModel`

### Verification completed

- `go test ./internal/turn -run 'Test(ProjectBootstrapWatchdogTimeoutForModel|HandleCompletedProjectExecutionContinuationTurnConsumesBoundedSizeQueueFailure)' -count=1`

### Live proof after redeploy

- fresh clean `rerun-55` project:
  - project `675a2529-59e0-4580-ac5c-deea4486371b`
  - session `9265afdd-0930-409e-bb9c-d347ffdebe12`
  - kickoff turn `d6fcbaa2-97b4-4c3d-bf15-16eedbffde0f`
  - kickoff invocation `9efaf0f6-3a38-477f-8db7-7ff67f8992aa`
- live worker log at kickoff showed:
  - `project bootstrap watchdog configured ... model_name=qwen2.5:72b ... effective_timeout=4m0s`
- the fix is real but only partial:
  - after waiting past the old 90-second boundary, kickoff turn `d6fcbaa2-97b4-4c3d-bf15-16eedbffde0f` was still `in_progress` and invocation `9efaf0f6-3a38-477f-8db7-7ff67f8992aa` was still `in_flight`
  - that kickoff turn later completed cleanly at `13:54:13 MDT`
  - the immediate bootstrap resume turn then regressed to the old 90-second timeout path:
    - turn `aa411ab3-de7f-4d1f-af7a-5b61524dfbf5`
    - invocation `8cf8c7ab-9eb7-4aef-b86d-dcadc0e132f8`
    - failed at `13:55:43 MDT` with `bootstrap setup watchdog timed out after 1m30s with zero persisted staffing drafts, project assignments, scoped tasks, or flow templates; model invocation 8cf8c7ab-9eb7-4aef-b86d-dcadc0e132f8 remained in_flight`
  - automatic restart project was then created:
    - `e46671c6-c5dc-4ca5-85f1-7ffc1973226e`
    - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-55-restart`

### Current live state

- qwen remains the current org profile for all three logical profiles:
  - `haiku|10|qwen2.5:72b`
  - `standard|10|qwen2.5:72b`
  - `high-capability|10|qwen2.5:72b`
- worker concurrency is still `2` in tmux `codex-e2e-20260324`
- active clean proof target is now the narrower seam inside `rerun-55`
  - kickoff timeout extension is proven
  - bootstrap resume follow-on still incorrectly times out after `1m30s`

### Current next step

- commit/push the kickoff-timeout slice to `origin/main` as a meaningful checkpoint
- patch the bootstrap resume/follow-on path so it also uses the slow-model watchdog timeout instead of falling back to `1m30s`
- rerun from a fresh clean project after that resume-path fix

## 2026-03-25 13:22 MDT update

- deployed runtime commit is `7252a20b` on `origin/main`
- dedicated tmux runtime is still `codex-e2e-20260324`
- `./bin/ottercamp health` is green
- the old rerun-47 failure note below is stale historical context, not the current live seam
- pre-test gate audit against `issues/plan-0325a.md` is now complete again and the focused gate slice is green

### Plan-0325A gate status

- task worktree isolation: implemented and covered
  - `internal/tools/native/task_worktree.go`
  - `internal/tools/native/task_worktree_test.go`
  - `internal/tools/native/native_integration_test.go`
- execution entry-head capture: implemented and covered
  - `internal/flow/execution_service.go`
  - `internal/flow/execution_service_test.go`
- deferred later-wave wakeup protection: implemented and covered
  - `internal/flow/execution_service.go`
  - `internal/flow/execution_service_test.go`
  - `internal/turn/engine_integration_test.go`
- bootstrap profile rotation: implemented and covered
  - `internal/bootstrap/bootstrap.go`
  - `internal/bootstrap/bootstrap_integration_test.go`

### Verification completed at this checkpoint

- `go test ./internal/tools/native -run 'TestEnsureTaskWorktreeFailsClosedWhenMainWorktreeOwnsTaskBranch|TestTaskCreateBoundedFollowOnChildAddsDependencyOnPreviousSibling|TestTaskCreateBootstrapTopLevelOrchestrationParentUsesSetupTasksWithoutSessionMetadata|TestTaskCreateDuringBootstrapTopLevelOrchestrationParentSkipsPlanningHeuristics' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegration(ProjectTaskSessionUsesTaskSpecificWorktree|FlowAdvanceCreatesCanonicalCommitWhenCommitSHAOmitted|FlowReviewDecisionApproveCreatesEmptyCanonicalCommit|FlowReviewDecisionRejectCreatesCanonicalRejectionCommit|FlowRecoveryDecisionRetryCreatesFreshExecution|ParentTaskCanCreateBoundedFollowOnChild|ConcurrentParentChildCreatesSerializeDependencyChain)' -count=1`
- `go test ./internal/flow -run 'Test(EnsureActiveExecutionCapturesEntryHeadSHA(WhenBindingSession|FromTaskBranchRef|FromTaskWorktreeWhenBranchNameMissing)|ActivateDraftDependentsAfterTaskDone(KeepsDeferredLaterWaveTasksDraft|QueuesExplicitFirstWaveTaskWhenReady)|ActivateDraftOrchestrationParentAfterChildDoneKeepsDeferredLaterWaveParentDraft)' -count=1`
- `go test -tags=integration ./internal/bootstrap -run 'TestBootstrapRun(SeedsAndIsIdempotent|PreservesExistingCurrentModelProfileVersion|RotatesCurrentModelProfileVersionWhenSeedChanges)' -count=1`

### Current next step

- rebuild/restart the tmux runtime on commit `7252a20b`
- note that deployed commit in `issues/log-0325a.md`
- start a fresh operator canary under `issues/plan-0325b.md`
- treat the first new runtime seam in that operator run as the next product fix target

## 2026-03-25 13:24 MDT update

- rerun-48 is the first fresh `plan-0325b` operator canary after the pre-test gate audit
- fresh project creation worked through the real org session
  - project id: `57eeb40d-5905-4d1e-bd36-02adcb8b2232`
  - slug: `speaker-pipeline-ops-validation-fresh-20260325-rerun-48`
  - project session id: `3764d5d5-8c0e-45b4-87be-0b94c16e58e3`

### First real rerun-48 seam

- bootstrap progressed cleanly through setup tasks `2-6`
- the first failure seam is in `bootstrap.setup.persist` first-wave selection
- the live PM selected top-level orchestration parents before any bounded executable child tasks existed:
  - task `9` `WS1: Pipeline Configuration & Scaffold`
  - task `10` `WS2: Execution Lifecycle Validation`
- those parent tasks were wrongly accepted as `first_wave_task_ids` and got:
  - `bootstrap_first_wave_selected = true`
  - no assignee
  - no flow template
- bootstrap then failed with:
  - `kickoff validation failed: first-wave task 10 (WS2: Execution Lifecycle Validation) has no assigned agent, so bootstrap cannot queue runnable execution`

### Root cause

- `internal/tools/native/mutation_tools.go`
  - `bootstrapFirstWaveSelectableTasksExcludingBlocked(...)` only excluded decomposition parents that already had child rows
  - orchestration-only parent containers with `decomposition.orchestration_only=true` but zero child rows were still considered selectable
  - explicit `first_wave_task_ids` validation also lacked a reject path for these parent containers

### New fix staged locally

- patched [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go)
  - first-wave selectable-task filtering now excludes any task where `taskRequiresBoundedChildren(...)` is true
  - explicit `select-first-wave` validation now rejects orchestration-only parent containers with actionable guidance to select bounded executable child tasks instead
- added focused coverage in:
  - [`internal/tools/native/mutation_tools_test.go`](../internal/tools/native/mutation_tools_test.go)
    - `TestBootstrapFirstWaveSelectableTasksSkipsOrchestrationOnlyParentWithoutChildren`
  - [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
    - `TestIntegrationBootstrapSetupPersistRejectsOrchestrationOnlyParentSelection`

### Verification completed in this stretch

- `go test ./internal/tools/native -run 'Test(BootstrapFirstWaveSelectableTasks(SkipsParentsAndDeferredFinalizationTasks|SkipsOrchestrationOnlyParentWithoutChildren)|TaskCreateBootstrapTopLevelOrchestrationParentUsesSetupTasksWithoutSessionMetadata)' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegration(BootstrapSetupPersistRejects(DependencyBlockedFirstWaveSelection|OrchestrationOnlyParentSelection)|ProjectTaskSessionUsesTaskSpecificWorktree)' -count=1`

### Current next step

- commit and push this bootstrap-selection fix slice
- rebuild/restart `codex-e2e-20260324`
- start fresh rerun-49
- verify that first-wave selection no longer accepts orchestration parent containers before bounded child-task materialization

## 2026-03-25 13:03 MDT update

- deployed runtime at that time had another uncommitted native bootstrap-parent patch on top of pushed commit `660fd4c4`
- dedicated tmux runtime was `codex-e2e-20260324`
- health was green after the latest rebuild/restart
- fresh rerun-47 was the current clean proof target and had advanced far enough to prove the latest parent-task slice

### What rerun-46 proved live

- rerun-46 project id: `f56c456f-870a-4647-be30-c9d256e0ea12`
- rerun-46 project session id: `db21265f-c37d-40e4-9ed5-13def09970f8`
- bootstrap staffing now persisted successfully under the newest code in that run:
  - task `2` `Bind repo and environment` -> `done`
  - task `3` `Staff the project` -> `done`
- top-level parent workstream tasks `9-11` were created in live traffic:
  - task `9` `Workstream A: Pipeline Scaffold Setup`
  - task `10` `Workstream B: Review Path Validation`
  - task `11` `Workstream C: Wave Gating Validation`

### Remaining live seam after rerun-46

- the bootstrap-parent bypass is still not complete in production behavior
- live tool results for rerun-46 messages `27-29` show the parent `task.create` calls still emitted planning artifacts:
  - task `9` returned `planning.prd_spec` artifacts under `planning/prd-spec/oc-9-*`
  - task `10` returned `planning.discovery_plan` artifacts under `planning/discovery-plan/oc-10-*`
  - task `11` returned `planning.discovery_plan` artifacts under `planning/discovery-plan/oc-11-*`
- persisted task rows confirm the contamination survived creation:
  - tasks `9-11` all have assigned worker `79d81743-a6fe-4899-85c5-e74748f34ce4`
  - tasks `9-11` all have flow template `9a60dfee-1fc7-4e3a-bd05-f9da1bb97552`
  - tasks `9-11` all still carry non-empty `planning` metadata
- bootstrap then failed immediately afterward with:
  - `kickoff validation failed: bootstrap setup persisted staffing but did not yet materialize any executable non-bootstrap project tasks`
- rerun-46 project-session metadata now records:
  - `project_bootstrap.status = failed`
  - `current_phase = task_tree_persisted`
  - `last_checkpoint = flow_templates_persisted`

### Latest code change not yet live-proven

- patched [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go)
  - bootstrap top-level orchestration-parent bypass now keys off either:
    - active bootstrap session metadata, or
    - an actually-active bootstrap task tree (`bootstrap_setup_task` rows still incomplete)
  - this removes the earlier dependency on session-scoped bootstrap metadata being present in every tool execution context
- added focused coverage in [`internal/tools/native/mutation_tools_test.go`](../internal/tools/native/mutation_tools_test.go)
  - `TestTaskCreateBootstrapTopLevelOrchestrationParentUsesSetupTasksWithoutSessionMetadata`
  - plus the earlier matcher/orchestration-parent tests

### Verification completed in this stretch

- `go test ./internal/tools/native -run 'Test(TaskCreate(DuringBootstrap(TopLevelOrchestrationParentSkipsPlanningHeuristics|WithoutAssignedAgentUsesBootstrapFlowBeforePlanningHeuristics)|BootstrapTopLevelOrchestrationParentUsesSetupTasksWithoutSessionMetadata)|LooksLikeBootstrapOrchestrationParentMatchesLiveBootstrapContainers|TaskCreateWithExplicitFlowTemplateDoesNotApplyPlanningHeuristics)$' -count=1`
- rebuilt `./bin/ottercamp`
- restarted `serve` and `worker --concurrency 24` in tmux
- `./bin/ottercamp health` passed after restart

### Live proof on rerun-47

- rerun-47 project id: `22fbefae-35e3-4b6c-a2f4-157969ec04cf`
- rerun-47 project session id: `036be1aa-1440-42a8-829d-23fed43df696`
- top-level parent workstreams were finally created cleanly under the latest binary:
  - task `9` `Workstream A: Pipeline Scaffold & Configuration`
  - task `10` `Workstream B: Validation Scripts & Tests`
  - task `11` `Workstream C: Review & Recovery Path Demonstration`
  - task `12` `Workstream D: Wave Gating & Final Documentation`
- the live parent `task.create` tool results for messages `26-29` returned only plain `task` payloads
  - no `planning` payload was returned on any of those four creates
- the persisted task rows also show the new intended behavior:
  - tasks `9-12` all have `assigned_agent_id = null`
  - descriptions are explicit orchestration-container descriptions
  - they were not force-attached to worker assignment at creation time

### Current next seam

- the bootstrap-parent contamination bug is now proven fixed in live traffic
- rerun-47 still fails immediately afterward in bootstrap validation timing
- current session metadata now records:
  - `project_bootstrap.status = failed`
  - `current_phase = task_tree_persisted`
  - `validation_failure_reason = kickoff validation failed: bootstrap setup persisted staffing but did not yet materialize any executable non-bootstrap project tasks`
- that means the next product seam is no longer top-level parent planning contamination
- the next seam is that bootstrap validation is firing too early, before the same still-running kickoff turn has finished materializing bounded executable child tasks under the new parent workstreams

## 2026-03-25 12:43 MDT update

## 2026-03-25 12:39 MDT update

- fresh rerun-42 canary is active on project `a39624fe-f1d8-4ee2-b6fa-c47a2853b3b6`
- fresh project session is `f7f706bd-94e9-4b5d-9a4a-3b40ef314226`
- rerun-42 reproduced the dependency seam again on a clean project, but more precisely than before:
  - parent `9` has child ids `13,14` persisted, but no `14 -> 13` dependency row
  - parent `11` has child ids `17,18,19` persisted, but no `19 -> 18` dependency row
  - the dependency table only contained:
    - `16 -> 15`
    - `18 -> 17`

### Root cause: concurrent sibling child creation under one parent

- the earlier follow-on child repair worked in focused tests but still failed live when multiple `task.create` calls under the same parent executed in parallel inside one model turn
- parent metadata was eventually correct (`child_task_ids` and `decomposition_parent_task_id` were present), but the dependency repair pass raced before sibling tasks were all visible, so no later repair filled the missing direct edge

### New fix: serialize parent-scoped child mutation per parent task

- patched [`internal/tools/native/executor.go`](../internal/tools/native/executor.go)
  - added executor-scoped parent task mutex storage
- patched [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go)
  - parent-scoped `task.create` now acquires a per-parent mutation lock before child reuse/create, parent metadata append, and dependency-chain repair
  - this forces sibling child creation under the same parent to serialize even when the model emits multiple `task.create` calls in one turn
- added focused regression coverage in [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
  - `TestIntegrationConcurrentParentChildCreatesSerializeDependencyChain`

### Verification completed in this stretch

- `go test ./internal/tools/native -run 'TestTaskCreateBoundedFollowOnChildAddsDependencyOnPreviousSibling$' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegration(ParentTaskCanCreateBoundedFollowOnChild|ConcurrentParentChildCreatesSerializeDependencyChain)$' -count=1`

### Current next step

- rebuild/restart the dedicated tmux runtime on top of this lock-based fix
- start fresh rerun-43
- verify on the new clean project that parent-scoped child creation now yields the missing live edges (`14 -> 13`, `19 -> 18`) before first-wave execution starts

## 2026-03-25 12:43 MDT update

- fresh rerun-43 canary is active on project `79cd4362-8caf-4037-a03c-f369e3f5ee41`
- fresh project session is `24f8024b-e6f0-47ba-a912-f4da2f0ea06e`
- the parent-scoped child-creation race fix is now proven in live traffic on a clean project

### Live proof of the serialized child-chain repair

- rerun-43 created the clean child set:
  - `13`, `14` under parent `9`
  - `15`, `16` under parent `10`
  - `17` under parent `11`
  - `18`, `19` under parent `12` decomposition output
- the live dependency table now contains the missing direct sibling edges:
  - `14 -> 13`
  - `16 -> 15`
  - `19 -> 18`
- this is the first clean rerun where the previously-missing direct-create edge and the previously-missing later follow-on edge both appeared under the latest binary

### What remains visible on the same clean run

- top-level parent workstream creation is still contaminated by planning/playbook inference:
  - task `9` got discovery-plan artifacts under `planning/discovery-plan/oc-9-*`
  - task `10` got discovery-plan artifacts under `planning/discovery-plan/oc-10-*`
  - task `11` got discovery-plan artifacts under `planning/discovery-plan/oc-11-*`
  - task `12` got metrics-framework artifacts under `planning/metrics-framework/oc-12-*`
- bootstrap is also still mis-resuming at first-wave selection:
  - the resume prompt explicitly instructs `bootstrap.setup.persist` first
  - the PM still begins with blocked broad rereads (`project.list`), causing the engine to end the turn and retry

### Current next seam

- the dependency-chain bug is fixed
- the next operator-visible product seam is now the lingering planning contamination on top-level bootstrap parent creation, plus the stubborn bootstrap resume compliance issue at first-wave selection

## 2026-03-25 12:24 MDT update

- active operator canary is still rerun-40 project `85c6f2ad-ce59-425f-b9f9-ce4f81b5d545`
- rerun-40 is no longer clean proof material because the bad graph already let later sibling work execute out of order
- current board slice on rerun-40:
  - task `12`: `done`
  - task `13`: `in_progress`
  - task `14`: `done`
  - task `15`: `done`
  - task `16`: `draft`
  - task `17`: `draft`
  - task `18`: `done`
  - task `19`: `draft`
  - task `20`: `draft`

### New decomposed-child dependency fix

- patched [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go)
  - `createDecomposedParentChildren(...)` now adds sequential sibling `project_task_dependency` edges in child creation order
  - the same helper is used for reused and newly-created decomposed children, so follow-on retries preserve the same ordering graph instead of recreating an empty one
- added focused coverage in [`internal/tools/native/mutation_tools_test.go`](../internal/tools/native/mutation_tools_test.go)
  - `TestTaskCreateDecomposedChildrenAddSequentialDependenciesDuringBootstrap`
- added focused integration coverage in [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
  - `TestIntegrationBootstrapParentDecompositionAddsSequentialChildDependencies`

### Verification completed in this stretch

- `go test ./internal/tools/native -run 'Test(TaskCreateDecomposedChildrenAddSequentialDependenciesDuringBootstrap|TaskUpdateQueuedOversizedTaskReusesExistingDecomposedChildren)$' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegration(BootstrapParentDecompositionAddsSequentialChildDependencies|ParentTaskCanDecomposeBroadFollowOnChildRequest)$' -count=1`

### Live evidence captured before the fresh rerun

- the contaminated rerun-40 child dependency graph currently resolves as:
  - `13 -> 12`
  - `14 -> 13`
  - `16 -> 15`
  - `17 -> 16`
  - `19 -> 18`
  - `20 -> 19`
- but rerun-40 still shows the earlier bad effect in persisted task state:
  - task `14` already reached `done` while task `13` remains `in_progress`
- that means rerun-40 cannot be used to prove the fix; the next required step is a fresh rerun under the rebuilt binary

### Current next seam

- no new runtime seam has been proven yet after this patch
- the next step is operational:
  - rebuild
  - restart `codex-e2e-20260324`
  - create a brand-new fresh validation project
  - verify bounded child tasks come out with the sequential dependency graph before execution starts

## 2026-03-25 12:28 MDT update

- fresh rerun-41 canary is active on project `567d4066-f928-40c2-bab2-fa48760e3f54`
- fresh project session is `991c637d-8613-408e-adc0-49e723e9adb3`
- rerun-41 proved the first dependency patch only covered siblings created inside the same decomposition batch

### New follow-on child dependency repair

- patched [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go)
  - added `ensureParentChildTaskDependencyChain(...)`
  - parent-scoped child create/reuse paths now resync the full sibling dependency chain after any child creation, not just within a single decomposition batch
- added focused coverage in [`internal/tools/native/mutation_tools_test.go`](../internal/tools/native/mutation_tools_test.go)
  - `TestTaskCreateBoundedFollowOnChildAddsDependencyOnPreviousSibling`
- extended integration coverage in [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
  - `TestIntegrationParentTaskCanCreateBoundedFollowOnChild`

### Fresh live proof before the second fix

- rerun-41 cleanly created the staffed bootstrap tree and child task set `14-22`
- fresh dependency rows showed only the batch-created pairs:
  - `16 -> 15`
  - `18 -> 17`
  - `21 -> 20`
- missing edge on the clean project:
  - task `19` had no `19 -> 18` dependency because it was appended later under the same parent after the earlier `17/18` batch

### Verification completed in this stretch

- `go test ./internal/tools/native -run 'Test(TaskCreate(BoundedFollowOnChildAddsDependencyOnPreviousSibling|DecomposedChildrenAddSequentialDependenciesDuringBootstrap)|TaskUpdateQueuedOversizedTaskReusesExistingDecomposedChildren)$' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegration(ParentTaskCanCreateBoundedFollowOnChild|BootstrapParentDecompositionAddsSequentialChildDependencies|ParentTaskCanDecomposeBroadFollowOnChildRequest)$' -count=1`

### Current next seam

- the clean rerun has already exposed the next operator-visible issue independently of the dependency fix:
  - top-level workstream parents `9-13` still picked up planning-playbook artifacts on creation even though this is bootstrap execution work
  - rerun-41 bootstrap then had to recover from oversized child requests while materializing the child tree
- immediate next action is still to redeploy the latest dependency-chain fix and re-check rerun-41 under the new binary, but the likely next product seam after that is the lingering planning contamination on fresh bootstrap parent creation

## 2026-03-25 09:05 MDT update

- rerun-38 project `fc4a025d-e7c9-4b88-9485-17d2b6328e52` is still the main fresh canary
- tasks `9-12`, `16`, and `17` are `done`
- later-wave tasks `13-15` and `18-24` were still stuck `draft` because the project session had only a dead pending `project_bootstrap` dispatch and no `project_execution_continuation` message
- tmux stack was rebuilt/restarted again
- health is green

### New project continuation recovery fixes

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `hasQueuedAgentTurnForSession(...)` now ignores queued/claimed `agent_turn` jobs for project sessions when the queued trigger message source is `project_bootstrap` but session bootstrap metadata already says `status=completed`
  - this closes the engine-side false positive that blocked `maybeContinueProjectExecutionAfterTaskCompletion(...)` after task `10` completed
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestHasQueuedAgentTurnForSessionIgnoresCompletedBootstrapDispatch`

- patched [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `RequeueActiveProjectSessionsWithoutTurns(...)` now ignores the same stale completed-bootstrap dispatches when deciding whether a project session already has a queued job
  - when bootstrap is complete and draft work remains, but the only surviving pending user message is the dead bootstrap resume prompt, worker startup now synthesizes a fresh pending `project_execution_continuation` message instead of trying to recycle the bootstrap trigger
- added focused coverage in [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - `TestJobWorkerRequeueActiveProjectSessionsWithoutTurnsIgnoresCompletedBootstrapDispatch`

### Verification completed in this stretch

- `go test ./internal/turn -run 'Test(HasQueuedAgentTurnForSessionIgnoresCompletedBootstrapDispatch|SanitizeInheritedRunAttribution(DropsTerminalRun|KeepsActiveRun)|SyncBoundFlowExecutionTurnOwnershipClearsStaleLiveRunID|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation)$' -count=1`
- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RequeueActiveProjectSessionsWithoutTurns(IgnoresCompletedBootstrapDispatch|SkipsFinalMessages)?|RecoverStaleInProgressTriggeredTurns(SuppressesCompletedProjectBootstrapRequeue|SuppressesProjectContinuationWithoutOpenTasks|KeepsQueuedRetry|UsesExecutionMetadataLiveTurn)?|ClaimPendingAgentTurns(SkipsStaleProjectBootstrapAndContinuationJobs|PrioritizesFreshOrgWorkOverProjectContinuation))' -count=1`
- rebuilt `./bin/ottercamp`
- restarted tmux `serve` and `worker`
- `./bin/ottercamp health` passed

### Live proof

- the stale inert bootstrap job is still present on rerun-38:
  - job `5041420c-a5ba-4e32-8c46-9e1494551a42`
  - payload message `53069ddc-9504-4122-9794-b9b2be3c4ae9`
  - source `project_bootstrap`
- but startup recovery no longer depends on that message being reusable
- worker startup synthesized a fresh continuation user message on the same project session:
  - message `d653de35-4a9a-46c2-8166-516c6f670971`
  - source `project_execution_continuation`
  - content starts `Continue the active project execution now. Bootstrap is complete and draft project work remains...`
- worker also created and claimed a real dispatch for that synthesized continuation:
  - job `cc13af14-fd52-4f40-af63-c34b2a43e895`
  - turn `1400a51f-655c-4507-b064-c08f15c45ae5`
  - session `efd6b36b-7687-4b23-a635-9f00df19bbb5`
- the new project continuation turn is model-backed in live traffic:
  - model invocation `6ee11e50-6596-44b0-a469-f8ac346c19f4`
  - status `in_flight`
  - created at `2026-03-25 09:04:39.86506-06`

### Current next seam

- the dead-bootstrap blockage is fixed
- rerun-38 is now waiting on the first live synthesized project-continuation turn to finish
- as of this update:
  - task `13` is still `draft`
  - task `14` is still `draft`
  - task `15` is still `draft`
  - tasks `18-24` are still `draft`
- so the next check is whether turn `1400a51f-655c-4507-b064-c08f15c45ae5` actually queues/promotes task `13` or whether the project continuation lane still drifts once the model responds

## 2026-03-25 06:00 MDT update

- rerun-38 project `fc4a025d-e7c9-4b88-9485-17d2b6328e52` is still the main fresh canary
- current focused board slice:
  - task `10`: `blocked`
  - task `12`: `done`
  - task `13`: `draft`
  - task `14`: `draft`
  - task `15`: `draft`
- tmux stack was rebuilt/restarted again
- health is green

### New review-lane CLI guard

- patched [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go)
  - `cli.execute` is now blocked in `review` task sessions before any shell command runs
  - review lanes now get `review_action_required` guidance telling them to inspect bounded artifacts with normal file/git tools and then call `flow.review_decision`
  - this closes the repeated live seam where reviewers were using shell-based `find`, `ls`, `cat`, and `git show` probes across workspace roots instead of making the review decision
- added focused coverage in [`internal/tools/native/mutation_tools_test.go`](../internal/tools/native/mutation_tools_test.go)
  - `TestCLIExecuteBlockedInReviewTaskSession`

### Verification completed in this stretch

- `go test ./internal/tools/native -run 'Test(CLIExecuteBlockedInReviewTaskSession|FileWriteRejectsNarratedTaskPlaceholderContent|FileWriteAllowsValidationExecutionPlanDeliverable)' -count=1`
- `go test ./internal/flow -run 'Test(EnsureActiveExecution(ReturnsExistingActiveAfterCreateConflict|SerializesConcurrentFlowStarts|BackfillsMissingCurrentNodeExecution)|StartFlowReturnsExistingActiveAfterCreateConflict)' -count=1`
- rebuilt `./bin/ottercamp`
- restarted tmux `serve` and `worker`
- `./bin/ottercamp health` passed

### Live proof since the last checkpoint

- task `12` is now fully resolved:
  - review execution `12a8a564-b7d1-47d7-981e-e92cca3a1639` is `completed`
  - persisted review metadata now shows:
    - `review_decision.decision = approve`
    - `decided_at = 2026-03-25T11:50:43.58377Z`
  - task `12` itself is now `done`
- the duplicate-active execution fix held through that final review path; task `12` did not reopen the old stranded-execution loop

### Current next seam

- rerun-38 is now primarily blocked by task `10`:
  - task `10` remains `blocked` on review node `adbf1bdd-d9d9-4a81-99ac-f159a7f7d726`
- a separate fresh review lane on rerun-39 (`WS-3: Analyze Results and Sign Off`, task id `ae7c187c-6b05-4519-95ec-43a7b3d4054d`) is the current live probe for the new review guard:
  - session `2e096fc1-b20c-4c61-bff5-f23b7ef07fc4`
  - execution `f079561c-ef46-4eb4-ab08-a82eff1da166`
- before the new guard, that lane was repeatedly using broad `cli_execute` and absolute-path review inspection against workspace `speaker-pipeline-ops-validation-fresh-20260325-rerun-39-6b0b9c`
- the next check is whether the new binary stops that behavior in fresh review retries and forces a direct `flow.review_decision`

## 2026-03-25 05:52 MDT update

- rerun-38 remains the active fresh canary on project `fc4a025d-e7c9-4b88-9485-17d2b6328e52`
- current focused task board slice:
  - task `10`: `blocked`
  - task `11`: `done`
  - task `12`: `review`
  - task `16`: `done`
  - task `17`: `done`
- tmux session remains `codex-e2e-20260324`
  - health is green after rebuild/restart

### New single-active flow execution fix

- patched [`internal/flow/execution_service.go`](../internal/flow/execution_service.go)
  - `StartFlow(...)` now treats `repo.ErrConflict` from execution creation as “another process already created the active execution” and falls back to `GetActive(...)`
  - `EnsureActiveExecution(...)` now does the same when backfilling a missing active execution for the current node
  - this closes the cross-process race where two workers could both see no active execution and both insert an `active` row for the same task/node
- added focused coverage in [`internal/flow/execution_service_test.go`](../internal/flow/execution_service_test.go)
  - `TestEnsureActiveExecutionReturnsExistingActiveAfterCreateConflict`
  - `TestStartFlowReturnsExistingActiveAfterCreateConflict`

### New DB invariant for active task-node executions

- added migration [`migrations/0129_flow_node_execution_single_active.sql`](../migrations/0129_flow_node_execution_single_active.sql)
  - first collapses existing duplicate `active` rows per `(task_id, flow_node_id)` by marking older rows `abandoned`
  - then adds partial unique index:
    - `flow_node_execution_single_active_per_node_idx`
    - unique on `(task_id, flow_node_id)` where `status = 'active'`
- updated repo coverage in [`internal/repo/flow_execution_integration_test.go`](../internal/repo/flow_execution_integration_test.go)
  - `TestFlowNodeExecutionRepoRejectsDuplicateActiveExecutionForTaskNode`
  - adjusted lifecycle expectations so completed work no longer falls back to another still-active sibling execution on the same node

### Verification completed in this stretch

- `go test ./internal/flow -run 'Test(EnsureActiveExecution(ReturnsExistingActiveAfterCreateConflict|SerializesConcurrentFlowStarts|BackfillsMissingCurrentNodeExecution)|StartFlowReturnsExistingActiveAfterCreateConflict)' -count=1`
- `go test -tags=integration ./internal/repo -run 'TestFlowNodeExecutionRepo(LifecycleAndGetActive|RejectsDuplicateActiveExecutionForTaskNode|RejectAndVisitIncrement|UpdateRuntimeSubstate)' -count=1`
- rebuilt `./bin/ottercamp`
- restarted tmux `serve` and `worker`
- `./bin/ottercamp health` passed

### Live proof

- before the fix, task `12` had two simultaneous `active` work-node executions on visit `15`:
  - `f5535e4c-46b7-4eb8-afb2-c44d08590b5c`
  - `5b423b7d-5678-445a-b5e3-765b8b8aee1e`
- after the migration + restart:
  - both duplicate visit-15 work executions were retired to `abandoned`
  - a new single work execution `efec28c4-5ab1-4698-a175-d6d6bcb4a9f6` ran and completed
  - task `12` advanced cleanly into review:
    - current flow node id `adbf1bdd-d9d9-4a81-99ac-f159a7f7d726`
    - active review execution `12a8a564-b7d1-47d7-981e-e92cca3a1639`
    - active review session `c3d5ff52-5425-4704-8a4e-1b709a95918a`
- the previously stale `project_task.metadata.recovery_file_write_checkpoint` is no longer present on task `12`
- the real deliverable from the out-of-tree task worktree still exists:
  - `/Users/sam/otter-data/task-worktrees/speaker-pipeline-ops-validation-fresh-20260325-rerun-38/task-12/FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`

### Current next seam

- the critical path is no longer:
  - wrong-target checkpoint hijack
  - missing `flow.recovery_decision`
  - duplicate active work executions on task 12
- the next live seam is review-turn stability on task `12`:
  - review execution `12a8a564-b7d1-47d7-981e-e92cca3a1639` is active
  - review session `c3d5ff52-5425-4704-8a4e-1b709a95918a` is active
  - first post-fix review turn `c0766ea9-8c4d-44fb-819c-329a30b77915` failed with:
    - `invalid status transition`
    - then worker recovery marked it stale and scheduled a fresh retry
  - current retry turn is `bf4e2b6c-2431-4cc4-9f2c-86d0e71dad45`
  - review lane is still wasting turns on absolute-path `file.read` / `cli_execute` inspection of bootstrap artifacts instead of directly using `flow.review_decision`
- so the next debugging target is no longer work execution ownership
- it is review-lane status-transition / stale-turn recovery behavior on the new review execution

## 2026-03-25 05:48 MDT update

- rerun-38 is still the active fresh canary on project `fc4a025d-e7c9-4b88-9485-17d2b6328e52`
- current focused task board slice:
  - task `10`: `blocked`
  - task `11`: `done`
  - task `12`: `in_progress`
  - task `16`: `done`
  - task `17`: `done`
- tmux session is still `codex-e2e-20260324`
  - health is green after the latest rebuild/restart

### New deterministic task continuation summary fix

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - async `project_task` continuation now preserves the current direct recovery request when the trigger user message explicitly instructs `flow.recovery_decision` or `flow.review_decision`
  - deterministic continuation summary form is now:
    - `Active task request: <current user message>`
  - this closes the live task-12 context-compression seam where continuation was summarizing stale rejection history instead of the actual recovery instruction
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestContinuationTurnUsesDeterministicActiveRequestSummaryForDirectTaskRecoveryPrompt`

### New native tool catalog migration

- added migration [`migrations/0128_tool_definition_flow_recovery_decision.sql`](../migrations/0128_tool_definition_flow_recovery_decision.sql)
  - seeds native tool definition `flow.recovery_decision`
  - required capability: `flow.control`
  - schema requires:
    - `flow_node_execution_id`
    - `decision`
  - `decision` enum:
    - `resume`
    - `retry`
    - `block`
    - `escalate`
- live DB proof after restart:
  - `tool_definition.name = flow.recovery_decision`
  - `display_name = Flow Recovery Decision`
  - `required_capability = flow.control`

### Live proof: task-12 direct retry control plane now works

- old review session `19162741-cf6c-4141-a5c0-9a8307f607f0` correctly preserved the direct recovery request in compressed continuation:
  - `[Continuation summary] Active task request: Use flow.recovery_decision now. Decision: retry ... Stay pinned to FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md.`
- existing session tool-set cache was stale and did not include the new native tool, so I invalidated `session_tool_set` rows for that session’s agents and started a fresh task session:
  - fresh session: `39a65150-3508-4d6a-834f-121bbcd6ca2e`
- live proof from the fresh session:
  - the model called `flow_recovery_decision`
  - args:
    - `decision = retry`
    - `flow_node_execution_id = d4e1d332-7b6b-4a15-9f84-92807a59c7fb`
    - reason explicitly said to stay pinned to `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md` and not switch to sibling bootstrap artifact `BOOTSTRAP-VALIDATION-OC9.md`
- control-plane aftermath:
  - old review execution `d4e1d332-7b6b-4a15-9f84-92807a59c7fb` remains `rejected` but now has persisted `recovery_decision` metadata
  - retry-related review execution `d87ade95-c477-4cca-88ce-93b6b53ecda8` is `abandoned`
  - task `12` returned to `in_progress`

### Live proof: task-12 resumed lane now writes the correct target deliverable

- stale `project_task.metadata.recovery_file_write_checkpoint` is still visible and still wrong:
  - `target_path = BOOTSTRAP-VALIDATION-OC9.md`
  - `updated_at = 2026-03-25T11:23:35.364132Z`
- but live behavior is no longer pinned to that stale target:
  - active work execution `5b423b7d-5678-445a-b5e3-765b8b8aee1e`
  - session `8f0e8c1a-9ee6-471a-8c54-a8dda80b676d`
  - turn `d5de68e7-94c5-4f5e-91a3-7455baabca6b` wrote `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`
  - file exists on disk at:
    - `/Users/sam/otter-data/task-worktrees/speaker-pipeline-ops-validation-fresh-20260325-rerun-38/task-12/FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`
- substantive file body is now real and task-scoped:
  - it validates completed first-wave tasks 16 and 17
  - it leaves tasks 18 and 19 as pending rejection/recovery validation
  - it no longer writes or reuses sibling artifact `BOOTSTRAP-VALIDATION-OC9.md`

### New current blocker: duplicate active work executions on task 12

- the stale checkpoint hijack is no longer the critical path
- task 12 now has two simultaneous `active` work-node executions on the same flow node:
  - `5b423b7d-5678-445a-b5e3-765b8b8aee1e`
    - session `4752c1be-2877-4100-baf4-392dd5417739`
    - metadata includes stranded-execution recovery checkpoint and latest `live_run_id = f611e473-cc83-4863-b6a6-2f3a83c5d4d5`
  - `f5535e4c-46b7-4eb8-afb2-c44d08590b5c`
    - session `7e60fe22-a041-4fc6-8115-79bfa316c1f8`
    - still `active` even though that session is already closed
- live consequence:
  - task 12 repeatedly re-enters supervisor recovery with `active execution lost live task turn`
  - new supervisor recovery session `4752c1be-2877-4100-baf4-392dd5417739` was spawned after session `8f0e8c1a-...` had already produced the real deliverable
- this now looks like duplicate-active execution / stale-active execution cleanup, not file-target selection, tool availability, or continuation-summary loss

### Verification completed in this stretch

- `go test ./internal/turn -run 'Test(PersistRecoveryFileWriteCheckpoint(KeepsCurrentTaskTargetWhenHistoricalSiblingArtifactExists|KeepsExistingSubstantiveTargetPath|DiscardsCrossTaskExistingTargetPath|PrefersAuthoritativeFailureTargetPath|PrefersBetterTaskMatchedExistingTarget)|LoadRecoveryResumeStateIncludesValidationSiblingContext|RecoveryFileWriteDraftRejectReason(AllowsValidationExecutionPlanDeliverable|AllowsValidationPlanChecklistThatMentionsFlowNodeExecutionId)|MaybeSynthesizeRecoveryFileWriteToolCalls(UsesDirectSubstantiveAssistantBody|UsesSubstantiveAssistantDraftBlock))' -count=1`
- `go test ./internal/turn -run 'Test(ContinuationTurnUsesDeterministicActiveRequestSummaryForDirectTaskRecoveryPrompt|PersistRecoveryFileWriteCheckpointKeepsCurrentTaskTargetWhenHistoricalSiblingArtifactExists|LoadRecoveryResumeStateIncludesValidationSiblingContext)' -count=1`
- rebuilt `./bin/ottercamp`
- restarted tmux `serve` and `worker`

### Next live step

- inspect the retry / recovery path that leaves prior work execution `f5535e4c-46b7-4eb8-afb2-c44d08590b5c` active when newer work execution `5b423b7d-5678-445a-b5e3-765b8b8aee1e` already exists
- confirm whether retry, supervisor recovery, or execution handoff is failing to abandon superseded active executions for the same task + flow node
- after fixing that, rerun task 12 and verify that the real deliverable advances to review instead of spawning another stranded-execution recovery loop

## 2026-03-25 05:32 MDT update

- rerun-38 is still the active fresh canary on project `fc4a025d-e7c9-4b88-9485-17d2b6328e52`
- current focused task board slice:
  - task `10`: `blocked`
  - task `11`: `done`
  - task `12`: `blocked`
  - task `16`: `done`
  - task `17`: `done`
- tmux session is still `codex-e2e-20260324`
  - pane `0.0` serve PID: `20574`
  - pane `0.1` worker PID: `61644`
  - health is green after the latest rebuild/restart

### New recovery checkpoint anti-hijack fix

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - historical recovery fallback can no longer replace a current-task checkpoint target with a sibling task artifact during blocked/review persistence
  - this is the exact OC-12 seam where a blocked review on `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md` got rebound to sibling bootstrap artifact `BOOTSTRAP-VALIDATION-OC9.md`
  - the new guard now re-checks historical fallback candidates against current-task scope before they are allowed to override either reconciliation or persisted checkpoint state
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestPersistRecoveryFileWriteCheckpointKeepsCurrentTaskTargetWhenHistoricalSiblingArtifactExists`

### Validation sibling-context fix from the latest patch cycle

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - recovery resume state for orchestration-style validation parent tasks now includes sibling validation task statuses from the same `V2*` family
  - task 12 can now be resumed with concrete sibling execution context instead of claiming that first-wave status is unknowable from the task lane
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestLoadRecoveryResumeStateIncludesValidationSiblingContext`

### Verification completed in this stretch

- `go test ./internal/turn -run 'Test(PersistRecoveryFileWriteCheckpoint(KeepsCurrentTaskTargetWhenHistoricalSiblingArtifactExists|KeepsExistingSubstantiveTargetPath|DiscardsCrossTaskExistingTargetPath|PrefersAuthoritativeFailureTargetPath|PrefersBetterTaskMatchedExistingTarget)|LoadRecoveryResumeStateIncludesValidationSiblingContext|RecoveryFileWriteDraftRejectReason(AllowsValidationExecutionPlanDeliverable|AllowsValidationPlanChecklistThatMentionsFlowNodeExecutionId)|MaybeSynthesizeRecoveryFileWriteToolCalls(UsesDirectSubstantiveAssistantBody|UsesSubstantiveAssistantDraftBlock))' -count=1`
- rebuilt `./bin/ottercamp`
- restarted tmux `serve` and `worker`

### Live proof and current blocker

- task `12` did briefly reach `review`, and the live review session proved this was not a “review without artifact” bug:
  - session `19162741-cf6c-4141-a5c0-9a8307f607f0`
  - flow node execution `d4e1d332-7b6b-4a15-9f84-92807a59c7fb`
  - the reviewer correctly found that committed file `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md` was only a stale blocker memo, not evidence of first-wave execution
  - rejection was therefore correct, and the reject path exhausted allowed visits
- concrete live artifact state at this checkpoint:
  - task worktree for review contained:
    - `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`
    - `BOOTSTRAP-VALIDATION-OC9.md`
  - `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md` was only the blocker memo, while the durable recovery artifact still contains the real drafted validation plan under `.ottercamp/recovery/FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`
- stale metadata still visible in the DB after the restart:
  - task `12` metadata still shows checkpoint target `BOOTSTRAP-VALIDATION-OC9.md`
  - there is no pending/claimed `agent_turn` job for task `12`
  - so the live lane is currently blocked on control-plane recovery action, not on worker ownership

### Next live step

- drive or observe the next recovery/control-plane action for task `12`
- verify that the newly rebuilt engine preserves `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md` instead of rebinding to `BOOTSTRAP-VALIDATION-OC9.md`
- if the recovery action still rebinds to bootstrap content, inspect the control-plane path that seeds the next blocked-task retry rather than the checkpoint persistence path itself

## 2026-03-25 05:14 MDT update

- rerun-38 remains the active fresh canary on project `fc4a025d-e7c9-4b88-9485-17d2b6328e52`
- current focused task board slice:
  - task `10`: `blocked`
  - task `11`: `done`
  - task `12`: `in_progress`
  - task `16`: `done`
  - task `17`: `done`
- tmux session is still `codex-e2e-20260324`
  - pane `0` serve PID: `78926`
  - pane `1` worker PID: `78925`
  - health is green after the latest rebuild/restart

### New turn-engine recovery synthesis fix

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `substantiveRecoveryDraftFromAssistantContent(...)` now prefers fenced substantive drafts first, but also falls back to a direct structured assistant Markdown body when it already is the real deliverable body
  - this closes the live gap where task-12 was emitting a full markdown deliverable in the assistant reply, but recovery synthesis ignored it because it was not fenced
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestMaybeSynthesizeRecoveryFileWriteToolCallsUsesDirectSubstantiveAssistantBody`

### New recovery draft classifier fix

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - `recoveryFileWriteDraftRejectReason(...)` no longer treats every occurrence of `flow_node_execution_id` as a request for operator control-plane input
  - the runtime-control-plane rejection now only fires on actual request phrasing like “need/provide/share the flow_node_execution_id”
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestRecoveryFileWriteDraftRejectReasonAllowsValidationPlanChecklistThatMentionsFlowNodeExecutionId`

### New validation-plan allowance fix

- patched both [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go) and [`internal/turn/engine.go`](../internal/turn/engine.go)
  - the execution-plan/checklist rejection heuristic now exempts real `validation-plan` deliverables when the body is clearly a validation plan (`Validation Objective`, `Validation Checkpoints`, `Success Criteria`, `Failure Mode Registry`, `Validation Execution Plan`)
  - this keeps rejecting fake test-execution plans while allowing `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`
- added focused coverage:
  - [`internal/tools/native/mutation_tools_test.go`](../internal/tools/native/mutation_tools_test.go)
    - `TestFileWriteAllowsValidationExecutionPlanDeliverable`
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
    - `TestRecoveryFileWriteDraftRejectReasonAllowsValidationExecutionPlanDeliverable`

### New orchestration-task context-read fix

- patched [`internal/turn/engine.go`](../internal/turn/engine.go)
  - task-scoped async execution still blocks broad context tools by default
  - but orchestration-style validation parent tasks now get a narrow exemption for `project.get` and `task.list`
  - the exemption is intentionally limited to tasks that look like orchestration/parent validation tasks; memory browsing, agent browsing, and other broad reads stay blocked
- added focused coverage in [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
  - `TestShouldBlockTaskExecutionBroadContextToolAllowsOrchestrationValidationContextReads`

### Verification completed in this stretch

- `go test ./internal/turn -run 'TestMaybeSynthesizeRecoveryFileWriteToolCalls(UsesDirectSubstantiveAssistantBody|UsesSubstantiveAssistantDraftBlock|UsesPersistedTargetDraftAfterGenericRetryReply|UsesContinuationSummaryDraft|OverridesInvalidMutationWithPersistedDraft|SkipsRejectedPersistedTargetDraft)' -count=1`
- `go test ./internal/turn -run 'TestRecoveryFileWriteDraftRejectReason(AllowsValidationExecutionPlanDeliverable|AllowsValidationPlanChecklistThatMentionsFlowNodeExecutionId|RejectsExecutionPlanForTaskLog|RejectsExecutionSpecCompletionMemoWithoutArtifacts)' -count=1`
- `go test ./internal/turn -run 'Test(ShouldBlockTaskExecutionBroadContextTool(AllowsOrchestrationValidationContextReads)?|RecoveryFileWriteDraftRejectReason(AllowsValidationExecutionPlanDeliverable|AllowsValidationPlanChecklistThatMentionsFlowNodeExecutionId|RejectsExecutionPlanForTaskLog|RejectsExecutionSpecCompletionMemoWithoutArtifacts)|MaybeSynthesizeRecoveryFileWriteToolCalls(UsesDirectSubstantiveAssistantBody|UsesSubstantiveAssistantDraftBlock|UsesPersistedTargetDraftAfterGenericRetryReply|UsesContinuationSummaryDraft|OverridesInvalidMutationWithPersistedDraft|SkipsRejectedPersistedTargetDraft))' -count=1`
- `go test ./internal/tools/native -run 'TestFileWrite(AllowsValidationExecutionPlanDeliverable|RejectsExecutionPlanningNarration|RejectsRecoveryGuidanceSummaryPlaceholder)' -count=1`
- repeated rebuilds of `./bin/ottercamp`
- repeated tmux restarts of `serve` and `worker`

### Live proof and current blocker

- task `12` did materially move forward under these patches:
  - it stopped staying permanently `blocked`
  - it is now back to `in_progress`
  - session `fe5e2862-a5ab-4ae6-ba8e-2b536d46e8ef` remains the active task-12 lane
- live evidence from the newest task-12 session proved the earlier bugs:
  - the model did emit a real validation-plan body for `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`
  - the old `flow_node_execution_id` false positive and validation-plan false positive were both real seams in this lane
- the remaining blocker is now narrower and structural:
  - task 12 is an orchestration/validation parent task that needs real first-wave sibling execution evidence
  - even after the narrow `project.get` / `task.list` exemption landed in code, the currently active session is still cycling through retry stubs like:
    - `The system is blocking me from exploring the workspace ...`
    - `Unable to retrieve first-wave task execution data ...`
  - latest visible active session state at this checkpoint:
    - session `fe5e2862-a5ab-4ae6-ba8e-2b536d46e8ef`
    - current turn `aff85916-d94b-4369-8b20-88a5a2e9571f`
    - status `active`
- no `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md` file exists yet in the rerun-38 workspace

### Next live step

- keep tracing the post-restart task-12 lane under the newest binary
- verify whether the current active session actually exercises the new `project.get` / `task.list` exemption or is still continuing from pre-patch retry context
- if the current lane is still poisoned by stale retry context, force/observe the next genuinely fresh retry and confirm whether it can finally gather sibling first-wave evidence and write `FIRST-WAVE-EXECUTION-VALIDATION-PLAN.md`

## 2026-03-25 03:27 MDT update

- patched [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go) again:
  - `RecoverClaimedAgentTurnsWithoutLiveOwnership(...)` now honors a post-model grace window and will not recover a claimed async `agent_turn` if the current in-progress turn has a very recent `model_invocation.status='completed'`
  - this fixes the live false positive where rerun-33’s org turn had just finished one local Ollama invocation, but inline claimed-job recovery immediately requeued the same job before the turn could finalize
- added regression coverage in [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go):
  - `TestJobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnershipKeepsCurrentInProgressAttemptWithRecentCompletedInvocation`
- verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RecoverClaimedAgentTurnsWithoutLiveOwnership(KeepsCurrentInProgressAttemptWithRecentCompletedInvocation|RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentPendingAttempt)?|ClaimPendingAgentTurnsPrioritizesFreshOrgWorkOverProjectContinuation|RecoverStaleInProgressTriggeredTurns(FailsNonHeartbeatingClaimedAttemptWithoutRun|RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage))' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)' -count=1`

### Live model-routing cutover

- operationally enabled the existing healthy local OpenAI-compatible connection through the supported admin API:
  - provider `openai` id `76d72a73-da99-48ca-ad48-7f1c53342b2e`
  - connection `Ollama Local` id `5cbe78b4-77ef-4984-9e55-ed76a577def3`
  - base URL `http://localhost:11434/v1`
  - `is_enabled` is now `true`
- repointed all current org logical profiles to the local provider/model through the supported admin API:
  - `standard` v6 -> provider `openai`, model `qwen2.5:72b`
  - `high-capability` v6 -> provider `openai`, model `qwen2.5:72b`
  - `haiku` v6 -> provider `openai`, model `qwen2.5:72b`
- direct local validation before the cutover:
  - `curl http://localhost:11434/v1/models` returned `qwen2.5:72b`
  - `curl http://localhost:11434/v1/chat/completions ... model=qwen2.5:72b` returned `OK`

### Live rerun-33 proof

- fresh org canary message:
  - org session `672477c4-4431-4969-bf7b-923253907972`
  - message `f342b909-f3eb-44e9-8ee8-7d31cf3da3a0`
- the local-provider route is live in production behavior:
  - org turn `00390afc-afba-407b-9959-48f97c1fb708`
  - model invocations on that same turn are now `qwen2.5:72b`, not Anthropic:
    - `372d4287-3637-48f6-b259-38e314a3935c` completed
    - `9f0785a7-ef34-4883-861d-5f693779bc71` completed
    - current live invocation `3858b1fb-1417-4f07-9de6-d34521668884` is still `in_flight`
- fresh project creation succeeded under the new route:
  - project `68ff263f-d1db-4e95-81a4-a3cfe5ea47d2`
  - slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-33`
  - status `active`
  - canonical seeded bootstrap tasks `1-8` exist on the new project

### Current live seam

- the product is no longer blocked on Anthropic rate limits for org-session canaries
- rerun-33 has already created the project, but the org turn has not fully finalized/handoff-completed yet:
  - org session still shows `current_turn_id = 00390afc-afba-407b-9959-48f97c1fb708`
  - the org turn is still `in_progress`
  - there is not yet a `project`-scope async session row for project `68ff263f-d1db-4e95-81a4-a3cfe5ea47d2`
- that means the next runtime seam is no longer provider starvation or fresh-job claim order
- the next seam is org-turn completion / project-session handoff after a successful `project.create`

## 2026-03-25 03:20 MDT update

- new worker queue fixes landed in [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go):
  - `processAvailableJobs(...)` now calls `RecoverClaimedAgentTurnsWithoutLiveOwnership(...)` inline after `RecoverStaleClaims(...)` and before claiming fresh work
  - `claimPendingByFilter(...)` now adds an `agent_turn_claim_bias` so equal-priority fresh org/user work outranks async project continuation/bootstrap dispatches with message metadata `source=project_execution_continuation|project_bootstrap`
- new regression coverage landed in [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go):
  - `TestJobWorkerClaimPendingAgentTurnsPrioritizesFreshOrgWorkOverProjectContinuation`
  - follow-up coverage for inline recovery / stale triggered-turn recovery paths passed in the same patch cycle
- verification completed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(ClaimPendingAgentTurns(PrioritizesFreshOrgWorkOverProjectContinuation|ClaimsOnlyOneJobPerSession|AllowsMatchingPendingCurrentTurn)|RecoverClaimedAgentTurnsWithoutLiveOwnership(RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentPendingAttempt)?|RecoverStaleInProgressTriggeredTurns(RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage|FailsNonHeartbeatingClaimedAttemptWithoutRun|KeepsExistingPendingRetryJobForProjectSession|KeepsPendingRetryJobWithoutRetryCountForProjectSession))' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)' -count=1`

### Live notes from this stretch

- rerun-31 was briefly poisoned by an operator restart typo in `OTTERCAMP_MASTER_KEY`
  - failed retry turn: `064556b5-e739-45e2-baed-804d977498a0`
  - error: `decrypt ciphertext: cipher: message authentication failed`
  - that bad restart was corrected immediately afterward; current tmux env is back on the known-good key
- fresh rerun-32 org canary was posted through the real org session:
  - session `672477c4-4431-4969-bf7b-923253907972`
  - user message `17521b34-a7c2-4e8e-a412-5a791f451d58`
- live proof for the new claim ordering is in the worker tail:
  - startup recovered stale claims: `count=17`
  - first claimed `agent_turn` after restart was the fresh rerun-32 org message, not the old project continuation flood
  - claimed job: `624a2f9c-aded-4caf-aba4-06fb9eabf3a5`
  - created turn: `33d069e0-734e-4029-8aaf-5aec7c869349`
  - that turn ran and completed before the worker returned to idle
- rerun-32 did **not** create a project row yet
  - there is still no `speaker-pipeline-ops-validation-fresh-20260324-rerun-32*` project in `project`
  - the rerun-32 turn failed bounded by provider rate limit, not queue starvation
    - turn `33d069e0-734e-4029-8aaf-5aec7c869349`
    - status `failed`
    - error carries provider 429 with effective retry about `44m36s`
  - replacement pending retry job now exists:
    - job `558a97ab-28cb-44d6-8e8b-1cf19de1a514`
    - status `pending`
    - payload points at message `17521b34-a7c2-4e8e-a412-5a791f451d58`
    - `retry_count=1`
    - `run_after=2026-03-25 03:45:47.365279-06`
    - `rate_limit_jitter_applied=true`
- current org session state at this checkpoint:
  - `chat_session.current_turn_id = NULL`
  - session status remains `active`
  - worker is healthy and idle with `claimed=0`

### What is actually fixed vs. what remains

- fixed:
  - fresh org/user work is no longer starved behind equal-priority async project continuation jobs
  - non-heartbeating claimed `agent_turn` attempts are now recoverable during the regular claim loop instead of waiting for startup / the 24h cleanup pass
- not fixed:
  - stale async `project` continuation jobs from old canaries still appear able to re-enter the same pre-assistant stall once they finally get CPU time
  - the next runtime seam is that deeper project-continuation stall, not org claim starvation anymore

### Next live step

- wait for or force the bounded rerun-32 retry at `03:45:47 MDT`
- confirm whether it creates the rerun-32 project under the new queue behavior
- if rerun-32 still fails at org stage, inspect the org-turn outcome itself
- if rerun-32 succeeds, move directly into the fresh project bootstrap and continue the canary

## 2026-03-25 02:44 MDT update

- rerun-31 is still the active org-session canary:
  - org session `672477c4-4431-4969-bf7b-923253907972`
  - user message `f29b24cf-e27e-40fd-90ee-b5d1a1dd0337`
- bounded retry job is still healthy and pending:
  - job `f1ab8d27-8c98-4d37-9d05-902e393525c0`
  - status `pending`
  - `run_after=2026-03-25 02:54:51.032304-06`
  - effective delay at check time: about `10m07s`
  - payload still points at the correct rerun-31 user message with `retry_count=1`
- current org session state at the same checkpoint:
  - `chat_session.current_turn_id = NULL`
  - session status remains `active`
  - latest rerun-31 turn is still the expected failed rate-limit attempt:
    - turn `988225be-3ca3-4593-b3c1-44c996188b5e`
    - status `failed`
    - trigger message `f29b24cf-e27e-40fd-90ee-b5d1a1dd0337`
    - error carries the original provider 429 with `retry_after=42h48m15s`
- worker is otherwise idle and healthy while waiting for the capped retry window:
  - latest worker tail is just periodic claim scans and `no pending jobs`
  - there is no new stuck claimed job, no live orphaned turn, and no fresh queue ownership regression at this checkpoint
- no `speaker-pipeline-ops-validation-fresh-20260324-rerun-31*` project row exists yet

### Next live step

- watch rerun-31 at or just after `02:54:51 MDT`
- confirm the bounded retry actually claims, creates a fresh org turn, and either creates the rerun-31 project or exposes the next provider/runtime seam

## 2026-03-25 02:08 MDT update

- tmux session is still `codex-e2e-20260324`
  - pane `0` serve PID: `73551`
  - pane `1` worker PID: `73751`
- health was green before this patch set and the latest focused tests passed locally

### New fix: stale org-turn recovery now retries from the synthetic user continuation prompt, not the summary system message

- patched [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
  - `RecoverStaleInProgressTriggeredTurns(...)` now resolves an effective retry message before enqueuing a recovered retry
  - for async `organization` sessions whose persisted `trigger_message_id` points at a system continuation summary, worker now prefers the latest pending synthetic user message with:
    - `source=organization_continuation_resume`
    - `synthetic_user_message=true`
  - if no such message exists, it falls back to the latest pending user message, then finally the original trigger message
- rationale:
  - rerun-30 proved that stale cleanup did enqueue a retry after failing stale turn `69`
  - but it retried message `a36ac67a-0aa8-419b-9096-2d1c71e2a419`, which is the summary system message `551`, not the synthetic user continuation prompt
  - that retry completed immediately and left the org lane with no live rerun-30 project creation attempt

### New regression coverage

- patched [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
  - added `TestJobWorkerRecoverStaleInProgressTriggeredTurnsRequeuesOrganizationContinuationUsingPendingSyntheticUserMessage`
  - the test mirrors the rerun-30 shape:
    - async organization session
    - stale in-progress turn triggered by a summary system message
    - pending synthetic `organization_continuation_resume` user prompt in the same session
    - completed model invocation + streaming assistant stub
    - expected recovered retry job must target the synthetic user message with `retry_count=1`
  - added `TestJobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnershipRecoversCurrentInProgressAttemptWithoutModelOrRun`
  - this covers the newer live seam after redeploy:
    - claimed `agent_turn`
    - matching `current_turn_id`
    - current turn status `in_progress`
    - no model invocation
    - no run
    - expected result is recovery back to `pending`

### Verification completed

- `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RecoverStaleInProgressTriggeredTurns(RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage|KeepsExistingPendingRetryJobForProjectSession|KeepsPendingRetryJobWithoutRetryCountForProjectSession)|RequeueStrandedUserMessageTurns(IgnoresNewerFailedAssistantStub)?)' -count=1`
- `go test ./internal/controlplane -run 'TestTaskQueueProcessorHandleTaskCompletedEvent(IgnoresFlowTemplateRequiredFromParentAutoComplete|IgnoresFlowTemplateRequiredFromFollowOnQueue)' -count=1`
- follow-up worker verification after the second fix:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RecoverClaimedAgentTurnsWithoutLiveOwnership(RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentPendingAttempt)?|RecoverStaleInProgressTriggeredTurns(RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage|FailsNonHeartbeatingClaimedAttemptWithoutRun|KeepsExistingPendingRetryJobForProjectSession|KeepsPendingRetryJobWithoutRetryCountForProjectSession)|RequeueStrandedUserMessageTurns(IgnoresNewerFailedAssistantStub)?)' -count=1`

### Current rerun-30 live state at this checkpoint

- org session remains `672477c4-4431-4969-bf7b-923253907972`
- stale bad-root turn `69` is now failed:
  - turn `5099af79-fc13-46e0-b3e3-4204c279da87`
  - trigger message `a36ac67a-0aa8-419b-9096-2d1c71e2a419` (summary system message)
  - error: `recovered stale in-progress message turn without live job or execution; scheduling a fresh retry`
  - `current_turn_id` on the session is now `NULL`
- the stale cleanup retry that already ran before this patch was:
  - job `5e73af03-d643-4f35-882e-6ebf4de39c21`
  - payload message `a36ac67a-0aa8-419b-9096-2d1c71e2a419`
  - retry count `1`
  - job status `done`
- relevant surviving continuation messages:
  - `26d545be-64ac-48fe-bc93-cd173a6ebe9d`
    - role `user`
    - status `pending`
    - metadata `{"source":"organization_continuation_resume","synthetic_user_message":true}`
  - `a36ac67a-0aa8-419b-9096-2d1c71e2a419`
    - role `system`
    - status `final`
- no `speaker-pipeline-ops-validation-fresh-20260324-rerun-30*` project row exists yet

### Next live step

- rebuild and restart tmux under the newest worker binary
- verify rerun-30 claimed job `99f9c697-83cc-4750-9cf4-c9a7aa1849a6` now gets recovered even though its `current_turn_id` still matches
- then push a fresh org canary after the session is unwedged

### Live proof after redeploy

- rebuilt `./bin/ottercamp`
- respawned tmux panes under the known-good env
  - pane `0` serve PID now `37212`
  - pane `1` worker PID now `37211`
- health stayed green after restart:
  - `./bin/ottercamp health`
- rerun-30 stuck claimed/current-turn state is now cleared in production:
  - session `672477c4-4431-4969-bf7b-923253907972` now has `current_turn_id = NULL`
  - dead in-progress turn `19800404-4437-447a-97ac-0363361cf169` is now `failed`
    - error: `recovered stale in-progress message turn without live job or execution; scheduling a fresh retry`
  - stale claimed job `99f9c697-83cc-4750-9cf4-c9a7aa1849a6` is no longer pinning the session
    - final status: `dead_letter`
    - final error: `purged stale terminal message-attempt dispatch during claim`
- fresh org canary posted after the unwedging:
  - rerun-31 user message `f29b24cf-e27e-40fd-90ee-b5d1a1dd0337`
  - the session actually advanced under the live worker:
    - job `9d3ba816-3c9a-44dd-9a37-964ec2142443` ran and completed
    - worker then queued retry job `f1ab8d27-8c98-4d37-9d05-902e393525c0`
      - status `pending`
      - payload message `f29b24cf-e27e-40fd-90ee-b5d1a1dd0337`
      - retry count `1`
      - `rate_limit_jitter_applied=true`
- current live blocker is operational, not ownership/state cleanup:
  - worker tail shows provider rate limiting at `2026-03-25 02:11:44 MDT`
  - `retry_after` surfaced as roughly `42h48m`
  - because of that, rerun-31 is queued for a delayed retry rather than proving project creation immediately

## 2026-03-25 02:18 MDT update

- new worker pacing fix:
  - patched [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
    - `agentTurnRateLimitDelay(...)` now caps provider-supplied rate-limit retry hints at the existing worker backoff ceiling `agentTurnRateLimitBackoffCap` (`30m`)
    - rationale: a single provider 429 was parking rerun-31 behind a `42h48m` retry window, which is unacceptable for live canary progress
- new focused unit coverage:
  - [`internal/jobqueue/worker_test.go`](../internal/jobqueue/worker_test.go)
    - `TestAgentTurnRateLimitDelayCapsProviderHintAtBackoffCap`
    - `TestAgentTurnRateLimitDelayUsesProviderHintWhenBelowBackoffCap`
- verification passed:
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap))' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RecoverClaimedAgentTurnsWithoutLiveOwnership(RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentPendingAttempt)?|RecoverStaleInProgressTriggeredTurns(RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage|FailsNonHeartbeatingClaimedAttemptWithoutRun|KeepsExistingPendingRetryJobForProjectSession|KeepsPendingRetryJobWithoutRetryCountForProjectSession)|RequeueStrandedUserMessageTurns(IgnoresNewerFailedAssistantStub)?)' -count=1`
- next live step:
  - rebuild + restart tmux
  - verify rerun-31 pending retry no longer carries a multi-day effective delay

## 2026-03-25 02:24 MDT update

- follow-up rate-limit migration fix:
  - patched [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
    - `RejitterPendingRateLimitedAgentTurns(...)` now also clamps already-jittered pending rate-limited retries when their `run_after` still exceeds the `30m` worker backoff cap
    - the helper now takes the jitter state explicitly and always applies a final ceiling via `clampRateLimitedRunAfter(...)`
  - this addresses the live rerun-31 case where job `f1ab8d27-8c98-4d37-9d05-902e393525c0` was still parked about `1 day 18h` out because it had been scheduled before the earlier cap patch and already carried `rate_limit_jitter_applied=true`
- new coverage:
  - [`internal/jobqueue/worker_test.go`](../internal/jobqueue/worker_test.go)
    - `TestRejitteredRateLimitedRunAfterClampsOversizedRunAfter`
  - [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
    - `TestJobWorkerRejitterPendingRateLimitedAgentTurnsClampsAlreadyJitteredOversizedRunAfter`
- verification passed:
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RejitterPendingRateLimitedAgentTurns(ClampsAlreadyJitteredOversizedRunAfter)?|RecoverClaimedAgentTurnsWithoutLiveOwnership(RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentPendingAttempt)?|RecoverStaleInProgressTriggeredTurns(RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage|FailsNonHeartbeatingClaimedAttemptWithoutRun|KeepsExistingPendingRetryJobForProjectSession|KeepsPendingRetryJobWithoutRetryCountForProjectSession)|RequeueStrandedUserMessageTurns(IgnoresNewerFailedAssistantStub)?)' -count=1`
- next live step:
  - rebuild + restart tmux
  - confirm rerun-31 pending retry job `f1ab8d27-8c98-4d37-9d05-902e393525c0` is clamped down to the live `30m` ceiling instead of staying multi-day

## 2026-03-25 02:35 MDT update

- live proof for the worker-side clamp landed:
  - rerun-31 retry job `f1ab8d27-8c98-4d37-9d05-902e393525c0`
    - before repair: roughly `1 day 18h` out
    - after repair + redeploy: `run_after=2026-03-25 02:54:51 -06:00`
    - effective delay at check time: about `29m39s`
- final consistency fix:
  - patched [`internal/turn/engine.go`](../internal/turn/engine.go)
    - `rateLimitRetryDelay(...)` now also caps provider retry hints at `maxRateLimitBackoff` (`30m`)
    - this prevents future rate-limited retries from being enqueued with multi-day windows in the first place
- new turn coverage:
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
    - `TestHandleTurnJobRateLimitedCapsProviderHintAtMaxBackoff`
- verification passed:
  - `go test ./internal/turn -run 'TestHandleTurnJobRateLimited(CapsProviderHintAtMaxBackoff|EnqueuesRetryUsingProviderHint|UsesBackoffWhenNoRetryHint|RetryCapStopsRequeue)' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RejitterPendingRateLimitedAgentTurns(ClampsAlreadyJitteredOversizedRunAfter)?|RecoverClaimedAgentTurnsWithoutLiveOwnership(RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentPendingAttempt)?|RecoverStaleInProgressTriggeredTurns(RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage|FailsNonHeartbeatingClaimedAttemptWithoutRun|KeepsExistingPendingRetryJobForProjectSession|KeepsPendingRetryJobWithoutRetryCountForProjectSession)|RequeueStrandedUserMessageTurns(IgnoresNewerFailedAssistantStub)?)' -count=1`
- current live state:
  - queue/runtime ownership bugs are not the blocker anymore
  - rerun-31 is healthy and waiting on its bounded retry window
  - enabled provider inventory is still:
    - Anthropic `ed226b62...` rate_limited
    - Anthropic `660d1c99...` unavailable
    - Anthropic `f5407b75...` rate_limited
  - the only non-Anthropic connection is local Ollama `5cbe78b4...`, but it is still disabled in config even though `localhost:11434` is reachable and serving models

## 2026-03-25 01:18 MDT update

- tmux `codex-e2e-20260324` has been rebuilt and restarted again
  - pane `0` current PID: `18935`
  - pane `1` current PID: `18937`
- health passed after restart:
  - `./bin/ottercamp health`
    - `db=true`
    - `migrations=true`
    - `pgvector=true`
    - `storage=true`
- worker concurrency is currently back at `24` to drain backlog

### New fix: native `project.create` generated slugs now retry on collision

- patched `internal/tools/native/mutation_tools.go`
  - when the caller does not explicitly provide a slug and `project.create` gets `projectsvc.ErrSlugTaken`, native now retries with generated suffixed slugs instead of surfacing the conflict back to the model
  - explicit user-supplied slugs still fail immediately on `ErrSlugTaken`
- new focused tests:
  - `internal/tools/native/mutation_tools_test.go`
    - `TestProjectCreateRetriesGeneratedSlugAfterCollision`
    - `TestProjectCreateDoesNotRetryExplicitSlugAfterCollision`
  - `internal/tools/native/native_integration_test.go`
    - `TestIntegrationProjectCreateRetriesGeneratedSlugAfterCollision`
- verification passed:
  - `go test ./internal/tools/native -run 'TestProjectCreate(RetriesGeneratedSlugAfterCollision|DoesNotRetryExplicitSlugAfterCollision|NormalizesExecutionFirstDeliveryMode|NormalizesValidationDeliveryMode)' -count=1`
  - `go test -tags=integration ./internal/tools/native -run 'TestIntegrationProjectCreate(RetriesGeneratedSlugAfterCollision|NormalizesExecutionFirstDeliveryMode|NormalizesValidationDeliveryMode|ReusesArchivedSlugThroughProjectService)' -count=1`

### New fix: `controlplane.task-completed` no longer retries forever on non-runnable follow-ons

- patched `internal/controlplane/task_queue_processor.go`
  - `handleTaskCompletedEvent(...)` now consumes `tasksvc.ErrFlowTemplateRequired` from `processNextEligibleQueuedTask(...)` instead of returning an error that replays the same historical `task.status_changed` event forever
- rationale:
  - worker logs are still hot-looping on `consumer_name=controlplane.task-completed seq=1798643 error="task requires a flow template before it can be queued"`
  - live repro task:
    - project `9aa82752-a1b7-4c02-96ce-641ac7214914`
    - task `61` `Validate OC-13 evidence artifacts meet retention policy`
    - parent orchestration chain under task `25` still contains many draft planning-only `execution_spec` children without runnable flow attachment
  - replaying the same completed-task event forever is worse than consuming it once when the next follow-on is not runnable yet
- new focused test:
  - `internal/controlplane/task_queue_processor_test.go`
    - `TestTaskQueueProcessorHandleTaskCompletedEventIgnoresFlowTemplateRequiredFromFollowOnQueue`
- verification passed:
  - `go test ./internal/controlplane -run 'TestTaskQueueProcessorHandleTaskCompletedEvent(IgnoresFlowTemplateRequiredFromFollowOnQueue|AutoCompletesParentTask|CatchesUpDormantParentTasks|AutoCompletesBootstrapPlanningTasks|FailsBlockedTrackingRuns)' -count=1`
- this fix is local only right now; it has not yet been redeployed into the tmux runtime

### Current live rerun-30 state

- org session remains the long-lived async organization session:
  - session `672477c4-4431-4969-bf7b-923253907972`
- fresh rerun message:
  - message `b4b540af-97dc-4a78-90fa-84a07e1f52e7`
  - requested project name: `Speaker Pipeline Ops Validation Fresh 20260324 Rerun 30`
- current org turn:
  - turn `67` / `ac04a5de-4208-48f1-897c-0e380daf44f0`
  - status `in_progress`
- live model invocation on that turn:
  - invocation `d8cf0e5d-b8cd-4f65-9b61-2a58298973c9`
  - provider model `claude-haiku-4-5-20251001`
  - status `in_flight`
  - only streamed assistant content so far is the token `I'll`
- latest continuation messages:
  - `544`: `[Prompt input exceeded 64000-token guardrail - continuing in a new turn.]`
  - `545`: `[Context compressed - continuing in a new turn.]`
  - `546`: continuation summary correctly names the active rerun-30 request
  - `547`: synthetic direct-action continuation prompt
  - `548`: assistant message still `streaming` with content `I'll`
- no rerun-30 project row exists yet

## 2026-03-25 01:12 MDT update

- current repo HEAD still: `3879e16e` (`3879e16e85595b7734d14885a41b061dd70b9626`)
- active tmux session remains `codex-e2e-20260324`
  - pane `0`: `serve`
  - pane `1`: `worker`
- latest live pane PIDs before the next restart:
  - serve: `98918`
  - worker: `68808`
- health was green before this update:
  - `./bin/ottercamp health`
    - `pgvector=true`
    - `storage=true`
    - `db=true`
    - `migrations=true`

### New fix: native `project.create` now self-heals generated slug collisions

- patched `internal/tools/native/mutation_tools.go`
  - `handleProjectCreate(...)` now distinguishes explicit slug input from auto-generated slug input
  - when the user did not provide a slug and project creation returns `projectsvc.ErrSlugTaken`, native `project.create` now retries with generated suffixed slugs instead of surfacing the collision back to the model
  - explicit slugs still preserve the old behavior and return `ErrSlugTaken` immediately
  - the same file already normalizes `delivery_mode: execution_first|validation -> gated`; this slug retry path is layered on top of that fix
- rationale:
  - rerun-29 showed a real org-session loop where the model repeatedly called `project.create`
  - every attempt came back as `project slug is already taken`
  - the name was canonical and the model had not explicitly supplied a required fixed slug, so the runtime should have recovered by minting a suffixed slug instead of spinning

### New focused tests

- `internal/tools/native/mutation_tools_test.go`
  - added `TestProjectCreateRetriesGeneratedSlugAfterCollision`
  - added `TestProjectCreateDoesNotRetryExplicitSlugAfterCollision`
- `internal/tools/native/native_integration_test.go`
  - added `TestIntegrationProjectCreateRetriesGeneratedSlugAfterCollision`

### Verification that passed

- `go test ./internal/tools/native -run 'TestProjectCreate(RetriesGeneratedSlugAfterCollision|DoesNotRetryExplicitSlugAfterCollision|NormalizesExecutionFirstDeliveryMode|NormalizesValidationDeliveryMode)' -count=1`
- `go test -tags=integration ./internal/tools/native -run 'TestIntegrationProjectCreate(RetriesGeneratedSlugAfterCollision|NormalizesExecutionFirstDeliveryMode|NormalizesValidationDeliveryMode|ReusesArchivedSlugThroughProjectService)' -count=1`

### Current live seam

- worker pane is still showing repeated background consumer failures on:
  - `consumer_name=controlplane.task-completed`
  - `seq=1798643`
  - `error="task requires a flow template before it can be queued"`
- this is not the same seam as the rerun-29 org-session slug loop
- next live step after rebuilding is a fresh org canary (`rerun-30` or later) to confirm:
  - project creation no longer loops on `project slug is already taken`
  - bootstrap task seeding still happens
  - the bootstrap `task.create` contamination fix is finally exercised on a fresh project path under the newest binary

## 2026-03-25 00:06 MDT update

- current repo HEAD still: `3879e16e` (`3879e16e85595b7734d14885a41b061dd70b9626`)
- active tmux session remains `codex-e2e-20260324`
  - pane `0`: `serve`
  - pane `1`: `worker`
- latest live pane PIDs after clean env respawn:
  - serve: `98918`
  - worker: `98919`
- health is green after restart:
  - `./bin/ottercamp health`
    - `pgvector=true`
    - `storage=true`
    - `db=true`
    - `migrations=true`

### New fix: blocked kickoff `session.create` now ends the org turn cleanly

- patched `internal/turn/engine.go`
  - after blocked tool results are appended, the engine now checks `shouldStopAfterBlockedProjectKickoffSessionCreate(...)`
  - when Frank is in an `organization` kickoff turn with a locked `projectIdentity`, and the blocked follow-on tool is specifically `session.create` with the `handoff-only` guard error, the turn now stops immediately
  - this lets normal `completeTurn -> ensureProjectKickoffHandoff(...)` create the canonical async project session + synthetic Lori handoff, instead of burning a fourth org-session model invocation
- rationale:
  - live rerun-25 showed the old path:
    - `project.create` succeeded
    - model then called `session.create`
    - tool correctly returned `project kickoff is now handoff-only ...`
    - a fourth org-session model invocation then failed with `model_error: invalid status transition`
    - worker cleanup later failed the turn with `worker cleanup failed stale in_flight model invocation without live in-progress turn`
  - the new patch removes that extra invocation on the `session.create` path entirely

### New focused tests

- `internal/turn/engine_test.go`
  - added `TestShouldStopAfterBlockedProjectKickoffSessionCreate`
- existing focused unit coverage still passes for the adjacent org-kickoff logic:
  - `TestBuildSyntheticProjectKickoffHandoffPrefersFreshProjectContext`
  - `TestContinuationTurnUsesDeterministicActiveRequestSummaryForAsyncOrganizationSession`
  - `TestContinuationTurnAppendsDirectActionPromptForAsyncOrganizationSession`
  - `TestShouldAppendSyntheticUserPromptIgnoresDuplicateOnCompletedTurn`
  - `TestShouldAppendSyntheticUserPromptSkipsDuplicatePendingSource`
  - `TestProjectCreateStateMachinePreventsConflictReentryAfterSuccess`

### Verification that passed

- `go test ./internal/turn -run 'Test(ShouldStopAfterBlockedProjectKickoffSessionCreate|BuildSyntheticProjectKickoffHandoffPrefersFreshProjectContext|ContinuationTurnUsesDeterministicActiveRequestSummaryForAsyncOrganizationSession|ContinuationTurnAppendsDirectActionPromptForAsyncOrganizationSession|ShouldAppendSyntheticUserPrompt(IgnoresDuplicateOnCompletedTurn|SkipsDuplicatePendingSource))' -count=1`
- `go test ./internal/turn -run 'TestProjectCreateStateMachinePreventsConflictReentryAfterSuccess' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`

### Integration-test note

- `go test -tags=integration ./internal/turn -run 'TestTurnEngineIntegration(KickoffBlockedSessionCreateStopsAndBackfillsProjectHandoff|KickoffBlocksFollowOnToolsAfterProjectCreate|BackfillsMissingProjectKickoffHandoffAfterProjectCreate)' -count=1`
  - could not be used as a gate because `internal/turn/engine_integration_test.go` already has unrelated compile failures in this worktree
  - examples from the current build:
    - `undefined: mustProjectWorkspaceRoot`
    - `undefined: taskcheckpoint.ApplyRecoveryFileWriteCheckpoint`
    - `undefined: mustCreateReviewFlowTemplate`
    - `undefined: mustCreateBootstrapWorkerAgent`

### Live rerun-25 trace that motivated the patch

- org session: `672477c4-4431-4969-bf7b-923253907972`
- failing turn: `51ccdf1a-2e29-4c89-be68-fb2dfc54764d` (`turn_number=57`)
- trigger message sequence:
  - `53c2a4c0-0c4b-4123-9103-5276189db438`
    - continuation summary: `Active organization request: Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 25 ...`
  - `fd917a50-415b-4ea9-9522-8734976c0fcb`
    - synthetic direct-action org continuation prompt
  - `021e1b9c-06f5-4451-b09c-608abbb7224f`
    - assistant: `I'll create the project now.`
  - `376cf34b-8669-4c94-b45b-fa59f43338c2`
    - `project.create` success
    - project id `f30714a2-6781-4acb-88be-a9886bd01cd5`
    - slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-25`
  - `b534b5a5-9b48-4469-8ae0-896f77f43b29`
    - assistant: `Now creating the project session and handoff:`
  - `3ebaf67a-075c-414b-9cfe-da219c126fd0`
    - blocked `session.create` tool_result:
      - `project kickoff is now handoff-only: project already created as slug=... Provide Lori the handoff summary and end the turn without additional tool use`
  - `84647330-5982-4b28-84d5-5b85c1e1a880`
    - empty assistant placeholder for the next invocation
    - eventually marked failed
- model invocations on turn 57:
  - `f8e12c07-9978-43ab-8465-30a6a2033bdc` `completed`
  - `f230348f-7a88-4b15-8f85-260d8aad9dc9` `completed`
  - `b07cd206-f08c-43cf-8bab-8155ff2898eb` `completed`
  - `4c07714a-465c-4c42-ab9e-fca1146edc33` `failed`
    - `error_code=model_error`
    - `error_message=invalid status transition`
- project row for rerun-25 exists
- project session for rerun-25 was never created
- seeded bootstrap tasks `1-8` already existed on rerun-25 and no duplicate `9+` tasks had appeared at the time of the last DB check

### Live replay under the patched binary

- logged in through the real API:
  - `POST /v1/auth/login`
  - admin token issued successfully for `s@swh.me`
- posted fresh rerun-26 through the real chat API:
  - `POST /v1/chat-sessions/672477c4-4431-4969-bf7b-923253907972/messages`
  - created message:
    - id `29cde061-2952-4695-8673-724394412ace`
    - sequence `484`
    - status `pending`
  - request content:
    - `Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 26 ...`
- queue row for rerun-26 exists:
  - job `5fcd7e2d-ef5f-4205-93fc-2df0a6599162`
  - `job_type=agent_turn`
  - `status=pending`
  - `attempts=0`
- current blocker for live proof is worker pressure, not the org kickoff code path yet:
  - `agent_turn` counts at last check:
    - `claimed=9`
    - `pending=56`
  - worker tail still shows:
    - `job queue: no execution slots available inflight=12 capacity=12`
- after a worker restart to `OTTERCAMP_WORKER_CONCURRENCY=16`, rerun-26 was still pending on the immediate follow-up poll
- as of this update:
  - no rerun-26 turn row exists yet
  - no rerun-26 project row exists yet
  - so the new patch is built, deployed, and queued for live validation, but not yet proven in production traffic because the org replay has not been claimed

## 2026-03-24 live handoff update

- current repo HEAD: `3879e16e` (`3879e16e85595b7734d14885a41b061dd70b9626`)
- active tmux session remains `codex-e2e-20260324`
  - pane `0`: `serve`
  - pane `1`: `worker`
- service health is green again after hard respawn
- worker is currently running at `OTTERCAMP_WORKER_CONCURRENCY=8`
  - this is an operational mitigation while tracing a worker-pressure bug that still reproduces at `24`

### 2026-03-24 23:02 MDT update

- fixed org/project continuation-summary rooting in `internal/turn/engine.go`
  - `continueTurn` no longer asks the summary model to summarize the last three user messages across the whole long-lived session for async/sync org/project continuations
  - it now prefers the current turn's `initialMessageID` user message as the `HumanMessages` input for continuation-summary generation on `organization` and `project` sessions
  - this is the direct code fix for rerun-18's failure mode where prompt compression continued from an older escalation thread instead of the fresh create-project request
- new focused regression in `internal/turn/engine_test.go`
  - `TestContinuationTurnUsesCurrentTriggerMessageForOrganizationContinuationSummary`
- focused verification that passed:
  - `go test ./internal/turn -run 'Test(ContinuationTurnUsesCurrentTriggerMessageForOrganizationContinuationSummary|ContinuationTurnOnContextCompressed|ContinuationTurnCapsSummaryBeforeReusingAsHistoryRoot)' -count=1`
- rebuilt `./bin/ottercamp`
- hard-respawned tmux panes after the patch
  - pane list now:
    - `0 85943 ottercamp 0`
    - `1 85942 ottercamp 0`
    - `2 19786 zsh 0`
    - `3 18643 zsh 0`
- `./bin/ottercamp health` is green after restart
- fresh live replay under the patched binary:
  - same org session: `672477c4-4431-4969-bf7b-923253907972`
  - fresh user message for rerun-19:
    - message `f778edcf-ddd7-404e-b4af-5cd3f09c6fe5`
    - content starts `Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 19 ...`
  - live result so far:
    - the rerun-19 message is still `pending`
    - no new turn has been created for trigger `f778edcf-ddd7-404e-b4af-5cd3f09c6fe5` yet
    - no project row exists yet for slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-19`
  - reason this is not yet a product-level pass/fail on the new patch:
    - the worker is still backlog-bound at concurrency `8`
    - live worker tail at the same time is dominated by:
      - `job queue: no execution slots available inflight=8 capacity=8`
      - unrelated project/project_task retries being claimed and requeued
    - so the continuation-rooting fix is live, but it has not yet been exercised in production on rerun-19 because the org-session message has not been scheduled into a turn yet
- narrowed current seam:
  - not the rerun-18 continuation-root selection anymore at code level
  - the immediate live blocker is worker queue drain / fairness for fresh org-session messages while the system is saturated with unrelated project/project_task retries

### 2026-03-24 23:05 MDT update

- rerun-19 proved the first org-session continuation fix in live traffic
  - org session: `672477c4-4431-4969-bf7b-923253907972`
  - rerun-19 user message: `f778edcf-ddd7-404e-b4af-5cd3f09c6fe5`
  - live turns:
    - turn `44` / `2bdb8786-01d4-4968-b247-3b3515982051`
      - `completed`
      - trigger message `f778edcf-ddd7-404e-b4af-5cd3f09c6fe5`
      - output only:
        - `[Prompt input exceeded 64000-token guardrail - continuing in a new turn.]`
        - `[Context compressed - continuing in a new turn.]`
    - turn `45` / `c651c3d7-5b19-4afb-b12d-31fa3e65ca3c`
      - `completed`
      - trigger message `8a24f82a-77ca-4292-b106-67cfc41a70df`
      - continuation summary now correctly referenced the fresh rerun-19 project-create request rather than the older escalation threads
  - that confirms the first patch worked: the continuation-summary model is now rooted to the current trigger message for org sessions
- rerun-19 also exposed the next org-session bug:
  - after the corrected continuation summary, the org continuation still ended with generic assistant chat:
    - assistant message `d7a066e6-8f43-417c-b782-045e437839b0`
    - content: `I'm ready to help. What do you need?`
  - no project row was created for slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-19`
- second org-session continuation fix landed immediately after that:
  - `internal/turn/engine.go`
    - added `shouldAppendOrganizationContinuationActionPrompt`
    - added `buildOrganizationContinuationActionPrompt`
    - `appendContinuationSummaryAndAction` now appends an explicit direct-action user prompt for async `organization` sessions
  - new focused regression in `internal/turn/engine_test.go`
    - `TestContinuationTurnAppendsDirectActionPromptForAsyncOrganizationSession`
  - focused verification that passed:
    - `go test ./internal/turn -run 'Test(ContinuationTurnUsesCurrentTriggerMessageForOrganizationContinuationSummary|ContinuationTurnAppendsDirectActionPromptForAsyncOrganizationSession|ContinuationTurnOnContextCompressed|ContinuationTurnCapsSummaryBeforeReusingAsHistoryRoot)' -count=1`
  - rebuilt `./bin/ottercamp`
  - hard-respawned tmux panes again
    - pane list now:
      - `0 38639 ottercamp 0`
      - `1 38638 ottercamp 0`
      - `2 19786 zsh 0`
      - `3 18643 zsh 0`
  - `./bin/ottercamp health` is green after restart
- fresh live replay after the second org-session patch:
  - rerun-20 user message: `90daea73-d824-4561-9f28-18f61a96cc44`
  - requested slug: `speaker-pipeline-ops-validation-fresh-20260324-rerun-20`
  - current dispatch row:
    - job `eefeb0b8-93b2-4f7d-b822-97ec78ae1406`
    - `job_type=agent_turn`
    - `status=pending`
    - `attempts=0`
  - current live result:
    - message `90daea73-d824-4561-9f28-18f61a96cc44` is still `pending`
    - no turn exists yet for rerun-20
    - no project row exists yet for slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-20`
- current narrowed blocker after both org-session prompt fixes:
  - worker saturation / fairness is again the immediate live seam
  - rerun-20 is waiting behind unrelated in-flight work even at worker concurrency `8`
  - if rerun-20 eventually claims and still fails to create the project, the next bug is in org-session action execution after the new synthetic prompt

### 2026-03-24 23:15 MDT update

- rerun-20 fully exercised the new org continuation path in live traffic
  - user message: `90daea73-d824-4561-9f28-18f61a96cc44`
  - turn `46` / `7aba32c0-f4af-4789-94f2-ea898aa9f356`
    - `completed`
    - output:
      - `[Prompt input exceeded 64000-token guardrail - continuing in a new turn.]`
      - `[Context compressed - continuing in a new turn.]`
  - turn `47` / `94a28a52-169b-4fe8-914e-362f394d16f7`
    - `completed`
    - continuation summary rooted to rerun-20
    - appended the new synthetic org continuation prompt
    - the assistant then took direct project-create action instead of falling back to `I'm ready`
- rerun-20 exposed the next concrete runtime bug:
  - the first real `project.create` tool call used the requested slug exactly:
    - assistant message `ec77450a-f071-4b7f-a70b-093403638933`
    - tool args included:
      - `slug: speaker-pipeline-ops-validation-fresh-20260324-rerun-20`
      - `delivery_mode: execution_first`
  - all subsequent `project.create` retries also failed
  - every tool result reported:
    - `project slug is already taken`
  - but there was no project row for rerun-20
- root cause found:
  - this was not a real slug collision
  - `internal/tools/native/mutation_tools.go` was passing `delivery_mode=execution_first` through to `projectsvc.Create`
  - the project table only accepts `gated|continuous|scheduled`
  - manual CLI reproduction proved the distinction:
    - `./bin/ottercamp project create ... --slug speaker-pipeline-ops-validation-fresh-20260324-rerun-20`
      - succeeded immediately when using the standard CLI/project handler path
    - the org-session native tool path failed because of the delivery mode alias, not because the slug was actually taken
- fix landed:
  - `internal/tools/native/mutation_tools.go`
    - added `normalizeProjectDeliveryModeInput`
    - `project.create` now maps `execution_first -> gated`
    - `project.update` also normalizes the same alias
  - tests added:
    - `internal/tools/native/mutation_tools_test.go`
      - `TestProjectCreateNormalizesExecutionFirstDeliveryMode`
    - `internal/tools/native/native_integration_test.go`
      - `TestIntegrationProjectCreateNormalizesExecutionFirstDeliveryMode`
  - verification that passed:
    - `go test ./internal/tools/native -run 'TestProjectCreateNormalizesExecutionFirstDeliveryMode' -count=1`
    - `go test -tags=integration ./internal/tools/native -run 'TestIntegrationProjectCreate(NormalizesExecutionFirstDeliveryMode|ReusesArchivedSlugThroughProjectService)' -count=1`
  - rebuilt `./bin/ottercamp`
  - hard-respawned tmux again
    - pane list now:
      - `0 57267 ottercamp 0`
      - `1 57268 ottercamp 0`
      - `2 19786 zsh 0`
      - `3 18643 zsh 0`
  - `./bin/ottercamp health` is green after restart
- fresh live replay under the alias fix:
  - rerun-21 user message: `7e60f6eb-7fe5-45a4-8f24-50caba27a610`
  - requested slug: `speaker-pipeline-ops-validation-fresh-20260324-rerun-21`
  - current dispatch row:
    - job `5aaaa749-27cb-4960-9c7b-7751d90139f5`
    - `status=pending`
    - `attempts=0`
  - current live state:
    - no rerun-21 turn yet
    - no rerun-21 project row yet
- current blocker after the project-create alias fix:
  - org continuation and direct-action prompting are now good enough to reach real `project.create`
  - the fresh rerun-21 request is currently waiting behind the global async queue backlog
  - queue state at last check:
    - `agent_turn pending runnable = 37`
    - `agent_turn claimed = 4`
  - so the next live seam is queue drain / worker fairness again, not the org-session prompt path or the bogus slug-conflict bug

### 2026-03-24 23:31 MDT update

- fixed stale synthetic org prompt suppression in `internal/turn/engine.go`
  - `shouldAppendSyntheticUserPrompt` now ignores duplicate pending synthetic prompts when they belong to an already terminal turn
  - this was the direct fix for rerun-21, where turn `49` completed with only the continuation summary and generic `I'm ready` because the old rerun-20 synthetic org prompt was still pending on a completed turn
- new focused regression in `internal/turn/engine_test.go`
  - `TestShouldAppendSyntheticUserPromptIgnoresDuplicateOnCompletedTurn`
- fixed async organization continuation summaries to stop fabricating completed work
  - `internal/turn/engine.go`
    - added `organizationActiveRequestContinuationSummary`
    - async `organization` continuations now bypass the summary model and use a deterministic summary rooted to the current user request:
      - `Active organization request: <normalized user request>`
  - this was the direct code fix for rerun-22, where the continuation summary incorrectly said `# Project Created: Speaker Pipeline Ops Validation Fresh 20260324 Rerun 22` even though no such project row existed
- new focused regression in `internal/turn/engine_test.go`
  - `TestContinuationTurnUsesDeterministicActiveRequestSummaryForAsyncOrganizationSession`
- focused verification that passed:
  - `go test ./internal/turn -run 'Test(ContinuationTurnUsesDeterministicActiveRequestSummaryForAsyncOrganizationSession|ContinuationTurnAppendsDirectActionPromptForAsyncOrganizationSession|ContinuationTurnUsesCurrentTriggerMessageForOrganizationContinuationSummary|ShouldAppendSyntheticUserPrompt(IgnoresDuplicateOnCompletedTurn|SkipsDuplicatePendingSource))' -count=1`

### 2026-03-24 23:32 MDT update

- fixed worker startup cleanup so it no longer crashes on draft auto-complete candidates that still lack a flow template
  - `internal/worker/worker.go`
    - `startupCleanupProjectDrafts` now treats `tasksvc.ErrFlowTemplateRequired` like an ineligible draft for both:
      - dormant orchestration-parent auto-complete
      - satisfied draft auto-complete
    - this prevents the worker from aborting at startup with:
      - `worker startup project cleanup: task requires a flow template before it can be queued`
- new regression in `internal/worker/worker_test.go`
  - `TestStartupCleanupProjectDraftsSkipsSatisfiedDraftWithoutFlowTemplate`
- focused verification that passed:
  - `go test ./internal/worker -run 'Test(StartupCleanupProjectDraftsSkipsSatisfiedDraftWithoutFlowTemplate|DraftTaskAutoCompletesWhenPlanningAndOutcomeAreSatisfied|DraftTaskAutoCompletesRejectsBroadTask|DraftTaskAutoCompletesRejectsIncompletePlanning)' -count=1`
  - `go test ./internal/worker -count=1`
- rebuilt `./bin/ottercamp`
- tmux panes after the latest successful restart:
  - `0 43010 ottercamp 0`
  - `1 59468 ottercamp 0`
  - `2 19786 zsh 0`
  - `3 18643 zsh 0`
- health is green after the latest restart

### 2026-03-24 23:33 MDT live state

- rerun-22 final diagnosis is now confirmed from the DB:
  - turn `50` compressed the original rerun-22 request
  - turn `51` included the synthetic org continuation prompt
  - but the continuation summary itself falsely claimed the project had already been created, which is what the new deterministic-summary fix addresses
  - there is still no project row for slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-22`
- fresh replay under the patched binary:
  - rerun-23 user message: `f0bbe508-7d95-41c7-b144-cbb87021cc80`
  - slug requested: `speaker-pipeline-ops-validation-fresh-20260324-rerun-23`
  - current dispatch row:
    - job `08c68d5b-ce49-4b67-8d6a-b7bcc224c131`
    - `status=pending`
    - `attempts=0`
  - no rerun-23 turn exists yet
  - no project row exists yet for rerun-23
- current live blocker has narrowed again:
  - not org-summary hallucination anymore at the code level
  - not worker startup cleanup anymore
  - immediate live seam is worker claim/drain starvation
    - with worker concurrency `8`, rerun-23 stayed behind eight claimed `project_task` supervisor-recovery jobs
    - after raising worker concurrency to `12`, the worker remained healthy and recovered three stale in-progress triggered turns on startup, but rerun-23 is still pending
    - latest queue snapshot:
      - `agent_turn claimed = 13`
      - `agent_turn pending = 47`
    - worker tail still shows:
      - `job queue: no execution slots available inflight=12 capacity=12`
- next critical-path question:
  - whether the claimed supervisor-recovery task lanes are making forward progress or whether they are consuming slots without enough throughput, starving fresh org-session work even after the startup fixes

### 2026-03-24 23:32 MDT live proof

- rerun-23 finally exercised the fixed org continuation path in production:
  - user message: `f0bbe508-7d95-41c7-b144-cbb87021cc80`
  - turn `52` / `85ec1897-fe09-4f96-808d-60a3c3830c51`
    - `completed`
    - output only:
      - `[Prompt input exceeded 64000-token guardrail - continuing in a new turn.]`
      - `[Context compressed - continuing in a new turn.]`
  - turn `53` / `055d0ffe-d747-46ea-a8cd-f0bbd654e024`
    - continuation summary is now correct in live traffic:
      - `[Continuation summary] Active organization request: Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 23 ...`
    - the synthetic org continuation prompt was appended again as expected
    - the assistant then took direct action:
      - assistant: `I'll create the project now.`
      - `project.create` succeeded
      - tool result project id: `6d34b192-2cdc-4437-a1f7-fa8e6952bb1b`
      - tool result slug: `speaker-pipeline-ops-validation-fresh-20260324-rerun-23`
    - DB proof:
      - project row now exists:
        - `6d34b192-2cdc-4437-a1f7-fa8e6952bb1b|speaker-pipeline-ops-validation-fresh-20260324-rerun-23|active|2026-03-24 23:31:44.897217-06`
    - after project creation, the runtime emitted the expected handoff guards:
      - `[Project identity locked: ...]`
      - `[Kickoff handoff requirement: provide Lori a complete handoff summary ...]`
    - the assistant is now streaming the Lori handoff markdown in the same turn
- practical conclusion:
  - the org continuation path is now live-fixed through the exact seam that was broken:
    - no stale synthetic-prompt suppression
    - no fabricated `project created` continuation summary
    - no bogus `execution_first` slug-collision failure
    - direct `project.create` succeeded in the live org session
- current next seam after this proof:
  - not org-session continuation anymore
  - next thing to watch is whether the Lori handoff / async project bootstrap lane for project `6d34b192-2cdc-4437-a1f7-fa8e6952bb1b` proceeds cleanly under the same backlog-heavy worker

### 2026-03-24 23:40 MDT update

- fixed project kickoff handoff instructions in `internal/turn/engine.go`
  - `buildSyntheticProjectKickoffHandoffFromRequest` now explicitly tells Lori/Frank that project creation already seeded the canonical bootstrap task tree:
    - Bootstrap governance gate
    - Bind repo and environment
    - Staff the project
    - Decompose workstreams into bounded tasks
    - Validate task sizing and dependencies
    - Attach and validate flow templates
    - Select first-wave runnable tasks
    - Request and record Frank sign-off
  - it now explicitly says:
    - do not recreate or duplicate those seeded bootstrap tasks
    - reuse the existing bootstrap task tree
    - persist setup with `bootstrap.setup.persist` instead of creating parallel replacements
- new focused regression in `internal/turn/engine_test.go`
  - `TestBuildSyntheticProjectKickoffHandoffPrefersFreshProjectContext`
  - now asserts the handoff contains the seeded-task-tree / no-duplicate-task guidance
- focused verification that passed:
  - `go test ./internal/turn -run 'TestBuildSyntheticProjectKickoffHandoffPrefersFreshProjectContext' -count=1`
- rebuilt `./bin/ottercamp`
- hard-respawned tmux panes again
  - current pane list after this restart:
    - `0 65969 ottercamp 0` (serve pid may differ after next respawn; verify live)
    - `1 27424 ottercamp 0` earlier, then later respawned again under the latest build
  - health is green after restart

### 2026-03-24 23:40 MDT live bootstrap findings

- rerun-23 project id: `6d34b192-2cdc-4437-a1f7-fa8e6952bb1b`
- its async project session: `5588a1aa-92ba-45a3-8a9f-076e1589be21`
- live proof from that session:
  - turn `1` failed after Frank duplicated bootstrap work instead of reusing the seeded bootstrap tasks
  - he created duplicate validation tasks `9-13`
  - task-create failures included:
    - `planning playbook state is required before planning artifacts can be recorded`
    - bounded-size rejections for oversized tasks
  - the project bootstrap runtime then recovered into turn `2` and resumed from persisted state
- DB state proving the duplicate-task seam:
  - seeded canonical bootstrap tasks `1-8` already exist on the project
  - duplicate task rows `9-13` also exist and show `bootstrap_first_wave_selected` metadata
  - session metadata currently reports bootstrap:
    - `phase = first_wave_selected`
    - `status = active`
- this is what led to the new kickoff handoff patch above

### 2026-03-24 23:41 MDT live queue state

- fresh rerun-24 org request is in queue under the newest kickoff handoff guidance:
  - message `5564c083-8709-493d-b80c-44b7e5104f79`
  - dispatch job `524b3b0b-f635-47f4-9e03-45cb3dec32ee`
  - current state: `pending`
  - no rerun-24 project row exists yet
- immediate live blocker remains worker slot pressure, not the org or kickoff handoff path at code level
  - worker tail is still repeatedly showing:
    - `job queue: no execution slots available inflight=12 capacity=12`
  - rerun-24 has not claimed yet, so the seeded-bootstrap-task handoff fix is not live-proven on a fresh project turn yet

### 2026-03-24 23:45 MDT update

- fixed another native project-create delivery-mode alias regression in `internal/tools/native/mutation_tools.go`
  - `normalizeProjectDeliveryModeInput` now maps both:
    - `execution_first -> gated`
    - `validation -> gated`
- this was discovered from rerun-24 live traffic:
  - the assistant tool-call metadata on turn `55` showed:
    - `delivery_mode: "validation"`
  - each `project.create` attempt then failed with the same fake:
    - `project slug is already taken`
  - manual CLI reproduction with the same slug still succeeded immediately, proving it was not a real slug collision
- new focused tests:
  - `internal/tools/native/mutation_tools_test.go`
    - `TestProjectCreateNormalizesValidationDeliveryMode`
  - `internal/tools/native/native_integration_test.go`
    - `TestIntegrationProjectCreateNormalizesValidationDeliveryMode`
- verification that passed:
  - `go test ./internal/tools/native -run 'TestProjectCreateNormalizes(ExecutionFirst|Validation)DeliveryMode' -count=1`
  - `go test -tags=integration ./internal/tools/native -run 'TestIntegrationProjectCreate(NormalizesExecutionFirstDeliveryMode|NormalizesValidationDeliveryMode)' -count=1`
- rebuilt `./bin/ottercamp` and restarted tmux again after the patch

### 2026-03-24 23:46 MDT live org-session state

- rerun-24 details:
  - turn `54` compressed the request
  - turn `55` used the correct active-request continuation summary
  - it then failed only because the native tool path still rejected `delivery_mode: validation`
  - there is still no rerun-24 project row
- rerun-25 fresh replay has been queued under the new delivery-mode fix:
  - message `0a3eb2c7-0892-4cd4-b87b-e44a7ead8fc7`
  - slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-25`
  - at last check it had not yet claimed into a turn

### 2026-03-24 22:47 MDT update

- OC-27 is now `done`
  - task row: `27|done||Validation execution: test pipeline capacity rejection`
  - review session `2f07b5c0-f7dc-4e4b-b2ef-a66319377e97` is now `closed`
  - final retry turn `e3301bb5-ac29-43c3-8440-bb6410d08894` ended `cancelled` with `stop_reason=session_closed`
  - turn-owned run/model rows completed before the close:
    - run `66ca2818-219f-47d7-b6ce-6a0f1cdc7fad` `completed`
    - model invocation `0a991d83-741c-4835-aa18-8086b06c319d` `completed`
- practical conclusion from the live canary:
  - rerun-16 has now gone end-to-end to all tasks `done`
  - the remaining product issue is worker pressure / fairness under high backlog and higher concurrency, not the OC-27 review lane itself
- fresh create follow-up:
  - a lower-level CLI create succeeded for rerun-17:
    - project `88ef661b-70cb-4bb9-afcc-3c4704f2c1f0`
    - slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-17`
    - it seeded the canonical bootstrap task tree (`tasks 1-8`), but did not itself prove the full Frank handoff path
  - the higher-level org-session create for rerun-18 exposed a new control-plane seam:
    - org session: `672477c4-4431-4969-bf7b-923253907972`
    - pending user message: `345c3063-ff8b-4785-b99d-a4c079b9392b`
    - initial dispatch job: `a49a2565-70d6-4324-846c-513c098c8f8b`
    - rerun-18's own turn actually did run first:
      - turn `35835681-7514-4c8b-801a-dc9225bf19b8`
      - trigger message `345c3063-ff8b-4785-b99d-a4c079b9392b`
      - it completed immediately with only guardrail/system outputs:
        - `[Prompt input exceeded 64000-token guardrail - continuing in a new turn.]`
        - `[Context compressed - continuing in a new turn.]`
    - after that terminal turn existed, the dispatch job was correctly purged as stale:
      - `purged stale terminal message-attempt dispatch during claim`
    - meanwhile the org session also advanced and completed an unrelated turn:
      - turn `13afd1c4-593c-402d-92f7-fa29a9eaad88`
      - trigger message `a8d0c019-8c10-4080-87f0-2c87f0b940c2`
    - net effect:
      - rerun-18 project was not created
      - the rerun-18 user message is still `pending`
  - current next bug after rerun-16 success:
    - organization-session prompt-compression continuation is not preserving or replaying the pending create-project request cleanly
    - this is separate from the worker-pressure / task-lane issue that blocked OC-27

### 2026-03-24 22:44 MDT update

- new worker fixes landed in this stretch:
  - `internal/jobqueue/worker.go`
    - `RecoverStaleInProgressTriggeredTurns` now also fails `project_task` turns older than the heartbeat window when they have no run, no model invocation, and no backing pending/claimed `agent_turn` job
    - removed the forced 5-second heartbeat clamp for `agent_turn` claims; heartbeat now uses the stale-threshold-derived interval again (`projectBootstrapStaleThreshold / 3`, currently `40s`)
  - `internal/jobqueue/worker_integration_test.go`
    - added `TestJobWorkerRecoverStaleInProgressTriggeredTurnsFailsOrphanedAttemptWithoutJobRunOrInvocation`
  - `internal/jobqueue/worker_test.go`
    - added `TestClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp`
- verification that passed:
  - `go test ./internal/jobqueue -run 'TestClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RecoverStaleInProgressTriggeredTurnsFailsOrphanedAttemptWithoutJobRunOrInvocation|RecoverStaleInProgressTriggeredTurnsFailsNonHeartbeatingClaimedAttemptWithoutRun|RecoverClaimedAgentTurnsWithoutLiveOwnershipKeepsCurrentPendingAttempt|PurgeStaleAgentTurnJobsKeepsSupervisorRecoveryJobForActiveExecutionWithoutTurn|RequeueActiveExecutionSessionsWithoutTurns)' -count=1`
  - rebuilt `./bin/ottercamp`
  - hard-respawned tmux worker pane again; current pane list:
    - `0 30149 ottercamp 0`
    - `1 43519 ottercamp 0`
- live proof for the new orphaned-turn recovery:
  - session `2f07b5c0-f7dc-4e4b-b2ef-a66319377e97`
  - stale turn `0fe3da98-394d-4a30-8f1c-5ecc2aaadaea` is now `failed`
  - `chat_session.current_turn_id` was cleared on startup
  - failure reason recorded on the turn:
    - `recovered stale in-progress message turn without live job or execution; scheduling a fresh retry`
- critical new root cause discovered while debugging the next retry:
  - the worker was pool-starved, not OC-27-specific hung
  - a crash dump from the pre-patch worker showed many goroutines blocked in `pgxpool.Acquire`
  - repeated hotspots in the dump:
    - `maintainClaim -> agentTurnSessionClosed`
    - `executeClaimedJob -> RequeueActiveExecutionSessionsWithoutTurns -> enqueueAgentTurnDispatch`
  - this explains why `RecoverClaimedAgentTurnsWithoutLiveOwnership` matched the live OC-27 claimed row in SQL but never fired in production: the stale-claim loop was starving for a DB connection
- current live OC-27 state after the heartbeat-clamp patch is narrower but not finished:
  - task `27` is still `review`
  - the first retry job after the heartbeat patch still starved under concurrency `24`:
    - job `736b9cc0-6fe1-45d8-ab26-6ba6c17d8b92`
    - it reached `agent_turn dispatch: loaded message` and then never heartbeated
    - after restart pressure, it was eventually dead-lettered with `stale claim exceeded max attempts`
  - operational mitigation that immediately changed behavior:
    - respawned the worker with `OTTERCAMP_WORKER_CONCURRENCY=8`
  - under concurrency `8`, OC-27 finally progressed past the old stop point:
    - new retry job: `58f723c3-52f7-4682-99ea-aa1f842c09cc`
    - session current turn: `e3301bb5-ac29-43c3-8440-bb6410d08894`
    - worker log now shows the full startup sequence:
      - `loaded session`
      - `loaded message`
      - `resolved agent`
      - `created turn record`
      - `start inbound turn ... should_run=true`
  - current live state at handoff:
    - task `27` was still `review` at that point
    - session `2f07b5c0-f7dc-4e4b-b2ef-a66319377e97` was active
    - current turn `e3301bb5-ac29-43c3-8440-bb6410d08894` was `in_progress`
- next seam to debug if this still stalls:
  - concurrency/backlog-driven worker pressure remains a live product issue
  - the code-level heartbeat fix helped, but the strongest live signal so far is that OC-27 only clears the startup bottleneck when worker concurrency is reduced from `24` to `8`

### Protected artifacts reminder

Do not touch:

- `.oc.db`
- `data/objects/`
- `internal/turn/bootstrap_refresh_codex_test.go`
- `skills/`
- stray file `"'done' order by task_number;\""`

### Current live canary

- project slug: `speaker-pipeline-ops-validation-fresh-20260324-rerun-16`
- project id: `f7a98d65-74dc-4bef-9025-efcb5728d6a3`
- current board:
  - tasks `1-27`: `done`

### Most recent fixes landed in this stretch

- widened execution-log target inference for titles like `Validation execution: test pipeline capacity rejection`
  - this now maps OC-27-style execution tasks onto canonical targets like:
    - `Test/test-execution-oc27-test-pipeline-capacity-rejection.md`
  - focused regressions added in `internal/turn/engine_test.go`:
    - `TestPreferredTaskDeliverablePathInfersValidationExecutionLogTarget`
    - `TestHandleTaskFileWriteWrongPathRewritesValidationExecutionDocumentToCanonicalTarget`
- task/session execution was already moved onto per-task git worktrees outside the repo tree
  - key files:
    - `internal/tools/native/task_worktree.go`
    - `internal/tools/native/executor.go`
    - `internal/turn/engine.go`
    - `internal/flowcommit/git.go`
- worker/session recovery hardening from the earlier stretch remains active:
  - closed-session run retirement
  - stale model invocation cleanup
  - immediate requeue of active execution sessions without turns
  - review-lane prompt synthesis and review-only guardrails
- worker startup now also runs stale in-progress triggered-turn recovery
  - file: `internal/jobqueue/worker.go`
  - focused regression:
    - `TestJobWorkerRecoverStaleInProgressTriggeredTurnsKeepsExistingPendingRetryJob`
- supervisor recovery dispatch purging now exempts active `project_task` execution sessions that have lost their live turn
  - file: `internal/jobqueue/worker.go`
  - focused regression:
    - `TestJobWorkerPurgeStaleAgentTurnJobsKeepsSupervisorRecoveryJobForActiveExecutionWithoutTurn`

### What just happened live on OC-27

- the earlier work-lane path-inference bug is fixed in production behavior
- OC-27 work execution completed on branch `task/27`
- runtime commit visible in live review context:
  - `f5a7e24c5cb6ae1e380d96adfbd3cea6c7449686`
  - message: `work: OC-27 capacity rejection test execution complete (6/6 PASS)`
- the fresh review session is:
  - session id: `2f07b5c0-f7dc-4e4b-b2ef-a66319377e97`
  - flow execution id: `30b1ad9d-2441-42ce-a663-8bdda280e7d9`
- reviewer is now reading the correct execution artifact content:
  - `# OC-27: Capacity Rejection Test — Execution Results`
  - not the older design-doc path

### Current blocker on OC-27

- review did not finish cleanly on the first fresh review turn
- stale review turn that was cleaned up:
  - turn id: `35b81f1f-8497-43e1-87c0-eceb4525ca6d`
  - model invocation id: `3c9bd9a1-542f-43b2-9f25-cdd006e01345`
  - invocation stayed `in_flight` until worker restart
- after worker restart, startup stale-invocation cleanup did the right thing:
  - run `52c8b80c-9925-4e5f-9f2d-94165fcc83b4` was marked `failed`
  - failure reason:
    - `worker cleanup failed stale in_flight model invocation without live in-progress turn`
  - assistant message `6997c0de-e38b-44cf-9184-077fe904e5ef` was marked `failed`
  - retry marker appended:
    - system message `0ec41be8-cd64-4e1a-beab-2511337dc4d5`
    - content: `[Retry attempt 1 started.]`
- current fresh retry turn is already running:
  - session current turn id: `1a7624b7-75bb-42b8-aa51-a04de7e49f5c`
  - worker log shows:
    - `agent_turn dispatch: start inbound turn ... session_id=2f07b5c0-f7dc-4e4b-b2ef-a66319377e97 ... turn_id=1a7624b7-75bb-42b8-aa51-a04de7e49f5c ... should_run=true`

### Immediate next step

- stay on OC-27 only
- verify whether retry turn `1a7624b7-75bb-42b8-aa51-a04de7e49f5c` ends with `flow.review_decision`
- if it stalls or drifts again, the next product bug is specifically review-turn stale-invocation / retry quality on the final remaining task, not execution targeting, worktree ownership, or bootstrap

### New clue after stale cleanup

- after the stale cleanup retry started, the new retry turn entered a different failure mode before model launch:
  - turn `1a7624b7-75bb-42b8-aa51-a04de7e49f5c` is `in_progress`
  - it has no `model_invocation` rows yet
  - the only message attached to the new turn is:
    - `0ec41be8-cd64-4e1a-beab-2511337dc4d5`
    - `[Retry attempt 1 started.]`
- the actual review prompt message is still attached to the old failed turn:
  - message `3972e0ec-c9e2-47e1-8b0d-9676a099a4d5`
  - its `turn_id` is still `35b81f1f-8497-43e1-87c0-eceb4525ca6d`
- that means the retry path currently appears to create a fresh turn while reusing the old trigger message id without rebinding/duplicating the user message onto the new turn
- this is now the most likely code seam if OC-27 does not progress:
  - retry turn / trigger-message rebinding
  - or downstream logic that assumes the current turn already owns a user message before model invocation starts

### 2026-03-24 22:18 MDT update

- the pre-model no-run retry turn is now fixed on worker restart:
  - startup stale-triggered-turn recovery failed turn `1a7624b7-75bb-42b8-aa51-a04de7e49f5c`
  - `chat_session.current_turn_id` for session `2f07b5c0-f7dc-4e4b-b2ef-a66319377e97` is now `NULL`
  - that proves the new startup call to `RecoverStaleInProgressTriggeredTurns` is working in production
- the next live seam was then exposed and patched:
  - startup purge logic was still dead-lettering retry-count-0 supervisor recovery jobs even when they were the only valid recovery dispatch for an active `project_task` execution session with no live turn
  - this was why session `2f07b5c0-f7dc-4e4b-b2ef-a66319377e97` had no runnable recovery job after the stale-turn cleanup
- after the second worker patch and restart, OC-27 now has a valid pending recovery dispatch again:
  - job `27e9337e-55f8-4504-8845-00fffcc4e5dc`
  - payload message id `096b01d4-788b-4bb2-9a1a-7a1b52fe9a76`
  - payload flow execution id `30b1ad9d-2441-42ce-a663-8bdda280e7d9`
  - status: `pending`
- current live state is narrower now:
  - session `2f07b5c0-f7dc-4e4b-b2ef-a66319377e97` is active with no current turn
  - task `27` remains `review`
  - the worker has not yet claimed job `27e9337e-55f8-4504-8845-00fffcc4e5dc`
  - if that job claims and progresses, the remaining work is just finishing OC-27
  - if it still starves indefinitely, the next bug is scheduling/claim fairness rather than stale-turn cleanup or supervisor-job purging

### Local verification already run for the newest inference patch

- `go test ./internal/turn -run 'Test(PreferredTaskDeliverablePathInfers(ValidationExecutionLogTarget|TestExecutionLogTarget|HappyPathExecutionLogTarget)|HandleTaskFileWriteWrongPathRewrites(ValidationExecutionDocumentToCanonicalTarget|ToInferredTestExecutionTarget|ScenarioExecutionPlanToCanonicalTarget))' -count=1`
- `go build -o ./bin/ottercamp ./cmd/ottercamp`
- hard respawned tmux `serve` and `worker`
- `./bin/ottercamp health`
- latest worker verification:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(PurgeStaleAgentTurnJobs(KeepsSupervisorRetryJob|KeepsLiveSupervisorRecoveryTurn|KeepsSupervisorRecoveryJobForActiveExecutionWithoutTurn)|RecoverStaleInProgressTriggeredTurns(KeepsExistingPendingRetryJob|IgnoresQueuedJobForDifferentExecution|UsesExecutionMetadataLiveTurn|FailsPostModelOrphanedExecutionTurn)?|RequeueActiveExecutionSessionsWithoutTurns)' -count=1`

## Current repo state

- branch: `main`
- latest local commit: `6b5e62fb` `Normalize missing-history continuation summaries`
- latest pushed commit: `6b5e62fb` `Normalize missing-history continuation summaries`
- latest pushed slices in this stretch:
  - `6b5e62fb` `Normalize missing-history continuation summaries`
  - `efbaef30` `Reject missing-context continuation replies`
  - `0703cbc8` `Ignore turn-in-progress during blocked cleanup`
  - `80b0243f` `Treat status transition races as recoverable`
  - `41611d3f` `Reject cross-task clarification recovery replies`
  - `90f13034` `Prefer authoritative recovery checkpoint targets`
  - `cfa95b3a` `Prefer deliverable targets in recovery write halts`
  - `08d6a950` `Skip wakeups for terminal flow executions`
  - `c7444ed7` `Preserve deliverable targets in validation recovery`
  - `96497ec1` `Rewrite task file writes to deliverable targets`
  - `96304268` `Allow multiple project roles per agent`
  - `5f6bbeeb` `Resume failed bootstrap sessions on project resume`
  - `018b2fd1` `Normalize project domain event actors`
  - `3f228033` `Bypass stale pause checks on project resume`
  - `17c09de4` `Clear stale project pause on resume`
  - `5cb20e85` `Prefer fresh bootstrap recovery on resume`
- tracked worktree should be clean except:
  - this local-only file
  - unrelated user docs edits in:
    - `docsv2/03-projects-and-task-flow.md`
    - `docsv2/18-web-ui.md`
    - `docsv2/ui-spec-for-figma.md`
- leave these long-standing untracked artifacts alone:
  - `.oc.db`
  - `data/objects/`
  - `internal/turn/bootstrap_refresh_codex_test.go`
  - `skills/`
- also ignore this stray shell-created untracked artifact unless the primary session is actively using it:
  - `"'done' order by task_number;\""`

## Current focus

- active canary is now `fresh-43`
  - project: `f2d31978-5849-46b1-b924-d6a366d5adda`
  - project session: `d8d679cc-dd5a-4ead-a406-35de6675b2e4`
  - task-20 latest execution session: `35e14c78-7dee-4cf9-b25a-cc7213fb931e`
- current task state:
  - `done=8`
  - `in_progress=1`
  - `blocked=2`
  - `draft=14`
  - remaining active lanes are now:
    - task `18` `in_progress`
    - task `19` `blocked`
    - task `20` `blocked`
- what the newest slices fixed:
  - continuation summaries that say no task session history / no continuation summary was included are now normalized to `Continuation summary unavailable.` instead of being treated as meaningful summaries
  - generic recovery replies now also reject missing-context / no-specific-request variants, not just cross-task clarification variants
  - stale `flow_current` / `flow_transition` wakeups now no-op when their referenced `flow_node_execution` is already terminal, which stopped task `18` from continuing to append kickoff work onto a closed rejected lane
  - recovery-halting `file.write` failures now prefer the tool-result `deliverable_path` when present, so the halt message and artifact/checkpoint path pivot to the real deliverable
  - checkpoint persistence now treats a target path named explicitly in the current failure reason as authoritative, instead of preserving an older substantive but wrong checkpoint target
  - repeated `file.read` focus failures now preserve the native validator's `deliverable_path` and persist that target into recovery checkpoint metadata when the validation loop blocks
  - execution-first task sessions now rewrite `file.write` calls that drift to the wrong path back onto the explicit deliverable target when the task already has an execution-owned output path
  - project resume now appends a fresh bootstrap recovery message for failed bootstrap sessions instead of replaying stale bootstrap-complete prompts
  - `project.resumed` domain events are being written again because `human_user` actors are normalized to `human`
  - resume-triggered turns bypass stale pause suppression and clear lingering `settings.pause` before requeue
  - bootstrap recovery can reassign staffing on the resumed live project session instead of needing a relaunch
  - agents can now hold multiple active project roles on the same project without clobbering each other
- last completed live bug/fix:
  - `fresh-43` had a recoverable bootstrap failure on `reviewer_assignment_missing`
  - after resume under the new recovery path, the model assigned Florian as `reviewer`
  - because `agent_project_assignment` was unique on `(agent_id, project_id)`, that reviewer assignment overwrote Florian's existing `project_manager` row
  - bootstrap immediately failed again with `pm_assignment_missing`
  - pushed commit `96304268` fixes that by:
    - migrating assignment uniqueness to `(agent_id, project_id, role)` in `0127_agent_project_assignment_multi_role.sql`
    - updating repo upserts to `ON CONFLICT (agent_id, project_id, role)`
    - preserving delete/remove-all-roles behavior while making PM-removal event emission multi-role aware
    - adding repo, service, and HTTP integration coverage for same-agent multi-role assignments
- live deployment state:
  - `6b5e62fb` is built, pushed, and deployed locally to `oc-svc` / `oc-worker`
  - `./bin/ottercamp health` is green
  - `fresh-43` now has these active assignments:
    - `project_manager|true|3dfee822-a82e-4705-8215-c3341dd8681a`
    - `reviewer|true|3dfee822-a82e-4705-8215-c3341dd8681a`
    - `worker|true|ae8e7d05-6fef-48d2-9d1a-94d96e023ab6`
    - `worker|true|906a7988-1e06-4ec9-8144-2e5e4342af12`
  - the project pause is cleared and execution has resumed without re-pausing on the same invalid-status race
- current next seam:
  - current live task mix after resume:
    - task `18` `in_progress`
    - task `19` `blocked`
    - task `20` `blocked`
  - task `18` was the recent continuation/generic-reply drift lane:
    - assistant replies included:
      - `I need to clarify which task to continue...`
      - `The continuation summary references multiple tasks with different statuses...`
      - `Now let me check the project context to understand the strategic direction...`
  - task `18` previously moved `in_progress -> blocked` with:
    - `blocker_reason = recovery halted after 3 retries without a usable assistant response`
  - blocked-task cleanup used to thrash because `controlplane.task-completed` tried to `CloseSession(...)` on the still-live task session and got `chat.ErrTurnInProgress`
  - `0703cbc8` now ignores that cleanup-time `ErrTurnInProgress` and still abandons the active execution immediately
  - the next likely fix path is execution-owned continuation context / stronger continuation rooting for task `18`, not project-pausing or blocked cleanup churn
  - `41611d3f` / `efbaef30` now harden `looksLikeGenericTaskRecoveryReply(...)` against the cross-task clarification and missing-context families, and `6b5e62fb` normalizes missing-history continuation summaries
  - because those reply families are now explicitly filtered/normalized, any continued task-18 drift likely points to the continuation-summary / history-rooting path itself, not just missing phrase coverage
  - concrete code-level clue from `internal/turn/engine.go`:
    - `continueTurn(...)` still asks the model for `Purpose: "continuation_summary"` using only `lastNUserMessages(messages, 3)` plus `InstructionHint: "Summarize the work completed so far and what remains."`
    - `lastNUserMessages(...)` is a literal role filter over recent `user` messages; it does not include recent substantive assistant finals, tool results, current task metadata, or execution-owned checkpoint context
    - the stronger task fallback summary only activates when `sessionHasSupervisorRecoveryPrompt(messages)` is already true, and that helper only scans the last ~6 session messages for a recovery-resume marker
    - so ordinary compressed continuation on a live task lane can still be rooted in a thin, user-message-only summary request instead of execution-owned task/session state
    - if task `18` keeps drifting, the next likely fix is to build continuation context from active execution/session/task metadata and recent substantive tool/assistant state, not more generic-reply phrase matching
  - test coverage clue from `internal/turn/engine_test.go`:
    - current tests cover summary normalization, supervisor-fallback behavior, and synthetic continuation-root prompts well
    - they do not appear to cover a stronger execution-rooted continuation-summary input built from active task metadata, recent substantive assistant output, or recent tool results
    - if the primary session changes this path, adding that test first would reduce churn and keep the fix from collapsing back into prompt-only guardrails
  - likely lowest-risk implementation slice:
    - the continuation flow already has synthetic root-message / `HistoryStartID` plumbing (`appendContinuationSummaryAndAction(...)`, `persistTurnHistoryStart(...)`, `taskContinuationResumeMessageMetadata(...)`)
    - that means the safest next change is probably to improve the content of the continuation root for async task sessions, not to change broader turn assembly mechanics
    - in practice: build the root from execution-owned task/session state plus recent substantive artifacts/tool outputs, then keep reusing the existing synthetic root + history-start path
  - likely reusable building blocks already exist in `internal/turn/engine.go`:
    - `latestSubstantiveAssistantFinalForTurn(...)`
    - `latestRecoveryArtifactDraftForTurn(...)`
    - `continuationSummaryDraftContent(...)`
    - `latestTaskHistoricalSubstantiveDraftContent(...)`
    - `recoveryFileWriteDraftContent(...)` already implements an ordered "best available draft" search across current-turn assistant output, prior substantive drafts, continuation-summary draft content, historical task drafts, and persisted recovery drafts
    - those helpers are currently used in recovery/draft flows, but they point to a low-risk way to enrich continuation context from real session artifacts instead of only `lastNUserMessages(...)`
    - that recovery ordering is probably the cleanest template for a continuation-root builder: prefer concrete current-task artifacts first, then fall back to thinner summary text
  - likely best test-first entrypoints:
    - extend the existing continuation-root / `HistoryStartID` test around `TestContinuationTurnAppendsDirectActionPromptForAsyncProjectTask`
    - add a task-session case where the continuation root should prefer a substantive prior draft/artifact over a generic model-written summary
    - reuse the substantive-draft helper expectations from `TestLatestSubstantiveAssistantFinalForTurnSkipsLaterIntentPlaceholder`
    - that would let the primary session prove the new continuation root is grounded in real task artifacts before changing prompt text again
  - strategic bridge to `issues/samtalkoutcomes.md`:
    - this continuation seam is a concrete example of the larger architecture problem documented there
    - today the continuation root is inferred from recent chat text
    - the desired model is for live execution context to come from execution-owned state first, with chat text as supporting context
    - so a good local fix here would also be a small step toward the broader `flow_node_execution` runtime-anchor direction, not just another continuation patch
  - plausible minimal patch shape:
    - keep `appendContinuationSummaryAndAction(...)` and the existing synthetic root / `HistoryStartID` behavior unchanged
    - change only the summary-selection block inside `continueTurn(...)`
    - for async `project_task` sessions, try an execution-rooted summary builder before calling the model:
      - prefer a substantive current/prior draft or recovery artifact using the existing helper ordering
      - if a concrete artifact draft exists, compact/use that as the continuation summary root
      - only fall back to `Purpose: "continuation_summary"` over `lastNUserMessages(...)` when no concrete execution artifact can be recovered
      - if implemented that way, `resolveModelProfile(..., "continuation_summary", ...)` can also move behind the fallback path, which keeps the no-model happy path even narrower
    - that would be a narrow patch with lower risk than changing assembler behavior or broad prompt wiring
  - the current product gap is no longer checkpoint poisoning; it is continuation quality on the remaining live execution lane (`18`) plus recovery quality / direct-write behavior on the blocked lanes (`19`, `20`)

## Latest shipped slices

### `0703cbc8` `Ignore turn-in-progress during blocked cleanup`

- `abandonActiveFlowExecutionsForTask(...)` now ignores `chat.ErrTurnInProgress` from `CloseSession(...)` and still abandons the active execution
- this stops `controlplane.task-completed` from retrying noisily on blocked tasks whose active turn has not finished yet
- focused regression added:
  - `TestTaskQueueProcessorHandleTaskCompletedEventIgnoresTurnInProgressWhenClosingBlockedSession`
- verification:
  - `go test ./internal/controlplane -run 'TestTaskQueueProcessorHandleTaskCompletedEvent(FailsBlockedTrackingRuns|IgnoresTurnInProgressWhenClosingBlockedSession)' -count=1`
  - `go test ./internal/controlplane -count=1`

### `80b0243f` `Treat status transition races as recoverable`

- `isRecoverableProjectExecutionFailure(...)` now treats these runtime races as recoverable instead of project-pausing terminal failures:
  - `chat.ErrInvalidStatusTransition`
  - `tasksvc.ErrInvalidStatusTransition`
  - `repo.ErrConflict`
- this prevents the generic unhandled-turn failure path from pausing a live project after first-wave execution when the actual fault is a duplicate/late status transition rather than a real terminal execution failure
- verification:
  - `go test ./internal/turn -run 'Test(IsRecoverableProjectExecutionFailure|LooksLikeGenericTaskRecoveryReplyDetectsCrossTaskClarificationReply)' -count=1`
  - `go test ./internal/turn -count=1`

### `41611d3f` `Reject cross-task clarification recovery replies`

- `looksLikeGenericTaskRecoveryReply(...)` now rejects the exact task-18 continuation drift family:
  - `I need to clarify which task to continue`
  - `The continuation summary references multiple tasks`
  - `references multiple tasks with different statuses`
  - `let me check the project context to understand the strategic direction`
- focused regression added:
  - `TestLooksLikeGenericTaskRecoveryReplyDetectsCrossTaskClarificationReply`
- verification:
  - `go test ./internal/turn -run 'TestLooksLikeGenericTaskRecoveryReplyDetectsCrossTaskClarificationReply|TestLooksLikeGenericTaskRecoveryReplyDetectsKickoffReadyAssistReply|TestLooksLikeGenericTaskRecoveryReplyDetectsFocusQuestionMenu' -count=1`
  - `go test ./internal/turn -count=1`

### `90f13034` `Prefer authoritative recovery checkpoint targets`

- `persistRecoveryFileWriteCheckpoint(...)` now treats the current target path as authoritative when the failure reason explicitly names that target
- this prevents older substantive but wrong checkpoint targets from overriding the new deliverable path during checkpoint reconciliation
- focused regression added:
  - `TestPersistRecoveryFileWriteCheckpointPrefersAuthoritativeFailureTargetPath`
- verification:
  - `go test ./internal/turn -run 'Test(PersistRecoveryFileWriteCheckpointPrefersAuthoritativeFailureTargetPath|HandleRecoveryPopulatedFileWriteOutcomePrefersDeliverablePathFromToolResult)' -count=1`
  - `go test ./internal/turn -count=1`

### `cfa95b3a` `Prefer deliverable targets in recovery write halts`

- `handleRecoveryPopulatedFileWriteOutcome(...)` now prefers `result.Output["deliverable_path"]` when a recovered `file.write` fails with:
  - `deliverable_path_required`
  - `explicit_deliverable_focus_required`
  - `recovery_target_focus_required`
- halt messages, persisted artifacts, and blocked-task reasons now point at the real deliverable target instead of the attempted wrong path
- focused regression added:
  - `TestHandleRecoveryPopulatedFileWriteOutcomePrefersDeliverablePathFromToolResult`
- verification:
  - `go test ./internal/turn -run 'Test(HandleRecoveryPopulatedFileWriteOutcomePrefersDeliverablePathFromToolResult|CollectToolValidationFailuresPreservesDeliverablePath|HandleUserMessageValidationLoopBlockPersistsDeliverableCheckpoint)' -count=1`
  - `go test ./internal/turn -count=1`

### `08d6a950` `Skip wakeups for terminal flow executions`

- `dispatchTaskQueueWakeup(...)` now ignores `flow_current` / `flow_transition` wakeups when the referenced `flow_node_execution` is already terminal instead of appending kickoff work onto a dead lane
- this fixed the live task-18 pattern where closed rejected sessions kept accumulating `flow.rejected` kickoff messages and then spawning stale inbound-turn retries
- focused regression added:
  - `TestDispatchTaskQueueWakeupFlowTransitionSkipsTerminalExecution`
- verification:
  - `go test ./internal/controlplane -run 'Test(EnsureFlowRun(AddsParticipantAndKickoffMessage|FinalizesDuplicatePendingKickoffs)|DispatchTaskQueueWakeupFlowTransition(UsesExecutionSession|SkipsTerminalExecution))' -count=1`
  - `go test ./internal/controlplane -count=1`

### `c7444ed7` `Preserve deliverable targets in validation recovery`

- `toolValidationFailure` now preserves `deliverable_path` from native validator output
- `classifyToolValidationFailure(...)` threads that through for validation codes like:
  - `recovery_target_focus_required`
  - `explicit_deliverable_focus_required`
- when a deterministic validation loop blocks a task, the engine now persists a recovery checkpoint from the validator-reported deliverable path before returning
- this prevents read-focus failures from poisoning `recovery_file_write_checkpoint.target_path` to the last attempted planning file
- focused regressions added in `internal/turn/engine_test.go`:
  - `TestCollectToolValidationFailuresPreservesDeliverablePath`
  - `TestHandleUserMessageValidationLoopBlockPersistsDeliverableCheckpoint`
- verification:
  - `go test ./internal/turn -run 'TestCollectToolValidationFailures(PreservesDeliverablePath|SuppressesFocusReadFailuresAfterSuccessfulFileWrite)|TestHandleUserMessageValidationLoopBlockPersistsDeliverableCheckpoint' -count=1`
  - `go test ./internal/turn -count=1`

### `96497ec1` `Rewrite task file writes to deliverable targets`

- execution-first task turns now catch `file.write` calls aimed at the wrong workspace path and rewrite them onto the task's explicit deliverable target when one is already known from execution-owned context
- this is narrower than the earlier deliverable guardrails:
  - previous slices rejected wrong-path writes or alternate helper-file drift
  - this slice actively rewrites wrong-path writes onto the correct deliverable target when the intended deliverable is already known
- implementation details:
  - new `handleTaskFileWriteWrongPath(...)` hook in tier-2 dispatch
  - path normalization / nested-path helpers added for turn workspace comparisons
  - only applies to `project_task` sessions in execution-first mode
- focused regression coverage added in `internal/turn/engine_test.go` for:
  - rewriting a wrong-path write to the recovery/execution target
  - skipping rewrite for non-execution-first tasks
- verification:
  - `go test ./internal/turn -count=1`

### `96304268` `Allow multiple project roles per agent`

- changed assignment uniqueness from `(agent_id, project_id)` to `(agent_id, project_id, role)` via `0127_agent_project_assignment_multi_role.sql`
- repo assignment upsert is now role-scoped:
  - `ON CONFLICT (agent_id, project_id, role)`
  - assigning `reviewer` no longer overwrites an existing `project_manager` row for the same agent/project
- remove/deactivate still removes all active roles for the agent/project pair, but now:
  - deactivation uses a deterministic CTE/`LIMIT 1` return shape
  - PM-removal event emission checks all active roles, not just one arbitrary row
- added repo/service/server integration regressions for:
  - same-agent PM + reviewer coexistence
  - removal of a multi-role agent deactivating all roles while still emitting `agent.pm_removed`
- verification:
  - `go test ./internal/agent -count=1`
  - `go test -tags=integration ./internal/agent -count=1`
  - targeted `internal/repo` integration tests for multi-role assignments passed
  - targeted `internal/server` integration tests for multi-role assignment HTTP passed
  - full `go test -tags=integration ./internal/repo -count=1` still has the unrelated pre-existing failure `TestToolDefinitionSeedSchemasIncludePropertiesAndRequiredParameters`

### `ed122cf1` `Report blocked review outcomes from flow.review_decision`

- native `flow.review_decision` now returns a blocked outcome payload when the reject path exhausts `MaxVisits` and the task is blocked instead of returning a misleading normal rejection shape
- `handleFlowReviewDecision(...)` now reloads the task after a successful reject call and reports:
  - `blocked=true`
  - `message=review rejection recorded, but the reject path has exhausted its allowed visits and the task is now blocked`
- added integration coverage in `internal/tools/native/native_integration_test.go`:
  - `TestIntegrationFlowReviewDecisionRejectReturnsBlockedWhenRejectPathExhausted`
- verification:
  - `go test -tags=integration ./internal/tools/native -run 'TestIntegrationFlowReviewDecisionRejectReturnsBlockedWhenRejectPathExhausted$' -count=1`
  - commit pushed to `origin/main`

### `945c9fdf` `Close blocked task sessions during flow cleanup`

- blocked-task cleanup in `controlplane.task-completed` now closes the active session attached to each active `flow_node_execution` before abandoning the execution
- this closes the exact stale shape seen on `fresh-38`:
  - task already `blocked`
  - execution already `rejected`
  - session still `active`
  - `current_turn_id` still pointing at a completed turn
- implementation detail:
  - `taskQueueChatService` now exposes `CloseSession(...)`
  - `abandonActiveFlowExecutionsForTask(...)` closes `execution.SessionID` when present, then abandons the execution
- test coverage:
  - blocked-task processor unit test now asserts the active execution session is closed during blocked cleanup
- verification:
  - `go test ./internal/controlplane -run 'TestTaskQueueProcessorHandleTaskCompletedEvent(FailsRunsForBlockedTasks|AutoCompletesParentTask|ConfirmsCancellingSchedulerRuns)$' -count=1`
  - `go test ./internal/controlplane -count=1`
  - rebuilt and sequentially restarted `oc-svc` / `oc-worker`
  - `./bin/ottercamp health` green

### `ddbb87f5` `Catch more generic recovery resume replies`

- hardened `looksLikeGenericTaskRecoveryReply(...)` against two live OC-13 phrase families that were still slipping through as “usable”:
  - `What do you need me to do? Please specify...`
  - status inventory replies like `Current Status: task is in the Work node... I have access to: ...`
- added focused turn-engine regressions for both variants:
  - `TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReplyPleaseSpecifyVariant`
  - `TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReplyStatusInventoryVariant`
- verification:
  - `go test ./internal/turn -run 'TestHandleTurnCompletedEvent(BlocksRepeatedGenericRecoveryReplyStatusInventoryVariant|BlocksRepeatedGenericRecoveryReplyPleaseSpecifyVariant|BlocksRepeatedGenericRecoveryReplyHelpWithVariant|BlocksRepeatedGenericRecoveryReply)$' -count=1`
  - `go test ./internal/turn -count=1`
  - rebuilt and sequentially restarted `oc-svc` / `oc-worker`
  - `./bin/ottercamp health` green
- live effect already observed on `fresh-39`:
  - task `13` now emits a queued retry with `retry_count=1` after the generic recovery variant instead of silently treating it as progress

### `a79624a4` `Block exhausted review rejection loops`

- review-node `flow.review_decision reject` no longer bubbles `ErrMaxVisitsExceeded` back to the reviewer as a deadlock
- when the reject path has exhausted its allowed visits:
  - the current review execution is rejected
  - the task is transitioned to `blocked`
  - `flow.rejected` is published with blocked / max-visits metadata
  - the service returns a terminal blocked outcome instead of an error
- added both unit and integration coverage in `internal/flow`
- verification:
  - `go test ./internal/flow -run 'TestRejectFlowNodeMaxVisitsExceeded$' -count=1`
  - `go test -tags=integration ./internal/flow -run 'TestFlowExecutionServiceRejectFlowNodeMaxVisits$' -count=1`

### `e078d47e` `Pause recoverable bootstrap failures`

- recoverable bootstrap structural failures now pause instead of archive once bootstrap has reached a durable checkpoint like `task_tree_persisted` or later
- this preserves the live project/session for follow-up recovery instead of forcing relaunch
- verification:
  - `go test ./internal/turn -count=1`

### `5ff06012` `Carry first-wave selection errors through bootstrap recovery`

- bootstrap recovery now promotes `bootstrap.setup.persist` selection failures into a first-class recoverable bootstrap failure instead of dropping back to generic continuation
- `projectBootstrapBlockedRecoveryFailure(...)` now recognizes:
  - `first_wave_task_selection_required`
  - `invalid_first_wave_selection`
- first-wave-selection bootstrap failures are now classified as `projectBootstrapFailureFirstWaveExecution` and treated as recoverable
- bootstrap recovery prompts now explicitly tell the model to:
  - call `bootstrap.setup.persist` first
  - include `first_wave_task_ids` or `first_wave_task_numbers`
  - avoid rereads before doing that
- bootstrap resume snapshot/state now includes `Selectable first-wave tasks: ...` with exact task ids/numbers/titles/assigned agents when selection is the blocker
- `bootstrap.setup.persist` now returns `selectable_first_wave_tasks` on:
  - `first_wave_task_selection_required`
  - `invalid_first_wave_selection`
- verification:
  - `go test ./internal/turn -count=1`
  - `go test -tags=integration ./internal/tools/native -run 'TestIntegrationBootstrapSetupPersist(RequiresExplicitFirstWaveSelectionAndPersistsIt|RejectsProjectWideGateInFirstWaveSelection)$' -count=1`
  - sequential rebuild/restart completed after the commit

### `8c1ad658` `Use phase-aware bootstrap continuation prompts`

- plain bootstrap auto-continuation messages no longer always use the generic kickoff-style bootstrap prompt
- once a project has active persisted bootstrap state and should root at the resume state, continuation generation now uses `buildProjectBootstrapResumeActionPrompt(...)`
- this is intended to stop post-shaping sessions from falling back into:
  - `Let me first examine the current project state...`
  - `project.get`
  - `file.read`
  - broad scaffold rereads
- focused regression coverage added for:
  - fresh kickoff metadata preservation
  - phase-aware bootstrap continuation prompt selection
- verification:
  - `go test ./internal/turn -count=1`
  - sequential rebuild/restart completed after the commit

### `9c74498a` `Surface first-wave selection choices after bootstrap persist`

- successful `bootstrap.setup.persist` responses now include `selectable_first_wave_tasks` whenever `select-first-wave` is still one of the remaining canonical bootstrap steps
- turn-engine `appendToolResults(...)` now appends an explicit system instruction when that happens:
  - names the selectable first-wave task ids/numbers/titles
  - tells the model not to call `project.get`, `task.list`, or scaffold reads first
  - tells it to call `bootstrap.setup.persist` again with `select-first-wave` plus `first_wave_task_ids` / `first_wave_task_numbers`
- verification:
  - `go test ./internal/turn -count=1`
  - `go test -tags=integration ./internal/tools/native -run 'TestIntegrationBootstrapSetupPersist(RequiresExplicitFirstWaveSelectionAndPersistsIt|RejectsProjectWideGateInFirstWaveSelection)$' -count=1`

### `e66f26de` `Surface selected bootstrap waves in recovery`

- bootstrap resume snapshots now include the currently selected first-wave task ids/titles for partial-wave materialization failures
- bootstrap resume state messages now show that selected-wave line alongside the validation failure
- bootstrap recovery prompts now explicitly tell the model to use the currently selected first-wave task ids already listed and shrink that exact subset directly instead of asking the runtime to restate the selection
- verification:
  - `go test ./internal/turn -run 'TestBuildProjectBootstrapResume(ActionPromptForPartialFirstWaveMaterialization|StateMessageIncludesSelectedFirstWaveTasks|StateMessageIncludesSelectableFirstWaveTasks|ActionPromptForExplicitFirstWaveSelection)$' -count=1`
  - `go test ./internal/turn -count=1`
  - sequential rebuild/restart completed

### `3da64e30` `Guard task kickoff turns from generic replies`

- generic non-action reply handling now applies to execution-owned kickoff prompts from `task_queue_processor`, not just recovery/continuation prompts
- kickoff prompts like `Start work on task: ...` or `Start review on task: ...` with execution metadata now retry or block when the assistant answers with generic `I'm ready to assist / what do you need` narration instead of acting
- added focused helper and turn-completion coverage in `internal/turn/engine_test.go`
- verification:
  - `go test ./internal/turn -run 'Test(TaskExecutionKickoffMessageDetectsTaskQueueKickoff|LooksLikeGenericTaskRecoveryReplyDetectsKickoffReadyAssistReply|HandleTurnCompletedEventRetriesGenericTaskQueueKickoffReply|HandleTurnCompletedEventBlocksRepeatedGenericTaskQueueKickoffReply)$' -count=1`
  - `go test ./internal/turn -count=1`
  - sequential rebuild/restart completed

### `65c85b9d` `Keep review turns out of recovery checkpoints`

- review-node task turns no longer prepend stale file-write recovery checkpoint prompts before the synthetic review action prompt
- `appendRecoveryResumeState(...)` now skips tasks already in `review`, so `runTurn(...)` can append the review-only `flow.review_decision` prompt instead
- added unit coverage that review tasks with recovery checkpoints do not root history at recovery state
- verification:
  - `go test ./internal/turn -run 'Test(AppendRecoveryResumeStateSkipsReviewTasks|AppendReviewActionStateRootsHistoryForReviewTask|HandleTurnCompletedEventRetriesGenericTaskQueueKickoffReply|HandleTurnCompletedEventBlocksRepeatedGenericTaskQueueKickoffReply)$' -count=1`
  - `go test ./internal/turn -count=1`
  - sequential rebuild/restart completed

### `2e8c9b63` `Require review decisions before turn completion`

- completed review-node turns are no longer allowed to silently narrate inspection and drift
- `HandleTurnCompletedEvent(...)` now handles both:
  - work-node `in_progress` completions
  - review-node `review` completions
- if a review turn completes without a successful `flow.review_decision` tool result:
  - the lane gets a bounded retry using the same review-action message
  - after repeated retries, the task is blocked with an explicit `flow.review_decision` reason
- helpers added:
  - `taskReviewActionMessage(...)`
  - `reviewTurnCompletedWithDecision(...)`
  - `handleCompletedReviewTurnWithoutDecision(...)`
- focused coverage added for:
  - retrying a review turn that narrates without deciding
  - blocking repeated review turns that still fail to call `flow.review_decision`
- verification:
  - `go test ./internal/turn -run 'TestHandleTurnCompletedEvent(RetriesReviewTurnWithoutDecision|BlocksRepeatedReviewTurnWithoutDecision|RetriesGenericTaskQueueKickoffReply|BlocksRepeatedGenericTaskQueueKickoffReply)$' -count=1`
  - `go test ./internal/turn -count=1`
  - sequential rebuild/restart completed

### `cd3d00a9` `Honor Output paths as explicit deliverables`

- the explicit deliverable parser now treats both:
  - `Deliverable: path`
  - `Output: path`
  as execution-owned target paths
- updated both:
  - turn-engine explicit deliverable parsing / completion detection
  - native mutation guardrails for execution-first tasks
- execution-first task sessions with an explicit `Output:` path now reject writes to alternate workspace files, not just planning artifacts
- this closes the live OC-17 failure where recovery drifted from `test-result-validation.log` onto `oc-17-validation-test-execution.py`
- focused coverage added for:
  - `Output:` counting as an explicit deliverable write in the turn engine
  - native rejection of alternate helper-file writes when the contract says `Output: test-result-validation.log`
- verification:
  - `go test ./internal/turn -run 'TestExplicitExecutionDeliverableWriteCompleted(RecognizesOutputPath)?$' -count=1`
  - `go test ./internal/tools/native -run 'TestFile(WriteRejectsPlanningArtifactMutationForExecutionFirstTaskWithExplicitDeliverable|WriteRejectsAlternateMutationForExecutionFirstTaskWithExplicitOutput|EditRejectsPlanningArtifactMutationForExecutionFirstTaskWithExplicitDeliverable)$' -count=1`
  - `go test ./internal/turn -count=1`
  - `go test ./internal/tools/native -count=1`
  - sequential rebuild/restart completed

## Outcomes alignment

Against `issues/samtalkoutcomes.md`:

- partially implemented:
  - execution ownership is being tightened around active runtime state instead of loose session/message inference
  - bootstrap recovery is being turned into structured runtime state instead of generic continuation heuristics
  - resume/recovery prompts now carry more explicit node/task identity and bounded next actions
  - review-mode completion is now enforced in code, not only via prompt wording: review turns must eventually call `flow.review_decision` or they are retried then blocked
  - explicit task deliverable contracts are being enforced more directly in code, including `Output:` paths for execution-first tasks
  - validation/recovery is starting to preserve execution-owned deliverable targets instead of letting repeated focus-read failures poison recovery checkpoints toward planning artifacts
- not implemented yet:
  - `flow_node_execution` as the universal single runtime owner across the whole stack
  - universal fresh-session-per-node semantics
  - command-driven dispatch everywhere
  - supervisory recovery fully separated from node execution transcripts
  - canonical commit/review artifacts as a hard runtime invariant

## Strategic bridge

- The current live lane is still valid: keep using the active canary to expose the next deterministic runtime bug and patch it directly.
- But the larger direction in `issues/samtalkoutcomes.md` still stands:
  - once the current bootstrap/resume lane is no longer the front-door blocker, the next architectural pivot should be Slice 1 / Slice 2:
    - make `flow_node_execution` the explicit single owner of active node runtime state
    - add or finish explicit runtime substate as the primary execution-health surface
- In short:
  - keep taking high-leverage live fixes while they matter
  - do not mistake that for completing the execution-state-machine rework
  - when the canary stabilizes beyond the present seam, pivot from bootstrap-specialized repair toward execution ownership
- Continuation-specific note:
  - the recent continuation slices (`41611d3f`, `efbaef30`, `6b5e62fb`) are useful hardening, but they are still mostly classification/normalization at the prompt/reply layer
  - if continuation drift remains after those fixes, the next real step is not more phrase matching; it is execution-owned continuation context rooted directly in the active task execution/session state

## Runtime / operational notes

- rebuilds/restarts must be sequential:
  1. `go build -o ./bin/ottercamp ./cmd/ottercamp`
  2. respawn `oc-svc` with `.env` loaded
  3. respawn `oc-worker` with `.env` loaded
  4. `./bin/ottercamp health`
- current runtime sessions expected:
  - `oc-svc`
  - `oc-worker`
  - `oc-ops`
  - `codex`
- latest sequential rebuild/restart completed after `c7444ed7`
- `./bin/ottercamp health` was green after both respawns
- latest sequential rebuild/restart also completed after `f2f0454f`
- the last health check after that local rebuild/restart was green

## Live canaries

### `fresh-35`

- project: `8fe0b85a-3804-4488-9160-f0ae95c2ca00`
- session: `680f26e4-8885-4dff-9894-459fef2ec562`
- this was the canary that exposed the explicit first-wave-selection seam
- before `5ff06012`:
  - bootstrap hit `first_wave_task_selection_required`
  - follow-on turns broad-reread project/task state instead of acting on the explicit blocker
- after `5ff06012`:
  - the session stopped broad-rereading
  - it called `bootstrap.setup.persist` directly on recovery turns
  - the next failure became narrower: it supplied the wrong IDs and used an agent id in `first_wave_task_ids`, which returned `invalid_first_wave_selection`
- current status:
  - project is paused, not archived
  - this canary is useful as evidence that the recovery path now hits the right tool and that the remaining seam is selection-ID usability, not generic bootstrap drift

### `fresh-36`

- project: `4e738c1d-d1b9-4232-82db-9de1f9a55212`
- session: `b63a3a1d-413e-4d02-b01a-b685268b907e`
- kickoff message:
  - `Run a clean end-to-end validation project for Speaker Pipeline Ops. You are the interactive operator for this project. Create or assign the needed PM, worker, and reviewer staff. Build a bounded validation task tree, attach the correct work/review flow, explicitly select a small first wave, keep later-wave work in draft, and continue until real execution is underway. Do not ask the user to read docs or restate the request. Work directly from the product state and tools.`
- current known state:
  - bootstrap staffing completed again
  - PM / worker / reviewer were created and assigned
  - bounded executable validation tasks `9-21` were created and assigned
  - setup tasks `2-6` are now `done`
  - task `7` (`Select first-wave runnable tasks`) is still `draft`
  - task `8` (`Request and record Frank sign-off`) is still `draft`
  - governance gate task `1` is still `draft`
  - bootstrap is now past staffing/decomposition/flow attachment and is in the first-wave-selection seam
- latest live behavior:
  - the PM created a real task tree and attached a flow template successfully
  - the session then entered repeated `validation_loop_blocked` turns while still active
  - root cause: it had already accumulated generic pending bootstrap continuation messages before `8c1ad658` deployed
  - those old queued user messages kept replaying the old generic continuation wording even after restart
  - latest active turn at last check:
    - session current turn: `4a16423c-ebba-4195-b3d1-67eed6000797`
    - trigger message: `5977847e-7766-4d7d-bf73-6673b10137a7`
  - the canary has not yet promoted a first wave into execution
- this run is now useful mainly as evidence of the pre-fix queue residue problem, not as the clean validator for `8c1ad658`

### `fresh-37` (next clean canary to create)

### `fresh-37` (current primary canary)

- project: `0fe2a391-3268-4a3f-874c-6ea5fe620a88`
- session: `fa7fb41c-65a8-4f10-8438-c9f28bc1d634`
- what it has already validated:
  - `8c1ad658` is real in live traffic
  - once bootstrap reached `current_phase=first_wave_selected` / `last_successful_checkpoint=flow_templates_persisted`, the continuation message switched from the old generic kickoff prompt to the persisted-state resume prompt
  - the model then correctly started with `bootstrap.setup.persist`
- current live state before `9c74498a` consumes:
  - setup tasks `2-6` are done
  - task tree and flow templates are persisted
  - first-wave selection is still pending
  - the model is still rereading `project.get` / `task.list` after the successful persist because the tool result only named the remaining step slugs and not the exact selectable tasks
- next expected check after `9c74498a`:
  - the next successful `bootstrap.setup.persist` result should carry `selectable_first_wave_tasks`
  - the turn engine should append a system instruction naming the exact candidate task ids
  - the model should then call `bootstrap.setup.persist` again with `select-first-wave` instead of rereading project/task state
- latest live seam after that:
  - bootstrap successfully persisted `select-first-wave` and `record-frank-sign-off`
  - validation then failed with:
    - `only 11 of 12 selected first-wave child tasks produced runnable agent_turn jobs`
  - that gap is now patched in `e66f26de`
- latest observed live state after redeploy:
  - `fresh-37` is no longer stuck in bootstrap
  - bootstrap tasks `1-8` are `done`
- current task counts at last check:
    - `done=19`
    - `in_progress=0`
    - `review=1`
    - `blocked=0`
  - task `10` is now `done`
  - task `19` is now `done`
  - only task `15` remains `in_progress`
- runtime note:
  - live worker currently runs with `OTTERCAMP_WORKER_CONCURRENCY=12` via tmux respawn override for faster canary drain
- this is the clean post-fix validation run to keep watching

## Current live canary focus

- completed prior canary: `fresh-37`
  - project id: `0fe2a391-3268-4a3f-874c-6ea5fe620a88`
  - final state: all `20` tasks `done`
  - task `15` was the live proof for:
    - `1c3ff309` max-visit review rejections stay in `review`
    - `1eab9c88` resumed-review lanes dispatch through flow wakeups
- current active canary: `fresh-38`
  - project id: `722fab7b-6949-4969-998f-402ab6325cd0`
  - project session: `d6d74892-f6db-45f2-bf48-a034b1a393f0`
  - current state at latest check:
    - `done=21`
    - `in_progress=2`
    - `review=1`
    - `draft=3`
    - `blocked=0`
  - active non-done lanes at latest check:
    - task `11` `Collect & Aggregate Runtime Metrics` `in_progress`
    - task `20` `Execute Test Scenario 2 (Agent Handoff)` `in_progress`
    - task `21` `Execute Test Scenario 3 (Error Handling)` `review`
    - tasks `28-30` remain `draft`
  - important live proof:
    - deferred later-wave activation is working
    - stale/closed task dispatch cleanup is working
    - the remaining hotspot has narrowed to review/work quality on the last few lanes, not queue deadlock

## Current next seam

- `fresh-38` exposed the blocked-session cleanup seam, but it is no longer the clean validator because those blocked sessions were already stale before `945c9fdf` deployed
- new primary canary: `fresh-39`
  - project id: `3a46cac2-bd73-4579-8940-7fd6565fcecb`
  - project session: `0bc0235e-d259-491f-bf47-270e8a5b05c4`
  - kickoff message id: `8f4df481-cbce-44ed-821d-3e5d29252dd2`
  - current state:
    - `done=11`
    - `in_progress=1`
    - `draft=1`
    - no `review`
    - no `blocked`
  - completed live proofs on this canary:
    - bootstrap completed cleanly
    - first-wave execution started cleanly
    - review lanes for tasks `10` and `12` drained to `done` without stale blocked/review session residue
  - current remaining live lane:
    - task `13` `Synthesize Validation Findings & Report`
    - active task session: `c2a2b78e-6e5f-444c-b426-e6bf2e0e4835`
    - active execution: `4b3311a9-60f9-435a-9a01-0ceff766a6a1`
    - current issue family is no longer queue ownership; it is repeated recovery/context-inventory churn on the final synthesis task
    - latest important live proof after `b1d2d8bb`:
      - newest `[Recovery resume state]` message now shows:
        - `Target file: Work/OC-13-SYNTHESIZE-VALIDATION-FINDINGS-REPORT.md`
      - before that slice, the same lane kept targeting:
        - `planning/discovery-plan/oc-13-validation-plan.md`
    - latest remaining live seam after `f2f0454f`:
      - the lane did write to the correct `Work/...` path
      - but the pre-fix active retry chain had already persisted a bad questionnaire draft beginning:
        - `I'm standing by to validate the Speaker Pipeline Ops workflow...`
      - `f2f0454f` adds explicit rejection for that `What I need from you / Concise ask` placeholder family so the next fresh retry should stop reusing it as file body content
    - latest remaining live seam after `67164d19`:
      - the reopened post-review work lane had been inheriting a bogus continuation summary that said:
        - `Before I proceed, I need clarification on: Source Data / Severity Definitions / Documentation Format`
      - `67164d19` now rejects that continuation-summary family as non-file-body placeholder content
      - latest live proof: the fresh work turn moved on to reading the real OC-10/11/12 report artifacts instead of reopening that clarification checklist
    - latest remaining live seam after `510f6f27`:
      - the lane was still reusing two fallback placeholders as if they were draft content:
        - `Task execution is already underway...`
        - `I don't see a durable draft... Please provide the substantive draft or recovery artifact...`
      - `510f6f27` now:
        - treats the missing-draft continuation-summary family as unavailable summary output
        - rejects both fallback families as non-file-body draft content
      - the current in-progress turn on `fresh-39` still shows some pre-deploy baggage from the already-open retry chain, so the next clean retry is the real validation point
    - latest remaining live seam after `6ada1e4a`:
      - another stale placeholder family is still visible on disk/in-session:
        - `# Ready to Continue OC-13 Validation Synthesis ... What I need from you ... I'll move quickly once you confirm direction.`
      - `6ada1e4a` rejects that family as non-file-body confirmation text
      - the active turn observed immediately after deploy was still carrying pre-deploy history, so the next fresh retry is again the real validation point
- the next move from restart should be:
  1. continue watching `fresh-39`
  2. confirm whether task `13` now stops reusing the questionnaire draft and writes a real synthesis report body
  3. if it still loops, patch the next remaining non-report placeholder variant rather than steering the task

The next move from here:

1. continue watching `fresh-38`
2. if completed-bootstrap stale messages reappear, start `fresh-39` to validate `5f644cd5` on a clean end-to-end cycle
3. keep implementing `issues/samtalkoutcomes.md` slices rather than falling back to manual steering

## Latest pushed slice

- `a679c02c` `Ensure supervisor recovery sessions get kickoff messages`
- what it changed:
  - supervisor recovery now ensures kickoff seeding for started, promoted, and coalesced wakeups; only deferred wakeups skip kickoff
  - supervisor kickoff metadata now includes `flow_node_execution_id`
  - supervisor dedupes on existing execution-scoped recovery kickoff messages before appending
  - added unit coverage forcing the coalesced-wakeup path in `internal/controlplane/service_test.go`
- live proof:
  - task `10` on `fresh-37` moved from the old empty repaired-session shape to `done`

- `501ab051` `Ignore stale pending turns without live ownership`
- what it changed:
  - worker claim selection now lets a runnable `agent_turn` job through even when `chat_session.current_turn_id` points at a different pending turn, as long as that older pending turn has no live job and no active execution `live_turn_id`
  - added integration coverage for the exact active-execution + stale-pending-current-turn + queued-retry case in `internal/jobqueue/worker_integration_test.go`
- live proof:
  - task `15` had a runnable retry job `06b7954e-c49e-4f60-a48c-381364c2361e` that stayed `pending` until this patch
  - after deploy, that job moved to `done`

- `ff024149` `Prefer newest pending turns for session ownership`
- what it changed:
  - `repo.CanonicalLiveTurn(...)` now treats the newest pending turn as canonical instead of the oldest pending turn
  - updated `internal/repo/chat_test.go` to match the new pending-turn ordering
- live proof:
  - task `15` session `7d9452fd-ba2a-45b3-ae16-a949ccfd70ce` no longer snaps `current_turn_id` back to the obsolete supervisor recovery turn `0da5f872-615f-4570-866a-c67a337d71fd`
  - current live turn is now fresh in-progress turn `bba01b58-e116-4de4-9f61-a3f449ea27a5`

- `1c3ff309` `Keep exhausted review rejections in review`
- what it changed:
  - review-node `flow.review_decision reject` calls that hit `ErrMaxVisitsExceeded` no longer force the task into `blocked`
  - the task stays in `review`, which keeps the lane resumable instead of converting it into a dead-end status
- live proof:
  - task `15` stopped falling permanently into blocked-on-review and was able to keep progressing through fresh review executions

- `1eab9c88` `Dispatch review resumes through flow wakeups`
- what it changed:
  - `processQueuedTask(...)` now routes `task.status_changed -> review` through `ensureFlowRun(...)` instead of only `ensureTaskFlowExecutionState(...)`
  - that means blocked-review resumes now create the wakeup run, kickoff message, and turn instead of stopping at `active execution + waiting_for_turn`
  - added integration coverage in `internal/controlplane/task_queue_processor_integration_test.go` for resumable blocked review tasks
- verification:
  - `go test ./internal/controlplane -count=1`
  - `go test -tags=integration ./internal/controlplane -run 'TestTaskQueueProcessorIntegrationResumeValidationBlocked(ReviewTaskStartsFreshTurn|TaskStartsFreshTurn)' -count=1`
  - sequential rebuild/restart completed after the commit
- live proof:
  - task `15` on `fresh-37` no longer stranded at `active review execution + empty waiting session`
  - `fresh-37` is now fully drained with all tasks `done`

- `5f644cd5` `Retire stale bootstrap continuation messages`
- what it changed:
  - when bootstrap materializes/completes, the turn engine now finalizes pending `project_bootstrap` user messages so they cannot be requeued later
  - stale `project_bootstrap` auto-continue turns that still fire after completion now get an explicit redirect out of bootstrap mode instead of appending another bootstrap resume state
  - added focused unit coverage in `internal/turn/engine_test.go` for:
    - redirecting completed-bootstrap auto-continue turns
    - finalizing pending bootstrap-source user messages
- verification:
  - `go test ./internal/turn -count=1`
  - sequential rebuild/restart completed after the commit

## Fresh-37 closeout

- final project state:
  - `done=20`
  - `in_progress=0`
  - `review=0`
  - `blocked=0`
  - `draft=0`
- final task-15 state:
  - `task_number=15`
  - `work_status=done`
  - title: `Test speaker update endpoint`
- useful last-seam IDs:
  - transient stranded review execution: `d1fc2db0-e353-489e-bdb9-0ac4c80991ee`
  - transient closed review session from that seam: `dddfb37a-affa-4993-9a05-2e8de2a3ecd1`
  - final task record: `e8f3e0b1-4a3e-4860-b164-7728e2ea4ae1`

- `719d0d47` `Show workspace roots in task prompts`
- what it changed:
  - task prompt context now includes `Workspace Root: ...` derived from the project slug via `internal/workspace.ProjectRoot`
  - added prompt test coverage so the workspace-root line is present in task-scoped prompts
- live intent:
  - the next task-scoped retry/recovery turn now has the concrete workspace path in prompt context instead of forcing the model to guess paths like `/workspace`

## Useful commands / IDs

- `fresh-35` project: `8fe0b85a-3804-4488-9160-f0ae95c2ca00`
- `fresh-35` session: `680f26e4-8885-4dff-9894-459fef2ec562`
- `fresh-36` project: `4e738c1d-d1b9-4232-82db-9de1f9a55212`
- `fresh-36` session: `b63a3a1d-413e-4d02-b01a-b685268b907e`
- `fresh-37` project: `0fe2a391-3268-4a3f-874c-6ea5fe620a88`
- `fresh-37` session: `fa7fb41c-65a8-4f10-8438-c9f28bc1d634`

## Primary references

- `issues/samtalkoutcomes.md`
- `issues/samtalk.md`
- `internal/turn/engine.go`
- `internal/turn/engine_test.go`
- `internal/tools/native/mutation_tools.go`
- `internal/tools/native/native_integration_test.go`
## 2026-03-23 Review kickoff dedupe

- Latest pushed fix: `5f4a815c` `Deduplicate wakeup kickoff messages`
- Files:
  - `internal/controlplane/task_queue_processor.go`
  - `internal/controlplane/task_queue_processor_test.go`
- Root cause confirmed on `fresh-38`:
  - task `14` session `e2f4ef39-c81b-4738-ab8c-98e9213d3541`
  - active execution `ef20dcea-6c8b-4f8b-88c3-ce11231356af`
  - duplicated pending `Start review on task:` messages existed for the same `run_id=1ed316a3-d065-40fb-9ee3-ed9585990eed`
  - synthetic `task_review_action` prompt was also present, so review turns were receiving overlapping kickoff/action inputs
- Fix shipped:
  - `appendWakeupKickoff(...)` now reconciles existing kickoff messages by `run_id` before appending
  - duplicate pending kickoff messages are finalized, keeping only the newest kickoff for that run
  - added unit test `TestEnsureFlowRunFinalizesDuplicatePendingKickoffs`
- Verification:
  - `go test ./internal/controlplane -run 'TestEnsureFlowRun(KickoffIsIdempotent|FinalizesDuplicatePendingKickoffs)$' -count=1`
  - `go test ./internal/controlplane -count=1`
  - rebuilt `./bin/ottercamp`
  - sequentially respawned `oc-svc` and `oc-worker`
  - `./bin/ottercamp health` green
- Live result after deploy:
  - active review task initially moved to task `26`
  - active review session `ec4c19bc-3cc4-42be-b0a9-b88e63e91b90`
  - active execution `57e9255c-0415-4ecb-9b9c-e182b9c9ed31`
  - active session had exactly one pending `task_queue_processor` review kickoff for run `1ffc17ca-5c17-49bc-9aae-7455cb53c595`
  - one pending `agent_turn` job existed for that session/message
  - old pending kickoff rows remained only on closed historical review sessions, not duplicated inside the live review run
- Follow-on observation:
  - task `26` then progressed through review handling and returned to `in_progress`
  - the review execution `57e9255c-0415-4ecb-9b9c-e182b9c9ed31` is now `rejected`
  - the review session `ec4c19bc-3cc4-42be-b0a9-b88e63e91b90` is closed
  - fresh-38 current state is now:
    - `done=10`
    - `draft=13`
    - `in_progress=4`
    - `review=1`
  - current review lane has moved to task `19`
- Current next seam:
  - keep monitoring `fresh-38` task `19` review lane for whether the same review-turn cleanup/retry pathology repeats
  - kickoff duplication is no longer the active bug family on the canary

## 2026-03-23 Review decision auto-apply

- Latest local work in progress:
  - none; latest slice is committed and pushed
- Live pattern that motivated it on `fresh-38`:
  - review turns for tasks like `19` and `24` often produce explicit prose decisions (`REJECT`, findings, rework required)
  - but they still miss the `flow.review_decision` tool call
  - current engine behavior retries or blocks only after completion, which still wastes turns and leaves review lanes churning
- Fix in progress:
  - `handleCompletedReviewTurnWithoutDecision(...)` now detects explicit textual approve/reject decisions in the assistant output
  - when the decision is unambiguous, the engine auto-applies it through `flowAdvancer` instead of retrying for a missing tool call
  - added unit test `TestHandleTurnCompletedEventAutoRejectsExplicitReviewDecision`
- Verification:
  - `go test ./internal/turn -run 'TestHandleTurnCompletedEvent(RetriesReviewTurnWithoutDecision|BlocksRepeatedReviewTurnWithoutDecision|AutoRejectsExplicitReviewDecision)$' -count=1`
  - `go test ./internal/turn -count=1`
- Next immediate step:
  - commit/push this review auto-decision slice
  - redeploy sequentially
  - verify live review lanes stop looping on prose-only rejections

## 2026-03-24 Directory deliverable guidance

- Latest local work in progress:
  - `internal/tools/native/mutation_tools.go`
  - `internal/tools/native/mutation_tools_test.go`
- Live blocker after `5b5add16`:
  - `fresh-38` task `19` is now blocked in the work lane, not review
  - blocker signature: repeated `file.write:deliverable_path_required`
  - task contract declares `Output: Test`
  - work lane keeps trying to write `planning/prd-spec/oc-19-prd.md` instead of a concrete file under `Test/`
- Fix in progress:
  - when the explicit deliverable path looks like a directory, native validation now says:
    - explicit deliverable directory `Test/`
    - write a concrete file under `Test/`
  - added unit test `TestFileWriteRejectsPlanningArtifactMutationForExecutionFirstTaskWithDirectoryOutput`
- Verification:
  - `go test ./internal/tools/native -run 'TestFileWriteRejects(PlanningArtifactMutationForExecutionFirstTaskWithExplicitDeliverable|AlternateMutationForExecutionFirstTaskWithExplicitOutput|PlanningArtifactMutationForExecutionFirstTaskWithDirectoryOutput)$' -count=1`
  - `go test ./internal/tools/native -count=1`
- Next immediate step:
  - commit/push this deliverable-directory guidance slice
  - redeploy sequentially
  - verify task `19` can be resumed under the clearer path guidance

## 2026-03-24 Supervisor recovery continuation fallback

- Latest local work in progress:
  - `internal/turn/engine.go`
  - `internal/turn/engine_test.go`
- Live failure after the earlier review/work fixes:
  - task `15` review session `f0990e29-fa1d-4186-b8b1-8e116179b1a8`
  - supervisor recovery plus context compression generated a continuation summary that asked for:
    - context of the interrupted task
    - what were we working on
    - current status / next steps
  - later live variant also said:
    - `I don't see any active task context to resume`
    - `It appears this may be the start of our conversation`
    - `Remaining work`
  - newest live variant also said:
    - `# Task Resume`
    - `you've sent the resume command three times`
    - `what task needs to be resumed`
    - `Task Description`
  - assistant then fell into generic review chat instead of continuing autonomously
- Fix in progress:
  - widened `continuationSummaryLooksUnavailable(...)` so supervisor-style context questionnaires are treated as unavailable summaries
  - stronger rule: async task sessions now skip model-generated continuation summaries entirely when recent user history contains a supervisor recovery prompt, and go straight to `taskExecutionContinuationFallbackSummary()`
  - added regression test `TestContinuationTurnUsesTaskFallbackSummaryForSupervisorContextQuestionnaire`
- Verification:
  - `go test ./internal/turn -run 'TestContinuationTurn(NormalizesGenericNoContextSummary|UsesTaskFallbackSummaryForAsyncProjectTask|UsesTaskFallbackSummaryForSupervisorContextQuestionnaire)$' -count=1`
  - `go test ./internal/turn -count=1`
- Next immediate step:
  - commit/push this continuation-summary hardening
  - redeploy sequentially
  - verify task `15` review recovery uses the direct task fallback summary instead of asking for context

## 2026-03-24 Claim-time stale dispatch cleanup

- Latest local work in progress:
  - `internal/jobqueue/worker.go`
  - `internal/jobqueue/worker_integration_test.go`
- Live repro after the earlier review/recovery slices:
  - startup purge was already dead-lettering stale terminal message-attempt dispatches
  - but a cancelled retry job could still remain `pending` if it was enqueued after startup purge ran
  - concrete live repro before the fix:
    - job `e3facc8d-7034-4ead-af47-526b1fe7d740`
    - session `bd342830-1cb8-4f52-9ffe-c0531076915b`
    - message `a46c4095-fba7-465b-8d44-831ec873530a`
    - retry `2`
    - matching turn `6b4aabb5-c809-4a8e-aea1-2507027601e8` already existed with `status=failed`
  - claim logic was skipping this stale job, but leaving it `pending`, so the queue retained cancelled/superseded dispatch noise until the next restart
- Fix in progress:
  - `claimPendingAgentTurns(...)` now dead-letters exact terminal message-attempt jobs before claiming fresh `agent_turn` work
  - added integration test `TestJobWorkerClaimPendingAgentTurnsDeadLettersTerminalMessageAttemptDispatch`
- Verification:
  - `go test ./internal/jobqueue -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(ClaimPendingAgentTurnsDeadLettersTerminalMessageAttemptDispatch|PurgeStaleAgentTurnJobsRemovesTerminalMessageAttemptDispatches)$' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted `oc-svc` and `oc-worker` sequentially
  - health is green
  - the stale live repro job is gone; current pending `agent_turn` jobs are fresh active dispatches, not cancelled leftovers

## Current live state after claim-time cleanup

- Latest pushed/local baseline for this slice:
  - `bc978464` `Dead-letter stale task dispatches during claim`
- `fresh-38` current counts:
  - `done=14`
  - `draft=15`
  - `in_progress=1`

## 2026-03-24 Explicit deliverable-directory completion signal

- Latest local work in progress:
  - `internal/turn/engine.go`
  - `internal/turn/engine_test.go`
- Live repro after `bc978464`:
  - queue cleanup no longer blocked `fresh-38`
  - task `26` moved back to `in_progress` on active session `deb36032-8917-40d7-8b80-f99e7e1ecd0d`
  - it successfully wrote concrete files under the explicit deliverable directory:
    - `Metrics/OC-26-QUALITY-GATE-SECOND-REVIEW.md`
    - `Metrics/oc-26-metric-tree.md`
    - `Metrics/oc-26-instrumentation-plan.md`
  - but the lane did not stop after those successful writes and continued issuing more tool calls in the same turn
  - root cause:
    - `explicitExecutionDeliverableWriteCompleted(...)` only treated an exact file-path match as terminal
    - explicit deliverable directories like `Output: Metrics` were accepted by native mutation guards, but not by the turn-engine stop/completion signal
- Fix in progress:
  - `writtenPathMatchesExplicitDeliverable(...)` now treats child files under an explicit deliverable directory as matching the task deliverable
  - `explicitExecutionDeliverableWriteCompleted(...)` and `shouldStopAfterExecutionArtifactWrite(...)` now stop on successful writes under that directory
  - added regression tests:
    - `TestExplicitExecutionDeliverableWriteCompletedRecognizesDirectoryOutputChildFile`
    - `TestShouldStopAfterExecutionArtifactWriteForExplicitDirectoryOutput`
- Verification:
  - `go test ./internal/turn -run 'Test(ExplicitExecutionDeliverableWriteCompletedRecognizes(OutputPath|DirectoryOutputChildFile)|ShouldStopAfterExecutionArtifactWrite(ForPlannedArtifact|ForExplicitDirectoryOutput))$' -count=1`
  - `go test ./internal/turn -count=1`
  - rebuilt and redeployed sequentially
  - health is green
- Current note:
  - the active task-26 turn `1a9cbae7-8706-4fc7-b248-c7d2018ac14d` started before this patch deployed, so live verification needs the next fresh retry/turn under the new build

## Current live state after 02f0b6d3

- Latest pushed commits in this stretch:
  - `bc978464` `Dead-letter stale task dispatches during claim`
  - `02f0b6d3` `Stop execution turns after deliverable directory writes`
- Key live validation result:
  - `fresh-38` proved the deferred-wave continuation contract is now working in practice
  - after the first wave drained, later-wave tasks auto-activated without operator intervention
- Concrete live evidence:
  - task `26` moved from the old blocked planning-write loop onto a fresh retry under the new build
  - the fresh retry wrote `Metrics/oc-26-dashboard-spec.md`
  - the turn completed cleanly instead of chaining more writes
  - immediately afterward, the project advanced into the later wave
- Current `fresh-38` state at last check:
  - `done=18`
  - `in_progress=6`
  - `review=3`
  - `draft=3`
- Current open work is no longer bootstrap or deferred-wave activation.
  - the next likely seam is later-wave execution/review quality, especially review sessions that keep inspecting instead of calling `flow.review_decision`

## 2026-03-24 Claim-time closed-session dispatch cleanup

- Latest local work in progress:
  - `internal/jobqueue/worker.go`
  - `internal/jobqueue/worker_integration_test.go`
- Live repro after the later wave activated:
  - fresh later-wave review churn created pending `agent_turn` jobs with `last_error=turn cancelled`
  - examples:
    - `b00fa3a6-a8c0-40a7-b0c2-60f9c47ec992`
    - `ed189401-0498-4a55-bcd3-f23d436c8feb`
  - both pointed at sessions already in `status=closed`
  - startup purge handled them on restart, but claim-time cleanup still only dead-lettered exact terminal attempts, not closed sessions
- Fix in progress:
  - `claimPendingAgentTurns(...)` now dead-letters closed/archived-session `agent_turn` jobs before claim selection
  - updated integration coverage:
    - `TestJobWorkerClaimPendingAgentTurnsDeadLettersClosedSessions`
    - `TestJobWorkerClaimPendingAgentTurnsDeadLettersTerminalMessageAttemptDispatch`
- Verification:
  - `go test ./internal/jobqueue -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerClaimPendingAgentTurns(DeadLettersClosedSessions|DeadLettersTerminalMessageAttemptDispatch)$' -count=1`
  - rebuilt and redeployed sequentially
  - health is green
- Live note:
  - after redeploy the specific stale jobs are gone from the pending/claimed set
  - the observed rows are now terminal and no longer queue noise

## Current later-wave validation status

- Latest pushed commits:
  - `02f0b6d3` `Stop execution turns after deliverable directory writes`
  - `960292f3` `Dead-letter closed task sessions during claim`
- Current canary remains:
  - `fresh-38`
  - project id `722fab7b-6949-4969-998f-402ab6325cd0`
- Important current result:
  - later-wave execution is still progressing without manual steering
  - there are no blocked tasks
  - there are no pending `agent_turn` jobs left with `last_error=turn cancelled`
- Latest observed counts:
  - `done=20`
  - `in_progress=5`
  - `review=2`
  - `draft=3`
- Current likely next seam:
  - review-lane efficiency/decisioning, not queue ownership
  - active review lanes are still inspection-heavy and have not yet shown fresh `flow.review_decision` calls in the later wave
  - however, review churn is advancing: several review turns have exited cleanly with `session_closed` and sent tasks back to `in_progress` without creating new queue deadlocks
  - current active review set has narrowed again to task `10` and task `27`
  - both are still in first in-progress review turns under active execution ownership
  - no fresh `flow.review_decision` calls have appeared yet on those live sessions

- ## 2026-03-25 00:18 MDT

- Current tmux/runtime state:
  - tmux session: `codex-e2e-20260324`
  - serve pane `0`: pid `98918`
  - worker pane `1`: restarted under the latest binary, pid changed from the prior `18364`
  - worker concurrency: `16`
  - `./bin/ottercamp health` is green after the latest worker restart

- Latest code changes:
  - [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go)
    - `FailStaleModelInvocations` no longer treats active async `organization`/`project` continuation turns as stale after 15 seconds just because no `agent_turn` job remains claimed.
    - active async non-`project_task` sessions now use the continuation threshold in the stale-invocation branch, which closes the rerun-25/rerun-26 race where worker cleanup failed a valid in-flight continuation and the model callback later hit `model_error: invalid status transition`.
    - `RequeueActiveExecutionSessionsWithoutTurns` now retires stale `run` ownership rows before enqueueing a retry, so execution slots do not remain consumed by `in_progress` runs on sessions with no live turn.
    - `RecoverStaleInProgressTriggeredTurns` now also repairs:
      - stale claimed `project` session dispatches after model completion
      - `project` session stale turns that already have a newer pending/claimed retry job, including retries whose payload omitted `retry_count`
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - `projectBootstrapLastCheckpoint(progress)` now treats persisted staffing drafts as reaching the staffing checkpoint, so automatic-failure metadata no longer collapses draft-staffed bootstrap state back to `project_created`.
    - restart bootstrap validation now skips the narrative-only stall path when the turn already ended with the explicit `[Bootstrap validation recovery reread blocked ...]` system guard message.
  - [`internal/tools/native/mutation_tools.go`](/Users/sam/dev/otter-camp/internal/tools/native/mutation_tools.go)
    - bootstrap `task.create` now resolves the bootstrap work/review flow before planning heuristics even when no `assigned_agent_id` was supplied yet
    - executable bootstrap tasks now normalize `blocks_scope=all -> none` unless the metadata explicitly marks them orchestration-only
  - [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go)
    - added `TestJobWorkerFailStaleModelInvocationsSkipsActiveAsyncOrganizationSession`
    - added `TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsRetiresStaleRunOwnership`
    - added `TestJobWorkerRecoverStaleInProgressTriggeredTurnsFailsClaimedProjectSessionAfterCompletedInvocation`
    - added `TestJobWorkerRecoverStaleInProgressTriggeredTurnsKeepsExistingPendingRetryJobForProjectSession`
    - added `TestJobWorkerRecoverStaleInProgressTriggeredTurnsKeepsPendingRetryJobWithoutRetryCountForProjectSession`

- Verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(FailStaleModelInvocations(SkipsActiveAsyncOrganizationSession|SkipsActiveExecutionTaskSession|FailsOldActiveExecutionTaskSession|RequeuesTriggeredProjectSession)|RequeueActiveExecutionSessionsWithoutTurnsRetiresStaleRunOwnership)' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerRecoverStaleInProgressTriggeredTurns(KeepsPendingRetryJobWithoutRetryCountForProjectSession|KeepsExistingPendingRetryJobForProjectSession|KeepsExistingPendingRetryJob|FailsClaimedProjectSessionAfterCompletedInvocation|FailsNonHeartbeatingClaimedAttemptWithoutRun|FailsOrphanedAttemptWithoutJobRunOrInvocation|FailsPostModelOrphanedExecutionTurn|IgnoresStaleRunWithoutLiveInvocation)' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp)' -count=1`
  - `go test ./internal/turn -run 'Test(BuildProjectBootstrapAutomaticFailureRecordUsesStaffingCheckpointForPersistedDrafts|ProjectBootstrapRecoveryEndedByRereadGuard|BuildProjectBootstrapAutomaticFailureMessageUsesPauseLanguageAfterRecoverableCheckpoint|ShouldStopAfterBlockedProjectKickoffSessionCreate|ContinuationTurnUsesDeterministicActiveRequestSummaryForAsyncOrganizationSession)' -count=1`
  - `go test ./internal/tools/native -run 'TestTaskCreateDuringBootstrap(WithAssignedAgentUsesBootstrapFlowWithoutPlanningHeuristics|WithoutAssignedAgentUsesBootstrapFlowBeforePlanningHeuristics|NormalizesExecutableBlocksScopeFromAllToNone)' -count=1`
  - `go build -o ./bin/ottercamp ./cmd/ottercamp`

- Live evidence from the stale-run retirement fix:
  - `run.status='in_progress'` count dropped from `18` to `13` before the latest restart, and is now down to `10`
  - worker logs after restart show normal claiming/completion activity instead of only dead slot pressure

- Prior live repro that motivated the async-continuation stale-invocation fix:
  - rerun-25:
    - organization session `672477c4-4431-4969-bf7b-923253907972`
    - project `f30714a2-6781-4acb-88be-a9886bd01cd5` was created
    - follow-on continuation then died with `model_error: invalid status transition`
  - rerun-26:
    - project `43da3856-0b9a-4063-af4d-ce0d72982d86`
    - turn `59` / `c532b121-e287-48d8-b25c-c59930d529f2`
    - failed model invocation `4d6fccf1-0017-41a4-94d2-f031baa4929a`
    - error `invalid status transition`
    - this still happened after the earlier `session.create` fast-stop patch, which is why the worker cleanup path was the next seam

- Current live probe:
  - rerun-27 org request was posted successfully at `2026-03-25 00:17:19 MDT`
  - message id: `7a4359b0-a68a-4c4d-baf0-20d9e39bda11`
  - initial dispatch: `bbdadacd-6a3b-4479-889d-3d62a0814033`
  - target org session: `672477c4-4431-4969-bf7b-923253907972`
  - goal of this probe was to prove the fresh async organization continuation can survive project creation without the stale-invocation cleanup racing it into `invalid status transition`

- Live proof from rerun-27:
  - context-compression turn `60` completed normally
  - continuation turn `61` / `45210ec3-5b4f-4acf-9cfe-46b28802d14f` stayed alive past the old 15-second kill window
  - turn `61` completed after six model invocations instead of dying with `worker cleanup failed stale in_flight model invocation without live in-progress turn`
  - real project row now exists:
    - project `dfb00cae-0d39-407f-b215-d6688874cf1e`
    - slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-27`
    - delivery mode `gated`
  - the org turn hit the `session.create` handoff-only guard as intended and still completed cleanly
  - a real async project session was backfilled:
    - session `77bbae17-4d08-400f-9cda-d81944c4611d`
    - kickoff handoff message `3546fb81-0f1a-473c-a075-610f37535ff9`
  - seeded bootstrap tasks remain exactly `1-8`; the older duplicate-bootstrap regression (`9-13`) has not reappeared so far on rerun-27
  - kickoff turn `1` / `54e0d685-5e4f-40bd-a43d-f8f2d88a4233` later failed on the bootstrap watchdog after persisting staffing:
    - `bootstrap setup watchdog timed out after 1m30s after partial persisted setup (staffing_drafts=3, assignments=0, scoped_tasks=0, flow_templates=0, first_wave_tasks=0, first_wave_promoted=0, first_wave_execution=0, first_wave_jobs=0)`
  - the project settings now show:
    - `project_bootstrap.status = active`
    - `last_successful_checkpoint = staffing_persisted`
    - `current_phase = task_tree_persisted`
  - recovery turn `2` / `0f7c373d-dfbc-4e9d-a0d9-1078f724b518` then exposed a second queue bug:
    - its only model invocation completed
    - assistant message `6b353cdd-ad09-4e33-b075-29403999ea28` stayed `streaming`
    - the old claimed retry job was demoted back to `pending`
    - stale `current_turn_id` still blocked the session
  - after the latest worker patches/restart, that stale turn was recovered correctly:
    - turn `2` is now `failed` with `recovered stale in-progress message turn without live job or execution; scheduling a fresh retry`
    - old retry job `3ccb3081-d5a4-4f39-b495-b8e0929e0010` is dead-lettered
    - new retry job `0840b247-90d9-46fe-8636-5b3082404722` is created with `retry_count=1`
    - fresh project-session turn `3` / `2607caa9-5d8a-428a-99e5-6a0e23ff8dbd` is now `in_progress`
  - that fresh retry completed and exposed the next seam:
    - the archived original project `dfb00cae-0d39-407f-b215-d6688874cf1e` still recorded inconsistent automatic-failure metadata:
      - `failure_phase = project_created`
      - `last_checkpoint = project_created`
      - `last_successful_checkpoint = staffing_persisted`
    - root cause was `projectBootstrapLastCheckpoint(progress)` ignoring `StaffingDraftCount`
  - the runtime then created restart project `8c82b36a-1f90-45a3-b984-d350f6a48574` and restart session `66364714-3bef-46a5-8251-af4a2bd21b23`
  - that restart also persisted staffing (`staffing_persisted`) but got archived because turn `2` ended with:
    - assistant narrative
    - blocked `project.get`
    - system message `[Bootstrap validation recovery reread blocked - ending this turn so the next continuation can repair the named blocker directly.]`
    - yet the validator still classified it as `automatic bootstrap restart replied with narrative only and never persisted staffed executable work`
  - latest local patch fixes both of those engine-side issues, but they have not yet been re-proven live on a fresh rerun under the newest binary
  - rerun-28 then live-proved those engine-side fixes:
    - project `fae427e5-a8c3-441a-a184-66beb9dfbf47` stayed `active`
    - reread-blocked recovery turns `3` and `4` did not trigger the old false restart archive path
    - automatic failure metadata is now coherent:
      - `failure_phase = task_tree_persisted`
      - `last_checkpoint = task_tree_persisted`
      - `last_successful_checkpoint = flow_templates_persisted`
  - rerun-28 exposed the next live bootstrap creation seam:
    - first kickoff turn `1` persisted staffing and assignments successfully:
      - `staffing_drafts=3`
      - `assignments=3`
    - subsequent bootstrap work created tasks `9-13`, but they were contaminated as planning/gate tasks:
      - each had `blocks_scope = all`
      - each had planning metadata/artifact contracts
      - each had `bootstrap_first_wave_selected = false`
    - bootstrap then paused with:
      - `kickoff validation failed: no bounded first-wave tasks remain after excluding orchestration-only parent workstreams`
    - root cause identified in native `task.create`:
      - bootstrap flow resolution happened too late when `assigned_agent_id` was omitted, so planning heuristics attached first
      - executable bootstrap tasks were allowed to retain `blocks_scope=all`
  - latest local patch fixes those native bootstrap task-creation issues, but this native-layer fix is not yet live-proven on a fresh rerun

## 2026-03-24 Review rejection max-visits deadlock

- Latest local work in progress:
  - `internal/flow/execution_service.go`
  - `internal/flow/execution_service_test.go`
  - `internal/flow/execution_service_integration_test.go`
  - `internal/tools/native/mutation_tools.go`
  - `internal/tools/native/native_integration_test.go`
- Live repro:
  - task `27` `Final Validation Sign-Off`
  - review lane called `flow.review_decision`
  - tool returned `max visits exceeded`
  - reviewer then retried into a deadlock loop because the service refused to record any terminal outcome once the reject path visit cap had been hit
- Fix in progress:
  - review-node rejection at reject-path max visits now rejects the current review execution and transitions the task to `blocked` terminal flow state instead of throwing `ErrMaxVisitsExceeded` back to the reviewer
  - focused flow tests updated for the new behavior
- Verification:
  - `go test ./internal/flow -run 'TestRejectFlowNodeMaxVisitsExceeded$' -count=1`
  - `go test -tags=integration ./internal/flow -run 'TestFlowExecutionServiceRejectFlowNodeMaxVisits$' -count=1`
  - rebuilt and redeployed sequentially
- Live result:
  - task `27` is now `done`
  - project state after deploy:
    - `done=21`
    - `in_progress=3`
    - `review=3`
    - `draft=3`
- Follow-on fix in progress:
  - `flow.review_decision` now reports a blocked terminal outcome when a reject decision exhausts the reject-path visit cap, instead of returning a misleading `next_node_id`
  - focused native integration test added:
    - `TestIntegrationFlowReviewDecisionRejectReturnsBlockedWhenRejectPathExhausted`


## 2026-03-24 06:14 MDT
- Pushed `5f6bbeeb` `Resume failed bootstrap sessions on project resume`.
- Root cause for missing live resume validation was broader: `project.resumed` events from the project service were silently failing because `domain_event.actor_type` only accepts `human`, while project service was publishing `human_user`.
- Pushed `018b2fd1` `Normalize project domain event actors`.
- After that, live `project.resumed` events were written again, but worker logs showed resume-triggered pending bootstrap messages were still skipped because they observed the project as paused during the same event tick.
- Pushed `3f228033` `Bypass stale pause checks on project resume` to bypass pause suppression during resume-triggered enqueue.
- Live validation then showed the deeper race: the project row could still present `settings.pause` during the resume handler/execution path, so queued jobs were created but `HandleTurnJob` still skipped them as paused.
- Pushed `17c09de4` `Clear stale project pause on resume`.
- Live validation on `fresh-43` now passes the resume deadlock:
  - project `f2d31978-5849-46b1-b924-d6a366d5adda`
  - session `d8d679cc-dd5a-4ead-a406-35de6675b2e4`
  - project settings pause is cleared in DB after resume
  - session now has active `current_turn_id = c2769033-d91d-4608-af2b-0e6cec5ca256`
  - worker no longer suppresses this project session as paused
- Remaining task for this lane: watch the resumed bootstrap repair actually restaff a reviewer / persist the repair, then continue using the active canary for the next failure class.


- Pushed `5cb20e85` `Prefer fresh bootstrap recovery on resume`.
- Live proof on `fresh-43` (`f2d31978-5849-46b1-b924-d6a366d5adda`): failed bootstrap resume now appends a fresh pending recovery message instead of replaying stale pre-failure bootstrap-complete prompts.
- Current recovery message id: `b3507a80-6060-41f1-a875-bfea2f55697c`.
- Current active turn id after resume: `995523c3-f1a9-46a6-8aa4-9c39eb155308`.
- Project pause remains cleared while the recovery turn is active.
- The live recovery turn has already made real progress: Florian (`3dfee822-a82e-4705-8215-c3341dd8681a`) is now assigned as active `reviewer` on the project.
- The next likely seam is not resume plumbing anymore, but whether the bootstrap recovery turn persists the required first-wave reviewer/task-review metadata cleanly enough to clear `reviewer_assignment_missing` without re-pausing.

## 2026-03-25 01:41 MDT

- tmux session remains `codex-e2e-20260324`
  - pane `0` serve PID `16732`
  - pane `1` worker PID `17314`
  - pane `2` shell PID `19786`
  - pane `3` shell PID `18643`
- stack is healthy after the latest restart:
  - `./bin/ottercamp health` passes
  - runtime env restored from local `.env`, including:
    - `OTTERCAMP_MASTER_KEY=xXkoTOQSHWQ0+p6TwamcxojVDOhr/w45M2q6MMEIpVY=`
    - DB `postgres://ottercamp:oc_pg_xK9mR2vL8nQ4wP7z@localhost:5432/ottercamp_oc2?sslmode=disable`
    - `OPENCLAW_WS_SECRET=imD43miaJssorV48gcUTJPwFIHiQJpUg8peghsDARHk`
- new local code in this stretch:
  - [`internal/controlplane/task_queue_processor.go`](../internal/controlplane/task_queue_processor.go)
    - task-completed auto-complete helpers now ignore `tasksvc.ErrFlowTemplateRequired` instead of replaying the same historical `task.status_changed` event forever
  - [`internal/controlplane/task_queue_processor_test.go`](../internal/controlplane/task_queue_processor_test.go)
    - added regression for parent auto-complete leaking `ErrFlowTemplateRequired`
  - [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
    - `RequeueStrandedUserMessageTurns` now ignores newer assistant/system messages and selects the latest stranded pending user message instead
  - [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
    - added regression for the exact rerun-30 shape: a newer failed assistant stub must not block requeue of the real pending continuation user prompt
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
    - organization continuation summaries now skip synthetic `organization_continuation_resume` prompts and walk back to the last substantive user request
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
    - added regression proving a synthetic continuation prompt reuses the prior real create-project request
- verification completed:
  - `go test ./internal/controlplane -run 'TestTaskQueueProcessorHandleTaskCompletedEvent(IgnoresFlowTemplateRequiredFromParentAutoComplete|IgnoresFlowTemplateRequiredFromFollowOnQueue|AutoCompletesParentTask|CatchesUpDormantParentTasks|AutoCompletesBootstrapPlanningTasks|FailsBlockedTrackingRuns)' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RequeueStrandedUserMessageTurns|RequeueStrandedUserMessageTurnsIgnoresNewerFailedAssistantStub|FailStaleModelInvocationsRequeuesTriggeredProjectSession|RequeueActiveProjectSessionsWithoutTurns)' -count=1`
  - `go test ./internal/turn -run 'Test(ContinuationTurnUsesDeterministicActiveRequestSummaryForAsyncOrganizationSession|ContinuationTurnUsesPriorRealRequestWhenTriggeredBySyntheticOrganizationContinuationPrompt|ContinuationTurnAppendsDirectActionPromptForAsyncOrganizationSession|ContinuationTurnUsesCurrentTriggerMessageForOrganizationContinuationSummary)' -count=1`
  - rebuilt `./bin/ottercamp` after each patch set and restarted tmux sequentially
- live result: the hot `controlplane.task-completed seq=1798643` replay loop is no longer visible in the fresh worker tail after the controlplane patch/redeploy
- rerun-30 organization lane:
  - org session `672477c4-4431-4969-bf7b-923253907972`
  - stale turn `67` failed cleanly:
    - turn `ac04a5de-4208-48f1-897c-0e380daf44f0`
    - failure: `worker cleanup failed stale in_flight model invocation without live in-progress turn`
    - stale model invocation `d8cf0e5d-b8cd-4f65-9b61-2a58298973c9` now `failed`
  - worker requeue fix proved live:
    - startup requeued the stranded synthetic continuation user prompt `547`
    - dead-lettered stale queued dispatch `15e44e20-7743-4bd9-8e5a-6cd20174973c`
    - created fresh continuation turn `68` / `f95127a9-1b9d-4b20-8c2d-b9283fdcc3f7` for trigger message `26d545be-64ac-48fe-bc93-cd173a6ebe9d`
  - that continuation then context-compressed again and spawned:
    - summary `551` / `a36ac67a-0aa8-419b-9096-2d1c71e2a419`
    - fresh synthetic action prompt `552` / `e9fabbe2-2465-4649-8971-dbc1b718dc22`
    - current live turn `69` / `5099af79-fc13-46e0-b3e3-4204c279da87`
  - current turn `69` is still genuinely active, not orphaned:
    - it has a long chain of completed `agent_turn` model invocations from `01:37:40 MDT` through `01:39:56 MDT`
    - current in-flight invocation is `2015dfdd-05db-4b77-9417-20f5e5fe2e9b` created `2026-03-25 01:39:57 MDT`
  - project creation is still not complete:
    - no `speaker-pipeline-ops-validation-fresh-20260324-rerun-30*` project row exists yet
- important live caveat:
  - the code fix for synthetic org continuation summaries is in the latest binary and unit-tested, but it has **not yet been proven live**
  - current turn `69` began before that last fix was deployed and is still running on the old continuation root
  - evidence of the old bad root is visible in message stream `553-595`: the org lane drifted into an old active project/session instead of creating rerun-30
- next step:
  - wait for turn `69` to finish or be cleaned up, then confirm the next retry uses the prior real request (`543`) rather than the synthetic continuation prompt (`547`/`552`)
  - only after that should rerun-30 be considered recovered

## 2026-03-25 01:50 MDT

- latest local worker hardening:
  - [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
    - removed eager `RequeueActiveProjectSessionsWithoutTurns(...)` from post-`agent_turn` completion repair
    - rationale: that immediate repair path was re-enqueuing live async project sessions even while provider rate limits were already firing, causing hot retry churn and wasted slots
    - execution-session repair after `agent_turn` completion remains enabled
- verification:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RequeueActiveProjectSessionsWithoutTurns|RequeueStrandedUserMessageTurns|RequeueStrandedUserMessageTurnsIgnoresNewerFailedAssistantStub|RequeueActiveExecutionSessionsWithoutTurns)' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux again
- current tmux pane PIDs after the latest redeploy:
  - pane `0` serve PID `73551`
  - pane `1` worker PID `73751`
  - pane `2` shell PID `19786`
  - pane `3` shell PID `18643`
- health remains green after the redeploy
- live observation after the worker patch:
  - provider rate limits are still active on connections:
    - `ed226b62-8526-498e-8804-a495a48fa58d`
    - `f5407b75-723e-4909-b79f-da951ded9c73`
  - but the specific log spam
    - `job queue: requeued active project sessions without turns after agent_turn completion`
    - no longer appears in the fresh post-redeploy tail during subsequent rate-limited completions
  - that means the hot project-session self-requeue loop is at least partially suppressed in the new binary
- rerun-30 status is unchanged at the product level:
  - org session `672477c4-4431-4969-bf7b-923253907972`
  - turn `69` / `5099af79-fc13-46e0-b3e3-4204c279da87` still `in_progress`
  - latest invocation on that turn still shows `2015dfdd-05db-4b77-9417-20f5e5fe2e9b` `in_flight`
  - no `speaker-pipeline-ops-validation-fresh-20260324-rerun-30*` project row exists yet
- remaining critical live gap:
  - the org synthetic-summary fix is still not proven live because turn `69` predates that last engine patch and remains active on the old continuation root
  - next validation target is still the first fresh rerun-30 retry after turn `69` exits

## 2026-03-25 04:00 MDT

- tmux session remains `codex-e2e-20260324`
  - pane `0` serve PID `71000`
  - pane `1` worker PID `71001`
  - pane `2` shell PID `19786`
  - pane `3` shell PID `18643`
- stack is healthy after the latest restart:
  - `./bin/ottercamp health` passes
  - current wall clock for this checkpoint: `2026-03-25 04:00 MDT`
- latest local worker hardening:
  - [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
    - `RecoverClaimedAgentTurnsWithoutLiveOwnership(...)` now honors a post-model grace window and will not immediately recover a claimed async turn when its current `in_progress` turn has a recent completed model invocation
    - added `shouldRequeueRecoveredProjectTrigger(...)` and used it from `RecoverStaleInProgressTriggeredTurns(...)`
    - stale recovered `project_bootstrap` turns are now suppressed unless `project_bootstrap.status = active`
    - stale recovered `project_execution_continuation` turns are now suppressed unless the project still has open non-terminal tasks
    - worker now logs `job queue: suppressed stale project trigger requeue` for these dropped historical project lanes
  - [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
    - added `TestJobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnershipKeepsCurrentInProgressAttemptWithRecentCompletedInvocation`
    - added `TestJobWorkerRecoverStaleInProgressTriggeredTurnsSuppressesCompletedProjectBootstrapRequeue`
    - added `TestJobWorkerRecoverStaleInProgressTriggeredTurnsSuppressesProjectContinuationWithoutOpenTasks`
- verification completed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(RecoverClaimedAgentTurnsWithoutLiveOwnership(KeepsCurrentInProgressAttemptWithRecentCompletedInvocation|RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentPendingAttempt)?|RecoverStaleInProgressTriggeredTurns(SuppressesCompletedProjectBootstrapRequeue|SuppressesProjectContinuationWithoutOpenTasks|KeepsExistingPendingRetryJobForProjectSession|RequeuesOrganizationContinuationUsingPendingSyntheticUserMessage|FailsNonHeartbeatingClaimedAttemptWithoutRun)|ClaimPendingAgentTurnsPrioritizesFreshOrgWorkOverProjectContinuation)' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
- live proof for the post-model grace fix:
  - rerun-33 org lane successfully created project `68ff263f-d1db-4e95-81a4-a3cfe5ea47d2`
  - slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-33`
  - canonical bootstrap tasks `1-8` are seeded on that project
  - the original org turn later failed on restart cleanup before `session.create` handoff finished:
    - turn `00390afc-afba-407b-9959-48f97c1fb708`
    - failure `worker cleanup failed stale in_flight model invocation without live in-progress turn`
  - the org session was left clean rather than wedged:
    - session `672477c4-4431-4969-bf7b-923253907972`
    - `current_turn_id = NULL`
- live proof for stale project-trigger suppression:
  - after the latest restart, worker startup emitted many `suppressed stale project trigger requeue` lines instead of reactivating every historical project lane
  - examples included sessions:
    - `54e75f15-...`
    - `e4390faa-...`
    - `b63a3a1d-...`
    - `fddd9a30-...`
  - this materially reduced the restart flood, but did not eliminate it because some historical project sessions still matched `active bootstrap` or `open tasks > 0`
- rerun-34 current trace:
  - fresh org message `119b93da-832f-44b3-9034-858b6d38de20`
  - first dispatch job `ad7ace32-8f36-4353-adfe-2b995984f6c4` completed immediately with last error `purged stale terminal message-attempt dispatch during claim`
  - turn `96a8d7be-74c6-4571-a7b8-6e9df84829dd` completed without any model invocation rows
  - turn messages only contain:
    - system `[Prompt input exceeded 64000-token guardrail - continuing in a new turn.]`
    - system `[Context compressed - continuing in a new turn.]`
  - pending retry job now exists:
    - job `6ed976ee-c731-4961-85fd-da94cce09dda`
    - `retry_count = 1`
    - `run_after = 2026-03-25 03:59:59.357658-06`
    - payload still points at session `672477c4-4431-4969-bf7b-923253907972` and message `119b93da-832f-44b3-9034-858b6d38de20`
- current queue pressure snapshot:
  - `agent_turn claimed = 17`
  - `agent_turn pending = 50`
  - `chat_session_cleanup pending = 603`
  - active sessions:
    - `organization = 2`
    - `project = 45`
    - `project_task = 84`
- current critical path:
  - local Ollama routing is working and fresh org requests are no longer blocked on provider rate limiting
  - the remaining bottleneck is worker backlog and startup recovery pressure from old `project` / `project_task` lanes
  - next fix target is to further narrow startup recovery so fresh org/project work can claim slots before the historical recovered project backlog floods the queue again

## 2026-03-25 04:19 MDT

- tmux session remains `codex-e2e-20260324`
  - pane `0` serve PID `28987`
  - pane `1` worker PID `28990`
  - pane `2` shell PID `19786`
  - pane `3` shell PID `18643`
- stack is healthy after the latest restart:
  - `./bin/ottercamp health` passes
- new local worker hardening:
  - [`internal/jobqueue/worker.go`](../internal/jobqueue/worker.go)
    - stale model-invocation retries now respect the same `shouldRequeueRecoveredProjectTrigger(...)` eligibility gate as stale triggered turns
    - stale continuation-turn recovery now also respects that project eligibility gate instead of re-enqueuing completed bootstrap / no-open-task project sessions forever
    - `shouldRequeueRecoveredProjectTrigger(...)` now also treats `project_continuation_resume` as continuation-only and requires open non-terminal tasks
    - claim-time SQL now skips stale pending `project_bootstrap` jobs for sessions whose bootstrap status is no longer `active`
    - claim-time SQL now skips stale pending `project_execution_continuation` / `project_continuation_resume` jobs when the project has no open non-terminal tasks
  - [`internal/jobqueue/worker_integration_test.go`](../internal/jobqueue/worker_integration_test.go)
    - added `TestJobWorkerClaimPendingAgentTurnsSkipsStaleProjectBootstrapAndContinuationJobs`
    - added `TestJobWorkerRecoverStaleInProgressContinuationTurnsSuppressesCompletedProjectBootstrapRequeue`
    - added `TestJobWorkerRecoverStaleInProgressContinuationTurnsSuppressesProjectContinuationWithoutOpenTasks`
- verification completed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(ClaimPendingAgentTurns(SkipsStaleProjectBootstrapAndContinuationJobs|PrioritizesFreshOrgWorkOverProjectContinuation)|RecoverStaleInProgressContinuationTurns(SuppressesCompletedProjectBootstrapRequeue|SuppressesProjectContinuationWithoutOpenTasks|KeepsQueuedRetry|UsesExecutionMetadataLiveTurn)?)' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalUsesStaleThresholdWithoutAgentTurnClamp|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)' -count=1`
- live proof after restart:
  - startup no longer recreated the old `17` claimed stale project lanes
  - immediate queue snapshot after the first patched restart dropped to:
    - `agent_turn claimed = 0`
    - `agent_turn pending = 67`
  - after the clean respawn restart, live worker state settled at:
    - `agent_turn claimed = 3`
    - `agent_turn pending = 62`
    - `chat_session_cleanup pending = 603`
  - worker tail now reports `job queue: no pending jobs` between runnable claims instead of immediately reclaiming the old completed-bootstrap project sessions
- fresh org canary proof under the patched worker:
  - rerun-35 user message: `7c9107fd-9402-4a57-b65d-9bab015a30bc`
  - deterministic continuation summary was correct:
    - `[Continuation summary] Active organization request: Create a new project named Speaker Pipeline Ops Validation Fresh 20260324 Rerun 35 ...`
  - real project created successfully:
    - project id `72ae14ef-2e0a-4cbe-aba9-7ccce42f0e47`
    - slug `speaker-pipeline-ops-validation-fresh-20260324-rerun-35`
    - status `active`
  - project session was created and bootstrap started:
    - session `7b3f718e-f3bf-4afc-9924-a26f630f3b48`
    - current turn `d62186b5-dcdd-4f82-9c7c-3a6e4c8820fa`
    - bootstrap metadata now shows `status=active`, `current_phase=staffing_persisted`, `last_successful_checkpoint=project_created`
- next live defect exposed by rerun-35:
  - the new project session still ignored the seeded bootstrap-task guidance and immediately created parallel top-level tasks `9-13` during bootstrap instead of reusing the canonical seeded `1-8`
  - live evidence from project session `7b3f718e-f3bf-4afc-9924-a26f630f3b48` shows repeated top-level `task.create` calls for:
    - `Happy Path Testing`
    - `Edge Case & Error Condition Testing`
    - `Load Scenario Testing`
    - `SLA Definition & Integration Readiness`
  - the resulting project task tree on rerun-35 now includes draft tasks `9-13` in addition to the seeded canonical scaffold
- new engine hardening for that bootstrap mutation seam:
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
    - active project bootstrap sessions now block root-level `task.create` tool calls with no `parent_task_id`
    - blocked tool result instructs Frank to reuse the seeded canonical bootstrap tree, create bounded child tasks only under `parent_task_id`, and persist setup with `bootstrap.setup.persist`
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
    - added `TestShouldBlockProjectBootstrapTopLevelTaskCreate`
- verification completed:
  - `go test ./internal/turn -run 'Test(ShouldBlockProjectBootstrapTopLevelTaskCreate|ShouldStopAfterBlockedProjectKickoffSessionCreate)' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
- current fresh canary state:
  - rerun-36 user message: `12b3dac8-dec5-45b3-b097-718182cf6ed4`
  - its dispatch job currently exists as pending:
    - `d11f62ce-b53d-4e85-97a5-917e02085510`
  - the org session still has an older active continuation turn in front of it:
    - turn `f488cdf6-4c6c-4fdd-b31d-142b352211c8`
    - trigger message `a52aaf29-cd2a-4413-ab74-74bf0a65d6db`
  - no rerun-36 project row exists yet
- current critical path:
  - worker startup/claim pressure is materially improved and no longer starves fresh org work with automatically reclaimed completed-bootstrap project sessions
  - the next product seam is bootstrap behavior on the fresh project session itself: rerun-35 showed that prompt-only guidance was insufficient, so the new engine guard must now be proven live on the next fresh project canary once rerun-36 clears the current org-session turn

## 2026-03-25 04:33 MDT

- tmux session remains `codex-e2e-20260324`
  - pane `0` serve PID `65690`
  - pane `1` worker PID `65691`
  - pane `2` shell PID `19786`
  - pane `3` shell PID `18643`
- stack is healthy after the latest rebuild/restart:
  - `go test ./internal/turn -run 'Test(ShouldBlockProjectBootstrapSetupTaskChildCreate|ShouldStopAfterBlockedProjectKickoffSessionCreate)' -count=1`
  - `go build -o ./bin/ottercamp ./cmd/ottercamp`
  - `./bin/ottercamp health` passes
- correction to the previous bootstrap guard:
  - the earlier root-level `task.create` guard in [`internal/turn/engine.go`](../internal/turn/engine.go) was too broad
  - rerun-36 proved that normal top-level non-bootstrap project tasks must still be allowed during active bootstrap
  - the broad guard pushed Frank into a worse path: creating executable work as children beneath bootstrap setup task `4`
  - that broad guard has now been removed
- replacement engine hardening now live:
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
    - dispatch precheck now calls `shouldBlockProjectBootstrapSetupTaskChildCreate(...)`
    - it blocks only `task.create` calls whose `parent_task_id` resolves to a task with `metadata.bootstrap_setup_task=true`
    - top-level non-bootstrap `task.create` remains allowed during active bootstrap
    - the blocked tool error now explicitly says the setup task is orchestration-only and instructs the agent to create bounded non-bootstrap work as normal project tasks, then persist setup progress with `bootstrap.setup.persist`
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
    - removed the old `TestShouldBlockProjectBootstrapTopLevelTaskCreate`
    - added `TestShouldBlockProjectBootstrapSetupTaskChildCreate`
- rerun-36 live proof of the actual defect:
  - org turn `32fdbb60-5241-4d4f-a5aa-bf73171c04c5` eventually created project:
    - project id `9ed78f5a-681c-4b22-86d9-8604e094c73d`
    - slug `speaker-pipeline-ops-validation-rerun-36`
  - project session:
    - `19272477-cece-41f7-be68-6b5935f251ee`
  - failed bootstrap turn:
    - `c9c60f1e-8064-4054-a20b-d2cfbc2877a6`
  - failure class now confirmed:
    - `bootstrap_setup_task_hidden_children`
  - exact validation failure:
    - `kickoff validation failed: bootstrap setup task 4 (Decompose workstreams into bounded tasks) must stay orchestration-only, so executable task 13 (Review Test Results & Coverage) cannot be hidden beneath it; delegate deliverable work into normal project tasks instead`
  - DB inspection confirmed the bad nesting:
    - setup task `4` = `cfc5e20f-13d9-45b4-8e38-981a99c29ecf`
    - tasks `9-13` all carried `metadata.decomposition_parent_task_id = cfc5e20f-13d9-45b4-8e38-981a99c29ecf`
- current next live step:
  - prove on a fresh org canary that bootstrap no longer creates executable children beneath setup task `4`
  - a fresh rerun-37 create request was attempted immediately after restart but the API returned a temporary rate limit:
    - request id `01KMJ8SYQ52K2J44Z1Z42DHXJ8`
    - `retry_after = 60`

## 2026-03-25 04:37 MDT

- tmux session remains `codex-e2e-20260324`
  - pane `0` serve PID `79001`
  - pane `1` worker PID `79002`
  - pane `2` shell PID `19786`
  - pane `3` shell PID `18643`
- stack is healthy after the latest rebuild/restart:
  - `go test ./internal/tools/native -run 'TestProjectCreateNormalizes(ExecutionFirst|Validation|Agile)DeliveryMode' -count=1`
  - `go test -tags=integration ./internal/tools/native -run 'TestIntegrationProjectCreateNormalizes(ExecutionFirst|Validation|Agile)DeliveryMode' -count=1`
  - `go build -o ./bin/ottercamp ./cmd/ottercamp`
  - `./bin/ottercamp health` passes
- new native project-create hardening:
  - [`internal/tools/native/mutation_tools.go`](../internal/tools/native/mutation_tools.go)
    - `normalizeProjectDeliveryModeInput(...)` no longer only maps `execution_first` and `validation`
    - it now preserves only the real DB modes `gated`, `continuous`, and `scheduled`
    - model-emitted aliases such as `agile`, `execution`, `project`, `project_task`, `async`, `autonomous`, and `canary` now normalize to `gated`
    - unknown/unsupported delivery-mode values now also fall back to `gated` instead of bubbling into misleading downstream create failures
  - [`internal/tools/native/mutation_tools_test.go`](../internal/tools/native/mutation_tools_test.go)
    - added `TestProjectCreateNormalizesAgileDeliveryMode`
  - [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
    - added `TestIntegrationProjectCreateNormalizesAgileDeliveryMode`
- live proof for the delivery-mode fix:
  - rerun-37 never reached bootstrap because org turn `2a8a0d74-b901-4e04-8072-0b229d181703` kept calling `project.create` with invalid aliases
  - exact live input for tool execution `b04abd9f-2aef-41fe-b4b5-8c9830f409ed` included:
    - `delivery_mode = "agile"`
  - that path was incorrectly surfacing as `project slug is already taken`
  - after the normalization patch, fresh rerun-38 created successfully on the first real create path:
    - project id `fc4a025d-e7c9-4b88-9485-17d2b6328e52`
    - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-38`
    - org message `9164eddc-0c61-4f90-ab3b-ed1ed83b8d14`
    - project session `efd6b36b-7687-4b23-a635-9f00df19bbb5`
- live proof for the corrected bootstrap guard:
  - rerun-38 bootstrap initially tried the old raw setup mutation path and got the expected system-managed error:
    - `bootstrap setup checklist tasks are system-managed during bootstrap; use bootstrap.setup.persist instead of raw task.update mutations`
  - the project session then created bounded executable work as normal project tasks `9-24`
  - direct DB proof on rerun-38:
    - non-bootstrap tasks with `metadata.decomposition_parent_task_id = task 4 id` = `0`
    - only the seeded bootstrap setup tasks `2-8` still carry `bootstrap_setup_task=true`
    - new tasks `9-24` have normal top-level metadata instead of being hidden beneath setup task `4`
  - bootstrap metadata on rerun-38 is now healthy and progressing:
    - `status = active`
    - `current_phase = first_wave_selected`
    - `last_successful_checkpoint = flow_templates_persisted`
    - `assignment_count = 3`
    - `planned_task_count = 16`
    - `planned_flow_template_count = 1`
    - completed checkpoints:
      - `project_created`
      - `staffing_persisted`
      - `task_tree_persisted`
      - `flow_templates_persisted`
- current critical path:
  - the org `project.create` seam and the bootstrap setup-task child-hiding seam are both fixed in live traffic
  - rerun-38 is now the active fresh canary and has progressed into first-wave selection
  - next step is to keep driving rerun-38 through first-wave selection, execution, review, rejection/retry, and recovery to find the next runtime defect

## 2026-03-25 04:43 MDT

- tmux session remains `codex-e2e-20260324`
  - pane `0` serve PID `11267`
  - pane `1` worker PID `11266`
  - pane `2` shell PID `19786`
  - pane `3` shell PID `18643`
- additional worktree/runtime hardening landed after rerun-38 reached first-wave execution:
  - [`internal/tools/native/task_worktree.go`](../internal/tools/native/task_worktree.go)
    - `ensureTaskWorktree(...)` now runs `git worktree prune --expire now` before validating or reusing task worktrees
    - stale `git worktree remove --force ... is not a working tree` cases now fall back to removing the leftover filesystem directory instead of failing the task lane
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
    - mirrored the same prune-first and tolerant stale-remove behavior in `ensureTurnTaskWorktree(...)`
  - [`internal/tools/native/native_integration_test.go`](../internal/tools/native/native_integration_test.go)
    - added `TestEnsureTaskWorktreePrunesDuplicateAndStaleMetadata`
    - added `TestEnsureTaskWorktreeRepairsDanglingGitdirPointer`
  - [`internal/tools/native/git_tools.go`](../internal/tools/native/git_tools.go)
    - `git.log` now treats `your current branch ... does not have any commits yet` as an empty commit list instead of a fatal tool error
  - [`internal/tools/native/file_git_test.go`](../internal/tools/native/file_git_test.go)
    - added `TestGitLogReturnsEmptyOnUnbornBranch`
- verification completed:
  - `go test -tags=integration ./internal/tools/native -run 'Test(EnsureTaskWorktreePrunesDuplicateAndStaleMetadata|EnsureTaskWorktreeRepairsDanglingGitdirPointer|IntegrationProjectCreateNormalizesAgileDeliveryMode)' -count=1`
  - `go test ./internal/tools/native -run 'TestGitLog(LimitAtMostFive|ReturnsEmptyOnUnbornBranch)' -count=1`
  - `go test ./internal/turn -run 'TestShouldBlockProjectBootstrapSetupTaskChildCreate' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
- live proof from rerun-38:
  - before the patch, task lanes hit duplicate/prunable worktree metadata:
    - `git worktree prune -n -v` reported `Removing worktrees/task-161: gitdir file does not exist`
    - and `Removing worktrees/task-171: duplicate entry`
  - the task-17 lane exposed the exact bad state:
    - duplicate worktree metadata dirs `task-17` and `task-171` both pointed at the same `task-17/.git`
  - after the prune-first patch and restart:
    - `git worktree prune -n -v` on rerun-38 is clean
    - `git worktree list --porcelain` initially collapsed back to only the main worktree
    - fresh task worktrees were then recreated cleanly for active lanes, including task `10`, `12`, `17`, and `9`
  - the earlier `... validation failed, cannot remove working tree ...` seam is gone
  - the follow-on stale pointer failure was also narrowed and fixed:
    - exact live error was `git worktree remove --force ... is not a working tree`
    - after the tolerant-remove patch, rerun-38 rehydrated new active sessions instead of leaving those stale `.git`-only directories as blockers
- current live state on rerun-38 project `fc4a025d-e7c9-4b88-9485-17d2b6328e52`:
  - bootstrap is complete:
    - `status = completed`
    - `current_phase = first_wave_jobs_claimed`
    - `first_wave_task_count = 6`
    - `first_wave_execution_count = 6`
    - `first_wave_job_count = 6`
  - active/review task lanes after the latest restart:
    - task `9` in progress
    - task `10` in review
    - task `11` in progress
    - task `12` in progress
    - task `16` in progress
    - task `17` in progress
- next critical path:
  - task worktree registration/cleanup is materially healthier in live traffic now
  - the next issue to watch is the remaining task-lane behavior on unborn repos, especially review/execution prompts that still try odd `file.list` paths or browse history before writing the actual deliverable

## 2026-03-25 06:10 MDT

- tmux session remains `codex-e2e-20260324`
  - rebuilt `./bin/ottercamp` twice in this stretch
  - restarted tmux `serve` and `worker` twice
  - `./bin/ottercamp health` stayed green after each restart
- new review-lane fixes landed:
  - [`internal/turn/engine.go`](../internal/turn/engine.go)
    - `explicitReviewDecisionFromText(...)` now also infers `reject` from strong reviewer findings, not only explicit `DECISION: REJECT` strings
    - the new heuristic synthesizes `flow.review_decision(reject)` when a review reply already states failure evidence like placeholder deliverable content plus missing required artifacts
  - [`internal/turn/engine_test.go`](../internal/turn/engine_test.go)
    - added `TestMaybeSynthesizeTaskReviewDecisionToolCallsInfersRejectFromStrongFindings`
  - [`internal/tools/native/query_tools.go`](../internal/tools/native/query_tools.go)
    - `session.list` is now blocked in review task sessions with `review_action_required`, matching the earlier `cli.execute` review-lane guard
  - [`internal/tools/native/query_tools_test.go`](../internal/tools/native/query_tools_test.go)
    - added `TestSessionListBlockedInReviewTaskSession`
- verification passed:
  - `go test ./internal/turn -run 'TestMaybeSynthesizeTaskReviewDecisionToolCalls(FromExplicitRejectDecision|SkipsWhenDecisionToolAlreadyPresent|InfersRejectFromStrongFindings)' -count=1`
  - `go test ./internal/tools/native -run 'Test(SessionListBlockedInReviewTaskSession|CLIExecuteBlockedInReviewTaskSession)' -count=1`
- live proof on rerun-39 task 10 review lane:
  - before the patch, review session [`53a8cbc9-f98c-4859-8416-a266738363ba`](../internal/turn/engine.go) kept wasting retries on:
    - absolute-path `file_list`
    - `session.list`
    - repeated exploratory reads instead of calling `flow.review_decision`
  - the unstable review execution was:
    - execution `7b680904-9276-4114-baee-bd39bfa8e4ce`
    - review session `53a8cbc9-f98c-4859-8416-a266738363ba`
  - after the new engine/native patches and restart:
    - execution `7b680904-9276-4114-baee-bd39bfa8e4ce` is now `rejected`
    - review decision metadata records:
      - `decision = reject`
      - `decided_at = 2026-03-25T12:08:43.624969Z`
    - the review session closed cleanly at `2026-03-25 06:08:43 MDT`
    - the task moved back to a fresh work execution:
      - execution `8ba2cfd5-e669-44df-859d-6e5442dc8f5a`
      - live turn `7479163e-397e-4db3-93ed-3e74bd963619`
- current live rerun-38 / rerun-39-adjacent state:
  - task `10` is now back to `in_progress`
  - tasks `9`, `11`, `12`, `16`, `17` are `done`
  - tasks `13-15`, `18-24` remain `draft`
- current critical path:
  - the reviewer no longer needs an explicit `DECISION:` line to reject once it has already stated strong failure findings
  - `session.list` is no longer available from review task sessions
  - the next live seam is still later-wave release and remaining rejection/retry coverage on the rerun-38/rerun-39 validation project family

## 2026-03-25 06:21 MDT

- operational/runtime routing fix applied to keep the canary moving:
  - current `model_profile` rows for `haiku`, `standard`, and `high-capability` had drifted back to Anthropic `claude-haiku-4-5-20251001`
  - the healthy local path already existed in the DB:
    - provider connection `5cbe78b4-77ef-4984-9e55-ed76a577def3`
    - provider `76d72a73-da99-48ca-ad48-7f1c53342b2e`
    - display name `Ollama Local`
    - base URL override `http://localhost:11434/v1`
    - health `healthy`
  - the non-current local model profiles were already present at version `6` for:
    - `haiku -> qwen2.5:72b`
    - `standard -> qwen2.5:72b`
    - `high-capability -> qwen2.5:72b`
  - I switched those three logical profiles back to `is_current=true` and demoted the Anthropic version `7` rows back to non-current
- why this was necessary:
  - task-10 fresh work retry session `b4c0adf2-0c8f-4428-b05e-f62700b16470` was stuck on repeated Anthropic 429s
  - direct DB proof before the switch:
    - repeated failed invocations on provider `6e470533-e646-4cd9-a404-a9d0d5d98465`
    - model `claude-haiku-4-5-20251001`
    - retry hints as large as `38h46m38s`
  - the pending retry job also inherited the stale rate-limit backoff:
    - job `48b2c781-f76f-4de6-a001-5af51d562fb2`
    - run_after was in the future from the Anthropic 429 path
  - I manually pulled that queued retry forward after the profile switch so it would re-dispatch immediately under the healthy provider
- live proof after the switch:
  - session `b4c0adf2-0c8f-4428-b05e-f62700b16470` now has active turn `699693a4-fca2-49f6-933e-8c0bf21f6377`
  - the new active invocation is:
    - invocation `572450af-bcfe-425c-96f0-fc8690dab8f5`
    - provider `76d72a73-da99-48ca-ad48-7f1c53342b2e`
    - model `qwen2.5:72b`
    - status `in_flight`
  - task `10` on project `63a7d9fb-f7fc-43f8-840e-52a024287b2b` is back to `in_progress`
  - that proves the runtime is no longer pinned to Anthropic for this lane
- current critical path:
  - provider failover is operational again
  - the next thing to watch is whether task `10` completes cleanly under the local qwen path and then releases the later-wave validation tasks

## 2026-03-25 06:33 MDT

- tmux session remains `codex-e2e-20260324`
  - the earlier restart failure is fixed
  - `serve` and `worker` were both rebuilt and restarted successfully under the same tmux panes:
    - serve pane `codex-e2e-20260324:0.0`
    - worker pane `codex-e2e-20260324:0.1`
  - `./bin/ottercamp health` is green again after the restart
- new queue/runtime hardening landed:
  - [`internal/gateway/queue.go`](../internal/gateway/queue.go)
    - queued async dispatch now applies the selected provider connection's `max_concurrent` to the shared `ConcurrencyManager`
    - this closes the bug where queued async work ignored connection-specific limits and silently used the default per-connection cap instead
  - [`internal/gateway/queue_test.go`](../internal/gateway/queue_test.go)
    - added `TestPriorityQueueUsesConnectionMaxConcurrentFromRouter`
  - verification passed:
    - `go test ./internal/gateway -run 'TestPriorityQueue(OrdersSyncInteractiveBeforeAsync|UsesConnectionMaxConcurrentFromRouter|SoftPreemptionCancelsAsync|ReturnsErrQueueTimeout)' -count=1`
- new bootstrap fix landed:
  - [`internal/bootstrap/bootstrap.go`](../internal/bootstrap/bootstrap.go)
    - model-profile seeding is now idempotent when a matching desired org profile version already exists in history
    - bootstrap now re-activates that matching historical version instead of blindly calling `Deprecate(...)` and colliding with `model_profile_logical_org_version_uniq_idx`
    - added helper logic to:
      - locate an exact matching org profile version by logical profile + provider/model/capability fields
      - flip `is_current` back onto that historical version when it already represents the desired seed
  - [`internal/bootstrap/bootstrap_integration_test.go`](../internal/bootstrap/bootstrap_integration_test.go)
    - added `TestBootstrapRunReusesMatchingHistoricalModelProfileVersion`
    - the test reproduces the live crash shape:
      - create a newer matching seeded profile version
      - manually point `is_current` back to an older divergent version
      - rerun bootstrap and prove it reuses the existing matching history row instead of trying to recreate the same version
  - verification passed:
    - `go test ./internal/bootstrap -count=1`
    - `go test -tags=integration ./internal/bootstrap -run 'TestBootstrapRun(ReusesMatchingHistoricalModelProfileVersion|SeedsAndIsIdempotent)' -count=1`
- live proof of the bootstrap fix:
  - before the patch, both serve and worker died at startup on:
    - `bootstrap step 5 (seed-model-profiles): failed - repo: conflict: duplicate key value violates unique constraint "model_profile_logical_org_version_uniq_idx"`
  - after the patch and rebuild:
    - serve startup now logs `bootstrap step 5 (seed-model-profiles): done`
    - worker startup also clears step 5 and stays alive
    - serve reached `server started` on `:4110`
    - worker resumed claiming and completing `agent_turn` jobs instead of dying at bootstrap
- current live state after restart:
  - project `63a7d9fb-f7fc-43f8-840e-52a024287b2b` still has a single active flow execution:
    - execution `8ba2cfd5-e669-44df-859d-6e5442dc8f5a`
    - task `10`
    - title `WS-3: Analyze Results and Sign Off`
    - `work_status = in_progress`
  - task board on that project is now:
    - tasks `1-9` done
    - task `10` in progress
    - tasks `11-12` done
- current queue/provider observations:
  - the worker is alive, but backlog pressure is still high:
    - `job_queue` currently has `833` rows in `pending` or `claimed`
  - the local Ollama connection is still showing `4` `model_invocation` rows as `in_flight` even though `max_concurrent = 2`
  - those `in_flight` rows are older async `project` turns created before the restart, not fresh task-10 admissions
  - the worker log itself is currently idling with `no pending jobs` on each short poll after startup, so the remaining local over-cap discrepancy looks like stale invocation state / backlog interference, not the original queue-cap omission
- next critical path:
  - gateway queue cap enforcement is now in code and tested
  - bootstrap no longer crashes the worker on startup
  - the next seam is stale local `in_flight` invocation cleanup and backlog drain behavior while task `10` continues on the local qwen provider path

## 2026-03-25 06:37 MDT

- another bootstrap/runtime fix landed immediately after the 06:33 restart:
  - [`internal/bootstrap/bootstrap.go`](../internal/bootstrap/bootstrap.go)
    - startup model-profile seeding now preserves an org's existing current profile selection instead of rotating it back to the seeded default on every restart
    - step 5 still creates the initial seed rows when a logical profile is missing, but once a current org profile exists it is treated as authoritative
    - this is the fix for the live regression where the successful step-5 crash fix still reverted the org from local qwen version `6` back to Anthropic version `7`
  - [`internal/bootstrap/bootstrap_integration_test.go`](../internal/bootstrap/bootstrap_integration_test.go)
    - renamed and repointed the regression coverage to `TestBootstrapRunPreservesExistingCurrentModelProfileVersion`
    - it now proves bootstrap does not override an operator-selected current profile on rerun, while still avoiding the duplicate-version conflict
- verification passed:
  - `go test ./internal/bootstrap -count=1`
  - `go test -tags=integration ./internal/bootstrap -run 'TestBootstrapRun(PreservesExistingCurrentModelProfileVersion|SeedsAndIsIdempotent)' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and then `worker`
  - `./bin/ottercamp health` remained green
- live proof:
  - after the new patch, I restarted `serve` and confirmed step 5 still completes:
    - `bootstrap step 5 (seed-model-profiles): done`
  - I then restored the org's current profiles to the local qwen version `6` rows:
    - `haiku -> qwen2.5:72b`
    - `standard -> qwen2.5:72b`
    - `high-capability -> qwen2.5:72b`
  - I restarted the worker after that DB flip to prove bootstrap would preserve them
  - post-worker-start current rows are still:
    - `haiku version 6 current true`
    - `standard version 6 current true`
    - `high-capability version 6 current true`
  - that confirms the restart no longer forces the org back onto Anthropic defaults
- task-10 live recovery under the preserved qwen path:
  - active work execution remains:
    - execution `8ba2cfd5-e669-44df-859d-6e5442dc8f5a`
    - task `10`
    - title `WS-3: Analyze Results and Sign Off`
  - the pending rate-limited retry job was:
    - `dcdc8d72-243d-4fde-ad88-21dbc39f7490`
    - initially scheduled for `06:59:48 MDT`
  - I pulled that retry forward manually after restoring qwen
  - live result:
    - the job moved to `claimed`
    - session `b4c0adf2-0c8f-4428-b05e-f62700b16470` now has current turn `ba8d4faa-75af-4612-a376-5919440ab915`
    - worker log shows:
      - `agent_turn dispatch: start inbound turn`
      - `turn_status=in_progress`
      - `should_run=true`
    - a fresh invocation now exists on the local provider:
      - invocation `3b6cfd59-a0c0-4e96-bddf-ce611745883c`
      - status `in_flight`
      - provider connection `5cbe78b4-77ef-4984-9e55-ed76a577def3`
      - model `qwen2.5:72b`
- current critical path:
  - the main bootstrap regressions are fixed:
    - step 5 no longer crashes on duplicate historical versions
    - step 5 no longer rewrites current org model choices on restart
  - task `10` is actively running again on the local provider
  - the next thing to watch is whether turn `ba8d4faa-75af-4612-a376-5919440ab915` completes cleanly or exposes a new live execution/recovery seam

## 2026-03-25 06:50 MDT

- live task-10 progress after the qwen recovery:
  - work session `b4c0adf2-0c8f-4428-b05e-f62700b16470`
  - qwen work retry turn `ba8d4faa-75af-4612-a376-5919440ab915`
  - qwen invocation `3b6cfd59-a0c0-4e96-bddf-ce611745883c`
  - that invocation completed successfully at `2026-03-25 06:40:33 MDT`
  - the follow-up work turn `0be55280-d536-4be0-8093-e895f8ef3201` also completed successfully at `2026-03-25 06:45:37 MDT`
  - work execution `8ba2cfd5-e669-44df-859d-6e5442dc8f5a` is now `completed`
  - task `10` advanced from `in_progress` to `review`
- fresh review lane for task `10`:
  - new review execution `751de148-e135-4de7-a8ff-c4b86588a132`
  - review session `3b4c1769-afc6-45ef-9375-9828b7a85221`
  - the first queued review dispatch `c2c0ee1a-7d2d-4b7d-b457-d45b92c18cfa` was superseded/dead-lettered as the session moved forward
  - current review turn is now `a7f71eb5-3298-4c9b-8edb-291723551cff`
  - current review invocation is:
    - `1d5e54d1-2aea-4eb5-ae84-444cb67a176b`
    - provider connection `5cbe78b4-77ef-4984-9e55-ed76a577def3`
    - model `qwen2.5:72b`
    - status still `in_flight`
- new bottleneck discovered while pushing the review lane:
  - worker slot pressure after restart is no longer primarily the old in-memory leak
  - the deeper problem is recovery/startup fan-out onto the local provider
  - after the controlled worker restart, the worker reactivated a batch of stale async sessions and qwen `in_flight` count jumped sharply:
    - first to `14`
    - later trimmed by cleanup to `12`
  - current composition of those qwen `in_flight` rows is:
    - `3` `project` async sessions
    - `11` `project_task` async sessions at peak
  - many of those were created together around `06:49:08 MDT`, immediately after worker restart/recovery
- worker/runtime observations:
  - even when DB `claimed` jobs dropped well below the worker’s execution capacity, the worker kept reporting:
    - `job queue: no execution slots available inflight=10 capacity=10`
  - a controlled worker restart cleared the old local slot drift, but restart recovery then fanned many async sessions back out immediately
  - cleanup is trimming them, but only gradually:
    - `job queue: purged stale agent_turn jobs` count `4`
    - `job queue: failed stale model invocations` count `1`
  - one concrete recovered qwen invocation failure observed:
    - invocation `b1e79cb7-27b8-4324-ab9b-0cdbf3d5b2b4`
    - `error_code = stale_turn_recovered`
    - `error_message = recovered stale in-progress message turn without live job or execution; scheduling a fresh retry`
- code/behavior conclusion from the live run:
  - bootstrap profile preservation is working
  - task-10 work execution is working end-to-end on qwen
  - the remaining live problem is worker recovery fan-out / local-provider saturation:
    - restart or cleanup-driven session recovery is admitting too many async qwen turns at once
    - task-10 review is now in that backlog instead of failing on Anthropic rate limits
- next critical path:
  - keep task-10 review invocation `1d5e54d1-2aea-4eb5-ae84-444cb67a176b` under observation
  - if it does not drain, patch the worker recovery/requeue path so it does not stampede stale async sessions back onto the local provider immediately after restart

## 2026-03-25 07:08 MDT

- new worker claim fix landed in [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go):
  - per-session `agent_turn` claim ranking now prefers the newest pending logical message inside a single async session
  - cross-session/global ordering is unchanged
  - this is specifically to stop old pending task review retries from beating a newer supervisor recovery or fresh rewritten review prompt in the same `project_task` session
- focused regression added in [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go):
  - `TestJobWorkerClaimPendingAgentTurnsPrefersNewestTaskSessionMessage`
  - verified together with:
    - `TestJobWorkerClaimPendingAgentTurnsPrioritizesFreshOrgWorkOverProjectContinuation`
    - `TestJobWorkerClaimPendingAgentTurnsSkipsStaleProjectBootstrapAndContinuationJobs`
    - `TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsCapsRecoveryBatch`
- verification command:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(ClaimPendingAgentTurns(PrefersNewestTaskSessionMessage|PrioritizesFreshOrgWorkOverProjectContinuation|SkipsStaleProjectBootstrapAndContinuationJobs)|RequeueActiveExecutionSessionsWithoutTurnsCapsRecoveryBatch)$' -count=1`
- rebuilt `./bin/ottercamp`
- restarted only the tmux worker pane with the new binary:
  - pane `codex-e2e-20260324:0.1`
  - serve stayed up and `./bin/ottercamp health` remained green

- live proof on task-10 review session `3b4c1769-afc6-45ef-9375-9828b7a85221`:
  - before the patch, the same session had:
    - older pending review retry job `c27b44c3-7bd7-426a-b7ff-3cb66a10927b`
    - newer supervisor recovery job `ff6e118e-c624-42d7-af18-fa9222b56a58`
  - after the worker restart on the patched binary:
    - the worker claimed `ff6e118e-c624-42d7-af18-fa9222b56a58`
    - it did **not** claim `c27b44c3-7bd7-426a-b7ff-3cb66a10927b`
    - worker log showed:
      - `session_id=3b4c1769-afc6-45ef-9375-9828b7a85221`
      - `message_id=d76743f0-2031-47a2-b7a3-140ac7f07868`
      - `turn_id=44ec3949-78a1-40d5-b97b-751724d83ba1`
      - `should_run=true`
- task-10 session/message state after that live restart:
  - old pending retry still pending:
    - job `c27b44c3-7bd7-426a-b7ff-3cb66a10927b`
    - message `c0f88c89-295d-4ca0-b965-60fa9aff58dd`
    - retry_count `2`
  - newer supervisor recovery is the live owner:
    - job `ff6e118e-c624-42d7-af18-fa9222b56a58`
    - status `claimed`
    - message `d76743f0-2031-47a2-b7a3-140ac7f07868`
  - that recovery path rewrote into a fresh review-action prompt:
    - new user message `e631d30d-48b4-40fd-b88d-70273a7e32fd`
    - source `task_review_action`
  - current live review turn:
    - turn `44ec3949-78a1-40d5-b97b-751724d83ba1`
    - status `in_progress`
    - trigger message `e631d30d-48b4-40fd-b88d-70273a7e32fd`
  - current live invocation:
    - invocation `efd5583d-ddb8-48b2-9fba-361f18012b3b`
    - status `in_flight`
    - turn_id `44ec3949-78a1-40d5-b97b-751724d83ba1`

- remaining seam after this fix:
  - the message-selection problem is improved/live-proven
  - the next ownership detail to watch is the supervisor run row:
    - run `ebf6ca2b-2aef-4642-b1c9-d8d30e6a457a` is still `in_progress`
    - it still has `turn_id = NULL`
    - meanwhile the actual review turn and model invocation are live
  - so the next bug, if this turn stalls again, is no longer “wrong pending job claimed first”
  - it will be run/turn ownership reconciliation for the supervisor recovery path

## 2026-03-25 07:16 MDT

- separate worker CLI bug fixed in [`cmd/ottercamp/main.go`](/Users/sam/dev/otter-camp/cmd/ottercamp/main.go):
  - `ottercamp worker --concurrency N` previously ignored its CLI args entirely
  - the worker path only read `OTTERCAMP_WORKER_CONCURRENCY`, so the tmux command `./bin/ottercamp worker --concurrency 24` was silently running at the default queue `BatchSize=10`
  - patched `runWorker(args []string)` to parse `--concurrency` and set `OTTERCAMP_WORKER_CONCURRENCY` before config/worker startup
- verification:
  - `go test ./cmd/ottercamp -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux worker pane `codex-e2e-20260324:0.1` with the same command:
    - `./bin/ottercamp worker --concurrency 24`
- live proof the CLI fix is real:
  - before the patch, worker logs repeatedly showed:
    - `job queue: no execution slots available inflight=10 capacity=10`
  - after the patched restart, the same worker immediately showed higher capacity:
    - `job queue: claiming pending jobs slots=5 inflight=19`
    - later `slots=13 inflight=11`
  - that confirms the live queue cap is no longer stuck at `10`

- task-10 review lane after the higher-capacity restart:
  - the prior supervisor handoff turn was eventually failed as stale:
    - turn `44ec3949-78a1-40d5-b97b-751724d83ba1`
    - failed at `2026-03-25 07:06:24 MDT`
    - error `recovered stale project_task inbound turn without active run ownership; scheduling a fresh retry`
  - the supervisor recovery dispatch was then dead-lettered:
    - job `ff6e118e-c624-42d7-af18-fa9222b56a58`
    - message `d76743f0-2031-47a2-b7a3-140ac7f07868`
  - the live session moved back onto the original review-action family at retry `3`:
    - claimed job `efd7f616-034b-4b0a-9ad4-321457100638`
    - message `c0f88c89-295d-4ca0-b965-60fa9aff58dd`
    - current turn `59610fac-4a8d-41b8-a5a7-95ccb35c3963`
    - turn number `4`
    - retry_count `3`
    - status `in_progress`
    - trigger message `2ecb3a33-259e-4071-afc4-d0caec1bcf25`

- updated conclusion:
  - the worker concurrency flag bug is fixed and live
  - the earlier message-selection bug is also fixed and did influence the first post-patch claim
  - but task-10 still falls back into `recovered stale project_task inbound turn without active run ownership`
  - so the remaining critical seam is still the review-lane stale inbound-turn recovery path itself, not worker capacity anymore

## 2026-03-25 07:15 MDT

- new review/task-turn stale-recovery guard landed in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go):
  - `recoverProjectTaskStaleInboundTurnWithoutRun(...)` now returns early when the supposedly stale `project_task` turn already has an `in_flight` `model_invocation`
  - this avoids failing a genuinely running review/task turn just because no active `run` row is bound yet
- focused unit coverage added in [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go):
  - `TestRecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation`
- verification:
  - `go test ./internal/turn -run 'TestRecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation$' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux worker pane `codex-e2e-20260324:0.1` again on the new binary
  - health stayed green

- live proof on task-10 review session `3b4c1769-afc6-45ef-9375-9828b7a85221` after the patch:
  - current turn is still:
    - `3953ea04-e65b-4d71-9cce-37f22a18f087`
    - turn number `13`
    - retry_count `12`
    - status `in_progress`
  - current invocation is still live:
    - `9cbd5638-a731-46f6-9546-0fad24bd9baa`
    - status `in_flight`
    - turn_id `3953ea04-e65b-4d71-9cce-37f22a18f087`
  - two later `agent_turn` jobs for the same session were processed on the patched worker while that live turn/invocation remained active:
    - job `b3cf88f1-2738-4b3c-9fbd-326e184af885`
      - message `c0f88c89-295d-4ca0-b965-60fa9aff58dd`
      - retry_count `12`
      - worker log:
        - `turn_id=3953ea04-e65b-4d71-9cce-37f22a18f087`
        - `turn_status=in_progress`
        - `should_run=false`
        - `agent_turn job: completed`
    - job `24ed9154-353f-4c99-b5db-4d4ed320af48`
      - message `8077c960-9e70-4181-8789-66d398852fe8`
      - retry_count `0`
      - same result:
        - `turn_status=in_progress`
        - `should_run=false`
        - `agent_turn job: completed`
- most important behavioral change:
  - before this patch, these follow-on jobs would fail the live turn into yet another `recovered stale project_task inbound turn without active run ownership`
  - after this patch, they no-op cleanly while the live invocation remains active

- current state:
  - the capacity fix is still live (`slots=24` after restart)
  - task-10 is not finished yet
  - but the specific stale-retry loop caused by duplicate follow-on jobs hitting a live in-progress turn appears to be broken in production behavior

## 2026-03-25 07:24 MDT

- new review empty-output retry guard landed in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go):
  - `handleCompletedReviewTurnWithoutDecision(...)` now treats blank assistant output as transient runtime/no-output instead of consuming the terminal “completed without flow.review_decision” blocker path
  - on blank output it appends:
    - `[Review turn returned empty assistant output. Retrying with a fresh review-decision prompt.]`
  - then it re-enqueues a fresh synthetic `task_review_action` prompt instead of blocking the task
- focused regression added in [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go):
  - `TestHandleTurnCompletedEventRetriesEmptyReviewTurnWithoutDecisionAtRetryLimit`
- focused verification passed:
  - `go test ./internal/turn -run 'Test(HandleTurnCompletedEvent(RetriesEmptyReviewTurnWithoutDecisionAtRetryLimit|BlocksRepeatedReviewTurnWithoutDecision|RetriesReviewTurnWithoutDecision)|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation)$' -count=1`

- deploy notes:
  - rebuilt `./bin/ottercamp`
  - restarted tmux panes `codex-e2e-20260324:0.0` and `codex-e2e-20260324:0.1`
  - first respawn attempt dropped the exported Ottercamp env and failed with:
    - `config error: OTTERCAMP_MODE is required`
  - both panes were then respawned with the full env inline
  - health is green again:
    - `./bin/ottercamp health`
    - `db=true migrations=true pgvector=true storage=true`

- live state after deploy:
  - task 10 is still blocked:
    - task `ae7c187c-6b05-4519-95ec-43a7b3d4054d`
    - `WS-3: Analyze Results and Sign Off`
    - `work_status=blocked`
  - the review execution that exposed the bug is unchanged historical state, not a fresh post-patch run:
    - flow node execution `751de148-e135-4de7-a8ff-c4b86588a132`
    - `status=abandoned`
    - session `3b4c1769-afc6-45ef-9375-9828b7a85221`
    - session status is now:
      - `closed`
      - `closed_at=2026-03-25 07:17:42.064188-06`
  - latest turns in that closed review session remain:
    - `3953ea04-e65b-4d71-9cce-37f22a18f087`
      - `failed`
      - `retry_count=12`
      - `error=worker cleanup failed stale in_flight model invocation without live in-progress turn`
    - `cd2fe20d-1d7d-460d-8b1d-44b3387b584f`
      - `completed`
      - `retry_count=13`
      - empty assistant output
  - the new blank-output retry logic is therefore deployed but not yet proven live on a fresh review turn

- worker / tmux status after the successful restart:
  - service pane `codex-e2e-20260324:0.0` rebounded and logged:
    - `server started addr=[::]:4110`
  - worker pane `codex-e2e-20260324:0.1` is healthy and active again
  - worker tail immediately resumed claiming live jobs at the configured higher capacity

- narrowed next seam:
  - the bug fix is now in the live binary
  - but task-10 stayed blocked because the old review session was already closed before the patch was deployed
  - next step is to trace what path should reopen or retry a blocked review lane after `handleCompletedReviewTurnWithoutDecision(...)` has already halted and closed the session, then force a fresh post-patch review execution to prove the new behavior in production

## 2026-03-25 07:35 MDT

- found and fixed the real resumability gap for blocked review lanes:
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - when `handleCompletedReviewTurnWithoutDecision(...)` halts a review after repeated retries, it now persists a blocked validation-guard record before calling `MarkBlocked(...)`
    - the persisted guard is:
      - `tool_name = flow.review_decision`
      - `failure_code = review_decision_required`
      - `failure_reason = review turn completed without calling flow.review_decision`
  - [`internal/task/recovery_resume.go`](/Users/sam/dev/otter-camp/internal/task/recovery_resume.go)
    - added backward-compatible resume classification for older blocked review tasks that only have blocker reason `review turn completed without calling flow.review_decision` and no persisted validation guard
    - that makes historical rows like task 10 resumable via `ResumeValidationBlockedTask(...)` and supervisor auto-resume without manual DB surgery
- related focused coverage:
  - [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go)
    - `TestHandleTurnCompletedEventBlocksRepeatedReviewTurnWithoutDecision` now proves the blocked validation guard is written into task metadata
  - [`internal/task/service_test.go`](/Users/sam/dev/otter-camp/internal/task/service_test.go)
    - `TestClassifyTaskResumeDecisionAllowsHistoricalReviewDecisionBlockerWithoutGuard`
- verification passed:
  - `go test ./internal/turn -run 'Test(HandleTurnCompletedEvent(BlocksRepeatedReviewTurnWithoutDecision|RetriesEmptyReviewTurnWithoutDecisionAtRetryLimit)|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation)$' -count=1`
  - `go test ./internal/task -run 'Test(ClassifyTaskResumeDecisionAllowsHistoricalReviewDecisionBlockerWithoutGuard|ResumeValidationBlockedTaskRequiresResumableBlockedState)$' -count=1`
  - `go test ./internal/worker -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker` with the full env inline
  - health is green

- live proof on task 10 (`ae7c187c-6b05-4519-95ec-43a7b3d4054d`):
  - after the latest restart and supervisor poll, task 10 auto-resumed from historical blocked state:
    - `work_status=review`
    - `updated_at=2026-03-25 07:34:43.579819-06`
  - supervisor/task-service created a fresh active review execution:
    - flow node execution `a74806e9-bd0a-4d56-9cff-8465da982679`
    - `status=active`
  - a fresh active review session was created:
    - session `b79fa514-2fdb-4214-8868-4c02f254b35c`
    - `status=active`
    - `current_turn_id=707c22ef-9724-4a40-a6cf-33529b6f2499`
  - fresh live turn:
    - turn `707c22ef-9724-4a40-a6cf-33529b6f2499`
    - `status=in_progress`
    - `retry_count=0`
  - review kickoff messages in that new session now show the correct resumed state:
    - `Start review on task: WS-3: Analyze Results and Sign Off`
    - metadata includes:
      - `recovery_action = resume_validation_blocked_task`
      - `recovery_blocker_class = deterministic_validation_loop`
      - `validation_tool_name = flow.review_decision`
      - `validation_failure_code = review_decision_required`
      - `flow_node_execution_id = a74806e9-bd0a-4d56-9cff-8465da982679`
    - follow-on synthetic review prompt is present:
      - `Review only. Inspect the current deliverables and use flow.review_decision to approve or reject this review step.`

- current critical path:
  - the supervisor resume path is now proven live again
  - the remaining question is whether this fresh task-10 review turn actually resolves through `flow.review_decision` under the new prompts and empty-output guard, or whether the review lane still exposes another runtime seam once the model replies

## 2026-03-25 07:53 MDT

- found and fixed the missing retry-attribution branch for review startup prompts:
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - `appendReviewActionState(...)` no longer uses bare `syntheticContinuationActionMessageMetadata(...)`
    - it now carries run attribution forward from the initiating message/session history via `syntheticContinuationActionMessageMetadataWithCarryForward(...)`
    - added helper:
      - `reviewActionMetadataSource(...)`
    - this was the exact live gap creating fresh synthetic `task_review_action` messages with no `run_id`, even after the earlier retry-path carry-forward patch
- focused regression tightened in [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go):
  - `TestAppendReviewActionStateRootsHistoryForReviewTask`
    - now seeds an initial kickoff message with `run_id`
    - asserts the synthetic review-action prompt created by `appendReviewActionState(...)` preserves that `run_id`
- focused verification passed:
  - `go test ./internal/turn -run 'Test(AppendReviewActionStateRootsHistoryForReviewTask|HandleTurnCompletedEventRetriesReviewTurnWithoutDecision|HandleTurnCompletedEventRetriesReviewTurnWithoutDecisionRestoresRunIDFromSessionHistory|HandleTurnCompletedEventRetriesEmptyReviewTurnWithoutDecisionAtRetryLimit|HandleTurnCompletedEventBlocksRepeatedReviewTurnWithoutDecision)$' -count=1`
- deployed:
  - rebuilt `./bin/ottercamp`
  - restarted tmux panes:
    - `codex-e2e-20260324:0.0`
    - `codex-e2e-20260324:0.1`
  - health is green:
    - `./bin/ottercamp health`

- live proof on task-10 review session `b79fa514-2fdb-4214-8868-4c02f254b35c`:
  - the first fresh post-patch review attempt started at `2026-03-25 07:53:28 MDT`
  - worker log showed the session was dispatched again from the latest synthetic review prompt:
    - job `a0ec3d40-1ae2-4855-b839-83d9fae3797b`
    - message `baf594c2-bcc3-4971-92b3-6fb0ffea5023`
    - then a fresh turn `68c96ecc-2476-4fd8-8968-1590ae9afeb5`
  - most important live result:
    - a new synthetic review-action message was appended after the patch:
      - message `d1b9bdbc-1178-414d-b229-debe99c95894`
      - created at `2026-03-25 07:53:28.816126-06`
    - its metadata now includes:
      - `run_id = cabb485b-5383-40a2-8973-76b924883f50`
      - `task_id = ae7c187c-6b05-4519-95ec-43a7b3d4054d`
      - `source = task_review_action`
      - `flow_node_execution_id = a74806e9-bd0a-4d56-9cff-8465da982679`
    - this is the first live proof that fresh review-action prompts created by `appendReviewActionState(...)` now preserve run attribution in production behavior

- current live state after the patch:
  - task `10` remains `review`
  - current review turn:
    - `68c96ecc-2476-4fd8-8968-1590ae9afeb5`
    - `status=in_progress`
    - `retry_count=7`
  - current model invocation:
    - `e6817eb1-75e3-4b8c-a93f-d2957989876e`
    - `status=in_flight`
    - `turn_id=68c96ecc-2476-4fd8-8968-1590ae9afeb5`

- narrowed next seam:
  - missing `run_id` carry-forward is no longer the blocker
  - the remaining issue is that task-10 review invocations are still eventually being cleaned up as stale `in_flight` model invocations after a few minutes
  - next step is to trace why these review invocations lose live ownership / in-progress visibility despite correct prompt attribution, rather than continuing to chase message metadata

## 2026-03-25 07:56 MDT

- found and fixed the worker heartbeat timing bug behind the remaining task-10 review churn:
  - [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go)
    - `claimHeartbeatInterval(...)` now clamps `agent_turn` heartbeat scheduling to `claimedAgentTurnHeartbeatGrace`
    - before this patch:
      - non-heartbeating `agent_turn` recovery started after `30s`
      - but normal `agent_turn` claim heartbeats were still using `staleClaimThreshold/3`
      - with the live worker config that meant the first heartbeat would not arrive until roughly `100s`
      - long-running review turns were therefore being “recovered” before their first heartbeat
    - after this patch:
      - `agent_turn` heartbeats fire on the tighter `claimedAgentTurnHeartbeatGrace/3` cadence
      - live turns can keep their claimed dispatch alive long enough to finish normally
- focused test update in [`internal/jobqueue/worker_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_test.go):
  - renamed and tightened:
    - `TestClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace`
- focused verification passed:
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace|AgentTurnRateLimitDelayCapsProviderHintAtBackoffCap|AgentTurnRateLimitDelayUsesProviderHintWhenBelowBackoffCap|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)$' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'Test(JobWorkerHeartbeatPreventsStaleClaimRecoveryForRunningJob|JobWorkerRecoverClaimedAgentTurnsWithoutLiveOwnership(KeepsCurrentPendingAttempt|RecoversCurrentInProgressAttemptWithoutModelOrRun|KeepsCurrentInProgressAttemptWithRecentCompletedInvocation)?)$' -count=1`
- deployed:
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
  - health is green

- live proof on task-10 review lane:
  - before the patch, the worker repeatedly logged:
    - `job queue: recovered non-heartbeating claimed agent_turn jobs before claim`
    - then reclaimed the same task-10 review dispatch while the live turn was still in progress
  - after the patch and restart:
    - task-10 review turn `68c96ecc-2476-4fd8-8968-1590ae9afeb5` stayed alive through the full recovery window
    - the corresponding model invocation `e6817eb1-75e3-4b8c-a93f-d2957989876e` reached:
      - `status=completed`
      - `completed_at=2026-03-25 07:55:03.171718-06`
    - there were no fresh task-10 log lines showing:
      - `recovered non-heartbeating claimed agent_turn`
      - or a fresh `stale model invocation` cleanup against that turn
  - the session then closed normally:
    - review session `b79fa514-2fdb-4214-8868-4c02f254b35c`
    - `closed_at=2026-03-25 07:55:03.520739-06`
  - the assistant output for that turn recorded a real review decision tool call:
    - message `7abf85c9-e100-46a9-8f8d-de2f062ff245`
    - metadata includes:
      - `tool_calls[0].name = flow_review_decision`
      - `decision = approve`
      - `flow_node_execution_id = a74806e9-bd0a-4d56-9cff-8465da982679`

- live task outcome:
  - task `ae7c187c-6b05-4519-95ec-43a7b3d4054d`
    - `WS-3: Analyze Results and Sign Off`
    - is now `done`
    - `updated_at=2026-03-25 07:55:03.526518-06`
  - review execution `a74806e9-bd0a-4d56-9cff-8465da982679` is now `completed`
    - metadata includes:
      - `review_decision.decision = approve`
      - `review_decision.decided_at = 2026-03-25T13:55:03.210845Z`
      - `live_run_id = cabb485b-5383-40a2-8973-76b924883f50`
  - terminal follow-on flow execution:
    - `da02bbc3-a6fd-426e-a65f-ac9e34990b02`
    - `status=completed`

- net effect:
  - the two review-lane bugs from this stretch are now both proven live:
    - synthetic review-action prompts preserve `run_id`
    - long-running review turns heartbeat before non-heartbeating recovery can reclaim them
  - task 10 no longer blocks rerun-38/39 progress

## 2026-03-25 08:00 MDT

- found one more backward-compatibility gap in blocked-task resume classification:
  - [`internal/task/recovery_resume.go`](/Users/sam/dev/otter-camp/internal/task/recovery_resume.go)
    - `classifyTaskResumeDecision(...)` previously only treated the historical review blocker as resumable on exact string equality:
      - `review turn completed without calling flow.review_decision`
    - older rows from the rerun-38 worktree failure stored:
      - `review turn completed without calling flow.review_decision: <extra runtime detail>`
    - added:
      - `recoveryResumeReasonMatchesReviewDecisionRequired(...)`
    - resume classification now accepts both:
      - exact historical blocker string
      - prefixed blocker string with appended runtime detail
- focused regression added in [`internal/task/service_test.go`](/Users/sam/dev/otter-camp/internal/task/service_test.go):
  - `TestClassifyTaskResumeDecisionAllowsHistoricalReviewDecisionBlockerWithDetailSuffix`
- focused verification passed:
  - `go test ./internal/task -run 'Test(ClassifyTaskResumeDecisionAllowsHistoricalReviewDecisionBlocker(WithoutGuard|WithDetailSuffix)|ResumeValidationBlockedTaskRequiresResumableBlockedState|ResumeValidationBlockedTaskRejectsNonBlockedTask)$' -count=1`
- deployed:
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
  - health is green

- live proof on rerun-38 main project `fc4a025d-e7c9-4b88-9485-17d2b6328e52`:
  - previously blocked task:
    - task `ffa596f9-3c6a-422e-8706-2ae68af85f8c`
    - `V1-1: Verify Canonical Bootstrap Steps Are Seeded`
    - `work_status=blocked`
  - product-surface resume attempt before the patch failed with:
    - `task_resume_blocked_without_resumable_state`
    - because the stored blocker reason was:
      - `review turn completed without calling flow.review_decision: git worktree remove --force ... is not a .git file`
  - after the patch and restart, the same HTTP endpoint succeeded:
    - `POST /v1/tasks/ffa596f9-3c6a-422e-8706-2ae68af85f8c/resume`
    - authenticated as the local admin on the live server
  - resulting live state:
    - task `10` is now back to `review`
    - fresh review execution:
      - `b77891e5-39fd-4c68-bcc7-9441da5f77bf`
    - fresh active review session:
      - `01e281ab-bffb-44f1-ac9e-2605591500ae`
      - `status=active`
      - `created_at=2026-03-25 08:00:00.944562-06`

- current critical path after this resume:
  - rerun-38 is no longer hard-stuck on the old historical worktree-removal blocker
  - next thing to watch is whether revived task `10` review completes cleanly under the newer worktree + review runtime fixes, and whether later-wave tasks `13-24` begin releasing behind it

## 2026-03-25 08:16 MDT

- deployed the pending legacy worktree-removal compatibility patch that had been staged but not yet verified:
  - [`internal/tools/native/task_worktree.go`](/Users/sam/dev/otter-camp/internal/tools/native/task_worktree.go)
    - `removeTaskWorktree(...)` now treats these `git worktree remove --force` failures as recoverable and falls back to `os.RemoveAll(worktreeRoot)`:
      - `is not a working tree`
      - legacy `validation failed, cannot remove working tree ... is not a .git file`
      - legacy `validation failed, cannot remove working tree ... not a git repository`
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - `removeTurnTaskWorktree(...)` now mirrors the same compatibility logic through `turnRecoverableWorktreeRemoveError(...)`

- coverage now exists on both code paths:
  - [`internal/tools/native/task_worktree_test.go`](/Users/sam/dev/otter-camp/internal/tools/native/task_worktree_test.go)
    - `TestIsRecoverableWorktreeRemoveError`
  - [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go)
    - `TestTurnRecoverableWorktreeRemoveError`

- focused verification passed:
  - `go test ./internal/tools/native -run 'TestIsRecoverableWorktreeRemoveError$' -count=1`
  - `go test ./internal/turn -run 'Test(TurnRecoverableWorktreeRemoveError|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation)$' -count=1`

- deployed:
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
  - `./bin/ottercamp health` is green

- live proof after restart on rerun-38 task `10` review session `01e281ab-bffb-44f1-ac9e-2605591500ae`:
  - the session was picked up again immediately after restart:
    - worker log:
      - `2026-03-25 08:12:52 MDT`
      - `agent_turn dispatch: start inbound turn ... turn_id=f73039b3-612a-4b7b-9df0-c4871048f264`
  - current live state:
    - session remains `active`
    - current turn:
      - `f73039b3-612a-4b7b-9df0-c4871048f264`
      - `status=in_progress`
      - `trigger_message_id=7da5ab00-2346-4206-bc4b-ae522a396dc8`
    - current invocation:
      - `af3ece45-833b-4fe1-899d-8c30dd7e25e9`
      - `status=in_flight`
      - model `qwen2.5:72b`
      - `run_id=99ace434-ccf3-4d0f-abce-939e5b05bc18`
  - importantly:
    - the old worktree-removal failure has not reappeared since restart
    - there is no fresh `tool_result` showing:
      - `validation failed, cannot remove working tree ... is not a .git file`

- narrower remaining seam on task `10`:
  - the review lane is still internally inconsistent even though the closeout error has not resurfaced yet:
    - older pending user prompts remain stacked in the session:
      - kickoff review message `62663390-3839-42ac-955d-3c5b279f18e9`
      - old review action `18d77cf6-83bc-45e1-bbd1-928a6c00eb6e`
      - supervisor recovery `64094dc3-a2d0-428c-8e2b-d783275963be`
      - old review action `5eb5abed-90b7-4510-9f44-2d56f19c2b18`
      - fresh review action `7da5ab00-2346-4206-bc4b-ae522a396dc8`
    - the claimed live `agent_turn` job is still the original kickoff-dispatch row:
      - job `8df041b7-fe35-45ae-9c55-74f3f2a111c1`
      - payload message id `62663390-3839-42ac-955d-3c5b279f18e9`
      - status `claimed`
      - `claimed_at` continues heartbeating
    - but the actual live turn is rooted to the newer synthetic review-action message `7da5ab00-...`
    - the live invocation is also still attributed to the original failed run id `99ace434-ccf3-4d0f-abce-939e5b05bc18`, while the intervening supervisor run `2053749b-b69e-406b-8913-3bd53960d84d` is already terminal `failed`

- current conclusion:
  - the legacy worktree-removal corruption seam is now patched and deployed
  - the next remaining issue on the rerun-38 critical path is review retry / supervisor recovery consistency:
    - stale pending review prompts are not being retired
    - the live claimed job remains rooted to the original kickoff message while the active turn is rooted to the newest synthetic review prompt
    - current task state is still:
      - task `10`
      - `work_status=review`

## 2026-03-25 08:23 MDT

- found and fixed a narrower worker cleanup bug behind the rerun-38 review churn:
  - [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go)
    - `PurgeStaleAgentTurnJobs(...)` condition 6 previously dead-lettered only `pending` duplicate live message-attempt dispatches
    - that allowed multiple simultaneously `claimed` `agent_turn` jobs to survive for the same live `project_task` execution/session when an older kickoff dispatch and a newer retry dispatch both pointed at the same `flow_node_execution_id`
    - the update now purges duplicate `claimed` live dispatches too, not just `pending` ones
- new focused regression:
  - [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go)
    - `TestJobWorkerPurgeStaleAgentTurnJobsPurgesDuplicateClaimedLiveExecutionDispatchAcrossMessages`
- focused verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerPurgeStaleAgentTurnJobs(KeepsClaimedLiveMessageAttemptDispatch|PurgesDuplicateClaimedLiveExecutionDispatchAcrossMessages)$' -count=1`
  - `go test ./internal/jobqueue -run 'TestClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace$' -count=1`
- deployed:
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
  - `./bin/ottercamp health` is green

- live proof on rerun-38 task `10` review session `01e281ab-bffb-44f1-ac9e-2605591500ae`:
  - before this patch, the session carried two claimed `agent_turn` jobs at once:
    - older kickoff dispatch:
      - `8df041b7-fe35-45ae-9c55-74f3f2a111c1`
      - payload message id `62663390-3839-42ac-955d-3c5b279f18e9`
    - fresh retry dispatch:
      - `8f1c9d3d-3a1c-41d2-ac3d-3b01816ff831`
      - payload message id `8399b964-9d2b-4ce6-b60a-0b01ac7ab874`
  - after the patch and restart:
    - older kickoff dispatch `8df041b7-fe35-45ae-9c55-74f3f2a111c1` is now:
      - `status=dead_letter`
      - `last_error=purged duplicate live message-attempt dispatch`
    - only the fresh retry dispatch remains live:
      - `8f1c9d3d-3a1c-41d2-ac3d-3b01816ff831`
      - `status=claimed`
  - this is the first live proof that the worker is no longer preserving both claimed dispatches for the same task-10 review execution

- current live state after that fix:
  - task `10` is still `review`
  - current turn:
    - `4f5be40e-6ef6-4bc8-acb9-db6ac429fe8b`
    - `status=in_progress`
    - `trigger_message_id=23056b82-f447-47f6-a593-684843b106a0`
  - current invocation:
    - `6877de32-f7de-4f48-b297-38a58c7830a8`
    - `status=in_flight`
    - model `qwen2.5:72b`
    - `run_id=7de28565-a649-4288-88b1-fda13d290b14`

- remaining blocker:
  - the duplicate-claimed-dispatch seam is fixed
  - but the active task-10 review turn is still in flight under local Ollama
  - the next thing to watch is whether this cleaner retry finally completes the review lane, or whether there is still another provider/ownership seam after the duplicate-job cleanup

## 2026-03-25 08:31 MDT

- found and fixed the next worker cleanup inconsistency behind rerun-38 task `10` review recovery:
  - [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go)
    - `FailStaleModelInvocations(...)` previously failed any in-progress `run` for the stale turn's session when `turn_id = stale_turn_id OR turn_id IS NULL`
    - that was too broad for supervisor recovery runs with `turn_id IS NULL` that get immediately reused by a newer live turn on the same `flow_node_execution`
    - the update now skips failing a candidate run when there is another `model_invocation` still `in_flight` on that same `run_id` for a different turn
- new focused regression:
  - [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go)
    - `TestJobWorkerFailStaleModelInvocationsKeepsSharedLiveRunForNewerTurn`
- focused verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerFailStaleModelInvocations(FailsOldActiveExecutionTaskSession|KeepsSharedLiveRunForNewerTurn|SkipsActiveExecutionTaskSession|FailsOrphanedLiveTurnsWithoutClaimedJob)$' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)$' -count=1`
- deployed:
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
  - `./bin/ottercamp health` is green

- live proof after restart on rerun-38 task `10` review session `01e281ab-bffb-44f1-ac9e-2605591500ae`:
  - the lane rolled forward onto a fresh supervisor recovery run instead of continuing with the earlier failed shared run:
    - previous failed run:
      - `7de28565-a649-4288-88b1-fda13d290b14`
      - `status=failed`
    - current live run:
      - `4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`
      - `status=in_progress`
      - `flow_node_execution_id=b77891e5-39fd-4c68-bcc7-9441da5f77bf`
  - the current live turn/invocation pair is now:
    - turn `eb058686-f6c6-456c-8f70-479941ba6e9b`
      - `status=in_progress`
      - `trigger_message_id=221b3ce2-e553-4df2-b28b-523ad9b02621`
    - invocation `d50ba8d3-eca1-4faa-9781-7d7dadaea293`
      - `status=in_flight`
      - `run_id=4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`
  - the earlier stale retry turn was recovered cleanly:
    - turn `cf5a8dc0-7088-4545-afa2-8b2992ade2fc`
      - `status=failed`
      - `error_message=recovered stale in-progress message turn without live job or execution; scheduling a fresh retry`
    - invocation `fac0d9bd-bc12-4987-b39e-3386dfa4955d`
      - `status=failed`
      - still attached only to the old failed run `7de28565-a649-4288-88b1-fda13d290b14`
  - the temporarily duplicated claimed dispatches also drained after restart:
    - `ea579521-c273-4526-a66e-c5513d06f2f2`
      - `status=done`
      - message `ba928924-2a8d-41a8-985d-6a0b47384879`
    - `ae0601d2-2de4-4ad7-b3a6-2d548d5d6367`
      - `status=done`
      - message `23056b82-f447-47f6-a593-684843b106a0`
    - worker log shows the superseded retry hitting `should_run=false` instead of persisting as a live claimed blocker

- current live state:
  - rerun-38 task `10` is still `review`
  - session `01e281ab-bffb-44f1-ac9e-2605591500ae` remains `active`
  - current turn `eb058686-f6c6-456c-8f70-479941ba6e9b` is still running on local Ollama under the fresh run `4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`

- current blocker:
  - the run-failure inconsistency is patched and deployed
  - the next thing to verify is whether this fresh task-10 review turn actually resolves the lane or exposes the next review-session recovery seam

## 2026-03-25 08:39 MDT

- found and fixed the next false-positive cleanup seam for long-running local task turns:
  - [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go)
    - `FailStaleModelInvocations(...)` was still treating active `project_task` invocations as stale after only `2m` once the dispatch job had finished, even while the model was legitimately still running
    - this threshold now uses the normal async task continuation window instead of the old 2-minute cutoff
- focused worker verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerFailStaleModelInvocations(FailsOldActiveExecutionTaskSession|KeepsSharedLiveRunForNewerTurn|SkipsActiveExecutionTaskSession|FailsOrphanedLiveTurnsWithoutClaimedJob)$' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)$' -count=1`

- live proof for the widened threshold:
  - rerun-38 task `10` current review invocation `e031607b-713f-4356-858a-201a18593f39`
    - created at `2026-03-25 08:32:55.277165-06`
    - remained `status=in_flight` at `2026-03-25 08:37:04.053069-06`
  - that is more than 4 minutes after start, so the old 2-minute false positive is no longer firing on the live lane

- found the next narrower inconsistency immediately after that:
  - fresh retries are still inheriting `run_id` from message metadata even when that run is already terminal `failed`
  - this causes new invocations to bind to the failed supervisor run `4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`

- patched that at turn construction:
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - added `sanitizeInheritedRunAttribution(...)`
    - inbound task/review turns now drop inherited `run_id` / `run_step_id` / `run_attempt_id` when the referenced run is no longer `created` or `in_progress`
- focused engine verification passed:
  - `go test ./internal/turn -run 'Test(SanitizeInheritedRunAttribution(DropsTerminalRun|KeepsActiveRun)|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation)$' -count=1`

- deployed:
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
  - `./bin/ottercamp health` is green

- current live state:
  - task `10` review session `01e281ab-bffb-44f1-ac9e-2605591500ae` has rolled again:
    - current turn `f5461b23-4180-4c67-b14e-81dbe131ef4e`
    - current invocation `d7350275-f273-4b83-8c5d-cc8a86cd7187`
    - both are `in_progress` / `in_flight`
  - this specific retry still points at the previously failed run `4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`, which indicates it was created before the latest engine sanitizer was exercised on a fresh retry path

- current blocker:
  - the long-turn stale-invocation false positive is fixed and live-proven
  - the failed-run-attribution scrub is now in code and deployed
  - the next thing to verify is the next fresh task-10 retry under the newest engine build:
    - it should come up with a brand-new live run instead of reusing failed run `4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`

## 2026-03-25 08:46 MDT

- live proof is now in for both parts of the latest runtime hardening on rerun-38 task `10`:
  - current review turn:
    - `e516eb3d-d68b-4507-bc3c-aad8258e7c3d`
    - `status=in_progress`
    - `trigger_message_id=cbde787b-4db0-4eb3-b340-f0d45c63b5f3`
    - `started_at=2026-03-25 08:43:45.701813-06`
  - current model invocation:
    - `f0f7cf05-cff3-490a-9ece-64815bff410b`
    - `status=in_flight`
    - `created_at=2026-03-25 08:43:45.752281-06`
    - `run_id=NULL`

- this proves two things in production:
  - the widened stale-invocation threshold is holding:
    - at `2026-03-25 08:46:55.204462-06`, invocation `f0f7cf05-...` was still `in_flight`
    - so it survived more than 3 minutes after start and did not get killed at the old 2-minute cleanup boundary
  - the failed-run-attribution scrub is also holding on a fresh retry:
    - this is the first task-10 retry whose live invocation has no inherited failed `run_id`
    - the immediately previous retry `d7350275-f273-4b83-8c5d-cc8a86cd7187` was still incorrectly bound to failed run `4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`
    - the current retry `f0f7cf05-...` is no longer carrying that failed run forward

- remaining live state:
  - task `10` is still `review`
  - session `01e281ab-bffb-44f1-ac9e-2605591500ae` is still `active`
  - stale cleanup is still running globally every minute:
    - worker logs show general `job queue: failed stale model invocations` counts for other sessions
    - but there is no fresh failure on task-10’s current invocation yet

- current blocker:
  - the stale-kill seam and failed-run inheritance seam are both now live-proven fixed for task `10`
  - the next thing to observe is whether this still-running review turn actually resolves the task through `flow.review_decision`, or whether a new review-lane behavior bug appears after the cleanup issues are out of the way

## 2026-03-25 08:48 MDT

- task `10` is now fully complete on rerun-38:
  - [`project_task`] task `10` / `ffa596f9-3c6a-422e-8706-2ae68af85f8c`
    - `work_status=done`
    - `completed_at=2026-03-25 08:48:23.992821-06`
  - review execution `b77891e5-39fd-4c68-bcc7-9441da5f77bf`
    - `status=completed`
    - `completed_at=2026-03-25 08:48:23.970068-06`
    - `review_decision.decision=approve`
  - review session `01e281ab-bffb-44f1-ac9e-2605591500ae`
    - `status=closed`
  - final successful task-10 invocation:
    - `f0f7cf05-cff3-490a-9ece-64815bff410b`
    - `status=completed`
    - `created_at=2026-03-25 08:43:45.752281-06`
    - `completed_at=2026-03-25 08:48:23.44805-06`
    - `run_id=NULL`
  - the final turn `e516eb3d-d68b-4507-bc3c-aad8258e7c3d` is `cancelled` because the session closed cleanly after terminal review completion

- additional metadata cleanup patch landed after task `10` completed:
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - `syncBoundFlowExecutionTurnOwnership(...)` now overwrites `live_run_id` with the current turn’s actual run attribution instead of preserving stale metadata
  - [`internal/flow/execution_service.go`](/Users/sam/dev/otter-camp/internal/flow/execution_service.go)
    - `executionMetadataWithCompletedBy(...)` now clears `live_run_id` / `live_turn_id` when an execution is marked complete
  - focused verification passed:
    - `go test ./internal/turn -run 'Test(SanitizeInheritedRunAttribution(DropsTerminalRun|KeepsActiveRun)|SyncBoundFlowExecutionTurnOwnershipClearsStaleLiveRunID|RecoverProjectTaskStaleInboundTurnWithoutRunKeepsLiveInvocation)$' -count=1`
    - `go test ./internal/flow -run 'Test(ExecutionMetadataWithCompletedByClearsLiveOwner|AdvanceFlowRejectsSelfReview)$' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux `serve` and `worker`
  - `./bin/ottercamp health` is green

- current residual metadata inconsistency:
  - task `10` completed before the last terminal metadata-cleanup patch was deployed
  - so its already-completed execution row still shows stale:
    - `metadata.live_run_id = 4f80f5b2-4dc7-42b6-aef4-9e1ff763d0c9`
  - that is now a post-fix validation item rather than a blocker for task-10 correctness

- current rerun-38 board after task `10` closeout:
  - `done`: tasks `1-12`, `16`, `17`
  - `draft`: tasks `13`, `14`, `15`, `18-24`

- current blocker / next frontier:
  - task `10` is no longer the critical path
  - the next thing to verify is later-wave release:
    - why tasks `13+` remain `draft` after the task-10 closeout
  - secondarily, the new terminal metadata-cleanup patch still needs live proof on a future execution completion to confirm `live_run_id` is cleared on the fresh binary

## 2026-03-25 09:23 MDT

- new worker fixes landed in [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go):
  - stale project-session cleanup now processes paused/failed project sessions instead of ignoring them forever
    - cleanup still fails the stale turn, clears `current_turn_id`, and suppresses requeue when the project is paused or non-active
  - stale null-trigger `project` continuations can now recover even when there is no prior trigger message
    - for completed-bootstrap project sessions with remaining open work, recovery synthesizes a fresh pending `project_execution_continuation` user message instead of requiring a historical trigger
  - `claimPendingAgentTurns(...)` now uses the same fresh-first ordering in the final global claim step that it already used inside per-session ranking
    - this stops old `project` continuation/bootstrap retries from always winning slots over newer project continuations

- focused regressions added in [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go):
  - `TestJobWorkerRecoverStaleInProgressContinuationTurnsSynthesizesProjectContinuationWithoutPriorTrigger`
  - `TestJobWorkerRecoverStaleInProgressTriggeredTurnsClearsPausedProjectWithoutRequeue`
  - `TestJobWorkerClaimPendingAgentTurnsPrefersNewestProjectContinuation`

- verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorker(ClaimPendingAgentTurns(PrefersNewestProjectContinuation|PrioritizesFreshOrgWorkOverProjectContinuation|SkipsStaleProjectBootstrapAndContinuationJobs)|RecoverStaleInProgressContinuationTurns(SynthesizesProjectContinuationWithoutPriorTrigger|SuppressesCompletedProjectBootstrapRequeue|SuppressesProjectContinuationWithoutOpenTasks)?|RecoverStaleInProgressTriggeredTurns(ClearsPausedProjectWithoutRequeue|SuppressesCompletedProjectBootstrapRequeue|SuppressesProjectContinuationWithoutOpenTasks)?)' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)$' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux worker pane `codex-e2e-20260324:0.1` with `--concurrency 24`

- live proof:
  - paused failed bootstrap session `364e869d-e8ef-4aa4-bf33-f8de6db11187`
    - now has `current_turn_id = NULL`
    - stale turn `12b4dbca-c090-4a3d-838b-17da3863a3fc` is `failed`
    - no fresh retry was queued; only terminal historical `agent_turn` rows remain
  - stale project backlog moved:
    - before this round: `inflight_project_turns = 19`
    - after stale-project cleanup patch and restart: `inflight_project_turns = 16`
    - after the later claim-order restart: `inflight_project_turns = 17`, `inflight_project_invocations = 22`
    - interpretation: cleanup successfully removed some zombies, but the worker still has a broad active project backlog
  - fresh rerun-38 session is again live on a true continuation turn:
    - project session `efd6b36b-7687-4b23-a635-9f00df19bbb5`
    - current turn `11b95ff1-5489-4d87-ae35-88ec20daa377`
    - source `project_execution_continuation`
    - started `2026-03-25 09:22:07.025621-06`
  - one older backlog project session that previously held a current turn no longer does:
    - session `73ecd8a3-ae51-4926-a977-d65e3d047720`
    - `current_turn_id = NULL`

- current live state on rerun-38 project `fc4a025d-e7c9-4b88-9485-17d2b6328e52` is unchanged at the task board level:
  - `done`: tasks `1-12`, `16`, `17`
  - `draft`: tasks `13`, `14`, `15`, `18-24`

- current blocker:
  - the stale paused/null-trigger leaks are materially improved and the fresh claim-order bias is deployed
  - but the worker is still spending many slots on old active `project` continuations across historical reruns
  - rerun-38 is active again, yet later-wave project tasks `13+` have still not been advanced
  - next likely seam is project-session continuation behavior after claim, not bootstrap setup or task-lane cleanup

## 2026-03-25 09:31 MDT

- git state:
  - committed and pushed current patch set to `main`
  - commit: `7b078d26`
  - subject: `Harden recovery and project execution runtime`
  - protected untracked artifacts were intentionally left out:
    - `.oc.db`
    - `data/objects/`
    - `internal/turn/bootstrap_refresh_codex_test.go`
    - `skills/`
    - stray `"'done' order by task_number;\""`
    - local scratch `tmp_recovery_resume.go`

- current live rerun-38 state:
  - project session `efd6b36b-7687-4b23-a635-9f00df19bbb5`
  - current turn `11b95ff1-5489-4d87-ae35-88ec20daa377`
  - turn source `project_execution_continuation`
  - turn status `in_progress`
  - current model invocation `b4cc95a7-ca4c-420e-8f64-5dd8a3363391`
  - invocation status `in_flight`
  - assistant placeholder message `a7759cf7-4a7e-4001-85ab-afce01f94c4f` is still `pending`

- important clarification on the later-wave seam:
  - rerun-38 tasks `13-24` still remain `draft`
  - there are **no** rows in `project_task_dependency` for this project, so the later-wave tasks are not being held by dependency edges
  - their only visible task metadata is `{"bootstrap_first_wave_selected": false}`
  - the continuation fallback path in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) still looks correct:
    - `handleCompletedProjectExecutionContinuationTurn(...)` calls `nextRunnableDraftProjectTask(...)`
    - if the continuation turn completes without successful tool results, it should auto-queue the next runnable draft task
  - so the current issue is not that the later-wave tasks are structurally blocked
  - the current issue is that the live project continuation turn has not completed yet, so that fallback auto-queue path has not fired

- additional live noise still present:
  - stale old bootstrap dispatch `5041420c-a5ba-4e32-8c46-9e1494551a42` still exists as `pending` on the same rerun-38 session for message `53069ddc-9504-4122-9794-b9b2be3c4ae9`
  - the active claimed dispatch for the current good continuation is:
    - `f959b6a9-65f0-4be1-8ac2-1fb579b828f1`
    - payload points to continuation message `d653de35-4a9a-46c2-8166-516c6f670971`
    - `retry_count=1`

- current blocker:
  - rerun-38 is on the correct `project_execution_continuation` lane now
  - later-wave tasks are not dependency-blocked
  - the next thing to prove is whether the still-running continuation turn actually mutates the task tree or completes into the narrative-only fallback that auto-queues the next draft task

## 2026-03-25 09:33 MDT

- latest live saturation evidence after the worker cleanup / fresh-first claim patches:
  - worker tail is still repeatedly logging:
    - `job queue: no execution slots available inflight=24 capacity=24`
  - current count of `in_flight` project-scope model invocations is still `20`
  - the oldest active `project` invocations are not all fresh work:
    - `a877cbb7-8595-4385-9786-30193a09d3ec` on session `d8d679cc-dd5a-4ead-a406-35de6675b2e4` (`speaker-pipeline-ops-validation-fresh-43`), age about `27m`
    - `7e46f9d9-0fd9-4163-86de-5821282a1501` on session `e1e9661b-6197-410a-971a-a120c4a239ee` (`speaker-pipeline-ops-validation-fresh-24`), age about `27m`
    - `686ce9b6-69e8-41a9-bf41-0c7679c09b0a` on session `fddd9a30-f90c-4818-ab7f-0d0fc1bd84db` (`speaker-pipeline-ops-validation-fresh-21`), age about `27m`
  - several sibling rerun-38-family project continuations are also occupying slots simultaneously:
    - session `8c399b76-37ee-4f3e-8668-410c3b304d1d` / slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-38-8bb712`
    - session `45ea2e24-4a3d-4fd3-9fe2-727a2d8b07a8` / slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-38-fa545e`
    - session `5a5d2bf8-df95-43c5-9fb1-8b4ec2f30340` / slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-38-8a66cb`
    - session `bee8c64a-1347-441e-9781-0bc6748028c0` / slug `speaker-pipeline-validation-fresh-20260325-38`
    - session `d01da998-a0ac-4d94-9e42-b5587b6066f9` / slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-38-d3783d`
  - worker is also still periodically reintroducing old project lanes:
    - `job queue: requeued active project sessions without turns count=2`
    - then later `count=4`

- current rerun-38 board is still:
  - `done`: tasks `1-12`, `16`, `17`
  - `draft`: tasks `13`, `14`, `15`, `18-24`

- updated interpretation:
  - the remaining blocker is no longer task dependency structure
  - it is project-session continuation saturation:
    - old historical `project` continuations are still holding model slots for long stretches
    - fresh rerun-38 continuation work is competing with those old lanes plus multiple rerun-38 sibling continuations
  - next code path to tighten is worker cleanup / claim behavior for stale long-running `project_execution_continuation` invocations and the `requeued active project sessions without turns` loop

## 2026-03-25 09:39 MDT

- two more worker fixes landed in [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go):
  - `FailStaleModelInvocations(...)` now fails orphaned async `project`/`project_task` model invocations with `turn_id = NULL` on the normal continuation budget instead of letting them sit until the generic `30m` orphan timeout
  - `claimPendingByFilter(...)` now applies a global cap of `4` concurrent async `project` continuation/bootstrap claims via `maxInFlightProjectContinuations`
    - this is claim-time back-pressure, not just stale cleanup, so a worker restart no longer floods all `24` slots with historical project continuations at once

- new regressions added in [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go):
  - `TestJobWorkerFailStaleModelInvocationsFailsOrphanedAsyncProjectSessionInvocationWithoutTurn`
  - `TestJobWorkerClaimPendingAgentTurnsCapsConcurrentProjectContinuations`

- verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerFailStaleModelInvocations(FailsOrphanedAsyncProjectSessionInvocationWithoutTurn|FailsOrphanedLiveTurnsWithoutClaimedJob|SkipsActiveAsyncOrganizationSession|SkipsActiveExecutionTaskSession|FailsOldActiveExecutionTaskSession|RequeuesTriggeredProjectSession)$' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerClaimPendingAgentTurns(PrioritizesFreshOrgWorkOverProjectContinuation|PrefersNewestProjectContinuation|CapsConcurrentProjectContinuations|SkipsStaleProjectBootstrapAndContinuationJobs)$' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)$' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux worker pane `codex-e2e-20260324:0.1` with `--concurrency 24`
  - `./bin/ottercamp health` is green

- live proof after restart:
  - startup no longer refilled the pool with a wall of project continuations
  - worker log now shows:
    - `job queue: requeued active project sessions without turns on startup count=3`
    - then a fresh organization turn claimed first:
      - session `672477c4-4431-4969-bf7b-923253907972`
      - turn `f1f35641-6716-404c-9a0f-28c98dbf807e`
  - after restart stabilization:
    - `in_flight` project-scope model invocations dropped from `20` to `15`
    - distinct live scope mix was:
      - `project = 13`
      - `project_task = 11`
      - `organization = 1`
  - rerun-38 canonical project session is now clean but waiting:
    - session `efd6b36b-7687-4b23-a635-9f00df19bbb5`
    - `current_turn_id = NULL`
    - pending continuation dispatch `832bfe39-f0f7-4f5b-b19a-6b4690e37864`
    - payload:
      - message `d653de35-4a9a-46c2-8166-516c6f670971`
      - `retry_count = 2`
  - sibling rerun-38 session `d01da998-a0ac-4d94-9e42-b5587b6066f9` is in the same state:
    - `current_turn_id = NULL`
    - pending dispatch `00038cc2-9586-4c43-8122-9d9b311ee111`

- current rerun-38 board is still:
  - `done`: tasks `1-12`, `16`, `17`
  - `draft`: tasks `13`, `14`, `15`, `18-24`

- current blocker is narrower now:
  - the restart flood is fixed
  - rerun-38 is no longer buried inside a `24`-slot project-continuation stampede
  - the remaining issue is stale project-continuation attrition inside the capped pool:
    - old project continuations still hold `13` live slots
    - rerun-38 continuation is now correctly pending behind that budget instead of being immediately drowned out
  - first post-patch stale-scan proof is also live:
    - worker logged `job queue: failed stale model invocations count=1` at `2026-03-25 09:40:01 MDT`
    - rerun-38 pending continuation dispatch `832bfe39-f0f7-4f5b-b19a-6b4690e37864` was still pending after that first scan, so one more stale-attrition cycle or a stricter freshness gate is still needed before rerun-38 itself gets a project slot

## 2026-03-25 09:44 MDT

- follow-up stale-cleanup hardening landed in [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go):
  - the same continuation-budget threshold now applies not only to async `project`/`project_task` invocations with `turn_id = NULL`, but also to invocations attached to stale detached turns that are no longer the session’s current live turn
  - this closes the live gap where old project invocations like:
    - `14810dfa-53fc-413d-8c43-48d759d976f3`
    - `b15fcd49-0f58-4739-a93b-b74e080f31a9`
    were still waiting on the generic `30m` orphan timeout because they had a stale `turn_id`

- new regression added in [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go):
  - `TestJobWorkerFailStaleModelInvocationsFailsDetachedAsyncProjectSessionInvocationWithStaleTurn`

- verification passed:
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerFailStaleModelInvocations(FailsDetachedAsyncProjectSessionInvocationWithStaleTurn|FailsOrphanedAsyncProjectSessionInvocationWithoutTurn|FailsOrphanedLiveTurnsWithoutClaimedJob|SkipsActiveAsyncOrganizationSession|SkipsActiveExecutionTaskSession|FailsOldActiveExecutionTaskSession|RequeuesTriggeredProjectSession)$' -count=1`
  - `go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerClaimPendingAgentTurns(CapsConcurrentProjectContinuations|PrioritizesFreshOrgWorkOverProjectContinuation|PrefersNewestProjectContinuation|SkipsStaleProjectBootstrapAndContinuationJobs)$' -count=1`
  - `go test ./internal/jobqueue -run 'Test(ClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace|AgentTurnRateLimitDelay(CapsProviderHintAtBackoffCap|UsesProviderHintWhenBelowBackoffCap)|RejitteredRateLimitedRunAfterClampsOversizedRunAfter)$' -count=1`
  - rebuilt `./bin/ottercamp`
  - restarted tmux worker pane `codex-e2e-20260324:0.1`
  - `./bin/ottercamp health` is green

- live proof:
  - new worker startup logged:
    - `job queue: failed stale model invocations on startup count=8`
  - after that restart, project-scope `in_flight` invocation count dropped again:
    - from `15` to `11`
  - the oldest live project invocations are now the ~`09:35 MDT` continuation cohort; the older ~`09:15 MDT` detached invocations disappeared from the oldest-live list

- current rerun-38 / sibling state:
  - rerun-38 canonical session `efd6b36b-7687-4b23-a635-9f00df19bbb5`
    - `current_turn_id = NULL`
    - pending continuation dispatch `832bfe39-f0f7-4f5b-b19a-6b4690e37864` still exists
  - sibling session `d01da998-a0ac-4d94-9e42-b5587b6066f9`
    - now has live current turn `0c55ed42-ddaa-4ceb-820c-705efccc76e3`
    - trigger message `dee07ef6-43d7-4b70-85c0-ccdcf9205ba4`

- current blocker:
  - stale project continuation attrition is materially better
  - the next remaining seam is freshness/selection inside the surviving ~`09:35 MDT` project continuation cohort:
    - rerun-38 canonical session is still pending
    - a sibling rerun-38 session is the one currently holding live project continuation ownership

## 2026-03-25 10:45 MDT

- fresh-install Claude/provider recovery work is now separated into two distinct states:
  - the intended default model breakdown is confirmed in code and tests:
    - `high-capability -> claude-opus-4-20250514`
    - `standard -> claude-sonnet-4-20250514`
    - `haiku -> claude-haiku-4-5-20251001`
  - the live Anthropic connection is healthy for plain message requests, but not yet for tool-using agent turns when authenticated with the provided `sk-ant-oat01-...` token

- live runtime state on the freshly reset install:
  - `./bin/ottercamp health` is green
  - provider connections:
    - openai / local Ollama connection `34b13633-46b0-4471-ba52-1c0ad20cd36b` healthy
    - anthropic / `Anthropic Primary` connection `b4e3c5c7-ebb6-4d8f-bad6-c70ba3b2447f` healthy
  - current org model profiles are correctly pointed at Anthropic:
    - `high-capability` version `3` current true -> `claude-opus-4-20250514`
    - `standard` version `3` current true -> `claude-sonnet-4-20250514`
    - `haiku` version `3` current true -> `claude-haiku-4-5-20251001`

- bootstrap/profile fixes landed:
  - [`internal/repo/model_profile.go`](/Users/sam/dev/otter-camp/internal/repo/model_profile.go)
    - `Deprecate(...)` now allocates the next historical version number via `MAX(version)+1` instead of `current.Version+1`, which fixes duplicate-version conflicts after operator rewinds
  - [`internal/repo/model_registry_integration_test.go`](/Users/sam/dev/otter-camp/internal/repo/model_registry_integration_test.go)
    - added `TestModelProfileRepoDeprecateUsesNextHistoricalVersion`
  - [`internal/bootstrap/bootstrap.go`](/Users/sam/dev/otter-camp/internal/bootstrap/bootstrap.go)
    - bootstrap now seeds org profiles only when absent and preserves an existing current org profile on rerun instead of rotating it back to the seed
  - [`internal/bootstrap/bootstrap_test.go`](/Users/sam/dev/otter-camp/internal/bootstrap/bootstrap_test.go)
    - removed the stale unit test for the old rotation helper

- verification passed:
  - `go test -tags=integration ./internal/repo -run 'TestModelProfileRepo(DeprecateAndCurrentUniqueness|DeprecateUsesNextHistoricalVersion)$' -count=1`
  - `go test -tags=integration ./internal/bootstrap -run 'TestBootstrapRun(SeedsAndIsIdempotent|PreservesExistingCurrentModelProfileVersion)$' -count=1`
  - `go test ./internal/gateway -run 'Test(BuildProviderBodyOmitsToolsForListeningEval|BuildProviderBodyAnthropicIncludesTools|ApplyAnthropicSubscriptionHeaders)' -count=1`

- Anthropic credential finding:
  - the provided `sk-ant-oat01-...` token works for plain `Messages` API calls only when sent through the OAuth/beta header path
  - direct minimal plain-message request succeeds under that path
  - direct tool-enabled request fails reproducibly with HTTP `400` / `invalid_request_error` even with a single trivial hardcoded tool, so the failure is not caused by OtterCamp prompt history or by one malformed tool schema
  - direct bearer request without the OAuth/beta headers returns:
    - `OAuth authentication is currently not supported.`
  - inference from the live repro:
    - this credential path is usable for non-tool requests
    - this credential path is not currently usable for OtterCamp `agent_turn` traffic, because OtterCamp agent turns depend on tool use

- concrete live repro details:
  - Frank session: `0cca8ad7-76ee-4c09-be0b-871a701ce6d5`
  - successful listening-eval invocation on Anthropic:
    - `55fcf8bc-fa92-43ff-bbfc-e11d90e71798`
    - purpose `listening_eval`
    - model `claude-haiku-4-5-20251001`
  - failing tool-enabled Anthropic agent-turn invocation:
    - `a4246e81-dc04-457d-b173-e03af021076c`
    - purpose `agent_turn`
    - model `claude-opus-4-20250514`
    - error `provider http 400: {"type":"error","error":{"type":"invalid_request_error","message":"Error"},"request_id":"req_011CZQ7e5i97T1zcGUUqZ81G"}`

- current blocker:
  - the model/profile breakdown is now correct and bootstrap reruns preserve operator-selected profiles
  - the remaining Claude blocker is credential capability, not routing:
    - to run Frank and other tool-using agents on Anthropic end-to-end, OtterCamp still needs a real Anthropic API key path or a different supported Anthropic auth flow for tool-enabled Messages requests

- post-rebuild live proof:
  - rebuilt `./bin/ottercamp`
  - restarted tmux backend session `ottercamp-fresh`
  - `./bin/ottercamp health` stayed green
  - sent a fresh Frank probe:
    - user message `46a6e380-f701-4d66-9e1a-50b8f338b0b8`
    - turn `2166a21f-dba4-41a0-9e61-df683d1ce0a4`
  - post-restart Anthropic split is unchanged:
    - `listening_eval` invocation `403c4988-d898-4fe5-a795-956130f9a1a4` completed on `claude-haiku-4-5-20251001`
    - follow-on `agent_turn` invocation `68241c7d-a759-446c-a3e1-69d8a7a2dd17` failed on `claude-opus-4-20250514` with:
      - `provider http 400: {"type":"error","error":{"type":"invalid_request_error","message":"Error"},"request_id":"req_011CZQ8k6GQi6usrKZWT9vb5"}`

## 2026-03-25 Anthropic setup-token tool-use root cause and fix

- research result:
  - the `sk-ant-oat01-...` token family from `claude setup-token` does support tool-using Anthropic turns
  - local OpenClaw proof:
    - `openclaw models list --provider anthropic --all --json` succeeded with `ANTHROPIC_OAUTH_TOKEN=<setup-token>`
    - `openclaw agent --local --agent main --message 'List the first five files...' --timeout 90 --json` succeeded on Anthropic with real tool use
  - direct Anthropic API proof:
    - non-streaming tool requests succeeded with the same bearer token and OAuth headers
    - streaming tool requests also succeeded once the request matched Claude Code's OAuth shape

- actual root cause in OtterCamp:
  - OtterCamp was sending Anthropic subscription/setup-token `system` as a plain string
  - for Claude OAuth/setup-token auth, Opus tool-streaming requests require Claude Code-style `system` content blocks
  - the critical accepted shape is:
    - first `system` block: `You are Claude Code, Anthropic's official CLI for Claude.`
    - second `system` block: the real agent/system prompt
    - each with `cache_control.type = ephemeral`
  - with the old plain-string `system`, Anthropic returned the opaque HTTP `400` / `invalid_request_error` that Frank was hitting

- direct repro matrix:
  - failing:
    - `claude-opus-4-20250514` + `stream=true` + OAuth headers + tools + plain-string `system` -> `400 invalid_request_error`
  - succeeding:
    - `claude-opus-4-20250514` + `stream=true` + OAuth headers + tools + Claude Code identity `system` block + prompt `system` block -> `200`
    - `claude-sonnet-4-5-20250929` + same OAuth/tool path -> `200`
    - `claude-haiku-4-5-20251001` direct tool streaming also reproduced as healthy under the corrected shape
  - extra beta flags were not the blocker; the decisive fix was the `system` payload shape

- code fix landed:
  - [`internal/gateway/client.go`](/Users/sam/dev/otter-camp/internal/gateway/client.go)
    - added `providerBodyOptions`
    - Anthropic body building now knows when the connection is `subscription` auth
    - subscription auth now emits Claude Code-style `system` blocks via `anthropicSystemPayload(...)`
    - the first block is the Claude Code identity prelude
    - the second block is the original assembled system prompt when present
  - no model remapping workaround was required; the problem was request serialization, not the setup-token itself

- tests added/updated:
  - [`internal/gateway/client_test.go`](/Users/sam/dev/otter-camp/internal/gateway/client_test.go)
    - `TestBuildProviderBodyAnthropicSubscriptionUsesClaudeCodeSystemBlocks`
    - `TestBuildProviderBodyAnthropicAPIKeyKeepsStringSystemPrompt`
  - [`internal/gateway/client_integration_test.go`](/Users/sam/dev/otter-camp/internal/gateway/client_integration_test.go)
    - subscription integration test now asserts the Claude Code identity system block is present in the provider payload

- verification passed:
  - `go test ./internal/gateway -run 'Test(BuildProviderBodyAnthropicSubscriptionUsesClaudeCodeSystemBlocks|BuildProviderBodyAnthropicAPIKeyKeepsStringSystemPrompt|ApplyAnthropicSubscriptionHeaders|BuildProviderBodyOmitsToolsForListeningEval|BuildProviderBodyAnthropicIncludesTools)' -count=1`
  - `go test -tags=integration ./internal/gateway -run 'TestLiveModelGatewayCompleteAnthropicSubscriptionAuthUsesBearerToken|TestLiveModelGatewayCompleteAnthropicToolNamesAreSanitized' -count=1`

- live proof after rebuild/restart:
  - rebuilt `./bin/ottercamp`
  - restarted backend in tmux session `ottercamp-anthropic-fix`
  - `./bin/ottercamp health` passed
  - sent real Frank message:
    - session `0cca8ad7-76ee-4c09-be0b-871a701ce6d5`
    - user message `a28db52a-d95a-4259-b497-bbf3bb7e4758`
  - Anthropic tool-using `agent_turn` no longer failed with HTTP 400:
    - completed invocations:
      - `4fbaac4b-3d22-4d6e-b2a2-febfbc1386d1`
      - `318c4da9-d7bf-4893-9afd-f9d6c8ecdb5f`
      - `3a855ac7-1d84-421e-a7c9-853e1177c753`
      - `8d4e7c55-508f-4335-bdb8-254bbd3db5b2`
  - Frank emitted a real tool call and OtterCamp executed it:
    - assistant message `6123fe92-1dd6-4837-bc90-35729c1265ac`
    - tool call `project_create`
    - tool result message `90ce9e23-4728-4df8-b571-73cc61c8c81a`
    - created project:
      - id `25b20170-47d7-4929-b8d6-049868bfdeaf`
      - slug `anthropic-oauth-fix-probe`

- current live state:
  - the original Anthropic blocker is fixed
  - Frank is successfully running tool-enabled Claude Opus turns under setup-token/subscription auth
  - the remaining activity in the live session is just the follow-on project handoff/session creation chain continuing from the successful `project.create`

## 2026-03-25 Opus 4.6 alignment

- user-required correction:
  - Frank should be on Opus 4.6, not Sonnet and not the older `claude-opus-4-20250514`

- what was wrong:
  - Frank self-reported `Claude 3.5 Sonnet`, but that answer was stale and incorrect
  - the actual live `high-capability` org profile was still version `3` -> `claude-opus-4-20250514`

- fixes applied:
  - [`internal/bootstrap/bootstrap.go`](/Users/sam/dev/otter-camp/internal/bootstrap/bootstrap.go)
    - changed default `high-capability` seed from `claude-opus-4-20250514` to `claude-opus-4-6`
  - [`internal/bootstrap/bootstrap_integration_test.go`](/Users/sam/dev/otter-camp/internal/bootstrap/bootstrap_integration_test.go)
    - updated expected seeded `high-capability` model to `claude-opus-4-6`
  - live org profile updated through API:
    - `PATCH /v1/model/profiles/high-capability`
    - new current row:
      - version `4`
      - display name `Claude Opus 4.6`
      - model `claude-opus-4-6`

- verification:
  - `go test -tags=integration ./internal/bootstrap -run 'TestBootstrapRun(SeedsAndIsIdempotent|PreservesExistingCurrentModelProfileVersion)$' -count=1`
  - DB proof after patch:
    - `high-capability` version `4` current true -> `claude-opus-4-6`

- live proof:
  - fresh Frank prompt:
    - message `daa31315-7fc6-49f7-917a-d82050d7a9a6`
  - fresh invocations after the profile bump:
    - `f0093cad-6d97-41cc-a19c-32d3508680e5` -> `claude-opus-4-6`
    - `d176d7d3-2a9a-4a42-8ae4-6ec7caa5438d` -> `claude-opus-4-6`
  - live tool-use proof on 4.6:
    - Frank emitted `project.list`
    - tool result confirmed project `anthropic-oauth-fix-probe`

- current truth:
  - Frank is now actually routing `agent_turn` traffic through `claude-opus-4-6`
  - if Frank verbally reports a different model again, trust `model_invocation.model_name`, not the assistant text

## 2026-03-25 Plan 0325A pre-test audit and bootstrap rotation checkpoint

- current plan context:
  - following `issues/plan-0325a.md` / `issues/plan-0325b.md` now
  - pre-test audit result:
    - gate 1 (`task worktree fail closed`) already appears implemented
    - gate 3 (`later-wave draft protection`) appears partially implemented but still needs fuller regression coverage
    - gate 4 (`bootstrap org-profile rotation`) was still open
    - gate 2 (`execution entry_head from actual task branch/worktree`) is still the next runtime fix after the bootstrap slice

- bootstrap rotation fix landed:
  - [`internal/bootstrap/bootstrap.go`](/Users/sam/dev/otter-camp/internal/bootstrap/bootstrap.go)
    - restored explicit org-profile rotation checks via `profileNeedsRotation(...)`
    - reruns now preserve current rows only when the seeded provider/model/display/window/capability fields still match
    - changed seed values now deprecate the current row and create a fresh current version
    - default profile seeds now include explicit display names:
      - `Claude Opus 4.6`
      - `Claude Sonnet 4`
      - `Claude Haiku 4.5`
  - [`internal/bootstrap/bootstrap_integration_test.go`](/Users/sam/dev/otter-camp/internal/bootstrap/bootstrap_integration_test.go)
    - `TestBootstrapRunPreservesExistingCurrentModelProfileVersion`
      - now proves unchanged seed reruns keep the same current row and no extra history version is created
    - `TestBootstrapRunRotatesCurrentModelProfileVersionWhenSeedChanges`
      - proves changed seed values rotate to a new current version with the new model/display values

- verification:
  - `go test -tags=integration ./internal/bootstrap -run 'TestBootstrapRun(SeedsAndIsIdempotent|PreservesExistingCurrentModelProfileVersion|RotatesCurrentModelProfileVersionWhenSeedChanges)$' -count=1`

- live/runtime state:
  - backend tmux remains:
    - `ottercamp-anthropic-fix:0.0` service
    - `ottercamp-anthropic-fix:0.1` worker `--concurrency 24`
  - `./bin/ottercamp health` was green before starting the plan work

- next seam:
  - tighten execution-owned `entry_head_sha` capture so it is sourced from the actual task worktree/branch context used by the execution, then add/fill the deferred later-wave wakeup regressions before starting the operator-style test run

## 2026-03-25 Plan 0325A runtime isolation / entry-head checkpoint

- current repo checkpoint before this slice:
  - pushed bootstrap rotation commit: `370ba629` `Restore bootstrap model profile rotation`

- runtime fixes landed:
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - removed the remaining `projectRoot` fallback from `taskWorkspaceRoot(...)`; task-lane worktree acquisition now fails closed instead of silently reusing the shared project root
    - `syncBoundFlowExecutionTurnOwnership(...)` now overwrites stale `entry_head_sha` metadata with the bound task worktree head when the task worktree head differs from the previously recorded value
  - [`internal/flow/execution_service.go`](/Users/sam/dev/otter-camp/internal/flow/execution_service.go)
    - `ensureExecutionEntryHead(...)` now prefers the managed task worktree head when that worktree already exists, instead of only using repo-path branch refs / shared repo HEAD
    - flow service now carries `DataDir` so managed task worktree paths can be derived consistently
  - [`internal/flow/execution_service_test.go`](/Users/sam/dev/otter-camp/internal/flow/execution_service_test.go)
    - added:
      - `TestEnsureActiveExecutionCapturesEntryHeadSHAFromTaskWorktreeWhenBranchNameMissing`
      - `TestActivateDraftDependentsAfterTaskDoneQueuesExplicitFirstWaveTaskWhenReady`
      - `TestActivateDraftOrchestrationParentAfterChildDoneKeepsDeferredLaterWaveParentDraft`
  - [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go)
    - added `TestTaskWorkspaceRootFailsClosedWhenMainWorktreeOwnsTaskBranch`

- verification:
  - `go test ./internal/flow -run 'Test(EnsureActiveExecutionCapturesEntryHeadSHA(WhenBindingSession|FromTaskBranchRef|FromTaskWorktreeWhenBranchNameMissing)|ActivateDraftDependentsAfterTaskDone(KeepsDeferredLaterWaveTasksDraft|QueuesExplicitFirstWaveTaskWhenReady)|ActivateDraftOrchestrationParentAfterChildDoneKeepsDeferredLaterWaveParentDraft)$' -count=1`
  - `go test ./internal/turn -run 'Test(TaskWorkspaceRootFailsClosedWhenMainWorktreeOwnsTaskBranch|WorkerModelEscalatesToHighCapabilityAfterTransientRetry|ResolveModelProfileWorkerDefaultsToStandardWithoutOverrides)$' -count=1`
  - `go test -tags=integration ./internal/tools/native -run 'TestIntegration(FlowAdvanceCreatesCanonicalCommitWhenCommitSHAOmitted|FlowReviewDecisionApproveCreatesEmptyCanonicalCommit|FlowReviewDecisionRejectCreatesCanonicalRejectionCommit|FlowRecoveryDecisionRetryCreatesFreshExecution|FlowRecoveryDecisionRetryFromReviewCreatesFreshWorkExecution)$' -count=1`

- pre-test gate status after this slice:
  - gate 1 fail-closed worktree isolation: implemented in native + turn runtime
  - gate 2 execution entry-head capture / canonical close lineage: hardened and reverified
  - gate 3 deferred later-wave protection: negative + positive + orchestration-parent regressions now covered
  - gate 4 bootstrap profile rotation: restored in prior slice

- next step:
  - commit/push this runtime slice, rebuild/restart the backend, verify `./bin/ottercamp health`, and then move into the operator-style `issues/plan-0325b.md` end-to-end run without patching code unless the run exposes a new real product seam

## 2026-03-25 Plan 0325B run-prep checkpoint

- latest deployed commit:
  - `59a956ae` `Expose current turn model in prompt`

- dedicated test runtime:
  - tmux session: `codex-e2e-20260324`
    - `0.0` service
    - `0.3` worker `--concurrency 24`
    - `0.1` operator shell
    - `0.2` inspection shell
  - the old `ottercamp-anthropic-fix` backend session was replaced so the test run has its own clean tmux layout

- rebuild / restart verification:
  - `go build -o ./bin/ottercamp ./cmd/ottercamp`
  - `./bin/ottercamp health` -> pass
  - `curl http://localhost:4110/health/live` -> `{"data":{"status":"ok"}}`

- current operator surface:
  - active organization session still exists:
    - session `0cca8ad7-76ee-4c09-be0b-871a701ce6d5`
  - no fresh Plan 0325B proof project has been created yet in this checkpoint

- immediate next action:
  - create a brand-new fresh validation project through the real chat/product surface, then begin logging Phase 1 bootstrap / project continuation evidence in `issues/log-0325a.md`

## 2026-03-25 Plan 0325B fresh run checkpoint: rerun-40

- real operator run started from the dedicated tmux stack and product chat surface
- org session request created:
  - project `85c6f2ad-ce59-425f-b9f9-ce4f81b5d545`
  - slug `speaker-pipeline-ops-validation-fresh-20260325-rerun-40`
  - async project session `856ae42a-5ed8-4c53-bf70-53dc6a9e0c46`

- bootstrap succeeded materially before the first code seam:
  - staffing persisted with PM `54857b82-8e7b-457c-ada7-123357bbede8` plus workers `1a5d4d3d-529a-492f-84fb-596b104a2114`, `c4713758-cfaa-42bc-bc4a-300e517028e1`, reviewer `7fec0bf7-a943-4cbb-ad60-13b56f87656b`
  - bounded child tasks 12-20 created under orchestration parents 9-11
  - first-wave subset selected and promoted to execution: 12, 15, 18
  - later-wave tasks 13, 14, 16, 17, 19, 20 remained `draft`

- first-wave execution proof before the break:
  - task 12 session `99174c1a-be05-428f-9c5c-abbff9a42d7d` execution `f7bd6baa-9a8a-4fce-b57d-46d00fd0cea9`
  - task 15 session `b584efd9-b81e-4290-9a69-3b1cdc8eb38e` execution `8935d900-3d1a-49b5-a18e-38b71c263fc3`
  - task 18 session `6a6f29fc-06ff-47d4-a2b6-28cd8a34c454` execution `12a9f7b9-2f11-4648-8871-2093398965c2`

- real product seam exposed by the clean run:
  - task 12 worktree repair loop failed on:
    - `git worktree remove --force .../task-12 ... validation failed, cannot remove working tree: '.../.git' does not exist`
  - task 15 worktree creation loop failed on:
    - `git worktree add --force -b task/15 ... fatal: '.../task-15' already exists`
  - task 18 did not hard-fail, but it still drifted into broad context gathering (`planning/...`, `git.log`, `git.status`, `cli.execute ls -la`) before any deliverable write

- fix landed for the first-wave worktree seam:
  - [`internal/tools/native/task_worktree.go`](/Users/sam/dev/otter-camp/internal/tools/native/task_worktree.go)
    - recoverable worktree-remove detection now treats `.../.git does not exist` as stale-repairable corruption
    - recoverable worktree-add detection now treats `fatal: '.../path' already exists` as stale-directory debris and retries after deleting the leftover directory
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - mirrored the same add/remove recovery logic in the turn-engine worktree path so task-session execution and recovery use identical repair behavior
  - test coverage:
    - [`internal/tools/native/task_worktree_test.go`](/Users/sam/dev/otter-camp/internal/tools/native/task_worktree_test.go)
    - [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go)
    - added explicit regressions for:
      - remove error with `.git does not exist`
      - add error with `already exists`

- focused verification:
  - `go test ./internal/tools/native -run 'Test(IsRecoverableWorktree(Remove|Add)Error|EnsureTaskWorktreeFailsClosedWhenMainWorktreeOwnsTaskBranch)$' -count=1`
  - `go test ./internal/turn -run 'Test(TurnRecoverableWorktree(Remove|Add)Error|TaskWorkspaceRootFailsClosedWhenMainWorktreeOwnsTaskBranch)$' -count=1`

- current next step:
  - rebuild and restart the dedicated `codex-e2e-20260324` stack
  - verify the fresh binary clears task-12 / task-15 stale directories and re-enters first-wave execution without operator cheating
  - then continue the Plan 0325B operator run from the same project

## 2026-03-25 Plan 0325B live follow-up: stale-worktree fix proven, empty-repo seam exposed

- live proof after the first rebuild/restart:
  - task 15 no longer wedged on `... already exists`; it advanced to `review`
  - fresh review session: `d86e3327-30d2-4cc3-b505-2f0e8aecc4c2`
  - branch `task/15` now has a work commit:
    - `60cef3fd521edbf856a66d8559bae73f0e438fca`
  - task 12 resumed into fresh recovery/work session:
    - `92453e7e-7d73-4494-a835-60e46264c987`

- next real product seam exposed by the same fresh run:
  - task 12 still could not create a task worktree on a brand-new project repo whose `HEAD` points at `refs/heads/main` but has no commits yet
  - runtime failure:
    - `git worktree add --force -b task/12 ... fatal: invalid reference: HEAD`
    - git hint explicitly recommended `git worktree add --orphan -b task/12 ...`
  - because runtime did not handle that state, the live agent attempted a bad workaround with `cli.execute`:
    - `git init`
    - empty `Initial commit`
  - that manual repo repair is not acceptable product behavior, so the correct fix is in runtime worktree provisioning

- fix landed for empty-repo task worktrees:
  - [`internal/tools/native/task_worktree.go`](/Users/sam/dev/otter-camp/internal/tools/native/task_worktree.go)
    - when the base branch does not exist and `HEAD` is unborn / invalid, task worktree creation now uses `git worktree add --force --orphan -b task/N ...`
  - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go)
    - mirrored the same unborn-HEAD/orphan logic for turn-engine task worktree provisioning
  - tests added:
    - [`internal/tools/native/task_worktree_test.go`](/Users/sam/dev/otter-camp/internal/tools/native/task_worktree_test.go)
      - `TestEnsureTaskWorktreeCreatesOrphanBranchForUnbornRepo`
    - [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go)
      - `TestEnsureTurnTaskWorktreeCreatesOrphanBranchForUnbornRepo`

- focused verification:
  - `go test ./internal/tools/native -run 'Test(IsRecoverableWorktree(Remove|Add)Error|EnsureTaskWorktree(FailsClosedWhenMainWorktreeOwnsTaskBranch|CreatesOrphanBranchForUnbornRepo))$' -count=1`
  - `go test ./internal/turn -run 'Test(TurnRecoverableWorktree(Remove|Add)Error|EnsureTurnTaskWorktreeCreatesOrphanBranchForUnbornRepo|TaskWorkspaceRootFailsClosedWhenMainWorktreeOwnsTaskBranch)$' -count=1`

- next step:
  - commit/push this combined worktree runtime slice
  - rebuild/restart again
  - verify task 12 no longer needs `git init` / empty-commit self-repair and that the next recovery turn goes straight into deliverable writing
