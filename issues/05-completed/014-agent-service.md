# 014: Agent Service

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | M (1–2 days) |
| Spec refs | doc 05 §StaffLifecycle, doc 05 §TempLifecycle, doc 05 §PromotionWorkflow, doc 14 §ConcurrentTempLimit |
| Spec status | finished |
| Depends on | 013, 006, 024 |
| Blocks | 015, 025, 085 |

## Scope

Implement all agent lifecycle business logic in `internal/agent/service.go`. This covers the
staff state machine, temp state machine, concurrent temp limit enforcement, temp
auto-retirement triggers, and the promotion workflow. Does not build API handlers (task 015)
or project assignment logic (task 025).

### Must build

**`internal/agent/service.go` — `AgentService` interface and implementation:**

```go
type AgentService interface {
    Create(ctx context.Context, req CreateAgentRequest) (*Agent, error)
    Get(ctx context.Context, orgID, agentID uuid.UUID) (*Agent, error)
    List(ctx context.Context, orgID uuid.UUID, filter AgentFilter) ([]*Agent, error)
    Update(ctx context.Context, orgID, agentID uuid.UUID, req UpdateAgentRequest) (*Agent, error)

    // Staff lifecycle
    Pause(ctx context.Context, orgID, agentID uuid.UUID) error
    Unpause(ctx context.Context, orgID, agentID uuid.UUID) error
    Retire(ctx context.Context, orgID, agentID uuid.UUID) error
    Cancel(ctx context.Context, orgID, agentID uuid.UUID) error

    // Temp lifecycle
    CreateTemp(ctx context.Context, orgID uuid.UUID, req CreateTempAgentRequest) (*Agent, error)
    ExpireTemp(ctx context.Context, orgID, agentID uuid.UUID, reason string) error
    PromoteTemp(ctx context.Context, orgID, agentID uuid.UUID, req PromoteTempRequest) (*Agent, error)

    // Internal
    EnforceMaxConcurrentTemps(ctx context.Context, orgID uuid.UUID) error
    RetireExpiredTemps(ctx context.Context) error  // called by scheduler
}
```

**Staff lifecycle state machine** (doc 05):
- `draft → active`: requires human approval (Lori review step); on transition emit `agent.activated` domain event
- `active → paused`: any org admin; emit `agent.paused`
- `paused → active`: any org admin; emit `agent.unpaused`
- `active → retired`: org admin; soft-delete semantics (row retained); emit `agent.retired`
- `active → cancelled`: org admin; hard-cancel mid-lifecycle; emit `agent.cancelled`
- `draft → cancelled`: allowed; emit `agent.cancelled`
- Invalid transitions return `ErrInvalidTransition` with current and target status in the error message

**Temp lifecycle state machine** (doc 05):
- Temp agents are created directly as `lifecycle_status='active'` (no draft state)
- `active → expired`: triggered by TTL expiry scheduler only (when `temp_expires_at < now()`); emit `agent.expired`; write archival summary (see below). Task completion and session close do NOT retire temps.
- `active → promoted`: triggered by promotion workflow; emit `agent.promoted`
- Staff lifecycle transitions (pause/retire/cancel) are rejected for temp agents with `ErrInvalidForTempAgent`

**Concurrent temp limit enforcement:**
- On `CreateTemp`: call `AgentRepo.CountActiveTemps(orgID)` and compare against `organization.settings.agents.max_concurrent_temps` (read from org settings jsonb; default: 5)
- If at or over limit: return `ErrConcurrentTempLimitReached` with current count and limit in the error message
- Enforcement is a synchronous check-before-insert; no DB-level constraint (acknowledged race condition for high-concurrency deployments — document in code comment)

**Temp auto-retirement** (TTL scheduler only, via task 024 scheduler):
- TTL expiry: `RetireExpiredTemps()` queries `temp_expires_at < now()` and `lifecycle_status='active'`; batches up to 100 per call; called by the periodic scheduler job (task 024)
- Task completion and session close events do NOT trigger temp retirement — temps persist across multiple tasks within their project
- Explicit retirement: PM or Lori calls `AgentService.RetireTemp(ctx, orgID, agentID)`; sets lifecycle_status='retired'

**Archival summary on expiration:**
- On `ExpireTemp`: generate a brief archival summary (stub for now — call `ArchivalSummaryService.Generate(agent)` which returns a static string in this task; real implementation in task 052/057)
- Store summary in `agent.metadata jsonb` under key `archival_summary`

**Promotion workflow:**
- `PromoteTemp(ctx, orgID, agentID, req)`:
  1. Verify agent is temp + active
  2. Create a new staff agent in `lifecycle_status='draft'` with fields copied from the temp (system_prompt, operator_instructions, tool lists, model profile)
  3. Set `temp.promoted_to_agent_id = new_staff.id`
  4. Set `temp.lifecycle_status = 'promoted'`
  5. Emit `agent.promoted` domain event with both IDs in payload
  6. Return the new draft staff agent
- The draft staff agent then follows the normal approval path (Lori review → human approval → active)
- Lori's review is triggered by the `agent.promoted` domain event (handled in task 014 as a stub that logs; real Lori chat integration in task 044)

### Must NOT build
- Agent API endpoints (task 015)
- `agent_project_assignment` or `agent_skill_attachment` (task 025)
- Full prompt assembly for Lori's review chat (task 044/050)
- Budget enforcement gate checks (implemented in task 053 using task 023 budgets)

## Acceptance Criteria

- [ ] `AgentService.Create` with `agent_class='staff'` creates agent in `lifecycle_status='draft'`
- [ ] `AgentService.CreateTemp` rejects when org is at max concurrent temps; returns `ErrConcurrentTempLimitReached`
- [ ] `AgentService.CreateTemp` with `temp_ttl_seconds` set computes `temp_expires_at = created_at + temp_ttl_seconds`
- [ ] `AgentService.Pause` on an already-paused agent returns `ErrInvalidTransition`
- [ ] `AgentService.Retire` on a temp agent returns `ErrInvalidForTempAgent`
- [ ] `AgentService.ExpireTemp` sets `lifecycle_status='expired'` and emits `agent.expired` domain event
- [ ] `RetireExpiredTemps` processes a batch of expired temps without error; each processed temp gets `lifecycle_status='expired'`
- [ ] `PromoteTemp` creates a new draft staff agent and sets `promoted_to_agent_id` on the temp

## Tests Required

**Unit tests:**
- Staff state machine: all valid transitions succeed; all invalid transitions return `ErrInvalidTransition`
- Temp state machine: temp-only operations succeed; staff operations on temp return `ErrInvalidForTempAgent`
- `EnforceMaxConcurrentTemps`: mocked repo returning count = limit → error; count = limit-1 → success
- `temp_expires_at` calculation: ttl=3600 → expires_at = created_at + 1 hour

**Integration tests:**
- `CreateTemp` + concurrent limit: seed org with max_concurrent_temps=2; create 2 temps → success; create 3rd → `ErrConcurrentTempLimitReached`
- Temp TTL expiry: create temp with `temp_ttl_seconds=1` and `temp_expires_at` in the past (use `clock.Fake`); call `RetireExpiredTemps` → temp expires
- `RetireExpiredTemps`: seed 5 expired temps (temp_expires_at in the past) + 2 future temps; call → 5 expired, 2 unchanged
- Promotion workflow: temp → `PromoteTemp` → new draft staff agent in DB; temp has `promoted_to_agent_id` set; `agent.promoted` event in `domain_event` table

**E2E tests:**
- None — covered by dedicated E2E task 085

## Implementer Notes

- Domain events emitted by this service go to the `domain_event` table via the `EventBus` from task 024. The `AgentService` must accept an `EventBus` dependency. At this layer, events are written transactionally with the state change (same DB transaction where possible).
- The `organization.settings` jsonb path for max concurrent temps is `settings -> 'agents' -> 'max_concurrent_temps'`. Default is 5 if the path is absent or null. Use `COALESCE((settings->'agents'->>'max_concurrent_temps')::int, 5)` in the query or read at the service layer.
- Lori's promotion review is event-driven. This task only emits `agent.promoted`. The actual chat message to Lori is wired in task 044. For now, log "agent.promoted event emitted; Lori review handler not yet registered" at INFO level.
- The archival summary stub should return `"Archival summary pending implementation"`. This placeholder is intentionally visible so that task 052/057 implementers know where to hook in.
- `RetireExpiredTemps` is designed to be called repeatedly (idempotent for already-expired temps). The batch size of 100 prevents a single call from scanning the entire table in a long transaction. Implement with `FOR UPDATE SKIP LOCKED` to allow multiple workers to safely call this concurrently.
