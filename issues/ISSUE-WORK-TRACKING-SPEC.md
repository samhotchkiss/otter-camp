# Otter Camp: Issues as the Single Work Tracking Primitive

## Context

Otter Camp currently has two overlapping systems for tracking work:

1. **Tasks** — Simple work items with agent assignment, priority, status lifecycle, and a dispatch queue. Built during the Day 1 sprint (Feb 4) as the core of the dispatch engine.
2. **Issues** — Originally built for GitHub issue/PR sync, then evolved into a content review workflow with approval states, participants, comments, and document review.

**Decision (Feb 7):** Consolidate on **issues only**. Tasks may become a filtered view on issues later, but issues are the single primitive going forward.

## Current State of Issues

### Data Model (`ProjectIssue` struct in `internal/store/project_issue_store.go`)

```go
type ProjectIssue struct {
    ID            string     `json:"id"`
    OrgID         string     `json:"org_id"`
    ProjectID     string     `json:"project_id"`
    IssueNumber   int64      `json:"issue_number"`
    Title         string     `json:"title"`
    Body          *string    `json:"body,omitempty"`
    State         string     `json:"state"`          // "open" or "closed" only
    Origin        string     `json:"origin"`
    DocumentPath  *string    `json:"document_path,omitempty"`
    ApprovalState string     `json:"approval_state"` // draft, ready_for_review, needs_changes, approved
    CreatedAt     time.Time  `json:"created_at"`
    UpdatedAt     time.Time  `json:"updated_at"`
    ClosedAt      *time.Time `json:"closed_at,omitempty"`
}
```

### Current Issue States

**State** (basic lifecycle):
- `open`
- `closed`

**Approval State** (review workflow):
- `draft` → `ready_for_review`
- `ready_for_review` → `needs_changes` | `approved`
- `needs_changes` → `ready_for_review`
- `approved` (terminal)

### Current API Endpoints (in `internal/api/router.go`, production build)

| Method | Endpoint | Handler | Purpose |
|--------|----------|---------|---------|
| GET | `/api/issues` | `issuesHandler.List` | List issues (filterable) |
| GET | `/api/issues/{id}` | `issuesHandler.Get` | Get issue detail with participants + comments |
| POST | `/api/issues/{id}/comments` | `issuesHandler.CreateComment` | Add a comment |
| POST | `/api/issues/{id}/approval-state` | `issuesHandler.TransitionApprovalState` | Change approval state |
| POST | `/api/issues/{id}/approve` | `issuesHandler.Approve` | Approve an issue |
| POST | `/api/issues/{id}/review/save` | `issuesHandler.SaveReview` | Save review checkpoint |
| POST | `/api/issues/{id}/review/address` | `issuesHandler.AddressReview` | Address review feedback |
| GET | `/api/issues/{id}/review/changes` | `issuesHandler.ReviewChanges` | View review diff |
| GET | `/api/issues/{id}/review/history` | `issuesHandler.ReviewHistory` | Review version history |
| GET | `/api/issues/{id}/review/history/{sha}` | `issuesHandler.ReviewVersion` | Specific review version |
| POST | `/api/issues/{id}/participants` | `issuesHandler.AddParticipant` | Add participant to issue |
| DELETE | `/api/issues/{id}/participants/{agentID}` | `issuesHandler.RemoveParticipant` | Remove participant |
| POST | `/api/projects/{id}/issues/link` | `issuesHandler.CreateLinkedIssue` | Create linked issue |
| POST | `/api/projects/{id}/issues/import` | `projectIssueSyncHandler.ManualImport` | Import from GitHub |
| GET | `/api/projects/{id}/issues/status` | `projectIssueSyncHandler.Status` | GitHub sync status |

### What's Missing (no endpoint exists)

- **`POST /api/projects/{id}/issues`** — There is no way to create an issue through the API. `CreateLinkedIssue` creates issues linked to documents, not standalone work items.

### CLI (`cmd/otter/main.go`)

The `otter` CLI has **zero issue commands**. Current commands:
- `otter auth login` — Store API token + org
- `otter whoami` — Validate token
- `otter project create` — Create a project
- `otter clone` — Clone a project repo
- `otter remote add` — Add origin remote
- `otter repo info` — Show repo URL
- `otter version` — Show version

---

## What Needs to Change

### 1. Add `owner_agent_id` to Issues

Issues currently have **participants** (many-to-many with roles) and **reviewer_agent_id** on review versions, but no direct assignment field.

**Add to `project_issues` table:**
```sql
ALTER TABLE project_issues ADD COLUMN owner_agent_id UUID REFERENCES agents(id);
```

This is who's responsible for the work. Participants can still exist for visibility/collaboration.

### 2. Expand Issue States for Work Tracking

Current states (`open`/`closed`) are too simple for work management. Issues need to represent the full work lifecycle.

**Proposed state expansion** (choose one approach):

**Option A — Expand the `state` enum:**
```
open → in_progress → review → closed
         ↓
       blocked → in_progress
```
Valid states: `open`, `in_progress`, `blocked`, `review`, `closed`

**Option B — Add a separate `work_status` field** (keep state as open/closed for GitHub compat):
```sql
ALTER TABLE project_issues ADD COLUMN work_status TEXT DEFAULT 'queued';
```
Values: `queued`, `in_progress`, `blocked`, `review`, `done`, `cancelled`

Option B preserves backward compatibility with GitHub sync (which only knows open/closed) while adding workflow granularity.

**For reference**, the tasks system had these statuses: `queued`, `dispatched`, `in_progress`, `blocked`, `review`, `done`, `cancelled` — with priorities P0-P3.

### 3. Add Due Dates to Issues

Issues need two time-based fields for accountability:

```sql
ALTER TABLE project_issues ADD COLUMN due_at TIMESTAMPTZ;
ALTER TABLE project_issues ADD COLUMN next_step_due_at TIMESTAMPTZ;
ALTER TABLE project_issues ADD COLUMN next_step TEXT;
```

- `due_at` — When the issue should be fully resolved
- `next_step_due_at` — When the next action is expected
- `next_step` — Description of what that next action is

The `next_step` + `next_step_due_at` pair is useful for detecting stalled work — if `next_step_due_at` passes without a state change or comment, that's a signal something's stuck.

### 4. Add Priority to Issues

Tasks had P0-P3 priority. Issues have no priority field.

```sql
ALTER TABLE project_issues ADD COLUMN priority TEXT DEFAULT 'P2';
```
Values: `P0` (critical), `P1` (high), `P2` (normal), `P3` (low)

### 5. Create Issue API Endpoint

**Add `POST /api/projects/{id}/issues`** for creating standalone issues (not linked to documents).

Request body:
```json
{
    "title": "Implement WebSocket server",
    "body": "Full description of the work to be done...",
    "owner_agent_id": "uuid-of-agent",  // optional
    "priority": "P1",                    // optional, default P2
    "labels": ["engineering", "backend"] // optional, if labels are added
}
```

Response: the created issue object.

### 6. Issue State Transition Endpoint

**Add `POST /api/issues/{id}/state`** (or `PATCH /api/issues/{id}`) for updating work status.

This should enforce valid transitions (e.g., can't go from `closed` to `in_progress` without reopening).

### 7. CLI Issue Commands

Add to the `otter` CLI:

```
otter issue create <title> --project <name> [--body <text>] [--assign <agent>] [--priority P0-P3]
otter issue list [--project <name>] [--state <state>] [--mine]
otter issue view <issue-number>
otter issue comment <issue-number> <body>
otter issue assign <issue-number> <agent>
otter issue close <issue-number>
otter issue reopen <issue-number>
```

These should call the API endpoints above. `--mine` filters to issues where the authenticated agent is the owner.

---

## Files to Modify

### Database Migration
- **New migration** (e.g., `migrations/038_issue_work_tracking.up.sql`): Add `owner_agent_id`, `work_status` (or expand `state`), `priority`, `due_at`, `next_step_due_at`, `next_step` columns to `project_issues`

### Store Layer
- **`internal/store/project_issue_store.go`**: Update `ProjectIssue` struct, `CreateProjectIssueInput`, filters, and queries to include new fields. Add state transition validation.

### API Layer
- **`internal/api/issues.go`**: Add `CreateIssue` handler, update `List` to support new filters (state, priority, owner)
- **`internal/api/router.go`**: Register new routes: `POST /api/projects/{id}/issues`, `PATCH /api/issues/{id}`

### CLI
- **`cmd/otter/main.go`**: Add `issue` command with subcommands (create, list, view, comment, assign, close, reopen)
- **`internal/ottercli/client.go`**: Add API client methods for issue operations

---

## Reference: Tasks System (for migration)

The existing tasks system has the right concepts. Key files for reference:

- **`internal/api/tasks.go`**: Task CRUD handlers, status validation
- **Task statuses**: queued, dispatched, in_progress, blocked, review, done, cancelled
- **Task priorities**: P0, P1, P2, P3
- **Task struct fields**: id, org_id, project_id, number, title, description, status, priority, context (JSONB), assigned_agent_id, parent_task_id

The `context` JSONB field and `parent_task_id` (for sub-tasks/hierarchy) are worth considering for issues as well.

---

## Summary

| Gap | What to Build | Priority |
|-----|---------------|----------|
| No issue creation API | `POST /api/projects/{id}/issues` | **Must have** |
| No agent assignment | Add `owner_agent_id` column + API support | **Must have** |
| No work states | Add `work_status` field (queued/in_progress/blocked/review/done/cancelled) | **Must have** |
| No priority | Add `priority` field (P0-P3) | **Should have** |
| No due dates | Add `due_at`, `next_step_due_at`, `next_step` fields | **Must have** |
| No CLI issue commands | `otter issue create/list/view/comment/assign/close` | **Must have** |
| No state transitions | `PATCH /api/issues/{id}` with validation | **Should have** |
