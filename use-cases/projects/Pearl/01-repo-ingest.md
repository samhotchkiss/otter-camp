# Pearl — Repo Ingest & Sync

## Objective
Ingest Pearl’s GitHub repository into OtterCamp so all code + history are available locally and representable in the project interface.

## Sync Direction (Code)
- **Bi‑directional.**
- **GitHub → OtterCamp:** pull updates, merge, and reflect in OtterCamp.
- **OtterCamp → GitHub:** push when a human initiates publish to `main`.

## Branch Scope
- Mirror **default branch** (e.g., `main`).
- Mirror **active feature branches** (explicitly selected or recently updated).
- Do **not** mirror all historical branches by default.

## Local Repo Storage
- Each OtterCamp project may map to **zero or one** GitHub repo.
- When mapped, OtterCamp keeps a **local clone**:
  - `repo_path`: filesystem path
  - `default_branch`
  - `last_synced_sha`
  - `active_branches[]`

## Pull Strategy
- Primary: **GitHub webhooks** (push events) → enqueue sync.
- Secondary: **periodic poll** (e.g., every N minutes) → detect drift.
- Manual: **Resync button** triggers immediate pull.

## Merge Conflicts
- If GitHub updates cause conflict:
  - **Prompt user** to choose: `Keep GitHub` or `Keep OtterCamp`.
  - No auto‑merge UI in MVP.
  - Record decision in OtterCamp activity log.

## Failure Paths (MVP)
- **Sync fails:** surface error, keep last known SHA, log failure in activity feed.
- **Conflict unresolved:** remain in “needs decision” state; **deploys blocked** until resolved.

## Minimum API Surface (MVP)
- `POST /api/projects/:id/repo/sync` — manual resync
- `GET /api/projects/:id/repo/status` — sync state, last SHA, conflicts
- `GET /api/projects/:id/repo/branches` — default + active branches

## Data Stored
- **Repo metadata** (org_id, repo_url, default_branch, active_branches, last_synced_sha)
- **Commit metadata** (sha, author, date, subject, body)
- **File tree snapshots** (on demand for code browser)

## Acceptance Criteria
- Full commit history available locally.
- Default branch + active branches tracked.
- Manual re‑sync works and logs an activity event.
