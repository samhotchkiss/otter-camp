# 066: Push Notification Preferences — Schema, Delivery Consumer, and API

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 19 §PushNotifications, doc 19 §PushPreferences, doc 02 §NotificationSystem |
| Spec status | finished |
| Depends on | 005, 027, 043, 044, 007 |
| Blocks | 069, 083 |

## Scope

Define the `push_notification_preference` table (ISSUE #28 — new table not defined in any
spec); implement preference storage for per-urgency-tier enable/disable, per-project
overrides, quiet hours, and per-event-type filtering; build the push delivery consumer
(APNs/FCM adapter stubs); and expose the preference API.

> ⚠️ ISSUE #28 (GAP): Doc 19 requires server-side push notification preferences but no table
> or column is defined anywhere in the spec. This task defines the schema using best judgment
> per doc 19 requirements. The design decision: use a dedicated `push_notification_preference`
> table (one row per user) rather than extending `human_user` with a jsonb column, to allow
> indexing on `user_id` and to keep `human_user` from accumulating preferences. Sam must
> review and confirm the schema before the DDL is considered authoritative.

### Must build

**Migration** (`0077_push_notification_preference.sql`):

```sql
CREATE TABLE push_notification_preference (
    id              uuid        primary key default gen_random_uuid(),
    user_id         uuid        not null references human_user(id) on delete cascade,
    organization_id uuid        not null references organization(id) on delete cascade,

    -- Per-urgency-tier enable/disable:
    -- Keys: 'urgent', 'high', 'normal', 'low'
    -- Values: true = push enabled for this tier, false = disabled
    tier_enabled    jsonb       not null default '{"urgent":true,"high":true,"normal":true,"low":false}',

    -- Per-project push overrides:
    -- Array of {project_id: uuid, enabled: bool, tiers: {...}}
    -- Absence of a project entry = use tier_enabled defaults
    project_overrides jsonb     not null default '[]',

    -- Quiet hours (user's local timezone; enforced server-side using utc_offset):
    quiet_hours_enabled boolean not null default false,
    quiet_hours_start   text,   -- "HH:MM" 24-hour, e.g. "22:00"
    quiet_hours_end     text,   -- "HH:MM" 24-hour, e.g. "08:00"
    quiet_hours_timezone text,  -- IANA timezone string, e.g. "America/New_York"

    -- Per-event-type filtering:
    -- Keys: event_type strings (e.g. 'task.blocked', 'task.review_requested')
    -- Values: true = send push for this event type; false = suppress
    -- Absence of key = use tier_enabled default
    event_type_overrides jsonb  not null default '{}',

    -- Push token registration (may have multiple devices):
    -- Array of {token: string, platform: 'apns'|'fcm', device_id: string, registered_at: timestamptz}
    -- Tokens are NOT encrypted here — they are not secrets (per APNs/FCM design)
    push_tokens     jsonb       not null default '[]',

    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now(),

    UNIQUE (user_id, organization_id)
);
CREATE INDEX idx_push_pref_user ON push_notification_preference (user_id);
```

**Repository** (`internal/push/preference_repository.go`):

`PushPreferenceRepository`:
- `GetByUser(ctx, userID, orgID) (*PushNotificationPreference, error)` — returns nil if not found
- `Upsert(ctx, pref PushNotificationPreference) error` — ON CONFLICT (user_id, org_id) DO UPDATE
- `RegisterToken(ctx, userID, orgID, token PushToken) error` — appends to `push_tokens` jsonb array; deduplicates by `device_id`
- `RevokeToken(ctx, userID, orgID, deviceID string) error` — removes matching entry from `push_tokens` array

**Push preference service** (`internal/push/preference_service.go`):

`PushPreferenceService.GetPreferences(ctx, userID, orgID) (PushNotificationPreference, error)`:
- Calls repository; if no row exists, returns a default preference object (all tiers at default).

`PushPreferenceService.UpdatePreferences(ctx, userID, orgID, update PushPreferenceUpdate) error`:
- Validates `quiet_hours_start` / `quiet_hours_end` format if provided (`HH:MM`).
- Validates `quiet_hours_timezone` against IANA timezone list.
- Validates tier keys are in `{'urgent','high','normal','low'}`.
- Upserts via repository.

`PushPreferenceService.ShouldDeliver(ctx, userID, orgID, projectID *uuid.UUID, urgencyTier, eventType string) (bool, error)`:
- The delivery gate used before sending a push notification.
- Returns false if any of:
  1. `tier_enabled[urgencyTier]` is false (and no project override enables it).
  2. `event_type_overrides[eventType]` is explicitly false.
  3. Project override for `projectID` has `enabled=false` (overrides tier).
  4. Quiet hours are active: convert current UTC time to `quiet_hours_timezone`; if current
     local time is between `quiet_hours_start` and `quiet_hours_end` (handling midnight wrap),
     return false UNLESS `urgencyTier='urgent'` (urgent notifications bypass quiet hours).
- Returns true otherwise.

**Push delivery consumer** (`internal/push/delivery_consumer.go`):

Consumes `domain_event` rows with event types:
- `task.blocked` → urgency `high`
- `task.review_requested` → urgency `high`
- `inbox.item_created` → urgency tier from `inbox_item.urgency`
- `run.dead_lettered` → urgency `urgent`
- `project.push_failed` → urgency `urgent`
- `task.completed` → urgency `normal`

`PushDeliveryConsumer.Consume(ctx, event DomainEvent) error`:
- Parse event; determine target user ID(s) from payload (e.g., task participants for
  `task.blocked`, PM for `run.dead_lettered`).
- For each target user:
  1. Load `PushNotificationPreference` via `PushPreferenceService.GetPreferences`.
  2. Call `ShouldDeliver`; if false: skip with reason logged.
  3. If should deliver: build notification payload; dispatch via `PushAdapter.Send`.

`PushDeliveryConsumer.BuildPayload(event DomainEvent, urgencyTier string) PushPayload`:
- `PushPayload`:
  - `title` — human-readable summary (e.g., "Task blocked: {task_title}")
  - `body` — detail text (e.g., "Waiting on dependency resolution")
  - `category` — APNs/FCM notification category (e.g., `"task_blocked"`)
  - `deep_link` — URL scheme: `ottercamp://tasks/{task_id}` (mobile app routing)
  - `item_id` — the source entity ID for client-side dedup
  - `badge_count` — omitted (client computes from inbox count)

**APNs and FCM adapter stubs** (`internal/push/adapters/`):

```go
type PushAdapter interface {
    Send(ctx context.Context, token PushToken, payload PushPayload) error
}
```

- `APNSAdapter.Send`: stub — logs the payload and returns nil. Real APNs integration
  is out of scope for this iteration; the interface allows a real implementation to be
  swapped in without changing callers.
- `FCMAdapter.Send`: same stub pattern.
- Selection by `token.platform`: `'apns'` → `APNSAdapter`; `'fcm'` → `FCMAdapter`.
- Wrap both in a `MultiAdapter` that routes by platform.

**Push token registration** (extend `human_user` API, task 007):

`POST /v1/me/push-token`:
- Body: `{token: string, platform: 'apns'|'fcm', device_id: string}`.
- Calls `PushPreferenceRepository.RegisterToken`.
- Returns 200 `{data: {registered: true}}`.
- Deduplicates by `device_id` (updating token value if device_id already exists).

`DELETE /v1/me/push-token/:device_id`:
- Calls `PushPreferenceRepository.RevokeToken`.
- Returns 204.

**Push preference API:**

`GET /v1/me/push-preferences`:
- Returns the authenticated user's push preferences for their current org.
- Calls `PushPreferenceService.GetPreferences`.
- Returns default preference object (not 404) if no row exists.

`PATCH /v1/me/push-preferences`:
- Body: partial update — any subset of fields.
- Calls `PushPreferenceService.UpdatePreferences`.
- Returns 200 with updated preference object.

### Must NOT build

- In-app notification system (doc 02's inbox/notification model — task 027, 028)
- Real APNs or FCM HTTP client (stubs only)
- Mobile dashboard endpoint (task 069)
- SSE/WebSocket notification delivery (task 047)
- `inbox_item` DDL or service (tasks 027, 028)

## Acceptance Criteria

- [ ] `push_notification_preference` table has a unique constraint on `(user_id, organization_id)`; second row for same pair is rejected
- [ ] `PushPreferenceService.ShouldDeliver` returns false for `tier_enabled.normal=false` when event urgency is `normal`
- [ ] `PushPreferenceService.ShouldDeliver` returns true for `urgency='urgent'` even during quiet hours
- [ ] `PushPreferenceService.ShouldDeliver` returns false for quiet hours with urgency `high` when current time is within quiet window
- [ ] `PushPreferenceRepository.RegisterToken` updates the existing entry when the same `device_id` is registered again (no duplicate)
- [ ] `PushDeliveryConsumer.Consume` skips delivery and logs reason when `ShouldDeliver` returns false
- [ ] `GET /v1/me/push-preferences` returns default preference object (not 404) for a user with no stored preferences
- [ ] `PATCH /v1/me/push-preferences` with invalid quiet hours format returns 422

## Tests Required

**Unit tests:**
- `ShouldDeliver`: tier disabled → false; tier enabled → true; quiet hours active + high urgency → false; quiet hours active + urgent urgency → true; midnight-wrap quiet hours (22:00–06:00) test case
- `BuildPayload`: `task.blocked` event → title contains "blocked"; deep link contains task ID
- `PushPreferenceService.UpdatePreferences`: invalid timezone string → error; invalid `HH:MM` format → error; valid update → no error
- `MultiAdapter.Send`: `platform='apns'` → routes to `APNSAdapter`; `platform='fcm'` → routes to `FCMAdapter`

**Integration tests:**
- `PushPreferenceRepository.Upsert` round-trip: create then update; verify updated fields reflect changes; unique constraint prevents second row for same user+org
- Token registration: register token for device A; re-register same device A with new token → only one entry for device A; register device B → two entries
- `GET /v1/me/push-preferences` for user with no row → 200 with default values; `PATCH` → 200 with updated values; `GET` again → updated values persisted

**E2E tests:**
- None — covered by dedicated E2E task 083

## Implementer Notes

> ⚠️ ISSUE #28 (GAP): This table is entirely the result of spec gap-filling. The schema defined here is based on doc 19's functional requirements (per-urgency push enable/disable, per-project overrides, quiet hours, per-event-type filtering). Sam must review the `push_notification_preference` schema before this task is implemented and confirm whether the `push_tokens` jsonb array or a separate `push_device_token` table is preferred. If a separate table is chosen, this task's scope expands by ~0.5 days.

**Quiet hours midnight wrap:** when `quiet_hours_start > quiet_hours_end` (e.g., 22:00–06:00),
the quiet period wraps midnight. Check: `current_hour >= start OR current_hour < end`.
When `start <= end` (e.g., 13:00–14:00), check: `current_hour >= start AND current_hour < end`.
