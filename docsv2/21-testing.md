---
## Summary

This spec defines OtterCamp V2's testing strategy: how every component is tested, how tests are organized, and how automated testing uses the real CLI and API to verify the system works end-to-end. The core principle is that tests are not optional — every feature ships with full-coverage unit tests, and every user-facing flow is covered by automated end-to-end tests that exercise the real system through its public interfaces.

The architecture has three layers. **Unit tests** cover every package and module in isolation with full coverage — mocking external boundaries (model providers, MCP servers, filesystem for non-system tests) and testing the important logic: policy evaluation, tool resolution, prompt assembly, memory retrieval, flow progression, event emission. **Integration tests** run against a real PostgreSQL database with real schema migrations, testing service interactions, database queries, event bus delivery, and model gateway routing with recorded provider responses. **End-to-end tests** spin up a complete OtterCamp instance in test mode and exercise it through the CLI (`ottercamp` commands) and REST API — creating projects, creating tasks, chatting with agents, having agents interact with the filesystem and browser, and verifying outcomes through API assertions.

Test mode is controlled by a single configuration flag: `OTTERCAMP_MODE=test`. Test mode enables: deterministic model responses (doc 07), a state reset API for cleaning up between tests, synthetic time control for testing schedules and TTLs, and relaxed timeouts for CI environments. Test mode does NOT disable auth, policy evaluation, sandboxing, control plane enforcement, or audit logging — those are the things being tested. A test instance is functionally identical to a production instance except for the model response layer and test infrastructure endpoints. The flag is read once at startup and cannot be changed at runtime.

---

# 21. Testing

## Purpose

Define the testing strategy, architecture, and practices for OtterCamp V2. This spec ensures that every component is tested, that the system is verified end-to-end through its public interfaces, and that testing infrastructure is a first-class part of the codebase — not an afterthought.

Testing is how we know the system works. Unit tests verify logic. Integration tests verify component interactions. End-to-end tests verify the product. All three are required. All three run in CI. All three must pass before code merges.

## Principles

1. **Every feature ships with tests.** No exceptions. A feature without tests is not done. Unit tests for logic, integration tests for persistence and service boundaries, E2E tests for user-facing flows.

2. **Full coverage on unit tests.** Every package has unit test coverage. Coverage gaps are tech debt — tracked and resolved, not ignored. Critical paths (policy evaluation, tool resolution, prompt assembly, flow progression) have exhaustive case coverage.

3. **Test through the public interface.** End-to-end tests use the CLI and REST API — the same interfaces a human or external system would use. They do not reach into internal APIs, bypass auth, or skip policy evaluation. If the test cannot do it, neither can a user.

4. **Test instances are real instances.** A test instance runs the full stack: real PostgreSQL, real schema, real auth, real policy evaluation, real control plane, real tool execution. The only substitution is model responses (deterministic/recorded) and test infrastructure endpoints. If it works in test mode, it works in production.

5. **Tests are deterministic.** Flaky tests are bugs. Model responses are deterministic (doc 07). Time-dependent tests use synthetic time. Random data uses seeded generators. Tests that fail intermittently are fixed immediately.

6. **Tests run fast.** Unit tests complete in seconds. Integration tests complete in low minutes. E2E tests complete in minutes, not hours. Slow tests get optimized or restructured. CI pipeline has strict time budgets per stage.

## Test Mode Configuration

### The `OTTERCAMP_MODE` Flag

Every OtterCamp instance runs in one of two modes, set via environment variable:

```
OTTERCAMP_MODE=production    # default — full production behavior
OTTERCAMP_MODE=test          # enables test infrastructure
```

The mode is read once at startup and stored in memory. It cannot be changed at runtime. If `OTTERCAMP_MODE` is not set, the instance defaults to production mode.

### What Test Mode Enables

Test mode adds infrastructure for automated testing without weakening the system under test:

**Deterministic model responses.** All model profiles are automatically set to deterministic mode (doc 07) — temperature 0, fixed seed, response caching. For E2E tests, a response fixture system allows pre-recorded responses to be loaded and served without calling a real model provider. The fixture system maps prompt patterns to responses, supporting both exact match and fuzzy match by conversation shape.

**State reset API.** A `POST /test/reset` endpoint that:
- Drops all data except the schema (truncates all tables).
- Re-runs bootstrap to restore the starter trio, default skills, model profiles, flow templates, org policy, and General session.
- Returns the org ID, user credentials, and API key for the fresh instance.

This endpoint does not exist in production mode. It is the foundation of test isolation — every E2E test starts from a clean, bootstrapped state.

**Synthetic time control.** A `POST /test/time/advance` endpoint that advances the system clock by a specified duration. Used for testing:
- Schedule triggers (cron-based task creation)
- Token budget period resets (daily/weekly/monthly)
- Memory consolidation jobs (sleep-time reflection)
- Session expiry and TTL-based temp agent cleanup
- Anomaly detection windows

The system clock in test mode is controlled by an internal clock abstraction. All time-dependent code reads from this abstraction, not directly from `now()`. In production mode, the abstraction returns the real system time.

**Test-only seeding endpoints.** `POST /test/seed/*` endpoints for injecting test fixtures:
- `/test/seed/memories` — bulk load memories with specific content, confidence, and source types
- `/test/seed/invocations` — create model invocation records for cost/budget testing
- `/test/seed/events` — inject domain events for testing event subscribers

**Relaxed timeouts.** CI environments may be slower than production hardware. Test mode applies a configurable multiplier (default 3x) to all timeouts: tool execution, model calls, turn duration, heartbeat intervals. Set via `OTTERCAMP_TEST_TIMEOUT_MULTIPLIER`.

### What Test Mode Does NOT Change

Test mode does not weaken or bypass any production behavior:

- **Auth is enforced.** Tests must authenticate. API key and session-based auth work identically.
- **Policy evaluation runs.** Capability checks, policy layers, allow/deny decisions all run. Tests verify these work correctly.
- **Sandboxing is active.** CLI execution and browser actions are sandboxed identically to production. Tests verify the sandbox works.
- **Control plane executes fully.** Run, RunStep, ToolExecution records are created. The broker processes tier 2 tools. Tests verify the audit trail.
- **Audit logging fires.** Domain events emit. Audit events record security-sensitive actions. Tests verify these events exist.
- **Database schema is identical.** Same migrations, same tables, same constraints. No test-only schema modifications.

The guarantee: if a test passes in test mode, the same operation will succeed in production mode (assuming equivalent model responses).

## Test Architecture

### Layer 1: Unit Tests

Unit tests verify individual functions, methods, and modules in isolation. They are fast (milliseconds per test), deterministic, and have no external dependencies.

**Scope:**
- Business logic: policy evaluation, capability matching, scope checks
- Tool resolution pipeline: each stage independently, then the full pipeline
- Prompt assembly: layer ordering, budget allocation, truncation, compression triggers
- Memory retrieval: query parsing, scoring, ranking, dedup filtering
- Flow progression: node transitions, rejection loops, visit counting
- Event mapping: domain state changes to correct event types
- Schema validation: JSON Schema validation for tool parameters
- Token counting: budget calculations, rollup aggregation

**Mocking boundaries:**
- Model provider API calls — always mocked in unit tests
- Database queries — mocked or use in-memory stores
- File system — mocked except for system tool tests
- External services (remote APIs, MCP servers, browser) — always mocked
- Time — use injectable clock

**Coverage expectations:**
- All packages: 90%+ line coverage minimum
- Critical paths (policy, tools, prompt assembly, flow, memory): 95%+ with branch coverage
- Coverage is measured and reported in CI. Coverage regression blocks merge.

**Naming and organization:**
- Test files live alongside source files: `policy.go` → `policy_test.go`
- Test names describe behavior: `TestPolicyEvaluation_DenyOverridesAllow`
- Table-driven tests for combinatorial cases (policy layers, tool resolution, prompt budget allocation)

### Layer 2: Integration Tests

Integration tests verify that components work correctly together, with real infrastructure.

**Scope:**
- Database: queries return correct results, constraints enforce integrity, migrations run cleanly
- Event bus: events emit on state changes, subscribers receive events, LISTEN/NOTIFY delivery
- Model gateway: routing selects correct connection, fallback chains trigger, invocations are recorded
- Auth: session creation, API key validation, org isolation, rate limiting
- Memory pipeline: extraction → scoring → normalization → embedding → storage → retrieval
- Tool execution: tier 1 scope checks, tier 2 broker path, RunStep creation

**Infrastructure:**
- Real PostgreSQL (in Docker, spun up by test harness or CI)
- Real schema migrations (tests run against a freshly migrated database)
- Recorded model responses (captured from real providers, replayed in tests)
- In-process API server (no network hop for most integration tests)

**Test isolation:**
- Each integration test suite gets its own database (created and dropped by the test harness)
- Database creation is fast: run migrations once, template the database, clone per suite
- No shared state between test suites

**Recorded model responses:**
- A `testdata/responses/` directory contains recorded provider responses organized by test scenario
- Each recording includes: the prompt hash, the provider response, token counts, and latency
- Recordings are committed to version control — they are test fixtures, not generated artifacts
- When a recording is missing, the test can optionally call the real provider (gated behind an env var: `OTTERCAMP_TEST_LIVE_MODELS=true`) and save the response for future replay

### Layer 3: End-to-End Tests

End-to-end tests exercise the complete system through its public interfaces: the CLI and REST API. They verify that the product works as a user would experience it.

**Infrastructure:**
- A real OtterCamp instance running in test mode (`OTTERCAMP_MODE=test`)
- Real PostgreSQL
- Real auth (tests create users, get API keys, authenticate requests)
- Deterministic model responses via fixtures
- The CLI binary (`ottercamp`) as a test client
- HTTP client for REST API assertions

**Test lifecycle:**
1. Before each test (or test suite): call `POST /test/reset` to reset state
2. Bootstrap verifies: starter trio exists, General session created, skills loaded
3. Test executes: CLI commands and/or API calls
4. Assertions: verify outcomes via API (query state, check events, inspect audit trail)
5. Cleanup: next test's reset handles this

### E2E Test Scenarios

The following scenarios are required for launch readiness. Each maps to a real user workflow.

**Bootstrap and First Use**
- Fresh install: bootstrap completes, starter trio operational, General session accessible
- `ottercamp bootstrap` is idempotent — running twice does not duplicate data
- First user can authenticate and receive API key
- Frank responds in the General session

**Chat and Conversation**
- Human sends message to Frank via API, receives streaming response
- Human @mentions Lori — Lori responds, listening eval runs
- Human @mentions Ellie — Ellie responds with memory query
- Cancel and steer: human cancels a streaming response, sends redirect
- Multi-turn: context from previous turns is present in agent responses

**Memory**
- Human tells Frank a preference ("I prefer short commit messages")
- Ellie extracts and stores the memory (verify `memory` table)
- In a later session, passive injection includes the memory in agent context
- `memory.query` tool returns relevant results
- JSONL import via CLI: `ottercamp memory import <path>` processes fixtures

**Project and Task Flow**
- Human asks Frank to create a project (via chat → `project.create` tool call)
- PM is assigned, staffed by Lori
- PM creates a task with flow template "Work + Review"
- Worker agent picks up task, creates branch, writes files (via `file.write`, `cli.execute`)
- Worker signals done (`flow.advance`)
- Reviewer reviews and approves (`flow.review_decision`)
- Task merges to main
- Full lifecycle verified via API: task status transitions, flow node executions, git state

**Agent Tool Execution**
- Tier 1 tool: agent reads project files — no RunStep, logged as chat message
- Tier 2 tool: agent writes file — RunStep created, policy evaluated, audit trail exists
- Policy denial: agent without CLI capability calls `cli.execute` — receives "not permitted"
- Communication draft: agent calls `email.compose` — draft staged in inbox, agent notified of staging

**System Integration**
- CLI execution: agent runs a shell command in project workspace, output captured
- File operations: agent reads, writes, and deletes files in project workspace
- Sandbox enforcement: agent cannot access files outside project workspace
- Browser: agent navigates to URL, extracts content, captures screenshot (with headless browser in test)

**Scheduling and Async**
- PM creates a recurring schedule (cron)
- Advance synthetic time past the cron trigger
- Verify task auto-created and agent picks it up
- Async execution: agent works autonomously without human input

**Auth and Security**
- Login with correct credentials — succeeds
- Login with wrong credentials — fails with rate limiting
- API key authentication — works for all endpoints
- Org isolation: two orgs on same instance cannot see each other's data
- Session expiry: advance time past session TTL, verify session invalid

**Cost Controls**
- Create a token budget with hard limit
- Seed invocations to approach the limit
- Verify next model call is denied when hard limit exceeded
- Verify soft limit triggers notification event but allows the call

**Error Recovery**
- Agent tool call times out — agent receives timeout error, adapts
- Model provider returns error — retry with backoff, then fallback
- Agent gets stuck — stuck task detection fires, PM notified

### CLI as Test Client

The `ottercamp` CLI is both a user tool and a test client. E2E tests invoke the CLI binary and verify its output:

```
# Bootstrap
ottercamp bootstrap --email test@example.com --password testpass123

# Chat
ottercamp chat send --session general "Create a new project called TestProject"

# Memory import
ottercamp memory import ./testdata/v1-export.jsonl

# Project operations (via API, but could also be CLI)
ottercamp project list
ottercamp task list --project TestProject
```

For complex assertions (checking database state, verifying events, inspecting audit trails), tests use the REST API directly. The CLI is for exercising user-facing workflows; the API is for verification.

### API as Test Client

REST API tests use a standard HTTP client. All requests include authentication (API key or session token).

```
# Create a chat turn
POST /api/sessions/{id}/turns
Authorization: Bearer <api_key>
{"content": "Hello Frank"}

# Verify project was created
GET /api/projects
Authorization: Bearer <api_key>
# Assert: response includes "TestProject"

# Check audit trail
GET /api/events?type=project.created
Authorization: Bearer <api_key>
# Assert: event exists with correct metadata
```

## Unit Test Domains

Organized by spec domain. Each domain lists the critical test cases that must exist.

### Policy Evaluation (doc 16)

- Policy layers apply in correct order (instance > org > project > agent)
- Deny at any layer overrides allow at lower layers
- Default-deny: capability not granted → denied
- Binary outcome only: no intermediate states
- Instance safety policy cannot be overridden

### Tool Resolution (doc 20)

- Stage 1: native + enabled external tools compose the universe
- Stage 2: allow/deny globs match correctly (`project.*` matches `project.create`)
- Stage 2: deny overrides allow
- Stage 3: flow node `tool_domains` deprioritizes but does not exclude
- Stage 4: tier 2 tools without capability are excluded
- Stage 4: tier 1 tools bypass capability gate
- Full pipeline: starter trio gets correct tool sets (Frank, Lori, Ellie defaults)
- Caching: tool set computed once per session, stable across turns

### Prompt Assembly (doc 05)

- 7 layers assembled in correct order
- Budget allocation respects layer priorities (tool descriptions lowest)
- Truncation drops lowest-priority content first
- Skills loaded based on activation rules (identity always, org defaults always)
- Memory injection: top-k results injected, dedup with explicit query
- Token counting is accurate across all layers

### Flow Progression (doc 03)

- Node transitions: work → review → done
- Rejection loops: review reject → back to work, visit counter increments
- Actor resolution: role → agent mapping
- Subtask creation within flow node execution
- Task status transitions: draft → queued → in_progress → review → done
- Dependency enforcement: blocked task cannot start until dependency resolves

### Memory Retrieval (doc 06)

- Four-stage retrieval: scope filter → taxonomy classification → subtree retrieval → relevance ranking
- Entity synthesis memories boost retrieval
- Trust tier caps: import (0.6), chat (0.8), human (1.0)
- Dedup: near-duplicate detection catches similar memories
- Passive injection excludes already-injected memories from `memory.query`

### Event System (doc 12)

- State changes emit correct event types
- Event payloads include required fields
- Subscriber delivery: events reach registered consumers
- Durable log: events persisted for replay

## Test Data and Fixtures

### Bootstrap Test Org

Every test starts from the standard bootstrap state (via `POST /test/reset`):
- One organization, one human user (owner)
- Starter trio: Frank, Lori, Ellie — active, with profiles and skills
- General session with correct participants
- Default model profiles, flow templates, skills, org policy
- One API key for the test user

This is identical to a fresh production install.

### Model Response Fixtures

Stored in `testdata/responses/` organized by scenario:

```
testdata/
  responses/
    chat/
      frank-greeting.json       # Frank's response to "Hello"
      lori-staffing.json        # Lori recommending agents for a project
      ellie-memory-query.json   # Ellie answering a memory question
    flow/
      worker-write-file.json    # Worker agent writing code
      reviewer-approve.json     # Reviewer approving work
    memory/
      extraction-from-chat.json # Ellie extracting a memory from conversation
```

Each fixture includes the prompt pattern (for matching) and the complete provider response. Fixtures are version-controlled and updated when agent behavior or prompt assembly changes.

### JSONL Import Fixtures

Test data for the memory import path:

```
testdata/
  import/
    valid-memories.jsonl        # Well-formed V1 export
    mixed-quality.jsonl         # Mix of high and low quality content
    malformed-lines.jsonl       # Some invalid JSONL lines (should skip, not crash)
    empty.jsonl                 # Empty file (edge case)
```

### Project Fixtures

Pre-built project configurations for E2E tests:

```
testdata/
  projects/
    simple-go-project/          # Small Go project with a few files
    typescript-project/         # TypeScript project with package.json
```

These are cloned into test workspaces for system integration tests (agent file operations, CLI execution, git operations).

## CI/CD Integration

### Pipeline Stages

```
1. Lint          → static analysis, formatting, vet
2. Unit Tests    → all packages, coverage report
3. Build         → compile binary, verify it starts
4. Integration   → database tests, service tests (PostgreSQL in Docker)
5. E2E           → full instance in test mode, CLI + API tests
6. Coverage Gate → fail if coverage drops below threshold
```

Stages 1-3 run in parallel where possible. Stages 4 and 5 both require the binary from stage 3 but are independent of each other and can run in parallel. Stage 6 requires stages 2, 4, and 5 to collect all coverage data.

### Time Budgets

- Lint: < 30 seconds
- Unit tests: < 2 minutes
- Build: < 1 minute
- Integration tests: < 5 minutes
- E2E tests: < 10 minutes
- Total pipeline: < 15 minutes

If any stage exceeds its budget, it is a signal that tests need optimization.

### Coverage Gates

- Overall: 90% line coverage minimum
- Critical packages (policy, tools, prompt, flow, memory): 95% minimum
- Coverage delta: no merge if coverage decreases by more than 1% from the base branch
- New files: must have tests (enforced by CI rule — new `.go` files without corresponding `_test.go` are flagged)

### Test Environment

CI runs tests in containerized environments:
- PostgreSQL: Docker container, ephemeral per run
- OtterCamp binary: built in the build stage, used in E2E stage
- Model responses: fixture-based (no live provider calls in CI)
- Browser: headless Chrome in Docker for browser integration tests
- No internet access during tests (except for explicitly gated live-model tests)

## Schema

### No Test-Specific Tables

Test mode does not add tables to the database schema. The schema is identical in test and production. Test infrastructure (reset, time control, seeding) operates through application-level APIs that manipulate existing tables.

The `OTTERCAMP_MODE` value is not stored in the database — it is an in-memory configuration read at startup. There is no database record of whether an instance is in test or production mode.

## Cross-Doc References

| Spec | Relevance |
|------|-----------|
| Doc 04 (Auth) | Auth tests: login, API keys, sessions, org isolation |
| Doc 05 (Agents) | Prompt assembly tests, starter trio verification |
| Doc 06 (Memory) | Memory pipeline tests, retrieval tests, import tests |
| Doc 07 (Models) | Deterministic mode, recorded responses, replay |
| Doc 08 (Deployment) | `OTTERCAMP_MODE` env var, Docker test environment |
| Doc 09 (MCP Integration) | External tool discovery and execution tests |
| Doc 11 (System) | CLI sandbox tests, browser tests |
| Doc 12 (API/Events) | API endpoint tests, event emission tests |
| Doc 13 (Security) | Audit trail tests, budget tests, anomaly detection tests |
| Doc 15 (Migration) | JSONL import tests |
| Doc 16 (Control Plane) | Policy evaluation tests, capability tests, execution lifecycle |
| Doc 20 (Tools) | Tool resolution pipeline tests, tier routing tests |

## Resolved Decisions

1. **Two modes: test and production.** Controlled by `OTTERCAMP_MODE` environment variable. Default is production. Read once at startup, immutable at runtime.

2. **Test mode does not weaken production behavior.** Auth, policy, sandboxing, control plane, and audit logging all run identically. Tests verify these systems work — disabling them would defeat the purpose.

3. **State reset via API.** `POST /test/reset` truncates all tables and re-bootstraps. This is the test isolation mechanism. Only available in test mode.

4. **Synthetic time control.** An injectable clock abstraction used by all time-dependent code. In test mode, time can be advanced via API. In production, it returns real system time.

5. **Deterministic model responses.** Test mode uses doc 07's deterministic mode (temperature 0, fixed seed) plus a fixture system for pre-recorded responses. No live provider calls in CI.

6. **Three test layers: unit, integration, E2E.** All required. All run in CI. All must pass before merge.

7. **Full unit test coverage.** 90% minimum across all packages, 95% for critical paths. Coverage regression blocks merge.

8. **E2E tests use CLI and API.** Tests exercise the real public interfaces. CLI for user workflows, API for assertions and verification. No internal API backdoors.

9. **No test-specific schema.** Database schema is identical in test and production. Test infrastructure is application-level, not schema-level.

10. **Model response fixtures are version-controlled.** Stored in `testdata/responses/`, committed to the repo. Updated when prompts or agent behavior change.

11. **CI time budget: 15 minutes total.** Individual stage budgets enforce test performance. Slow tests are bugs.

12. **Every feature ships with tests.** Non-negotiable. A feature PR without tests does not merge.

## Open Questions

- **Live model tests in CI**: should CI optionally run a subset of tests against real model providers (gated behind `OTTERCAMP_TEST_LIVE_MODELS=true` and an API key secret)? This catches provider API changes early but adds cost, latency, and flakiness.
- **Browser test infrastructure**: headless Chrome in CI adds complexity. Should browser E2E tests be a separate optional stage, or required for every merge?
- **Test data freshness**: model response fixtures become stale as prompts evolve. Should there be an automated job that periodically re-records fixtures against live providers?
- **Performance regression testing**: should CI track and alert on test duration regressions (e.g., a test that was 100ms now takes 2s)?
