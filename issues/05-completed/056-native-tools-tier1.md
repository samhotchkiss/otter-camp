# 056: Native Tools — Tier 1 (Read-Only)

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 20 §NativeTierOne, doc 20 §FileTools, doc 20 §GitTools, doc 20 §SearchTools, doc 11 §WorkingDirectory |
| Spec status | finished |
| Depends on | 055, 003, 013, 027, 038, 040 |
| Blocks | 057, 077, 087 |

## Scope

Implement all tier 1 (read-only, no capability gate) native tools: file reading, directory
listing, git status/diff/log, grep/search. Includes the per-session working directory
abstraction and path traversal prevention. These tools are executed inline (no separate
worker process), and are called via `NativeToolExecutor.Execute` from task 055.

### Must build

**`NativeToolExecutor`** (in `internal/tools/native/executor.go`):
- `NativeToolExecutor.Execute(ctx, toolName, input) (map[string]any, error)` — routes to the correct handler by `toolName`.
- Returns structured output matching the tool's output schema.
- On unknown tool name: return `ErrUnknownTool`.

**Working directory management:**
- `SessionWorkDir` struct: wraps a per-session root path.
  - Created at session start; stored in session context (not persisted to DB — in-memory for session lifetime).
  - Default root: `<data_dir>/workspaces/<org_id>/<project_id>/<task_id>/` — created on first use.
  - `SessionWorkDir.ResolvePath(relOrAbsPath string) (string, error)`:
    - If the input is absolute: verify it is within the workspace root after `filepath.Clean`. If not, return `ErrPathTraversal`.
    - If relative: join with workspace root, then `filepath.Clean`, then verify prefix is still the workspace root.
    - Reject paths containing `..` components before and after cleaning.
    - Reject paths that start with `/` but escape the workspace root.
  - `SessionWorkDir.Root() string` — returns the workspace root path.
- The workspace root is pinned to the project/task directory for task-scoped sessions. For org-scoped sessions without a task, the workspace root is `<data_dir>/workspaces/<org_id>/general/`.

**Tier 1 tool handlers:**

`file.read`:
- Input: `{path: string, encoding?: "utf8"|"base64", max_bytes?: integer}`
- Resolves path via `SessionWorkDir.ResolvePath`.
- Reads the file; truncates at `max_bytes` (default 1MB; hard max 10MB).
- Returns `{content: string, encoding: "utf8"|"base64", byte_size: integer, truncated: boolean}`.
- Error if path resolves outside workspace: `{error: "path_traversal"}`.
- Error if file does not exist: `{error: "not_found"}`.
- Does NOT follow symlinks that escape the workspace root.

`file.list` (directory listing):
- Input: `{path?: string, recursive?: boolean, pattern?: string}`
- `path` defaults to workspace root if omitted.
- Resolves path via `ResolvePath`.
- Returns `{entries: [{name, path, type: "file"|"directory", size_bytes, modified_at}], total: integer}`.
- `pattern` supports glob syntax (e.g. `*.go`, `**/*.md`).
- Max 1000 entries; if exceeded, returns the first 1000 with `{truncated: true}`.
- Does NOT recurse into hidden directories (`.git`, `.env`, etc.) unless `path` explicitly points into them.

`file.search` (grep equivalent):
- Input: `{pattern: string, path?: string, recursive?: boolean, case_insensitive?: boolean, max_results?: integer, file_pattern?: string}`
- Uses Go's `regexp` package for pattern matching (RE2 syntax).
- `path` defaults to workspace root.
- `file_pattern` filters which files to search (glob; default `*`).
- Returns `{matches: [{file, line_number, line_content, context_before: [string], context_after: [string]}], total_matches: integer, truncated: boolean}`.
- Max results: 500 (hard cap); default 100.
- Context: 2 lines before and after each match.

`git.status`:
- Input: `{path?: string}` — defaults to workspace root.
- Resolves path; verifies it is within workspace.
- Runs `git status --porcelain=v1` via `exec.CommandContext` with the resolved path as working directory.
- Returns `{branch: string, ahead: integer, behind: integer, files: [{path, status: "M"|"A"|"D"|"R"|"?"|"!"}], is_dirty: boolean}`.
- If path is not a git repo: `{error: "not_a_git_repo"}`.

`git.diff`:
- Input: `{path?: string, ref?: string, staged?: boolean, file_path?: string}`
- Runs `git diff [--cached] [<ref>] [-- <file_path>]`.
- Returns `{diff: string, files_changed: integer, insertions: integer, deletions: integer}`.
- Max diff output: 200KB (truncated with `{truncated: true}`).

`git.log`:
- Input: `{path?: string, limit?: integer, since?: string, author?: string, file_path?: string}`
- Runs `git log --oneline [options]`.
- `limit` default 20, max 100.
- Returns `{commits: [{sha, short_sha, author, email, date, message}]}`.

`memory.query`:
- Input: `{query: string, scope?: "org"|"project"|"agent", limit?: integer}`
- Delegates to `MemoryService.Query(ctx, agentID, query, scope, limit)` (task 040).
- Tier 1: no capability gate.
- Returns `{memories: [{id, content, confidence, source_type, created_at}], total: integer}`.

`project.list` (read-only):
- Input: `{limit?: integer, cursor?: string}`
- Calls `ProjectRepository.ListByOrg(ctx, orgID, limit, cursor)`.
- Returns `{projects: [{id, slug, name, status, delivery_mode}], meta: {cursor}}`.

`project.get` (read-only):
- Input: `{project_id: string}`
- Calls `ProjectRepository.Get(ctx, projectID)`.
- Returns `{project: {...}}`.

`task.list` (read-only):
- Input: `{project_id?: string, status?: string, limit?: integer, cursor?: string}`
- Returns `{tasks: [{id, task_number, title, work_status, assignee}], meta: {cursor}}`.

`task.get` (read-only):
- Input: `{task_id: string}`
- Returns `{task: {...full task row...}}`.

`inbox.list`:
- Input: `{status?: "pending"|"actioned"|"all", limit?: integer}`
- Returns `{items: [{id, item_type, urgency, created_at, summary}], meta: {cursor}}`.
- Scoped to the requesting agent's inbox items.

`session.list` (read-only):
- Input: `{scope_type?: string, scope_id?: string, status?: string, limit?: integer}`
- Returns `{sessions: [{id, scope_type, scope_id, mode, status}], meta: {cursor}}`.

`session.get` (read-only):
- Input: `{session_id: string}`
- Returns `{session: {...}, participant_count: integer}`.

`session.history`:
- Input: `{session_id: string, limit?: integer, before_sequence?: integer}`
- Returns `{messages: [{sequence, author_type, author_id, role, content, created_at}], meta: {next_sequence}}`.

`agent.list` (read-only):
- Input: `{class?: "staff"|"temp", status?: string, limit?: integer}`
- Returns `{agents: [{id, name, class, lifecycle_status}], meta: {cursor}}`.

`agent.get` (read-only):
- Input: `{agent_id: string}`
- Returns `{agent: {...}}`.

`flow.get_template`:
- Input: `{flow_template_id: string}`
- Returns `{template: {...}, nodes: [...]}}`.

`flow.get_execution`:
- Input: `{flow_node_execution_id: string}`
- Returns `{execution: {...current_node, visit_count, subtasks, session_id, commit_sha}}`.

`schedule.list`:
- Input: `{project_id: string}`
- Returns `{schedules: [{id, cron, flow_template_id, overlap_policy, last_fired_at, next_fire_at}]}`.

`merge_queue.status`:
- Input: `{project_id: string}`
- Returns `{entries: [{id, task_id, position, enqueued_at}], length: integer}`.

**Tool definition registry entries** (`tool_definition` table, seeded at bootstrap):
For each tier 1 tool above, ensure a row exists in `tool_definition` with:
- `tool_name`: the dot-namespaced name (e.g. `file.read`)
- `tool_tier`: `'tier1'`
- `tool_domain`: `'native'`
- `capability`: null (tier 1 tools have no capability requirement)
- `description`, `input_schema`, `output_schema`: JSON Schema documents

**Migration:**
- `0063_tool_definition_tier1_seed.sql` — insert rows for all tier 1 tools above (idempotent via `ON CONFLICT DO NOTHING`)

### Must NOT build

- Tier 2 mutation tools (task 057)
- CLI executor (task 058)
- Browser executor (task 059)
- Tool broker dispatch (task 055)
- Memory extraction/storage (tasks 039, 040, 041) — `memory.query` delegates to those services

## Acceptance Criteria

- [ ] `file.read` with a path that contains `../` components resolves correctly and returns `ErrPathTraversal` if the resolved path escapes the workspace root
- [ ] `file.read` on a symlink that points outside the workspace returns `ErrPathTraversal`
- [ ] `file.list` with `recursive=true` returns up to 1000 entries; `truncated=true` if more exist
- [ ] `file.search` returns 2-line context around each match; respects `max_results` cap of 500
- [ ] `git.status` on a non-git directory returns `{error: "not_a_git_repo"}`
- [ ] `git.log` with `limit=5` returns at most 5 commits
- [ ] `memory.query` delegates to `MemoryService.Query` — no direct DB access in the tool handler
- [ ] All tier 1 tool names have corresponding `tool_definition` rows after bootstrap migration

## Tests Required

**Unit tests:**
- `ResolvePath("../../../etc/passwd")` → `ErrPathTraversal`
- `ResolvePath("/absolute/path/outside")` → `ErrPathTraversal`
- `ResolvePath("./subdir/file.txt")` → resolves to `<workspace_root>/subdir/file.txt`
- `file.read` truncation: file larger than `max_bytes` → `truncated=true`, content limited
- `file.list` pattern filter: workspace has `foo.go` and `bar.txt`; `pattern=*.go` → only `foo.go` returned
- `file.search` context lines: match on line 5 → returns lines 3,4 (before) and 6,7 (after)
- `git.status` mock: mock `exec.CommandContext` to return known `--porcelain=v1` output → parse correctly
- `git.diff` max output: output >200KB → truncated response

**Integration tests:**
- `file.read` round-trip: write a file to a temp workspace directory; call `file.read` → returns content; call with path escaping workspace → error
- `file.search` on a real directory tree: seed 3 files with known content; search for pattern that matches in 2 of them → 2 results returned
- `git.status` against a real git repo (use the test repo from task 001): correct branch name, clean/dirty status

**E2E tests:**
- None — covered by dedicated E2E task 087
