# 075: Model Gateway Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1-2 days) |
| Spec refs | doc 07 §ProviderRouting, doc 07 §FallbackChain, doc 07 §PriorityQueue, doc 07 §TokenRollup, doc 07 §ModelProfileScopeHierarchy, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 010, 035, 036, 037, 062 |
| Blocks | 089 |

## Scope

Integration test suite for the model gateway: provider connection selection (health-aware),
fallback chain on provider failure, priority queue ordering and soft preemption, token
rollup accuracy, and model profile scope resolution hierarchy. All tests use a real
PostgreSQL database via `testdb.New(t)`. Model provider HTTP endpoints are replaced with
in-process test doubles that return scripted responses (no real API calls; no network
required).

### Must build

**Test file:** `internal/modelgw/routing_integration_test.go`

**Test file:** `internal/modelgw/rollup_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/modelgw.go`:
- `MakeProvider(t, db, opts)` — creates `model_provider` + `provider_connection` rows;
  returns IDs
- `MakeModelProfile(t, db, orgID, providerID)` — creates `model_profile` row with
  logical_profile_id; returns profile
- `MakeProfileAssignment(t, db, scopeType, scopeID, profileID)` — creates
  `model_profile_assignment` row
- `MockProviderServer(t, fixture)` — starts an in-process HTTP server that returns
  scripted model responses; returns base URL for use in provider_connection config

**Test scenarios in routing_integration_test.go:**

`TestRouting_HealthAwareSelection` — create 2 provider connections: conn-A (healthy),
conn-B (healthy); mock conn-A to return 500 on the next call (degraded); gateway selects
conn-B for the following request; assert `model_invocation` row records the correct
connection ID.

`TestRouting_FallbackChain_OnFailure` — create profile with fallback_profile_id chain:
profile-A → profile-B → profile-C; mock profile-A provider to fail; gateway tries
profile-B; mock profile-B to fail; gateway tries profile-C; profile-C succeeds; assert
3 `model_invocation` rows (or 1 with attempt chain recorded in metadata).

`TestRouting_FallbackChain_Exhausted` — all providers in fallback chain fail; gateway
returns error to caller with `model.all_providers_failed` code; no successful invocation
row; error logged.

`TestRouting_HealthState_DegradedTransition` — mock provider starts returning 429s;
health monitor detects rate_limited state; gateway stops routing to that provider; health
state in `provider_connection` row transitions to 'rate_limited'; recovery check
re-enables after mock returns 200.

`TestPriorityQueue_OrderingUnderLoad` — submit 4 concurrent requests: 1 sync_interactive,
1 sync_system, 1 async_agent, 1 async_system; global concurrency slot limit = 1 (for
test determinism); assert requests are processed in priority order:
sync_interactive > sync_system > async_agent > async_system.

`TestPriorityQueue_SoftPreemption` — submit 1 async_agent request; while it is in flight,
submit 1 sync_interactive request; if slots are exhausted, the async request is soft-paused
(most-recently-started async); sync_interactive executes next; verify ordering via
`model_invocation` row timestamps.

`TestPriorityQueue_Timeout_AsyncSystem` — submit async_system request; mock provider
takes longer than the queue timeout for async_system tier; request times out; caller
receives timeout error; slot released.

`TestProfileScope_FlowNodeOverridesAgent` — create org with model profile assignment at
org level (profile-org); project level (profile-project); agent level (profile-agent);
flow_node level (profile-node); invoke model within a flow_node execution context;
assert profile-node is selected (flow_node scope wins — highest priority in hierarchy).

`TestProfileScope_AgentOverridesProject` — same setup but no flow_node assignment; invoke
with agent context; assert profile-agent is selected.

`TestProfileScope_OrgFallback` — no project/agent/flow_node assignments; invoke; assert
profile-org is selected.

`TestProfileScope_NoAssignment_Fails` — no assignments at any level; invoke; gateway
returns `model.no_profile_found` error (no hidden default per doc 07).

**Test scenarios in rollup_integration_test.go:**

`TestTokenRollup_InvocationRecord` — complete a model invocation via mock provider that
returns token counts (input_tokens=100, output_tokens=50); assert `model_invocation` row
has correct `input_tokens`, `output_tokens`, `total_tokens`; assert `run_attempt` row
(if present) has token counts denormalized.

`TestTokenRollup_DailyAggregation` — create 5 model_invocation rows for the same day
and org; run the daily rollup job; assert one `model_usage_rollup` row exists for that
day/org combination; `total_tokens` equals the sum of all invocation tokens.

`TestTokenRollup_GroupByProvider` — invocations on 2 different provider connections;
rollup job runs; 2 `model_usage_rollup` rows: one per provider; GET /v1/usage?group_by=provider
returns grouped results.

`TestTokenRollup_GroupByProject` — invocations attributed to 3 different projects;
rollup produces 3 project-grouped rows; GET /v1/usage?group_by=project returns correct
breakdown.

`TestPromptCapture_ToObjectStorage` — complete a model invocation; org redaction policy
allows capture; prompt and response body saved to object storage; `model_invocation.prompt_ref`
and `response_ref` fields set to storage keys; content retrievable from storage.

`TestPromptCapture_RedactionPolicy` — org has `redact_prompts=true`; model invocation
completes; `model_invocation.prompt_ref` is null (not stored); response_ref also null.

`TestInvocationPurpose_Routing` — invoke with `invocation_purpose='summarization'`;
gateway selects the Haiku-class system profile (not the agent's profile); invocation row
records correct purpose.

### Must NOT build

- E2E tests for model gateway (task 087)
- Real network calls to Anthropic/OpenAI (all providers use `MockProviderServer`)
- Load/performance benchmarks

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/modelgw/... -tags integration`
- [ ] `MockProviderServer` is in-process (no external network dependencies); tests pass in CI without API keys
- [ ] `TestPriorityQueue_OrderingUnderLoad` sets the global concurrency slot limit to 1 to make ordering deterministic
- [ ] `TestProfileScope_NoAssignment_Fails` verifies the error code is `model.no_profile_found` (not a panic)
- [ ] `TestTokenRollup_DailyAggregation` verifies the rollup sum is arithmetically correct
- [ ] `TestPromptCapture_ToObjectStorage` uses the real filesystem object storage adapter (not mocked)
- [ ] `TestRouting_FallbackChain_Exhausted` asserts no `model_invocation` success row exists after all providers fail

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestRouting_HealthAwareSelection`
- `TestRouting_FallbackChain_OnFailure`
- `TestRouting_FallbackChain_Exhausted`
- `TestRouting_HealthState_DegradedTransition`
- `TestPriorityQueue_OrderingUnderLoad`
- `TestPriorityQueue_SoftPreemption`
- `TestPriorityQueue_Timeout_AsyncSystem`
- `TestProfileScope_FlowNodeOverridesAgent`
- `TestProfileScope_AgentOverridesProject`
- `TestProfileScope_OrgFallback`
- `TestProfileScope_NoAssignment_Fails`
- `TestTokenRollup_InvocationRecord`
- `TestTokenRollup_DailyAggregation`
- `TestTokenRollup_GroupByProvider`
- `TestTokenRollup_GroupByProject`
- `TestPromptCapture_ToObjectStorage`
- `TestPromptCapture_RedactionPolicy`
- `TestInvocationPurpose_Routing`

**E2E tests:** None — covered by task 087.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- Model provider HTTP: `MockProviderServer` in-process test double (scripted responses)
- Object storage: real filesystem adapter (temporary directory, cleaned up with t.Cleanup)
- Clock: injected `clock.Fake` for health state transition timing tests

**MockProviderServer design:**
The mock server should implement the minimal Anthropic Messages API shape:
```go
type MockProviderServer struct {
    t        *testing.T
    handlers []MockHandler  // scripted per-call responses
    callCount int
}
// Each call pops the next handler; if exhausted, returns 500
```
This allows `TestRouting_FallbackChain_OnFailure` to script exactly which calls fail.

**ISSUE #18 (run_attempt_id for agent turn-loop calls):**
`TestTokenRollup_InvocationRecord` should test both cases: invocation with `run_attempt_id`
set (worker-domain call) and without (chat turn-loop call). Add a `// TODO(issue-18):`
comment on the nullable case noting the ambiguity.

**ISSUE #21 (RESOLVED):** `model_invocation` retention is 90 days (task 080).
Rollup integration tests here verify accuracy of rollup computation only.

**Concurrency test determinism:**
`TestPriorityQueue_OrderingUnderLoad` and `TestPriorityQueue_SoftPreemption` rely on
controlled concurrency. Set global slots to 1 and use a channel-based mock provider that
blocks until the test signals it, ensuring deterministic ordering.
