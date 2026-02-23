# 057: Native Tools — Tier 2 (Mutation)

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 20 §NativeTierTwo, doc 20 §FileWriteTools, doc 20 §GitMutationTools, doc 20 §ProjectTaskMutation, doc 11 §GitOperationRules, doc 16 §CapabilityGate |
| Spec status | finished |
| Depends on | 056, 055, 033, 024, 027, 028, 030 |
| Blocks | 077, 087 |

## Scope

Implement all tier 2 (mutation) native tools: file write/edit/delete, git commit/push, shell
execute, project/task/agent mutation, flow control, memory record, and communication tools.
Every tier 2 tool requires a pre-execution capability policy check (handled by the broker in
task 055) before the handler is invoked. These handlers only run after the broker has
confirmed `policy_decision='allowed'`.

### Must build

**Tier 2 tool handlers** (added to `NativeToolExecutor` from task 056):

`file.write`:
- Capability: `system.file.write`
- Input: `{path: string, content: string, encoding?: "utf8"|"base64", create_dirs?: boolean}`
- Resolves path via `SessionWorkDir.ResolvePath` (path traversal prevention from task 056).
- If `create_dirs=true`: creates parent directories as needed (all within workspace).
- Writes file atomically (write to `.tmp` file, then rename).
- Returns `{path: string, byte_size: integer, created: boolean}`.
- Records `audit_event` with `event_type='file_written'`.

`file.edit`:
- Capability: `system.file.write`
- Input: `{path: string, old_string: string, new_string: string, replace_all?: boolean}`
- Resolves and reads the file; applies find-and-replace.
- If `old_string` not found in file: returns `{error: "old_string_not_found"}`.
- If `replace_all=false` (default) and `old_string` appears more than once: returns `{error: "ambiguous_match", count: N}` — caller must provide more context.
- Writes result atomically.
- Returns `{path: string, replacements_made: integer}`.

`file.delete`:
- Capability: `system.file.write`
- Input: `{path: string}`
- Resolves path; verifies within workspace.
- Does not delete directories (use `cli.execute rm -rf` for that).
- Returns `{deleted: true}` on success.

`git.commit`:
- Capability: `system.file.write` (git commit requires file write capability — doc 20)
- Input: `{message: string, paths?: [string], all?: boolean}`
- `paths`: specific files to stage; `all=true` stages all modified/untracked files.
- Runs `git add <paths>` then `git commit -m <message>`.
- Returns `{sha: string, short_sha: string, files_committed: integer, message: string}`.
- Git operation rules (doc 11):
  - NEVER commit directly to `main` or `master` branches.
  - If current branch is `main` or `master`: return `{error: "cannot_commit_to_main"}`.
  - Verify `git config user.email` is set before committing; if not, set to `agent@ottercamp.internal`.

`git.push`:
- Capability: `system.git.push` (distinct capability from file write)
- Input: `{remote?: string, branch?: string, force?: boolean}`
- Git operation rules (doc 11):
  - `force=true` is only allowed if the branch is NOT `main`, `master`, or any branch matching `shared/*`.
  - If `force=true` and branch is protected: return `{error: "force_push_denied", branch: <name>}`.
  - Default `remote` is `origin`; default `branch` is current branch.
- Runs `git push [--force] <remote> <branch>`.
- Returns `{remote: string, branch: string, commits_pushed: integer}`.

`cli.execute`:
- Capability: `system.cli.execute`
- Delegated to `CLIExecutor` (task 058). This tool handler is a thin wrapper:
  - Input: `{command: string, working_directory?: string, timeout_seconds?: integer, env?: map[string]string}`
  - Calls `CLIExecutor.Execute(ctx, input)`.
  - Returns structured output from `CLIExecutor`.

`memory.record`:
- Capability: `memory.write`
- Input: `{content: string, scope?: "org"|"project"|"agent", sensitivity?: "normal"|"restricted", tags?: [string]}`
- Calls `MemoryService.RecordExplicit(ctx, agentID, content, scope, sensitivity, tags)` (task 039/041).
- Returns `{memory_id: uuid, status: "queued"|"stored"}`.

`project.create`:
- Capability: `project.manage`
- Input: `{name: string, slug?: string, delivery_mode?: string, description?: string}`
- Calls `ProjectService.Create(ctx, input)` (task 018).
- Returns `{project: {id, slug, name, status}}`.

`project.update`:
- Capability: `project.manage`
- Input: `{project_id: string, name?: string, description?: string, delivery_mode?: string}`
- Calls `ProjectService.Update(ctx, input)`.
- Returns `{project: {...}}`.

`task.create`:
- Capability: `task.manage`
- Input: `{project_id: string, title: string, description?: string, flow_template_id?: string, requires_human_review?: boolean}`
- Calls `TaskService.Create(ctx, input)` (task 028).
- Returns `{task: {id, task_number, work_status}}`.

`task.update`:
- Capability: `task.manage`
- Input: `{task_id: string, title?: string, description?: string, work_status?: string}`
- Calls `TaskService.Update(ctx, input)`.
- Returns `{task: {...}}`.

`task.add_dependency`:
- Capability: `task.manage`
- Input: `{source_type: string, source_id: string, depends_on_type: string, depends_on_id: string}`
- Calls `TaskService.AddDependency(ctx, input)` — rejects cycles.
- Returns `{dependency_id: uuid}`.

`task.remove_dependency`:
- Capability: `task.manage`
- Input: `{dependency_id: string}`
- Calls `TaskService.RemoveDependency(ctx, dependencyID)`.
- Returns `{removed: true}`.

`subtask.create`:
- Capability: `task.manage`
- Input: `{flow_node_execution_id: string, title: string, description?: string, assignee_type?: string, assignee_id?: string}`
- Calls `FlowExecutionService.CreateSubtask(ctx, input)` (task 030).
- Returns `{subtask: {id, status, sequence_number}}`.

`subtask.update`:
- Capability: `task.manage`
- Input: `{subtask_id: string, status?: string, title?: string, description?: string}`
- Returns `{subtask: {...}}`.

`flow.advance`:
- Capability: `flow.control`
- Input: `{flow_node_execution_id: string, commit_sha?: string}`
- Signals that the agent has completed the current flow node.
- Calls `FlowExecutionService.Advance(ctx, input)` (task 030).
- Records `commit_sha` on the `flow_node_execution` row.
- Returns `{advanced_to_node_id: uuid|null, flow_completed: boolean}`.

`flow.review_decision`:
- Capability: `flow.control`
- Input: `{flow_node_execution_id: string, decision: "approve"|"reject", reason?: string}`
- Calls `FlowExecutionService.RecordReviewDecision(ctx, input)` (task 030).
- Returns `{next_node_id: uuid|null}`.

`flow.create_template`:
- Capability: `project.manage`
- Input: `{project_id: string, name: string, nodes: [{...}]}`
- Calls `ProjectService.CreateFlowTemplate(ctx, input)` (task 018).
- Returns `{template: {id, version}}`.

`schedule.create`:
- Capability: `project.manage`
- Input: `{project_id: string, cron: string, flow_template_id: string, overlap_policy: string, max_duration_ms?: integer}`
- Calls `ProjectService.CreateSchedule(ctx, input)`.
- Returns `{schedule: {id, cron, next_fire_at}}`.

`schedule.update`:
- Capability: `project.manage`
- Input: `{schedule_id: string, cron?: string, overlap_policy?: string}`
- Returns `{schedule: {...}}`.

`schedule.delete`:
- Capability: `project.manage`
- Input: `{schedule_id: string}`
- Returns `{deleted: true}`.

`agent.create_temp`:
- Capability: `agent.create_temp`
- Input: `{name: string, system_prompt: string, scope_type: string, scope_id: string, ttl_seconds?: integer, skill_ids?: [string]}`
- Calls `AgentService.CreateTemp(ctx, input)` (task 014).
- Returns `{agent: {id, name, lifecycle_status}}`.

`agent.update`:
- Capability: `agent.manage`
- Input: `{agent_id: string, system_prompt?: string, operator_instructions?: string, tool_allow_list?: [string], tool_deny_list?: [string]}`
- Returns `{agent: {...}}`.

`session.create`:
- Capability: `session.manage`
- Input: `{scope_type: string, scope_id: string, mode?: string, title?: string}`
- Calls `ChatService.CreateSession(ctx, input)` (task 044).
- Returns `{session: {id, status, mode}}`.

`session.invite_agent`:
- Capability: `session.manage`
- Input: `{session_id: string, agent_id: string}`
- Calls `ChatService.AddParticipant(ctx, sessionID, 'agent', agentID)` (task 044).
- Returns `{participant_id: uuid}`.

`message.send`:
- Capability: `message.send`
- Input: `{session_id: string, content: string, role?: "user"|"assistant"}`
- Calls `ChatService.AppendMessage(ctx, input)` (task 044).
- Returns `{message_id: uuid, sequence: integer}`.

**Communication tool stubs (doc 20 §CommunicationTools):**

`email.compose` and `slack.post` are tier 2 tools that create `draft_action_review` inbox items
rather than executing immediately (by design — human reviews before sending). Implement as:
- `email.compose`: Capability `communication.send`. Creates `inbox_item(item_type='draft_action_review', metadata={action:'email.compose', ...input})`. Returns `{inbox_item_id: uuid, status: 'pending_review'}`.
- `slack.post`: Same pattern. Capability `communication.send`.
- The approval flow (what happens when the human approves/rejects the inbox item) is a thin
  spec area (see CHUNKING-PLAN thin spec note for doc 20). Implement approval as a no-op stub
  that marks the inbox_item as `actioned` and logs the decision. Full implementation deferred.

**Tool definition registry entries (tier 2):**
- `0064_tool_definition_tier2_seed.sql` — insert rows for all tier 2 tools with:
  - `tool_tier='tier2'`
  - `tool_domain='native'`
  - `capability`: the capability string from each handler above

### Must NOT build

- Tier 1 tools (task 056)
- CLI executor (task 058) — `cli.execute` handler delegates to it
- Browser executor (task 059)
- The capability policy check itself (task 055 broker handles this)

## Acceptance Criteria

- [ ] `file.edit` with `old_string` matching exactly once in file → file updated, `{replacements_made: 1}` returned
- [ ] `file.edit` with `old_string` matching twice and `replace_all=false` → `{error: "ambiguous_match", count: 2}` returned, file NOT modified
- [ ] `git.commit` on `main` branch → `{error: "cannot_commit_to_main"}` without executing git
- [ ] `git.push` with `force=true` on `shared/staging` branch → `{error: "force_push_denied"}`
- [ ] `git.push` with `force=true` on a feature branch (not `main`/`master`/`shared/*`) → succeeds
- [ ] `email.compose` creates an `inbox_item` with `item_type='draft_action_review'` and returns `pending_review` status
- [ ] All tier 2 tool names have corresponding `tool_definition` rows after bootstrap migration

## Tests Required

**Unit tests:**
- `file.write` atomic write: write to workspace path; on success, verify `.tmp` intermediate file does not exist
- `file.edit` replace_all=false with 2 occurrences → `ErrAmbiguousMatch`, file unchanged
- `file.edit` old_string not found → `{error: "old_string_not_found"}`
- `git.commit` current branch=`main` → error without running git
- `git.push` force on protected branch → error without running git
- `git.push` force on unprotected branch → runs with `--force` flag
- `memory.record` → delegates to `MemoryService.RecordExplicit` (verify call made, no direct DB access)
- `email.compose` → inbox_item created with correct metadata

**Integration tests:**
- `file.write` then `file.read`: write content to workspace; read it back → content matches
- `file.delete` then `file.read`: delete file; read → `{error: "not_found"}`
- `task.create` → task row in DB; `task.add_dependency` → dependency row; `task.remove_dependency` → row removed; cycle check: add A→B→A → `ErrCycleDetected`
- `flow.advance` on a real `flow_node_execution` row → execution advances, next node ID returned

**E2E tests:**
- None — covered by dedicated E2E task 087
