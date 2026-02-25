# 018: Project Service

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | S (≤1 day) |
| Spec refs | doc 03 §ProjectService, doc 03 §FlowTemplateImmutability, doc 03 §TaskScheduleService, doc 03a §DeliveryModes |
| Spec status | finished |
| Depends on | 016, 017, 013 |
| Blocks | 019, 028, 031 |

## Scope

Implement project CRUD, flow template CRUD with immutability enforcement, task schedule
CRUD with cron validation and overlap policy, and project slug uniqueness logic. Delivers
`internal/project/service.go`. No API handlers here (task 019).

### Must build

**`internal/project/service.go` — `ProjectService` interface and implementation:**

```go
type ProjectService interface {
    // Project CRUD
    Create(ctx context.Context, req CreateProjectRequest) (*Project, error)
    Get(ctx context.Context, orgID, projectID uuid.UUID) (*Project, error)
    GetBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*Project, error)
    List(ctx context.Context, orgID uuid.UUID, filter ProjectFilter) ([]*Project, error)
    Update(ctx context.Context, orgID, projectID uuid.UUID, req UpdateProjectRequest) (*Project, error)
    Delete(ctx context.Context, orgID, projectID uuid.UUID) error

    // Flow template CRUD
    CreateFlowTemplate(ctx context.Context, req CreateFlowTemplateRequest) (*FlowTemplate, error)
    GetFlowTemplate(ctx context.Context, orgID, templateID uuid.UUID) (*FlowTemplate, error)
    ListFlowTemplates(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID) ([]*FlowTemplate, error)
    UpdateFlowTemplate(ctx context.Context, orgID, templateID uuid.UUID, req UpdateFlowTemplateRequest) (*FlowTemplate, error)

    // Flow node CRUD (within a template)
    AddFlowNode(ctx context.Context, templateID uuid.UUID, req AddFlowNodeRequest) (*FlowNode, error)
    UpdateFlowNode(ctx context.Context, nodeID uuid.UUID, req UpdateFlowNodeRequest) (*FlowNode, error)
    RemoveFlowNode(ctx context.Context, nodeID uuid.UUID) error
    GetFlowNodes(ctx context.Context, templateID uuid.UUID) ([]*FlowNode, error)

    // Task schedule CRUD
    CreateSchedule(ctx context.Context, req CreateScheduleRequest) (*TaskSchedule, error)
    GetSchedule(ctx context.Context, scheduleID uuid.UUID) (*TaskSchedule, error)
    ListSchedules(ctx context.Context, projectID uuid.UUID) ([]*TaskSchedule, error)
    UpdateSchedule(ctx context.Context, scheduleID uuid.UUID, req UpdateScheduleRequest) (*TaskSchedule, error)
    DeleteSchedule(ctx context.Context, scheduleID uuid.UUID) error
}
```

**Project CRUD rules:**
- `Create`: validate slug format (`^[a-z0-9-]+$`, max 64 chars); `GetBySlug` to check uniqueness; return `ErrSlugTaken` if exists
- `Update`: slug changes are allowed; re-check uniqueness; emit `project.updated` domain event
- `Delete`: check for active tasks (work_status not in `done, cancelled`) before deleting; return `ErrProjectHasActiveTasks` if found (check applied at service layer via query)

**Flow template immutability enforcement** (doc 03):
- `UpdateFlowTemplate`: updating an in-use template (has active `project_task` rows referencing it) creates a NEW version row (new ID, `version+1, is_current=true`) and sets the old row to `is_current=false`; the old row is NOT modified — it is preserved for audit and for in-flight tasks that still reference it
- If the template has never been used (no task rows reference it), update the existing row in-place
- `in-use` check: `SELECT 1 FROM project_task WHERE flow_template_id = $1 AND work_status NOT IN ('done','cancelled') LIMIT 1`
- Emit `flow_template.version_created` domain event on new version creation
- System templates (`is_system=true`) cannot be updated; return `ErrSystemTemplateProtected`

**Flow node management:**
- `AddFlowNode`: validate `actor_id` exists in `agent` table if `actor_type='agent'` (application-layer FK check)
- `RemoveFlowNode`: if template is in-use, return `ErrTemplateInUse` (cannot modify nodes of active template — must create new version first)
- Setting `start_node_id` on a template: call `FlowTemplateRepo.Update` with `start_node_id` after nodes are created; validate the node belongs to the template

**Task schedule CRUD:**
- `CreateSchedule`: validate `cron_expression` using a Go cron parser (standard 5-field: `MIN HOUR DOM MON DOW`); return `ErrInvalidCronExpression` with the parser error message if invalid
- Compute `next_fire_at` from `now()` + cron expression; store it
- `UpdateSchedule`: re-validate cron on change; recompute `next_fire_at`
- `DeleteSchedule`: allowed; hard delete; active tasks spawned from this schedule are unaffected

**Cron expression constraints:**
- Minimum interval: do not allow schedules firing more than once per minute (cron is already per-minute minimum)
- Maximum interval: no upper limit
- Use `github.com/robfig/cron/v3` for parsing

### Must NOT build
- Task creation, flow advancement (task 028, 030)
- Delivery mode enforcement (task 031)
- Schedule tick execution (task 065)
- Project API handlers (task 019)

## Acceptance Criteria

- [ ] `ProjectService.Create` with duplicate slug in same org returns `ErrSlugTaken`
- [ ] `ProjectService.Create` with duplicate slug in different org succeeds
- [ ] `ProjectService.Delete` with active tasks (work_status='in_progress') returns `ErrProjectHasActiveTasks`
- [ ] `UpdateFlowTemplate` on an in-use template: new version row created; old row `is_current=false`; old row `id` unchanged
- [ ] `UpdateFlowTemplate` on a not-in-use template: existing row updated in-place; no new version row
- [ ] `UpdateFlowTemplate` on `is_system=true` template returns `ErrSystemTemplateProtected`
- [ ] `CreateSchedule` with invalid cron expression (e.g. `"not-a-cron"`) returns `ErrInvalidCronExpression`
- [ ] `CreateSchedule` with valid cron expression stores correct `next_fire_at`

## Tests Required

**Unit tests:**
- Slug validation: valid slugs pass; slugs with uppercase, spaces, leading hyphens fail
- Cron validation: valid expressions (`"0 9 * * 1-5"`) succeed; invalid expressions fail with `ErrInvalidCronExpression`
- `next_fire_at` computation: verify correct timestamp computed for a known cron + known `now()`

**Integration tests:**
- `ProjectService.Create` + `GetBySlug`: round-trip; duplicate slug in same org → error; duplicate in different org → success
- `UpdateFlowTemplate` in-use check: seed a `project_task` row in `in_progress`; call Update → new version created; verify DB row counts
- `UpdateFlowTemplate` not-in-use: no task rows referencing template; Update → in-place modification
- `CreateSchedule` + `ListSchedules`: schedules scoped to project; different project does not appear in list
- `DeleteSchedule`: hard delete verified; associated tasks unaffected

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

- The flow template versioning pattern mirrors `model_profile` versioning in task 010: the `logical_profile_id` equivalent here is the `slug`, and the `is_current` flag is used the same way. New tasks always pick up the current version via `FlowTemplateRepo.GetCurrentBySlug`.
- In-use check must be fast. The query `SELECT 1 FROM project_task WHERE flow_template_id = $1 AND work_status NOT IN ('done','cancelled') LIMIT 1` uses the existing index on `(project_id)` — add `(flow_template_id, work_status)` index to `project_task` in task 027 to support this query.
- `ProjectService.Delete` performs a soft check only; actual cascade deletes of tasks are rejected. An org admin who wants to delete a project with active tasks must first cancel all tasks manually. This is a safety gate, not a technical constraint.
- For `AddFlowNode`, the `actor_id` existence check queries `AgentRepo.GetByID`. If the agent does not exist (or belongs to a different org), return `ErrAgentNotFound`. This check is a best-effort validation; concurrent agent deletion is not guarded against at the DB level.
- Emit domain events for project lifecycle changes. `project.created`, `project.updated`, `project.deleted` events are used by the supervisor (task 053) and the activity feed.

