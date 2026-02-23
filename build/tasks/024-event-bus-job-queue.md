# 024: Event Bus and Job Queue

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | M (1–2 days) |
| Spec refs | doc 12 §DomainEvent, doc 12 §ConsumerCursor, doc 12 §JobQueue, doc 12 §IdempotencyKey |
| Spec status | finished |
| Depends on | 002, 003 |
| Blocks | 014, 023, 047, 052, 065, 079 |

## Scope

Build the four infrastructure tables (`domain_event`, `consumer_cursor`, `job_queue`,
`idempotency_key`) plus the event bus, job worker polling loop, LISTEN/NOTIFY wakeup,
stale claim recovery, and daily idempotency cleanup job. This is the messaging backbone
used by all domain services.

### Must build

**Migrations:**
- `0026_domain_event.sql`
- `0027_consumer_cursor.sql`
- `0028_job_queue.sql`
- `0029_idempotency_key.sql`

**`domain_event` table** (doc 12):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `seq bigserial not null` — global sequence for ordering; used for consumer cursor position
- `event_type text not null` — e.g. `agent.activated`, `task.completed`, `mcp.catalog.changed`
- `actor_type text not null check (actor_type in ('human','agent','system','supervisor'))` — 4-value enum per doc 12; 'supervisor' is the extension beyond canonical 3
- `actor_id uuid` — null for system/supervisor actors
- `payload jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- Index: `(organization_id, seq)` — primary consumer query
- Index: `(organization_id, event_type, created_at)` — filtering queries
- Index: `(seq)` — global ordering; also used for LISTEN/NOTIFY wake-up check

**`consumer_cursor` table** (doc 12):
- `id uuid primary key default gen_random_uuid()`
- `consumer_name text not null` — logical consumer identifier (e.g. `agent.expiry_handler`, `ssse.fan_out`)
- `organization_id uuid references organization(id) on delete cascade` — null = global consumer
- `last_seq bigint not null default 0` — last processed `domain_event.seq` value
- `updated_at timestamptz not null default now()`
- Unique constraint: `(consumer_name, organization_id)` — handle nulls with partial index

**`job_queue` table** (doc 12):
- `id uuid primary key default gen_random_uuid()`
- `job_type text not null` — e.g. `budget.anomaly_scan`, `merge_execute`, `schedule_tick`
- `priority integer not null default 100` — lower = higher priority; tiers: 10=critical, 50=high, 100=normal, 200=low
- `payload jsonb not null default '{}'`
- `status text not null default 'pending' check (status in ('pending','claimed','done','failed','dead_letter'))`
- `claimed_by text` — worker instance identifier; null = unclaimed
- `claimed_at timestamptz` — null = unclaimed
- `attempts integer not null default 0` — number of times this job has been attempted
- `max_attempts integer not null default 3`
- `last_error text` — last failure reason; null if never failed
- `run_after timestamptz not null default now()` — enables delayed jobs (retry backoff)
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(status, priority, run_after) WHERE status = 'pending'` — polling query
- Index: `(claimed_at) WHERE status = 'claimed'` — stale claim recovery scan

**`idempotency_key` table** (doc 12):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `key_hash text not null` — SHA-256 of the client-provided Idempotency-Key header value
- `request_hash text not null` — SHA-256 of `(method + path + body)`; used for collision detection
- `response_status integer not null` — HTTP status of the original response
- `response_body jsonb` — stored response body for replay
- `created_at timestamptz not null default now()`
- `expires_at timestamptz not null` — `created_at + 24 hours`; used by cleanup job
- Unique constraint: `(organization_id, key_hash)`
- Index: `(expires_at) WHERE expires_at < now()` — cleanup job scan

**`internal/eventbus/bus.go` — `EventBus` interface and implementation:**

```go
type EventBus interface {
    // Publish writes a domain_event row and sends a NOTIFY signal
    Publish(ctx context.Context, tx *sql.Tx, event DomainEvent) error

    // Subscribe registers a consumer; handler is called for each new event
    // Consumer position tracked in consumer_cursor table
    Subscribe(consumerName string, orgID *uuid.UUID, handler EventHandler) Subscription

    // Unsubscribe removes a consumer
    Unsubscribe(sub Subscription)
}

type EventHandler func(ctx context.Context, event DomainEvent) error
```

**Event bus implementation details:**
- `Publish`: INSERT into `domain_event`; call `pg_notify('domain_events', seq::text)` in the same transaction (or immediately after commit)
- LISTEN/NOTIFY channel name: `domain_events` (global; consumers filter by org + type)
- Each consumer goroutine: listens on the PostgreSQL `domain_events` channel; on notification, queries `domain_event WHERE seq > last_seq ORDER BY seq LIMIT 100`; processes each event; updates `consumer_cursor.last_seq` after successful processing
- At-least-once delivery: if a consumer crashes mid-processing, the cursor is NOT advanced, so events are re-delivered on restart
- Consumer handlers must be idempotent (each domain package is responsible for idempotency in its handler)
- On startup: each consumer runs a catch-up query (process all events since cursor position) before switching to LISTEN/NOTIFY mode

**`internal/jobqueue/worker.go` — job worker:**

```go
type JobWorker interface {
    Start(ctx context.Context) error
    Stop() error
    Register(jobType string, handler JobHandler)
}

type JobHandler func(ctx context.Context, job Job) error
```

**Job worker implementation:**
- Poll loop: `SELECT ... FROM job_queue WHERE status='pending' AND run_after <= now() ORDER BY priority ASC, run_after ASC LIMIT 10 FOR UPDATE SKIP LOCKED`
- On claim: UPDATE `status='claimed'`, `claimed_by=<worker_id>`, `claimed_at=now()`, `attempts++`
- On success: UPDATE `status='done'`
- On failure: if `attempts < max_attempts`: UPDATE `status='pending'`, `run_after = now() + backoff(attempts)`, `last_error=...`; else UPDATE `status='dead_letter'`
- Backoff: exponential, base 1s, multiplier 2, max 5 minutes
- LISTEN/NOTIFY wakeup: worker also listens on `job_enqueued` channel; on notification, wake up immediately (don't wait for next poll tick)
- Poll interval: 5 seconds (fallback when no NOTIFY received)
- Worker ID: `<hostname>-<pid>-<uuid>` — generated at startup

**Stale claim recovery:**
- Background goroutine runs every 60 seconds
- Query: `SELECT id FROM job_queue WHERE status='claimed' AND claimed_at < now() - interval '5 minutes'`
- For each stale claim: if `attempts < max_attempts`: reset to `status='pending'`, `claimed_by=null`, `claimed_at=null`, `run_after=now()`; else move to `status='dead_letter'`
- Stale threshold is configurable (default: 5 minutes)

**Daily idempotency cleanup job:**
- Job type: `idempotency.cleanup`
- Handler: `DELETE FROM idempotency_key WHERE expires_at < now()` with `LIMIT 1000` to batch
- Enqueued daily via cron-style timer in the worker startup (not a task_schedule — internal job)

**Enqueue helper:**
```go
func (w *JobWorker) Enqueue(ctx context.Context, tx *sql.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
```
`tx` is optional — if nil, uses a new transaction. Emits `pg_notify('job_enqueued', job_id::text)` after insert.

### Must NOT build
- SSE event stream fan-out (task 047 — uses EventBus but is a separate concern)
- Specific job handlers (each domain task registers its own handlers)
- WebSocket (task 047)
- Trace span table (task 063)

## Acceptance Criteria

- [ ] Migrations `0026`–`0029` apply cleanly
- [ ] `EventBus.Publish` inserts a `domain_event` row and the row's `seq` is strictly increasing
- [ ] Consumer receives events: publish 3 events → consumer handler called 3 times in seq order
- [ ] Consumer cursor advances after successful handler: cursor position = last processed seq
- [ ] Consumer does NOT advance cursor on handler error: event is re-delivered on next startup
- [ ] `JobWorker` claims a pending job with `FOR UPDATE SKIP LOCKED`: two workers do not claim the same job
- [ ] Job success: status transitions to `done`
- [ ] Job failure (transient): status resets to `pending` with backoff `run_after`; `attempts` incremented
- [ ] Job failure (max attempts): status transitions to `dead_letter`
- [ ] Stale claim recovery: job claimed > 5 minutes ago by dead worker is reset to `pending`
- [ ] Idempotency key: same `(org, key_hash)` returns stored response; different `request_hash` returns 409

## Tests Required

**Unit tests:**
- Backoff computation: attempts 0,1,2,3 → correct `run_after` durations
- Consumer cursor advance logic: success → cursor updated; error → cursor unchanged
- Idempotency key collision detection: same key + same body → replay; same key + different body → `ErrIdempotencyConflict`

**Integration tests:**
- `EventBus.Publish` + Subscribe: real PostgreSQL + LISTEN/NOTIFY; publish event; consumer receives it within 2 seconds
- At-least-once delivery: handler returns error on first call → event re-delivered after restart (simulate by clearing consumer state)
- Job worker: enqueue 5 jobs; start worker; all 5 processed within 10 seconds
- `FOR UPDATE SKIP LOCKED`: two concurrent workers; each job processed exactly once (verify by counting handler invocations)
- Stale claim recovery: claim a job manually; set `claimed_at = now() - 10 minutes`; run recovery; verify job reset to pending
- Idempotency cleanup: seed expired keys; run cleanup job; verify deleted; non-expired keys remain

**E2E tests:**
- None — covered by dedicated E2E task 079

## Implementer Notes

- ISSUE #20 (AMBIGUOUS): `domain_event.actor_type` includes `'supervisor'` as a 4th value beyond the canonical 3. This is an intentional extension per doc 12 ("code that switches on actor_type must handle all four values"). The `audit_event` table (task 008) does NOT include 'supervisor' — supervisor-initiated actions are tracked in domain_event only. Do not add 'supervisor' to `audit_event.principal_type` without Sam's resolution of ISSUE #20.
- The event bus must support transactional publishing: the `Publish` call takes an optional `*sql.Tx`. When a transaction is provided, the INSERT and NOTIFY happen within that transaction. This ensures that if the transaction rolls back, the event is not published. Use `pg_notify` within the transaction (note: PostgreSQL delivers NOTIFY only on transaction commit, so this is safe).
- Consumer goroutines should have a dedicated panic recovery wrapper — a panicking consumer must not crash the entire server. Log the panic with stack trace, skip the event, advance the cursor to prevent stuck processing.
- The `idempotency_key` table serves the API middleware (task 067). This task builds the table and the `IdempotencyKeyRepo` with methods `Store`, `Get`, `CheckConflict`. The middleware wiring is in task 067.
- Worker ID format: `<hostname>-<pid>-<uuid>` ensures uniqueness across restarts on the same host. Use `os.Hostname()` + `os.Getpid()` + `uuid.New()` at worker startup.
- `SKIP LOCKED` requires PostgreSQL 9.5+. OtterCamp requires PostgreSQL 14+ (established by task 002), so this is safe.
