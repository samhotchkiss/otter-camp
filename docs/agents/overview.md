# Agents: Overview

> Summary: OtterCamp's agent model — three permanent agents, chameleon instances, and how this differs from OpenClaw.
> Last updated: 2026-02-16
> Audience: Agents and maintainers implementing agent operations.

## The Three Permanent Agents

Every OtterCamp install has exactly three permanent agents:

1. **Frank** — Chief of Staff. Orchestrator and coordinator. Routes work, manages priorities, talks to the human.
2. **Lori** — People manager. Owns agent lifecycle — hiring, firing, staffing decisions, workload management, coordination around blocked/overloaded queues.
3. **Ellie** — Memory and compliance system. Extracts memories from conversations, retrieves context, injects proactive context, handles compliance review.

These three are always running. They are the operational backbone.

## Chameleon Agents

Everyone else is a **definition** — a personality, skillset, and context package that gets loaded into a chameleon agent instance on demand.

Examples of agent definitions:
- Josh S (Head of Engineering) — spun up when architecture/issues/testing work is needed
- Derek (Engineering Lead) — spun up for special projects and implementation
- Jeremy H (Head of QC) — spun up for code review and merge gating
- Jeff G (Head of Design) — spun up for UI/UX and visual identity work
- Nova, Stone, Ivy, etc. — spun up for their respective domains

Chameleon agents:
- **Don't persist.** They exist for the duration of their task.
- **Load their definition at runtime.** Identity, personality (SOUL.md), skills, and relevant context get injected when the chameleon instance starts.
- **Have full capability while active.** They're not lesser agents — they have the same powers as permanent agents during their lifetime.
- **Dissolve when done.** Work product is committed; the agent instance goes away.

## How This Differs from OpenClaw

| Aspect | OpenClaw (legacy) | OtterCamp |
|---|---|---|
| Agent count | 13+ always-running | 3 permanent + chameleon on-demand |
| OpenClaw agents used | 13+ | Exactly 5 (main, elephant, ellie-extractor, lori, chameleon) |
| Identity | Fixed, persistent workspaces | Definitions loaded at runtime into chameleon |
| Lifecycle | Always on, heartbeat-monitored | Spun up when needed, dissolve when done |
| Resource usage | High (13 concurrent agents) | Low (3 + burst capacity) |

**Key insight:** The 13-agent OpenClaw roster was the precursor. In OtterCamp, only Frank (main), Ellie (elephant + ellie-extractor), and Lori are permanent. Everyone else becomes a chameleon definition loaded on demand. The bridge must never create sessions under non-permanent agent IDs.

**See `docs/agents/openclaw-bridge-routing.md` for the definitive routing specification.**

## Agent Model in Code

Core store: `internal/store/agent_store.go`

Agents are workspace-scoped identities with:
- `slug`, `display_name`, `status`
- optional `session_pattern`
- ephemeral vs non-ephemeral classification

Admin routes (`internal/api/router.go`):
- create/list/get agents
- retire/reactivate agents
- ping/reset via bridge dispatch
- inspect agent files and memory files

Runtime state is merged from:
- persisted agent rows
- OpenClaw sync snapshots (`agent_sync_state`, sync metadata)

## Starter Trio (Onboarding)

On `otter init` / onboarding bootstrap, OtterCamp seeds Frank, Lori, and Ellie.

Source: `internal/api/onboarding.go`

## Related Docs

- `docs/agents/openclaw-bridge-routing.md` — **How OtterCamp routes to OpenClaw** (five authorized agents, session key formats, routing rules)
- `docs/agents/loris-role.md` — Lori's responsibilities
- `docs/agents/hiring-and-firing.md` — Agent lifecycle operations
- `docs/agents/runtime-modes.md` — Local vs hosted behavior
- `docs/memories/ellies-role.md` — Ellie's responsibilities

## Change Log

- 2026-02-19: Updated with five-agent OpenClaw model reference, linked to bridge routing doc.
- 2026-02-16: Major rewrite. Documented three-permanent-agent model, chameleon concept, and distinction from OpenClaw's 13-agent roster.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
