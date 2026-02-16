# Agents: Hiring and Firing

> Summary: Agent lifecycle operations — creating chameleon instances, retiring them, and the protected permanent agents.
> Last updated: 2026-02-16
> Audience: Agents and admins managing roster changes.

## Agent Types

### Permanent Agents (Protected)
Frank, Lori, and Ellie are permanent. They cannot be retired. They are seeded on onboarding and always running.

### Chameleon Agents (On-Demand)
All other agents are chameleon instances — spun up from a definition when needed, dissolved when done. Lori manages their lifecycle.

## Hire / Create a Chameleon Instance

Primary endpoint:
- `POST /api/admin/agents`

Flow:
1. Lori (or admin) decides a task needs a specific agent definition
2. Chameleon instance is created with the definition's identity, personality, and skills loaded
3. Instance is active for the duration of its task
4. Workspace/org scoping is enforced
5. Valid slot/name/model shape required

## Retire a Chameleon Instance

Endpoints:
- `POST /api/admin/agents/{id}/retire`
- `POST /api/admin/agents/retire/project/{projectID}`

Behavior:
- Status transitions to `retired`
- **Protected permanent agents (Frank, Lori, Ellie) are blocked from retirement**
- Work product should be committed before retirement
- Lifecycle updates can include filesystem/config cleanup

## Reactivate

Endpoint:
- `POST /api/admin/agents/{id}/reactivate`

Behavior:
- Status transitions back to `active`
- Bridge/runtime control may be required for immediate availability
- Definition is re-loaded into the chameleon instance

## Change Log

- 2026-02-16: Rewritten with chameleon model, permanent agent protection, and Lori's lifecycle role.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
