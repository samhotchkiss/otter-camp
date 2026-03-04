# Task Runner Instructions

Shared queue root: `issues/`

Lane order:
1. `01-ready`
2. `02-in-progress`
3. `03-needs-review`
4. `04-in-review`
5. `05-completed`

Execution rules:
- On a fresh run, start with `001-project-scaffold.md`.
- Pull the next actionable file from `01-ready` (respect `Depends on` in the task header).
- If one or more files in `01-ready` include a top-level `## Reviewer Required Changes` block, prioritize those files before net-new ready tasks (lowest task number first).
- Move it to `02-in-progress` before making any code changes.
- Read `build/CONTEXT.md` first, then read the task file completely.
- In task 001, initialize `go.mod` with module path: `github.com/samhotchkiss/otter-camp`.
- Implement exactly the scoped requirements in the task file.
- Run required tests from the task (`go test ./...`, `go test ./... -tags integration`, `go test ./... -tags e2e` as applicable).
- If integration tests require `testdb.New(t)` and it does not exist, complete task 002 first (do not stub around it).
- For CLI-related tasks, build and smoke-test the binary (`go build ./cmd/ottercamp`, then run relevant `ottercamp ...` commands).
- Use one branch/PR per task by default.
- Commit and push every completed task branch to origin.
- Open/update a PR for each task targeting branch `v2`.
- Only mark a task completed after its reviewed changes are merged into `v2`.
- When implementation and required tests pass, move the task to `03-needs-review`.
- Reviewer moves files between `04-in-review` and either `05-completed` or back to `01-ready`.

Test gating policy:
- Run task-scoped tests first (packages touched by the task) and treat these as blocking.
  - Example: `go test ./internal/foo ./internal/bar`
  - Integration if required by task: `go test ./internal/foo ./internal/bar -tags integration`
- Do not run broad `go test ./...` as the first signal for task completion.
- Optional full-suite runs are non-blocking unless they fail in the touched task scope.
- Every run summary must classify test outcomes explicitly:
  - `task_scope`: pass/fail (blocking)
  - `baseline_unrelated`: pass/fail (non-blocking if pre-existing/unrelated)
  - `decision`: `proceed` or `blocked`

Reviewer-required changes handling:
- A top-level `## Reviewer Required Changes` block in a task file is authoritative and must be treated as mandatory acceptance criteria for that rework pass.
- Resolve each checklist item in that block with concrete code/test changes.
- Add or update tests for every required change item.
- When all required items are resolved, remove the top-level `## Reviewer Required Changes` block before moving the file back to `03-needs-review`.
- Preserve reviewer feedback and resolution evidence in `issues/notes.md` (include task file name, fix summary, and test commands run).

Routing guardrails:
- Primary API routes are under `/v1/*`.
- Health routes are `/health/live`, `/health/ready` (aliases: `/health`, `/ready`).
- Test-mode reset route is `POST /test/reset` (only when `OTTERCAMP_MODE=test`).
