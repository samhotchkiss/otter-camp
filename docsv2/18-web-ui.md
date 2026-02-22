# 18. Web UI

## Status: Stub — to be fleshed out.

## Purpose

The primary graphical interface for OtterCamp. Full-featured dashboard for managing projects, chatting with agents, reviewing work, and monitoring the system.

## Key Capabilities (to be designed)

- Dashboard: action items, project status, progress since last visit, agent activity feed.
- Chat: org/project/task-scoped sessions, multi-agent conversations, real-time streaming.
- Projects: task boards (kanban), task detail with structured context, flow visualization, dependency graphs.
- Agents: directory, profiles, activity timelines, staffing management via Lori.
- Settings: org/project configuration, model profiles, concurrency limits, skill management, MCP connections.
- Observability: run history, cost tracking, queue depth, approval queue.

## Design Considerations

- Real-time by default (WebSocket/SSE for live updates).
- Dark mode by default.
- Command bar for keyboard-driven navigation (Superhuman-style, from V1 spec F15).
- Designed for operators, not teams — one person's view of their operation.

## Open Questions

- Frontend framework (React, Svelte, etc.)?
- SSR vs SPA vs hybrid?
- How much feature parity with TUI is required at launch?
- Mobile-responsive web vs dedicated mobile app?
