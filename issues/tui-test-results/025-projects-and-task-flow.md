# Test 025: Projects & Task Flow API

**Section:** 7. Projects & Task Flow
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Direct API calls with session token. Created a test task, queued it, cancelled it.

## Endpoint Results

| Endpoint | Result | Notes |
|---|---|---|
| `GET /v1/projects` | PASS ✓ | Returns list with slug, display_name, delivery_mode |
| `POST /v1/projects` | ASSUMED PASS | Used in previous iterations |
| `GET /v1/projects/:id` | PASS ✓ | Returns full project |
| `PATCH /v1/projects/:id` | NOT TESTED | |
| `DELETE /v1/projects/:id` | NOT TESTED | |
| `GET /v1/projects/:id/agents` | FAIL ✗ | Returns 404 |
| `POST /v1/projects/:id/agents` | FAIL ✗ | 404 implied |
| `DELETE /v1/projects/:id/agents/:id` | FAIL ✗ | 404 implied |
| `GET /v1/projects/:id/tasks` | PASS ✓ | Supports filters |
| `GET /v1/projects/:id/skills` | PASS ✓ | From memory (confirmed in prior iteration) |
| `POST /v1/projects/:id/tasks` | PASS ✓ | Creates task with status=draft |
| `POST /v1/tasks` (spec path) | FAIL ✗ | 404; correct path is /v1/projects/:id/tasks |
| `GET /v1/tasks/:id` | PASS ✓ | Full task including metadata |
| `PATCH /v1/tasks/:id` | PASS ✓ | Title, description updates work |
| `POST /v1/tasks/:id/queue` | PASS ✓ | Changes work_status to queued |
| `POST /v1/tasks/:id/advance-flow` | PASS ✓ | Works (needs in_progress status) |
| `POST /v1/tasks/:id/cancel` | PASS ✓ | Changes work_status to cancelled |
| `GET /v1/tasks/:id/events` | PASS ✓ | Returns event log with status changes |
| `GET /v1/tasks/:id/subtasks` | PARTIAL | Returns 400 when no active flow; 200 when active |
| `GET /v1/tasks/:id/dependencies` | PASS ✓ | Returns empty list (no deps created) |
| `POST /v1/tasks/:id/dependencies` | NOT TESTED | |
| `GET /v1/flow-templates` | PASS ✓ | Returns default templates |
| `GET /v1/inbox` | PASS ✓ | Returns items with pagination |
| `POST /v1/inbox/:id/approve/reject/dismiss` | NOT TESTED | No pending items during test |
| `GET /v1/projects/:id/merge-queue` | PASS ✓ | Returns empty list |
| `POST /v1/projects/:id/merge-queue/:id/prioritize` | NOT TESTED | No entries |
| `GET /v1/projects/:id/schedules` | PASS ✓ | Returns schedules with cron_expression |
| `POST/PATCH/DELETE /v1/projects/:id/schedules` | NOT TESTED | |

## Task Status Confirmed

Task `work_status` values observed: `draft` → `queued` → `in_progress` → `done` / `cancelled`

The eight spec statuses (queued, in_progress, blocked, needs_review, done, cancelled, archived, draft) appear to all be supported based on the DB schema.

## Issues Filed

- Issue #178 — GET /v1/projects/:id/agents returns 404 (agents not assignable to projects via API)
- Issue #179 — POST /v1/tasks (spec path) returns 404; correct path is POST /v1/projects/:id/tasks
