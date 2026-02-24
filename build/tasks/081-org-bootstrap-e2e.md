# 081: Org Bootstrap E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 14 §BootstrapSequence, doc 21 §E2ETests, doc 12 §TestReset |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

E2E test scenario for a fresh OtterCamp install bootstrap. Spins up a full OtterCamp
instance in `OTTERCAMP_MODE=test`. Uses only the `ottercamp` CLI binary and REST API
calls over HTTP — no internal Go APIs. Verifies the 10-step idempotent bootstrap sequence
(doc 14) completes correctly, that all required seed entities exist, and that a second
bootstrap run is a no-op.

### Must build

**Test file:** `e2e/bootstrap_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /v1/test/reset` before the scenario begins. The test binary
starts `ottercamp serve` as a subprocess and waits for `/v1/health` to return 200 before
proceeding.

Helper utilities used from `e2e/testutil/`:
- `StartServer(t)` — starts `ottercamp serve` subprocess; returns base URL; defers
  `server.Stop(t)` for cleanup
- `ResetState(t, baseURL)` — calls `POST /v1/test/reset` and asserts 204
- `AdminToken(t, baseURL)` — calls `POST /v1/auth/login` with bootstrap admin credentials
  and returns a Bearer token
- `GET(t, baseURL, path, token)` — performs an authenticated GET; returns body bytes and
  status code
- `POST(t, baseURL, path, token, body)` — performs an authenticated POST; returns body
  bytes and status code

**Scenario: `TestBootstrap_FreshInstall`**

Step 1 — Reset state:
```
POST /v1/test/reset
→ 204 No Content
```

Step 2 — Run bootstrap:
```
ottercamp bootstrap --server http://localhost:4110
→ exit code 0
→ stdout contains "Bootstrap complete"
```

Step 3 — Check health:
```
ottercamp health --server http://localhost:4110
→ exit code 0
→ stdout contains "healthy" (case-insensitive)
GET /v1/health
→ 200 { "status": "healthy" }
```

Step 4 — Verify organization created:
```
GET /v1/orgs/current
→ 200
→ body.data.id is non-empty UUID
→ body.data.name is non-empty string
```

Step 5 — Verify Frank (Chief of Staff) agent exists and is active:
```
GET /v1/agents?name=Frank
→ 200
→ body.data[0].lifecycle_status == "active"
→ body.data[0].agent_class == "staff"
→ body.data[0].role is non-empty
```

Step 6 — Verify default PM agent exists and is active:
```
GET /v1/agents?role=pm
→ 200
→ body.data length >= 1
→ body.data[0].lifecycle_status == "active"
```

Step 7 — Verify default capability policies seeded:
```
GET /v1/control/policies?policy_layer=instance
→ 200
→ body.data length >= 1
→ body.data[0].policy_layer == "instance"

GET /v1/control/policies?policy_layer=org
→ 200
→ body.data length >= 1
```

Step 8 — Verify at least one model profile assignment exists at org scope:
```
GET /v1/model/assignments?scope_type=organization
→ 200
→ body.data length >= 1
→ body.data[0].scope_type == "organization"
→ body.data[0].profile_id is non-empty string
```

Step 9 — Verify audit event for bootstrap was written:
```
GET /v1/audit?action=bootstrap_complete
→ 200
→ body.data length >= 1
→ body.data[0].action == "bootstrap_complete"
```

**Scenario: `TestBootstrap_Idempotent`**

Runs bootstrap a second time and asserts no duplicate entities are created:

Step 1 — Reset state and run first bootstrap:
```
POST /v1/test/reset
ottercamp bootstrap --server http://localhost:4110
→ exit code 0
```

Step 2 — Capture entity counts:
```
GET /v1/agents → record count N1
GET /v1/control/policies?policy_layer=instance → record count P1
GET /v1/model/assignments?scope_type=organization → record count A1
```

Step 3 — Run bootstrap again:
```
ottercamp bootstrap --server http://localhost:4110
→ exit code 0
→ stdout contains "Bootstrap complete" or "Already bootstrapped"
```

Step 4 — Verify counts unchanged:
```
GET /v1/agents → count == N1
GET /v1/control/policies?policy_layer=instance → count == P1
GET /v1/model/assignments?scope_type=organization → count == A1
```

**Scenario: `TestBootstrap_TestReset_ProducesCleanState`**

Step 1 — Bootstrap, create an extra agent:
```
POST /v1/test/reset
ottercamp bootstrap
POST /v1/agents { name: "ephemeral-test-agent", agent_class: "staff" }
→ 201
```

Step 2 — Reset state:
```
POST /v1/test/reset
→ 204
```

Step 3 — Re-bootstrap and verify the extra agent is gone:
```
ottercamp bootstrap
GET /v1/agents?name=ephemeral-test-agent
→ 200
→ body.data length == 0
```

### Must NOT build

- UI or TUI interactions
- Any test that calls internal Go package functions directly
- Tests that use a mock database — full PostgreSQL instance required

## Acceptance Criteria

- [ ] `TestBootstrap_FreshInstall` passes: `ottercamp bootstrap` exits 0, health returns healthy
- [ ] Frank agent is verified active with `lifecycle_status == "active"` and `agent_class == "staff"`
- [ ] At least one PM agent exists with `lifecycle_status == "active"`
- [ ] `GET /v1/control/policies?policy_layer=instance` returns at least 1 row
- [ ] `GET /v1/control/policies?policy_layer=org` returns at least 1 row
- [ ] `GET /v1/model/assignments?scope_type=organization` returns at least 1 row
- [ ] Audit event with `action == "bootstrap_complete"` exists
- [ ] `TestBootstrap_Idempotent` passes: second bootstrap produces identical entity counts
- [ ] `TestBootstrap_TestReset_ProducesCleanState` passes: entity created before reset is absent after reset + re-bootstrap
- [ ] Full scenario completes in under 90 seconds

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestBootstrap_FreshInstall` — full 9-step verification as described above
- `TestBootstrap_Idempotent` — second bootstrap run produces same entity counts
- `TestBootstrap_TestReset_ProducesCleanState` — reset wipes ephemeral entities; re-bootstrap restores only seed data

## Implementer Notes

**Test binary and server startup:**
The E2E tests require the `ottercamp` binary to be pre-built (by `make build`) before
the test suite runs. The `StartServer(t)` helper must wait for `GET /v1/health` to return
200 before allowing tests to proceed. Use a 30-second timeout with a 250ms poll interval.

**ISSUE #27 (RESOLVED — path prefix):**
All API calls use `/v1/` paths (doc 12). Doc 21 examples have been corrected.

**ISSUE #22 (RESOLVED — health endpoint path):**
Canonical paths: `GET /health/live` (liveness) aliased as `GET /health`; `GET /health/ready`
(readiness) aliased as `GET /ready`. Health endpoints are NOT under `/v1/`. The `StartServer`
helper should poll `GET /health/live` (or `/health`) for readiness. The E2E test health
assertion should use `GET /health/live` returning 200.

**OTTERCAMP_MODE=test:**
The server subprocess must be started with `OTTERCAMP_MODE=test` in its environment.
This enables the `POST /v1/test/reset` endpoint and activates deterministic model
responses (no real provider API calls are made).

**Bootstrap admin credentials:**
In test mode, `ottercamp bootstrap` seeds a known admin user. The `AdminToken` helper
uses those known credentials. The username and password are fixed test constants
(e.g., `admin@localhost` / `test-bootstrap-password`). These values must match what
`ottercamp bootstrap` seeds in `OTTERCAMP_MODE=test`.

**ISSUE #2 (bootstrap sequence doc discrepancy):**
Doc 14's 10-step sequence is authoritative. This test verifies the outcomes of that
sequence, not doc 04's stale 7-step sequence.
