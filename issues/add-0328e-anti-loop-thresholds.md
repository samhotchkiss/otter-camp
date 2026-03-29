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
