# 025: Agent Project Assignment and Skill Attachment

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | S (≤1 day) |
| Spec refs | doc 05 §AgentProjectAssignment, doc 05 §AgentSkillAttachment, doc 10 §AgentSkillAttachment |
| Spec status | finished |
| Depends on | 013, 011, 016 |
| Blocks | 026, 028, 030, 033 |

## Scope

Build the two agent join tables — `agent_project_assignment` and `agent_skill_attachment` —
along with their repository layers and the service logic for assigning agents to projects and
attaching skills. Includes PM role enforcement (exactly one PM per project) and priority
ordering for skill attachments.

### Must build

**Migrations:**
- `0030_agent_project_assignment.sql`
- `0031_agent_skill_attachment.sql`

**`agent_project_assignment` table** (doc 05):
- `id uuid primary key default gen_random_uuid()`
- `agent_id uuid not null references agent(id) on delete cascade`
- `project_id uuid not null references project(id) on delete cascade`
- `role text not null check (role in ('pm','worker','reviewer','observer'))` — PM is the primary manager role
- `assigned_by_type text not null check (assigned_by_type in ('human_user','agent','system'))`
- `assigned_by_id uuid` — null for system sentinel (00000000-0000-0000-0000-000000000000)
- `is_active boolean not null default true`
- `assigned_at timestamptz not null default now()`
- `deactivated_at timestamptz`
- Unique constraint: `(agent_id, project_id)` — one row per agent per project (role changes update the existing row)
- Partial unique index: `(project_id) WHERE role = 'pm' AND is_active = true` — exactly one active PM per project
- Index: `(project_id, role, is_active)`
- Index: `(agent_id, is_active)`

**`agent_skill_attachment` table** (doc 05, doc 10):
- `id uuid primary key default gen_random_uuid()`
- `agent_id uuid not null references agent(id) on delete cascade`
- `skill_id uuid not null references skill(id) on delete cascade`
- `priority integer not null default 100` — lower number = higher priority; controls prompt injection order
- `attached_by_type text not null check (attached_by_type in ('human_user','agent','system'))`
- `attached_by_id uuid` — null for system sentinel
- `is_active boolean not null default true`
- `attached_at timestamptz not null default now()`
- `deactivated_at timestamptz`
- Unique constraint: `(agent_id, skill_id)` — one attachment per (agent, skill) pair; reactivation updates the existing row
- Index: `(agent_id, is_active, priority)` — ordered skill list query
- Index: `(skill_id)` — cross-reference: which agents use this skill?

**Repository layer:**
- `AgentProjectAssignmentRepo`: `Assign`, `Deactivate`, `GetByAgentAndProject`, `ListByAgent`, `ListByProject`, `GetPM`
- `AgentSkillAttachmentRepo`: `Attach`, `Detach`, `ListByAgent`, `ListBySkill`, `SetPriority`, `GetByAgentAndSkill`

**Service layer (`internal/agent/assignment_service.go`):**
- `AssignToProject(ctx, agentID, projectID, role, assignedBy)` — creates or reactivates row; enforces PM uniqueness (if role='pm', deactivate prior PM first within a transaction)
- `RemoveFromProject(ctx, agentID, projectID)` — sets `is_active=false`, `deactivated_at=now()`; if removed agent was PM, fires `agent.pm_removed` domain event
- `AttachSkill(ctx, agentID, skillID, priority, attachedBy)` — creates or reactivates row
- `DetachSkill(ctx, agentID, skillID)` — sets `is_active=false`
- `ReorderSkills(ctx, agentID, priorityMap map[uuid.UUID]int)` — bulk update priorities in one transaction

### Must NOT build
- Agent API endpoints for assignments and skills (task 026)
- Project membership validation for MCP resource reads (task 055)
- Flow node actor assignment (task 017 already handles the `actor_id` field)
- Skill content loading for prompt assembly (task 050)

## Acceptance Criteria

- [ ] Migration `0030_agent_project_assignment.sql` applies cleanly; partial unique index on `(project_id) WHERE role='pm' AND is_active=true` is present
- [ ] `AssignToProject` with `role='pm'` deactivates the prior PM row before inserting the new one, atomically
- [ ] Assigning a second PM to the same project without deactivating the first returns `ErrPMConflict`
- [ ] `RemoveFromProject` sets `is_active=false` and records `deactivated_at`; does not hard-delete the row
- [ ] Migration `0031_agent_skill_attachment.sql` applies cleanly; unique constraint on `(agent_id, skill_id)` is present
- [ ] `AttachSkill` on a previously detached `(agent_id, skill_id)` reactivates the existing row rather than inserting a duplicate
- [ ] `AgentSkillAttachmentRepo.ListByAgent` returns only `is_active=true` rows, ordered by `priority ASC`
- [ ] `ReorderSkills` updates all specified priorities in a single transaction; partial list is valid (only listed skills updated)

## Tests Required

**Unit tests:**
- `AssignToProject` PM swap: mock repo; verify deactivate-old-PM then insert-new-PM sequence; verify both calls share the same transaction
- `AttachSkill` idempotency: reattach a detached skill → existing row updated, no duplicate inserted
- `ReorderSkills`: priority map applied correctly; skills not in the map retain their existing priority

**Integration tests:**
- PM uniqueness: assign agent A as PM → assign agent B as PM → verify A's row `is_active=false`, B's row `is_active=true`; query `GetPM(projectID)` returns B
- Partial unique index: insert two rows with `role='pm'` and `is_active=true` for the same project directly → DB rejects with unique violation
- `agent_skill_attachment`: attach skill → detach → re-attach; verify row count stays at 1; `is_active` toggles correctly
- `ListByAgent` ordering: attach three skills with priorities 200, 50, 100; verify returned order is 50, 100, 200

**E2E tests:**
- None — covered by dedicated E2E task 085

## Implementer Notes

- The partial unique index on `(project_id) WHERE role='pm' AND is_active=true` is the DB-level enforcement of the one-PM-per-project rule. Application logic must always deactivate the prior PM row before inserting the new one, but the index serves as the final safety net against concurrent races.
- `assigned_by_id` and `attached_by_id` follow the polymorphic convention: when the assigning actor is the system (bootstrap, auto-assignment), store `null` (or the sentinel UUID `00000000-0000-0000-0000-000000000000` if the application layer always requires a non-null value for display). The system sentinel convention is established in the polymorphic FK catalog.
- Deactivation is always soft — rows are never deleted. This preserves the audit trail of who was assigned when.
- Skill priority ordering is the injection order used by the prompt assembly engine (task 050). Lower priority number = injected earlier (or more prominently). The default of 100 leaves room for both high-priority (1–99) and low-priority (101+) placements without collision.
