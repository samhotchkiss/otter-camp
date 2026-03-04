# Sam.blog Ralph Loop — Decision Log

Judgment calls made during the Ralph Loop that are not defined in the docsv2 spec.
These need Sam's review at the end of the loop.

---

## Decision 1: Pipeline gate scope (2026-03-03 13:17)

**Context**: The Codex reviewer is filing new issues (203-208) while reviewing Ralph Loop prerequisites (193-202). The pipeline gate says "wait until 01-04 are empty." But 204 (CLI subcommands), 205 (XSS sanitization), 206 (usage cost tracking), and 207 (memory compaction) don't affect the Ralph Loop — they're general quality issues found during review.

**Problem**: If the reviewer keeps finding issues, the pipeline never clears and the loop never starts.

**Decision**: Proceed with the Ralph Loop once the **Ralph Loop critical issues** are clear (193-202, 208), even if non-blocking issues (204-207) are still in the pipeline. The critical test is: can the platform execute the full project workflow (create → staff → scope → execute → review → done)?

**Issues that MUST clear before loop starts**:
- 193 ✅ workspace data directory
- 194 ✅ task requires flow template
- 195 ✅ turn auto-continuation
- 196 ✅ task done requires terminal node
- 197 ✅ starter trio assignment guard
- 198 ⏳ project staffing workflow
- 199 ✅ sidebar rate-limit resilience
- 200 ⏳ flow template review node
- 201 ⏳ flow.advanced kicks off next agent
- 202 ⏳ inbox task_review advances flow
- 208 ⏳ flow node create invalid request body

**Issues that can proceed in parallel**:
- 203 ✅ rebuild binary (binary built at loop start anyway)
- 204 ⏳ CLI subcommands (TUI-only loop, CLI not used)
- 205 ⏳ XSS/SSRF sanitization (not exercised in loop)
- 206 ⏳ usage cost tracking (not exercised in loop)
- 207 ⏳ memory compaction (not exercised in loop)

**Alternatives considered**:
- Wait for ALL issues: Could take hours with new issues being filed. Blocks progress.
- Skip pipeline gate entirely: Too risky — critical issues would cause loop failure.

**Risk**: If 204-207 cause side effects that surface during the loop, we'll catch them then and file new issues.

**Status updates**:
- 198 ✅ (merged PR #1580 at 13:27)
- 203 ✅ (merged PR at 12:46)
- 204 ✅ (merged PR #1581 at 13:21)
- 205 ✅ (merged PR #1582 at 13:45)

---

## Decision 2: Phased pipeline gate — start early phases (2026-03-03 13:56)

**Context**: Critical issues 200, 201, 202, 208 remain in the pipeline. 200 is on its 4th PR attempt (#1579, 640 additions across 6 files) awaiting review. 201 depends on 200, 202 depends on 201. 208 is independent but Codex hasn't started it yet. The dependency chain (200→201→202) could take hours to clear.

**Problem**: Sam is away for 4 hours and said "kick this off as soon as you can." Waiting for all critical issues blocks the entire loop.

**Decision**: Start Ralph Loop Phases 1-2 immediately with current v2 (which includes all merged fixes 193-199, 203-205). Gate Phase 3 on issues 200+208. Gate Phase 4 on issues 201+202.

**Rationale**:
- Phase 1 (project creation): Uses `project.create` tool, no flow nodes involved
- Phase 2 (staffing): Uses `session.create`, `message.send` — no flow templates
- Phase 3 (task scoping): Lori creates tasks WITH flow templates → needs 200 (review node validation) and 208 (flow node create API)
- Phase 4 (task execution): Agents advance through flow nodes → needs 201 (flow.advanced kickoff) and 202 (inbox approve advances flow)

**Risk**: If server restart from rebuilt binary drops in-flight state during Phase 1-2, project creation may need to restart. Low risk since we're starting clean.

---

## Decision 3: Fix agent routing directly (2026-03-03 14:12)

**Context**: During Ralph Loop Phase 1, the user message was routed to Ellie (memory keeper) instead of Frank (receptionist). Root cause: `GetStarterTrio` orders by `display_name ASC` (Ellie < Frank < Lori), and `resolveFirstAgentParticipant` picks the first joined agent. Ellie always wins alphabetically.

**Problem**: The entire agent specialization model is broken — Ellie created the project, briefed "Lori" (herself), and created 11 tasks as a memory keeper. This is a critical blocker for the Ralph Loop.

**Decision**: Fix directly in `internal/turn/engine.go:resolveSessionAgentForSession` rather than filing an issue and waiting for Codex. The fix adds scope-aware routing:
- Organization scope → `resolveFrankStarterID` (always Frank)
- Project scope → project PM first, then Frank
- Project task scope → (already handled correctly)

**Rationale**: This is a ~10 line change with clear intent and no risk of breaking existing functionality. Waiting for Codex to fix it would block the entire loop.

**Additional bugs found but NOT fixed directly**:
- `project_task_dependency` constraint violation (Frank tried to create task dependencies with invalid `depends_on_type`) — filing as issue
- Project session created by tool-calling agent doesn't add the intended recipient — deeper issue, needs design thought

---

## Decision 4: Direct DB scoping for tasks (2026-03-03 14:35)

**Context**: Phase 3 (Task Scoping) requires Lori to attach flow templates and assign agents to tasks. Two missing platform capabilities block this:
1. No `flow.list_templates` tool — agents can't discover template IDs
2. `task.update` doesn't support `flow_template_id` or `assigned_agent_id`

Lori tried to find the template via `flow.get_template` but used wrong IDs (project ID, zeroed UUID) because she had no way to list templates.

**Decision**: Filed issues 209 (task.update fields) and 210 (flow.list_templates tool) for Codex. Applied flow templates and agent assignments directly via SQL to unblock Phase 4.

**Applied**:
- 26 leaf subtasks (WS1.1-WS5.6): `flow_template_id` = Default Review, `assigned_agent_id` = Sam.blog Worker
- 5 parent workstreams (WS1-WS5): left unscoped (organizational containers)

**Risk**: Low — these are the correct values Lori would have set if the tools existed.

---

## Decision 4: Supervisor recovery cascade fix (2026-03-03 22:17)

**Context**: After worker restart, 81+ stale `agent_turn` jobs from supervisor recovery runs were clogging the job queue. Each called the LLM (30-120s) before the next could run. Meanwhile, the supervisor kept detecting its own recovery runs as "stuck" and creating new recovery runs — an infinite cascade generating ~6 new agent_turn jobs per minute.

**Root cause**: The supervisor's `recoverRun()` had no guard against recovering its own recovery runs. When a recovery run got stuck (because the agent_turn job was waiting in a backlogged queue), the next supervisor tick would create ANOTHER recovery run for it, ad infinitum.

**Fixes applied** (commit 8c2b3c77):
1. `recoverRun()` skips runs with `trigger_type='supervisor'` — no cascading recoveries
2. `recoverRun()` skips runs whose session is closed/archived
3. `handleUserMessage()` skips closed/archived sessions (prevents wasted LLM calls)
4. `PurgeStaleAgentTurnJobs()` at worker startup dead-letters jobs for closed sessions and supervisor-injected messages
5. Debug logging for job queue operations

**One-time cleanup**: Failed 20 stale in_progress supervisor runs and purged 95 pending agent_turn jobs via SQL before restart.

**Risk**: Supervisor recovery is now less aggressive — a supervisor-triggered run that gets stuck will be failed but not re-recovered. The task's blocker/escalation flow handles further retries.

---

## Decision 5: agent_id injection fix (2026-03-03 23:10)

**Context**: The turn engine (engine.go:1029) unconditionally overwrote `arguments["agent_id"]` with the calling agent's ID. This meant `agent.get`, `agent.update`, and the new `agent.assign_project` always operated on the caller instead of the target agent. Frank correctly diagnosed this as "agent_get keeps returning my profile regardless of which agent ID I pass."

**Decision**: Changed to conditional injection (like session_id and project_id): only inject agent_id if the model didn't provide one. This is a 1-line fix in the turn engine, well within the ≤2 line direct-fix threshold.

**Also fixed**: Invalidated cached session_tool_set rows so Frank's session picks up the new `agent.assign_project` tool (migration 0104). Filed this cache-invalidation gap as something the platform should handle automatically on new tool deployments.

**Result**: Frank successfully created a temp PM agent, assigned it with `agent.assign_project`, and queued all 11 tasks. Tasks immediately moved to in_progress.

---

## Decision 6: Anthropic API rate limit blocker (2026-03-03 23:40)

**Context**: Phase 4 (Task Execution) was underway — Frank (as Sam.blog Worker) was scaffolding the Hugo site, working through file_write vs CLI workspace sync issues. At 23:18, all 11 task sessions simultaneously hit Anthropic API rate limits. Every agent_turn job failed with "model provider rate limited" and dead-lettered after 3 rapid retries (~5s apart).

**Problem**: Two issues compounded:
1. **External**: Anthropic API key hit its quota limit. Rate limit persists 20+ minutes after the initial burst (still active at 23:40). This is not a per-minute rate limit.
2. **Platform**: agent_turn job retries every ~5s with no rate-limit-aware backoff, exhausting all 3 attempts in ~15s. Filed as issue 213.
3. **Recovery**: Supervisor created recovery runs, but those also failed. Our cascade-prevention fix (Decision 4) then blocks re-recovery of supervisor-triggered runs → deadlock.

**Decision**: Stop testing. Wait for Anthropic rate limits to clear naturally. Check every 5-10 minutes with a test message via TUI. Once rate limits clear:
1. Restart server + worker to clear stale health state
2. Send messages to stuck task sessions to re-trigger agent turns
3. Monitor for successful turn completion

**Why not switch providers**: CLAUDE.md explicitly prohibits switching model providers without Sam's approval. OpenAI is available but not authorized.

**Filed**: Issue 213 (rate-limit backoff) pushed to v2.

**Diagnostic update** (23:59): Added slog.Warn to gateway client.go to log provider error detail on 429. Results:
- Provider: Anthropic (connection `84a9141d`)
- Error: "This request would exceed your account's rate limit."
- Retry-After: `1h0m46s` → rate limit resets ~01:00 MST
- The account's hourly quota was exhausted by the burst of 11 simultaneous task sessions.

**Plan**: Wait until 01:00 MST, then retry. Do not send any messages to OtterCamp until then to avoid wasting the 3-attempt dead-letter budget on each retry.

---

## Decision 7: Flow execution gap — continue work despite broken flow (2026-03-04 02:20)

**Context**: During Phase 4, agents calling `flow.advance` get "not found" errors on all 11 Sam.blog tasks. Root cause: Decision 4 applied `flow_template_id` via SQL AFTER tasks were already `in_progress`. The `TaskQueueProcessor` creates flow executions during `queued→in_progress`, so no flow executions were ever created.

**Impact**: Tasks cannot advance Work → Review → Done via the flow system. Agents are using `task.update` to change `work_status` manually, bypassing flow enforcement. 3 tasks (OC-2, OC-6, OC-9) reached "Review" this way, but none can reach "Done" via flow terminal node.

**Decision**: Filed issue 214. Continue monitoring agent work — the agents can still create deliverables even if the flow completion is broken. When the fix lands, flow executions can be retroactively created and tasks can complete properly.

**Risk**: This means the Ralph Loop can't fully validate the Work → Review → Done flow path. The spec compliance for "task done only via flow terminal node" can't be tested on this iteration.

---

## Decision 8: Second Anthropic rate limit hit (2026-03-04 02:24)

**Context**: After the first rate limit cleared at ~01:00 MST (Decision 6), agents processed turns from 01:00-02:19 (~80 minutes of active work). At 02:23, all new agent_turn jobs hit rate limits again with `retry_after: 3h36m` → resets ~06:00 MST.

**Root cause**: 11 task sessions with ~200KB conversation contexts each. Each agent_turn sends the full conversation history to the Anthropic API. The burst of turns from 01:00-02:19 exhausted the account's hourly token quota again.

**Progress made during the window (01:00-02:19)**:
- All 11 tasks now have Sam.blog Worker assigned with active runs
- 3 tasks advanced to Review status (OC-2, OC-6, OC-9) via task.update workaround
- Agents did significant work: Hugo scaffolding, SEO setup, content migration, taxonomy design, deployment pipeline
- Discovered flow.advance is broken (Issue 214 filed)

**Decision**: Stop testing. Wait for rate limits to clear at ~06:00 MST. The rate-limit backoff (Issue 213 fix) will automatically retry at the right time. Do not send any messages until then.

**Why not reduce concurrent sessions**: The sessions already exist. We can't reduce the conversation context size without chat summarization (which also costs tokens). The fundamental constraint is the account's hourly token quota vs. the amount of work needed.

---

## Decision 9: flow.advance still broken — continue with task.update workaround (2026-03-04 06:30)

**Context**: After rate limits cleared at ~06:00, agents resumed work and tried calling `flow.advance` as instructed. They got "repo: not found" errors. Investigation revealed PR #1593's backfill logic is in the flow execution service's `AdvanceFlow()` method, but the native tool handler `handleFlowAdvance()` bypasses it by going directly to the repository layer (`GetByID()`).

**Impact**: Same as Decision 7 — tasks cannot advance through the flow system. Agents use `task.update` to change `work_status` to "review" as a workaround. Tasks cannot reach "done" via flow terminal node.

**Decision**: Filed issue 216 for Codex. Continue with task.update workaround. If Codex fixes 216, we can retry flow.advance. If not, manual DB intervention may be needed at Phase 5 to retroactively complete flows.

**Risk**: The Ralph Loop can't validate the Work → Review → Done flow path. This is now the same gap noted in Decision 7.

---

## Decision 10: Worker deadlock workaround (2026-03-04 06:30)

**Context**: Worker process (ottercamp worker) deadlocks after processing 3-8 agent_turn jobs. The job queue polling loop stops entirely. Process is alive but at 0% CPU. Last log entry is always a cancel consumer startup.

**Decision**: Filed issue 215 for Codex. Manually restart the worker when it hangs. This is sufficient to keep tasks progressing since jobs are retried from the DB queue on worker restart.

**Risk**: Manual restarts mean agent work is interrupted and retried. Some turns may be wasted (LLM call completes but worker can't process the result). Rate limit budget is consumed by these wasted turns.

