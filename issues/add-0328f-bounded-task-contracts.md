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
