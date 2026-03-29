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
