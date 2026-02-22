# 14. Open Questions and Build Phasing

## Biggest Open Product Decisions

- How strong is default human approval for risky actions?
- What is the managed-hosting launch shape vs self-host launch shape?
- Minimum bootstrap dataset that ships with V2 (starter trio profiles, default flow templates, default skills)?

## Build Phases

### Phase 0: Foundation

- Freeze domain model and API contracts.
- Implement modular monolith boundaries in one codebase (API service + worker process).
- Auth/tenancy baseline.
- Model gateway abstraction with concurrency limits and per-provider queuing.
- Event bus and run/job framework.
- Agent identity and prompt assembly pipeline.

### Phase 1: Synchronous Chat via TUI

The first usable product. A human can open the TUI and talk to agents in real-time.

- TUI application with sync chat sessions (org-scoped).
- Starter trio seeded on bootstrap (Frank, Lori, Ellie).
- Agent runtime model: prompt assembly with identity, policies, skills, memory, conversation history.
- Ellie's passive memory injection (simple scoped retrieval — no vector infrastructure required yet).
- Model gateway connected to at least one provider.
- Session scoping (org scope first, project/task scope comes in Phase 2).

**Milestone: you can brainstorm with Frank, ask Lori about staffing, ask Ellie what she remembers.**

### Phase 2: Projects and Tasks

The system can manage work, not just chat.

- Project and task CRUD with full lifecycle (draft → queued → in_progress → done).
- Flow templates and flow nodes with agent assignment.
- Task-scoped sessions (async mode) — agents can work autonomously.
- Blocker escalation via task dependencies.
- Runs linked to tasks and flow nodes (control plane integration).
- Skills as markdown documents, activated per flow node.
- Temp agent lifecycle (hire/fire within a project).

**Milestone: you can create a project, Frank and Lori staff it, agents work tasks autonomously, you review results.**

### Phase 3: OtterCamp Builds Itself

Development moves inside OtterCamp. The system is building itself.

- Web UI (full dashboard, task boards, chat, agent management).
- MCP connection manager and policy layer.
- CLI and browser control runtime.
- Improved model routing, budget controls, and cost tracking.
- Full observability stack.
- Memory pipeline hardening (compaction, decay, contradiction detection).

**Milestone: OtterCamp development happens within OtterCamp. Agents are building the product.**

### Phase 4: Hardening and Distribution

- Security hardening, audit expansion, retention controls.
- Self-host packaging (Docker Compose) and upgrade path.
- Mobile UI for monitoring and quick actions.
- Multi-tenant managed operations and billing controls.

## Immediate Deep-Dive Order

1. Auth/tenancy/identity contracts (foundation for everything)
2. Model gateway and provider abstraction (need this before chat works)
3. Chat session + context architecture + prompt assembly (Phase 1 core)
4. Agent profiles and starter trio bootstrap (Phase 1 core)
5. Project/task flow contracts (Phase 2)
6. Control plane and run execution (Phase 2)
7. Memory pipeline details (iterative across phases)
