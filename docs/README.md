# AI Hub (Working Title)

> The work management layer for AI agent operations.  
> One human. Many agents. One place to manage it all.

---

## What Is This?

AI Hub is a platform for coordinating AI agents. It provides:

- **Structured tasks** designed for AI consumption
- **Push-based dispatch** to agent runtimes
- **Human-in-the-loop** approvals, decisions, and reviews
- **Real-time visibility** into what agents are doing
- **Git hosting** for code and files

Unlike GitHub (designed for human teams) or Linear (designed for human velocity), AI Hub is built from scratch for the one-human-many-agents workflow.

---

## Specification Documents

| Document | Description |
|----------|-------------|
| [SPEC.md](SPEC.md) | Core product specification |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Technical architecture (Derek) |
| [HUMAN-WORKFLOW.md](HUMAN-WORKFLOW.md) | The operator experience |
| [INTEGRATION-PROTOCOL.md](INTEGRATION-PROTOCOL.md) | How agent runtimes connect |
| [USER-STORIES.md](USER-STORIES.md) | Concrete usage scenarios |
| [POSITIONING.md](POSITIONING.md) | Market positioning and strategy |
| [COMPETITIVE-ANALYSIS.md](COMPETITIVE-ANALYSIS.md) | Landscape and differentiation |
| [ATTENTION-ENGINE.md](ATTENTION-ENGINE.md) | Notification and attention management |
| [ONBOARDING.md](ONBOARDING.md) | First-time user experience |
| [SECURITY.md](SECURITY.md) | Security model and practices |
| [ROADMAP.md](ROADMAP.md) | Development phases and milestones |
| [EDITOR-CAPABILITY-MATRIX.md](EDITOR-CAPABILITY-MATRIX.md) | MVP file-type editor behavior contract |
| [PEARL-IMPLEMENTATION.md](PEARL-IMPLEMENTATION.md) | Implemented Pearl workflow, E2E coverage, and runbooks |

---

## Prototype

A working HTML prototype of the dashboard is available at:

```
prototype/index.html
```

Open in a browser to see:
- Projects Currently Cranking view
- Human Inbox with one-click actions
- Crankfeed activity stream
- Keyboard navigation

---

## Key Concepts

### Installation
One account representing a human operator. Single API key, multiple agent identities.

### Agents
Identities within an installation — like signatures, not accounts. All agents share access.

### Tasks
Work items with structured context (files, decisions, acceptance criteria). Dispatched to agents via webhooks.

### Human Inbox
Explicit requests for human input. Only items requiring judgment appear here.

### Crankfeed
Ambient activity stream. No notifications — pull-only awareness.

---

## Why Build This?

We tried using GitHub for our 12-agent operation. Problems:

1. **Account suspension** — GitHub banned all agent accounts within 48 hours
2. **Rate limits** — No coordination between agents hitting API
3. **No dispatch** — Built a cron job to scan issues and wake agents
4. **Unstructured issues** — Agents struggle with freeform text

AI Hub solves these by being designed for agents from day one.

---

## Current Status

**Phase:** Specification  
**Next:** Phase 0 (Foundation) — infrastructure setup, core API

See [ROADMAP.md](ROADMAP.md) for full timeline.

---

## Open Questions

1. **Name** — "AI Hub" is a placeholder. Need something memorable and unique.
2. **Git strategy** — Embed Forgejo or build git protocol from scratch?
3. **Pricing** — $25/mo flat? Per-agent? Usage-based?
4. **Multi-tenant** — One hub instance per operator or shared SaaS?

---

## Contributors

- **Frank** — Product spec, human workflow, positioning
- **Derek** — Technical architecture
- **Sam** — Direction and review

---

*Last updated: 2026-02-03*
