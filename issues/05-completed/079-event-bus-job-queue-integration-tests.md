# 079: Event Bus and Job Queue Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 12 §DomainEvents, doc 12 §JobQueue, doc 12 §IdempotencyKey, doc 12 §ConsumerCursor, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 024 |
| Blocks | 089 |

## Scope

Integration test suite for the event bus and job queue infrastructure: domain event
publish/subscribe with consumer cursor tracking, LISTEN/NOTIFY wake-up, job queue SKIP
LOCKED concurrency, stale claim recovery, dead-letter promotion, and idempotency key
enforcement. All tests use a real PostgreSQL database via `testdb.New(t)`.

### Must build

**Test file:** `internal/eventbus/eventbus_integration_test.go`

**Test file:** `internal/jobqueue/jobqueue_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/eventbus.go`:
- `PublishEvent(t, db, orgID, eventType, payload)` — inserts a `domain_event` row;
  returns the seq ID
- `AdvanceCursor(t, db, consumerName, seq)` — updates `consumer_cursor` for a named
  consumer
- `EnqueueJob(t, db, jobType, priority, payload)` — inserts a `job_queue` row

**Test scenarios in eventbus_integration_test.go:**

`TestEventBus_Publish_OrderedBySeq` — publish 5 events for the same org; select from
`domain_event` ordered by seq; events appear in publish order; seq values are strictly
monotonically increasing.

`TestEventBus_ConsumerCursor_Tracking` — consumer "consumer-A" processes up to seq=10;
update cursor to seq=10; query for events after cursor: only events with seq>10 returned;
consumer "consumer-B" has its own independent cursor (no shared state between consumers).

`TestEventBus_ListenNotify_Wakeup` — consumer is blocking on LISTEN 'domain_events';
another goroutine publishes a new event; NOTIFY is sent; consumer wakes within timeout;
event is received and processed. Use a real PostgreSQL connection for both sides.

`TestEventBus_MultipleConsumers_Independent` — create 3 consumers each with their own
cursor starting at seq=0; publish 10 events; each consumer independently reads all 10
events; no interference between consumer cursors; each consumer can be at a different
position.

`TestEventBus_GapDetection` — consumer cursor is at seq=5; events exist at seq=1,2,3,4,5
and seq=8,9,10 (gap at 6,7 — simulating events purged or missed); consumer reads from
seq=5; detects gap; appropriate gap handling behavior (skip to seq=8 or signal
`X-Events-Gap`).

`TestEventBus_ActorType_Supervisor` — publish event with `actor_type='supervisor'`;
assert the event is stored correctly in `domain_event` with the supervisor actor_type;
consumers receive it. Verifies that domain_event supports the 4th actor_type value.

**Test scenarios in jobqueue_integration_test.go:**

`TestJobQueue_Priority_Ordering` — enqueue 4 jobs: 1 with priority=10 (highest), 1
with priority=5, 1 with priority=1, 1 with priority=1; worker polls with SKIP LOCKED;
jobs are dequeued in priority order (highest first); within same priority, FIFO order.

`TestJobQueue_SKIP_LOCKED_Concurrency` — 2 workers polling simultaneously (2 goroutines);
10 jobs in queue; each job is claimed by exactly one worker (SKIP LOCKED prevents
double-claim); total processed = 10; no job processed twice. Use real DB transaction
semantics.

`TestJobQueue_Retry_OnTransientFailure` — worker claims job; marks it as failed with
`failure_class='transient'`; job is re-enqueued (attempt_number++); max retries per
domain respected; after max retries, job promoted to dead_letter.

`TestJobQueue_DeadLetter_Promotion` — job exhausts all retry attempts; promoted to dead
letter status; `dead_lettered_at` set; dead-letter job does NOT get re-enqueued; monitoring
query can find dead-letter jobs for operator review.

`TestJobQueue_StaleClaim_Recovery` — worker claims job (sets claimed_by, claimed_at);
worker crashes (simulated by not completing); advance clock past stale claim threshold;
stale claim recovery job runs; job is made available again (claimed_by reset); another
worker can now claim it.

`TestJobQueue_ListenNotify_Wakeup` — worker is sleeping (no jobs); another goroutine
inserts a new high-priority job and sends NOTIFY 'job_queue'; sleeping worker wakes
immediately (within timeout); job is claimed and processed. Avoids polling delays.

`TestIdempotency_KeyUniqueness` — record idempotency key for request-hash-A; attempt to
record same key within 24h window; second insert returns conflict (409 or error code);
`idempotency_key` table has exactly 1 row for that key.

`TestIdempotency_KeyExpiry` — record idempotency key with `expires_at` in the past
(use clock.Fake); cleanup job runs; expired key row is deleted; new request with same
key-hash can now be recorded (idempotency window has expired).

`TestIdempotency_RequestHashCollision` — two different requests with the same
Idempotency-Key header value but different `request_hash` (different bodies); this is a
collision; service returns 409 with `idempotency_collision` error; neither request is
processed (collision is fatal, not first-wins).

`TestJobQueue_at_least_once_delivery` — publish 5 jobs; simulate one worker crashing
mid-job (stale claim); recovery re-delivers that job; all 5 jobs are eventually completed
by healthy workers; job IDs observed by successful completion handlers includes all 5;
the re-delivered job is processed exactly twice total (once failed, once succeeded) but
its idempotent effect is applied only once (verified via idempotency key on the job).

### Must NOT build

- E2E tests involving specific domain events (those are in domain-specific E2E tasks)
- SSE delivery tests (task 047) — that's the HTTP fan-out layer, not the DB event bus
- Delivery/scheduling job queue tests (task 080)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/eventbus/... ./internal/jobqueue/... -tags integration`
- [ ] `TestJobQueue_SKIP_LOCKED_Concurrency` uses 2 goroutines with real DB connections; asserts no double-processing
- [ ] `TestIdempotency_RequestHashCollision` asserts a 409 response with `idempotency_collision` error code
- [ ] `TestEventBus_ListenNotify_Wakeup` uses a real LISTEN/NOTIFY (not a mock channel); test has a timeout assertion
- [ ] `TestJobQueue_StaleClaim_Recovery` uses `clock.Fake` to advance past the stale threshold without real time sleep
- [ ] `TestJobQueue_DeadLetter_Promotion` asserts the dead-lettered job is NOT re-enqueued
- [ ] `TestEventBus_ActorType_Supervisor` confirms the supervisor actor_type value is accepted by the domain_event CHECK constraint

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestEventBus_Publish_OrderedBySeq`
- `TestEventBus_ConsumerCursor_Tracking`
- `TestEventBus_ListenNotify_Wakeup`
- `TestEventBus_MultipleConsumers_Independent`
- `TestEventBus_GapDetection`
- `TestEventBus_ActorType_Supervisor`
- `TestJobQueue_Priority_Ordering`
- `TestJobQueue_SKIP_LOCKED_Concurrency`
- `TestJobQueue_Retry_OnTransientFailure`
- `TestJobQueue_DeadLetter_Promotion`
- `TestJobQueue_StaleClaim_Recovery`
- `TestJobQueue_ListenNotify_Wakeup`
- `TestIdempotency_KeyUniqueness`
- `TestIdempotency_KeyExpiry`
- `TestIdempotency_RequestHashCollision`
- `TestJobQueue_at_least_once_delivery`

**E2E tests:** None — covered by domain-specific E2E tasks.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)` — critically, LISTEN/NOTIFY requires real DB
  connections (cannot be mocked)
- Clock: injected `clock.Fake` for stale claim threshold and idempotency expiry
- Workers: goroutine-based in-process workers (not separate processes)

**LISTEN/NOTIFY in tests:**
`testdb.New(t)` must support multiple concurrent connections to the same test database
for LISTEN/NOTIFY to work. The test helper should provide a second connection
(`testdb.NewConn(t)`) for the listener goroutine, distinct from the main connection used
for writes.

**SKIP LOCKED test setup:**
For `TestJobQueue_SKIP_LOCKED_Concurrency`, use a transaction-per-worker approach:
each worker goroutine opens its own DB connection and runs
`SELECT ... FOR UPDATE SKIP LOCKED` within a transaction. The test uses a WaitGroup to
ensure both workers start polling before any jobs are available, then releases all 10
jobs at once. Assert final counts after all workers drain the queue.

**ISSUE #13 (RESOLVED — domain_event table):**
`domain_event` is fully defined in doc 12 and implemented in task 024. These tests
depend on task 024 being complete.

**at-least-once delivery test:**
`TestJobQueue_at_least_once_delivery` is the most complex test. The pattern:
1. Enqueue 5 jobs
2. Start 2 workers; both begin claiming
3. Force one worker to "crash" by discarding its transaction (rollback) after claim
4. Advance clock past stale threshold
5. Recovery job re-enqueues the abandoned job
6. Remaining worker picks it up
7. Assert final completion count = 5 unique job IDs
The idempotency check on re-delivery (step 7) requires the job payload to include an
idempotency_key so the test can verify the effect is applied once.
