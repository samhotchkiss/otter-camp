# 066: Push Notification Preferences — Delivery Consumer and API

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 19 §PushNotifications, doc 19 §PushPreferences, doc 02 §NotificationSystem |
| Spec status | finished |
| Depends on | 005, 027, 043, 044, 007 |
| Blocks | 069, 083 |

## Scope

Implement push notification preference storage (inside `human_user.settings` jsonb — no new
table), build the push delivery consumer (APNs/FCM adapter stubs), and expose the preference API.

> ✅ ISSUE #28 (RESOLVED): Push notification preferences are stored in `human_user.settings` jsonb
> under two keys: `push_preferences` (tier settings, quiet hours, per-project overrides,
> per-event-type overrides) and `push_tokens` (registered device tokens). No separate
> `push_notification_preference` table is created. This keeps `human_user` as the single source of
> truth for user configuration and avoids a one-row-per-user table pattern.

### Must build

**No new migration** — preferences live in `human_user.settings` (added in task 005).

**`push_preferences` jsonb shape** (stored under `human_user.settings->'push_preferences'`):

```json
{
  "tier_enabled": {
    "urgent": true,
    "high": true,
    "normal": true,
    "low": false
  },
  "project_overrides": [],
  "quiet_hours_enabled": false,
  "quiet_hours_start": null,
  "quiet_hours_end": null,
  "quiet_hours_timezone": null,
  "event_type_overrides": {}
}
```

**`push_tokens` jsonb shape** (stored under `human_user.settings->'push_tokens'`):

```json
[
  {
    "token": "...",
    "platform": "apns",
    "device_id": "...",
    "registered_at": "2025-01-01T00:00:00Z"
  }
]
```

**`push_preferences` field semantics:**
- `tier_enabled`: keys `'urgent'`, `'high'`, `'normal'`, `'low'`; value true = push enabled for that tier
- `project_overrides`: array of `{project_id, enabled, tiers: {...}}`; absence of a project entry = use tier_enabled defaults
- `quiet_hours_enabled`: if false, quiet hours fields are ignored
- `quiet_hours_start` / `quiet_hours_end`: `"HH:MM"` 24-hour format (e.g. `"22:00"`)
- `quiet_hours_timezone`: IANA timezone string (e.g. `"America/New_York"`)
- `event_type_overrides`: keys = event_type strings; values true/false; absence = use tier_enabled default

**Repository** (`internal/push/preference_repository.go`):

`PushPreferenceRepository` reads and writes to `human_user.settings` using `HumanUserRepo`:
- `GetPreferences(ctx, userID) (*PushPreferences, error)` — reads `settings->'push_preferences'`; returns default struct if key absent
- `SavePreferences(ctx, userID, prefs PushPreferences) error` — writes to `settings->'push_preferences'` using `UPDATE human_user SET settings = jsonb_set(settings, '{push_preferences}', $1) WHERE id = $2`
- `GetTokens(ctx, userID) ([]PushToken, error)` — reads `settings->'push_tokens'`; returns empty slice if absent
- `RegisterToken(ctx, userID, token PushToken) error` — upserts into `push_tokens` array by `device_id`; uses `jsonb_set` to write updated array; deduplicates by device_id (update token value if same device_id)
- `RevokeToken(ctx, userID, deviceID string) error` — removes matching entry from `push_tokens` array

All writes use atomic `UPDATE ... SET settings = jsonb_set(...)` — no read-modify-write race.

**Push preference service** (`internal/push/preference_service.go`):

`PushPreferenceService.GetPreferences(ctx, userID) (PushPreferences, error)`:
- Calls repository; if no `push_preferences` key exists in settings, returns a default preference struct (all tiers at defaults).

`PushPreferenceService.UpdatePreferences(ctx, userID, update PushPreferenceUpdate) error`:
- Validates `quiet_hours_start` / `quiet_hours_end` format if provided (`HH:MM`).
- Validates `quiet_hours_timezone` against IANA timezone list.
- Validates tier keys are in `{'urgent','high','normal','low'}`.
- Saves via repository.

`PushPreferenceService.ShouldDeliver(ctx, userID, projectID *uuid.UUID, urgencyTier, eventType string) (bool, error)`:
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
  1. Load preferences via `PushPreferenceService.GetPreferences`.
  2. Call `ShouldDeliver`; if false: skip with reason logged.
  3. If should deliver: load tokens via `PushPreferenceRepository.GetTokens`; build
     notification payload; dispatch via `PushAdapter.Send`.

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
- Returns the authenticated user's push preferences.
- Calls `PushPreferenceService.GetPreferences`.
- Returns default preference object (not 404) if no preferences are stored.

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

- [ ] `human_user.settings` stores push preferences under key `push_preferences`; a second upsert for the same user updates the key (no duplicate rows, no new table)
- [ ] `PushPreferenceService.ShouldDeliver` returns false for `tier_enabled.normal=false` when event urgency is `normal`
- [ ] `PushPreferenceService.ShouldDeliver` returns true for `urgency='urgent'` even during quiet hours
- [ ] `PushPreferenceService.ShouldDeliver` returns false for quiet hours with urgency `high` when current time is within quiet window
- [ ] `PushPreferenceRepository.RegisterToken` updates the existing entry when the same `device_id` is registered again (no duplicate in `push_tokens` array)
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
- `PushPreferenceRepository` round-trip: save preferences → read back; verify updated fields reflect changes; second save overwrites first (no duplicate storage)
- Token registration (via `human_user.settings`): register token for device A; re-register same device A with new token → only one entry for device A in `push_tokens` array; register device B → two entries
- `GET /v1/me/push-preferences` for user with no `push_preferences` key → 200 with default values; `PATCH` → 200 with updated values; `GET` again → updated values persisted

**E2E tests:**
- None — covered by dedicated E2E task 083

## Implementer Notes

**Storage mechanics:**

Preferences are stored as two keys in `human_user.settings`:
- `settings->'push_preferences'` — the preference object (tier settings, quiet hours, etc.)
- `settings->'push_tokens'` — the array of registered device tokens

Use `jsonb_set(settings, '{push_preferences}', $1::jsonb)` for atomic updates. Do NOT
read-modify-write at the application layer — use PostgreSQL jsonb_set to prevent
races between concurrent preference updates.

For `push_tokens` array, use a PostgreSQL expression to upsert by device_id:
```sql
UPDATE human_user
SET settings = jsonb_set(
    settings,
    '{push_tokens}',
    (
        SELECT jsonb_agg(
            CASE WHEN elem->>'device_id' = $device_id THEN $new_token::jsonb ELSE elem END
        )
        FROM jsonb_array_elements(COALESCE(settings->'push_tokens', '[]')) AS elem
        -- If device not found in array, append it:
        -- If no existing entry, the result is null → handle in application via COALESCE
    )
)
WHERE id = $user_id
```
Or compute the updated array in Go and write back atomically.

**Quiet hours midnight wrap:** when `quiet_hours_start > quiet_hours_end` (e.g., 22:00–06:00),
the quiet period wraps midnight. Check: `current_hour >= start OR current_hour < end`.
When `start <= end` (e.g., 13:00–14:00), check: `current_hour >= start AND current_hour < end`.

**Default preferences struct:**
```go
var DefaultPushPreferences = PushPreferences{
    TierEnabled: map[string]bool{
        "urgent": true,
        "high":   true,
        "normal": true,
        "low":    false,
    },
    ProjectOverrides:    []ProjectPushOverride{},
    QuietHoursEnabled:   false,
    EventTypeOverrides:  map[string]bool{},
}
```
