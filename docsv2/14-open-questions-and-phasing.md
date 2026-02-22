# 14. Open Questions and Build Phasing

## Biggest Open Product Decisions

- How much of current task API naming should remain (`issues` vs `project-tasks`)?
- Do we add full dependency graph execution now or later?
- How strong is default human approval for risky actions?
- What is the managed-hosting launch shape vs self-host launch shape?

## Phase 0: Contracts and Skeleton

- Freeze domain model and API contracts.
- Implement modular monolith boundaries in one codebase (with a separate worker process).
- Implement auth/tenancy baseline.
- Build direct model gateway abstraction.
- Implement event bus and run/job framework.

## Phase 1: Core UX and Workflow

- Multi-party chat with session context manager.
- Projects/tasks/flow with existing fundamentals.
- Staff agent assignment and temp agent lifecycle.
- Basic memory ingestion and retrieval.

## Phase 2: Integrations and Execution

- MCP connection manager and policy layer.
- Skills layering and resolver.
- CLI and browser control runtime.
- Improved model routing and budget controls.

## Phase 3: Hardening and Commercial Readiness

- Security hardening, audit expansion, retention controls.
- Full observability stack and on-call readiness.
- Self-host packaging and upgrade path stabilization.
- Multi-tenant managed operations and billing controls.

## Immediate Deep-Dive Order (Recommended)

1. Auth/tenancy/identity contracts
2. Chat session + context architecture
3. Project/task flow contracts
4. Model gateway and policy model
5. Memory pipeline details
6. Integrations (MCP, skills, CLI/browser)
