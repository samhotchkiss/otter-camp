# Test 023: Audit & Compliance

**Section:** 22. Audit & Compliance
**Functionality list items:**
- Immutable audit trail (append-only)
- Audit events for: logins, logouts, role changes, API key lifecycle, policy changes, agent mutations
- Audit events filterable by action, date range, actor
- `GET /v1/audit` — list audit events (alias: `/v1/audit/events`)
- Audit events include: actor (human/agent/system), action, resource, timestamp, IP, outcome
- Retention: audit events retained per org policy (default: indefinite)
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

1. `GET /v1/audit` with session token
2. Applied filters: `?action=policy.created`, `?from=...&to=...`, `?action=auth.login`
3. Examined distinct action types in audit trail
4. Checked response schema fields

## API Response (sample event)

```json
{
    "id": "f9b3cd4a-...",
    "action": "policy.deleted",
    "event_type": "policy.deleted",
    "principal_type": "human",
    "principal_id": "efd84423-...",
    "target_type": "capability_policy",
    "target_id": "0c26deac-...",
    "created_at": "2026-02-26T08:01:21.037579-07:00"
}
```

## Distinct Action Types Found

```
bootstrap_complete
file_written
policy.created
policy.deleted
policy.updated
```

## Findings Per Item

**Immutable audit trail (append-only):**
- No DELETE endpoint exists for audit events (checked API surface)
- Events accumulate over time ✓
- Append-only by design (no evidence of update capability)
- Result: PASS (by design)

**Audit events for: logins, logouts, role changes, API key lifecycle, policy changes, agent mutations:**
- `auth.login` action: NOT recorded (tested `?action=auth.login` → empty)
- `auth.logout` action: NOT recorded
- Role changes: NOT recorded
- API key lifecycle: NOT recorded
- Policy changes: RECORDED (policy.created, policy.updated, policy.deleted) ✓
- Agent mutations: NOT recorded (no agent.created/updated events)
- File writes: RECORDED (file_written) ✓
- Bootstrap: RECORDED (bootstrap_complete) ✓
- Result: PARTIAL (policy changes yes; auth/key/role/agent events missing)

**Audit events filterable by action, date range, actor:**
- Filter by action: `?action=policy.created` → works ✓
- Filter by date range: `?from=...&to=...` → works ✓
- Filter by actor: no `?actor=` or `?principal_id=` parameter tested (may not exist)
- Result: PARTIAL (action + date filters work; actor filter unclear)

**GET /v1/audit and GET /v1/audit/events alias:**
- `GET /v1/audit` → returns audit data ✓ (requires session token, not API key)
- `GET /v1/audit/events` → alias works ✓
- Note: API key auth returns 403 "missing api key scope" — endpoint requires session auth
- Result: PASS

**Audit events include: actor, action, resource, timestamp, IP, outcome:**
- `principal_type` (human/agent) ✓
- `principal_id` ✓
- `action` ✓
- `target_type` + `target_id` (resource) ✓
- `created_at` (timestamp) ✓
- IP address: NOT in response ✗
- Outcome: NOT in response ✗
- Result: PARTIAL

**Retention per org policy:**
- Not tested (no retention policy API found)
- Events appear to be retained indefinitely (no TTL observed)
- Result: UNTESTED

## Issues Filed

- Issue #174 — Audit: missing login/logout/API key/role change/agent mutation events; no IP/outcome fields
