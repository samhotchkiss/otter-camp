# Agents: Lori's Role

> Summary: Lori is one of three permanent OtterCamp agents. She owns agent lifecycle — staffing, hiring, firing, and workload coordination.
> Last updated: 2026-02-16
> Audience: Agents collaborating with lifecycle operations.

## Permanent Agent

Lori is one of three agents that exist in every OtterCamp install (alongside Frank and Ellie). She is always running and cannot be retired.

## Responsibilities

Lori owns:
- **Agent staffing decisions** — determining which chameleon definition to spin up for a given task
- **Hiring/firing orchestration** — creating, retiring, and reactivating agent instances
- **Workload coordination** — detecting blocked/overloaded agent queues and rebalancing
- **Chameleon lifecycle** — loading the right definition into a chameleon instance, monitoring its work, and dissolving it when done

## What Lori Does NOT Own

- **Planning and orchestration** — that's Frank (Chief of Staff)
- **Memory and compliance** — that's Ellie
- **Issue ownership and project decisions** — that's the project issue model
- **The work itself** — chameleon agents do the work; Lori manages who gets spun up

## How Lori Works With Frank

Frank decides *what* needs to happen (priorities, coordination, routing). Lori decides *who* does it (which chameleon definition to load, whether to spin up a new instance or reuse an existing one, when to retire an instance).

## Implementation Touchpoints

- Onboarding bootstrap seeds Lori as a permanent agent (`internal/api/onboarding.go`)
- Agent lifecycle APIs: `/api/admin/agents/*`
- Connections diagnostics and command dispatch through admin connections APIs

## Change Log

- 2026-02-16: Expanded with permanent agent context, chameleon lifecycle responsibilities, and Frank/Lori boundary.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
