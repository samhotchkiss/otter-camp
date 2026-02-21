# Agents: OpenClaw Bridge Routing

> Summary: How OtterCamp routes messages to OpenClaw agents via the bridge. Defines the five authorized agents, session key formats, and routing rules.
> Last updated: 2026-02-19
> Audience: Agents and engineers working on bridge, API, or session routing.

## The Five OpenClaw Agents

OtterCamp communicates with exactly five agents in OpenClaw. No other agent should ever be invoked directly by the bridge.

| Agent ID | Name | Model | Role | Session Scope |
|---|---|---|---|---|
| `main` | Frank | Opus | Chief of Staff, orchestrator | Global (main session) |
| `elephant` | Ellie | Sonnet | Memory agent, context injection | Per-request |
| `ellie-extractor` | Ellie (extraction) | Haiku | Memory extraction from conversations | Per-extraction |
| `lori` | Lori | Opus | People manager, agent lifecycle | Per-request |
| `chameleon` | (varies) | Sonnet | Identity-injected agent for all project/issue work | Per-project or per-issue |

### Agent Descriptions

**main (Frank)** — Messages from OtterCamp addressed to Frank route to the main OpenClaw session. This is a true pass-through: OtterCamp webchat → bridge → Frank's main session. Frank's conversations are never project-scoped; he is always global. When Slack is deprecated, OtterCamp will be the sole channel to Frank.

**elephant (Ellie)** — The memory and context agent. Injects proactive context into sessions, retrieves memories on demand. Uses Sonnet for balanced speed and quality.

**ellie-extractor** — A dedicated extraction instance that processes conversation logs to extract memories, projects, and issues. Uses Haiku to minimize cost and rate limit impact. Never uses Opus.

**lori** — People manager. Owns agent lifecycle decisions (hiring, firing, staffing). Uses Opus for high-quality reasoning on staffing decisions. Invoked on-demand, not continuously.

**chameleon** — The universal agent slot. All non-permanent agent work routes through chameleon. OtterCamp injects the full identity (personality, skills, context) of the assigned agent definition at session creation time. A chameleon session is scoped to a project or issue and dissolves when done.

## Session Key Formats

### Frank (main)

Messages to Frank route to his **existing main session**, not a new project-scoped session.

```
Bridge method: agent (not chat.send)
Target: Frank's active main session key
```

This means OtterCamp messages interleave with other main-session traffic (Slack, cron, heartbeats). This is intentional — Frank is global.

### Chameleon (project chat)

```
agent:chameleon:oc:{project_uuid}
```

The session is seeded with the identity of the **project's primary agent** (the project manager). For example, if the Technonymous project's lead agent is Josh S, the chameleon session gets Josh S's identity injected.

### Chameleon (issue chat)

```
agent:chameleon:oc:{project_uuid}:{issue_uuid}
```

The session is seeded with the identity of the **issue owner**. This is a more focused scope than project chat.

### Ellie / Ellie-Extractor / Lori

These use bridge-internal session keys managed by the bridge itself. They are not directly addressable from OtterCamp's project/issue chat UI.

## Routing Rules

### Non-Permanent Agent Remapping

When the OtterCamp API resolves a project's lead agent (e.g., `technonymous`, `derek`, `nova`), the bridge **must not** create a session under that agent ID in OpenClaw. Instead:

1. The bridge identifies the agent slug from the dispatch event
2. If the slug is `main`, route to Frank's main session
3. If the slug is `elephant`, `ellie-extractor`, or `lori`, route to that specific agent
4. **For all other slugs**, route to `chameleon` using the `agent:chameleon:oc:{scope}` key format
5. The bridge injects the resolved agent's identity into the chameleon session

### What Must Never Happen

- A session key like `agent:technonymous:project:...` — this creates a dedicated OpenClaw agent for a chameleon definition
- A session key like `agent:derek:issue:...` — same problem
- Any agent other than the five listed above being invoked in OpenClaw

### Future: Multi-Agent Project Chat

The session key structure supports multiple agents in the same project chat. Each agent instance would get its own session key:

```
agent:chameleon:oc:{project_uuid}:{agent_slug}
```

All instances read from the same project chat room in OtterCamp. The bridge would manage multiple concurrent chameleon sessions per project. This is not implemented yet but the key format is forward-compatible.

## OpenClaw Config Requirements

The OpenClaw gateway config (`openclaw.json`) must include these five agents in `agents.list`:

```json
{ "id": "main", "heartbeat": { "every": "15m" } }
{ "id": "elephant", "model": "anthropic/claude-sonnet-4-6" }
{ "id": "ellie-extractor", "model": "anthropic/claude-haiku-4-5" }
{ "id": "lori", "model": "anthropic/claude-opus-4-6" }
{ "id": "chameleon", "model": "anthropic/claude-sonnet-4-6" }
```

Other agents may exist in the config for Slack channel routing (legacy), but OtterCamp bridge routing must only target these five.

## Change Log

- 2026-02-19: Created. Defines the five-agent routing model, session key formats, and routing rules for the OtterCamp bridge.
