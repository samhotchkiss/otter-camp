# Ralph Loop Progress
Started: 2026-02-25T22:41
Last updated: 2026-02-26T02:50

## Session Goal
1. Full spec validation (every method/action in every spec doc) ✅ COMPLETE (iteration 2)
2. DB verification after each action ✅ COMPLETE
3. Memory system (extraction + insertion) ✅ VERIFIED (extraction working, synthesis blocked - see issue 126)
4. Context loading verification ✅ VERIFIED (memory query returns results, Frank uses tools correctly)
5. TUI testing ✅ (go build + go test pass)
6. Skills, browser control, secret storage - NOT DIRECTLY ACCESSIBLE via REST
7. Security audit ✅ COMPLETE (previous session)
8. UX flow improvements (50 improvements) ✅ COMPLETE (in issues/notes.md)
9. Monitoring dashboard ✅ Built at /tmp/ottercamp-dashboard/index.html

## Spec Validation Status (Iteration 2)
- [x] 02-chat.md - PASS (all routes work)
- [x] 03-projects-and-task-flow.md - PASS (all routes work)
- [x] 03a-shipping-and-delivery.md - PASS (schedules, environments, remotes work)
- [x] 04-auth-tenancy-and-identity.md - PASS (auth, sessions, API keys, /users/me alias added)
- [x] 05-agents-staff-and-temps.md - PASS (agents, templates, project assignments)
- [x] 06-memory.md - PASS (query works, extraction working, synthesis blocked)
- [x] 07-models-and-inference.md - PASS (providers, profiles, connections, usage)
- [x] 09-mcp-integration.md - PARTIAL (connections work, catalog empty - degraded connections)
- [x] 10-skills-integration.md - PASS (/v1/skills, /v1/projects/{id}/skills, /v1/skills/catalog all work)
- [x] 12-api-events-and-realtime.md - PASS (SSE stream at /v1/events/stream works, /v1/ws/negotiate works)
- [x] 13-security-observability-costs.md - PASS (/metrics exists, /audit works)
- [x] 16-agent-control-plane.md - PASS (all control plane routes work, cost summary now returns real data)
- [x] 20-tools-and-tool-policy.md - PASS (/v1/tools global listing added)

## Security Audit Results (iteration 1 - still valid)
- FIXED: API key scope enforcement
- FIXED: 4MB request body size limit
- FIXED: Email case normalization
- FIXED: Account lockout enumeration prevention
- FIXED: /metrics restricted to localhost
- FIXED: SSRF in MCP URLs

## Issues Found & Filed
- 120: Memory cold-start (FIXED)
- 121: /metrics - actually exists (CLOSED)
- 122: Audit events (CLOSED, /v1/audit/events alias added)
- 123: API key scopes (FIXED)
- 124: Cost summary always returned 0 (FIXED - now queries model_usage_rollup)
- 125: Trace spans never populated (FILED - TraceSpanService not wired)
- 126: Memory entity synthesis not running (FILED - SleepReflector not registered in worker)

## Endpoints Added This Loop (Iteration 2)
- GET /v1/agents/{id}/config → returns agent config subset
- GET /v1/agents/{id}/tools → returns 67 enabled tools
- GET /v1/skills → org-level skills
- GET /v1/projects/{id}/skills → project-scoped skills
- GET /v1/audit/events → alias for /v1/audit
- GET /v1/users/me → alias for /v1/auth/me
- GET /v1/tools → global list of all enabled tool definitions
- GET /v1/skills/catalog → alias for /v1/skills
- GET /v1/memory/consolidation-runs → alias for /v1/memory/compaction-runs

## Fixes Made This Loop (Iteration 2)
- Issue 124: GET /v1/control/cost/summary now queries model_usage_rollup (was run_attempt=0 rows)
- Model profiles: standard/haiku re-patched to gpt-4o-mini via OpenAI
- Memory query field: `query` (not `text`), `max_results` (not `top_k`)
- SSE path: /v1/events/stream (not /v1/events), requires ?scopes=<type>:<id>

## Commits Made This Loop (Iteration 2)
- 2b5de459: feat(agents): GET /v1/agents/{id}/config and GET /v1/agents/{id}/tools
- 895a5eb6: feat(skills): GET /v1/skills and GET /v1/projects/{id}/skills
- b9ce0253: feat(audit): GET /v1/audit/events alias
- 4032a5b3: fix(tests): update for new API signatures and auto-slug
- 7a770621: fix(control): cost summary queries model_usage_rollup
- 67bc0d96: feat(auth): GET /v1/users/me alias
- 37111633: feat(api): GET /v1/tools, /v1/skills/catalog, /v1/memory/consolidation-runs
- 13091f13: docs: file issues 125 and 126

## Fixes Made This Loop (Iteration 3/4)
- Memory compaction: SleepReflector wired with ThresholdDeduplicator (issue 126 partially fixed)
- Supervisor runaway: detectedStuckRuns now fails the original stuck run after recoverRun
  → stops infinite recovery run creation (was accumulating 6 new runs/minute = 2570+ total)
- Flow task kickoff (issue 102 FIXED): ensureFlowRun re-fetches execution after CreateRun to get
  session_id set by RouteRunToSession. Agent participant now added, kickoff message sent.
  VERIFIED: Frank responded with tool calls in new test flow task.
- run_mode=async in task queue run metadata → supervisor uses 5-min async threshold

## Commits Made This Loop (Iteration 3/4)
- cf19c4c2: feat(memory): wire SleepReflector in worker with ThresholdDeduplicator
- aa5d4675: fix(supervisor,task_queue): stop runaway run creation for stuck flow tasks
- a707272e: fix(task_queue): set run_mode=async for scheduler-triggered runs

## DB State (verified 2026-02-26T02:10)
- Runs: 2594 total (2570 supervisor/cancelled from cleanup, 13 completed, 7 failed)
- Chat messages: 1250 (up from 1199 - flow task test generated ~51 messages)
- Memories: 77 (all candidate, 7-day promotion hold by design)
- Memory entities: 0 (candidates still in hold period)
- Memory compaction runs: 5 (2 completed, 3 pending - waiting for age threshold)
- Tools enabled: 67
- Projects: 16
- Agents: 7

## Fixes Made This Loop (Iteration 5)
- Issue 125: TraceSpanService wired into LiveModelGateway (model.invoke spans)
  - Span per callProvider: ok on success, error on failure. TurnID as trace_id.
  - Attributes: model.name, provider.slug, connection.id, invocation.id, purpose, streaming
- Issue 127 (NEW+FIXED): Dangling assistant+tool_calls in conversation history
  - openAIMessages/anthropicMessages: retroactively remove assistant+tool_calls
    when a non-tool message appears before any tool_results were added
  - Fixes listening_eval 400 errors on sessions with interrupted tool dispatches
- trace_span partitions: manually created 2026-02-26 through 2026-03-02 (dev gap)
- Issue 128 (NEW): filed for trace_span partition gap on fresh start (daily job only creates future)

## Commits Made This Loop (Iteration 5)
- d6fb5335: fix(gateway): wire trace spans per model invocation, fix dangling tool_calls in history

## DB State (verified 2026-02-26T02:50)
- Trace spans: 16 (12 error from pre-fix, 4 ok from post-fix verification)
- Model invocations: 3 completed (recent, post-fix)
- Chat messages: 1279 (up from 1250)

## Known Remaining Issues
- Memory entity synthesis not running yet (issue 126) - waiting for 7-day candidate hold
- Issue 128: trace_span partition gap on fresh start (LOW priority)
- MCP catalog empty (degraded connections in dev)
- total_cost_microcents=0 (no pricing configured in model_provider)
- push.delivery.consumer "closed pool" errors in worker - cosmetic in dev
