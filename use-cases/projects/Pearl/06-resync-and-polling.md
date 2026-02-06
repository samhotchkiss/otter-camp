# Pearl — Re‑Sync & Polling

## Objective
Ensure OtterCamp stays in sync with GitHub even if webhooks are missed.

## Strategy
- **Primary:** GitHub webhooks (push + issues/PRs).
- **Secondary:** Periodic poll (safety net).
- **Manual:** User‑triggered re‑sync.

## Polling Rules (MVP)
- Poll on a fixed interval: **every 60 minutes** (default).
- Compare latest SHA and issue update timestamps.
- Only fetch deltas since last sync.

## Manual Re‑Sync
- OtterCamp UI offers **Re‑Sync** button.
- Also provide **API trigger** for agents:
  - `POST /api/projects/:id/repo/sync`
  - `POST /api/projects/:id/issues/import`

## Acceptance Criteria
- Missed webhook events are reconciled by polling.
- Manual re‑sync updates commit + issue data on demand.
