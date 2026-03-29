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
