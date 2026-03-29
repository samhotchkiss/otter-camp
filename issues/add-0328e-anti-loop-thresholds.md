## Anti-Loop Thresholds And Replan Triggers

### Goal

Stop the system from paying for the same failure shape repeatedly.

### Core Principle

Repeated failure patterns should trigger replanning or escalation, not endless retries.

### Examples Of Loop Signals

- same failure class 3 or more times
- same artifact rewritten repeatedly
- same reread pattern across turns
- same PM rediscovery loop
- same validation guard firing repeatedly

### Why This Matters

A large amount of wasted runtime comes from the system retrying a lane that has already shown it needs a different plan.

### Direction

- Count repeated failures by normalized failure family.
- Set thresholds that trigger replan, resume, retry, or escalate decisions.
- Make those thresholds visible to supervisory lanes so they do not keep improvising the same move.

### Working Notes

- 2026-03-29 03:31 MDT - Immediate hardening leverage. Much of the Sam.blog work already introduced one-off guards for repeated failure families; this note should become the generalized threshold layer.
- Likely touchpoints: normalized validation / failure families in `internal/turn/engine.go`, continuation suppression and recovery logic in `internal/jobqueue/worker.go`, and retry metadata on turns/messages.
- Integration plan: promote repeated-family counters into explicit thresholds that trigger replan, escalation, or deterministic stop/hand-off behavior instead of more narrow ad hoc patches.
- Status: active follow-on candidate after bounded task contracts and supervisory stop conditions.
- 2026-03-29 06:00 MDT - Picked up the first worker-side anti-loop freshness slice. The concrete live symptom was stacks of pending synthetic recovery prompts on task `85` session `324f566b-751b-4d09-915f-f821abbfd37a` still carrying older `metadata.repo_version` values after deploy, which kept recovery draining old prompts instead of letting the new runtime guidance take over cleanly.
- 2026-03-29 06:00 MDT - Implemented a narrow cleanup rule in `internal/jobqueue/worker.go:PurgeStaleAgentTurnJobs(...)`: pending/claimed `agent_turn` dispatches tied to pending synthetic user prompts from an older `repo_version` are now dead-lettered, and the stale pending synthetic prompts themselves are marked failed, as long as no pending/in-progress turn still owns that message. Covered sources: `task_recovery_action`, `task_recovery_resume`, `task_review_action`, `project_continuation_resume`, `organization_continuation_resume`, and `project_execution_continuation`.
- 2026-03-29 06:00 MDT - Focused integration coverage is green in `internal/jobqueue/worker_integration_test.go` for both the purge path and the keep-live-turn exception. This is still a freshness cleanup, not the full generalized threshold layer, but it establishes the right supervisory pattern: once a retry family is superseded by a new runtime generation, stale prompts should be deterministically retired instead of retried again.
- 2026-03-29 06:05 MDT - Live proof on the restarted `repo_version=3551` runtime is good enough to keep this slice in the finished bucket. Task `85` session `324f566b-751b-4d09-915f-f821abbfd37a` now has `0` pending synthetic prompts from an older repo version without a live turn, and `59` stale recovery prompts on that session have already been marked failed with `superseded stale synthetic prompt after repo_version change`. One older `3549` prompt is still validly in flight because turn `60` currently owns it; that is the intended exception path covered by the new worker test.
- 2026-03-29 06:09 MDT - Picked up the next threshold seam from the fresh one-hour live scan. Task `85` is still the dominant hot session at roughly `1.53M` tokens / `70` `validation_loop_blocked` turns in the last hour, but the waste shape has changed: the runtime is now cheaply stopping direct-write-only discovery, yet every new recovery turn is still allowed one more bounded `file.write without content` correction before it loops again.
- 2026-03-29 06:09 MDT - Root cause was narrower than the stop policy itself. The surviving checkpoint reason on task `85` says `file.write ... was emitted without content; the next retry must provide the full file body`, but `RecoveryFileWriteFailureIsMissingContent(...)` only recognized the older `retried without content` phrasing. That meant `recoveryCheckpointShowsMissingContent(...)` returned false on each new resume turn, so the engine kept granting one more correction hop instead of escalating to the repeated-resume halt.
- 2026-03-29 06:09 MDT - Implemented the classifier fix in `internal/taskcheckpoint/recovery_file_write.go` and added direct regression coverage in both `internal/taskcheckpoint/recovery_file_write_test.go` and `internal/turn/engine_test.go`. This is the first real anti-loop threshold move on the direct-write recovery family itself: once the checkpoint already proves a prior empty `file.write`, the next empty `file.write` on a resumed turn should block immediately rather than spending another bounded correction.
- 2026-03-29 06:24 MDT - Picked up the next live seam on the same task-85 family after the classifier fix. Fresh DB/message inspection showed the resume prompt was already surfacing the persisted checkpoint (`file.write ... was emitted without content`), but the next resumed turn still emitted one more empty `file.write` before correction. Root cause: prompt construction uses `recoveryFileWriteCheckpointCandidate(...)`, which can recover checkpoint state from the synthetic resume message metadata, while the actual missing-content / missing-command / repeated-successful-write halt helpers were still consulting `currentRecoveryFileWriteCheckpoint(...)`, which only looks at task metadata.
- 2026-03-29 06:24 MDT - Implemented the alignment fix in `internal/turn/engine.go` by routing those halt/strengthening helpers through a new effective checkpoint helper backed by `recoveryFileWriteCheckpointCandidate(...)`. Added a focused regression in `internal/turn/engine_test.go` that simulates the exact synthetic-resume path: no task-level checkpoint, but the initial resume message already carries `recovery_checkpoint_target_path` plus the missing-content correction reason. Focused turn tests are green. This should finally make repeated-resume halts honor the same checkpoint source the prompt already shows, which is the right anti-loop behavior for hot synthetic recovery turns.
- 2026-03-29 06:33 MDT - Live proof landed immediately after deploy on `repo_version=3554`. Task `85` session `324f566b-751b-4d09-915f-f821abbfd37a` produced fresh turn `66`; message `492` still surfaced the prior missing-content checkpoint in `[Recovery resume state]`, but the turn no longer appended another `[Recovery correction: file.write ... without content]`. Instead it went directly to terminal halt message `495`, and the task is now `blocked` with checkpoint failure reason `repeated recovery file.write without content ... latest retry again omitted the full file body`. That is the anti-loop behavior this note was aiming for: once the synthetic resume prompt already proves the prior failure family, the next identical retry no longer gets a free extra correction hop.
- 2026-03-29 13:45 MDT - Picked up the next cross-turn loop family on SamBot task `155`. The duplicate shared-file child matcher was already live in kickoff preflight, but once the lane slipped into `validation_loop_blocked`, `enqueueTaskValidationBlockedContinuationPrompt(...)` was still synthesizing fresh recovery prompts for the same malformed child. That created a new anti-loop seam: the same non-executable lane could keep rearming itself across turns even though the runtime already knew it should never execute.
- 2026-03-29 13:45 MDT - Implemented a terminal continuation stop in `internal/turn/engine.go`. The validation-blocked continuation path now reuses the existing malformed-child classifiers (`no-decompose`, `procedural/support-only`, and duplicate shared-file children) and blocks the task instead of appending another synthetic recovery prompt. Focused turn coverage is green in `internal/turn/engine_test.go` for the duplicate shared-file case. Fresh production proof is pending the next post-deploy completion of task `155`'s currently open recovery turn.
- 2026-03-29 13:47 MDT - Fresh production proof landed on `repo_version=3568` immediately after restart. Task `155` is now `blocked`; fresh turn `61efff0a-b3ab-429e-b09f-90267e1adb90` ended `validation_loop_blocked`, and the session emitted the terminal system message saying the lane duplicates parent shared single-file deliverable `planning/sambot-example-conversations.md` owned by `OC-154`. Crucially, there is no new synthetic recovery prompt after that completion. This is the desired anti-loop behavior: malformed duplicate child lanes stop once and stay stopped.
