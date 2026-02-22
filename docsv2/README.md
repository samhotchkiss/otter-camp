# OtterCamp V2 Product Spec (First Pass)

This directory is the first full draft of OtterCamp V2.

## Context

- V2 removes the OpenClaw dependency.
- V2 integrates directly with LLM providers.
- V2 is a clean-room rebuild: no existing OtterCamp runtime code is carried forward.
- V2 keeps selected product concepts where they are still strong (for example, task flow and review gates).
- V2 is multi-human + multi-agent from day one.

## Documents

1. `01-architecture-and-domain.md`
2. `02-chat.md`
3. `03-projects-and-task-flow.md`
4. `04-auth-tenancy-and-identity.md`
5. `05-agents-staff-and-temps.md`
6. `06-memory.md`
7. `07-models-and-inference.md`
8. `08-deployment-and-self-hosting.md`
9. `09-mcp-integration.md`
10. `10-skills-integration.md`
11. `11-system-integration-cli-and-browser.md`
12. `12-api-events-and-realtime.md`
13. `13-security-observability-costs.md`
14. `14-open-questions-and-phasing.md`
15. `15-migration-and-backward-compat.md`
16. `16-agent-control-plane.md`
17. `17-tools-and-secrets.md`

## The 10 Areas You Requested

- Chat
- Projects
- Organizational auth and data segmentation
- Agents (staff and temps)
- Memory management
- Models
- Self-hosting and deployment modes
- MCP integration
- Skills integration
- System integration (CLI + browser control)

## What Else Was Missing (Added in This Draft)

- API and event contracts (REST + realtime + job orchestration)
- Security posture (RBAC/ABAC, secret handling, auditability)
- Observability and SRE operations
- Cost controls and quota management (token spend, model budgets)
- Data lifecycle and retention policy
- Fresh-start cutover strategy (no V1 data/schema migration)
- Build phasing and sequencing

## Drafting Principles

- Prefer concrete contracts over abstract architecture language.
- Rebuild implementation from scratch; carry forward concepts, not old runtime code.
- Keep the current strong product ideas (issue/task flow, review gates, escalation).
- Minimize irreversible decisions until APIs and runtime boundaries are validated.
- Design for local-first development and production deployment parity.
