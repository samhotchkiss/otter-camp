# 080: Security and Observability Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 04 §AuditEvents, doc 08 §SecretScrubbing, doc 13 §Retention, doc 13 §SecretInvariants, doc 13 §RequestIDPropagation, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 008, 009, 024, 063 |
| Blocks | 089 |

## Scope

Integration test suite for security and observability cross-cutting concerns: secret
scrubber invariants (5 invariants — never in prompts/logs/API/audit/memory), audit event
completeness for key domain actions, rate limiter behavior under load, request_id
propagation through all layers, and retention job correctness (verify rows are deleted
after their retention period). All tests use a real PostgreSQL database via `testdb.New(t)`.

### Must build

**Test file:** `internal/security/scrubber_integration_test.go`

**Test file:** `internal/security/audit_integration_test.go`

**Test file:** `internal/observability/retention_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/security.go`:
- `MakeSecret(t, db, orgID, slug, value)` — creates a `secret` row with the given
  plaintext value (encrypted at rest); returns slug
- `MakeAuditEvent(t, db, orgID, action, principalType, principalID)` — inserts an
  audit_event row directly; returns ID

**Test scenarios in scrubber_integration_test.go:**

`TestScrubber_Invariant1_NotInPrompts` — invoke a model gateway request where a known
secret value is present in the input context (simulated by injecting it into the prompt
assembly layer); assert the scrubber removes it before the prompt is sent to the mock
provider; `model_invocation.prompt_ref` content (if stored) does not contain the raw value.

`TestScrubber_Invariant2_NotInAPIResponse` — create an API response that would normally
include a secret field (e.g., transport_config with inline credentials); assert the API
response body has the secret value replaced with `[REDACTED]`; the raw value is never
returned to the HTTP client.

`TestScrubber_Invariant3_NotInAuditEvent` — trigger an action that generates an audit
event; ensure the audit_event row's `metadata` jsonb field does not contain any raw
secret values; the scrubber is applied before the audit event is written.

`TestScrubber_Invariant4_NotInLogs` — enable request logging; make a request that passes
through a secret value; assert the structured log output does not contain the raw secret
(use a log capture hook in the test); `[REDACTED]` appears in the log instead.

`TestScrubber_Invariant5_NotInMemory` — memory extraction pipeline receives a message
containing a known secret pattern (e.g., a value matching the format of an API key);
Stage 0 garbage rejection catches it OR the scrubber prevents it from being stored;
no `memory` row contains the raw secret value.

`TestScrubber_KnownPatterns` — test that the scrubber recognizes multiple known secret
patterns: (1) values matching known stored secret slugs, (2) API key format patterns
(sk-ant-..., sk-...), (3) bearer token patterns; all are replaced with `[REDACTED]`.

`TestScrubber_NoFalsePositives` — normal text that happens to contain a substring like
"key" or "token" is NOT scrubbed; the scrubber only acts on known patterns and registered
secret values; assert normal text passes through unchanged.

**Test scenarios in audit_integration_test.go:**

`TestAudit_Login_Recorded` — successful login produces `audit_event` with action='login',
principal_type='human_user'; failed login produces action='login_failed'; `organization_id`,
`ip_address`, `user_agent` fields populated.

`TestAudit_APIKey_Issuance` — POST /v1/api-keys; `audit_event` row with
action='api_key_created'; the raw API key is NOT in the audit event metadata; only the
key ID and name.

`TestAudit_AgentTransition_Recorded` — agent lifecycle transition (draft → active);
audit_event with action='agent_activated'; principal_type and principal_id correctly set
to the user who triggered the transition.

`TestAudit_PolicyDecision_Recorded` — capability gate deny decision; audit_event with
action='capability_denied'; `capability` field in metadata; `policy_layer` that triggered
the deny is recorded.

`TestAudit_RunCreated_Recorded` — control plane run created; audit_event with
action='run_created'; `run_id` in metadata; `organization_id` correct.

`TestAudit_SecretAccess_Recorded` — GET /v1/secrets or secret slug resolution;
audit_event with action='secret_accessed'; secret slug in metadata (not the value);
principal_id of the accessor.

`TestAudit_OrganizationIsolation` — audit events from org A are NOT returned when
querying as org B user (if audit events are queryable via API); isolation enforced at the
query layer.

`TestAudit_DelegatedAction` — agent performs action on behalf of human user (via
`delegated_by` field); audit_event has correct principal_type='agent',
principal_id=agent_id, delegated_by_type='human_user', delegated_by_id=user_id.

**Test scenarios in retention_integration_test.go:**

`TestRetention_ChatMessages_90Days` — create chat_message rows with `created_at` 91 days
ago (use clock.Fake or direct DB insert with past timestamp); run the daily retention job;
assert messages older than 90 days are deleted; messages within 90 days are NOT deleted.

`TestRetention_RunRecords_90Days` — create `run` rows, `run_step` rows, `run_attempt`
rows 91 days old; run retention job; assert these rows are deleted; verify cascade
deletes `run_event`, `run_artifact` rows for the deleted runs.

`TestRetention_ModelInvocations_90Days` — create `model_invocation` rows 91 days old;
run retention job; assert rows older than 90 days are deleted and rows within 90 days
are preserved. `RetentionModelInvocationDays = 90` (ISSUE #21 resolved).

`TestRetention_DomainEvents_90Days` — create `domain_event` rows 91 days old; run
retention job; events older than 90 days deleted; events within 90 days preserved.

`TestRetention_AuditEvents_1Year` — create `audit_event` rows 13 months old; run
retention job; events older than 1 year deleted; events within 1 year preserved.

`TestRetention_ArchivedMemories_1Year` — create `memory` rows with `is_archived=true`
and `archived_at` 13 months ago; run retention job; archived memories older than 1 year
deleted; active memories NOT deleted regardless of age.

`TestRetention_TraceSpans_7Days` — create `trace_span` rows 8 days old; run retention
job; rows older than 7 days deleted; rows within 7 days preserved; partition pruning
works (spans are partitioned by day).

`TestRetention_Idempotent` — run the retention job twice on the same dataset; second run
finds nothing to delete (no double-delete errors, no panic); row counts are identical
after both runs.

`TestRequestID_Propagation` — make an HTTP request without X-Request-ID header; server
generates one; response contains X-Request-ID header; structured log entries for the
request all contain the same request_id; if a downstream service call is made, the
request_id is forwarded.

`TestRequestID_ClientProvided` — make request with X-Request-ID: my-test-id-123; server
uses the client-provided ID; all log entries contain my-test-id-123; response header
echoes it back.

### Must NOT build

- E2E tests for security scanning (task 089 CI pipeline)
- Performance/load tests for rate limiting
- Tests for secret encryption/decryption internals (those are in task 009 unit tests)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/security/... ./internal/observability/... -tags integration`
- [ ] All 5 scrubber invariants have a dedicated test scenario
- [ ] `TestScrubber_NoFalsePositives` passes at least 5 normal text samples through without scrubbing
- [ ] `TestRetention_ModelInvocations_90Days` deletes rows older than 90 days and preserves rows within 90 days
- [ ] `TestRetention_RunRecords_90Days` verifies CASCADE deletes for run_step, run_attempt, run_event, run_artifact
- [ ] `TestAudit_SecretAccess_Recorded` verifies the secret slug is in audit metadata but the plaintext value is NOT
- [ ] `TestRequestID_Propagation` asserts the same request_id appears in at least 3 structured log lines

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestScrubber_Invariant1_NotInPrompts`
- `TestScrubber_Invariant2_NotInAPIResponse`
- `TestScrubber_Invariant3_NotInAuditEvent`
- `TestScrubber_Invariant4_NotInLogs`
- `TestScrubber_Invariant5_NotInMemory`
- `TestScrubber_KnownPatterns`
- `TestScrubber_NoFalsePositives`
- `TestAudit_Login_Recorded`
- `TestAudit_APIKey_Issuance`
- `TestAudit_AgentTransition_Recorded`
- `TestAudit_PolicyDecision_Recorded`
- `TestAudit_RunCreated_Recorded`
- `TestAudit_SecretAccess_Recorded`
- `TestAudit_OrganizationIsolation`
- `TestAudit_DelegatedAction`
- `TestRetention_ChatMessages_90Days`
- `TestRetention_RunRecords_90Days`
- `TestRetention_ModelInvocations_90Days`
- `TestRetention_DomainEvents_90Days`
- `TestRetention_AuditEvents_1Year`
- `TestRetention_ArchivedMemories_1Year`
- `TestRetention_TraceSpans_7Days`
- `TestRetention_Idempotent`
- `TestRequestID_Propagation`
- `TestRequestID_ClientProvided`

**E2E tests:** None — covered by tasks 082 and 089.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- Clock: injected `clock.Fake` for retention age calculations
- Log output: captured via a test log hook (structured logger must support test output capture)
- Model gateway: `MockProviderServer` only for scrubber invariant tests
- Object storage: real filesystem adapter (temporary directory)

**ISSUE #21 (RESOLVED):**
`model_invocation` retention is 90 days. `TestRetention_ModelInvocations_90Days` tests
the single correct value. No parameterization or `t.Skip` needed.

**Audit event coverage principle:**
These tests are not exhaustive — they verify the most critical audit events. The
acceptance criterion for completeness is that every state-mutating API endpoint in the
system emits at least one audit_event. A full audit completeness check (scan all handler
code paths) is part of the CI pipeline task 089.

**Log capture hook:**
For `TestScrubber_Invariant4_NotInLogs`, use the zerolog test output hook (or equivalent
for the chosen logger) that captures log entries to a buffer during the test. Assert the
buffer does not contain the raw secret value.

**trace_span retention:**
`TestRetention_TraceSpans_7Days` depends on the trace_span partition pruning behavior
from task 063. If trace_span uses range partitioning by day, the retention job drops
entire partitions (ALTER TABLE ... DETACH PARTITION ... DROP). Test this by verifying the
row count is 0 for spans older than 7 days after the job runs.
