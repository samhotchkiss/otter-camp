# 008: Audit Event Schema and Service

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | S (≤1 day) |
| Spec refs | doc 04 §AuditEvent, doc 04 §Delegation |
| Spec status | finished |
| Depends on | 005 |
| Blocks | 012 |

## Scope

Create the `audit_event` table DDL and migration, implement the `AuditEvent.Record()` helper
that is called throughout the system, and cover delegation patterns (principal + delegated_by).

Note: The dependency graph places `audit_event` at L3 because its polymorphic `principal_id`
can reference `agent` (an L2 table). However, the table's primary FK is to `organization` (L0),
`human_user` (L1), and `principal_id` is stored as a plain `uuid` with no SQL FK constraint
(application-layer polymorphic). The migration and repository can be created at L1 depth —
only the `agent` principal_type values will be unused until task 013 creates the `agent` table.

### Must build

**Migration:**
- `0010_audit_event.sql`

**`audit_event` table** (doc 04):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `event_type text not null` — e.g. `auth.login`, `auth.logout`, `user.created`, `agent.created`, `secret.accessed`, `policy.changed`
- `principal_type text not null check (principal_type in ('human', 'agent', 'system'))` — who performed the action
- `principal_id uuid not null` — no SQL FK (polymorphic; agent table doesn't exist at this layer)
- `delegated_by_type text check (delegated_by_type in ('human', 'agent', 'system'))` — null = no delegation
- `delegated_by_id uuid` — null = no delegation; must be non-null when `delegated_by_type` is set
- `target_type text` — e.g. `human_user`, `agent`, `secret`, `session`
- `target_id uuid` — no SQL FK (polymorphic)
- `metadata jsonb not null default '{}'` — event-specific context (e.g. IP address, changed fields)
- `created_at timestamptz not null default now()`
- Index: `(organization_id, created_at DESC)` — primary query pattern
- Index: `(organization_id, principal_id)` — for "all actions by this principal"
- Index: `(organization_id, target_type, target_id)` — for "all events affecting this entity"
- Partial index: `(organization_id, event_type, created_at DESC)` — for event type filtering
- CHECK constraint: `(delegated_by_type IS NULL) = (delegated_by_id IS NULL)` — either both set or both null

**`AuditRecorder` interface** (`internal/audit/recorder.go`):
```go
type AuditRecorder interface {
    Record(ctx context.Context, event Event) error
    RecordAsync(ctx context.Context, event Event)  // fire-and-forget; logs error on failure
}

type Event struct {
    OrgID          uuid.UUID
    EventType      string
    PrincipalType  string   // "human" | "agent" | "system"
    PrincipalID    uuid.UUID
    DelegatedByType *string
    DelegatedByID   *uuid.UUID
    TargetType     *string
    TargetID       *uuid.UUID
    Metadata       map[string]any
}
```

**`AuditService`** implementing `AuditRecorder`:
- `Record`: inserts synchronously; returns error if insert fails
- `RecordAsync`: calls `Record` in a goroutine; logs failure at warn level (never panics)
- Principal extraction helper: `audit.PrincipalFromContext(ctx)` returns `(principalType, principalID)` from context (set by auth middleware)

**`AuditRepo`** (`internal/repo/audit_event.go`):
- `Insert(ctx, Event)` — single row insert
- `ListByOrg(ctx, orgID, filters, pagination)` — filters: event_type, principal_id, target_type+id, date range
- `CountByOrg(ctx, orgID, filters)` — for pagination metadata

**Event type constants** (`internal/audit/event_types.go`):
- Establish the naming convention: `domain.action` e.g. `auth.login`, `auth.logout`, `auth.login_failed`, `auth.session_revoked`, `apikey.issued`, `apikey.revoked`
- Define only the auth-domain constants in this task; other domains add their own constants in their respective tasks

### Must NOT build
- Audit API endpoints (those are embedded in other domain endpoints, not a dedicated `/v1/audit` route per the spec)
- Agent-principal audit events (principal_type='agent' events work at schema level but no agent exists yet)

## Acceptance Criteria

- [ ] Migration `0010` applies cleanly; `audit_event` table exists with all columns and indexes
- [ ] `CHECK (principal_type IN ('human', 'agent', 'system'))` constraint: inserting `principal_type='robot'` raises a constraint violation
- [ ] CHECK constraint on delegation: inserting `delegated_by_type='human'` with `delegated_by_id=NULL` raises a constraint violation; inserting with both set succeeds
- [ ] `AuditService.Record` inserts a row and returns nil; calling it with a bad `orgID` (org doesn't exist) returns a foreign key violation error
- [ ] `AuditService.RecordAsync` fires without blocking the caller; a failed insert is logged at warn level and does not propagate the error to the caller
- [ ] `audit.PrincipalFromContext` returns the correct principal type and ID that was injected by the auth middleware in task 007
- [ ] `AuditRepo.ListByOrg` with `event_type` filter returns only matching rows; with date range returns only rows in range

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `AuditService.RecordAsync`: verify it does not block (call it with a fake recorder that sleeps 100ms and verify the caller returns in <10ms)
- `audit.PrincipalFromContext`: test context with principal set returns correct values; empty context returns zero values (not panics)
- Event type constants: verify all constant values match the `domain.action` naming convention (regex check)

**Integration tests:**
- `AuditService.Record` against real PostgreSQL:
  - Insert with all fields populated → read back via `AuditRepo.ListByOrg` → verify all fields match
  - Insert with delegation (both delegated_by fields set) → read back → delegation fields present
  - Insert with `principal_type='system'` and system sentinel UUID → succeeds
  - `ListByOrg` filters: by event_type, by date range, by target_type+target_id
  - Constraint violation: duplicate principal_type + wrong delegated_by combination

**E2E tests:**
- None — covered by dedicated E2E task 081 (bootstrap audit event is the first audit event in the system)

## Implementer Notes

- ISSUE #20 (AMBIGUOUS): `audit_event.principal_type` currently does not include `'supervisor'`. Supervisor-initiated actions (stuck run recovery, timeout cancellation from task 053) cannot be logged to `audit_event` as-is. Until Sam resolves this, do not add `'supervisor'` to the CHECK constraint. Supervisor actions should be recorded in `domain_event` and `run_event` instead.

> ISSUE #20 (AMBIGUOUS): `audit_event.principal_type` check constraint excludes `'supervisor'`. Supervisor-initiated actions (e.g. stuck run recovery from task 053) can be recorded in `domain_event` and `run_event` but not in `audit_event`. Do not add `'supervisor'` to the audit_event principal_type check until Sam resolves whether supervisor-initiated actions belong in the audit trail.

- The system sentinel UUID (`00000000-0000-0000-0000-000000000000`) is used when `principal_type='system'` to represent actions performed by the system itself (bootstrap, scheduled jobs). This is consistent across doc 04 and doc 12.
- `RecordAsync` is the primary call pattern used throughout the codebase. Most callers do not need to know if audit recording succeeded. `Record` is for contexts where the audit trail is security-critical (e.g. failed login events) and failure should surface.
- The `AuditRecorder` interface introduced here is injected into `auth.Service` (task 006), and later into every other service that records audit events. A `NoopRecorder` implementation is provided for tests that don't need audit coverage.
- 1-year retention for audit events (per doc 13) is handled by the retention enforcement job in task 063, not here.
