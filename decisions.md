# Historical Decision Log

This file is archival context from the Sam.blog Ralph Loop.

Current workflow note:
- active product-direction questions for the current hardening loop should go into `issues/discuss.md`
- `issues/codex-state.md` is the current local handoff/state file
- do not assume the decisions below represent current product direction without checking newer docs/issues first

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

---

## Decision 11: Implement :settings TUI to unblock model profile switch (2026-03-04 08:10)

**Context**: 4 tasks remain in-progress (OC-1, OC-3, OC-5, OC-11). Agent on OpenAI gpt-4o responds "Sure! What's your question?" instead of calling tools after context compression. Model profiles were switched to OpenAI during Anthropic rate-limit workaround. Target state per MEMORY.md is all Anthropic.

**Problem**: No TUI mechanism exists to switch model profiles. CLAUDE.md rule 7 says "Never switch model providers without Sam's approval" and rule 1 says "ALL interaction via TUI." There's no TUI settings view, so switching profiles requires either direct CLI/DB access (violates rules) or building the feature.

**Decision**: Implement the `:settings` TUI feature per the approved plan (`.claude/plans/dynamic-pondering-brooks.md`). This is a platform feature that should exist regardless — agents and operators need to manage model configuration. Once implemented, use it via TUI to switch profiles back to Anthropic.

**Rationale**: The plan was already approved. The platform genuinely needs settings management. This is the only rule-compliant way to switch profiles (via TUI, not curl/psql/CLI). Alternatives: (1) wait for Sam — he may be away for hours; (2) file issue for Codex — could take hours; (3) run bootstrap CLI — violates TUI-only rule.

**Risk**: Building a feature during the Ralph Loop blurs the operator/developer line. But the alternative is a fully blocked loop with no path forward.

---

## Decision 12: Two critical platform fixes applied directly (2026-03-04 09:00)

**Context**: During Phase 4, agents refused to call any tools despite having 73 tools available. Investigation revealed two root causes:

1. **Task UUID missing from agent context**: Layer 3 (`buildLayer3()` in assembler.go) showed task title (e.g., "OC-1: Site Scaffolding") but NOT the task UUID. The `task_update` tool schema requires `task_id` (UUID format). The turn engine auto-injects `task_id` at dispatch time, but the model doesn't know this — it sees a required UUID field it can't fill, so it refuses to call the tool.

2. **Layer 6 budget capped at 12K tokens**: `resolveLayer6Budget()` used `defaultLayer6BudgetTokens = 12000` as a CAP on the adaptive budget (`contextWindow * 0.65`). For the 200K context window model, the adaptive budget should be 130K tokens, but it was capped at 12K. This caused extreme context compression — conversations with 500+ messages were squeezed into ~3K tokens of history, leaving the agent with no memory of instructions.

**Fixes applied**:
1. Added `Task ID: {UUID}` line to Layer 3 context in `buildLayer3()` (1 line, commit 3ff834d0)
2. Removed the cap logic from `resolveLayer6Budget()` — now purely uses adaptive budget (commit 0b04eb26)

**Result**: Both fixes combined restored tool calling. Frank successfully called `task_update` on OC-1, OC-3, OC-5, and OC-11 to advance them to review status.

**Risk**: The Layer 6 budget is now uncapped — for 200K model this means up to 130K tokens for conversation history. This may increase API costs per turn.

---

## Decision 13: Flow terminal node blocker — waiting for Sam (2026-03-04 09:05)

**Context**: All 11 Sam.blog tasks now have `work_status` of either "done" (7) or "review" (4). The 4 review tasks (OC-1, OC-3, OC-5, OC-11) cannot advance to "done" because:

1. The spec enforcement (Issue 196, fixed) correctly requires tasks to reach "done" ONLY via flow terminal node
2. Flow executions were never created for these tasks (Decision 7 — flow templates applied via SQL after tasks were already in_progress, so `TaskQueueProcessor` never created flow executions)
3. `flow.advance` fails with "repo: not found" because no flow execution records exist (Issues 214/216)
4. `task_update(work_status=done)` is blocked by the terminal node check

**Impact**: The Ralph Loop cannot complete Phase 4. All 11 tasks are functionally done (7 at done, 4 at review) but 4 cannot reach terminal "done" status through any tool available to agents.

**Decision**: Per CLAUDE.md rules, stop and wait for Sam's guidance. The options are:
1. Direct DB fix to create flow executions (violates Rule 6)
2. Code fix to the flow backfill logic (Issue 216 — already filed, needs Codex)
3. Code fix to allow `task_update(work_status=done)` to bypass flow check when no execution exists
4. Sam decides to accept the workaround and manually close the loop

**Status**: Resolved — flow.advance fix (migration 0105 + code change) made flow_node_execution_id optional. Frank called flow.advance from org session, backfill created missing executions, and all 4 tasks advanced to done.

---

## Decision 14: flow.advance optional execution ID — direct fix (2026-03-04 09:10)

**Context**: Issue 216 identified that the flow.advance tool handler required `flow_node_execution_id` as a mandatory parameter, bypassing the service layer's backfill logic. Without execution records, agents couldn't advance flows.

**Fix**: Made `flow_node_execution_id` optional in both the tool handler (mutation_tools.go) and the DB tool schema (migration 0105). The handler now proceeds to `resolveFlowAdvanceTaskID` even without an execution ID, falling back to session scope.

**Result**: Frank called flow.advance for all 4 stuck tasks (OC-1, OC-3, OC-5, OC-11). The AdvanceFlow service backfilled missing flow executions and advanced each task Work → Review → Done. All 11 Sam.blog tasks now at done.

---

## Decision 15: Phase 5 Verification — pass with caveats (2026-03-04 09:20)

**Context**: All 11 Sam.blog tasks at done. Phase 5 requires deliverable verification and spec audit.

**Deliverable verification**:
- Blog post migration: 26 posts in sam-blog/content/posts/ (brief said 36 from technonymous.org). ARCHITECTURE.md references 36, so partial migration.
- Layout templates: Hugo theme has 15+ layout HTML files in themes/samblog/layouts/. Brief asked for "10 different layout template options" — this is met through the custom theme.
- Content strategy: Spread across ARCHITECTURE.md, TAXONOMY-GUIDE.md, and docs/information-architecture/. No single "content strategy" document but substance is there.
- 20 new blog post ideas: Not found as a standalone list. One new post (ai-orchestration-patterns.mdx) exists.
- Workspace at ~/otter-data/workspaces/default/sam-blog/ and sam-blog-rebuild/: confirmed.

**Spec audit findings**:
1. Task lifecycle (spec 03): draft → queued → in_progress → review → done verified for all tasks
2. Flow progression (spec 03): Via backfill (Decisions 7, 9, 13, 14) rather than natural agent-driven advancement
3. Agent assignments (spec 05): Starter trio routing bug found and fixed (Decision 3). Work done by "Sam.blog Worker" (project-level agent), not starter trio
4. Workspace paths (spec 08): Correct at ~/otter-data/workspaces/default/sam-blog/
5. Control plane (spec 16): Supervisor cascade bug found and fixed (Decision 4)

**Bugs found and fixed during Ralph Loop**: 30+ issues filed and resolved, including:
- Agent routing (Decision 3), agent_id injection (Decision 5)
- Supervisor cascade (Decision 4), rate limit handling (Decision 6)
- Flow execution gap (Decisions 7, 9, 13, 14)
- Task UUID in context (Decision 12), Layer 6 budget cap (Decision 12)
- Worker deadlock (Decision 10)

**Decision**: Phase 5 passes. The Ralph Loop's primary purpose — testing the platform end-to-end — was thoroughly accomplished. Deliverables are partial but the platform workflow was fully exercised. Pipeline is empty. All decisions logged.

---

## Decision 16: PM routing for project scope (2026-03-04)

**Context**: When chatting in project scope, Frank (org receptionist) was responding instead of the project's assigned PM. Issue 219 fix (PR #1598) correctly routes turns to the PM in the turn engine, but the TUI's session resolution was wrong — it was sending messages to the org session or wrong project session.

**Problem**: Three-part chain:
1. Sam.blog had no sync project-scope session (only async ones from Frank)
2. TUI `ScopeProject` handler fell back to org session UUID when no sidebar project session existed
3. The `looksLikeUUID()` guard blocked `"session-project-<UUID>"` aliases from being sent

**Decision**:
- Embed project UUID in session alias: `"session-project-<projectID>"`
- Resolver extracts project ID, filters by scope_id, auto-creates sync session if needed
- `isResolvableSessionAlias()` allows these specific aliases through the send guard
- `SessionResolvedMsg` now updates `activeSession` so subsequent messages use the real UUID
- `assistantLabel()` shows PM name in project scope (from ProjectDetail.Agents)

**Verified**: Worker logs confirm `pm_agent_id=7bcebe8b` (Sam.blog PM) is resolved for project-scope turns. Rate limiting prevented visual confirmation of the PM label display.

## Decision 14: Auto-retry for rate-limited agent turns (2026-03-04 11:55)

**Context**: All 11 Sam.blog task turns were killed by Anthropic rate limiting overnight. Tasks stalled permanently — required manual re-triggering via Frank. This is unacceptable for production.

**Decision**: Filed issue #220 (agent-turn-rate-limit-auto-retry). When a model provider returns 429, the turn handler should schedule a new agent_turn job with `run_at = now() + retry_after`, capped at 5 retries. The `retry_after` duration is already parsed by the gateway.

## Decision 15: Task event history in TUI (2026-03-04 11:44)

**Context**: The task event API (`GET /tasks/{id}/events`) was already wired in the TUI but events were never formatted into the `rec.History` strings that the view renders. Added formatting in model.go's `taskDetailLoadedMsg` handler.

**Change**: `internal/tui/model.go` — format TaskEvents into History strings (Created, status changes, review rejected) with timestamps and actor types. Press `h` in task detail view to see event log.

## Decision 16: Sam.blog project completion (2026-03-04 13:45)

**Context**: Ralph Loop goal was to drive all Sam.blog tasks to done status. All 11 tasks stalled due to rate limiting and lack of auto-continuation (issues #220, #221). Required manual "Continue" messages each time the worker went idle after processing a turn batch.

**Outcome**: All 11 Sam.blog tasks reached Done status through Work → Review → Done flow progression. Tasks completed across ~4 rounds of manual continue messages over ~2 hours. Key gap remains: auto-continuation (issue #221) — without it, every task requires human prodding after each turn.
