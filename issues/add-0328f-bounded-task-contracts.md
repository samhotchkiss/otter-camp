## Bounded Task Contract Preflight

### Goal

Prevent malformed or overly broad tasks from reaching execution.

### Core Principle

Every executable task should have a bounded contract before it is allowed to run.

### Required Elements

- clear deliverable
- bounded scope
- acceptance expectation
- no procedural "use tool X" phrasing as the core task definition

### Why This Matters

Many expensive failures come from tasks that were never truly executable:

- too broad
- too procedural
- unclear deliverable
- missing definition of done

### Direction

- Preflight new tasks before queueing them.
- Reject or rewrite tasks that are procedural or ambiguous.
- Decompose broad tasks before execution begins.

### Working Notes

- 2026-03-29 05:09 MDT - The bounded-task contract now extends one step beyond queue-time rejection. When a PM bounded-size retry already says “split task 84” and the lane then reads the missing deliverable path (`templates/template-08-replace.html`), the runtime should preserve the bounded/decompose contract instead of bouncing into the generic missing-prerequisite queue path for the same broad parent.
- 2026-03-29 05:09 MDT - Fresh pre-deploy live root cause on session `5383ab5a-fecd-4a22-a403-d1e5620b96b8`: bounded-size retry `6518` correctly targeted task `84`, but after `file.read not_found` on the exact deliverable path (`6524-6525`), the next PM retry `6526-6529` tried to queue task `84` again and immediately re-hit bounded-size. That is a contract leak: the task is already known to be too broad, so “missing deliverable” should mean “decompose this parent,” not “queue the same parent again.”
- 2026-03-29 05:09 MDT - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) now recognizes this exact bounded-size + missing-deliverable family and issues another split-focused `project_execution_continuation` instead of taking the generic missing-dependency queue path. Focused regression coverage is in [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go). Fresh production proof is pending after the `repo_version=3546` restart because the next PM continuation message (`6564`) is still waiting in `job_queue`.
- 2026-03-29 03:31 MDT - Highest near-term leverage from the 0328 additions. Many Sam.blog failures came from procedural or malformed tasks (`use browser...`, decomposed steps, broad scrape parents) that should never have reached execution.
- Existing groundwork already in the runtime: bounded-size checks, no-decompose guards, procedural-section filtering, and PM/task preflight stops.
- Likely touchpoints: task creation / decomposition tools, executable-task queue activation, and PM mutation validation before a task becomes runnable.
- Integration plan: add one bounded-contract preflight before queueing executable tasks; reject or rewrite tasks missing deliverable, scope, or acceptance expectation, or whose core definition is primarily procedural.
- Status: first implementation candidate from the 0328 backlog.
- 2026-03-29 03:46 MDT - First implementation slice landed on `repo_version=3536`: pure procedural instruction artifacts are now blocked from becoming executable work. The canonical queue path rejects them with `ErrExecutableTaskContractRequired`, and `task.create` / `task.update` surface a rewrite hint that tells the lane to restate the work as a bounded deliverable-focused task instead of tool steps.
- Verified with focused coverage in `internal/taskdecomp`, `internal/tools/native`, `internal/task` integration, and `internal/server` integration. Remaining work for this note is broader than this first slice: explicit acceptance expectations and clearer deliverable-contract checks beyond the pure procedural/task-shape family.
- 2026-03-29 14:08 MDT - Picked up the next bounded-contract seam from the live SamBot example-conversation child lanes. Tasks `164-167` inherit the shared parent file `planning/sambot-example-conversations.md` and are allowed to make bounded section edits only. When recovery turns on those child lanes hit `file.read not_found` for the inherited parent file, that is not a retryable discovery miss: it proves the shared integration surface does not exist yet, so the child lane still lacks a valid executable contract.
- 2026-03-29 14:08 MDT - `internal/turn/engine.go` now blocks that shape directly in `handleToolValidationResults(...)`. If a recovery turn on a decomposed child reads its inherited shared parent file and gets `not_found`, the runtime persists a recovery checkpoint, marks the task blocked, cancels the stale recovery resume dispatch, and tells the operator to resume the parent task or a write-owning replacement lane first. Focused turn coverage is green in `internal/turn/engine_test.go`.
- 2026-03-29 14:11 MDT - Fresh production proof landed immediately after deploy. SamBot task `164` completed turn `e77500da-a692-478d-ab79-bfc546698997`, emitted the new stop message `inherited parent file planning/sambot-example-conversations.md is still missing`, and moved to `blocked` instead of reopening another recovery continuation. That is the bounded-contract behavior we wanted: the child lane no longer pretends it can start bounded section work before the shared parent document exists.
- 2026-03-29 15:09 MDT - The bounded-contract seam is wider than queue-time task shape. SamBot task `171` proved a task can still violate the contract at execution time by copying the task brief itself into the target deliverable (`planning/sambot-mvp-spec.md`) and then cycling through work/review until max visits exhaust.
- 2026-03-29 15:09 MDT - Added a local execution-time guard for that family:
  - native `file.write` now rejects task-brief echo scaffolds as `non_substantive_content`
  - native `file.read` now treats an in-progress deliverable containing the same scaffold as `placeholder_deliverable`
  - recovery draft reuse now rejects it as `copied the task brief/instruction scaffold`
- 2026-03-29 15:09 MDT - This belongs under bounded task contracts because the file body is not a deliverable at all; it is the task contract restated verbatim. A runnable task still needs a second guard that the produced artifact is no longer just the instruction scaffold.
- 2026-03-29 15:21 MDT - Another bounded-contract leak showed up immediately after SamBot child tasks `173-176` launched: explicit output-file contracts encoded as `sambot/widget.html (or sambot/index.html)` or `(sambot/widget.html or similar)` were not parsed as deliverable targets, so recovery kept drifting back to the parent spec or unrelated root files.
- 2026-03-29 15:21 MDT - I patched the explicit deliverable parsers in the native layer, turn engine, and worker hints so path-first titles and `(path or similar)` hints now count as real deliverable contracts. This keeps execution lanes anchored on the actual `sambot/...` file they own instead of inheriting stale parent-spec recovery targets. Focused native/turn/jobqueue tests are green; next step is live proof after redeploy.
- 2026-03-29 15:23 MDT - Live proof is in on the backend child lane: task `176` now carries `sambot/api.js` as the recovery target on the new binary instead of inheriting `planning/sambot-feature-spec.md`. The widget-side children (`173` / `175`) are still finishing older in-flight turns, so I still want one fresh retry window there before calling this contract family fully closed.
- 2026-03-29 15:29 MDT - There was one more contract leak underneath that same SamBot family: broad non-markdown task `174` (`Backend API wiring`) could still treat `planning/sambot-feature-spec.md` and even sibling frontend file `sambot/widget.html` as valid deliverable targets because the generic matcher was too permissive once no explicit output path was present.
- 2026-03-29 15:29 MDT - I tightened that matcher in the native layer and turn engine so non-markdown tasks now reject dependency/context artifacts and obvious frontend/backend role mismatches. That keeps broad backend tasks from drifting back onto planning docs or frontend widget files while the explicit `sambot/api.js` child exists. Tests are green; the remaining check is the next fresh task `174` review turn after the older in-flight widget-target review drains.
