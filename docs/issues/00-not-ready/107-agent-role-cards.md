# Issue #107 — Agent Role Cards

> ⚠️ **NOT READY** — Vision spec, not yet scoped for implementation.

## Inspiration

[Voxyz (@Voxyz_ai) tweet](https://x.com/voxyz_ai/status/2020633743401345158) — "every agent now has a role card. what they own, what they deliver, what they can't touch, when to escalate. went from vibes to actual job descriptions"

Their system has 6 agents with a visual "Meet the Team" page showing:
- Hierarchical layout (orchestrator → input/output specialists → observer)
- Each agent card has: avatar, name, archetype tag, role description
- Expanded role card detail panel with ownership, deliverables, boundaries, escalation rules
- Three-tier visual flow: coordinator at top, specialists in middle (input vs output), meta-layer at bottom

## Concept for Otter Camp

Build a **Role Card** primitive for each agent in the Otter Camp agent management UI. This goes beyond the current agent cards (#103) by making agent roles, boundaries, and escalation rules first-class structured data — not just freeform text in SOUL.md/IDENTITY.md.

### Role Card Fields

| Field | Description | Example |
|-------|-------------|---------|
| **Name** | Display name | Frank |
| **Role** | Job title | Chief of Staff |
| **Archetype** | Personality tag | The Orchestrator |
| **Avatar** | Visual identity | Custom image or generated |
| **Owns** | What this agent is responsible for | Cross-agent coordination, Slack routing, Codex pipeline |
| **Delivers** | What outputs this agent produces | Morning briefings, issue specs, progress summaries |
| **Boundaries** | What this agent should NOT do | Direct code commits, external messaging without review |
| **Escalates When** | Trigger conditions for human involvement | Destructive actions, ambiguous priorities, external comms |
| **Reports To** | Hierarchy | Sam (direct) |
| **Direct Reports** | Agents under this one | All 12 agents |
| **Channels** | Primary Slack/comms channels | #leadership, all channels (listening) |

### Visual Team Page

A "Meet the Team" view in Otter Camp showing:
- All agents in a hierarchical/org-chart layout
- Click to expand full role card
- Status indicators (active, idle, stalled, offline)
- Quick-access to agent's recent activity, open issues, and chat

### How It Differs from Current Agent Cards (#103)

- #103 is about agent CRUD and status display
- This is about **structured role definitions** that agents themselves can reference
- Role cards could be used by the Pipeline system (#105) to auto-assign roles
- Could replace or complement SOUL.md/IDENTITY.md with structured data that the UI can render

### Integration Points

- **Pipeline (#105)**: Role cards define who can be Planner, Worker, Reviewer per project
- **Agent Management (#103)**: Role cards are the detail view for each agent
- **Issues**: Auto-suggest assignment based on "Owns" field
- **Chat/DMs**: Show role card on hover over agent name

## Open Questions

1. Should role cards fully replace SOUL.md/IDENTITY.md, or complement them?
2. Should agents be able to edit their own role cards?
3. How granular should "Owns" and "Boundaries" be? Free-text vs structured rules?
4. Should there be a visual org-chart builder or just a flat list?

## References

- Voxyz tweet + image showing their "Meet the Team" page
- Our current org chart in AGENTS.md
- Agent management spec (#103)
- Pipeline spec (#105)
