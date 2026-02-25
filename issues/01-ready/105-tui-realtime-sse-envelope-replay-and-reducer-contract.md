# Task 105: TUI realtime SSE envelope, replay, and reducer contract

Layer: L2
Effort: L
Depends on: 103

## Context

Doc 17 defines a normative SSE envelope (`seq`, `event_id`, `event_type`, context IDs) and strict replay behavior (`since_seq`, ordered apply, dedupe, gap recovery). The TUI cannot provide reliable live operations without this event pipeline.

## Required Fix

Implement the realtime data plane for the TUI:

- Add SSE client with reconnect/backoff and last-seq checkpointing.
- Enforce envelope validation for required fields:
  - `seq`, `event_id`, `event_type`, `occurred_at`, `org_id`, `payload`
- Add deterministic event reducer pipeline:
  - apply in ascending `seq`
  - ignore duplicate `event_id`
  - ignore unknown `event_type` with debug log
- Implement startup/reconnect flow:
  1. fetch snapshots for visible views
  2. connect SSE with `since_seq=<last_seq>`
  3. replay events in order
  4. transition connection state to `connected` only after replay complete
- Implement gap/retention failure fallback:
  - mark stream degraded
  - hard-refresh affected snapshots
  - continue streaming without restart
- Surface connection state in status bar (`connected`, `reconnecting`, `disconnected`).

## Acceptance Criteria

- [ ] TUI resumes from `since_seq` after reconnect and does not lose ordered updates
- [ ] Duplicate events do not mutate state twice
- [ ] Unknown event types do not crash or block reducer
- [ ] Missing required envelope fields mark degraded connection and surface user-visible status
- [ ] Gap recovery performs snapshot refresh and resumes live updates
- [ ] `go build ./...` passes

## Required Tests

- Unit: reducer ordering/dedup tests (`seq`, `event_id` behavior)
- Unit: envelope validation tests (required field missing cases)
- Integration: SSE reconnect with replay from `since_seq` using httptest stream
- Integration: replay gap simulation forces hard-refresh then recovers
- Integration: connection status transitions reflected in status bar model
