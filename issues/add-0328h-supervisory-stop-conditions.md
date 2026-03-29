## Explicit Stop Conditions For PM And Reviewer Lanes

### Goal

Keep supervisory lanes from turning into narration engines.

### Core Principle

PM and reviewer lanes should know when to stop thinking and take the next concrete action.

### Stop / Act Rules

- if the next action is clear, act
- if the same rediscovery family repeats, stop and hand off
- if missing information is truly blocking, ask one targeted question
- do not keep expanding context after the lane has already identified the right next move

### Why This Matters

Supervisory lanes can burn a lot of time on:

- rereads
- rediscovery
- narration
- repeated task inspection

They need sharper stopping rules than ordinary execution lanes.

### Direction

- Define PM/reviewer-specific early-stop conditions.
- Convert repeated rediscovery into deterministic transitions.
- Keep supervisory lanes focused on steering, not re-browsing.

### Working Notes

- 2026-03-29 13:45 MDT - Added a new supervisory stop on the task-side continuation path. A malformed child lane that has already ended `validation_loop_blocked` should not receive another synthetic `task_continuation_resume` / recovery-style prompt just because the latest turn stopped cheaply.
- 2026-03-29 13:45 MDT - `internal/turn/engine.go:enqueueTaskValidationBlockedContinuationPrompt(...)` now checks the same malformed-child families already enforced at kickoff preflight (`no-decompose`, procedural/support-only, and duplicate shared-file children). When matched, the engine appends the same terminal system explanation, marks the task blocked, and suppresses further continuation prompt creation for that lane. This closes the task-155 duplicate-full-file replay family without relying on a brand-new kickoff turn.
- 2026-03-29 13:47 MDT - Live proof is in. SamBot task `155` completed fresh turn `61efff0a-b3ab-429e-b09f-90267e1adb90`, appended the duplicate shared-file halt message, and transitioned to `blocked` with no follow-on synthetic recovery prompt. That is the supervisory stop we wanted: the runtime ends the malformed lane instead of supervising one more retry.
- 2026-03-29 14:08 MDT - Added the next supervisory stop on recovery validation itself. Shared-file child lanes that are only allowed to perform bounded `file.edit` work should not keep re-reading the inherited parent file after `not_found` proves the integration surface is missing.
- 2026-03-29 14:08 MDT - The turn engine now treats `file.read not_found` on an inherited shared parent deliverable during recovery as terminal for that child lane: it blocks the task, records a checkpoint, cancels the stale resume dispatch, and redirects work back to the parent or a write-owning replacement lane. This is aimed directly at the live task `164-166` family on `planning/sambot-example-conversations.md`.
- 2026-03-29 14:11 MDT - Live proof is in on task `164`. After deploy, the first fresh recovery turn that re-read the missing inherited file stopped immediately with the new shared-deliverable missing-file message and the task transitioned to `blocked`. The lane no longer spins on child-level recovery when the parent integration surface does not exist.

- 2026-03-29 13:24 MDT - Picked up the next supervisory/task-lane stop seam after the bounded-size PM split created SamBot child tasks `155` / `156` for shared deliverable `planning/sambot-example-conversations.md`.
- 2026-03-29 13:24 MDT - Fresh live proof on child session `2c91777a-0ee4-44fd-b90c-70703796b95e` (task `156`) showed a narrower malformed-child family than the older `Reference planning/...` junk:
  - the child task title/description was `Use Sam's voice and opinions as established in the SamBot feature spec at planning/sambot-feature-spec.md and the scraped blog content in content/posts/`
  - the lane immediately tried to reread the shared parent sources, then switched into whole-file ownership of `planning/sambot-example-conversations.md`
  - the existing shared-deliverable guard blocked the unsafe `file.write`, but only after the child had already spent a full turn proving it was support-only instruction text rather than bounded execution work
- 2026-03-29 13:24 MDT - Local hardening is in [`internal/taskdecomp/decomposition.go`](/Users/sam/dev/otter-camp/internal/taskdecomp/decomposition.go):
  - `TaskLooksProceduralInstructionArtifact(...)` now treats `Use ... as established in planning/... and content/...` style support-only children as malformed procedural artifacts when they cite source artifacts but still name no concrete output action
  - this feeds the existing malformed-child snapshot filters and kickoff preflight, so these support-only children should now block before model call instead of reaching the shared-deliverable guard later
- 2026-03-29 13:24 MDT - Focused verification is green:
  - `GOFLAGS='' go test ./internal/taskdecomp -run 'Test(TaskLooksProceduralInstructionArtifact|ExtractDeliverablesIgnoresReferenceOnlyInstructionLines)$' -count=1`
  - `GOFLAGS='' go test -tags=integration ./internal/turn -run 'TestTurnEngineIntegrationMalformed(Procedural|Support)ChildKickoffPreflightBlocksBeforeModelCall$' -count=1`
- 2026-03-29 13:56 MDT - The next stop family is the adjacent “duplicate full-file child” case. Fresh live state after the support-child fix:
  - task `156` now blocks correctly in kickoff preflight
  - sibling task `155` is still an active child lane even though its title/description simply re-claim the exact parent single-file deliverable `planning/sambot-example-conversations.md`
  - that leaves the PM lane waiting behind a child that is not actually narrower than the parent; it just churns into the later shared-deliverable write guard
- 2026-03-29 13:56 MDT - Local hardening is in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) and [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go):
  - duplicate child tasks that still say `produce/write the file <same parent single-file path>` are now treated as malformed child artifacts
  - async kickoff preflight blocks those lanes before model call
  - PM/worker continuation snapshots now ignore those duplicate full-file children the same way they already ignore procedural/reference-only junk
- 2026-03-29 13:56 MDT - Focused verification is green:
  - `GOFLAGS='' go test ./internal/turn -run 'TestProjectExecutionContinuationSnapshotIgnoresMalformed(DuplicateSharedFile|Procedural|ReferenceOnly)Children$' -count=1`
  - `GOFLAGS='' go test -tags=integration ./internal/turn -run 'TestTurnEngineIntegrationMalformed(DuplicateSharedFile|Support|Procedural)ChildKickoffPreflightBlocksBeforeModelCall$' -count=1`
  - `GOFLAGS='' go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerProjectExecutionContinuationSnapshotIgnoresMalformed(DuplicateSharedFile|Procedural|ReferenceOnly)Children$' -count=1`

- 2026-03-29 11:20 MDT - Fresh live PM proof on session `5383ab5a-fecd-4a22-a403-d1e5620b96b8`: continuation prompt `8369` already named only terminally blocked leaf tasks owning `planning/sambot-feature-spec.md` and explicitly said to leave `resume_policy=terminal_keep_blocked` lanes blocked, but the same PM turn still issued `file.read planning/sambot-feature-spec.md` (`8372`) after the broader `task.list` rediscovery guard had already fired at `8371`.
- 2026-03-29 11:20 MDT - Picked up the next stop-condition slice in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go): project continuations should treat deliverable reads for terminally blocked leaf-task paths as supervisory stop candidates, not as legitimate PM inspection.
- 2026-03-29 11:20 MDT - Initial implementation plan:
  - parse `work_status=blocked ... deliverable_path=... resume_policy=terminal_keep_blocked` paths from the continuation prompt
  - block `file.read` on those exact deliverables from the PM lane
  - steer the PM lane toward replacement / closeout / parent-update actions instead of rereading the blocked leaf deliverable
- 2026-03-29 11:26 MDT - Live canary exposed the real dispatch gap: the PM turn entered this path with tool alias `file_read`, while the first implementation only switched on dotted names like `file.read`. The stop family now needs to normalize both dotted and underscored tool names anywhere it guards PM rediscovery.
- 2026-03-29 11:44 MDT - The next PM rediscovery seam is narrower and same-turn only. Fresh live PM turns `8653-8661` on session `5383ab5a-fecd-4a22-a403-d1e5620b96b8` already stop after blocked named-`task.get` rereads, but the engine still appends every blocked result in a pure rediscovery batch before it emits the stop. That keeps redundant blocked `task.get` evidence in the transcript even when the stop family has already fired.
- 2026-03-29 11:44 MDT - Picked up the follow-on in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go): pure blocked project-continuation rediscovery batches should keep only the minimum blocked evidence needed for the stop message instead of appending every blocked named-task reread in the same turn.
- 2026-03-29 11:44 MDT - Local implementation is in place and focused tests are green:
  - added `trimBlockedProjectContinuationRediscoveryResults(...)` and applied it before blocked PM rediscovery batches are appended to the session
  - added `TestTrimBlockedProjectContinuationRediscoveryResultsKeepsMinimumEvidence`
  - added `TestDispatchToolsTrimsPureBlockedProjectContinuationRediscoveryBatch`
  - verified with `GOFLAGS='' go test ./internal/turn -run 'Test(ShouldStopAfterBlockedProjectContinuationRediscovery|TrimBlockedProjectContinuationRediscoveryResultsKeepsMinimumEvidence|DispatchToolsStopsAfterPureBlockedProjectContinuationRediscoveryBatch|DispatchToolsTrimsPureBlockedProjectContinuationRediscoveryBatch|ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksTerminalBlockedLeafDeliverableRead)$' -count=1`
- 2026-03-29 05:46 MDT - The next explicit stop refinement is the task-recovery analogue of the same supervisory problem: once the runtime has already forced “write the file body or report one blocker sentence,” the turn still should not get a second blocked rediscovery hop in the same batch.
- 2026-03-29 05:46 MDT - Fresh live proof before the follow-on stop: task `85` session `324f566b-751b-4d09-915f-f821abbfd37a` hit turns `43-44` with the new direct-write guard already active at messages `336` / `296` (`recovery_direct_write_required` on `planning/prd-spec/oc-34-prd.md` or the target file itself), but the same assistant batch still also spent one more blocked `file.list templates` hop (`337` / `297`) before the turn finally ended on the older repeated-focus stop (`338` / `298`).
- 2026-03-29 05:46 MDT - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) now treats direct-write-only recovery as a full blocked-batch stop family instead of only a per-tool guard:
  - `shouldBlockTaskRecoveryReadScopeTool(...)` now covers `file.list` and `file.search` in direct-write-only mode, not just `file.read` / read-only `cli.execute`
  - `dispatchTools(...)` now stops immediately when a pure blocked recovery batch is entirely `recovery_direct_write_required`, appending one direct-write-only turn-stop message instead of letting the turn continue after the blocked tool results
- 2026-03-29 05:46 MDT - Focused verification is green:
  - `GOFLAGS='' go test ./internal/turn -run 'Test(ShouldBlockTaskRecoveryReadScopeTool|RecoveryDirectWriteOnlyState|ShouldStopAfterBlockedTaskRecoveryDirectWriteOnly|DispatchToolsStopsAfterPureBlockedTaskRecoveryDirectWriteBatch|ClassifyToolValidationFailureRecognizesRecovery(DirectWriteRequired|SourceFetchWriteRequired)|HandleRecoveryFileWriteWithoutContent(EnablesDirectWriteOnlyState|StopsAfterRepeatedResumeFailure))$' -count=1`
- 2026-03-29 05:46 MDT - This slice is locally test-green and ready to deploy; the direct post-deploy proof target is that task `85` should stop immediately after the first blocked direct-write-only recovery batch instead of paying the extra `file.list templates` tool result in the same turn.
- 2026-03-29 05:18 MDT - The next supervisory stop family is exact active-deliverable rereads from the PM lane. Fresh pre-fix proof on session `5383ab5a-fecd-4a22-a403-d1e5620b96b8`: continuation prompt `6605` already named task `88` as `work_status=in_progress deliverable_path=planning/sambot-feature-spec.md` and task `85` as `work_status=in_progress deliverable_path=templates/template-08-replace.html`, but the follow-on PM turns still opened with `file.read not_found` on those exact task-owned paths (`6608-6609`) before falling back into the missing-dependency / active-replacement stop pair.
- 2026-03-29 05:18 MDT - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) now treats `file.read` of any `work_status=in_progress|review` `deliverable_path=...` already named in the continuation prompt as the task-lane-boundary stop family, not as acceptable PM inspection. The new helper `projectContinuationPromptLiveDeliverablePaths(...)` feeds `shouldBlockProjectContinuationSnapshotRediscoveryTool(...)`, and the focused regression `TestShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksActiveDeliverableRead` is green.
- 2026-03-29 05:18 MDT - Deployed on `repo_version=3547`, rebuilt/restarted cleanly, and [`./bin/ottercamp health --output json`](/Users/sam/dev/otter-camp/bin/ottercamp) is `status=ok`. Direct post-deploy production proof is still pending because the PM session drained idle at turn `619` after the pre-fix stop chain and has not emitted a fresh continuation turn on the new binary yet.
- 2026-03-29 05:09 MDT - The supervisory stop family from `04:58 MDT` needed one more refinement. A bounded-size PM retry can still hit a missing-deliverable stop on the exact output path before any child tasks exist. That missing file is not a generic prerequisite miss; it is evidence that the broad parent still has not been decomposed/executed.
- 2026-03-29 05:09 MDT - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) now preserves the bounded-size supervisory context across that stop: when the exact deliverable for the currently focused broad draft is missing, the follow-on continuation keeps the “split this parent now” instruction instead of steering back into “queue the smallest replacement task.”
- 2026-03-29 05:09 MDT - Pre-deploy live trace proving the need: `6518 -> 6524/6525 -> 6526/6529` on session `5383ab5a-fecd-4a22-a403-d1e5620b96b8`, where the PM lane already knew task `84` was too broad but still bounced through one generic missing-dependency retry that tried to queue it again.
- 2026-03-29 04:58 MDT - The next explicit supervisory stop family is now live for PM bounded-size failures. [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) now treats `project_execution_continuation` turns that end on bounded-size policy as a real supervisory stop, even if the same turn successfully created the draft task that needs decomposition.
- 2026-03-29 04:58 MDT - Fresh production proof on `repo_version=3545`: Sam.blog PM session `5383ab5a-fecd-4a22-a403-d1e5620b96b8` ended turn `65888993-5fdb-45bc-8681-244bd949908f` with bounded-size stop `6517`, and the runtime immediately appended continuation message `6518` with direct split guidance for task `84` (`Your last continuation turn proved that task 84 ... is still too broad ... split task 84 ... use only task.list(parent_task_id=...)`). The follow-on PM turn `9b047ab2-9d55-4118-a0c6-c9f4bf704bb5` then launched automatically instead of letting the session drain idle.
- 2026-03-29 04:58 MDT - The next leverage is smaller now: that focused bounded-size retry still opened with `file.list templates` and hit the existing artifact-root rediscovery guard at `6522`. So the remaining supervisory improvement is not “retry after bounded-size stop” anymore; it is “convert that focused retry directly into child-task creation or the narrowest child-task inspection without even one artifact-root probe.”
- 2026-03-29 03:31 MDT - Immediate leverage and partially underway. A large share of the Sam.blog hardening work has already been PM/reviewer stop conditions, but the rules are still scattered across family-specific guards.
- Likely touchpoints: PM/reviewer prompt builders and `shouldStop...` guard families in `internal/turn/engine.go`, plus continuation suppression in `internal/jobqueue/worker.go`.
- Integration plan: consolidate supervisory stop families into explicit lane-level rules and metrics so PM/reviewer behavior is intentionally bounded rather than only patched case by case.
- Status: first implementation candidate alongside bounded task contracts.
- 2026-03-29 04:00 MDT - First explicit PM rediscovery-stop follow-on is now live. [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) appends one focused supervisory retry after a `projectContinuationRediscoveryGuardPrefix` stop instead of letting the PM session drain idle immediately.
- 2026-03-29 04:00 MDT - Fresh production proof on `repo_version=3538`: Sam.blog PM session `5383ab5a-fecd-4a22-a403-d1e5620b96b8` first hit the old broad-reread stop on turn `73f6578c-f412-41f0-b4e0-e2c73d8262e0`, then the runtime created focused continuation message `e61befed-bd69-4dd8-a2e5-c74ef5a8f7b1` and ran turn `3877ecfa-9b43-44c7-866a-5eb50fd80895` with the new instruction block:
  - `Your last continuation turn spent its entire tool batch on broad rediscovery...`
  - `Current blocked task: task 71 ...`
  - `Your next assistant action must create, queue, or update the smallest bounded recovery step...`
- 2026-03-29 04:00 MDT - The follow-on turn still ended `validation_loop_blocked` on `task_lane_owned_by_project_task_session`, so the stop-family improvement is now: `rediscovery-only stop -> one bounded supervisory retry`, not full autonomous settlement yet. Next leverage is teaching the focused retry to convert directly into a handoff or blocker report without even one blocked artifact-root probe.
- 2026-03-29 04:25 MDT - That next leverage is now live. [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) short-circuits the focused supervisory retry when the named blocked task already owns execution through `current_flow_node_id` or an active async `project_task` session.
- 2026-03-29 04:25 MDT - Fresh production proof on `repo_version=3540`: session `5383ab5a-fecd-4a22-a403-d1e5620b96b8` ran turn `17e32c32-4665-4dcc-badf-293848a3eaa3`, hit the rediscovery stop, and then received only the task-lane-boundary system correction for task `71` instead of a second focused PM continuation message. The PM lane stopped cleanly without spending another retry turn on work already owned by the task lane.
- 2026-03-29 04:43 MDT - The first `3540` ownership shortcut was too broad for live PM recovery. I verified in SQL that several blocked tasks still carry `current_flow_node_id` even though their async `project_task` sessions are closed and the matching `flow_node_execution` rows are already terminal. Treating `current_flow_node_id` alone as “live lane ownership” can strand PM recovery behind dead task lanes.
- 2026-03-29 04:43 MDT - The next live PM seam was in the continuation-summary anchor, not the rediscovery guard itself. On session `5383ab5a-fecd-4a22-a403-d1e5620b96b8`, fresh `project_execution_continuation` message `6159` correctly focused blocked tasks `71` and `81`, but the max-tool-calls summary `6176` still regressed to the stale `OC-45 through OC-48 ... content/technonymous-index.json` storyline. That means supervisory stop conditions also need a correct “what request am I continuing?” anchor, not just better stop rules.
- 2026-03-29 04:46 MDT - The `3543` binary is now live. The first fresh PM turn after restart ran from new continuation message `6476`, created replacement task `82`, and then stopped on bounded-size policy instead of bouncing on stale task-lane ownership or PM-side `flow_owned_status_blocked`. The exact max-tool-calls continuation-summary branch did not re-fire in that first post-restart turn, so direct live proof for the summary-anchor fix still depends on the next natural long PM continuation.
- 2026-03-29 12:00 MDT - Picked up the next supervisory stop family in the child-task recovery lane: decomposed SamBot section tasks that inherit the parent single-file spec are still vulnerable to whole-file `file.write` replay during recovery, which can overwrite the shared document instead of applying a bounded section update.
- 2026-03-29 12:00 MDT - Focused local implementation is in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go):
  - async task execution now blocks `file.write` when a decomposed child only inherits a parent shared single-file explicit deliverable like `planning/sambot-feature-spec.md`
  - pure blocked batches in that family now stop immediately with a shared-deliverable guard message instead of continuing the turn
  - recovery handlers that would otherwise populate/synthesize persisted-draft `file.write` calls now halt early and checkpoint the failure for the same inherited shared file path
- 2026-03-29 12:00 MDT - Focused verification is green:
  - `GOFLAGS='' go test ./internal/turn -run 'Test(ShouldStopAfterBlockedTaskExecution(InheritedSharedWrite|SiblingMutation)|ShouldBlockTaskExecutionInheritedSharedDeliverableWriteTool|HandleRecoveryFileWriteWithoutContentStopsForInheritedSharedParentDeliverable)$' -count=1`
- 2026-03-29 12:00 MDT - Direct post-deploy canary remains SamBot child lanes under parent task `139`, especially task `145` / session `c6bfa4ef-6f7c-4186-8014-3a620f065f96`: the expected new behavior is that recovery stops with edit-oriented guidance instead of replaying another whole-file write into `planning/sambot-feature-spec.md`.
- 2026-03-29 12:05 MDT - Fresh live canary on task `142` showed the first version was still too soft. Session `5c403c27-3662-4843-a19f-d957258f9ec5` hit the new guard repeatedly at `45/46`, `50/51`, and `55/56`: the unsafe `file.write` was blocked each time, but recovery kept auto-rearming because the stop did not yet cancel its own recovery dispatch.
- 2026-03-29 12:05 MDT - Tightened the same slice in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go): inherited shared-deliverable recovery stops now set `recoveryBlockReason` and cancel the recovery dispatch metadata, so the child lane is treated like a real blocked recovery instead of a soft validation-only retry loop.
- 2026-03-29 12:09 MDT - The first `recoveryBlockReason` hardening still missed the actual live path. The repeated task-142 retries were being stopped by the generic blocked-tool batch branch (`Task shared-deliverable guard ...`) rather than the narrower `haltRecoveryInheritedSharedDeliverableWrite(...)` helper, so the cancelled dispatch metadata never got applied on those turns.
- 2026-03-29 12:09 MDT - Follow-on hardening is now in [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go): when a pure blocked inherited-shared-file `file.write` batch stops a recovery turn, `dispatchTools(...)` now also sets the same shared-parent `recoveryBlockReason` and cancels the triggering recovery dispatch metadata before ending the turn.
- 2026-03-29 12:34 MDT - The next supervisory-stop miss is the PM wake path after the last live child lane goes `blocked`.
  - fresh live proof on session `5383ab5a-fecd-4a22-a403-d1e5620b96b8`: after task `142` blocked and its task session closed, the project lane still drained idle even though parent draft task `139` had flipped from `child work exists` into `fresh replacement child work`
  - root cause split across both runtime layers:
    - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) only tried to wake the PM lane from terminal task turns/status changes when the triggering task was `done` or `cancelled`
    - both engine and worker settlement checks treated `remainingDraftTasks == 0` as “project settled” even when the snapshot still had `ReplacementDraftLine` / `FocusTaskLine` proving replacement-parent work remained
- 2026-03-29 12:34 MDT - Local fix is now in place and focused tests are green.
  - changed [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go):
    - blocked task tails can now enter the PM continuation wake path
    - blocked-tail wakes anchor the continuation prompt to the latest actual completed task instead of falsely calling the blocked task “latest completed”
    - project-settled checks now keep PM work alive when the snapshot still shows replacement-draft or focused-parent work, even if `remainingDraftTasks == 0`
  - changed [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go):
    - `ensureProjectContinuationMessageDecision(...)` now uses the same “replacement-draft parents still count as remaining work” rule, so idle-project recovery no longer suppresses this state as settled
  - changed tests:
    - [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go)
      - added `TestMaybeContinueProjectExecutionAfterTaskCompletionUsesLatestCompletedTaskForBlockedTail`
      - added `TestHandleTaskStatusChangedEventBlockedWakesProjectContinuation`
    - [`internal/jobqueue/worker_integration_test.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker_integration_test.go)
      - added `TestJobWorkerEnsureProjectContinuationMessageKeepsReplacementDraftParentsActionable`
  - verified with:
    - `GOFLAGS='' go test ./internal/turn -run 'Test(MaybeContinueProjectExecutionAfterTaskCompletion(IgnoresBlockedTasksForWakeup|UsesLatestCompletedTaskForBlockedTail)|HandleTaskStatusChangedEventBlockedWakesProjectContinuation)$' -count=1`
    - `GOFLAGS='' go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerEnsureProjectContinuationMessageKeepsReplacementDraftParentsActionable$' -count=1`
  - direct post-deploy proof target:
    - Sam.blog PM session `5383ab5a-fecd-4a22-a403-d1e5620b96b8` should wake again after task `142`’s blocked tail and surface the `Draft parent tasks need fresh replacement child work` continuation instead of staying idle with no pending continuation message
- 2026-03-29 12:39 MDT - The blocked-tail PM wake fix is now live-proven, and the remaining seam is decomposition quality rather than continuation recovery.
  - fresh live proof on Sam.blog PM session `5383ab5a-fecd-4a22-a403-d1e5620b96b8` after restart:
    - continuation `cde7704d-640d-44eb-9735-9639a90957b0` woke immediately instead of leaving the project idle
    - the PM lane created replacement task `146` for the blocked architecture slice
    - the next PM continuation then correctly hit the bounded-size stop, decomposed `146`, and queued child tasks `147` / `148`
  - the new problem exposed by that live run is narrower:
    - child `147` / `148` are malformed decomposition artifacts, not real bounded deliverables
    - `147` is procedural summary junk
    - `148` is a reference-only instruction child (`Reference planning/sambot-feature-spec.md ...`) that should never have been created as executable work
  - picked up the follow-on hardening in [`internal/taskdecomp/decomposition.go`](/Users/sam/dev/otter-camp/internal/taskdecomp/decomposition.go):
    - `reference ... planning/...` / `refer to ...` instruction lines are now classified as procedural instruction artifacts
    - that lets the existing malformed-child machinery ignore them in PM snapshots and block them on the task-lane side the same way it already does for `Use cli_execute ...` or browser-only junk children
  - focused verification is green:
    - `GOFLAGS='' go test ./internal/taskdecomp -run 'Test(TaskLooksProceduralInstructionArtifact|ExtractDeliverablesIgnoresReferenceOnlyInstructionLines)$' -count=1`
    - `GOFLAGS='' go test ./internal/turn -run 'TestProjectExecutionContinuationSnapshotIgnoresMalformed(Procedural|ReferenceOnly)Children$' -count=1`
    - `GOFLAGS='' go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerProjectExecutionContinuationSnapshotIgnoresMalformed(Procedural|ReferenceOnly)Children$' -count=1`
  - deploy / proof target:
    - after redeploy, malformed reference-only children like Sam.blog task `148` should be treated as malformed child artifacts rather than as active bounded work, restoring parent task `146` as the PM-visible architecture work unit
- 2026-03-29 14:11 MDT - Shared-deliverable recovery stops needed to persist terminal validation-loop metadata, not just mark the task blocked.
  - fresh live evidence from Sam.blog task `165`:
    - turns `096eb2b5-332c-455b-9eb2-b79bfa180962` and `4eddb044-98b3-41a1-8b26-b0c38abbce77` both ended with `[Recovery shared-deliverable guard: inherited parent file \`planning/sambot-example-conversations.md\` is still missing ...]`
    - despite that terminal stop, a new async task session `bf26e68d-a8b0-4330-960a-d8d5a9e0e2d8` reopened immediately afterward and resumed the same child lane again
  - root cause:
    - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) marked the task `blocked`, but the custom `missingInheritedSharedDeliverableStop` branch kept `agent_turn_validation_guard.blocked=false`
    - [`internal/jobqueue/worker.go`](/Users/sam/dev/otter-camp/internal/jobqueue/worker.go) intentionally skips active blocked task sessions only when that validation guard bit is set, so the stale execution remained eligible for resume
  - local fix:
    - force the validation guard state to `blocked=true` with count at the standard threshold whenever the inherited shared-deliverable recovery stop fires
    - widened [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go) so the existing missing-file regression now asserts both `WorkStatus=blocked` and terminal validation-guard metadata
  - verified with:
    - `GOFLAGS='' go test ./internal/turn -run 'Test(HandleToolValidationResultsBlocksRecoveryMissingInheritedSharedDeliverable|EnqueueTaskValidationBlockedContinuationPromptBlocksMalformedDuplicateSharedFileChild|BuildTaskReviewActionPromptIncludesAcceptanceCriteria)$' -count=1`
  - post-deploy live proof:
    - task `165` is no longer relaunching; it settled out of the active lane and then the PM continuation cancelled stale tasks `164-167` and replacement parent `154`
    - current project state after that cleanup: `158 done`, `154 cancelled`, `164-167 cancelled`, with PM continuation moving on to the remaining architecture parent `157`
- 2026-03-29 14:19 MDT - Follow-on PM rediscovery hardening is locally deployed and test-green: project continuations now block `file.list(path=planning)` when the continuation prompt already names the relevant planning deliverable path(s).
  - motivation:
    - fresh PM turn `c788dd5c-2726-4896-a6df-784a8589c422` spent its remaining tool budget on blocked named-`task.get`s plus a `planning/` browse after the continuation already named `planning/sambot-architecture.md` and `planning/sambot-feature-spec.md`
    - that meant the existing broad-rediscovery guard fired only after extra planning-root rereads instead of converting the batch into an earlier pure blocked rediscovery stop
  - local change:
    - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) now treats `file.list(path=planning)` the same way it already treats companion planning `file.read`s when the continuation prompt already names the planning targets
    - widened [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go) with `TestShouldBlockProjectContinuationSnapshotRediscoveryToolBlocksPlanningRootBrowseWhenPlanningTargetsNamed`
  - verified with:
    - `GOFLAGS='' go test ./internal/turn -run 'Test(ShouldBlockProjectContinuationSnapshotRediscoveryToolBlocks(CompanionPlanningArtifactRead|PlanningRootBrowseWhenPlanningTargetsNamed)|HandleToolValidationResultsBlocksRecoveryMissingInheritedSharedDeliverable|DispatchToolsStopsAfterSecondSingleBlockedProjectContinuationRediscoveryInSameTurn)$' -count=1`
  - deploy / proof status:
    - deployed on the current local runtime after rebuild/restart
    - direct live proof is still pending because the next PM canary turn `fa05f520-df52-4b32-bffa-c954b7b9bb60` is currently stuck in a long-running in-flight provider invocation before it has emitted any tool calls on the new binary
